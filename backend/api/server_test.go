package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil || report.Status != diagnostic.StatusHealthy {
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
