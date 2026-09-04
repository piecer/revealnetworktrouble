package com.checknetwork.app;

import com.checknetwork.app.core.AnalysisPresentationRegistry;
import com.checknetwork.app.core.Report;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/** Native presentation with privacy-safe semantic blocks and one explicitly folded raw block. */
public final class AnalysisPresentation {
    public static final int MAX_RAW_RESULT_CHARS = 256 * 1024;
    public static final int MAX_SAFE_PRESENTATION_CHARS = 128 * 1024;
    private static final int MAX_SAFE_BLOCK_CHARS = 2048;

    public static final class Block {
        private final String key, heading, body;
        private final boolean folded;
        Block(String key, String heading, String body) { this(key, heading, body, false); }
        Block(String key, String heading, String body, boolean folded) {
            this.key = key; this.heading = heading; this.body = body; this.folded = folded;
        }
        public String key() { return key; }
        public String heading() { return heading; }
        public String body() { return body; }
        public boolean folded() { return folded; }
        @Override public String toString() { return key + " — " + heading + ": " + body; }
    }

    private final List<Block> blocks;
    private AnalysisPresentation(List<Block> blocks) { this.blocks = Collections.unmodifiableList(blocks); }
    public List<Block> blocks() { return blocks; }

    public static AnalysisPresentation from(Report report) {
        List<Block> out = new ArrayList<>();
        SafeBudget budget = new SafeBudget(MAX_SAFE_PRESENTATION_CHARS);
        String countSummary = report.summary().passed() + " completed observations, "
                + report.summary().failed() + " incomplete observations";
        String context = "Status: " + AnalysisPresentationRegistry.reportStatus(report)
                + "\nStarted: " + report.startedAt()
                + "\nDuration: " + report.durationMs() + " ms"
                + "\nChecks: " + report.summary().total() + " total, " + countSummary
                + "\nVerdict: " + AnalysisPresentationRegistry.verdict(report);
        addSafe(out, budget, "report_context", "Report", context);

        for (AnalysisPresentationRegistry.Section section : AnalysisPresentationRegistry.sections(report)) {
            addSafe(out, budget, section.key(), section.heading(), section.body());
        }

        if (report.analysis().isPresent()) {
            for (Report.EnrichmentCoverage enrichment : report.analysis().orElseThrow().coverage().enrichment()) {
                StringBuilder summary = new StringBuilder("Source category: ").append(wire(enrichment.source().name()))
                        .append("\nCache hits: ").append(enrichment.cacheHits())
                        .append("\nUpstream fetches: ").append(enrichment.upstreamFetches())
                        .append("\nMaximum age: ").append(enrichment.maxAgeMs()).append(" ms");
                for (Report.EnrichmentFailure failure : enrichment.failures()) summary.append("\n")
                        .append(wire(failure.kind().name())).append(": ").append(failure.count())
                        .append(failure.retryable() ? " (retryable)" : " (not retryable)");
                addSafe(out, budget, "enrichment_coverage", "Enrichment", summary.toString());
            }
        }

        StringBuilder results = new StringBuilder("Checks: ").append(report.summary().total()).append(" total");
        for (Report.Result result : report.results()) results.append("\n")
                .append(AnalysisPresentationRegistry.kindLabel(result.kind())).append(" — ")
                .append(AnalysisPresentationRegistry.resultOutcome(result)).append(" — ")
                .append(result.latencyMs()).append(" ms");
        addSafe(out, budget, "result_summary", "Result summary", results.toString());
        report.compactTopology().ifPresent(topology -> addSafe(out, budget, "compact_topology", "Compact topology",
                "Nodes: " + topology.nodeCount() + "\nLinks: " + topology.linkCount() + "\nRoutes: "
                        + topology.routeCount() + "\nTruncated: " + (topology.truncated() ? "yes" : "no")));
        out.add(new Block("raw_results", "Raw results", rawResults(report), true));
        return new AnalysisPresentation(out);
    }

    private static void addSafe(List<Block> out, SafeBudget budget, String key, String heading, String body) {
        out.add(new Block(key, heading, budget.take(body, MAX_SAFE_BLOCK_CHARS)));
    }

    private static String rawResults(Report report) {
        BoundedText out = new BoundedText(MAX_RAW_RESULT_CHARS);
        int index = 0;
        for (Report.Result result : report.results()) {
            if (index > 0) out.add("\n\n");
            out.add("Result ").add(String.valueOf(++index))
                    .add("\nKind: ").add(result.kind().wireValue().toUpperCase(Locale.ROOT))
                    .add("\nStatus: ").add(status(result.status()))
                    .add("\nAddress: ").add(result.address())
                    .add("\nLatency: ").add(String.valueOf(result.latencyMs())).add(" ms")
                    .add("\nError: ").add(emptyAsNone(result.errorCode()))
                    .add("\nMessage: ").add(emptyAsNone(result.message()))
                    .add("\nDetails:");
            if (result.details().isEmpty()) out.add(" none");
            else for (Map.Entry<String,Object> detail : result.details().entrySet()) out.add("\n  ")
                    .add(detail.getKey()).add(": ").addValue(detail.getValue());
        }
        if (report.results().isEmpty()) out.add("No result records");
        report.compactTopology().ifPresent(topology -> {
            out.add("\n\nCompact topology: ").add(String.valueOf(topology.nodeCount())).add(" nodes, ")
                    .add(String.valueOf(topology.linkCount())).add(" links, ").add(String.valueOf(topology.routeCount()))
                    .add(" routes; truncated: ").add(topology.truncated() ? "yes" : "no");
            Object geo = topology.opaqueData().get("geo");
            out.add("\nGeo observations: "); if (geo instanceof Map<?,?>) out.addValue(geo); else out.add("none");
        });
        return out.text();
    }

    private static String emptyAsNone(String value) { return value == null || value.isEmpty() ? "none" : value; }

    private static final class SafeBudget {
        private int remaining;
        SafeBudget(int limit) { remaining = limit; }
        String take(String value, int blockLimit) {
            String source = value == null ? "" : value;
            int allowed = Math.min(blockLimit, remaining);
            if (allowed <= 0) return "Presentation limit reached.";
            String result = source.length() <= allowed ? source
                    : source.substring(0, Math.max(0, allowed - 1)) + "…";
            remaining -= result.length();
            return result;
        }
    }

    private static final class BoundedText {
        private final int limit;
        private final StringBuilder value = new StringBuilder();
        private boolean truncated;
        BoundedText(int limit) { this.limit = limit; }
        BoundedText add(String text) {
            if (truncated || text == null) return this;
            int remaining = limit - value.length();
            if (text.length() <= remaining) value.append(text);
            else { if (remaining > 1) value.append(text, 0, remaining - 1); if (remaining > 0) value.append('…'); truncated = true; }
            return this;
        }
        BoundedText addValue(Object item) {
            if (item == null) return add("null");
            if (item instanceof Map<?,?> map) {
                add("{"); boolean first = true;
                for (Map.Entry<?,?> entry : map.entrySet()) { if (!first) add(", "); first = false; add(String.valueOf(entry.getKey())).add(": ").addValue(entry.getValue()); }
                return add("}");
            }
            if (item instanceof List<?> list) {
                add("["); for (int i = 0; i < list.size(); i++) { if (i > 0) add(", "); addValue(list.get(i)); } return add("]");
            }
            return add(String.valueOf(item));
        }
        String text() { return value.toString(); }
    }

    private static String status(Report.Status status) {
        return switch (status) { case HEALTHY -> "Healthy"; case DEGRADED -> "Degraded"; case UNREACHABLE -> "Unreachable"; };
    }
    private static String wire(String value) { return value.toLowerCase(Locale.ROOT); }
}
