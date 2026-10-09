package diagnostic

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGeoDetailsCachePreservesOriginalTimesAndValueSnapshots(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"city":"Original","latitude":0,"longitude":0,"connection":{"asn":1,"org":"Owner","isp":"ISP"}}`))
	}))
	defer provider.Close()
	fetched := time.Date(2026, 10, 9, 0, 0, 0, 123000000, time.UTC)
	now := fetched
	lookup := NewIPWhoIsLookupWithConfig(provider.Client(), provider.URL, nil, GeoIPCacheConfig{Now: func() time.Time { return now }, TTL: time.Hour})
	first, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	original := first.GeoDetails
	first.GeoDetails.City = "MUTATED"
	first.Geolocation.City = "MUTATED"
	first.ASN.Organization = "MUTATED"
	now = fetched.Add(time.Minute)
	cached, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	expected := original
	expected.Source = GeoIPSourceCache
	if cached.GeoDetails != expected || cached.Source != GeoIPSourceCache || !cached.FetchedAt.Equal(fetched) || !cached.ExpiresAt.Equal(fetched.Add(time.Hour)) || cached.Geolocation.City != "Original" || cached.ASN.Organization != "Owner" {
		t.Fatalf("cache snapshot alias/restamp/wrong source: %+v", cached)
	}
	attempts := []TraceAttempt{{Attempt: 1, Status: StatusHealthy, Topology: &Topology{Reached: true, Nodes: []TopologyNode{{Hop: 1, Address: "8.8.8.8", Status: "healthy"}, {Hop: 2, Address: "8.8.8.8", Status: "healthy"}}}}}
	coverage := enrichTopologiesWithCoverage(context.Background(), attempts, lookup)
	if calls.Load() != 1 || coverage.CacheHits != 1 || coverage.UpstreamFetches != 0 {
		t.Fatalf("duplicate upstream query: %d %+v", calls.Load(), coverage)
	}
	a, b := &attempts[0].Topology.Nodes[0], &attempts[0].Topology.Nodes[1]
	a.GeoDetails.City = "NODE_MUTATION"
	if b.GeoDetails != expected || cached.GeoDetails != expected {
		t.Fatal("hidden snapshot alias between nodes/cache")
	}
	clone := cloneIPMetadata(cached)
	clone.GeoDetails.ISP = "CLONE_MUTATION"
	if cached.GeoDetails != expected {
		t.Fatal("hidden snapshot clone aliases source")
	}
	again, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	if err != nil || again.GeoDetails != expected || calls.Load() != 1 {
		t.Fatal("node/clone mutation reached cache")
	}
}

func TestGeoDetailsTextOnlyKeepsLegacyErrorUncachedThroughEnrichment(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/8.8.8.8" {
			t.Errorf("unexpected lookup %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"success":true,"city":"서울","continent":"Asia","timezone":{"id":"Asia/Seoul"}}`))
	}))
	defer provider.Close()
	now := time.Date(2026, 10, 9, 1, 2, 3, 456789000, time.UTC)
	lookup := NewIPWhoIsLookupWithConfig(provider.Client(), provider.URL, nil, GeoIPCacheConfig{Now: func() time.Time { return now }, TTL: time.Hour})
	metadata, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
	kind, _ := normalizedGeoIPFailure(err)
	if err == nil || kind != GeoIPErrorMalformed || validIPMetadata(metadata) || metadata.Geolocation != nil || metadata.ASN != nil {
		t.Fatalf("legacy text-only success invented: %+v %v", metadata, err)
	}
	if metadata.GeoDetails.City != "서울" || metadata.GeoDetails.Source != GeoIPSourceUpstream || !metadata.GeoDetails.FetchedAt.Equal(now) || !metadata.GeoDetails.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("usable text/provenance lost alongside legacy error: %+v", metadata.GeoDetails)
	}
	if metadata.Source != "" || !metadata.FetchedAt.IsZero() {
		t.Fatalf("legacy error metadata changed: %+v", metadata)
	}
	attempts := []TraceAttempt{{Attempt: 1, Status: StatusHealthy, Topology: &Topology{Reached: true, Nodes: []TopologyNode{{ID: "hop-1", Hop: 1, Address: "8.8.8.8", Status: "healthy"}, {ID: "hop-2", Hop: 2, Address: "8.8.8.8", Status: "healthy"}}}}}
	coverage := enrichTopologiesWithCoverage(context.Background(), attempts, lookup)
	if calls.Load() != 2 || coverage.UpstreamFetches != 0 || coverage.CacheHits != 0 || enrichmentFailureCount(coverage.Failures) != 1 {
		t.Fatalf("legacy accounting/cache/dedupe changed: calls=%d coverage=%+v", calls.Load(), coverage)
	}
	node := attempts[0].Topology.Nodes[0]
	details := reflect.ValueOf(node).FieldByName("GeoDetails")
	if !details.IsValid() || details.Interface().(GeoDetailSnapshot) != metadata.GeoDetails {
		t.Fatalf("text-only supplemental error not propagated to raw observation: %+v", node)
	}
}

func TestGeoDetailsDecoderRetainsIndependentProviderText(t *testing.T) {
	metadata, err := decodeGeoIPResponse(strings.NewReader(`{"success":true,"latitude":0,"longitude":0,"city":"서울 <>&\"\\ 😀","region":"Seoul","country":"South Korea","country_code":"KR","continent":"Asia","continent_code":"AS","region_code":"11","postal":"04527","timezone":{"id":"Asia/Seoul"},"connection":{"asn":15169,"org":"Owner","isp":"Distinct ISP","domain":"example.net"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Geolocation == nil || metadata.Geolocation.Latitude != 0 || metadata.Geolocation.Longitude != 0 || metadata.ASN.Organization != "Owner" {
		t.Fatalf("legacy changed: %+v", metadata)
	}
	details := reflect.ValueOf(metadata).FieldByName("GeoDetails")
	if !details.IsValid() {
		t.Fatal("validated supplemental provider text was discarded")
	}
	for name, want := range map[string]string{"City": "서울 <>&\"\\ 😀", "Region": "Seoul", "Country": "South Korea", "CountryCode": "KR", "Continent": "Asia", "ContinentCode": "AS", "RegionCode": "11", "Postal": "04527", "Timezone": "Asia/Seoul", "ISP": "Distinct ISP", "NetworkDomain": "example.net", "Provider": "ipwho.is"} {
		field := details.FieldByName(name)
		if !field.IsValid() || field.String() != want {
			t.Fatalf("%s = %v; want %q", name, field, want)
		}
	}
}
