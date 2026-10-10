package com.checknetwork.app.core;

import java.nio.ByteBuffer;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Domain-specific byte parser: no floating point or permissive JSON materializer. */
final class IPContextJson {
    private static final Pattern NUMBER = Pattern.compile("(-?)(0|[1-9][0-9]*)(?:\\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?");
    private final String text;
    private int at, tokens;
    private IPContextJson(String text) { this.text = text; }
    static Object parse(byte[] bytes) {
        if (bytes == null || bytes.length > IPContextParser.MAX_BYTES) throw invalid();
        try {
            String text = StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(bytes)).toString();
            IPContextJson parser = new IPContextJson(text);
            Object value = parser.value(0); parser.space();
            if (parser.at != text.length()) throw invalid();
            return value;
        } catch (java.nio.charset.CharacterCodingException failure) { throw invalid(); }
    }
    private Object value(int depth) {
        if (depth > 8 || ++tokens > 4096) throw invalid();
        space();
        if (at == text.length()) throw invalid();
        char c = text.charAt(at);
        if (c == '"') return string();
        if (c == '{') {
            at++; Map<String,Object> map = new LinkedHashMap<>(); space();
            if (take('}')) return Collections.unmodifiableMap(map);
            do {
                space(); String key = string(); space(); expect(':');
                if (map.containsKey(key)) throw invalid();
                map.put(key, value(depth + 1)); space();
                if (take('}')) return Collections.unmodifiableMap(map);
                expect(',');
            } while (true);
        }
        if (c == '[') {
            at++; ArrayList<Object> list = new ArrayList<>(); space();
            if (take(']')) return Collections.unmodifiableList(list);
            do {
                list.add(value(depth + 1)); space();
                if (take(']')) return Collections.unmodifiableList(list);
                expect(',');
            } while (true);
        }
        Matcher number = NUMBER.matcher(text).region(at, text.length());
        if (!number.lookingAt()) throw invalid(); // null/booleans have no place in this domain.
        at = number.end();
        return integer(number);
    }
    private static long integer(Matcher m) {
        String fraction = m.group(3) == null ? "" : m.group(3);
        String digits = m.group(2) + fraction;
        int first = 0; while (first < digits.length() && digits.charAt(first) == '0') first++;
        if (first == digits.length()) return 0L; // zero is exact even with an arbitrarily large exponent.
        if (!m.group(1).isEmpty()) throw invalid();
        int end = digits.length(); while (digits.charAt(end - 1) == '0') end--;
        String exp = m.group(4);
        long exponent = 0;
        if (exp != null) {
            boolean negative = exp.charAt(0) == '-'; int i = exp.charAt(0) == '-' || exp.charAt(0) == '+' ? 1 : 0;
            for (; i < exp.length(); i++) { exponent = Math.min(100000L, exponent * 10 + exp.charAt(i) - '0'); }
            if (negative) exponent = -exponent;
        }
        long zeros = exponent - fraction.length() + digits.length() - end;
        if (zeros < 0 || end - first + zeros > 10) throw invalid();
        long value = Long.parseLong(digits.substring(first, end));
        for (long i = 0; i < zeros; i++) value *= 10;
        if (value > 4294967295L) throw invalid();
        return value;
    }
    private String string() {
        expect('"'); StringBuilder out = new StringBuilder(); boolean closed = false;
        while (at < text.length()) {
            char c = text.charAt(at++);
            if (c == '"') { closed = true; break; }
            if (c < 32) throw invalid();
            if (c == '\\') {
                if (at == text.length()) throw invalid();
                c = text.charAt(at++);
                switch (c) {
                    case '"', '\\', '/' -> out.append(c);
                    case 'b' -> out.append('\b'); case 'f' -> out.append('\f');
                    case 'n' -> out.append('\n'); case 'r' -> out.append('\r'); case 't' -> out.append('\t');
                    case 'u' -> {
                        int code = 0;
                        for (int i = 0; i < 4; i++) {
                            if (at == text.length()) throw invalid();
                            char hex = text.charAt(at++);
                            if (!(hex >= '0' && hex <= '9' || hex >= 'a' && hex <= 'f' || hex >= 'A' && hex <= 'F')) throw invalid();
                            code = code * 16 + Character.digit(hex, 16);
                        }
                        out.append((char) code);
                    }
                    default -> throw invalid();
                }
            } else out.append(c);
        }
        if (!closed) throw invalid();
        for (int i = 0; i < out.length(); i++) {
            char c = out.charAt(i);
            if (Character.isHighSurrogate(c)) {
                if (++i == out.length() || !Character.isLowSurrogate(out.charAt(i))) throw invalid();
            } else if (Character.isLowSurrogate(c)) throw invalid();
        }
        return out.toString();
    }
    private void space() { while (at < text.length() && " \t\r\n".indexOf(text.charAt(at)) >= 0) at++; }
    private boolean take(char c) { if (at < text.length() && text.charAt(at) == c) { at++; return true; } return false; }
    private void expect(char c) { if (!take(c)) throw invalid(); }
    static IllegalArgumentException invalid() { return new IllegalArgumentException("Invalid IP context response"); }
}
