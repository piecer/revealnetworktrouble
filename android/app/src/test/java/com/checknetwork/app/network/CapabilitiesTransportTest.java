package com.checknetwork.app.network;

import static org.junit.Assert.*;

import com.checknetwork.app.core.CheckCapabilities;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.SocketTimeoutException;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class CapabilitiesTransportTest {
    private static final String VALID = "{\"kinds\":[\"dns\"],\"topology_modes\":[\"full\"],\"limits\":{"
            + "\"max_targets\":20,\"max_traceroute_attempts\":10,\"timeout_ms_min\":100,\"timeout_ms_max\":30000}}";
    private enum Boundary { OPEN, CONFIG, RESPONSE, CONTENT_LENGTH, INPUT_OPEN, INPUT_READ, INPUT_CLOSE, HEADER }

    @Test public void getsChecksJsonWithHttpsAuthorizationNoRedirectsAndDeadline() throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        FakeScheduler scheduler = new FakeScheduler();
        CapabilitiesTransport transport = transport("https://api.example.test", "top-secret", connection, scheduler);

        CheckCapabilities capabilities = transport.newCall().execute();

        assertTrue(capabilities.kinds().contains(com.checknetwork.app.core.CheckKind.DNS));
        assertEquals("GET", connection.method);
        assertEquals("application/json", connection.headers.get("Accept"));
        assertEquals("Bearer top-secret", connection.headers.get("Authorization"));
        assertFalse(connection.followRedirects);
        assertFalse(connection.doOutput);
        assertEquals(CapabilitiesTransport.DEADLINE_MILLIS, connection.connectTimeout);
        assertEquals(CapabilitiesTransport.DEADLINE_MILLIS, connection.readTimeout);
        assertEquals(CapabilitiesTransport.DEADLINE_MILLIS, scheduler.delayMillis);
        assertEquals(1, connection.disconnects);
        assertTrue(scheduler.ticket.cancelled);
    }

    @Test public void cleartextDebugNeverEmitsAuthorization() throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        transport("http://localhost:8080", "top-secret", connection, new FakeScheduler()).newCall().execute();
        assertFalse(connection.headers.containsKey("Authorization"));
    }

    @Test public void rejectsContentLengthStreamOverflowMalformedUtf8AndMalformedJson() throws Exception {
        FakeConnection length = new FakeConnection(200, bytes(VALID));
        length.contentLength = CheckCapabilities.MAX_JSON_BYTES + 1L;
        assertKind(TransportException.Kind.RESPONSE_TOO_LARGE, length);
        assertEquals(0, length.inputRequests);

        FakeConnection streamed = new FakeConnection(200, new byte[CheckCapabilities.MAX_JSON_BYTES + 1]);
        assertKind(TransportException.Kind.RESPONSE_TOO_LARGE, streamed);
        assertTrue(streamed.bytesRead <= CheckCapabilities.MAX_JSON_BYTES + 1);

        FakeConnection utf8 = new FakeConnection(200, new byte[]{(byte) 0xc3, 0x28});
        assertKind(TransportException.Kind.INVALID_RESPONSE, utf8);
        FakeConnection json = new FakeConnection(200, bytes("{secret invalid json"));
        TransportException malformed = assertKind(TransportException.Kind.INVALID_RESPONSE, json);
        assertFalse(malformed.toString().contains("secret"));
    }

    @Test public void everyNon2xxStatusUsesStructuredApiErrorWithBoundedBody() throws Exception {
        for (int status : new int[]{301, 401, 418, 503}) {
            byte[] body = new byte[CapabilitiesTransport.MAX_ERROR_BODY_BYTES + 1];
            FakeConnection connection = new FakeConnection(status, body);
            TransportException error = assertThrows(TransportException.class,
                    () -> transport("https://api.example.test", "client-secret", connection, new FakeScheduler()).newCall().execute());
            assertEquals(TransportException.Kind.API, error.kind());
            assertEquals(status, error.apiError().orElseThrow().status());
            assertTrue(connection.bytesRead <= CapabilitiesTransport.MAX_ERROR_BODY_BYTES + 1);
            assertFalse(error.toString().contains("client-secret"));
        }
    }

    @Test public void strictErrorJsonStaysNonretryableApiFailureAtEveryAdversarialBoundaryCount100() throws Exception {
        String canonical = "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"}}";
        String duplicateCode = "{\"error\":{\"code\":\"server_busy\",\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"}}";
        String duplicateError = "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"},\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"}}";
        String deep = "{\"error\":" + "[".repeat(12_000) + "]".repeat(12_000) + "}";
        for (int run = 0; run < 100; run++) for (byte[] body : new byte[][]{
                bytes(canonical + canonical), bytes(canonical + " HOSTILE-TRAILING"),
                bytes(duplicateCode), bytes(duplicateError), bytes(deep),
                new byte[]{'{', '"', 'e', 'r', 'r', 'o', 'r', '"', ':', '"', (byte) 0xc3, 0x28, '"', '}'}}) {
            FakeConnection connection = new FakeConnection(503, body);
            TransportException failure = assertThrows(TransportException.class,
                    () -> transport("https://api.example.test", null, connection, new FakeScheduler()).newCall().execute());
            assertEquals(TransportException.Kind.API, failure.kind());
            assertInvalidApiError(failure, 503);
        }

        byte[] prefix = bytes(canonical);
        byte[] exact = java.util.Arrays.copyOf(prefix, CapabilitiesTransport.MAX_ERROR_BODY_BYTES);
        java.util.Arrays.fill(exact, prefix.length, exact.length, (byte) ' ');
        FakeConnection accepted = new FakeConnection(503, exact);
        TransportException typed = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, accepted, new FakeScheduler()).newCall().execute());
        assertEquals("server_busy", typed.apiError().orElseThrow().code());

        FakeConnection over = new FakeConnection(503, java.util.Arrays.copyOf(exact, exact.length + 1));
        TransportException rejected = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, over, new FakeScheduler()).newCall().execute());
        assertInvalidApiError(rejected, 503);
    }

    private static void assertInvalidApiError(TransportException failure, int status) {
        assertEquals(status, failure.apiError().orElseThrow().status());
        assertEquals("invalid_server_response", failure.apiError().orElseThrow().code());
        assertFalse(failure.apiError().orElseThrow().retryable());
        assertFalse(failure.apiError().orElseThrow().retryAt().isPresent());
        assertFalse(failure.toString().contains("HOSTILE"));
    }

    @Test public void cancellationAndDeadlineDisconnectExactlyOnceAndWinLateIo() throws Exception {
        for (boolean deadline : new boolean[]{false, true}) {
            BlockingConnection connection = new BlockingConnection();
            FakeScheduler scheduler = new FakeScheduler();
            CapabilitiesTransport.Call call = transport("https://api.example.test", null, connection, scheduler).newCall();
            ExecutorService executor = Executors.newSingleThreadExecutor();
            try {
                Future<TransportException.Kind> result = executor.submit(() -> {
                    try { call.execute(); return null; }
                    catch (TransportException error) { return error.kind(); }
                });
                assertTrue(connection.entered.await(2, TimeUnit.SECONDS));
                if (deadline) scheduler.fire(); else { call.cancel(); call.cancel(); }
                assertEquals(deadline ? TransportException.Kind.TIMEOUT : TransportException.Kind.CANCELLED,
                        result.get(2, TimeUnit.SECONDS));
                assertEquals(1, connection.disconnects);
            } finally { executor.shutdownNow(); }
        }
    }

    @Test public void sharedSessionBudgetBoundsSchedulerAndSocketTimeouts() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        clock.nanos = TimeUnit.MILLISECONDS.toNanos(312_000L);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        FakeScheduler scheduler = new FakeScheduler();
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);

        new CapabilitiesTransport(config, url -> connection, clock, scheduler, deadline).newCall().execute();

        assertEquals(3_000L, scheduler.delayMillis);
        assertEquals(3_000, connection.connectTimeout);
        assertEquals(3_000, connection.readTimeout);
    }

    @Test public void exactSharedExpirySkipsConnectionOpen() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        clock.nanos = TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
        int[] opens = {0};
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport transport = new CapabilitiesTransport(config, url -> {
            opens[0]++;
            throw new AssertionError("expired discovery must not open a connection");
        }, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(0, opens[0]);
    }

    @Test public void sharedExpiryDuringIoWinsLateNetworkFailure() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        connection.onResponse = () -> clock.nanos =
                TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
        connection.responseFailure = new IOException("late network failure");
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport transport = new CapabilitiesTransport(
                config, url -> connection, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(1, connection.disconnects);
    }

    @Test public void sharedExpiryDuringSocketTimeoutRemainsTimeout() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        connection.onResponse = () -> clock.nanos =
                TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
        connection.responseFailure = new SocketTimeoutException("late socket timeout");
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport transport = new CapabilitiesTransport(
                config, url -> connection, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(1, connection.disconnects);
    }

    @Test public void exactSharedExpiryDuringConfigurationSkipsResponseIo() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        connection.onRequestProperty = () -> clock.nanos =
                TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport transport = new CapabilitiesTransport(
                config, url -> connection, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(0, connection.responseRequests);
        assertEquals(1, connection.disconnects);
    }

    @Test public void exactSharedExpiryDuringContentLengthSkipsSuccessBodyOpen() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        connection.onContentLength = () -> clock.nanos =
                TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport transport = new CapabilitiesTransport(
                config, url -> connection, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(0, connection.inputRequests);
        assertEquals(1, connection.disconnects);
    }

    @Test public void exactExpiryAtEverySynchronousBoundaryStopsBeforeTheNextNetworkCall() throws Exception {
        for (int run = 0; run < 100; run++)
            for (Boundary boundary : Boundary.values()) assertCapabilitiesBoundary(boundary, 10_000L, true);
    }

    @Test public void oneMillisecondRemainingAtEverySynchronousBoundaryStillAllowsTheCurrentCall() throws Exception {
        for (Boundary boundary : Boundary.values()) assertCapabilitiesBoundary(boundary, 9_999L, false);
    }

    @Test public void sharedExpiryWinnerIsStableAcrossOneHundredRuns() throws Exception {
        for (int run = 0; run < 100; run++) {
            FakeClock clock = new FakeClock();
            OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
            FakeConnection connection = new FakeConnection(200, bytes(VALID));
            connection.onResponse = () -> clock.nanos =
                    TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
            connection.responseFailure = new IOException("late network failure");
            ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
            CapabilitiesTransport transport = new CapabilitiesTransport(
                    config, url -> connection, clock, new FakeScheduler(), deadline);

            TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);

            assertEquals("run " + run, TransportException.Kind.TIMEOUT, timeout.kind());
            assertEquals("run " + run, 1, connection.disconnects);
        }
    }

    @Test public void sharedExpiryDuringConnectionOpenWinsLateNetworkFailure() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport transport = new CapabilitiesTransport(config, url -> {
            clock.nanos = TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
            throw new IOException("late open failure");
        }, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
    }

    @Test public void sharedExpiryDuringBodyReadWinsLateNetworkFailure() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        connection.input = new InputStream() {
            @Override public int read() throws IOException { throw lateFailure(); }
            @Override public int read(byte[] target, int offset, int length) throws IOException {
                throw lateFailure();
            }
            private IOException lateFailure() {
                clock.nanos = TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
                return new IOException("late body failure");
            }
        };
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport transport = new CapabilitiesTransport(
                config, url -> connection, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(1, connection.disconnects);
    }

    @Test public void sharedExpiryAtMalformedParseWinsInvalidResponse() throws Exception {
        ExpiringReadClock clock = new ExpiringReadClock(21);
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(200, bytes("{malformed"));
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport transport = new CapabilitiesTransport(
                config, url -> connection, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(1, connection.disconnects);
    }

    @Test public void cancellationWinsWhenSharedExpiryAndLateNetworkFailureAreSimultaneous() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport.Call[] holder = new CapabilitiesTransport.Call[1];
        CapabilitiesTransport transport = new CapabilitiesTransport(
                config, url -> connection, clock, new FakeScheduler(), deadline);
        holder[0] = transport.newCall();
        connection.onResponse = () -> {
            clock.nanos = TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
            holder[0].cancel();
        };
        connection.responseFailure = new IOException("simultaneous late failure");

        TransportException cancelled = assertThrows(TransportException.class, holder[0]::execute);

        assertEquals(TransportException.Kind.CANCELLED, cancelled.kind());
        assertEquals(1, connection.disconnects);
    }

    @Test public void cleanupFailuresCannotReplaceSuccessOrApiOutcome() throws Exception {
        FakeConnection success = new FakeConnection(200, bytes(VALID));
        success.input = closeFailing(bytes(VALID)); success.disconnectFailure = new IllegalStateException("secret disconnect");
        FakeScheduler scheduler = new FakeScheduler(); scheduler.ticket.failure = new IllegalStateException("secret timer");
        assertTrue(transport("https://api.example.test", null, success, scheduler).newCall().execute()
                .kinds().contains(com.checknetwork.app.core.CheckKind.DNS));

        FakeConnection api = new FakeConnection(401, bytes("{}"));
        api.input = closeFailing(bytes("{}")); api.disconnectFailure = new IllegalStateException("secret disconnect");
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, api, new FakeScheduler()).newCall().execute());
        assertEquals(TransportException.Kind.API, error.kind());
    }

    @Test public void socketTimeoutAndIoUseStableNonreflectingClassifications() throws Exception {
        FakeConnection timeout = new FakeConnection(200, bytes(VALID));
        timeout.responseFailure = new SocketTimeoutException("Bearer server secret");
        assertKind(TransportException.Kind.TIMEOUT, timeout);
        FakeConnection network = new FakeConnection(200, bytes(VALID));
        network.responseFailure = new IOException("Bearer server secret");
        assertKind(TransportException.Kind.NETWORK, network);
    }

    private static CapabilitiesTransport transport(String base, String token, HttpURLConnection connection,
            FakeScheduler scheduler) {
        ApiConnectionConfig config = ApiConnectionConfig.create(base, base.startsWith("http:"), token);
        return new CapabilitiesTransport(config, url -> {
            assertEquals(config.checksEndpoint().toURL(), url);
            return connection;
        }, new FakeClock(), scheduler);
    }

    private static TransportException assertKind(TransportException.Kind kind, FakeConnection connection) {
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, connection, new FakeScheduler()).newCall().execute());
        assertEquals(kind, error.kind());
        assertFalse(error.toString().contains("secret"));
        return error;
    }

    private static void assertCapabilitiesBoundary(Boundary boundary, long elapsedMillis, boolean expires)
            throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(boundary == Boundary.HEADER ? 401 : 200, bytes(VALID));
        Runnable advance = () -> clock.nanos = TimeUnit.MILLISECONDS.toNanos(elapsedMillis);
        if (boundary == Boundary.CONFIG) connection.onRequestProperty = advance;
        if (boundary == Boundary.RESPONSE) connection.onResponse = advance;
        if (boundary == Boundary.CONTENT_LENGTH) connection.onContentLength = advance;
        if (boundary == Boundary.INPUT_OPEN) connection.onInputOpen = advance;
        if (boundary == Boundary.INPUT_READ) connection.onInputRead = advance;
        if (boundary == Boundary.INPUT_CLOSE) connection.onInputClose = advance;
        if (boundary == Boundary.HEADER) connection.onHeader = advance;
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        CapabilitiesTransport transport = new CapabilitiesTransport(config, url -> {
            if (boundary == Boundary.OPEN) advance.run();
            return connection;
        }, clock, new FakeScheduler(), deadline);

        if (expires) {
            TransportException timeout = assertThrows(TransportException.class, transport.newCall()::execute);
            assertEquals(boundary.name(), TransportException.Kind.TIMEOUT, timeout.kind());
            switch (boundary) {
                case OPEN, CONFIG -> assertEquals(boundary.name(), 0, connection.responseRequests);
                case RESPONSE -> assertEquals(boundary.name(), 0, connection.contentLengthRequests);
                case CONTENT_LENGTH -> assertEquals(boundary.name(), 0, connection.inputRequests);
                case INPUT_OPEN -> assertEquals(boundary.name(), 0, connection.readRequests);
                case INPUT_READ -> {
                    assertEquals(boundary.name(), 1, connection.readRequests);
                    assertEquals(boundary.name(), 0, connection.inputCloses);
                }
                case INPUT_CLOSE, HEADER -> { }
            }
        } else if (boundary == Boundary.HEADER) {
            TransportException api = assertThrows(TransportException.class, transport.newCall()::execute);
            assertEquals(boundary.name(), TransportException.Kind.API, api.kind());
        } else {
            assertTrue(boundary.name(), transport.newCall().execute().kinds()
                    .contains(com.checknetwork.app.core.CheckKind.DNS));
        }
        assertEquals(boundary.name(), 1, connection.disconnects);
    }

    private static InputStream closeFailing(byte[] value) {
        return new ByteArrayInputStream(value) {
            @Override public void close() throws IOException { throw new IOException("secret close"); }
        };
    }
    private static byte[] bytes(String value) { return value.getBytes(StandardCharsets.UTF_8); }

    private static final class FakeClock implements ReportTransport.Clock {
        long nanos;
        @Override public long nanoTime() { return nanos; }
        @Override public Instant now() { return Instant.EPOCH; }
    }
    private static final class ExpiringReadClock implements ReportTransport.Clock {
        private final int expiryRead;
        private int reads;
        ExpiringReadClock(int expiryRead) { this.expiryRead = expiryRead; }
        @Override public long nanoTime() {
            return ++reads >= expiryRead
                    ? TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS) : 0L;
        }
        @Override public Instant now() { return Instant.EPOCH; }
    }
    private static final class FakeScheduler implements ReportTransport.Scheduler {
        Runnable task; long delayMillis; final FakeTicket ticket = new FakeTicket();
        @Override public ReportTransport.Scheduled schedule(Runnable task, long delayMillis) {
            this.task = task; this.delayMillis = delayMillis; return ticket;
        }
        void fire() { task.run(); }
    }
    private static final class FakeTicket implements ReportTransport.Scheduled {
        boolean cancelled; RuntimeException failure;
        @Override public void cancel() { cancelled = true; if (failure != null) throw failure; }
    }

    private static class FakeConnection extends HttpURLConnection {
        final Map<String,String> headers = new LinkedHashMap<>();
        InputStream input; int status; long contentLength = -1;
        int responseRequests; int contentLengthRequests; int inputRequests; int readRequests; int inputCloses;
        int bytesRead; int disconnects;
        String method; int connectTimeout; int readTimeout; boolean followRedirects = true; boolean doOutput;
        IOException responseFailure; RuntimeException disconnectFailure;
        Runnable onRequestProperty = () -> {}; Runnable onResponse = () -> {}; Runnable onContentLength = () -> {};
        Runnable onInputOpen = () -> {}; Runnable onInputRead = () -> {}; Runnable onInputClose = () -> {};
        Runnable onHeader = () -> {};
        FakeConnection(int status, byte[] body) throws Exception {
            super(new URL("https://fake.invalid")); this.status = status; this.input = counting(body);
        }
        private InputStream counting(byte[] body) {
            return new ByteArrayInputStream(body) {
                @Override public synchronized int read(byte[] target, int offset, int length) {
                    readRequests++; int count = super.read(target, offset, length); if (count > 0) bytesRead += count;
                    onInputRead.run(); return count;
                }
                @Override public synchronized int read() {
                    readRequests++; int value = super.read(); if (value >= 0) bytesRead++; onInputRead.run(); return value;
                }
                @Override public void close() throws IOException { inputCloses++; onInputClose.run(); super.close(); }
            };
        }
        @Override public void setRequestMethod(String value) { method = value; }
        @Override public void setRequestProperty(String key, String value) { headers.put(key, value); onRequestProperty.run(); }
        @Override public String getHeaderField(String key) { onHeader.run(); return headers.get(key); }
        @Override public Map<String,List<String>> getHeaderFields() {
            onHeader.run();
            Map<String,List<String>> values = new LinkedHashMap<>();
            for (Map.Entry<String,String> entry : headers.entrySet())
                values.put(entry.getKey(), List.of(entry.getValue()));
            return values;
        }
        @Override public void setConnectTimeout(int value) { connectTimeout = value; }
        @Override public void setReadTimeout(int value) { readTimeout = value; }
        @Override public void setInstanceFollowRedirects(boolean value) { followRedirects = value; }
        @Override public void setDoOutput(boolean value) { doOutput = value; }
        @Override public int getResponseCode() throws IOException {
            responseRequests++;
            onResponse.run();
            if (responseFailure != null) throw responseFailure;
            return status;
        }
        @Override public long getContentLengthLong() { contentLengthRequests++; onContentLength.run(); return contentLength; }
        @Override public InputStream getInputStream() { inputRequests++; onInputOpen.run(); return input; }
        @Override public InputStream getErrorStream() { inputRequests++; onInputOpen.run(); return input; }
        @Override public void disconnect() { disconnects++; if (disconnectFailure != null) throw disconnectFailure; }
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() {}
    }
    private static final class BlockingConnection extends FakeConnection {
        final CountDownLatch entered = new CountDownLatch(1); final CountDownLatch release = new CountDownLatch(1);
        BlockingConnection() throws Exception { super(200, bytes(VALID)); }
        @Override public int getResponseCode() throws IOException {
            entered.countDown();
            try { release.await(2, TimeUnit.SECONDS); }
            catch (InterruptedException interrupted) { Thread.currentThread().interrupt(); }
            throw new IOException("late secret io");
        }
        @Override public void disconnect() { super.disconnect(); release.countDown(); }
    }
}
