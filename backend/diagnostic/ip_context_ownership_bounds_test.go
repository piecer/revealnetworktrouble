package diagnostic

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func contextWaitIdle(t *testing.T, s *IPContextService) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		s.mu.Lock()
		active, changed := s.active, s.changed
		s.mu.Unlock()
		if active == 0 {
			return
		}
		select {
		case <-changed:
		case <-timer.C:
			t.Fatal("owned work did not settle")
		}
	}
}
func TestIPContextCacheLRUEvictionAndExpiry(t *testing.T) {
	var advance atomic.Int64
	var calls atomic.Int32
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewIPContextService(IPContextOptions{Now: func() time.Time { return base.Add(time.Duration(advance.Load())) }, Resolver: contextTestResolver{}, HTTP: func(context.Context, string) (int, []byte, error) { calls.Add(1); return 404, nil, nil }})
	lookup := func(a string) IPContext {
		v, e := s.Lookup(context.Background(), a, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		contextWaitIdle(t, s)
		return v
	}
	for i := 1; i <= 256; i++ {
		lookup(fmt.Sprintf("1.1.%d.%d", i/256, i%256))
	}
	if len(s.cache) != 256 {
		t.Fatalf("entries=%d", len(s.cache))
	}
	if lookup("1.1.0.1").Source != "cache" {
		t.Fatal("missing hit")
	}
	lookup("1.1.1.1")
	if len(s.cache) != 256 || lookup("1.1.0.1").Source != "cache" || lookup("1.1.0.2").Source != "upstream" {
		t.Fatal("not deterministic LRU")
	}
	before := calls.Load()
	advance.Store(int64(31 * time.Second))
	if lookup("1.1.0.1").Source != "upstream" || calls.Load() != before+2 {
		t.Fatal("expired cache served or duplicate fetch")
	}
}
func TestIPContextSixPrimitivesAndNoColdQueue(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	entered := make(chan struct{}, 8)
	var active, maximum atomic.Int32
	block := func() {
		n := active.Add(1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
	}
	s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{reverse: func(context.Context, string) ([]string, error) { block(); return nil, nil }}, HTTP: func(context.Context, string) (int, []byte, error) { block(); return 404, nil, nil }})
	done := make(chan error, 2)
	for _, a := range []string{"1.1.1.1", "8.8.8.8"} {
		go func(a string) {
			_, err := s.Lookup(context.Background(), a, time.Now().Add(-5700*time.Millisecond))
			done <- err
		}(a)
	}
	for i := 0; i < 6; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("primitive did not start")
		}
	}
	if _, e := s.Lookup(context.Background(), "9.9.9.9", time.Now()); e != ErrIPContextBusy {
		t.Fatalf("third %v", e)
	}
	for i := 0; i < 2; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if maximum.Load() != 6 || active.Load() != 6 {
		t.Fatalf("max=%d active=%d", maximum.Load(), active.Load())
	}
	once.Do(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if active.Load() != 0 {
		t.Fatal("primitive leak")
	}
}
func TestIPContextOldGenerationCannotClearOrMutateReplacement(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var reverse atomic.Int32
	var advance atomic.Int64
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewIPContextService(IPContextOptions{Now: func() time.Time { return base.Add(time.Duration(advance.Load())) }, Resolver: contextTestResolver{reverse: func(context.Context, string) ([]string, error) {
		if reverse.Add(1) == 1 {
			<-release
			return []string{"old.ex"}, nil
		}
		return []string{"new.ex"}, nil
	}, forward: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
	}}, HTTP: func(context.Context, string) (int, []byte, error) { return 404, nil, nil }})
	first, e := s.Lookup(context.Background(), "1.1.1.1", time.Now().Add(-5800*time.Millisecond))
	if e != nil || first.ReverseDNS.Status != "timeout" {
		t.Fatalf("first %+v %v", first, e)
	}
	advance.Store(int64(31 * time.Second))
	second, e := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if e != nil || second.ReverseDNS.Names[0].Name != "new.ex" {
		t.Fatalf("second %+v %v", second, e)
	}
	once.Do(func() { close(release) })
	contextWaitIdle(t, s)
	third, e := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if e != nil || third.Source != "cache" || third.ReverseDNS.Names[0].Name != "new.ex" || first.ReverseDNS.Status != "timeout" {
		t.Fatalf("late overwrite %+v %v", third, e)
	}
}
func TestIPContextRPKIGroupDeadlineAndPanic(t *testing.T) {
	for _, fault := range []string{"deadline", "panic"} {
		t.Run(fault, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var calls atomic.Int32
			s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, HTTP: func(c context.Context, u string) (int, []byte, error) {
				if strings.Contains(u, "network-info") {
					return 200, []byte(`{"status":"ok","data":{"prefix":"1.1.1.0/24","asns":[1,2,3,4]}}`), nil
				}
				if strings.Contains(u, "rpki-validation") {
					calls.Add(1)
					if fault == "panic" {
						panic("PRIVATE_RPKI_CANARY")
					}
					<-release
					return 200, nil, nil
				}
				return 404, nil, nil
			}})
			v, e := s.Lookup(context.Background(), "1.1.1.1", time.Now().Add(-5800*time.Millisecond))
			if e != nil || v.Routing.Status != "ok" || len(v.Routing.Origins) != 4 {
				t.Fatalf("routing lost %+v %v", v, e)
			}
			want := "timeout"
			wantCalls := int32(1)
			if fault == "panic" {
				want = "unavailable"
				wantCalls = 4
			}
			for _, o := range v.Routing.Origins {
				if o.RPKI.Status != want || o.RPKI.Validity != "" {
					t.Fatal(o)
				}
			}
			if calls.Load() != wantCalls {
				t.Fatalf("calls=%d want %d", calls.Load(), wantCalls)
			}
			once.Do(func() { close(release) })
			contextWaitIdle(t, s)
		})
	}
}
