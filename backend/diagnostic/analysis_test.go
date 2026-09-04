package diagnostic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var analysisTestNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

const (
	maxConsumerAnalysisItems = 64
	maxConsumerCoverageItems = 128
)

func maximumAnalysisReport() Report {
	results := make([]Result, 20)
	for index := range results {
		results[index] = Result{
			Kind: KindHTTPS, Address: fmt.Sprintf("https://max-%02d.example.test", index),
			Status: StatusUnreachable, ErrorCode: "unexpected_status", StartedAt: analysisTestNow,
			Details: map[string]any{
				"status_code": 503, "expected_status": 200, "tls_version": "TLS 1.3",
				"certificate_expires_at": analysisTestNow.Add(-time.Hour),
			},
		}
	}
	analysis := Analyze(results, analysisTestNow)
	return Report{
		ID: "maximum-analysis-report", Status: StatusUnreachable, StartedAt: analysisTestNow,
		DurationMS: 20, Summary: Summary{Total: 20, Failed: 20}, Results: results, Analysis: &analysis,
	}
}

func TestMaximumAnalysisProducerCardinalityFitsBoundedConsumers(t *testing.T) {
	report := maximumAnalysisReport()
	analysis := report.Analysis
	if got := [4]int{len(analysis.Findings), len(analysis.Evidence), len(analysis.Actions), len(analysis.Coverage.Available)}; got != [4]int{40, 40, 40, 80} {
		t.Fatalf("maximum producer cardinality = %v, want [40 40 40 80]", got)
	}
	for name, count := range map[string]int{
		"findings": len(analysis.Findings), "evidence": len(analysis.Evidence), "actions": len(analysis.Actions),
	} {
		if count > maxConsumerAnalysisItems {
			t.Fatalf("%s producer cardinality %d exceeds consumer cap %d", name, count, maxConsumerAnalysisItems)
		}
	}
	for name, count := range map[string]int{
		"coverage.available": len(analysis.Coverage.Available), "coverage.missing": len(analysis.Coverage.Missing),
		"coverage.provider_failures": len(analysis.Coverage.ProviderFailures), "coverage.limitations": len(analysis.Coverage.Limitations),
	} {
		if count > maxConsumerCoverageItems {
			t.Fatalf("%s producer cardinality %d exceeds consumer cap %d", name, count, maxConsumerCoverageItems)
		}
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join("..", "..", "testdata", "maximum-analysis-report.json")
	if os.Getenv("UPDATE_ANALYSIS_FIXTURE") == "1" {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(encoded, '\n'), want) {
		t.Fatal("maximum analysis fixture is stale; regenerate with UPDATE_ANALYSIS_FIXTURE=1 go test ./backend/diagnostic -run TestMaximumAnalysisProducerCardinalityFitsBoundedConsumers")
	}
}

func TestAnalysisCoveragePerResultMaximaFitBoundedConsumers(t *testing.T) {
	maxMissing := Analyze([]Result{{Kind: KindHTTPS, Status: StatusHealthy, Details: map[string]any{}}}, analysisTestNow)
	if got := len(maxMissing.Coverage.Missing); got != 3 {
		t.Fatalf("maximum missing signals per result = %d, want 3", got)
	}
	maxLimitations := Analyze([]Result{{Kind: KindHTTPS, Status: StatusHealthy, ErrorCode: "unexpected_status", Details: map[string]any{}}}, analysisTestNow)
	if got := len(maxLimitations.Coverage.Limitations); got != 5 {
		t.Fatalf("maximum limitations per result = %d, want 5", got)
	}
	maxProviderFailures := Analyze([]Result{{Kind: KindTraceroute, Status: StatusHealthy, Details: map[string]any{
		"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0,
		"attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
		"geoip_provider_failures": 1,
	}}}, analysisTestNow)
	if len(maxProviderFailures.Coverage.ProviderFailures) != 1 {
		t.Fatalf("maximum provider failures per result = %d, want 1", len(maxProviderFailures.Coverage.ProviderFailures))
	}
	for name, perResult := range map[string]int{"missing": 3, "limitations": 5, "provider_failures": 1} {
		if total := perResult * 20; total > maxConsumerCoverageItems {
			t.Fatalf("coverage.%s maximum %d exceeds consumer cap %d", name, total, maxConsumerCoverageItems)
		}
	}
}

func TestAnalyzeDNSFailureFromTypedFacts(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindDNS, Address: "missing.example", Status: StatusUnreachable, ErrorCode: "connection_failed"}}, analysisTestNow)
	if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 {
		t.Fatalf("analysis = %+v", analysis)
	}
	finding := analysis.Findings[0]
	if finding.ID == "" || finding.Code != FindingDNSResolutionFailed || finding.Confidence != ConfidenceDirect {
		t.Fatalf("finding = %+v", finding)
	}
	if len(analysis.Evidence) != 1 || analysis.Evidence[0].ResultIndex != 0 || analysis.Evidence[0].Kind != KindDNS || analysis.Evidence[0].Signal != "error_code" || analysis.Evidence[0].Provenance != ProvenanceResult {
		t.Fatalf("evidence = %+v", analysis.Evidence)
	}
	if len(analysis.Actions) != 1 || analysis.Actions[0].EscalationCondition == "" {
		t.Fatalf("actions = %+v", analysis.Actions)
	}
}

func TestAnalyzeHealthyDNSAndTCPFailureAsEndpointReachability(t *testing.T) {
	analysis := Analyze([]Result{
		{Kind: KindDNS, Address: "service.example", Status: StatusHealthy, Details: map[string]any{"addresses": []string{"203.0.113.10"}, "answer_count": 1}},
		{Kind: KindTCP, Address: "203.0.113.10:443", Status: StatusUnreachable, ErrorCode: "connection_failed"},
	}, analysisTestNow)
	if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingEndpointConnectFailed {
		t.Fatalf("analysis = %+v", analysis)
	}
	if analysis.Findings[0].Category != CategoryConnectivity || analysis.Evidence[0].ResultIndex != 1 {
		t.Fatalf("finding/evidence = %+v / %+v", analysis.Findings, analysis.Evidence)
	}
}

func TestAnalyzeHTTPUnexpectedStatusFromObservedDetails(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindHTTP, Address: "https://service.example/health", Status: StatusUnreachable, ErrorCode: "unexpected_status", Details: map[string]any{"status_code": 503, "expected_status": 200}}}, analysisTestNow)
	if len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingHTTPUnexpectedStatus {
		t.Fatalf("analysis = %+v", analysis)
	}
	evidence := analysis.Evidence[0]
	if evidence.Signal != "http.status_code" || evidence.Observed != "503" || evidence.Expected != "200" || evidence.Provenance != ProvenanceDetails {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestAnalyzeStableExecutionAndInputErrorCodes(t *testing.T) {
	tests := []struct {
		name      string
		errorCode string
		want      FindingCode
	}{
		{name: "invalid target", errorCode: "invalid_url", want: FindingInvalidTarget},
		{name: "timeout", errorCode: "timeout", want: FindingExecutionTimeout},
		{name: "cancelled", errorCode: "cancelled", want: FindingExecutionCancelled},
		{name: "policy", errorCode: "network_policy_blocked", want: FindingTargetPolicyBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := Analyze([]Result{{Kind: KindHTTP, Address: "target", Status: StatusUnreachable, ErrorCode: tt.errorCode}}, analysisTestNow)
			if len(analysis.Findings) != 1 || analysis.Findings[0].Code != tt.want {
				t.Fatalf("analysis = %+v", analysis)
			}
		})
	}
}

func TestAnalyzeResponseReadFailureUsesStableLimitationWithoutClaimingConnectFailure(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindHTTP, Address: "http://example.test", Status: StatusUnreachable, ErrorCode: "response_read_failed", Message: "BODY_ERROR_CANARY"}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) == 0 {
		t.Fatalf("analysis=%+v", analysis)
	}
	limitation := analysis.Coverage.Limitations[len(analysis.Coverage.Limitations)-1]
	if limitation.Signal != "response_body" || strings.Contains(limitation.Reason, "BODY_ERROR_CANARY") {
		t.Fatalf("limitation=%+v", limitation)
	}
}

func TestAnalyzeServiceGreetingSuccessHasExactScopeLimitation(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindSSH, Address: "ssh.example:22", Status: StatusHealthy, Details: map[string]any{"verification_scope": "server_greeting"}}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) != 1 {
		t.Fatalf("analysis=%+v", analysis)
	}
	want := CoverageIssue{Code: CoverageUnsupportedDetails, ResultIndex: 0, Kind: KindSSH, Signal: "service_verification_scope", Reason: "only the expected server-first greeting was observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested"}
	if analysis.Coverage.Limitations[0] != want {
		t.Fatalf("limitation=%+v want=%+v", analysis.Coverage.Limitations[0], want)
	}
}

func TestAnalyzeServiceGreetingFailureHasOneFixedPrivateFinding(t *testing.T) {
	const canary = "RAW_GREETING_CANARY_220_SECRET"
	analysis := Analyze([]Result{{Kind: KindSMTP, Address: "mail.example:25", Status: StatusDegraded, ErrorCode: ResultErrorServiceGreetingUnverified, Message: canary, Details: map[string]any{}}}, analysisTestNow)
	if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingServiceGreetingUnverified || len(analysis.Evidence) != 1 || len(analysis.Actions) != 1 {
		t.Fatalf("analysis=%+v", analysis)
	}
	finding, evidence, action := analysis.Findings[0], analysis.Evidence[0], analysis.Actions[0]
	if finding.Severity != SeverityWarning || finding.Category != CategoryApplication || finding.Confidence != ConfidenceDirect || evidence.Provenance != ProvenanceResult {
		t.Fatalf("finding=%+v evidence=%+v", finding, evidence)
	}
	if finding.Title != "Service greeting was not verified" || finding.Summary != "The transport connected, but the expected server-first service greeting was not verified." || evidence.Signal != "error_code" || evidence.Observed != "service_greeting_unverified" || evidence.Expected != "expected server-first greeting" {
		t.Fatalf("finding=%+v evidence=%+v", finding, evidence)
	}
	if action.Title != "Verify the service greeting" || action.Step != "Verify that the intended service is listening and returns its expected server-first greeting without client input, then repeat the check." || action.ExpectedResult != "The endpoint returns the expected bounded server-first greeting." || action.EscalationCondition != "Escalate to the service owner if the expected greeting is still not verified after listener and protocol configuration are confirmed." {
		t.Fatalf("action=%+v", action)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil || bytes.Contains(encoded, []byte(canary)) {
		t.Fatalf("serialized analysis leaked greeting: %s err=%v", encoded, err)
	}
}

func TestAnalyzeServiceGreetingDetailShapesAreClosed(t *testing.T) {
	future := analysisTestNow.Add(90 * 24 * time.Hour)
	valid := []Result{
		{Kind: KindSSH, Status: StatusHealthy, Details: map[string]any{"verification_scope": "server_greeting"}},
		{Kind: KindIMAPS, Status: StatusHealthy, Details: map[string]any{
			"verification_scope": "server_greeting", "tls_version": "TLS 1.3", "cipher_suite": "TLS_AES_128_GCM_SHA256",
			"certificate_subject": "CN=imap.example.test", "certificate_expires_at": future,
		}},
		{Kind: KindPOP3, Status: StatusDegraded, ErrorCode: ResultErrorServiceGreetingUnverified},
		{Kind: KindSMTPS, Status: StatusDegraded, ErrorCode: ResultErrorServiceGreetingUnverified, Details: map[string]any{}},
	}
	for index, result := range valid {
		analysis := Analyze([]Result{result}, analysisTestNow)
		if result.Status == StatusHealthy {
			if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) != 1 || analysis.Coverage.Limitations[0].Signal != "service_verification_scope" {
				t.Fatalf("valid healthy shape %d rejected: %+v", index, analysis)
			}
		} else if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingServiceGreetingUnverified {
			t.Fatalf("valid degraded shape %d rejected: %+v", index, analysis)
		}
	}

	invalid := []Result{
		{Kind: KindSSH, Status: StatusHealthy, Details: map[string]any{}},
		{Kind: KindSSH, Status: StatusHealthy, Details: map[string]any{"verification_scope": 7}},
		{Kind: KindSSH, Status: StatusHealthy, Details: map[string]any{"verification_scope": "server_greeting", "tls_version": "TLS 1.3"}},
		{Kind: KindIMAPS, Status: StatusHealthy, Details: map[string]any{
			"verification_scope": "server_greeting", "tls_version": "TLS 1.3", "certificate_expires_at": future, "raw_greeting": "HOSTILE_GREETING_CANARY",
		}},
		{Kind: KindIMAPS, Status: StatusHealthy, Details: map[string]any{
			"verification_scope": "future_scope", "tls_version": "TLS 1.3", "certificate_expires_at": future,
		}},
		{Kind: KindSMTP, Status: StatusDegraded, ErrorCode: ResultErrorServiceGreetingUnverified, Details: map[string]any{"verification_scope": "server_greeting"}},
		{Kind: KindPOP3S, Status: StatusDegraded, ErrorCode: ResultErrorServiceGreetingUnverified, Details: map[string]any{"tls_version": "TLS 1.3"}},
		{Kind: KindSMTP, Status: StatusHealthy, ErrorCode: ResultErrorServiceGreetingUnverified, Details: map[string]any{"verification_scope": "server_greeting"}},
		{Kind: KindSMTP, Status: StatusUnreachable, ErrorCode: ResultErrorServiceGreetingUnverified},
	}
	for index, result := range invalid {
		analysis := Analyze([]Result{result}, analysisTestNow)
		if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || !coverageHasSignal(analysis.Coverage.Limitations, "service_verification_details") {
			t.Fatalf("invalid greeting shape %d accepted: result=%+v analysis=%+v", index, result, analysis)
		}
		encoded, err := json.Marshal(analysis)
		if err != nil || bytes.Contains(encoded, []byte("HOSTILE_GREETING_CANARY")) {
			t.Fatalf("invalid greeting shape %d leaked details: %s err=%v", index, encoded, err)
		}
	}
}

func TestAnalyzeServiceGreetingTimeoutAndCancellationFindingsWin(t *testing.T) {
	for _, test := range []struct {
		code string
		want FindingCode
	}{
		{"timeout", FindingExecutionTimeout},
		{"cancelled", FindingExecutionCancelled},
	} {
		analysis := Analyze([]Result{{Kind: KindIMAPS, Status: StatusUnreachable, ErrorCode: test.code, Details: map[string]any{"verification_scope": "server_greeting"}}}, analysisTestNow)
		if len(analysis.Findings) != 1 || analysis.Findings[0].Code != test.want {
			t.Fatalf("code=%q analysis=%+v", test.code, analysis)
		}
		for _, finding := range analysis.Findings {
			if finding.Code == FindingServiceGreetingUnverified {
				t.Fatalf("code=%q emitted greeting finding: %+v", test.code, analysis)
			}
		}
	}
}

func TestAnalyzeCancelledIsInconclusive(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindHTTP, Status: StatusUnreachable, ErrorCode: "cancelled"}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingExecutionCancelled {
		t.Fatalf("cancelled analysis = %+v", analysis)
	}
}

func TestAnalyzeTLSObservedDowngradeAndCertificateDates(t *testing.T) {
	analysis := Analyze([]Result{
		{Kind: KindHTTPS, Address: "https://downgrade.example", Status: StatusUnreachable, ErrorCode: "tls_downgrade"},
		{Kind: KindIMAPS, Address: "expired.example", Status: StatusHealthy, Details: map[string]any{"verification_scope": "server_greeting", "tls_version": "TLS 1.3", "certificate_expires_at": analysisTestNow.Add(-time.Hour)}},
		{Kind: KindHTTPS, Address: "https://soon.example", Status: StatusHealthy, Details: map[string]any{"tls_version": "TLS 1.2", "certificate_expires_at": analysisTestNow.Add(7 * 24 * time.Hour)}},
	}, analysisTestNow)
	want := []FindingCode{FindingTLSCertificateExpired, FindingTLSDowngrade, FindingTLSCertificateExpiring}
	if len(analysis.Findings) != len(want) {
		t.Fatalf("analysis = %+v", analysis)
	}
	for index, code := range want {
		if analysis.Findings[index].Code != code || analysis.Findings[index].Category != CategorySecurity {
			t.Fatalf("finding[%d] = %+v", index, analysis.Findings[index])
		}
	}
	if analysis.Evidence[1].Signal != "tls.certificate_expires_at" || analysis.Evidence[1].Provenance != ProvenanceDetails {
		t.Fatalf("certificate evidence = %+v", analysis.Evidence[1])
	}
}

func TestAnalyzeTracerouteReachabilityAndPathDegradationWithoutOverclaim(t *testing.T) {
	degradedTopology := Topology{Reached: true, Nodes: []TopologyNode{{ID: "hop-0", Hop: 0, Status: "healthy"}, {ID: "hop-1", Hop: 1, Address: "192.0.2.1", LatencyMS: 80, Status: "degraded"}}, Links: []TopologyLink{{From: "hop-0", To: "hop-1", Status: "degraded", LatencyDeltaMS: 79}}}
	analysis := Analyze([]Result{
		{Kind: KindTraceroute, Address: "all.example", Status: StatusUnreachable, ErrorCode: "destination_unreached", Details: map[string]any{"attempts_total": 3, "attempts_reached": 0, "attempts_failed": 3, "attempts_unreached": 3, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0}},
		{Kind: KindTraceroute, Address: "partial.example", Status: StatusDegraded, Details: map[string]any{"attempts_total": 3, "attempts_reached": 2, "attempts_failed": 1, "attempts_unreached": 1, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0}},
		{Kind: KindTraceroute, Address: "degraded.example", Status: StatusDegraded, Details: map[string]any{"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0, "topology": degradedTopology}},
	}, analysisTestNow)
	want := []FindingCode{FindingTracerouteUnreachable, FindingTraceroutePartialReachability, FindingTraceroutePathDegraded}
	if len(analysis.Findings) != len(want) {
		t.Fatalf("analysis = %+v", analysis)
	}
	for index, code := range want {
		if analysis.Findings[index].Code != code || analysis.Findings[index].Category != CategoryRouting {
			t.Fatalf("finding[%d] = %+v", index, analysis.Findings[index])
		}
		text := strings.ToLower(analysis.Findings[index].Title + analysis.Findings[index].Summary)
		if strings.Contains(text, "packet loss") || strings.Contains(text, "probab") || strings.Contains(text, "root cause") {
			t.Fatalf("finding overclaimed unobserved causality: %q", text)
		}
	}
}

func TestAnalyzeTracerouteExecutionFailureDoesNotClaimRouteFailure(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "traceroute_failed", Details: map[string]any{"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1, "attempts_unreached": 0, "attempts_execution_failed": 1, "attempts_timed_out": 0, "attempts_cancelled": 0}}}, analysisTestNow)
	if len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingTracerouteExecutionFailed || analysis.Findings[0].Category != CategoryExecution {
		t.Fatalf("execution analysis = %+v", analysis)
	}
	if strings.Contains(strings.ToLower(analysis.Findings[0].Summary), "destination was not observed") {
		t.Fatalf("execution failure overclaimed route state: %+v", analysis.Findings[0])
	}
}

func TestAnalyzeJSONLegacyDetailsAndMalformedCoverage(t *testing.T) {
	var legacy []Result
	if err := json.Unmarshal([]byte(`[{"kind":"http","address":"https://service.example","status":"unreachable","error_code":"unexpected_status","details":{"status_code":503,"expected_status":200}},{"kind":"https","address":"https://tls.example","status":"healthy","details":{"certificate_expires_at":"2026-09-02T12:00:00Z"}}]`), &legacy); err != nil {
		t.Fatal(err)
	}
	analysis := Analyze(legacy, analysisTestNow)
	if len(analysis.Findings) != 2 || analysis.Findings[0].Code != FindingHTTPUnexpectedStatus || analysis.Findings[1].Code != FindingTLSCertificateExpiring {
		t.Fatalf("legacy analysis = %+v", analysis)
	}
	malformed := Analyze([]Result{{Kind: KindHTTP, Status: StatusUnreachable, ErrorCode: "unexpected_status", Details: map[string]any{"status_code": "503"}}}, analysisTestNow)
	if malformed.Verdict != VerdictAttention || len(malformed.Findings) != 1 || malformed.Findings[0].Confidence != ConfidenceLimited || len(malformed.Coverage.Limitations) == 0 {
		t.Fatalf("malformed analysis = %+v", malformed)
	}
}

func TestAnalyzeIsDeterministicDeduplicatedAndIgnoresMessage(t *testing.T) {
	results := []Result{
		{Kind: KindDNS, Address: "same.example", Status: StatusUnreachable, ErrorCode: "connection_failed", Message: "backend text one"},
		{Kind: KindDNS, Address: "same.example", Status: StatusUnreachable, ErrorCode: "connection_failed", Message: "different backend text"},
	}
	first := Analyze(results, analysisTestNow)
	second := Analyze(results, analysisTestNow)
	if !reflect.DeepEqual(first, second) || len(first.Findings) != 1 || len(first.Findings[0].EvidenceIDs) != 2 || len(first.Actions) != 1 {
		t.Fatalf("analysis not deterministic/deduplicated: %+v / %+v", first, second)
	}
}

func TestAnalyzeHealthyDoesNotFabricateCause(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindDNS, Status: StatusHealthy, Details: map[string]any{"addresses": []string{"1.1.1.1"}, "answer_count": 1}}}, analysisTestNow)
	if analysis.Verdict != VerdictHealthy || len(analysis.Findings) != 0 || len(analysis.Evidence) != 0 || len(analysis.Actions) != 0 {
		t.Fatalf("healthy analysis = %+v", analysis)
	}
}

func TestLegacyReportJSONOmitsAnalysis(t *testing.T) {
	encoded, err := json.Marshal(Report{ID: "legacy", Results: []Result{}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"analysis"`)) {
		t.Fatalf("nil additive analysis was serialized: %s", encoded)
	}
}

func TestAnalysisJSONUsesAdditiveSnakeCaseContract(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindDNS, Address: "missing.example", Status: StatusUnreachable, ErrorCode: "connection_failed"}}, analysisTestNow)
	report := Report{ID: "new", Analysis: &analysis}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range [][]byte{[]byte(`"analysis"`), []byte(`"evidence_ids"`), []byte(`"action_ids"`), []byte(`"expected_result"`), []byte(`"escalation_condition"`), []byte(`"provider_failures"`)} {
		if !bytes.Contains(encoded, field) {
			t.Fatalf("missing %s in %s", field, encoded)
		}
	}
}

func TestAnalyzeJSONTracerouteTopologyDetectsProducerDegradation(t *testing.T) {
	var results []Result
	if err := json.Unmarshal([]byte(`[{"kind":"traceroute","address":"route.example","status":"degraded","details":{"attempts_total":1,"attempts_reached":1,"attempts_failed":0,"attempts_unreached":0,"attempts_execution_failed":0,"attempts_timed_out":0,"attempts_cancelled":0,"topology":{"reached":true,"nodes":[{"id":"hop-1","hop":1,"status":"degraded"}],"links":[]}}}]`), &results); err != nil {
		t.Fatal(err)
	}
	analysis := Analyze(results, analysisTestNow)
	if len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingTraceroutePathDegraded {
		t.Fatalf("JSON topology analysis = %+v", analysis)
	}
}

func TestAnalyzeAggregateTracerouteTopologyCountsOnlyItsObservedPathShape(t *testing.T) {
	degraded := Topology{Reached: true, Nodes: []TopologyNode{{ID: "hop-1", Hop: 1, Address: "192.0.2.1", Status: "degraded"}}}
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{
		"attempts_total": 2, "attempts_reached": 2, "attempts_failed": 0,
		"attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
		"topology": degraded,
	}}}, analysisTestNow)
	if len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingTraceroutePathDegraded || len(analysis.Evidence) != 1 {
		t.Fatalf("aggregate topology analysis = %+v", analysis)
	}
	if got := analysis.Evidence[0].Observed; got != "degraded segment observed among 1 completed attempt" {
		t.Fatalf("aggregate topology evidence count = %q", got)
	}
}

func TestAnalyzeUnsupportedKindIsInconclusive(t *testing.T) {
	analysis := Analyze([]Result{{Kind: Kind("future"), Status: StatusUnreachable, Details: map[string]any{}}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) == 0 {
		t.Fatalf("unsupported analysis = %+v", analysis)
	}
}

func TestAnalyzeMalformedKnownKindStatusIsInconclusive(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindHTTP, Status: Status("mystery"), ErrorCode: "connection_failed", Details: map[string]any{}}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) == 0 {
		t.Fatalf("malformed status analysis = %+v", analysis)
	}
}

func TestAnalyzeContradictoryOrUnknownErrorIsInconclusive(t *testing.T) {
	for _, result := range []Result{
		{Kind: KindHTTP, Status: StatusHealthy, ErrorCode: "timeout"},
		{Kind: KindTCP, Status: StatusUnreachable, ErrorCode: "future_error"},
	} {
		analysis := Analyze([]Result{result}, analysisTestNow)
		if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) == 0 {
			t.Fatalf("result=%+v analysis=%+v", result, analysis)
		}
	}
}

func TestAnalyzeTracerouteExecutionFailuresDoNotBecomeRouteFailures(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{
		"attempts_total": 2, "attempts_reached": 1, "attempts_failed": 1,
		"attempts_unreached": 0, "attempts_execution_failed": 1, "attempts_timed_out": 1, "attempts_cancelled": 0,
	}}}, analysisTestNow)
	if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingExecutionTimeout || analysis.Findings[0].Category != CategoryExecution {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeTracerouteExecutionReachabilityRemainIndependentWithoutEligiblePathEvidence(t *testing.T) {
	degraded := Topology{Reached: true, Nodes: []TopologyNode{{ID: "hop-1", Hop: 1, Address: "192.0.2.1", Status: "degraded"}}}
	analysis := Analyze([]Result{{Kind: KindTraceroute, Address: "independent.example", Status: StatusDegraded, Details: map[string]any{
		"attempts_total": 3, "attempts_reached": 1, "attempts_failed": 2,
		"attempts_unreached": 1, "attempts_execution_failed": 1, "attempts_timed_out": 1, "attempts_cancelled": 0,
		"topology": degraded,
	}}}, analysisTestNow)
	want := []FindingCode{FindingExecutionTimeout, FindingTraceroutePartialReachability}
	if len(analysis.Findings) != len(want) {
		t.Fatalf("findings = %+v, want %v", analysis.Findings, want)
	}
	for index, code := range want {
		if analysis.Findings[index].Code != code {
			t.Fatalf("finding[%d] = %q, want %q; all=%+v", index, analysis.Findings[index].Code, code, analysis.Findings)
		}
	}
}

func TestAnalyzeTracerouteUnreachedAndTimeoutRemainIndependent(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindTraceroute, Address: "unreached.example", Status: StatusUnreachable, ErrorCode: "timeout", Details: map[string]any{
		"attempts_total": 2, "attempts_reached": 0, "attempts_failed": 2,
		"attempts_unreached": 1, "attempts_execution_failed": 1, "attempts_timed_out": 1, "attempts_cancelled": 0,
	}}}, analysisTestNow)
	want := []FindingCode{FindingExecutionTimeout, FindingTracerouteUnreachable}
	if len(analysis.Findings) != len(want) {
		t.Fatalf("findings = %+v, want %v coverage=%+v", analysis.Findings, want, analysis.Coverage)
	}
	for index, code := range want {
		if analysis.Findings[index].Code != code {
			t.Fatalf("finding[%d] = %q, want %q; all=%+v", index, analysis.Findings[index].Code, code, analysis.Findings)
		}
	}
	if got := analysis.Evidence[1].Observed; got != "0/1 completed attempts reached" {
		t.Fatalf("reachability denominator = %q", got)
	}
}

func TestAnalyzeTracerouteEachExecutionClassCombinesWithPartialReachability(t *testing.T) {
	for _, test := range []struct {
		name                string
		timedOut, cancelled int
		want                FindingCode
	}{
		{name: "timeout", timedOut: 1, want: FindingExecutionTimeout},
		{name: "command error", want: FindingTracerouteExecutionFailed},
		{name: "cancellation", cancelled: 1, want: FindingExecutionCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			analysis := Analyze([]Result{{Kind: KindTraceroute, Address: "partial.example", Status: StatusDegraded, Details: map[string]any{
				"attempts_total": 3, "attempts_reached": 1, "attempts_failed": 2,
				"attempts_unreached": 1, "attempts_execution_failed": 1,
				"attempts_timed_out": test.timedOut, "attempts_cancelled": test.cancelled,
			}}}, analysisTestNow)
			want := []FindingCode{test.want, FindingTraceroutePartialReachability}
			if len(analysis.Findings) != len(want) {
				t.Fatalf("findings = %+v, want %v coverage=%+v", analysis.Findings, want, analysis.Coverage)
			}
			for index, code := range want {
				if analysis.Findings[index].Code != code {
					t.Fatalf("finding[%d] = %q, want %q", index, analysis.Findings[index].Code, code)
				}
			}
			if evidence := analysis.Evidence[0]; evidence.Provenance != ProvenanceDetails || !strings.HasPrefix(evidence.Signal, "traceroute.") {
				t.Fatalf("execution evidence was not counter-derived: %+v", evidence)
			}
		})
	}
}

func TestAnalyzeMixedTracerouteExecutionHasOneCountSpecificFinding(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "traceroute_execution_incomplete", Details: map[string]any{
		"attempts_total": 2, "attempts_reached": 0, "attempts_failed": 2,
		"attempts_unreached": 0, "attempts_execution_failed": 2, "attempts_timed_out": 1, "attempts_cancelled": 1,
	}}}, analysisTestNow)
	if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingTracerouteExecutionFailed || analysis.Evidence[0].Observed != "2 execution failures: 1 timed out, 1 cancelled, 0 command errors" {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeTLSHandshakeFailureIsSecurityEvidence(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindIMAPS, Status: StatusUnreachable, ErrorCode: "tls_handshake_failed"}}, analysisTestNow)
	if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingTLSHandshakeFailed || analysis.Findings[0].Category != CategorySecurity {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeTraceroutePathInstabilityFromTypedAndLegacyAttempts(t *testing.T) {
	attempts := []TraceAttempt{
		{Attempt: 1, Status: StatusHealthy, Topology: &Topology{Reached: true, Nodes: []TopologyNode{{ID: "a", Hop: 1, Address: "192.0.2.1"}, {ID: "b", Hop: 2, Address: "203.0.113.9"}}}},
		{Attempt: 2, Status: StatusHealthy, Topology: &Topology{Reached: true, Nodes: []TopologyNode{{ID: "a", Hop: 1, Address: "192.0.2.2"}, {ID: "b", Hop: 2, Address: "203.0.113.9"}}}},
	}
	for _, value := range []any{attempts, mustLegacyJSONValue(t, attempts)} {
		analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{
			"attempts_total": 2, "attempts_reached": 2, "attempts_failed": 0,
			"attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0, "attempts": value,
		}}}, analysisTestNow)
		if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingTraceroutePathUnstable {
			t.Fatalf("analysis = %+v", analysis)
		}
	}
}

func TestAnalyzeRealTracerouteExcludesParsableCommandErrorFromPathEvidence(t *testing.T) {
	outputs := []struct {
		body string
		err  error
	}{
		{body: "traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.1  1.0 ms\n2  203.0.113.8  5.0 ms\n"},
		{body: "traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.2  1.0 ms\n2  203.0.113.8  5.0 ms\n", err: errors.New("exit status 1")},
	}
	call := 0
	checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
		output := outputs[call]
		call++
		return []byte(output.body), output.err
	}, nil, nil)

	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 2})
	analysis := Analyze([]Result{result}, analysisTestNow)
	if call != 2 || result.Details["attempts_reached"] != 1 || result.Details["attempts_execution_failed"] != 1 {
		t.Fatalf("producer result = %+v (calls=%d)", result, call)
	}
	if got := findingCodes(analysis.Findings); !reflect.DeepEqual(got, []FindingCode{FindingTracerouteExecutionFailed}) {
		t.Fatalf("analysis synthesized path evidence from failed command output: findings=%v analysis=%+v", got, analysis)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "multiple") {
		t.Fatalf("single eligible completed attempt claimed multiple paths: %s", encoded)
	}
}

func TestAnalyzeRealTracerouteExcludesDegradedCommandErrorFromPathEvidence(t *testing.T) {
	outputs := []struct {
		body string
		err  error
	}{
		{body: "traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.1  1.0 ms\n2  203.0.113.8  5.0 ms\n"},
		{body: "traceroute to example.test (203.0.113.8), 30 hops max\n1  192.0.2.1  1.0 ms\n2  203.0.113.8  80.0 ms\n", err: errors.New("exit status 1")},
	}
	call := 0
	checker := NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
		output := outputs[call]
		call++
		return []byte(output.body), output.err
	}, nil, nil)

	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 2})
	analysis := Analyze([]Result{result}, analysisTestNow)
	if got := findingCodes(analysis.Findings); !reflect.DeepEqual(got, []FindingCode{FindingTracerouteExecutionFailed}) {
		t.Fatalf("analysis synthesized degraded path evidence from failed command output: findings=%v analysis=%+v", got, analysis)
	}
}

func TestAnalyzeSerializedTracerouteOutputsUseObservationOnlyProse(t *testing.T) {
	degraded := Topology{Reached: true, Nodes: []TopologyNode{{ID: "hop-1", Hop: 1, Address: "192.0.2.1", Status: "degraded"}}}
	first := Topology{Reached: true, Nodes: []TopologyNode{{ID: "hop-1", Hop: 1, Address: "192.0.2.1", Status: "healthy"}}}
	second := Topology{Reached: true, Nodes: []TopologyNode{{ID: "hop-1", Hop: 1, Address: "192.0.2.2", Status: "healthy"}}}
	results := []Result{
		{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "timeout", Details: map[string]any{"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1, "attempts_unreached": 0, "attempts_execution_failed": 1, "attempts_timed_out": 1, "attempts_cancelled": 0}},
		{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "cancelled", Details: map[string]any{"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1, "attempts_unreached": 0, "attempts_execution_failed": 1, "attempts_timed_out": 0, "attempts_cancelled": 1}},
		{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "traceroute_failed", Details: map[string]any{"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1, "attempts_unreached": 0, "attempts_execution_failed": 1, "attempts_timed_out": 0, "attempts_cancelled": 0}},
		{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "traceroute_execution_incomplete", Details: map[string]any{"attempts_total": 2, "attempts_reached": 0, "attempts_failed": 2, "attempts_unreached": 0, "attempts_execution_failed": 2, "attempts_timed_out": 1, "attempts_cancelled": 1}},
		{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "destination_unreached", Details: map[string]any{"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1, "attempts_unreached": 1, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0}},
		{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{"attempts_total": 2, "attempts_reached": 1, "attempts_failed": 1, "attempts_unreached": 1, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0}},
		{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0, "topology": degraded}},
		{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{"attempts_total": 2, "attempts_reached": 2, "attempts_failed": 0, "attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0, "attempts": []TraceAttempt{{Attempt: 1, Status: StatusHealthy, Topology: &first}, {Attempt: 2, Status: StatusHealthy, Topology: &second}}}},
	}
	outputs := make([]Analysis, 0, len(results))
	for _, result := range results {
		outputs = append(outputs, Analyze([]Result{result}, analysisTestNow))
	}
	encoded, err := json.Marshal(outputs)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"packet loss", "packet-loss", "packet_loss", "probab", "likelihood", "likely", "caus", "root cause", "root-cause", "root_cause", "rootcause"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("serialized traceroute output contains prohibited prose %q: %s", forbidden, encoded)
		}
	}
}

func mustLegacyJSONValue(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestAnalyzeRejectsInvalidHTTPStatusRange(t *testing.T) {
	for _, status := range []any{700, float64(9223372036854775808)} {
		analysis := Analyze([]Result{{Kind: KindHTTP, Status: StatusUnreachable, ErrorCode: "unexpected_status", Details: map[string]any{"status_code": status, "expected_status": 200}}}, analysisTestNow)
		if len(analysis.Findings) != 1 || analysis.Findings[0].Confidence != ConfidenceLimited || len(analysis.Coverage.Limitations) == 0 {
			t.Fatalf("status=%v analysis=%+v", status, analysis)
		}
	}
}

func TestIntegerDetailRejectsFloatOutsideIntRange(t *testing.T) {
	if value, ok := integerDetail(map[string]any{"value": float64(9223372036854775808)}, "value"); ok {
		t.Fatalf("integerDetail accepted wrapped value %d", value)
	}
}

func TestAnalyzeSurfacesGeoIPProviderFailuresAsCoverage(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusHealthy, Details: map[string]any{
		"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "geoip_provider_failures": 2,
	}}}, analysisTestNow)
	if len(analysis.Coverage.ProviderFailures) != 1 || analysis.Coverage.ProviderFailures[0].Signal != "geoip" {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeDegradedTraceExplainsMissingTopologyCoverage(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{
		"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "topology": map[string]any{"nodes": "malformed"},
	}}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Coverage.Limitations) == 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeHealthyMalformedDNSAndEndpointFactsAreInconclusive(t *testing.T) {
	for _, result := range []Result{
		{Kind: KindDNS, Status: StatusHealthy, Details: map[string]any{"addresses": []any{}, "answer_count": 1}},
		{Kind: KindTCP, Status: StatusHealthy, Details: map[string]any{"remote_address": 42}},
		{Kind: KindHTTP, Status: StatusHealthy, Details: map[string]any{"status_code": 200}},
	} {
		analysis := Analyze([]Result{result}, analysisTestNow)
		if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) == 0 {
			t.Fatalf("result=%+v analysis=%+v", result, analysis)
		}
	}
}

func TestAnalyzeMissingFailureCodeIsInconclusive(t *testing.T) {
	for _, result := range []Result{{Kind: KindDNS, Status: StatusUnreachable}, {Kind: KindTCP, Status: StatusUnreachable}} {
		analysis := Analyze([]Result{result}, analysisTestNow)
		if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Missing) == 0 {
			t.Fatalf("result=%+v analysis=%+v", result, analysis)
		}
	}
}

func TestAnalyzeTraceAttemptsMustMatchAggregateCounters(t *testing.T) {
	first := Topology{Reached: true, Nodes: []TopologyNode{{Hop: 1, Address: "192.0.2.1"}}}
	second := Topology{Reached: true, Nodes: []TopologyNode{{Hop: 1, Address: "192.0.2.2"}}}
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{
		"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0,
		"attempts": []TraceAttempt{{Attempt: 1, Status: StatusHealthy, Topology: &first}, {Attempt: 2, Status: StatusHealthy, Topology: &second}},
	}}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) == 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeTraceAggregatePathMustAgreeWithCompletedAttempts(t *testing.T) {
	healthy := Topology{Reached: true, Nodes: []TopologyNode{{ID: "healthy", Hop: 1, Address: "192.0.2.1", Status: "healthy"}}}
	degraded := Topology{Reached: true, Nodes: []TopologyNode{{ID: "degraded", Hop: 1, Address: "192.0.2.1", Status: "degraded"}}}
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{
		"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0,
		"attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
		"attempts": []TraceAttempt{{Attempt: 1, Status: StatusHealthy, Topology: &healthy}}, "topology": degraded,
	}}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || !coverageHasSignal(analysis.Coverage.Limitations, "trace_paths") {
		t.Fatalf("contradictory aggregate path was accepted: %+v", analysis)
	}
}

func TestAnalyzeHealthyHTTPSRequiresTLSFacts(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindHTTPS, Status: StatusHealthy, Details: map[string]any{"status_code": 200, "expected_status": 200}}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Missing) == 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeRejectsOutOfRangeOrContradictoryTraceCounters(t *testing.T) {
	for _, result := range []Result{
		{Kind: KindTraceroute, Status: StatusHealthy, Details: map[string]any{"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1}},
		{Kind: KindTraceroute, Status: StatusDegraded, Details: map[string]any{"attempts_total": MaxTraceAttempts + 1, "attempts_reached": 1, "attempts_failed": MaxTraceAttempts}},
	} {
		analysis := Analyze([]Result{result}, analysisTestNow)
		if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) == 0 {
			t.Fatalf("result=%+v analysis=%+v", result, analysis)
		}
	}
}

func TestCoverageSeparatesAbsentFromPresentMalformedSignals(t *testing.T) {
	malformed := Analyze([]Result{
		{Kind: KindHTTP, Status: StatusUnreachable, ErrorCode: "unexpected_status", Details: map[string]any{"status_code": "503", "expected_status": 200}},
		{Kind: KindHTTPS, Status: StatusHealthy, Details: map[string]any{"status_code": 200, "expected_status": 200, "tls_version": "TLS 1.3", "certificate_expires_at": 42}},
	}, analysisTestNow)
	for _, missing := range malformed.Coverage.Missing {
		if missing == "results[0].details.http_status" || missing == "results[1].details.certificate_expires_at" {
			t.Fatalf("present malformed signal reported missing: %+v", malformed.Coverage)
		}
	}
	if len(malformed.Coverage.Limitations) < 2 {
		t.Fatalf("malformed limitations = %+v", malformed.Coverage)
	}
	absent := Analyze([]Result{
		{Kind: KindHTTP, Status: StatusUnreachable, ErrorCode: "unexpected_status", Details: map[string]any{}},
		{Kind: KindHTTPS, Status: StatusHealthy, Details: map[string]any{"status_code": 200, "expected_status": 200, "tls_version": "TLS 1.3"}},
	}, analysisTestNow)
	if !containsString(absent.Coverage.Missing, "results[0].details.http_status") || !containsString(absent.Coverage.Missing, "results[1].details.tls_certificate") {
		t.Fatalf("absent signals not reported missing: %+v", absent.Coverage)
	}
}

func TestAnalyzeRejectsContradictoryTraceAttemptSemantics(t *testing.T) {
	reached := Topology{Reached: true, Nodes: []TopologyNode{{Hop: 1, Address: "192.0.2.1", Status: "healthy"}}}
	attemptSets := [][]TraceAttempt{
		{{Attempt: 1, Status: StatusUnreachable, Topology: &reached}},
		{{Attempt: 1, Status: StatusHealthy, Topology: &reached, ErrorCode: "timeout"}},
		{{Attempt: 1, Status: StatusUnreachable, ErrorCode: "future_error"}},
	}
	for _, attempts := range attemptSets {
		for _, encoded := range []any{attempts, mustLegacyJSONValue(t, attempts)} {
			analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "timeout", Details: map[string]any{
				"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1,
				"attempts_unreached": 0, "attempts_execution_failed": 1, "attempts_timed_out": 1, "attempts_cancelled": 0,
				"attempts": encoded,
			}}}, analysisTestNow)
			if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) == 0 {
				t.Fatalf("attempts=%+v analysis=%+v", attempts, analysis)
			}
		}
	}
	outerMismatch := Analyze([]Result{{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "timeout", Details: map[string]any{
		"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1,
		"attempts_unreached": 1, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
	}}}, analysisTestNow)
	if outerMismatch.Verdict != VerdictInconclusive || len(outerMismatch.Findings) != 0 {
		t.Fatalf("outer mismatch = %+v", outerMismatch)
	}
	allGenericIncomplete := Analyze([]Result{{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "traceroute_execution_incomplete", Details: map[string]any{
		"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1,
		"attempts_unreached": 0, "attempts_execution_failed": 1, "attempts_timed_out": 0, "attempts_cancelled": 0,
	}}}, analysisTestNow)
	if allGenericIncomplete.Verdict != VerdictInconclusive || len(allGenericIncomplete.Findings) != 0 || !coverageHasSignal(allGenericIncomplete.Coverage.Limitations, "trace_error") {
		t.Fatalf("all-generic incomplete = %+v", allGenericIncomplete)
	}
}

func coverageHasSignal(issues []CoverageIssue, signal string) bool {
	for _, issue := range issues {
		if issue.Signal == signal {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestAnalyzeRunnerExecutionLimitationsUseStableFindings(t *testing.T) {
	tests := []struct {
		code string
		want FindingCode
	}{
		{"checker_panic", FindingCheckerPanic},
		{"checker_capacity_unavailable", FindingCheckerCapacityUnavailable},
	}
	for _, tt := range tests {
		analysis := Analyze([]Result{{Kind: KindDNS, Status: StatusUnreachable, ErrorCode: tt.code}}, analysisTestNow)
		if len(analysis.Findings) != 1 || analysis.Findings[0].Code != tt.want {
			t.Fatalf("Analyze(%q)=%+v", tt.code, analysis)
		}
		for _, limitation := range analysis.Coverage.Limitations {
			if strings.Contains(limitation.Reason, "unknown") {
				t.Fatalf("stable code rendered unknown: %+v", limitation)
			}
		}
	}
}

func TestAnalyzeTracerouteUnavailableReportsExecutionTruth(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "traceroute_unavailable", Message: "traceroute is unavailable"}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingTracerouteUnavailable || analysis.Findings[0].Category != CategoryExecution || analysis.Findings[0].Confidence != ConfidenceDirect {
		t.Fatalf("analysis = %+v", analysis)
	}
	if len(analysis.Coverage.Limitations) != 0 {
		t.Fatalf("known startup limitation was reported as unsupported: %+v", analysis.Coverage.Limitations)
	}
}

func TestAnalyzeAggregatesStrictGeoIPEnrichmentCoverage(t *testing.T) {
	results := []Result{
		{Kind: KindTraceroute, Status: StatusHealthy, Details: map[string]any{
			"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0,
			"geoip_provider_failures": 1,
			"geoip_enrichment": EnrichmentCoverage{
				Provider: "geoip", Source: EnrichmentSourceUpstream, UpstreamFetches: 1, MaxAgeMS: 10,
				Failures: []EnrichmentFailure{{Kind: GeoIPErrorTimeout, Count: 1, Retryable: true}},
			},
		}},
		{Kind: KindTraceroute, Status: StatusHealthy, Details: map[string]any{
			"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0,
			"geoip_provider_failures": 2,
			"geoip_enrichment": mustLegacyJSONValue(t, EnrichmentCoverage{
				Provider: "geoip", Source: EnrichmentSourceCache, CacheHits: 2, MaxAgeMS: 100,
				Failures: []EnrichmentFailure{{Kind: GeoIPErrorUnavailable, Count: 2, Retryable: true}},
			}),
		}},
	}
	analysis := Analyze(results, analysisTestNow)
	want := []EnrichmentCoverage{{
		Provider: "geoip", Source: EnrichmentSourceMixed, CacheHits: 2, UpstreamFetches: 1, MaxAgeMS: 100,
		Failures: []EnrichmentFailure{
			{Kind: GeoIPErrorTimeout, Count: 1, Retryable: true},
			{Kind: GeoIPErrorUnavailable, Count: 2, Retryable: true},
		},
	}}
	if !reflect.DeepEqual(analysis.Coverage.Enrichment, want) {
		t.Fatalf("enrichment = %+v, want %+v", analysis.Coverage.Enrichment, want)
	}
	if len(analysis.Coverage.ProviderFailures) != 2 {
		t.Fatalf("legacy provider failures changed: %+v", analysis.Coverage.ProviderFailures)
	}
}

func TestAnalyzeRejectsMalformedOrContradictoryGeoIPEnrichmentAsLimitation(t *testing.T) {
	cases := []any{
		map[string]any{"provider": "geoip", "source": "cache", "cache_hits": 0.0, "upstream_fetches": 1.0, "max_age_ms": 0.0, "failures": []any{}},
		map[string]any{"provider": "geoip", "source": "none", "cache_hits": 0.0, "upstream_fetches": 0.0, "max_age_ms": 0.0, "failures": []any{}, "target": "SECRET-TARGET"},
		map[string]any{"provider": "geoip", "source": "none", "cache_hits": 0.0, "upstream_fetches": 0.0, "max_age_ms": 0.0, "failures": []any{map[string]any{"kind": "timeout", "count": 1.0, "retryable": false}}},
		map[string]any{"provider": "geoip", "source": "none", "cache_hits": 0.0, "upstream_fetches": 0.0, "max_age_ms": 0.0, "failures": []any{map[string]any{"kind": "timeout", "count": 2.0, "retryable": true}}},
	}
	for index, detail := range cases {
		result := Result{Kind: KindTraceroute, Status: StatusHealthy, Details: map[string]any{
			"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0,
			"geoip_provider_failures": 1, "geoip_enrichment": detail,
		}}
		analysis := Analyze([]Result{result}, analysisTestNow)
		if len(analysis.Coverage.Enrichment) != 0 || !coverageHasSignal(analysis.Coverage.Limitations, "geoip_enrichment") {
			t.Fatalf("case %d accepted malformed enrichment: %+v", index, analysis.Coverage)
		}
		encoded, err := json.Marshal(analysis)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(bytes.ToLower(encoded), []byte("secret-target")) {
			t.Fatalf("malformed privacy canary reflected: %s", encoded)
		}
	}
}

func TestAnalyzeGeoIPEnrichmentAggregateIsBoundedAndLegacyJSONUnchanged(t *testing.T) {
	legacy := Coverage{Available: []string{}, Missing: []string{}, ProviderFailures: []CoverageIssue{}, Limitations: []CoverageIssue{}}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"available":[],"missing":[],"provider_failures":[],"limitations":[]}` {
		t.Fatalf("legacy coverage JSON changed: %s", encoded)
	}

	results := make([]Result, MaxTargets)
	for index := range results {
		results[index] = Result{Kind: KindTraceroute, Status: StatusHealthy, Details: map[string]any{
			"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0,
			"geoip_enrichment": EnrichmentCoverage{Provider: "geoip", Source: EnrichmentSourceUpstream, UpstreamFetches: 1, Failures: []EnrichmentFailure{}},
		}}
	}
	analysis := Analyze(results, analysisTestNow)
	if len(analysis.Coverage.Enrichment) != 1 || analysis.Coverage.Enrichment[0].UpstreamFetches != MaxTargets || len(analysis.Coverage.Enrichment[0].Failures) > len(geoIPFailureKinds()) {
		t.Fatalf("unbounded or incorrect aggregate: %+v", analysis.Coverage.Enrichment)
	}
}

func TestAnalyzeTracerouteSmallCounterMatrix(t *testing.T) {
	for total := 1; total <= 3; total++ {
		for reached := 0; reached <= total; reached++ {
			for unreached := 0; unreached <= total-reached; unreached++ {
				executionFailed := total - reached - unreached
				for timedOut := 0; timedOut <= executionFailed; timedOut++ {
					for cancelled := 0; cancelled <= executionFailed-timedOut; cancelled++ {
						status := StatusUnreachable
						if reached == total {
							status = StatusHealthy
						} else if reached > 0 {
							status = StatusDegraded
						}
						errorCode := ""
						if status == StatusUnreachable {
							if executionFailed > 0 {
								errorCode = summarizeTraceExecutionErrors(executionFailed, timedOut, cancelled)
							} else {
								errorCode = "destination_unreached"
							}
						}
						result := Result{Kind: KindTraceroute, Status: status, ErrorCode: errorCode, Details: map[string]any{
							"attempts_total": total, "attempts_reached": reached, "attempts_failed": unreached + executionFailed,
							"attempts_unreached": unreached, "attempts_execution_failed": executionFailed,
							"attempts_timed_out": timedOut, "attempts_cancelled": cancelled,
						}}
						analysis := Analyze([]Result{result}, analysisTestNow)
						codes := findingCodes(analysis.Findings)
						want := []FindingCode{}
						if executionFailed > 0 {
							switch {
							case timedOut == executionFailed:
								want = append(want, FindingExecutionTimeout)
							case cancelled == executionFailed:
								want = append(want, FindingExecutionCancelled)
							default:
								want = append(want, FindingTracerouteExecutionFailed)
							}
						}
						if reached == 0 && unreached > 0 {
							want = append(want, FindingTracerouteUnreachable)
						} else if reached > 0 && unreached > 0 {
							want = append(want, FindingTraceroutePartialReachability)
						}
						if !reflect.DeepEqual(codes, want) {
							t.Fatalf("total=%d reached=%d unreached=%d execution=%d timeout=%d cancelled=%d: codes=%v want=%v coverage=%+v", total, reached, unreached, executionFailed, timedOut, cancelled, codes, want, analysis.Coverage)
						}
						if len(codes) > 2 {
							t.Fatalf("more than one execution/reachability finding per family: %v", codes)
						}
					}
				}
			}
		}
	}
}

func TestAnalyzeTracerouteMalformedCountersNeverSynthesizeFacts(t *testing.T) {
	valid := map[string]any{
		"attempts_total": 2, "attempts_reached": 1, "attempts_failed": 1,
		"attempts_unreached": 1, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0,
	}
	cases := []map[string]any{
		{"attempts_total": 2, "attempts_reached": 1, "attempts_failed": 1},
		cloneTraceDetails(valid, "attempts_reached", -1),
		cloneTraceDetails(valid, "attempts_reached", MaxTraceAttempts+1),
		cloneTraceDetails(valid, "attempts_total", float64(1.5)),
		cloneTraceDetails(valid, "attempts_failed", 0),
		cloneTraceDetails(valid, "attempts_execution_failed", 2),
		cloneTraceDetails(valid, "attempts_timed_out", 1),
		cloneTraceDetails(valid, "attempts_cancelled", int64(^uint64(0)>>1)),
	}
	for index, details := range cases {
		analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusDegraded, Message: "PRIVATE_COUNTER_CANARY", Details: details}}, analysisTestNow)
		if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || !coverageHasSignal(analysis.Coverage.Limitations, "trace_attempts") {
			t.Fatalf("malformed case %d produced facts: %+v", index, analysis)
		}
		encoded, err := json.Marshal(analysis)
		if err != nil || bytes.Contains(encoded, []byte("PRIVATE_COUNTER_CANARY")) {
			t.Fatalf("malformed case %d leaked private input: %s err=%v", index, encoded, err)
		}
	}
}

func TestAnalyzeTraceroutePathFindingCombinesWithExecutionAndIsDeterministic(t *testing.T) {
	firstPath := Topology{Reached: true, Nodes: []TopologyNode{{ID: "first", Hop: 1, Address: "192.0.2.10", Status: "degraded"}}}
	secondPath := Topology{Reached: true, Nodes: []TopologyNode{{ID: "second", Hop: 1, Address: "192.0.2.20", Status: "healthy"}}}
	attempts := []TraceAttempt{
		{Attempt: 1, Status: StatusDegraded, Topology: &firstPath},
		{Attempt: 2, Status: StatusHealthy, Topology: &secondPath},
		{Attempt: 3, Status: StatusUnreachable, ErrorCode: "timeout", Message: "PRIVATE_ATTEMPT_CANARY"},
	}
	result := Result{Kind: KindTraceroute, Status: StatusDegraded, Message: "PRIVATE_RESULT_CANARY", Details: map[string]any{
		"attempts_total": 3, "attempts_reached": 2, "attempts_failed": 1,
		"attempts_unreached": 0, "attempts_execution_failed": 1, "attempts_timed_out": 1, "attempts_cancelled": 0,
		"attempts": attempts, "topology": firstPath,
	}}
	first := Analyze([]Result{result}, analysisTestNow)
	second := Analyze([]Result{result}, analysisTestNow)
	want := []FindingCode{FindingExecutionTimeout, FindingTraceroutePathUnstable}
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(findingCodes(first.Findings), want) {
		t.Fatalf("analysis=%+v second=%+v want=%v", first, second, want)
	}
	if got := first.Evidence[1].Observed; got != "multiple successful completed path signatures among 2 reached completed attempts" {
		t.Fatalf("path evidence = %q", got)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"private_", "packet loss", "probab", "caused by", "root cause"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("analysis used private or causal language %q: %s", forbidden, encoded)
		}
	}
}

func findingCodes(findings []Finding) []FindingCode {
	codes := make([]FindingCode, len(findings))
	for index := range findings {
		codes[index] = findings[index].Code
	}
	return codes
}

func cloneTraceDetails(source map[string]any, key string, value any) map[string]any {
	clone := make(map[string]any, len(source))
	for sourceKey, sourceValue := range source {
		clone[sourceKey] = sourceValue
	}
	clone[key] = value
	return clone
}

func TestAnalyzeExactTLSFailureMatrixUsesFixedPrivateTemplates(t *testing.T) {
	validity := map[string]any{
		"certificate_not_before": analysisTestNow.Add(-24 * time.Hour).Format(time.RFC3339),
		"certificate_not_after":  analysisTestNow.Format(time.RFC3339),
	}
	cases := []struct {
		code    string
		details map[string]any
		finding FindingCode
	}{
		{ResultErrorTLSCertificateExpired, validity, FindingTLSCertificateExpired},
		{ResultErrorTLSCertificateNotYetValid, map[string]any{
			"certificate_not_before": analysisTestNow.Add(time.Second).Format(time.RFC3339),
			"certificate_not_after":  analysisTestNow.Add(24 * time.Hour).Format(time.RFC3339),
		}, FindingTLSCertificateNotYetValid},
		{ResultErrorTLSHostnameMismatch, nil, FindingTLSHostnameMismatch},
		{ResultErrorTLSUntrusted, nil, FindingTLSUntrusted},
		{ResultErrorTLSHandshakeFailed, nil, FindingTLSHandshakeFailed},
	}

	results := make([]Result, 0, len(cases))
	for index, test := range cases {
		results = append(results, Result{
			Kind: KindHTTPS, Address: fmt.Sprintf("https://tls-%d.example.test", index), Status: StatusUnreachable,
			ErrorCode: test.code, Message: "HOSTILE_ERROR_PROSE_CANARY", Details: test.details,
		})
		analysis := Analyze([]Result{results[len(results)-1]}, analysisTestNow)
		if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 || analysis.Findings[0].Code != test.finding || string(analysis.Findings[0].Code) != test.code {
			t.Fatalf("code %q analysis = %+v", test.code, analysis)
		}
		if len(analysis.Evidence) != 1 || analysis.Evidence[0].Signal != "error_code" || analysis.Evidence[0].Observed != test.code || len(analysis.Actions) != 1 {
			t.Fatalf("code %q did not use fixed result evidence/action: %+v", test.code, analysis)
		}
		encoded, err := json.Marshal(analysis)
		if err != nil {
			t.Fatal(err)
		}
		for _, canary := range []string{"HOSTILE_ERROR_PROSE_CANARY", "HOSTILE_CERTIFICATE_PROSE_CANARY"} {
			if bytes.Contains(encoded, []byte(canary)) {
				t.Fatalf("code %q reflected hostile prose: %s", test.code, encoded)
			}
		}
	}

	first := Analyze(results, analysisTestNow)
	second := Analyze(results, analysisTestNow)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("TLS matrix ordering is nondeterministic: %+v / %+v", first, second)
	}
	wantOrder := []FindingCode{
		FindingTLSCertificateExpired,
		FindingTLSCertificateNotYetValid,
		FindingTLSHandshakeFailed,
		FindingTLSHostnameMismatch,
		FindingTLSUntrusted,
	}
	if len(first.Findings) != len(wantOrder) {
		t.Fatalf("TLS findings = %+v", first.Findings)
	}
	for index, want := range wantOrder {
		if first.Findings[index].Code != want {
			t.Fatalf("TLS finding order[%d] = %q, want %q", index, first.Findings[index].Code, want)
		}
	}
}

func TestAnalyzeTLSFailureDetailsAreExactAndCanonical(t *testing.T) {
	validExpired := map[string]any{
		"certificate_not_before": analysisTestNow.Add(-24 * time.Hour).Format(time.RFC3339),
		"certificate_not_after":  analysisTestNow.Format(time.RFC3339),
	}
	invalid := []struct {
		code    string
		details map[string]any
	}{
		{ResultErrorTLSCertificateExpired, nil},
		{ResultErrorTLSCertificateExpired, map[string]any{"certificate_not_before": validExpired["certificate_not_before"]}},
		{ResultErrorTLSCertificateExpired, map[string]any{"certificate_not_before": validExpired["certificate_not_before"], "certificate_not_after": validExpired["certificate_not_after"], "subject": "HOSTILE_CERTIFICATE_PROSE_CANARY"}},
		{ResultErrorTLSCertificateExpired, map[string]any{"certificate_not_before": time.Now(), "certificate_not_after": validExpired["certificate_not_after"]}},
		{ResultErrorTLSCertificateExpired, map[string]any{"certificate_not_before": "2026-09-01T03:00:00-09:00", "certificate_not_after": validExpired["certificate_not_after"]}},
		{ResultErrorTLSCertificateExpired, map[string]any{"certificate_not_before": analysisTestNow.Add(-time.Hour).Format(time.RFC3339), "certificate_not_after": analysisTestNow.Add(time.Hour).Format(time.RFC3339)}},
		{ResultErrorTLSCertificateNotYetValid, validExpired},
		{ResultErrorTLSHostnameMismatch, map[string]any{"subject": "HOSTILE_CERTIFICATE_PROSE_CANARY"}},
		{ResultErrorTLSUntrusted, map[string]any{"issuer": "HOSTILE_CERTIFICATE_PROSE_CANARY"}},
		{ResultErrorTLSHandshakeFailed, map[string]any{"error": "HOSTILE_ERROR_PROSE_CANARY"}},
	}
	for index, test := range invalid {
		analysis := Analyze([]Result{{Kind: KindIMAPS, Status: StatusUnreachable, ErrorCode: test.code, Details: test.details}}, analysisTestNow)
		if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || !coverageHasSignal(analysis.Coverage.Limitations, "tls_failure_details") {
			t.Fatalf("invalid TLS details %d accepted: %+v", index, analysis)
		}
		encoded, err := json.Marshal(analysis)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte("HOSTILE_")) {
			t.Fatalf("invalid TLS detail leaked: %s", encoded)
		}
	}

	for _, code := range []string{ResultErrorTLSHostnameMismatch, ResultErrorTLSUntrusted, ResultErrorTLSHandshakeFailed} {
		for _, details := range []map[string]any{nil, {}} {
			analysis := Analyze([]Result{{Kind: KindSMTPS, Status: StatusUnreachable, ErrorCode: code, Details: details}}, analysisTestNow)
			if len(analysis.Findings) != 1 || string(analysis.Findings[0].Code) != code || coverageHasSignal(analysis.Coverage.Limitations, "tls_failure_details") {
				t.Fatalf("empty details for %q rejected: %+v", code, analysis)
			}
		}
	}
	contradictory := Analyze([]Result{{Kind: KindHTTPS, Status: StatusDegraded, ErrorCode: ResultErrorTLSUntrusted}}, analysisTestNow)
	if contradictory.Verdict != VerdictInconclusive || len(contradictory.Findings) != 0 || !coverageHasSignal(contradictory.Coverage.Limitations, "error_code") {
		t.Fatalf("degraded TLS failure status was accepted: %+v", contradictory)
	}
}
