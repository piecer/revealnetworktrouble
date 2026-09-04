package com.checknetwork.app;

import static org.junit.Assert.*;

import com.checknetwork.app.core.AnalysisPresentationRegistry;
import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportMarkdownExporter;
import com.checknetwork.app.core.ReportParser;
import java.util.Arrays;
import java.util.EnumSet;
import java.util.List;
import java.util.Set;
import java.util.stream.Collectors;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class AnalysisPresentationContractTest {
    private static final List<String> SEMANTIC_KEYS = List.of(
            "cause", "supporting_evidence", "expectation", "evidence_directness",
            "coverage_limitation", "next_action");
    private static final String POISON = "POISON-private.example-Bearer-secret-192.0.2.77";
    private static final String GREETING_SCOPE = "Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.";

    @Test public void registryIsExhaustiveForAllTwentyThreeClosedFindingCodesAndSixStableKeys() throws Exception {
        assertEquals(23, Report.FindingCode.values().length);
        assertEquals(EnumSet.allOf(Report.FindingCode.class), AnalysisPresentationRegistry.registeredCodes());
        assertEquals(SEMANTIC_KEYS, AnalysisPresentationRegistry.semanticKeys());
        for (Report.FindingCode code : Report.FindingCode.values()) {
            assertEquals("finding." + code.name().toLowerCase(java.util.Locale.ROOT),
                    AnalysisPresentationRegistry.presentationKey(code));
        }
    }

    @Test public void everyFindingCodeRendersAllSemanticSectionsFromLocalTextOnly() throws Exception {
        for (Report.FindingCode code : Report.FindingCode.values()) {
            Report report = reportFor(code, safeSignal(code), safeObserved(code), "PRODUCER-EXPECTED-" + POISON,
                    true, true, new JSONArray());
            AnalysisPresentation presentation = AnalysisPresentation.from(report);
            Set<String> keys = presentation.blocks().stream().filter(block -> !block.folded())
                    .map(AnalysisPresentation.Block::key).collect(Collectors.toSet());
            assertTrue(code + " screen keys=" + keys, keys.containsAll(SEMANTIC_KEYS));
            String safeScreen = presentation.blocks().stream().filter(block -> !block.folded())
                    .map(Object::toString).collect(Collectors.joining("\n"));
            String markdown = ReportMarkdownExporter.export(report);
            for (String key : SEMANTIC_KEYS) {
                assertTrue(code + " markdown missing " + key, markdown.contains("semantic-key: " + key));
            }
            assertTrue(safeScreen.contains(AnalysisPresentationRegistry.presentationKey(code)));
            assertTrue(markdown.contains(AnalysisPresentationRegistry.presentationKey(code)));
            assertNoPoison(safeScreen);
            assertNoPoison(markdown);
        }
    }

    @Test public void missingEvidenceActionAndCoverageStillHaveFixedExplicitSections() throws Exception {
        Report report = reportFor(Report.FindingCode.DNS_RESOLUTION_FAILED, "error_code", "connection_failed", POISON,
                false, false, new JSONArray());
        String screen = safeScreen(report);
        String markdown = ReportMarkdownExporter.export(report);
        for (String rendered : List.of(screen, markdown)) {
            assertTrue(rendered.contains("No validated supporting evidence was linked."));
            assertTrue(rendered.contains("Verify DNS resolution using an approved resolver and repeat the check."));
            assertTrue(rendered.contains("DNS resolution was not established by this observation."));
            assertNoPoison(rendered);
        }
    }

    @Test public void coverageCodesAndSignalsUseFixedCategoriesAndNeverReasons() throws Exception {
        JSONArray issues = new JSONArray();
        String[] codes = {"missing_details", "malformed_details", "unsupported_details"};
        String[] signals = {"details", "tls_failure_details", "service_verification_scope"};
        for (int index = 0; index < codes.length; index++) {
            issues.put(new JSONObject().put("code", codes[index]).put("result_index", 0).put("kind", "dns")
                    .put("signal", signals[index]).put("reason", "REASON-" + index + "-" + POISON));
        }
        Report report = reportFor(Report.FindingCode.DNS_RESOLUTION_FAILED, "error_code", "connection_failed", POISON,
                true, true, issues);
        for (String rendered : List.of(safeScreen(report), ReportMarkdownExporter.export(report))) {
            assertTrue(rendered.contains("Required observation was not available"));
            assertTrue(rendered.contains("Observation did not match the bounded contract"));
            assertTrue(rendered.contains("Observation is outside this analysis version's supported scope"));
            assertTrue(rendered.contains("checker details"));
            assertTrue(rendered.contains("TLS verification details"));
            assertTrue(rendered.contains("server-greeting verification scope"));
            assertNoPoison(rendered);
        }
    }

    @Test public void onlyBoundedValidatedEvidenceValuesAreShown() throws Exception {
        Report valid = reportFor(Report.FindingCode.HTTP_UNEXPECTED_STATUS, "http.status_code", "503", "200",
                true, true, new JSONArray());
        for (String rendered : List.of(safeScreen(valid), ReportMarkdownExporter.export(valid))) {
            assertTrue(rendered.contains("Observed HTTP status: 503"));
            assertTrue(rendered.contains("Expected HTTP status: 200"));
        }
        Report hostile = reportFor(Report.FindingCode.HTTP_UNEXPECTED_STATUS, "SIGNAL-" + POISON, POISON, POISON,
                true, true, new JSONArray());
        assertThrows(IllegalArgumentException.class, () -> safeScreen(hostile));
        assertThrows(IllegalArgumentException.class, () -> ReportMarkdownExporter.export(hostile));
    }

    @Test public void validatedAttemptAndCountAreRenderedWithoutAddressOrRawProse() throws Exception {
        JSONObject details = new JSONObject().put("attempts_total", 2).put("attempts_reached", 1)
                .put("attempts_failed", 1).put("attempts_unreached", 1).put("attempts_execution_failed", 0)
                .put("attempts_timed_out", 0).put("attempts_cancelled", 0);
        JSONObject result = new JSONObject().put("kind", "traceroute").put("address", POISON)
                .put("status", "degraded").put("latency_ms", 8).put("started_at", "2026-09-02T00:00:00Z")
                .put("message", POISON).put("details", details);
        JSONObject finding = new JSONObject().put("id", "f-1").put("code", "traceroute_partial_reachability")
                .put("severity", "warning").put("category", "routing").put("title", POISON).put("summary", POISON)
                .put("confidence", "direct").put("evidence_ids", new JSONArray().put("e-1"))
                .put("action_ids", new JSONArray());
        JSONObject evidence = new JSONObject().put("id", "e-1").put("result_index", 0).put("kind", "traceroute")
                .put("attempt", 1).put("address", POISON).put("signal", "traceroute.attempts_reached")
                .put("observed", "1/2 completed attempts reached").put("expected", "2/2 completed attempts reached").put("provenance", "details");
        JSONObject analysis = new JSONObject().put("verdict", "attention").put("findings", new JSONArray().put(finding))
                .put("evidence", new JSONArray().put(evidence)).put("actions", new JSONArray())
                .put("coverage", new JSONObject().put("available", new JSONArray()).put("missing", new JSONArray())
                        .put("provider_failures", new JSONArray()).put("limitations", new JSONArray()));
        Report report = ReportParser.parse(new JSONObject().put("id", POISON).put("status", "degraded")
                .put("started_at", "2026-09-02T00:00:00Z").put("duration_ms", 8)
                .put("summary", new JSONObject().put("total", 1).put("passed", 0).put("failed", 1))
                .put("results", new JSONArray().put(result)).put("analysis", analysis).toString());
        for (String rendered : List.of(safeScreen(report), ReportMarkdownExporter.export(report))) {
            assertTrue(rendered.contains("1 of 2 completed attempts reached (attempt 1)"));
            assertNoPoison(rendered);
        }
    }

    @Test public void allEightHealthyGreetingKindsHaveExactLimitedScopeWithoutHealthClaims() throws Exception {
        for (String kind : List.of("imap", "imaps", "pop3", "pop3s", "smtp", "smtps", "ssh", "submission")) {
            Report report = healthyGreetingReport(kind);
            for (String rendered : List.of(safeScreen(report), ReportMarkdownExporter.export(report))) {
                assertTrue(kind, rendered.contains(GREETING_SCOPE));
                assertTrue(rendered.contains(kind.toUpperCase(java.util.Locale.ROOT)));
                assertFalse(rendered.contains("PASS"));
                assertFalse(rendered.contains("Healthy"));
                assertFalse(rendered.contains("normal response"));
                assertFalse(rendered.contains("response is normal"));
                assertNoPoison(rendered);
                for (String key : SEMANTIC_KEYS) assertTrue(rendered.contains(key));
            }
        }
    }

    @Test public void rawBlockRemainsLastFoldedAndBoundedWhileSafeBlocksStayPrivate() throws Exception {
        Report report = reportFor(Report.FindingCode.TLS_DOWNGRADE, "error_code", "tls_downgrade", POISON,
                true, true, new JSONArray());
        List<AnalysisPresentation.Block> blocks = AnalysisPresentation.from(report).blocks();
        AnalysisPresentation.Block raw = blocks.get(blocks.size() - 1);
        assertEquals("raw_results", raw.key());
        assertTrue(raw.folded());
        assertTrue(raw.body().contains(POISON));
        assertTrue(raw.body().length() <= AnalysisPresentation.MAX_RAW_RESULT_CHARS);
        assertTrue(safeScreen(report).length() <= AnalysisPresentation.MAX_SAFE_PRESENTATION_CHARS);
        assertTrue(ReportMarkdownExporter.export(report).length() <= ReportMarkdownExporter.MAX_EXPORT_CHARS);
    }

    private static String safeObserved(Report.FindingCode code) {
        return switch (code) {
            case CHECKER_CAPACITY_UNAVAILABLE -> "checker_capacity_unavailable";
            case CHECKER_PANIC -> "checker_panic";
            case DNS_RESOLUTION_FAILED -> "connection_failed";
            case ENDPOINT_CONNECT_FAILED -> "connection_failed";
            case EXECUTION_CANCELLED -> "cancelled";
            case EXECUTION_TIMEOUT -> "timeout";
            case HTTP_UNEXPECTED_STATUS -> "unexpected_status";
            case INVALID_TARGET -> "invalid_url";
            case SERVICE_GREETING_UNVERIFIED -> "service_greeting_unverified";
            case TARGET_POLICY_BLOCKED -> "network_policy_blocked";
            case TLS_CERTIFICATE_EXPIRED -> "tls_certificate_expired";
            case TLS_CERTIFICATE_EXPIRING -> "2026-09-20T00:00:00Z";
            case TLS_CERTIFICATE_NOT_YET_VALID -> "tls_certificate_not_yet_valid";
            case TLS_DOWNGRADE -> "tls_downgrade";
            case TLS_HANDSHAKE_FAILED -> "tls_handshake_failed";
            case TLS_HOSTNAME_MISMATCH -> "tls_hostname_mismatch";
            case TLS_UNTRUSTED -> "tls_untrusted";
            case TRACEROUTE_EXECUTION_FAILED -> "1";
            case TRACEROUTE_PARTIAL_REACHABILITY -> "1/2 completed attempts reached";
            case TRACEROUTE_PATH_DEGRADED -> "1 completed path evidence record";
            case TRACEROUTE_PATH_UNSTABLE -> "multiple successful completed path signatures among 2 reached completed attempts";
            case TRACEROUTE_UNAVAILABLE -> "traceroute_unavailable";
            case TRACEROUTE_UNREACHABLE -> "0/2 completed attempts reached";
        };
    }

    private static String safeSignal(Report.FindingCode code) {
        return switch (code) {
            case HTTP_UNEXPECTED_STATUS -> "http.status_code";
            case TLS_CERTIFICATE_EXPIRING -> "tls.certificate_expires_at";
            case TRACEROUTE_EXECUTION_FAILED -> "traceroute.attempts_execution_failed";
            case TRACEROUTE_PARTIAL_REACHABILITY, TRACEROUTE_UNREACHABLE -> "traceroute.attempts_reached";
            case TRACEROUTE_PATH_DEGRADED -> "traceroute.path_status";
            case TRACEROUTE_PATH_UNSTABLE -> "traceroute.path_signatures";
            default -> "error_code";
        };
    }

    private static Report reportFor(Report.FindingCode code, String signal, String observed, String expected,
                                    boolean withEvidence, boolean withAction, JSONArray limitations) throws Exception {
        JSONObject result = new JSONObject().put("kind", "dns").put("address", "RESULT-ADDRESS-" + POISON)
                .put("status", "unreachable").put("latency_ms", 7).put("started_at", "2026-09-02T00:00:00Z")
                .put("error_code", "connection_failed").put("message", "RESULT-MESSAGE-" + POISON);
        JSONObject finding = new JSONObject().put("id", "FINDING-ID-" + POISON)
                .put("code", code.name().toLowerCase(java.util.Locale.ROOT)).put("severity", "warning")
                .put("category", "execution").put("title", "TITLE-" + POISON).put("summary", "SUMMARY-" + POISON)
                .put("confidence", "direct").put("evidence_ids", withEvidence ? new JSONArray().put("e-1") : new JSONArray())
                .put("action_ids", withAction ? new JSONArray().put("a-1") : new JSONArray());
        JSONArray evidence = withEvidence ? new JSONArray().put(new JSONObject().put("id", "e-1")
                .put("result_index", 0).put("kind", "dns").put("address", "EVIDENCE-ADDRESS-" + POISON)
                .put("signal", signal).put("observed", observed).put("expected", expected).put("provenance", "result")) : new JSONArray();
        JSONArray actions = withAction ? new JSONArray().put(new JSONObject().put("id", "a-1")
                .put("title", "ACTION-TITLE-" + POISON).put("step", "ACTION-STEP-" + POISON)
                .put("expected_result", "ACTION-EXPECTED-" + POISON).put("escalation_condition", "ACTION-ESCALATE-" + POISON)) : new JSONArray();
        JSONObject analysis = new JSONObject().put("verdict", "attention").put("findings", new JSONArray().put(finding))
                .put("evidence", evidence).put("actions", actions)
                .put("coverage", new JSONObject().put("available", new JSONArray().put("AVAILABLE-" + POISON))
                        .put("missing", new JSONArray().put("MISSING-" + POISON)).put("provider_failures", new JSONArray())
                        .put("limitations", limitations));
        JSONObject root = new JSONObject().put("id", "REPORT-ID-" + POISON).put("status", "unreachable")
                .put("started_at", "2026-09-02T00:00:00Z").put("duration_ms", 12)
                .put("summary", new JSONObject().put("total", 1).put("passed", 0).put("failed", 1))
                .put("results", new JSONArray().put(result)).put("analysis", analysis);
        return ReportParser.parse(root.toString());
    }

    private static Report healthyGreetingReport(String kind) throws Exception {
        JSONObject details = new JSONObject().put("verification_scope", "server_greeting");
        if (Set.of("imaps", "pop3s", "smtps").contains(kind)) {
            details.put("certificate_expires_at", "2027-01-01T00:00:00Z")
                    .put("certificate_subject", "PROVIDER-" + POISON)
                    .put("cipher_suite", "TLS_AES_128_GCM_SHA256")
                    .put("tls_version", "TLS 1.3");
        }
        JSONObject result = new JSONObject().put("kind", kind).put("address", "SERVICE-" + POISON)
                .put("status", "healthy").put("latency_ms", 4).put("started_at", "2026-09-02T00:00:00Z")
                .put("message", "GREETING-" + POISON)
                .put("details", details);
        JSONObject issue = new JSONObject().put("code", "unsupported_details").put("result_index", 0).put("kind", kind)
                .put("signal", "service_verification_scope").put("reason", "REASON-" + POISON);
        JSONObject analysis = new JSONObject().put("verdict", "inconclusive").put("findings", new JSONArray())
                .put("evidence", new JSONArray()).put("actions", new JSONArray())
                .put("coverage", new JSONObject().put("available", new JSONArray().put("AVAILABLE-" + POISON))
                        .put("missing", new JSONArray()).put("provider_failures", new JSONArray())
                        .put("limitations", new JSONArray().put(issue)));
        JSONObject root = new JSONObject().put("id", "REPORT-ID-" + POISON).put("status", "healthy")
                .put("started_at", "2026-09-02T00:00:00Z").put("duration_ms", 4)
                .put("summary", new JSONObject().put("total", 1).put("passed", 1).put("failed", 0))
                .put("results", new JSONArray().put(result)).put("analysis", analysis);
        return ReportParser.parse(root.toString());
    }

    private static String safeScreen(Report report) {
        return AnalysisPresentation.from(report).blocks().stream().filter(block -> !block.folded())
                .map(Object::toString).collect(Collectors.joining("\n"));
    }

    private static void assertNoPoison(String rendered) {
        for (String fragment : Arrays.asList(POISON, "TITLE-", "SUMMARY-", "ACTION-", "REASON-", "AVAILABLE-", "MISSING-", "PROVIDER-", "REPORT-ID-")) {
            assertFalse("leaked " + fragment + " in:\n" + rendered, rendered.contains(fragment));
        }
    }
}
