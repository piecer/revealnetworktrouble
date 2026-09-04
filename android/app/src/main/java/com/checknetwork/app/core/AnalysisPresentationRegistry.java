package com.checknetwork.app.core;

import java.time.Instant;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.EnumMap;
import java.util.EnumSet;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Closed, local-only human presentation registry. Producer-owned prose and network identifiers
 * are deliberately absent from every returned section.
 */
public final class AnalysisPresentationRegistry {
    public static final String GREETING_SCOPE = "Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.";
    private static final List<String> SEMANTIC_KEYS = List.of(
            "cause", "supporting_evidence", "expectation", "evidence_directness",
            "coverage_limitation", "next_action");
    private static final Pattern INTEGER = Pattern.compile("(?:0|[1-9][0-9]{0,2})");
    private static final Pattern REACH = Pattern.compile("(\\d{1,2})/(\\d{1,2}) completed attempts reached");
    private static final Pattern PATH_COUNT = Pattern.compile("degraded segment observed among (\\d{1,2}) completed attempts?");
    private static final Pattern PATH_SIGNATURES = Pattern.compile("multiple successful completed path signatures among (\\d{1,2}) reached completed attempts");
    private static final Pattern EXECUTION_CLASSES = Pattern.compile("(\\d{1,2}) execution failures: (\\d{1,2}) timed out, (\\d{1,2}) cancelled, (\\d{1,2}) command errors");
    private static final Set<String> SAFE_ERROR_CODES = Set.of(
            "cancelled", "checker_capacity_unavailable", "checker_panic", "connection_failed",
            "destination_unreached", "invalid_address", "invalid_url", "network_policy_blocked",
            "partial_answer", "response_read_failed", "service_greeting_unverified", "timeout",
            "tls_certificate_expired", "tls_certificate_not_yet_valid", "tls_downgrade",
            "tls_handshake_failed", "tls_hostname_mismatch", "tls_untrusted",
            "traceroute_execution_incomplete", "traceroute_failed", "traceroute_unavailable", "unexpected_status");
    private static final EnumMap<Report.FindingCode, Template> REGISTRY = buildRegistry();
    private static final Map<String, EvidenceSpec> EVIDENCE_REGISTRY = buildEvidenceRegistry();
    private static final Map<String, String> COVERAGE_REGISTRY = buildCoverageRegistry();

    private AnalysisPresentationRegistry() {}

    public record Section(String key, String heading, String body) {}

    private record Template(String cause, String expectation, String limitation, String actionKey, String action,
                            Set<String> signals) {}

    private record EvidenceSpec(String observedShape, String expectedShape) {}

    public static Set<Report.FindingCode> registeredCodes() {
        return Collections.unmodifiableSet(EnumSet.copyOf(REGISTRY.keySet()));
    }

    public static List<String> semanticKeys() { return SEMANTIC_KEYS; }

    public static Set<String> registeredActionKeys() {
        Set<String> keys = new HashSet<>();
        for (Template template : REGISTRY.values()) keys.add(template.actionKey());
        return Collections.unmodifiableSet(keys);
    }

    public static Set<String> registeredEvidenceSignals() {
        return EVIDENCE_REGISTRY.keySet();
    }

    public static Set<String> registeredCoverageSignals() {
        return COVERAGE_REGISTRY.keySet();
    }

    public static String presentationKey(Report.FindingCode code) {
        requireTemplate(code);
        return "finding." + code.name().toLowerCase(Locale.ROOT);
    }

    public static String actionKey(Report.FindingCode code) { return requireTemplate(code).actionKey(); }

    public static String evidenceObservedShape(String signal) { return requireEvidenceSpec(signal).observedShape(); }

    public static String evidenceExpectedShape(String signal) { return requireEvidenceSpec(signal).expectedShape(); }

    public static boolean hasHealthyGreeting(Report report) {
        for (Report.Result result : report.results()) if (isHealthyGreeting(result)) return true;
        return false;
    }

    public static String reportStatus(Report report) {
        return statusLabel(report.status());
    }

    public static String verdict(Report report) {
        if (report.analysis().isEmpty()) return "Analysis unavailable";
        return switch (report.analysis().orElseThrow().verdict()) {
            case HEALTHY -> "No known finding in the checks that were performed";
            case ATTENTION -> "Attention needed within the observed scope";
            case INCONCLUSIVE -> "Inconclusive within the observed scope";
        };
    }

    public static String resultOutcome(Report.Result result) {
        return isHealthyGreeting(result) ? GREETING_SCOPE : statusLabel(result.status());
    }

    public static List<Section> sections(Report report) {
        List<Section> sections = new ArrayList<>();
        if (report.analysis().isEmpty()) {
            addSix(sections, "analysis.unavailable", "Automatic analysis is unavailable for this report.",
                    "No validated supporting evidence was linked.", "A current bounded analysis record is expected.",
                    "Evidence directness is unavailable.", "Only bounded result status and counts can be summarized.",
                    "Run the check against a producer that supplies the closed analysis contract.");
            return List.copyOf(sections);
        }
        Report.Analysis analysis = report.analysis().orElseThrow();
        validateClosedSignals(analysis);
        for (Report.Finding finding : analysis.findings()) addFinding(sections, finding, analysis);
        for (int resultIndex = 0; resultIndex < report.results().size(); resultIndex++) {
            Report.Result result = report.results().get(resultIndex);
            if (isHealthyGreeting(result)) addGreeting(sections, result, resultIndex, analysis.coverage());
        }
        if (sections.isEmpty()) {
            addSix(sections, "analysis.no_known_finding", "No known finding was produced for the checks that were performed.",
                    "No validated supporting evidence was linked.", "Only the explicitly performed checks can support a conclusion.",
                    "No finding-specific evidence directness is available.", "Untested behavior is not implied by an empty finding list.",
                    "Review the bounded result statuses and repeat any check whose scope is insufficient.");
        }
        return List.copyOf(sections);
    }

    private static void validateClosedSignals(Report.Analysis analysis) {
        for (Report.Evidence evidence : analysis.evidence()) requireEvidenceSpec(evidence.signal());
        validateCoverageSignals(analysis.coverage().providerFailures());
        validateCoverageSignals(analysis.coverage().limitations());
    }

    private static void validateCoverageSignals(List<Report.CoverageIssue> issues) {
        for (Report.CoverageIssue issue : issues) coverageSignalLabel(issue.signal());
    }

    private static void addFinding(List<Section> out, Report.Finding finding, Report.Analysis analysis) {
        Template template = requireTemplate(finding.code());
        String marker = presentationKey(finding.code());
        EvidenceProjection evidence = evidence(finding, template, analysis.evidence());
        add(out, "cause", "Cause", marker + "\n" + template.cause());
        add(out, "supporting_evidence", "Supporting evidence", evidence.supporting());
        add(out, "expectation", "Expectation", evidence.expected() == null ? template.expectation() : evidence.expected());
        add(out, "evidence_directness", "Evidence directness", directness(finding.confidence(), evidence.direct()));
        add(out, "coverage_limitation", "Coverage limitation", coverage(template.limitation(), evidence.resultIndexes(), analysis.coverage()));
        add(out, "next_action", "Next action", template.actionKey() + "\n" + template.action());
    }

    private static void addGreeting(List<Section> out, Report.Result result, int resultIndex, Report.Coverage coverage) {
        String marker = "service." + result.kind().wireValue() + ".server_greeting";
        add(out, "cause", "Cause", marker + "\nThe expected server-first greeting was observed.");
        add(out, "supporting_evidence", "Supporting evidence", kindLabel(result.kind()) + " status: greeting observed.");
        add(out, "expectation", "Expectation", "Expected server-first greeting.");
        add(out, "evidence_directness", "Evidence directness", "Direct bounded result observation.");
        add(out, "coverage_limitation", "Coverage limitation", GREETING_SCOPE + coverageIssues(Set.of(resultIndex), coverage));
        add(out, "next_action", "Next action", "Run separately authorized protocol-level checks if command, authentication, STARTTLS, mailbox, or end-to-end behavior must be verified.");
    }

    private static void addSix(List<Section> out, String marker, String cause, String evidence, String expectation,
                               String directness, String limitation, String action) {
        add(out, "cause", "Cause", marker + "\n" + cause);
        add(out, "supporting_evidence", "Supporting evidence", evidence);
        add(out, "expectation", "Expectation", expectation);
        add(out, "evidence_directness", "Evidence directness", directness);
        add(out, "coverage_limitation", "Coverage limitation", limitation);
        add(out, "next_action", "Next action", action);
    }

    private static void add(List<Section> out, String key, String heading, String body) {
        out.add(new Section(key, heading, body));
    }

    private record EvidenceProjection(String supporting, String expected, boolean direct, Set<Integer> resultIndexes) {}

    private static EvidenceProjection evidence(Report.Finding finding, Template template, List<Report.Evidence> all) {
        Map<String, Report.Evidence> byId = new HashMap<>();
        for (Report.Evidence evidence : all) byId.put(evidence.id(), evidence);
        List<String> lines = new ArrayList<>();
        String expected = null;
        boolean direct = false;
        Set<Integer> indexes = new HashSet<>();
        for (String id : finding.evidenceIds()) {
            Report.Evidence item = byId.get(id);
            if (item == null) continue;
            requireEvidenceSpec(item.signal());
            if (!template.signals().contains(item.signal())) continue;
            SafeEvidence safe = safeEvidence(item);
            if (safe == null) continue;
            String itemExpected = safe.expected(item.expected());
            StringBuilder line = new StringBuilder(safe.label()).append(": ").append(safe.observed());
            item.attempt().ifPresent(attempt -> line.append(" (attempt ").append(attempt).append(')'));
            if (itemExpected != null) line.append("; ").append(itemExpected);
            lines.add(line.toString());
            if (expected == null) expected = itemExpected;
            direct |= item.provenance() == Report.Provenance.RESULT || item.provenance() == Report.Provenance.DETAILS;
            indexes.add(item.resultIndex());
        }
        return new EvidenceProjection(lines.isEmpty() ? "No validated supporting evidence was linked." : String.join("\n", lines),
                expected, direct, Collections.unmodifiableSet(indexes));
    }

    private record SafeEvidence(String label, String observed, EvidenceExpected expectedBuilder) {
        String expected(String producerExpected) { return expectedBuilder.render(producerExpected); }
    }
    private interface EvidenceExpected { String render(String value); }

    private static SafeEvidence safeEvidence(Report.Evidence evidence) {
        String signal = evidence.signal();
        String observed = evidence.observed();
        requireEvidenceSpec(signal);
        if ("error_code".equals(signal) && SAFE_ERROR_CODES.contains(observed)) {
            return new SafeEvidence("Observed result code", observed, ignored -> null);
        }
        if ("http.status_code".equals(signal)) {
            Integer actual = boundedInteger(observed, 100, 599);
            Integer expected = boundedInteger(evidence.expected(), 100, 599);
            if (actual == null || expected == null) return null;
            return new SafeEvidence("Observed HTTP status", String.valueOf(actual), ignored -> "Expected HTTP status: " + expected);
        }
        if ("tls.certificate_expires_at".equals(signal)) {
            String timestamp = safeTimestamp(observed);
            if (timestamp == null) return null;
            return new SafeEvidence("Observed certificate expiry", timestamp, ignored -> null);
        }
        if (Set.of("traceroute.attempts_timed_out", "traceroute.attempts_cancelled", "traceroute.attempts_execution_failed").contains(signal)) {
            Integer count = boundedInteger(observed, 0, 10);
            Integer expected = boundedInteger(evidence.expected(), 0, 10);
            if (count == null || expected == null) return null;
            return new SafeEvidence(traceSignalLabel(signal), count + " attempts",
                    ignored -> "Expected count: " + expected + " attempts");
        }
        if ("traceroute.attempts_reached".equals(signal)) {
            Matcher matcher = REACH.matcher(observed);
            Matcher expectedMatcher = REACH.matcher(evidence.expected() == null ? "" : evidence.expected());
            if (!matcher.matches() || !expectedMatcher.matches()) return null;
            int reached = Integer.parseInt(matcher.group(1)), completed = Integer.parseInt(matcher.group(2));
            int expectedReached = Integer.parseInt(expectedMatcher.group(1));
            int expectedCompleted = Integer.parseInt(expectedMatcher.group(2));
            if (completed > 10 || reached > completed || expectedCompleted > 10 || expectedReached > expectedCompleted) return null;
            return new SafeEvidence("Observed completed reachability", reached + " of " + completed + " completed attempts reached",
                    ignored -> "Expected completed reachability: " + expectedReached + " of " + expectedCompleted
                            + " completed attempts reached");
        }
        if ("traceroute.path_status".equals(signal)) {
            Matcher matcher = PATH_COUNT.matcher(observed);
            if (!matcher.matches() || Integer.parseInt(matcher.group(1)) > 10) return null;
            int count = Integer.parseInt(matcher.group(1));
            return new SafeEvidence("Observed producer-classified path status", count + " completed attempt"
                    + (count == 1 ? " contained" : "s contained") + " a producer-classified degraded segment", ignored -> null);
        }
        if ("traceroute.path_signatures".equals(signal)) {
            Matcher matcher = PATH_SIGNATURES.matcher(observed);
            if (!matcher.matches() || Integer.parseInt(matcher.group(1)) > 10) return null;
            return new SafeEvidence("Observed path-signature count", matcher.group(1) + " reached completed attempts had multiple path signatures", ignored -> null);
        }
        if ("traceroute.execution_failures".equals(signal)) {
            Matcher matcher = EXECUTION_CLASSES.matcher(observed);
            if (!matcher.matches()) return null;
            int total = Integer.parseInt(matcher.group(1)), timed = Integer.parseInt(matcher.group(2));
            int cancelled = Integer.parseInt(matcher.group(3)), command = Integer.parseInt(matcher.group(4));
            Integer expected = executionFailureExpected(evidence.expected());
            if (total > 10 || total != timed + cancelled + command || expected == null) return null;
            return new SafeEvidence("Observed execution-failure counts",
                    total + " total; " + timed + " timed out; " + cancelled + " cancelled; " + command + " command errors",
                    ignored -> "Expected count: " + expected + " execution failures");
        }
        return null;
    }

    private static String coverage(String fixed, Set<Integer> resultIndexes, Report.Coverage coverage) {
        return fixed + coverageIssues(resultIndexes, coverage);
    }

    private static String coverageIssues(Set<Integer> resultIndexes, Report.Coverage coverage) {
        if (resultIndexes.isEmpty()) return "";
        List<String> labels = new ArrayList<>();
        appendCoverage(labels, resultIndexes, coverage.providerFailures());
        appendCoverage(labels, resultIndexes, coverage.limitations());
        return labels.isEmpty() ? "" : "\n" + String.join("\n", labels);
    }

    private static void appendCoverage(List<String> labels, Set<Integer> indexes, List<Report.CoverageIssue> issues) {
        for (Report.CoverageIssue issue : issues) {
            if (labels.size() >= 8) return;
            if (!indexes.contains(issue.resultIndex())) continue;
            labels.add(coverageCodeLabel(issue.code()) + ": " + coverageSignalLabel(issue.signal()));
        }
    }

    private static String coverageCodeLabel(Report.CoverageCode code) {
        return switch (code) {
            case MISSING_DETAILS -> "Required observation was not available";
            case MALFORMED_DETAILS -> "Observation did not match the bounded contract";
            case UNSUPPORTED_DETAILS -> "Observation is outside this analysis version's supported scope";
        };
    }

    public static String coverageSignalLabel(String signal) {
        String label = COVERAGE_REGISTRY.get(signal);
        if (label == null) throw new IllegalArgumentException("unknown coverage presentation signal");
        return label;
    }

    private static String directness(Report.Confidence confidence, boolean linked) {
        if (!linked) return "No validated evidence directness was established.";
        return switch (confidence) {
            case DIRECT -> "Direct bounded observation.";
            case CORROBORATED -> "Corroborated bounded observations.";
            case LIMITED -> "Limited bounded observation; additional verification is required.";
        };
    }

    private static boolean isHealthyGreeting(Report.Result result) {
        if (result.status() != Report.Status.HEALTHY || !isServiceKind(result.kind())) return false;
        return "server_greeting".equals(result.details().get("verification_scope"));
    }

    private static boolean isServiceKind(CheckKind kind) {
        return switch (kind) {
            case IMAP, IMAPS, POP3, POP3S, SMTP, SMTPS, SSH, SUBMISSION -> true;
            default -> false;
        };
    }

    public static String kindLabel(CheckKind kind) {
        return switch (kind) {
            case DNS -> "DNS"; case TCP -> "TCP"; case HTTP -> "HTTP"; case HTTPS -> "HTTPS";
            case SSH -> "SSH"; case SMTP -> "SMTP"; case SUBMISSION -> "SUBMISSION"; case SMTPS -> "SMTPS";
            case IMAP -> "IMAP"; case IMAPS -> "IMAPS"; case POP3 -> "POP3"; case POP3S -> "POP3S";
            case TRACEROUTE -> "TRACEROUTE";
        };
    }

    private static String statusLabel(Report.Status status) {
        return switch (status) {
            case HEALTHY -> "Completed within observed scope";
            case DEGRADED -> "Degraded";
            case UNREACHABLE -> "Unreachable";
        };
    }

    private static String traceSignalLabel(String signal) {
        return switch (signal) {
            case "traceroute.attempts_timed_out" -> "Observed timed-out count";
            case "traceroute.attempts_cancelled" -> "Observed cancelled count";
            case "traceroute.attempts_execution_failed" -> "Observed execution-failure count";
            default -> throw new IllegalArgumentException("unsupported internal trace signal");
        };
    }

    private static Integer boundedInteger(String value, int minimum, int maximum) {
        if (value == null || !INTEGER.matcher(value).matches()) return null;
        int parsed = Integer.parseInt(value);
        return parsed >= minimum && parsed <= maximum ? parsed : null;
    }

    private static Integer executionFailureExpected(String value) {
        if (value == null || !value.endsWith(" execution failures")) return null;
        return boundedInteger(value.substring(0, value.length() - " execution failures".length()), 0, 10);
    }

    private static String safeTimestamp(String value) {
        if (value == null || value.length() > 32 || !value.endsWith("Z")) return null;
        try {
            Instant parsed = Instant.parse(value);
            return parsed.toString().equals(value) ? value : null;
        } catch (DateTimeParseException ignored) { return null; }
    }

    private static Template requireTemplate(Report.FindingCode code) {
        Template template = REGISTRY.get(code);
        if (template == null) throw new IllegalStateException("closed finding registry is incomplete");
        return template;
    }

    private static EvidenceSpec requireEvidenceSpec(String signal) {
        EvidenceSpec spec = EVIDENCE_REGISTRY.get(signal);
        if (spec == null) throw new IllegalArgumentException("unknown evidence presentation signal");
        return spec;
    }

    private static Map<String, EvidenceSpec> buildEvidenceRegistry() {
        Map<String, EvidenceSpec> values = new HashMap<>();
        putEvidence(values, "error_code", "closed_error_code", "none");
        putEvidence(values, "http.status_code", "http_status", "http_status");
        putEvidence(values, "tls.certificate_expires_at", "utc_timestamp", "none");
        putEvidence(values, "traceroute.attempts_cancelled", "count", "count");
        putEvidence(values, "traceroute.attempts_execution_failed", "count", "count");
        putEvidence(values, "traceroute.attempts_reached", "completed_fraction", "completed_fraction");
        putEvidence(values, "traceroute.attempts_timed_out", "count", "count");
        putEvidence(values, "traceroute.execution_failures", "execution_failure_counts", "count");
        putEvidence(values, "traceroute.path_signatures", "path_count", "none");
        putEvidence(values, "traceroute.path_status", "path_count", "none");
        if (values.size() != 10) throw new IllegalStateException("closed evidence registry is incomplete");
        return Collections.unmodifiableMap(values);
    }

    private static void putEvidence(Map<String, EvidenceSpec> values, String signal,
                                    String observedShape, String expectedShape) {
        if (values.put(signal, new EvidenceSpec(observedShape, expectedShape)) != null)
            throw new IllegalStateException("duplicate evidence presentation signal");
    }

    private static Map<String, String> buildCoverageRegistry() {
        Map<String, String> values = new HashMap<>();
        putCoverage(values, "certificate_expires_at", "certificate expiry");
        putCoverage(values, "details", "checker details");
        putCoverage(values, "dns_answers", "DNS answer counts");
        putCoverage(values, "endpoint", "endpoint observation");
        putCoverage(values, "error_code", "result code");
        putCoverage(values, "geoip", "location enrichment");
        putCoverage(values, "geoip_enrichment", "location enrichment availability");
        putCoverage(values, "http_status", "HTTP status");
        putCoverage(values, "kind", "checker kind");
        putCoverage(values, "response_body", "bounded response sample");
        putCoverage(values, "result", "result observation");
        putCoverage(values, "service_verification_details", "server-greeting verification details");
        putCoverage(values, "service_verification_scope", "server-greeting verification scope");
        putCoverage(values, "status", "result status");
        putCoverage(values, "tls", "TLS verification");
        putCoverage(values, "tls_certificate", "TLS certificate");
        putCoverage(values, "tls_failure_details", "TLS verification details");
        putCoverage(values, "trace_attempts", "traceroute attempt counts");
        putCoverage(values, "trace_error", "traceroute error classification");
        putCoverage(values, "trace_paths", "bounded traceroute paths");
        putCoverage(values, "trace_topology", "bounded topology evidence");
        if (values.size() != 21) throw new IllegalStateException("closed coverage registry is incomplete");
        return Collections.unmodifiableMap(values);
    }

    private static void putCoverage(Map<String, String> values, String signal, String label) {
        if (values.put(signal, label) != null) throw new IllegalStateException("duplicate coverage presentation signal");
    }

    private static EnumMap<Report.FindingCode, Template> buildRegistry() {
        EnumMap<Report.FindingCode, Template> values = new EnumMap<>(Report.FindingCode.class);
        put(values, Report.FindingCode.CHECKER_CAPACITY_UNAVAILABLE, "Checker execution capacity was unavailable, so service behavior was not established.", "The checker is admitted and completes with a bounded observation.", "No endpoint-health conclusion can be drawn from an admission failure.", "Retry after existing bounded checker work has completed.", "error_code");
        put(values, Report.FindingCode.CHECKER_PANIC, "The checker stopped unexpectedly, so service behavior was not established.", "The checker completes and returns a bounded observation.", "A checker failure is not evidence of endpoint failure.", "Repeat the check; escalate to the runtime owner if the checker repeatedly stops.", "error_code");
        put(values, Report.FindingCode.DNS_RESOLUTION_FAILED, "DNS resolution was not established by this observation.", "The hostname resolves to at least one intended address.", "DNS failure does not establish endpoint or application behavior.", "Verify DNS resolution using an approved resolver and repeat the check.", "error_code");
        put(values, Report.FindingCode.ENDPOINT_CONNECT_FAILED, "An endpoint connection was not established.", "The intended endpoint accepts a connection on the configured port.", "Application and authentication behavior were not tested without a connection.", "Verify listener availability and approved network access, then repeat the check.", "error_code");
        put(values, Report.FindingCode.EXECUTION_CANCELLED, "The check was cancelled before a complete observation was available.", "The bounded check remains active through completion.", "Cancellation does not establish service health or failure.", "Repeat the check when it can remain active through completion.", "error_code", "traceroute.attempts_cancelled");
        put(values, Report.FindingCode.EXECUTION_TIMEOUT, "The check did not complete within its bounded deadline.", "The check completes before the configured deadline.", "A timeout does not identify the underlying network or service cause.", "Repeat once with the same bounded deadline and verify endpoint availability independently.", "error_code", "traceroute.attempts_timed_out");
        put(values, Report.FindingCode.HTTP_UNEXPECTED_STATUS, "The observed HTTP status differed from the configured expectation.", "The endpoint returns the configured expected HTTP status.", "Status alone does not establish response-body or end-to-end application behavior.", "Verify the configured expected status and endpoint route, then repeat the request.", "http.status_code", "error_code");
        put(values, Report.FindingCode.INVALID_TARGET, "The target was rejected before a network observation was made.", "The target has accepted syntax, scheme, hostname, and port.", "No network or service behavior was tested.", "Correct the target configuration and run the check again.", "error_code");
        put(values, Report.FindingCode.SERVICE_GREETING_UNVERIFIED, "A transport connection occurred, but the expected server-first greeting was not verified.", "The intended service returns its expected bounded server-first greeting without client input.", "Command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.", "Verify the listener and server-first greeting configuration, then repeat the check.", "error_code");
        put(values, Report.FindingCode.TARGET_POLICY_BLOCKED, "Deployment policy blocked the target before a network connection was attempted.", "The target is approved under the selected deployment policy.", "No network or service behavior was tested.", "Use an approved target or an explicitly authorized trusted-local deployment.", "error_code");
        put(values, Report.FindingCode.TLS_CERTIFICATE_EXPIRED, "The certificate was outside its validity period at verification time.", "The endpoint presents a certificate valid at verification time.", "Application behavior beyond certificate verification was not established.", "Renew and deploy the intended certificate chain, then repeat the check.", "error_code", "tls.certificate_expires_at");
        put(values, Report.FindingCode.TLS_CERTIFICATE_EXPIRING, "The observed certificate expiry was within the bounded renewal window.", "The certificate remains valid beyond the renewal window.", "Renewal and deployment state were not independently verified.", "Confirm renewal and deployment before expiry, then repeat the check.", "tls.certificate_expires_at");
        put(values, Report.FindingCode.TLS_CERTIFICATE_NOT_YET_VALID, "The certificate validity period had not started at verification time.", "The endpoint presents a certificate valid at verification time.", "Application behavior beyond certificate verification was not established.", "Verify certificate deployment and system clocks, then repeat the check.", "error_code");
        put(values, Report.FindingCode.TLS_DOWNGRADE, "TLS was not preserved for the observed HTTP path.", "Every redirect and final response preserve verified TLS.", "Authentication and end-to-end application behavior were not tested.", "Inspect redirects and the final endpoint, then repeat the TLS check.", "error_code");
        put(values, Report.FindingCode.TLS_HANDSHAKE_FAILED, "The transport connected, but the TLS handshake did not complete.", "The TLS handshake completes with the intended endpoint.", "Certificate identity, authentication, and application behavior were not established.", "Verify certificate chain, server name, protocol versions, and cipher compatibility.", "error_code");
        put(values, Report.FindingCode.TLS_HOSTNAME_MISMATCH, "The certificate did not verify for the requested server name.", "Certificate verification succeeds for the requested server name.", "Application behavior beyond server-identity verification was not established.", "Deploy the intended certificate and verify endpoint routing, then repeat the check.", "error_code");
        put(values, Report.FindingCode.TLS_UNTRUSTED, "The certificate chain did not verify to an approved trust anchor.", "The intended certificate chain verifies to an approved trust anchor.", "Application behavior beyond certificate trust verification was not established.", "Deploy the complete intended chain and verify its trust anchor.", "error_code");
        put(values, Report.FindingCode.TRACEROUTE_EXECUTION_FAILED, "One or more traceroute command attempts did not produce a completed route observation.", "Every command attempt completes and produces bounded route evidence.", "Command failure does not establish destination reachability.", "Verify the traceroute runtime and permissions, then repeat the bounded trace.", "traceroute.attempts_execution_failed", "traceroute.execution_failures");
        put(values, Report.FindingCode.TRACEROUTE_PARTIAL_REACHABILITY, "Completed traceroute attempts had differing destination-reachability outcomes.", "Completed attempts show consistent reachability or a reproducible divergence.", "The observation does not identify the cause of route variation.", "Compare reached and unreached completed routes from the same vantage point.", "traceroute.attempts_reached");
        put(values, Report.FindingCode.TRACEROUTE_PATH_DEGRADED, "The producer classified a segment in completed route evidence as degraded.", "Repeated completed route evidence establishes whether that segment remains degraded.", "The observation does not establish a loss rate or root cause.", "Repeat the trace and compare the same segment from an approved vantage point.", "traceroute.path_status");
        put(values, Report.FindingCode.TRACEROUTE_PATH_UNSTABLE, "Successful completed attempts contained multiple bounded path signatures.", "The path stabilizes or the same divergence is reproduced.", "Path variation alone does not establish failure or root cause.", "Repeat the trace and compare the first divergence in completed paths.", "traceroute.path_signatures");
        put(values, Report.FindingCode.TRACEROUTE_UNAVAILABLE, "Traceroute capability was unavailable, so no route observation was established.", "A functional traceroute capability is available before diagnostics begin.", "No route or destination-health conclusion can be drawn without a traceroute observation.", "Restore the supported traceroute capability, then repeat the bounded check.", "error_code");
        put(values, Report.FindingCode.TRACEROUTE_UNREACHABLE, "No completed traceroute attempt observed the destination as reached.", "At least one completed trace reaches the destination or identifies a stable last responsive hop.", "This does not establish why the destination was not reached.", "Repeat from the same vantage point and compare bounded route evidence.", "traceroute.attempts_reached");
        if (values.size() != Report.FindingCode.values().length) throw new IllegalStateException("closed finding registry is incomplete");
        return values;
    }

    private static void put(EnumMap<Report.FindingCode, Template> values, Report.FindingCode code,
                            String cause, String expectation, String limitation, String action, String... signals) {
        if (values.put(code, new Template(cause, expectation, limitation, actionKeyFor(code), action, Set.of(signals))) != null)
            throw new IllegalStateException("duplicate finding presentation");
    }

    private static String actionKeyFor(Report.FindingCode code) {
        return switch (code) {
            case CHECKER_CAPACITY_UNAVAILABLE -> "action.checker_capacity_unavailable";
            case CHECKER_PANIC -> "action.checker_panic";
            case DNS_RESOLUTION_FAILED -> "action.dns_resolution_failed";
            case ENDPOINT_CONNECT_FAILED -> "action.endpoint_connect_failed";
            case EXECUTION_CANCELLED -> "action.execution_cancelled";
            case EXECUTION_TIMEOUT -> "action.execution_timeout";
            case HTTP_UNEXPECTED_STATUS -> "action.http_unexpected_status";
            case INVALID_TARGET -> "action.invalid_target";
            case SERVICE_GREETING_UNVERIFIED -> "action.service_greeting_unverified";
            case TARGET_POLICY_BLOCKED -> "action.target_policy_blocked";
            case TLS_CERTIFICATE_EXPIRED -> "action.tls_certificate_expired";
            case TLS_CERTIFICATE_EXPIRING -> "action.tls_certificate_expiring";
            case TLS_CERTIFICATE_NOT_YET_VALID -> "action.tls_certificate_not_yet_valid";
            case TLS_DOWNGRADE -> "action.tls_downgrade";
            case TLS_HANDSHAKE_FAILED -> "action.tls_handshake_failed";
            case TLS_HOSTNAME_MISMATCH -> "action.tls_hostname_mismatch";
            case TLS_UNTRUSTED -> "action.tls_untrusted";
            case TRACEROUTE_EXECUTION_FAILED -> "action.traceroute_execution_failed";
            case TRACEROUTE_PARTIAL_REACHABILITY -> "action.traceroute_partial_reachability";
            case TRACEROUTE_PATH_DEGRADED -> "action.traceroute_path_degraded";
            case TRACEROUTE_PATH_UNSTABLE -> "action.traceroute_path_unstable";
            case TRACEROUTE_UNAVAILABLE -> "action.traceroute_unavailable";
            case TRACEROUTE_UNREACHABLE -> "action.traceroute_unreachable";
        };
    }
}
