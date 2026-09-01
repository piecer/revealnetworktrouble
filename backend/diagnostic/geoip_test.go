package diagnostic

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
)

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
	for _, address := range []string{"10.0.0.1", "100.64.0.1", "192.0.2.1", "203.0.113.1", "2001:db8::1", "::1"} {
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
