package com.checknetwork.app.core;

import static org.junit.Assert.*;

import java.util.Arrays;
import java.util.List;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class CheckCapabilitiesTest {
    private static final String GO_FIXTURE = "{"
            + "\"kinds\":[\"dns\",\"tcp\",\"http\",\"https\",\"traceroute\",\"ssh\",\"smtp\",\"submission\",\"smtps\",\"imap\",\"imaps\",\"pop3\",\"pop3s\"],"
            + "\"topology_modes\":[\"full\",\"compact\"],"
            + "\"limits\":{"
            + "\"max_targets\":20,\"max_traceroute_attempts\":10,"
            + "\"timeout_ms_min\":100,\"timeout_ms_max\":30000,"
            + "\"compact_topology_nodes\":500,\"compact_topology_links\":1000,"
            + "\"compact_response_bytes_exclusive\":1048576,\"compact_geo_bundle_bytes\":4096}}";

    @Test public void currentGoFixtureAcceptsEveryLocalKindAndRequest() {
        CheckCapabilities capabilities = CheckCapabilities.parse(GO_FIXTURE);
        assertEquals(13, capabilities.kinds().size());
        for (CheckKind kind : CheckKind.values()) assertTrue(capabilities.supports(kind));
        assertTrue(capabilities.supports(ReportRequest.TopologyMode.FULL));
        assertTrue(capabilities.supports(ReportRequest.TopologyMode.COMPACT));

        ReportRequest.Builder all = ReportRequest.builder().timeoutMs(30_000);
        for (CheckKind kind : CheckKind.values()) {
            TargetInput.Builder target = TargetInput.builder(kind, kind.wireValue() + ".example.test");
            if (kind == CheckKind.TRACEROUTE) target.attempts(10);
            all.addTarget(target.build());
        }
        capabilities.validate(all.build());
    }

    @Test public void unknownFutureKindsAndModesAreIgnoredWithinBounds() {
        CheckCapabilities capabilities = CheckCapabilities.parse(fixture(
                "[\"dns\",\"future-probe-v2\"]", "[\"compact\",\"future-map-v2\"]", 2, 5, 100, 5000));
        assertEquals(java.util.Set.of(CheckKind.DNS), capabilities.kinds());
        assertEquals(java.util.Set.of(ReportRequest.TopologyMode.COMPACT), capabilities.topologyModes());
        assertFalse((capabilities.toString()).contains("future-probe-v2"));
    }

    @Test public void reducedCapabilitiesRejectUnsupportedRequestsWithClosedReasons() {
        CheckCapabilities reduced = CheckCapabilities.parse(fixture(
                "[\"dns\",\"traceroute\"]", "[\"full\"]", 1, 3, 500, 2_000));
        reduced.validate(ReportRequest.builder().timeoutMs(500)
                .addTarget(TargetInput.of(CheckKind.DNS, "one.test")).build());

        assertRejected(reduced, ReportRequest.builder().timeoutMs(500)
                .addTarget(TargetInput.of(CheckKind.TCP, "one.test")).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.CHECK_KIND);
        assertRejected(reduced, ReportRequest.builder().timeoutMs(500)
                .addTarget(TargetInput.of(CheckKind.DNS, "one.test"))
                .addTarget(TargetInput.of(CheckKind.DNS, "two.test")).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.TARGET_COUNT);
        assertRejected(reduced, ReportRequest.builder().timeoutMs(499)
                .addTarget(TargetInput.of(CheckKind.DNS, "one.test")).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.TIMEOUT);
        assertRejected(reduced, ReportRequest.builder().timeoutMs(2_001)
                .addTarget(TargetInput.of(CheckKind.DNS, "one.test")).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.TIMEOUT);
        assertRejected(reduced, ReportRequest.builder().timeoutMs(500)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "one.test").attempts(4).build()).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.TRACEROUTE_ATTEMPTS);
        assertRejected(reduced, ReportRequest.builder().timeoutMs(500)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "one.test").build()).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.TRACEROUTE_ATTEMPTS);
        assertRejected(reduced, ReportRequest.topologyBuilder().timeoutMs(500)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "one.test").attempts(3).build()).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.TOPOLOGY_MODE);
    }

    @Test public void mismatchPriorityIsDeterministicAndExceptionDoesNotReflectInputsOrServerLimits() {
        CheckCapabilities reduced = CheckCapabilities.parse(fixture(
                "[\"dns\"]", "[]", 1, 1, 500, 1_000));
        ReportRequest request = ReportRequest.topologyBuilder().timeoutMs(2_000)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "PRIVATE-TARGET.example")
                        .attempts(10).build())
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "PRIVATE-TOKEN.example")
                        .attempts(10).build()).build();

        CheckCapabilities.CapabilityMismatchException error = assertThrows(
                CheckCapabilities.CapabilityMismatchException.class, () -> reduced.validate(request));

        assertEquals(CheckCapabilities.CapabilityMismatchException.Reason.CHECK_KIND, error.reason());
        assertEquals("The report request is not supported by the server capabilities.", error.getMessage());
        assertEquals("CapabilityMismatchException{reason=CHECK_KIND}", error.toString());
        for (String secret : List.of("PRIVATE-TARGET", "PRIVATE-TOKEN", "dns", "500", "1000")) {
            assertFalse(error.getMessage().contains(secret));
            assertFalse(error.toString().contains(secret));
        }
        assertArrayEquals(new CheckCapabilities.CapabilityMismatchException.Reason[]{
                CheckCapabilities.CapabilityMismatchException.Reason.CHECK_KIND,
                CheckCapabilities.CapabilityMismatchException.Reason.TARGET_COUNT,
                CheckCapabilities.CapabilityMismatchException.Reason.TIMEOUT,
                CheckCapabilities.CapabilityMismatchException.Reason.TRACEROUTE_ATTEMPTS,
                CheckCapabilities.CapabilityMismatchException.Reason.TOPOLOGY_MODE},
                CheckCapabilities.CapabilityMismatchException.Reason.values());
    }

    @Test public void parseRequiresOneBoundedObjectAndStrictRequiredIntegerLimits() {
        for (String invalid : Arrays.asList(null, "", "[]", "{}", GO_FIXTURE + "{}",
                fixture("[]", "[\"full\"]", 1, 1, 100, 1000).replace("\"limits\"", "\"missing\""),
                fixture("[\"dns\"]", "[\"full\"]", 1, 1, 100, 1000).replace("\"max_targets\":1", "\"max_targets\":1.0"),
                fixture("[\"dns\"]", "[\"full\"]", 1, 1, 100, 1000).replace("\"max_targets\":1", "\"max_targets\":\"1\""),
                fixture("[\"dns\"]", "[\"full\"]", 0, 1, 100, 1000),
                fixture("[\"dns\"]", "[\"full\"]", 1, 0, 100, 1000),
                fixture("[\"dns\"]", "[\"full\"]", 1, 1, 2000, 1000),
                fixture("[1]", "[\"full\"]", 1, 1, 100, 1000),
                fixture("[\"dns\"]", "{}", 1, 1, 100, 1000))) {
            IllegalArgumentException error = assertThrows(IllegalArgumentException.class,
                    () -> CheckCapabilities.parse(invalid));
            assertEquals("Invalid checks capability response", error.getMessage());
        }

        String oversized = GO_FIXTURE + " ".repeat(CheckCapabilities.MAX_JSON_BYTES);
        assertThrows(IllegalArgumentException.class, () -> CheckCapabilities.parse(oversized));
        String tooManyKinds = fixture("[" + "\"future\",".repeat(CheckCapabilities.MAX_LIST_ITEMS) + "\"future\"]",
                "[\"full\"]", 1, 1, 100, 1000);
        assertThrows(IllegalArgumentException.class, () -> CheckCapabilities.parse(tooManyKinds));
        String longUnknown = fixture("[\"" + "x".repeat(CheckCapabilities.MAX_NAME_BYTES + 1) + "\"]",
                "[\"full\"]", 1, 1, 100, 1000);
        assertThrows(IllegalArgumentException.class, () -> CheckCapabilities.parse(longUnknown));
    }

    @Test public void advertisedGrowthIsSafelyIntersectedWithLocalRequestBounds() {
        CheckCapabilities capabilities = CheckCapabilities.parse(fixture(
                "[\"dns\"]", "[\"full\"]", 100, 100, 1, 100_000));
        assertEquals(ContractLimits.MAX_TARGETS, capabilities.maxTargets());
        assertEquals(ContractLimits.MIN_TIMEOUT_MS, capabilities.minTimeoutMs());
        assertEquals(ContractLimits.MAX_TIMEOUT_MS, capabilities.maxTimeoutMs());
        assertEquals(ContractLimits.MAX_TRACEROUTE_ATTEMPTS, capabilities.maxTracerouteAttempts());
    }

    @Test public void duplicateNamesAreRejectedBeforeMaterializationAtEveryObjectLevel() {
        for (String malformed : List.of(
                GO_FIXTURE.replace("\"kinds\":", "\"kinds\":[],\"kinds\":"),
                GO_FIXTURE.replace("\"max_targets\":20", "\"max_targets\":1,\"max_targets\":20"),
                GO_FIXTURE.replace("\"max_targets\":20", "\"max_targets\":1,\"max_\\u0074argets\":20"),
                withUnknown("{\"nested\":[{\"future\":1,\"future\":2}]}"),
                withUnknown("{\"nested\":[{\"future\":1,\"f\\u0075ture\":2}]}"))) {
            assertInvalid(malformed);
        }
    }

    @Test public void strictLexicalKeywordsRejectNonJsonCapitalization() {
        for (String value : List.of("TRUE", "True", "FALSE", "False", "NULL", "Null")) {
            assertInvalid(withUnknown(value));
        }
    }

    @Test public void strictLexicalStringsRejectRawControls() {
        for (int control = 0; control < 0x20; control++) {
            assertInvalid(withUnknown("\"before" + (char) control + "after\""));
        }
    }

    @Test public void strictLexicalStringsRejectNonJsonEscapes() {
        for (String value : List.of("\"\\q\"", "\"\\'\"", "\"\\v\"", "\"\\0\"",
                "\"\\x41\"", "\"\\u+123\"", "\"\\u-123\"", "\"\\u12G4\"", "\"\\\n\"")) {
            assertInvalid(withUnknown(value));
        }
    }

    @Test public void strictLexicalStructureRejectsLenientJsonAndTrailingDocuments() {
        for (String malformed : List.of(
                GO_FIXTURE.replace('"', '\''),
                GO_FIXTURE.replace("\"kinds\"", "kinds"),
                GO_FIXTURE.replace("\"kinds\":", "\"kinds\"="),
                GO_FIXTURE.replace("\"kinds\":", "\"kinds\"=>"),
                GO_FIXTURE.replace(",", ";"),
                "/*comment*/" + GO_FIXTURE, GO_FIXTURE + "//comment",
                GO_FIXTURE + "{}", GO_FIXTURE + "null", GO_FIXTURE + "garbage",
                GO_FIXTURE + "\f", "\ufeff" + GO_FIXTURE,
                withUnknown("[1,]"), withUnknown("[,1]"), withUnknown("[1,,2]"),
                withUnknown("{\"x\":1,}"), withUnknown("unquoted"),
                withUnknown("['\"'," + "[".repeat(64) + "0" + "]".repeat(64) + ",'\"']"))) {
            assertInvalid(malformed);
        }
    }

    @Test public void strictLexicalNumbersRejectNonJsonForms() {
        for (String value : List.of("+1", "01", "-01", ".1", "1.", "1e", "1e+", "NaN", "Infinity", "0x20")) {
            assertInvalid(withUnknown(value));
        }
    }

    @Test public void validUnknownJsonLexemesRemainForwardCompatible() {
        for (String value : List.of("null", "true", "false", "0", "-0", "1.25", "1e+20", "-2E-3",
                "\"quotes: \\\" slash: \\/ backslash: \\\\ control: \\b\\f\\n\\r\\t\\u0000\"",
                "\"apostrophe ' brackets [{]} café 🌐\"", "\"\\uD83C\\uDF10\"")) {
            assertEquals(CheckCapabilities.parse(GO_FIXTURE).kinds(),
                    CheckCapabilities.parse(withUnknown(value)).kinds());
        }
        CheckCapabilities.parse(" \r\n\t" + GO_FIXTURE + " \r\n\t");
    }

    @Test public void unpairedSurrogatesAreRejectedInKnownAndUnknownNamesAndValues() {
        for (String lone : List.of("\\uD800", "\\uDC00", "\\uD800x", "\\uDC00\\uD800",
                String.valueOf((char) 0xd800), String.valueOf((char) 0xdc00))) {
            assertInvalid(withUnknown("\"" + lone + "\""));
            assertInvalid(withUnknown("{\"" + lone + "\":0}"));
            assertInvalid(GO_FIXTURE.replace("\"dns\"", "\"" + lone + "\""));
        }
    }

    @Test public void depthEightIsAcceptedButNineIsRejectedIncludingUnknownFields() {
        CheckCapabilities.parse(withUnknown("[".repeat(7) + "0" + "]".repeat(7)));
        assertInvalid(withUnknown("[".repeat(8) + "0" + "]".repeat(8)));
        CheckCapabilities.parse(withUnknown("{\"x\":".repeat(7) + "0" + "}".repeat(7)));
        assertInvalid(withUnknown("{\"x\":".repeat(8) + "0" + "}".repeat(8)));
        assertInvalid(withUnknown("[".repeat(10_000) + "0" + "]".repeat(10_000)));
    }

    @Test public void utf8BodyLimitIsInclusiveAndNotACharacterLimit() {
        String unicode = withUnknown("\"café 🌐\"");
        int bytes = unicode.getBytes(java.nio.charset.StandardCharsets.UTF_8).length;
        String exact = unicode + " ".repeat(CheckCapabilities.MAX_JSON_BYTES - bytes);
        assertEquals(CheckCapabilities.MAX_JSON_BYTES, exact.getBytes(java.nio.charset.StandardCharsets.UTF_8).length);
        CheckCapabilities.parse(exact);
        assertInvalid(exact + " ");
        assertInvalid(withUnknown("\"" + "🌐".repeat(CheckCapabilities.MAX_JSON_BYTES / 4) + "\""));
    }

    private static String withUnknown(String value) {
        return GO_FIXTURE.substring(0, GO_FIXTURE.length() - 1) + ",\"unknown\":" + value + "}";
    }

    private static void assertInvalid(String value) {
        IllegalArgumentException error = assertThrows(value, IllegalArgumentException.class,
                () -> CheckCapabilities.parse(value));
        assertEquals("Invalid checks capability response", error.getMessage());
    }

    private static void assertRejected(CheckCapabilities capabilities, ReportRequest request,
            CheckCapabilities.CapabilityMismatchException.Reason reason) {
        CheckCapabilities.CapabilityMismatchException error = assertThrows(
                CheckCapabilities.CapabilityMismatchException.class, () -> capabilities.validate(request));
        assertEquals(reason, error.reason());
        assertFalse(error.getMessage().contains("one.test"));
    }

    private static String fixture(String kinds, String modes, int maxTargets, int maxAttempts, int minTimeout, int maxTimeout) {
        return "{\"kinds\":" + kinds + ",\"topology_modes\":" + modes + ",\"limits\":{"
                + "\"max_targets\":" + maxTargets + ",\"max_traceroute_attempts\":" + maxAttempts + ","
                + "\"timeout_ms_min\":" + minTimeout + ",\"timeout_ms_max\":" + maxTimeout + "}}";
    }
}
