package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func TestPublicProxyHeadersCannotBypassAuthenticationOrRateLimit(t *testing.T) {
	config := publicServerConfig()
	config.RateLimitPerMinute = 1
	config.AllowedOrigins = []string{"https://ui.example"}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(successChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", config)
	if err != nil {
		t.Fatal(err)
	}
	request := func(remote, forwarded string, authorized bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = remote
		// Caller-supplied proxy identity and an allowed Origin confer no trust.
		req.Header.Set("Origin", "https://ui.example")
		req.Header.Set("X-Forwarded-For", forwarded)
		req.Header.Set("X-Real-IP", forwarded)
		req.Header.Set("Forwarded", "for="+forwarded+";proto=https")
		if authorized {
			req.Header.Set("Authorization", "Bearer "+config.APIKey)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if got := request("192.0.2.10:1234", "198.51.100.1", false); got.Code != http.StatusUnauthorized {
		t.Fatalf("proxy headers bypassed authentication: %d", got.Code)
	}
	if got := request("192.0.2.10:1234", "198.51.100.1", true); got.Code != http.StatusOK {
		t.Fatalf("authorized request failed or unauthenticated request consumed quota: %d", got.Code)
	}
	got := request("192.0.2.10:5678", "198.51.100.2", true)
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("changing forwarded identity reset the direct-peer quota: status=%d", got.Code)
	}
	if got := request("192.0.2.11:1234", "198.51.100.2", true); got.Code != http.StatusOK {
		t.Fatalf("different direct peer should have independent quota: %d", got.Code)
	}
}
