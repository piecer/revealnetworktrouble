package com.checknetwork.app.core;

import static org.junit.Assert.*;

import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class ReportMarkdownExporterTest {
    @Test public void exportsDeterministicHumanSummaryWithoutSensitiveRawFields() {
        String secret="SUPER-secret-token-123";
        String json="{\"id\":\"report-secret-id\",\"status\":\"unreachable\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":12,"
                +"\"summary\":{\"total\":1,\"passed\":0,\"failed\":1},\"results\":[{\"kind\":\"https\",\"address\":\"private.example.test\",\"status\":\"unreachable\",\"latency_ms\":7,\"started_at\":\"2026-09-02T00:00:00Z\",\"message\":\"Bearer "+secret+" at 192.0.2.1\",\"details\":{\"hostname\":\"router.internal\"}}],"
                +"\"analysis\":{\"verdict\":\"attention\",\"findings\":[{\"id\":\"finding-secret\",\"code\":\"tls_downgrade\",\"severity\":\"critical\",\"category\":\"security\",\"title\":\"private.example.test "+secret+"\",\"summary\":\"Observed 192.0.2.1\",\"confidence\":\"direct\",\"evidence_ids\":[\"e\"],\"action_ids\":[\"a\"]}],"
                +"\"evidence\":[{\"id\":\"e\",\"result_index\":0,\"kind\":\"https\",\"address\":\"private.example.test\",\"signal\":\"redirect\",\"observed\":\"http://private.example.test "+secret+"\",\"provenance\":\"details\"}],"
                +"\"actions\":[{\"id\":\"a\",\"title\":\"Visit private.example.test\",\"step\":\"use "+secret+"\",\"expected_result\":\"192.0.2.1\",\"escalation_condition\":\"router.internal\"}],\"coverage\":{\"available\":[],\"missing\":[],\"provider_failures\":[],\"limitations\":[]}}}";
        Report report=ReportParser.parse(json);
        String first=ReportMarkdownExporter.export(report);
        assertEquals(first,ReportMarkdownExporter.export(report));
        for(String forbidden:new String[]{"report-secret-id","private.example.test","192.0.2.1","router.internal",secret,"Bearer","details","Observed","finding-secret"})
            assertFalse("leaked "+forbidden,first.contains(forbidden));
        assertTrue(first.contains("HTTPS"));
        assertTrue(first.contains("TLS downgrade"));
        assertTrue(first.contains("Unreachable"));
    }

    @Test public void legacyReportHasExplicitAnalysisUnavailableLine() {
        Report report=ReportParser.parse("{\"id\":\"x\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":0,\"summary\":{\"total\":0,\"passed\":0,\"failed\":0},\"results\":[]}");
        assertTrue(ReportMarkdownExporter.export(report).contains("Analysis unavailable"));
    }
}
