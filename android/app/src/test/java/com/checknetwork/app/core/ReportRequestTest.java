package com.checknetwork.app.core;

import static org.junit.Assert.*;

import java.util.Arrays;
import java.util.HashSet;
import org.junit.Test;

public final class ReportRequestTest {
    @Test public void exposesAllThirteenKindsAndImmutableContractLimits() {
        assertEquals(13, CheckKind.values().length);
        assertEquals(new HashSet<>(Arrays.asList(
                "dns", "tcp", "http", "https", "ssh", "smtp", "submission",
                "smtps", "imap", "imaps", "pop3", "pop3s", "traceroute")),
                new HashSet<>(CheckKind.wireValues()));
        assertEquals(20, ContractLimits.MAX_TARGETS);
        assertEquals(100, ContractLimits.MIN_TIMEOUT_MS);
        assertEquals(30_000, ContractLimits.MAX_TIMEOUT_MS);
        assertEquals(10, ContractLimits.MAX_TRACEROUTE_ATTEMPTS);
        assertEquals(8 * 1024 * 1024, ContractLimits.MAX_TRANSPORT_BYTES);
    }

    @Test public void serializesEverySupportedRequestKind() {
        ReportRequest.Builder builder = ReportRequest.builder();
        for (CheckKind kind : CheckKind.values()) {
            TargetInput.Builder target = TargetInput.builder(kind, kind.wireValue() + ".example.test");
            if (kind == CheckKind.TRACEROUTE) target.attempts(5);
            builder.addTarget(target.build());
        }
        String json = builder.build().toJson();
        for (CheckKind kind : CheckKind.values()) assertTrue(json.contains("\"kind\":\"" + kind.wireValue() + "\""));
        assertThrows(IllegalArgumentException.class, () -> CheckKind.fromWire("ftp"));
    }

    @Test public void serializesHttpsExpectedStatusDeterministically() {
        TargetInput target = TargetInput.builder(CheckKind.HTTPS, "https://example.test")
                .expectedStatus(204).build();
        ReportRequest request = ReportRequest.builder().timeoutMs(5_000).addTarget(target).build();
        assertEquals("{\"targets\":[{\"kind\":\"https\",\"address\":\"https://example.test\",\"expected_status\":204}],\"timeout_ms\":5000}", request.toJson());
        assertEquals(request.signature(), request.signature());
        assertFalse(request.signature().contains("example.test"));
    }

    @Test public void expectedStatusIsOnlyForHttpKindsAndWithinHttpRange() {
        assertThrows(IllegalArgumentException.class, () -> TargetInput.builder(CheckKind.DNS, "example.test").expectedStatus(200).build());
        assertThrows(IllegalArgumentException.class, () -> TargetInput.builder(CheckKind.HTTP, "http://example.test").expectedStatus(99).build());
        assertThrows(IllegalArgumentException.class, () -> TargetInput.builder(CheckKind.HTTPS, "https://example.test").expectedStatus(600).build());
        assertEquals(Integer.valueOf(599), TargetInput.builder(CheckKind.HTTPS, "https://example.test").expectedStatus(599).build().expectedStatus());
    }

    @Test public void attemptsAreOnlyForTracerouteAndWithinOneToTen() {
        assertThrows(IllegalArgumentException.class, () -> TargetInput.builder(CheckKind.TCP, "example.test:443").attempts(2).build());
        assertThrows(IllegalArgumentException.class, () -> TargetInput.builder(CheckKind.TRACEROUTE, "example.test").attempts(0).build());
        assertThrows(IllegalArgumentException.class, () -> TargetInput.builder(CheckKind.TRACEROUTE, "example.test").attempts(11).build());
    }

    @Test public void validatesTargetCountTimeoutAndDefensivelyCopies() {
        assertThrows(IllegalStateException.class, () -> ReportRequest.builder().build());
        assertThrows(IllegalArgumentException.class, () -> ReportRequest.builder().timeoutMs(99));
        assertThrows(IllegalArgumentException.class, () -> ReportRequest.builder().timeoutMs(30_001));
        ReportRequest.Builder builder = ReportRequest.builder();
        for (int i = 0; i < 20; i++) builder.addTarget(TargetInput.of(CheckKind.DNS, "target-" + i));
        ReportRequest request = builder.build();
        assertEquals(20, request.targets().size());
        assertThrows(IllegalArgumentException.class, () -> builder.addTarget(TargetInput.of(CheckKind.DNS, "overflow")));
        assertThrows(UnsupportedOperationException.class, () -> request.targets().clear());
    }

    @Test public void topologyRequestsRequireCompactAllTracerouteTargets() {
        ReportRequest request = ReportRequest.topologyBuilder()
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "example.test").attempts(5).build())
                .build();
        assertEquals(ReportRequest.TopologyMode.COMPACT, request.topologyMode());
        assertTrue(request.toJson().contains("\"topology_mode\":\"compact\""));
        assertThrows(IllegalArgumentException.class, () -> ReportRequest.topologyBuilder().topologyMode(ReportRequest.TopologyMode.FULL));
        assertThrows(IllegalArgumentException.class, () -> ReportRequest.topologyBuilder().addTarget(TargetInput.of(CheckKind.DNS, "example.test")));
    }

    @Test public void requestModelCannotContainCredentials() {
        assertFalse(Arrays.stream(ReportRequest.Builder.class.getMethods())
                .map(method -> method.getName().toLowerCase())
                .anyMatch(name -> name.contains("token") || name.contains("credential") || name.contains("bearer") || name.contains("password")));
    }
}
