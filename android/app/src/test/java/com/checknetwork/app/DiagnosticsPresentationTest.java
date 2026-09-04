package com.checknetwork.app;

import static org.junit.Assert.*;

import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportParser;
import com.checknetwork.app.core.ReportRequest;
import java.util.List;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class DiagnosticsPresentationTest {
    @Test public void exposesAllThirteenKindsWithContextualLabelsAndHints() {
        List<TargetKindOption> options = TargetKindOption.all();
        assertEquals(13, options.size());
        for (CheckKind kind : CheckKind.values()) {
            TargetKindOption option = TargetKindOption.forKind(kind);
            assertNotNull(option.label());
            assertFalse(option.label().equalsIgnoreCase(kind.wireValue()));
            assertFalse(option.addressHint().isBlank());
        }
    }

    @Test public void buildsKindSpecificOptionsAndTopologyPayloads() {
        FormState mixed = new FormState("https://api.example.test", 5000, List.of(
                new FormState.Target(CheckKind.HTTP, "https://example.test", "204", ""),
                new FormState.Target(CheckKind.TRACEROUTE, "example.test", "", "3")));
        ReportRequest mixedRequest = mixed.toRequest();
        assertTrue(mixedRequest.toJson().contains("\"expected_status\":204"));
        assertTrue(mixedRequest.toJson().contains("\"attempts\":3"));
        assertFalse(mixedRequest.toJson().contains("topology_mode"));

        FormState topology = new FormState("https://api.example.test", 5000, List.of(
                new FormState.Target(CheckKind.TRACEROUTE, "example.test", "", "10")));
        assertTrue(topology.toRequest().toJson().contains("\"topology_mode\":\"compact\""));
    }

    @Test public void rejectsOptionsOutsideTheirKindsAndRanges() {
        assertThrows(IllegalArgumentException.class, () -> new FormState("https://x.test", 5000,
                List.of(new FormState.Target(CheckKind.DNS, "x.test", "200", ""))).toRequest());
        assertThrows(IllegalArgumentException.class, () -> new FormState("https://x.test", 5000,
                List.of(new FormState.Target(CheckKind.HTTP, "https://x.test", "99", ""))).toRequest());
        assertThrows(IllegalArgumentException.class, () -> new FormState("https://x.test", 5000,
                List.of(new FormState.Target(CheckKind.TRACEROUTE, "x.test", "", "11"))).toRequest());
    }

    @Test public void analysisPresentationKeepsIdentityCoverageFindingsEvidenceActionsLimitationsResultsTopologyOrder() {
        String json="{\"id\":\"report-7\",\"status\":\"degraded\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":12,"+
                "\"summary\":{\"total\":1,\"passed\":0,\"failed\":1},\"results\":[{\"kind\":\"traceroute\",\"address\":\"example.test\",\"status\":\"degraded\",\"latency_ms\":12,\"started_at\":\"2026-09-02T00:00:00Z\",\"details\":{\"attempts_cancelled\":0,\"attempts_execution_failed\":0,\"attempts_failed\":0,\"attempts_reached\":1,\"attempts_timed_out\":0,\"attempts_total\":1,\"attempts_unreached\":0,\"topology\":{\"reached\":true,\"nodes\":[{\"id\":\"hop-1\",\"hop\":1,\"status\":\"healthy\"},{\"id\":\"hop-2\",\"hop\":2,\"status\":\"degraded\"}],\"links\":[{\"from\":\"hop-1\",\"to\":\"hop-2\",\"status\":\"degraded\",\"latency_delta_ms\":70}]}}}],"+
                "\"analysis\":{\"verdict\":\"attention\",\"findings\":[{\"id\":\"f1\",\"code\":\"traceroute_path_degraded\",\"severity\":\"warning\",\"category\":\"routing\",\"title\":\"Path degraded\",\"summary\":\"Loss observed\",\"confidence\":\"direct\",\"evidence_ids\":[\"e1\"],\"action_ids\":[\"a1\"]}],"+
                "\"evidence\":[{\"id\":\"e1\",\"result_index\":0,\"kind\":\"traceroute\",\"address\":\"example.test\",\"signal\":\"traceroute.path_status\",\"observed\":\"degraded segment observed among 1 completed attempt\",\"expected\":\"producer prose withheld\",\"provenance\":\"details\"}],"+
                "\"actions\":[{\"id\":\"a1\",\"title\":\"Retry path\",\"step\":\"Retry\",\"expected_result\":\"Stable\",\"escalation_condition\":\"Still lossy\"}],"+
                "\"coverage\":{\"available\":[\"route\"],\"missing\":[\"topology\"],\"provider_failures\":[{\"code\":\"missing_details\",\"result_index\":0,\"kind\":\"traceroute\",\"signal\":\"trace_topology\",\"reason\":\"provider failed\"}],\"limitations\":[]}}}";
        Report report=ReportParser.parse(json);
        List<AnalysisPresentation.Block> blocks=AnalysisPresentation.from(report).blocks();
        assertEquals("Report", blocks.get(0).heading());
        String all=blocks.toString();
        assertFalse(all.contains("report-7")); assertTrue(all.contains("Degraded")); assertTrue(all.contains("Attention"));
        assertTrue(all, all.contains("Direct bounded observation")); assertFalse(all.toLowerCase().contains("probability"));
        assertTrue(all.contains("supporting_evidence")); assertTrue(all.contains("next_action"));
        assertTrue(indexKey(blocks,"cause") < indexKey(blocks,"supporting_evidence"));
        assertTrue(indexKey(blocks,"supporting_evidence") < indexKey(blocks,"expectation"));
        assertTrue(indexKey(blocks,"expectation") < indexKey(blocks,"evidence_directness"));
        assertTrue(indexKey(blocks,"evidence_directness") < indexKey(blocks,"coverage_limitation"));
        assertTrue(indexKey(blocks,"coverage_limitation") < indexKey(blocks,"next_action"));
        assertTrue(indexKey(blocks,"next_action") < index(blocks,"Result summary"));
    }

    @Test public void rawResultsArePlainBoundedCompleteAndAlwaysLast() {
        String json="{\"id\":\"http-report\",\"status\":\"unreachable\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":9,"+
                "\"summary\":{\"total\":1,\"passed\":0,\"failed\":1},\"results\":[{\"kind\":\"http\",\"address\":\"https://example.test\",\"status\":\"unreachable\",\"latency_ms\":9,\"started_at\":\"2026-09-02T00:00:00Z\",\"error_code\":\"unexpected_status\",\"message\":\"unexpected HTTP status\",\"details\":{\"expected_status\":200,\"status_code\":503}}]}";
        List<AnalysisPresentation.Block> blocks=AnalysisPresentation.from(ReportParser.parse(json)).blocks();

        AnalysisPresentation.Block raw=blocks.get(blocks.size()-1);
        assertEquals("Raw results",raw.heading());assertTrue(raw.folded());
        assertTrue(raw.body().contains("Kind: HTTP"));assertTrue(raw.body().contains("Status: Unreachable"));
        assertTrue(raw.body().contains("Address: https://example.test"));assertTrue(raw.body().contains("Latency: 9 ms"));
        assertTrue(raw.body().contains("Error: unexpected_status"));assertTrue(raw.body().contains("Message: unexpected HTTP status"));
        assertTrue(raw.body().contains("Details:"));assertTrue(raw.body().contains("expected_status: 200"));
        assertTrue(raw.body().contains("status_code: 503"));
        assertTrue(raw.body().length()<=AnalysisPresentation.MAX_RAW_RESULT_CHARS);
    }

    @Test public void rawResultsIncludeCompactTopologyAndGeoObservations() throws Exception {
        String json=new String(getClass().getClassLoader().getResourceAsStream("core/compact-traceroute-report.json").readAllBytes(),StandardCharsets.UTF_8);
        List<AnalysisPresentation.Block> blocks=AnalysisPresentation.from(ReportParser.parse(json)).blocks();
        String body=blocks.get(blocks.size()-1).body();
        assertTrue(body.contains("Compact topology: 1 nodes, 0 links, 1 routes; truncated: no"));
        assertTrue(body.contains("Geo observations:"));assertTrue(body.contains("eligible: 0"));assertTrue(body.contains("included: 0"));
    }

    @Test public void enrichmentRendersFixedPrivacySafeBlockAfterCoverageBeforeResults() throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata", "enrichment-failures-report.json").normalize();
        assertTrue("missing Go-produced fixture at " + fixture, Files.isRegularFile(fixture));
        Report report = ReportParser.parse(new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8));
        List<AnalysisPresentation.Block> blocks = AnalysisPresentation.from(report).blocks();
        assertTrue(indexKey(blocks, "coverage_limitation") < index(blocks, "Enrichment"));
        assertTrue(index(blocks, "Enrichment") < index(blocks, "Result summary"));
        String body = blocks.get(index(blocks, "Enrichment")).body();
        assertTrue(body.contains("Source category: none"));
        assertTrue(body.contains("Cache hits: 0"));
        assertTrue(body.contains("Upstream fetches: 0"));
        assertTrue(body.contains("Maximum age: 0 ms"));
        assertTrue(body.contains("busy: 1 (retryable)"));
        assertTrue(body.contains("cancelled: 2 (not retryable)"));
        assertFalse(body.toLowerCase().contains("provider"));
        assertFalse(body.toLowerCase().contains("geoip"));
        assertFalse(body.contains("URL-CANARY"));
        assertFalse(body.contains("IP-CANARY"));
        assertFalse(body.contains("TARGET-CANARY"));
    }

    @Test public void checkerExecutionFindingsRenderOnlyGenericPrivacySafeText() throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata", "checker-execution-report.json").normalize();
        JSONObject hostile = new JSONObject(new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8));
        hostile.getJSONObject("analysis").getJSONArray("findings").getJSONObject(0)
                .put("title", "TITLE-CANARY-private-target").put("summary", "SUMMARY-CANARY-Bearer-secret");
        hostile.getJSONObject("analysis").getJSONArray("findings").getJSONObject(1)
                .put("title", "CAPACITY-TITLE-CANARY").put("summary", "CAPACITY-SUMMARY-CANARY");
        Report report = ReportParser.parse(hostile.toString());
        String rendered = AnalysisPresentation.from(report).blocks().toString();
        assertTrue(rendered.contains("finding.checker_panic"));
        assertTrue(rendered.contains("finding.checker_capacity_unavailable"));
        assertTrue(rendered.contains("The checker stopped unexpectedly, so service behavior was not established."));
        assertTrue(rendered.contains("Checker execution capacity was unavailable, so service behavior was not established."));
        assertFalse(rendered.contains("CANARY"));
    }

    private static int index(List<AnalysisPresentation.Block> blocks,String heading){
        for(int i=0;i<blocks.size();i++) if(blocks.get(i).heading().equals(heading)) return i;
        return -1;
    }
    private static int indexKey(List<AnalysisPresentation.Block> blocks,String key){
        for(int i=0;i<blocks.size();i++) if(blocks.get(i).key().equals(key)) return i;
        return -1;
    }
}
