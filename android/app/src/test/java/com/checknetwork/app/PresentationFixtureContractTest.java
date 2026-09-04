package com.checknetwork.app;

import static org.junit.Assert.*;

import com.checknetwork.app.core.AnalysisPresentationRegistry;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportMarkdownExporter;
import com.checknetwork.app.core.ReportParser;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.HashSet;

import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.stream.Collectors;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class PresentationFixtureContractTest {
    private static final String FIXTURE_SHA256 = "d4268ebb2208ccc666bf79016824a5656ff1683b6f5dfb8fda43c6e6a5749607";
    private static final String GREETING_SCOPE = AnalysisPresentationRegistry.GREETING_SCOPE;

    @Test public void exactProducerFixtureClosesEveryLocalRegistryAndEveryScenarioRenders() throws Exception {
        byte[] fixtureBytes = Files.readAllBytes(fixturePath());
        assertEquals(FIXTURE_SHA256, hex(MessageDigest.getInstance("SHA-256").digest(fixtureBytes)));
        assertEquals('\n', fixtureBytes[fixtureBytes.length - 1]);
        JSONObject fixture = new JSONObject(new String(fixtureBytes, StandardCharsets.UTF_8));
        assertEquals("presentation-contract-v1", fixture.getString("schema"));

        assertEquals(strings(fixture.getJSONArray("semantic_keys")), AnalysisPresentationRegistry.semanticKeys());
        assertEquals(21, fixture.getJSONArray("coverage_signals").length());
        assertEquals(new HashSet<>(strings(fixture.getJSONArray("coverage_signals"))),
                AnalysisPresentationRegistry.registeredCoverageSignals());

        JSONArray evidenceSignals = fixture.getJSONArray("evidence_signals");
        assertEquals(10, evidenceSignals.length());
        Set<String> expectedEvidenceSignals = new HashSet<>();
        for (int index = 0; index < evidenceSignals.length(); index++) {
            JSONObject evidence = evidenceSignals.getJSONObject(index);
            String signal = evidence.getString("signal");
            expectedEvidenceSignals.add(signal);
            assertEquals(evidence.getString("observed_shape"), AnalysisPresentationRegistry.evidenceObservedShape(signal));
            assertEquals(evidence.getString("expected_shape"), AnalysisPresentationRegistry.evidenceExpectedShape(signal));
        }
        assertEquals(expectedEvidenceSignals, AnalysisPresentationRegistry.registeredEvidenceSignals());

        JSONArray relationships = fixture.getJSONArray("action_relationships");
        assertEquals(23, relationships.length());
        Set<String> expectedActions = new HashSet<>();
        for (int index = 0; index < relationships.length(); index++) {
            JSONObject relationship = relationships.getJSONObject(index);
            String findingKey = relationship.getString("finding_presentation_key");
            String actionKey = relationship.getString("key");
            Report.FindingCode code = Report.FindingCode.valueOf(
                    findingKey.substring("finding.".length()).toUpperCase(Locale.ROOT));
            assertEquals(findingKey, AnalysisPresentationRegistry.presentationKey(code));
            assertEquals(actionKey, AnalysisPresentationRegistry.actionKey(code));
            expectedActions.add(actionKey);
        }
        assertEquals(expectedActions, AnalysisPresentationRegistry.registeredActionKeys());

        JSONArray scenarios = fixture.getJSONArray("scenarios");
        assertEquals(33, scenarios.length());
        Set<String> observedActions = new HashSet<>();
        Set<String> observedEvidence = new HashSet<>();
        Set<String> observedFindingKeys = new HashSet<>();
        for (int scenarioIndex = 0; scenarioIndex < scenarios.length(); scenarioIndex++) {
            JSONObject scenario = scenarios.getJSONObject(scenarioIndex);
            JSONObject reportFixture = new JSONObject(scenario.getJSONObject("report").toString());
            satisfyRequiredHealthyGreetingDetails(reportFixture);
            Report report = ReportParser.parse(reportFixture.toString());
            String screen = safeScreen(report);
            String markdown = ReportMarkdownExporter.export(report);
            assertFalse(scenario.getString("name"), screen.isEmpty());
            assertFalse(scenario.getString("name"), markdown.isEmpty());
            assertFalse(screen.contains("default bounded observation"));
            assertFalse(markdown.contains("default bounded observation"));
            assertFalse(screen.contains("normal response"));
            assertFalse(markdown.contains("normal response"));
            assertFalse(screen.contains("PASS"));
            assertFalse(markdown.contains("PASS"));

            JSONArray findings = scenario.getJSONArray("findings");
            for (int findingIndex = 0; findingIndex < findings.length(); findingIndex++) {
                JSONObject finding = findings.getJSONObject(findingIndex);
                String findingKey = finding.getString("presentation_key");
                String actionKey = finding.getString("action_relationship");
                observedFindingKeys.add(findingKey);
                observedActions.add(actionKey);
                assertTrue(findingKey, screen.contains(findingKey));
                assertTrue(findingKey, markdown.contains(findingKey));
                assertTrue(actionKey, screen.contains(actionKey));
                assertTrue(actionKey, markdown.contains(actionKey));
                for (String key : AnalysisPresentationRegistry.semanticKeys()) {
                    assertTrue(scenario.getString("name") + " screen " + key, screen.contains(key));
                    assertTrue(scenario.getString("name") + " markdown " + key,
                            markdown.contains("semantic-key: " + key));
                }
                JSONArray evidence = finding.getJSONArray("evidence");
                for (int evidenceIndex = 0; evidenceIndex < evidence.length(); evidenceIndex++) {
                    observedEvidence.add(evidence.getJSONObject(evidenceIndex).getString("signal"));
                }
            }
            if (findings.length() > 0) {
                assertFalse(scenario.getString("name"), screen.contains("No validated supporting evidence was linked."));
                assertFalse(scenario.getString("name"), markdown.contains("No validated supporting evidence was linked."));
            }
        }
        assertEquals(23, observedFindingKeys.size());
        assertEquals(expectedActions, observedActions);
        assertEquals(expectedEvidenceSignals, observedEvidence);
    }

    @Test public void nilTracerouteCapabilityReportIsInconclusiveAndUsesOnlyFixedLocalPresentation() throws Exception {
        JSONObject reportJson = scenario(fixture(), "finding.traceroute_unavailable").getJSONObject("report");
        String poison = "RAW-PATH-PROBE-ERROR-PROSE-CANARY";
        reportJson.getJSONArray("results").getJSONObject(0)
                .put("address", poison).put("message", poison);
        JSONObject analysis = reportJson.getJSONObject("analysis");
        analysis.getJSONArray("findings").getJSONObject(0)
                .put("title", poison).put("summary", poison);
        analysis.getJSONArray("evidence").getJSONObject(0)
                .put("address", poison).put("expected", poison);
        analysis.getJSONArray("actions").getJSONObject(0)
                .put("title", poison).put("step", poison)
                .put("expected_result", poison).put("escalation_condition", poison);

        Report report = ReportParser.parse(reportJson.toString());
        assertEquals(Report.Verdict.INCONCLUSIVE, report.analysis().orElseThrow().verdict());
        assertEquals(Report.FindingCode.TRACEROUTE_UNAVAILABLE,
                report.analysis().orElseThrow().findings().get(0).code());
        assertEquals("traceroute_unavailable", report.results().get(0).errorCode());
        assertTrue(report.results().get(0).details().isEmpty());

        for (String rendered : List.of(safeScreen(report), ReportMarkdownExporter.export(report))) {
            assertTrue(rendered.contains("Inconclusive within the observed scope"));
            assertTrue(rendered.contains("finding.traceroute_unavailable"));
            assertTrue(rendered.contains("action.traceroute_unavailable"));
            assertTrue(rendered.contains("Traceroute capability was unavailable, so no route observation was established."));
            assertTrue(rendered.contains("Observed result code: traceroute_unavailable"));
            assertTrue(rendered.contains("Restore the supported traceroute capability, then repeat the bounded check."));
            assertFalse(rendered.contains(poison));
            assertFalse(rendered.contains("Install or repair the supported traceroute executable"));
            assertFalse(rendered.contains("startup capability probe"));
        }
    }

    @Test public void allTwentyOneCoverageSignalsHaveFixedPrivateLabels() throws Exception {
        JSONObject fixture = fixture();
        JSONObject reportJson = findingScenario(fixture, "finding.dns_resolution_failed").getJSONObject("report");
        for (String signal : strings(fixture.getJSONArray("coverage_signals"))) {
            JSONObject oneReport = new JSONObject(reportJson.toString());
            JSONArray issues = new JSONArray().put(new JSONObject().put("code", "missing_details")
                    .put("result_index", 0).put("kind", "dns").put("signal", signal)
                    .put("reason", "PRIVATE-REASON-" + signal));
            oneReport.getJSONObject("analysis").getJSONObject("coverage").put("limitations", issues);
            Report report = ReportParser.parse(oneReport.toString());
            for (String rendered : List.of(safeScreen(report), ReportMarkdownExporter.export(report))) {
                String label = AnalysisPresentationRegistry.coverageSignalLabel(signal);
                assertFalse(signal, label.isBlank());
                assertTrue(signal + " -> " + label, rendered.contains(label));
                assertFalse(rendered.contains("PRIVATE-REASON-"));
                assertFalse(rendered.contains("default bounded observation"));
            }
        }
    }

    @Test public void unknownEvidenceAndCoverageSignalsFailClosedWithoutFallback() throws Exception {
        JSONObject fixture = fixture();
        JSONObject unknownEvidence = findingScenario(fixture, "finding.dns_resolution_failed").getJSONObject("report");
        unknownEvidence.getJSONObject("analysis").getJSONArray("evidence").getJSONObject(0)
                .put("signal", "unknown.presentation.signal");
        Report evidenceReport = ReportParser.parse(unknownEvidence.toString());
        assertThrows(IllegalArgumentException.class, () -> AnalysisPresentation.from(evidenceReport));
        assertThrows(IllegalArgumentException.class, () -> ReportMarkdownExporter.export(evidenceReport));

        JSONObject unlinkedEvidence = findingScenario(fixture, "finding.dns_resolution_failed").getJSONObject("report");
        unlinkedEvidence.getJSONObject("analysis").getJSONArray("findings").getJSONObject(0)
                .put("evidence_ids", new JSONArray());
        unlinkedEvidence.getJSONObject("analysis").getJSONArray("evidence").getJSONObject(0)
                .put("signal", "unknown.unlinked.presentation.signal");
        Report unlinkedEvidenceReport = ReportParser.parse(unlinkedEvidence.toString());
        assertThrows(IllegalArgumentException.class, () -> AnalysisPresentation.from(unlinkedEvidenceReport));
        assertThrows(IllegalArgumentException.class, () -> ReportMarkdownExporter.export(unlinkedEvidenceReport));

        JSONObject unknownCoverage = findingScenario(fixture, "finding.dns_resolution_failed").getJSONObject("report");
        unknownCoverage.getJSONObject("analysis").getJSONObject("coverage").getJSONArray("limitations")
                .put(new JSONObject().put("code", "missing_details").put("result_index", 0).put("kind", "dns")
                        .put("signal", "unknown.presentation.coverage").put("reason", "private"));
        Report coverageReport = ReportParser.parse(unknownCoverage.toString());
        assertThrows(IllegalArgumentException.class, () -> AnalysisPresentation.from(coverageReport));
        assertThrows(IllegalArgumentException.class, () -> ReportMarkdownExporter.export(coverageReport));
        assertThrows(IllegalArgumentException.class,
                () -> AnalysisPresentationRegistry.coverageSignalLabel("unknown.presentation.coverage"));
        assertThrows(IllegalArgumentException.class,
                () -> AnalysisPresentationRegistry.evidenceObservedShape("unknown.presentation.signal"));
    }

    @Test public void fixtureEvidenceUsesTypedObservedAndExpectedProjection() throws Exception {
        JSONArray scenarios = fixture().getJSONArray("scenarios");
        for (int scenarioIndex = 0; scenarioIndex < scenarios.length(); scenarioIndex++) {
            JSONObject scenario = scenarios.getJSONObject(scenarioIndex);
            if (!"finding".equals(scenario.getString("purpose"))) continue;
            JSONObject reportJson = scenario.getJSONObject("report");
            Report report = ReportParser.parse(reportJson.toString());
            String rendered = safeScreen(report) + "\n" + ReportMarkdownExporter.export(report);
            JSONArray evidence = reportJson.getJSONObject("analysis").getJSONArray("evidence");
            for (int index = 0; index < evidence.length(); index++) {
                JSONObject item = evidence.getJSONObject(index);
                assertTypedProjection(rendered, item.getString("signal"), item.getString("observed"),
                        item.optString("expected", null));
            }
        }
    }

    @Test public void fullReportStatusAndVerdictNeverInheritAGreetingClaim() throws Exception {
        JSONObject fixture = fixture();
        Report dns = ReportParser.parse(scenario(fixture, "control.dns_only_healthy").getJSONObject("report").toString());
        for (String rendered : List.of(safeScreen(dns), ReportMarkdownExporter.export(dns))) {
            assertTrue(rendered.contains("Completed within observed scope"));
            assertTrue(rendered.contains("No known finding in the checks that were performed"));
            assertFalse(rendered.contains(GREETING_SCOPE));
            assertFalse(rendered.contains("Healthy"));
            assertFalse(rendered.contains(" passed"));
        }

        Report mixed = ReportParser.parse(scenario(fixture, "control.mixed_greeting_healthy_dns_failed")
                .getJSONObject("report").toString());
        AnalysisPresentation presentation = AnalysisPresentation.from(mixed);
        AnalysisPresentation.Block context = presentation.blocks().get(0);
        assertEquals("report_context", context.key());
        assertTrue(context.body().contains("Status: Degraded"));
        assertTrue(context.body().contains("Verdict: Attention needed within the observed scope"));
        assertFalse(context.body().contains(GREETING_SCOPE));
        String markdown = ReportMarkdownExporter.export(mixed);
        String markdownHeader = markdown.substring(0, markdown.indexOf("## Check outcomes"));
        assertTrue(markdownHeader.contains("Overall status: Degraded"));
        assertTrue(markdownHeader.contains("Verdict: Attention needed within the observed scope"));
        assertFalse(markdownHeader.contains(GREETING_SCOPE));
        assertTrue(safeScreen(mixed).contains(GREETING_SCOPE));
        assertTrue(markdown.contains(GREETING_SCOPE));
    }

    private static void assertTypedProjection(String rendered, String signal, String observed, String expected) {
        switch (signal) {
            case "error_code" -> {
                assertTrue(rendered.contains("Observed result code: " + observed));
            }
            case "http.status_code" -> {
                assertTrue(rendered.contains("Observed HTTP status: " + observed));
                assertTrue(rendered.contains("Expected HTTP status: " + expected));
            }
            case "tls.certificate_expires_at" -> assertTrue(rendered.contains("Observed certificate expiry: " + observed));
            case "traceroute.attempts_cancelled", "traceroute.attempts_execution_failed",
                    "traceroute.attempts_timed_out" -> {
                assertTrue(rendered.contains(observed + " attempts"));
                assertTrue(rendered.contains("Expected count: " + expected + " attempts"));
            }
            case "traceroute.attempts_reached" -> {
                String[] actualParts = observed.split("[/ ]", 3);
                String[] expectedParts = expected.split("[/ ]", 3);
                assertTrue(rendered.contains(actualParts[0] + " of " + actualParts[1] + " completed attempts reached"));
                assertTrue(rendered.contains("Expected completed reachability: " + expectedParts[0] + " of "
                        + expectedParts[1] + " completed attempts reached"));
            }
            case "traceroute.execution_failures" -> {
                assertTrue(rendered.contains("2 total; 1 timed out; 1 cancelled; 0 command errors"));
                assertTrue(rendered.contains("Expected count: 0 execution failures"));
            }
            case "traceroute.path_signatures" ->
                    assertTrue(rendered.contains("2 reached completed attempts had multiple path signatures"));
            case "traceroute.path_status" ->
                    assertTrue(rendered.contains("1 completed attempt contained a producer-classified degraded segment"));
            default -> fail("fixture introduced untested evidence signal " + signal);
        }
    }

    private static void satisfyRequiredHealthyGreetingDetails(JSONObject report) throws Exception {
        JSONArray results = report.getJSONArray("results");
        for (int index = 0; index < results.length(); index++) {
            JSONObject result = results.getJSONObject(index);
            if (!"healthy".equals(result.getString("status"))
                    || !Set.of("imaps", "pop3s", "smtps").contains(result.getString("kind"))) continue;
            JSONObject details = result.getJSONObject("details");
            assertEquals("server_greeting", details.getString("verification_scope"));
            assertTrue(details.has("certificate_expires_at"));
            assertTrue(details.has("tls_version"));
            details.put("certificate_subject", "fixture certificate subject")
                    .put("cipher_suite", "TLS_AES_128_GCM_SHA256");
        }
    }

    private static JSONObject fixture() throws Exception {
        return new JSONObject(new String(Files.readAllBytes(fixturePath()), StandardCharsets.UTF_8));
    }

    private static Path fixturePath() {
        Path path = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata", "presentation-contract.json").normalize();
        assertTrue("missing producer fixture at " + path, Files.isRegularFile(path));
        return path;
    }

    private static JSONObject scenario(JSONObject fixture, String name) throws Exception {
        JSONArray scenarios = fixture.getJSONArray("scenarios");
        for (int index = 0; index < scenarios.length(); index++) {
            JSONObject scenario = scenarios.getJSONObject(index);
            if (name.equals(scenario.getString("name"))) return new JSONObject(scenario.toString());
        }
        throw new AssertionError("missing scenario " + name);
    }

    private static JSONObject findingScenario(JSONObject fixture, String name) throws Exception {
        return scenario(fixture, name);
    }

    private static List<String> strings(JSONArray array) throws Exception {
        List<String> values = new ArrayList<>();
        for (int index = 0; index < array.length(); index++) values.add(array.getString(index));
        return values;
    }

    private static String safeScreen(Report report) {
        return AnalysisPresentation.from(report).blocks().stream().filter(block -> !block.folded())
                .map(Object::toString).collect(Collectors.joining("\n"));
    }

    private static String hex(byte[] bytes) {
        StringBuilder value = new StringBuilder(bytes.length * 2);
        for (byte item : bytes) value.append(String.format(Locale.ROOT, "%02x", item & 0xff));
        return value.toString();
    }
}
