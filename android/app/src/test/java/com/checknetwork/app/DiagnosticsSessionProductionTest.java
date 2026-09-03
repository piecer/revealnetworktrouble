package com.checknetwork.app;

import static org.junit.Assert.*;

import com.checknetwork.app.core.CheckCapabilities;
import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.core.TargetInput;
import com.checknetwork.app.network.ApiConnectionConfig;
import com.checknetwork.app.network.CapabilitiesTransport;
import com.checknetwork.app.network.OperationDeadline;
import com.checknetwork.app.network.ReportTransport;
import com.checknetwork.app.network.TransportException;
import com.checknetwork.app.state.RequestState;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class DiagnosticsSessionProductionTest {
    private static final String ALL = "{\"kinds\":[\"dns\",\"tcp\",\"http\",\"https\",\"traceroute\",\"ssh\",\"smtp\",\"submission\",\"smtps\",\"imap\",\"imaps\",\"pop3\",\"pop3s\"],"
            + "\"topology_modes\":[\"full\",\"compact\"],\"limits\":{\"max_targets\":20,\"max_traceroute_attempts\":10,\"timeout_ms_min\":100,\"timeout_ms_max\":30000}}";
    private static final String REDUCED = "{\"kinds\":[\"dns\"],\"topology_modes\":[\"full\"],\"limits\":{\"max_targets\":1,\"max_traceroute_attempts\":3,\"timeout_ms_min\":500,\"timeout_ms_max\":2000}}";
    private static final String REPORT = "{\"id\":\"r1\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,"
            + "\"results\":[{\"kind\":\"dns\",\"address\":\"example.test\",\"status\":\"healthy\",\"latency_ms\":1,\"started_at\":\"2026-09-02T00:00:00Z\",\"details\":{}}],"
            + "\"summary\":{\"total\":1,\"passed\":1,\"failed\":0}}";

    @Test public void discoveryRunsBeforePostAndReducedServerBlocksLocally() throws Exception {
        ProbeTransports reduced = new ProbeTransports(REDUCED, REPORT);
        try (Fixture fixture = fixture(reduced)) {
            fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), tcpRequest());
            RequestState state = fixture.awaitTerminal();
            assertEquals(RequestState.Phase.ERROR, state.phase());
            assertEquals(TransportException.Kind.UNSUPPORTED_CAPABILITY,
                    state.error().orElseThrow().kind());
            assertEquals(CheckCapabilities.CapabilityMismatchException.Reason.CHECK_KIND,
                    state.error().orElseThrow().capabilityMismatchReason().orElseThrow());
            assertEquals(List.of("GET"), reduced.executedMethods());
            assertEquals(0, reduced.reportConnections.size());
        }

        ProbeTransports current = new ProbeTransports(ALL, REPORT);
        try (Fixture fixture = fixture(current)) {
            fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), dnsRequest());
            assertEquals(RequestState.Phase.READY, fixture.awaitTerminal().phase());
            assertEquals(List.of("GET", "POST"), current.executedMethods());
        }
    }

    @Test public void everyReducedCapabilityFieldMapsToItsTypedReasonWithoutCreatingReportTransport() throws Exception {
        assertMismatch("{\"kinds\":[\"dns\"],\"topology_modes\":[\"full\"],\"limits\":{\"max_targets\":20,\"max_traceroute_attempts\":10,\"timeout_ms_min\":100,\"timeout_ms_max\":30000}}",
                tcpRequest(), CheckCapabilities.CapabilityMismatchException.Reason.CHECK_KIND);
        assertMismatch("{\"kinds\":[\"dns\"],\"topology_modes\":[\"full\"],\"limits\":{\"max_targets\":1,\"max_traceroute_attempts\":10,\"timeout_ms_min\":100,\"timeout_ms_max\":30000}}",
                ReportRequest.builder().timeoutMs(1_000)
                        .addTarget(TargetInput.of(CheckKind.DNS, "one.test"))
                        .addTarget(TargetInput.of(CheckKind.DNS, "two.test")).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.TARGET_COUNT);
        assertMismatch("{\"kinds\":[\"dns\"],\"topology_modes\":[\"full\"],\"limits\":{\"max_targets\":20,\"max_traceroute_attempts\":10,\"timeout_ms_min\":1500,\"timeout_ms_max\":2000}}",
                dnsRequest(), CheckCapabilities.CapabilityMismatchException.Reason.TIMEOUT);
        assertMismatch("{\"kinds\":[\"traceroute\"],\"topology_modes\":[\"compact\"],\"limits\":{\"max_targets\":20,\"max_traceroute_attempts\":2,\"timeout_ms_min\":100,\"timeout_ms_max\":30000}}",
                ReportRequest.builder().timeoutMs(1_000)
                        .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "trace.test").attempts(3).build()).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.TRACEROUTE_ATTEMPTS);
        assertMismatch("{\"kinds\":[\"traceroute\"],\"topology_modes\":[\"full\"],\"limits\":{\"max_targets\":20,\"max_traceroute_attempts\":10,\"timeout_ms_min\":100,\"timeout_ms_max\":30000}}",
                ReportRequest.topologyBuilder().timeoutMs(1_000)
                        .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "trace.test").attempts(3).build()).build(),
                CheckCapabilities.CapabilityMismatchException.Reason.TOPOLOGY_MODE);
    }

    @Test public void malformedDiscoveryStaysInvalidResponseAndProgrammingRuntimeStaysNetwork() throws Exception {
        ProbeTransports malformed = new ProbeTransports("{invalid capability json", REPORT);
        try (Fixture fixture = fixture(malformed)) {
            fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), dnsRequest());
            assertEquals(TransportException.Kind.INVALID_RESPONSE,
                    fixture.awaitTerminal().error().orElseThrow().kind());
            assertEquals(0, malformed.reportConnections.size());
        }

        for (RuntimeException unexpected : List.of(
                new IllegalArgumentException("private illegal argument detail"),
                new IllegalStateException("private runtime detail"))) {
            ProbeTransports programmingFailure = new ProbeTransports(ALL, REPORT);
            programmingFailure.reportFactoryFailure = unexpected;
            try (Fixture fixture = fixture(programmingFailure)) {
                fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), dnsRequest());
                TransportException error = fixture.awaitTerminal().error().orElseThrow();
                assertEquals(TransportException.Kind.NETWORK, error.kind());
                assertFalse(error.toString().contains("private"));
            }
        }
    }

    @Test public void retryWithSupportedRequestUsesNewOwnerAndSucceedsInSameSession() throws Exception {
        ProbeTransports probes = new ProbeTransports(REDUCED, REPORT);
        try (Fixture fixture = fixture(probes)) {
            long firstOwner = fixture.session.start(
                    ApiConnectionConfig.create("https://api.example.test", false, null), tcpRequest());
            RequestState mismatch = fixture.awaitTerminal();
            assertEquals(TransportException.Kind.UNSUPPORTED_CAPABILITY,
                    mismatch.error().orElseThrow().kind());

            fixture.resetTerminalLatch();
            long secondOwner = fixture.session.start(
                    ApiConnectionConfig.create("https://api.example.test", false, null), dnsRequest());
            assertTrue(secondOwner > firstOwner);
            assertEquals(RequestState.Phase.READY, fixture.awaitTerminal().phase());
            assertEquals(List.of("GET", "GET", "POST"), probes.executedMethods());
            assertEquals(2, probes.deadlineIdentities.size());
        }
    }

    @Test public void cancellationOrExpiryObservedDuringDiscoveryWinsOverMismatchOneHundredTimes() throws Exception {
        for (int run = 0; run < 100; run++) {
            ProbeTransports cancelled = new ProbeTransports(REDUCED, REPORT);
            try (Fixture fixture = fixture(cancelled)) {
                cancelled.capabilityOnResponse = fixture.session::cancel;
                fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), tcpRequest());
                assertEquals("cancel run " + run, RequestState.Phase.CANCELLED, fixture.awaitTerminal().phase());
                fixture.awaitWorkerDrain();
                assertEquals("cancel terminal publications " + run, 1, fixture.terminalPublications);
                assertEquals("cancel final state " + run, RequestState.Phase.CANCELLED, fixture.session.state().phase());
                assertEquals("cancel run " + run, 0, cancelled.reportConnections.size());
            }

            Clock clock = new Clock();
            ProbeTransports expired = new ProbeTransports(REDUCED, REPORT, clock);
            expired.advanceDiscoveryMillis = ReportTransport.MAX_DEADLINE_MILLIS;
            try (Fixture fixture = fixture(expired, clock)) {
                fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), tcpRequest());
                assertEquals("expiry run " + run, TransportException.Kind.TIMEOUT,
                        fixture.awaitTerminal().error().orElseThrow().kind());
                assertEquals("expiry run " + run, 0, expired.reportConnections.size());
            }
        }
    }

    @Test public void cancellationBetweenGetAndPostPreventsReportExecution() throws Exception {
        ProbeTransports probes = new ProbeTransports(ALL, REPORT);
        probes.blockReportFactory = true;
        try (Fixture fixture = fixture(probes)) {
            fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), dnsRequest());
            assertTrue(probes.reportFactoryEntered.await(2, TimeUnit.SECONDS));
            fixture.session.cancel();
            probes.releaseReportFactory.countDown();
            assertTrue(probes.reportFactoryExited.await(2, TimeUnit.SECONDS));
            assertEquals(RequestState.Phase.CANCELLED, fixture.session.state().phase());
            assertEquals(List.of("GET"), probes.executedMethods());
        }
    }

    @Test public void unauthorizedDiscoveryCanRetryAfterCredentialChangeAndBothHttpsCallsUseExactBearer() throws Exception {
        ProbeTransports probes = new ProbeTransports(ALL, REPORT);
        probes.rejectToken = "Bearer old-token";
        try (Fixture fixture = fixture(probes)) {
            fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, "old-token"), dnsRequest());
            RequestState first = fixture.awaitTerminal();
            assertEquals(RequestState.Phase.ERROR, first.phase());
            assertEquals(401, first.error().orElseThrow().apiError().orElseThrow().status());

            fixture.resetTerminalLatch();
            fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, "new-token"), dnsRequest());
            assertEquals(RequestState.Phase.READY, fixture.awaitTerminal().phase());

            assertEquals("Bearer old-token", probes.capabilityConnections.get(0).headers.get("Authorization"));
            assertEquals("Bearer new-token", probes.capabilityConnections.get(1).headers.get("Authorization"));
            assertEquals("Bearer new-token", probes.reportConnections.get(0).headers.get("Authorization"));
        }
    }

    @Test public void cleartextDebugNeverSendsBearerOnDiscoveryOrReport() throws Exception {
        ProbeTransports probes = new ProbeTransports(ALL, REPORT);
        try (Fixture fixture = fixture(probes)) {
            fixture.session.start(ApiConnectionConfig.create("http://localhost:8080", true, "must-not-leak"), dnsRequest());
            assertEquals(RequestState.Phase.READY, fixture.awaitTerminal().phase());
            assertFalse(probes.capabilityConnections.get(0).headers.containsKey("Authorization"));
            assertFalse(probes.reportConnections.get(0).headers.containsKey("Authorization"));
        }
    }

    @Test public void oneSessionDeadlineAndClockAreSharedByDiscoveryAndReport() throws Exception {
        Clock clock = new Clock();
        ProbeTransports probes = new ProbeTransports(ALL, REPORT, clock);
        try (Fixture fixture = fixture(probes, clock)) {
            fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), dnsRequest());
            assertEquals(RequestState.Phase.READY, fixture.awaitTerminal().phase());
            assertSame(probes.capabilitiesDeadline, probes.reportDeadline);
            assertSame(clock, probes.capabilitiesClock);
            assertSame(clock, probes.reportClock);
            assertEquals(1, probes.deadlineIdentities.size());
        }
    }

    @Test public void exhaustedDiscoveryBudgetSkipsReportAndPublishesOneTimeout() throws Exception {
        Clock clock = new Clock();
        ProbeTransports probes = new ProbeTransports(ALL, REPORT, clock);
        probes.advanceDiscoveryMillis = ReportTransport.MAX_DEADLINE_MILLIS;
        try (Fixture fixture = fixture(probes, clock)) {
            fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), dnsRequest());
            RequestState state = fixture.awaitTerminal();
            assertEquals(RequestState.Phase.ERROR, state.phase());
            assertEquals(com.checknetwork.app.network.TransportException.Kind.TIMEOUT,
                    state.error().orElseThrow().kind());
            assertEquals(0, probes.reportConnections.size());
            assertNull(probes.reportDeadline);
            assertEquals(1L, probes.reportFactoryEntered.getCount());
            assertEquals(1, fixture.timeoutPublications);
        }
    }

    private static Fixture fixture(ProbeTransports transports) {
        return fixture(transports, transports.clock);
    }

    private static Fixture fixture(ProbeTransports transports, Clock clock) {
        ExecutorService executor = Executors.newSingleThreadExecutor();
        DiagnosticsSession session = DiagnosticsSession.createProductionForTests(
                Runnable::run, executor, transports, clock);
        return new Fixture(session, executor);
    }

    private static ReportRequest dnsRequest() {
        return ReportRequest.builder().timeoutMs(1_000).addTarget(TargetInput.of(CheckKind.DNS, "example.test")).build();
    }
    private static ReportRequest tcpRequest() {
        return ReportRequest.builder().timeoutMs(1_000).addTarget(TargetInput.of(CheckKind.TCP, "example.test")).build();
    }

    private static void assertMismatch(String capabilities, ReportRequest request,
            CheckCapabilities.CapabilityMismatchException.Reason reason) throws Exception {
        ProbeTransports probes = new ProbeTransports(capabilities, REPORT);
        try (Fixture fixture = fixture(probes)) {
            fixture.session.start(ApiConnectionConfig.create("https://api.example.test", false, null), request);
            TransportException error = fixture.awaitTerminal().error().orElseThrow();
            assertEquals(TransportException.Kind.UNSUPPORTED_CAPABILITY, error.kind());
            assertEquals(reason, error.capabilityMismatchReason().orElseThrow());
            assertEquals(0, probes.reportConnections.size());
            assertNull(probes.reportDeadline);
            assertEquals(1L, probes.reportFactoryEntered.getCount());
            assertEquals(List.of("GET"), probes.executedMethods());
        }
    }

    private static final class Fixture implements AutoCloseable {
        final DiagnosticsSession session; final ExecutorService executor;
        volatile CountDownLatch terminal = new CountDownLatch(1);
        volatile int timeoutPublications;
        volatile int terminalPublications;
        Fixture(DiagnosticsSession session, ExecutorService executor) {
            this.session = session; this.executor = executor;
            session.attach(this, state -> {
                if (state.phase() == RequestState.Phase.ERROR && state.error().isPresent()
                        && state.error().orElseThrow().kind()
                        == com.checknetwork.app.network.TransportException.Kind.TIMEOUT) timeoutPublications++;
                if (!state.busy() && state.phase() != RequestState.Phase.IDLE) {
                    terminalPublications++;
                    terminal.countDown();
                }
            });
        }
        RequestState awaitTerminal() throws InterruptedException {
            assertTrue(terminal.await(3, TimeUnit.SECONDS)); return session.state();
        }
        void awaitWorkerDrain() throws Exception { executor.submit(() -> {}).get(3, TimeUnit.SECONDS); }
        void resetTerminalLatch() { terminal = new CountDownLatch(1); }
        @Override public void close() {
            session.destroy(); executor.shutdownNow();
        }
    }

    private static final class ProbeTransports implements DiagnosticsSession.ProductionTransports {
        final String capabilitiesJson; final String reportJson;
        final List<ProbeConnection> capabilityConnections = new ArrayList<>();
        final List<ProbeConnection> reportConnections = new ArrayList<>();
        final List<String> executionOrder = java.util.Collections.synchronizedList(new ArrayList<>());
        final java.util.Set<OperationDeadline> deadlineIdentities =
                java.util.Collections.newSetFromMap(new java.util.IdentityHashMap<>());
        final Clock clock;
        final CountDownLatch reportFactoryEntered = new CountDownLatch(1);
        final CountDownLatch releaseReportFactory = new CountDownLatch(1);
        final CountDownLatch reportFactoryExited = new CountDownLatch(1);
        volatile boolean blockReportFactory; volatile String rejectToken; volatile long advanceDiscoveryMillis;
        volatile Runnable capabilityOnResponse = () -> {};
        volatile RuntimeException reportFactoryFailure;
        volatile OperationDeadline capabilitiesDeadline; volatile OperationDeadline reportDeadline;
        volatile ReportTransport.Clock capabilitiesClock; volatile ReportTransport.Clock reportClock;

        ProbeTransports(String capabilitiesJson, String reportJson) {
            this(capabilitiesJson, reportJson, new Clock());
        }

        ProbeTransports(String capabilitiesJson, String reportJson, Clock clock) {
            this.capabilitiesJson = capabilitiesJson; this.reportJson = reportJson; this.clock = clock;
        }

        @Override public CapabilitiesTransport capabilities(ApiConnectionConfig config, OperationDeadline deadline) {
            capabilitiesDeadline = deadline; capabilitiesClock = clock; deadlineIdentities.add(deadline);
            String authorization = config.authorizationHeader().orElse(null);
            int status = rejectToken != null && rejectToken.equals(authorization) ? 401 : 200;
            String body = status == 401 ? "{\"error\":{\"code\":\"unauthorized\",\"message\":\"secret\"}}" : capabilitiesJson;
            ProbeConnection connection = new ProbeConnection(status, body, executionOrder);
            connection.onResponse = () -> {
                clock.nanos = TimeUnit.MILLISECONDS.toNanos(advanceDiscoveryMillis);
                capabilityOnResponse.run();
            };
            capabilityConnections.add(connection);
            return new CapabilitiesTransport(config, url -> connection, clock, new Scheduler(), deadline);
        }

        @Override public ReportTransport reports(ApiConnectionConfig config, OperationDeadline deadline) {
            if (reportFactoryFailure != null) throw reportFactoryFailure;
            reportDeadline = deadline; reportClock = clock; deadlineIdentities.add(deadline);
            reportFactoryEntered.countDown();
            if (blockReportFactory) {
                boolean interrupted = false;
                while (true) {
                    try { releaseReportFactory.await(); break; }
                    catch (InterruptedException ignored) { interrupted = true; }
                }
                if (interrupted) Thread.currentThread().interrupt();
            }
            ProbeConnection connection = new ProbeConnection(200, reportJson, executionOrder);
            reportConnections.add(connection);
            reportFactoryExited.countDown();
            return new ReportTransport(config, url -> connection, clock, new Scheduler(), deadline);
        }

        List<String> executedMethods() { synchronized (executionOrder) { return List.copyOf(executionOrder); } }
    }

    private static final class Clock implements ReportTransport.Clock {
        volatile long nanos;
        @Override public long nanoTime() { return nanos; }
        @Override public Instant now() { return Instant.EPOCH; }
    }
    private static final class Scheduler implements ReportTransport.Scheduler {
        @Override public ReportTransport.Scheduled schedule(Runnable task, long delayMillis) { return () -> {}; }
    }

    private static final class ProbeConnection extends HttpURLConnection {
        final Map<String,String> headers = new LinkedHashMap<>();
        final InputStream body; final int status; final List<String> order;
        final ByteArrayOutputStream output = new ByteArrayOutputStream();
        String method; Runnable onResponse = () -> {};
        ProbeConnection(int status, String body, List<String> order) {
            super(url()); this.status = status; this.body = new ByteArrayInputStream(body.getBytes(StandardCharsets.UTF_8)); this.order = order;
        }
        private static URL url() { try { return new URL("https://fake.invalid"); } catch (Exception impossible) { throw new AssertionError(impossible); } }
        @Override public void setRequestMethod(String value) { method = value; }
        @Override public void setRequestProperty(String key, String value) { headers.put(key, value); }
        @Override public int getResponseCode() { order.add(method); onResponse.run(); return status; }
        @Override public InputStream getInputStream() { return body; }
        @Override public InputStream getErrorStream() { return body; }
        @Override public java.io.OutputStream getOutputStream() { return output; }
        @Override public long getContentLengthLong() { return -1; }
        @Override public void disconnect() {}
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() {}
    }
}
