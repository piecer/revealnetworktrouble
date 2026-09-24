package diagnostic

import (
	"context"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestTraceObservationEnrichmentDeadlineReservesPublicationTime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	reportDeadline, _ := ctx.Deadline()
	var enrichmentDeadline time.Time
	checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(observationReachedTrace), nil
	}, geoIPLookupFunc(func(enrichmentCtx context.Context, _ net.IP) (IPMetadata, error) {
		enrichmentDeadline, _ = enrichmentCtx.Deadline()
		<-enrichmentCtx.Done()
		return IPMetadata{}, geoIPContextError(enrichmentCtx.Err())
	}), nil)
	result := checker.Check(ctx, Target{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 1})
	if enrichmentDeadline.IsZero() || !enrichmentDeadline.Before(reportDeadline) || ctx.Err() != nil || result.Status != StatusHealthy {
		t.Fatalf("enrichment deadline=%s report deadline=%s parent=%v result=%+v", enrichmentDeadline, reportDeadline, ctx.Err(), result)
	}
}

func TestTraceObservationParentCancellationOverridesEnrichment(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent", true: "supervisor"}[shutdown], func(t *testing.T) {
			started := make(chan struct{})
			lookup := NewIPWhoIsLookup(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				close(started)
				<-req.Context().Done()
				return nil, req.Context().Err()
			})}, "https://geo.invalid/")
			checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
				return []byte(observationReachedTrace), nil
			}, lookup, nil)
			supervisor := mustSupervisor(t, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan Report, 1)
			go func() {
				report, _ := NewRunnerWithSupervisor(supervisor, checker).Run(ctx, Request{
					TimeoutMS: 100, TopologyMode: TopologyModeCompact,
					Targets: []Target{{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 1}},
				})
				done <- report
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("enrichment did not start")
			}
			drainCtx, stopDrain := context.WithTimeout(context.Background(), time.Second)
			defer stopDrain()
			if shutdown {
				supervisor.Shutdown(drainCtx)
			} else {
				cancel()
			}
			select {
			case report := <-done:
				if result := report.Results[0]; result.ErrorCode != "cancelled" || result.Details != nil || len(report.CompactTopology.Routes) != 0 {
					t.Fatalf("cancellation lost terminal priority: %+v", report)
				}
			case <-drainCtx.Done():
				t.Fatal("cancelled Runner did not return")
			}
			if active := supervisor.Shutdown(drainCtx); active != 0 {
				t.Fatalf("checker did not drain: %d", active)
			}
		})
	}
}

func TestTraceObservationSurvivesSaturatedGeoIP(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	lookup := NewIPWhoIsLookupWithConfig(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-release
		return nil, context.DeadlineExceeded
	})}, "https://geo.invalid/", nil, GeoIPCacheConfig{MaxActive: 1, MaxQueued: 1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	t.Cleanup(func() { cancel(); <-done; <-done; close(release) })
	go func() { _, _ = lookup.Lookup(ctx, net.ParseIP("1.1.1.1")); done <- struct{}{} }()
	<-started
	go func() { _, _ = lookup.Lookup(ctx, net.ParseIP("9.9.9.9")); done <- struct{}{} }()
	waitForGeoIPWaiters(t, lookup, "9.9.9.9", 1)
	checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(observationReachedTrace), nil
	}, lookup, nil)
	report, err := NewRunnerWithSupervisor(mustSupervisor(t, 1), checker).Run(context.Background(), Request{
		TimeoutMS: 100, TopologyMode: TopologyModeCompact,
		Targets: []Target{{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := report.Results[0]
	if result.Status != StatusHealthy || result.Details["attempts_reached"] != 1 || len(report.CompactTopology.Routes) != 1 {
		t.Fatalf("saturation erased core observation: %+v", report)
	}
	coverage := result.Details["geoip_enrichment"].(EnrichmentCoverage)
	if !reflect.DeepEqual(coverage.Failures, []EnrichmentFailure{{Kind: GeoIPErrorBusy, Count: 1, Retryable: true}}) {
		t.Fatalf("saturation failure=%+v", coverage)
	}
}
