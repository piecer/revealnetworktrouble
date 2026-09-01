package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

const (
	maxBodyBytes                = 1 << 20
	defaultMaxConcurrentReports = 4
	defaultMaxRateLimitClients  = 4096
)

type DeploymentMode string

const (
	ModeTrustedLocal DeploymentMode = "trusted-local"
	ModePublic       DeploymentMode = "public"
)

type ServerConfig struct {
	AllowedOrigins       []string
	MaxConcurrentReports int
	BusyRetryAfter       time.Duration
	Mode                 DeploymentMode
	APIKey               string
	RateLimitPerMinute   int
	MaxRateLimitClients  int
}

type clientWindow struct {
	started time.Time
	count   int
}

type Server struct {
	runner         *diagnostic.Runner
	logger         *slog.Logger
	allowedOrigins map[string]struct{}
	version        string
	admission      chan struct{}
	busyRetryAfter time.Duration
	mode           DeploymentMode
	apiKeyHash     [sha256.Size]byte
	rateLimit      int
	maxRateClients int
	rateMu         sync.Mutex
	clients        map[string]clientWindow
}

func NewServer(runner *diagnostic.Runner, logger *slog.Logger, version string, allowedOrigins []string) http.Handler {
	handler, err := NewServerWithConfig(runner, logger, version, ServerConfig{AllowedOrigins: allowedOrigins, Mode: ModeTrustedLocal})
	if err != nil {
		panic(err)
	}
	return handler
}

func NewServerWithConfig(runner *diagnostic.Runner, logger *slog.Logger, version string, config ServerConfig) (http.Handler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if config.Mode == "" {
		config.Mode = ModeTrustedLocal
	}
	if config.Mode != ModeTrustedLocal && config.Mode != ModePublic {
		return nil, fmt.Errorf("unsupported deployment mode %q", config.Mode)
	}
	if config.Mode == ModePublic {
		if strings.TrimSpace(config.APIKey) == "" {
			return nil, fmt.Errorf("public mode requires an API key")
		}
		if config.RateLimitPerMinute <= 0 {
			return nil, fmt.Errorf("public mode requires a positive per-client rate limit")
		}
		if config.MaxRateLimitClients == 0 {
			config.MaxRateLimitClients = defaultMaxRateLimitClients
		}
		if config.MaxRateLimitClients < 1 {
			return nil, fmt.Errorf("public mode requires a positive rate-limit client bound")
		}
	}
	if config.MaxConcurrentReports == 0 {
		config.MaxConcurrentReports = defaultMaxConcurrentReports
	}
	if config.MaxConcurrentReports < 1 {
		return nil, fmt.Errorf("max concurrent reports must be positive")
	}
	if config.BusyRetryAfter <= 0 {
		config.BusyRetryAfter = time.Second
	}
	s := &Server{
		runner: runner, logger: logger, version: version,
		allowedOrigins: make(map[string]struct{}),
		admission:      make(chan struct{}, config.MaxConcurrentReports),
		busyRetryAfter: config.BusyRetryAfter,
		mode:           config.Mode,
		apiKeyHash:     sha256.Sum256([]byte(config.APIKey)),
		rateLimit:      config.RateLimitPerMinute,
		maxRateClients: config.MaxRateLimitClients,
		clients:        make(map[string]clientWindow),
	}
	for _, origin := range config.AllowedOrigins {
		s.allowedOrigins[strings.TrimSpace(origin)] = struct{}{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/checks", s.checks)
	mux.HandleFunc("POST /api/v1/reports", s.createReport)
	mux.HandleFunc("OPTIONS /api/v1/", s.options)
	return s.middleware(mux), nil
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.version, "time": time.Now().UTC()})
}

func (s *Server) checks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"kinds":  []string{"dns", "tcp", "http", "https", "traceroute", "ssh", "smtp", "submission", "smtps", "imap", "imaps", "pop3", "pop3s"},
		"limits": map[string]any{"max_targets": diagnostic.MaxTargets, "max_traceroute_attempts": diagnostic.MaxTraceAttempts, "timeout_ms_min": diagnostic.MinTimeout.Milliseconds(), "timeout_ms_max": diagnostic.MaxTimeout.Milliseconds()},
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
	select {
	case s.admission <- struct{}{}:
		defer func() { <-s.admission }()
	default:
		retrySeconds := int(math.Ceil(s.busyRetryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(1, retrySeconds)))
		writeError(w, http.StatusServiceUnavailable, "server_busy", "report capacity is temporarily unavailable")
		return
	}
	report, err := s.runner.Run(r.Context(), req)
	if err != nil {
		s.logger.Error("report failed")
		writeError(w, http.StatusInternalServerError, "internal_error", "report could not be generated")
		return
	}
	if s.mode == ModePublic {
		for _, result := range report.Results {
			if result.ErrorCode == "network_policy_blocked" {
				writeError(w, http.StatusUnprocessableEntity, "network_policy_blocked", "target is not allowed in public mode")
				return
			}
		}
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
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if s.mode == ModePublic && r.Method != http.MethodOptions {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			providedHash := sha256.Sum256([]byte(provided))
			if provided == r.Header.Get("Authorization") || subtle.ConstantTimeCompare(providedHash[:], s.apiKeyHash[:]) != 1 {
				writeError(w, http.StatusUnauthorized, "unauthorized", "valid API credentials are required")
				return
			}
			if allowed, retryAfter := s.allowClient(clientAddress(r.RemoteAddr), time.Now()); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
				writeError(w, http.StatusTooManyRequests, "rate_limited", "per-client request limit exceeded")
				return
			}
		}
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func clientAddress(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
}

func (s *Server) allowClient(client string, now time.Time) (bool, time.Duration) {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	for key, window := range s.clients {
		if now.Sub(window.started) >= time.Minute {
			delete(s.clients, key)
		}
	}
	window, ok := s.clients[client]
	if !ok {
		if len(s.clients) >= s.maxRateClients {
			return false, time.Minute
		}
		s.clients[client] = clientWindow{started: now, count: 1}
		return true, 0
	}
	if window.count >= s.rateLimit {
		return false, time.Minute - now.Sub(window.started)
	}
	window.count++
	s.clients[client] = window
	return true, 0
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
