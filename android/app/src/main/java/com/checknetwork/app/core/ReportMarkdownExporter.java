package com.checknetwork.app.core;

import java.util.Objects;

/** Deterministic, bounded, allowlist-only human export. */
public final class ReportMarkdownExporter {
    public static final int MAX_EXPORT_CHARS = 128 * 1024;
    private ReportMarkdownExporter() {}

    public static String export(Report report) {
        Objects.requireNonNull(report, "report");
        BoundedMarkdown out = new BoundedMarkdown(MAX_EXPORT_CHARS);
        out.add("# Network diagnostic summary\n\n")
                .add("- Overall status: ").add(AnalysisPresentationRegistry.reportStatus(report)).add("\n")
                .add("- Started: ").add(report.startedAt().toString()).add("\n")
                .add("- Duration: ").add(String.valueOf(report.durationMs())).add(" ms\n")
                .add("- Checks: ").add(String.valueOf(report.summary().total())).add(" total, ");
        out.add(String.valueOf(report.summary().passed())).add(" completed observations, ")
                .add(String.valueOf(report.summary().failed())).add(" incomplete observations\n");
        out.add("- Verdict: ").add(AnalysisPresentationRegistry.verdict(report)).add("\n\n")
                .add("## Check outcomes\n\n");
        if (report.results().isEmpty()) out.add("No checks were returned.\n");
        for (int index = 0; index < report.results().size(); index++) {
            Report.Result result = report.results().get(index);
            out.add(String.valueOf(index + 1)).add(". **").add(AnalysisPresentationRegistry.kindLabel(result.kind()))
                    .add("** — ").add(AnalysisPresentationRegistry.resultOutcome(result)).add(" (")
                    .add(String.valueOf(result.latencyMs())).add(" ms)\n");
        }
        out.add("\n## Analysis\n\n");
        for (AnalysisPresentationRegistry.Section section : AnalysisPresentationRegistry.sections(report)) {
            out.add("<!-- semantic-key: ").add(section.key()).add(" -->\n")
                    .add("### ").add(section.key()).add("\n\n")
                    .add(section.body()).add("\n\n");
        }
        if (report.analysis().isPresent()) {
            for (Report.EnrichmentCoverage enrichment : report.analysis().orElseThrow().coverage().enrichment()) {
                out.add("<!-- semantic-key: enrichment_coverage -->\n### enrichment_coverage\n\n")
                        .add("- Source category: ").add(enrichment.source().name().toLowerCase(java.util.Locale.ROOT)).add("\n")
                        .add("- Cache hits: ").add(String.valueOf(enrichment.cacheHits())).add("\n")
                        .add("- Upstream fetches: ").add(String.valueOf(enrichment.upstreamFetches())).add("\n")
                        .add("- Maximum age: ").add(String.valueOf(enrichment.maxAgeMs())).add(" ms\n");
                for (Report.EnrichmentFailure failure : enrichment.failures()) {
                    out.add("- ").add(failure.kind().name().toLowerCase(java.util.Locale.ROOT)).add(": ")
                            .add(String.valueOf(failure.count())).add(failure.retryable() ? " (retryable)\n" : " (not retryable)\n");
                }
                out.add("\n");
            }
        }
        report.compactTopology().ifPresent(topology -> out.add("## Compact topology summary\n\n")
                .add("- Nodes: ").add(String.valueOf(topology.nodeCount())).add("\n")
                .add("- Links: ").add(String.valueOf(topology.linkCount())).add("\n")
                .add("- Routes: ").add(String.valueOf(topology.routeCount())).add("\n")
                .add("- Truncated: ").add(topology.truncated() ? "yes\n" : "no\n"));
        return out.text();
    }

    private static final class BoundedMarkdown {
        private final int limit;
        private final StringBuilder value = new StringBuilder();
        private boolean truncated;
        BoundedMarkdown(int limit) { this.limit = limit; }
        BoundedMarkdown add(String text) {
            if (truncated || text == null) return this;
            int remaining = limit - value.length();
            if (text.length() <= remaining) value.append(text);
            else {
                if (remaining > 1) value.append(text, 0, remaining - 1);
                if (remaining > 0) value.append('…');
                truncated = true;
            }
            return this;
        }
        String text() { return value.toString(); }
    }
}
