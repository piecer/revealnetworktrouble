package diagnostic

import (
	"context"
	"errors"
	"sync"
	"time"
)

type ipContextFlightKey struct{}
type ipContextFlight struct {
	work                 sync.WaitGroup
	cancel               context.CancelFunc
	ready                chan struct{}
	waiters              int
	waiterContexts       map[*byte]context.Context
	abandoned, published bool
	value                IPContext
	err                  error
}
type ipContextCallResult[T any] struct {
	value T
	err   error
}

// Only the generation owns primitive lifetime, not the context-select caller.
// At most one primitive per component is outstanding; timeout stops that stage.
func contextOwnedCall[T any](ctx context.Context, fn func() (T, error)) (zero T, err error) {
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	f, _ := ctx.Value(ipContextFlightKey{}).(*ipContextFlight)
	if f == nil {
		return zero, errors.New("missing work owner")
	}
	f.work.Add(1)
	done := make(chan ipContextCallResult[T], 1)
	go func() {
		defer f.work.Done()
		result := ipContextCallResult[T]{}
		func() {
			defer func() {
				if recover() != nil {
					result.err = errors.New("provider callback failed")
				}
			}()
			result.value, result.err = fn()
		}()
		done <- result
	}()
	select {
	case result := <-done:
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		return result.value, result.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}
func (s *IPContextService) Lookup(ctx context.Context, address string, started time.Time) (IPContext, error) {
	if !validContextAddress(address) {
		return IPContext{}, ErrIPContextInvalid
	}
	if err := ctx.Err(); err != nil {
		return IPContext{}, err
	}
	now := s.now()
	if now.Year() < 1 || now.Year() > 9999 {
		return IPContext{}, ErrIPContextInvalid
	}
	s.mu.Lock()
	if s.draining {
		s.mu.Unlock()
		return IPContext{}, ErrIPContextBusy
	}
	if cached, ok := s.cache[address]; ok {
		if now.Before(cached.expires) {
			s.sequence++
			cached.used = s.sequence
			s.cache[address] = cached
			value := cloneIPContext(cached.value)
			value.Source = "cache"
			s.mu.Unlock()
			return value, nil
		}
		delete(s.cache, address)
	}
	f := s.flights[address]
	if f != nil && !f.published && !contextFlightHasLiveWaiter(f) {
		f.abandoned = true
		f.cancel()
	}
	if f != nil && (f.abandoned || f.waiters >= 4) {
		s.mu.Unlock()
		return IPContext{}, ErrIPContextBusy
	}
	if f == nil {
		if s.active >= 2 {
			s.mu.Unlock()
			return IPContext{}, ErrIPContextBusy
		}
		if !time.Now().Before(started.Add(6 * time.Second)) {
			s.mu.Unlock()
			return IPContext{}, context.DeadlineExceeded
		}
		flightCtx, cancel := context.WithDeadline(context.Background(), started.Add(6*time.Second))
		f = &ipContextFlight{cancel: cancel, ready: make(chan struct{}), waiters: 1, waiterContexts: map[*byte]context.Context{}}
		flightCtx = context.WithValue(flightCtx, ipContextFlightKey{}, f)
		s.flights[address] = f
		s.active++
		go s.acquire(flightCtx, address, f)
	} else {
		f.waiters++
	}
	waiter := new(byte)
	f.waiterContexts[waiter] = ctx
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		f.waiters--
		delete(f.waiterContexts, waiter)
		if f.waiters == 0 && !f.published {
			f.abandoned = true
			f.cancel()
		}
	}()
	select {
	case <-ctx.Done():
		return IPContext{}, ctx.Err()
	case <-f.ready:
		if ctx.Err() != nil {
			return IPContext{}, ctx.Err()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if f.abandoned {
			return IPContext{}, context.Canceled
		}
		if f.err != nil {
			return IPContext{}, f.err
		}
		return cloneIPContext(f.value), nil
	}
}
func (s *IPContextService) acquire(ctx context.Context, address string, f *ipContextFlight) {
	f.work.Add(3)
	dns := make(chan IPContextReverse, 1)
	reg := make(chan IPContextRegistration, 1)
	bgp := make(chan IPContextRouting, 1)
	go func() { defer f.work.Done(); dns <- s.reverse(ctx, address) }()
	go func() { defer f.work.Done(); reg <- s.registration(ctx, address) }()
	go func() { defer f.work.Done(); bgp <- s.routing(ctx, address) }()
	value := IPContext{SchemaVersion: 1, Address: address, Source: "upstream", ReverseDNS: <-dns, Registration: <-reg, Routing: <-bgp}
	now := s.now()
	for _, stamp := range []string{value.ReverseDNS.FetchedAt, value.Registration.FetchedAt, value.Routing.FetchedAt} {
		if fetched, ok := contextStamp(stamp); ok && fetched.After(now) {
			now = fetched
		}
	}
	for _, origin := range value.Routing.Origins {
		if checked, ok := contextStamp(origin.RPKI.CheckedAt); ok && checked.After(now) {
			now = checked
		}
	}
	value.FetchedAt = ipContextTime(now)
	value.ExpiresAt = ipContextTime(now.Add(contextSnapshotTTL(value)))
	_, validationErr := MarshalIPContext(value)
	s.mu.Lock()
	live := contextFlightHasLiveWaiter(f)
	if !live {
		f.abandoned = true
		f.cancel()
	}
	if !f.abandoned && live && !s.draining {
		f.value = value
		f.published = true
		f.err = validationErr
		if validationErr == nil {
			s.cacheSnapshot(value)
		}
		if s.flights[address] == f {
			delete(s.flights, address)
		}
	}
	close(f.ready)
	s.mu.Unlock()
	// This join includes late resolver/dial/HTTP/body work. Publication is not exit.
	f.work.Wait()
	f.cancel()
	s.mu.Lock()
	if s.flights[address] == f {
		delete(s.flights, address)
	}
	s.active--
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// Caller holds s.mu: retained waiter count is not proof of live authority.
func contextFlightHasLiveWaiter(f *ipContextFlight) bool {
	for _, waiter := range f.waiterContexts {
		if waiter.Err() == nil {
			return true
		}
	}
	return false
}
func cloneIPContext(v IPContext) IPContext {
	v.ReverseDNS.Names = append(make([]IPContextName, 0, len(v.ReverseDNS.Names)), v.ReverseDNS.Names...)
	v.Routing.Origins = append(make([]IPContextOrigin, 0, len(v.Routing.Origins)), v.Routing.Origins...)
	return v
}
func (s *IPContextService) BeginDrain() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draining = true
	if s.transport != nil {
		s.transport.Close()
	}
	for _, f := range s.flights {
		f.abandoned = true
		f.cancel()
	}
}
func (s *IPContextService) Close(ctx context.Context) error {
	s.BeginDrain()
	for {
		s.mu.Lock()
		active := s.active
		changed := s.changed
		s.mu.Unlock()
		if active == 0 {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
