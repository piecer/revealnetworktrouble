package diagnostic

import (
	"context"
	"testing"
	"time"
)

func TestIPContextInjectedAcquisitionClock(t *testing.T) {
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 6000000, time.UTC)
	s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, HTTP: func(context.Context, string) (int, []byte, error) { return 404, nil, nil }, Now: func() time.Time { return fixed }})
	v, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.FetchedAt != "2026-01-02T03:04:05.006Z" || v.ReverseDNS.FetchedAt != v.FetchedAt || v.Registration.FetchedAt != v.FetchedAt || v.Routing.FetchedAt != v.FetchedAt {
		t.Fatalf("clock %+v", v)
	}
	cached, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if err != nil || cached.Source != "cache" || cached.FetchedAt != v.FetchedAt {
		t.Fatalf("cache clock %+v %v", cached, err)
	}
}
