package diagnostic

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIPContextAbandonedCallbacksKeepLeases(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	entered := make(chan struct{}, 4)
	var calls atomic.Int32
	s := contextTestService(contextTestResolver{reverse: func(context.Context, string) ([]string, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return []string{"late.ex"}, nil
	}})
	for _, address := range []string{"1.1.1.1", "8.8.8.8"} {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func(a string) { _, e := s.Lookup(ctx, a, time.Now()); done <- e }(address)
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("callback not entered")
		}
		cancel()
		select {
		case e := <-done:
			if !errors.Is(e, context.Canceled) {
				t.Errorf("cancel=%v", e)
			}
		case <-time.After(100 * time.Millisecond):
			once.Do(func() { close(release) })
			<-done
			t.Fatal("caller retained by noncooperative primitive")
		}
	}
	_, err := s.Lookup(context.Background(), "9.9.9.9", time.Now())
	if !errors.Is(err, ErrIPContextBusy) || calls.Load() != 2 {
		t.Fatalf("third admission err=%v calls=%d", err, calls.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if s.Close(ctx) == nil {
		t.Fatal("claimed stop while callbacks alive")
	}
	once.Do(func() { close(release) })
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := s.Close(ctx2); err != nil {
		t.Fatal(err)
	}
}
func TestIPContextCallerDeadlineDoesNotOwnSharedFlight(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	entered := make(chan struct{}, 4)
	var calls atomic.Int32
	s := contextTestService(contextTestResolver{reverse: func(context.Context, string) ([]string, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return nil, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, e := s.Lookup(ctx, "1.1.1.1", time.Now()); first <- e }()
	<-entered
	second := make(chan IPContext, 1)
	go func() { v, _ := s.Lookup(context.Background(), "1.1.1.1", time.Now()); second <- v }()
	// A second upstream entry is immediate evidence of missing singleflight.
	select {
	case <-entered:
		once.Do(func() { close(release) })
		cancel()
		<-first
		<-second
		t.Fatal("same-IP join started new primitive")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	select {
	case e := <-first:
		if !errors.Is(e, context.Canceled) {
			t.Error(e)
		}
	case <-time.After(100 * time.Millisecond):
		once.Do(func() { close(release) })
		<-first
		<-second
		t.Fatal("first waiter did not cancel")
	}
	once.Do(func() { close(release) })
	select {
	case v := <-second:
		if v.ReverseDNS.Status != "not_found" || calls.Load() != 1 {
			t.Fatalf("snapshot=%+v calls=%d", v, calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("join cancelled")
	}
}
func TestIPContextProviderPanicIsComponentFailure(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Error("provider panic escaped")
		}
	}()
	s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{reverse: func(context.Context, string) ([]string, error) { panic("DNS_PRIVATE_CANARY") }}, HTTP: func(context.Context, string) (int, []byte, error) { panic("HTTP_PRIVATE_CANARY") }})
	got, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if err != nil || got.ReverseDNS.Status != "unavailable" || got.Registration.Status != "unavailable" || got.Routing.Status != "unavailable" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}
