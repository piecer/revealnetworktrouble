package com.checknetwork.app.core;

import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.time.format.ResolverStyle;
import java.util.Arrays;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** Closed v1 metadata contract over original UTF-8 bytes, bound to the captured request IP. */
public final class IPContextParser {
    public static final int MAX_BYTES = 16384;
    private static final DateTimeFormatter TIME = DateTimeFormatter.ofPattern("uuuu-MM-dd'T'HH:mm:ss.SSS'Z'", java.util.Locale.ROOT).withResolverStyle(ResolverStyle.STRICT);
    private IPContextParser() {}
    public static boolean isEligible(String address) { return GeoAddressPolicy.isCanonicalPublic(address); }
    public static IPContext parse(byte[] bytes, String requestedAddress) {
        if (!isEligible(requestedAddress)) throw invalid();
        Map<String,Object> root = object(IPContextJson.parse(bytes), "schema_version address source fetched_at expires_at reverse_dns registration routing", "");
        if (!requestedAddress.equals(root.get("address")) || integer(root.get("schema_version"), 1) != 1) throw invalid();
        member(root.get("source"), "upstream cache");
        Instant fetched = stamp(root.get("fetched_at")), expires = stamp(root.get("expires_at"));
        boolean stable = reverse(root.get("reverse_dns"), fetched);
        stable &= registration(root.get("registration"), fetched, requestedAddress);
        stable &= routing(root.get("routing"), fetched, requestedAddress);
        if (!fetched.plusMillis(stable ? 300000 : 30000).equals(expires)) throw invalid();
        if (encodedSize(root) + 1 > MAX_BYTES) throw invalid();
        return new IPContext(root);
    }
    private static boolean reverse(Object value, Instant fetched) {
        Map<String,Object> bundle = object(value, "status source fetched_at names omitted", "");
        timed(bundle, "fetched_at", "system_resolver", fetched);
        String status = member(bundle.get("status"), "ok limited not_found timeout unavailable invalid_response");
        List<?> names = array(bundle.get("names"), 8);
        countShape(status, names.size(), integer(bundle.get("omitted"), 56), 8);
        boolean stable = Set.of("ok", "limited", "not_found").contains(status);
        String previous = "";
        for (Object item : names) {
            Map<String,Object> name = object(item, "name forward_status", "");
            String text = string(name.get("name"));
            if (text.length() > 253 || text.compareTo(previous) <= 0) throw invalid();
            for (String label : text.split("\\.", -1)) {
                if (label.length() == 0 || label.length() > 63 || !label.matches("[a-z0-9_-]+") || label.startsWith("-") || label.endsWith("-")) throw invalid();
            }
            previous = text;
            String forward = member(name.get("forward_status"), "confirmed mismatch not_found timeout unavailable invalid_response limited");
            stable &= Set.of("confirmed", "mismatch", "not_found", "limited").contains(forward);
        }
        return stable;
    }
    private static boolean registration(Object value, Instant fetched, String requested) {
        String facts = "start_address end_address handle name type country organization registered_at updated_at";
        Map<String,Object> bundle = object(value, "status source fetched_at", "registry " + facts);
        timed(bundle, "fetched_at", "rdap", fetched);
        String status = member(bundle.get("status"), "ok not_found unavailable timeout rate_limited invalid_response");
        if (bundle.containsKey("registry")) member(bundle.get("registry"), "arin apnic ripe lacnic afrinic");
        if (!status.equals("ok")) {
            for (String key : facts.split(" ")) if (bundle.containsKey(key)) throw invalid();
        } else {
            byte[] start = canonical(bundle.get("start_address")), end = canonical(bundle.get("end_address")), query = canonical(requested);
            if (start.length != query.length || end.length != query.length || compare(start, query) > 0 || compare(end, query) < 0) throw invalid();
            int total = 0;
            for (String key : new String[]{"handle", "name", "type", "country", "organization"}) {
                if (!bundle.containsKey(key)) continue;
                String text = string(bundle.get(key)); int size = text.getBytes(StandardCharsets.UTF_8).length;
                if (size == 0 || size > 256) throw invalid();
                if (key.equals("country") && !text.matches("[A-Z]{2}")) throw invalid();
                total += size;
            }
            if (total > 1536) throw invalid();
            for (String key : new String[]{"registered_at", "updated_at"}) if (bundle.containsKey(key)) stamp(bundle.get(key));
        }
        return status.equals("ok") || status.equals("not_found");
    }
    private static boolean routing(Object value, Instant fetched, String requested) {
        Map<String,Object> bundle = object(value, "status source fetched_at origins omitted", "prefix");
        timed(bundle, "fetched_at", "ripe_ris", fetched);
        String status = member(bundle.get("status"), "ok limited not_found timeout unavailable rate_limited invalid_response");
        List<?> origins = array(bundle.get("origins"), 4);
        countShape(status, origins.size(), integer(bundle.get("omitted"), 60), 4);
        if (status.equals("ok") || status.equals("limited")) prefix(bundle.get("prefix"), requested);
        else if (bundle.containsKey("prefix")) throw invalid();
        boolean stable = Set.of("ok", "limited", "not_found").contains(status);
        long previous = 0;
        for (Object item : origins) {
            Map<String,Object> origin = object(item, "asn rpki", "");
            long asn = integer(origin.get("asn"), 4294967295L);
            if (asn <= previous) throw invalid(); previous = asn;
            Map<String,Object> rpki = object(origin.get("rpki"), "status source checked_at", "validity");
            timed(rpki, "checked_at", "ripe_rpki", fetched);
            String rpkiStatus = member(rpki.get("status"), "ok timeout unavailable rate_limited invalid_response");
            if (rpkiStatus.equals("ok")) member(rpki.get("validity"), "valid invalid_asn invalid_length unknown");
            else if (rpki.containsKey("validity")) throw invalid();
            stable &= rpkiStatus.equals("ok");
        }
        return stable;
    }
    private static void prefix(Object raw, String requested) {
        String text = string(raw); String[] parts = text.split("/", -1);
        if (parts.length != 2 || !parts[1].matches("0|[1-9][0-9]{0,2}")) throw invalid();
        byte[] network = canonical(parts[0]), query = canonical(requested);
        int bits = Integer.parseInt(parts[1]);
        if (network.length != query.length || bits > network.length * 8) throw invalid();
        for (int i = 0; i < network.length * 8; i++) {
            int mask = 1 << (7 - i % 8);
            if (i < bits ? (network[i / 8] & mask) != (query[i / 8] & mask) : (network[i / 8] & mask) != 0) throw invalid();
        }
    }
    private static byte[] canonical(Object raw) {
        String text = string(raw);
        if (text.length() > 39) throw invalid();
        byte[] bytes = GeoAddressPolicy.parse(text);
        if (bytes == null) throw invalid();
        if (bytes.length == 16) {
            if (!GeoAddressPolicy.canonicalV6(bytes).equals(text)) throw invalid();
            boolean mapped = bytes[10] == (byte) 255 && bytes[11] == (byte) 255;
            for (int i = 0; i < 10; i++) mapped &= bytes[i] == 0;
            if (mapped) throw invalid();
        }
        return bytes;
    }
    private static int compare(byte[] a, byte[] b) {
        for (int i = 0; i < a.length; i++) if (a[i] != b[i]) return Integer.compare(a[i] & 255, b[i] & 255);
        return 0;
    }
    private static void timed(Map<String,Object> value, String key, String source, Instant root) {
        if (!source.equals(value.get("source")) || stamp(value.get(key)).isAfter(root)) throw invalid();
    }
    private static Instant stamp(Object raw) {
        String text = string(raw);
        if (!text.matches("[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\\.[0-9]{3}Z") || text.startsWith("0000")) throw invalid();
        try { return LocalDateTime.parse(text, TIME).toInstant(ZoneOffset.UTC); }
        catch (java.time.DateTimeException failure) { throw invalid(); }
    }
    private static void countShape(String status, int count, long omitted, int max) {
        boolean valid = switch (status) {
            case "ok" -> count >= 1 && count <= max && omitted == 0;
            case "limited" -> count == max && omitted > 0;
            default -> count == 0 && omitted == 0;
        };
        if (!valid) throw invalid();
    }
    private static long integer(Object value, long max) { if (!(value instanceof Long n) || n < 0 || n > max) throw invalid(); return n; }
    private static String string(Object value) { if (!(value instanceof String text)) throw invalid(); return text; }
    private static String member(Object value, String allowed) {
        String text = string(value); if (!Arrays.asList(allowed.split(" ")).contains(text)) throw invalid(); return text;
    }
    private static List<?> array(Object value, int max) { if (!(value instanceof List<?> list) || list.size() > max) throw invalid(); return list; }
    private static Map<String,Object> object(Object value, String required, String optional) {
        if (!(value instanceof Map<?,?> map)) throw invalid();
        Set<String> allowed = new HashSet<>(Arrays.asList(required.split(" ")));
        for (String key : allowed) if (!map.containsKey(key)) throw invalid();
        if (!optional.isEmpty()) allowed.addAll(Arrays.asList(optional.split(" ")));
        for (Object key : map.keySet()) if (!allowed.contains(key) || map.get(key) == null) throw invalid();
        @SuppressWarnings("unchecked") Map<String,Object> result = (Map<String,Object>) map; return result;
    }
    /** Size of canonical Go encoding/json representation, including HTML-sensitive escaping. */
    private static int encodedSize(Object value) {
        if (value instanceof Map<?,?> map) {
            int n = 2; for (Map.Entry<?,?> entry : map.entrySet()) n += encodedSize(entry.getKey()) + 1 + encodedSize(entry.getValue()) + 1;
            return n - (map.isEmpty() ? 0 : 1);
        }
        if (value instanceof List<?> list) { int n = 2; for (Object item : list) n += encodedSize(item) + 1; return n - (list.isEmpty() ? 0 : 1); }
        if (value instanceof String text) {
            int n = 2;
            for (int i = 0; i < text.length();) {
                int c = text.codePointAt(i); i += Character.charCount(c);
                if (c == '"' || c == '\\' || c == '\n' || c == '\r' || c == '\t' || c == '\b' || c == '\f') n += 2;
                else if (c < 32 || c == '<' || c == '>' || c == '&' || c == 0x2028 || c == 0x2029) n += 6;
                else n += c <= 127 ? 1 : c <= 2047 ? 2 : c <= 65535 ? 3 : 4;
            }
            return n;
        }
        return value.toString().length();
    }
    private static IllegalArgumentException invalid() { return IPContextJson.invalid(); }
}
