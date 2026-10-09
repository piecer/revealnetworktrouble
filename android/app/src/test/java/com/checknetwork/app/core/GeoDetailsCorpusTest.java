package com.checknetwork.app.core;

import static org.junit.Assert.*;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.ParameterizedRobolectricTestRunner;

/** Every case crosses the real raw parser; never materialize/re-encode the sidecar. */
@RunWith(ParameterizedRobolectricTestRunner.class)
public final class GeoDetailsCorpusTest {
    private final int index;
    public GeoDetailsCorpusTest(int index) { this.index = index; }

    @ParameterizedRobolectricTestRunner.Parameters(name = "case-{0}")
    public static List<Object[]> cases() {
        List<Object[]> result = new ArrayList<>();
        for (int index = 0; index < 165; index++) result.add(new Object[]{index});
        return result;
    }

    @Test public void exactRawProducerCase() throws Exception {
        JSONObject corpus = new JSONObject(GeoDetailsTest.fixture("geo-details-corpus.json"));
        assertEquals(165, corpus.getJSONArray("cases").length());
        JSONObject item = corpus.getJSONArray("cases").getJSONObject(index);
        String raw = expand(corpus, item);
        boolean actual;
        try { ReportParser.parse(raw); actual = true; }
        catch (ReportParseException rejected) { actual = false; }
        JSONObject outcome = new JSONObject().put("index", index).put("name", item.getString("name"))
                .put("expected", item.getBoolean("accept")).put("actual", actual)
                .put("raw_sha256", sha256(raw));
        System.out.println("GEO_CASE " + outcome);
        assertEquals(item.getString("name"), item.getBoolean("accept"), actual);
    }

    public static String expand(JSONObject corpus, JSONObject item) throws Exception {
        String sidecar = corpus.getJSONObject("bases").getString(item.getString("base"));
        JSONArray edits = item.getJSONArray("edits");
        for (int index = 0; index < edits.length(); index++) {
            JSONObject edit = edits.getJSONObject(index);
            String old = edit.getString("old");
            int at = sidecar.indexOf(old);
            assertTrue("missing first-occurrence corpus edit " + item.getString("name"), at >= 0);
            sidecar = sidecar.substring(0, at) + edit.getString("new") + sidecar.substring(at + old.length());
        }
        return corpus.getString("report_prefix") + sidecar
                + (item.optBoolean("duplicate_root", false) ? ",\"geo_details\":" + sidecar : "")
                + corpus.getString("report_suffix");
    }

    private static String sha256(String raw) throws Exception {
        StringBuilder result = new StringBuilder();
        for (byte value : MessageDigest.getInstance("SHA-256").digest(raw.getBytes(StandardCharsets.UTF_8)))
            result.append(String.format(Locale.ROOT, "%02x", value & 255));
        return result.toString();
    }
}
