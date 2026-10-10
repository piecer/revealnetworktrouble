package api

import (
	"context"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type contextBlockedBody struct {
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (b *contextBlockedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { b.entered <- struct{}{} })
	<-b.release
	return 0, io.EOF
}
func (b *contextBlockedBody) Close() error { return nil }
func TestIPContextFourHandlersIndependentOfReportAdmission(t *testing.T) {
	s, e := newServer(diagnostic.NewRunner(&ipContextChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{MaxConcurrentReports: 1, MaxConcurrentBodyDecodes: 8})
	if e != nil {
		t.Fatal(e)
	}
	h := newNetHTTPAdapter(s)
	s.admission <- struct{}{}
	defer func() { <-s.admission }()
	entered, release, done := make(chan struct{}, 5), make(chan struct{}), make(chan struct{}, 4)
	var once sync.Once
	defer once.Do(func() { close(release) })
	for i := 0; i < 4; i++ {
		go func() {
			req := httptest.NewRequest("POST", "/api/v1/ip-context", nil)
			req.Body = &contextBlockedBody{entered: entered, release: release}
			h.ServeHTTP(httptest.NewRecorder(), req)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("report admission consumed or handler did not start")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/ip-context", strings.NewReader(`{"address":"127.0.0.1"}`)))
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"server_busy"`) {
		t.Errorf("fifth handler: %d %s", w.Code, w.Body)
	}
	once.Do(func() { close(release) })
	for i := 0; i < 4; i++ {
		<-done
	}
	if len(s.admission) != 1 || len(s.bodyDecodes) != 0 {
		t.Fatal("foreign/released admission mismatch")
	}
}

type contextDeadlineRecorder struct {
	*httptest.ResponseRecorder
	read, write time.Time
}

func (w *contextDeadlineRecorder) SetReadDeadline(d time.Time) error  { w.read = d; return nil }
func (w *contextDeadlineRecorder) SetWriteDeadline(d time.Time) error { w.write = d; return nil }
func TestIPContextFixedDeadlines(t *testing.T) {
	h := NewServer(diagnostic.NewRunner(&ipContextChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", nil)
	for _, early := range []bool{false, true} {
		w := &contextDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ip-context", strings.NewReader(`{"address":"1.1.1.1"}`))
		start := time.Now()
		if early {
			ctx, cancel := context.WithDeadline(req.Context(), start.Add(500*time.Millisecond))
			defer cancel()
			req = req.WithContext(ctx)
		}
		h.ServeHTTP(w, req)
		if w.read.IsZero() || w.write.IsZero() {
			t.Fatal("fixed deadlines not installed")
		}
		limit := 8 * time.Second
		if early {
			limit = 500 * time.Millisecond
		}
		if w.write.After(start.Add(limit+20*time.Millisecond)) || w.read.After(start.Add(time.Second+20*time.Millisecond)) {
			t.Fatalf("deadlines extended read=%v write=%v", w.read.Sub(start), w.write.Sub(start))
		}
	}
}
