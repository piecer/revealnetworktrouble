package diagnostic

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Model the lock boundary after cancellation but before the old waiter's
// deferred retirement acquires s.mu. Its retained count must not revive it.
func TestIPContextJoinCannotReviveCancelledRetainedWaiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	flightCtx, cancelFlight := context.WithCancel(context.Background())
	defer cancelFlight()
	ready := make(chan struct{})
	close(ready)
	f := &ipContextFlight{waiters: 1, waiterContexts: map[*byte]context.Context{new(byte): ctx}, cancel: cancelFlight, ready: ready}
	s := NewIPContextService(IPContextOptions{})
	s.flights["1.1.1.1"] = f
	s.active = 1
	_, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if !errors.Is(err, ErrIPContextBusy) || !f.abandoned || flightCtx.Err() == nil {
		t.Fatalf("cancelled generation was revived: err=%v abandoned=%v cancelled=%v", err, f.abandoned, flightCtx.Err())
	}
	if f.waiters != 1 || len(f.waiterContexts) != 1 {
		t.Fatal("new waiter retained on abandoned generation")
	}
}
