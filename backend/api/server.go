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
	"net"
	"net/http"
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
	// MaxConcurrentBodyDecodesLimit is the audited hard bound for authenticated
	// request bodies being decoded concurrently.
	MaxConcurrentBodyDecodesLimit = 64
	// MaxConcurrentResponseWritesLimit bounds response-write goroutines and must
	// never exceed the report admission bound.
	MaxConcurrentResponseWritesLimit = 16
	hardMaxConcurrentResponseWrites  = MaxConcurrentResponseWritesLimit
)

var (
	errCompactResponseTooLarge = errors.New("compact response too large")
	errResponseSerialization   = errors.New("response serialization failed")
)

type compactReportMarshaler func(diagnostic.Report) ([]byte, error)
type compactTopologyBuilder func(diagnostic.Report, int, bool) diagnostic.CompactTopologyBuildResult

type DeploymentMode string

const (
	ModeTrustedLocal DeploymentMode = "trusted-local"
	ModePublic       DeploymentMode = "public"
)

// DrainingProvider supplies process-owned admission state. Implementations must
// be safe for concurrent request and shutdown access.
type DrainingProvider interface {
	IsDraining() bool
}

type ServerConfig struct {
	AllowedOrigins              []string
	MaxConcurrentReports        int
	MaxConcurrentBodyDecodes    int
	MaxConcurrentResponseWrites int
	BusyRetryAfter              time.Duration
	Mode                        DeploymentMode
	APIKey                      string
	RateLimitPerMinute          int
	MaxRateLimitClients         int
	Revision                    string
	DrainingProvider            DrainingProvider
}

// HealthResponse identifies the exact application build serving the request.
type HealthResponse struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

type checksResponse struct {
	Kinds         []string     `json:"kinds"`
	TopologyModes []string     `json:"topology_modes"`
	Limits        checksLimits `json:"limits"`
}

type checksLimits struct {
	MaxTargets                    int   `json:"max_targets"`
	MaxTracerouteAttempts         int   `json:"max_traceroute_attempts"`
	TimeoutMSMin                  int64 `json:"timeout_ms_min"`
	TimeoutMSMax                  int64 `json:"timeout_ms_max"`
	CompactTopologyNodes          int   `json:"compact_topology_nodes"`
	CompactTopologyLinks          int   `json:"compact_topology_links"`
	CompactResponseBytesExclusive int   `json:"compact_response_bytes_exclusive"`
	CompactGeoBundleBytes         int   `json:"compact_geo_bundle_bytes"`
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
	reportTerminal    bool
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
	revision       string
	admission      chan struct{}
	bodyDecodes    chan struct{}
	responseWrites chan struct{}
	busyRetryAfter time.Duration
	mode           DeploymentMode
	apiKeyHash     [sha256.Size]byte
	rateLimit      int
	maxRateClients int
	rateMu         sync.Mutex
	clients        map[string]clientWindow
	draining       DrainingProvider
}

func NewServer(runner *diagnostic.Runner, logger *slog.Logger, version string, allowedOrigins []string) http.Handler {
	handler, err := NewServerWithConfig(runner, logger, version, ServerConfig{AllowedOrigins: allowedOrigins, Mode: ModeTrustedLocal})
	if err != nil {
		panic(err)
	}
	return handler
}

func NewServerWithConfig(runner *diagnostic.Runner, logger *slog.Logger, version string, config ServerConfig) (http.Handler, error) {
	server, err := newServer(runner, logger, version, config)
	if err != nil {
		return nil, err
	}
	return newNetHTTPAdapter(server), nil
}

func newServer(runner *diagnostic.Runner, logger *slog.Logger, version string, config ServerConfig) (*Server, error) {
	if runner == nil {
		return nil, fmt.Errorf("diagnostic runner is required")
	}
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
	if config.MaxConcurrentBodyDecodes == 0 {
		config.MaxConcurrentBodyDecodes = config.MaxConcurrentReports
	}
	if config.MaxConcurrentBodyDecodes < 1 || config.MaxConcurrentBodyDecodes > MaxConcurrentBodyDecodesLimit {
		return nil, fmt.Errorf("max concurrent body decodes must be between 1 and %d", MaxConcurrentBodyDecodesLimit)
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
	if config.Revision == "" {
		config.Revision = "dev"
	}
	server := &Server{
		runner: runner, logger: logger, version: version, revision: config.Revision,
		allowedOrigins: make(map[string]struct{}),
		admission:      make(chan struct{}, config.MaxConcurrentReports),
		bodyDecodes:    make(chan struct{}, config.MaxConcurrentBodyDecodes),
		responseWrites: make(chan struct{}, config.MaxConcurrentResponseWrites),
		busyRetryAfter: config.BusyRetryAfter,
		mode:           config.Mode,
		apiKeyHash:     sha256.Sum256([]byte(config.APIKey)),
		rateLimit:      config.RateLimitPerMinute,
		maxRateClients: config.MaxRateLimitClients,
		clients:        make(map[string]clientWindow),
		draining:       config.DrainingProvider,
	}
	for _, origin := range config.AllowedOrigins {
		server.allowedOrigins[strings.TrimSpace(origin)] = struct{}{}
	}
	return server, nil
}

func (s *Server) health(response responder, _ *http.Request) {
	response.writeHealth(HealthResponse{Status: "ok", Version: s.version, Revision: s.revision})
}

func (s *Server) checks(response responder, _ *http.Request) {
	response.writeChecks(checksResponse{
		Kinds:         []string{"dns", "tcp", "http", "https", "traceroute", "ssh", "smtp", "submission", "smtps", "imap", "imaps", "pop3", "pop3s"},
		TopologyModes: []string{"full", "compact"},
		Limits: checksLimits{
			MaxTargets:                    diagnostic.MaxTargets,
			MaxTracerouteAttempts:         diagnostic.MaxTraceAttempts,
			TimeoutMSMin:                  diagnostic.MinTimeout.Milliseconds(),
			TimeoutMSMax:                  diagnostic.MaxTimeout.Milliseconds(),
			CompactTopologyNodes:          diagnostic.CompactTopologyMaxNodes,
			CompactTopologyLinks:          diagnostic.CompactTopologyMaxLinks,
			CompactResponseBytesExclusive: diagnostic.CompactTopologyMaxResponseBytes,
			CompactGeoBundleBytes:         diagnostic.CompactTopologyMaxGeoBundleBytes,
		},
	})
}

type reportDecodeFailure struct{ key apiErrorKey }

func (s *Server) decodeReportRequest(response responder, request *http.Request) (diagnostic.Request, bool) {
	if request.ContentLength > maxBodyBytes {
		s.rejectReport(response, request, apiErrorRequestTooLarge, apiErrorMetadata{})
		return diagnostic.Request{}, false
	}
	select {
	case s.bodyDecodes <- struct{}{}:
	default:
		s.rejectReport(response, request, apiErrorBodyDecodeCapacity, apiErrorMetadata{RetryAfter: s.busyRetryAfter})
		return diagnostic.Request{}, false
	}

	var decoded diagnostic.Request
	var failure *reportDecodeFailure
	func() {
		defer func() { <-s.bodyDecodes }()
		response.limitRequestBody(request, maxBodyBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&decoded); err != nil {
			if isRequestTooLarge(err) {
				failure = &reportDecodeFailure{key: apiErrorRequestTooLarge}
				return
			}
			failure = &reportDecodeFailure{key: apiErrorInvalidJSON}
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if isRequestTooLarge(err) {
				failure = &reportDecodeFailure{key: apiErrorRequestTooLarge}
				return
			}
			failure = &reportDecodeFailure{key: apiErrorInvalidJSON}
		}
	}()
	if failure != nil {
		s.rejectReport(response, request, failure.key, apiErrorMetadata{})
		return diagnostic.Request{}, false
	}
	return decoded, true
}

func (s *Server) createReport(response responder, request *http.Request) {
	observation := observationFromRequest(request)
	decoded, ok := s.decodeReportRequest(response, request)
	if !ok {
		return
	}
	if err := s.runner.Validate(decoded); err != nil {
		s.rejectReport(response, request, apiErrorInvalidRequest, apiErrorMetadata{})
		return
	}
	select {
	case s.admission <- struct{}{}:
		s.emit(request, TelemetryEventReportAdmit, TelemetryOutcomeOK, 0)
	default:
		s.rejectReport(response, request, apiErrorServerBusy, apiErrorMetadata{RetryAfter: s.busyRetryAfter})
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

	s.emit(request, TelemetryEventReportStart, TelemetryOutcomeOK, 0)
	runnerStarted := time.Now()
	report, err := s.runner.RunWithID(request.Context(), observation.reportID, decoded)
	observation.runnerDuration = time.Since(runnerStarted)
	if err != nil {
		releaseAdmission()
		response.setReportWriteDeadline()
		response.writeAPIError(apiErrorInternal, apiErrorMetadata{})
		s.emit(request, TelemetryEventReportFinish, observation.outcome, 0)
		return
	}
	if request.Context().Err() != nil {
		releaseAdmission()
		observation.status = 499
		observation.outcome = TelemetryOutcomeCancel
		s.emit(request, TelemetryEventReportCancel, TelemetryOutcomeCancel, 0)
		return
	}
	s.emit(request, TelemetryEventReportComputed, TelemetryOutcomeOK, 0)
	if s.mode == ModePublic && reportHasResultCode(report, "network_policy_blocked") {
		releaseAdmission()
		response.setReportWriteDeadline()
		s.emit(request, TelemetryEventReportReject, TelemetryOutcomePolicy, 0)
		response.writeAPIError(apiErrorNetworkPolicy, apiErrorMetadata{})
		return
	}

	marshalStarted := time.Now()
	var payload []byte
	var marshalErr error
	if decoded.TopologyMode == diagnostic.TopologyModeCompact {
		payload, marshalErr = marshalCompactResponse(report)
	} else {
		payload, marshalErr = marshalFullResponse(report)
	}
	observation.marshalDuration = time.Since(marshalStarted)
	if marshalErr != nil {
		releaseAdmission()
		response.setReportWriteDeadline()
		switch {
		case errors.Is(marshalErr, errCompactResponseTooLarge):
			response.writeAPIError(apiErrorCompactTooLarge, apiErrorMetadata{})
		case isFullResponseBudgetError(marshalErr):
			response.writeAPIError(apiErrorFullTooLarge, apiErrorMetadata{})
		default:
			response.writeAPIError(apiErrorSerialization, apiErrorMetadata{})
		}
		s.emit(request, TelemetryEventReportFinish, observation.outcome, 0)
		return
	}

	select {
	case s.responseWrites <- struct{}{}:
	default:
		payload = nil
		releaseAdmission()
		response.setReportWriteDeadline()
		response.writeAPIError(apiErrorWriteCapacity, apiErrorMetadata{})
		s.emit(request, TelemetryEventReportFinish, observation.outcome, 0)
		return
	}
	releaseAdmission()
	defer func() { <-s.responseWrites }()
	response.setReportWriteDeadline()
	response.writeReport(reportJSON{payload: payload})
	s.emitReportFinish(request, observation.outcome, report)
}

func isFullResponseBudgetError(err error) bool {
	return errors.Is(err, errFullResponseTooLarge) || errors.Is(err, errFullResponseStringLimit) ||
		errors.Is(err, errFullResponseContainerLimit) || errors.Is(err, errFullResponseNodeLimit) ||
		errors.Is(err, errFullResponseDepthLimit) || errors.Is(err, errFullResponseCycle)
}

func isRequestTooLarge(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

func (s *Server) options(response responder, _ *http.Request) { response.writeNoContent() }

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

func (s *Server) serveHTTP(response responder, request *http.Request) {
	route, unmatchedStatus := classifyRequest(request)
	observation := &requestObservation{started: time.Now(), requestID: NewRequestID(), method: telemetryMethod(request.Method), route: route}
	if request.Body != nil {
		counter := &countingReadCloser{ReadCloser: request.Body}
		observation.requestBody = counter
		request.Body = counter
	}
	if route == TelemetryRouteReports {
		observation.reportID = NewReportID()
	}
	request = request.WithContext(context.WithValue(request.Context(), requestObservationKey{}, observation))
	response.bindObservation(observation)
	defer func() {
		if recover() != nil {
			s.containPanic(response, request, observation)
		}
		if observation.status == 0 {
			attempted, _, status := response.responseState()
			if attempted && status != 0 {
				observation.status = status
			} else {
				observation.status = http.StatusInternalServerError
				observation.outcome = TelemetryOutcomePanicSafeFailure
			}
		}
		s.emit(request, TelemetryEventHTTPTerminal, observation.outcome, observation.status)
	}()

	response.setSecurityHeaders()
	origin := request.Header.Get("Origin")
	if origin != "" && s.originAllowed(origin) {
		response.setCORSHeaders(origin)
	}
	if route == TelemetryRouteReports {
		s.emit(request, TelemetryEventReportSubmit, TelemetryOutcomeOK, 0)
	}
	if s.mode == ModePublic && request.Method != http.MethodOptions {
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		providedHash := sha256.Sum256([]byte(provided))
		if provided == request.Header.Get("Authorization") || subtle.ConstantTimeCompare(providedHash[:], s.apiKeyHash[:]) != 1 {
			if route == TelemetryRouteReports {
				s.emit(request, TelemetryEventReportReject, TelemetryOutcomeUnauthorized, 0)
			}
			response.writeAPIError(apiErrorUnauthorized, apiErrorMetadata{})
			return
		}
		if allowed, retryAfter := s.allowClient(clientAddress(request.RemoteAddr), time.Now()); !allowed {
			if route == TelemetryRouteReports {
				s.emit(request, TelemetryEventReportReject, TelemetryOutcomeRateLimited, 0)
			}
			response.writeAPIError(apiErrorRateLimited, apiErrorMetadata{RetryAfter: retryAfter})
			return
		}
	}
	if unmatchedStatus != 0 {
		if unmatchedStatus == http.StatusMethodNotAllowed {
			response.writeAPIError(apiErrorMethodNotAllowed, apiErrorMetadata{Allow: apiAllowForPath(request.URL.Path)})
		} else {
			response.writeAPIError(apiErrorRouteNotFound, apiErrorMetadata{})
		}
		return
	}
	if route != TelemetryRouteOptions && s.draining != nil && s.draining.IsDraining() {
		if route == TelemetryRouteReports {
			s.rejectReport(response, request, apiErrorServerDraining, apiErrorMetadata{RetryAfter: s.busyRetryAfter})
		} else {
			response.writeAPIError(apiErrorServerDraining, apiErrorMetadata{RetryAfter: s.busyRetryAfter})
		}
		return
	}
	switch route {
	case TelemetryRouteHealth:
		s.health(response, request)
	case TelemetryRouteChecks:
		s.checks(response, request)
	case TelemetryRouteReports:
		s.createReport(response, request)
	case TelemetryRouteOptions:
		s.options(response, request)
	}
}

func apiAllowForPath(path string) apiAllowMethods {
	switch path {
	case "/api/v1/health", "/api/v1/checks":
		return apiAllowGet
	case "/api/v1/reports":
		return apiAllowPost
	default:
		return apiAllowOptions
	}
}

func (s *Server) containPanic(response responder, request *http.Request, observation *requestObservation) {
	defer func() { _ = recover() }()
	observation.outcome = TelemetryOutcomePanicSafeFailure
	attempted, _, status := response.responseState()
	if attempted && status != 0 {
		observation.status = status
	} else {
		observation.status = http.StatusInternalServerError
		response.writeAPIError(apiErrorInternal, apiErrorMetadata{})
		observation.outcome = TelemetryOutcomePanicSafeFailure
	}
	if observation.route == TelemetryRouteReports && !observation.reportTerminal {
		s.emit(request, TelemetryEventReportFinish, TelemetryOutcomePanicSafeFailure, 0)
	}
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

func (s *Server) telemetryRecord(request *http.Request, event TelemetryEvent, outcome TelemetryOutcome, status int) TelemetryRecord {
	observation := observationFromRequest(request)
	requestBytes := int64(0)
	if observation.requestBody != nil {
		requestBytes = observation.requestBody.bytes
	}
	return TelemetryRecord{
		Event: event, Outcome: outcome, RequestID: observation.requestID, ReportID: observation.reportID,
		Method: observation.method, Route: observation.route, Status: status,
		Active: int64(len(s.admission)), Capacity: int64(cap(s.admission)), RequestBytes: requestBytes,
		ResponseAttemptedBytes: observation.responseAttempted, ResponseBytes: observation.responseActual,
		Duration: time.Since(observation.started), RunnerDuration: observation.runnerDuration,
		MarshalDuration: observation.marshalDuration, WriteDuration: observation.writeDuration,
	}
}

func (s *Server) emit(request *http.Request, event TelemetryEvent, outcome TelemetryOutcome, status int) {
	observation := observationFromRequest(request)
	if observation == nil {
		return
	}
	if event == TelemetryEventReportFinish || event == TelemetryEventReportReject || event == TelemetryEventReportCancel {
		observation.reportTerminal = true
	}
	_ = EmitTelemetry(s.logger, s.telemetryRecord(request, event, outcome, status))
}

func (s *Server) emitReportFinish(request *http.Request, outcome TelemetryOutcome, report diagnostic.Report) {
	observation := observationFromRequest(request)
	if observation == nil {
		return
	}
	observation.reportTerminal = true
	record := s.telemetryRecord(request, TelemetryEventReportFinish, outcome, 0)
	if outcome == TelemetryOutcomeOK && observation.responseAttempted > 0 &&
		observation.responseActual == observation.responseAttempted && report.Analysis != nil {
		candidate := record
		candidate.ReportStatus = report.Status
		candidate.AnalysisVerdict = report.Analysis.Verdict
		candidate.TotalResults = report.Summary.Total
		candidate.FailedResults = report.Summary.Failed
		candidate.FindingCount = len(report.Analysis.Findings)
		candidate.reportDiagnosticsPresent = true
		if validReportDiagnostics(candidate) {
			record = candidate
		}
	}
	_ = EmitTelemetry(s.logger, record)
}

func (s *Server) rejectReport(response responder, request *http.Request, key apiErrorKey, metadata apiErrorMetadata) {
	definition := apiErrorDefinitionFor(key)
	s.emit(request, TelemetryEventReportReject, definition.Outcome, 0)
	response.writeAPIError(key, metadata)
}

func reportHasResultCode(report diagnostic.Report, code string) bool {
	for _, result := range report.Results {
		if result.ErrorCode == code {
			return true
		}
	}
	return false
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
