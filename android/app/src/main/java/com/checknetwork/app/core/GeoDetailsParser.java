package com.checknetwork.app.core;

import android.util.JsonReader;
import android.util.JsonToken;
import java.io.IOException;
import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.time.format.ResolverStyle;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

/** Sidecar-only semantics after ReportParser's whole-wire duplicate/depth/resource preflight. */
final class GeoDetailsParser {
    private static final List<String> TEXT = List.of("city", "region", "country", "country_code",
            "continent", "continent_code", "region_code", "postal", "timezone", "isp", "network_domain");
    private static final Set<String> REQUIRED = Set.of("address", "provider", "source");
    private static final DateTimeFormatter TIMESTAMP = DateTimeFormatter
            .ofPattern("uuuu-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.ROOT).withResolverStyle(ResolverStyle.STRICT);

    private GeoDetailsParser() {}

    static GeoDetails parse(String raw) {
        // Read numeric lexemes directly: JSONObject may already have rounded decimal counts.
        try (JsonReader reader = new JsonReader(new StringReader(raw))) {
            reader.setLenient(false);
            reader.beginObject();
            while (reader.hasNext()) {
                if (reader.nextName().equals("geo_details")) return sidecar(reader);
                reader.skipValue();
            }
            throw invalid();
        } catch (IOException | IllegalStateException | NumberFormatException ex) {
            throw invalid();
        }
    }

    private static GeoDetails sidecar(JsonReader reader) throws IOException {
        require(reader, JsonToken.BEGIN_OBJECT);
        reader.beginObject();
        Set<String> keys = new HashSet<>();
        int version = -1, total = -1, omitted = -1;
        List<GeoDetails.Entry> entries = null;
        while (reader.hasNext()) {
            String key = reader.nextName();
            if (!keys.add(key)) throw invalid();
            switch (key) {
                case "schema_version" -> version = count(reader, 1);
                case "total" -> total = count(reader, 6200);
                case "omitted" -> omitted = count(reader, 6200);
                case "entries" -> entries = entries(reader);
                default -> throw invalid();
            }
        }
        reader.endObject();
        if (version != 1 || total < 0 || omitted < 0 || entries == null
                || total != entries.size() + omitted) throw invalid();
        GeoDetails result = new GeoDetails(total, omitted, entries);
        if (canonicalBytes(result) > 131072) throw invalid();
        return result;
    }

    private static int count(JsonReader reader, int max) throws IOException {
        require(reader, JsonToken.NUMBER);
        return GeoDetailsNumbers.boundedInt(reader.nextString(), max);
    }

    private static List<GeoDetails.Entry> entries(JsonReader reader) throws IOException {
        require(reader, JsonToken.BEGIN_ARRAY);
        reader.beginArray();
        List<GeoDetails.Entry> entries = new ArrayList<>();
        String previous = "";
        while (reader.hasNext()) {
            if (entries.size() == 500) throw invalid();
            GeoDetails.Entry entry = entry(reader);
            if (entry.address().compareTo(previous) <= 0) throw invalid();
            previous = entry.address();
            entries.add(entry);
        }
        reader.endArray();
        return entries;
    }

    private static GeoDetails.Entry entry(JsonReader reader) throws IOException {
        require(reader, JsonToken.BEGIN_OBJECT);
        reader.beginObject();
        Map<String, String> fields = new LinkedHashMap<>();
        int textBytes = 0;
        while (reader.hasNext()) {
            String key = reader.nextName();
            if (fields.containsKey(key) || !(REQUIRED.contains(key) || TEXT.contains(key)
                    || key.equals("fetched_at") || key.equals("expires_at"))) throw invalid();
            require(reader, JsonToken.STRING);
            String value = reader.nextString();
            int bytes = value.getBytes(StandardCharsets.UTF_8).length;
            if (value.isEmpty() || bytes > 256) throw invalid();
            if (TEXT.contains(key)) textBytes += bytes;
            fields.put(key, value);
        }
        reader.endObject();
        if (!fields.keySet().containsAll(REQUIRED) || textBytes > 1536
                || !"ipwho.is".equals(fields.get("provider"))
                || !Set.of("upstream", "cache").contains(fields.get("source"))
                || !GeoAddressPolicy.isCanonicalPublic(fields.get("address"))) throw invalid();
        String fetched = fields.get("fetched_at"), expires = fields.get("expires_at");
        if ((fetched == null) != (expires == null)) throw invalid();
        if (fetched != null && (timestamp(expires).isBefore(timestamp(fetched)))) throw invalid();
        return new GeoDetails.Entry(fields);
    }

    private static LocalDateTime timestamp(String text) {
        if (!text.matches("[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\\.[0-9]{3}Z")
                || text.startsWith("0000")) throw invalid();
        try { return LocalDateTime.parse(text, TIMESTAMP); }
        catch (java.time.DateTimeException ex) { throw invalid(); }
    }

    /** Go encoding/json standalone length, not raw input length or JSONObject serialization. */
    private static int canonicalBytes(GeoDetails sidecar) {
        int bytes = ("{\"schema_version\":1,\"total\":" + sidecar.total() + ",\"omitted\":"
                + sidecar.omitted() + ",\"entries\":[]}").length();
        boolean firstEntry = true;
        for (GeoDetails.Entry entry : sidecar.entries()) {
            if (!firstEntry) bytes++;
            firstEntry = false;
            bytes += 2;
            boolean firstField = true;
            for (Map.Entry<String, String> field : entry.fields().entrySet()) {
                if (!firstField) bytes++;
                firstField = false;
                bytes += goStringBytes(field.getKey()) + 1 + goStringBytes(field.getValue());
            }
        }
        return bytes;
    }

    private static int goStringBytes(String value) {
        int bytes = 2;
        for (int index = 0; index < value.length(); index++) {
            char c = value.charAt(index);
            if (c == '"' || c == '\\' || c == '\b' || c == '\f' || c == '\n' || c == '\r' || c == '\t') bytes += 2;
            else if (c < 0x20 || c == '<' || c == '>' || c == '&' || c == '\u2028' || c == '\u2029') bytes += 6;
            else if (c < 0x80) bytes++;
            else if (c < 0x800) bytes += 2;
            else if (Character.isHighSurrogate(c)) { bytes += 4; index++; }
            else bytes += 3;
        }
        return bytes;
    }

    /** Only extended reports opt into this stricter lexical contract; legacy admission is unchanged. */
    static void validateLexemes(String json) {
        for (int index = 0; index < json.length();) {
            char c = json.charAt(index++);
            if (c == '"') {
                boolean closed = false;
                while (index < json.length()) {
                    c = json.charAt(index++);
                    if (c == '"') { closed = true; break; }
                    if (c < 0x20) throw invalid();
                    if (c != '\\') continue;
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
            } else if (!delimiter(c)) {
                int start = index - 1;
                while (index < json.length() && json.charAt(index) != '"' && !delimiter(json.charAt(index))) index++;
                if (!JSON_LITERAL.matcher(json).region(start, index).matches()) throw invalid();
            }
        }
    }

    private static final java.util.regex.Pattern JSON_LITERAL = java.util.regex.Pattern.compile(
            "(?:true|false|null|-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)");

    private static boolean delimiter(char c) {
        return c == '{' || c == '}' || c == '[' || c == ']' || c == ':' || c == ','
                || c == ' ' || c == '\t' || c == '\r' || c == '\n';
    }

    private static void require(JsonReader reader, JsonToken token) throws IOException {
        if (reader.peek() != token) throw invalid();
    }

    private static ReportParseException invalid() {
        return new ReportParseException("report.geo_details: invalid supplemental metadata");
    }
}
