package diagnostic

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGeoDetailsIndependentDecoderBoundariesAndFailures(t *testing.T) {
	for _, extra := range []string{`"continent":7`, `"continent_code":[]`, `"region_code":{}`, `"postal":false`, `"timezone":"wrong"`, `"timezone":{"id":7}`, `"connection":{"asn":1,"org":"Owner","domain":3}`, `"continent":"` + strings.Repeat("x", 257) + `"`} {
		payload := `{"success":true,"latitude":0,"longitude":0,"city":"valid",` + extra + `}`
		metadata, err := decodeGeoIPResponse(strings.NewReader(payload))
		if err != nil || metadata.Geolocation == nil || metadata.Geolocation.City != "valid" || metadata.GeoDetails != (GeoDetailSnapshot{}) {
			t.Fatalf("malformed extra spoiled legacy or invented provenance: %s %+v %v", extra, metadata, err)
		}
	}
	// Exactly six 256-byte strings; no legacy-only field is made excessive.
	for _, n := range []int{256, 257} {
		value := strings.Repeat("😀", 64)
		if n == 257 {
			value += "x"
		}
		payload := map[string]any{"success": true, "latitude": 0, "longitude": 0, "continent": value}
		data, _ := json.Marshal(payload)
		metadata, err := decodeGeoIPResponse(strings.NewReader(string(data)))
		if err != nil || metadata.Geolocation == nil || (metadata.GeoDetails.Provider != "") != (n == 256) {
			t.Fatalf("UTF8 field boundary %d: %+v %v", n, metadata, err)
		}
	}
	for _, extra := range []string{"", "x"} {
		value := strings.Repeat("x", 256)
		payload := map[string]any{"success": true, "latitude": 0, "longitude": 0, "continent": value, "continent_code": value, "region_code": value, "postal": value, "timezone": map[string]any{"id": value}, "connection": map[string]any{"domain": value}, "city": extra}
		data, _ := json.Marshal(payload)
		metadata, err := decodeGeoIPResponse(strings.NewReader(string(data)))
		if err != nil || metadata.Geolocation == nil || (metadata.GeoDetails.Provider != "") != (extra == "") {
			t.Fatalf("combined byte boundary extra=%q: %+v %v", extra, metadata, err)
		}
	}
	for _, payload := range []string{`{"success":true}`, `{"success":false,"city":"valid"}`, `{"success":true,"city":"valid","city":"duplicate"}`, `{"success":true,"city":"valid"} {}`, `{"success":true,"city":"\ud800"}`, `{"success":true,"city":"valid"`, `{"city":"valid"}`} {
		metadata, err := decodeGeoIPResponse(strings.NewReader(payload))
		if err == nil || metadata.GeoDetails != (GeoDetailSnapshot{}) || validIPMetadata(metadata) {
			t.Fatalf("failure stamped metadata: %s %+v %v", payload, metadata, err)
		}
	}
}

func TestGeoDetailsTransportFailureNeverStampsSnapshot(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"success":true,"city":"must not survive"}`))
			}))
			defer provider.Close()
			lookup := NewIPWhoIsLookup(provider.Client(), provider.URL)
			metadata, err := lookup.Lookup(context.Background(), net.ParseIP("8.8.8.8"))
			if err == nil || metadata.GeoDetails != (GeoDetailSnapshot{}) {
				t.Fatalf("transport failure stamped: %+v %v", metadata, err)
			}
		})
	}
}

func TestGeoDetailsRawEligibilityFirstSnapshotAndCanonicalAddress(t *testing.T) {
	first := GeoDetailSnapshot{Provider: "ipwho.is", Source: GeoIPSourceUpstream, City: "first"}
	later := GeoDetailSnapshot{Provider: "ipwho.is", Source: GeoIPSourceCache, City: "later", ISP: "must not merge"}
	node := func(address, status string, hop int, s GeoDetailSnapshot) TopologyNode {
		return TopologyNode{Address: address, Status: status, Hop: hop, GeoDetails: s}
	}
	topology := &Topology{Reached: true, Nodes: []TopologyNode{
		node("8.8.8.8", "healthy", 0, later), node("8.8.8.8", "healthy", 1, GeoDetailSnapshot{}),
		node("::ffff:8.8.8.8", "healthy", 2, first), node("8.8.8.8", "healthy", 3, later),
		node("1.1.1.1", "degraded", 4, first), node("2606:4700:4700:0:0:0:0:1111", "healthy", 5, first),
		node("9.9.9.9", "failure", 6, later), node("8.8.4.4", "unknown", 7, later), node("192.168.1.1", "healthy", 8, later),
		node("name.example", "healthy", 9, later), node("fe80::1%eth0", "healthy", 10, later), node("203.0.113.1", "healthy", 11, later),
	}}
	// topologyStatus is based on reached and degraded state; use the same typed predicate.
	attempt := TraceAttempt{Attempt: 3, Status: topologyStatus(*topology), Topology: topology}
	failed := TraceAttempt{Attempt: 1, Status: StatusUnreachable, ErrorCode: "timeout", Topology: &Topology{Reached: true, Nodes: []TopologyNode{node("9.9.9.9", "healthy", 1, later)}}}
	report := Report{Results: []Result{{Kind: KindTraceroute, Details: map[string]any{"attempts": []TraceAttempt{failed, attempt}}}}}
	before := append([]TopologyNode(nil), topology.Nodes...)
	side := BuildGeoDetails(report)
	if side == nil || side.Total != 3 || side.Omitted != 0 || len(side.Entries) != 3 {
		t.Fatalf("bad eligibility: %+v", side)
	}
	addresses := []string{side.Entries[0].Address, side.Entries[1].Address, side.Entries[2].Address}
	if !reflect.DeepEqual(addresses, []string{"1.1.1.1", "2606:4700:4700::1111", "8.8.8.8"}) {
		t.Fatalf("noncanonical order: %v", addresses)
	}
	if side.Entries[2].City != "first" || side.Entries[2].ISP != "" || side.Entries[2].Source != GeoIPSourceUpstream {
		t.Fatal("later snapshot overwritten or merged")
	}
	side.Entries[2].City = "output mutation"
	if !reflect.DeepEqual(before, topology.Nodes) {
		t.Fatal("raw hidden snapshots mutated")
	}
	// Malformed provenance/times must not reserve a key ahead of a later valid one.
	for _, mutate := range []func(*GeoDetailSnapshot){func(s *GeoDetailSnapshot) { s.Provider = "other" }, func(s *GeoDetailSnapshot) { s.Source = "other" }, func(s *GeoDetailSnapshot) { s.City = strings.Repeat("x", 257) }, func(s *GeoDetailSnapshot) { s.FetchedAt = time.Now() }, func(s *GeoDetailSnapshot) { s.FetchedAt = time.Unix(2, 0); s.ExpiresAt = time.Unix(1, 0) }} {
		bad := first
		mutate(&bad)
		topology.Nodes = []TopologyNode{node("8.8.8.8", "healthy", 1, bad), node("8.8.8.8", "healthy", 2, later)}
		// Remove degraded earlier nodes so the consistent attempt status changes.
		report.Results[0].Details["attempts"] = []TraceAttempt{{Attempt: 1, Status: topologyStatus(*topology), Topology: topology}}
		got := BuildGeoDetails(report)
		if got.Total != 1 || got.Entries[0].City != "later" {
			t.Fatalf("invalid earlier snapshot won: %+v", got)
		}
	}
	empty := BuildGeoDetails(Report{})
	encoded, _ := json.Marshal(empty)
	if string(encoded) != `{"schema_version":1,"total":0,"omitted":0,"entries":[]}` {
		t.Fatalf("empty noncanonical: %s", encoded)
	}
}
