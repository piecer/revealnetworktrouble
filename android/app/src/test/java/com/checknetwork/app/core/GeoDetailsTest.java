package com.checknetwork.app.core;

import static org.junit.Assert.*;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class GeoDetailsTest {
    public static String fixture(String name) throws Exception {
        return new String(Files.readAllBytes(Path.of(System.getProperty("user.dir"), "..", "..", "testdata", name)), StandardCharsets.UTF_8);
    }

    @Test public void retainsProducerSidecarInsteadOfDroppingIt() throws Exception {
        Report report = ReportParser.parse(fixture("geo-details-rich-full-report.json"));
        GeoDetails details = report.geoDetails().orElseThrow();
        assertEquals(3, details.total());
        assertEquals(0, details.omitted());
        assertEquals("Different access ISP", details.entries().get(2).fields().get("isp"));
        assertEquals("cache", details.entries().get(2).source());
        assertThrows(UnsupportedOperationException.class, () -> details.entries().clear());
        assertThrows(UnsupportedOperationException.class, () -> details.entries().get(2).fields().put("isp", "changed"));
    }

    @Test public void extendedReportRejectsIllegalJsonLexemesBeforeMaterialization() throws Exception {
        org.json.JSONObject corpus = new org.json.JSONObject(fixture("geo-details-corpus.json"));
        String minimal = corpus.getJSONObject("bases").getString("minimal");
        for (String value : new String[]{"\"bad\ntext\"", "\"bad\\xtext\"", "\"bad\\'text\""}) {
            String sidecar = minimal.replace("\"city\":\"city\"", "\"city\":" + value);
            String raw = corpus.getString("report_prefix") + sidecar + corpus.getString("report_suffix");
            assertThrows(value, ReportParseException.class, () -> ReportParser.parse(raw));
        }
    }

    @Test public void decimalCountsNeverRoundFractionsOrUnderflowToValidIntegers() throws Exception {
        org.json.JSONObject corpus = new org.json.JSONObject(fixture("geo-details-corpus.json"));
        String minimal = corpus.getJSONObject("bases").getString("minimal");
        for (String value : new String[]{"1.000000000000000000000000001", "0.99999999999999999999999999", "1e-999999999", "-1e-999999999", "6200.000000000000000000001"}) {
            String sidecar = minimal.replace("\"total\":1", "\"total\":" + value);
            String raw = corpus.getString("report_prefix") + sidecar + corpus.getString("report_suffix");
            assertThrows(value, ReportParseException.class, () -> ReportParser.parse(raw));
        }
    }

    @Test public void companionPrecisionCanariesRejectBeforeAnyFloatingPointAdmission() throws Exception {
        org.json.JSONObject corpus = new org.json.JSONObject(fixture("geo-details-corpus.json"));
        String minimal = corpus.getJSONObject("bases").getString("minimal");
        for (String key : new String[]{"schema_version", "total", "omitted"}) {
            String old = "\"" + key + "\":" + (key.equals("omitted") ? "0" : "1");
            for (String number : new String[]{"1.0000000000000000000001", "6200.0000000000000001", "1e-9999", "-1e-9999",
                    "1e999999999999999999999999999999", "1e-999999999999999999999999999999"}) {
                String raw = corpus.getString("report_prefix") + minimal.replace(old, "\"" + key + "\":" + number)
                        + corpus.getString("report_suffix");
                assertThrows(key + ": " + number, ReportParseException.class, () -> ReportParser.parse(raw));
            }
        }
    }

    @Test public void hugeEquivalentZeroAndScaledOneNeedNoBigIntegerExpansion() throws Exception {
        org.json.JSONObject corpus = new org.json.JSONObject(fixture("geo-details-corpus.json"));
        String minimal = corpus.getJSONObject("bases").getString("minimal");
        for (String number : new String[]{"0e999999999999999999999999999999", "-0e-999999999999999999999999999999"}) {
            String raw = corpus.getString("report_prefix") + minimal.replace("\"omitted\":0", "\"omitted\":" + number)
                    + corpus.getString("report_suffix");
            assertEquals(0, ReportParser.parse(raw).geoDetails().orElseThrow().omitted());
        }
        String scaled = "1" + "0".repeat(20000) + "e-20000";
        String raw = corpus.getString("report_prefix") + minimal.replace("\"total\":1", "\"total\":" + scaled)
                + corpus.getString("report_suffix");
        assertEquals(1, ReportParser.parse(raw).geoDetails().orElseThrow().total());
    }

    @Test public void allFiveRealProducerWitnessesRetainFieldsAndLegacyReportsStillParse() throws Exception {
        for (String name : new String[]{"rich-full", "rich-compact", "empty-full", "empty-compact", "truncated-compact"}) {
            Report report = ReportParser.parse(fixture("geo-details-" + name + "-report.json"));
            GeoDetails details = report.geoDetails().orElseThrow();
            assertEquals(details.total(), details.entries().size() + details.omitted());
            if (name.startsWith("rich")) assertEquals(3, details.entries().size());
            if (name.startsWith("empty")) assertTrue(details.entries().isEmpty());
            if (name.startsWith("truncated")) { assertEquals(500, details.entries().size()); assertEquals(1, details.omitted()); }
            assertThrows(UnsupportedOperationException.class, () -> details.entries().clear());
            for (GeoDetails.Entry entry : details.entries()) {
                assertThrows(UnsupportedOperationException.class, () -> entry.fields().clear());
            }
        }
        assertFalse(ReportParser.parse(fixture("enrichment-upstream-report.json")).geoDetails().isPresent());
    }

    @Test public void publicPolicyMatchesEveryProducerBlockedNetworkAndCanonicalAddressRules() throws Exception {
        String go = new String(Files.readAllBytes(Path.of(System.getProperty("user.dir"), "..", "..", "backend", "diagnostic", "network_policy.go")), StandardCharsets.UTF_8);
        java.util.regex.Matcher blocked = java.util.regex.Pattern.compile("\"([0-9a-f:.]+)/(\\d+)\"").matcher(go);
        org.json.JSONObject corpus = new org.json.JSONObject(fixture("geo-details-corpus.json"));
        String minimal = corpus.getJSONObject("bases").getString("minimal");
        int checked = 0;
        while (blocked.find()) {
            String address = blocked.group(1);
            assertFalse(address, accepts(corpus, minimal.replace("8.8.8.8", address)));
            checked++;
        }
        assertEquals(34, checked);
        for (String address : new String[]{"100.63.255.255", "100.128.0.0", "172.15.255.255", "172.32.0.0", "198.17.255.255", "198.20.0.0", "223.255.255.255",
                "2001:4860:4860::8888", "2606:4700:4700::1111", "2a00:1450:4001:800::200e", "1::", "1:2:3:4:5:6:7:8", "1::2:0:0:3:4"})
            assertTrue(address, accepts(corpus, minimal.replace("8.8.8.8", address)));
        for (String address : new String[]{"100.64.0.1", "100.127.255.255", "172.31.255.255", "198.19.255.255", "240.1.1.1", "255.255.255.255", "fe80::123", "febf::1", "fdff::1", "ff02::1", "2001:1ff::1", "2001:db8::123", "::ffff:808:808", "1:0:0:2::3:4", "1:2:3:4:5:6:7::8", "1:::2", "1:2:3:4:5:6:7:08", "1:2:3:4:5:6:7:AB", "1:2:3:4:5:6:7:8%eth0"})
            assertFalse(address, accepts(corpus, minimal.replace("8.8.8.8", address)));
    }

    @Test public void timestampYearsLeapDaysAndMissingPairsHaveExactGregorianSemantics() throws Exception {
        org.json.JSONObject corpus = new org.json.JSONObject(fixture("geo-details-corpus.json"));
        String minimal = corpus.getJSONObject("bases").getString("minimal");
        for (String time : new String[]{"0001-01-01T00:00:00.000Z", "9999-12-31T23:59:59.999Z", "2000-02-29T12:34:56.789Z"}) {
            String sidecar = minimal.replace("\"city\":\"city\"", "\"fetched_at\":\"" + time + "\",\"expires_at\":\"" + time + "\"");
            assertTrue(time, accepts(corpus, sidecar));
        }
        for (String time : new String[]{"0000-01-01T00:00:00.000Z", "1900-02-29T00:00:00.000Z", "2026-04-31T00:00:00.000Z", "2026-01-01T24:00:00.000Z", "2026-01-01T00:60:00.000Z", "2026-01-01T00:00:60.000Z"}) {
            String sidecar = minimal.replace("\"city\":\"city\"", "\"fetched_at\":\"" + time + "\",\"expires_at\":\"" + time + "\"");
            assertFalse(time, accepts(corpus, sidecar));
        }
    }

    @Test public void escapedKeysDoNotBypassCountArithmeticOrDuplicateAdmission() throws Exception {
        org.json.JSONObject corpus = new org.json.JSONObject(fixture("geo-details-corpus.json"));
        String minimal = corpus.getJSONObject("bases").getString("minimal");
        String prefix = corpus.getString("report_prefix").replace("\"geo_details\":", "\"geo_\\u0064etails\":");
        String sidecar = minimal.replace("\"total\":1", "\"t\\u006ftal\":10e-1");
        assertEquals(1, ReportParser.parse(prefix + sidecar + corpus.getString("report_suffix")).geoDetails().orElseThrow().total());
        String fractional = sidecar.replace("10e-1", "1.0000000000000000000001");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(prefix + fractional + corpus.getString("report_suffix")));
        String duplicate = sidecar.replace("\"omitted\":0", "\"total\":1,\"omitted\":0");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(prefix + duplicate + corpus.getString("report_suffix")));
    }

    private static boolean accepts(org.json.JSONObject corpus, String sidecar) throws Exception {
        try { ReportParser.parse(corpus.getString("report_prefix") + sidecar + corpus.getString("report_suffix")); return true; }
        catch (ReportParseException rejected) { return false; }
    }

    @Test public void malformedPresentSidecarRejectsTheWholeProducerReport() throws Exception {
        String raw = fixture("geo-details-rich-full-report.json");
        int start = raw.indexOf("\"geo_details\":");
        assertTrue(start > 0);
        String malformed = raw.substring(0, start) + "\"geo_details\":null}";
        assertThrows(ReportParseException.class, () -> ReportParser.parse(malformed));
    }
}
