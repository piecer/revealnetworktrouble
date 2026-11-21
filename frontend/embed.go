// Package frontend serves the optional, embedded Web workspace without a runtime filesystem.
package frontend

import (
	"embed"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Keep the explicit production inventory aligned with the standalone Web image.
//
//go:embed index.html styles.css app.js common-prefix.js state.js topology-model.js topology-renderer.js geo-map.js topology-presentation.js topology-visualizer.js
var assets embed.FS

type asset struct {
	body        []byte
	contentType string
}

// WithUI handles only exact public asset paths. Every other request keeps the
// existing API authentication, error, telemetry and operational-probe boundary.
func WithUI(next http.Handler) http.Handler {
	public := make(map[string]asset)
	entries, err := assets.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		body, err := assets.ReadFile(entry.Name())
		if err != nil {
			panic(err)
		}
		contentType := "text/javascript; charset=utf-8"
		switch path.Ext(entry.Name()) {
		case ".html":
			contentType = "text/html; charset=utf-8"
			const standalone = `id="api-base-url" type="url" value="http://localhost:9090"`
			const sameOrigin = `id="api-base-url" type="url" value="" data-api-same-origin="true" readonly title="내장 UI는 현재 주소의 API에 연결합니다."`
			if strings.Count(string(body), standalone) != 1 {
				panic("embedded UI requires exactly one API address input")
			}
			body = []byte(strings.Replace(string(body), standalone, sameOrigin, 1))
		case ".css":
			contentType = "text/css; charset=utf-8"
		}
		public["/"+entry.Name()] = asset{body: body, contentType: contentType}
	}
	public["/"] = public["/index.html"]
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		value, ok := public[request.URL.Path]
		if !ok || request.URL.EscapedPath() != request.URL.Path {
			next.ServeHTTP(writer, request)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		// Dynamic route colors and tooltip positions use style properties; allow
		// attributes only, never inline scripts or style elements. API fetches stay local.
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; connect-src 'self'; img-src 'self' blob:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'")
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", value.contentType)
		writer.Header().Set("Content-Length", strconv.Itoa(len(value.body)))
		if request.Method == http.MethodHead {
			writer.WriteHeader(http.StatusOK)
			return
		}
		_, _ = writer.Write(value.body)
	})
}
