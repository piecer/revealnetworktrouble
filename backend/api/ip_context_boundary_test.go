package api

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type contextPanicBody struct{}

func (contextPanicBody) Read([]byte) (int, error) { panic("PRIVATE_BODY_CANARY") }
func (contextPanicBody) Close() error             { return nil }

type ipContextCancelBody struct{ cancel context.CancelFunc }

func (b ipContextCancelBody) Read([]byte) (int, error) {
	b.cancel()
	return 0, errors.New("PRIVATE_READ_CANARY")
}
func (ipContextCancelBody) Close() error { return nil }
func TestIPContextBoundaryPrecedenceAndSharedCapacity(t *testing.T) {
	s, err := newServer(diagnostic.NewRunner(&ipContextChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{Mode: ModePublic, APIKey: "secret", RateLimitPerMinute: 100, MaxConcurrentBodyDecodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	h := newNetHTTPAdapter(s)
	for _, tc := range []struct {
		name, method, path, body, auth string
		key                            apiErrorKey
	}{
		{"auth", "POST", "/api/v1/ip-context", `{"address":"1.1.1.1"}`, "", apiErrorUnauthorized},
		{"method", "GET", "/api/v1/ip-context", "", "Bearer secret", apiErrorMethodNotAllowed},
		{"query", "POST", "/api/v1/ip-context?x=1", `{"address":"1.1.1.1"}`, "Bearer secret", apiErrorInvalidRequest},
		{"empty-query", "POST", "/api/v1/ip-context?", `{"address":"1.1.1.1"}`, "Bearer secret", apiErrorInvalidRequest},
		{"semantic-before-disabled", "POST", "/api/v1/ip-context", `{"address":"127.0.0.1"}`, "Bearer secret", apiErrorInvalidRequest},
		{"disabled", "POST", "/api/v1/ip-context", `{"address":"1.1.1.1"}`, "Bearer secret", apiErrorServerBusy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", tc.auth)
			if tc.name == "auth" {
				req.Body = contextPanicBody{}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != apiErrorDefinitionFor(tc.key).Status || !bytes.Equal(w.Body.Bytes(), marshalAPIError(tc.key)) {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if tc.name == "method" && w.Header().Get("Allow") != "POST, OPTIONS" {
				t.Fatal(w.Header())
			}
		})
	}
	s.bodyDecodes <- struct{}{}
	req := httptest.NewRequest("POST", "/api/v1/ip-context", strings.NewReader(`{"address":"1.1.1.1"}`))
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	<-s.bodyDecodes
	if !bytes.Equal(w.Body.Bytes(), marshalAPIError(apiErrorBodyDecodeCapacity)) {
		t.Fatal(w.Body)
	}
	for _, length := range []int64{-1, 257} {
		req := httptest.NewRequest("POST", "/api/v1/ip-context", strings.NewReader(strings.Repeat(" ", 257)))
		req.ContentLength = length
		req.Header.Set("Authorization", "Bearer secret")
		if length == 257 {
			req.Body = contextPanicBody{}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 413 {
			t.Fatalf("length=%d: %d %s", length, w.Code, w.Body)
		}
	}
	req = httptest.NewRequest("OPTIONS", "/api/v1/ip-context", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	if len(s.contextHandlers) != 0 || len(s.bodyDecodes) != 0 || len(s.admission) != 0 {
		t.Fatal("lease leak")
	}
}
func TestIPContextBodyPanicAndCancelledReadPrivacy(t *testing.T) {
	var logs bytes.Buffer
	s, err := newServer(diagnostic.NewRunner(&ipContextChecker{}), slog.New(slog.NewJSONHandler(&logs, nil)), "test", ServerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	h := newNetHTTPAdapter(s)
	req := httptest.NewRequest("POST", "/api/v1/ip-context", nil)
	req.Body = contextPanicBody{}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !bytes.Equal(w.Body.Bytes(), marshalAPIError(apiErrorInternal)) {
		t.Fatal(w.Body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req = httptest.NewRequest("POST", "/api/v1/ip-context", nil).WithContext(ctx)
	req.Body = ipContextCancelBody{cancel}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Body.Len() != 0 {
		t.Fatal("publication after cancellation")
	}
	if strings.Contains(logs.String(), "CANARY") || len(s.contextHandlers) != 0 || len(s.bodyDecodes) != 0 {
		t.Fatal("privacy/admission leak")
	}
	records := decodeTelemetryLines(t, logs.Bytes())
	last := records[len(records)-1]
	if last["status"] != float64(499) || last["outcome"] != string(TelemetryOutcomeCancel) || last["capacity"] != float64(4) {
		t.Fatalf("cancellation telemetry %v", last)
	}
}

type contextBlockedWriter struct {
	*httptest.ResponseRecorder
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (w *contextBlockedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { w.entered <- struct{}{} })
	<-w.release
	return w.ResponseRecorder.Write(p)
}
func TestIPContextHandlerAndWriterLeasesLastThroughWrite(t *testing.T) {
	service := diagnostic.NewIPContextService(diagnostic.IPContextOptions{Resolver: &contextResolver{}, HTTP: func(context.Context, string) (int, []byte, error) { return 404, nil, nil }})
	s, e := newServer(diagnostic.NewRunner(&ipContextChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{IPContext: service})
	if e != nil {
		t.Fatal(e)
	}
	h := newNetHTTPAdapter(s)
	release, entered, done := make(chan struct{}), make(chan struct{}, 4), make(chan struct{}, 4)
	var once sync.Once
	defer once.Do(func() { close(release) })
	for i := 0; i < 4; i++ {
		go func() {
			w := &contextBlockedWriter{ResponseRecorder: httptest.NewRecorder(), entered: entered, release: release}
			h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/ip-context", strings.NewReader(`{"address":"1.1.1.1"}`)))
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("write not reached")
		}
	}
	if len(s.contextHandlers) != 4 || len(s.responseWrites) != 4 || len(s.admission) != 0 {
		t.Fatal("wrong retained leases")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/ip-context", strings.NewReader(`{"address":"127.0.0.1"}`)))
	if !bytes.Equal(w.Body.Bytes(), marshalAPIError(apiErrorServerBusy)) {
		t.Fatal(w.Body)
	}
	once.Do(func() { close(release) })
	for i := 0; i < 4; i++ {
		<-done
	}
	if len(s.contextHandlers) != 0 || len(s.responseWrites) != 0 {
		t.Fatal("lease leak")
	}
	s.responseWrites <- struct{}{}
	for len(s.responseWrites) < cap(s.responseWrites) {
		s.responseWrites <- struct{}{}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/ip-context", strings.NewReader(`{"address":"1.1.1.1"}`)))
	if !bytes.Equal(w.Body.Bytes(), marshalAPIError(apiErrorWriteCapacity)) {
		t.Fatal(w.Body)
	}
	for len(s.responseWrites) > 0 {
		<-s.responseWrites
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = service.Close(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestIPContextRealSocketBodyReadDeadline(t *testing.T) {
	var logs bytes.Buffer
	h := NewServer(diagnostic.NewRunner(&ipContextChecker{}), slog.New(slog.NewJSONHandler(&logs, nil)), "test", nil)
	srv := httptest.NewServer(h)
	defer srv.Close()
	conn, e := net.Dial("tcp", srv.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	io.WriteString(conn, "POST /api/v1/ip-context HTTP/1.1\r\nHost: fixture\r\nContent-Length: 21\r\n\r\n")
	response, e := http.ReadResponse(bufio.NewReader(conn), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	b, e := io.ReadAll(response.Body)
	if e != nil {
		t.Fatal(e)
	}
	if len(b) != 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("body deadline status=%d elapsed=%v body=%s", response.StatusCode, time.Since(start), b)
	}
	// Go's transport cancels the request on read failure and may synthesize empty
	// 200 headers. The application must publish no context/error after cancellation.
	records := decodeTelemetryLines(t, logs.Bytes())
	if len(records) != 1 || records[0]["status"] != float64(499) || records[0]["response_attempted_bytes"] != float64(0) {
		t.Fatalf("transport-cancel accounting %v", records)
	}
}

type ipContextLiveReadFailure struct{}

func (ipContextLiveReadFailure) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }
func (ipContextLiveReadFailure) Close() error             { return nil }
func TestIPContextLiveBodyDeadlineUsesInvalidJSON(t *testing.T) {
	h := NewServer(diagnostic.NewRunner(&ipContextChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", nil)
	request := httptest.NewRequest("POST", "/api/v1/ip-context", nil)
	request.Body = ipContextLiveReadFailure{}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 400 || !bytes.Equal(w.Body.Bytes(), marshalAPIError(apiErrorInvalidJSON)) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
