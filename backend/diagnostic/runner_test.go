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

type checkerFunc struct {
	kind Kind
	fn   func(context.Context, Target) Result
}

func (c checkerFunc) Kind() Kind { return c.kind }
func (c checkerFunc) Check(ctx context.Context, target Target) Result {
	return c.fn(ctx, target)
}

func (f fakeChecker) Kind() Kind { return f.kind }
func (f fakeChecker) Check(_ context.Context, target Target) Result {
	result := Result{Kind: f.kind, Address: target.Address, Status: f.status, StartedAt: time.Now().UTC()}
	if f.status == StatusUnreachable {
		result.ErrorCode = "connection_failed"
	}
	return result
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
		{Targets: []Target{{Kind: KindDNS, Address: "example.test"}}, TimeoutMS: (1 << 58) + 1000},
		{Targets: []Target{{Kind: KindDNS, Address: "example.test", Attempts: 2}}},
		{Targets: []Target{{Kind: KindDNS, Address: "example.test", Attempts: MaxTraceAttempts + 1}}},
	}
	for _, req := range tests {
		if err := runner.Validate(req); err == nil {
			t.Fatalf("Validate(%+v) returned nil", req)
		}
	}
}

func TestMaximumValidRequestBudgetCoversEveryTracerouteAttempt(t *testing.T) {
	req := Request{
		TimeoutMS: int(MaxTimeout.Milliseconds()),
		Targets:   []Target{{Kind: KindTraceroute, Address: "example.test", Attempts: MaxTraceAttempts}},
	}
	runner := NewRunner(fakeChecker{kind: KindTraceroute, status: StatusHealthy})
	if err := runner.Validate(req); err != nil {
		t.Fatalf("maximum request was rejected: %v", err)
	}
	wantMinimum := MaxTimeout * MaxTraceAttempts
	if got := RequestBudget(req); got < wantMinimum {
		t.Fatalf("RequestBudget() = %s, want at least %s", got, wantMinimum)
	}
	if MaxRequestBudget < RequestBudget(req) || MaxRequestBudget <= 35*time.Second {
		t.Fatalf("MaxRequestBudget=%s request=%s", MaxRequestBudget, RequestBudget(req))
	}
}

func TestRunnerWiresRequestDeadlineAndAttemptTimeoutToTraceroute(t *testing.T) {
	req := Request{
		TimeoutMS: 200,
		Targets:   []Target{{Kind: KindTraceroute, Address: "example.test", Attempts: 2}},
	}
	checker := checkerFunc{kind: KindTraceroute, fn: func(ctx context.Context, target Target) Result {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("traceroute checker context has no request deadline")
		}
		remaining := time.Until(deadline)
		budget := RequestBudget(req)
		if remaining <= 0 || remaining > budget || remaining < budget-100*time.Millisecond {
			t.Fatalf("request deadline remaining=%s budget=%s", remaining, budget)
		}
		if got := attemptTimeout(ctx); got != 200*time.Millisecond {
			t.Fatalf("attempt timeout=%s", got)
		}
		return Result{Kind: target.Kind, Address: target.Address, Status: StatusHealthy}
	}}
	if _, err := NewRunner(checker).Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerAddsAnalysisUsingInjectedClock(t *testing.T) {
	clock := func() time.Time { return analysisTestNow }
	runner := NewRunnerWithClock(clock, fakeChecker{kind: KindDNS, status: StatusUnreachable})
	report, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "missing.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Analysis == nil || report.Analysis.Verdict != VerdictAttention || len(report.Analysis.Findings) != 1 || report.Analysis.Findings[0].Code != FindingDNSResolutionFailed {
		t.Fatalf("report analysis = %+v", report.Analysis)
	}
}
