package diagnostic

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIPContextCacheSnapshots(t *testing.T) {
	for _, stable := range []bool{true, false} {
		t.Run(map[bool]string{true: "stable", false: "transient"}[stable], func(t *testing.T) {
			var calls atomic.Int32
			s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, HTTP: func(c context.Context, u string) (int, []byte, error) {
				calls.Add(1)
				if strings.Contains(u, "network-info") && stable {
					return 200, []byte(`{"status":"ok","data":{"prefix":"","asns":[]}}`), nil
				}
				return 404, nil, nil
			}})
			first, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			second, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 || second.Source != "cache" || first.Source != "upstream" || first.FetchedAt != second.FetchedAt || first.ExpiresAt != second.ExpiresAt {
				t.Fatalf("cache first=%+v second=%+v calls=%d", first, second, calls.Load())
			}
			fetched, _ := time.Parse("2006-01-02T15:04:05.000Z", first.FetchedAt)
			expires, _ := time.Parse("2006-01-02T15:04:05.000Z", first.ExpiresAt)
			ttl := 30 * time.Second
			if stable {
				ttl = 5 * time.Minute
			}
			if expires.Sub(fetched) != ttl {
				t.Fatalf("TTL %v want %v", expires.Sub(fetched), ttl)
			}
			second.ReverseDNS.Names = append(second.ReverseDNS.Names, IPContextName{Name: "poison"})
			third, _ := s.Lookup(context.Background(), "1.1.1.1", time.Now())
			if len(third.ReverseDNS.Names) != 0 || third.ReverseDNS.Names == nil || third.Routing.Origins == nil {
				t.Fatal("cache alias or null arrays")
			}
		})
	}
}
