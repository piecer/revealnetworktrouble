package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/api"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func operationalRequest(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

func TestOperationalRoutesExposeFixedStartupReadyAndDrainContracts(t *testing.T) {
	var businessCalls atomic.Int32
	business := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		businessCalls.Add(1)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNoContent)
	})
	state := newOperationalState(true)
	handler := newOperationalHandler(state, business)

	assert := func(method, target string, wantStatus int, wantBody string) {
		t.Helper()
		recorder := operationalRequest(t, handler, method, target)
		if recorder.Code != wantStatus || recorder.Body.String() != wantBody {
			t.Fatalf("%s %s: status=%d body=%q", method, target, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("%s %s Content-Type=%q", method, target, recorder.Header().Get("Content-Type"))
		}
	}

	assert(http.MethodGet, "/livez", http.StatusOK, liveBody)
	assert(http.MethodGet, "/readyz", http.StatusServiceUnavailable, startingBody)
	state.MarkAccepting()
	assert(http.MethodGet, "/readyz", http.StatusOK, readyBody)
	state.BeginDrain()
	assert(http.MethodGet, "/livez", http.StatusOK, liveBody)
	assert(http.MethodGet, "/readyz", http.StatusServiceUnavailable, drainingBody)
	assert(http.MethodGet, "/api/v1/health", http.StatusNoContent, "")
	if businessCalls.Load() != 1 {
		t.Fatalf("operational wrapper business calls=%d, want pass-through while draining", businessCalls.Load())
	}
}

func TestOperationalRoutesHEADHasNoBodyAndOtherMethodsAreFixed405(t *testing.T) {
	state := newOperationalState(true)
	state.MarkAccepting()
	handler := newOperationalHandler(state, http.NotFoundHandler())

	for _, route := range []string{"/livez", "/readyz"} {
		head := operationalRequest(t, handler, http.MethodHead, route)
		if head.Code != http.StatusOK || head.Body.Len() != 0 {
			t.Fatalf("HEAD %s: status=%d body=%q", route, head.Code, head.Body.String())
		}
		if head.Header().Get("Content-Type") != "application/json; charset=utf-8" || head.Header().Get("Content-Length") == "" {
			t.Fatalf("HEAD %s headers=%v", route, head.Header())
		}
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodOptions} {
			recorder := operationalRequest(t, handler, method, route)
			if recorder.Code != http.StatusMethodNotAllowed || recorder.Body.String() != "{\"status\":\"method_not_allowed\"}\n" {
				t.Fatalf("%s %s: status=%d body=%q", method, route, recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("Allow") != "GET, HEAD" {
				t.Fatalf("%s %s Allow=%q", method, route, recorder.Header().Get("Allow"))
			}
		}
	}
}

func TestOperationalRoutesRequireExactUnescapedPathAndNeverReflectRawPath(t *testing.T) {
	const canary = "PATH_SECRET_CANARY"
	business := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	state := newOperationalState(true)
	state.MarkAccepting()
	handler := newOperationalHandler(state, business)

	for _, target := range []string{"/livez/", "/readyz/", "/api/v1/livez"} {
		recorder := operationalRequest(t, handler, http.MethodGet, target)
		if recorder.Code != http.StatusTeapot {
			t.Fatalf("non-exact %q intercepted: status=%d body=%q", target, recorder.Code, recorder.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	request.URL = &url.URL{Path: "/livez", RawPath: "/%6civez", RawQuery: "value=" + canary}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("escaped alias intercepted: status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	for _, target := range []string{"/livez?value=" + canary, "/readyz?value=" + canary} {
		recorder := operationalRequest(t, handler, http.MethodGet, target)
		if strings.Contains(recorder.Body.String(), canary) || recorder.Body.Len() > 80 {
			t.Fatalf("operational response reflected request data: %q", recorder.Body.String())
		}
	}
}

func TestReadyzMissingTracerouteUsesOnlyClosedReason(t *testing.T) {
	state := newOperationalState(false)
	state.MarkAccepting()
	handler := newOperationalHandler(state, http.NotFoundHandler())
	recorder := operationalRequest(t, handler, http.MethodGet, "/readyz?path=/secret/bin/traceroute&credential=SECRET")
	if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != tracerouteMissingBody {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || len(body) != 2 {
		t.Fatalf("schema=%v err=%v", body, err)
	}
	for _, forbidden := range []string{"/secret", "SECRET", "error", "path", "target", "provider", "credential", "id"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), strings.ToLower(forbidden)) {
			t.Fatalf("response leaked %q: %q", forbidden, recorder.Body.String())
		}
	}
}

type noNetworkChecker struct{ calls *atomic.Int32 }

func (checker noNetworkChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (checker noNetworkChecker) Check(context.Context, diagnostic.Target) diagnostic.Result {
	checker.calls.Add(1)
	return diagnostic.Result{Kind: diagnostic.KindDNS, Status: diagnostic.StatusHealthy}
}

type blockingOperationalChecker struct {
	started chan struct{}
	release chan struct{}
}

func (checker blockingOperationalChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (checker blockingOperationalChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	close(checker.started)
	<-checker.release
	return diagnostic.Result{Kind: target.Kind, Status: diagnostic.StatusHealthy}
}

type blockingOperationalBody struct {
	reader  *strings.Reader
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (body *blockingOperationalBody) Read(buffer []byte) (int, error) {
	body.once.Do(func() { close(body.started) })
	<-body.release
	return body.reader.Read(buffer)
}

func (*blockingOperationalBody) Close() error { return nil }

func TestPublicOperationalProbesBypassAuthenticationAndRateWindow(t *testing.T) {
	var checkerCalls atomic.Int32
	business, err := api.NewServerWithConfig(
		diagnostic.NewRunner(noNetworkChecker{calls: &checkerCalls}),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"test",
		api.ServerConfig{Mode: api.ModePublic, APIKey: "secret", RateLimitPerMinute: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := newOperationalState(true)
	state.MarkAccepting()
	handler := newOperationalHandler(state, business)

	for probe := 0; probe < 10; probe++ {
		for _, route := range []string{"/livez", "/readyz"} {
			if recorder := operationalRequest(t, handler, http.MethodGet, route); recorder.Code != http.StatusOK {
				t.Fatalf("probe=%d route=%s status=%d body=%q", probe, route, recorder.Code, recorder.Body.String())
			}
		}
	}
	if checkerCalls.Load() != 0 {
		t.Fatalf("operational probes invoked checker/network path %d times", checkerCalls.Load())
	}

	unauthorized := operationalRequest(t, handler, http.MethodGet, "/api/v1/health")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized business status=%d body=%q", unauthorized.Code, unauthorized.Body.String())
	}
	authorizedRequest := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		request.RemoteAddr = "192.0.2.10:1234"
		request.Header.Set("Authorization", "Bearer secret")
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if first := authorizedRequest(); first.Code != http.StatusOK {
		t.Fatalf("first authenticated business status=%d body=%q", first.Code, first.Body.String())
	}
	if second := authorizedRequest(); second.Code != http.StatusTooManyRequests {
		t.Fatalf("second authenticated business status=%d body=%q", second.Code, second.Body.String())
	}
}

func TestOperationalStateDrivesClosedAPIDrainAfterPublicAuthAndRate(t *testing.T) {
	state := newOperationalState(true)
	state.MarkAccepting()
	business, err := api.NewServerWithConfig(
		diagnostic.NewRunner(noNetworkChecker{calls: &atomic.Int32{}}),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"test",
		api.ServerConfig{
			Mode: api.ModePublic, APIKey: "secret", RateLimitPerMinute: 1,
			BusyRetryAfter: 5 * time.Second, DrainingProvider: state,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := newOperationalHandler(state, business)
	state.BeginDrain()

	unauthorized := operationalRequest(t, handler, http.MethodGet, "/api/v1/health")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized draining status=%d body=%q", unauthorized.Code, unauthorized.Body.String())
	}

	authorizedRequest := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		request.RemoteAddr = "192.0.2.25:1234"
		request.Header.Set("Authorization", "Bearer secret")
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	draining := authorizedRequest()
	wantBody := "{\"error\":{\"code\":\"server_draining\",\"message\":\"server is draining and temporarily unavailable\"}}\n"
	if draining.Code != http.StatusServiceUnavailable || draining.Body.String() != wantBody || draining.Header().Get("Retry-After") != "5" {
		t.Fatalf("draining status=%d retry=%q body=%q", draining.Code, draining.Header().Get("Retry-After"), draining.Body.String())
	}
	if limited := authorizedRequest(); limited.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit did not precede drain: status=%d body=%q", limited.Code, limited.Body.String())
	}
	if ready := operationalRequest(t, handler, http.MethodGet, "/readyz"); ready.Code != http.StatusServiceUnavailable || ready.Body.String() != drainingBody {
		t.Fatalf("draining readiness status=%d body=%q", ready.Code, ready.Body.String())
	}
}

func TestOperationalStateSnapshotsAreConcurrentAndIgnoreTemporaryCapacity(t *testing.T) {
	state := newOperationalState(true)
	state.MarkAccepting()
	const workers = 32
	const snapshots = 1000
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			for snapshot := 0; snapshot < snapshots; snapshot++ {
				got := state.Snapshot()
				if got.Status != operationalReady || got.Reason != "" {
					t.Errorf("snapshot=%+v", got)
					return
				}
			}
		}()
	}
	wait.Wait()
}

func TestReadyzDoesNotFlapAtTemporaryReportAndCheckerCapacity(t *testing.T) {
	checker := blockingOperationalChecker{started: make(chan struct{}), release: make(chan struct{})}
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	business, err := api.NewServerWithConfig(
		diagnostic.NewRunnerWithSupervisor(supervisor, checker),
		slog.New(slog.NewTextHandler(io.Discard, nil)), "test",
		api.ServerConfig{Mode: api.ModeTrustedLocal, MaxConcurrentReports: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := newOperationalState(true)
	state.MarkAccepting()
	handler := newOperationalHandler(state, business)

	reportDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(`{"targets":[{"kind":"dns","address":"capacity.example"}]}`))
		handler.ServeHTTP(recorder, request)
		reportDone <- recorder
	}()
	<-checker.started
	if snapshot := supervisor.Snapshot(); snapshot.Active != snapshot.Capacity {
		t.Fatalf("checker was not saturated: %+v", snapshot)
	}
	if ready := operationalRequest(t, handler, http.MethodGet, "/readyz"); ready.Code != http.StatusOK || ready.Body.String() != readyBody {
		t.Fatalf("saturated readiness status=%d body=%q", ready.Code, ready.Body.String())
	}
	close(checker.release)
	if completed := <-reportDone; completed.Code != http.StatusOK {
		t.Fatalf("report status=%d body=%q", completed.Code, completed.Body.String())
	}
}

func TestReadyzDoesNotFlapAtTemporaryBodyDecodeCapacity(t *testing.T) {
	var checkerCalls atomic.Int32
	business, err := api.NewServerWithConfig(
		diagnostic.NewRunner(noNetworkChecker{calls: &checkerCalls}),
		slog.New(slog.NewTextHandler(io.Discard, nil)), "test",
		api.ServerConfig{Mode: api.ModeTrustedLocal, MaxConcurrentBodyDecodes: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := newOperationalState(true)
	state.MarkAccepting()
	handler := newOperationalHandler(state, business)
	body := &blockingOperationalBody{
		reader:  strings.NewReader(`{"targets":[{"kind":"dns","address":"body.example"}]}`),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/reports", body)
	reportDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		reportDone <- recorder
	}()
	<-body.started
	if ready := operationalRequest(t, handler, http.MethodGet, "/readyz"); ready.Code != http.StatusOK || ready.Body.String() != readyBody {
		t.Fatalf("body-saturated readiness status=%d body=%q", ready.Code, ready.Body.String())
	}
	close(body.release)
	if completed := <-reportDone; completed.Code != http.StatusOK {
		t.Fatalf("report status=%d body=%q", completed.Code, completed.Body.String())
	}
	if checkerCalls.Load() != 1 {
		t.Fatalf("checker calls=%d", checkerCalls.Load())
	}
}

func TestReadyzSnapshotIgnoresTemporaryConnectionCapacity(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := newBoundedListener(raw, 1)
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	if listener.Active() != 1 {
		t.Fatalf("listener active=%d", listener.Active())
	}

	state := newOperationalState(true)
	state.MarkAccepting()
	handler := newOperationalHandler(state, http.NotFoundHandler())
	if ready := operationalRequest(t, handler, http.MethodGet, "/readyz"); ready.Code != http.StatusOK || ready.Body.String() != readyBody {
		t.Fatalf("connection-saturated readiness status=%d body=%q", ready.Code, ready.Body.String())
	}
}

type readinessObservingShutdownServer struct {
	state    *operationalState
	snapshot operationalSnapshot
	err      error
}

func (server *readinessObservingShutdownServer) Shutdown(context.Context) error {
	server.snapshot = server.state.Snapshot()
	return server.err
}

func TestShutdownServiceSuccessfulDrainLogsCompletionInfo(t *testing.T) {
	state := newOperationalState(true)
	state.MarkAccepting()
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	err = shutdownService(
		context.Background(),
		time.Second,
		state,
		&readinessObservingShutdownServer{state: state},
		supervisor,
		slog.New(slog.NewJSONHandler(&logs, nil)),
	)
	if err != nil {
		t.Fatalf("shutdown error=%v", err)
	}
	var record map[string]any
	if decodeErr := json.Unmarshal(logs.Bytes(), &record); decodeErr != nil {
		t.Fatalf("shutdown log=%q: %v", logs.String(), decodeErr)
	}
	if record["level"] != "INFO" || record["msg"] != "service shutdown completed" {
		t.Fatalf("shutdown record=%v", record)
	}
	if record["active"] != float64(0) || record["stuck"] != float64(0) || record["remaining"] != float64(0) {
		t.Fatalf("shutdown counts=%v", record)
	}
	if _, exists := record["reason"]; exists {
		t.Fatalf("successful shutdown has failure reason: %v", record)
	}
}

func TestShutdownServiceClosesOperationalAdmissionBeforeHTTPAndCheckerShutdown(t *testing.T) {
	state := newOperationalState(true)
	state.MarkAccepting()
	serverErr := errors.New("http shutdown failed")
	server := &readinessObservingShutdownServer{state: state, err: serverErr}
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	err = shutdownService(context.Background(), time.Second, state, server, supervisor, logger)
	if !errors.Is(err, serverErr) {
		t.Fatalf("shutdown error=%v", err)
	}
	if server.snapshot.Status != operationalNotReady || server.snapshot.Reason != reasonDraining {
		t.Fatalf("HTTP shutdown observed state=%+v", server.snapshot)
	}
	if snapshot := state.Snapshot(); snapshot.Reason != reasonDraining {
		t.Fatalf("final operational state=%+v", snapshot)
	}
}
