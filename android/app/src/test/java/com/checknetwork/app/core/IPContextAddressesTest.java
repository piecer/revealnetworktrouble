package com.checknetwork.app.core;

import static org.junit.Assert.*;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class IPContextAddressesTest {
    @Test public void compactAndFullAgreeWithoutDependingOnGeoSidecarOrCoordinates() throws Exception {
        List<String> expected = List.of("8.8.8.8","1.1.1.1","9.9.9.9","208.67.222.222","2606:4700:4700::1111");
        for (String variant : new String[]{"compact","full"}) {
            JSONObject raw = new JSONObject(GeoDetailsTest.fixture("geo-details-rich-"+variant+"-report.json")); raw.remove("geo_details");
            Report report = ReportParser.parse(raw.toString());
            assertFalse(report.geoDetails().isPresent());
            IPContextAddresses eligible = IPContextAddresses.from(report); assertEquals(expected, eligible.addresses()); assertEquals(0,eligible.omitted());
            assertThrows(UnsupportedOperationException.class, () -> eligible.addresses().add("8.8.4.4"));
        }
    }
    @Test public void responsiveMissingLatencyIsNotLostButFailureAndUnknownNeverBecomeCandidates() throws Exception {
        assertEquals(List.of("8.8.8.8","9.9.9.9"), IPContextAddresses.from(ReportParser.parse(GeoDetailsTest.fixture("full-observation-integrity-report.json"))).addresses());
        for (String fixture : new String[]{"traceroute-failed-reached-parse-full-report.json","traceroute-command-failure-full-report.json","traceroute-timeout-full-report.json"}) {
            assertTrue(fixture, IPContextAddresses.from(ReportParser.parse(GeoDetailsTest.fixture(fixture))).addresses().isEmpty());
        }
        assertEquals(List.of("8.8.8.8"), IPContextAddresses.from(ReportParser.parse(GeoDetailsTest.fixture("trace-observation-later-DNS-failure-full.json"))).addresses());
    }
    @Test public void canonicalPublicPolicyExcludesPrivateMappedReservedAndMalformedWithoutNormalizing() throws Exception {
        String raw = GeoDetailsTest.fixture("enrichment-upstream-report.json");
        for (String value : new String[]{"10.0.0.1", "127.0.0.1", "192.0.2.1", "100.64.0.1", "169.254.1.1", "2001:db8::1", "::ffff:8.8.8.8", "08.8.8.8", "8.8.8.8 ", "Unknown"}) {
            // Remove auxiliary metadata; ReportParser still validates the full real result shape.
            JSONObject document = new JSONObject(raw.replace("8.8.8.8", value));
            IPContextAddresses eligible = IPContextAddresses.from(ReportParser.parse(document.toString()));
            assertFalse(value, eligible.addresses().contains(value)); assertEquals(List.of("1.1.1.1"), eligible.addresses());
        }
    }
    @Test public void fullProjectionKeeps500UniqueWithExactOmittedAccounting() throws Exception {
        IPContextAddresses addresses = IPContextAddresses.from(ReportParser.parse(largeFullReport()));
        assertEquals(500,addresses.addresses().size()); assertEquals(100,addresses.omitted());
        assertEquals(500,new java.util.HashSet<>(addresses.addresses()).size());
        assertEquals("11.0.0.1",addresses.addresses().get(0)); assertEquals("11.0.1.250",addresses.addresses().get(499));
    }
    /** Bounded synthetic report test fixture; parsed by the real ReportParser, not a producer witness. */
    public static String largeFullReport() throws Exception {
        JSONObject report = new JSONObject(GeoDetailsTest.fixture("enrichment-upstream-report.json"));
        report.remove("analysis"); report.remove("geo_details"); JSONArray results = new JSONArray();
        JSONObject template = report.getJSONArray("results").getJSONObject(0);
        for (int r = 0; r < 20; r++) {
            JSONObject result = new JSONObject(template.toString()); JSONArray nodes = new JSONArray(), links = new JSONArray();
            nodes.put(new JSONObject().put("id","hop-0").put("hop",0).put("address","local").put("status","healthy"));
            String destination = "";
            for (int hop = 1; hop <= 30; hop++) {
                int n = r * 30 + hop - 1; destination = "11.0." + n / 250 + "." + (n % 250 + 1);
                nodes.put(new JSONObject().put("id","hop-"+hop).put("hop",hop).put("address",destination).put("status","healthy").put("latency_ms",hop));
                links.put(new JSONObject().put("from","hop-"+(hop-1)).put("to","hop-"+hop).put("status","healthy"));
            }
            JSONObject topology = new JSONObject().put("reached",true).put("nodes",nodes).put("links",links);
            JSONObject details = result.getJSONObject("details"); details.remove("geoip_enrichment");
            details.put("topology",topology).put("attempts",new JSONArray().put(new JSONObject().put("attempt",1).put("status","healthy").put("topology",topology)));
            result.put("address",destination); results.put(result);
        }
        report.put("results",results).put("summary",new JSONObject().put("total",20).put("passed",20).put("failed",0));
        return report.toString();
    }
}
