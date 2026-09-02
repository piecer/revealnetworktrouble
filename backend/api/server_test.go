package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

type successChecker struct{}

func (successChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (successChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusHealthy}
}

func newTestHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(diagnostic.NewRunner(successChecker{}), logger, "test", []string{"http://localhost:3000"})
}

func TestHealthAndCORS(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
}

func TestChecksAdvertisesHTTPSAndServices(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/checks", nil)
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Kinds []string `json:"kinds"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"https": false, "traceroute": false, "ssh": false, "imaps": false}
	for _, kind := range response.Kinds {
		if _, ok := want[kind]; ok {
			want[kind] = true
		}
	}
	for kind, found := range want {
		if !found {
			t.Errorf("%q missing from kinds: %v", kind, response.Kinds)
		}
	}
}

func TestCreateReport(t *testing.T) {
	body := bytes.NewBufferString(`{"targets":[{"kind":"dns","address":"example.test"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var report diagnostic.Report
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil || report.Status != diagnostic.StatusHealthy || report.Analysis == nil {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestRejectsUnknownField(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewBufferString(`{"targets":[],"secret":true}`))
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

type blockingChecker struct {
	started chan struct{}
	release chan struct{}
}

func (blockingChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (c blockingChecker) Check(ctx context.Context, target diagnostic.Target) diagnostic.Result {
	select {
	case c.started <- struct{}{}:
	case <-ctx.Done():
		return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusUnreachable, ErrorCode: "cancelled"}
	}
	select {
	case <-c.release:
		return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusHealthy}
	case <-ctx.Done():
		return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusUnreachable, ErrorCode: "cancelled"}
	}
}

func reportRequest(ctx context.Context) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewBufferString(`{"targets":[{"kind":"dns","address":"example.test"}]}`))
	return req.WithContext(ctx)
}

func TestReportAdmissionRejectsWhenFullAndRecoversAfterCompletion(t *testing.T) {
	checker := blockingChecker{started: make(chan struct{}, 2), release: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), logger, "test", ServerConfig{
		AllowedOrigins:       []string{"http://localhost:3000"},
		MaxConcurrentReports: 1,
		BusyRetryAfter:       7 * time.Second,
		Mode:                 ModeTrustedLocal,
	})
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.ServeHTTP(first, reportRequest(context.Background()))
	}()
	<-checker.started

	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, reportRequest(context.Background()))
	if busy.Code != http.StatusServiceUnavailable || busy.Header().Get("Retry-After") != "7" {
		t.Fatalf("busy status=%d retry=%q body=%s", busy.Code, busy.Header().Get("Retry-After"), busy.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(busy.Body).Decode(&response); err != nil || response.Error.Code != "server_busy" {
		t.Fatalf("busy response=%+v err=%v", response, err)
	}

	close(checker.release)
	wg.Wait()
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}

	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, reportRequest(context.Background()))
	if recovered.Code != http.StatusOK {
		t.Fatalf("capacity was not released: status=%d body=%s", recovered.Code, recovered.Body.String())
	}
}

func TestReportAdmissionRecoversAfterCancellation(t *testing.T) {
	checker := blockingChecker{started: make(chan struct{}, 2), release: make(chan struct{})}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{
		MaxConcurrentReports: 1,
		BusyRetryAfter:       time.Second,
		Mode:                 ModeTrustedLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(first, reportRequest(ctx))
		close(done)
	}()
	<-checker.started
	cancel()
	<-done

	close(checker.release)
	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, reportRequest(context.Background()))
	if recovered.Code != http.StatusOK {
		t.Fatalf("capacity was not released after cancellation: status=%d body=%s", recovered.Code, recovered.Body.String())
	}
}

func publicServerConfig() ServerConfig {
	return ServerConfig{
		Mode:                 ModePublic,
		APIKey:               "test-secret",
		RateLimitPerMinute:   10,
		MaxConcurrentReports: 1,
	}
}

func TestPublicModeRequiresAPIKeyAndPositiveRateLimit(t *testing.T) {
	runner := diagnostic.NewRunner(successChecker{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, config := range []ServerConfig{
		{Mode: ModePublic, RateLimitPerMinute: 1},
		{Mode: ModePublic, APIKey: "secret"},
		{Mode: ModePublic, APIKey: "secret", RateLimitPerMinute: -1},
	} {
		if _, err := NewServerWithConfig(runner, logger, "test", config); err == nil {
			t.Fatalf("public config %+v was accepted", config)
		}
	}
}

func TestPublicModeAuthenticatesEvenWhenCORSAllowsOrigin(t *testing.T) {
	config := publicServerConfig()
	config.AllowedOrigins = []string{"https://ui.example"}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(successChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", config)
	if err != nil {
		t.Fatal(err)
	}
	for _, authorization := range []string{"", "Bearer wrong"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set("Origin", "https://ui.example")
		req.Header.Set("Authorization", authorization)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("Access-Control-Allow-Origin") != "https://ui.example" {
			t.Fatalf("auth=%q status=%d cors=%q body=%s", authorization, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"), rec.Body.String())
		}
		var response struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&response)
		if response.Error.Code != "unauthorized" {
			t.Fatalf("response=%+v", response)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPublicModeRateLimitsEachClient(t *testing.T) {
	config := publicServerConfig()
	config.RateLimitPerMinute = 1
	handler, err := NewServerWithConfig(diagnostic.NewRunner(successChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", config)
	if err != nil {
		t.Fatal(err)
	}
	request := func(remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = remote
		req.Header.Set("Authorization", "Bearer test-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if got := request("198.51.100.10:1234"); got.Code != http.StatusOK {
		t.Fatalf("first status=%d", got.Code)
	}
	limited := request("198.51.100.10:5678")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("limited status=%d retry=%q body=%s", limited.Code, limited.Header().Get("Retry-After"), limited.Body.String())
	}
	if got := request("198.51.100.11:1234"); got.Code != http.StatusOK {
		t.Fatalf("other client status=%d", got.Code)
	}
}

func TestPublicModeBoundsRateLimitClientCardinality(t *testing.T) {
	config := publicServerConfig()
	config.MaxRateLimitClients = 1
	handler, err := NewServerWithConfig(diagnostic.NewRunner(successChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", config)
	if err != nil {
		t.Fatal(err)
	}
	request := func(remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = remote
		req.Header.Set("Authorization", "Bearer test-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if got := request("[2001:db8::1]:1234"); got.Code != http.StatusOK {
		t.Fatalf("first status=%d", got.Code)
	}
	if got := request("[2001:db8::2]:1234"); got.Code != http.StatusTooManyRequests {
		t.Fatalf("new client bypassed cardinality bound: status=%d body=%s", got.Code, got.Body.String())
	}
}

type policyBlockedChecker struct{}

func (policyBlockedChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (policyBlockedChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusUnreachable, ErrorCode: "network_policy_blocked", Message: "target is not allowed in public mode"}
}

func TestPublicPolicyBlockReturnsStablePrivacySafe422(t *testing.T) {
	var logs bytes.Buffer
	handler, err := NewServerWithConfig(diagnostic.NewRunner(policyBlockedChecker{}), slog.New(slog.NewTextHandler(&logs, nil)), "test", publicServerConfig())
	if err != nil {
		t.Fatal(err)
	}
	req := reportRequest(context.Background())
	req.URL.RawQuery = "token=query-secret"
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || bytes.Contains(rec.Body.Bytes(), []byte("example.test")) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{"example.test", "query-secret", "test-secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs leaked %q: %s", secret, logs.String())
		}
	}
	var response struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&response)
	if response.Error.Code != "network_policy_blocked" || response.Error.Message != "target is not allowed in public mode" {
		t.Fatalf("response=%+v", response)
	}
}
