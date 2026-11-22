package diagnostic

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The off switch must short-circuit before any provider request, and the
// disabled state has to be observable as a typed sentinel so analysis can
// report a configuration limitation instead of guessing from missing details.

func TestDisabledGeoIPLookupMakesNoProviderRequests(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	lookup := NewIPWhoIsLookupWithConfig(http.DefaultClient, server.URL+"/", nil, GeoIPCacheConfig{})
	lookup.SetDisabled(true)
	metadata, err := lookup.Lookup(context.Background(), net.ParseIP("203.0.113.7"))
	if !errors.Is(err, ErrGeoIPDisabled) {
		t.Fatalf("disabled lookup must return the disabled sentinel, got %v", err)
	}
	if metadata.Source != GeoIPSourceDisabled {
		t.Fatalf("disabled metadata must report its source, got %q", metadata.Source)
	}
	if requests != 0 {
		t.Fatalf("disabled lookup must not contact the provider, got %d requests", requests)
	}
}

// Cached metadata is not evidence of the current configuration state: a
// disabled switch reports disabled even when the cache still holds a hit.
func TestDisabledGeoIPLookupDoesNotServeCache(t *testing.T) {
	lookup := NewIPWhoIsLookupWithConfig(http.DefaultClient, "https://ipwho.is/", nil, GeoIPCacheConfig{})
	lookup.SetDisabled(true)
	lookup.mu.Lock()
	lookup.storeCacheLocked("203.0.113.7", IPMetadata{
		Geolocation: &GeoLocation{Country: "KR"},
		Source:      GeoIPSourceCache,
		FetchedAt:   time.Now(),
	}, time.Now())
	lookup.mu.Unlock()
	_, err := lookup.Lookup(context.Background(), net.ParseIP("203.0.113.7"))
	if !errors.Is(err, ErrGeoIPDisabled) {
		t.Fatalf("cache must not serve metadata while disabled, got %v", err)
	}
}
