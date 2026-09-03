package com.checknetwork.app.core;

import java.util.Objects;

/** Deterministic allowlist-only human export. Raw identifiers and network observations never enter it. */
public final class ReportMarkdownExporter {
    private ReportMarkdownExporter() {}

    public static String export(Report report) {
        Objects.requireNonNull(report, "report");
        StringBuilder out = new StringBuilder();
        out.append("# Network diagnostic summary\n\n")
                .append("- Overall status: **").append(label(report.status())).append("**\n")
                .append("- Started: ").append(report.startedAt()).append("\n")
                .append("- Duration: ").append(report.durationMs()).append(" ms\n")
                .append("- Checks: ").append(report.summary().total())
                .append(" total, ").append(report.summary().passed()).append(" passed, ")
                .append(report.summary().failed()).append(" failed\n\n")
                .append("## Check outcomes\n\n");
        if (report.results().isEmpty()) out.append("No checks were returned.\n");
        for (int index = 0; index < report.results().size(); index++) {
            Report.Result result = report.results().get(index);
            out.append(index + 1).append(". **").append(kindLabel(result.kind())).append("** — ")
                    .append(label(result.status())).append(" (").append(result.latencyMs()).append(" ms)\n");
        }
        out.append("\n## Analysis\n\n");
        if (report.analysis().isEmpty()) {
            out.append("Analysis unavailable (legacy report).\n");
        } else {
            Report.Analysis analysis = report.analysis().orElseThrow();
            out.append("- Verdict: **").append(verdictLabel(analysis.verdict())).append("**\n")
                    .append("- Findings: ").append(analysis.findings().size()).append("\n")
                    .append("- Evidence records withheld: ").append(analysis.evidence().size()).append("\n")
                    .append("- Recommended actions withheld: ").append(analysis.actions().size()).append("\n");
            for (Report.Finding finding : analysis.findings()) {
                out.append("  - ").append(findingLabel(finding.code())).append(" [")
                        .append(finding.severity().name().toLowerCase(java.util.Locale.ROOT)).append(", ")
                        .append(finding.confidence().name().toLowerCase(java.util.Locale.ROOT)).append("]\n");
            }
            out.append("- Coverage: ").append(analysis.coverage().available().size()).append(" available, ")
                    .append(analysis.coverage().missing().size()).append(" missing, ")
                    .append(analysis.coverage().providerFailures().size()).append(" provider failures, ")
                    .append(analysis.coverage().limitations().size()).append(" limitations\n");
        }
        report.compactTopology().ifPresent(topology -> out.append("\n## Compact topology summary\n\n")
                .append("- Nodes: ").append(topology.nodeCount()).append("\n")
                .append("- Links: ").append(topology.linkCount()).append("\n")
                .append("- Routes: ").append(topology.routeCount()).append("\n")
                .append("- Truncated: ").append(topology.truncated() ? "yes" : "no").append("\n"));
        return out.toString();
    }

    private static String label(Report.Status status) {
        switch (status) { case HEALTHY:return "Healthy";case DEGRADED:return "Degraded";default:return "Unreachable"; }
    }
    private static String verdictLabel(Report.Verdict verdict) {
        switch (verdict) { case HEALTHY:return "Healthy";case ATTENTION:return "Attention needed";default:return "Inconclusive"; }
    }
    private static String kindLabel(CheckKind kind) {
        switch(kind){
            case DNS:return "DNS";case TCP:return "TCP";case HTTP:return "HTTP";case HTTPS:return "HTTPS";
            case SSH:return "SSH";case SMTP:return "SMTP";case SUBMISSION:return "Mail submission";case SMTPS:return "SMTPS";
            case IMAP:return "IMAP";case IMAPS:return "IMAPS";case POP3:return "POP3";case POP3S:return "POP3S";default:return "Traceroute";
        }
    }
    private static String findingLabel(Report.FindingCode code) {
        return switch(code) {
            case DNS_RESOLUTION_FAILED -> "DNS resolution failed";
            case ENDPOINT_CONNECT_FAILED -> "Endpoint connection failed";
            case HTTP_UNEXPECTED_STATUS -> "Unexpected HTTP status";
            case INVALID_TARGET -> "Invalid target";
            case EXECUTION_TIMEOUT -> "Execution timed out";
            case EXECUTION_CANCELLED -> "Execution cancelled";
            case TLS_DOWNGRADE -> "TLS downgrade";
            case TLS_CERTIFICATE_EXPIRED -> "TLS certificate expired";
            case TLS_CERTIFICATE_EXPIRING -> "TLS certificate expiring";
            case TLS_HANDSHAKE_FAILED -> "TLS handshake failed";
            case TARGET_POLICY_BLOCKED -> "Target blocked by policy";
            case TRACEROUTE_UNREACHABLE -> "Traceroute destination unreachable";
            case TRACEROUTE_PARTIAL_REACHABILITY -> "Traceroute partial reachability";
            case TRACEROUTE_PATH_DEGRADED -> "Traceroute path degraded";
            case TRACEROUTE_PATH_UNSTABLE -> "Traceroute path unstable";
            case TRACEROUTE_EXECUTION_FAILED -> "Traceroute execution failed";
            case CHECKER_PANIC -> "Checker execution failed";
            case CHECKER_CAPACITY_UNAVAILABLE -> "Checker capacity was unavailable";
        };
    }
}
