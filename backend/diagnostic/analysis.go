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
	tracePathCompleted int
	tracePathReached   int
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
	facts := normalizeResults(results, &builder.analysis.Coverage, now.UTC())
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
		if finding.Code != FindingExecutionCancelled && finding.Code != FindingCheckerCapacityUnavailable && finding.Code != FindingTracerouteUnavailable {
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
		if isTLSFailureErrorCode(fact.errorCode) {
			return
		}
		if isServiceGreetingKind(fact.kind) && (fact.status == StatusHealthy || fact.errorCode == ResultErrorServiceGreetingUnverified) {
			return
		}
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
	if b.addTLSFailure(fact) {
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
	case "checker_panic":
		b.add(fact, FindingCheckerPanic, SeverityWarning, CategoryExecution,
			"Checker execution failed", "The checker stopped unexpectedly, so service health was not established.", ConfidenceDirect,
			"error_code", fact.errorCode, "completed checker execution", ProvenanceResult,
			"Repeat the failed check", "Repeat the bounded check and verify the checker runtime remains available.", "The repeated checker completes with an observation.", "Escalate to the runtime owner if the checker repeatedly stops unexpectedly.")
		return
	case "checker_capacity_unavailable":
		b.add(fact, FindingCheckerCapacityUnavailable, SeverityInfo, CategoryExecution,
			"Checker capacity was unavailable", "The bounded checker supervisor had no execution slot, so service health was not established.", ConfidenceDirect,
			"error_code", fact.errorCode, "available checker execution capacity", ProvenanceResult,
			"Retry after capacity is available", "Repeat the check after existing checker work has completed.", "The repeated check is admitted and completes with an observation.", "Escalate to the runtime owner if checker capacity remains unavailable.")
		return
	case "traceroute_unavailable":
		b.add(fact, FindingTracerouteUnavailable, SeverityInfo, CategoryExecution,
			"Traceroute is unavailable", "No functional traceroute capability was established at startup, so route health was not observed.", ConfidenceDirect,
			"error_code", fact.errorCode, "functional traceroute capability established at startup", ProvenanceResult,
			"Restore traceroute capability", "Install or repair the supported traceroute executable, then restart the service so startup can probe it.", "The restarted service reports ready and a traceroute check produces an observation.", "Escalate to the runtime owner if the startup capability probe remains unavailable.")
		return
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
		if fact.kind == KindTraceroute {
			break
		}
		b.add(fact, FindingExecutionTimeout, SeverityWarning, CategoryExecution,
			"Check timed out", "The check did not complete within its execution deadline.", ConfidenceDirect,
			"error_code", fact.errorCode, "completion before the configured deadline", ProvenanceResult,
			"Verify the timeout", "Retry once with the same bounded timeout and verify endpoint responsiveness from the same vantage point.", "The check completes before the configured deadline.", "Escalate if repeated bounded checks time out after endpoint availability is independently verified.")
		return
	case "cancelled":
		if fact.kind == KindTraceroute {
			break
		}
		b.add(fact, FindingExecutionCancelled, SeverityInfo, CategoryExecution,
			"Check was cancelled", "The check ended after cancellation, so service health was not established.", ConfidenceDirect,
			"error_code", fact.errorCode, "completed observation", ProvenanceResult,
			"Repeat the cancelled check", "Run the check again when the request can remain active through completion.", "The repeated check completes with an observed result.", "Escalate if checks are repeatedly cancelled without an intentional caller cancellation.")
		return
	case "response_read_failed":
		b.hasUnexplained = true
		b.analysis.Coverage.Limitations = append(b.analysis.Coverage.Limitations, CoverageIssue{Code: CoverageUnsupportedDetails, ResultIndex: fact.index, Kind: fact.kind, Signal: "response_body", Reason: "the bounded HTTP response sample could not be completed"})
		return
	case ResultErrorServiceGreetingUnverified:
		b.add(fact, FindingServiceGreetingUnverified, SeverityWarning, CategoryApplication,
			"Service greeting was not verified", "The transport connected, but the expected server-first service greeting was not verified.", ConfidenceDirect,
			"error_code", fact.errorCode, "expected server-first greeting", ProvenanceResult,
			"Verify the service greeting", "Verify that the intended service is listening and returns its expected server-first greeting without client input, then repeat the check.", "The endpoint returns the expected bounded server-first greeting.", "Escalate to the service owner if the expected greeting is still not verified after listener and protocol configuration are confirmed.")
		return
	}

	if fact.kind == KindTraceroute {
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
		genericFailed := fact.traceExecutionFail - fact.traceTimedOut - fact.traceCancelled
		switch {
		case fact.traceTimedOut == fact.traceExecutionFail:
			b.add(fact, FindingExecutionTimeout, SeverityWarning, CategoryExecution,
				"Traceroute attempts timed out", "One or more traceroute attempts did not complete within their execution deadline.", ConfidenceDirect,
				"traceroute.attempts_timed_out", strconv.Itoa(fact.traceTimedOut), "0", ProvenanceDetails,
				"Repeat the timed-out trace", "Repeat the bounded trace from the same vantage point.", "Every attempt completes before its deadline.", "Escalate if bounded attempts repeatedly time out after runtime availability is verified.")
		case fact.traceCancelled == fact.traceExecutionFail:
			b.add(fact, FindingExecutionCancelled, SeverityInfo, CategoryExecution,
				"Traceroute attempts were cancelled", "One or more traceroute attempts ended after cancellation.", ConfidenceDirect,
				"traceroute.attempts_cancelled", strconv.Itoa(fact.traceCancelled), "0", ProvenanceDetails,
				"Repeat the cancelled trace", "Run the trace again when the request can remain active.", "The repeated trace completes.", "Escalate if traces are cancelled without caller cancellation.")
		case genericFailed == fact.traceExecutionFail:
			b.add(fact, FindingTracerouteExecutionFailed, SeverityWarning, CategoryExecution,
				"Traceroute command attempts failed", "One or more traceroute command attempts did not produce a completed route observation.", ConfidenceDirect,
				"traceroute.attempts_execution_failed", strconv.Itoa(fact.traceExecutionFail), "0", ProvenanceDetails,
				"Verify traceroute execution", "Verify the traceroute executable and permissions, then repeat the bounded check.", "Every command attempt completes and produces a parseable route observation.", "Escalate to the runtime owner if traceroute command attempts still cannot complete.")
		default:
			observed := fmt.Sprintf("%d execution failures: %d timed out, %d cancelled, %d command errors", fact.traceExecutionFail, fact.traceTimedOut, fact.traceCancelled, genericFailed)
			b.add(fact, FindingTracerouteExecutionFailed, SeverityWarning, CategoryExecution,
				"Traceroute attempts had multiple execution failures", "Traceroute attempts ended with more than one recorded execution-failure class.", ConfidenceDirect,
				"traceroute.execution_failures", observed, "0 execution failures", ProvenanceDetails,
				"Verify traceroute execution", "Repeat the bounded trace and verify each execution-failure class separately.", "Every command attempt completes and produces a parseable route observation.", "Escalate to the runtime owner if execution failures recur.")
		}
	}

	completed := fact.traceReached + fact.traceUnreached
	observed := fmt.Sprintf("%d/%d completed attempts reached", fact.traceReached, completed)
	switch {
	case fact.traceReached == 0 && fact.traceUnreached > 0:
		b.add(fact, FindingTracerouteUnreachable, SeverityCritical, CategoryRouting,
			"No completed traceroute attempt reached the destination", "The destination was not observed as reached in any completed traceroute attempt.", ConfidenceDirect,
			"traceroute.attempts_reached", observed, fmt.Sprintf("%d/%d completed attempts reached", completed, completed), ProvenanceDetails,
			"Verify the route from the same vantage point", "Repeat the bounded trace and compare the last responsive hop and destination reachability.", "At least one completed trace reaches the destination or identifies a stable last responsive hop.", "Escalate with the recorded hops if repeated completed traces still do not reach the destination.")
	case fact.traceReached > 0 && fact.traceUnreached > 0:
		b.add(fact, FindingTraceroutePartialReachability, SeverityWarning, CategoryRouting,
			"Traceroute reachability varied across completed attempts", "Some completed traceroute attempts reached the destination and others did not.", ConfidenceDirect,
			"traceroute.attempts_reached", observed, fmt.Sprintf("%d/%d completed attempts reached", completed, completed), ProvenanceDetails,
			"Compare repeated routes", "Compare reached and unreached completed attempts for the first stable divergence while preserving the same vantage point.", "Repeated completed attempts show consistent reachability or a reproducible divergence.", "Escalate with both route sets if the variation persists.")
	}

	if fact.tracePathCompleted > 0 {
		switch {
		case fact.tracePathUnstable && fact.tracePathReached >= 2:
			b.add(fact, FindingTraceroutePathUnstable, SeverityWarning, CategoryRouting,
				"Traceroute paths varied across attempts", "Successful completed attempts observed more than one hop sequence from the same vantage point.", ConfidenceDirect,
				"traceroute.path_signatures", fmt.Sprintf("multiple successful completed path signatures among %d reached completed attempts", fact.tracePathReached), "one stable successful completed path signature", ProvenanceDetails,
				"Compare path variants", "Repeat the bounded trace and compare the first hop where successful completed paths diverge.", "The path stabilizes or the same divergence is reproduced.", "Escalate with the successful completed path variants if the divergence persists.")
		case fact.tracePathDegraded:
			b.add(fact, FindingTraceroutePathDegraded, SeverityWarning, CategoryRouting,
				"A degraded route segment was observed", "The traceroute producer classified at least one completed route hop or link as degraded.", ConfidenceLimited,
				"traceroute.path_status", completedPathEvidenceCount(fact.tracePathCompleted), "no producer-classified degraded segment in completed path evidence", ProvenanceDetails,
				"Verify the degraded segment", "Repeat the trace and compare the same hop transition across completed attempts and another approved vantage point.", "The repeated observations show whether the segment status remains degraded.", "Escalate with the repeated hop and route observations if the segment status remains degraded.")
		}
	}
	if fact.traceExecutionFail == 0 && fact.traceUnreached == 0 && !fact.tracePathUnstable && !fact.tracePathDegraded {
		b.hasUnexplained = true
	}
}

func (b *analysisBuilder) addCertificate(fact normalizedResultFacts, code FindingCode, severity FindingSeverity, title, summary string) {
	b.add(fact, code, severity, CategorySecurity, title, summary, ConfidenceDirect,
		"tls.certificate_expires_at", fact.certificateExpires.UTC().Format(time.RFC3339), "certificate valid beyond the 30-day renewal window", ProvenanceDetails,
		"Verify certificate renewal", "Confirm the deployed certificate chain and renewal schedule on the observed endpoint.", "The endpoint presents a currently valid certificate with adequate renewal margin.", "Escalate to the certificate owner if renewal or deployment cannot be confirmed before expiry.")
}

func (b *analysisBuilder) addTLSFailure(fact normalizedResultFacts) bool {
	type template struct {
		code           FindingCode
		title          string
		summary        string
		actionTitle    string
		step           string
		expectedResult string
		escalation     string
	}
	var value template
	switch fact.errorCode {
	case ResultErrorTLSCertificateExpired:
		value = template{FindingTLSCertificateExpired, "TLS certificate is expired", "The TLS certificate was expired at the verification time.", "Renew the TLS certificate", "Renew and deploy the intended certificate chain, then repeat the check.", "The endpoint presents a certificate that is valid at verification time.", "Escalate to the certificate owner if a current certificate cannot be deployed."}
	case ResultErrorTLSCertificateNotYetValid:
		value = template{FindingTLSCertificateNotYetValid, "TLS certificate is not yet valid", "The TLS certificate was not yet valid at the verification time.", "Verify certificate activation", "Verify the endpoint certificate deployment and system clocks, then repeat the check.", "The endpoint presents a certificate that is valid at verification time.", "Escalate to the certificate owner if the validity window or deployment remains incorrect."}
	case ResultErrorTLSHostnameMismatch:
		value = template{FindingTLSHostnameMismatch, "TLS certificate name does not match", "The TLS certificate did not match the requested server name.", "Correct the TLS server identity", "Deploy a certificate for the requested server name and verify endpoint routing, then repeat the check.", "Certificate verification succeeds for the requested server name.", "Escalate to the TLS endpoint owner if the intended name still does not verify."}
	case ResultErrorTLSUntrusted:
		value = template{FindingTLSUntrusted, "TLS certificate is not trusted", "The TLS certificate chain did not verify to a trusted authority.", "Correct the TLS trust chain", "Deploy the intended complete certificate chain and verify its trust anchor, then repeat the check.", "The endpoint certificate chain verifies to an approved trust anchor.", "Escalate to the certificate owner if the intended chain remains untrusted."}
	case ResultErrorTLSHandshakeFailed:
		value = template{FindingTLSHandshakeFailed, "TLS handshake did not complete", "The transport connected, but the TLS handshake failed.", "Verify the TLS endpoint", "Check the certificate chain, server name, protocol versions, and cipher compatibility, then repeat the check.", "The TLS handshake completes with the intended endpoint.", "Escalate to the TLS endpoint owner if the handshake still fails with a valid trust chain and server name."}
	default:
		return false
	}
	b.add(fact, value.code, SeverityCritical, CategorySecurity, value.title, value.summary, ConfidenceDirect,
		"error_code", fact.errorCode, "verified TLS handshake", ProvenanceResult,
		value.actionTitle, value.step, value.expectedResult, value.escalation)
	return true
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
		aFamily, aTrace := tracerouteFindingFamily(a, b.analysis.Evidence)
		cFamily, cTrace := tracerouteFindingFamily(c, b.analysis.Evidence)
		if aTrace != cTrace {
			return aTrace
		}
		if aTrace && aFamily != cFamily {
			return aFamily < cFamily
		}
		if severityRank(a.Severity) != severityRank(c.Severity) {
			return severityRank(a.Severity) < severityRank(c.Severity)
		}
		if a.Code != c.Code {
			return a.Code < c.Code
		}
		return a.ID < c.ID
	})
}

func tracerouteFindingFamily(finding Finding, evidence []Evidence) (int, bool) {
	traceEvidence := false
	for _, evidenceID := range finding.EvidenceIDs {
		for _, item := range evidence {
			if item.ID == evidenceID && item.Kind == KindTraceroute {
				traceEvidence = true
				break
			}
		}
	}
	if !traceEvidence {
		return 0, false
	}
	switch finding.Code {
	case FindingExecutionTimeout, FindingExecutionCancelled, FindingTracerouteExecutionFailed:
		return 0, true
	case FindingTracerouteUnreachable, FindingTraceroutePartialReachability:
		return 1, true
	case FindingTraceroutePathDegraded, FindingTraceroutePathUnstable:
		return 2, true
	default:
		return 0, false
	}
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

func normalizeResults(results []Result, coverage *Coverage, now time.Time) []normalizedResultFacts {
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
		tlsFailure := result.Status == StatusUnreachable && isTLSKind(result.Kind) && isTLSFailureErrorCode(result.ErrorCode)
		greetingFailure := isServiceGreetingKind(result.Kind) && result.ErrorCode == ResultErrorServiceGreetingUnverified
		tracerouteUnavailable := result.Kind == KindTraceroute && result.Status == StatusUnreachable && result.ErrorCode == "traceroute_unavailable"
		if result.Details == nil && !(tlsFailure && tlsFailurePermitsNoDetails(result.ErrorCode)) && !greetingFailure && !tracerouteUnavailable {
			coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details", index))
			coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMissingDetails, ResultIndex: index, Kind: result.Kind, Signal: "details", Reason: "checker details were not observed"})
		}
		if tlsFailure {
			if valid, missing := validTLSFailureDetails(result.ErrorCode, result.Details, now); !valid {
				fact.detailsValid = false
				if missing {
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.tls_validity", index))
				}
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "tls_failure_details", Reason: "TLS failure details did not match the closed error-code contract"})
			}
		}
		if isServiceGreetingKind(result.Kind) && (result.Status == StatusHealthy || greetingFailure) {
			if !validServiceGreetingDetails(result) {
				fact.detailsValid = false
				if result.Status == StatusHealthy {
					if _, exists := result.Details[ResultDetailVerificationScope]; !exists {
						coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.verification_scope", index))
					}
				}
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "service_verification_details", Reason: "service greeting status, error code, scope, or details did not match the closed result contract"})
			} else if result.Status == StatusHealthy {
				coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.verification_scope", index))
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageUnsupportedDetails, ResultIndex: index, Kind: result.Kind, Signal: "service_verification_scope", Reason: "only the expected server-first greeting was observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested"})
			}
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
				if version, ok := stringDetail(result.Details, ResultDetailTLSVersion); !ok || strings.TrimSpace(version) == "" {
					fact.detailsValid = false
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.tls", index))
					coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMissingDetails, ResultIndex: index, Kind: result.Kind, Signal: "tls", Reason: "healthy TLS result lacked handshake facts"})
				}
				if _, exists := result.Details[ResultDetailCertificateExpires]; !exists {
					fact.detailsValid = false
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.tls_certificate", index))
					coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMissingDetails, ResultIndex: index, Kind: result.Kind, Signal: "tls_certificate", Reason: "healthy TLS result lacked certificate expiry facts"})
				}
			}
			if expires, ok := timeDetail(result.Details, ResultDetailCertificateExpires); ok {
				fact.certificateExpires, fact.hasCertificate = expires, true
				coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.tls_certificate", index))
			} else if _, exists := result.Details[ResultDetailCertificateExpires]; exists {
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: ResultDetailCertificateExpires, Reason: "certificate expiry detail was malformed"})
			}
		}
		if result.Kind == KindTraceroute && !tracerouteUnavailable {
			legacyGeoIPFailures, legacyGeoIPFailuresOK := integerDetail(result.Details, "geoip_provider_failures")
			if legacyGeoIPFailuresOK && legacyGeoIPFailures > 0 {
				coverage.ProviderFailures = append(coverage.ProviderFailures, CoverageIssue{Code: CoverageMissingDetails, ResultIndex: index, Kind: result.Kind, Signal: "geoip", Reason: fmt.Sprintf("%d GeoIP enrichment lookups failed", legacyGeoIPFailures)})
			}
			if enrichmentValue, exists := result.Details["geoip_enrichment"]; exists {
				enrichment, ok := normalizedEnrichmentCoverage(enrichmentValue, legacyGeoIPFailures, legacyGeoIPFailuresOK)
				if ok && index < MaxTargets && mergeEnrichmentCoverage(coverage, enrichment) {
					coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.geoip_enrichment", index))
				} else {
					coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "geoip_enrichment", Reason: "GeoIP enrichment coverage was malformed, contradictory, or exceeded its bound"})
				}
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
			if totalOK && reachedOK && failedOK && unreachedOK && executionOK && timedOutOK && cancelledOK && total > 0 && total <= MaxTraceAttempts && reached >= 0 && reached <= total && failed >= 0 && failed <= total && unreached >= 0 && unreached <= total && executionFailed >= 0 && executionFailed <= total && timedOut >= 0 && timedOut <= executionFailed && cancelled >= 0 && cancelled <= executionFailed && failed == unreached+executionFailed && total-reached == failed && timedOut <= executionFailed-cancelled && traceCountersMatchStatus(result.Status, total, reached) {
				fact.traceTotal, fact.traceReached, fact.traceFailed, fact.hasTraceCounters = total, reached, failed, true
				fact.traceUnreached, fact.traceExecutionFail, fact.traceTimedOut, fact.traceCancelled = unreached, executionFailed, timedOut, cancelled
				topologyValue, topologyExists := result.Details["topology"]
				topology, topologyOK := normalizedTopology(topologyValue)
				attemptsObserved := false
				if attemptsValue, exists := result.Details["attempts"]; exists {
					attemptsObserved = true
					attempts, ok := normalizedTraceAttempts(attemptsValue)
					eligible, eligibleOK := eligibleCompletedTraceAttempts(attempts, reached, unreached)
					if ok && traceAttemptsMatchCounters(attempts, total, reached, unreached, executionFailed, timedOut, cancelled) && eligibleOK {
						attemptsDegraded := traceAttemptsDegraded(eligible)
						if topologyOK && topologyDegraded(topology) && !attemptsDegraded {
							coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "trace_paths", Reason: "traceroute aggregate topology contradicted completed attempt path evidence"})
						}
						fact.tracePathCompleted = len(eligible)
						fact.tracePathReached = reached
						fact.tracePathDegraded = attemptsDegraded
						fact.tracePathUnstable = tracePathsUnstable(eligible)
						coverage.Available = append(coverage.Available, fmt.Sprintf("results[%d].details.trace_paths", index))
					} else {
						fact.hasTraceCounters = false
						fact.detailsValid = false
						reason := "traceroute attempts were malformed"
						if ok {
							reason = "traceroute attempts contradicted aggregate counters or completed path evidence"
						}
						coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "trace_paths", Reason: reason})
					}
				} else if executionFailed == 0 && topologyOK && ((topology.Reached && reached > 0) || (!topology.Reached && unreached > 0)) {
					fact.tracePathCompleted = 1
					if topology.Reached {
						fact.tracePathReached = 1
					}
					fact.tracePathDegraded = topologyDegraded(topology)
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
				if !totalExists || !reachedExists || !failedExists || !unreachedExists || !executionExists || !timedOutExists || !cancelledExists {
					coverage.Missing = append(coverage.Missing, fmt.Sprintf("results[%d].details.trace_attempts", index))
				}
				coverage.Limitations = append(coverage.Limitations, CoverageIssue{Code: CoverageMalformedDetails, ResultIndex: index, Kind: result.Kind, Signal: "trace_attempts", Reason: "traceroute counters were missing, malformed, or inconsistent"})
			}
		}
		facts = append(facts, fact)
	}
	return facts
}

const maxGeoIPLookupsPerResult = MaxTraceAttempts * (MaxTraceHops + 1)

func normalizedEnrichmentCoverage(value any, legacyFailures int, legacyFailuresOK bool) (EnrichmentCoverage, bool) {
	var normalized EnrichmentCoverage
	switch detail := value.(type) {
	case EnrichmentCoverage:
		normalized = detail
	case *EnrichmentCoverage:
		if detail == nil {
			return EnrichmentCoverage{}, false
		}
		normalized = *detail
	case map[string]any:
		if !hasOnlyKeys(detail, "provider", "source", "cache_hits", "upstream_fetches", "max_age_ms", "failures") {
			return EnrichmentCoverage{}, false
		}
		provider, providerOK := detail["provider"].(string)
		sourceText, sourceOK := detail["source"].(string)
		cacheHits, cacheOK := integerDetail(detail, "cache_hits")
		upstreamFetches, upstreamOK := integerDetail(detail, "upstream_fetches")
		maxAge, ageOK := integerDetail(detail, "max_age_ms")
		failures, failuresOK := normalizedEnrichmentFailures(detail["failures"])
		if !providerOK || !sourceOK || !cacheOK || !upstreamOK || !ageOK || !failuresOK {
			return EnrichmentCoverage{}, false
		}
		normalized = EnrichmentCoverage{Provider: provider, Source: EnrichmentSource(sourceText), CacheHits: cacheHits, UpstreamFetches: upstreamFetches, MaxAgeMS: int64(maxAge), Failures: failures}
	default:
		return EnrichmentCoverage{}, false
	}

	if normalized.Failures == nil {
		return EnrichmentCoverage{}, false
	}
	failures, ok := normalizedEnrichmentFailures(normalized.Failures)
	if !ok {
		return EnrichmentCoverage{}, false
	}
	normalized.Failures = failures
	failureCount := enrichmentFailureCount(failures)
	lookupCount := normalized.CacheHits + normalized.UpstreamFetches + failureCount
	if normalized.Provider != "geoip" || normalized.CacheHits < 0 || normalized.UpstreamFetches < 0 || normalized.MaxAgeMS < 0 || normalized.MaxAgeMS > defaultGeoIPCacheTTL.Milliseconds() || lookupCount < 0 || lookupCount > maxGeoIPLookupsPerResult {
		return EnrichmentCoverage{}, false
	}
	if normalized.Source != enrichmentSource(normalized.CacheHits, normalized.UpstreamFetches) {
		return EnrichmentCoverage{}, false
	}
	if legacyFailuresOK && legacyFailures >= 0 && legacyFailures != failureCount {
		return EnrichmentCoverage{}, false
	}
	return normalized, true
}

func normalizedEnrichmentFailures(value any) ([]EnrichmentFailure, bool) {
	var failures []EnrichmentFailure
	switch values := value.(type) {
	case []EnrichmentFailure:
		if values == nil {
			return nil, false
		}
		failures = make([]EnrichmentFailure, len(values))
		copy(failures, values)
	case []any:
		if values == nil || len(values) > len(geoIPFailureKinds()) {
			return nil, false
		}
		failures = make([]EnrichmentFailure, 0, len(values))
		for _, value := range values {
			object, ok := value.(map[string]any)
			if !ok || !hasOnlyKeys(object, "kind", "count", "retryable") {
				return nil, false
			}
			kind, kindOK := object["kind"].(string)
			count, countOK := integerDetail(object, "count")
			retryable, retryableOK := object["retryable"].(bool)
			if !kindOK || !countOK || !retryableOK {
				return nil, false
			}
			failures = append(failures, EnrichmentFailure{Kind: GeoIPErrorKind(kind), Count: count, Retryable: retryable})
		}
	default:
		return nil, false
	}
	if len(failures) > len(geoIPFailureKinds()) {
		return nil, false
	}
	seen := make(map[GeoIPErrorKind]struct{}, len(failures))
	for _, failure := range failures {
		if !validGeoIPErrorKind(failure.Kind) || failure.Count <= 0 || failure.Count > maxGeoIPLookupsPerResult || failure.Retryable != geoIPFailureRetryable(failure.Kind) {
			return nil, false
		}
		if _, exists := seen[failure.Kind]; exists {
			return nil, false
		}
		seen[failure.Kind] = struct{}{}
	}
	sort.Slice(failures, func(i, j int) bool { return failures[i].Kind < failures[j].Kind })
	return failures, true
}

func hasOnlyKeys(object map[string]any, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			return false
		}
	}
	return true
}

func mergeEnrichmentCoverage(coverage *Coverage, addition EnrichmentCoverage) bool {
	if len(coverage.Enrichment) == 0 {
		failures := make([]EnrichmentFailure, len(addition.Failures))
		copy(failures, addition.Failures)
		addition.Failures = failures
		coverage.Enrichment = []EnrichmentCoverage{addition}
		return true
	}
	aggregate := coverage.Enrichment[0]
	aggregateLimit := MaxTargets * maxGeoIPLookupsPerResult
	if aggregate.CacheHits > aggregateLimit-addition.CacheHits || aggregate.UpstreamFetches > aggregateLimit-addition.UpstreamFetches {
		return false
	}
	aggregate.CacheHits += addition.CacheHits
	aggregate.UpstreamFetches += addition.UpstreamFetches
	if addition.MaxAgeMS > aggregate.MaxAgeMS {
		aggregate.MaxAgeMS = addition.MaxAgeMS
	}
	counts := make(map[GeoIPErrorKind]int, len(geoIPFailureKinds()))
	for _, group := range [][]EnrichmentFailure{aggregate.Failures, addition.Failures} {
		for _, failure := range group {
			if counts[failure.Kind] > aggregateLimit-failure.Count {
				return false
			}
			counts[failure.Kind] += failure.Count
		}
	}
	aggregate.Failures = aggregate.Failures[:0]
	for _, kind := range geoIPFailureKinds() {
		if count := counts[kind]; count > 0 {
			aggregate.Failures = append(aggregate.Failures, EnrichmentFailure{Kind: kind, Count: count, Retryable: geoIPFailureRetryable(kind)})
		}
	}
	aggregate.Source = enrichmentSource(aggregate.CacheHits, aggregate.UpstreamFetches)
	coverage.Enrichment[0] = aggregate
	return true
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
		return status == StatusUnreachable && reached == 0 && executionFailed > 0 && timedOut == executionFailed
	case "cancelled":
		return status == StatusUnreachable && reached == 0 && executionFailed > 0 && cancelled == executionFailed
	case "traceroute_failed":
		return status == StatusUnreachable && reached == 0 && executionFailed > 0 && timedOut == 0 && cancelled == 0
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
		return status == StatusUnreachable && reached == 0 && executionFailed > 0 && categories >= 2
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
	case "network_policy_blocked", "invalid_url", "invalid_address", "timeout", "cancelled", "connection_failed", "response_read_failed", "checker_panic", "checker_capacity_unavailable":
		return true
	case "traceroute_unavailable":
		return fact.kind == KindTraceroute
	case ResultErrorServiceGreetingUnverified:
		return fact.status == StatusDegraded && isServiceGreetingKind(fact.kind)
	case "tls_downgrade", "unexpected_status":
		return fact.kind == KindHTTP || fact.kind == KindHTTPS
	case ResultErrorTLSCertificateExpired, ResultErrorTLSCertificateNotYetValid, ResultErrorTLSHostnameMismatch, ResultErrorTLSUntrusted, ResultErrorTLSHandshakeFailed:
		return fact.status == StatusUnreachable && isTLSKind(fact.kind)
	case "destination_unreached", "traceroute_failed", "traceroute_execution_incomplete":
		return fact.kind == KindTraceroute
	default:
		return false
	}
}

func isTLSFailureErrorCode(code string) bool {
	switch code {
	case ResultErrorTLSCertificateExpired, ResultErrorTLSCertificateNotYetValid, ResultErrorTLSHostnameMismatch, ResultErrorTLSUntrusted, ResultErrorTLSHandshakeFailed:
		return true
	default:
		return false
	}
}

func tlsFailurePermitsNoDetails(code string) bool {
	return code == ResultErrorTLSHostnameMismatch || code == ResultErrorTLSUntrusted || code == ResultErrorTLSHandshakeFailed
}

func validTLSFailureDetails(code string, details map[string]any, now time.Time) (valid, missing bool) {
	if tlsFailurePermitsNoDetails(code) {
		return len(details) == 0, false
	}
	if code != ResultErrorTLSCertificateExpired && code != ResultErrorTLSCertificateNotYetValid {
		return false, false
	}
	if len(details) != 2 {
		_, before := details[ResultDetailCertificateBefore]
		_, after := details[ResultDetailCertificateAfter]
		return false, !before || !after
	}
	before, beforeOK := canonicalUTCRFC3339Detail(details, ResultDetailCertificateBefore)
	after, afterOK := canonicalUTCRFC3339Detail(details, ResultDetailCertificateAfter)
	if !beforeOK || !afterOK || !before.Before(after) {
		return false, false
	}
	switch code {
	case ResultErrorTLSCertificateExpired:
		return !now.Before(after), false
	case ResultErrorTLSCertificateNotYetValid:
		return now.Before(before), false
	default:
		return false, false
	}
}

func canonicalUTCRFC3339Detail(details map[string]any, key string) (time.Time, bool) {
	text, ok := details[key].(string)
	if !ok {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil || parsed.UTC().Format(time.RFC3339) != text {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func isTLSKind(kind Kind) bool {
	switch kind {
	case KindHTTPS, KindSMTPS, KindIMAPS, KindPOP3S:
		return true
	default:
		return false
	}
}

func isServiceGreetingKind(kind Kind) bool {
	switch kind {
	case KindSSH, KindSMTP, KindSubmission, KindSMTPS, KindIMAP, KindIMAPS, KindPOP3, KindPOP3S:
		return true
	default:
		return false
	}
}

func validServiceGreetingDetails(result Result) bool {
	if result.ErrorCode == ResultErrorServiceGreetingUnverified {
		return result.Status == StatusDegraded && len(result.Details) == 0
	}
	if result.Status != StatusHealthy || result.ErrorCode != "" || result.Details == nil {
		return false
	}
	scope, ok := stringDetail(result.Details, ResultDetailVerificationScope)
	if !ok || scope != VerificationScopeServerGreeting {
		return false
	}
	allowed := map[string]struct{}{ResultDetailVerificationScope: struct{}{}}
	if isTLSKind(result.Kind) {
		for _, key := range []string{ResultDetailTLSVersion, ResultDetailCipherSuite, ResultDetailCertificateSubject, ResultDetailCertificateExpires} {
			allowed[key] = struct{}{}
		}
		if value, exists := result.Details[ResultDetailTLSVersion]; exists {
			text, valid := value.(string)
			if !valid || strings.TrimSpace(text) == "" {
				return false
			}
		}
		for _, key := range []string{ResultDetailCipherSuite, ResultDetailCertificateSubject} {
			if value, exists := result.Details[key]; exists {
				if _, valid := value.(string); !valid {
					return false
				}
			}
		}
		if _, exists := result.Details[ResultDetailCertificateExpires]; exists {
			if _, valid := timeDetail(result.Details, ResultDetailCertificateExpires); !valid {
				return false
			}
		}
	}
	for key := range result.Details {
		if _, ok := allowed[key]; !ok {
			return false
		}
	}
	return true
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
	case KindTCP:
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

func eligibleCompletedTraceAttempts(attempts []TraceAttempt, reached, unreached int) ([]TraceAttempt, bool) {
	if len(attempts) == 0 || reached < 0 || unreached < 0 {
		return nil, false
	}
	eligible := make([]TraceAttempt, 0, reached+unreached)
	observedReached, observedUnreached := 0, 0
	for _, attempt := range attempts {
		if attempt.ErrorCode != "" {
			continue
		}
		if attempt.Topology == nil || attempt.Status != topologyStatus(*attempt.Topology) {
			return nil, false
		}
		eligible = append(eligible, attempt)
		if attempt.Topology.Reached {
			observedReached++
		} else {
			observedUnreached++
		}
	}
	if len(eligible) != reached+unreached || observedReached != reached || observedUnreached != unreached {
		return nil, false
	}
	return eligible, true
}

func completedPathEvidenceCount(count int) string {
	noun := "attempts"
	if count == 1 {
		noun = "attempt"
	}
	return fmt.Sprintf("degraded segment observed among %d completed %s", count, noun)
}

func traceAttemptsDegraded(attempts []TraceAttempt) bool {
	for _, attempt := range attempts {
		if attempt.ErrorCode == "" && attempt.Topology != nil && topologyDegraded(*attempt.Topology) {
			return true
		}
	}
	return false
}

func tracePathsUnstable(attempts []TraceAttempt) bool {
	reachedAttempts := 0
	for _, attempt := range attempts {
		if attempt.ErrorCode == "" && attempt.Topology != nil && attempt.Topology.Reached {
			reachedAttempts++
		}
	}
	if reachedAttempts < 2 {
		return false
	}
	signatures := make(map[string]struct{})
	for _, attempt := range attempts {
		if attempt.ErrorCode != "" || attempt.Topology == nil || !attempt.Topology.Reached {
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
