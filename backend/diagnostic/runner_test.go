package diagnostic

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
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

type deadlineOnlyContext struct {
	deadline time.Time
	err      error
}

func (c deadlineOnlyContext) Deadline() (time.Time, bool) { return c.deadline, !c.deadline.IsZero() }
func (deadlineOnlyContext) Done() <-chan struct{}         { return nil }
func (c deadlineOnlyContext) Err() error                  { return c.err }
func (deadlineOnlyContext) Value(any) any                 { return nil }

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

func TestRunnerRedirectFinalStatusContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/start" {
			http.Redirect(w, request, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	checker := HTTPChecker{Client: server.Client()}
	for _, test := range []struct {
		expected  int
		status    Status
		errorCode string
	}{
		{http.StatusNoContent, StatusHealthy, ""},
		{http.StatusFound, StatusUnreachable, "unexpected_status"},
	} {
		result := checker.Check(context.Background(), Target{Kind: KindHTTP, Address: server.URL + "/start", ExpectedStatus: test.expected})
		if result.Status != test.status || result.ErrorCode != test.errorCode || result.Details["status_code"] != http.StatusNoContent {
			t.Errorf("expected=%d result=%+v", test.expected, result)
		}
	}
}

func TestRunnerRedirectFinalStatusRevalidatesEveryHTTPSHop(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Host {
		case "first.test":
			http.Redirect(w, request, "https://second.test/middle", http.StatusFound)
		case "second.test":
			http.Redirect(w, request, "https://third.test/final", http.StatusTemporaryRedirect)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	resolver := &resolverSequence{addresses: [][]net.IPAddr{
		ipAnswers("93.184.216.31"), ipAnswers("93.184.216.32"), ipAnswers("93.184.216.33"),
		ipAnswers("93.184.216.34"), ipAnswers("93.184.216.35"),
	}}
	dialer := &mappingDialer{target: server.Listener.Addr().String()}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	checker := HTTPSChecker{Client: client, Policy: NewNetworkPolicy(resolver, dialer)}
	result := checker.Check(context.Background(), Target{Kind: KindHTTPS, Address: "https://first.test/start", ExpectedStatus: http.StatusNoContent})
	if result.Status != StatusHealthy || result.ErrorCode != "" || result.Details["status_code"] != http.StatusNoContent {
		t.Fatalf("final HTTPS response result=%+v", result)
	}
	if resolver.calls != 5 || len(dialer.calls) != 3 {
		t.Fatalf("redirect revalidation resolver_calls=%d dialer_calls=%v", resolver.calls, dialer.calls)
	}
}

func TestRunnerExpectedStatusContract(t *testing.T) {
	kinds := []Kind{KindDNS, KindTCP, KindHTTP, KindHTTPS, KindTraceroute, KindSSH, KindSMTP, KindSubmission, KindSMTPS, KindIMAP, KindIMAPS, KindPOP3, KindPOP3S}
	checkers := make([]Checker, 0, len(kinds))
	for _, kind := range kinds {
		checkers = append(checkers, fakeChecker{kind: kind, status: StatusHealthy})
	}
	runner := NewRunner(checkers...)

	for _, kind := range kinds {
		if err := runner.Validate(Request{Targets: []Target{{Kind: kind, Address: "example.test"}}}); err != nil {
			t.Errorf("omitted expected_status rejected for %s: %v", kind, err)
		}
	}
	for _, kind := range []Kind{KindHTTP, KindHTTPS} {
		for _, status := range []int{100, 599} {
			if err := runner.Validate(Request{Targets: []Target{{Kind: kind, Address: "https://example.test", ExpectedStatus: status}}}); err != nil {
				t.Errorf("expected_status=%d rejected for %s: %v", status, kind, err)
			}
		}
	}
	for _, test := range []struct {
		kind   Kind
		status int
	}{
		{KindHTTP, 99}, {KindHTTP, 600}, {KindHTTPS, 99}, {KindHTTPS, 600},
		{KindDNS, 200}, {KindTCP, 200}, {KindTraceroute, 200}, {KindSSH, 200},
		{KindSMTP, 200}, {KindSubmission, 200}, {KindSMTPS, 200}, {KindIMAP, 200},
		{KindIMAPS, 200}, {KindPOP3, 200}, {KindPOP3S, 200},
	} {
		if err := runner.Validate(Request{Targets: []Target{{Kind: test.kind, Address: "example.test", ExpectedStatus: test.status}}}); err == nil {
			t.Errorf("expected_status=%d accepted for %s", test.status, test.kind)
		}
	}
}

func TestRunnerRejectsInvalidRequests(t *testing.T) {
	runner := NewRunner(fakeChecker{kind: KindDNS, status: StatusHealthy})
	tests := []Request{
		{},
		{Targets: []Target{{Kind: KindDNS, Address: ""}}},
		{Targets: []Target{{Kind: KindHTTP, Address: "https://example.test"}}},
		{Targets: []Target{{Kind: KindDNS, Address: "example.test"}}, TimeoutMS: 50},
		{Targets: []Target{{Kind: KindDNS, Address: "example.test"}}, TimeoutMS: int(^uint(0) >> 1)},
		{Targets: []Target{{Kind: KindDNS, Address: "example.test", Attempts: 2}}},
		{Targets: []Target{{Kind: KindDNS, Address: "example.test", Attempts: MaxTraceAttempts + 1}}},
	}
	for _, req := range tests {
		if err := runner.Validate(req); err == nil {
			t.Fatalf("Validate(%+v) returned nil", req)
		}
	}
}

func TestRunnerValidatesTopologyModeAndCompactScope(t *testing.T) {
	runner := NewRunner(
		fakeChecker{kind: KindDNS, status: StatusHealthy},
		fakeChecker{kind: KindTraceroute, status: StatusHealthy},
	)
	valid := []Request{
		{Targets: []Target{{Kind: KindDNS, Address: "example.test"}}},
		{TopologyMode: TopologyModeFull, Targets: []Target{{Kind: KindDNS, Address: "example.test"}}},
		{TopologyMode: TopologyModeCompact, Targets: []Target{{Kind: KindTraceroute, Address: "example.test"}}},
	}
	for _, request := range valid {
		if err := runner.Validate(request); err != nil {
			t.Fatalf("Validate(%+v) = %v", request, err)
		}
	}
	for _, mode := range []TopologyMode{"", " ", "FULL", "Compact", "unknown"} {
		request := Request{TopologyMode: mode, Targets: []Target{{Kind: KindTraceroute, Address: "example.test"}}}
		request.topologyModeSet = true
		if err := runner.Validate(request); err == nil {
			t.Fatalf("Validate() accepted explicitly supplied mode %q", mode)
		}
	}
	for _, targets := range [][]Target{
		{{Kind: KindDNS, Address: "example.test"}},
		{{Kind: KindTraceroute, Address: "example.test"}, {Kind: KindDNS, Address: "example.test"}},
	} {
		if err := runner.Validate(Request{TopologyMode: TopologyModeCompact, Targets: targets}); err == nil {
			t.Fatalf("compact request accepted non-traceroute targets: %+v", targets)
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

func TestRunnerBuildsCompactTopologyAfterAnalysisWithoutPruningRawFacts(t *testing.T) {
	attempts := []TraceAttempt{compactTestAttempt(1,
		TopologyNode{ID: "local", Hop: 0, Address: "local", Status: "healthy"},
		TopologyNode{ID: "destination", Hop: 1, Address: "203.0.113.8", Status: "healthy"},
	)}
	checker := checkerFunc{kind: KindTraceroute, fn: func(context.Context, Target) Result {
		return Result{Kind: KindTraceroute, Status: StatusHealthy, Details: map[string]any{
			"attempts": attempts, "attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0,
			"attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
		}}
	}}
	report, err := NewRunner(checker).Run(context.Background(), Request{
		TopologyMode: TopologyModeCompact,
		Targets:      []Target{{Kind: KindTraceroute, Address: "example.test", Attempts: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Analysis == nil || report.CompactTopology == nil || len(report.CompactTopology.Routes) != 1 {
		t.Fatalf("compact report was not built after analysis: %+v", report)
	}
	cached, ok := report.CachedCompactTopologyBuild()
	if !ok || cached.Topology != report.CompactTopology || cached.AcceptedTransactions != 1 {
		t.Fatalf("compact build metadata was not cached: ok=%v build=%+v", ok, cached)
	}
	gotAttempts, ok := report.Results[0].Details["attempts"].([]TraceAttempt)
	if !ok || !reflect.DeepEqual(gotAttempts, attempts) {
		t.Fatalf("raw attempts were pruned or mutated: %#v", report.Results[0].Details["attempts"])
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

func TestRunnerRecoversCheckerPanicInSubprocess(t *testing.T) {
	if os.Getenv("CHECKNETWORK_PANIC_HELPER") == "1" {
		supervisor, err := NewCheckerSupervisor(2)
		if err != nil {
			t.Fatal(err)
		}
		checker := checkerFunc{kind: KindDNS, fn: func(_ context.Context, target Target) Result {
			if target.Address == "secret.example" {
				panic("secret panic text")
			}
			return Result{Kind: KindDNS, Address: target.Address, Status: StatusHealthy}
		}}
		runner := NewRunnerWithSupervisor(supervisor, checker)
		report, err := runner.RunWithID(context.Background(), "0123456789abcdef01234567", Request{Targets: []Target{{Kind: KindDNS, Address: "secret.example"}, {Kind: KindDNS, Address: "sibling.example"}}})
		if err != nil {
			t.Fatal(err)
		}
		result := report.Results[0]
		if result.ErrorCode != "checker_panic" || result.Address != "" || strings.Contains(result.Message, "secret") {
			t.Fatalf("unsafe panic result: %+v", result)
		}
		if report.Results[1].Status != StatusHealthy {
			t.Fatalf("sibling did not survive: %+v", report.Results[1])
		}
		followup, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "followup.example"}}})
		if err != nil || followup.Results[0].Status != StatusHealthy {
			t.Fatalf("follow-up did not survive: %+v err=%v", followup, err)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunnerRecoversCheckerPanicInSubprocess$")
	cmd.Env = append(os.Environ(), "CHECKNETWORK_PANIC_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("panic escaped worker boundary: %v\n%s", err, output)
	}
}

func TestRunnerNoncooperativeDeadlineLateImmutabilityAndCapacityRecovery(t *testing.T) {
	supervisor, err := NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	checker := checkerFunc{kind: KindDNS, fn: func(context.Context, Target) Result {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return Result{Kind: KindDNS, Address: "late-mutated.example", Status: StatusHealthy}
	}}
	runner := NewRunnerWithSupervisor(supervisor, checker)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	before := time.Now()
	report, err := runner.Run(ctx, Request{Targets: []Target{{Kind: KindDNS, Address: "blocked.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(before); elapsed > 200*time.Millisecond {
		t.Fatalf("Run blocked for %s", elapsed)
	}
	<-started
	if report.Results[0].ErrorCode != "cancelled" {
		t.Fatalf("want cancellation priority: %+v", report.Results[0])
	}
	if snapshot := supervisor.Snapshot(); snapshot.Active != 1 || snapshot.Stuck != 1 || snapshot.Capacity != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	saturated, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "second.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	if saturated.Results[0].ErrorCode != "checker_capacity_unavailable" || calls.Load() != 1 {
		t.Fatalf("saturation report=%+v calls=%d", saturated, calls.Load())
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for supervisor.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if report.Results[0].ErrorCode != "cancelled" || report.Results[0].Address != "blocked.example" {
		t.Fatalf("late mutation: %+v", report.Results[0])
	}
	third, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "third.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	if third.Results[0].Status != StatusHealthy || calls.Load() != 2 {
		t.Fatalf("capacity did not recover: %+v calls=%d", third, calls.Load())
	}
}

func TestRunnerNoncooperativeCheckerReturnsAtRequestBudgetWithTimeout(t *testing.T) {
	supervisor := mustSupervisor(t, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	checker := checkerFunc{kind: KindDNS, fn: func(context.Context, Target) Result {
		close(started)
		<-release
		return Result{Kind: KindDNS, Status: StatusHealthy}
	}}
	req := Request{TimeoutMS: int(MinTimeout.Milliseconds()), Targets: []Target{{Kind: KindDNS, Address: "deadline.example"}}}
	before := time.Now()
	report, err := NewRunnerWithSupervisor(supervisor, checker).Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	elapsed := time.Since(before)
	if elapsed < RequestBudget(req)-100*time.Millisecond || elapsed > RequestBudget(req)+250*time.Millisecond {
		t.Fatalf("elapsed=%s budget=%s", elapsed, RequestBudget(req))
	}
	if report.Results[0].ErrorCode != "timeout" {
		t.Fatalf("deadline result=%+v", report.Results[0])
	}
	close(release)
}

func TestRunnerPreCancelledParentNeverAdmitsOrCallsChecker(t *testing.T) {
	supervisor := mustSupervisor(t, 1)
	var calls atomic.Int32
	checker := checkerFunc{kind: KindDNS, fn: func(context.Context, Target) Result {
		calls.Add(1)
		return Result{Kind: KindDNS, Status: StatusHealthy}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := NewRunnerWithSupervisor(supervisor, checker).Run(ctx, Request{Targets: []Target{{Kind: KindDNS, Address: "cancelled.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Results[0].ErrorCode; got != "cancelled" {
		t.Fatalf("pre-cancelled result=%+v", report.Results[0])
	}
	if calls.Load() != 0 {
		t.Fatalf("checker calls=%d", calls.Load())
	}
	if snapshot := supervisor.Snapshot(); snapshot.Active != 0 || snapshot.Stuck != 0 {
		t.Fatalf("pre-cancelled run touched supervisor: %+v", snapshot)
	}
}

func TestRunnerSimultaneousCompletionAndCancellationUsesCommittedCancellation(t *testing.T) {
	supervisor := mustSupervisor(t, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	checker := checkerFunc{kind: KindDNS, fn: func(context.Context, Target) Result {
		close(started)
		<-release
		// Commit cancellation immediately before returning a healthy result. The
		// terminal arbiter must publish exactly one deterministic cancellation.
		cancel()
		return Result{Kind: KindDNS, Address: "must-not-win.example", Status: StatusHealthy}
	}}
	done := make(chan Report, 1)
	go func() {
		report, _ := NewRunnerWithSupervisor(supervisor, checker).Run(ctx, Request{Targets: []Target{{Kind: KindDNS, Address: "race.example"}}})
		done <- report
	}()
	<-started
	close(release)
	report := <-done
	if got := report.Results[0]; got.ErrorCode != "cancelled" || got.Address != "race.example" {
		t.Fatalf("terminal result=%+v", got)
	}
}

func TestRunnerCheckerDeadlineObservedAtReturnOverridesLateHealthyResult(t *testing.T) {
	supervisor := mustSupervisor(t, 1)
	deadlineObserved := make(chan struct{})
	allowReturn := make(chan struct{})
	checker := checkerFunc{kind: KindDNS, fn: func(ctx context.Context, target Target) Result {
		<-ctx.Done()
		close(deadlineObserved)
		<-allowReturn
		return Result{Kind: target.Kind, Address: "late-healthy.example", Status: StatusHealthy}
	}}
	req := Request{TimeoutMS: int(MinTimeout.Milliseconds()), Targets: []Target{{Kind: KindDNS, Address: "deadline.example"}}}
	done := make(chan Report, 1)
	started := time.Now()
	go func() {
		report, _ := NewRunnerWithSupervisor(supervisor, checker).Run(context.Background(), req)
		done <- report
	}()
	<-deadlineObserved
	if snapshot := supervisor.Snapshot(); snapshot.Active != 1 {
		t.Fatalf("lease released before checker return: %+v", snapshot)
	}
	close(allowReturn)
	report := <-done
	if elapsed := time.Since(started); elapsed >= RequestBudget(req)-200*time.Millisecond {
		t.Fatalf("completion waited for report grace: elapsed=%s budget=%s", elapsed, RequestBudget(req))
	}
	if got := report.Results[0]; got.ErrorCode != "timeout" || got.Status != StatusUnreachable || got.Address != "deadline.example" {
		t.Fatalf("deadline result=%+v", got)
	}
	if snapshot := supervisor.Snapshot(); snapshot.Active != 0 {
		t.Fatalf("lease retained after checker return: %+v", snapshot)
	}
}

func TestRunnerCompletionAtCheckerDeadlineTimesOutBeforeTimerCallbackRuns(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })

	const runs = 100
	healthy := 0
	unexpected := 0
	for i := 0; i < runs; i++ {
		supervisor := mustSupervisor(t, 1)
		checker := checkerFunc{kind: KindDNS, fn: func(ctx context.Context, target Target) Result {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("non-traceroute checker context has no deadline")
			}
			for time.Now().Before(deadline) {
			}
			return Result{Kind: target.Kind, Address: target.Address, Status: StatusHealthy}
		}}
		runner := NewRunnerWithSupervisor(supervisor, checker)
		outcomes := make(chan indexedOutcome, 1)
		lease := supervisor.acquire()
		terminal := &targetTerminal{
			index: 0, target: Target{Kind: KindDNS, Address: "deadline.example"}, started: time.Now().UTC(),
			parentCtx: context.Background(), runCtx: context.Background(), supervisor: supervisor, lease: lease, outcomes: outcomes,
		}
		go runner.runChecker(context.Background(), time.Millisecond, terminal.target, lease, terminal)
		result := (<-outcomes).result
		if result.Status == StatusHealthy {
			healthy++
		}
		if result.ErrorCode != "timeout" {
			unexpected++
		}
	}
	if healthy != 0 || unexpected != 0 {
		t.Fatalf("healthy results=%d/%d; non-timeout results=%d/%d", healthy, runs, unexpected, runs)
	}
}

func TestRunnerPanicAtCheckerDeadlineTimesOutBeforeTimerCallbackRuns(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })

	supervisor := mustSupervisor(t, 1)
	checker := checkerFunc{kind: KindDNS, fn: func(ctx context.Context, _ Target) Result {
		deadline, ok := ctx.Deadline()
		if !ok {
			panic("missing checker deadline")
		}
		for time.Now().Before(deadline) {
		}
		panic("deadline panic")
	}}
	runner := NewRunnerWithSupervisor(supervisor, checker)
	outcomes := make(chan indexedOutcome, 1)
	lease := supervisor.acquire()
	terminal := &targetTerminal{
		target: Target{Kind: KindDNS, Address: "panic.example"}, started: time.Now().UTC(),
		parentCtx: context.Background(), runCtx: context.Background(), supervisor: supervisor, lease: lease, outcomes: outcomes,
	}
	go runner.runChecker(context.Background(), time.Millisecond, terminal.target, lease, terminal)
	if got := (<-outcomes).result; got.ErrorCode != "timeout" || got.Status != StatusUnreachable {
		t.Fatalf("panic-at-deadline result=%+v", got)
	}
}

func TestTargetTerminalCompletionDeadlineBoundaries(t *testing.T) {
	boundary := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name        string
		target      Target
		runCtx      context.Context
		checkCtx    context.Context
		completedAt time.Time
		wantCode    string
	}{
		{name: "before checker deadline", target: Target{Kind: KindDNS}, runCtx: context.Background(), checkCtx: deadlineOnlyContext{deadline: boundary}, completedAt: boundary.Add(-time.Nanosecond)},
		{name: "exactly at checker deadline", target: Target{Kind: KindDNS}, runCtx: context.Background(), checkCtx: deadlineOnlyContext{deadline: boundary}, completedAt: boundary, wantCode: "timeout"},
		{name: "after checker deadline", target: Target{Kind: KindDNS}, runCtx: context.Background(), checkCtx: deadlineOnlyContext{deadline: boundary}, completedAt: boundary.Add(time.Nanosecond), wantCode: "timeout"},
		{name: "checker error visible before timestamp", target: Target{Kind: KindDNS}, runCtx: context.Background(), checkCtx: deadlineOnlyContext{deadline: boundary, err: context.DeadlineExceeded}, completedAt: boundary.Add(-time.Hour), wantCode: "timeout"},
		{name: "exactly at report deadline", target: Target{Kind: KindDNS}, runCtx: deadlineOnlyContext{deadline: boundary}, checkCtx: context.Background(), completedAt: boundary, wantCode: "timeout"},
		{name: "report error visible before timestamp", target: Target{Kind: KindDNS}, runCtx: deadlineOnlyContext{deadline: boundary, err: context.DeadlineExceeded}, checkCtx: context.Background(), completedAt: boundary.Add(-time.Hour), wantCode: "timeout"},
		{name: "contexts without deadlines", target: Target{Kind: KindDNS}, runCtx: context.Background(), checkCtx: context.Background(), completedAt: boundary},
		{name: "traceroute ignores checker deadline", target: Target{Kind: KindTraceroute}, runCtx: deadlineOnlyContext{deadline: boundary.Add(time.Hour)}, checkCtx: deadlineOnlyContext{deadline: boundary}, completedAt: boundary},
		{name: "traceroute uses report deadline", target: Target{Kind: KindTraceroute}, runCtx: deadlineOnlyContext{deadline: boundary}, checkCtx: deadlineOnlyContext{deadline: boundary.Add(time.Hour)}, completedAt: boundary, wantCode: "timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			supervisor := mustSupervisor(t, 1)
			terminal := &targetTerminal{target: test.target, started: boundary, parentCtx: context.Background(), runCtx: test.runCtx, supervisor: supervisor}
			outcome, claimed := terminal.complete(Result{Kind: test.target.Kind, Status: StatusHealthy}, test.checkCtx, test.completedAt)
			if !claimed {
				t.Fatal("completion was not claimed")
			}
			if got := outcome.result.ErrorCode; got != test.wantCode {
				t.Fatalf("error_code=%q, want %q; result=%+v", got, test.wantCode, outcome.result)
			}
		})
	}
}

func TestRunnerHealthyCompletionBeforeCheckerDeadlineRemainsHealthy(t *testing.T) {
	checker := checkerFunc{kind: KindDNS, fn: func(_ context.Context, target Target) Result {
		return Result{Kind: target.Kind, Address: target.Address, Status: StatusHealthy}
	}}
	report, err := NewRunnerWithSupervisor(mustSupervisor(t, 1), checker).Run(context.Background(), Request{
		TimeoutMS: int(MinTimeout.Milliseconds()), Targets: []Target{{Kind: KindDNS, Address: "healthy.example"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Results[0]; got.Status != StatusHealthy || got.ErrorCode != "" {
		t.Fatalf("healthy result=%+v", got)
	}
}

func TestRunnerParentCancellationObservedAtReturnOverridesLateHealthyResult(t *testing.T) {
	supervisor := mustSupervisor(t, 1)
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	checker := checkerFunc{kind: KindDNS, fn: func(checkCtx context.Context, target Target) Result {
		close(started)
		<-checkCtx.Done()
		return Result{Kind: target.Kind, Address: "late-healthy.example", Status: StatusHealthy}
	}}
	done := make(chan Report, 1)
	go func() {
		report, _ := NewRunnerWithSupervisor(supervisor, checker).Run(ctx, Request{Targets: []Target{{Kind: KindDNS, Address: "cancel.example"}}})
		done <- report
	}()
	<-started
	cancel()
	if got := (<-done).Results[0]; got.ErrorCode != "cancelled" || got.Address != "cancel.example" {
		t.Fatalf("cancel result=%+v", got)
	}
}

func TestRunnerSupervisorCancellationObservedAtReturnOverridesLateHealthyResult(t *testing.T) {
	supervisor := mustSupervisor(t, 1)
	started := make(chan struct{})
	checker := checkerFunc{kind: KindDNS, fn: func(checkCtx context.Context, target Target) Result {
		close(started)
		<-checkCtx.Done()
		return Result{Kind: target.Kind, Address: "late-healthy.example", Status: StatusHealthy}
	}}
	done := make(chan Report, 1)
	go func() {
		report, _ := NewRunnerWithSupervisor(supervisor, checker).Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "shutdown.example"}}})
		done <- report
	}()
	<-started
	waitCtx, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if remaining := supervisor.Shutdown(waitCtx); remaining < 0 || remaining > 1 {
		t.Fatalf("remaining=%d", remaining)
	}
	if got := (<-done).Results[0]; got.ErrorCode != "cancelled" || got.Address != "shutdown.example" {
		t.Fatalf("shutdown result=%+v", got)
	}
}

func TestRunnerPanicKeepsCheckerPanicUnlessDeadlineAlreadyObservable(t *testing.T) {
	t.Run("checker panic", func(t *testing.T) {
		checker := checkerFunc{kind: KindDNS, fn: func(context.Context, Target) Result { panic("canary") }}
		report, err := NewRunnerWithSupervisor(mustSupervisor(t, 1), checker).Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "panic.example"}}})
		if err != nil {
			t.Fatal(err)
		}
		if got := report.Results[0].ErrorCode; got != "checker_panic" {
			t.Fatalf("error_code=%q", got)
		}
	})
	t.Run("deadline then panic", func(t *testing.T) {
		checker := checkerFunc{kind: KindDNS, fn: func(ctx context.Context, _ Target) Result {
			<-ctx.Done()
			panic("canary")
		}}
		report, err := NewRunnerWithSupervisor(mustSupervisor(t, 1), checker).Run(context.Background(), Request{
			TimeoutMS: int(MinTimeout.Milliseconds()), Targets: []Target{{Kind: KindDNS, Address: "panic.example"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := report.Results[0].ErrorCode; got != "timeout" {
			t.Fatalf("error_code=%q", got)
		}
	})
}

func TestRunnerRunWithIDValidatesAndPreservesOpaqueID(t *testing.T) {
	runner := NewRunner(fakeChecker{kind: KindDNS, status: StatusHealthy})
	req := Request{Targets: []Target{{Kind: KindDNS, Address: "example.test"}}}
	const id = "0123456789abcdef01234567"
	report, err := runner.RunWithID(context.Background(), id, req)
	if err != nil || report.ID != id {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	for _, invalid := range []string{"", "short", "0123456789ABCDEF01234567", "../../etc/passwd........"} {
		if _, err := runner.RunWithID(context.Background(), invalid, req); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}

func TestCheckerSupervisorReleasesSlotBeforeCompletedRunReturns(t *testing.T) {
	supervisor := mustSupervisor(t, 1)
	checker := checkerFunc{kind: KindDNS, fn: func(context.Context, Target) Result {
		return Result{Kind: KindDNS, Status: StatusHealthy}
	}}
	runner := NewRunnerWithSupervisor(supervisor, checker)
	for i := 0; i < 100; i++ {
		if _, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "example.test"}}}); err != nil {
			t.Fatal(err)
		}
		if got := supervisor.Snapshot().Active; got != 0 {
			t.Fatalf("active=%d after completed Run", got)
		}
	}
}

func TestCheckerSupervisorDefaultAndHardMaximum(t *testing.T) {
	defaultSupervisor := mustSupervisor(t, 0)
	if got := defaultSupervisor.Snapshot().Capacity; got != DefaultCheckerCapacity {
		t.Fatalf("default capacity=%d", got)
	}
	if _, err := NewCheckerSupervisor(MaxCheckerCapacity); err != nil {
		t.Fatalf("hard maximum rejected: %v", err)
	}
	if _, err := NewCheckerSupervisor(MaxCheckerCapacity + 1); err == nil {
		t.Fatal("capacity above hard maximum accepted")
	}
}

func TestCheckerSupervisorShutdownIsBoundedAndReportsRemaining(t *testing.T) {
	supervisor := mustSupervisor(t, 1)
	started, release := make(chan struct{}), make(chan struct{})
	checker := checkerFunc{kind: KindDNS, fn: func(context.Context, Target) Result {
		close(started)
		<-release
		return Result{Kind: KindDNS, Status: StatusHealthy}
	}}
	runner := NewRunnerWithSupervisor(supervisor, checker)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _ = runner.Run(ctx, Request{Targets: []Target{{Kind: KindDNS, Address: "stuck.example"}}})
	<-started
	shutdownCtx, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stop()
	if remaining := supervisor.Shutdown(shutdownCtx); remaining != 1 {
		t.Fatalf("remaining=%d", remaining)
	}
	if snapshot := supervisor.Snapshot(); snapshot.Active != 1 || snapshot.Stuck != 1 {
		t.Fatalf("bounded shutdown snapshot=%+v", snapshot)
	}
	report, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindDNS, Address: "new.example"}}})
	if err != nil || report.Results[0].ErrorCode != "checker_capacity_unavailable" {
		t.Fatalf("post-shutdown report=%+v err=%v", report, err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for supervisor.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := supervisor.Snapshot(); snapshot.Active != 0 || snapshot.Stuck != 0 {
		t.Fatalf("released shutdown worker did not recover: %+v", snapshot)
	}
}

func TestCheckerSupervisorShutdownSnapshotSynchronouslyCapturesActiveLeases(t *testing.T) {
	supervisor := mustSupervisor(t, 1)
	lease := supervisor.acquire()
	if lease == nil {
		t.Fatal("active lease was not admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if snapshot := supervisor.ShutdownSnapshot(ctx); snapshot.Active != 1 || snapshot.Stuck != 1 || snapshot.Capacity != 1 {
		t.Fatalf("shutdown snapshot=%+v", snapshot)
	}
	supervisor.release(lease)
	if snapshot := supervisor.Snapshot(); snapshot.Active != 0 || snapshot.Stuck != 0 {
		t.Fatalf("released lease did not clear shutdown accounting: %+v", snapshot)
	}
}

func mustSupervisor(t *testing.T, capacity int) *CheckerSupervisor {
	t.Helper()
	s, err := NewCheckerSupervisor(capacity)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
