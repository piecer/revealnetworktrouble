package diagnostic

import (
	"bytes"
	"encoding/json"
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

func TestAnalyzeCancelledIsInconclusive(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindHTTP, Status: StatusUnreachable, ErrorCode: "cancelled"}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingExecutionCancelled {
		t.Fatalf("cancelled analysis = %+v", analysis)
	}
}

func TestAnalyzeTLSObservedDowngradeAndCertificateDates(t *testing.T) {
	analysis := Analyze([]Result{
		{Kind: KindHTTPS, Address: "https://downgrade.example", Status: StatusUnreachable, ErrorCode: "tls_downgrade"},
		{Kind: KindIMAPS, Address: "expired.example", Status: StatusHealthy, Details: map[string]any{"tls_version": "TLS 1.3", "certificate_expires_at": analysisTestNow.Add(-time.Hour)}},
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
		{Kind: KindTraceroute, Address: "all.example", Status: StatusUnreachable, ErrorCode: "destination_unreached", Details: map[string]any{"attempts_total": 3, "attempts_reached": 0, "attempts_failed": 3}},
		{Kind: KindTraceroute, Address: "partial.example", Status: StatusDegraded, Details: map[string]any{"attempts_total": 3, "attempts_reached": 2, "attempts_failed": 1}},
		{Kind: KindTraceroute, Address: "degraded.example", Status: StatusDegraded, Details: map[string]any{"attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "topology": degradedTopology}},
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
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "traceroute_failed", Details: map[string]any{"attempts_total": 1, "attempts_reached": 0, "attempts_failed": 1}}}, analysisTestNow)
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
	if err := json.Unmarshal([]byte(`[{"kind":"traceroute","address":"route.example","status":"degraded","details":{"attempts_total":1,"attempts_reached":1,"attempts_failed":0,"topology":{"reached":true,"nodes":[{"id":"hop-1","hop":1,"status":"degraded"}],"links":[]}}}]`), &results); err != nil {
		t.Fatal(err)
	}
	analysis := Analyze(results, analysisTestNow)
	if len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingTraceroutePathDegraded {
		t.Fatalf("JSON topology analysis = %+v", analysis)
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

func TestAnalyzeMixedTracerouteExecutionIsInconclusive(t *testing.T) {
	analysis := Analyze([]Result{{Kind: KindTraceroute, Status: StatusUnreachable, ErrorCode: "traceroute_execution_incomplete", Details: map[string]any{
		"attempts_total": 2, "attempts_reached": 0, "attempts_failed": 2,
		"attempts_unreached": 0, "attempts_execution_failed": 2, "attempts_timed_out": 1, "attempts_cancelled": 1,
	}}}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Findings) != 0 || len(analysis.Coverage.Limitations) == 0 {
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
			"attempts_total": 2, "attempts_reached": 2, "attempts_failed": 0, "attempts": value,
		}}}, analysisTestNow)
		if analysis.Verdict != VerdictAttention || len(analysis.Findings) != 1 || analysis.Findings[0].Code != FindingTraceroutePathUnstable {
			t.Fatalf("analysis = %+v", analysis)
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
