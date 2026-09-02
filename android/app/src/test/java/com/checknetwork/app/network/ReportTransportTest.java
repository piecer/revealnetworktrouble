package com.checknetwork.app.network;

import static org.junit.Assert.*;

import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.ContractLimits;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.core.TargetInput;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.SocketTimeoutException;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class ReportTransportTest {
    private static final String VALID = "{\"id\":\"r1\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[{\"kind\":\"dns\",\"address\":\"example.test\",\"status\":\"healthy\",\"latency_ms\":1,\"started_at\":\"2026-09-02T00:00:00Z\",\"details\":{}}],\"summary\":{\"total\":1,\"passed\":1,\"failed\":0}}";

    private static ReportRequest request() {
        return ReportRequest.builder().timeoutMs(1_000).addTarget(TargetInput.of(CheckKind.DNS, "example.test")).build();
    }

    @Test public void successFixtureSatisfiesStrictParser() {
        assertEquals("r1", com.checknetwork.app.core.ReportParser.parse(VALID).id());
    }

    @Test public void postsJsonWithBoundedTimeoutAndHttpsAuthorization() throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        FakeScheduler scheduler = new FakeScheduler();
        ReportTransport transport = transport("https://api.example.test", "top-secret", connection, new FakeClock(), scheduler);

        ReportTransport.Response response = transport.newCall(request()).execute();

        assertEquals("POST", connection.method);
        assertEquals("application/json; charset=utf-8", connection.headers.get("Content-Type"));
        assertEquals("application/json", connection.headers.get("Accept"));
        assertEquals("Bearer top-secret", connection.headers.get("Authorization"));
        assertArrayEquals(request().toJson().getBytes(StandardCharsets.UTF_8), connection.output.toByteArray());
        assertEquals(1_000, connection.connectTimeout);
        assertEquals(1_000, connection.readTimeout);
        assertEquals(8_000L, scheduler.delayMillis);
        assertEquals(VALID, response.rawJson());
        assertEquals("r1", response.report().id());
        assertTrue(scheduler.ticket.cancelled);
        assertEquals(1, connection.disconnects);
    }

    @Test public void cleartextDebugNeverEmitsAuthorization() throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        transport("http://localhost:8080", "top-secret", connection, new FakeClock(), new FakeScheduler())
                .newCall(request()).execute();
        assertFalse(connection.headers.containsKey("Authorization"));
    }

    @Test public void contentLengthRejectsBeforeReadingAndExactEightMiBIsAccepted() throws Exception {
        byte[] exact = paddedValid(ContractLimits.MAX_TRANSPORT_BYTES);
        FakeConnection accepted = new FakeConnection(200, exact);
        accepted.contentLength = exact.length;
        assertEquals("r1", transport("https://api.example.test", null, accepted, new FakeClock(), new FakeScheduler())
                .newCall(request()).execute().report().id());

        FakeConnection oversized = new FakeConnection(200, bytes(VALID));
        oversized.contentLength = (long) ContractLimits.MAX_TRANSPORT_BYTES + 1;
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, oversized, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertEquals(TransportException.Kind.RESPONSE_TOO_LARGE, error.kind());
        assertEquals(0, oversized.inputRequests);
    }

    @Test public void streamedCapCountsMultibyteBytesAndWinsEvenWhenCloseFails() throws Exception {
        byte[] body = "€".repeat(2_796_203).getBytes(StandardCharsets.UTF_8);
        FakeConnection connection = new FakeConnection(200, body);
        connection.contentLength = -1;
        connection.input = new ByteArrayInputStream(body) {
            @Override public void close() throws IOException { throw new IOException("cancel/close failed with server prose"); }
        };
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, connection, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertEquals(TransportException.Kind.RESPONSE_TOO_LARGE, error.kind());
        assertFalse(error.toString().contains("server prose"));
    }

    @Test public void manualCancellationDisconnectsExactlyOnceAndClassifiesLateIoAsCancelled() throws Exception {
        BlockingConnection connection = new BlockingConnection();
        ReportTransport.Call call = transport("https://api.example.test", null, connection, new FakeClock(), new FakeScheduler()).newCall(request());
        ExecutorService executor = Executors.newSingleThreadExecutor();
        try {
            Future<TransportException.Kind> result = executor.submit(() -> {
                try { call.execute(); return null; }
                catch (TransportException error) { return error.kind(); }
            });
            assertTrue(connection.entered.await(2, TimeUnit.SECONDS));
            call.cancel();
            call.cancel();
            assertEquals(TransportException.Kind.CANCELLED, result.get(2, TimeUnit.SECONDS));
            assertEquals(1, connection.disconnects);
        } finally { executor.shutdownNow(); }
    }

    @Test public void scheduledAbsoluteDeadlineInterruptsSlowCallAndDisconnectsOnce() throws Exception {
        BlockingConnection connection = new BlockingConnection();
        FakeScheduler scheduler = new FakeScheduler();
        ReportTransport.Call call = transport("https://api.example.test", null, connection, new FakeClock(), scheduler).newCall(request());
        ExecutorService executor = Executors.newSingleThreadExecutor();
        try {
            Future<TransportException.Kind> result = executor.submit(() -> {
                try { call.execute(); return null; }
                catch (TransportException error) { return error.kind(); }
            });
            assertTrue(connection.entered.await(2, TimeUnit.SECONDS));
            scheduler.fire();
            assertEquals(TransportException.Kind.TIMEOUT, result.get(2, TimeUnit.SECONDS));
            assertEquals(1, connection.disconnects);
        } finally { executor.shutdownNow(); }
    }

    @Test public void deadlineUsesMaxTracerouteAttemptsGraceAndDocumentedCap() {
        ReportRequest tenAttempts = ReportRequest.builder().timeoutMs(30_000)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "example.test").attempts(10).build()).build();
        assertEquals(ReportTransport.MAX_DEADLINE_MILLIS, ReportTransport.deadlineMillis(tenAttempts));
        ReportRequest defaults = ReportRequest.builder().timeoutMs(2_000)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "example.test").build())
                .addTarget(TargetInput.of(CheckKind.DNS, "other.test")).build();
        assertEquals(17_000L, ReportTransport.deadlineMillis(defaults));
    }

    @Test public void classifiesNetworkSocketTimeoutMalformedSuccessAndStructuredErrorsWithoutProse() throws Exception {
        FakeConnection network = new FakeConnection(200, bytes(VALID)); network.responseFailure = new IOException("Bearer stolen");
        assertKind(TransportException.Kind.NETWORK, network);
        FakeConnection timeout = new FakeConnection(200, bytes(VALID)); timeout.responseFailure = new SocketTimeoutException("secret");
        assertKind(TransportException.Kind.TIMEOUT, timeout);
        FakeConnection malformed = new FakeConnection(200, bytes("<html>secret</html>"));
        assertKind(TransportException.Kind.INVALID_RESPONSE, malformed);

        for (int status : new int[]{401, 422, 429, 503}) {
            String prose = "Bearer server-secret prose";
            FakeConnection operational = new FakeConnection(status,
                    bytes("{\"error\":{\"code\":\"" + expectedCode(status) + "\",\"message\":\"" + prose + "\"}}"));
            operational.headers.put("Retry-After", "2");
            TransportException error = assertThrows(TransportException.class,
                    () -> transport("https://api.example.test", "client-secret", operational, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
            assertEquals(TransportException.Kind.API, error.kind());
            assertEquals(status, error.apiError().orElseThrow().status());
            assertFalse(error.toString().contains(prose));
            assertFalse(error.toString().contains("client-secret"));
        }
    }

    @Test public void errorBodyIsReadThroughIndependentSmallBound() throws Exception {
        byte[] huge = new byte[ReportTransport.MAX_ERROR_BODY_BYTES + 1];
        FakeConnection connection = new FakeConnection(401, huge); connection.contentLength = -1;
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, connection, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertEquals(TransportException.Kind.API, error.kind());
        assertEquals("unauthorized", error.apiError().orElseThrow().code());
        assertTrue(connection.inputBytesRead <= ReportTransport.MAX_ERROR_BODY_BYTES + 1);
    }

    @Test public void structuredStatusWinsWhenErrorBodyCleanupFails() throws Exception {
        FakeConnection connection = new FakeConnection(401, bytes("{}"));
        connection.input = new ByteArrayInputStream(bytes("{}")) {
            @Override public void close() throws IOException { throw new IOException("Bearer reflected server prose"); }
        };
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", "client-secret", connection, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertEquals(TransportException.Kind.API, error.kind());
        assertEquals("unauthorized", error.apiError().orElseThrow().code());
        assertFalse(error.toString().contains("server prose"));
    }

    @Test public void structuredStatusWinsWhenErrorBodyReadFails() throws Exception {
        FakeConnection connection = new FakeConnection(503, bytes("ignored"));
        connection.input = new InputStream() {
            @Override public int read() throws IOException { throw new IOException("server body failed"); }
            @Override public int read(byte[] bytes, int offset, int length) throws IOException { throw new IOException("server body failed"); }
        };
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, connection, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertEquals(TransportException.Kind.API, error.kind());
        assertEquals(503, error.apiError().orElseThrow().status());
        assertEquals("server_busy", error.apiError().orElseThrow().code());
    }

    @Test public void cleanupRuntimeFailuresNeverReplaceSuccessfulResultAndAllStagesRun() throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        connection.input = new ByteArrayInputStream(bytes(VALID)) {
            @Override public void close() { throw new IllegalStateException("input close"); }
        };
        connection.disconnectFailure = new IllegalStateException("disconnect");
        FakeScheduler scheduler = new FakeScheduler(); scheduler.ticket.cancelFailure = new IllegalStateException("deadline cancel");
        ReportTransport.Response response = transport("https://api.example.test", null, connection, new FakeClock(), scheduler)
                .newCall(request()).execute();
        assertEquals("r1", response.report().id());
        assertTrue(scheduler.ticket.cancelled);
        assertEquals(1, connection.disconnects);
    }

    @Test public void schedulerCancelExecutingDeadlineCannotStealCommittedSuccess() throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        connection.disconnectFailure = new IllegalStateException("disconnect");
        CancelRunsTaskScheduler scheduler = new CancelRunsTaskScheduler();
        ReportTransport.Response response = transport("https://api.example.test", null, connection, new FakeClock(), scheduler)
                .newCall(request()).execute();
        assertEquals("r1", response.report().id());
        assertEquals(1, connection.disconnects);
    }

    @Test public void deadlineWinningDuringLastClockReadCannotReturnSuccess() throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        FakeScheduler scheduler = new FakeScheduler();
        HookClock clock = new HookClock(8, scheduler::fire);
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, connection, clock, scheduler).newCall(request()).execute());
        assertEquals(TransportException.Kind.TIMEOUT, error.kind());
    }

    @Test public void cancellationWinningDuringLastClockReadCannotReturnSuccess() throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        FakeScheduler scheduler = new FakeScheduler();
        ReportTransport.Call[] holder = new ReportTransport.Call[1];
        HookClock clock = new HookClock(8, () -> holder[0].cancel());
        holder[0] = transport("https://api.example.test", null, connection, clock, scheduler).newCall(request());
        TransportException error = assertThrows(TransportException.class, holder[0]::execute);
        assertEquals(TransportException.Kind.CANCELLED, error.kind());
    }

    @Test public void documentedAndUnknownHttpErrorsUseSafeStructuredApiOutcomes() throws Exception {
        for (int status : new int[]{500, 418}) {
            FakeConnection connection = new FakeConnection(status, bytes("{\"error\":{\"code\":\"compact_response_too_large\",\"message\":\"secret upstream prose\"}}"));
            TransportException error = assertThrows(TransportException.class,
                    () -> transport("https://api.example.test", "client-secret", connection, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
            assertEquals(TransportException.Kind.API, error.kind());
            assertEquals(status, error.apiError().orElseThrow().status());
            assertFalse(error.toString().contains("secret"));
            assertFalse(error.toString().contains("upstream prose"));
        }
    }

    private static void assertKind(TransportException.Kind kind, FakeConnection connection) {
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, connection, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertEquals(kind, error.kind());
        assertFalse(error.toString().contains("secret"));
    }

    private static String expectedCode(int status) {
        return switch (status) { case 401 -> "unauthorized"; case 422 -> "invalid_request"; case 429 -> "rate_limited"; default -> "server_busy"; };
    }

    private static ReportTransport transport(String base, String token, HttpURLConnection connection, FakeClock clock, ReportTransport.Scheduler scheduler) {
        return new ReportTransport(ApiConnectionConfig.create(base, base.startsWith("http:"), token), url -> connection, clock, scheduler);
    }

    private static byte[] paddedValid(int size) {
        byte[] prefix = bytes(VALID); byte[] result = new byte[size];
        System.arraycopy(prefix, 0, result, 0, prefix.length);
        for (int i = prefix.length; i < size; i++) result[i] = ' ';
        return result;
    }
    private static byte[] bytes(String value) { return value.getBytes(StandardCharsets.UTF_8); }

    private static class FakeClock implements ReportTransport.Clock {
        long nanos;
        @Override public long nanoTime() { return nanos; }
        @Override public Instant now() { return Instant.EPOCH; }
    }
    private static final class HookClock extends FakeClock {
        final int triggerCall; final Runnable hook; int calls;
        HookClock(int triggerCall, Runnable hook) { this.triggerCall=triggerCall; this.hook=hook; }
        @Override public long nanoTime() { if (++calls == triggerCall) hook.run(); return nanos; }
    }

    private static class FakeScheduler implements ReportTransport.Scheduler {
        Runnable task; long delayMillis; final FakeTicket ticket = new FakeTicket();
        @Override public ReportTransport.Scheduled schedule(Runnable task, long delayMillis) {
            this.task = task; this.delayMillis = delayMillis; return ticket;
        }
        void fire() { task.run(); }
    }
    private static class FakeTicket implements ReportTransport.Scheduled {
        boolean cancelled; RuntimeException cancelFailure;
        @Override public void cancel() { cancelled = true; if (cancelFailure != null) throw cancelFailure; }
    }
    private static final class CancelRunsTaskScheduler implements ReportTransport.Scheduler {
        Runnable task;
        @Override public ReportTransport.Scheduled schedule(Runnable task, long delayMillis) {
            this.task=task; return () -> this.task.run();
        }
    }

    private static class FakeConnection extends HttpURLConnection {
        final java.util.Map<String,String> headers = new java.util.LinkedHashMap<>();
        ByteArrayOutputStream output = new ByteArrayOutputStream(); InputStream input;
        int status; long contentLength = -1; int inputRequests; int inputBytesRead; int disconnects;
        String method; int connectTimeout; int readTimeout; IOException responseFailure; RuntimeException disconnectFailure;
        FakeConnection(int status, byte[] body) throws Exception { super(new URL("https://fake.invalid")); this.status=status; this.input=counting(body); }
        private InputStream counting(byte[] body) {
            return new ByteArrayInputStream(body) {
                @Override public synchronized int read(byte[] target, int off, int len) {
                    int count = super.read(target, off, len); if (count > 0) inputBytesRead += count; return count;
                }
                @Override public synchronized int read() { int value=super.read(); if(value>=0) inputBytesRead++; return value; }
            };
        }
        @Override public void setRequestMethod(String method) { this.method=method; }
        @Override public void setRequestProperty(String key,String value) { headers.put(key,value); }
        @Override public String getHeaderField(String key) { return headers.get(key); }
        @Override public void setConnectTimeout(int value) { connectTimeout=value; }
        @Override public void setReadTimeout(int value) { readTimeout=value; }
        @Override public java.io.OutputStream getOutputStream() { return output; }
        @Override public int getResponseCode() throws IOException { if(responseFailure!=null) throw responseFailure; return status; }
        @Override public long getContentLengthLong() { return contentLength; }
        @Override public InputStream getInputStream() { inputRequests++; return input; }
        @Override public InputStream getErrorStream() { inputRequests++; return input; }
        @Override public void disconnect() { disconnects++; if (disconnectFailure != null) throw disconnectFailure; }
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() {}
    }

    private static final class BlockingConnection extends FakeConnection {
        final CountDownLatch entered = new CountDownLatch(1); final CountDownLatch released = new CountDownLatch(1);
        BlockingConnection() throws Exception { super(200, bytes(VALID)); }
        @Override public int getResponseCode() throws IOException {
            entered.countDown();
            try { if (!released.await(2, TimeUnit.SECONDS)) throw new IOException("blocked"); }
            catch (InterruptedException interrupted) { Thread.currentThread().interrupt(); throw new IOException("interrupted", interrupted); }
            throw new IOException("disconnected");
        }
        @Override public void disconnect() { super.disconnect(); released.countDown(); }
    }
}
