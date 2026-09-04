package com.checknetwork.app.core;

import static org.junit.Assert.*;


import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.EnumMap;
import java.util.Map;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class ReportMarkdownExporterTest {
    private static String checkerExecutionGoFixture() throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata", "checker-execution-report.json").normalize();
        assertTrue("missing Go-produced fixture at " + fixture, Files.isRegularFile(fixture));
        return new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8);
    }

    @Test public void exportsDeterministicHumanSummaryWithoutSensitiveRawFields() {
        String secret="SUPER-secret-token-123";
        String json="{\"id\":\"report-secret-id\",\"status\":\"unreachable\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":12,"
                +"\"summary\":{\"total\":1,\"passed\":0,\"failed\":1},\"results\":[{\"kind\":\"https\",\"address\":\"private.example.test\",\"status\":\"unreachable\",\"latency_ms\":7,\"started_at\":\"2026-09-02T00:00:00Z\",\"error_code\":\"unexpected_status\",\"message\":\"Bearer "+secret+" at 192.0.2.1\",\"details\":{\"content_type\":\"router.internal\"}}],"
                +"\"analysis\":{\"verdict\":\"attention\",\"findings\":[{\"id\":\"finding-secret\",\"code\":\"tls_downgrade\",\"severity\":\"critical\",\"category\":\"security\",\"title\":\"private.example.test "+secret+"\",\"summary\":\"Observed 192.0.2.1\",\"confidence\":\"direct\",\"evidence_ids\":[\"e\"],\"action_ids\":[\"a\"]}],"
                +"\"evidence\":[{\"id\":\"e\",\"result_index\":0,\"kind\":\"https\",\"address\":\"private.example.test\",\"signal\":\"error_code\",\"observed\":\"http://private.example.test "+secret+"\",\"provenance\":\"details\"}],"
                +"\"actions\":[{\"id\":\"a\",\"title\":\"Visit private.example.test\",\"step\":\"use "+secret+"\",\"expected_result\":\"192.0.2.1\",\"escalation_condition\":\"router.internal\"}],\"coverage\":{\"available\":[],\"missing\":[],\"provider_failures\":[],\"limitations\":[]}}}";
        Report report=ReportParser.parse(json);
        String first=ReportMarkdownExporter.export(report);
        assertEquals(first,ReportMarkdownExporter.export(report));
        for(String forbidden:new String[]{"report-secret-id","private.example.test","192.0.2.1","router.internal",secret,"Bearer","details","Observed","finding-secret"})
            assertFalse("leaked "+forbidden,first.contains(forbidden));
        assertTrue(first.contains("HTTPS"));
        assertTrue(first.contains("finding.tls_downgrade"));
        assertTrue(first.contains("Unreachable"));
    }

    @Test public void legacyReportHasExplicitAnalysisUnavailableLine() {
        Report report=ReportParser.parse("{\"id\":\"x\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":0,\"summary\":{\"total\":0,\"passed\":0,\"failed\":0},\"results\":[]}");
        assertTrue(ReportMarkdownExporter.export(report).contains("Analysis unavailable"));
    }

    @Test public void checkerExecutionFixtureUsesSpecificPrivacySafeLabelsInsteadOfTracerouteFailure() throws Exception {
        String markdown = ReportMarkdownExporter.export(ReportParser.parse(checkerExecutionGoFixture()));
        assertTrue(markdown.contains("finding.checker_panic"));
        assertTrue(markdown.contains("finding.checker_capacity_unavailable"));
        assertFalse(markdown.contains("Traceroute execution failed"));

        String hostile = checkerExecutionGoFixture()
                .replace("Checker execution failed", "BACKEND-TITLE-CANARY")
                .replace("The checker stopped unexpectedly, so service health was not established.", "BACKEND-SUMMARY-CANARY")
                .replace("Checker capacity was unavailable", "BACKEND-CAPACITY-TITLE-CANARY")
                .replace("The bounded checker supervisor had no execution slot, so service health was not established.", "BACKEND-CAPACITY-SUMMARY-CANARY");
        String hostileMarkdown = ReportMarkdownExporter.export(ReportParser.parse(hostile));
        for (String canary : new String[]{"BACKEND-TITLE-CANARY", "BACKEND-SUMMARY-CANARY",
                "BACKEND-CAPACITY-TITLE-CANARY", "BACKEND-CAPACITY-SUMMARY-CANARY"}) {
            assertFalse("leaked backend prose " + canary, hostileMarkdown.contains(canary));
        }
        assertTrue(hostileMarkdown.contains("finding.checker_panic"));
        assertTrue(hostileMarkdown.contains("finding.checker_capacity_unavailable"));
    }

    @Test public void everyFindingCodeHasAnExplicitFixedPresentationKey() {
        assertEquals("new FindingCode requires an explicit registry entry",
                java.util.EnumSet.allOf(Report.FindingCode.class), AnalysisPresentationRegistry.registeredCodes());
        for (Report.FindingCode code : Report.FindingCode.values())
            assertEquals("finding." + code.name().toLowerCase(java.util.Locale.ROOT),
                    AnalysisPresentationRegistry.presentationKey(code));
    }
}
