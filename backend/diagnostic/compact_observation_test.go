package diagnostic

import (
	"context"
	"fmt"
	"testing"
)

func TestCompactRTTAveragesOnlyResponsiveObservations(t *testing.T) {
	for _, latency := range []float64{0, 100} {
		t.Run(fmt.Sprintf("measured-%g", latency), func(t *testing.T) {
			outputs := []string{
				fmt.Sprintf("traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1 8.8.8.8 %g ms\n", latency),
				"traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1 9.9.9.9 1 ms\n2 *\n",
			}
			call := 0
			checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
				output := outputs[call]
				call++
				return []byte(output), nil
			}, nil, nil)
			result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 2})
			compact := BuildCompactTopology(Report{Results: []Result{result}})
			found := false
			for _, node := range compact.Nodes {
				if node.Kind == CompactNodeUnknown && node.LatencyMSAvg != nil {
					t.Fatalf("unknown RTT invented: %+v", node)
				}
				if node.Address == "8.8.8.8" {
					found = true
					if node.Observations != 2 || node.LatencyMSAvg == nil || *node.LatencyMSAvg != latency {
						t.Fatalf("unobserved destination diluted measured RTT %g: %+v", latency, node)
					}
				}
			}
			if !found {
				t.Fatal("observed destination missing")
			}
		})
	}
}

func TestCompactUnreachedDestinationHasNoRTT(t *testing.T) {
	checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("traceroute to 8.8.8.8 (8.8.8.8), 30 hops max\n1 *\n"), nil
	}, nil, nil)
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "8.8.8.8", Attempts: 1})
	compact := BuildCompactTopology(Report{Results: []Result{result}})
	for _, node := range compact.Nodes {
		if node.LatencyMSAvg != nil {
			t.Fatalf("nonresponsive route invented RTT: %+v", node)
		}
	}
}
