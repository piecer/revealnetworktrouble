package diagnostic

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestIPContextCancelledWaiterCannotPublishCache(t *testing.T) {
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, HTTP: func(c context.Context, u string) (int, []byte, error) {
			if strings.Contains(u, "network-info") {
				cancel()
			}
			return 404, nil, nil
		}})
		_, _ = s.Lookup(ctx, "1.1.1.1", time.Now())
		cancel()
		deadline := time.After(time.Second)
		for {
			s.mu.Lock()
			active := s.active
			cached := len(s.cache)
			changed := s.changed
			s.mu.Unlock()
			if active == 0 {
				if cached != 0 {
					t.Fatalf("cancelled-only acquisition published cache at iteration %d", i)
				}
				break
			}
			select {
			case <-changed:
			case <-deadline:
				t.Fatal("did not settle")
			}
		}
	}
}
