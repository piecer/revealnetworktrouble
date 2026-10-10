package com.checknetwork.app.core;

import static org.junit.Assert.*;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Base64;
import java.util.Map;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class IPContextParserTest {
    public static JSONObject corpus() throws Exception {
        return new JSONObject(new String(Files.readAllBytes(Path.of("../../testdata/ip-context-corpus.json")), java.nio.charset.StandardCharsets.UTF_8));
    }
    @Test public void lexicalAdmissionRejectsDecodedDuplicateKeysBadEscapesDeepValuesAndTinyNonzeroFractions() throws Exception {
        JSONObject row = corpus().getJSONArray("cases").getJSONObject(0);
        String source = new String(Base64.getDecoder().decode(row.getString("wire_base64")), java.nio.charset.StandardCharsets.UTF_8);
        for (String bad : new String[]{
                source.replace("\"schema_version\":1", "\"schema_version\":1,\"schema_\\u0076ersion\":1"),
                source.replace("one.example", "one\\x.example"),
                source.replace("one.example", "one\n.example"),
                source.replace("\"omitted\":0", "\"omitted\":1e-999999999999999999999"),
                source.replace("\"schema_version\":1", "\"schema_version\":1.0000000000000000000000000000000000001"),
                "{\"extra\":" + "[".repeat(2000) + "0" + "]".repeat(2000) + "}"}) {
            assertThrows(IllegalArgumentException.class, () -> IPContextParser.parse(bad.getBytes(java.nio.charset.StandardCharsets.UTF_8), "1.1.1.1"));
        }
    }
    public static Object normalize(Object value) throws Exception {
        if (value instanceof JSONObject object) {
            Map<String,Object> result = new java.util.LinkedHashMap<>();
            java.util.Iterator<String> keys = object.keys();
            while (keys.hasNext()) { String key = keys.next(); result.put(key, normalize(object.get(key))); }
            return result;
        }
        if (value instanceof org.json.JSONArray array) {
            java.util.List<Object> result = new java.util.ArrayList<>();
            for (int i = 0; i < array.length(); i++) result.add(normalize(array.get(i)));
            return result;
        }
        return value instanceof Number number ? number.longValue() : value;
    }
    @Test public void firstProducerBytesBecomeImmutableSeparateContext() throws Exception {
        JSONObject row = corpus().getJSONArray("cases").getJSONObject(0);
        Class<?> parser;
        try { parser = Class.forName("com.checknetwork.app.core.IPContextParser"); }
        catch (ClassNotFoundException absent) { fail("IP-context byte parser missing"); return; }
        Object context = parser.getMethod("parse", byte[].class, String.class).invoke(null,
                Base64.getDecoder().decode(row.getString("wire_base64")), row.getString("requested_address"));
        @SuppressWarnings("unchecked") Map<String,Object> projection = (Map<String,Object>) context.getClass().getMethod("projection").invoke(context);
        assertEquals(normalize(row.getJSONObject("projection")), projection);
        assertThrows(UnsupportedOperationException.class, () -> projection.put("address", "9.9.9.9"));
        @SuppressWarnings("unchecked") Map<String,Object> registration = (Map<String,Object>) projection.get("registration");
        assertThrows(UnsupportedOperationException.class, registration::clear);
    }
}
