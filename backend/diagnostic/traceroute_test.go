package diagnostic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

type geoIPLookupFunc func(context.Context, net.IP) (IPMetadata, error)

func (fn geoIPLookupFunc) Lookup(ctx context.Context, ip net.IP) (IPMetadata, error) {
	return fn(ctx, ip)
}

func TestTracerouteEmitsFreshUpstreamGeoIPEnrichmentCoverage(t *testing.T) {
	now := time.Now().UTC()
	checker := TracerouteChecker{
		Command: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1  8.8.8.8  1.0 ms\n"), nil
		},
		GeoIP: geoIPLookupFunc(func(context.Context, net.IP) (IPMetadata, error) {
			return IPMetadata{Source: GeoIPSourceUpstream, FetchedAt: now, ExpiresAt: now.Add(defaultGeoIPCacheTTL)}, nil
		}),
	}

	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 1})
	coverage, ok := result.Details["geoip_enrichment"].(EnrichmentCoverage)
	if !ok {
		t.Fatalf("geoip_enrichment = %#v", result.Details["geoip_enrichment"])
	}
	want := EnrichmentCoverage{Provider: "geoip", Source: EnrichmentSourceUpstream, UpstreamFetches: 1, MaxAgeMS: coverage.MaxAgeMS, Failures: []EnrichmentFailure{}}
	if !reflect.DeepEqual(coverage, want) {
		t.Fatalf("coverage = %+v, want %+v", coverage, want)
	}
	if coverage.MaxAgeMS < 0 || coverage.MaxAgeMS > 100 {
		t.Fatalf("fresh upstream max_age_ms = %d", coverage.MaxAgeMS)
	}
	if _, exists := result.Details["geoip_provider_failures"]; exists {
		t.Fatalf("zero legacy provider failures emitted: %+v", result.Details)
	}
	encoded, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"8.8.8.8", "target", "error", "timestamp", "url"} {
		if strings.Contains(strings.ToLower(string(encoded)), canary) {
			t.Fatalf("privacy canary %q leaked in %s", canary, encoded)
		}
	}
}

func TestGeoIPEnrichmentCoverageAggregatesUniqueMixedLookupsAndTypedFailures(t *testing.T) {
	now := time.Now().UTC()
	attempts := []TraceAttempt{
		{Topology: &Topology{Nodes: []TopologyNode{{Address: "8.8.8.8"}, {Address: "1.1.1.1"}, {Address: "9.9.9.9"}}}},
		{Topology: &Topology{Nodes: []TopologyNode{{Address: "8.8.8.8"}, {Address: "208.67.222.222"}}}},
	}
	lookup := geoIPLookupFunc(func(_ context.Context, ip net.IP) (IPMetadata, error) {
		switch ip.String() {
		case "8.8.8.8":
			fetched := now.Add(-72 * time.Hour)
			return IPMetadata{Source: GeoIPSourceCache, FetchedAt: fetched, ExpiresAt: fetched.Add(time.Hour)}, nil
		case "1.1.1.1":
			return IPMetadata{Source: GeoIPSourceUpstream, FetchedAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour)}, nil
		case "9.9.9.9":
			return IPMetadata{}, &GeoIPError{Kind: GeoIPErrorTimeout, Retryable: true}
		default:
			return IPMetadata{}, errors.New("SECRET provider URL https://geo.invalid/208.67.222.222")
		}
	})

	coverage := enrichTopologiesWithCoverage(context.Background(), attempts, lookup)
	want := EnrichmentCoverage{
		Provider: "geoip", Source: EnrichmentSourceMixed, CacheHits: 1, UpstreamFetches: 1,
		MaxAgeMS: time.Hour.Milliseconds(),
		Failures: []EnrichmentFailure{
			{Kind: GeoIPErrorTimeout, Count: 1, Retryable: true},
			{Kind: GeoIPErrorUnavailable, Count: 1, Retryable: true},
		},
	}
	if !reflect.DeepEqual(coverage, want) {
		t.Fatalf("coverage = %+v, want %+v", coverage, want)
	}
	if failures := enrichmentFailureCount(coverage.Failures); failures != 2 {
		t.Fatalf("legacy failure count = %d, want 2", failures)
	}
	encoded, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"secret", "geo.invalid", "208.67.222.222", "fetched_at", "expires_at"} {
		if strings.Contains(strings.ToLower(string(encoded)), canary) {
			t.Fatalf("privacy canary %q leaked in %s", canary, encoded)
		}
	}
}

func TestGeoIPFailureKindsHaveFixedRetryabilityAndStableOrder(t *testing.T) {
	want := []EnrichmentFailure{
		{Kind: GeoIPErrorBusy, Count: 1, Retryable: true},
		{Kind: GeoIPErrorCancelled, Count: 1, Retryable: false},
		{Kind: GeoIPErrorMalformed, Count: 1, Retryable: false},
		{Kind: GeoIPErrorNotFound, Count: 1, Retryable: false},
		{Kind: GeoIPErrorPolicy, Count: 1, Retryable: false},
		{Kind: GeoIPErrorRateLimited, Count: 1, Retryable: true},
		{Kind: GeoIPErrorTimeout, Count: 1, Retryable: true},
		{Kind: GeoIPErrorUnavailable, Count: 1, Retryable: true},
	}
	got := make([]EnrichmentFailure, 0, len(geoIPFailureKinds()))
	for _, kind := range geoIPFailureKinds() {
		normalizedKind, retryable := normalizedGeoIPFailure(&GeoIPError{Kind: kind, Retryable: !geoIPFailureRetryable(kind)})
		got = append(got, EnrichmentFailure{Kind: normalizedKind, Count: 1, Retryable: retryable})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fixed failures = %+v, want %+v", got, want)
	}
}

func TestTraceroutePreservesLegacyGeoIPProviderFailureCount(t *testing.T) {
	checker := TracerouteChecker{
		Command: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1  8.8.8.8  1.0 ms\n"), nil
		},
		GeoIP: geoIPLookupFunc(func(context.Context, net.IP) (IPMetadata, error) {
			return IPMetadata{}, errors.New("provider prose must not escape")
		}),
	}
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 1})
	if result.Details["geoip_provider_failures"] != 1 {
		t.Fatalf("legacy failure count = %#v", result.Details["geoip_provider_failures"])
	}
	coverage := result.Details["geoip_enrichment"].(EnrichmentCoverage)
	if coverage.Source != EnrichmentSourceNone || !reflect.DeepEqual(coverage.Failures, []EnrichmentFailure{{Kind: GeoIPErrorUnavailable, Count: 1, Retryable: true}}) {
		t.Fatalf("coverage = %+v", coverage)
	}
}

func TestParseTracerouteRejectsOutputOverByteLimit(t *testing.T) {
	output := "traceroute to example.test (203.0.113.8), 30 hops max\n1  203.0.113.8  1.0 ms\n"
	output += strings.Repeat("x", MaxTraceOutputBytes+1-len(output))

	_, err := parseTraceroute(output, "example.test")
	if !errors.Is(err, ErrTraceOutputLimit) {
		t.Fatalf("error = %v, want %v", err, ErrTraceOutputLimit)
	}
}

func TestParseTracerouteRejectsCommandHopOverLimit(t *testing.T) {
	output := "traceroute to example.test (203.0.113.8), 30 hops max\n31  203.0.113.8  1.0 ms\n"

	_, err := parseTraceroute(output, "example.test")
	if !errors.Is(err, ErrTraceHopLimit) {
		t.Fatalf("error = %v, want %v", err, ErrTraceHopLimit)
	}
}

func TestParseTracerouteRejectsNonPositiveAndNonIncreasingHops(t *testing.T) {
	for _, tt := range []struct {
		name string
		hops string
	}{
		{name: "negative", hops: "-1  192.0.2.1  1.0 ms"},
		{name: "zero", hops: "0  192.0.2.1  1.0 ms"},
		{name: "duplicate", hops: "1  192.0.2.1  1.0 ms\n1  192.0.2.2  2.0 ms"},
		{name: "decreasing", hops: "2  192.0.2.1  1.0 ms\n1  192.0.2.2  2.0 ms"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output := "traceroute to example.test (203.0.113.8), 30 hops max\n" + tt.hops
			_, err := parseTraceroute(output, "example.test")
			if !errors.Is(err, ErrTraceHopSequence) {
				t.Fatalf("error = %v, want %v", err, ErrTraceHopSequence)
			}
		})
	}
}

func TestParseTracerouteAllowsSkippedStrictlyIncreasingHops(t *testing.T) {
	output := "traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.1  1.0 ms\n3  192.0.2.3  3.0 ms\n30  *\n"
	topology, err := parseTraceroute(output, "example.test")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int, 0, len(topology.Nodes))
	for _, node := range topology.Nodes {
		got = append(got, node.Hop)
	}
	want := []int{0, 1, 3, 30, 31}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hops = %v, want %v", got, want)
	}
}

func TestParseTracerouteExactHopRangeCapsNodesAndLinksIncludingSyntheticDestination(t *testing.T) {
	var output strings.Builder
	output.WriteString("traceroute to example.test (203.0.113.8), 30 hops max\n")
	for hop := 1; hop <= MaxTraceHops; hop++ {
		fmt.Fprintf(&output, "%d  *\n", hop)
	}
	topology, err := parseTraceroute(output.String(), "example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(topology.Nodes), MaxTraceHops+2; got != want {
		t.Fatalf("nodes = %d, want %d", got, want)
	}
	if got, want := len(topology.Links), MaxTraceHops+1; got != want {
		t.Fatalf("links = %d, want %d", got, want)
	}
	if topology.Nodes[1].Hop != 1 || topology.Nodes[MaxTraceHops].Hop != MaxTraceHops || topology.Nodes[MaxTraceHops+1].Hop != MaxTraceHops+1 {
		t.Fatalf("unexpected boundary hops: %+v", topology.Nodes)
	}
}

func TestParseTracerouteKeepsSyntheticDestinationAfterValidHopThirty(t *testing.T) {
	output := "traceroute to example.test (203.0.113.8), 30 hops max\n30  *\n"

	topology, err := parseTraceroute(output, "example.test")
	if err != nil {
		t.Fatal(err)
	}
	last := topology.Nodes[len(topology.Nodes)-1]
	if topology.Reached || last.Hop != MaxTraceHops+1 || last.Address != "example.test" || last.Status != "failure" {
		t.Fatalf("unexpected synthetic destination: %+v", topology)
	}
}

func TestParseTracerouteRejectsMalformedAndNonFiniteLatency(t *testing.T) {
	for _, latency := range []string{"1e999", "NaN", "Inf", "+Inf", "-Inf", "not-a-number"} {
		t.Run(latency, func(t *testing.T) {
			output := "traceroute to example.test (203.0.113.8), 30 hops max\n1  203.0.113.8  " + latency + " ms\n"
			topology, err := parseTraceroute(output, "example.test")
			if !errors.Is(err, ErrTraceLatency) {
				t.Fatalf("error = %v, want %v", err, ErrTraceLatency)
			}
			for _, node := range topology.Nodes {
				if node.Status == "healthy" && node.Hop > 0 {
					t.Fatalf("malformed latency produced healthy node: %+v", node)
				}
			}
		})
	}
}

func TestParseTracerouteParsesWindowsThreeProbeRowsUsingMinimumLatency(t *testing.T) {
	output := `
Tracing route to example.test [203.0.113.8]
Over a maximum of 30 hops:

  1    <1 ms     3 ms     *        192.0.2.1
  2     *        *        *        Request timed out.
  3    11 ms     9 ms    10 ms     203.0.113.8

Trace complete.
`
	topology, err := parseTraceroute(output, "example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !topology.Reached || len(topology.Nodes) != 4 || len(topology.Links) != 3 {
		t.Fatalf("Windows topology = %+v", topology)
	}
	if node := topology.Nodes[1]; node.Address != "192.0.2.1" || node.Status != "healthy" || node.LatencyMS != 1 {
		t.Fatalf("first Windows hop = %+v", node)
	}
	if node := topology.Nodes[2]; node.Address != "" || node.Status != "unknown" {
		t.Fatalf("timed-out Windows hop = %+v", node)
	}
	if node := topology.Nodes[3]; node.Address != "203.0.113.8" || node.LatencyMS != 9 {
		t.Fatalf("destination Windows hop = %+v", node)
	}
}

func TestParseTracerouteWindowsDirectAddressRetainsSyntheticDestination(t *testing.T) {
	output := "Tracing route to 203.0.113.8\nOver a maximum of 30 hops:\n  1     1 ms     *     2 ms     192.0.2.1\n 30     *        *        *        Request timed out.\n"
	topology, err := parseTraceroute(output, "203.0.113.8")
	if err != nil {
		t.Fatal(err)
	}
	if topology.Reached || len(topology.Nodes) != 4 || topology.Nodes[len(topology.Nodes)-1].Hop != 31 || topology.Nodes[len(topology.Nodes)-1].Status != "failure" {
		t.Fatalf("Windows unreached topology = %+v", topology)
	}
}

func TestParseTraceroutePreservesSubMillisecondLatencySyntax(t *testing.T) {
	output := "traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.1  <1 ms\n2  203.0.113.8  1.0 ms\n"
	topology, err := parseTraceroute(output, "example.test")
	if err != nil {
		t.Fatal(err)
	}
	if topology.Nodes[1].Status != "healthy" || topology.Nodes[1].LatencyMS != 1 {
		t.Fatalf("sub-millisecond node = %+v", topology.Nodes[1])
	}
}

func TestParseTracerouteFindsHeaderAfterWarningAndReachesDestination(t *testing.T) {
	output := "traceroute: warning: multiple addresses found\n" +
		"traceroute to example.test (203.0.113.8), 30 hops max\n" +
		"1  192.0.2.1  1.0 ms\n" +
		"2  203.0.113.8  2.0 ms\n"

	topology, err := parseTraceroute(output, "example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !topology.Reached || len(topology.Nodes) != 3 || topology.Nodes[2].Address != "203.0.113.8" {
		t.Fatalf("warning-prefixed topology = %+v", topology)
	}
}

func TestParseTracerouteRejectsHopOutputWithoutHeader(t *testing.T) {
	output := "traceroute: warning: header unavailable\n1  203.0.113.8  1.0 ms\n"
	if _, err := parseTraceroute(output, "example.test"); err == nil {
		t.Fatal("hop output without a traceroute header was accepted")
	}
}

func TestParseTracerouteRejectsNegativeAndExcessiveFiniteLatency(t *testing.T) {
	for _, latency := range []string{"-0.01", "30000.01", "1.7976931348623157e308"} {
		t.Run(latency, func(t *testing.T) {
			output := "traceroute to example.test (203.0.113.8), 30 hops max\n1  203.0.113.8  " + latency + " ms\n"
			_, err := parseTraceroute(output, "example.test")
			if !errors.Is(err, ErrTraceLatency) {
				t.Fatalf("error = %v, want %v", err, ErrTraceLatency)
			}
		})
	}
}

func TestClassifyTopologyNeverProducesNonFiniteLatencyDelta(t *testing.T) {
	topology := Topology{Nodes: []TopologyNode{
		{ID: "hop-0", Hop: 0, Status: "healthy"},
		{ID: "hop-1", Hop: 1, Status: "healthy", LatencyMS: 1.7976931348623157e308},
		{ID: "hop-2", Hop: 2, Status: "healthy", LatencyMS: -1.7976931348623157e308},
	}}

	classifyTopology(&topology)
	for _, link := range topology.Links {
		if math.IsNaN(link.LatencyDeltaMS) || math.IsInf(link.LatencyDeltaMS, 0) {
			t.Fatalf("non-finite classified link: %+v", link)
		}
	}
}

func TestTracerouteMalformedLatencyCannotBecomeHealthy(t *testing.T) {
	checker := TracerouteChecker{Command: func(context.Context, string, ...string) ([]byte, error) {
		return []byte("traceroute to example.test (203.0.113.8), 30 hops max\n1  203.0.113.8  1e999 ms\n"), nil
	}}
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 1})
	if result.Status == StatusHealthy || result.ErrorCode != "traceroute_failed" {
		t.Fatalf("malformed latency became healthy: %+v", result)
	}
}

func TestTraceroutePassesPlatformCommandSpecToInjectedCommand(t *testing.T) {
	const timeout = 1750 * time.Millisecond
	wantName, wantArgs := traceCommandSpec(timeout, "example.test")
	calls := 0
	checker := TracerouteChecker{Command: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if name != wantName || !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("command = %s %v, want %s %v", name, args, wantName, wantArgs)
		}
		return []byte("traceroute to example.test (203.0.113.8), 30 hops max\n 1  203.0.113.8  21.4 ms\n"), nil
	}}
	result := checker.Check(withAttemptTimeout(context.Background(), timeout), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 1})
	if calls != 1 || result.Status != StatusHealthy {
		t.Fatalf("calls=%d result=%+v", calls, result)
	}
}

func TestTracerouteBuildsHealthyTopology(t *testing.T) {
	checker := TracerouteChecker{Command: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("traceroute to example.test (203.0.113.8), 30 hops max\n 1  192.0.2.1  2.1 ms\n 2  203.0.113.8  21.4 ms\n"), nil
	}}
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test"})
	if result.Status != StatusHealthy {
		t.Fatalf("unexpected result: %+v", result)
	}
	topology := result.Details["topology"].(Topology)
	if !topology.Reached || len(topology.Nodes) != 3 || len(topology.Links) != 2 {
		t.Fatalf("unexpected topology: %+v", topology)
	}
}

func TestTracerouteClassifiesUnknownLatencyJumpAndFailure(t *testing.T) {
	topology, err := parseTraceroute("traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.1  1.2 ms\n2  *\n3  198.51.100.4  80.5 ms", "example.test")
	if err != nil {
		t.Fatal(err)
	}
	if topology.Reached || topology.Nodes[2].Status != "unknown" || topology.Nodes[len(topology.Nodes)-1].Status != "failure" {
		t.Fatalf("unexpected topology: %+v", topology)
	}
	if topologyStatus(topology) != StatusUnreachable {
		t.Fatalf("status = %s", topologyStatus(topology))
	}
}

func TestTracerouteDecreasingHealthyRTTOmitsNonAdditionalLatencyDelta(t *testing.T) {
	topology, err := parseTraceroute("traceroute to 203.0.113.8 (203.0.113.8), 30 hops max\n1  192.0.2.1  20.0 ms\n2  203.0.113.8  5.0 ms", "203.0.113.8")
	if err != nil {
		t.Fatal(err)
	}
	if delta, valid := roundedTraceLatencyDelta(5, 20); !valid || delta != 0 {
		t.Fatalf("decreasing RTT delta = %v, valid = %t; want 0, true", delta, valid)
	}
	if topologyStatus(topology) != StatusHealthy || topology.Nodes[2].Status != "healthy" || topology.Links[1].Status != "healthy" {
		t.Fatalf("decreasing healthy RTT was degraded: %+v", topology)
	}
	encoded, err := json.Marshal(topology.Links)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"from":"hop-0","to":"hop-1","status":"healthy"},{"from":"hop-1","to":"hop-2","status":"healthy"}]`
	if string(encoded) != want {
		t.Fatalf("serialized links = %s, want %s", encoded, want)
	}
}

func TestRoundedTraceLatencyDeltaRequiresBoundedFiniteInputs(t *testing.T) {
	validCases := []struct {
		name              string
		current, previous float64
		want              float64
	}{
		{name: "zero", current: 5, previous: 5, want: 0},
		{name: "rounded positive", current: 80.126, previous: 20, want: 60.13},
		{name: "maximum", current: MaxTraceLatencyMS, previous: 0, want: MaxTraceLatencyMS},
	}
	for _, tt := range validCases {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := roundedTraceLatencyDelta(tt.current, tt.previous)
			if !valid || got != tt.want || got < 0 || got > MaxTraceLatencyMS {
				t.Fatalf("roundedTraceLatencyDelta(%v, %v) = %v, %t; want %v, true", tt.current, tt.previous, got, valid, tt.want)
			}
		})
	}

	invalidCases := []struct {
		name              string
		current, previous float64
	}{
		{name: "negative current", current: -0.01, previous: 0},
		{name: "negative previous", current: 0, previous: -0.01},
		{name: "current above maximum", current: MaxTraceLatencyMS + 0.01, previous: 0},
		{name: "previous above maximum", current: 0, previous: MaxTraceLatencyMS + 0.01},
		{name: "NaN current", current: math.NaN(), previous: 0},
		{name: "positive infinity previous", current: 0, previous: math.Inf(1)},
		{name: "negative infinity current", current: math.Inf(-1), previous: 0},
	}
	for _, tt := range invalidCases {
		t.Run(tt.name, func(t *testing.T) {
			if delta, valid := roundedTraceLatencyDelta(tt.current, tt.previous); valid || delta != 0 {
				t.Fatalf("roundedTraceLatencyDelta(%v, %v) = %v, %t; want 0, false", tt.current, tt.previous, delta, valid)
			}
		})
	}
}

func TestTracerouteMarksPositiveLatencyJumpDegradedAndSerializesRoundedDelta(t *testing.T) {
	topology, err := parseTraceroute("traceroute to 203.0.113.8 (203.0.113.8), 30 hops max\n1  192.0.2.1  20.0 ms\n2  203.0.113.8  80.126 ms", "203.0.113.8")
	if err != nil {
		t.Fatal(err)
	}
	if topology.Nodes[2].Status != "degraded" || topology.Links[1].Status != "degraded" || topologyStatus(topology) != StatusDegraded {
		t.Fatalf("unexpected topology: %+v", topology)
	}
	encoded, err := json.Marshal(topology.Links)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"from":"hop-0","to":"hop-1","status":"healthy"},{"from":"hop-1","to":"hop-2","status":"degraded","latency_delta_ms":60.13}]`
	if string(encoded) != want {
		t.Fatalf("serialized links = %s, want %s", encoded, want)
	}
}

func TestTracerouteAggregatesRepeatedRoutesAndPartialFailure(t *testing.T) {
	outputs := []struct {
		body string
		err  error
	}{
		{body: "traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.1  1.2 ms\n2  203.0.113.8  20.0 ms"},
		{body: "traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.2  1.4 ms\n2  203.0.113.8  24.0 ms"},
		{err: errors.New("timed out")},
	}
	call := 0
	checker := TracerouteChecker{Command: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		output := outputs[call]
		call++
		return []byte(output.body), output.err
	}}
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 3})
	if call != 3 || result.Status != StatusDegraded || result.ErrorCode != "" {
		t.Fatalf("unexpected aggregate: calls=%d result=%+v", call, result)
	}
	if result.Details["attempts_total"] != 3 || result.Details["attempts_reached"] != 2 || result.Details["attempts_failed"] != 1 {
		t.Fatalf("unexpected counters: %+v", result.Details)
	}
	attempts := result.Details["attempts"].([]TraceAttempt)
	if len(attempts) != 3 || attempts[0].Topology.Nodes[1].Address == attempts[1].Topology.Nodes[1].Address || attempts[2].ErrorCode != "traceroute_failed" {
		t.Fatalf("attempt details were not preserved: %+v", attempts)
	}
}

func TestTracerouteDefaultsToFiveAttempts(t *testing.T) {
	calls := 0
	checker := TracerouteChecker{Command: func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte("traceroute to example.test (203.0.113.8), 30 hops max\n1  203.0.113.8  20.0 ms"), nil
	}}
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test"})
	if calls != DefaultTraceAttempts || result.Details["attempts_total"] != DefaultTraceAttempts {
		t.Fatalf("calls=%d attempts_total=%v", calls, result.Details["attempts_total"])
	}
}

func TestTracerouteRejectsUnsafeAddressAndReportsCommandFailure(t *testing.T) {
	checker := TracerouteChecker{Command: func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("not installed")
	}}
	if result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.com; id"}); result.ErrorCode != "invalid_address" {
		t.Fatalf("unsafe address accepted: %+v", result)
	}
	if result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.com"}); result.ErrorCode != "traceroute_failed" {
		t.Fatalf("unexpected command failure: %+v", result)
	}
}

func TestTracerouteGivesEveryAttemptAFreshTimeout(t *testing.T) {
	deadlines := make([]time.Duration, 0, 2)
	checker := TracerouteChecker{Command: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("attempt context has no deadline")
		}
		deadlines = append(deadlines, time.Until(deadline))
		return []byte("traceroute to example.test (203.0.113.8), 30 hops max\n1  203.0.113.8  1.0 ms"), nil
	}}
	runner := NewRunner(checker)
	_, err := runner.Run(context.Background(), Request{
		TimeoutMS: 200,
		Targets:   []Target{{Kind: KindTraceroute, Address: "example.test", Attempts: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deadlines) != 2 {
		t.Fatalf("attempt deadlines=%v", deadlines)
	}
	for i, remaining := range deadlines {
		if remaining < 150*time.Millisecond || remaining > 250*time.Millisecond {
			t.Fatalf("attempt %d received %s instead of a fresh 200ms timeout", i+1, remaining)
		}
	}
}

func TestTracerouteParsablePartialOutputWithCommandErrorsIsNotHealthy(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "deadline", err: context.DeadlineExceeded, code: "timeout"},
		{name: "cancelled", err: context.Canceled, code: "cancelled"},
		{name: "nonzero exit", err: errors.New("exit status 1"), code: "traceroute_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := TracerouteChecker{Command: func(context.Context, string, ...string) ([]byte, error) {
				return []byte("traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.1  1.0 ms\n2  203.0.113.8  5.0 ms"), tt.err
			}}
			result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 1})
			if result.Status == StatusHealthy || result.ErrorCode != tt.code {
				t.Fatalf("command error became healthy: %+v", result)
			}
			attempts, ok := result.Details["attempts"].([]TraceAttempt)
			if !ok || len(attempts) != 1 || attempts[0].Topology == nil || !attempts[0].Topology.Reached || attempts[0].ErrorCode != tt.code {
				t.Fatalf("safe partial topology was not retained: %#v", result.Details["attempts"])
			}
		})
	}
}

func TestTracerouteUnparseableCommandErrorsKeepStableCodes(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		code string
	}{
		{name: "deadline", err: context.DeadlineExceeded, code: "timeout"},
		{name: "cancelled", err: context.Canceled, code: "cancelled"},
		{name: "output limit", err: ErrTraceOutputLimit, code: "traceroute_failed"},
		{name: "exit", err: errors.New("exit status 1"), code: "traceroute_failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			checker := TracerouteChecker{Command: func(context.Context, string, ...string) ([]byte, error) { return nil, tt.err }}
			result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 1})
			if result.Status == StatusHealthy || result.ErrorCode != tt.code {
				t.Fatalf("result = %+v", result)
			}
			attempts := result.Details["attempts"].([]TraceAttempt)
			if attempts[0].ErrorCode != tt.code {
				t.Fatalf("attempt = %+v", attempts[0])
			}
		})
	}
}

func TestTracerouteUsesAttemptContextErrorOverKilledProcessError(t *testing.T) {
	checker := TracerouteChecker{Command: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, errors.New("signal: killed")
	}}
	result := checker.Check(withAttemptTimeout(context.Background(), 10*time.Millisecond), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 1})
	if result.ErrorCode != "timeout" {
		t.Fatalf("result = %+v", result)
	}
}

func TestTracerouteSeparatesExecutionFailureAndUnreachedCounters(t *testing.T) {
	calls := 0
	checker := TracerouteChecker{Command: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("traceroute to example.test (203.0.113.8), 30 hops max\n1  203.0.113.8  1.0 ms"), nil
		}
		<-ctx.Done()
		return nil, errors.New("signal: killed")
	}}
	result := checker.Check(withAttemptTimeout(context.Background(), 10*time.Millisecond), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 2})
	if result.Status != StatusDegraded || result.Details["attempts_reached"] != 1 || result.Details["attempts_unreached"] != 0 || result.Details["attempts_execution_failed"] != 1 || result.Details["attempts_timed_out"] != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestTracerouteMixedExecutionErrorSummaryIsPermutationInvariant(t *testing.T) {
	run := func(errorsInOrder []error) Result {
		index := 0
		checker := TracerouteChecker{Command: func(context.Context, string, ...string) ([]byte, error) {
			err := errorsInOrder[index]
			index++
			return nil, err
		}}
		return checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: len(errorsInOrder)})
	}
	first := run([]error{context.Canceled, context.DeadlineExceeded})
	second := run([]error{context.DeadlineExceeded, context.Canceled})
	if first.ErrorCode != "traceroute_execution_incomplete" || second.ErrorCode != first.ErrorCode {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestTracerouteLatencyIncludesAttemptsAndEnrichment(t *testing.T) {
	checker := TracerouteChecker{Command: func(context.Context, string, ...string) ([]byte, error) {
		time.Sleep(20 * time.Millisecond)
		return []byte("traceroute to example.test (203.0.113.8), 30 hops max\n1  203.0.113.8  1.0 ms"), nil
	}}
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 1})
	if result.LatencyMS < 15 {
		t.Fatalf("LatencyMS=%d was captured before the attempt completed", result.LatencyMS)
	}
}
