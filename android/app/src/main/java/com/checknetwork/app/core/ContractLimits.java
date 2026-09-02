package com.checknetwork.app.core;

/** Client-side v1 bounds. The HTTP body cap is enforced by the transport layer. */
public final class ContractLimits {
    public static final int MAX_TARGETS = 20;
    public static final int DEFAULT_TIMEOUT_MS = 5_000;
    public static final int MIN_TIMEOUT_MS = 100;
    public static final int MAX_TIMEOUT_MS = 30_000;
    public static final int DEFAULT_TRACEROUTE_ATTEMPTS = 5;
    public static final int MAX_TRACEROUTE_ATTEMPTS = 10;
    public static final int MAX_TRANSPORT_BYTES = 8 * 1024 * 1024;
    public static final int MAX_STRING_CHARS = 4_096;
    public static final int MAX_ERROR_CODE_CHARS = 128;
    public static final int MAX_REPORT_STRING_CHARS = 1_000_000;
    public static final int MAX_REPORT_CONTAINERS = 32_768;
    public static final int MAX_DETAIL_ARRAY_ITEMS = 2_048;
    public static final int MAX_DETAIL_KEYS = 256;
    public static final int MAX_DETAIL_DEPTH = 8;
    /** Global lexical container depth bound applied before JSON materialization. */
    public static final int MAX_JSON_DEPTH = 16;
    public static final int MAX_TOPOLOGY_NODES_TOTAL = 8_192;
    public static final int MAX_TOPOLOGY_LINKS_TOTAL = 16_384;
    public static final int MAX_FINDINGS = 64;
    public static final int MAX_EVIDENCE = 64;
    public static final int MAX_ACTIONS = 64;
    public static final int MAX_COVERAGE_ITEMS = 128;
    public static final int MAX_COMPACT_TOPOLOGY_NODES = 500;
    public static final int MAX_COMPACT_TOPOLOGY_LINKS = 1_000;
    public static final int MAX_COMPACT_RESPONSE_BYTES_EXCLUSIVE = 1 << 20;
    public static final int MAX_COMPACT_GEO_BUNDLE_BYTES = 4_096;

    private ContractLimits() {}
}
