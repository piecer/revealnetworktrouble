package diagnostic

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIPWhoIsLookupPublicPolicyBlocksPrivateProviderSink(t *testing.T) {
	lookup := NewIPWhoIsLookupWithPolicy(nil, "http://127.0.0.1/", NewNetworkPolicy(nil, nil))
	_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if !errors.Is(err, ErrNetworkPolicyBlocked) {
		t.Fatalf("private GeoIP provider was not blocked: %v", err)
	}
}

func TestIPWhoIsLookupPublicPolicyRechecksRedirectAndBypassesProxy(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Host == "first.test" {
			http.Redirect(w, r, "http://second.test/8.8.8.8", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	resolver := &resolverSequence{addresses: [][]net.IPAddr{
		ipAnswers("93.184.216.34"),
		ipAnswers("93.184.216.35"),
		ipAnswers("127.0.0.1"),
	}}
	dialer := &mappingDialer{target: server.Listener.Addr().String()}
	proxyCalled := false
	client := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
		proxyCalled = true
		return url.Parse("http://127.0.0.1:9")
	}}}
	lookup := NewIPWhoIsLookupWithPolicy(client, "http://first.test/", NewNetworkPolicy(resolver, dialer))
	_, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if !errors.Is(err, ErrNetworkPolicyBlocked) || proxyCalled || requests != 1 || len(dialer.calls) != 1 || resolver.calls != 3 {
		t.Fatalf("err=%v proxy=%v requests=%d dials=%+v resolver_calls=%d", err, proxyCalled, requests, dialer.calls, resolver.calls)
	}
}

func TestIPWhoIsLookupMapsAndCachesResponse(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/8.8.8.8" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true,"city":"Mountain View","region":"California","country":"United States","country_code":"US","latitude":37.4,"longitude":-122.1,"connection":{"asn":15169,"org":"Google LLC"}}`))}, nil
	})}

	lookup := NewIPWhoIsLookup(client, "https://geo.example.test/")
	for range 2 {
		metadata, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
		if err != nil {
			t.Fatal(err)
		}
		if metadata.Geolocation.City != "Mountain View" || metadata.ASN.Number != 15169 || metadata.ASN.Organization != "Google LLC" {
			t.Fatalf("metadata = %+v", metadata)
		}
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestIPWhoIsLookupCacheIsBoundedLRUAndExpires(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true}`))}, nil
	})}
	lookup := NewIPWhoIsLookupWithConfig(client, "https://geo.example.test/", nil, GeoIPCacheConfig{
		MaxEntries: 2,
		TTL:        time.Hour,
		Now:        func() time.Time { return now },
	})
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "8.8.8.8", "9.9.9.9"} {
		if _, err := lookup.Lookup(context.Background(), net.ParseIP(address)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 || len(lookup.cache) != 2 {
		t.Fatalf("calls=%d cache=%d", calls, len(lookup.cache))
	}
	if _, err := lookup.Lookup(context.Background(), net.ParseIP("1.1.1.1")); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(lookup.cache) != 2 {
		t.Fatalf("LRU eviction failed: calls=%d cache=%d", calls, len(lookup.cache))
	}
	now = now.Add(2 * time.Hour)
	if _, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8")); err != nil {
		t.Fatal(err)
	}
	if calls != 5 || len(lookup.cache) > 2 {
		t.Fatalf("TTL refresh failed: calls=%d cache=%d", calls, len(lookup.cache))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

type recordingGeoIP struct {
	mu    sync.Mutex
	calls map[string]int
}

func (r *recordingGeoIP) Lookup(_ context.Context, ip net.IP) (IPMetadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[ip.String()]++
	return IPMetadata{
		Geolocation: &GeoLocation{City: "Seoul", Country: "South Korea", CountryCode: "KR", Latitude: 37.56, Longitude: 126.97},
		ASN:         &ASNInfo{Number: 15169, Organization: "Example Network"},
	}, nil
}

func TestEnrichTopologiesOnlyLooksUpUniquePublicIPs(t *testing.T) {
	lookup := &recordingGeoIP{calls: make(map[string]int)}
	attempts := []TraceAttempt{
		{Topology: &Topology{Nodes: []TopologyNode{{Address: "10.0.0.1"}, {Address: "8.8.8.8"}, {Address: "8.8.8.8"}}}},
		{Topology: &Topology{Nodes: []TopologyNode{{Address: "192.0.2.1"}, {Address: "1.1.1.1"}}}},
	}
	enrichTopologies(context.Background(), attempts, lookup)

	if len(lookup.calls) != 2 || lookup.calls["8.8.8.8"] != 1 || lookup.calls["1.1.1.1"] != 1 {
		t.Fatalf("calls = %#v", lookup.calls)
	}
	if attempts[0].Topology.Nodes[0].PublicIP || attempts[1].Topology.Nodes[0].PublicIP {
		t.Fatal("private or documentation address marked public")
	}
	node := attempts[0].Topology.Nodes[1]
	if !node.PublicIP || node.Geolocation == nil || node.Geolocation.City != "Seoul" || node.ASN == nil || node.ASN.Number != 15169 {
		t.Fatalf("node = %+v", node)
	}
}

func TestPublicIPClassification(t *testing.T) {
	for _, address := range []string{"10.0.0.1", "100.64.0.1", "192.0.2.1", "203.0.113.1", "2001:db8::1", "64:ff9b::a00:1", "100::1", "::1"} {
		if isPublicIP(net.ParseIP(address)) {
			t.Errorf("%s classified as public", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !isPublicIP(net.ParseIP(address)) {
			t.Errorf("%s classified as non-public", address)
		}
	}
}
