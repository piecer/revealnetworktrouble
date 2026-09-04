package diagnostic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func compactTestAttempt(number int, nodes ...TopologyNode) TraceAttempt {
	links := make([]TopologyLink, 0, len(nodes)-1)
	for i := 1; i < len(nodes); i++ {
		links = append(links, TopologyLink{From: nodes[i-1].ID, To: nodes[i].ID, Status: nodes[i].Status})
	}
	topology := Topology{Reached: true, Nodes: nodes, Links: links}
	return TraceAttempt{Attempt: number, Status: topologyStatus(topology), Topology: &topology}
}

func compactTestReport(attemptValues ...any) Report {
	results := make([]Result, len(attemptValues))
	for i, attempts := range attemptValues {
		results[i] = Result{Kind: KindTraceroute, Status: StatusHealthy, Details: map[string]any{"attempts": attempts}}
	}
	return Report{Results: results}
}

func TestCloneCompactTopologyPreservesCanonicalEmptyGraphSlices(t *testing.T) {
	source := &CompactTopology{
		Schema: CompactTopologySchemaV1, Selection: CompactTopologySelectionV1,
		Nodes: []CompactTopologyNode{}, Links: []CompactTopologyLink{}, Routes: []CompactTopologyRoute{},
	}
	clone := CloneCompactTopology(source)
	if clone.Nodes == nil || clone.Links == nil || clone.Routes == nil {
		t.Fatalf("clone changed non-nil empty graph slices to nil: nodes=%#v links=%#v routes=%#v", clone.Nodes, clone.Links, clone.Routes)
	}
	got, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"compact-v1","selection":"fair-complete-prefix-v1","limits":{"nodes":0,"links":0,"max_response_bytes_exclusive":0,"max_geo_bundle_bytes":0},"nodes":[],"links":[],"routes":[],"stats":{"nodes":{"total":0,"displayed":0,"omitted":0},"links":{"total":0,"displayed":0,"omitted":0},"routes":{"total":0,"displayed":0,"complete":0,"partial":0,"omitted":0},"node_observations":{"total":0,"displayed":0,"omitted":0},"link_observations":{"total":0,"displayed":0,"omitted":0}},"geo":{"eligible":0,"available":0,"included":0,"omitted":0,"unavailable":0},"truncated":false}`
	if string(got) != want {
		t.Fatalf("canonical empty clone JSON = %s, want %s", got, want)
	}
}

func TestBuildCompactTopologyCanonicalizesEmptyASNBeforeAccountingAndSerialization(t *testing.T) {
	report := compactTestReport([]TraceAttempt{compactTestAttempt(1,
		TopologyNode{ID: "local", Hop: 0, Address: "local", Status: "healthy"},
		TopologyNode{ID: "public", Hop: 1, Address: "8.8.8.8", Status: "healthy", PublicIP: true, ASN: &ASNInfo{}},
	)})
	compact := BuildCompactTopology(report)
	if compact.Nodes[1].ASN != nil {
		t.Fatalf("empty ASN was projected: %+v", compact.Nodes[1])
	}
	if got, want := compact.Geo, (CompactGeoStats{Eligible: 1, Unavailable: 1}); got != want {
		t.Fatalf("empty ASN affected Geo accounting: got %+v want %+v", got, want)
	}
	encoded, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"asn":{}`)) || bytes.Contains(encoded, []byte(`"asn"`)) {
		t.Fatalf("empty ASN leaked into compact JSON: %s", encoded)
	}
}

func TestBuildCompactTopologySerializesValidEmptyGraphWithArrays(t *testing.T) {
	compact := BuildCompactTopology(Report{Results: []Result{}})
	if compact.Nodes == nil || compact.Links == nil || compact.Routes == nil {
		t.Fatalf("producer empty graph is non-canonical: %+v", compact)
	}
	encoded, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"nodes":[],"links":[],"routes":[]`)) {
		t.Fatalf("empty graph arrays were not serialized exactly: %s", encoded)
	}
}

func TestBuildCompactTopologyCanonicalizesAndAggregatesTypedAndLegacyRoutes(t *testing.T) {
	typed := []TraceAttempt{compactTestAttempt(1,
		TopologyNode{ID: "a", Hop: 0, Address: "local", Status: "healthy"},
		TopologyNode{ID: "b", Hop: 1, Address: "2001:0db8:0:0:0:0:0:1", Status: "healthy", LatencyMS: 1},
		TopologyNode{ID: "c", Hop: 2, Address: "::ffff:192.0.2.1", Status: "healthy", LatencyMS: 3},
		TopologyNode{ID: "d", Hop: 3, Address: "Host.Example.", Status: "unknown"},
		TopologyNode{ID: "e", Hop: 4, Status: "failure"},
	)}
	legacy := []any{map[string]any{
		"attempt": 2, "status": "degraded",
		"topology": map[string]any{"reached": true, "nodes": []any{
			map[string]any{"id": "a", "hop": 0, "address": "LOCAL", "status": "healthy"},
			map[string]any{"id": "b", "hop": 1, "address": "2001:db8::1", "status": "degraded", "latency_ms": 3},
			map[string]any{"id": "c", "hop": 2, "address": "192.0.2.1", "status": "degraded", "latency_ms": 5},
			map[string]any{"id": "d", "hop": 3, "address": "host.example", "status": "healthy"},
			map[string]any{"id": "e", "hop": 4, "address": "", "status": "unknown"},
		}},
	}}
	report := compactTestReport(typed, legacy)
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}

	compact := BuildCompactTopology(report)
	if compact == nil {
		t.Fatal("BuildCompactTopology returned nil")
	}
	if got, want := compact.Stats.Nodes, (CompactCountStats{Total: 6, Displayed: 6}); got != want {
		t.Fatalf("node stats = %+v, want %+v", got, want)
	}
	if got, want := compact.Stats.Links, (CompactCountStats{Total: 5, Displayed: 5}); got != want {
		t.Fatalf("link stats = %+v, want %+v", got, want)
	}
	if compact.Stats.NodeObservations.Total != 10 || compact.Stats.LinkObservations.Total != 8 {
		t.Fatalf("observation stats = %+v", compact.Stats)
	}
	addresses := make([]string, len(compact.Nodes))
	for i, node := range compact.Nodes {
		addresses[i] = node.Address
	}
	if want := []string{"local", "2001:db8::1", "192.0.2.1", "host.example", "", ""}; !reflect.DeepEqual(addresses, want) {
		t.Fatalf("addresses = %#v, want %#v", addresses, want)
	}
	if compact.Nodes[1].Status != "degraded" || compact.Nodes[1].LatencyMSAvg == nil || *compact.Nodes[1].LatencyMSAvg != 2 {
		t.Fatalf("IPv6 aggregate = %+v", compact.Nodes[1])
	}
	if len(compact.Routes) != 2 || compact.Routes[0].NodeIDs[4] == compact.Routes[1].NodeIDs[4] {
		t.Fatalf("route-scoped unknown nodes merged: %+v", compact.Routes)
	}
	for _, link := range compact.Links {
		if link.From == link.To {
			t.Fatalf("self-link emitted: %+v", link)
		}
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("BuildCompactTopology mutated the raw report")
	}
}

func TestBuildCompactTopologyOmitsSelfLinksAndAggregatesRawLinkStatus(t *testing.T) {
	topology := Topology{
		Reached: true,
		Nodes: []TopologyNode{
			{ID: "local", Hop: 0, Address: "local", Status: "healthy"},
			{ID: "same-a", Hop: 1, Address: "192.0.2.1", Status: "healthy", LatencyMS: 1},
			{ID: "same-b", Hop: 2, Address: "192.0.2.1", Status: "healthy", LatencyMS: 2},
			{ID: "next", Hop: 3, Address: "192.0.2.2", Status: "healthy", LatencyMS: 3},
		},
		Links: []TopologyLink{
			{From: "local", To: "same-a", Status: "healthy"},
			{From: "same-a", To: "same-b", Status: "failure"},
			{From: "same-b", To: "next", Status: "degraded"},
		},
	}
	report := compactTestReport([]TraceAttempt{{Attempt: 1, Status: StatusHealthy, Topology: &topology}})
	compact := BuildCompactTopology(report)
	if compact.Stats.Links.Total != 2 || compact.Stats.LinkObservations.Total != 2 || len(compact.Links) != 2 {
		t.Fatalf("self-link affected link statistics: %+v", compact.Stats)
	}
	if compact.Links[1].Status != "degraded" {
		t.Fatalf("raw link status was not retained: %+v", compact.Links)
	}
	if compact.Nodes[0].LatencyMSAvg != nil {
		t.Fatalf("local node gained a synthetic zero latency: %+v", compact.Nodes[0])
	}
}

func TestBuildCompactTopologyCollapsesConsecutiveCanonicalNodesBeforeSelectingEdges(t *testing.T) {
	topology := Topology{
		Reached: true,
		Nodes: []TopologyNode{
			{ID: "local", Hop: 0, Address: "local", Status: "healthy"},
			{ID: "v6-a", Hop: 1, Address: "2001:0db8:0:0:0:0:0:1", Status: "healthy", LatencyMS: 1},
			{ID: "v6-b", Hop: 2, Address: "2001:db8::1", Status: "degraded", LatencyMS: 3},
			{ID: "mapped", Hop: 3, Address: "::ffff:192.0.2.1", Status: "healthy", LatencyMS: 5},
			{ID: "v4", Hop: 4, Address: "192.0.2.1", Status: "healthy", LatencyMS: 7},
			{ID: "unknown-a", Hop: 5, Status: "unknown"},
			{ID: "unknown-b", Hop: 6, Status: "unknown"},
		},
	}
	for i := 1; i < len(topology.Nodes); i++ {
		topology.Links = append(topology.Links, TopologyLink{From: topology.Nodes[i-1].ID, To: topology.Nodes[i].ID, Status: topology.Nodes[i].Status})
	}

	build := BuildCompactTopologyWithOptions(compactTestReport([]TraceAttempt{{Attempt: 1, Status: StatusDegraded, Topology: &topology}}), -1, true)
	compact := build.Topology
	if got, want := compact.Routes[0].NodeIDs, []string{"n000001", "n000002", "n000003", "n000004", "n000005"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical route node IDs = %#v, want %#v", got, want)
	}
	if got, want := compact.Stats.NodeObservations, (CompactCountStats{Total: 5, Displayed: 5}); got != want {
		t.Fatalf("canonical route observation stats = %+v, want %+v", got, want)
	}
	if got, want := compact.Stats.LinkObservations, (CompactCountStats{Total: 4, Displayed: 4}); got != want || build.AcceptedTransactions != 4 {
		t.Fatalf("canonical link stats = %+v, transactions=%d, want 4", got, build.AcceptedTransactions)
	}
	if node := compact.Nodes[1]; node.Observations != 2 || node.Status != "degraded" || node.HopMin != 1 || node.HopMax != 2 || node.LatencyMSAvg == nil || *node.LatencyMSAvg != 2 {
		t.Fatalf("collapsed IPv6 aggregate = %+v", node)
	}
	if node := compact.Nodes[2]; node.Observations != 2 || node.LatencyMSAvg == nil || *node.LatencyMSAvg != 6 {
		t.Fatalf("collapsed mapped IPv4 aggregate = %+v", node)
	}
	if compact.Routes[0].NodeIDs[3] == compact.Routes[0].NodeIDs[4] {
		t.Fatalf("consecutive unknown identities merged: %+v", compact.Routes[0])
	}
}

func TestBuildCompactTopologyMaximumUnreachedRouteHasThirtyTwoNodes(t *testing.T) {
	nodes := make([]TopologyNode, 0, CompactTopologyMaxRouteNodes)
	nodes = append(nodes, TopologyNode{ID: "local", Hop: 0, Address: "local", Status: "healthy"})
	for hop := 1; hop <= MaxTraceHops; hop++ {
		nodes = append(nodes, TopologyNode{ID: fmt.Sprintf("hop-%d", hop), Hop: hop, Address: fmt.Sprintf("192.0.2.%d", hop), Status: "healthy", LatencyMS: float64(hop)})
	}
	nodes = append(nodes, TopologyNode{ID: "destination", Hop: MaxTraceHops + 1, Address: "example.test", Status: "failure"})
	attempt := compactTestAttempt(1, nodes...)
	attempt.Topology.Reached = false
	attempt.Status = StatusUnreachable

	compact := BuildCompactTopology(compactTestReport([]TraceAttempt{attempt}))
	if CompactTopologyMaxRouteNodes != 32 {
		t.Fatalf("CompactTopologyMaxRouteNodes = %d, want 32", CompactTopologyMaxRouteNodes)
	}
	if got := len(compact.Routes[0].NodeIDs); got != CompactTopologyMaxRouteNodes {
		t.Fatalf("route nodes = %d, want %d", got, CompactTopologyMaxRouteNodes)
	}
}

func TestBuildCompactTopologyWithOptionsExposesHiddenTransactionCountAndGeoToggle(t *testing.T) {
	geo := &GeoLocation{City: "Seoul"}
	report := compactTestReport([]TraceAttempt{compactTestAttempt(1,
		TopologyNode{ID: "l", Hop: 0, Address: "local", Status: "healthy"},
		TopologyNode{ID: "a", Hop: 1, Address: "8.8.8.8", Status: "healthy", PublicIP: true, Geolocation: geo},
		TopologyNode{ID: "b", Hop: 2, Address: "1.1.1.1", Status: "healthy"},
	)})
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}

	build := BuildCompactTopologyWithOptions(report, 1, false)
	if build.AcceptedTransactions != 1 || build.Topology == nil || build.Topology.Geo.Included != 0 || build.Topology.Geo.Omitted != 1 {
		t.Fatalf("option build = %+v", build)
	}
	encoded, err := json.Marshal(build)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("AcceptedTransactions")) || bytes.Contains(encoded, []byte("accepted_transactions")) {
		t.Fatalf("transaction count leaked into JSON: %s", encoded)
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("option build mutated the raw report")
	}
}

func TestBuildCompactTopologyIsFairCompletePrefixAndExposesTransactionLimit(t *testing.T) {
	long := func(attempt int, prefix string) TraceAttempt {
		return compactTestAttempt(attempt,
			TopologyNode{ID: "l", Hop: 0, Address: "local", Status: "healthy"},
			TopologyNode{ID: "1", Hop: 1, Address: prefix + "-1", Status: "healthy"},
			TopologyNode{ID: "2", Hop: 2, Address: prefix + "-2", Status: "healthy"},
			TopologyNode{ID: "3", Hop: 3, Address: prefix + "-3", Status: "healthy"},
		)
	}
	report := compactTestReport([]TraceAttempt{long(1, "first")}, []TraceAttempt{long(1, "second")})
	result := buildCompactTopology(report, compactBuildOptions{maxNodes: 3, maxLinks: 1000, maxAcceptedTransactions: -1})
	if result.AcceptedTransactions != 2 || len(result.Topology.Routes) != 2 {
		t.Fatalf("fair selection result = %+v", result)
	}
	for _, route := range result.Topology.Routes {
		if route.Complete || len(route.NodeIDs) != 2 {
			t.Fatalf("route is not a complete prefix: %+v", route)
		}
	}
	if got := result.Topology.TruncationReasons; !reflect.DeepEqual(got, []CompactTruncationReason{CompactTruncationNodeLimit}) {
		t.Fatalf("reasons = %#v", got)
	}

	limited := buildCompactTopology(report, compactBuildOptions{maxNodes: 500, maxLinks: 1000, maxAcceptedTransactions: 1})
	if limited.AcceptedTransactions != 1 {
		t.Fatalf("accepted transactions = %d", limited.AcceptedTransactions)
	}
	if limited.Topology.Stats.LinkObservations.Displayed != 1 {
		t.Fatalf("transaction-limited stats = %+v", limited.Topology.Stats)
	}
}

func TestBuildCompactTopologyExactMaximumFixtureStatsAndDeterminism(t *testing.T) {
	results := make([]any, MaxTargets)
	for resultIndex := 0; resultIndex < MaxTargets; resultIndex++ {
		attempts := make([]TraceAttempt, MaxTraceAttempts)
		for attemptIndex := range attempts {
			nodes := make([]TopologyNode, 0, MaxTraceHops+1)
			nodes = append(nodes, TopologyNode{ID: "local", Hop: 0, Address: "local", Status: "healthy"})
			for hop := 1; hop <= MaxTraceHops; hop++ {
				nodes = append(nodes, TopologyNode{ID: fmt.Sprintf("h%d", hop), Hop: hop, Address: fmt.Sprintf("r%d-a%d-h%d.example", resultIndex, attemptIndex, hop), Status: "healthy", LatencyMS: float64(hop)})
			}
			attempts[attemptIndex] = compactTestAttempt(attemptIndex+1, nodes...)
		}
		results[resultIndex] = attempts
	}
	report := compactTestReport(results...)
	compact := BuildCompactTopology(report)
	if got, want := compact.Stats.Nodes, (CompactCountStats{Total: 6001, Displayed: 500, Omitted: 5501}); got != want {
		t.Fatalf("node stats = %+v, want %+v", got, want)
	}
	if got, want := compact.Stats.Links, (CompactCountStats{Total: 6000, Displayed: 499, Omitted: 5501}); got != want {
		t.Fatalf("link stats = %+v, want %+v", got, want)
	}
	if got, want := compact.Stats.Routes, (CompactRouteStats{Total: 200, Displayed: 200, Partial: 200}); got != want {
		t.Fatalf("route stats = %+v, want %+v", got, want)
	}
	if got, want := compact.Stats.NodeObservations, (CompactCountStats{Total: 6200, Displayed: 699, Omitted: 5501}); got != want {
		t.Fatalf("node observation stats = %+v, want %+v", got, want)
	}
	if got, want := compact.Stats.LinkObservations, (CompactCountStats{Total: 6000, Displayed: 499, Omitted: 5501}); got != want {
		t.Fatalf("link observation stats = %+v, want %+v", got, want)
	}
	for i, stats := range compact.ResultStats {
		wantDisplayed := 35
		if i == MaxTargets-1 {
			wantDisplayed = 34
		}
		if stats.Routes.Total != 10 || stats.Routes.Partial != 10 || stats.NodeObservations.Total != 310 || stats.NodeObservations.Displayed != wantDisplayed || stats.LinkObservations.Total != 300 || stats.LinkObservations.Displayed != wantDisplayed-10 {
			t.Fatalf("result stats[%d] = %+v", i, stats)
		}
	}
	if got := compact.TruncationReasons; !reflect.DeepEqual(got, []CompactTruncationReason{CompactTruncationNodeLimit}) || !compact.Truncated {
		t.Fatalf("truncation = %v %#v", compact.Truncated, got)
	}
	assertCompactReferences(t, compact)
	first, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		next, err := json.Marshal(BuildCompactTopology(report))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("marshal %d differed", i)
		}
	}
}

func TestBuildCompactTopologyGeoBundleIsAtomicAndInvalidStringsAreUnavailable(t *testing.T) {
	smallGeo := &GeoLocation{City: "Seoul", CountryCode: "KR", Latitude: 37.5, Longitude: 127}
	hugeASN := &ASNInfo{Number: 64500, Organization: strings.Repeat("x", CompactTopologyMaxGeoBundleBytes+1)}
	report := compactTestReport([]TraceAttempt{compactTestAttempt(1,
		TopologyNode{ID: "l", Hop: 0, Address: "local", Status: "healthy"},
		TopologyNode{ID: "a", Hop: 1, Address: "8.8.8.8", Status: "healthy", PublicIP: true, Geolocation: smallGeo},
		TopologyNode{ID: "b", Hop: 2, Address: "1.1.1.1", Status: "healthy", PublicIP: true, ASN: hugeASN},
		TopologyNode{ID: "c", Hop: 3, Address: "9.9.9.9", Status: "healthy", PublicIP: true},
	)})
	compact := BuildCompactTopology(report)
	if got, want := compact.Geo, (CompactGeoStats{Eligible: 3, Available: 1, Included: 1, Unavailable: 2}); got != want {
		t.Fatalf("geo stats = %+v, want %+v", got, want)
	}
	if compact.Nodes[1].Geolocation == nil || compact.Nodes[1].ASN != nil || compact.Nodes[2].Geolocation != nil || compact.Nodes[2].ASN != nil {
		t.Fatalf("geo bundles were not copied atomically: %+v", compact.Nodes)
	}
	if compact.Truncated || len(compact.TruncationReasons) != 0 {
		t.Fatalf("invalid metadata was reported as truncation: %#v", compact.TruncationReasons)
	}
}

func TestBuildCompactTopologyRejectsInvalidGeoBundlesAsUnavailable(t *testing.T) {
	invalidBundles := []struct {
		name string
		geo  *GeoLocation
		asn  *ASNInfo
	}{
		{name: "non-finite latitude", geo: &GeoLocation{Latitude: math.NaN()}},
		{name: "latitude out of range", geo: &GeoLocation{Latitude: 90.0001}},
		{name: "longitude out of range", geo: &GeoLocation{Longitude: -180.0001}},
		{name: "oversized city", geo: &GeoLocation{City: strings.Repeat("x", CompactTopologyMaxGeoStringBytes+1)}},
		{name: "oversized organization", asn: &ASNInfo{Organization: strings.Repeat("x", CompactTopologyMaxGeoStringBytes+1)}},
		{name: "valid geo with invalid ASN", geo: &GeoLocation{Latitude: 37.5, Longitude: 127}, asn: &ASNInfo{Organization: strings.Repeat("x", CompactTopologyMaxGeoStringBytes+1)}},
	}
	if strconv.IntSize > 32 {
		tooLarge := uint64(math.MaxUint32) + 1
		invalidBundles = append(invalidBundles, struct {
			name string
			geo  *GeoLocation
			asn  *ASNInfo
		}{name: "ASN exceeds uint32", asn: &ASNInfo{Number: uint(tooLarge)}})
	}

	nodes := []TopologyNode{{ID: "local", Hop: 0, Address: "local", Status: "healthy"}}
	for index, bundle := range invalidBundles {
		nodes = append(nodes, TopologyNode{
			ID: fmt.Sprintf("invalid-%d", index), Hop: index + 1,
			Address: fmt.Sprintf("203.0.113.%d", index+1), Status: "healthy", PublicIP: true,
			Geolocation: bundle.geo, ASN: bundle.asn,
		})
	}
	compact := BuildCompactTopology(compactTestReport([]TraceAttempt{compactTestAttempt(1, nodes...)}))
	if got, want := compact.Geo, (CompactGeoStats{Eligible: len(invalidBundles), Unavailable: len(invalidBundles)}); got != want {
		t.Fatalf("invalid bundle stats = %+v, want %+v", got, want)
	}
	if compact.Truncated || len(compact.TruncationReasons) != 0 {
		t.Fatalf("invalid bundles were reported as size truncation: %+v", compact.TruncationReasons)
	}
	for index, node := range compact.Nodes[1:] {
		if node.Geolocation != nil || node.ASN != nil {
			t.Fatalf("invalid bundle %q leaked partially: %+v", invalidBundles[index].name, node)
		}
	}
	encoded, err := json.Marshal(compact)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("invalid bundle poisoned compact JSON: %q, %v", encoded, err)
	}
}

func TestBuildCompactTopologyIncludesZeroLatencyAndUsesLaterValidGeoBundle(t *testing.T) {
	geo := &GeoLocation{City: "Seoul", CountryCode: "KR", Latitude: 37.5, Longitude: 127}
	report := compactTestReport([]TraceAttempt{
		compactTestAttempt(1,
			TopologyNode{ID: "l1", Hop: 0, Address: "local", Status: "healthy"},
			TopologyNode{ID: "a1", Hop: 1, Address: "8.8.8.8", Status: "healthy", LatencyMS: 0, PublicIP: true},
		),
		compactTestAttempt(2,
			TopologyNode{ID: "l2", Hop: 0, Address: "local", Status: "healthy"},
			TopologyNode{ID: "a2", Hop: 1, Address: "8.8.8.8", Status: "healthy", LatencyMS: 10, PublicIP: true, Geolocation: geo},
		),
	})

	compact := BuildCompactTopology(report)
	if len(compact.Nodes) != 2 || compact.Nodes[1].LatencyMSAvg == nil || *compact.Nodes[1].LatencyMSAvg != 5 {
		t.Fatalf("zero latency was excluded from aggregate: %+v", compact.Nodes)
	}
	if compact.Nodes[1].Geolocation == nil || compact.Nodes[1].Geolocation.City != "Seoul" {
		t.Fatalf("later valid Geo bundle was ignored: %+v", compact.Nodes[1])
	}
	if got, want := compact.Geo, (CompactGeoStats{Eligible: 1, Available: 1, Included: 1}); got != want {
		t.Fatalf("geo stats = %+v, want %+v", got, want)
	}
}

func TestBuildCompactTopologyGeoContributionExactBoundary(t *testing.T) {
	organizationFor := func(contribution int) string {
		t.Helper()
		template := CompactTopologyNode{ID: "n000002", Kind: CompactNodeIP, Address: "8.8.8.8", Status: "healthy", HopMin: 1, HopMax: 1, Observations: 1, PublicIP: true}
		for size := 1; size <= contribution; size++ {
			organization := strings.Repeat("x", size)
			if compactGeoBundleContribution(template, nil, &ASNInfo{Number: 64500, Organization: organization}) == contribution {
				return organization
			}
		}
		t.Fatalf("could not construct Geo contribution of %d bytes", contribution)
		return ""
	}
	for _, tt := range []struct {
		name         string
		contribution int
		included     int
		omitted      int
	}{
		{name: "4096 accepted", contribution: 4096, included: 1},
		{name: "4097 omitted", contribution: 4097, omitted: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			asn := &ASNInfo{Number: 64500, Organization: organizationFor(tt.contribution)}
			report := compactTestReport([]TraceAttempt{compactTestAttempt(1,
				TopologyNode{ID: "l", Hop: 0, Address: "local", Status: "healthy"},
				TopologyNode{ID: "a", Hop: 1, Address: "8.8.8.8", Status: "healthy", PublicIP: true, ASN: asn},
			)})
			compact := BuildCompactTopology(report)
			if compact.Geo.Included != tt.included || compact.Geo.Omitted != tt.omitted {
				t.Fatalf("contribution %d produced Geo stats %+v", tt.contribution, compact.Geo)
			}
			withoutBundle := compact.Nodes[1]
			withoutBundle.Geolocation = nil
			withoutBundle.ASN = nil
			if got := compactGeoBundleContribution(withoutBundle, nil, asn); got != tt.contribution {
				t.Fatalf("Geo contribution = %d, want %d", got, tt.contribution)
			}
		})
	}
}

func TestBuildCompactTopologySkipsMalformedAttemptsAndNonFiniteLatency(t *testing.T) {
	report := compactTestReport(
		[]any{map[string]any{"attempt": "bad", "topology": map[string]any{"nodes": "bad"}}},
		[]TraceAttempt{compactTestAttempt(1,
			TopologyNode{ID: "l", Hop: 0, Address: "local", Status: "healthy"},
			TopologyNode{ID: "a", Hop: 1, Address: "example.test", Status: "healthy", LatencyMS: math.Inf(1)},
		)},
	)
	compact := BuildCompactTopology(report)
	if compact.Stats.Routes.Total != 1 || len(compact.Nodes) != 2 || compact.Nodes[1].LatencyMSAvg != nil {
		t.Fatalf("malformed/non-finite input was not handled safely: %+v", compact)
	}
}

func TestCompactReportPrunesOnlyRawTracerouteTopologyWithoutMutation(t *testing.T) {
	analysis := &Analysis{Verdict: VerdictAttention, Findings: []Finding{}, Evidence: []Evidence{}, Actions: []Action{}, Coverage: Coverage{Available: []string{}, Missing: []string{}, ProviderFailures: []CoverageIssue{}, Limitations: []CoverageIssue{}}}
	report := Report{
		ID: "report", Status: StatusDegraded, Summary: Summary{Total: 2, Failed: 1}, Analysis: analysis,
		Results: []Result{
			{Kind: KindTraceroute, Address: "example.test", Status: StatusDegraded, LatencyMS: 42, Message: "kept", Details: map[string]any{
				"attempts": []TraceAttempt{{Attempt: 1, Topology: &Topology{}}}, "topology": Topology{},
				"attempts_total": 1, "geoip_provider_failures": 2, "bounded_other": "kept",
			}},
			{Kind: KindDNS, Details: map[string]any{"attempts": "not traceroute", "topology": "kept"}},
		},
	}
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	topology := BuildCompactTopology(report)
	compact := BuildCompactReport(report, topology)
	if compact.CompactTopology != topology || compact.Analysis != analysis || compact.Summary != report.Summary {
		t.Fatalf("report scalars/analysis/topology not preserved: %+v", compact)
	}
	details := compact.Results[0].Details
	if _, ok := details["attempts"]; ok {
		t.Fatalf("attempts retained: %+v", details)
	}
	if _, ok := details["topology"]; ok {
		t.Fatalf("representative topology retained: %+v", details)
	}
	for _, key := range []string{"attempts_total", "geoip_provider_failures", "bounded_other"} {
		if _, ok := details[key]; !ok {
			t.Fatalf("bounded detail %q removed: %+v", key, details)
		}
	}
	if compact.Results[1].Details["attempts"] != "not traceroute" || compact.Results[1].Details["topology"] != "kept" {
		t.Fatalf("non-traceroute details were pruned: %+v", compact.Results[1].Details)
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("compact report projection mutated raw report")
	}
}

func TestBuildCompactTopologyCountsEmptyAndDuplicateTopologyAttempts(t *testing.T) {
	attempts := []TraceAttempt{
		{Attempt: 1, Status: StatusUnreachable, Topology: &Topology{}},
		compactTestAttempt(2,
			TopologyNode{ID: "l1", Hop: 0, Address: "local", Status: "healthy"},
			TopologyNode{ID: "u1", Hop: 1, Status: "unknown"},
		),
		compactTestAttempt(2,
			TopologyNode{ID: "l2", Hop: 0, Address: "local", Status: "healthy"},
			TopologyNode{ID: "u2", Hop: 1, Status: "unknown"},
		),
		{Attempt: 3, Status: StatusUnreachable, ErrorCode: "execution_failed"},
	}

	compact := BuildCompactTopology(compactTestReport(attempts))
	if got, want := compact.Stats.Routes, (CompactRouteStats{Total: 3, Displayed: 2, Complete: 2, Omitted: 1}); got != want {
		t.Fatalf("route stats = %+v, want %+v", got, want)
	}
	if len(compact.Routes) != 2 || compact.Routes[0].Attempt != 2 || compact.Routes[1].Attempt != 2 {
		t.Fatalf("duplicate attempts were not retained deterministically: %+v", compact.Routes)
	}
	if compact.Routes[0].NodeIDs[1] == compact.Routes[1].NodeIDs[1] {
		t.Fatalf("malformed route-scoped unknown identities collided: %+v", compact.Routes)
	}
}

func TestBuildCompactTopologyRejectsFailedAndInconsistentAttempts(t *testing.T) {
	reached := Topology{Reached: true, Nodes: []TopologyNode{{ID: "local", Hop: 0, Address: "local", Status: "healthy"}, {ID: "public", Hop: 1, Address: "8.8.8.8", Status: "healthy", PublicIP: true}}, Links: []TopologyLink{{From: "local", To: "public", Status: "healthy"}}}
	unreached := Topology{Reached: false, Nodes: []TopologyNode{{ID: "local", Hop: 0, Address: "local", Status: "healthy"}, {ID: "destination", Hop: 1, Address: "example.test", Status: "failure"}}, Links: []TopologyLink{{From: "local", To: "destination", Status: "failure"}}}
	report := compactTestReport([]TraceAttempt{
		{Attempt: 1, Status: StatusUnreachable, Topology: &reached, ErrorCode: "traceroute_failed", Message: "raw command diagnostic"},
		{Attempt: 2, Status: StatusUnreachable, Topology: &reached},
		{Attempt: 3, Status: StatusHealthy, Topology: &unreached},
		{Attempt: 4, Status: StatusUnreachable, Topology: &unreached},
	})
	compact := BuildCompactTopology(report)
	if got, want := len(compact.Routes), 1; got != want {
		t.Fatalf("compact routes=%d, want %d: %+v", got, want, compact.Routes)
	}
	if compact.Routes[0].Attempt != 4 || compact.Routes[0].Reached || compact.Routes[0].Status != StatusUnreachable {
		t.Fatalf("only eligible status-consistent attempt should remain: %+v", compact.Routes)
	}
	if got, want := compact.Stats.Routes.Total, 1; got != want {
		t.Fatalf("route stats total=%d, want %d", got, want)
	}
	if compact.Geo.Eligible != 0 || compact.Geo.Available != 0 || compact.Geo.Included != 0 {
		t.Fatalf("ineligible reached public node contaminated Geo stats: %+v", compact.Geo)
	}
}

func TestBuildCompactTopologyEmptyGraphAlwaysMarshalsArrays(t *testing.T) {
	compact := BuildCompactTopology(compactTestReport([]TraceAttempt{
		{Attempt: 1, Status: StatusUnreachable, Topology: &Topology{}},
		{Attempt: 2, Status: StatusUnreachable},
	}))
	encoded, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"nodes":[]`, `"links":[]`, `"routes":[]`} {
		if !bytes.Contains(encoded, []byte(field)) {
			t.Fatalf("empty graph JSON %s does not contain %s", encoded, field)
		}
	}
	if got, want := compact.Stats.Routes, (CompactRouteStats{Total: 1, Omitted: 1}); got != want {
		t.Fatalf("route stats = %+v, want %+v", got, want)
	}
}

func assertCompactReferences(t *testing.T, compact *CompactTopology) {
	t.Helper()
	ids := make(map[string]struct{}, len(compact.Nodes))
	for _, node := range compact.Nodes {
		if _, exists := ids[node.ID]; exists {
			t.Fatalf("duplicate node ID %q", node.ID)
		}
		ids[node.ID] = struct{}{}
	}
	for _, link := range compact.Links {
		if _, ok := ids[link.From]; !ok {
			t.Fatalf("dangling link from %q", link.From)
		}
		if _, ok := ids[link.To]; !ok {
			t.Fatalf("dangling link to %q", link.To)
		}
	}
	for _, route := range compact.Routes {
		for _, id := range route.NodeIDs {
			if _, ok := ids[id]; !ok {
				t.Fatalf("dangling route node %q", id)
			}
		}
	}
}
