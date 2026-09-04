package diagnostic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	DefaultTimeout            = 5 * time.Second
	MinTimeout                = 100 * time.Millisecond
	MaxTimeout                = 30 * time.Second
	MaxTargets                = 20
	DefaultTraceAttempts      = 5
	MaxTraceAttempts          = 10
	RequestBudgetGrace        = time.Second
	MaxRequestBudget          = MaxTimeout*MaxTraceAttempts + RequestBudgetGrace
	DefaultCheckerCapacity    = 80
	MaxCheckerCapacity        = 1024
	reportIDEncodedByteLength = 12
)

type attemptTimeoutKey struct{}

func RequestBudget(req Request) time.Duration {
	timeout := DefaultTimeout
	if req.TimeoutMS != 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	longest := timeout
	for _, target := range req.Targets {
		if target.Kind != KindTraceroute {
			continue
		}
		attempts := target.Attempts
		if attempts == 0 {
			attempts = DefaultTraceAttempts
		}
		if candidate := timeout * time.Duration(attempts); candidate > longest {
			longest = candidate
		}
	}
	return longest + RequestBudgetGrace
}

func withAttemptTimeout(ctx context.Context, timeout time.Duration) context.Context {
	return context.WithValue(ctx, attemptTimeoutKey{}, timeout)
}

func attemptTimeout(ctx context.Context) time.Duration {
	if timeout, ok := ctx.Value(attemptTimeoutKey{}).(time.Duration); ok && timeout > 0 {
		return timeout
	}
	return DefaultTimeout
}

type Checker interface {
	Kind() Kind
	Check(context.Context, Target) Result
}

type CheckerSupervisorSnapshot struct {
	Active   int
	Capacity int
	Stuck    int
}

type checkerLease struct {
	stuck bool
	done  bool
}

// CheckerSupervisor owns the process-level checker execution limit and shared
// shutdown cancellation. A lease remains active until the checker really exits.
type CheckerSupervisor struct {
	mu       sync.Mutex
	capacity int
	active   int
	stuck    int
	leases   map[*checkerLease]struct{}
	admit    bool
	ctx      context.Context
	cancel   context.CancelFunc
	drained  chan struct{}
}

func NewCheckerSupervisor(capacity int) (*CheckerSupervisor, error) {
	if capacity == 0 {
		capacity = DefaultCheckerCapacity
	}
	if capacity < 1 || capacity > MaxCheckerCapacity {
		return nil, fmt.Errorf("checker capacity must be between 1 and %d", MaxCheckerCapacity)
	}
	ctx, cancel := context.WithCancel(context.Background())
	drained := make(chan struct{})
	close(drained)
	return &CheckerSupervisor{
		capacity: capacity,
		leases:   make(map[*checkerLease]struct{}, capacity),
		admit:    true,
		ctx:      ctx,
		cancel:   cancel,
		drained:  drained,
	}, nil
}

func (s *CheckerSupervisor) acquire() *checkerLease {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.admit || s.active >= s.capacity {
		return nil
	}
	if s.active == 0 {
		s.drained = make(chan struct{})
	}
	lease := &checkerLease{}
	s.active++
	s.leases[lease] = struct{}{}
	return lease
}

func (s *CheckerSupervisor) release(lease *checkerLease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lease == nil || lease.done {
		return
	}
	lease.done = true
	delete(s.leases, lease)
	if lease.stuck {
		s.stuck--
	}
	s.active--
	if s.active == 0 {
		close(s.drained)
	}
}

func (s *CheckerSupervisor) markStuck(lease *checkerLease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lease == nil || lease.done || lease.stuck {
		return
	}
	lease.stuck = true
	s.stuck++
}

func (s *CheckerSupervisor) Snapshot() CheckerSupervisorSnapshot {
	if s == nil {
		return CheckerSupervisorSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *CheckerSupervisor) snapshotLocked() CheckerSupervisorSnapshot {
	return CheckerSupervisorSnapshot{Active: s.active, Capacity: s.capacity, Stuck: s.stuck}
}

// ShutdownSnapshot permanently stops admission, cancels the shared checker
// context, and waits only until ctx expires or all actual workers return. It
// captures all terminal counts at one moment after the wait; Go cannot forcibly
// terminate workers that have not returned.
func (s *CheckerSupervisor) ShutdownSnapshot(ctx context.Context) CheckerSupervisorSnapshot {
	if s == nil {
		return CheckerSupervisorSnapshot{}
	}
	s.mu.Lock()
	s.admit = false
	for lease := range s.leases {
		if !lease.done && !lease.stuck {
			lease.stuck = true
			s.stuck++
		}
	}
	s.cancel()
	drained := s.drained
	s.mu.Unlock()
	select {
	case <-drained:
	case <-ctx.Done():
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Shutdown is the compatibility form of ShutdownSnapshot. Its return value is
// the number of workers still running at the captured shutdown moment.
func (s *CheckerSupervisor) Shutdown(ctx context.Context) int {
	return s.ShutdownSnapshot(ctx).Active
}

var defaultCheckerSupervisor = func() *CheckerSupervisor {
	s, err := NewCheckerSupervisor(DefaultCheckerCapacity)
	if err != nil {
		panic("invalid default checker capacity")
	}
	return s
}()

type Runner struct {
	checkers   map[Kind]Checker
	now        func() time.Time
	supervisor *CheckerSupervisor
}

func NewRunner(checkers ...Checker) *Runner {
	return newRunner(time.Now, defaultCheckerSupervisor, checkers...)
}

func NewRunnerWithClock(now func() time.Time, checkers ...Checker) *Runner {
	return newRunner(now, defaultCheckerSupervisor, checkers...)
}

func NewRunnerWithSupervisor(supervisor *CheckerSupervisor, checkers ...Checker) *Runner {
	return newRunner(time.Now, supervisor, checkers...)
}

func NewRunnerWithClockAndSupervisor(now func() time.Time, supervisor *CheckerSupervisor, checkers ...Checker) *Runner {
	return newRunner(now, supervisor, checkers...)
}

func newRunner(now func() time.Time, supervisor *CheckerSupervisor, checkers ...Checker) *Runner {
	if now == nil {
		now = time.Now
	}
	if supervisor == nil {
		supervisor = defaultCheckerSupervisor
	}
	r := &Runner{checkers: make(map[Kind]Checker, len(checkers)), now: now, supervisor: supervisor}
	for _, checker := range checkers {
		r.checkers[checker.Kind()] = checker
	}
	return r
}

func (r *Runner) Validate(req Request) error {
	if req.topologyModeSet && req.TopologyMode == "" {
		return fmt.Errorf("topology_mode must be full or compact")
	}
	if req.TopologyMode != "" && req.TopologyMode != TopologyModeFull && req.TopologyMode != TopologyModeCompact {
		return fmt.Errorf("topology_mode must be full or compact")
	}
	if len(req.Targets) < 1 || len(req.Targets) > MaxTargets {
		return fmt.Errorf("targets must contain 1 to %d items", MaxTargets)
	}
	if req.TimeoutMS != 0 && (req.TimeoutMS < int(MinTimeout.Milliseconds()) || req.TimeoutMS > int(MaxTimeout.Milliseconds())) {
		return fmt.Errorf("timeout_ms must be between %d and %d", MinTimeout.Milliseconds(), MaxTimeout.Milliseconds())
	}
	for i, target := range req.Targets {
		if req.TopologyMode == TopologyModeCompact && target.Kind != KindTraceroute {
			return fmt.Errorf("targets[%d].kind must be traceroute when topology_mode is compact", i)
		}
		if strings.TrimSpace(target.Address) == "" {
			return fmt.Errorf("targets[%d].address is required", i)
		}
		if _, ok := r.checkers[target.Kind]; !ok {
			return fmt.Errorf("targets[%d].kind is unsupported", i)
		}
		if target.Attempts < 0 || target.Attempts > MaxTraceAttempts {
			return fmt.Errorf("targets[%d].attempts must be between 1 and %d", i, MaxTraceAttempts)
		}
		if target.Attempts != 0 && target.Kind != KindTraceroute {
			return fmt.Errorf("targets[%d].attempts is only supported for traceroute", i)
		}
		if target.ExpectedStatus != 0 {
			if target.Kind != KindHTTP && target.Kind != KindHTTPS {
				return fmt.Errorf("targets[%d].expected_status is only supported for HTTP and HTTPS", i)
			}
			if target.ExpectedStatus < 100 || target.ExpectedStatus > 599 {
				return fmt.Errorf("targets[%d].expected_status must be between 100 and 599", i)
			}
		}
	}
	return nil
}

type indexedOutcome struct {
	index  int
	result Result
}

// targetTerminal arbitrates the only terminal result a target may publish.
// After execution starts, the first committed event wins. At the instant Check
// returns (or panics), completion observes parent/supervisor cancellation, the
// report deadline, and the checker deadline before it may commit the checker
// result. Cleanup cancellation is deliberately performed only after this
// arbitration so it cannot manufacture a timeout or cancellation result.
type targetTerminal struct {
	once       sync.Once
	index      int
	target     Target
	started    time.Time
	parentCtx  context.Context
	runCtx     context.Context
	supervisor *CheckerSupervisor
	lease      *checkerLease
	outcomes   chan<- indexedOutcome
	stopRun    func() bool
	stopServer func() bool
}

func (t *targetTerminal) claim(result Result, code string) (indexedOutcome, bool) {
	var outcome indexedOutcome
	claimed := false
	t.once.Do(func() {
		if code != "" {
			result = executionResult(t.target, code, t.started)
			t.supervisor.markStuck(t.lease)
		}
		outcome = indexedOutcome{index: t.index, result: result}
		claimed = true
	})
	return outcome, claimed
}

func (t *targetTerminal) commit(result Result, code string) {
	if outcome, claimed := t.claim(result, code); claimed {
		t.outcomes <- outcome
	}
}

func (t *targetTerminal) cutoff() {
	code := "timeout"
	if t.parentCtx.Err() != nil || t.supervisor.ctx.Err() != nil {
		code = "cancelled"
	}
	t.commit(Result{}, code)
}

func (t *targetTerminal) complete(result Result, checkCtx context.Context, completedAt time.Time) (indexedOutcome, bool) {
	// Parent and supervisor cancellation take priority over deadlines. A
	// checker-local deadline is terminal even though the report grace remains.
	code := ""
	switch {
	case t.parentCtx.Err() != nil || t.supervisor.ctx.Err() != nil:
		code = "cancelled"
	case deadlineReached(t.runCtx, completedAt):
		code = "timeout"
	case t.target.Kind != KindTraceroute && deadlineReached(checkCtx, completedAt):
		code = "timeout"
	case errors.Is(checkCtx.Err(), context.Canceled):
		code = "cancelled"
	}
	outcome, claimed := t.claim(result, code)
	if t.stopRun != nil {
		t.stopRun()
	}
	if t.stopServer != nil {
		t.stopServer()
	}
	return outcome, claimed
}

func deadlineReached(ctx context.Context, completedAt time.Time) bool {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !completedAt.Before(deadline)
}

func (r *Runner) Run(ctx context.Context, req Request) (Report, error) {
	return r.RunWithID(ctx, newID(), req)
}

func (r *Runner) RunWithID(ctx context.Context, reportID string, req Request) (Report, error) {
	if !validReportID(reportID) {
		return Report{}, fmt.Errorf("report ID must be 24 lowercase hexadecimal characters")
	}
	if err := r.Validate(req); err != nil {
		return Report{}, err
	}
	timeout := DefaultTimeout
	if req.TimeoutMS != 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	started := time.Now().UTC()
	if ctx.Err() != nil {
		results := make([]Result, len(req.Targets))
		for i, target := range req.Targets {
			results[i] = executionResult(target, "cancelled", started)
		}
		report := Report{ID: reportID, StartedAt: started, Results: results}
		finalizeReport(r, req, &report)
		return report, nil
	}
	runCtx, cancelRun := context.WithTimeout(ctx, RequestBudget(req))
	defer cancelRun()
	outcomes := make(chan indexedOutcome, len(req.Targets))

	for i, target := range req.Targets {
		lease := r.supervisor.acquire()
		if lease == nil {
			outcomes <- indexedOutcome{index: i, result: executionResult(target, "checker_capacity_unavailable", started)}
			continue
		}
		terminal := &targetTerminal{
			index: i, target: target, started: started, parentCtx: ctx,
			runCtx: runCtx, supervisor: r.supervisor, lease: lease, outcomes: outcomes,
		}
		terminal.stopRun = context.AfterFunc(runCtx, terminal.cutoff)
		terminal.stopServer = context.AfterFunc(r.supervisor.ctx, terminal.cutoff)
		go r.runChecker(runCtx, timeout, target, lease, terminal)
	}

	results := make([]Result, len(req.Targets))
	for range req.Targets {
		outcome := <-outcomes
		results[outcome.index] = outcome.result
	}

	report := Report{ID: reportID, StartedAt: started, DurationMS: time.Since(started).Milliseconds(), Results: results}
	finalizeReport(r, req, &report)
	return report, nil
}

func (r *Runner) runChecker(runCtx context.Context, timeout time.Duration, target Target, lease *checkerLease, terminal *targetTerminal) {
	workerCtx, cancelWorker := context.WithCancel(runCtx)
	stopSupervisorCancel := context.AfterFunc(r.supervisor.ctx, cancelWorker)
	checkCtx := context.Context(workerCtx)
	cancelCheck := func() {}
	if target.Kind == KindTraceroute {
		checkCtx = withAttemptTimeout(workerCtx, timeout)
	} else {
		checkCtx, cancelCheck = context.WithTimeout(workerCtx, timeout)
	}
	started := time.Now().UTC()
	result := Result{}
	completedAt := time.Time{}
	defer func() {
		if completedAt.IsZero() {
			completedAt = time.Now()
		}
		if recover() != nil {
			result = Result{Kind: target.Kind, Status: StatusUnreachable, StartedAt: started, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "checker_panic", Message: "checker execution failed"}
		}
		outcome, publish := terminal.complete(result, checkCtx, completedAt)
		cancelCheck()
		stopSupervisorCancel()
		cancelWorker()
		r.supervisor.release(lease)
		if publish {
			terminal.outcomes <- outcome
		}
	}()
	result = r.checkers[target.Kind].Check(checkCtx, target)
	completedAt = time.Now()
}

func executionResult(target Target, code string, started time.Time) Result {
	message := "checker execution capacity unavailable"
	if code == "timeout" {
		message = "checker execution timed out"
	} else if code == "cancelled" {
		message = "checker execution cancelled"
	}
	return Result{Kind: target.Kind, Address: target.Address, Status: StatusUnreachable, StartedAt: started, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: code, Message: message}
}

func finalizeReport(r *Runner, req Request, report *Report) {
	hasDegraded := false
	for _, result := range report.Results {
		report.Summary.Total++
		if result.Status == StatusHealthy {
			report.Summary.Passed++
		} else {
			report.Summary.Failed++
		}
		if result.Status == StatusDegraded {
			hasDegraded = true
		}
	}
	switch {
	case report.Summary.Failed == 0:
		report.Status = StatusHealthy
	case hasDegraded || report.Summary.Passed > 0:
		report.Status = StatusDegraded
	default:
		report.Status = StatusUnreachable
	}
	analysis := Analyze(report.Results, r.now().UTC())
	report.Analysis = &analysis
	if req.TopologyMode == TopologyModeCompact {
		report.SetCompactTopologyBuild(BuildCompactTopologyWithOptions(*report, -1, true))
	}
}

func baseResult(kind Kind, address string, started time.Time, err error) Result {
	result := Result{Kind: kind, Address: address, StartedAt: started, LatencyMS: time.Since(started).Milliseconds(), Status: StatusHealthy}
	if err == nil {
		return result
	}
	result.Status = StatusUnreachable
	result.Message = err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result.ErrorCode = "timeout"
	case errors.Is(err, context.Canceled):
		result.ErrorCode = "cancelled"
	default:
		result.ErrorCode = "connection_failed"
	}
	return result
}

func validReportID(id string) bool {
	if len(id) != reportIDEncodedByteLength*2 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func newID() string {
	b := make([]byte, reportIDEncodedByteLength)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%024x", uint64(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b)
}
