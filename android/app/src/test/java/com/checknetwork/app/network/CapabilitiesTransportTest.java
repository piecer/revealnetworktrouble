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

    private static InputStream closeFailing(byte[] value) {
        return new ByteArrayInputStream(value) {
            @Override public void close() throws IOException { throw new IOException("secret close"); }
        };
    }
    private static byte[] bytes(String value) { return value.getBytes(StandardCharsets.UTF_8); }

    private static final class FakeClock implements ReportTransport.Clock {
        @Override public long nanoTime() { return 0; }
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
        InputStream input; int status; long contentLength = -1; int inputRequests; int bytesRead; int disconnects;
        String method; int connectTimeout; int readTimeout; boolean followRedirects = true; boolean doOutput;
        IOException responseFailure; RuntimeException disconnectFailure;
        FakeConnection(int status, byte[] body) throws Exception {
            super(new URL("https://fake.invalid")); this.status = status; this.input = counting(body);
        }
        private InputStream counting(byte[] body) {
            return new ByteArrayInputStream(body) {
                @Override public synchronized int read(byte[] target, int offset, int length) {
                    int count = super.read(target, offset, length); if (count > 0) bytesRead += count; return count;
                }
                @Override public synchronized int read() { int value = super.read(); if (value >= 0) bytesRead++; return value; }
            };
        }
        @Override public void setRequestMethod(String value) { method = value; }
        @Override public void setRequestProperty(String key, String value) { headers.put(key, value); }
        @Override public String getHeaderField(String key) { return headers.get(key); }
        @Override public void setConnectTimeout(int value) { connectTimeout = value; }
        @Override public void setReadTimeout(int value) { readTimeout = value; }
        @Override public void setInstanceFollowRedirects(boolean value) { followRedirects = value; }
        @Override public void setDoOutput(boolean value) { doOutput = value; }
        @Override public int getResponseCode() throws IOException { if (responseFailure != null) throw responseFailure; return status; }
        @Override public long getContentLengthLong() { return contentLength; }
        @Override public InputStream getInputStream() { inputRequests++; return input; }
        @Override public InputStream getErrorStream() { inputRequests++; return input; }
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
