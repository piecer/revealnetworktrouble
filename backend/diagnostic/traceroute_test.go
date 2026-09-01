package diagnostic

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestTracerouteBuildsHealthyTopology(t *testing.T) {
	checker := TracerouteChecker{Command: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "traceroute" || !reflect.DeepEqual(args, []string{"-n", "-q", "1", "-w", "2", "-m", "30", "example.test"}) {
			t.Fatalf("command = %s %v", name, args)
		}
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

func TestTracerouteMarksLatencyJumpDegraded(t *testing.T) {
	topology, err := parseTraceroute("traceroute to 203.0.113.8 (203.0.113.8), 30 hops max\n1  192.0.2.1  1.2 ms\n2  198.51.100.4  80.5 ms\n3  203.0.113.8  82.0 ms", "203.0.113.8")
	if err != nil {
		t.Fatal(err)
	}
	if topology.Nodes[2].Status != "degraded" || topology.Links[1].Status != "degraded" || topologyStatus(topology) != StatusDegraded {
		t.Fatalf("unexpected topology: %+v", topology)
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
