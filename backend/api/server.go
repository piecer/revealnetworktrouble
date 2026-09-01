package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

const maxBodyBytes = 1 << 20

type Server struct {
	runner         *diagnostic.Runner
	logger         *slog.Logger
	allowedOrigins map[string]struct{}
	version        string
}

func NewServer(runner *diagnostic.Runner, logger *slog.Logger, version string, allowedOrigins []string) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{runner: runner, logger: logger, version: version, allowedOrigins: make(map[string]struct{})}
	for _, origin := range allowedOrigins {
		s.allowedOrigins[strings.TrimSpace(origin)] = struct{}{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/checks", s.checks)
	mux.HandleFunc("POST /api/v1/reports", s.createReport)
	mux.HandleFunc("OPTIONS /api/v1/", s.options)
	return s.middleware(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.version, "time": time.Now().UTC()})
}

func (s *Server) checks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"kinds":  []string{"dns", "tcp", "http"},
		"limits": map[string]any{"max_targets": diagnostic.MaxTargets, "timeout_ms_min": diagnostic.MinTimeout.Milliseconds(), "timeout_ms_max": diagnostic.MaxTimeout.Milliseconds()},
	})
}

func (s *Server) createReport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req diagnostic.Request
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be a valid JSON report request")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return
	}
	if err := s.runner.Validate(req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_request", err.Error())
		return
	}
	report, err := s.runner.Run(r.Context(), req)
	if err != nil {
		s.logger.Error("report failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "report could not be generated")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) options(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func (s *Server) originAllowed(origin string) bool {
	if _, ok := s.allowedOrigins["*"]; ok {
		return true
	}
	_, ok := s.allowedOrigins[origin]
	return ok
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
