package com.checknetwork.app.core;

import android.util.JsonReader;
import android.util.JsonToken;
import java.io.IOException;
import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.Set;
import java.util.regex.Pattern;

/** Capability-specific JSON admission before JSONObject materialization. */
final class CapabilityJsonPreflight {
    private static final int MAX_JSON_DEPTH = 8;
    private static final Pattern JSON_LITERAL = Pattern.compile(
            "(?:true|false|null|-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)");

    private CapabilityJsonPreflight() {}

    static void validate(String json, int maxBytes) {
        if (json == null || json.getBytes(StandardCharsets.UTF_8).length > maxBytes) throw invalid();
        validateLexemes(json);
        try (JsonReader reader = new JsonReader(new StringReader(json))) {
            reader.setLenient(false);
            if (reader.peek() != JsonToken.BEGIN_OBJECT) throw invalid();
            validateValue(reader, 0);
            if (reader.peek() != JsonToken.END_DOCUMENT) throw invalid();
        } catch (IOException malformed) {
            throw invalid();
        }
    }

    /** Android JsonReader's non-lenient mode still accepts some non-JSON lexemes. */
    private static void validateLexemes(String json) {
        for (int index = 0; index < json.length();) {
            char current = json.charAt(index++);
            if (current == '"') {
                boolean closed = false;
                while (index < json.length()) {
                    current = json.charAt(index++);
                    if (current == '"') {
                        closed = true;
                        break;
                    }
                    if (current < 0x20) throw invalid();
                    if (current != '\\') continue;
                    if (index == json.length()) throw invalid();
                    char escape = json.charAt(index++);
                    if (escape == 'u') {
                        for (int digit = 0; digit < 4; digit++) {
                            if (index == json.length()) throw invalid();
                            char hex = json.charAt(index++);
                            if (!(hex >= '0' && hex <= '9') && !(hex >= 'a' && hex <= 'f')
                                    && !(hex >= 'A' && hex <= 'F')) throw invalid();
                        }
                    } else if ("\"\\/bfnrt".indexOf(escape) < 0) throw invalid();
                }
                if (!closed) throw invalid();
            } else if (!isJsonDelimiter(current)) {
                int start = index - 1;
                while (index < json.length() && json.charAt(index) != '"'
                        && !isJsonDelimiter(json.charAt(index))) index++;
                if (!JSON_LITERAL.matcher(json).region(start, index).matches()) throw invalid();
            }
        }
    }

    private static boolean isJsonDelimiter(char value) {
        return value == '{' || value == '}' || value == '[' || value == ']'
                || value == ':' || value == ',' || value == ' ' || value == '\t'
                || value == '\r' || value == '\n';
    }

    /** Walk unknown fields too; never let JSONObject collapse names or recurse past the bound. */
    private static void validateValue(JsonReader reader, int depth) throws IOException {
        switch (reader.peek()) {
            case BEGIN_OBJECT -> {
                if (depth >= MAX_JSON_DEPTH) throw invalid();
                reader.beginObject();
                Set<String> names = new HashSet<>();
                while (reader.hasNext()) {
                    String name = reader.nextName();
                    if (!wellFormedUtf16(name) || !names.add(name)) throw invalid();
                    validateValue(reader, depth + 1);
                }
                reader.endObject();
            }
            case BEGIN_ARRAY -> {
                if (depth >= MAX_JSON_DEPTH) throw invalid();
                reader.beginArray();
                while (reader.hasNext()) validateValue(reader, depth + 1);
                reader.endArray();
            }
            case STRING -> {
                if (!wellFormedUtf16(reader.nextString())) throw invalid();
            }
            case NUMBER -> reader.nextString();
            case BOOLEAN -> reader.nextBoolean();
            case NULL -> reader.nextNull();
            default -> throw invalid();
        }
    }

    private static boolean wellFormedUtf16(String value) {
        for (int index = 0; index < value.length(); index++) {
            char current = value.charAt(index);
            if (Character.isHighSurrogate(current)) {
                if (++index >= value.length() || !Character.isLowSurrogate(value.charAt(index))) return false;
            } else if (Character.isLowSurrogate(current)) return false;
        }
        return true;
    }

    static IllegalArgumentException invalid() {
        return new IllegalArgumentException("Invalid checks capability response");
    }
}
