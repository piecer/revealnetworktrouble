package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/api"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func TestServerStartedLogUsesExactBuildIdentity(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logServerStarted(logger, "127.0.0.1:43210", "0.1.0", "0123456789abcdef0123456789abcdef01234567")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["msg"] != "server started" || record["address"] != "127.0.0.1:43210" || record["version"] != "0.1.0" || record["revision"] != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("startup record=%v", record)
	}
	for _, forbidden := range []string{"build_path", "build_time"} {
		if _, exists := record[forbidden]; exists {
			t.Fatalf("startup record leaked %s: %v", forbidden, record)
		}
	}
}

func testEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestRuntimeBudgetEquationsAndHTTPServerContract(t *testing.T) {
	if headerTimeout != 5*time.Second {
		t.Fatalf("H=%v", headerTimeout)
	}
	if bodyReadAllowance != 30*time.Second {
		t.Fatalf("B=%v", bodyReadAllowance)
	}
	if executionTimeout != diagnostic.MaxRequestBudget {
		t.Fatalf("E=%v diagnostic=%v", executionTimeout, diagnostic.MaxRequestBudget)
	}
	if executionTimeout != 301*time.Second {
		t.Fatalf("E=%v", executionTimeout)
	}
	if responseGrace != 5*time.Second || shutdownMargin != 5*time.Second {
		t.Fatalf("R=%v D=%v", responseGrace, shutdownMargin)
	}
	if readTimeout != headerTimeout+bodyReadAllowance {
		t.Fatalf("read equation=%v", readTimeout)
	}
	if readTimeout != 35*time.Second {
		t.Fatalf("read timeout=%v", readTimeout)
	}
	if writeTimeout != bodyReadAllowance+executionTimeout+responseGrace {
		t.Fatalf("write equation=%v", writeTimeout)
	}
	if writeTimeout != 336*time.Second {
		t.Fatalf("write timeout=%v", writeTimeout)
	}
	if shutdownTimeout != headerTimeout+bodyReadAllowance+executionTimeout+responseGrace+shutdownMargin {
		t.Fatalf("shutdown equation=%v", shutdownTimeout)
	}
	if shutdownTimeout != 346*time.Second {
		t.Fatalf("shutdown timeout=%v", shutdownTimeout)
	}
	if normalClientTimeout != 315*time.Second || normalClientTimeout-(executionTimeout+responseGrace) < 9*time.Second {
		t.Fatalf("client timeout=%v normal margin=%v", normalClientTimeout, normalClientTimeout-(executionTimeout+responseGrace))
	}

	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	server := newHTTPServer("127.0.0.1:0", handler)
	if server.Addr != "127.0.0.1:0" || server.Handler == nil {
		t.Fatalf("server wiring=%+v", server)
	}
	if server.ReadHeaderTimeout != headerTimeout || server.ReadTimeout != readTimeout || server.WriteTimeout != writeTimeout {
		t.Fatalf("server timeouts: header=%v read=%v write=%v", server.ReadHeaderTimeout, server.ReadTimeout, server.WriteTimeout)
	}
	if server.MaxHeaderBytes != 64*1024 {
		t.Fatalf("MaxHeaderBytes=%d", server.MaxHeaderBytes)
	}
}

func TestLoadRuntimeConfigDefaultsToLoopbackTrustedLocal(t *testing.T) {
	config, err := loadRuntimeConfig(testEnvironment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.addr != "127.0.0.1:8080" || config.server.Mode != api.ModeTrustedLocal {
		t.Fatalf("config=%+v", config)
	}
	if config.maxConcurrentChecks != diagnostic.DefaultCheckerCapacity {
		t.Fatalf("default checker capacity=%d", config.maxConcurrentChecks)
	}
	if config.writeTimeout < diagnostic.MaxRequestBudget || config.shutdownTimeout < diagnostic.MaxRequestBudget {
		t.Fatalf("timeouts cut off valid request: write=%v shutdown=%v budget=%v", config.writeTimeout, config.shutdownTimeout, diagnostic.MaxRequestBudget)
	}
}

func TestLoadRuntimeConfigBoundsReportCapacityAtAPIHardLimit(t *testing.T) {
	config, err := loadRuntimeConfig(testEnvironment(map[string]string{
		"CHECKNETWORK_MAX_CONCURRENT_REPORTS": strconv.Itoa(api.MaxConcurrentReportsLimit),
	}))
	if err != nil || config.server.MaxConcurrentReports != api.MaxConcurrentReportsLimit {
		t.Fatalf("hard-limit config=%+v err=%v", config, err)
	}
	if _, err := loadRuntimeConfig(testEnvironment(map[string]string{
		"CHECKNETWORK_MAX_CONCURRENT_REPORTS": strconv.Itoa(api.MaxConcurrentReportsLimit + 1),
	})); err == nil {
		t.Fatalf("accepted report capacity above API hard limit %d", api.MaxConcurrentReportsLimit)
	}
}

func TestLoadRuntimeConfigBoundsCheckerCapacity(t *testing.T) {
	for _, capacity := range []int{1, diagnostic.MaxCheckerCapacity} {
		config, err := loadRuntimeConfig(testEnvironment(map[string]string{
			"CHECKNETWORK_MAX_CONCURRENT_CHECKS": strconv.Itoa(capacity),
		}))
		if err != nil || config.maxConcurrentChecks != capacity {
			t.Fatalf("capacity=%d config=%+v err=%v", capacity, config, err)
		}
	}
	for _, value := range []string{"0", "-1", "not-a-number", strconv.Itoa(diagnostic.MaxCheckerCapacity + 1)} {
		if _, err := loadRuntimeConfig(testEnvironment(map[string]string{"CHECKNETWORK_MAX_CONCURRENT_CHECKS": value})); err == nil {
			t.Fatalf("accepted checker capacity %q", value)
		}
	}
}

func TestLoadRuntimeConfigRejectsIncompletePublicMode(t *testing.T) {
	for _, values := range []map[string]string{
		{"CHECKNETWORK_MODE": "public"},
		{"CHECKNETWORK_MODE": "public", "CHECKNETWORK_API_KEY": "secret"},
		{"CHECKNETWORK_MODE": "public", "CHECKNETWORK_API_KEY": "secret", "CHECKNETWORK_RATE_LIMIT_PER_MINUTE": "0"},
		{"CHECKNETWORK_MODE": "invalid"},
	} {
		if _, err := loadRuntimeConfig(testEnvironment(values)); err == nil {
			t.Fatalf("accepted environment: %+v", values)
		}
	}
}

func TestLoadRuntimeConfigAcceptsExplicitPublicMode(t *testing.T) {
	config, err := loadRuntimeConfig(testEnvironment(map[string]string{
		"CHECKNETWORK_MODE":                   "public",
		"CHECKNETWORK_API_KEY":                "secret",
		"CHECKNETWORK_RATE_LIMIT_PER_MINUTE":  "12",
		"CHECKNETWORK_MAX_CONCURRENT_REPORTS": "3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if config.server.Mode != api.ModePublic || config.server.RateLimitPerMinute != 12 || config.server.MaxConcurrentReports != 3 {
		t.Fatalf("server config=%+v", config.server)
	}
	if config.writeTimeout != writeTimeout || config.shutdownTimeout != shutdownTimeout {
		t.Fatalf("runtime timeouts: write=%v shutdown=%v", config.writeTimeout, config.shutdownTimeout)
	}
}

func TestBuildCheckersAppliesPolicyOnlyInPublicMode(t *testing.T) {
	publicPolicy := diagnostic.NewNetworkPolicy(nil, nil)
	trusted := buildCheckers(api.ModeTrustedLocal, publicPolicy, nil)
	public := buildCheckers(api.ModePublic, publicPolicy, nil)
	if len(trusted) != len(public) || len(public) != 13 {
		t.Fatalf("trusted=%d public=%d", len(trusted), len(public))
	}
	assertPolicy := func(index int, checker diagnostic.Checker, want *diagnostic.NetworkPolicy) {
		t.Helper()
		var got *diagnostic.NetworkPolicy
		switch value := checker.(type) {
		case diagnostic.DNSChecker:
			got = value.Policy
		case diagnostic.TCPChecker:
			got = value.Policy
		case diagnostic.HTTPChecker:
			got = value.Policy
		case diagnostic.HTTPSChecker:
			got = value.Policy
		case diagnostic.TracerouteChecker:
			got = value.Policy
		case diagnostic.ServiceChecker:
			got = value.Policy
		default:
			t.Fatalf("checker %d has unexpected type %T", index, checker)
		}
		if got != want {
			t.Fatalf("checker %d policy=%p want %p", index, got, want)
		}
	}
	for index, checker := range trusted {
		assertPolicy(index, checker, nil)
	}
	for index, checker := range public {
		assertPolicy(index, checker, publicPolicy)
	}
}

type mainCheckerFunc struct {
	calls *atomic.Int32
	fn    func(context.Context, diagnostic.Target) diagnostic.Result
}

func (c mainCheckerFunc) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (c mainCheckerFunc) Check(ctx context.Context, target diagnostic.Target) diagnostic.Result {
	c.calls.Add(1)
	return c.fn(ctx, target)
}

func TestNewProductionRunnerUsesExactInjectedSupervisor(t *testing.T) {
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	if remaining := supervisor.Shutdown(context.Background()); remaining != 0 {
		t.Fatalf("remaining=%d", remaining)
	}
	var calls atomic.Int32
	runner := newProductionRunner(supervisor, mainCheckerFunc{calls: &calls, fn: func(context.Context, diagnostic.Target) diagnostic.Result {
		return diagnostic.Result{Kind: diagnostic.KindDNS, Status: diagnostic.StatusHealthy}
	}})
	report, err := runner.Run(context.Background(), diagnostic.Request{Targets: []diagnostic.Target{{Kind: diagnostic.KindDNS, Address: "injected.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || report.Results[0].ErrorCode != "checker_capacity_unavailable" {
		t.Fatalf("runner did not use injected supervisor: calls=%d result=%+v", calls.Load(), report.Results[0])
	}
}

type shutdownRecorder struct {
	mu    sync.Mutex
	order []string
	err   error
	wait  bool
}

func (r *shutdownRecorder) add(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, event)
}

func (r *shutdownRecorder) Shutdown(ctx context.Context) error {
	r.add("http")
	if r.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return r.err
}

func TestShutdownServiceUsesOneEndToEndDeadline(t *testing.T) {
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	runner := newProductionRunner(supervisor, mainCheckerFunc{calls: &atomic.Int32{}, fn: func(context.Context, diagnostic.Target) diagnostic.Result {
		close(started)
		<-release
		return diagnostic.Result{Kind: diagnostic.KindDNS, Status: diagnostic.StatusHealthy}
	}})
	runDone := make(chan struct{})
	go func() {
		_, _ = runner.Run(context.Background(), diagnostic.Request{Targets: []diagnostic.Target{{Kind: diagnostic.KindDNS, Address: "deadline.example"}}})
		close(runDone)
	}()
	<-started
	defer func() {
		close(release)
		<-runDone
	}()

	timeout := 50 * time.Millisecond
	begin := time.Now()
	operational := newOperationalState(true)
	operational.MarkAccepting()
	err = shutdownService(context.Background(), timeout, operational, &shutdownRecorder{wait: true}, supervisor, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	elapsed := time.Since(begin)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v", err)
	}
	if elapsed >= timeout+35*time.Millisecond {
		t.Fatalf("shutdown used more than one timeout window: elapsed=%v timeout=%v", elapsed, timeout)
	}
	if snapshot := supervisor.Snapshot(); snapshot.Active != 1 || snapshot.Stuck != 1 {
		t.Fatalf("noncooperative checker accounting=%+v", snapshot)
	}
}

func TestShutdownServiceCleansSupervisorAfterHTTPShutdownFailure(t *testing.T) {
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &shutdownRecorder{err: errors.New("http drain failed")}
	started := make(chan struct{})
	var calls atomic.Int32
	runner := newProductionRunner(supervisor, mainCheckerFunc{calls: &calls, fn: func(ctx context.Context, target diagnostic.Target) diagnostic.Result {
		close(started)
		<-ctx.Done()
		recorder.add("supervisor")
		return diagnostic.Result{Kind: target.Kind, Status: diagnostic.StatusHealthy}
	}})
	runDone := make(chan struct{})
	go func() {
		_, _ = runner.Run(context.Background(), diagnostic.Request{Targets: []diagnostic.Target{{Kind: diagnostic.KindDNS, Address: "private-target.example"}}})
		close(runDone)
	}()
	<-started

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	operational := newOperationalState(true)
	operational.MarkAccepting()
	if err := shutdownService(context.Background(), 100*time.Millisecond, operational, recorder, supervisor, logger); !errors.Is(err, recorder.err) {
		t.Fatalf("shutdown error=%v", err)
	}
	<-runDone
	recorder.mu.Lock()
	order := append([]string(nil), recorder.order...)
	recorder.mu.Unlock()
	if len(order) != 2 || order[0] != "http" || order[1] != "supervisor" {
		t.Fatalf("shutdown order=%v", order)
	}
	if snapshot := supervisor.Snapshot(); snapshot.Active != 0 || snapshot.Stuck != 0 {
		t.Fatalf("supervisor not drained: %+v", snapshot)
	}
	logText := logs.String()
	for _, field := range []string{`"active":0`, `"stuck":0`, `"remaining":0`} {
		if !strings.Contains(logText, field) {
			t.Fatalf("shutdown log missing %s: %s", field, logText)
		}
	}
	if strings.Contains(logText, "private-target.example") {
		t.Fatalf("shutdown log exposed target: %s", logText)
	}
}
