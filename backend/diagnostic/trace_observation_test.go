package diagnostic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"
)

const observationReachedTrace = "traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1  8.8.8.8  1.0 ms\n"

func TestTraceObservationConsecutiveResponsiveLatencyJumps(t *testing.T) {
	for _, test := range []struct {
		name, hops string
		wantDelta  float64
		wantStatus string
	}{
		{name: "two adjacent jumps", hops: "1  1.1.1.1  1 ms\n2  9.9.9.9  101 ms\n3  8.8.8.8  201 ms\n", wantDelta: 100, wantStatus: "degraded"},
		{name: "jump then small increase", hops: "1  1.1.1.1  1 ms\n2  9.9.9.9  101 ms\n3  8.8.8.8  111 ms\n", wantDelta: 10, wantStatus: "healthy"},
		{name: "jump then decrease", hops: "1  1.1.1.1  1 ms\n2  9.9.9.9  101 ms\n3  8.8.8.8  51 ms\n", wantStatus: "healthy"},
		{name: "unknown gap", hops: "1  1.1.1.1  1 ms\n2  * * *\n3  8.8.8.8  201 ms\n", wantStatus: "healthy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
				return []byte("traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n" + test.hops), nil
			}, nil, nil)
			report, err := NewRunnerWithSupervisor(mustSupervisor(t, 1), checker).Run(context.Background(), Request{
				TimeoutMS: 100, TopologyMode: TopologyModeCompact,
				Targets: []Target{{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 1}},
			})
			if err != nil {
				t.Fatal(err)
			}
			topology, ok := report.Results[0].Details["topology"].(Topology)
			if !ok || len(topology.Links) != 3 {
				t.Fatalf("topology=%+v", topology)
			}
			last := topology.Links[2]
			if last.LatencyDeltaMS != test.wantDelta || last.Status != test.wantStatus || topology.Nodes[3].Status != test.wantStatus {
				t.Fatalf("preceding classification suppressed RTT observation: link=%+v node=%+v", last, topology.Nodes[3])
			}
			if report.CompactTopology == nil || len(report.CompactTopology.Routes) != 1 {
				t.Fatalf("route missing: %+v", report.CompactTopology)
			}
		})
	}
}

type observationResolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f observationResolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

func TestTraceObservationHealthyPathVariationThroughRunner(t *testing.T) {
	for _, test := range []struct {
		name, secondHop string
		secondErr       error
		wantStatus      Status
		wantCodes       []FindingCode
		wantRoutes      int
	}{
		{name: "varied completed routes", secondHop: "9.9.9.9", wantStatus: StatusHealthy, wantCodes: []FindingCode{FindingTraceroutePathUnstable}, wantRoutes: 2},
		{name: "same completed route", secondHop: "1.1.1.1", wantStatus: StatusHealthy, wantRoutes: 2},
		{name: "failed variant excluded", secondHop: "9.9.9.9", secondErr: errors.New("command failed"), wantStatus: StatusDegraded, wantCodes: []FindingCode{FindingTracerouteExecutionFailed}, wantRoutes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
				calls++
				hop, err := "1.1.1.1", error(nil)
				if calls == 2 {
					hop, err = test.secondHop, test.secondErr
				}
				return []byte(fmt.Sprintf("traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1  %s  1.0 ms\n2  8.8.8.8  5.0 ms\n", hop)), err
			}, nil, nil)
			report, err := NewRunnerWithSupervisor(mustSupervisor(t, 1), checker).Run(context.Background(), Request{
				TimeoutMS: 100, TopologyMode: TopologyModeCompact,
				Targets: []Target{{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 2}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != test.wantStatus || report.Results[0].Status != test.wantStatus {
				t.Fatalf("path variation changed observed reachability: %+v", report)
			}
			if got := findingCodes(report.Analysis.Findings); len(got) != len(test.wantCodes) || len(got) > 0 && !reflect.DeepEqual(got, test.wantCodes) {
				t.Fatalf("findings=%v want %v (producer status=%s)", got, test.wantCodes, report.Results[0].Status)
			}
			if len(report.Analysis.Coverage.Limitations) != 0 || report.CompactTopology == nil || len(report.CompactTopology.Routes) != test.wantRoutes {
				t.Fatalf("observation projection invalid: %+v", report)
			}
		})
	}
}

func TestTraceObservationSurvivesLaterResolutionFailureThroughRunner(t *testing.T) {
	for _, test := range []struct {
		name                string
		err                 error
		code                string
		finding             FindingCode
		timedOut, cancelled int
	}{
		{name: "DNS error", err: errors.New("DNS unavailable"), code: "traceroute_failed", finding: FindingTracerouteExecutionFailed},
		{name: "DNS timeout", err: context.DeadlineExceeded, code: "timeout", finding: FindingExecutionTimeout, timedOut: 1},
		{name: "DNS cancelled", err: context.Canceled, code: "cancelled", finding: FindingExecutionCancelled, cancelled: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolves, commands := 0, 0
			policy := NewNetworkPolicy(observationResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
				resolves++
				if resolves == 2 {
					return nil, test.err
				}
				return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
			}), nil)
			checker := NewInjectedTracerouteChecker(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				commands++
				if args[len(args)-1] != "8.8.8.8" {
					return nil, errors.New("unvalidated command destination")
				}
				return []byte(observationReachedTrace), nil
			}, nil, policy)
			report, err := NewRunnerWithSupervisor(mustSupervisor(t, 1), checker).Run(context.Background(), Request{
				TimeoutMS: 100, TopologyMode: TopologyModeCompact,
				Targets: []Target{{Kind: KindTraceroute, Address: "route.example", Attempts: 3}},
			})
			if err != nil {
				t.Fatal(err)
			}
			result := report.Results[0]
			if result.Status != StatusDegraded || result.ErrorCode != "" || resolves != 3 || commands != 2 {
				t.Fatalf("later resolution failure erased observation: result=%+v resolves=%d commands=%d", result, resolves, commands)
			}
			for key, want := range map[string]int{"attempts_total": 3, "attempts_reached": 2, "attempts_failed": 1, "attempts_unreached": 0, "attempts_execution_failed": 1, "attempts_timed_out": test.timedOut, "attempts_cancelled": test.cancelled} {
				if result.Details[key] != want {
					t.Errorf("%s=%v want %d", key, result.Details[key], want)
				}
			}
			attempts, ok := result.Details["attempts"].([]TraceAttempt)
			if !ok || len(attempts) != 3 || !traceAttemptEligible(attempts[0]) || !traceAttemptEligible(attempts[2]) || attempts[1].ErrorCode != test.code || attempts[1].Topology != nil {
				t.Fatalf("attempt observations=%+v", attempts)
			}
			if got := findingCodes(report.Analysis.Findings); !reflect.DeepEqual(got, []FindingCode{test.finding}) || len(report.Analysis.Coverage.Limitations) != 0 {
				t.Fatalf("analysis contradicted preserved attempts: %+v", report.Analysis)
			}
			if report.CompactTopology == nil || len(report.CompactTopology.Routes) != 2 {
				t.Fatalf("compact lost completed routes: %+v", report.CompactTopology)
			}
		})
	}
}

func TestTraceObservationSurvivesSlowGeoIPThroughRunner(t *testing.T) {
	for _, cooperative := range []bool{true, false} {
		t.Run(map[bool]string{true: "cooperative", false: "noncooperative"}[cooperative], func(t *testing.T) {
			release := make(chan struct{})
			providerExited := make(chan struct{})
			lookup := NewIPWhoIsLookup(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				defer close(providerExited)
				if cooperative {
					<-req.Context().Done()
				} else {
					<-release
				}
				return nil, context.DeadlineExceeded
			})}, "https://geo.invalid/")
			t.Cleanup(func() {
				close(release)
				select {
				case <-providerExited:
				case <-time.After(time.Second):
					t.Error("provider did not exit after release")
				}
			})
			checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
				return []byte(observationReachedTrace), nil
			}, lookup, nil)
			supervisor := mustSupervisor(t, 1)
			report, err := NewRunnerWithSupervisor(supervisor, checker).Run(context.Background(), Request{
				TimeoutMS: 100, TopologyMode: TopologyModeCompact,
				Targets: []Target{{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 1}},
			})
			if err != nil {
				t.Fatal(err)
			}
			result := report.Results[0]
			if result.Status != StatusHealthy || result.ErrorCode != "" || result.Details["attempts_reached"] != 1 {
				t.Fatalf("optional GeoIP erased completed trace: %+v", result)
			}
			coverage := result.Details["geoip_enrichment"].(EnrichmentCoverage)
			wantFailures := []EnrichmentFailure{{Kind: GeoIPErrorTimeout, Count: 1, Retryable: true}}
			if !reflect.DeepEqual(coverage.Failures, wantFailures) || result.Details["geoip_provider_failures"] != 1 {
				t.Fatalf("typed timeout coverage = %+v", coverage)
			}
			if report.CompactTopology == nil || len(report.CompactTopology.Routes) != 1 || !report.CompactTopology.Routes[0].Reached {
				t.Fatalf("completed route missing from compact: %+v", report.CompactTopology)
			}
			if len(report.Analysis.Coverage.Enrichment) != 1 || !reflect.DeepEqual(report.Analysis.Coverage.Enrichment[0].Failures, wantFailures) {
				t.Fatalf("analysis lost typed enrichment coverage: %+v", report.Analysis)
			}
			if snapshot := supervisor.Snapshot(); snapshot.Active != 0 || snapshot.Stuck != 0 {
				t.Fatalf("checker failed to drain before publishing: %+v", snapshot)
			}
			before, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			// Provider completion must not mutate the report after publication.
			if !cooperative {
				release <- struct{}{}
			}
			<-providerExited
			after, err := json.Marshal(report)
			if err != nil || string(before) != string(after) {
				t.Fatalf("published report mutated: err=%v", err)
			}
		})
	}
}
