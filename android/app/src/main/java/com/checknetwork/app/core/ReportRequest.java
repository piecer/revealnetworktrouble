package com.checknetwork.app.core;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

public final class ReportRequest {
    public enum TopologyMode {
        FULL("full"), COMPACT("compact");
        private final String wireValue;
        TopologyMode(String value) { wireValue = value; }
        public String wireValue() { return wireValue; }
    }

    private final List<TargetInput> targets;
    private final int timeoutMs;
    private final TopologyMode topologyMode;
    private final boolean topologyRequest;

    private ReportRequest(Builder builder) {
        if (builder.targets.isEmpty()) throw new IllegalStateException("at least one target is required");
        targets = Collections.unmodifiableList(new ArrayList<>(builder.targets));
        timeoutMs = builder.timeoutMs;
        topologyMode = builder.topologyMode;
        topologyRequest = builder.topologyRequest;
        if (topologyRequest && topologyMode != TopologyMode.COMPACT)
            throw new IllegalArgumentException("topology requests require compact mode");
        if (topologyMode == TopologyMode.COMPACT || topologyRequest) {
            for (TargetInput target : targets) if (target.kind() != CheckKind.TRACEROUTE)
                throw new IllegalArgumentException("compact topology requests require only traceroute targets");
        }
    }

    public static Builder builder() { return new Builder(false); }
    public static Builder topologyBuilder() { return new Builder(true); }
    public List<TargetInput> targets() { return targets; }
    public int timeoutMs() { return timeoutMs; }
    public TopologyMode topologyMode() { return topologyMode; }
    public boolean isTopologyRequest() { return topologyRequest; }

    public String toJson() {
        StringBuilder out = new StringBuilder("{\"targets\":[");
        for (int i = 0; i < targets.size(); i++) {
            if (i > 0) out.append(',');
            TargetInput target = targets.get(i);
            out.append("{\"kind\":").append(quote(target.kind().wireValue()))
                    .append(",\"address\":").append(quote(target.address()));
            if (target.expectedStatus() != null) out.append(",\"expected_status\":").append(target.expectedStatus());
            if (target.attempts() != null) out.append(",\"attempts\":").append(target.attempts());
            out.append('}');
        }
        out.append("],\"timeout_ms\":").append(timeoutMs);
        if (topologyMode != null) out.append(",\"topology_mode\":").append(quote(topologyMode.wireValue()));
        return out.append('}').toString();
    }

    /** Hex SHA-256 of canonical request JSON. Authentication material is deliberately outside this model. */
    public String signature() {
        try {
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(toJson().getBytes(StandardCharsets.UTF_8));
            StringBuilder result = new StringBuilder(64);
            for (byte value : digest) result.append(String.format(java.util.Locale.ROOT, "%02x", value & 0xff));
            return result.toString();
        } catch (NoSuchAlgorithmException impossible) { throw new AssertionError(impossible); }
    }

    private static String quote(String value) {
        StringBuilder out = new StringBuilder(value.length() + 2).append('"');
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            switch (c) {
                case '"': out.append("\\\""); break;
                case '\\': out.append("\\\\"); break;
                case '\b': out.append("\\b"); break;
                case '\f': out.append("\\f"); break;
                case '\n': out.append("\\n"); break;
                case '\r': out.append("\\r"); break;
                case '\t': out.append("\\t"); break;
                default:
                    if (c < 0x20) out.append(String.format(java.util.Locale.ROOT, "\\u%04x", (int)c));
                    else out.append(c);
            }
        }
        return out.append('"').toString();
    }

    public static final class Builder {
        private final boolean topologyRequest;
        private final List<TargetInput> targets = new ArrayList<>();
        private int timeoutMs = ContractLimits.DEFAULT_TIMEOUT_MS;
        private TopologyMode topologyMode;
        private Builder(boolean topologyRequest) {
            this.topologyRequest = topologyRequest;
            this.topologyMode = topologyRequest ? TopologyMode.COMPACT : null;
        }
        public Builder timeoutMs(int value) {
            if (value < ContractLimits.MIN_TIMEOUT_MS || value > ContractLimits.MAX_TIMEOUT_MS)
                throw new IllegalArgumentException("timeout_ms must be from 100 to 30000");
            timeoutMs = value; return this;
        }
        public Builder topologyMode(TopologyMode value) {
            Objects.requireNonNull(value, "topologyMode");
            if (topologyRequest && value != TopologyMode.COMPACT)
                throw new IllegalArgumentException("topology requests require compact mode");
            topologyMode = value; return this;
        }
        public Builder addTarget(TargetInput target) {
            Objects.requireNonNull(target, "target");
            if (targets.size() >= ContractLimits.MAX_TARGETS) throw new IllegalArgumentException("at most 20 targets are allowed");
            if (topologyRequest && target.kind() != CheckKind.TRACEROUTE)
                throw new IllegalArgumentException("topology requests accept traceroute targets only");
            targets.add(target); return this;
        }
        public Builder targets(List<TargetInput> values) {
            Objects.requireNonNull(values, "targets");
            for (TargetInput value : values) addTarget(value);
            return this;
        }
        public ReportRequest build() { return new ReportRequest(this); }
    }
}
