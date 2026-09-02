package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
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
	reportWriteTimeout          = 5 * time.Second

	// MaxConcurrentReportsLimit is the audited hard bound for report admission.
	MaxConcurrentReportsLimit = 16
	// MaxConcurrentResponseWritesLimit bounds response-write goroutines and must
	// never exceed the report admission bound.
	MaxConcurrentResponseWritesLimit = 16
	hardMaxConcurrentResponseWrites  = MaxConcurrentResponseWritesLimit
)

var (
	errCompactResponseTooLarge = errors.New("compact response too large")
	errResponseSerialization   = errors.New("response serialization failed")
	errResponseWritePanic      = errors.New("response write panic")
)

type compactReportMarshaler func(diagnostic.Report) ([]byte, error)
type compactTopologyBuilder func(diagnostic.Report, int, bool) diagnostic.CompactTopologyBuildResult

type DeploymentMode string

const (
	ModeTrustedLocal DeploymentMode = "trusted-local"
	ModePublic       DeploymentMode = "public"
)

type ServerConfig struct {
	AllowedOrigins              []string
	MaxConcurrentReports        int
	MaxConcurrentResponseWrites int
	BusyRetryAfter              time.Duration
	Mode                        DeploymentMode
	APIKey                      string
	RateLimitPerMinute          int
	MaxRateLimitClients         int
}

type clientWindow struct {
	started time.Time
	count   int
}

type requestObservationKey struct{}

type countingReadCloser struct {
	io.ReadCloser
	bytes int64
}

func (reader *countingReadCloser) Read(payload []byte) (int, error) {
	count, err := reader.ReadCloser.Read(payload)
	reader.bytes += int64(count)
	return count, err
}

type requestObservation struct {
	started           time.Time
	requestID         string
	reportID          string
	method            string
	route             TelemetryRoute
	requestBody       *countingReadCloser
	status            int
	outcome           TelemetryOutcome
	responseAttempted int64
	responseActual    int64
	runnerDuration    time.Duration
	marshalDuration   time.Duration
	writeDuration     time.Duration
}

func observationFromRequest(request *http.Request) *requestObservation {
	observation, _ := request.Context().Value(requestObservationKey{}).(*requestObservation)
	return observation
}

type Server struct {
	runner         *diagnostic.Runner
	logger         *slog.Logger
	allowedOrigins map[string]struct{}
	version        string
	admission      chan struct{}
	responseWrites chan struct{}
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
	if config.MaxConcurrentReports < 1 || config.MaxConcurrentReports > MaxConcurrentReportsLimit {
		return nil, fmt.Errorf("max concurrent reports must be between 1 and %d", MaxConcurrentReportsLimit)
	}
	if config.MaxConcurrentResponseWrites == 0 {
		config.MaxConcurrentResponseWrites = min(config.MaxConcurrentReports, hardMaxConcurrentResponseWrites)
	}
	if config.MaxConcurrentResponseWrites < 1 || config.MaxConcurrentResponseWrites > hardMaxConcurrentResponseWrites {
		return nil, fmt.Errorf("max concurrent response writes must be between 1 and %d", hardMaxConcurrentResponseWrites)
	}
	if config.BusyRetryAfter <= 0 {
		config.BusyRetryAfter = time.Second
	}
	s := &Server{
		runner: runner, logger: logger, version: version,
		allowedOrigins: make(map[string]struct{}),
		admission:      make(chan struct{}, config.MaxConcurrentReports),
		responseWrites: make(chan struct{}, config.MaxConcurrentResponseWrites),
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

func (s *Server) health(w http.ResponseWriter, request *http.Request) {
	s.writeJSON(w, request, http.StatusOK, TelemetryOutcomeOK, map[string]any{"status": "ok", "version": s.version, "time": time.Now().UTC()})
}

func (s *Server) checks(w http.ResponseWriter, request *http.Request) {
	s.writeJSON(w, request, http.StatusOK, TelemetryOutcomeOK, map[string]any{
		"kinds":          []string{"dns", "tcp", "http", "https", "traceroute", "ssh", "smtp", "submission", "smtps", "imap", "imaps", "pop3", "pop3s"},
		"topology_modes": []string{"full", "compact"},
		"limits": map[string]any{
			"max_targets":                      diagnostic.MaxTargets,
			"max_traceroute_attempts":          diagnostic.MaxTraceAttempts,
			"timeout_ms_min":                   diagnostic.MinTimeout.Milliseconds(),
			"timeout_ms_max":                   diagnostic.MaxTimeout.Milliseconds(),
			"compact_topology_nodes":           diagnostic.CompactTopologyMaxNodes,
			"compact_topology_links":           diagnostic.CompactTopologyMaxLinks,
			"compact_response_bytes_exclusive": diagnostic.CompactTopologyMaxResponseBytes,
			"compact_geo_bundle_bytes":         diagnostic.CompactTopologyMaxGeoBundleBytes,
		},
	})
}

func (s *Server) createReport(w http.ResponseWriter, r *http.Request) {
	observation := observationFromRequest(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req diagnostic.Request
	if err := decoder.Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			s.rejectReport(w, r, http.StatusRequestEntityTooLarge, TelemetryOutcomeRequestTooLarge, "request_too_large", "request body exceeds the size limit")
			return
		}
		s.rejectReport(w, r, http.StatusBadRequest, TelemetryOutcomeInvalidJSON, "invalid_json", "request body must be a valid JSON report request")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if isRequestTooLarge(err) {
			s.rejectReport(w, r, http.StatusRequestEntityTooLarge, TelemetryOutcomeRequestTooLarge, "request_too_large", "request body exceeds the size limit")
			return
		}
		s.rejectReport(w, r, http.StatusBadRequest, TelemetryOutcomeInvalidJSON, "invalid_json", "request body must contain one JSON object")
		return
	}
	if err := s.runner.Validate(req); err != nil {
		s.rejectReport(w, r, http.StatusUnprocessableEntity, TelemetryOutcomeInvalidRequest, "invalid_request", err.Error())
		return
	}
	select {
	case s.admission <- struct{}{}:
		s.emit(r, TelemetryEventReportAdmit, TelemetryOutcomeOK, 0)
	default:
		retrySeconds := int(math.Ceil(s.busyRetryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(1, retrySeconds)))
		s.rejectReport(w, r, http.StatusServiceUnavailable, TelemetryOutcomeServerCapacity, "server_busy", "report capacity is temporarily unavailable")
		return
	}
	admissionHeld := true
	releaseAdmission := func() {
		if admissionHeld {
			<-s.admission
			admissionHeld = false
		}
	}
	defer releaseAdmission()

	s.emit(r, TelemetryEventReportStart, TelemetryOutcomeOK, 0)
	runnerStarted := time.Now()
	report, err := s.runner.RunWithID(r.Context(), observation.reportID, req)
	observation.runnerDuration = time.Since(runnerStarted)
	if err != nil {
		releaseAdmission()
		s.setReportWriteDeadline(w)
		s.writeError(w, r, http.StatusInternalServerError, TelemetryOutcomePanicSafeFailure, "internal_error", "report could not be generated")
		s.emit(r, TelemetryEventReportFinish, observation.outcome, 0)
		return
	}
	if r.Context().Err() != nil {
		releaseAdmission()
		observation.status = 499
		observation.outcome = TelemetryOutcomeCancel
		s.emit(r, TelemetryEventReportCancel, TelemetryOutcomeCancel, 0)
		return
	}
	s.emit(r, TelemetryEventReportComputed, TelemetryOutcomeOK, 0)
	if s.mode == ModePublic && reportHasResultCode(report, "network_policy_blocked") {
		releaseAdmission()
		s.setReportWriteDeadline(w)
		s.emit(r, TelemetryEventReportReject, TelemetryOutcomePolicy, 0)
		s.writeError(w, r, http.StatusUnprocessableEntity, TelemetryOutcomePolicy, "network_policy_blocked", "target is not allowed in public mode")
		return
	}

	marshalStarted := time.Now()
	var payload []byte
	var marshalErr error
	if req.TopologyMode == diagnostic.TopologyModeCompact {
		payload, marshalErr = marshalCompactResponse(report)
	} else {
		payload, marshalErr = marshalFullResponse(report)
	}
	observation.marshalDuration = time.Since(marshalStarted)
	if marshalErr != nil {
		releaseAdmission()
		s.setReportWriteDeadline(w)
		outcome, code, message := TelemetryOutcomeSerialization, "response_serialization_failed", "report response could not be serialized"
		if errors.Is(marshalErr, errCompactResponseTooLarge) {
			outcome, code, message = TelemetryOutcomeCompactSize, "compact_response_too_large", "compact report response exceeds the size limit"
		} else if isFullResponseBudgetError(marshalErr) {
			outcome, code, message = TelemetryOutcomeFullSize, "full_response_too_large", "full report response exceeds the size limit"
		}
		s.writeError(w, r, http.StatusInternalServerError, outcome, code, message)
		s.emit(r, TelemetryEventReportFinish, observation.outcome, 0)
		return
	}

	select {
	case s.responseWrites <- struct{}{}:
	default:
		payload = nil
		releaseAdmission()
		s.setReportWriteDeadline(w)
		s.writeError(w, r, http.StatusServiceUnavailable, TelemetryOutcomeWriteCapacity, "write_capacity_unavailable", "report response write capacity is temporarily unavailable")
		s.emit(r, TelemetryEventReportFinish, observation.outcome, 0)
		return
	}
	releaseAdmission()
	defer func() { <-s.responseWrites }()
	s.setReportWriteDeadline(w)
	finishOutcome := reportFinishOutcome(report)
	s.writeJSONPayload(w, r, http.StatusOK, TelemetryOutcomeOK, payload)
	if observation.outcome == TelemetryOutcomeWriteFailedZero || observation.outcome == TelemetryOutcomeWriteFailedPartial {
		finishOutcome = observation.outcome
	}
	s.emit(r, TelemetryEventReportFinish, finishOutcome, 0)
}

func (s *Server) setReportWriteDeadline(w http.ResponseWriter) {
	defer func() { _ = recover() }()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(reportWriteTimeout))
}

func isFullResponseBudgetError(err error) bool {
	return errors.Is(err, errFullResponseTooLarge) ||
		errors.Is(err, errFullResponseStringLimit) ||
		errors.Is(err, errFullResponseContainerLimit) ||
		errors.Is(err, errFullResponseNodeLimit) ||
		errors.Is(err, errFullResponseDepthLimit) ||
		errors.Is(err, errFullResponseCycle)
}

func isRequestTooLarge(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

func (s *Server) options(w http.ResponseWriter, request *http.Request) {
	s.writeEmpty(w, request, http.StatusNoContent, TelemetryOutcomeOK)
}

func classifyRequest(request *http.Request) (TelemetryRoute, int) {
	path := request.URL.Path
	if request.Method == http.MethodOptions && strings.HasPrefix(path, "/api/v1/") {
		return TelemetryRouteOptions, 0
	}
	switch path {
	case "/api/v1/health":
		if request.Method == http.MethodGet {
			return TelemetryRouteHealth, 0
		}
		return TelemetryRouteUnmatched, http.StatusMethodNotAllowed
	case "/api/v1/checks":
		if request.Method == http.MethodGet {
			return TelemetryRouteChecks, 0
		}
		return TelemetryRouteUnmatched, http.StatusMethodNotAllowed
	case "/api/v1/reports":
		if request.Method == http.MethodPost {
			return TelemetryRouteReports, 0
		}
		return TelemetryRouteUnmatched, http.StatusMethodNotAllowed
	default:
		return TelemetryRouteUnmatched, http.StatusNotFound
	}
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, unmatchedStatus := classifyRequest(r)
		observation := &requestObservation{started: time.Now(), requestID: NewRequestID(), method: telemetryMethod(r.Method), route: route}
		if r.Body != nil {
			counter := &countingReadCloser{ReadCloser: r.Body}
			observation.requestBody = counter
			r.Body = counter
		}
		if route == TelemetryRouteReports {
			observation.reportID = NewReportID()
		}
		r = r.WithContext(context.WithValue(r.Context(), requestObservationKey{}, observation))
		defer func() {
			if observation.status == 0 {
				observation.status = http.StatusInternalServerError
				observation.outcome = TelemetryOutcomePanicSafeFailure
			}
			s.emit(r, TelemetryEventHTTPTerminal, observation.outcome, observation.status)
		}()

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if route == TelemetryRouteReports {
			s.emit(r, TelemetryEventReportSubmit, TelemetryOutcomeOK, 0)
		}
		if s.mode == ModePublic && r.Method != http.MethodOptions {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			providedHash := sha256.Sum256([]byte(provided))
			if provided == r.Header.Get("Authorization") || subtle.ConstantTimeCompare(providedHash[:], s.apiKeyHash[:]) != 1 {
				if route == TelemetryRouteReports {
					s.emit(r, TelemetryEventReportReject, TelemetryOutcomeUnauthorized, 0)
				}
				s.writeError(w, r, http.StatusUnauthorized, TelemetryOutcomeUnauthorized, "unauthorized", "valid API credentials are required")
				return
			}
			if allowed, retryAfter := s.allowClient(clientAddress(r.RemoteAddr), time.Now()); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
				if route == TelemetryRouteReports {
					s.emit(r, TelemetryEventReportReject, TelemetryOutcomeRateLimited, 0)
				}
				s.writeError(w, r, http.StatusTooManyRequests, TelemetryOutcomeRateLimited, "rate_limited", "per-client request limit exceeded")
				return
			}
		}
		if unmatchedStatus != 0 {
			if unmatchedStatus == http.StatusMethodNotAllowed {
				w.Header().Set("Allow", allowedMethodForPath(r.URL.Path))
			}
			s.writeError(w, r, unmatchedStatus, TelemetryOutcomeUnmatched, "unmatched", "route not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func telemetryMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

func allowedMethodForPath(path string) string {
	switch path {
	case "/api/v1/health", "/api/v1/checks":
		return "GET, OPTIONS"
	case "/api/v1/reports":
		return "POST, OPTIONS"
	default:
		return "OPTIONS"
	}
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

func (s *Server) emit(request *http.Request, event TelemetryEvent, outcome TelemetryOutcome, status int) {
	observation := observationFromRequest(request)
	if observation == nil {
		return
	}
	requestBytes := int64(0)
	if observation.requestBody != nil {
		requestBytes = observation.requestBody.bytes
	}
	_ = EmitTelemetry(s.logger, TelemetryRecord{
		Event: event, Outcome: outcome, RequestID: observation.requestID, ReportID: observation.reportID,
		Method: observation.method, Route: observation.route, Status: status,
		Active: int64(len(s.admission)), Capacity: int64(cap(s.admission)), RequestBytes: requestBytes,
		ResponseAttemptedBytes: observation.responseAttempted, ResponseBytes: observation.responseActual,
		Duration: time.Since(observation.started), RunnerDuration: observation.runnerDuration,
		MarshalDuration: observation.marshalDuration, WriteDuration: observation.writeDuration,
	})
}

func (s *Server) rejectReport(w http.ResponseWriter, request *http.Request, status int, outcome TelemetryOutcome, code, message string) {
	s.emit(request, TelemetryEventReportReject, outcome, 0)
	s.writeError(w, request, status, outcome, code, message)
}

func (s *Server) writeEmpty(w http.ResponseWriter, request *http.Request, status int, outcome TelemetryOutcome) {
	observation := observationFromRequest(request)
	observation.status, observation.outcome = status, outcome
	w.WriteHeader(status)
}

func (s *Server) writeJSON(w http.ResponseWriter, request *http.Request, status int, outcome TelemetryOutcome, value any) {
	started := time.Now()
	payload, err := json.Marshal(value)
	observation := observationFromRequest(request)
	observation.marshalDuration += time.Since(started)
	if err != nil {
		status, outcome = http.StatusInternalServerError, TelemetryOutcomeSerialization
		payload = []byte(`{"error":{"code":"response_serialization_failed","message":"report response could not be serialized"}}`)
	}
	payload = append(payload, '\n')
	s.writeJSONPayload(w, request, status, outcome, payload)
}

func (s *Server) writeError(w http.ResponseWriter, request *http.Request, status int, outcome TelemetryOutcome, code, message string) {
	s.writeJSON(w, request, status, outcome, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (s *Server) writeJSONPayload(w http.ResponseWriter, request *http.Request, status int, outcome TelemetryOutcome, payload []byte) {
	observation := observationFromRequest(request)
	observation.status, observation.outcome = status, outcome
	started := time.Now()
	attempted, actual, err := writeJSONPayload(w, status, payload)
	observation.writeDuration += time.Since(started)
	observation.responseAttempted += int64(attempted)
	observation.responseActual += int64(actual)
	if err != nil {
		if actual == 0 {
			observation.outcome = TelemetryOutcomeWriteFailedZero
		} else {
			observation.outcome = TelemetryOutcomeWriteFailedPartial
		}
	}
}

func reportHasResultCode(report diagnostic.Report, code string) bool {
	for _, result := range report.Results {
		if result.ErrorCode == code {
			return true
		}
	}
	return false
}

func reportFinishOutcome(report diagnostic.Report) TelemetryOutcome {
	for _, item := range []struct {
		code    string
		outcome TelemetryOutcome
	}{
		{"checker_panic", TelemetryOutcomePanicSafeFailure},
		{"checker_capacity_unavailable", TelemetryOutcomeCheckerCapacity},
		{"timeout", TelemetryOutcomeTimeout},
	} {
		if reportHasResultCode(report, item.code) {
			return item.outcome
		}
	}
	return TelemetryOutcomeOK
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		payload = []byte(`{"error":{"code":"response_serialization_failed","message":"report response could not be serialized"}}`)
	}
	payload = append(payload, '\n')
	writeJSONPayload(w, status, payload)
}

func writeJSONPayload(w http.ResponseWriter, status int, payload []byte) (attempted, actual int, err error) {
	attempted = len(payload)
	defer func() {
		if recover() != nil {
			// A panicking ResponseWriter cannot report how many bytes it may have
			// committed internally. Account for zero actual bytes conservatively and
			// return only a fixed private sentinel, never the panic value.
			actual = 0
			err = errResponseWritePanic
			return
		}
		actual = max(0, min(actual, attempted))
		if err == nil && actual != attempted {
			err = io.ErrShortWrite
		}
	}()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.WriteHeader(status)
	actual, err = w.Write(payload)
	return attempted, actual, err
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func marshalCompactResponse(report diagnostic.Report) ([]byte, error) {
	return marshalCompactResponseWithBuilder(report, func(value diagnostic.Report) ([]byte, error) {
		// Leave one byte outside this candidate cap for the final newline. A
		// candidate that reaches the cap is still classified as non-fitting by
		// the strict exclusive response check below.
		return marshalClosedResponse(value, diagnostic.CompactTopologyMaxResponseBytes-1, false)
	}, diagnostic.BuildCompactTopologyWithOptions)
}

func marshalCompactResponseForTest(report diagnostic.Report, marshal compactReportMarshaler) ([]byte, error) {
	return marshalCompactResponseWithBuilder(report, marshal, diagnostic.BuildCompactTopologyWithOptions)
}

func marshalCompactResponseWithBuilder(report diagnostic.Report, marshal compactReportMarshaler, buildTopology compactTopologyBuilder) ([]byte, error) {
	build, cached := report.CachedCompactTopologyBuild()
	if !cached {
		build = buildTopology(report, -1, true)
	}
	initial := diagnostic.CloneCompactTopology(build.Topology)
	acceptedTransactions := build.AcceptedTransactions

	marshalTopology := func(topology *diagnostic.CompactTopology, responseLimited, geoLimited bool) ([]byte, bool, error) {
		setCompactTruncationReasons(topology, responseLimited, geoLimited)
		payload, err := marshal(diagnostic.BuildCompactReport(report, topology))
		if err != nil {
			if isFullResponseBudgetError(err) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("%w: %w", errResponseSerialization, err)
		}
		payload = append(payload, '\n')
		return payload, len(payload) < diagnostic.CompactTopologyMaxResponseBytes, nil
	}

	payload, fits, err := marshalTopology(initial, false, false)
	if err != nil {
		return nil, err
	}
	if fits {
		return payload, nil
	}

	geoBundles := compactGeoBundleCount(initial)
	low, high := 1, geoBundles
	var bestGeoPayload []byte
	for low <= high {
		removed := low + (high-low)/2
		candidate := compactTopologyWithoutLastGeoBundles(initial, removed)
		candidatePayload, candidateFits, marshalErr := marshalTopology(candidate, true, true)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if candidateFits {
			bestGeoPayload = candidatePayload
			high = removed - 1
		} else {
			low = removed + 1
		}
	}
	if bestGeoPayload != nil {
		return bestGeoPayload, nil
	}

	// The all-Geo-removed full selection was either probed above and remained
	// oversized, or no Geo was present. Find the greatest accepted transaction
	// count whose rebuilt no-Geo response fits.
	low, high = 0, acceptedTransactions-1
	var bestTransactionPayload []byte
	for low <= high {
		transactionLimit := low + (high-low)/2
		candidateBuild := buildTopology(report, transactionLimit, false)
		candidate := diagnostic.CloneCompactTopology(candidateBuild.Topology)
		candidatePayload, candidateFits, marshalErr := marshalTopology(candidate, true, geoBundles > 0)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if candidateFits {
			bestTransactionPayload = candidatePayload
			low = transactionLimit + 1
		} else {
			high = transactionLimit - 1
		}
	}
	if bestTransactionPayload != nil {
		return bestTransactionPayload, nil
	}
	return nil, errCompactResponseTooLarge
}

func compactGeoBundleCount(topology *diagnostic.CompactTopology) int {
	count := 0
	for _, node := range topology.Nodes {
		if node.Geolocation != nil || node.ASN != nil {
			count++
		}
	}
	return count
}

func compactTopologyWithoutLastGeoBundles(source *diagnostic.CompactTopology, removed int) *diagnostic.CompactTopology {
	candidate := diagnostic.CloneCompactTopology(source)
	for index := len(candidate.Nodes) - 1; index >= 0 && removed > 0; index-- {
		node := &candidate.Nodes[index]
		if node.Geolocation == nil && node.ASN == nil {
			continue
		}
		node.Geolocation = nil
		node.ASN = nil
		candidate.Geo.Included--
		candidate.Geo.Omitted++
		removed--
	}
	return candidate
}

func setCompactTruncationReasons(topology *diagnostic.CompactTopology, responseLimited, geoLimited bool) {
	present := make(map[diagnostic.CompactTruncationReason]bool, len(topology.TruncationReasons)+2)
	for _, reason := range topology.TruncationReasons {
		present[reason] = true
	}
	present[diagnostic.CompactTruncationResponseSize] = present[diagnostic.CompactTruncationResponseSize] || responseLimited
	present[diagnostic.CompactTruncationGeoLimit] = present[diagnostic.CompactTruncationGeoLimit] || geoLimited
	ordered := []diagnostic.CompactTruncationReason{
		diagnostic.CompactTruncationNodeLimit,
		diagnostic.CompactTruncationLinkLimit,
		diagnostic.CompactTruncationResponseSize,
		diagnostic.CompactTruncationGeoLimit,
	}
	topology.TruncationReasons = topology.TruncationReasons[:0]
	for _, reason := range ordered {
		if present[reason] {
			topology.TruncationReasons = append(topology.TruncationReasons, reason)
		}
	}
	topology.Truncated = len(topology.TruncationReasons) > 0
}
