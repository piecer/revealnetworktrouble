package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

type corsDraining struct{}

func (corsDraining) IsDraining() bool { return true }

func TestCORSRetryAfterExposure(t *testing.T) {
	for _, origin := range []string{"https://ui.example", "https://denied.example", "null", ""} {
		for _, scenario := range []struct {
			name, method, path string
			status             int
			draining, rate     bool
		}{
			{"success", http.MethodGet, "/api/v1/health", 200, false, false},
			{"preflight", http.MethodOptions, "/api/v1/reports", 204, false, false},
			{"draining", http.MethodPost, "/api/v1/reports", 503, true, false},
			{"rate-limited", http.MethodPost, "/api/v1/reports", 429, false, true},
		} {
			t.Run(scenario.name+"/"+origin, func(t *testing.T) {
				config := ServerConfig{AllowedOrigins: []string{"https://ui.example"}}
				if scenario.draining {
					config.DrainingProvider = corsDraining{}
				}
				if scenario.rate {
					config.Mode, config.APIKey, config.RateLimitPerMinute = ModePublic, "cors-test-key", 1
				}
				handler, err := NewServerWithConfig(diagnostic.NewRunner(), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", config)
				if err != nil {
					t.Fatal(err)
				}
				request := func() *http.Request {
					req := httptest.NewRequest(scenario.method, scenario.path, strings.NewReader("{}"))
					req.Header.Set("Origin", origin)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer cors-test-key")
					return req
				}
				if scenario.rate {
					handler.ServeHTTP(httptest.NewRecorder(), request())
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, request())
				if rec.Code != scenario.status {
					t.Fatalf("status=%d, want=%d, body=%s", rec.Code, scenario.status, rec.Body.String())
				}
				allowed := origin == "https://ui.example"
				wantExpose, wantOrigin := "", ""
				if allowed {
					wantExpose, wantOrigin = "Retry-After", origin
				}
				if got := rec.Header().Get("Access-Control-Expose-Headers"); got != wantExpose {
					t.Errorf("exposed headers=%q, want=%q", got, wantExpose)
				}
				if got := rec.Header().Get("Access-Control-Allow-Origin"); got != wantOrigin {
					t.Errorf("allowed origin=%q, want=%q", got, wantOrigin)
				}
				if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
					t.Error("credentials policy must not be expanded")
				}
				if allowed && rec.Header().Get("Vary") != "Origin" {
					t.Error("allowed response must vary on Origin")
				}
				if (scenario.draining || scenario.rate) && rec.Header().Get("Retry-After") == "" {
					t.Error("retryable response lost Retry-After")
				}
			})
		}
	}
}

// Opt-in real browser gate: actual Go handlers on owned ephemeral origins,
// native fetch/preflight, and the production Web response parser. No API replay.
func TestCORSRetryAfterBrowser(t *testing.T) {
	if os.Getenv("CHECKNETWORK_RUN_BROWSER_TESTS") != "1" {
		t.Skip("opt in with make web-test-cors-browser and installed Playwright")
	}
	frontend, err := filepath.Abs("../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	ui := httptest.NewServer(http.FileServer(http.Dir(frontend)))
	defer ui.Close()
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>Denied origin probe</title>")
	}))
	defer denied.Close()
	urls := map[string]string{"ui": ui.URL, "denied": denied.URL}
	for _, scenario := range []string{"draining", "rate"} {
		config := ServerConfig{AllowedOrigins: []string{ui.URL}}
		if scenario == "draining" {
			config.DrainingProvider = corsDraining{}
		} else {
			config.Mode, config.APIKey, config.RateLimitPerMinute = ModePublic, "cors-test-key", 1
		}
		handler, err := NewServerWithConfig(diagnostic.NewRunner(), slog.New(slog.NewTextHandler(io.Discard, nil)), "browser-test", config)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		defer server.Close()
		urls[scenario] = server.URL
	}
	encoded, err := json.Marshal(urls)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(frontend, "cors-retry.browser.cjs"), string(encoded))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser gate: %v\n%s", err, output)
	}
	t.Log(string(output))
}
