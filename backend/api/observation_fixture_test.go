package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

type observationFixtureTransport struct{}

func (observationFixtureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true,"connection":{"asn":15169},"latitude":null,"longitude":null}`))}, nil
}

func TestObservationIntegrityFixtureMatchesProducer(t *testing.T) {
	outputs := []string{
		"traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1 8.8.8.8 100 ms\n",
		"traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1 9.9.9.9 0 ms\n2 *\n",
	}
	calls := 0
	geo := diagnostic.NewIPWhoIsLookup(&http.Client{Transport: observationFixtureTransport{}}, "https://geo.example/")
	checker := diagnostic.NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
		output := outputs[calls]
		calls++
		return []byte(output), nil
	}, geo, nil)
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Shutdown(context.Background())
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, checker)
	report, err := runner.RunWithID(context.Background(), "abababababababababababab", diagnostic.Request{Targets: []diagnostic.Target{{Kind: diagnostic.KindTraceroute, Address: "8.8.8.8", Attempts: 2}}, TopologyMode: diagnostic.TopologyModeCompact})
	if err != nil {
		t.Fatal(err)
	}
	normalizeFixtureReport(&report, 10)
	// FetchedAt is a provider clock; freeze derived age without bypassing the
	// actual GeoIP decoder, cache, checker, Runner or compact serializer.
	for i := range report.Analysis.Coverage.Enrichment {
		report.Analysis.Coverage.Enrichment[i].MaxAgeMS = 0
	}
	for i := range report.Results {
		if value, ok := report.Results[i].Details["geoip_enrichment"].(diagnostic.EnrichmentCoverage); ok {
			value.MaxAgeMS = 0
			report.Results[i].Details["geoip_enrichment"] = value
		}
	}
	for _, node := range report.CompactTopology.Nodes {
		if node.Geolocation != nil {
			t.Fatalf("missing coordinates became a map point: %+v", node)
		}
		if node.Address == "8.8.8.8" && (node.LatencyMSAvg == nil || *node.LatencyMSAvg != 100) {
			t.Fatalf("synthetic destination diluted measured RTT: %+v", node)
		}
		if node.Address == "9.9.9.9" && (node.LatencyMSAvg == nil || *node.LatencyMSAvg != 0) {
			t.Fatalf("explicit observed zero RTT lost: %+v", node)
		}
		if node.Kind == diagnostic.CompactNodeUnknown && node.LatencyMSAvg != nil {
			t.Fatalf("unknown hop invented RTT: %+v", node)
		}
	}
	generated, err := marshalCompactResponse(report)
	if err != nil {
		t.Fatal(err)
	}
	fullReport := report
	fullReport.CompactTopology = nil
	full, err := marshalFullResponse(fullReport)
	if err != nil {
		t.Fatal(err)
	}
	for mode, payload := range map[string][]byte{"full": full, "compact": generated} {
		path := "../../testdata/" + mode + "-observation-integrity-report.json"
		if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
			if err := os.WriteFile(path, payload, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		committed, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(committed, payload) {
			t.Fatalf("%s observation-integrity fixture differs from real producer bytes", mode)
		}
	}
}
