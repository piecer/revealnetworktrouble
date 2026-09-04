package com.checknetwork.app;

import com.checknetwork.app.core.ApiError;
import java.util.Map;
import java.util.Objects;

/** Final local presentation allowlist for the closed producer API-error contract. */
final class ApiErrorPresentation {
    private static final String INVALID_CODE = "invalid_server_response";
    private static final Entry INVALID = new Entry(
            R.string.api_error_invalid_server_response, false, false, false);
    private static final Map<String, Entry> ENTRIES = Map.ofEntries(
            row(503, "body_decode_capacity_unavailable",
                    R.string.api_error_body_decode_capacity_unavailable, true, true, false),
            row(500, "compact_response_too_large",
                    R.string.api_error_compact_response_too_large, true, false, false),
            row(500, "full_response_too_large",
                    R.string.api_error_full_response_too_large, true, false, false),
            row(500, "internal_error", R.string.api_error_internal_error, true, false, false),
            row(400, "invalid_json", R.string.api_error_invalid_json, false, false, false),
            row(422, "invalid_request", R.string.api_error_invalid_request, false, false, false),
            row(405, "unmatched", R.string.api_error_method_not_allowed, false, false, false),
            row(422, "network_policy_blocked",
                    R.string.api_error_network_policy_blocked, false, false, false),
            row(429, "rate_limited", R.string.api_error_rate_limited, true, true, false),
            row(413, "request_too_large", R.string.api_error_request_too_large, false, false, false),
            row(500, "response_serialization_failed",
                    R.string.api_error_response_serialization_failed, true, false, false),
            row(404, "unmatched", R.string.api_error_route_not_found, false, false, false),
            row(503, "server_busy", R.string.api_error_server_busy, true, true, false),
            row(503, "server_draining", R.string.api_error_server_draining, true, true, false),
            row(401, "unauthorized", R.string.api_error_unauthorized, false, false, true),
            row(503, "write_capacity_unavailable",
                    R.string.api_error_write_capacity_unavailable, true, false, false));

    private ApiErrorPresentation() { }

    static Entry resolve(ApiError error) {
        Objects.requireNonNull(error, "error");
        if (INVALID_CODE.equals(error.code())) return INVALID;
        Entry entry = ENTRIES.get(key(error.status(), error.code()));
        if (entry == null || entry.retryable() != error.retryable()
                || !entry.retryAfterAllowed() && error.retryAt().isPresent()) return INVALID;
        return entry;
    }

    private static Map.Entry<String, Entry> row(int status, String code, int messageResource,
            boolean retryable, boolean retryAfterAllowed, boolean credentialFocus) {
        return Map.entry(key(status, code),
                new Entry(messageResource, retryable, retryAfterAllowed, credentialFocus));
    }

    private static String key(int status, String code) { return status + "\u0000" + code; }

    record Entry(int messageResource, boolean retryable, boolean retryAfterAllowed,
            boolean credentialFocus) { }
}
