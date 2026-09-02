package api

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func TestMarshalFullResponseMatchesEncodingJSONForGeoIPEnrichmentDTOs(t *testing.T) {
	coverage := diagnostic.EnrichmentCoverage{
		Provider: "geoip", Source: diagnostic.EnrichmentSourceMixed, CacheHits: 1, UpstreamFetches: 2, MaxAgeMS: 1234,
		Failures: []diagnostic.EnrichmentFailure{{Kind: diagnostic.GeoIPErrorTimeout, Count: 1, Retryable: true}},
	}
	analysis := diagnostic.Analyze([]diagnostic.Result{{Kind: diagnostic.KindTraceroute, Status: diagnostic.StatusHealthy, Details: map[string]any{
		"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "geoip_provider_failures": 1, "geoip_enrichment": coverage,
	}}}, time.Now().UTC())
	report := diagnostic.Report{Results: []diagnostic.Result{{Kind: diagnostic.KindTraceroute, Details: map[string]any{"geoip_enrichment": coverage}}}, Analysis: &analysis}
	want, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	got, err := marshalFullResponse(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("DTO parity mismatch\n got: %s\nwant: %s", got, want)
	}
	var decoded diagnostic.Report
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("actual full response no longer parses as Report: %v", err)
	}
	if decoded.Analysis == nil || len(decoded.Analysis.Coverage.Enrichment) != 1 {
		t.Fatalf("parsed enrichment missing: %+v", decoded.Analysis)
	}
	compact, err := marshalCompactResponse(report)
	if err != nil {
		t.Fatal(err)
	}
	var compactDecoded diagnostic.Report
	if err := json.Unmarshal(compact, &compactDecoded); err != nil {
		t.Fatalf("actual compact response no longer parses as Report: %v", err)
	}
	if compactDecoded.Analysis == nil || len(compactDecoded.Analysis.Coverage.Enrichment) != 1 {
		t.Fatalf("compact parsed enrichment missing: %+v", compactDecoded.Analysis)
	}
	var detail diagnostic.EnrichmentCoverage
	detailJSON, err := json.Marshal(compactDecoded.Results[0].Details["geoip_enrichment"])
	if err != nil || json.Unmarshal(detailJSON, &detail) != nil || detail.Provider != "geoip" {
		t.Fatalf("compact result detail missing or malformed: %#v (%v)", compactDecoded.Results[0].Details, err)
	}
}

func TestMarshalFullResponseMatchesEncodingJSONForProducerReports(t *testing.T) {
	now := time.Date(2026, 9, 2, 3, 4, 5, 678901234, time.FixedZone("producer", 9*60*60))
	geo := &diagnostic.GeoLocation{City: "Seoul <&> \ufffd", Region: "Seoul", Country: "Korea", CountryCode: "KR", Latitude: 37.5665, Longitude: 126.978}
	asn := &diagnostic.ASNInfo{Number: 64500, Organization: "A&B Networks"}
	topology := &diagnostic.Topology{
		Reached: true,
		Nodes: []diagnostic.TopologyNode{
			{ID: "local", Status: "healthy"},
			{ID: "hop-1", Hop: 1, Address: "203.0.113.8", LatencyMS: 1.25, Status: "healthy", PublicIP: true, Geolocation: geo, ASN: asn},
		},
		Links: []diagnostic.TopologyLink{{From: "local", To: "hop-1", Status: "healthy", LatencyDeltaMS: 1.25}},
	}
	analysis := diagnostic.Analyze([]diagnostic.Result{{
		Kind: diagnostic.KindHTTPS, Address: "https://example.test/<health>", Status: diagnostic.StatusUnreachable,
		StartedAt: now, ErrorCode: "unexpected_status", Details: map[string]any{
			"status_code": int32(503), "expected_status": uint16(200), "certificate_expires_at": now.Add(-time.Hour),
		},
	}}, now)
	latencyAverage := 2.5
	reports := []diagnostic.Report{
		{},
		{
			ID: "compact", Results: []diagnostic.Result{},
			CompactTopology: &diagnostic.CompactTopology{
				Schema: diagnostic.CompactTopologySchemaV1, Selection: diagnostic.CompactTopologySelectionV1,
				Nodes: []diagnostic.CompactTopologyNode{{ID: "n", Kind: diagnostic.CompactNodeIP, Status: "healthy", LatencyMSAvg: &latencyAverage, Observations: 1}},
				Links: []diagnostic.CompactTopologyLink{}, Routes: []diagnostic.CompactTopologyRoute{}, Truncated: false,
			},
		},
		{
			ID: "report-<&>\xff", Status: diagnostic.StatusDegraded, StartedAt: now, DurationMS: 42,
			Summary: diagnostic.Summary{Total: 1, Failed: 1}, Analysis: &analysis,
			Results: []diagnostic.Result{{
				Kind: diagnostic.KindTraceroute, Address: "example.test", Status: diagnostic.StatusDegraded,
				LatencyMS: 12, StartedAt: now, Message: "line\n\t<&>", Details: map[string]any{
					"attempts": []diagnostic.TraceAttempt{{Attempt: 1, Status: diagnostic.StatusHealthy, Topology: topology}},
					"topology": *topology, "geo": geo, "asn": asn,
					"ints":    []any{int(-1), int8(-2), int16(-3), int32(-4), int64(-5), uint(1), uint8(2), uint16(3), uint32(4), uint64(5)},
					"float32": float32(1.5), "float64": 1.23456789012345, "nil": nil, "bool": true,
					"uintptr": uintptr(123), "bytes": []byte{0, 1, 2, 253, 254, 255},
				},
			}},
		},
	}

	fixture, err := os.ReadFile("../../testdata/maximum-analysis-report.json")
	if err != nil {
		t.Fatal(err)
	}
	var maximum diagnostic.Report
	if err := json.Unmarshal(fixture, &maximum); err != nil {
		t.Fatal(err)
	}
	reports = append(reports, maximum)

	for index, report := range reports {
		want, err := json.Marshal(report)
		if err != nil {
			t.Fatalf("fixture %d reference marshal: %v", index, err)
		}
		want = append(want, '\n')
		got, err := marshalFullResponse(report)
		if err != nil {
			t.Fatalf("fixture %d: %v", index, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("fixture %d mismatch\n got: %s\nwant: %s", index, got, want)
		}
	}
}

func TestMarshalFullResponseRandomClosedDetailsMatchEncodingJSON(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5eed))
	for iteration := 0; iteration < 300; iteration++ {
		report := diagnostic.Report{
			ID: "random", Status: diagnostic.StatusHealthy,
			StartedAt: time.Date(2000+rng.Intn(40), time.Month(1+rng.Intn(12)), 1+rng.Intn(27), rng.Intn(24), rng.Intn(60), rng.Intn(60), rng.Intn(1e9), time.UTC),
			Results:   []diagnostic.Result{{Kind: diagnostic.KindDNS, Status: diagnostic.StatusHealthy, Details: randomClosedValue(rng, 0).(map[string]any)}},
		}
		want, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, '\n')
		got, err := marshalFullResponse(report)
		if err != nil {
			t.Fatalf("iteration %d: %v; details=%#v", iteration, err, report.Results[0].Details)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("iteration %d mismatch\n got: %s\nwant: %s", iteration, got, want)
		}
	}
}

func randomClosedValue(rng *rand.Rand, depth int) any {
	if depth >= 3 {
		switch rng.Intn(8) {
		case 0:
			return nil
		case 1:
			return rng.Intn(2) == 0
		case 2:
			return string([]byte{'<', '&', byte(rng.Intn(256)), '>'})
		case 3:
			return int8(rng.Intn(255) - 127)
		case 4:
			return uint32(rng.Uint32())
		case 5:
			return int64(rng.Uint64())
		case 6:
			return math.Float64frombits(rng.Uint64() &^ (uint64(0x7ff) << 52))
		default:
			return time.Unix(rng.Int63n(2_000_000_000), int64(rng.Intn(1e9))).UTC()
		}
	}
	result := make(map[string]any, 1+rng.Intn(4))
	for len(result) < capForRandomMap(result) {
		key := string([]byte{'k', byte(rng.Intn(20)), '<', byte(rng.Intn(256))})
		if rng.Intn(3) == 0 {
			values := make([]any, rng.Intn(4))
			for i := range values {
				values[i] = randomClosedValue(rng, depth+1)
			}
			result[key] = values
		} else {
			result[key] = randomClosedValue(rng, depth+1)
		}
	}
	return result
}

func capForRandomMap(value map[string]any) int {
	// The initial map capacity is not observable, so use a stable small target.
	return 3
}

func TestMarshalFullResponseAcceptsExactResponseLimitAndRejectsLimitPlusOne(t *testing.T) {
	const integerCount = 120_000
	chunks := make([][]uint64, 0, integerCount/maxFullResponseContainerElements+1)
	remaining := integerCount
	for remaining > 0 {
		count := min(remaining, maxFullResponseContainerElements)
		chunk := make([]uint64, count)
		for index := range chunk {
			chunk[index] = math.MaxUint64
		}
		chunks = append(chunks, chunk)
		remaining -= count
	}
	report := diagnostic.Report{Results: []diagnostic.Result{{Details: map[string]any{"numbers": chunks, "payload": ""}}}}
	base, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	encodedStringBytes := maxFullResponseBytes - (len(base) + 1)
	if encodedStringBytes <= 0 || encodedStringBytes > 6*maxFullResponseStringBytes {
		t.Fatalf("fixture cannot reach exact limit: base=%d remaining=%d", len(base)+1, encodedStringBytes)
	}
	nulls, ascii := encodedStringBytes/6, encodedStringBytes%6
	padding := strings.Repeat("\x00", nulls) + strings.Repeat("a", ascii)
	report.Results[0].Details["payload"] = padding

	body, err := marshalFullResponse(report)
	if err != nil {
		t.Fatalf("exact limit: %v", err)
	}
	if len(body) != maxFullResponseBytes || cap(body) != maxFullResponseBytes {
		t.Fatalf("exact response len=%d cap=%d, want %d", len(body), cap(body), maxFullResponseBytes)
	}

	report.Results[0].Details["payload"] = padding + "a"
	body, err = marshalFullResponse(report)
	if !errors.Is(err, errFullResponseTooLarge) {
		t.Fatalf("limit+1 error = %v", err)
	}
	if body != nil {
		t.Fatalf("limit+1 retained len=%d cap=%d", len(body), cap(body))
	}
}

func TestLimitedJSONBufferAcceptsExactLimitAndRejectsLimitPlusOne(t *testing.T) {
	buffer := newLimitedJSONBuffer(17)
	if err := buffer.writeString(strings.Repeat("x", 17)); err != nil {
		t.Fatalf("exact limit: %v", err)
	}
	if got := len(buffer.bytes()); got != 17 {
		t.Fatalf("length = %d", got)
	}
	if err := buffer.writeByte('x'); !errors.Is(err, errFullResponseTooLarge) {
		t.Fatalf("limit+1 error = %v", err)
	}
	if got := cap(buffer.bytes()); got > 17 {
		t.Fatalf("buffer capacity = %d, limit 17", got)
	}
}

func TestMarshalFullResponseRejectsAmplifiedGeoWithoutAmplifiedAllocation(t *testing.T) {
	largeSharedString := strings.Repeat("g", 256<<10)
	geo := &diagnostic.GeoLocation{City: largeSharedString, Latitude: 1, Longitude: 2}
	nodes := make([]diagnostic.TopologyNode, 200)
	for i := range nodes {
		nodes[i] = diagnostic.TopologyNode{ID: "node", Status: "healthy", Geolocation: geo}
	}
	report := diagnostic.Report{Results: []diagnostic.Result{{Details: map[string]any{
		"topology": diagnostic.Topology{Nodes: nodes},
	}}}}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	body, err := marshalFullResponse(report)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, errFullResponseStringLimit) {
		t.Fatalf("error = %v, body bytes = %d", err, len(body))
	}
	if body != nil {
		t.Fatalf("error returned retained body with len=%d cap=%d", len(body), cap(body))
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("amplified rejection allocated %d bytes", allocated)
	}
}

type invokingMarshaler struct{ calls *int }

func (m invokingMarshaler) MarshalJSON() ([]byte, error) {
	*m.calls++
	return []byte(`"invoked"`), nil
}

func TestMarshalFullResponseRejectsCustomMarshalerWithoutInvocation(t *testing.T) {
	calls := 0
	_, err := marshalFullResponse(diagnostic.Report{Results: []diagnostic.Result{{Details: map[string]any{"evil": invokingMarshaler{calls: &calls}}}}})
	if !errors.Is(err, errFullResponseUnsupported) {
		t.Fatalf("error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("MarshalJSON invoked %d times", calls)
	}
}

type panickingCompactMarshaler struct{ calls *int }

func (m panickingCompactMarshaler) MarshalJSON() ([]byte, error) {
	*m.calls++
	panic("COMPACT_MARSHAL_PANIC_CANARY")
}

func TestMarshalCompactResponseRejectsCustomMarshalersWithoutInvocation(t *testing.T) {
	calls := 0
	report := diagnostic.Report{Results: []diagnostic.Result{{Kind: diagnostic.KindDNS, Details: map[string]any{
		"evil": panickingCompactMarshaler{calls: &calls},
	}}}}
	body, err := marshalCompactResponse(report)
	if !errors.Is(err, errResponseSerialization) || !errors.Is(err, errFullResponseUnsupported) {
		t.Fatalf("error=%v body=%q", err, body)
	}
	if calls != 0 {
		t.Fatalf("MarshalJSON invoked %d times", calls)
	}
}

func TestMarshalCompactResponseRejectsTwoMiBDetailWithoutWholeValueAllocation(t *testing.T) {
	large := strings.Repeat("x", 2<<20)
	report := diagnostic.Report{Results: []diagnostic.Result{{Kind: diagnostic.KindDNS, Details: map[string]any{"large": large}}}}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	body, err := marshalCompactResponse(report)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, errCompactResponseTooLarge) || body != nil {
		t.Fatalf("error=%v body bytes=%d", err, len(body))
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("2 MiB compact rejection allocated %d bytes", allocated)
	}
}

func TestMarshalCompactResponseExactExclusiveBoundaryAndRollback(t *testing.T) {
	numbers := make([]uint64, 5_000)
	for index := range numbers {
		numbers[index] = math.MaxUint64
	}
	report := diagnostic.Report{Results: []diagnostic.Result{{Kind: diagnostic.KindDNS, Details: map[string]any{
		"numbers": numbers,
		"padding": "",
	}}}}
	topology := diagnostic.CloneCompactTopology(diagnostic.BuildCompactTopology(report))
	setCompactTruncationReasons(topology, false, false)
	base, err := json.Marshal(diagnostic.BuildCompactReport(report, topology))
	if err != nil {
		t.Fatal(err)
	}
	paddingBytes := diagnostic.CompactTopologyMaxResponseBytes - 2 - len(base)
	if paddingBytes <= 0 || paddingBytes >= maxFullResponseStringBytes {
		t.Fatalf("fixture padding=%d base=%d", paddingBytes, len(base))
	}
	report.Results[0].Details["padding"] = strings.Repeat("p", paddingBytes)

	body, err := marshalCompactResponse(report)
	if err != nil {
		t.Fatalf("exact boundary: %v", err)
	}
	if len(body) != diagnostic.CompactTopologyMaxResponseBytes-1 || body[len(body)-1] != '\n' {
		t.Fatalf("exact body bytes=%d", len(body))
	}

	report.Results[0].Details["padding"] = report.Results[0].Details["padding"].(string) + "p"
	body, err = marshalCompactResponse(report)
	if !errors.Is(err, errCompactResponseTooLarge) || body != nil {
		t.Fatalf("boundary+1 error=%v body bytes=%d", err, len(body))
	}
}

func TestMarshalCompactResponseMatchesEncodingJSONAndRejectsNonFiniteDetails(t *testing.T) {
	report := diagnostic.Report{ID: "reference", Results: []diagnostic.Result{{Kind: diagnostic.KindDNS, Details: map[string]any{"b": 2, "a": "x"}}}}
	topology := diagnostic.CloneCompactTopology(diagnostic.BuildCompactTopology(report))
	setCompactTruncationReasons(topology, false, false)
	want, err := json.Marshal(diagnostic.BuildCompactReport(report, topology))
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	got, err := marshalCompactResponse(report)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("error=%v\n got=%s\nwant=%s", err, got, want)
	}

	report.Results[0].Details["bad"] = math.Inf(1)
	got, err = marshalCompactResponse(report)
	if !errors.Is(err, errResponseSerialization) || !errors.Is(err, errFullResponseNonFinite) || got != nil {
		t.Fatalf("nonfinite error=%v body=%q", err, got)
	}
}

var textMarshalerCalls int

type invokingTextString string

func (invokingTextString) MarshalText() ([]byte, error) {
	textMarshalerCalls++
	panic("MarshalText must not be invoked")
}

type invokingTextBytes []byte

func (*invokingTextBytes) MarshalText() ([]byte, error) {
	textMarshalerCalls++
	panic("MarshalText must not be invoked")
}

var (
	_ encoding.TextMarshaler = invokingTextString("")
	_ encoding.TextMarshaler = (*invokingTextBytes)(nil)
)

func TestMarshalFullResponseRejectsTextMarshalersWithoutInvocation(t *testing.T) {
	bytesValue := invokingTextBytes("bytes")
	tests := []struct {
		name  string
		value any
	}{
		{name: "named string value receiver", value: invokingTextString("string")},
		{name: "named string map key", value: map[invokingTextString]any{invokingTextString("key"): true}},
		{name: "named byte slice pointer receiver on value", value: bytesValue},
		{name: "named byte slice pointer receiver on pointer", value: &bytesValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			textMarshalerCalls = 0
			body, err := marshalFullResponse(diagnostic.Report{Results: []diagnostic.Result{{Details: map[string]any{"evil": test.value}}}})
			if !errors.Is(err, errFullResponseUnsupported) {
				t.Fatalf("error = %v", err)
			}
			if body != nil {
				t.Fatalf("body = %q", body)
			}
			if textMarshalerCalls != 0 {
				t.Fatalf("MarshalText invoked %d times", textMarshalerCalls)
			}
		})
	}
}

func TestMarshalFullResponseRejectsCyclesUnsupportedValuesAndNonFiniteFloats(t *testing.T) {
	cycleMap := map[string]any{}
	cycleMap["self"] = cycleMap
	cycleSlice := make([]any, 1)
	cycleSlice[0] = cycleSlice
	tests := []struct {
		name  string
		value any
		want  error
	}{
		{"map cycle", cycleMap, errFullResponseCycle},
		{"slice cycle", cycleSlice, errFullResponseCycle},
		{"pointer", new(int), errFullResponseUnsupported},
		{"channel", make(chan int), errFullResponseUnsupported},
		{"function", func() {}, errFullResponseUnsupported},
		{"complex", complex(1, 2), errFullResponseUnsupported},
		{"unsupported struct", struct{ Value string }{"x"}, errFullResponseUnsupported},
		{"non-string map", map[int]any{1: "x"}, errFullResponseUnsupported},
		{"positive infinity", math.Inf(1), errFullResponseNonFinite},
		{"negative infinity float32", float32(math.Inf(-1)), errFullResponseNonFinite},
		{"nan", math.NaN(), errFullResponseNonFinite},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := marshalFullResponse(diagnostic.Report{Results: []diagnostic.Result{{Details: map[string]any{"value": test.value}}}})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if body != nil {
				t.Fatalf("body = %q", body)
			}
		})
	}
}

func TestMarshalFullResponseRejectsDepthContainersNodesAndStrings(t *testing.T) {
	deep := any("leaf")
	for range maxFullResponseDepth + 1 {
		deep = []any{deep}
	}
	manyContainers := make([]any, 33)
	for outer := range manyContainers {
		inner := make([]any, 1_000)
		for index := range inner {
			inner[index] = []any{}
		}
		manyContainers[outer] = inner
	}
	manyNodes := make([]any, 0, maxFullResponseNodes/maxFullResponseContainerElements+1)
	remainingNodes := maxFullResponseNodes + 1
	for remainingNodes > 0 {
		count := min(remainingNodes, maxFullResponseContainerElements)
		manyNodes = append(manyNodes, make([]any, count))
		remainingNodes -= count
	}
	tests := []struct {
		name  string
		value any
		want  error
	}{
		{"depth", deep, errFullResponseDepthLimit},
		{"containers", manyContainers, errFullResponseContainerLimit},
		{"nodes", manyNodes, errFullResponseNodeLimit},
		{"one string", strings.Repeat("s", maxFullResponseStringBytes+1), errFullResponseStringLimit},
		{"aggregate strings", []any{strings.Repeat("a", maxFullResponseStringBytes/2+1), strings.Repeat("b", maxFullResponseStringBytes/2+1)}, errFullResponseStringLimit},
		{"oversized map key", map[string]any{strings.Repeat("k", maxFullResponseStringBytes+1): true}, errFullResponseStringLimit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := marshalFullResponse(diagnostic.Report{Results: []diagnostic.Result{{Details: map[string]any{"value": test.value}}}})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}
