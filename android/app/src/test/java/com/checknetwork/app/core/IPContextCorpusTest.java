package com.checknetwork.app.core;

import static org.junit.Assert.*;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.Base64;
import java.util.List;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.ParameterizedRobolectricTestRunner;
import org.robolectric.annotation.Config;

/** Identical unchanged bytes and producer-derived projection oracle; not an independent oracle. */
@RunWith(ParameterizedRobolectricTestRunner.class)
@Config(sdk = 35)
public final class IPContextCorpusTest {
    private final int index;
    public IPContextCorpusTest(int index) { this.index = index; }
    @ParameterizedRobolectricTestRunner.Parameters(name = "case-{0}")
    public static List<Object[]> cases() {
        List<Object[]> cases = new ArrayList<>();
        for (int i = 0; i < 192; i++) cases.add(new Object[]{i});
        return cases;
    }
    @Test public void identicalWireAndProjection() throws Exception {
        byte[] corpusBytes = Files.readAllBytes(Paths.get("../../testdata/ip-context-corpus.json"));
        assertEquals("5976737708e238833b2491f707c7f2365ca73193397407b7b5cda89b6f3a3843", sha(corpusBytes));
        JSONObject corpus = IPContextParserTest.corpus(); assertEquals(192, corpus.getJSONArray("cases").length());
        JSONObject row = corpus.getJSONArray("cases").getJSONObject(index);
        byte[] wire = Base64.getDecoder().decode(row.getString("wire_base64"));
        IPContext actual = null;
        try { actual = IPContextParser.parse(wire, row.getString("requested_address")); }
        catch (IllegalArgumentException rejected) { assertEquals("Invalid IP context response", rejected.getMessage()); }
        boolean accepted = actual != null;
        boolean projection = !accepted || row.has("projection") && IPContextParserTest.normalize(row.getJSONObject("projection")).equals(actual.projection());
        System.out.println("IP_CONTEXT_CASE " + new JSONObject().put("id", row.getString("id")).put("index", index)
                .put("origin", row.getString("origin")).put("wire_sha256", sha(wire))
                .put("accepted", accepted).put("projection_equal", projection).put("oracle", "producer-derived"));
        assertEquals(row.getString("id"), row.getBoolean("accept"), accepted);
        assertTrue(row.getString("id"), projection);
    }
    private static String sha(byte[] bytes) throws Exception {
        StringBuilder out = new StringBuilder();
        for (byte b : MessageDigest.getInstance("SHA-256").digest(bytes)) out.append(String.format(java.util.Locale.ROOT, "%02x", b & 255));
        return out.toString();
    }
}
