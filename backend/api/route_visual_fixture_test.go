package api

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

// Synthetic observations, real runner/analysis/compact encoder. Never live probes.
type routeVisualChecker struct{}

func (routeVisualChecker) Kind() diagnostic.Kind { return diagnostic.KindTraceroute }
func (routeVisualChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	index := map[string]int{"8.8.4.4": 0, "1.1.1.1": 1, "9.9.9.9": 2}[target.Address]
	addresses := []string{"local", "10.0.0.1", "8.8.8.8", fmt.Sprintf("10.10.%d.1", index), target.Address}
	latencies := []float64{0, 0, 10, []float64{50, 100, 180}[index], []float64{25, 80, 180}[index]}
	if index == 2 {
		addresses = append(addresses[:4], "", "", target.Address)
		latencies = []float64{0, 0, 10, 180, 0, 0, 180}
	}
	topology := &diagnostic.Topology{Reached: true}
	for i, address := range addresses {
		status := "healthy"
		if address == "" {
			status = "unknown"
		}
		n := diagnostic.TopologyNode{ID: fmt.Sprintf("hop-%d", i), Hop: i, Address: address, Status: status, LatencyMS: latencies[i]}
		if address == "8.8.8.8" || address == "8.8.4.4" {
			n.PublicIP = true
			n.ASN = &diagnostic.ASNInfo{Number: 15169, Organization: "Synthetic fixture endpoint"}
		}
		topology.Nodes = append(topology.Nodes, n)
		if i > 0 {
			edgeStatus := status
			if addresses[i-1] == "" {
				edgeStatus = "unknown"
			}
			// Synthetic measured RTT jump, with the same >=50ms signal used
			// by the real trace classifier; unknown hops alone are not proof
			// of path degradation. Keep node/link observations consistent.
			var delta float64
			previous := topology.Nodes[i-1]
			if previous.Hop > 0 && previous.Status == "healthy" && status == "healthy" {
				delta = latencies[i] - latencies[i-1]
				if delta < 0 {
					delta = 0
				}
				if delta >= 50 {
					edgeStatus = "degraded"
					topology.Nodes[i].Status = "degraded"
				}
			}
			topology.Links = append(topology.Links, diagnostic.TopologyLink{From: previous.ID, To: n.ID, Status: edgeStatus, LatencyDeltaMS: delta})
		}
	}
	status := diagnostic.StatusHealthy
	for _, node := range topology.Nodes {
		if node.Status == "degraded" {
			status = diagnostic.StatusDegraded
		}
	}
	return diagnostic.Result{Kind: diagnostic.KindTraceroute, Address: target.Address, Status: status, StartedAt: fixtureTime, Details: map[string]any{
		"attempts":       []diagnostic.TraceAttempt{{Attempt: 1, Status: status, Topology: topology}, {Attempt: 2, Status: status, Topology: topology}},
		"attempts_total": 2, "attempts_reached": 2, "attempts_failed": 0, "attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
	}}
}
func TestRouteVisualFixtureMatchesExactProducerBytes(t *testing.T) {
	supervisor, err := diagnostic.NewCheckerSupervisor(3)
	if err != nil {
		t.Fatal(err)
	}
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, routeVisualChecker{})
	targets := []diagnostic.Target{}
	for _, address := range []string{"8.8.4.4", "1.1.1.1", "9.9.9.9"} {
		targets = append(targets, diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: address, Attempts: 2})
	}
	report, err := runner.RunWithID(context.Background(), "888888888888888888888888", diagnostic.Request{Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range report.Results {
		expected := diagnostic.StatusHealthy
		if result.Address != "8.8.4.4" {
			expected = diagnostic.StatusDegraded
		}
		if result.Status != expected || len(diagnostic.BuildCompactTopology(report).Routes) != 6 {
			t.Fatalf("fixture requires every route to complete: %s %s", result.Address, result.Status)
		}
	}
	for _, issue := range report.Analysis.Coverage.Limitations {
		if issue.Code == diagnostic.CoverageMalformedDetails {
			t.Fatalf("visual fixture must contain truthful diagnostic observations: %+v", issue)
		}
	}
	normalizeFixtureReport(&report, 7)
	got, err := marshalCompactResponse(report)
	if err != nil {
		t.Fatal(err)
	}
	again, err := marshalCompactResponse(report)
	if err != nil || !bytes.Equal(got, again) {
		t.Fatal("non-deterministic fixture")
	}
	path := "../../testdata/compact-route-visual-report.json"
	if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireFreshFixtureBytes(want, got); err != nil {
		t.Fatal(err)
	}
}
