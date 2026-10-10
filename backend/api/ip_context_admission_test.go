package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

type ipContextChecker struct{ calls atomic.Int32 }

func (*ipContextChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (c *ipContextChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	c.calls.Add(1)
	return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusHealthy}
}

func TestIPContextRequestLexicalAdmission(t *testing.T) {
	handler := NewServer(diagnostic.NewRunner(&ipContextChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", nil)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"address":"1.1.1.1","address":"1.1.1.1"}`, 400},
		{`{"address":"1.1.1.1","addr\u0065ss":"1.1.1.1"}`, 400},
		{`{"address":"\ud800"}`, 400},
		{"{\"address\":\"" + string([]byte{255}) + "\"}", 400},
		{`[]`, 422}, {`null`, 422}, {`{"address":null}`, 422},
		{`{"address":"1.1.1.1","x":{"k":0,"k":1}}`, 400},
		{`{"address":"1.1.1.1","x":0,"x":1}`, 400}, {`{"address":1}`, 422},
		{`{"address":"1.1.1.1","extra":0}`, 422}, {`{"address":"2001:4860:4860::8888"}`, 503},
		{`{"address":"2001:4860:4860:0:0:0:0:8888"}`, 422}, {`{"address":"::ffff:1.1.1.1"}`, 422},
		{`{"address":" 1.1.1.1"}`, 422}, {`{"address":"example.org"}`, 422}, {`{"address":"1.1.1.1"} {}`, 400},
		{`{"address":"1.1.1.1"}` + strings.Repeat(" ", 235), 503},
		{`{"address":"1.1.1.1"}` + strings.Repeat(" ", 236), 413},
	} {
		t.Run(tc.body, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/ip-context", strings.NewReader(tc.body)))
			if w.Code != tc.status {
				t.Fatalf("got %d %s want %d", w.Code, w.Body, tc.status)
			}
		})
	}
}

func TestIPContextPrivateAdmission(t *testing.T) {
	checker := &ipContextChecker{}
	handler := NewServer(diagnostic.NewRunner(checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "parent-ip-context", []string{"http://localhost:3000"})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/ip-context", strings.NewReader(`{"address":"127.0.0.1"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:3000")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	if err != nil {
		t.Fatal(err)
	}
	if checker.calls.Load() != 0 {
		t.Fatalf("context lookup reran diagnostics: %d", checker.calls.Load())
	}
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("IP-context private-address admission: got status=%d body=%s; want 422 invalid_request before lookup", response.StatusCode, body)
	}
	if string(body) != "{\"error\":{\"code\":\"invalid_request\",\"message\":\"request is invalid\"}}\n" {
		t.Fatalf("wrong closed error bytes: %q", body)
	}
	if response.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatal("context route escaped CORS")
	}
}
