package diagnostic

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"time"
)

type normalizedResultFacts struct {
	index              int
	kind               Kind
	address            string
	status             Status
	statusValid        bool
	detailsValid       bool
	errorCode          string
	httpStatus         int
	httpExpected       int
	hasHTTPStatus      bool
	certificateExpires time.Time
	hasCertificate     bool
	traceTotal         int
	traceReached       int
	traceFailed        int
	traceUnreached     int
	traceExecutionFail int
	traceTimedOut      int
	traceCancelled     int
	hasTraceCounters   bool
	tracePathDegraded  bool
	tracePathUnstable  bool
}

type analysisBuilder struct {
	analysis       Analysis
	findingByKey   map[string]int
	actionByKey    map[string]int
	hasUnexplained bool
}

func Analyze(results []Result, now time.Time) Analysis {
	builder := analysisBuilder{
		analysis: Analysis{
			Verdict:  VerdictHealthy,
			Findings: []Finding{}, Evidence: []Evidence{}, Actions: []Action{},
			Coverage: Coverage{Available: []string{}, Missing: []string{}, ProviderFailures: []CoverageIssue{}, Limitations: []CoverageIssue{}},
		},
		findingByKey: make(map[string]int),
		actionByKey:  make(map[string]int),
	}
	facts := normalizeResults(results, &builder.analysis.Coverage)
	for _, fact := range facts {
		builder.analyzeFact(fact, now.UTC())
	}
	if len(results) == 0 || (len(builder.analysis.Findings) == 0 && (builder.hasUnexplained || len(builder.analysis.Coverage.Limitations) > 0)) {
		builder.analysis.Verdict = VerdictInconclusive
	} else if len(builder.analysis.Findings) > 0 && !onlyInconclusiveFindings(builder.analysis.Findings) {
		builder.analysis.Verdict = VerdictAttention
	} else if len(builder.analysis.Findings) > 0 {
		builder.analysis.Verdict = VerdictInconclusive
	}
	builder.finalize()
	return builder.analysis
}

func onlyInconclusiveFindings(findings []Finding) bool {
	if len(findings) == 0 {
		return false
	}
	for _, finding := range findings {
		if finding.Code != FindingExecutionCancelled {
			return false
		}
	}
	return true
}

func (b *analysisBuilder) analyzeFact(fact normalizedResultFacts, now time.Time) {
	if !fact.statusValid {
		b.hasUnexplained = true
		return
	}
	if !fact.detailsValid {
		b.hasUnexplained = true
		if fact.kind == KindTraceroute && traceDetailsRequired(fact.errorCode) {
			return
		}
	}
	if !validErrorCode(fact) {
		b.hasUnexplained = true
		b.analysis.Coverage.Limitations = append(b.analysis.Coverage.Limitations, CoverageIssue{Code: CoverageUnsupportedDetails, ResultIndex: fact.index, Kind: fact.kind, Signal: "error_code", Reason: "error code is unknown or contradicts the result status"})
		return
	}
	if fact.errorCode == "tls_downgrade" {
		b.add(fact, FindingTLSDowngrade, SeverityCritical, CategorySecurity,
			"TLS was not preserved", "The HTTPS observation reported a TLS downgrade.", ConfidenceDirect,
			"error_code", fact.errorCode, "verified TLS for every HTTPS response", ProvenanceResult,
			"Verify TLS end to end", "Inspect each redirect and the final endpoint and verify that HTTPS is preserved throughout.", "Every redirect and the final response use verified TLS.", "Escalate to the endpoint owner if any redirect or final response still leaves TLS.")
		return
	}
	if fact.errorCode == "tls_handshake_failed" {
		b.add(fact, FindingTLSHandshakeFailed, SeverityCritical, CategorySecurity,
			"TLS handshake did not complete", "The transport connected, but the TLS handshake failed.", ConfidenceDirect,
			"error_code", fact.errorCode, "verified TLS handshake", ProvenanceResult,
			"Verify the TLS endpoint", "Check the certificate chain, server name, protocol versions, and cipher compatibility, then repeat the check.", "The TLS handshake completes with the intended endpoint.", "Escalate to the TLS endpoint owner if the handshake still fails with a valid trust chain and server name.")
		return
	}
	if fact.hasCertificate && !fact.certificateExpires.After(now) {
		b.addCertificate(fact, FindingTLSCertificateExpired, SeverityCritical, "TLS certificate is expired", "The observed peer certificate expiry is at or before the analysis time.")
	} else if fact.hasCertificate && !fact.certificateExpires.After(now.Add(30*24*time.Hour)) {
		b.addCertificate(fact, FindingTLSCertificateExpiring, SeverityWarning, "TLS certificate is near expiry", "The observed peer certificate expires within 30 days of the analysis time.")
	}
	if fact.status == StatusHealthy {
		return
	}

	switch fact.errorCode {
	case "network_policy_blocked":
		b.add(fact, FindingTargetPolicyBlocked, SeverityWarning, CategoryInput,
			"Target is blocked by deployment policy", "The public deployment policy rejected the target before a network connection was attempted.", ConfidenceDirect,
			"error_code", fact.errorCode, "target permitted by the selected deployment policy", ProvenanceResult,
			"Verify the deployment policy", "Use an approved public target, or run an explicitly trusted-local deployment for authorized private-network diagnostics.", "The target is either approved or intentionally diagnosed from a trusted-local deployment.", "Escalate to the deployment owner instead of weakening the public-mode address policy.")
		return
	case "invalid_url", "invalid_address":
		b.add(fact, FindingInvalidTarget, SeverityCritical, CategoryInput,
			"Target is invalid", "The checker rejected the target before a network observation was possible.", ConfidenceDirect,
			"error_code", fact.errorCode, "valid target syntax", ProvenanceResult,
			"Correct the target", "Verify the target syntax, scheme, hostname, and port, then run the check again.", "The corrected target is accepted and produces an observation.", "Escalate to the client or configuration owner if a valid target is still rejected.")
		return
	case "timeout":
		b.add(fact, FindingExecutionTimeout, SeverityWarning, CategoryExecution,
			"Check timed out", "The check did not complete within its execution deadline.", ConfidenceDirect,
			"error_code", fact.errorCode, "completion before the configured deadline", ProvenanceResult,
			"Verify the timeout", "Retry once with the same bounded timeout and verify endpoint responsiveness from the same vantage point.", "The check completes before the configured deadline.", "Escalate if repeated bounded checks time out after endpoint availability is independently verified.")
		return
	case "cancelled":
		b.add(fact, FindingExecutionCancelled, SeverityInfo, CategoryExecution,
			"Check was cancelled", "The check ended after cancellation, so service health was not established.", ConfidenceDirect,
			"error_code", fact.errorCode, "completed observation", ProvenanceResult,
			"Repeat the cancelled check", "Run the check again when the request can remain active through completion.", "The repeated check completes with an observed result.", "Escalate if checks are repeatedly cancelled without an intentional caller cancellation.")
		return
	}

	if fact.kind == KindTraceroute {
		if fact.errorCode == "traceroute_failed" {
			b.add(fact, FindingTracerouteExecutionFailed, SeverityWarning, CategoryExecution,
				"Traceroute execution did not complete", "The traceroute command failed, so destination reachability was not established.", ConfidenceDirect,
				"error_code", fact.errorCode, "completed traceroute execution", ProvenanceResult,
				"Verify traceroute execution", "Verify the traceroute executable and permissions, then repeat the bounded check.", "The command completes and produces a parseable route observation.", "Escalate to the runtime owner if the traceroute command still cannot complete.")
			return
		}
		b.analyzeTrace(fact)
		return
	}
	if (fact.kind == KindHTTP || fact.kind == KindHTTPS) && fact.errorCode == "unexpected_status" {
		if fact.hasHTTPStatus {
			b.add(fact, FindingHTTPUnexpectedStatus, SeverityWarning, CategoryApplication,
				"Application returned an unexpected status", "The observed HTTP status differs from the configured expectation.", ConfidenceDirect,
				"http.status_code", strconv.Itoa(fact.httpStatus), strconv.Itoa(fact.httpExpected), ProvenanceDetails,
				"Verify the application response", "Request the same URL and verify the health endpoint, routing, and configured expected status.", "The endpoint returns the configured expected HTTP status.", "Escalate to the application owner if the response remains unexpected after route and expectation configuration are verified.")
		} else {
			b.add(fact, FindingHTTPUnexpectedStatus, SeverityWarning, CategoryApplication,
				"Application returned an unexpected status", "The checker reported an HTTP status mismatch, but the status details were unavailable.", ConfidenceLimited,
				"error_code", fact.errorCode, "matching HTTP status details", ProvenanceResult,
				"Verify the application response", "Repeat the request and capture the observed and expected HTTP status.", "The repeated result contains both status values.", "Escalate if the mismatch persists after the expected status is verified.")
		}
		return
	}
	if fact.kind == KindDNS {
		b.add(fact, FindingDNSResolutionFailed, SeverityCritical, CategoryNameResolution,
			"Name resolution did not complete", "The DNS check did not resolve the requested name.", ConfidenceDirect,
			"error_code", fact.errorCode, "successful DNS resolution", ProvenanceResult,
			"Verify DNS resolution", "Resolve the same hostname with the configured resolver and one known-good resolver.", "The hostname returns at least one intended address consistently.", "Escalate to the DNS owner if the configured resolver still fails while the known-good resolver succeeds.")
		return
	}
	if isEndpointKind(fact.kind) {
		b.add(fact, FindingEndpointConnectFailed, SeverityCritical, CategoryConnectivity,
			"Endpoint connection did not complete", "The endpoint or service connection check failed.", ConfidenceDirect,
			"error_code", fact.errorCode, "successful endpoint connection", ProvenanceResult,
			"Verify endpoint reachability", "Retry the endpoint from the same vantage point and verify that the intended service is listening on the configured port.", "A connection is accepted by the intended endpoint.", "Escalate to the network or service owner if the endpoint remains unreachable after the listener and access policy are verified.")
		return
	}
	b.hasUnexplained = true
	b.analysis.Coverage.Limitations = append(b.analysis.Coverage.Limitations, CoverageIssue{Code: CoverageUnsupportedDetails, ResultIndex: fact.index, Kind: fact.kind, Signal: "result", Reason: "no analysis rule supports this non-healthy result"})
}

func (b *analysisBuilder) analyzeTrace(fact normalizedResultFacts) {
	if !fact.hasTraceCounters {
		b.hasUnexplained = true
		return
	}
	if fact.traceExecutionFail > 0 {
		switch {
		case fact.traceTimedOut == fact.traceExecutionFail:
			b.add(fact, FindingExecutionTimeout, SeverityWarning, CategoryExecution,
				"Traceroute attempts timed out", "One or more traceroute attempts did not complete within their execution deadline.", ConfidenceDirect,
				"traceroute.attempts_timed_out", strconv.Itoa(fact.traceTimedOut), "0", ProvenanceDetails,
				"Repeat the timed-out trace", "Repeat the bounded trace from the same vantage point.", "Every attempt completes before its deadline.", "Escalate if bounded attempts repeatedly time out after runtime availability is verified.")
		case fact.traceCancelled == fact.traceExecutionFail:
			b.add(fact, FindingExecutionCancelled, SeverityInfo, CategoryExecution,
				"Traceroute attempts were cancelled", "Traceroute execution was cancelled, so route reachability was not established.", ConfidenceDirect,
				"traceroute.attempts_cancelled", strconv.Itoa(fact.traceCancelled), "0", ProvenanceDetails,
				"Repeat the cancelled trace", "Run the trace again when the request can remain active.", "The repeated trace completes.", "Escalate if traces are cancelled without caller cancellation.")
		default:
			b.hasUnexplained = true
			b.analysis.Coverage.Limitations = append(b.analysis.Coverage.Limitations, CoverageIssue{Code: CoverageUnsupportedDetails, ResultIndex: fact.index, Kind: fact.kind, Signal: "trace_execution", Reason: "traceroute attempts ended with mixed execution outcomes"})
		}
		return
	}
	observed := fmt.Sprintf("%d/%d attempts reached", fact.traceReached, fact.traceTotal)
	switch {
	case fact.traceTotal > 0 && fact.traceReached == 0:
		b.add(fact, FindingTracerouteUnreachable, SeverityCritical, CategoryRouting,
			"No traceroute attempt reached the destination", "The destination was not observed as reached in any recorded traceroute attempt.", ConfidenceDirect,
			"traceroute.attempts_reached", observed, fmt.Sprintf("%d/%d attempts reached", fact.traceTotal, fact.traceTotal), ProvenanceDetails,
			"Verify the route from the same vantage point", "Repeat the bounded trace and compare the last responsive hop and destination reachability.", "At least one trace reaches the destination or identifies a stable last responsive hop.", "Escalate with the recorded hops if repeated traces still do not reach the destination.")
	case fact.traceReached < fact.traceTotal:
		b.add(fact, FindingTraceroutePartialReachability, SeverityWarning, CategoryRouting,
			"Traceroute reachability varied across attempts", "Some recorded traceroute attempts reached the destination and others did not.", ConfidenceDirect,
			"traceroute.attempts_reached", observed, fmt.Sprintf("%d/%d attempts reached", fact.traceTotal, fact.traceTotal), ProvenanceDetails,
			"Compare repeated routes", "Compare reached and unreached attempts for the first stable divergence while preserving the same vantage point.", "Repeated attempts show consistent reachability or a reproducible divergence.", "Escalate with both route sets if the variation persists.")
	case fact.tracePathUnstable:
		b.add(fact, FindingTraceroutePathUnstable, SeverityWarning, CategoryRouting,
			"Traceroute paths varied across attempts", "Successful attempts observed more than one hop sequence from the same vantage point.", ConfidenceDirect,
			"traceroute.path_signatures", "multiple successful path signatures", "one stable successful path signature", ProvenanceDetails,
			"Compare path variants", "Repeat the bounded trace and compare the first hop where successful paths diverge.", "The path stabilizes or the same divergence is reproduced.", "Escalate with the successful path variants if the divergence persists.")
	case fact.tracePathDegraded:
		b.add(fact, FindingTraceroutePathDegraded, SeverityWarning, CategoryRouting,
			"A degraded route segment was observed", "The traceroute producer classified at least one recorded hop or link as degraded.", ConfidenceLimited,
			"traceroute.path_status", "degraded segment observed", "no producer-classified degraded segment", ProvenanceDetails,
			"Verify the degraded segment", "Repeat the trace and compare the same hop transition across attempts and another approved vantage point.", "The segment is either consistently degraded or returns to the normal baseline.", "Escalate with repeated hop evidence; do not infer packet loss or a root cause from traceroute alone.")
	default:
		b.hasUnexplained = true
	}
}

func (b *analysisBuilder) addCertificate(fact normalizedResultFacts, code FindingCode, severity FindingSeverity, title, summary string) {
	b.add(fact, code, severity, CategorySecurity, title, summary, ConfidenceDirect,
		"tls.certificate_expires_at", fact.certificateExpires.UTC().Format(time.RFC3339), "certificate valid beyond the 30-day renewal window", ProvenanceDetails,
		"Verify certificate renewal", "Confirm the deployed certificate chain and renewal schedule on the observed endpoint.", "The endpoint presents a currently valid certificate with adequate renewal margin.", "Escalate to the certificate owner if renewal or deployment cannot be confirmed before expiry.")
}

func (b *analysisBuilder) add(fact normalizedResultFacts, code FindingCode, severity FindingSeverity, category FindingCategory, title, summary string, confidence Confidence, signal, observed, expected string, provenance EvidenceProvenance, actionTitle, step, expectedResult, escalation string) {
	evidence := Evidence{ID: fmt.Sprintf("e-%03d", len(b.analysis.Evidence)+1), ResultIndex: fact.index, Kind: fact.kind, Address: fact.address, Signal: signal, Observed: observed, Expected: expected, Provenance: provenance}
	b.analysis.Evidence = append(b.analysis.Evidence, evidence)
	key := string(code) + "\x00" + string(fact.kind) + "\x00" + fact.address
	actionKey := string(code) + "\x00" + fact.address
	actionIndex, actionExists := b.actionByKey[actionKey]
	if !actionExists {
		actionIndex = len(b.analysis.Actions)
		b.actionByKey[actionKey] = actionIndex
		b.analysis.Actions = append(b.analysis.Actions, Action{ID: fmt.Sprintf("a-%03d", actionIndex+1), Title: actionTitle, Step: step, ExpectedResult: expectedResult, EscalationCondition: escalation})
	}
	if findingIndex, exists := b.findingByKey[key]; exists {
		b.analysis.Findings[findingIndex].EvidenceIDs = append(b.analysis.Findings[findingIndex].EvidenceIDs, evidence.ID)
		return
	}
	findingIndex := len(b.analysis.Findings)
	b.findingByKey[key] = findingIndex
	b.analysis.Findings = append(b.analysis.Findings, Finding{ID: fmt.Sprintf("f-%03d", findingIndex+1), Code: code, Severity: severity, Category: category, Title: title, Summary: summary, Confidence: confidence, EvidenceIDs: []string{evidence.ID}, ActionIDs: []string{b.analysis.Actions[actionIndex].ID}})
}

func (b *analysisBuilder) finalize() {
	b.analysis.Coverage.Available = sortedUnique(b.analysis.Coverage.Available)
	b.analysis.Coverage.Missing = sortedUnique(b.analysis.Coverage.Missing)
	sortCoverageIssues(b.analysis.Coverage.Limitations)
	sortCoverageIssues(b.analysis.Coverage.ProviderFailures)
	sort.SliceStable(b.analysis.Findings, func(i, j int) bool {
		a, c := b.analysis.Findings[i], b.analysis.Findings[j]
		if severityRank(a.Severity) != severityRank(c.Severity) {
			return severityRank(a.Severity) < severityRank(c.Severity)
		}
		if a.Code != c.Code {
			return a.Code < c.Code
		}
		return a.ID < c.ID
	})
}

func sortCoverageIssues(issues []CoverageIssue) {
	sort.SliceStable(issues, func(i, j int) bool {
		a, c := issues[i], issues[j]
		if a.ResultIndex != c.ResultIndex {
			return a.ResultIndex < c.ResultIndex
		}
		if a.Code != c.Code {
			return a.Code < c.Code
		}
		return a.Signal < c.Signal
	})
}

func severityRank(severity FindingSeverity) int {
	switch severity {
	case SeverityCritical:
		return 0
	case SeverityWarning:
		return 1
	default:
		return 2
	}
}

func sortedUnique(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func normalizeResults(results []Result, coverage *Coverage) []normalizedResultFacts {
	facts := make([]normalizedResultFacts, 0, len(results))
	for index, result := range results {
		fact := normalizedResultFacts{index: index, kind: result.Kind, address: result.Address, status: result.Status, statusValid: validStatus(result.Status), detailsValid: true, errorCode: result.ErrorCode}
		coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].status", index))
		if !fact.statusValid {
			coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "status", Reason: "result status is not a supported value"})
		}
		if result.ErrorCode != "" {
			coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].error_code", index))
		} else if result.Status != StatusHealthy {
			coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].error_code", index))
		}
		if !supportedKind(result.Kind) {
			coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageUnsupportedDetails, ResultIndex: index, Kind: result.Kind, Signal: "kind", Reason: "result kind is unsupported by this analysis version"})
		}
		if result.Details == nil {
			coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details", index))
			coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMissingDetails, ResultIndex: index, Kind: result.Kind, Signal: "details", Reason: "checker details were not observed"})
		}
		if result.Status == StatusHealthy && result.Kind == KindDNS {
			addresses, addressesOK := stringSliceDetail(result.Details, "addresses")
			count, countOK := integerDetail(result.Details, "answer_count")
			_, addressesExist := result.Details["addresses"]
			_, countExists := result.Details["answer_count"]
			if !addressesOK || !countOK || count < 1 || len(addresses) != count {
				fact.detailsValid = false
				if !addressesExist || !countExists {
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.dns_answers", index))
				}
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "dns_answers", Reason: "DNS answers were missing, empty, malformed, or inconsistent"})
			} else {
				coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.dns_answers", index))
			}
		}
		if result.Status == StatusHealthy && isConnectionDetailKind(result.Kind) {
			remote, remoteOK := stringDetail(result.Details, "remote_address")
			_, remoteExists := result.Details["remote_address"]
			if !remoteOK || strings.TrimSpace(remote) == "" {
				fact.detailsValid = false
				if !remoteExists {
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.endpoint", index))
				}
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "endpoint", Reason: "connected endpoint details were missing or malformed"})
			} else {
				coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.endpoint", index))
			}
		}
		if result.Kind == KindHTTP || result.Kind == KindHTTPS {
			status, statusOK := integerDetail(result.Details, "status_code")
			expected, expectedOK := integerDetail(result.Details, "expected_status")
			_, statusExists := result.Details["status_code"]
			_, expectedExists := result.Details["expected_status"]
			if statusOK && expectedOK && status >= 100 && status <= 599 && expected >= 100 && expected <= 599 {
				fact.httpStatus, fact.httpExpected, fact.hasHTTPStatus = status, expected, true
				coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.http_status", index))
			} else if result.ErrorCode == "unexpected_status" {
				if !statusExists || !expectedExists {
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.http_status", index))
				}
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "http_status", Reason: "HTTP status details were missing or malformed"})
			}
			if result.Status == StatusHealthy && !fact.hasHTTPStatus {
				fact.detailsValid = false
				if !statusExists || !expectedExists {
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.http_status", index))
				}
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "http_status", Reason: "healthy HTTP result lacked valid observed and expected status details"})
			}
		}
		if isTLSKind(result.Kind) && result.Details != nil {
			if result.Status == StatusHealthy {
				if version, ok := stringDetail(result.Details, "tls_version"); !ok || strings.TrimSpace(version) == "" {
					fact.detailsValid = false
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.tls", index))
					coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMissingDetails, ResultIndex: index, Kind: result.Kind, Signal: "tls", Reason: "healthy TLS result lacked handshake facts"})
				}
				if _, exists := result.Details["certificate_expires_at"]; !exists {
					fact.detailsValid = false
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.tls_certificate", index))
					coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMissingDetails, ResultIndex: index, Kind: result.Kind, Signal: "tls_certificate", Reason: "healthy TLS result lacked certificate expiry facts"})
				}
			}
			if expires, ok := timeDetail(result.Details, "certificate_expires_at"); ok {
				fact.certificateExpires, fact.hasCertificate = expires, true
				coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.tls_certificate", index))
			} else if _, exists := result.Details["certificate_expires_at"]; exists {
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "certificate_expires_at", Reason: "certificate expiry detail was malformed"})
			}
		}
		if result.Kind == KindTraceroute {
			if failures, ok := integerDetail(result.Details, "geoip_provider_failures"); ok && failures > 0 {
				coverage.ProviderFailures = append(coverage.ProviderFailures, CoverageIssue{Code: CoverageMissingDetails, ResultIndex: index, Kind: result.Kind, Signal: "geoip", Reason: fmt.Sprintf("%d GeoIP enrichment lookups failed", failures)})
			}
			total, totalOK := integerDetail(result.Details, "attempts_total")
			reached, reachedOK := integerDetail(result.Details, "attempts_reached")
			failed, failedOK := integerDetail(result.Details, "attempts_failed")
			unreached, unreachedOK := integerDetail(result.Details, "attempts_unreached")
			executionFailed, executionOK := integerDetail(result.Details, "attempts_execution_failed")
			timedOut, timedOutOK := integerDetail(result.Details, "attempts_timed_out")
			cancelled, cancelledOK := integerDetail(result.Details, "attempts_cancelled")
			_, totalExists := result.Details["attempts_total"]
			_, reachedExists := result.Details["attempts_reached"]
			_, failedExists := result.Details["attempts_failed"]
			_, unreachedExists := result.Details["attempts_unreached"]
			_, executionExists := result.Details["attempts_execution_failed"]
			_, timedOutExists := result.Details["attempts_timed_out"]
			_, cancelledExists := result.Details["attempts_cancelled"]
			legacyCounters := !unreachedExists && !executionExists && !timedOutExists && !cancelledExists
			if legacyCounters {
				switch result.ErrorCode {
				case "timeout":
					unreached, executionFailed, timedOut, cancelled = 0, failed, failed, 0
				case "cancelled":
					unreached, executionFailed, timedOut, cancelled = 0, failed, 0, failed
				case "traceroute_failed", "traceroute_execution_incomplete":
					unreached, executionFailed, timedOut, cancelled = 0, failed, 0, 0
				default:
					unreached, executionFailed, timedOut, cancelled = failed, 0, 0, 0
				}
				unreachedOK, executionOK, timedOutOK, cancelledOK = true, true, true, true
			}
			if totalOK && reachedOK && failedOK && unreachedOK && executionOK && timedOutOK && cancelledOK && total > 0 && total <= MaxTraceAttempts && reached >= 0 && failed >= 0 && unreached >= 0 && executionFailed >= 0 && timedOut >= 0 && cancelled >= 0 && reached+failed == total && reached+unreached+executionFailed == total && timedOut+cancelled <= executionFailed && traceCountersMatchStatus(result.Status, total, reached) {
				fact.traceTotal, fact.traceReached, fact.traceFailed, fact.hasTraceCounters = total, reached, failed, true
				fact.traceUnreached, fact.traceExecutionFail, fact.traceTimedOut, fact.traceCancelled = unreached, executionFailed, timedOut, cancelled
				topologyValue, topologyExists := result.Details["topology"]
				topology, topologyOK := normalizedTopology(topologyValue)
				if topologyOK {
					fact.tracePathDegraded = topologyDegraded(topology)
				}
				attemptsObserved := false
				if attemptsValue, exists := result.Details["attempts"]; exists {
					attemptsObserved = true
					attempts, ok := normalizedTraceAttempts(attemptsValue)
					if ok && traceAttemptsMatchCounters(attempts, total, reached, unreached, executionFailed, timedOut, cancelled) {
						fact.tracePathUnstable = tracePathsUnstable(attempts)
						coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.trace_paths", index))
					} else {
						fact.hasTraceCounters = false
						fact.detailsValid = false
						reason := "traceroute attempts were malformed"
						if ok {
							reason = "traceroute attempts contradicted aggregate counters"
						}
						coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "trace_paths", Reason: reason})
					}
				}
				if !traceOuterErrorMatches(result.ErrorCode, result.Status, total, reached, unreached, executionFailed, timedOut, cancelled) {
					fact.hasTraceCounters = false
					fact.detailsValid = false
					coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "trace_error", Reason: "traceroute top-level error contradicted aggregate counters"})
				}
				if fact.traceExecutionFail == 0 && !topologyOK && !attemptsObserved {
					issue := CoverageIssue{ResultIndex: index, Kind: result.Kind, Signal: "trace_topology"}
					if topologyExists {
						issue.Code = CoverageMalformedDetails
						issue.Reason = "traceroute route topology was malformed"
					} else {
						issue.Code = CoverageMissingDetails
						issue.Reason = "traceroute route topology was not observed"
						coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.trace_topology", index))
					}
					coverage.Limitations = append(coverage.Limitations, issue)
				}
				if result.Status == StatusDegraded && fact.traceExecutionFail == 0 && !fact.tracePathUnstable && !fact.tracePathDegraded {
					reason := "degraded traceroute lacked a valid degraded topology or path variation"
					if attemptsObserved {
						reason = "degraded traceroute attempts did not contain an observable degradation signal"
					}
					coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "trace_topology", Reason: reason})
				}
				if fact.hasTraceCounters {
					coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.trace_attempts", index))
				}
			} else {
				fact.detailsValid = false
				if !totalExists || !reachedExists || !failedExists || (!legacyCounters && (!unreachedExists || !executionExists || !timedOutExists || !cancelledExists)) {
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.trace_attempts", index))
				}
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "trace_attempts", Reason: "traceroute counters were missing, malformed, or inconsistent"})
			}
		}
		facts = append(facts, fact)
	}
	return facts
}

func supportedKind(kind Kind) bool {
	switch kind {
	case KindDNS, KindTCP, KindHTTP, KindHTTPS, KindTraceroute, KindSSH, KindSMTP, KindSubmission, KindSMTPS, KindIMAP, KindIMAPS, KindPOP3, KindPOP3S:
		return true
	default:
		return false
	}
}

func validStatus(status Status) bool {
	return status == StatusHealthy || status == StatusDegraded || status == StatusUnreachable
}

func traceDetailsRequired(errorCode string) bool {
	switch errorCode {
	case "", "timeout", "cancelled", "destination_unreached", "traceroute_failed", "traceroute_execution_incomplete":
		return true
	default:
		return false
	}
}

func traceOuterErrorMatches(errorCode string, status Status, total, reached, unreached, executionFailed, timedOut, cancelled int) bool {
	switch errorCode {
	case "":
		return status == StatusHealthy || status == StatusDegraded
	case "timeout":
		return status == StatusUnreachable && executionFailed == total && timedOut == total
	case "cancelled":
		return status == StatusUnreachable && executionFailed == total && cancelled == total
	case "traceroute_failed":
		return status == StatusUnreachable && executionFailed == total && timedOut == 0 && cancelled == 0
	case "traceroute_execution_incomplete":
		genericFailed := executionFailed - timedOut - cancelled
		categories := 0
		if genericFailed > 0 {
			categories++
		}
		if timedOut > 0 {
			categories++
		}
		if cancelled > 0 {
			categories++
		}
		return status == StatusUnreachable && executionFailed == total && categories >= 2
	case "destination_unreached":
		return status == StatusUnreachable && reached == 0 && unreached == total && executionFailed == 0
	default:
		return true
	}
}

func validErrorCode(fact normalizedResultFacts) bool {
	if fact.status == StatusHealthy {
		return fact.errorCode == ""
	}
	if fact.errorCode == "" {
		return fact.kind == KindTraceroute && fact.status == StatusDegraded && fact.hasTraceCounters
	}
	switch fact.errorCode {
	case "network_policy_blocked", "invalid_url", "invalid_address", "timeout", "cancelled", "connection_failed":
		return true
	case "tls_downgrade", "unexpected_status":
		return fact.kind == KindHTTP || fact.kind == KindHTTPS
	case "tls_handshake_failed":
		return isTLSKind(fact.kind)
	case "destination_unreached", "traceroute_failed", "traceroute_execution_incomplete":
		return fact.kind == KindTraceroute
	default:
		return false
	}
}

func isTLSKind(kind Kind) bool {
	switch kind {
	case KindHTTPS, KindSMTPS, KindIMAPS, KindPOP3S:
		return true
	default:
		return false
	}
}

func isEndpointKind(kind Kind) bool {
	switch kind {
	case KindTCP, KindHTTP, KindHTTPS, KindSSH, KindSMTP, KindSubmission, KindSMTPS, KindIMAP, KindIMAPS, KindPOP3, KindPOP3S:
		return true
	default:
		return false
	}
}

func isConnectionDetailKind(kind Kind) bool {
	switch kind {
	case KindTCP, KindSSH, KindSMTP, KindSubmission, KindSMTPS, KindIMAP, KindIMAPS, KindPOP3, KindPOP3S:
		return true
	default:
		return false
	}
}

func stringDetail(details map[string]any, key string) (string, bool) {
	if details == nil {
		return "", false
	}
	value, ok := details[key].(string)
	return value, ok
}

func stringSliceDetail(details map[string]any, key string) ([]string, bool) {
	if details == nil {
		return nil, false
	}
	switch values := details[key].(type) {
	case []string:
		return values, true
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
}

func integerDetail(details map[string]any, key string) (int, bool) {
	if details == nil {
		return 0, false
	}
	switch value := details[key].(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		if value >= int64(math.MinInt) && value <= int64(math.MaxInt) {
			return int(value), true
		}
	case uint:
		if uint64(value) <= uint64(math.MaxInt) {
			return int(value), true
		}
	case uint64:
		if value <= uint64(math.MaxInt) {
			return int(value), true
		}
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
			return 0, false
		}
		if strconv.IntSize == 64 {
			if value < -9223372036854775808 || value >= 9223372036854775808 {
				return 0, false
			}
		} else if value < -2147483648 || value >= 2147483648 {
			return 0, false
		}
		return int(value), true
	case json.Number:
		parsed, err := strconv.ParseInt(string(value), 10, 64)
		if err == nil && parsed >= int64(math.MinInt) && parsed <= int64(math.MaxInt) {
			return int(parsed), true
		}
	}
	return 0, false
}

func timeDetail(details map[string]any, key string) (time.Time, bool) {
	if details == nil {
		return time.Time{}, false
	}
	switch value := details[key].(type) {
	case time.Time:
		if !value.IsZero() {
			return value.UTC(), true
		}
	case string:
		parsed, err := time.Parse(time.RFC3339, value)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func normalizedTopology(value any) (Topology, bool) {
	if value == nil {
		return Topology{}, false
	}
	if topology, ok := value.(Topology); ok {
		return topology, len(topology.Nodes) > 0
	}
	if topology, ok := value.(*Topology); ok && topology != nil {
		return *topology, len(topology.Nodes) > 0
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return Topology{}, false
	}
	var topology Topology
	if err := json.Unmarshal(encoded, &topology); err != nil || len(topology.Nodes) == 0 {
		return Topology{}, false
	}
	return topology, true
}

func normalizedTraceAttempts(value any) ([]TraceAttempt, bool) {
	if typed, ok := value.([]TraceAttempt); ok {
		return validateTraceAttempts(typed)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var attempts []TraceAttempt
	if err := json.Unmarshal(encoded, &attempts); err != nil {
		return nil, false
	}
	return validateTraceAttempts(attempts)
}

func validateTraceAttempts(attempts []TraceAttempt) ([]TraceAttempt, bool) {
	if len(attempts) == 0 || len(attempts) > MaxTraceAttempts {
		return nil, false
	}
	seen := make(map[int]struct{}, len(attempts))
	for _, attempt := range attempts {
		if attempt.Attempt < 1 || attempt.Attempt > len(attempts) || !validStatus(attempt.Status) {
			return nil, false
		}
		if _, exists := seen[attempt.Attempt]; exists {
			return nil, false
		}
		seen[attempt.Attempt] = struct{}{}
		if attempt.ErrorCode != "" {
			if attempt.Status != StatusUnreachable || !validTraceAttemptError(attempt.ErrorCode) {
				return nil, false
			}
			continue
		}
		if attempt.Topology == nil || attempt.Status != topologyStatus(*attempt.Topology) {
			return nil, false
		}
	}
	return attempts, true
}

func validTraceAttemptError(code string) bool {
	return code == "timeout" || code == "cancelled" || code == "traceroute_failed"
}

func traceCountersMatchStatus(status Status, total, reached int) bool {
	switch status {
	case StatusHealthy:
		return reached == total
	case StatusDegraded:
		return reached > 0
	case StatusUnreachable:
		return reached == 0
	default:
		return false
	}
}

func traceAttemptsMatchCounters(attempts []TraceAttempt, total, reached, unreached, executionFailed, timedOut, cancelled int) bool {
	if len(attempts) != total {
		return false
	}
	observedReached, observedUnreached, observedExecution := 0, 0, 0
	observedTimedOut, observedCancelled := 0, 0
	for _, attempt := range attempts {
		switch {
		case attempt.ErrorCode != "":
			observedExecution++
			if attempt.ErrorCode == "timeout" {
				observedTimedOut++
			} else if attempt.ErrorCode == "cancelled" {
				observedCancelled++
			}
		case attempt.Topology != nil && attempt.Topology.Reached:
			observedReached++
		default:
			observedUnreached++
		}
	}
	return observedReached == reached && observedUnreached == unreached && observedExecution == executionFailed && observedTimedOut == timedOut && observedCancelled == cancelled
}

func tracePathsUnstable(attempts []TraceAttempt) bool {
	signatures := make(map[string]struct{})
	for _, attempt := range attempts {
		if attempt.Topology == nil || !attempt.Topology.Reached {
			continue
		}
		nodes := append([]TopologyNode(nil), attempt.Topology.Nodes...)
		sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Hop < nodes[j].Hop })
		parts := make([]string, 0, len(nodes))
		for _, node := range nodes {
			parts = append(parts, fmt.Sprintf("%d:%s", node.Hop, strings.TrimSpace(node.Address)))
		}
		signatures[strings.Join(parts, "|")] = struct{}{}
	}
	return len(signatures) > 1
}

func topologyDegraded(value any) bool {
	switch topology := value.(type) {
	case Topology:
		for _, node := range topology.Nodes {
			if node.Status == "degraded" {
				return true
			}
		}
		for _, link := range topology.Links {
			if link.Status == "degraded" {
				return true
			}
		}
	case *Topology:
		if topology != nil {
			return topologyDegraded(*topology)
		}
	case map[string]any:
		for _, key := range []string{"nodes", "links"} {
			items, ok := topology[key].([]any)
			if !ok {
				continue
			}
			for _, item := range items {
				if object, ok := item.(map[string]any); ok && object["status"] == "degraded" {
					return true
				}
			}
		}
	}
	return false
}
