package com.checknetwork.app.core;

import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.EnumSet;
import java.util.Objects;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;
import org.json.JSONTokener;

/** Immutable, bounded local view of the server's v1 checks contract. */
public final class CheckCapabilities {
    public static final int MAX_JSON_BYTES = 64 * 1024;
    public static final int MAX_LIST_ITEMS = 256;
    public static final int MAX_NAME_BYTES = 128;
    private static final int MAX_JSON_DEPTH = 8;
    private static final long MAX_ADVERTISED_COUNT = 1_000_000L;
    private static final long MAX_ADVERTISED_TIMEOUT_MS = 86_400_000L;

    private final Set<CheckKind> kinds;
    private final Set<ReportRequest.TopologyMode> topologyModes;
    private final int maxTargets;
    private final int minTimeoutMs;
    private final int maxTimeoutMs;
    private final int maxTracerouteAttempts;

    private CheckCapabilities(Set<CheckKind> kinds, Set<ReportRequest.TopologyMode> modes,
            int maxTargets, int minTimeoutMs, int maxTimeoutMs, int maxTracerouteAttempts) {
        this.kinds = immutableKinds(kinds);
        this.topologyModes = immutableModes(modes);
        this.maxTargets = maxTargets;
        this.minTimeoutMs = minTimeoutMs;
        this.maxTimeoutMs = maxTimeoutMs;
        this.maxTracerouteAttempts = maxTracerouteAttempts;
    }

    /** Parses exactly one bounded JSON object, retaining only capabilities this client understands. */
    public static CheckCapabilities parse(String json) {
        try {
            preflight(json);
            JSONTokener tokener = new JSONTokener(json);
            Object decoded = tokener.nextValue();
            if (!(decoded instanceof JSONObject source) || tokener.nextClean() != 0) throw invalid();

            EnumSet<CheckKind> kinds = EnumSet.noneOf(CheckKind.class);
            JSONArray rawKinds = requiredArray(source, "kinds");
            parseNames(rawKinds, value -> {
                try { kinds.add(CheckKind.fromWire(value)); }
                catch (IllegalArgumentException unknownFutureKind) { /* bounded unknown values are ignored */ }
            });

            EnumSet<ReportRequest.TopologyMode> modes = EnumSet.noneOf(ReportRequest.TopologyMode.class);
            JSONArray rawModes = requiredArray(source, "topology_modes");
            parseNames(rawModes, value -> {
                for (ReportRequest.TopologyMode mode : ReportRequest.TopologyMode.values()) {
                    if (mode.wireValue().equals(value)) {
                        modes.add(mode);
                        return;
                    }
                }
            });

            Object rawLimits = required(source, "limits");
            if (!(rawLimits instanceof JSONObject limits)) throw invalid();
            long advertisedTargets = integer(limits, "max_targets", 1, MAX_ADVERTISED_COUNT);
            long advertisedAttempts = integer(limits, "max_traceroute_attempts", 1, MAX_ADVERTISED_COUNT);
            long advertisedMinTimeout = integer(limits, "timeout_ms_min", 1, MAX_ADVERTISED_TIMEOUT_MS);
            long advertisedMaxTimeout = integer(limits, "timeout_ms_max", 1, MAX_ADVERTISED_TIMEOUT_MS);
            if (advertisedMinTimeout > advertisedMaxTimeout) throw invalid();

            int effectiveMinTimeout = (int) Math.max(advertisedMinTimeout, ContractLimits.MIN_TIMEOUT_MS);
            int effectiveMaxTimeout = (int) Math.min(advertisedMaxTimeout, ContractLimits.MAX_TIMEOUT_MS);
            if (effectiveMinTimeout > effectiveMaxTimeout) throw invalid();
            return new CheckCapabilities(kinds, modes,
                    (int) Math.min(advertisedTargets, ContractLimits.MAX_TARGETS),
                    effectiveMinTimeout, effectiveMaxTimeout,
                    (int) Math.min(advertisedAttempts, ContractLimits.MAX_TRACEROUTE_ATTEMPTS));
        } catch (IllegalArgumentException expected) {
            throw expected;
        } catch (JSONException | RuntimeException malformed) {
            throw invalid();
        }
    }

    /** Local fixture for tests and callers that deliberately do not perform discovery. */
    public static CheckCapabilities v1() {
        return new CheckCapabilities(EnumSet.allOf(CheckKind.class),
                EnumSet.allOf(ReportRequest.TopologyMode.class), ContractLimits.MAX_TARGETS,
                ContractLimits.MIN_TIMEOUT_MS, ContractLimits.MAX_TIMEOUT_MS,
                ContractLimits.MAX_TRACEROUTE_ATTEMPTS);
    }

    public boolean supports(CheckKind kind) { return kind != null && kinds.contains(kind); }
    public boolean supports(ReportRequest.TopologyMode mode) { return mode != null && topologyModes.contains(mode); }

    /** Rejects a request without reflecting target values or server-provided strings. */
    public void validate(ReportRequest request) {
        Objects.requireNonNull(request, "request");
        if (request.targets().size() > maxTargets) throw unsupported("target count");
        if (request.timeoutMs() < minTimeoutMs || request.timeoutMs() > maxTimeoutMs)
            throw unsupported("timeout");
        if (request.topologyMode() != null && !supports(request.topologyMode()))
            throw unsupported("topology mode");
        for (TargetInput target : request.targets()) {
            if (!supports(target.kind())) throw unsupported("check kind");
            if (target.kind() == CheckKind.TRACEROUTE) {
                int attempts = target.attempts() == null
                        ? ContractLimits.DEFAULT_TRACEROUTE_ATTEMPTS : target.attempts();
                if (attempts > maxTracerouteAttempts) throw unsupported("traceroute attempts");
            }
        }
    }

    public Set<CheckKind> kinds() { return kinds; }
    public Set<ReportRequest.TopologyMode> topologyModes() { return topologyModes; }
    public int maxTargets() { return maxTargets; }
    public int minTimeoutMs() { return minTimeoutMs; }
    public int maxTimeoutMs() { return maxTimeoutMs; }
    public int maxTracerouteAttempts() { return maxTracerouteAttempts; }

    @Override public String toString() {
        return "CheckCapabilities{kinds=" + kinds.size() + ", topologyModes=" + topologyModes.size()
                + ", maxTargets=" + maxTargets + ", timeoutRange=" + minTimeoutMs + ".." + maxTimeoutMs
                + ", maxTracerouteAttempts=" + maxTracerouteAttempts + "}";
    }

    private static void preflight(String json) {
        if (json == null || json.getBytes(StandardCharsets.UTF_8).length > MAX_JSON_BYTES) throw invalid();
        int depth = 0;
        boolean quoted = false;
        boolean escaped = false;
        for (int index = 0; index < json.length(); index++) {
            char current = json.charAt(index);
            if (quoted) {
                if (escaped) escaped = false;
                else if (current == '\\') escaped = true;
                else if (current == '"') quoted = false;
            } else if (current == '"') {
                quoted = true;
            } else if (current == '{' || current == '[') {
                if (++depth > MAX_JSON_DEPTH) throw invalid();
            } else if (current == '}' || current == ']') {
                if (--depth < 0) throw invalid();
            }
        }
        if (quoted || depth != 0) throw invalid();
    }

    private static JSONArray requiredArray(JSONObject source, String key) throws JSONException {
        Object value = required(source, key);
        if (!(value instanceof JSONArray array) || array.length() > MAX_LIST_ITEMS) throw invalid();
        return array;
    }

    private static Object required(JSONObject source, String key) throws JSONException {
        if (!source.has(key) || source.isNull(key)) throw invalid();
        return source.get(key);
    }

    private interface NameConsumer { void accept(String value); }

    private static void parseNames(JSONArray values, NameConsumer consumer) throws JSONException {
        for (int index = 0; index < values.length(); index++) {
            Object raw = values.get(index);
            if (!(raw instanceof String value) || value.isEmpty()
                    || value.getBytes(StandardCharsets.UTF_8).length > MAX_NAME_BYTES) throw invalid();
            consumer.accept(value);
        }
    }

    private static long integer(JSONObject source, String key, long minimum, long maximum) throws JSONException {
        Object raw = required(source, key);
        if (!(raw instanceof Byte || raw instanceof Short || raw instanceof Integer || raw instanceof Long)) throw invalid();
        long value = ((Number) raw).longValue();
        if (value < minimum || value > maximum) throw invalid();
        return value;
    }

    private static Set<CheckKind> immutableKinds(Set<CheckKind> source) {
        EnumSet<CheckKind> copy = source.isEmpty() ? EnumSet.noneOf(CheckKind.class) : EnumSet.copyOf(source);
        return Collections.unmodifiableSet(copy);
    }

    private static Set<ReportRequest.TopologyMode> immutableModes(Set<ReportRequest.TopologyMode> source) {
        EnumSet<ReportRequest.TopologyMode> copy = source.isEmpty()
                ? EnumSet.noneOf(ReportRequest.TopologyMode.class) : EnumSet.copyOf(source);
        return Collections.unmodifiableSet(copy);
    }

    private static IllegalArgumentException unsupported(String field) {
        return new IllegalArgumentException("Report request exceeds advertised " + field + " capabilities");
    }

    private static IllegalArgumentException invalid() {
        return new IllegalArgumentException("Invalid checks capability response");
    }
}
