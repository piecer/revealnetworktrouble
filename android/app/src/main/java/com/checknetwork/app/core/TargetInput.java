package com.checknetwork.app.core;

import java.util.Objects;

public final class TargetInput {
    private final CheckKind kind;
    private final String address;
    private final Integer expectedStatus;
    private final Integer attempts;

    private TargetInput(Builder builder) {
        kind = Objects.requireNonNull(builder.kind, "kind");
        address = requireAddress(builder.address);
        expectedStatus = builder.expectedStatus;
        attempts = builder.attempts;
        if (expectedStatus != null && (!kind.supportsExpectedStatus() || expectedStatus < 100 || expectedStatus > 599))
            throw new IllegalArgumentException("expected_status is only valid for HTTP/HTTPS from 100 to 599");
        if (attempts != null && (kind != CheckKind.TRACEROUTE || attempts < 1 || attempts > ContractLimits.MAX_TRACEROUTE_ATTEMPTS))
            throw new IllegalArgumentException("attempts is only valid for traceroute from 1 to 10");
    }

    public static TargetInput of(CheckKind kind, String address) { return builder(kind, address).build(); }
    public static Builder builder(CheckKind kind, String address) { return new Builder(kind, address); }
    public CheckKind kind() { return kind; }
    public String address() { return address; }
    public Integer expectedStatus() { return expectedStatus; }
    public Integer attempts() { return attempts; }

    private static String requireAddress(String value) {
        if (value == null || value.trim().isEmpty() || value.length() > ContractLimits.MAX_STRING_CHARS)
            throw new IllegalArgumentException("address must contain 1 to 4096 characters");
        return value.trim();
    }

    public static final class Builder {
        private final CheckKind kind;
        private final String address;
        private Integer expectedStatus;
        private Integer attempts;
        private Builder(CheckKind kind, String address) { this.kind = kind; this.address = address; }
        public Builder expectedStatus(int value) { expectedStatus = value; return this; }
        public Builder attempts(int value) { attempts = value; return this; }
        public TargetInput build() { return new TargetInput(this); }
    }
}
