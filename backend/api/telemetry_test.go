package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

var (
	telemetryRequestIDPattern = regexp.MustCompile(`^req_[0-9a-f]{32}$`)
	telemetryReportIDPattern  = regexp.MustCompile(`^[0-9a-f]{24}$`)
)

func telemetryTestLogger(buffer *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buffer, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey || attr.Key == slog.LevelKey {
				return slog.Attr{}
			}
			return attr
		},
	}))
}

func validTelemetryRecord(event TelemetryEvent, outcome TelemetryOutcome) TelemetryRecord {
	record := TelemetryRecord{
		Event:                  event,
		Outcome:                outcome,
		RequestID:              "req_0123456789abcdef0123456789abcdef",
		Method:                 "POST",
		Route:                  TelemetryRouteReports,
		Active:                 2,
		Capacity:               4,
		RequestBytes:           128,
		ResponseAttemptedBytes: 512,
		ResponseBytes:          500,
		Duration:               17 * time.Millisecond,
		RunnerDuration:         11 * time.Millisecond,
		MarshalDuration:        3 * time.Millisecond,
		WriteDuration:          2 * time.Millisecond,
	}
	if event == TelemetryEventHTTPTerminal {
		record.Method = "GET"
		record.Route = TelemetryRouteHealth
		record.Status = 200
	} else {
		record.ReportID = "fedcba9876543210fedcba98"
	}
	if event == TelemetryEventReportFinish && outcome == TelemetryOutcomeOK {
		record.ResponseBytes = record.ResponseAttemptedBytes
		record.ReportStatus = diagnostic.StatusHealthy
		record.AnalysisVerdict = diagnostic.VerdictHealthy
		record.TotalResults = 1
		record.reportDiagnosticsPresent = true
	}
	return record
}

func decodeTelemetryLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) == 1 && len(lines[0]) == 0 {
		return nil
	}
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid telemetry JSON %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func TestEmitTelemetryAcceptsOnlyPositiveEventContractsAndHasExactFixedKeys(t *testing.T) {
	records := []TelemetryRecord{
		validTelemetryRecord(TelemetryEventReportSubmit, TelemetryOutcomeOK),
		validTelemetryRecord(TelemetryEventReportAdmit, TelemetryOutcomeOK),
		validTelemetryRecord(TelemetryEventReportStart, TelemetryOutcomeOK),
		validTelemetryRecord(TelemetryEventReportComputed, TelemetryOutcomeOK),
		validTelemetryRecord(TelemetryEventReportCancel, TelemetryOutcomeCancel),
	}
	for _, outcome := range []TelemetryOutcome{
		TelemetryOutcomeUnauthorized, TelemetryOutcomeRateLimited, TelemetryOutcomeInvalidJSON,
		TelemetryOutcomeInvalidRequest, TelemetryOutcomeRequestTooLarge, TelemetryOutcomeServerCapacity,
		TelemetryOutcomeServerDraining, TelemetryOutcomeBodyCapacity, TelemetryOutcomePolicy,
	} {
		records = append(records, validTelemetryRecord(TelemetryEventReportReject, outcome))
	}
	for _, outcome := range []TelemetryOutcome{
		TelemetryOutcomeOK, TelemetryOutcomeWriteCapacity, TelemetryOutcomeSerialization,
		TelemetryOutcomeFullSize, TelemetryOutcomeCompactSize, TelemetryOutcomeWriteFailedZero,
		TelemetryOutcomeWriteFailedPartial,
	} {
		records = append(records, validTelemetryRecord(TelemetryEventReportFinish, outcome))
	}
	for _, item := range []struct {
		status  int
		outcome TelemetryOutcome
	}{
		{200, TelemetryOutcomeOK}, {204, TelemetryOutcomeOK}, {400, TelemetryOutcomeInvalidJSON},
		{401, TelemetryOutcomeUnauthorized}, {404, TelemetryOutcomeUnmatched}, {405, TelemetryOutcomeUnmatched},
		{413, TelemetryOutcomeRequestTooLarge}, {422, TelemetryOutcomeInvalidRequest}, {422, TelemetryOutcomePolicy},
		{429, TelemetryOutcomeRateLimited}, {500, TelemetryOutcomePanicSafeFailure},
		{500, TelemetryOutcomeSerialization}, {500, TelemetryOutcomeFullSize}, {500, TelemetryOutcomeCompactSize},
		{503, TelemetryOutcomeServerCapacity}, {503, TelemetryOutcomeServerDraining}, {503, TelemetryOutcomeBodyCapacity}, {503, TelemetryOutcomeWriteCapacity},
		{200, TelemetryOutcomeWriteFailedZero}, {200, TelemetryOutcomeWriteFailedPartial},
	} {
		record := validTelemetryRecord(TelemetryEventHTTPTerminal, item.outcome)
		record.Status = item.status
		if item.outcome == TelemetryOutcomeUnmatched {
			record.Route = TelemetryRouteUnmatched
		}
		records = append(records, record)
	}
	wantBaseKeys := []string{
		"active", "capacity", "duration_ms", "event", "marshal_duration_ms",
		"method", "msg", "outcome", "report_id", "request_bytes", "request_id",
		"response_attempted_bytes", "response_bytes", "route", "runner_duration_ms",
		"status", "write_duration_ms",
	}

	var buffer bytes.Buffer
	logger := telemetryTestLogger(&buffer)
	for _, record := range records {
		if err := EmitTelemetry(logger, record); err != nil {
			t.Fatalf("EmitTelemetry(%q, %q, %d): %v", record.Event, record.Outcome, record.Status, err)
		}
	}

	logged := decodeTelemetryLines(t, buffer.Bytes())
	if len(logged) != len(records) {
		t.Fatalf("logged records=%d, want %d", len(logged), len(records))
	}
	for index, record := range logged {
		keys := make([]string, 0, len(record))
		for key := range record {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		wantKeys := append([]string(nil), wantBaseKeys...)
		if records[index].reportDiagnosticsPresent {
			wantKeys = append(wantKeys, "analysis_verdict", "failed_results", "finding_count", "report_status", "total_results")
			sort.Strings(wantKeys)
		}
		if !reflect.DeepEqual(keys, wantKeys) {
			t.Fatalf("record %d keys=%v, want %v", index, keys, wantKeys)
		}
		if record["msg"] != "api_telemetry" {
			t.Fatalf("record %d msg=%v", index, record["msg"])
		}
	}
}

func TestEmitTelemetryRejectsInvalidOrMisplacedReportDiagnosticsWithoutLogging(t *testing.T) {
	valid := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
	tests := []struct {
		name   string
		mutate func(*TelemetryRecord)
	}{
		{"missing presence", func(r *TelemetryRecord) { r.reportDiagnosticsPresent = false }},
		{"wrong event", func(r *TelemetryRecord) { r.Event = TelemetryEventReportComputed }},
		{"wrong outcome", func(r *TelemetryRecord) { r.Outcome = TelemetryOutcomeWriteFailedPartial }},
		{"wrong lifecycle status", func(r *TelemetryRecord) { r.Status = http.StatusOK }},
		{"incomplete write", func(r *TelemetryRecord) { r.ResponseBytes-- }},
		{"zero total", func(r *TelemetryRecord) { r.TotalResults = 0 }},
		{"total above producer max", func(r *TelemetryRecord) { r.TotalResults = diagnostic.MaxTargets + 1 }},
		{"failed above total", func(r *TelemetryRecord) { r.FailedResults = 2 }},
		{"finding below zero", func(r *TelemetryRecord) { r.FindingCount = -1 }},
		{"finding above exact producer max", func(r *TelemetryRecord) { r.FindingCount = 2*diagnostic.MaxTargets + 1 }},
		{"invalid status", func(r *TelemetryRecord) { r.ReportStatus = diagnostic.Status("STATUS_CANARY") }},
		{"invalid verdict", func(r *TelemetryRecord) { r.AnalysisVerdict = diagnostic.Verdict("VERDICT_CANARY") }},
		{"healthy with failures", func(r *TelemetryRecord) { r.FailedResults = 1 }},
		{"degraded without failures", func(r *TelemetryRecord) { r.ReportStatus = diagnostic.StatusDegraded }},
		{"unreachable without all failures", func(r *TelemetryRecord) { r.ReportStatus = diagnostic.StatusUnreachable }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			test.mutate(&record)
			var buffer bytes.Buffer
			if err := EmitTelemetry(telemetryTestLogger(&buffer), record); err == nil {
				t.Fatal("accepted invalid report diagnostics")
			}
			if buffer.Len() != 0 {
				t.Fatalf("invalid diagnostics logged: %s", buffer.String())
			}
		})
	}

	for _, mutate := range []func(*TelemetryRecord){
		func(r *TelemetryRecord) { r.Event = TelemetryEventReportComputed },
		func(r *TelemetryRecord) { r.Outcome = TelemetryOutcomeSerialization },
	} {
		record := valid
		record.reportDiagnosticsPresent = false
		mutate(&record)
		var buffer bytes.Buffer
		if err := EmitTelemetry(telemetryTestLogger(&buffer), record); err == nil || buffer.Len() != 0 {
			t.Fatalf("accepted hidden diagnostics without presence: record=%+v log=%s", record, buffer.String())
		}
	}
}

func TestEmitTelemetryAcceptsBaseSuccessfulReportFinishWithoutDiagnostics(t *testing.T) {
	record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
	record.ReportStatus = ""
	record.AnalysisVerdict = ""
	record.TotalResults = 0
	record.FailedResults = 0
	record.FindingCount = 0
	record.reportDiagnosticsPresent = false

	var logs bytes.Buffer
	if err := EmitTelemetry(telemetryTestLogger(&logs), record); err != nil {
		t.Fatalf("base successful report_finish rejected: %v", err)
	}
	records := decodeTelemetryLines(t, logs.Bytes())
	if len(records) != 1 || records[0]["event"] != string(TelemetryEventReportFinish) || records[0]["outcome"] != string(TelemetryOutcomeOK) {
		t.Fatalf("records=%v", records)
	}
	assertNoReportDiagnostics(t, records[0])
}

func TestEmitTelemetryAcceptsAllDegradedReportDiagnostics(t *testing.T) {
	record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
	record.ReportStatus = diagnostic.StatusDegraded
	record.AnalysisVerdict = diagnostic.VerdictAttention
	record.TotalResults = 2
	record.FailedResults = 2
	record.FindingCount = 2

	var logs bytes.Buffer
	if err := EmitTelemetry(telemetryTestLogger(&logs), record); err != nil {
		t.Fatalf("all-degraded report diagnostics rejected: %v", err)
	}
	entries := decodeTelemetryLines(t, logs.Bytes())
	if len(entries) != 1 || entries[0]["report_status"] != string(diagnostic.StatusDegraded) || entries[0]["failed_results"] != float64(2) {
		t.Fatalf("all-degraded diagnostics=%v", entries)
	}
}

func TestEmitTelemetryPreservesValidDiagnosticTupleAtFortyFindingLimit(t *testing.T) {
	record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
	record.TotalResults = diagnostic.MaxTargets
	record.FindingCount = maxTelemetryFindings

	var logs bytes.Buffer
	if err := EmitTelemetry(telemetryTestLogger(&logs), record); err != nil {
		t.Fatalf("valid boundary tuple rejected: %v", err)
	}
	got := decodeTelemetryLines(t, logs.Bytes())
	assertDeliveredReportDiagnostics(t, got, diagnostic.StatusHealthy, diagnostic.VerdictHealthy, diagnostic.MaxTargets, 0, 40)
	if maxTelemetryFindings != 40 {
		t.Fatalf("finding limit=%d, want 40", maxTelemetryFindings)
	}
}

func TestEmitTelemetryDirectRecordCannotBypassDiagnosticPresenceOrTupleValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TelemetryRecord)
	}{
		{
			name: "hidden malicious values",
			mutate: func(record *TelemetryRecord) {
				record.reportDiagnosticsPresent = false
				record.AnalysisVerdict = diagnostic.Verdict("DIRECT_HIDDEN_VERDICT_CANARY")
			},
		},
		{
			name: "flagged malicious tuple",
			mutate: func(record *TelemetryRecord) {
				record.ReportStatus = diagnostic.StatusHealthy
				record.FailedResults = 1
				record.AnalysisVerdict = diagnostic.Verdict("DIRECT_FLAGGED_VERDICT_CANARY")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
			test.mutate(&record)
			var logs bytes.Buffer
			if err := EmitTelemetry(telemetryTestLogger(&logs), record); err == nil {
				t.Fatal("malicious direct record accepted")
			}
			if logs.Len() != 0 {
				t.Fatalf("malicious direct record logged: %s", logs.String())
			}
		})
	}
}

func TestEmitTelemetryRejectsImpossibleEventOutcomeStatusAndRouteCombinations(t *testing.T) {
	tests := []struct {
		name   string
		record TelemetryRecord
	}{
		{"http unauthorized success", func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventHTTPTerminal, TelemetryOutcomeUnauthorized)
			r.Status = 200
			return r
		}()},
		{"http ok unauthorized", func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventHTTPTerminal, TelemetryOutcomeOK)
			r.Status = 401
			return r
		}()},
		{"http missing status", func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventHTTPTerminal, TelemetryOutcomeOK)
			r.Status = 0
			return r
		}()},
		{"report cancel ok", validTelemetryRecord(TelemetryEventReportCancel, TelemetryOutcomeOK)},
		{"report start write failed", validTelemetryRecord(TelemetryEventReportStart, TelemetryOutcomeWriteFailedZero)},
		{"report reject ok", validTelemetryRecord(TelemetryEventReportReject, TelemetryOutcomeOK)},
		{"report lifecycle status", func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
			r.Status = 200
			return r
		}()},
		{"report lifecycle method", func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
			r.Method = "GET"
			return r
		}()},
		{"report lifecycle route", func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
			r.Route = TelemetryRouteHealth
			return r
		}()},
		{"health report method mismatch", func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventHTTPTerminal, TelemetryOutcomeOK)
			r.Method = "POST"
			return r
		}()},
		{"reports health method mismatch", func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventHTTPTerminal, TelemetryOutcomeOK)
			r.Route = TelemetryRouteReports
			return r
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			if err := EmitTelemetry(telemetryTestLogger(&buffer), test.record); err == nil {
				t.Fatal("accepted impossible telemetry record")
			}
			if buffer.Len() != 0 {
				t.Fatalf("invalid record produced log: %q", buffer.String())
			}
		})
	}
}

func TestEmitTelemetryRejectsInvalidRecordsWithoutLogging(t *testing.T) {
	canaries := []string{
		"Bearer credential-canary",
		"secret=QUERY_CANARY",
		"target.internal.example",
		"203.0.113.91",
		"https://provider.invalid/private",
		"panic: PANIC_CANARY",
		"dial tcp ERROR_CANARY",
	}
	tests := []struct {
		name   string
		mutate func(*TelemetryRecord)
	}{
		{"event", func(r *TelemetryRecord) { r.Event = TelemetryEvent(canaries[0]) }},
		{"outcome", func(r *TelemetryRecord) { r.Outcome = TelemetryOutcome(canaries[1]) }},
		{"request id", func(r *TelemetryRecord) { r.RequestID = canaries[2] }},
		{"report id", func(r *TelemetryRecord) { r.ReportID = canaries[3] }},
		{"method", func(r *TelemetryRecord) { r.Method = canaries[4] }},
		{"route", func(r *TelemetryRecord) { r.Route = TelemetryRoute(canaries[5]) }},
		{"negative active", func(r *TelemetryRecord) { r.Active = -1 }},
		{"negative capacity", func(r *TelemetryRecord) { r.Capacity = -1 }},
		{"negative request bytes", func(r *TelemetryRecord) { r.RequestBytes = -1 }},
		{"negative attempted bytes", func(r *TelemetryRecord) { r.ResponseAttemptedBytes = -1 }},
		{"negative response bytes", func(r *TelemetryRecord) { r.ResponseBytes = -1 }},
		{"negative duration", func(r *TelemetryRecord) { r.Duration = -1 }},
		{"negative runner duration", func(r *TelemetryRecord) { r.RunnerDuration = -1 }},
		{"negative marshal duration", func(r *TelemetryRecord) { r.MarshalDuration = -1 }},
		{"negative write duration", func(r *TelemetryRecord) { r.WriteDuration = -1 }},
		{"bad status", func(r *TelemetryRecord) { r.Status = 99 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
			test.mutate(&record)
			if err := EmitTelemetry(telemetryTestLogger(&buffer), record); err == nil {
				t.Fatal("EmitTelemetry accepted invalid record")
			}
			if buffer.Len() != 0 {
				t.Fatalf("invalid record produced log: %q", buffer.String())
			}
		})
	}

	var buffer bytes.Buffer
	if err := EmitTelemetry(telemetryTestLogger(&buffer), validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)); err != nil {
		t.Fatal(err)
	}
	logged := buffer.String()
	for _, canary := range canaries {
		if strings.Contains(logged, canary) {
			t.Fatalf("telemetry leaked canary %q in %q", canary, logged)
		}
	}
}

func TestEmitTelemetryClampsOversizedBoundedValues(t *testing.T) {
	var buffer bytes.Buffer
	record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
	record.Active = maxTelemetryCount + 1
	record.Capacity = maxTelemetryCount + 2
	record.RequestBytes = maxTelemetryBytes + 1
	record.ResponseAttemptedBytes = maxTelemetryBytes + 3
	record.ResponseBytes = maxTelemetryBytes + 3
	record.Duration = maxTelemetryDuration + time.Hour
	record.RunnerDuration = maxTelemetryDuration + time.Hour
	record.MarshalDuration = maxTelemetryDuration + time.Hour
	record.WriteDuration = maxTelemetryDuration + time.Hour
	if err := EmitTelemetry(telemetryTestLogger(&buffer), record); err != nil {
		t.Fatal(err)
	}
	got := decodeTelemetryLines(t, buffer.Bytes())[0]
	for _, key := range []string{"active", "capacity"} {
		if got[key] != float64(maxTelemetryCount) {
			t.Fatalf("%s=%v, want %d", key, got[key], maxTelemetryCount)
		}
	}
	for _, key := range []string{"request_bytes", "response_attempted_bytes", "response_bytes"} {
		if got[key] != float64(maxTelemetryBytes) {
			t.Fatalf("%s=%v, want %d", key, got[key], maxTelemetryBytes)
		}
	}
	for _, key := range []string{"duration_ms", "runner_duration_ms", "marshal_duration_ms", "write_duration_ms"} {
		if got[key] != float64(maxTelemetryDuration.Milliseconds()) {
			t.Fatalf("%s=%v, want %d", key, got[key], maxTelemetryDuration.Milliseconds())
		}
	}
}

func TestEmitTelemetryValidatesCounterRelationshipsBeforeClamping(t *testing.T) {
	tests := []TelemetryRecord{
		func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
			r.Active = maxTelemetryCount + 2
			r.Capacity = maxTelemetryCount + 1
			return r
		}(),
		func() TelemetryRecord {
			r := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
			r.ResponseBytes = maxTelemetryBytes + 2
			r.ResponseAttemptedBytes = maxTelemetryBytes + 1
			return r
		}(),
	}
	for _, record := range tests {
		var buffer bytes.Buffer
		if err := EmitTelemetry(telemetryTestLogger(&buffer), record); err == nil {
			t.Fatal("accepted invalid unclamped relationship")
		}
		if buffer.Len() != 0 {
			t.Fatalf("invalid relationship produced log: %q", buffer.String())
		}
	}
}

func TestTelemetryRecordHasOnlyAllowlistedTypedFields(t *testing.T) {
	typeInfo := reflect.TypeOf(TelemetryRecord{})
	want := []string{
		"Active", "AnalysisVerdict", "Capacity", "Duration", "Event", "FailedResults", "FindingCount",
		"MarshalDuration", "Method", "Outcome", "ReportID", "ReportStatus", "RequestBytes", "RequestID",
		"ResponseAttemptedBytes", "ResponseBytes", "Route", "RunnerDuration", "Status", "TotalResults",
		"WriteDuration", "reportDiagnosticsPresent",
	}
	got := make([]string, 0, typeInfo.NumField())
	for index := 0; index < typeInfo.NumField(); index++ {
		field := typeInfo.Field(index)
		got = append(got, field.Name)
		if field.Type.Kind() == reflect.Map || field.Type.Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			t.Fatalf("unsafe extensible field %s %s", field.Name, field.Type)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TelemetryRecord fields=%v, want %v", got, want)
	}
}

func TestOpaqueIDsHaveFixedSyntaxAreUniqueAndFallbackSafely(t *testing.T) {
	const count = 4096
	seen := make(map[string]struct{}, count*2)
	for index := 0; index < count; index++ {
		for _, item := range []struct {
			id      string
			pattern *regexp.Regexp
		}{{NewRequestID(), telemetryRequestIDPattern}, {NewReportID(), telemetryReportIDPattern}} {
			if !item.pattern.MatchString(item.id) {
				t.Fatalf("ID %q has invalid syntax", item.id)
			}
			if _, duplicate := seen[item.id]; duplicate {
				t.Fatalf("duplicate ID %q", item.id)
			}
			seen[item.id] = struct{}{}
		}
	}

	failureCanary := "credential-query-target-IP-provider-panic-error-canary"
	fallback1 := newOpaqueID("req", errorReader{err: errors.New(failureCanary)})
	fallback2 := newOpaqueID("req", errorReader{err: errors.New(failureCanary)})
	if !telemetryRequestIDPattern.MatchString(fallback1) || !telemetryRequestIDPattern.MatchString(fallback2) {
		t.Fatalf("fallback syntax invalid: %q %q", fallback1, fallback2)
	}
	if fallback1 == fallback2 {
		t.Fatalf("fallback IDs are not unique: %q", fallback1)
	}
	if strings.Contains(fallback1, failureCanary) || strings.Contains(fallback2, failureCanary) {
		t.Fatal("fallback incorporated reader error/client data")
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestTelemetryIDsAndLoggingAreConcurrentRaceSafe(t *testing.T) {
	const workers = 32
	const perWorker = 128
	var buffer bytes.Buffer
	logger := telemetryTestLogger(&buffer)
	ids := make(chan string, workers*perWorker)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := 0; index < perWorker; index++ {
				record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
				record.RequestID = NewRequestID()
				record.ReportID = NewReportID()
				ids <- record.RequestID
				if err := EmitTelemetry(logger, record); err != nil {
					t.Errorf("EmitTelemetry: %v", err)
				}
			}
		}()
	}
	wait.Wait()
	close(ids)
	seen := make(map[string]struct{}, workers*perWorker)
	for id := range ids {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate concurrent ID %q", id)
		}
		seen[id] = struct{}{}
	}
	if records := decodeTelemetryLines(t, buffer.Bytes()); len(records) != workers*perWorker {
		t.Fatalf("logged records=%d, want %d", len(records), workers*perWorker)
	}
}

func TestNewOpaqueIDHandlesShortRandomReads(t *testing.T) {
	id := newOpaqueID("req", io.LimitReader(strings.NewReader("0123456789abcdef"), 16))
	if !telemetryRequestIDPattern.MatchString(id) {
		t.Fatalf("ID %q has invalid syntax", id)
	}
}

type telemetryTestChecker struct{}

func (telemetryTestChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (telemetryTestChecker) Check(context.Context, diagnostic.Target) diagnostic.Result {
	return diagnostic.Result{Kind: diagnostic.KindDNS, Status: diagnostic.StatusHealthy}
}

func TestGeneratedTelemetryReportIDIsAcceptedPreservedAndEmitted(t *testing.T) {
	id := NewReportID()
	report, err := diagnostic.NewRunner(telemetryTestChecker{}).RunWithID(context.Background(), id, diagnostic.Request{
		Targets: []diagnostic.Target{{Kind: diagnostic.KindDNS, Address: "example.test"}},
	})
	if err != nil || report.ID != id {
		t.Fatalf("report ID=%q err=%v, want %q", report.ID, err, id)
	}

	var buffer bytes.Buffer
	record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
	record.ReportID = report.ID
	if err := EmitTelemetry(telemetryTestLogger(&buffer), record); err != nil {
		t.Fatalf("EmitTelemetry: %v", err)
	}
	if got := decodeTelemetryLines(t, buffer.Bytes())[0]["report_id"]; got != id {
		t.Fatalf("logged report_id=%v, want %q", got, id)
	}
}

func TestNewReportIDFallbackHasExistingReportSyntax(t *testing.T) {
	failureCanary := "credential-query-target-IP-provider-panic-error-canary"
	first := newReportID(errorReader{err: errors.New(failureCanary)})
	second := newReportID(errorReader{err: errors.New(failureCanary)})
	if !telemetryReportIDPattern.MatchString(first) || !telemetryReportIDPattern.MatchString(second) {
		t.Fatalf("fallback syntax invalid: %q %q", first, second)
	}
	if first == second {
		t.Fatalf("fallback IDs are not unique: %q", first)
	}
	if strings.Contains(first, failureCanary) || strings.Contains(second, failureCanary) {
		t.Fatal("fallback incorporated reader error/client data")
	}
}

type failingTelemetryHandler struct{}

func (failingTelemetryHandler) Enabled(context.Context, slog.Level) bool { return true }
func (failingTelemetryHandler) Handle(context.Context, slog.Record) error {
	return errors.New("sink failed")
}
func (h failingTelemetryHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h failingTelemetryHandler) WithGroup(string) slog.Handler      { return h }

type panickingTelemetryHandler struct{}

func (panickingTelemetryHandler) Enabled(context.Context, slog.Level) bool { return true }
func (panickingTelemetryHandler) Handle(context.Context, slog.Record) error {
	panic("TELEMETRY_PANIC_CANARY")
}
func (h panickingTelemetryHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h panickingTelemetryHandler) WithGroup(string) slog.Handler      { return h }

func TestEmitTelemetryIsBestEffortAfterValidation(t *testing.T) {
	record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
	if err := EmitTelemetry(slog.New(failingTelemetryHandler{}), record); err != nil {
		t.Fatalf("sink failure escaped: %v", err)
	}
	if err := EmitTelemetry(nil, record); err != nil {
		t.Fatalf("nil best-effort sink failed: %v", err)
	}
	record.Event = "invalid"
	if err := EmitTelemetry(slog.New(failingTelemetryHandler{}), record); !errors.Is(err, errInvalidTelemetryRecord) {
		t.Fatalf("validation error=%v", err)
	}
}

func TestTelemetryHandlerPanicIsBestEffortAndDoesNotLeakReportLeases(t *testing.T) {
	record := validTelemetryRecord(TelemetryEventReportFinish, TelemetryOutcomeOK)
	if err := EmitTelemetry(slog.New(panickingTelemetryHandler{}), record); err != nil {
		t.Fatalf("telemetry panic escaped as error: %v", err)
	}

	checker := &countingChecker{}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), slog.New(panickingTelemetryHandler{}), "test", ServerConfig{
		MaxConcurrentReports:        1,
		MaxConcurrentResponseWrites: 1,
		Mode:                        ModeTrustedLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	for requestNumber := 1; requestNumber <= 2; requestNumber++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, reportRequest(context.Background()))
		if recorder.Code != http.StatusOK || checker.callCount() != requestNumber {
			t.Fatalf("request %d status=%d calls=%d body=%s", requestNumber, recorder.Code, checker.callCount(), recorder.Body.String())
		}
	}
}

func telemetryIntegrationHandler(t *testing.T, logger *slog.Logger, runner *diagnostic.Runner, config ServerConfig) http.Handler {
	t.Helper()
	if config.Mode == "" {
		config.Mode = ModeTrustedLocal
	}
	handler, err := NewServerWithConfig(runner, logger, "test", config)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func telemetryEventNames(records []map[string]any) []string {
	events := make([]string, len(records))
	for index := range records {
		events[index], _ = records[index]["event"].(string)
	}
	return events
}

var reportDiagnosticLogKeys = []string{"report_status", "analysis_verdict", "total_results", "failed_results", "finding_count"}

func assertNoReportDiagnostics(t *testing.T, record map[string]any) {
	t.Helper()
	for _, key := range reportDiagnosticLogKeys {
		if _, exists := record[key]; exists {
			t.Fatalf("unexpected %s in telemetry record: %v", key, record)
		}
	}
}

func assertDeliveredReportDiagnostics(t *testing.T, records []map[string]any, status diagnostic.Status, verdict diagnostic.Verdict, total, failed, findings int) {
	t.Helper()
	finishCount := 0
	for _, record := range records {
		if record["event"] != string(TelemetryEventReportFinish) {
			assertNoReportDiagnostics(t, record)
			continue
		}
		finishCount++
		want := map[string]any{
			"report_status": string(status), "analysis_verdict": string(verdict),
			"total_results": float64(total), "failed_results": float64(failed), "finding_count": float64(findings),
		}
		for key, value := range want {
			if record[key] != value {
				t.Fatalf("finish %s=%v, want %v; record=%v", key, record[key], value, record)
			}
		}
		if record["outcome"] != string(TelemetryOutcomeOK) {
			t.Fatalf("delivered finish outcome=%v, want ok", record["outcome"])
		}
	}
	if finishCount != 1 {
		t.Fatalf("report_finish count=%d, want 1; records=%v", finishCount, records)
	}
}

func assertAllRecordsLackReportDiagnostics(t *testing.T, records []map[string]any) {
	t.Helper()
	for _, record := range records {
		assertNoReportDiagnostics(t, record)
	}
}

func TestCompleteSuccessfulWriteFallsBackToBaseFinishForUnavailableOrInvalidDiagnostics(t *testing.T) {
	const verdictCanary = "INVALID_VERDICT_CANARY"
	tests := []struct {
		name   string
		report diagnostic.Report
	}{
		{
			name: "nil analysis",
			report: diagnostic.Report{
				Status:  diagnostic.StatusHealthy,
				Summary: diagnostic.Summary{Total: 1},
			},
		},
		{
			name: "healthy status contradicts failed count",
			report: diagnostic.Report{
				Status:   diagnostic.StatusHealthy,
				Summary:  diagnostic.Summary{Total: 1, Failed: 1},
				Analysis: &diagnostic.Analysis{Verdict: diagnostic.VerdictAttention},
			},
		},
		{
			name: "invalid verdict canary",
			report: diagnostic.Report{
				Status:   diagnostic.StatusHealthy,
				Summary:  diagnostic.Summary{Total: 1},
				Analysis: &diagnostic.Analysis{Verdict: diagnostic.Verdict(verdictCanary)},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			server, err := newServer(diagnostic.NewRunner(successChecker{}), telemetryTestLogger(&logs), "test", ServerConfig{MaxConcurrentReports: 1})
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			request := reportRequest(context.Background())
			observation := &requestObservation{
				started: time.Now(), requestID: NewRequestID(), reportID: NewReportID(),
				method: http.MethodPost, route: TelemetryRouteReports,
			}
			request = request.WithContext(context.WithValue(request.Context(), requestObservationKey{}, observation))
			writer := &responseWriter{raw: recorder, observation: observation}
			writer.writeReport(reportJSON{payload: []byte("{}\n")})
			server.emitReportFinish(request, TelemetryOutcomeOK, test.report)
			server.emit(request, TelemetryEventHTTPTerminal, observation.outcome, observation.status)

			if recorder.Code != http.StatusOK || recorder.Body.String() != "{}\n" {
				t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
			}
			records := decodeTelemetryLines(t, logs.Bytes())
			finishCount, terminalCount := 0, 0
			for _, record := range records {
				assertNoReportDiagnostics(t, record)
				switch record["event"] {
				case string(TelemetryEventReportFinish):
					finishCount++
					if record["outcome"] != string(TelemetryOutcomeOK) {
						t.Fatalf("finish=%v", record)
					}
				case string(TelemetryEventHTTPTerminal):
					terminalCount++
					if record["outcome"] != string(TelemetryOutcomeOK) || record["status"] != float64(http.StatusOK) {
						t.Fatalf("terminal=%v", record)
					}
				}
			}
			if finishCount != 1 || terminalCount != 1 {
				t.Fatalf("finish=%d terminal=%d events=%v records=%v", finishCount, terminalCount, telemetryEventNames(records), records)
			}
			if strings.Contains(logs.String(), verdictCanary) {
				t.Fatalf("invalid tuple canary leaked: %s", logs.String())
			}
		})
	}
}

func TestReportWriterAndDeadlinePanicsAreContainedWithSingleTerminalEvents(t *testing.T) {
	tests := []struct {
		name           string
		writer         *panickingResponseWriter
		wantOutcome    TelemetryOutcome
		wantBodyBytes  int
		wantWriteCalls int
	}{
		{
			name: "WriteHeader commits then panics", writer: &panickingResponseWriter{header: make(http.Header), panicWriteHeader: true},
			wantOutcome: TelemetryOutcomeWriteFailedZero,
		},
		{
			name: "Write mutates hidden partial state then panics", writer: &panickingResponseWriter{header: make(http.Header), panicWrite: true},
			wantOutcome: TelemetryOutcomeWriteFailedZero, wantBodyBytes: 7, wantWriteCalls: 1,
		},
		{
			name: "ResponseController deadline panics", writer: &panickingResponseWriter{header: make(http.Header), panicDeadline: true},
			wantOutcome: TelemetryOutcomeOK, wantWriteCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			checker := &countingChecker{}
			handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), telemetryTestLogger(&logs), "test", ServerConfig{
				MaxConcurrentReports:        1,
				MaxConcurrentResponseWrites: 1,
				Mode:                        ModeTrustedLocal,
			})
			if err != nil {
				t.Fatal(err)
			}

			handler.ServeHTTP(test.writer, reportRequest(context.Background()))
			if test.writer.status != http.StatusOK || test.writer.writeCalls != test.wantWriteCalls {
				t.Fatalf("status=%d write calls=%d body bytes=%d", test.writer.status, test.writer.writeCalls, test.writer.body.Len())
			}
			if test.wantOutcome != TelemetryOutcomeOK && test.writer.body.Len() != test.wantBodyBytes {
				t.Fatalf("panic path appended a second body: %q", test.writer.body.String())
			}

			records := decodeTelemetryLines(t, logs.Bytes())
			finishCount, terminalCount := 0, 0
			for _, telemetry := range records {
				switch telemetry["event"] {
				case string(TelemetryEventReportFinish):
					finishCount++
					if telemetry["outcome"] != string(test.wantOutcome) {
						t.Fatalf("finish=%v", telemetry)
					}
				case string(TelemetryEventHTTPTerminal):
					terminalCount++
					if telemetry["outcome"] != string(test.wantOutcome) || telemetry["status"] != float64(http.StatusOK) {
						t.Fatalf("terminal=%v", telemetry)
					}
					attempted := telemetry["response_attempted_bytes"].(float64)
					actual := telemetry["response_bytes"].(float64)
					if attempted <= 0 || (test.wantOutcome == TelemetryOutcomeOK && actual != attempted) || (test.wantOutcome != TelemetryOutcomeOK && actual != 0) {
						t.Fatalf("terminal write accounting=%v", telemetry)
					}
				}
			}
			if finishCount != 1 || terminalCount != 1 {
				t.Fatalf("finish=%d terminal=%d events=%v", finishCount, terminalCount, telemetryEventNames(records))
			}
			if test.wantOutcome == TelemetryOutcomeOK {
				assertDeliveredReportDiagnostics(t, records, diagnostic.StatusHealthy, diagnostic.VerdictInconclusive, 1, 0, 0)
			} else {
				assertAllRecordsLackReportDiagnostics(t, records)
			}
			if strings.Contains(logs.String(), "PANIC_CANARY") {
				t.Fatalf("panic text leaked to telemetry: %s", logs.String())
			}

			recovered := httptest.NewRecorder()
			handler.ServeHTTP(recovered, reportRequest(context.Background()))
			if recovered.Code != http.StatusOK || checker.callCount() != 2 {
				t.Fatalf("lease did not recover: status=%d calls=%d", recovered.Code, checker.callCount())
			}
		})
	}
}

func TestServerTelemetrySuccessSequenceCorrelatesIDsBytesAndDurations(t *testing.T) {
	var logs bytes.Buffer
	body := `{"targets":[{"kind":"dns","address":"example.test"}]}`
	handler := telemetryIntegrationHandler(t, telemetryTestLogger(&logs), diagnostic.NewRunner(successChecker{}), ServerConfig{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/reports?secret=QUERY_CANARY", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var report diagnostic.Report
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	records := decodeTelemetryLines(t, logs.Bytes())
	wantEvents := []string{"report_submit", "report_admit", "report_start", "report_computed", "report_finish", "http_terminal"}
	if got := telemetryEventNames(records); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("events=%v, want %v; logs=%s", got, wantEvents, logs.String())
	}
	requestID, reportID := records[0]["request_id"], records[0]["report_id"]
	if !telemetryRequestIDPattern.MatchString(requestID.(string)) || !telemetryReportIDPattern.MatchString(reportID.(string)) || report.ID != reportID {
		t.Fatalf("request_id=%v report_id=%v report.ID=%q", requestID, reportID, report.ID)
	}
	for index, record := range records {
		if record["request_id"] != requestID || record["report_id"] != reportID {
			t.Fatalf("record %d correlation=%v/%v", index, record["request_id"], record["report_id"])
		}
		if record["route"] != string(TelemetryRouteReports) || record["method"] != http.MethodPost {
			t.Fatalf("record %d route/method=%v/%v", index, record["route"], record["method"])
		}
		if record["duration_ms"].(float64) < record["runner_duration_ms"].(float64) || record["duration_ms"].(float64) < record["marshal_duration_ms"].(float64) || record["duration_ms"].(float64) < record["write_duration_ms"].(float64) {
			t.Fatalf("record %d durations invalid: %v", index, record)
		}
	}
	terminal := records[len(records)-1]
	if terminal["status"] != float64(http.StatusOK) || terminal["outcome"] != string(TelemetryOutcomeOK) || terminal["request_bytes"] != float64(len(body)) || terminal["response_attempted_bytes"] != float64(recorder.Body.Len()) || terminal["response_bytes"] != float64(recorder.Body.Len()) {
		t.Fatalf("terminal=%v body bytes=%d/%d", terminal, len(body), recorder.Body.Len())
	}
	assertDeliveredReportDiagnostics(t, records, diagnostic.StatusHealthy, diagnostic.VerdictInconclusive, 1, 0, 0)
	if strings.Contains(logs.String(), "QUERY_CANARY") || strings.Contains(logs.String(), "example.test") {
		t.Fatalf("telemetry leaked request data: %s", logs.String())
	}
}

func TestServerTelemetryDeterministicEarlyTerminalPaths(t *testing.T) {
	tests := []struct {
		name, method, target, body string
		config                     ServerConfig
		contentLength              int64
		streamed                   bool
		status                     int
		outcome                    TelemetryOutcome
		events                     []string
		wantRequestBytes           *int64
	}{
		{name: "unauthorized report", method: http.MethodPost, target: "/api/v1/reports", body: `{}`, config: ServerConfig{Mode: ModePublic, APIKey: "AUTH_CANARY", RateLimitPerMinute: 2}, status: 401, outcome: TelemetryOutcomeUnauthorized, events: []string{"report_submit", "report_reject", "http_terminal"}},
		{name: "invalid json", method: http.MethodPost, target: "/api/v1/reports", body: `{`, status: 400, outcome: TelemetryOutcomeInvalidJSON, events: []string{"report_submit", "report_reject", "http_terminal"}},
		{name: "invalid request", method: http.MethodPost, target: "/api/v1/reports", body: `{"targets":[]}`, status: 422, outcome: TelemetryOutcomeInvalidRequest, events: []string{"report_submit", "report_reject", "http_terminal"}},
		{name: "declared too large", method: http.MethodPost, target: "/api/v1/reports", body: `BODY_DECLARED_CANARY`, contentLength: maxBodyBytes + 1, status: 413, outcome: TelemetryOutcomeRequestTooLarge, events: []string{"report_submit", "report_reject", "http_terminal"}, wantRequestBytes: int64Pointer(0)},
		{name: "streamed too large", method: http.MethodPost, target: "/api/v1/reports", body: `{"targets":[{"kind":"dns","address":"BODY_STREAMED_CANARY` + strings.Repeat("x", maxBodyBytes) + `"}]}`, streamed: true, config: ServerConfig{MaxConcurrentBodyDecodes: 1}, status: 413, outcome: TelemetryOutcomeRequestTooLarge, events: []string{"report_submit", "report_reject", "http_terminal"}, wantRequestBytes: int64Pointer(maxBodyBytes + 1)},
		{name: "unmatched", method: http.MethodGet, target: "/private/PATH_CANARY?token=QUERY_CANARY", status: 404, outcome: TelemetryOutcomeUnmatched, events: []string{"http_terminal"}},
		{name: "method", method: http.MethodPut, target: "/api/v1/health?token=QUERY_CANARY", status: 405, outcome: TelemetryOutcomeUnmatched, events: []string{"http_terminal"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := telemetryIntegrationHandler(t, telemetryTestLogger(&logs), diagnostic.NewRunner(successChecker{}), test.config)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			if test.contentLength != 0 {
				request.ContentLength = test.contentLength
			}
			if test.streamed {
				request.Body = io.NopCloser(strings.NewReader(test.body))
				request.ContentLength = -1
			}
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			records := decodeTelemetryLines(t, logs.Bytes())
			if got := telemetryEventNames(records); !reflect.DeepEqual(got, test.events) {
				t.Fatalf("events=%v logs=%s", got, logs.String())
			}
			terminal := records[len(records)-1]
			if terminal["status"] != float64(test.status) || terminal["outcome"] != string(test.outcome) {
				t.Fatalf("terminal=%v", terminal)
			}
			if test.wantRequestBytes != nil && terminal["request_bytes"] != float64(*test.wantRequestBytes) {
				t.Fatalf("request bytes=%v, want %d", terminal["request_bytes"], *test.wantRequestBytes)
			}
			assertAllRecordsLackReportDiagnostics(t, records)
			for _, canary := range []string{"AUTH_CANARY", "PATH_CANARY", "QUERY_CANARY", "BODY_DECLARED_CANARY", "BODY_STREAMED_CANARY"} {
				if strings.Contains(logs.String(), canary) {
					t.Fatalf("canary %q leaked: %s", canary, logs.String())
				}
			}
			if test.streamed {
				recovered := httptest.NewRecorder()
				handler.ServeHTTP(recovered, reportRequest(context.Background()))
				if recovered.Code != http.StatusOK {
					t.Fatalf("body decode token was not released: status=%d body=%s", recovered.Code, recovered.Body.String())
				}
			}
		})
	}
}

func int64Pointer(value int64) *int64 { return &value }

type telemetryAggregateChecker struct{}

func (telemetryAggregateChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (telemetryAggregateChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	if strings.Contains(target.Address, "fail") {
		return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusUnreachable, ErrorCode: "connection_failed", Message: "DIAGNOSTIC_PROSE_CANARY"}
	}
	return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusHealthy, Details: map[string]any{"addresses": []string{"203.0.113.77"}, "answer_count": 1}}
}

func TestDeliveredReportFinishSeparatesLifecycleOutcomeFromDiagnosticAggregates(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		status        diagnostic.Status
		verdict       diagnostic.Verdict
		total, failed int
		findings      int
	}{
		{name: "DNS failure", body: `{"targets":[{"kind":"dns","address":"fail-target.example"}]}`, status: diagnostic.StatusUnreachable, verdict: diagnostic.VerdictAttention, total: 1, failed: 1, findings: 1},
		{name: "mixed", body: `{"targets":[{"kind":"dns","address":"healthy-target.example"},{"kind":"dns","address":"fail-target.example"}]}`, status: diagnostic.StatusDegraded, verdict: diagnostic.VerdictAttention, total: 2, failed: 1, findings: 1},
		{name: "healthy zero findings", body: `{"targets":[{"kind":"dns","address":"healthy-target.example"}]}`, status: diagnostic.StatusHealthy, verdict: diagnostic.VerdictHealthy, total: 1, failed: 0, findings: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := telemetryIntegrationHandler(t, telemetryTestLogger(&logs), diagnostic.NewRunner(telemetryAggregateChecker{}), ServerConfig{MaxConcurrentReports: 1})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(test.body)))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			records := decodeTelemetryLines(t, logs.Bytes())
			assertDeliveredReportDiagnostics(t, records, test.status, test.verdict, test.total, test.failed, test.findings)
			terminal := records[len(records)-1]
			if terminal["event"] != string(TelemetryEventHTTPTerminal) || terminal["outcome"] != string(TelemetryOutcomeOK) {
				t.Fatalf("terminal=%v", terminal)
			}
			for _, canary := range []string{"healthy-target.example", "fail-target.example", "203.0.113.77", "DIAGNOSTIC_PROSE_CANARY"} {
				if strings.Contains(logs.String(), canary) {
					t.Fatalf("privacy canary %q leaked: %s", canary, logs.String())
				}
			}
		})
	}
}

func TestServerTelemetryCancellationIsOperational499WithoutBody(t *testing.T) {
	var logs bytes.Buffer
	checker := blockingChecker{started: make(chan struct{}, 1), release: make(chan struct{})}
	handler := telemetryIntegrationHandler(t, telemetryTestLogger(&logs), diagnostic.NewRunner(checker), ServerConfig{MaxConcurrentReports: 1})
	ctx, cancel := context.WithCancel(context.Background())
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { handler.ServeHTTP(recorder, reportRequest(ctx)); close(done) }()
	<-checker.started
	cancel()
	<-done
	close(checker.release)
	if recorder.Body.Len() != 0 {
		t.Fatalf("cancel wrote body %q", recorder.Body.String())
	}
	records := decodeTelemetryLines(t, logs.Bytes())
	want := []string{"report_submit", "report_admit", "report_start", "report_cancel", "http_terminal"}
	if got := telemetryEventNames(records); !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%v logs=%s", got, logs.String())
	}
	if records[len(records)-2]["status"] != float64(0) || records[len(records)-1]["status"] != float64(499) || records[len(records)-1]["outcome"] != "cancelled" {
		t.Fatalf("cancel records=%v", records)
	}
	assertAllRecordsLackReportDiagnostics(t, records)
}

func TestTelemetryWriteFailuresKeepCommittedStatusForZeroAndPartialWrites(t *testing.T) {
	for _, actual := range []int{0, 7} {
		var logs bytes.Buffer
		handler := telemetryIntegrationHandler(t, telemetryTestLogger(&logs), diagnostic.NewRunner(successChecker{}), ServerConfig{MaxConcurrentReports: 1, MaxConcurrentResponseWrites: 1})
		writer := &failingResponseWriter{header: make(http.Header), actualPerWrite: actual, err: io.ErrClosedPipe}
		handler.ServeHTTP(writer, reportRequest(context.Background()))
		records := decodeTelemetryLines(t, logs.Bytes())
		terminal := records[len(records)-1]
		wantOutcome := string(TelemetryOutcomeWriteFailedPartial)
		if actual == 0 {
			wantOutcome = string(TelemetryOutcomeWriteFailedZero)
		}
		if terminal["status"] != float64(http.StatusOK) || terminal["outcome"] != wantOutcome || terminal["response_bytes"] != float64(actual) {
			t.Fatalf("actual=%d terminal=%v", actual, terminal)
		}
		assertAllRecordsLackReportDiagnostics(t, records)
	}
}

type panicTelemetryChecker struct{}

func (panicTelemetryChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (panicTelemetryChecker) Check(context.Context, diagnostic.Target) diagnostic.Result {
	panic("PANIC_PROSE_CANARY")
}

func TestServerTelemetryRatePolicyMarshalAndCheckerPanicSequences(t *testing.T) {
	t.Run("rate", func(t *testing.T) {
		var logs bytes.Buffer
		config := ServerConfig{Mode: ModePublic, APIKey: "AUTH_CANARY", RateLimitPerMinute: 1, MaxConcurrentReports: 1}
		handler := telemetryIntegrationHandler(t, telemetryTestLogger(&logs), diagnostic.NewRunner(successChecker{}), config)
		first := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		first.Header.Set("Authorization", "Bearer AUTH_CANARY")
		handler.ServeHTTP(httptest.NewRecorder(), first)
		logs.Reset()
		request := reportRequest(context.Background())
		request.Header.Set("Authorization", "Bearer AUTH_CANARY")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("status=%d", recorder.Code)
		}
		if got := telemetryEventNames(decodeTelemetryLines(t, logs.Bytes())); !reflect.DeepEqual(got, []string{"report_submit", "report_reject", "http_terminal"}) {
			t.Fatalf("events=%v logs=%s", got, logs.String())
		}
		if strings.Contains(logs.String(), "AUTH_CANARY") {
			t.Fatalf("auth leaked: %s", logs.String())
		}
	})

	tests := []struct {
		name   string
		runner *diagnostic.Runner
		config ServerConfig
		auth   bool
		status int
		events []string
		finish TelemetryOutcome
	}{
		{name: "policy", runner: diagnostic.NewRunner(policyBlockedChecker{}), config: ServerConfig{Mode: ModePublic, APIKey: "AUTH_CANARY", RateLimitPerMinute: 2, MaxConcurrentReports: 1}, auth: true, status: 422, events: []string{"report_submit", "report_admit", "report_start", "report_computed", "report_reject", "http_terminal"}, finish: TelemetryOutcomePolicy},
		{name: "marshal", runner: diagnostic.NewRunner(detailsChecker{details: map[string]any{"provider": make(chan int)}}), config: ServerConfig{MaxConcurrentReports: 1}, status: 500, events: []string{"report_submit", "report_admit", "report_start", "report_computed", "report_finish", "http_terminal"}, finish: TelemetryOutcomeSerialization},
		{name: "checker panic", runner: diagnostic.NewRunner(panicTelemetryChecker{}), config: ServerConfig{MaxConcurrentReports: 1}, status: 200, events: []string{"report_submit", "report_admit", "report_start", "report_computed", "report_finish", "http_terminal"}, finish: TelemetryOutcomeOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := telemetryIntegrationHandler(t, telemetryTestLogger(&logs), test.runner, test.config)
			request := reportRequest(context.Background())
			request.URL.RawQuery = "token=QUERY_CANARY"
			request.RemoteAddr = "203.0.113.99:4444"
			if test.auth {
				request.Header.Set("Authorization", "Bearer AUTH_CANARY")
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			records := decodeTelemetryLines(t, logs.Bytes())
			if got := telemetryEventNames(records); !reflect.DeepEqual(got, test.events) {
				t.Fatalf("events=%v logs=%s", got, logs.String())
			}
			if test.name == "policy" {
				if records[len(records)-2]["outcome"] != string(test.finish) {
					t.Fatalf("reject=%v", records[len(records)-2])
				}
			} else if records[len(records)-2]["outcome"] != string(test.finish) {
				t.Fatalf("finish=%v", records[len(records)-2])
			}
			if test.name == "checker panic" {
				if records[len(records)-1]["outcome"] != string(TelemetryOutcomeOK) {
					t.Fatalf("HTTP terminal not OK: %v", records[len(records)-1])
				}
				assertDeliveredReportDiagnostics(t, records, diagnostic.StatusUnreachable, diagnostic.VerdictAttention, 1, 1, 1)
			} else {
				assertAllRecordsLackReportDiagnostics(t, records)
			}
			for _, canary := range []string{"AUTH_CANARY", "QUERY_CANARY", "example.test", "203.0.113.99", "PANIC_PROSE_CANARY", "provider"} {
				if strings.Contains(logs.String(), canary) {
					t.Fatalf("canary %q leaked: %s", canary, logs.String())
				}
			}
		})
	}
}

type lockedTelemetryBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (buffer *lockedTelemetryBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.data.Write(payload)
}
func (buffer *lockedTelemetryBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.data.Bytes()...)
}

func TestConcurrentRealRequestsHaveUniqueRequestIDsAndSingleTerminals(t *testing.T) {
	const requests = 64
	logs := &lockedTelemetryBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
		if attr.Key == slog.TimeKey || attr.Key == slog.LevelKey {
			return slog.Attr{}
		}
		return attr
	}}))
	handler := telemetryIntegrationHandler(t, logger, diagnostic.NewRunner(successChecker{}), ServerConfig{})
	var wait sync.WaitGroup
	for index := 0; index < requests; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
			if recorder.Code != http.StatusOK {
				t.Errorf("status=%d", recorder.Code)
			}
		}()
	}
	wait.Wait()
	records := decodeTelemetryLines(t, logs.Bytes())
	if len(records) != requests {
		t.Fatalf("terminal count=%d want=%d", len(records), requests)
	}
	seen := make(map[string]bool, requests)
	for _, record := range records {
		id := record["request_id"].(string)
		if seen[id] {
			t.Fatalf("duplicate request ID %q", id)
		}
		seen[id] = true
		if record["event"] != "http_terminal" {
			t.Fatalf("non-terminal record=%v", record)
		}
	}
}

func TestServerCapacityTelemetryReportsActiveAndCapacity(t *testing.T) {
	logs := &lockedTelemetryBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	checker := blockingChecker{started: make(chan struct{}, 1), release: make(chan struct{})}
	handler := telemetryIntegrationHandler(t, logger, diagnostic.NewRunner(checker), ServerConfig{MaxConcurrentReports: 1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { handler.ServeHTTP(httptest.NewRecorder(), reportRequest(ctx)); close(done) }()
	<-checker.started
	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, reportRequest(context.Background()))
	if busy.Code != http.StatusServiceUnavailable {
		t.Fatalf("busy status=%d", busy.Code)
	}
	cancel()
	<-done
	close(checker.release)
	records := decodeTelemetryLines(t, logs.Bytes())
	foundReject := false
	for _, record := range records {
		if record["event"] == "report_reject" && record["outcome"] == string(TelemetryOutcomeServerCapacity) {
			foundReject = true
			if record["active"] != float64(1) || record["capacity"] != float64(1) {
				t.Fatalf("capacity reject=%v", record)
			}
		}
	}
	if !foundReject {
		t.Fatalf("server capacity reject missing: %v", records)
	}
	assertAllRecordsLackReportDiagnostics(t, records)
}

func TestWriteFailureSemanticTableAllowsAnyCommittedHTTPStatus(t *testing.T) {
	for _, status := range []int{200, 418, 503} {
		for _, outcome := range []TelemetryOutcome{TelemetryOutcomeWriteFailedZero, TelemetryOutcomeWriteFailedPartial} {
			record := validTelemetryRecord(TelemetryEventHTTPTerminal, outcome)
			record.Status = status
			if _, err := validateTelemetryRecord(record); err != nil {
				t.Fatalf("status=%d outcome=%s: %v", status, outcome, err)
			}
		}
	}
}

type telemetryDeadlineChecker struct{}

func (telemetryDeadlineChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (telemetryDeadlineChecker) Check(ctx context.Context, target diagnostic.Target) diagnostic.Result {
	<-ctx.Done()
	return diagnostic.Result{Kind: target.Kind, Address: "late-healthy.example", Status: diagnostic.StatusHealthy}
}

func TestReportFinishTelemetryMatchesCorrectedCheckerDeadlineResult(t *testing.T) {
	var logs bytes.Buffer
	runner := diagnostic.NewRunnerWithSupervisor(mustAPICheckerSupervisor(t, 1), telemetryDeadlineChecker{})
	handler := telemetryIntegrationHandler(t, telemetryTestLogger(&logs), runner, ServerConfig{MaxConcurrentReports: 1})
	body := `{"timeout_ms":100,"targets":[{"kind":"dns","address":"DEADLINE_TARGET_CANARY"}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var report diagnostic.Report
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].ErrorCode != "timeout" || report.Results[0].Status != diagnostic.StatusUnreachable || report.Status != diagnostic.StatusUnreachable {
		t.Fatalf("report=%+v", report)
	}
	records := decodeTelemetryLines(t, logs.Bytes())
	var finish map[string]any
	for _, record := range records {
		if record["event"] == string(TelemetryEventReportFinish) {
			finish = record
		}
	}
	if finish == nil || finish["outcome"] != string(TelemetryOutcomeOK) {
		t.Fatalf("finish=%v records=%v", finish, records)
	}
	assertDeliveredReportDiagnostics(t, records, diagnostic.StatusUnreachable, diagnostic.VerdictAttention, 1, 1, 1)
	if strings.Contains(logs.String(), "DEADLINE_TARGET_CANARY") || strings.Contains(logs.String(), "late-healthy.example") {
		t.Fatalf("telemetry leaked target data: %s", logs.String())
	}
}

func mustAPICheckerSupervisor(t *testing.T, capacity int) *diagnostic.CheckerSupervisor {
	t.Helper()
	supervisor, err := diagnostic.NewCheckerSupervisor(capacity)
	if err != nil {
		t.Fatal(err)
	}
	return supervisor
}

func TestCheckerCapacityFinishesWithStableOutcomeWhileHTTPRemainsOK(t *testing.T) {
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	checker := blockingChecker{started: make(chan struct{}, 1), release: make(chan struct{})}
	runner := diagnostic.NewRunnerWithSupervisor(supervisor, checker)
	firstDone := make(chan struct{})
	go func() {
		_, _ = runner.Run(context.Background(), diagnostic.Request{Targets: []diagnostic.Target{{Kind: diagnostic.KindDNS, Address: "FIRST_TARGET_CANARY"}}})
		close(firstDone)
	}()
	<-checker.started
	var logs bytes.Buffer
	handler := telemetryIntegrationHandler(t, telemetryTestLogger(&logs), runner, ServerConfig{MaxConcurrentReports: 1})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, reportRequest(context.Background()))
	close(checker.release)
	<-firstDone
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	records := decodeTelemetryLines(t, logs.Bytes())
	if got := records[len(records)-2]["outcome"]; got != string(TelemetryOutcomeOK) {
		t.Fatalf("finish outcome=%v records=%v", got, records)
	}
	if got := records[len(records)-1]["outcome"]; got != string(TelemetryOutcomeOK) {
		t.Fatalf("terminal outcome=%v", got)
	}
	assertDeliveredReportDiagnostics(t, records, diagnostic.StatusUnreachable, diagnostic.VerdictInconclusive, 1, 1, 1)
	if strings.Contains(logs.String(), "FIRST_TARGET_CANARY") {
		t.Fatalf("target leaked: %s", logs.String())
	}
}

func TestWriteCapacityHasDeterministicFinishAndTerminalSequence(t *testing.T) {
	logs := &lockedTelemetryBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	handler := telemetryIntegrationHandler(t, logger, diagnostic.NewRunner(successChecker{}), ServerConfig{MaxConcurrentReports: 1, MaxConcurrentResponseWrites: 1})
	blocked := newBarrierResponseWriter()
	firstDone := make(chan struct{})
	go func() { handler.ServeHTTP(blocked, reportRequest(context.Background())); close(firstDone) }()
	<-blocked.started
	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, reportRequest(context.Background()))
	if busy.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", busy.Code, busy.Body.String())
	}
	close(blocked.release)
	<-firstDone
	records := decodeTelemetryLines(t, logs.Bytes())
	var busyID string
	for _, record := range records {
		if record["event"] == "http_terminal" && record["status"] == float64(http.StatusServiceUnavailable) {
			busyID = record["report_id"].(string)
		}
	}
	if busyID == "" {
		t.Fatalf("busy terminal missing: %v", records)
	}
	var events []string
	for _, record := range records {
		if record["report_id"] == busyID {
			events = append(events, record["event"].(string))
			if record["event"] == "report_finish" && record["outcome"] != string(TelemetryOutcomeWriteCapacity) {
				t.Fatalf("finish=%v", record)
			}
		}
	}
	want := []string{"report_submit", "report_admit", "report_start", "report_computed", "report_finish", "http_terminal"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want=%v records=%v", events, want, records)
	}
	for _, record := range records {
		if record["report_id"] == busyID {
			assertNoReportDiagnostics(t, record)
		}
	}
}
