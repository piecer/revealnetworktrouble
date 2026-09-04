package com.checknetwork.app.core;

import android.util.JsonReader;
import android.util.JsonToken;
import java.io.IOException;
import java.io.StringReader;
import java.time.DateTimeException;
import java.time.Instant;
import java.util.Collections;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.Set;

/** Closed operational API-error contract; no server-provided code or prose is retained. */
public final class ApiError {
    static final int MAX_ERROR_BODY_CHARS = 64 * 1024;
    private static final int MAX_DEPTH = 2;
    private static final int MAX_PROPERTIES = 3;
    private static final int MAX_TOKENS = 10;
    private static final int MAX_WIRE_STRING_CHARS = 128;
    private static final String INVALID_CODE = "invalid_server_response";
    private static final String INVALID_MESSAGE = "The server returned an invalid error response.";
    private static final Map<String, Definition> DEFINITIONS = definitions();

    private final int status;
    private final String code;
    private final String safeMessage;
    private final boolean retryable;
    private final Instant retryAt;

    private ApiError(int status, String code, String safeMessage, boolean retryable, Instant retryAt) {
        this.status = status;
        this.code = code;
        this.safeMessage = safeMessage;
        this.retryable = retryable;
        this.retryAt = retryAt;
    }

    public static ApiError parse(int status, String body, String retryAfter, Instant now) {
        Objects.requireNonNull(now, "now");
        Definition definition = definition(status, body);
        if (definition == null) return invalid(status);
        Instant retryAt = definition.retryAfter ? parseRetryAfter(retryAfter, now) : null;
        return new ApiError(status, definition.code, definition.message, definition.retryable, retryAt);
    }

    private static Definition definition(int status, String body) {
        if (body == null || body.length() > MAX_ERROR_BODY_CHARS || !isWellFormedUtf16(body)) return null;
        try (JsonReader reader = new JsonReader(new StringReader(body))) {
            reader.setLenient(false);
            ParsedError parsed = new StrictErrorReader(reader).read();
            Definition definition = DEFINITIONS.get(key(status, parsed.code));
            return definition != null && definition.message.equals(parsed.message) ? definition : null;
        } catch (IOException | RuntimeException malformed) {
            return null;
        }
    }

    private static boolean isWellFormedUtf16(String value) {
        for (int index = 0; index < value.length(); index++) {
            char current = value.charAt(index);
            if (Character.isHighSurrogate(current)) {
                if (++index >= value.length() || !Character.isLowSurrogate(value.charAt(index))) return false;
            } else if (Character.isLowSurrogate(current)) return false;
        }
        return true;
    }

    private static ApiError invalid(int status) {
        return new ApiError(status, INVALID_CODE, INVALID_MESSAGE, false, null);
    }

    /** Parses canonical delta-seconds only: one ASCII decimal integer in the inclusive range 1..3600. */
    public static Instant parseRetryAfter(String value, Instant now) {
        Objects.requireNonNull(now, "now");
        if (value == null || value.isEmpty() || value.length() > 4 || value.charAt(0) == '0') return null;
        int seconds = 0;
        for (int index = 0; index < value.length(); index++) {
            char character = value.charAt(index);
            if (character < '0' || character > '9') return null;
            seconds = seconds * 10 + (character - '0');
        }
        if (seconds < 1 || seconds > 3600) return null;
        try {
            return now.plusSeconds(seconds);
        } catch (ArithmeticException | DateTimeException ignored) {
            return null;
        }
    }

    private static Map<String, Definition> definitions() {
        Map<String, Definition> values = new HashMap<>();
        add(values, 503, "body_decode_capacity_unavailable", true, true,
                "request body decode capacity is temporarily unavailable");
        add(values, 500, "compact_response_too_large", true, false,
                "compact report response exceeds the size limit");
        add(values, 500, "full_response_too_large", true, false,
                "full report response exceeds the size limit");
        add(values, 500, "internal_error", true, false, "report could not be generated");
        add(values, 400, "invalid_json", false, false,
                "request body must be a valid JSON report request");
        add(values, 422, "invalid_request", false, false, "request is invalid");
        add(values, 405, "unmatched", false, false, "route not found");
        add(values, 422, "network_policy_blocked", false, false,
                "target is not allowed in public mode");
        add(values, 429, "rate_limited", true, true, "per-client request limit exceeded");
        add(values, 413, "request_too_large", false, false, "request body exceeds the size limit");
        add(values, 500, "response_serialization_failed", true, false,
                "report response could not be serialized");
        add(values, 404, "unmatched", false, false, "route not found");
        add(values, 503, "server_busy", true, true, "report capacity is temporarily unavailable");
        add(values, 503, "server_draining", true, true,
                "server is draining and temporarily unavailable");
        add(values, 401, "unauthorized", false, false, "valid API credentials are required");
        add(values, 503, "write_capacity_unavailable", true, false,
                "report response write capacity is temporarily unavailable");
        return Collections.unmodifiableMap(values);
    }

    private static void add(Map<String, Definition> values, int status, String code,
            boolean retryable, boolean retryAfter, String message) {
        Definition previous = values.put(key(status, code),
                new Definition(code, retryable, retryAfter, message));
        if (previous != null) throw new IllegalStateException("duplicate API-error definition");
    }

    private static String key(int status, String code) { return status + "\u0000" + code; }

    public int status() { return status; }
    public String code() { return code; }
    public String safeMessage() { return safeMessage; }
    public boolean retryable() { return retryable; }
    public Optional<Instant> retryAt() { return Optional.ofNullable(retryAt); }

    @Override public String toString() {
        return "ApiError{status=" + status + ", code='" + code + "', retryable=" + retryable + "}";
    }

    private static final class StrictErrorReader {
        private final JsonReader reader;
        private int depth;
        private int properties;
        private int tokens;

        StrictErrorReader(JsonReader reader) { this.reader = reader; }

        ParsedError read() throws IOException {
            beginObject();
            Set<String> rootNames = new HashSet<>();
            ParsedError parsed = null;
            while (reader.hasNext()) {
                String name = nextName(rootNames);
                if (!"error".equals(name)) throw malformed();
                parsed = readErrorObject();
            }
            endObject();
            if (parsed == null || nextToken() != JsonToken.END_DOCUMENT) throw malformed();
            return parsed;
        }

        private ParsedError readErrorObject() throws IOException {
            beginObject();
            Set<String> names = new HashSet<>();
            String code = null;
            String message = null;
            while (reader.hasNext()) {
                String name = nextName(names);
                if ("code".equals(name)) code = nextBoundedString();
                else if ("message".equals(name)) message = nextBoundedString();
                else throw malformed();
            }
            endObject();
            if (code == null || message == null) throw malformed();
            return new ParsedError(code, message);
        }

        private void beginObject() throws IOException {
            if (nextToken() != JsonToken.BEGIN_OBJECT || ++depth > MAX_DEPTH) throw malformed();
            reader.beginObject();
        }

        private void endObject() throws IOException {
            if (nextToken() != JsonToken.END_OBJECT || depth-- <= 0) throw malformed();
            reader.endObject();
        }

        private String nextName(Set<String> seen) throws IOException {
            countToken();
            if (++properties > MAX_PROPERTIES) throw malformed();
            String name = reader.nextName();
            if (name.length() > MAX_WIRE_STRING_CHARS || !isWellFormedUtf16(name) || !seen.add(name))
                throw malformed();
            return name;
        }

        private String nextBoundedString() throws IOException {
            if (nextToken() != JsonToken.STRING) throw malformed();
            String value = reader.nextString();
            if (value.length() > MAX_WIRE_STRING_CHARS || !isWellFormedUtf16(value)) throw malformed();
            return value;
        }

        private JsonToken nextToken() throws IOException {
            countToken();
            return reader.peek();
        }

        private void countToken() {
            if (++tokens > MAX_TOKENS) throw malformed();
        }

        private static IllegalArgumentException malformed() {
            return new IllegalArgumentException("invalid API-error JSON");
        }
    }

    private static final class ParsedError {
        final String code;
        final String message;
        ParsedError(String code, String message) { this.code = code; this.message = message; }
    }

    private static final class Definition {
        final String code;
        final boolean retryable;
        final boolean retryAfter;
        final String message;

        Definition(String code, boolean retryable, boolean retryAfter, String message) {
            this.code = code;
            this.retryable = retryable;
            this.retryAfter = retryAfter;
            this.message = message;
        }
    }
}
