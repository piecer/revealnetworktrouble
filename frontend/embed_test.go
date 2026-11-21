package frontend

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestEmbeddedAssetsMatchStandaloneProductionInventory(t *testing.T) {
	want := []string{"app.js", "common-prefix.js", "geo-map.js", "index.html", "state.js", "styles.css", "topology-model.js", "topology-presentation.js", "topology-renderer.js", "topology-visualizer.js"}
	entries, err := fs.ReadDir(assets, ".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("embedded inventory=%v want=%v", names, want)
	}
	handler := WithUI(http.NotFoundHandler())
	for _, name := range want {
		t.Run(name, func(t *testing.T) {
			get := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/"+name, nil)
			request.Header.Set("If-Modified-Since", "Thu, 01 Jan 2099 00:00:00 GMT")
			request.Header.Set("If-None-Match", "*")
			handler.ServeHTTP(get, request)
			if get.Code != http.StatusOK || get.Header().Get("Cache-Control") != "no-store" || get.Header().Get("ETag") != "" || get.Header().Get("Last-Modified") != "" {
				t.Fatalf("stable asset incorrectly cached: status=%d headers=%v", get.Code, get.Header())
			}
			if name != "index.html" {
				want, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(get.Body.Bytes(), want) {
					t.Fatal("embedded module differs from standalone production bytes")
				}
			}
			contentType := get.Header().Get("Content-Type")
			if (strings.HasSuffix(name, ".js") && contentType != "text/javascript; charset=utf-8") || (strings.HasSuffix(name, ".css") && contentType != "text/css; charset=utf-8") || (name == "index.html" && contentType != "text/html; charset=utf-8") {
				t.Fatalf("wrong MIME type %q for %s", contentType, name)
			}
			if get.Header().Get("Content-Length") != strconv.Itoa(get.Body.Len()) {
				t.Fatal("wrong asset Content-Length")
			}
			head := httptest.NewRecorder()
			handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/"+name, nil))
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != get.Header().Get("Content-Length") {
				t.Fatalf("HEAD differs from GET: status=%d body=%s", head.Code, head.Body.String())
			}
		})
	}
}

func TestEmbeddedUIHasRestrictiveSecurityHeaders(t *testing.T) {
	handler := WithUI(http.NotFoundHandler())
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions} {
		for _, path := range []string{"/", "/index.html", "/app.js", "/styles.css"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			for name, value := range map[string]string{
				"X-Content-Type-Options": "nosniff",
				"X-Frame-Options":        "DENY",
				"Referrer-Policy":        "no-referrer",
			} {
				if response.Header().Get(name) != value {
					t.Errorf("%s %s: %s=%q want=%q", method, path, name, response.Header().Get(name), value)
				}
			}
			csp := response.Header().Get("Content-Security-Policy")
			for _, directive := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "style-src-attr 'unsafe-inline'", "connect-src 'self'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'", "object-src 'none'"} {
				if !strings.Contains(csp, directive) {
					t.Errorf("%s %s: missing CSP directive %q in %q", method, path, directive, csp)
				}
			}
			if method != http.MethodGet && method != http.MethodHead {
				if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" || response.Body.Len() != 0 {
					t.Errorf("static write method not rejected: %s %s: %d", method, path, response.Code)
				}
			}
		}
	}
}

func TestEmbeddedUILeavesNonAssetRequestsUntouched(t *testing.T) {
	for _, target := range []string{"/api/v1/health", "/api/v1/reports", "/livez", "/readyz", "/package.json", "/embed.go", "/embed_test.go", "/nginx.conf", "/node_modules/anything", "/.git/config", "/missing.js", "/topology", "/%61pp.js", "/%2e%2e/index.html", "//index.html", "/./index.html", "/app.js/extra"} {
		t.Run(target, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, target, nil)
			response := httptest.NewRecorder()
			calls := 0
			handler := WithUI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r != request || len(w.Header()) != 0 {
					t.Error("UI modified the API request or response headers")
				}
				w.WriteHeader(http.StatusTeapot)
			}))
			handler.ServeHTTP(response, request)
			if calls != 1 || response.Code != http.StatusTeapot {
				t.Fatalf("non-asset route intercepted: calls=%d status=%d", calls, response.Code)
			}
		})
	}
}

func TestEmbeddedHTMLNeverInterpolatesHostOrProxyHeaders(t *testing.T) {
	handler := WithUI(http.NotFoundHandler())
	var first []byte
	for _, target := range []string{"http://localhost:8080/", "https://untrusted.example/", "http://[::1]:8090/index.html"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("X-Forwarded-Host", `bad.example"><script>bad()</script>`)
		request.Header.Set("X-Forwarded-Proto", "javascript")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		body := response.Body.Bytes()
		if first == nil {
			first = append([]byte(nil), body...)
		}
		if !bytes.Equal(body, first) || bytes.Contains(body, []byte("untrusted.example")) || bytes.Contains(body, []byte("bad.example")) {
			t.Fatal("host-dependent embedded HTML")
		}
		if !bytes.Contains(body, []byte(`data-api-same-origin="true" readonly`)) || bytes.Contains(body, []byte(`value="http://localhost:9090"`)) {
			t.Fatal("embedded API field is not locked to the page origin")
		}
	}
}
