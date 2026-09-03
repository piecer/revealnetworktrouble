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
