package com.checknetwork.app;

import static org.junit.Assert.*;

import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportParser;
import com.checknetwork.app.core.ReportRequest;
import java.util.List;
import java.nio.charset.StandardCharsets;
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
                "\"summary\":{\"total\":1,\"passed\":0,\"failed\":1},\"results\":[{\"kind\":\"traceroute\",\"address\":\"example.test\",\"status\":\"degraded\",\"latency_ms\":12,\"started_at\":\"2026-09-02T00:00:00Z\",\"details\":{}}],"+
                "\"analysis\":{\"verdict\":\"attention\",\"findings\":[{\"id\":\"f1\",\"code\":\"traceroute_path_degraded\",\"severity\":\"warning\",\"category\":\"routing\",\"title\":\"Path degraded\",\"summary\":\"Loss observed\",\"confidence\":\"direct\",\"evidence_ids\":[\"e1\"],\"action_ids\":[\"a1\"]}],"+
                "\"evidence\":[{\"id\":\"e1\",\"result_index\":0,\"kind\":\"traceroute\",\"address\":\"example.test\",\"signal\":\"loss\",\"observed\":\"50%\",\"expected\":\"0%\",\"provenance\":\"result\"}],"+
                "\"actions\":[{\"id\":\"a1\",\"title\":\"Retry path\",\"step\":\"Retry\",\"expected_result\":\"Stable\",\"escalation_condition\":\"Still lossy\"}],"+
                "\"coverage\":{\"available\":[\"route\"],\"missing\":[\"asn\"],\"provider_failures\":[{\"code\":\"missing_details\",\"result_index\":0,\"kind\":\"traceroute\",\"signal\":\"asn\",\"reason\":\"provider failed\"}],\"limitations\":[]}}}";
        Report report=ReportParser.parse(json);
        List<AnalysisPresentation.Block> blocks=AnalysisPresentation.from(report).blocks();
        assertEquals("Report", blocks.get(0).heading());
        String all=blocks.toString();
        assertTrue(all.contains("report-7")); assertTrue(all.contains("Degraded")); assertTrue(all.contains("Attention"));
        assertTrue(all.contains("Confidence: Direct")); assertFalse(all.toLowerCase().contains("probability"));
        assertTrue(all.contains("Evidence")); assertTrue(all.contains("Actions")); assertTrue(all.contains("Provider failures"));
        assertTrue(index(blocks,"Report") < index(blocks,"Findings"));
        assertTrue(index(blocks,"Findings") < index(blocks,"Evidence"));
        assertTrue(index(blocks,"Evidence") < index(blocks,"Actions"));
        assertTrue(index(blocks,"Actions") < index(blocks,"Coverage and limitations"));
        assertTrue(index(blocks,"Coverage and limitations") < index(blocks,"Result summary"));
    }

    @Test public void rawResultsArePlainBoundedCompleteAndAlwaysLast() {
        String json="{\"id\":\"dns-report\",\"status\":\"degraded\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":9,"+
                "\"summary\":{\"total\":1,\"passed\":0,\"failed\":1},\"results\":[{\"kind\":\"dns\",\"address\":\"example.test\",\"status\":\"degraded\",\"latency_ms\":9,\"started_at\":\"2026-09-02T00:00:00Z\",\"error_code\":\"partial_answer\",\"message\":\"one resolver failed\",\"details\":{\"answer\":\"203.0.113.8\",\"addresses\":[\"203.0.113.8\"]}}]}";
        List<AnalysisPresentation.Block> blocks=AnalysisPresentation.from(ReportParser.parse(json)).blocks();

        AnalysisPresentation.Block raw=blocks.get(blocks.size()-1);
        assertEquals("Raw results",raw.heading());assertTrue(raw.folded());
        assertTrue(raw.body().contains("Kind: DNS"));assertTrue(raw.body().contains("Status: Degraded"));
        assertTrue(raw.body().contains("Address: example.test"));assertTrue(raw.body().contains("Latency: 9 ms"));
        assertTrue(raw.body().contains("Error: partial_answer"));assertTrue(raw.body().contains("Message: one resolver failed"));
        assertTrue(raw.body().contains("Details:"));assertTrue(raw.body().contains("answer: 203.0.113.8"));
        assertTrue(raw.body().contains("addresses: [203.0.113.8]"));
        assertTrue(raw.body().length()<=AnalysisPresentation.MAX_RAW_RESULT_CHARS);
    }

    @Test public void rawResultsIncludeCompactTopologyAndGeoObservations() throws Exception {
        String json=new String(getClass().getClassLoader().getResourceAsStream("core/compact-traceroute-report.json").readAllBytes(),StandardCharsets.UTF_8);
        List<AnalysisPresentation.Block> blocks=AnalysisPresentation.from(ReportParser.parse(json)).blocks();
        String body=blocks.get(blocks.size()-1).body();
        assertTrue(body.contains("Compact topology: 1 nodes, 0 links, 1 routes; truncated: no"));
        assertTrue(body.contains("Geo observations:"));assertTrue(body.contains("eligible: 0"));assertTrue(body.contains("included: 0"));
    }

    private static int index(List<AnalysisPresentation.Block> blocks,String heading){
        for(int i=0;i<blocks.size();i++) if(blocks.get(i).heading().equals(heading)) return i;
        return -1;
    }
}
