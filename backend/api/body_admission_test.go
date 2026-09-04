package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func bodyAdmissionHandler(t *testing.T, checker diagnostic.Checker, config ServerConfig) http.Handler {
	t.Helper()
	if config.Mode == "" {
		config.Mode = ModeTrustedLocal
	}
	if config.MaxConcurrentReports == 0 {
		config.MaxConcurrentReports = 1
	}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", config)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func bodyAdmissionErrorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid error response status=%d body=%q: %v", recorder.Code, recorder.Body.String(), err)
	}
	return response.Error.Code
}

func TestBodyDecodeAdmissionConfigurationDefaultsToReportLimitAndHasHardMaximum(t *testing.T) {
	runner := diagnostic.NewRunner(successChecker{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if MaxConcurrentBodyDecodesLimit != 64 {
		t.Fatalf("body-decode hard limit=%d", MaxConcurrentBodyDecodesLimit)
	}
	for _, capacity := range []int{1, MaxConcurrentBodyDecodesLimit} {
		if _, err := NewServerWithConfig(runner, logger, "test", ServerConfig{MaxConcurrentReports: 1, MaxConcurrentBodyDecodes: capacity, Mode: ModeTrustedLocal}); err != nil {
			t.Fatalf("capacity %d rejected: %v", capacity, err)
		}
	}
	for _, capacity := range []int{-1, MaxConcurrentBodyDecodesLimit + 1} {
		if _, err := NewServerWithConfig(runner, logger, "test", ServerConfig{MaxConcurrentReports: 1, MaxConcurrentBodyDecodes: capacity, Mode: ModeTrustedLocal}); err == nil {
			t.Fatalf("capacity %d accepted", capacity)
		}
	}
	configured, err := newServer(diagnostic.NewRunner(successChecker{}), logger, "test", ServerConfig{MaxConcurrentReports: 3, Mode: ModeTrustedLocal})
	if err != nil {
		t.Fatal(err)
	}
	if cap(configured.bodyDecodes) != 3 {
		t.Fatalf("default body decodes=%d want report capacity 3", cap(configured.bodyDecodes))
	}
}

func TestSlowPartialBodyOccupiesSlotAndMalformedReleaseRecovers(t *testing.T) {
	checker := &countingChecker{}
	handler := bodyAdmissionHandler(t, checker, ServerConfig{MaxConcurrentReports: 1, MaxConcurrentBodyDecodes: 1})
	reader, writer := io.Pipe()
	firstRequest := httptest.NewRequest(http.MethodPost, "/api/v1/reports", reader)
	first := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(first, firstRequest)
		close(firstDone)
	}()
	if _, err := io.WriteString(writer, `{"targets":[`); err != nil {
		t.Fatal(err)
	}

	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, reportRequest(context.Background()))
	if busy.Code != http.StatusServiceUnavailable || bodyAdmissionErrorCode(t, busy) != "body_decode_capacity_unavailable" || busy.Body.Len() > 256 {
		t.Fatalf("busy status=%d bytes=%d body=%s", busy.Code, busy.Body.Len(), busy.Body.String())
	}
	if checker.callCount() != 0 {
		t.Fatalf("checker called while body slot saturated: %d", checker.callCount())
	}
	_ = writer.CloseWithError(io.ErrUnexpectedEOF)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("malformed partial body did not finish")
	}
	if first.Code != http.StatusBadRequest {
		t.Fatalf("partial status=%d body=%s", first.Code, first.Body.String())
	}

	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, reportRequest(context.Background()))
	if recovered.Code != http.StatusOK || checker.callCount() != 1 {
		t.Fatalf("body capacity did not recover: status=%d calls=%d body=%s", recovered.Code, checker.callCount(), recovered.Body.String())
	}
}

func TestDecodedRequestBlockedInCheckerDoesNotRetainBodySlot(t *testing.T) {
	checker := blockingChecker{started: make(chan struct{}, 1), release: make(chan struct{})}
	handler := bodyAdmissionHandler(t, checker, ServerConfig{MaxConcurrentReports: 1, MaxConcurrentBodyDecodes: 1})
	first := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(first, reportRequest(context.Background()))
		close(firstDone)
	}()
	<-checker.started

	malformed := httptest.NewRecorder()
	handler.ServeHTTP(malformed, httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(`{`)))
	if malformed.Code != http.StatusBadRequest || bodyAdmissionErrorCode(t, malformed) != "invalid_json" {
		t.Fatalf("decoded request retained body slot: status=%d body=%s", malformed.Code, malformed.Body.String())
	}
	close(checker.release)
	<-firstDone
}

func TestBodyAdmissionAuthRateAndDeclaredOversizeRejectBeforeAcquire(t *testing.T) {
	config := publicServerConfig()
	config.MaxConcurrentBodyDecodes = 1
	config.RateLimitPerMinute = 1
	handler := bodyAdmissionHandler(t, &countingChecker{}, config)

	// Consume this client's one authenticated request without touching a body slot.
	health := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	health.RemoteAddr = "198.51.100.20:1"
	health.Header.Set("Authorization", "Bearer test-secret")
	handler.ServeHTTP(httptest.NewRecorder(), health)

	reader, writer := io.Pipe()
	slow := httptest.NewRequest(http.MethodPost, "/api/v1/reports", reader)
	slow.RemoteAddr = "198.51.100.21:1"
	slow.Header.Set("Authorization", "Bearer test-secret")
	slowDone := make(chan struct{})
	go func() { handler.ServeHTTP(httptest.NewRecorder(), slow); close(slowDone) }()
	_, _ = io.WriteString(writer, `{"targets":[`)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(`{`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("auth failure acquired body slot: status=%d", unauthorized.Code)
	}

	rateRequest := reportRequest(context.Background())
	rateRequest.RemoteAddr = "198.51.100.20:2"
	rateRequest.Header.Set("Authorization", "Bearer test-secret")
	rateLimited := httptest.NewRecorder()
	handler.ServeHTTP(rateLimited, rateRequest)
	if rateLimited.Code != http.StatusTooManyRequests {
		t.Fatalf("rate failure acquired body slot: status=%d body=%s", rateLimited.Code, rateLimited.Body.String())
	}

	oversizedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader("x"))
	oversizedRequest.RemoteAddr = "198.51.100.22:1"
	oversizedRequest.Header.Set("Authorization", "Bearer test-secret")
	oversizedRequest.ContentLength = maxBodyBytes + 1
	oversized := httptest.NewRecorder()
	handler.ServeHTTP(oversized, oversizedRequest)
	if oversized.Code != http.StatusRequestEntityTooLarge || bodyAdmissionErrorCode(t, oversized) != "request_too_large" {
		t.Fatalf("declared oversized request acquired body slot: status=%d body=%s", oversized.Code, oversized.Body.String())
	}

	_ = writer.CloseWithError(io.ErrUnexpectedEOF)
	<-slowDone
}

type contextCancelBody struct {
	ctx     context.Context
	started chan struct{}
	once    sync.Once
}

func (body *contextCancelBody) Read([]byte) (int, error) {
	body.once.Do(func() { close(body.started) })
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}
func (*contextCancelBody) Close() error { return nil }

func TestBodyDecodeCancellationReleasesCapacity(t *testing.T) {
	checker := &countingChecker{}
	handler := bodyAdmissionHandler(t, checker, ServerConfig{MaxConcurrentReports: 1, MaxConcurrentBodyDecodes: 1})
	ctx, cancel := context.WithCancel(context.Background())
	body := &contextCancelBody{ctx: ctx, started: make(chan struct{})}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewReader(nil)).WithContext(ctx)
	request.Body = body
	done := make(chan struct{})
	go func() { handler.ServeHTTP(httptest.NewRecorder(), request); close(done) }()
	<-body.started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled body decode did not return")
	}
	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, reportRequest(context.Background()))
	if recovered.Code != http.StatusOK || checker.callCount() != 1 {
		t.Fatalf("cancelled decode leaked capacity: status=%d calls=%d", recovered.Code, checker.callCount())
	}
}

type panicReadCloser struct{ once sync.Once }

func (reader *panicReadCloser) Read([]byte) (int, error) {
	reader.once.Do(func() { panic("BODY_READ_PANIC_CANARY") })
	return 0, io.EOF
}
func (*panicReadCloser) Close() error { return nil }

func TestBodyDecodePanicIsContainedAsRegisteredInternalErrorAndReleasesCapacity(t *testing.T) {
	checker := &countingChecker{}
	var logs bytes.Buffer
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), telemetryTestLogger(&logs), "test", ServerConfig{
		Mode: ModeTrustedLocal, MaxConcurrentReports: 1, MaxConcurrentBodyDecodes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewReader(nil))
	request.Body = &panicReadCloser{}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != string(marshalAPIError(apiErrorInternal)) {
		t.Fatalf("panic response status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	records := decodeTelemetryLines(t, logs.Bytes())
	if got := telemetryEventNames(records); !reflect.DeepEqual(got, []string{"report_submit", "report_finish", "http_terminal"}) {
		t.Fatalf("panic events=%v logs=%s", got, logs.String())
	}
	for _, record := range records[len(records)-2:] {
		if record["outcome"] != string(TelemetryOutcomePanicSafeFailure) {
			t.Fatalf("panic lifecycle=%v", record)
		}
	}
	if strings.Contains(recorder.Body.String(), "BODY_READ_PANIC_CANARY") || strings.Contains(logs.String(), "BODY_READ_PANIC_CANARY") {
		t.Fatalf("panic value leaked: body=%q logs=%s", recorder.Body.String(), logs.String())
	}

	surplus := httptest.NewRecorder()
	valid := `{"targets":[{"kind":"dns","address":"example.test"}]}`
	handler.ServeHTTP(surplus, httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(valid+` {}`)))
	if surplus.Code != http.StatusBadRequest {
		t.Fatalf("panic leaked slot before surplus JSON: status=%d body=%s", surplus.Code, surplus.Body.String())
	}

	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, reportRequest(context.Background()))
	if recovered.Code != http.StatusOK || checker.callCount() != 1 {
		t.Fatalf("panic leaked body capacity: status=%d calls=%d", recovered.Code, checker.callCount())
	}
}
