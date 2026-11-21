package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedWebUIRejectsInvalidConfiguration(t *testing.T) {
	for _, value := range []string{"true", "false", "ON", "yes", "1", "invalid"} {
		if _, err := loadRuntimeConfig(testEnvironment(map[string]string{"CHECKNETWORK_WEB_UI": value})); err == nil || !strings.Contains(err.Error(), "CHECKNETWORK_WEB_UI") {
			t.Errorf("invalid UI mode %q must fail closed with a configuration error, got %v", value, err)
		}
	}
}

func TestEmbeddedWebUIStartupProcess(t *testing.T) {
	// Re-execute the actual process startup without depending on Docker, DNS or traceroute.
	if os.Getenv("CHECKNETWORK_WEB_UI_TEST_CHILD") == "1" {
		os.Exit(runMain())
	}
	for _, mode := range []string{"", "off", "on", "public"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmbeddedWebUIStartupProcess$")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "CHECKNETWORK_") {
					command.Env = append(command.Env, entry)
				}
			}
			uiMode := mode
			if mode == "public" {
				uiMode = "on"
				command.Env = append(command.Env, "CHECKNETWORK_MODE=public", "CHECKNETWORK_API_KEY=test-only-ui-key", "CHECKNETWORK_RATE_LIMIT_PER_MINUTE=100")
			}
			command.Env = append(command.Env, "CHECKNETWORK_WEB_UI_TEST_CHILD=1", "CHECKNETWORK_ADDR=127.0.0.1:0", "CHECKNETWORK_WEB_UI="+uiMode)
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = io.Discard
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cancel(); _ = command.Wait() })
			started := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(output)
				for scanner.Scan() {
					var record struct{ Msg, Address string }
					if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Msg == "server started" {
						started <- "http://" + record.Address
					}
				}
			}()
			var base string
			select {
			case base = <-started:
			case <-ctx.Done():
				t.Fatal("real server did not start before the test deadline")
			}
			client := &http.Client{Timeout: 5 * time.Second}
			get := func(method, path, token string) (int, http.Header, []byte) {
				t.Helper()
				request, err := http.NewRequest(method, base+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if token != "" {
					request.Header.Set("Authorization", "Bearer "+token)
				}
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				return response.StatusCode, response.Header, body
			}
			status, headers, body := get(http.MethodGet, "/", "")
			if mode == "" || mode == "off" {
				if status != http.StatusNotFound || !strings.Contains(headers.Get("Content-Type"), "application/json") {
					t.Fatalf("API-only default changed: status=%d body=%s", status, body)
				}
				return
			}
			if status != http.StatusOK || !bytes.Contains(body, []byte(`data-api-same-origin="true"`)) || bytes.Contains(body, []byte(`value="http://localhost:9090"`)) {
				t.Fatalf("embedded UI unavailable or not same-origin: status=%d body=%.250s", status, body)
			}
			if status, _, head := get(http.MethodHead, "/", ""); status != http.StatusOK || len(head) != 0 {
				t.Fatalf("embedded HEAD status=%d body=%q", status, head)
			}
			for _, path := range []string{"app.js", "common-prefix.js", "geo-map.js", "state.js", "styles.css", "topology-model.js", "topology-presentation.js", "topology-renderer.js", "topology-visualizer.js"} {
				status, headers, body := get(http.MethodGet, "/"+path, "")
				want, err := os.ReadFile("../../frontend/" + path)
				if err != nil {
					t.Fatal(err)
				}
				if status != http.StatusOK || !bytes.Equal(body, want) || headers.Get("X-Content-Type-Options") != "nosniff" {
					t.Fatalf("embedded production asset %s differs: status=%d headers=%v", path, status, headers)
				}
			}
			// Static routing must not convert API errors, probes, or source paths into HTML.
			for _, path := range []string{"package.json", "embed.go", "nginx.conf", "dom.test.js", ".git/config", "missing.js", "topology", "%61pp.js", "app.js/extra", "../go.mod"} {
				if status, _, body := get(http.MethodGet, "/"+path, ""); status == http.StatusOK || bytes.Contains(body, []byte("<!DOCTYPE html>")) {
					t.Errorf("non-public path %q exposed: status=%d body=%.120s", path, status, body)
				}
			}
			if status, _, body := get(http.MethodGet, "/livez", ""); status != http.StatusOK || string(body) != liveBody {
				t.Fatalf("UI intercepted liveness: status=%d body=%s", status, body)
			}
			wantStatus := http.StatusOK
			if mode == "public" {
				wantStatus = http.StatusUnauthorized
			}
			if status, _, body := get(http.MethodGet, "/api/v1/health", ""); status != wantStatus || !json.Valid(body) {
				t.Fatalf("API auth/JSON boundary changed: status=%d body=%s", status, body)
			}
			if mode == "public" {
				if status, _, _ := get(http.MethodGet, "/api/v1/health", "test-only-ui-key"); status != http.StatusOK {
					t.Fatalf("authenticated API unavailable: %d", status)
				}
			}
		})
	}
}
