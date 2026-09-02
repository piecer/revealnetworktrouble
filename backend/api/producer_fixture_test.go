package api

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	result diagnostic.Result
}

func (fixtureResultChecker) Kind() diagnostic.Kind { return diagnostic.KindTraceroute }

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

func enrichmentFixtureReport(t *testing.T, reportID string, ips []string, lookup diagnostic.GeoIPLookup, maxAgeMS int64) diagnostic.Report {
	t.Helper()
	checker := diagnostic.TracerouteChecker{
		Command: func(context.Context, string, ...string) ([]byte, error) {
			return []byte(fixtureTraceOutput(ips)), nil
		},
		GeoIP: lookup,
	}
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
