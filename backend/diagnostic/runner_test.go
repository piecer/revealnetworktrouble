package diagnostic

import (
	"context"
	"testing"
	"time"
)

type fakeChecker struct {
	kind   Kind
	status Status
}

func (f fakeChecker) Kind() Kind { return f.kind }
func (f fakeChecker) Check(_ context.Context, target Target) Result {
	return Result{Kind: f.kind, Address: target.Address, Status: f.status, StartedAt: time.Now().UTC()}
}

func TestRunnerAggregatesStatusAndPreservesOrder(t *testing.T) {
	runner := NewRunner(fakeChecker{kind: KindDNS, status: StatusHealthy}, fakeChecker{kind: KindTCP, status: StatusUnreachable})
	report, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "example.test"}, {Kind: KindTCP, Address: "127.0.0.1:1"}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Status != StatusDegraded || report.Summary.Passed != 1 || report.Summary.Failed != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Results[0].Kind != KindDNS || report.Results[1].Kind != KindTCP {
		t.Fatalf("result order changed: %+v", report.Results)
	}
}

func TestRunnerKeepsAllDegradedResultsDegraded(t *testing.T) {
	runner := NewRunner(fakeChecker{kind: KindDNS, status: StatusDegraded})
	report, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "example.test"}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Status != StatusDegraded || report.Summary.Failed != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRunnerRejectsInvalidRequests(t *testing.T) {
	runner := NewRunner(fakeChecker{kind: KindDNS, status: StatusHealthy})
	tests := []Request{
		{},
		{Targets: []Target{{Kind: KindDNS, Address: ""}}},
		{Targets: []Target{{Kind: KindHTTP, Address: "https://example.test"}}},
		{Targets: []Target{{Kind: KindDNS, Address: "example.test"}}, TimeoutMS: 50},
		{Targets: []Target{{Kind: KindDNS, Address: "example.test", Attempts: 2}}},
		{Targets: []Target{{Kind: KindDNS, Address: "example.test", Attempts: MaxTraceAttempts + 1}}},
	}
	for _, req := range tests {
		if err := runner.Validate(req); err == nil {
			t.Fatalf("Validate(%+v) returned nil", req)
		}
	}
}
