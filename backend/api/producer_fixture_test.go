package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

var fixtureTime = time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

type fixtureGeoIPLookup func(net.IP) (diagnostic.IPMetadata, error)

func (lookup fixtureGeoIPLookup) Lookup(_ context.Context, ip net.IP) (diagnostic.IPMetadata, error) {
	return lookup(ip)
}

type fixtureResultChecker struct {
	kind   diagnostic.Kind
	result diagnostic.Result
}

func (checker fixtureResultChecker) Kind() diagnostic.Kind {
	if checker.kind == "" {
		return diagnostic.KindTraceroute
	}
	return checker.kind
}

func (checker fixtureResultChecker) Check(context.Context, diagnostic.Target) diagnostic.Result {
	return checker.result
}

type fixturePanicChecker struct {
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (*fixturePanicChecker) Kind() diagnostic.Kind { return diagnostic.KindTraceroute }

func (checker *fixturePanicChecker) Check(context.Context, diagnostic.Target) diagnostic.Result {
	checker.once.Do(func() { close(checker.entered) })
	<-checker.release
	panic("fixture checker panic")
}

func TestCommittedPresentationContractMatchesGoProducerBytes(t *testing.T) {
	got := presentationFixtureBytes(t)
	for run := 1; run < 100; run++ {
		if next := presentationFixtureBytes(t); !bytes.Equal(got, next) {
			t.Fatalf("presentation contract generation %d changed bytes", run)
		}
	}
	if len(got) == 0 || got[len(got)-1] != '\n' || !json.Valid(bytes.TrimSuffix(got, []byte{'\n'})) {
		t.Fatalf("presentation contract is not canonical newline-terminated JSON")
	}
	for _, forbidden := range [][]byte{[]byte("HOSTILE_"), []byte("POISON_"), []byte("PRIVATE_"), []byte("CANARY")} {
		if bytes.Contains(got, forbidden) {
			t.Fatalf("presentation contract contains hostile prose marker %q", forbidden)
		}
	}

	var contract diagnostic.PresentationContract
	if err := json.Unmarshal(got, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.SemanticKeys) != 6 || len(contract.EvidenceSignals) != 10 || len(contract.CoverageSignals) != 21 || len(contract.ActionRelationships) != 23 || len(contract.Scenarios) != 33 {
		t.Fatalf("presentation contract counts = semantics %d evidence %d coverage %d actions %d scenarios %d", len(contract.SemanticKeys), len(contract.EvidenceSignals), len(contract.CoverageSignals), len(contract.ActionRelationships), len(contract.Scenarios))
	}
	evidenceSignals := make([]string, len(contract.EvidenceSignals))
	for index, signal := range contract.EvidenceSignals {
		evidenceSignals[index] = signal.Signal
	}
	actionKeys := make([]string, len(contract.ActionRelationships))
	for index, relationship := range contract.ActionRelationships {
		actionKeys[index] = relationship.FindingPresentationKey
	}
	primary := 0
	names := make([]string, len(contract.Scenarios))
	for index, scenario := range contract.Scenarios {
		names[index] = scenario.Name
		if scenario.Purpose == diagnostic.PresentationPurposeFinding {
			primary++
		}
	}
	if primary != 23 || !sort.StringsAreSorted(names) || !sort.StringsAreSorted(evidenceSignals) || !sort.StringsAreSorted(contract.CoverageSignals) || !sort.StringsAreSorted(actionKeys) {
		t.Fatalf("presentation registries/scenarios are not exact and sorted: primary=%d names=%v evidence=%v coverage=%v actions=%v", primary, names, evidenceSignals, contract.CoverageSignals, actionKeys)
	}
	assertPresentationScenarioStatuses(t, contract)
	assertNilCapabilityPresentationScenario(t, contract)

	path := filepath.Join("..", "..", "testdata", "presentation-contract.json")
	if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("update presentation contract: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed presentation contract: %v", err)
	}
	if err := requireFreshFixtureBytes(want, got); err != nil {
		t.Fatal(err)
	}
}

func assertNilCapabilityPresentationScenario(t *testing.T, contract diagnostic.PresentationContract) {
	t.Helper()
	for _, scenario := range contract.Scenarios {
		if scenario.Name != "finding.traceroute_unavailable" {
			continue
		}
		if scenario.Purpose != diagnostic.PresentationPurposeFinding || scenario.Report.Status != diagnostic.StatusUnreachable ||
			scenario.Report.Analysis == nil || scenario.Report.Analysis.Verdict != diagnostic.VerdictInconclusive || len(scenario.Report.Results) != 1 {
			t.Fatalf("nil-capability report truth drifted: %+v", scenario)
		}
		result := scenario.Report.Results[0]
		if result.Kind != diagnostic.KindTraceroute || result.Status != diagnostic.StatusUnreachable || result.ErrorCode != "traceroute_unavailable" || result.Details != nil {
			t.Fatalf("nil-capability result shape drifted: %+v", result)
		}
		if len(scenario.Findings) != 1 || scenario.Findings[0].Code != diagnostic.FindingTracerouteUnavailable ||
			scenario.Findings[0].PresentationKey != "finding.traceroute_unavailable" || scenario.Findings[0].ActionRelationship != "action.traceroute_unavailable" ||
			!reflect.DeepEqual(scenario.Findings[0].Evidence, []diagnostic.PresentationEvidenceSignal{{Signal: "error_code", ObservedShape: diagnostic.EvidenceShapeClosedErrorCode, ExpectedShape: diagnostic.EvidenceShapeNone}}) ||
			len(scenario.Findings[0].CoverageSignals) != 0 {
			t.Fatalf("nil-capability presentation relationship drifted: %+v", scenario.Findings)
		}
		if len(scenario.Report.Analysis.Evidence) != 1 || scenario.Report.Analysis.Evidence[0].Signal != "error_code" || scenario.Report.Analysis.Evidence[0].Observed != "traceroute_unavailable" {
			t.Fatalf("nil-capability evidence drifted: %+v", scenario.Report.Analysis.Evidence)
		}
		return
	}
	t.Fatal("real nil-capability presentation scenario is missing")
}

func TestPresentationContractFreshnessRejectsOneByteMutation(t *testing.T) {
	generated := presentationFixtureBytes(t)
	mutated := append([]byte(nil), generated...)
	mutated[len(mutated)/2] ^= 1
	if err := requireFreshFixtureBytes(mutated, generated); err == nil {
		t.Fatal("one-byte stale presentation fixture mutation was accepted")
	}
}

func TestCommittedFindingContractMatchesGoProducerBytes(t *testing.T) {
	got, err := diagnostic.MarshalFindingContract()
	if err != nil {
		t.Fatalf("marshal finding contract: %v", err)
	}
	path := filepath.Join("..", "..", "testdata", "finding-contract.json")
	if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("update finding contract: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed finding contract: %v", err)
	}
	if err := requireFreshFixtureBytes(want, got); err != nil {
		t.Fatal(err)
	}
}

func TestFindingContractFreshnessRejectsOneByteMutation(t *testing.T) {
	generated, err := diagnostic.MarshalFindingContract()
	if err != nil {
		t.Fatal(err)
	}
	mutated := append([]byte(nil), generated...)
	mutated[len(mutated)/2] ^= 1
	if err := requireFreshFixtureBytes(mutated, generated); err == nil {
		t.Fatal("one-byte stale fixture mutation was accepted")
	}
}

func TestCommittedCompactCanonicalFixturesMatchGoProducerBytes(t *testing.T) {
	fixtures := []struct {
		name   string
		report func(*testing.T) diagnostic.Report
	}{
		{name: "compact-asn-context-report.json", report: compactASNContextFixtureReport},
		{name: "compact-zero-route-report.json", report: compactZeroRouteFixtureReport},
		{name: "compact-zero-node-attempt-report.json", report: compactZeroNodeAttemptFixtureReport},
		{name: "compact-empty-asn-report.json", report: compactEmptyASNFixtureReport},
		{name: "compact-geo-exact-boundary-report.json", report: compactGeoExactBoundaryFixtureReport},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			got, err := marshalCompactResponse(fixture.report(t))
			if err != nil {
				t.Fatalf("marshal compact Go-produced fixture: %v", err)
			}
			if bytes.Contains(got, []byte(`"asn":{}`)) {
				t.Fatalf("fixture emitted non-canonical empty ASN: %s", got)
			}
			path := filepath.Join("..", "..", "testdata", fixture.name)
			if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("update fixture: %v", err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed fixture: %v", err)
			}
			if err := requireFreshFixtureBytes(want, got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCompactGeoBoundaryProducerInputSplitsExactFromCapPlusOne(t *testing.T) {
	exactReport := compactGeoExactBoundaryFixtureReport(t)
	exact := diagnostic.BuildCompactTopologyWithOptions(exactReport, -1, true).Topology
	if exact == nil || exact.Geo.Included != 1 || exact.Geo.Omitted != 0 {
		t.Fatalf("exact-boundary Geo stats = %+v", exact.Geo)
	}
	exactNode := exact.Nodes[len(exact.Nodes)-1]
	with, err := json.Marshal(exactNode)
	if err != nil {
		t.Fatal(err)
	}
	exactNode.Geolocation, exactNode.ASN = nil, nil
	without, err := json.Marshal(exactNode)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(with) - len(without); got != diagnostic.CompactTopologyMaxGeoBundleBytes {
		t.Fatalf("exact producer Geo bundle bytes = %d, want %d", got, diagnostic.CompactTopologyMaxGeoBundleBytes)
	}

	overReport := compactGeoBoundaryFixtureReport(t, compactGeoBoundaryOrganization()+"x")
	over := diagnostic.BuildCompactTopologyWithOptions(overReport, -1, true).Topology
	if over == nil || over.Geo.Included != 0 || over.Geo.Omitted != 1 || !over.Truncated ||
		!reflect.DeepEqual(over.TruncationReasons, []diagnostic.CompactTruncationReason{diagnostic.CompactTruncationGeoLimit}) {
		t.Fatalf("cap+1 producer input was not omitted/truncated: topology=%+v", over)
	}
	for index, node := range over.Nodes {
		if node.Geolocation != nil || node.ASN != nil {
			t.Fatalf("cap+1 producer input emitted metadata on node %d", index)
		}
	}
	body, err := marshalCompactResponse(overReport)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(compactGeoBoundaryOrganization()+"x")) {
		t.Fatal("cap+1 producer input escaped into valid compact wire output")
	}
}

func requireFreshFixtureBytes(committed, generated []byte) error {
	if !bytes.Equal(committed, generated) {
		return fmt.Errorf("committed fixture is stale: Go-produced bytes differ")
	}
	return nil
}

func TestCommittedConsumerFixturesMatchGoProducerBytes(t *testing.T) {
	fixtures := []struct {
		name   string
		report func(*testing.T) diagnostic.Report
	}{
		{name: "checker-execution-report.json", report: checkerExecutionFixtureReport},
		{name: "enrichment-upstream-report.json", report: func(t *testing.T) diagnostic.Report {
			return enrichmentFixtureReport(t, "111111111111111111111111", []string{"8.8.8.8", "1.1.1.1"}, fixtureGeoIPLookup(func(ip net.IP) (diagnostic.IPMetadata, error) {
				return fixtureMetadata(ip, diagnostic.GeoIPSourceUpstream), nil
			}), 12)
		}},
		{name: "enrichment-cache-report.json", report: func(t *testing.T) diagnostic.Report {
			return enrichmentFixtureReport(t, "222222222222222222222222", []string{"9.9.9.9", "208.67.222.222", "8.8.4.4"}, fixtureGeoIPLookup(func(ip net.IP) (diagnostic.IPMetadata, error) {
				return fixtureMetadata(ip, diagnostic.GeoIPSourceCache), nil
			}), 60_000)
		}},
		{name: "enrichment-mixed-report.json", report: func(t *testing.T) diagnostic.Report {
			return enrichmentFixtureReport(t, "333333333333333333333333", []string{"1.0.0.1", "64.6.64.6", "94.140.14.14"}, fixtureGeoIPLookup(func(ip net.IP) (diagnostic.IPMetadata, error) {
				source := diagnostic.GeoIPSourceUpstream
				if ip.String() != "94.140.14.14" {
					source = diagnostic.GeoIPSourceCache
				}
				return fixtureMetadata(ip, source), nil
			}), 42_000)
		}},
		{name: "enrichment-failures-report.json", report: func(t *testing.T) diagnostic.Report {
			kinds := map[string]diagnostic.GeoIPErrorKind{
				"8.8.8.8":        diagnostic.GeoIPErrorBusy,
				"1.1.1.1":        diagnostic.GeoIPErrorCancelled,
				"8.8.4.4":        diagnostic.GeoIPErrorCancelled,
				"9.9.9.9":        diagnostic.GeoIPErrorMalformed,
				"208.67.222.222": diagnostic.GeoIPErrorNotFound,
				"1.0.0.1":        diagnostic.GeoIPErrorPolicy,
				"64.6.64.6":      diagnostic.GeoIPErrorRateLimited,
				"94.140.14.14":   diagnostic.GeoIPErrorTimeout,
				"76.76.2.0":      diagnostic.GeoIPErrorUnavailable,
			}
			ips := []string{"8.8.8.8", "1.1.1.1", "8.8.4.4", "9.9.9.9", "208.67.222.222", "1.0.0.1", "64.6.64.6", "94.140.14.14", "76.76.2.0"}
			return enrichmentFixtureReport(t, "444444444444444444444444", ips, fixtureGeoIPLookup(func(ip net.IP) (diagnostic.IPMetadata, error) {
				kind, ok := kinds[ip.String()]
				if !ok {
					t.Fatalf("unexpected GeoIP lookup for %s", ip)
				}
				return diagnostic.IPMetadata{}, fixtureGeoIPError(kind)
			}), 0)
		}},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			got, err := marshalFullResponse(fixture.report(t))
			if err != nil {
				t.Fatalf("marshal Go-produced fixture: %v", err)
			}
			path := filepath.Join("..", "..", "testdata", fixture.name)
			if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("update fixture: %v", err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed fixture: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("committed fixture is stale: Go-produced bytes differ (-want +got)\nwant: %s\n got: %s", want, got)
			}
		})
	}
}

func checkerExecutionFixtureReport(t *testing.T) diagnostic.Report {
	t.Helper()
	// With one P the runner finishes both synchronous lease acquisitions before
	// the newly started checker goroutine can run, making target two's rejection
	// independent of host scheduling. The admitted checker is then released to
	// panic only after the test observes that it started.
	previousProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	checker := &fixturePanicChecker{entered: entered, release: release}
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, checker)
	reportChannel := make(chan diagnostic.Report, 1)
	errorChannel := make(chan error, 1)
	go func() {
		report, runErr := runner.RunWithID(context.Background(), "0123456789abcdef01234567", diagnostic.Request{Targets: []diagnostic.Target{
			{Kind: diagnostic.KindTraceroute, Address: "panic-fixture.invalid", Attempts: 1},
			{Kind: diagnostic.KindTraceroute, Address: "capacity-fixture.invalid", Attempts: 1},
		}})
		reportChannel <- report
		errorChannel <- runErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("panic checker was not admitted")
	}
	close(release)
	report := <-reportChannel
	if err := <-errorChannel; err != nil {
		t.Fatal(err)
	}
	normalizeFixtureReport(&report, 25)
	if got := []string{report.Results[0].ErrorCode, report.Results[1].ErrorCode}; !reflect.DeepEqual(got, []string{"checker_panic", "checker_capacity_unavailable"}) {
		t.Fatalf("checker execution results = %v", got)
	}
	return report
}

// Synthetic observed trace, never a live ASN lookup. Production runner,
// analysis and compact encoder generate the exact consumer fixture bytes.
func compactASNContextFixtureReport(t *testing.T) diagnostic.Report {
	t.Helper()
	topology := &diagnostic.Topology{Reached: true}
	for index, address := range []string{"8.8.8.8", "10.1.2.3", "192.168.2.3", "8.8.4.4"} {
		node := diagnostic.TopologyNode{ID: fmt.Sprintf("hop-%d", index+1), Hop: index + 1, Address: address, Status: "healthy", LatencyMS: float64(index + 1)}
		if diagnostic.IsPublicDiagnosticIP(net.ParseIP(address)) {
			node.PublicIP = true
			node.ASN = &diagnostic.ASNInfo{Number: 15169, Organization: fmt.Sprintf("Fixture endpoint %d", index)}
		}
		topology.Nodes = append(topology.Nodes, node)
		if index > 0 {
			topology.Links = append(topology.Links, diagnostic.TopologyLink{From: topology.Nodes[index-1].ID, To: node.ID, Status: "healthy"})
		}
	}
	result := diagnostic.Result{Kind: diagnostic.KindTraceroute, Address: "8.8.4.4", Status: diagnostic.StatusHealthy, StartedAt: fixtureTime,
		Details: map[string]any{
			"attempts":       []diagnostic.TraceAttempt{{Attempt: 1, Status: diagnostic.StatusHealthy, Topology: topology}},
			"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "attempts_unreached": 0,
			"attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
		}}
	return runFixtureResult(t, "999999999999999999999999", diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: result.Address, Attempts: 1}, result, 7)
}

func compactZeroRouteFixtureReport(t *testing.T) diagnostic.Report {
	result := diagnostic.Result{
		Kind: diagnostic.KindDNS, Address: "zero-route.example", Status: diagnostic.StatusHealthy, StartedAt: fixtureTime,
		Details: map[string]any{"addresses": []string{"192.0.2.1"}, "answer_count": 1},
	}
	return runFixtureResult(t, "555555555555555555555555", diagnostic.Target{Kind: diagnostic.KindDNS, Address: result.Address}, result, 3)
}

func compactZeroNodeAttemptFixtureReport(t *testing.T) diagnostic.Report {
	result := diagnostic.Result{
		Kind: diagnostic.KindTraceroute, Address: "zero-node.example", Status: diagnostic.StatusUnreachable, StartedAt: fixtureTime,
		ErrorCode: "destination_unreached", Details: map[string]any{
			"attempts":       []diagnostic.TraceAttempt{{Attempt: 1, Status: diagnostic.StatusUnreachable, Topology: &diagnostic.Topology{Nodes: []diagnostic.TopologyNode{}, Links: []diagnostic.TopologyLink{}}}},
			"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1, "attempts_unreached": 1,
			"attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
		},
	}
	return runFixtureResult(t, "666666666666666666666666", diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: result.Address, Attempts: 1}, result, 4)
}

func compactEmptyASNFixtureReport(t *testing.T) diagnostic.Report {
	topology := &diagnostic.Topology{
		Reached: true,
		Nodes: []diagnostic.TopologyNode{
			{ID: "local", Hop: 0, Address: "local", Status: "healthy"},
			{ID: "public", Hop: 1, Address: "8.8.8.8", Status: "healthy", PublicIP: true, ASN: &diagnostic.ASNInfo{}},
		},
		Links: []diagnostic.TopologyLink{{From: "local", To: "public", Status: "healthy"}},
	}
	result := diagnostic.Result{
		Kind: diagnostic.KindTraceroute, Address: "8.8.8.8", Status: diagnostic.StatusHealthy, StartedAt: fixtureTime,
		Details: map[string]any{
			"attempts":       []diagnostic.TraceAttempt{{Attempt: 1, Status: diagnostic.StatusHealthy, Topology: topology}},
			"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "attempts_unreached": 0,
			"attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
		},
	}
	return runFixtureResult(t, "777777777777777777777777", diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: result.Address, Attempts: 1}, result, 5)
}

func compactGeoBoundaryOrganization() string {
	return strings.Repeat("<>&\u2028\u2029", 135) + "boundary!"
}

func compactGeoExactBoundaryFixtureReport(t *testing.T) diagnostic.Report {
	return compactGeoBoundaryFixtureReport(t, compactGeoBoundaryOrganization())
}

func compactGeoBoundaryFixtureReport(t *testing.T, organization string) diagnostic.Report {
	t.Helper()
	topology := &diagnostic.Topology{
		Reached: true,
		Nodes: []diagnostic.TopologyNode{
			{ID: "local", Hop: 0, Address: "local", Status: "healthy"},
			{ID: "boundary", Hop: 1, Address: "8.8.8.8", Status: "healthy", PublicIP: true,
				ASN: &diagnostic.ASNInfo{Number: 1, Organization: organization}},
		},
		Links: []diagnostic.TopologyLink{{From: "local", To: "boundary", Status: "healthy"}},
	}
	result := diagnostic.Result{
		Kind: diagnostic.KindTraceroute, Address: "8.8.8.8", Status: diagnostic.StatusHealthy, StartedAt: fixtureTime,
		Details: map[string]any{
			"attempts":       []diagnostic.TraceAttempt{{Attempt: 1, Status: diagnostic.StatusHealthy, Topology: topology}},
			"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "attempts_unreached": 0,
			"attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
		},
	}
	return runFixtureResult(t, "888888888888888888888888", diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: result.Address, Attempts: 1}, result, 6)
}

func runFixtureResult(t *testing.T, reportID string, target diagnostic.Target, result diagnostic.Result, durationMS int64) diagnostic.Report {
	t.Helper()
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, fixtureResultChecker{kind: target.Kind, result: result})
	report, err := runner.RunWithID(context.Background(), reportID, diagnostic.Request{Targets: []diagnostic.Target{target}})
	if err != nil {
		t.Fatal(err)
	}
	normalizeFixtureReport(&report, durationMS)
	return report
}

func enrichmentFixtureReport(t *testing.T, reportID string, ips []string, lookup diagnostic.GeoIPLookup, maxAgeMS int64) diagnostic.Report {
	t.Helper()
	checker := diagnostic.NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(fixtureTraceOutput(ips)), nil
	}, lookup, nil)
	target := diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: ips[len(ips)-1], Attempts: 1}
	result := checker.Check(context.Background(), target)
	result.StartedAt = fixtureTime
	result.LatencyMS = 7
	coverage, ok := result.Details["geoip_enrichment"].(diagnostic.EnrichmentCoverage)
	if !ok {
		t.Fatalf("real traceroute result omitted enrichment: %#v", result.Details)
	}
	coverage.MaxAgeMS = maxAgeMS
	result.Details["geoip_enrichment"] = coverage

	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, fixtureResultChecker{result: result})
	report, err := runner.RunWithID(context.Background(), reportID, diagnostic.Request{Targets: []diagnostic.Target{target}})
	if err != nil {
		t.Fatal(err)
	}
	normalizeFixtureReport(&report, 9)
	if report.Summary.Total != 1 || report.Summary.Passed != 1 || report.Summary.Failed != 0 || report.Status != diagnostic.StatusHealthy || report.Analysis.Verdict != diagnostic.VerdictHealthy {
		t.Fatalf("inconsistent enrichment report: status=%s summary=%+v verdict=%s", report.Status, report.Summary, report.Analysis.Verdict)
	}
	if len(report.Analysis.Coverage.Enrichment) != 1 || !reflect.DeepEqual(report.Analysis.Coverage.Enrichment[0], coverage) {
		t.Fatalf("analysis did not derive exact checker enrichment: got=%+v want=%+v", report.Analysis.Coverage.Enrichment, coverage)
	}
	failureCount := 0
	for _, failure := range coverage.Failures {
		failureCount += failure.Count
	}
	if got := len(report.Analysis.Coverage.ProviderFailures); (failureCount == 0 && got != 0) || (failureCount > 0 && got != 1) {
		t.Fatalf("provider failures inconsistent with %d typed lookup failures: %+v", failureCount, report.Analysis.Coverage.ProviderFailures)
	}
	return report
}

func normalizeFixtureReport(report *diagnostic.Report, durationMS int64) {
	report.StartedAt = fixtureTime
	report.DurationMS = durationMS
	for index := range report.Results {
		report.Results[index].StartedAt = fixtureTime
		report.Results[index].LatencyMS = int64(index + 1)
	}
	analysis := diagnostic.Analyze(report.Results, fixtureTime)
	report.Analysis = &analysis
}

func fixtureMetadata(ip net.IP, source diagnostic.GeoIPSource) diagnostic.IPMetadata {
	last := ip[len(ip)-1]
	return diagnostic.IPMetadata{
		Geolocation: &diagnostic.GeoLocation{City: fmt.Sprintf("Fixture City %d", last), Region: "Fixture Region", Country: "Fixture Country", CountryCode: "FC", Latitude: 37.5, Longitude: 127.0},
		ASN:         &diagnostic.ASNInfo{Number: 64_500 + uint(last), Organization: fmt.Sprintf("Fixture ASN %d", last)},
		Source:      source,
		FetchedAt:   fixtureTime,
		ExpiresAt:   fixtureTime.Add(24 * time.Hour),
	}
}

func fixtureGeoIPError(kind diagnostic.GeoIPErrorKind) *diagnostic.GeoIPError {
	retryable := kind == diagnostic.GeoIPErrorBusy || kind == diagnostic.GeoIPErrorRateLimited || kind == diagnostic.GeoIPErrorTimeout || kind == diagnostic.GeoIPErrorUnavailable
	return &diagnostic.GeoIPError{Kind: kind, Retryable: retryable}
}

func fixtureTraceOutput(ips []string) string {
	var output bytes.Buffer
	destination := ips[len(ips)-1]
	fmt.Fprintf(&output, "traceroute to %s (%s), 30 hops max\n", destination, destination)
	for index, ip := range ips {
		fmt.Fprintf(&output, "%d  %s  %d.0 ms\n", index+1, ip, index+1)
	}
	return output.String()
}
