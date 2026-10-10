package com.checknetwork.app.core;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;

/** Pure projection over already validated reports, including public observations without GeoIP. */
public final class IPContextAddresses {
    public static final int MAX_ADDRESSES = 500;
    private final List<String> addresses;
    private final int omitted;
    private IPContextAddresses(LinkedHashSet<String> unique) {
        ArrayList<String> all = new ArrayList<>(unique);
        omitted = Math.max(0, all.size() - MAX_ADDRESSES);
        addresses = Collections.unmodifiableList(new ArrayList<>(all.subList(0, Math.min(all.size(), MAX_ADDRESSES))));
    }
    public List<String> addresses() { return addresses; }
    public int omitted() { return omitted; }
    public static IPContextAddresses from(Report report) {
        LinkedHashSet<String> unique = new LinkedHashSet<>();
        if (report == null) return new IPContextAddresses(unique);
        if (report.compactTopology().isPresent()) {
            for (Object value : list(report.compactTopology().orElseThrow().opaqueData().get("nodes"))) {
                Map<?,?> node = map(value);
                if ("ip".equals(node.get("kind")) && positive(node.get("hop_min")) && positive(node.get("observations"))) add(node, unique);
            }
        } else for (Report.Result result : report.results()) {
            if (result.kind() != CheckKind.TRACEROUTE) continue;
            Map<String,Object> details = result.details();
            if (details.get("attempts") instanceof List<?>) {
                for (Object value : list(details.get("attempts"))) {
                    Map<?,?> attempt = map(value); Map<?,?> topology = map(attempt.get("topology"));
                    if (empty(attempt.get("error_code")) && eligibleTopology(attempt.get("status"), topology)) full(topology, unique);
                }
            } else if (empty(result.errorCode())) {
                Map<?,?> topology = map(details.get("topology"));
                if (eligibleTopology(result.status().name().toLowerCase(java.util.Locale.ROOT), topology)) full(topology, unique);
            }
        }
        return new IPContextAddresses(unique);
    }
    private static void full(Map<?,?> topology, LinkedHashSet<String> unique) {
        for (Object value : list(topology.get("nodes"))) {
            Map<?,?> node = map(value); if (positive(node.get("hop"))) add(node, unique);
        }
    }
    private static boolean eligibleTopology(Object status, Map<?,?> topology) {
        if (!(topology.get("reached") instanceof Boolean reached)) return false;
        String expected = reached ? "healthy" : "unreachable";
        if (reached) for (Object value : list(topology.get("nodes"))) {
            Object nodeStatus = map(value).get("status");
            if ("unknown".equals(nodeStatus) || "degraded".equals(nodeStatus)) expected = "degraded";
        }
        return expected.equals(status);
    }
    private static void add(Map<?,?> node, LinkedHashSet<String> unique) {
        if (!("healthy".equals(node.get("status")) || "degraded".equals(node.get("status")))) return;
        if (node.get("address") instanceof String address && IPContextParser.isEligible(address)) unique.add(address);
    }
    private static boolean empty(Object value) { return value == null || "".equals(value); }
    private static boolean positive(Object value) { return value instanceof Number n && n.doubleValue() > 0; }
    private static Map<?,?> map(Object value) { return value instanceof Map<?,?> m ? m : Map.of(); }
    private static List<?> list(Object value) { return value instanceof List<?> l ? l : List.of(); }
}
