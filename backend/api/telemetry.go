package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

// TelemetryEvent is a closed set of API lifecycle events.
type TelemetryEvent string

const (
	TelemetryEventHTTPTerminal   TelemetryEvent = "http_terminal"
	TelemetryEventReportSubmit   TelemetryEvent = "report_submit"
	TelemetryEventReportAdmit    TelemetryEvent = "report_admit"
	TelemetryEventReportReject   TelemetryEvent = "report_reject"
	TelemetryEventReportStart    TelemetryEvent = "report_start"
	TelemetryEventReportComputed TelemetryEvent = "report_computed"
	TelemetryEventReportFinish   TelemetryEvent = "report_finish"
	TelemetryEventReportCancel   TelemetryEvent = "report_cancel"
)

// TelemetryOutcome is a closed, prose-free classification of an event result.
type TelemetryOutcome string

const (
	TelemetryOutcomeOK                 TelemetryOutcome = "ok"
	TelemetryOutcomeUnauthorized       TelemetryOutcome = "unauthorized"
	TelemetryOutcomeRateLimited        TelemetryOutcome = "rate_limited"
	TelemetryOutcomeInvalidJSON        TelemetryOutcome = "invalid_json"
	TelemetryOutcomeInvalidRequest     TelemetryOutcome = "invalid_request"
	TelemetryOutcomeRequestTooLarge    TelemetryOutcome = "request_too_large"
	TelemetryOutcomeServerCapacity     TelemetryOutcome = "server_capacity_unavailable"
	TelemetryOutcomeServerDraining     TelemetryOutcome = "server_draining"
	TelemetryOutcomeBodyCapacity       TelemetryOutcome = "body_decode_capacity_unavailable"
	TelemetryOutcomeCheckerCapacity    TelemetryOutcome = "checker_capacity_unavailable"
	TelemetryOutcomeWriteCapacity      TelemetryOutcome = "write_capacity_unavailable"
	TelemetryOutcomePolicy             TelemetryOutcome = "policy_blocked"
	TelemetryOutcomeTimeout            TelemetryOutcome = "timeout"
	TelemetryOutcomeCancel             TelemetryOutcome = "cancelled"
	TelemetryOutcomePanicSafeFailure   TelemetryOutcome = "panic_safe_failure"
	TelemetryOutcomeSerialization      TelemetryOutcome = "serialization_failed"
	TelemetryOutcomeFullSize           TelemetryOutcome = "full_response_too_large"
	TelemetryOutcomeCompactSize        TelemetryOutcome = "compact_response_too_large"
	TelemetryOutcomeWriteFailedZero    TelemetryOutcome = "write_failed_zero"
	TelemetryOutcomeWriteFailedPartial TelemetryOutcome = "write_failed_partial"
	TelemetryOutcomeUnmatched          TelemetryOutcome = "unmatched"
)

// TelemetryRoute is a closed set of mux route patterns. It deliberately cannot
// contain a URL, query string, target, or other client-controlled path data.
type TelemetryRoute string

const (
	TelemetryRouteHealth    TelemetryRoute = "GET /api/v1/health"
	TelemetryRouteChecks    TelemetryRoute = "GET /api/v1/checks"
	TelemetryRouteReports   TelemetryRoute = "POST /api/v1/reports"
	TelemetryRouteOptions   TelemetryRoute = "OPTIONS /api/v1/"
	TelemetryRouteUnmatched TelemetryRoute = "unmatched"
)

const (
	maxTelemetryCount    int64 = 1 << 20
	maxTelemetryBytes    int64 = 1 << 30
	maxTelemetryDuration       = 24 * time.Hour
	maxTelemetryFindings       = 2 * diagnostic.MaxTargets
)

// TelemetryRecord is the complete telemetry input contract. It intentionally
// has no extension map, error, reason, address, URL, headers, or payload fields.
type TelemetryRecord struct {
	Event                  TelemetryEvent
	Outcome                TelemetryOutcome
	RequestID              string
	ReportID               string
	Method                 string
	Route                  TelemetryRoute
	Status                 int
	Active                 int64
	Capacity               int64
	RequestBytes           int64
	ResponseAttemptedBytes int64
	ResponseBytes          int64
	Duration               time.Duration
	RunnerDuration         time.Duration
	MarshalDuration        time.Duration
	WriteDuration          time.Duration
	ReportStatus           diagnostic.Status
	AnalysisVerdict        diagnostic.Verdict
	TotalResults           int
	FailedResults          int
	FindingCount           int

	reportDiagnosticsPresent bool
}

var (
	errInvalidTelemetryRecord = errors.New("invalid telemetry record")
	fallbackIDCounter         atomic.Uint64
	fallbackIDSeed            = sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10) + ":" + strconv.Itoa(os.Getpid())))
)

// NewRequestID returns an opaque, server-owned correlation identifier.
func NewRequestID() string { return newOpaqueID("req", rand.Reader) }

// NewReportID returns an opaque identifier in the report model's existing
// 24-lowercase-hex format.
func NewReportID() string { return newReportID(rand.Reader) }

func newReportID(source io.Reader) string { return opaqueHexID("", 12, source) }

func newOpaqueID(prefix string, source io.Reader) string {
	return opaqueHexID(prefix+"_", 16, source)
}

func opaqueHexID(prefix string, byteCount int, source io.Reader) string {
	random := make([]byte, byteCount)
	if source != nil {
		if _, err := io.ReadFull(source, random); err == nil {
			return prefix + hex.EncodeToString(random)
		}
	}

	// The fallback contains only process-local seed material and a monotonic
	// counter. In particular, neither reader errors nor any request data enter it.
	sequence := fallbackIDCounter.Add(1)
	var input [sha256.Size + 8]byte
	copy(input[:sha256.Size], fallbackIDSeed[:])
	binary.BigEndian.PutUint64(input[sha256.Size:], sequence)
	digest := sha256.Sum256(input[:])
	return prefix + hex.EncodeToString(digest[:byteCount])
}

// EmitTelemetry validates and bounds a record before making a single slog call.
// Invalid records produce no log entry. Delivery is deliberately best effort:
// a nil logger is a no-op, and slog.Logger does not expose Handler errors to its
// caller, so a sink failure never changes request processing.
func EmitTelemetry(logger *slog.Logger, record TelemetryRecord) (err error) {
	bounded, err := validateTelemetryRecord(record)
	if err != nil {
		return err
	}
	if logger == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			// Telemetry delivery is best effort. Do not let a buggy third-party
			// handler crash request processing or expose its panic value.
			err = nil
		}
	}()

	attrs := []slog.Attr{
		slog.String("event", string(bounded.Event)),
		slog.String("outcome", string(bounded.Outcome)),
		slog.String("request_id", bounded.RequestID),
		slog.String("report_id", bounded.ReportID),
		slog.String("method", bounded.Method),
		slog.String("route", string(bounded.Route)),
		slog.Int("status", bounded.Status),
		slog.Int64("active", bounded.Active),
		slog.Int64("capacity", bounded.Capacity),
		slog.Int64("request_bytes", bounded.RequestBytes),
		slog.Int64("response_attempted_bytes", bounded.ResponseAttemptedBytes),
		slog.Int64("response_bytes", bounded.ResponseBytes),
		slog.Int64("duration_ms", bounded.Duration.Milliseconds()),
		slog.Int64("runner_duration_ms", bounded.RunnerDuration.Milliseconds()),
		slog.Int64("marshal_duration_ms", bounded.MarshalDuration.Milliseconds()),
		slog.Int64("write_duration_ms", bounded.WriteDuration.Milliseconds()),
	}
	if bounded.reportDiagnosticsPresent {
		attrs = append(attrs,
			slog.String("report_status", string(bounded.ReportStatus)),
			slog.String("analysis_verdict", string(bounded.AnalysisVerdict)),
			slog.Int("total_results", bounded.TotalResults),
			slog.Int("failed_results", bounded.FailedResults),
			slog.Int("finding_count", bounded.FindingCount),
		)
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "api_telemetry", attrs...)
	return nil
}

func validateTelemetryRecord(record TelemetryRecord) (TelemetryRecord, error) {
	if !validTelemetryEvent(record.Event) {
		return TelemetryRecord{}, invalidTelemetry("event")
	}
	if !validTelemetryOutcome(record.Outcome) {
		return TelemetryRecord{}, invalidTelemetry("outcome")
	}
	if !validRequestID(record.RequestID) {
		return TelemetryRecord{}, invalidTelemetry("request_id")
	}
	if record.Event == TelemetryEventHTTPTerminal {
		if record.ReportID != "" && !validReportID(record.ReportID) {
			return TelemetryRecord{}, invalidTelemetry("report_id")
		}
	} else if !validReportID(record.ReportID) {
		return TelemetryRecord{}, invalidTelemetry("report_id")
	}
	if !validTelemetryMethod(record.Method) {
		return TelemetryRecord{}, invalidTelemetry("method")
	}
	if !validTelemetryRoute(record.Route) {
		return TelemetryRecord{}, invalidTelemetry("route")
	}
	if !validTelemetrySemantics(record) {
		return TelemetryRecord{}, invalidTelemetry("event contract")
	}
	if !validReportDiagnostics(record) {
		return TelemetryRecord{}, invalidTelemetry("report diagnostics")
	}
	if record.Active < 0 || record.Capacity < 0 || record.RequestBytes < 0 ||
		record.ResponseAttemptedBytes < 0 || record.ResponseBytes < 0 {
		return TelemetryRecord{}, invalidTelemetry("negative counter")
	}
	if record.Duration < 0 || record.RunnerDuration < 0 || record.MarshalDuration < 0 || record.WriteDuration < 0 {
		return TelemetryRecord{}, invalidTelemetry("negative duration")
	}

	// Check relationships on the original counters: clamping must not turn an
	// impossible pair into an apparently valid one.
	if record.Active > record.Capacity {
		return TelemetryRecord{}, invalidTelemetry("active exceeds capacity")
	}
	if record.ResponseBytes > record.ResponseAttemptedBytes {
		return TelemetryRecord{}, invalidTelemetry("response bytes exceed attempted bytes")
	}

	record.Active = min(record.Active, maxTelemetryCount)
	record.Capacity = min(record.Capacity, maxTelemetryCount)
	record.RequestBytes = min(record.RequestBytes, maxTelemetryBytes)
	record.ResponseAttemptedBytes = min(record.ResponseAttemptedBytes, maxTelemetryBytes)
	record.ResponseBytes = min(record.ResponseBytes, maxTelemetryBytes)
	record.Duration = min(record.Duration, maxTelemetryDuration)
	record.RunnerDuration = min(record.RunnerDuration, maxTelemetryDuration)
	record.MarshalDuration = min(record.MarshalDuration, maxTelemetryDuration)
	record.WriteDuration = min(record.WriteDuration, maxTelemetryDuration)

	return record, nil
}

func invalidTelemetry(field string) error {
	return fmt.Errorf("%w: %s", errInvalidTelemetryRecord, field)
}

func validTelemetryEvent(event TelemetryEvent) bool {
	switch event {
	case TelemetryEventHTTPTerminal,
		TelemetryEventReportSubmit,
		TelemetryEventReportAdmit,
		TelemetryEventReportReject,
		TelemetryEventReportStart,
		TelemetryEventReportComputed,
		TelemetryEventReportFinish,
		TelemetryEventReportCancel:
		return true
	default:
		return false
	}
}

func validTelemetryOutcome(outcome TelemetryOutcome) bool {
	switch outcome {
	case TelemetryOutcomeOK,
		TelemetryOutcomeUnauthorized,
		TelemetryOutcomeRateLimited,
		TelemetryOutcomeInvalidJSON,
		TelemetryOutcomeInvalidRequest,
		TelemetryOutcomeRequestTooLarge,
		TelemetryOutcomeServerCapacity,
		TelemetryOutcomeServerDraining,
		TelemetryOutcomeBodyCapacity,
		TelemetryOutcomeCheckerCapacity,
		TelemetryOutcomeWriteCapacity,
		TelemetryOutcomePolicy,
		TelemetryOutcomeTimeout,
		TelemetryOutcomeCancel,
		TelemetryOutcomePanicSafeFailure,
		TelemetryOutcomeSerialization,
		TelemetryOutcomeFullSize,
		TelemetryOutcomeCompactSize,
		TelemetryOutcomeWriteFailedZero,
		TelemetryOutcomeWriteFailedPartial,
		TelemetryOutcomeUnmatched:
		return true
	default:
		return false
	}
}

func validTelemetryMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE", "OTHER":
		return true
	default:
		return false
	}
}

func validTelemetryRoute(route TelemetryRoute) bool {
	switch route {
	case TelemetryRouteHealth, TelemetryRouteChecks, TelemetryRouteReports, TelemetryRouteOptions, TelemetryRouteUnmatched:
		return true
	default:
		return false
	}
}

func validTelemetrySemantics(record TelemetryRecord) bool {
	if record.Event != TelemetryEventHTTPTerminal {
		if record.Method != "POST" || record.Route != TelemetryRouteReports || record.Status != 0 {
			return false
		}
		switch record.Event {
		case TelemetryEventReportSubmit, TelemetryEventReportAdmit,
			TelemetryEventReportStart, TelemetryEventReportComputed:
			return record.Outcome == TelemetryOutcomeOK
		case TelemetryEventReportCancel:
			return record.Outcome == TelemetryOutcomeCancel
		case TelemetryEventReportReject:
			return outcomeIn(record.Outcome,
				TelemetryOutcomeUnauthorized, TelemetryOutcomeRateLimited,
				TelemetryOutcomeInvalidJSON, TelemetryOutcomeInvalidRequest,
				TelemetryOutcomeRequestTooLarge, TelemetryOutcomeServerCapacity,
				TelemetryOutcomeBodyCapacity, TelemetryOutcomeServerDraining, TelemetryOutcomePolicy)
		case TelemetryEventReportFinish:
			return outcomeIn(record.Outcome,
				TelemetryOutcomeOK, TelemetryOutcomePanicSafeFailure, TelemetryOutcomeWriteCapacity, TelemetryOutcomeSerialization,
				TelemetryOutcomeFullSize, TelemetryOutcomeCompactSize,
				TelemetryOutcomeWriteFailedZero, TelemetryOutcomeWriteFailedPartial)
		default:
			return false
		}
	}

	if record.Status < 100 || record.Status > 599 || !validTerminalRouteMethod(record.Route, record.Method) {
		return false
	}
	switch record.Outcome {
	case TelemetryOutcomeOK:
		return record.Status == 200 || record.Status == 204
	case TelemetryOutcomeInvalidJSON:
		return record.Status == 400
	case TelemetryOutcomeUnauthorized:
		return record.Status == 401
	case TelemetryOutcomeUnmatched:
		return record.Status == 404 || record.Status == 405
	case TelemetryOutcomeRequestTooLarge:
		return record.Status == 413
	case TelemetryOutcomeInvalidRequest, TelemetryOutcomePolicy:
		return record.Status == 422
	case TelemetryOutcomeRateLimited:
		return record.Status == 429
	case TelemetryOutcomePanicSafeFailure:
		// Recovery preserves a status whose write was already attempted; it must
		// not append a replacement 500 after that boundary.
		return true
	case TelemetryOutcomeSerialization, TelemetryOutcomeFullSize, TelemetryOutcomeCompactSize:
		return record.Status == 500
	case TelemetryOutcomeServerCapacity, TelemetryOutcomeServerDraining, TelemetryOutcomeBodyCapacity, TelemetryOutcomeWriteCapacity:
		return record.Status == 503
	case TelemetryOutcomeCancel:
		return record.Route == TelemetryRouteReports && record.Status == 499
	case TelemetryOutcomeWriteFailedZero, TelemetryOutcomeWriteFailedPartial:
		return true
	default:
		return false
	}
}

func validReportDiagnostics(record TelemetryRecord) bool {
	hasValues := record.ReportStatus != "" || record.AnalysisVerdict != "" ||
		record.TotalResults != 0 || record.FailedResults != 0 || record.FindingCount != 0
	if !record.reportDiagnosticsPresent {
		return !hasValues
	}
	if record.Event != TelemetryEventReportFinish || record.Outcome != TelemetryOutcomeOK || record.Status != 0 ||
		record.ResponseAttemptedBytes <= 0 || record.ResponseBytes != record.ResponseAttemptedBytes {
		return false
	}
	if record.TotalResults < 1 || record.TotalResults > diagnostic.MaxTargets ||
		record.FailedResults < 0 || record.FailedResults > record.TotalResults ||
		record.FindingCount < 0 || record.FindingCount > maxTelemetryFindings {
		return false
	}
	if record.AnalysisVerdict != diagnostic.VerdictHealthy &&
		record.AnalysisVerdict != diagnostic.VerdictAttention &&
		record.AnalysisVerdict != diagnostic.VerdictInconclusive {
		return false
	}
	switch record.ReportStatus {
	case diagnostic.StatusHealthy:
		return record.FailedResults == 0
	case diagnostic.StatusDegraded:
		// A degraded result is not counted as passed, so an all-degraded report
		// legitimately has failed_results == total_results.
		return record.FailedResults > 0
	case diagnostic.StatusUnreachable:
		return record.FailedResults == record.TotalResults
	default:
		return false
	}
}

func validTerminalRouteMethod(route TelemetryRoute, method string) bool {
	switch route {
	case TelemetryRouteHealth, TelemetryRouteChecks:
		return method == "GET"
	case TelemetryRouteReports:
		return method == "POST"
	case TelemetryRouteOptions:
		return method == "OPTIONS"
	case TelemetryRouteUnmatched:
		return true
	default:
		return false
	}
}

func outcomeIn(got TelemetryOutcome, allowed ...TelemetryOutcome) bool {
	for _, outcome := range allowed {
		if got == outcome {
			return true
		}
	}
	return false
}

func validRequestID(id string) bool {
	return len(id) == 36 && id[:4] == "req_" && validLowerHex(id[4:])
}

func validReportID(id string) bool { return len(id) == 24 && validLowerHex(id) }

func validLowerHex(value string) bool {
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}
