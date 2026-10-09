package com.checknetwork.app.core;

import java.util.Set;
import java.util.regex.Pattern;
import org.json.JSONException;
import org.json.JSONTokener;

/** Exact bounded count arithmetic, before Android's fixed-size numeric token buffer or Double. */
final class GeoDetailsNumbers {
    private static final Set<String> COUNTS = Set.of("schema_version", "total", "omitted");
    private static final Pattern NUMBER = Pattern.compile("-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?(?:[eE][+-]?[0-9]+)?");

    private GeoDetailsNumbers() {}

    /**
     * Normalize only number tokens directly inside a root geo_details object. Preserve all
     * keys, duplicates, strings and structure for the existing strict preflight. In particular
     * quoted numeric strings are not normalized. The original wire remains the raw/export value.
     * This avoids JsonReader's 1024-character number-token limitation without lenient parsing.
     */
    static String normalize(String raw) {
        int depth = 0, sidecarDepth = -1, copied = 0;
        StringBuilder normalized = null;
        for (int index = 0; index < raw.length();) {
            char c = raw.charAt(index++);
            if (c == '{' || c == '[') depth++;
            else if (c == '}' || c == ']') {
                depth--;
                if (depth < sidecarDepth) sidecarDepth = -1;
            } else if (c == '"') {
                int start = index - 1;
                boolean closed = false;
                while (index < raw.length()) {
                    c = raw.charAt(index++);
                    if (c == '\\') { if (index < raw.length()) index++; }
                    else if (c == '"') { closed = true; break; }
                }
                if (!closed) throw invalid();
                int after = whitespace(raw, index);
                if (after == raw.length() || raw.charAt(after) != ':' || index - start > 96) continue;
                String name;
                try {
                    Object decoded = new JSONTokener(raw.substring(start, index)).nextValue();
                    if (!(decoded instanceof String)) throw invalid();
                    name = (String) decoded;
                } catch (JSONException ex) { throw invalid(); }
                int valueAt = whitespace(raw, after + 1);
                if (depth == 1 && name.equals("geo_details") && valueAt < raw.length() && raw.charAt(valueAt) == '{') {
                    sidecarDepth = 2;
                } else if (depth == sidecarDepth && COUNTS.contains(name) && valueAt < raw.length()) {
                    char first = raw.charAt(valueAt);
                    if (first != '-' && (first < '0' || first > '9')) continue;
                    int end = valueAt;
                    while (end < raw.length() && ",}] \t\r\n".indexOf(raw.charAt(end)) < 0) end++;
                    String token = raw.substring(valueAt, end);
                    String canonical = Integer.toString(boundedInt(token, name.equals("schema_version") ? 1 : 6200));
                    if (!token.equals(canonical)) {
                        if (normalized == null) normalized = new StringBuilder(raw.length());
                        normalized.append(raw, copied, valueAt).append(canonical);
                        copied = end;
                    }
                    index = end;
                }
            }
        }
        return normalized == null ? raw : normalized.append(raw, copied, raw.length()).toString();
    }

    private static int whitespace(String value, int at) {
        while (at < value.length() && " \t\r\n".indexOf(value.charAt(at)) >= 0) at++;
        return at;
    }

    static int boundedInt(String raw, int max) {
        if (!NUMBER.matcher(raw).matches()) throw invalid();
        // Decimal positions, not floating point or BigInteger powers: O(token length), O(1) scratch.
        int start = raw.charAt(0) == '-' ? 1 : 0;
        int exponentAt = raw.indexOf('e');
        if (exponentAt < 0) exponentAt = raw.indexOf('E');
        int end = exponentAt < 0 ? raw.length() : exponentAt;
        int point = raw.indexOf('.');
        int fractional = point < 0 ? 0 : end - point - 1;
        int first = -1, last = -1, digits = 0;
        for (int i = start; i < end; i++) {
            char c = raw.charAt(i);
            if (c == '.') continue;
            if (c != '0') { if (first < 0) first = digits; last = digits; }
            digits++;
        }
        if (first < 0) return 0;
        if (start == 1) throw invalid();
        long exponent = 0;
        if (exponentAt >= 0) {
            int at = exponentAt + 1;
            boolean negative = raw.charAt(at) == '-';
            if (negative || raw.charAt(at) == '+') at++;
            for (; at < raw.length(); at++) exponent = Math.min(1_000_000_000L, exponent * 10 + raw.charAt(at) - '0');
            if (negative) exponent = -exponent;
        }
        // Saturation is beyond the maximum whole-wire length, hence cannot change admission.
        long zeros = exponent - fractional + digits - last - 1;
        if (zeros < 0 || last - first + 1 + zeros > 4) throw invalid();
        int value = 0, digit = 0;
        for (int i = start; i < end; i++) {
            char c = raw.charAt(i);
            if (c == '.') continue;
            if (digit >= first && digit <= last) value = value * 10 + c - '0';
            digit++;
        }
        while (zeros-- > 0) value *= 10;
        if (value > max) throw invalid();
        return value;
    }

    private static ReportParseException invalid() {
        return new ReportParseException("report.geo_details: invalid count token");
    }
}
