package com.checknetwork.app.core;

import static org.junit.Assert.*;

import java.lang.reflect.InvocationTargetException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Random;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class CheckCapabilitiesPreflightTest {
    private static final String BASE = "{\"kinds\":[\"dns\",\"traceroute\",\"future\"],"
            + "\"topology_modes\":[\"full\",\"compact\",\"future\"],\"limits\":{"
            + "\"max_targets\":2,\"max_traceroute_attempts\":3,"
            + "\"timeout_ms_min\":500,\"timeout_ms_max\":2000}}";

    @Test public void frozenPredecessorMatchesEveryOutcomeAndProjection() throws Exception {
        List<ReportRequest> requests = requests();
        int accepted = 0;
        int rejected = 0;
        List<String> corpus = corpus();
        for (int index = 0; index < corpus.size(); index++) {
            String json = corpus.get(index);
            List<Object> before = outcome(FrozenCheckCapabilities.class, json, requests);
            List<Object> after = outcome(CheckCapabilities.class, json, requests);
            assertEquals("corpus case " + index, before, after);
            if (before.get(0).equals("accepted")) accepted++;
            else rejected++;
        }
        assertTrue("valid corpus coverage", accepted > 200);
        assertTrue("hostile corpus coverage", rejected > 200);
        System.out.println("STAGE5_PARITY cases=" + corpus.size() + " accepted=" + accepted
                + " rejected=" + rejected + " requestProjectionsPerAccepted=" + requests.size());
    }

    @Test public void unknownFieldsPreserveDecodedNameScopeAndValidUnicode() {
        for (String value : List.of("{\"x\":1,\"X\":2}", "[{\"x\":1},{\"x\":2}]",
                "{\"\":0}", "{\"\\u0000\":\"\\u0000\"}",
                "{\"🌐\":\"\\uD83C\\uDF10\"}", "1e-9999")) {
            CheckCapabilities result = CheckCapabilities.parse(unknown(value));
            assertEquals(CheckCapabilities.parse(BASE).kinds(), result.kinds());
            assertEquals(2, result.maxTargets());
            assertEquals(500, result.minTimeoutMs());
            assertEquals(2000, result.maxTimeoutMs());
        }
        invalid(unknown("{\"🌐\":0,\"\\uD83C\\uDF10\":1}"));
        invalid(unknown("{\"\\u0000\":0,\"\\u0000\":1}"));
    }

    @Test public void capabilityListAndUtf8NameLimitsRemainInclusive() {
        CheckCapabilities.parse(BASE.replace("\"future\"", "\"" + "🌐".repeat(32) + "\""));
        invalid(BASE.replace("\"future\"", "\"" + "🌐".repeat(32) + "x\""));
        String entries = "[" + "\"dns\",".repeat(255) + "\"dns\"]";
        CheckCapabilities.parse(BASE.replace("[\"dns\",\"traceroute\",\"future\"]", entries));
        invalid(BASE.replace("[\"dns\",\"traceroute\",\"future\"]",
                entries.replace("]", ",\"dns\"]")));
    }

    @Test public void preflightAdmitsAnObjectAtItsExactUtf8BudgetWithoutCapabilitySemantics() {
        String json = "{\"future\":\"café 🌐\"}";
        int bytes = json.getBytes(StandardCharsets.UTF_8).length;
        CapabilityJsonPreflight.validate(json, bytes);
        invalidPreflight(json, bytes - 1);
        invalid(json); // Syntactic admission does not promise a capability document.
    }

    @Test public void preflightRequiresExactlyOneObject() {
        CapabilityJsonPreflight.validate("{}", 2);
        CapabilityJsonPreflight.validate(" \r\n\t{} \r\n\t", 10);
        for (String json : Arrays.asList(null, "", "[]", "null", "true", "0", "\"x\"", "{}{}", "{} null"))
            invalidPreflight(json, CheckCapabilities.MAX_JSON_BYTES);
        invalidPreflight("{}", 1);
    }

    @Test public void preflightEnforcesThePublicByteLimitBeforeWalkingHostileNesting() {
        assertEquals(65_536, CheckCapabilities.MAX_JSON_BYTES);
        String json = "{\"unknown\":\"🌐\"}";
        String exact = json + " ".repeat(65_536 - json.getBytes(StandardCharsets.UTF_8).length);
        CapabilityJsonPreflight.validate(exact, CheckCapabilities.MAX_JSON_BYTES);
        invalidPreflight(exact + " ", CheckCapabilities.MAX_JSON_BYTES);
        // Far deeper than a materializing recursive parser can handle, within the byte budget.
        invalidPreflight("{\"x\":" + "[".repeat(10_000) + "0" + "]".repeat(10_000) + "}", 65_536);
    }

    @Test public void preflightCountsTheRootInTheDepthLimit() {
        for (int depth : new int[]{6, 7}) {
            CapabilityJsonPreflight.validate(unknown("[".repeat(depth) + "0" + "]".repeat(depth)), 65_536);
            CapabilityJsonPreflight.validate(unknown("{\"x\":".repeat(depth) + "0" + "}".repeat(depth)), 65_536);
        }
        invalidPreflight(unknown("[".repeat(8) + "0" + "]".repeat(8)), 65_536);
        invalidPreflight(unknown("{\"x\":".repeat(8) + "0" + "}".repeat(8)), 65_536);
    }

    @Test public void preflightRejectsDecodedDuplicatesWithoutConfusingSiblingScopes() {
        for (String json : List.of("{\"x\":0,\"x\":1}", "{\"x\":0,\"\\u0078\":1}",
                "{\"unknown\":[{\"x\":0,\"\\u0078\":1}]}",
                "{\"🌐\":0,\"\\uD83C\\uDF10\":1}")) invalidPreflight(json, 65_536);
        CapabilityJsonPreflight.validate("{\"unknown\":[{\"x\":0},{\"x\":1}]}", 65_536);
        CapabilityJsonPreflight.validate("{\"x\":0,\"X\":1}", 65_536);
    }

    @Test public void preflightChecksLexemesAndUtf16InUnknownFields() {
        for (String value : List.of("TRUE", "Null", "01", "+1", "1.", "1e+", "NaN", "\"\\q\"",
                "\"\\u+123\"", "\"\\uD800\"", "\"\\uDC00\"", "{\"\\uD800\":0}",
                "\"" + (char) 0xd800 + "\"", "\"" + (char) 0xdc00 + "\""))
            invalidPreflight(unknown(value), 65_536);
        for (int control = 0; control < 32; control++)
            invalidPreflight(unknown("\"" + (char) control + "\""), 65_536);
        for (String value : List.of("null", "true", "false", "-0", "1.25e-2", "\"🌐\"",
                "\"\\uD83C\\uDF10\"", "\"\\b\\f\\n\\r\\t\\u0000\\\"\\\\\\/\""))
            CapabilityJsonPreflight.validate(unknown(value), 65_536);
    }

    private static void invalidPreflight(String json, int maxBytes) {
        IllegalArgumentException error = assertThrows(IllegalArgumentException.class,
                () -> CapabilityJsonPreflight.validate(json, maxBytes));
        assertEquals(IllegalArgumentException.class, error.getClass());
        assertEquals("Invalid checks capability response", error.getMessage());
    }

    private static void invalid(String json) {
        IllegalArgumentException error = assertThrows(IllegalArgumentException.class,
                () -> CheckCapabilities.parse(json));
        assertEquals(IllegalArgumentException.class, error.getClass());
        assertEquals("Invalid checks capability response", error.getMessage());
    }

    private static String unknown(String value) {
        return BASE.substring(0, BASE.length() - 1) + ",\"unknown\":" + value + "}";
    }

    private static List<String> corpus() {
        List<String> values = new ArrayList<>(Arrays.asList(null, "", "{}", "[]", "null", "true", "0", BASE));
        for (String atom : List.of("null", "true", "false", "0", "-0", "1.0", "1e+20", "-2E-3",
                "1e-9999", "1e9999", "9223372036854775808", "\"\"", "\"café 🌐\"",
                "\"\\uD83C\\uDF10\"", "\"\\b\\f\\n\\r\\t\\u0000\\\"\\\\\\/\"",
                "[]", "{}", "[1,true,null,\"x\",{}]", "{\"x\":0,\"X\":1}",
                "[{\"x\":0},{\"x\":1}]", "{\"\":0}", "{\"\\u0000\":0}")) values.add(unknown(atom));
        for (String atom : List.of("TRUE", "True", "FALSE", "False", "NULL", "Null", "+1", "01",
                "-01", ".1", "1.", "1e", "1e+", "NaN", "Infinity", "0x20", "undefined",
                "[1,]", "[,1]", "[1,,2]", "{\"x\":1,}", "{x:1}", "{'x':1}",
                "{\"x\":1,\"x\":2}", "{\"x\":1,\"\\u0078\":2}",
                "{\"🌐\":0,\"\\uD83C\\uDF10\":1}", "{\"\\u0000\":0,\"\\u0000\":1}",
                "\"\\q\"", "\"\\'\"", "\"\\v\"", "\"\\0\"", "\"\\x41\"",
                "\"\\u+123\"", "\"\\u-123\"", "\"\\u12G4\"", "\"\\u１２３４\"")) values.add(unknown(atom));
        for (int control = 0; control < 32; control++) {
            values.add(unknown("\"before" + (char) control + "after\""));
            values.add(BASE + (char) control);
        }
        for (String lone : List.of("\\uD800", "\\uDC00", "\\uD800x", "\\uDC00\\uD800",
                String.valueOf((char) 0xd800), String.valueOf((char) 0xdc00))) {
            values.add(unknown("\"" + lone + "\""));
            values.add(unknown("{\"" + lone + "\":0}"));
            values.add(BASE.replace("\"dns\"", "\"" + lone + "\""));
        }
        for (int depth : new int[]{0, 1, 6, 7, 8, 9, 64, 10_000}) {
            values.add(unknown("[".repeat(depth) + "0" + "]".repeat(depth)));
            values.add(unknown("{\"x\":".repeat(depth) + "0" + "}".repeat(depth)));
        }
        values.add(unknown("['\"'," + "[".repeat(64) + "0" + "]".repeat(64) + ",'\"']"));
        for (String padding : List.of(" ", "\t", "\r", "\n", "\f", "\ufeff", "//comment", "/*comment*/")) {
            values.add(padding + BASE);
            values.add(BASE + padding);
        }
        for (String tail : List.of("{}", "null", "true", "garbage", "[]")) values.add(BASE + tail);
        values.add(BASE.replace('"', '\''));
        values.add(BASE.replace(",", ";"));
        values.add(BASE.replace("\"kinds\":", "\"kinds\"=>"));
        values.add(BASE.replace("\"kinds\":", "\"kinds\":[],\"kinds\":"));
        values.add(BASE.replace("\"kinds\":", "\"kinds\":[],\"k\\u0069nds\":"));
        values.add(BASE.replace("\"kinds\"", "\"k\\u0069nds\""));
        for (String number : List.of("0", "1", "20", "1000000", "1000001", "-1", "1.0", "1e0",
                "true", "null", "\"1\"", "[]", "{}", "9223372036854775808")) {
            values.add(BASE.replace("\"max_targets\":2", "\"max_targets\":" + number));
            values.add(BASE.replace("\"max_traceroute_attempts\":3", "\"max_traceroute_attempts\":" + number));
            values.add(BASE.replace("\"timeout_ms_min\":500", "\"timeout_ms_min\":" + number));
            values.add(BASE.replace("\"timeout_ms_max\":2000", "\"timeout_ms_max\":" + number));
        }
        for (String key : List.of("kinds", "topology_modes", "limits", "max_targets", "timeout_ms_min")) {
            values.add(BASE.replace("\"" + key + "\"", "\"missing\""));
        }
        for (String names : List.of("[]", "[\"dns\"]", "[\"traceroute\"]", "[\"future\"]", "[\"\"]",
                "[0]", "[null]", "[true]", "{}", "null", "\"dns\"")) {
            values.add(BASE.replace("[\"dns\",\"traceroute\",\"future\"]", names));
            values.add(BASE.replace("[\"full\",\"compact\",\"future\"]", names));
        }
        for (int size : new int[]{255, 256, 257}) {
            values.add(BASE.replace("[\"dns\",\"traceroute\",\"future\"]",
                    "[" + "\"dns\",".repeat(size - 1) + "\"dns\"]"));
        }
        for (int size : new int[]{127, 128, 129}) {
            values.add(BASE.replace("\"future\"", "\"" + "x".repeat(size) + "\""));
        }
        String unicode = unknown("\"café 🌐\"");
        int bytes = unicode.getBytes(StandardCharsets.UTF_8).length;
        for (int cap : new int[]{65_535, 65_536, 65_537}) values.add(unicode + " ".repeat(cap - bytes));
        values.add(unknown("\"" + "🌐".repeat(16_384) + "\""));
        Random random = new Random(0x5A17L);
        for (int index = 0; index < 250; index++) values.add(unknown(randomValue(random, 0)));
        // Every prefix and one-character deletion; fixed substitutions expose parser recovery differences.
        for (int index = 0; index < BASE.length(); index++) {
            values.add(BASE.substring(0, index));
            values.add(BASE.substring(0, index) + BASE.substring(index + 1));
            for (char replacement : new char[]{'\'', '\\', '\u0000', ',', ' ', '[', '9'}) {
                values.add(BASE.substring(0, index) + replacement + BASE.substring(index + 1));
            }
        }
        return values;
    }

    private static String randomValue(Random random, int depth) {
        String[] atoms = {"null", "true", "false", "-12.5e+2", "\"café 🌐\"", "\"\\u0000\"", "0"};
        if (depth == 5 || random.nextInt(3) == 0) return atoms[random.nextInt(atoms.length)];
        if (random.nextBoolean()) return "[" + randomValue(random, depth + 1) + ","
                + randomValue(random, depth + 1) + "]";
        return "{\"left\":" + randomValue(random, depth + 1) + ",\"right\":"
                + randomValue(random, depth + 1) + "}";
    }

    private static List<Object> outcome(Class<?> parser, String json, List<ReportRequest> requests) throws Exception {
        Object capability;
        try {
            capability = parser.getMethod("parse", String.class).invoke(null, json);
        } catch (InvocationTargetException error) {
            Throwable cause = error.getCause();
            return Arrays.asList("rejected", cause.getClass().getName(), cause.getMessage());
        }
        List<Object> projection = new ArrayList<>();
        projection.add("accepted");
        for (String getter : List.of("kinds", "topologyModes", "maxTargets", "minTimeoutMs", "maxTimeoutMs",
                "maxTracerouteAttempts", "toString")) projection.add(parser.getMethod(getter).invoke(capability));
        for (CheckKind kind : CheckKind.values())
            projection.add(parser.getMethod("supports", CheckKind.class).invoke(capability, kind));
        projection.add(parser.getMethod("supports", CheckKind.class).invoke(capability, new Object[]{null}));
        for (ReportRequest.TopologyMode mode : ReportRequest.TopologyMode.values())
            projection.add(parser.getMethod("supports", ReportRequest.TopologyMode.class).invoke(capability, mode));
        projection.add(parser.getMethod("supports", ReportRequest.TopologyMode.class).invoke(capability, new Object[]{null}));
        for (ReportRequest request : requests) {
            try {
                parser.getMethod("validate", ReportRequest.class).invoke(capability, request);
                projection.add("supported");
            } catch (InvocationTargetException error) {
                Throwable cause = error.getCause();
                // Only the frozen enclosing-class identifier differs, never the exception/message/reason contract.
                projection.add(Arrays.asList(cause.getClass().getName().replace("FrozenCheckCapabilities", "CheckCapabilities"),
                        cause.getMessage(), cause.toString(), cause.getClass().getMethod("reason").invoke(cause).toString()));
            }
        }
        return projection;
    }

    private static List<ReportRequest> requests() {
        List<ReportRequest> requests = new ArrayList<>();
        for (CheckKind kind : CheckKind.values()) {
            for (int timeout : new int[]{100, 499, 500, 2000, 2001, 30_000})
                requests.add(ReportRequest.builder().timeoutMs(timeout)
                        .addTarget(TargetInput.of(kind, "example.test")).build());
        }
        for (int attempts : new int[]{1, 3, 4, 10}) {
            for (ReportRequest.TopologyMode mode : ReportRequest.TopologyMode.values())
                requests.add(ReportRequest.builder().timeoutMs(500).topologyMode(mode)
                        .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "example.test").attempts(attempts).build()).build());
        }
        for (int count : new int[]{1, 2, 3, 20}) {
            ReportRequest.Builder builder = ReportRequest.builder().timeoutMs(500);
            for (int index = 0; index < count; index++) builder.addTarget(TargetInput.of(CheckKind.DNS, "example.test"));
            requests.add(builder.build());
        }
        return requests;
    }
}
