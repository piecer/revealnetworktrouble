package com.checknetwork.app;

import static org.junit.Assert.*;

import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.core.TargetInput;
import com.checknetwork.app.network.ApiConnectionConfig;
import com.checknetwork.app.network.CapabilitiesTransport;
import com.checknetwork.app.network.ReportTransport;
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
            assertEquals(com.checknetwork.app.network.TransportException.Kind.INVALID_RESPONSE,
                    state.error().orElseThrow().kind());
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

    private static Fixture fixture(ProbeTransports transports) {
        ExecutorService executor = Executors.newSingleThreadExecutor();
        DiagnosticsSession session = DiagnosticsSession.createProductionForTests(Runnable::run, executor, transports);
        return new Fixture(session, executor);
    }

    private static ReportRequest dnsRequest() {
        return ReportRequest.builder().timeoutMs(1_000).addTarget(TargetInput.of(CheckKind.DNS, "example.test")).build();
    }
    private static ReportRequest tcpRequest() {
        return ReportRequest.builder().timeoutMs(1_000).addTarget(TargetInput.of(CheckKind.TCP, "example.test")).build();
    }

    private static final class Fixture implements AutoCloseable {
        final DiagnosticsSession session; final ExecutorService executor;
        volatile CountDownLatch terminal = new CountDownLatch(1);
        Fixture(DiagnosticsSession session, ExecutorService executor) {
            this.session = session; this.executor = executor;
            session.attach(this, state -> { if (!state.busy() && state.phase() != RequestState.Phase.IDLE) terminal.countDown(); });
        }
        RequestState awaitTerminal() throws InterruptedException {
            assertTrue(terminal.await(3, TimeUnit.SECONDS)); return session.state();
        }
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
        final CountDownLatch reportFactoryEntered = new CountDownLatch(1);
        final CountDownLatch releaseReportFactory = new CountDownLatch(1);
        final CountDownLatch reportFactoryExited = new CountDownLatch(1);
        volatile boolean blockReportFactory; volatile String rejectToken;

        ProbeTransports(String capabilitiesJson, String reportJson) {
            this.capabilitiesJson = capabilitiesJson; this.reportJson = reportJson;
        }

        @Override public CapabilitiesTransport capabilities(ApiConnectionConfig config) {
            String authorization = config.authorizationHeader().orElse(null);
            int status = rejectToken != null && rejectToken.equals(authorization) ? 401 : 200;
            String body = status == 401 ? "{\"error\":{\"code\":\"unauthorized\",\"message\":\"secret\"}}" : capabilitiesJson;
            ProbeConnection connection = new ProbeConnection(status, body, executionOrder);
            capabilityConnections.add(connection);
            return new CapabilitiesTransport(config, url -> connection, new Clock(), new Scheduler());
        }

        @Override public ReportTransport reports(ApiConnectionConfig config) {
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
            return new ReportTransport(config, url -> connection, new Clock(), new Scheduler());
        }

        List<String> executedMethods() { synchronized (executionOrder) { return List.copyOf(executionOrder); } }
    }

    private static final class Clock implements ReportTransport.Clock {
        @Override public long nanoTime() { return 0; }
        @Override public Instant now() { return Instant.EPOCH; }
    }
    private static final class Scheduler implements ReportTransport.Scheduler {
        @Override public ReportTransport.Scheduled schedule(Runnable task, long delayMillis) { return () -> {}; }
    }

    private static final class ProbeConnection extends HttpURLConnection {
        final Map<String,String> headers = new LinkedHashMap<>();
        final InputStream body; final int status; final List<String> order;
        final ByteArrayOutputStream output = new ByteArrayOutputStream();
        String method;
        ProbeConnection(int status, String body, List<String> order) {
            super(url()); this.status = status; this.body = new ByteArrayInputStream(body.getBytes(StandardCharsets.UTF_8)); this.order = order;
        }
        private static URL url() { try { return new URL("https://fake.invalid"); } catch (Exception impossible) { throw new AssertionError(impossible); } }
        @Override public void setRequestMethod(String value) { method = value; }
        @Override public void setRequestProperty(String key, String value) { headers.put(key, value); }
        @Override public int getResponseCode() { order.add(method); return status; }
        @Override public InputStream getInputStream() { return body; }
        @Override public InputStream getErrorStream() { return body; }
        @Override public java.io.OutputStream getOutputStream() { return output; }
        @Override public long getContentLengthLong() { return -1; }
        @Override public void disconnect() {}
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() {}
    }
}
