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
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.SocketTimeoutException;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
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
    private static final String VALID = "{\"id\":\"r1\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[{\"kind\":\"dns\",\"address\":\"example.test\",\"status\":\"healthy\",\"latency_ms\":1,\"started_at\":\"2026-09-02T00:00:00Z\",\"details\":{\"addresses\":[\"192.0.2.1\"],\"answer_count\":1}}],\"summary\":{\"total\":1,\"passed\":1,\"failed\":0}}";
    private enum Boundary {
        OPEN, CONFIG, OUTPUT_OPEN, OUTPUT_WRITE, OUTPUT_CLOSE, RESPONSE,
        CONTENT_LENGTH, INPUT_OPEN, INPUT_READ, INPUT_CLOSE, HEADER
    }

    private static ReportRequest request() {
        return ReportRequest.builder().timeoutMs(1_000).addTarget(TargetInput.of(CheckKind.DNS, "example.test")).build();
    }

    @Test public void successFixtureSatisfiesStrictParser() {
        assertEquals("r1", com.checknetwork.app.core.ReportParser.parse(VALID).id());
    }

    @Test public void healthyDnsConnectionFailureIsInvalidBeforeTransportPublishesAResponse() throws Exception {
        String contradictory = VALID.replace("\"status\":\"healthy\",\"latency_ms\":1",
                "\"status\":\"healthy\",\"error_code\":\"connection_failed\",\"latency_ms\":1");
        assertNotEquals(VALID, contradictory);
        assertInvalidReportTransport(contradictory);
    }

    @Test public void duplicateReportNeverReturnsRawOrParsedPartialResponseCount100() throws Exception {
        for (String fixtureName : new String[]{
                "maximum-analysis-report.json", "checker-execution-report.json",
                "enrichment-upstream-report.json", "enrichment-failures-report.json",
                "compact-zero-route-report.json", "compact-zero-node-attempt-report.json",
                "compact-geo-exact-boundary-report.json"}) {
            String canonical = producerFixture(fixtureName);
            assertEquals(canonical, executeSuccessfulReport(canonical).rawJson());
            for (String malformed : duplicateFirstFieldAtEveryObject(canonical)) {
                assertInvalidReportTransport(malformed);
            }
        }

        String duplicateRoot = VALID.replaceFirst("\\{", "{\"id\":null,");
        String duplicateDetails = VALID.replace("\"details\":{\"addresses\"",
                "\"details\":{\"addresses\":null,\"addresses\"");
        assertNotEquals(VALID, duplicateDetails);
        for (int run = 0; run < 100; run++) {
            for (String malformed : new String[]{duplicateRoot, duplicateDetails}) {
                assertInvalidReportTransport(malformed);
            }
        }
    }

    private static ReportTransport.Response executeSuccessfulReport(String raw) throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(raw));
        return transport("https://api.example.test", null, connection,
                new FakeClock(), new FakeScheduler()).newCall(request()).execute();
    }

    private static void assertInvalidReportTransport(String malformed) throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(malformed));
        java.util.concurrent.atomic.AtomicReference<ReportTransport.Response> published =
                new java.util.concurrent.atomic.AtomicReference<>();
        TransportException failure = assertThrows(TransportException.class,
                () -> published.set(transport("https://api.example.test", null, connection,
                        new FakeClock(), new FakeScheduler()).newCall(request()).execute()));
        assertEquals(TransportException.Kind.INVALID_RESPONSE, failure.kind());
        assertEquals("The server returned an invalid response.", failure.getMessage());
        assertNull(failure.getCause());
        assertFalse(failure.apiError().isPresent());
        assertNull(published.get());
        assertEquals(1, connection.disconnects);
    }

    private static String producerFixture(String name) throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata", name).normalize();
        assertTrue("missing Go-produced fixture at " + fixture, Files.isRegularFile(fixture));
        return new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8);
    }

    private static List<String> duplicateFirstFieldAtEveryObject(String json) {
        List<Integer> objectStarts = new ArrayList<>();
        boolean inString = false;
        boolean escaped = false;
        for (int index = 0; index < json.length(); index++) {
            char current = json.charAt(index);
            if (inString) {
                if (escaped) escaped = false;
                else if (current == '\\') escaped = true;
                else if (current == '"') inString = false;
            } else if (current == '"') inString = true;
            else if (current == '{') objectStarts.add(index);
        }
        List<String> mutations = new ArrayList<>();
        for (int start : objectStarts) {
            int nameStart = start + 1;
            while (nameStart < json.length() && Character.isWhitespace(json.charAt(nameStart))) nameStart++;
            if (nameStart < json.length() && json.charAt(nameStart) == '}') {
                mutations.add(json.substring(0, nameStart)
                        + "\"duplicate_probe\":null,\"duplicate_probe\":null"
                        + json.substring(nameStart));
                continue;
            }
            assertTrue("object must begin with a quoted field", nameStart < json.length() && json.charAt(nameStart) == '"');
            int nameEnd = nameStart + 1;
            boolean nameEscaped = false;
            for (; nameEnd < json.length(); nameEnd++) {
                char current = json.charAt(nameEnd);
                if (nameEscaped) nameEscaped = false;
                else if (current == '\\') nameEscaped = true;
                else if (current == '"') break;
            }
            assertTrue("unterminated producer field name", nameEnd < json.length());
            String rawName = json.substring(nameStart, nameEnd + 1);
            mutations.add(json.substring(0, nameStart) + rawName + ":null," + json.substring(nameStart));
        }
        return mutations;
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
        assertEquals(16_000, connection.connectTimeout);
        assertEquals(16_000, connection.readTimeout);
        assertEquals(16_000L, scheduler.delayMillis);
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

    @Test public void delayedLocalResponseHeadersUseAggregateDeadlineInsteadOfTargetTimeout() throws Exception {
        try (ServerSocket server = new ServerSocket(0, 1, InetAddress.getLoopbackAddress())) {
            ExecutorService serverExecutor = Executors.newSingleThreadExecutor();
            Future<?> served = serverExecutor.submit(() -> {
                try (java.net.Socket socket = server.accept()) {
                    InputStream input = socket.getInputStream();
                    String headers = readHttpHeaders(input);
                    int contentLength = httpContentLength(headers);
                    for (int remaining = contentLength; remaining > 0; ) {
                        int count = input.read(new byte[Math.min(remaining, 4_096)]);
                        if (count < 0) throw new IOException("request ended before its declared body");
                        remaining -= count;
                    }
                    Thread.sleep(1_250L); // Deliberately later than the request's 1,000 ms target timeout.
                    byte[] body = bytes(VALID);
                    OutputStream output = socket.getOutputStream();
                    output.write(("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: "
                            + body.length + "\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.US_ASCII));
                    output.write(body);
                    output.flush();
                } catch (Exception failure) {
                    throw new RuntimeException(failure);
                }
            });
            try {
                ApiConnectionConfig config = ApiConnectionConfig.create(
                        "http://" + InetAddress.getLoopbackAddress().getHostAddress() + ":" + server.getLocalPort(), true, null);
                long started = System.nanoTime();
                ReportTransport.Response response = new ReportTransport(config).newCall(request()).execute();
                long elapsedMillis = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started);

                assertEquals("r1", response.report().id());
                assertTrue("response must arrive after the old per-target read timeout", elapsedMillis >= 1_000L);
                served.get(2, TimeUnit.SECONDS);
            } finally {
                server.close();
                serverExecutor.shutdownNow();
            }
        }
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
        assertEquals(15_000L, ReportTransport.DEADLINE_GRACE_MILLIS);
        assertEquals(315_000L, ReportTransport.MAX_DEADLINE_MILLIS);
        assertEquals(ReportTransport.MAX_DEADLINE_MILLIS, ReportTransport.deadlineMillis(tenAttempts));
        ReportRequest defaults = ReportRequest.builder().timeoutMs(2_000)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "example.test").build())
                .addTarget(TargetInput.of(CheckKind.DNS, "other.test")).build();
        assertEquals(25_000L, ReportTransport.deadlineMillis(defaults));
        ReportRequest multipleAttempts = ReportRequest.builder().timeoutMs(1_000)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "first.test").attempts(3).build())
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "second.test").attempts(7).build()).build();
        assertEquals(22_000L, ReportTransport.deadlineMillis(multipleAttempts));
        ReportRequest nearMaximum = ReportRequest.builder().timeoutMs(29_999)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "example.test").attempts(10).build()).build();
        assertEquals(314_990L, ReportTransport.deadlineMillis(nearMaximum));
        assertEquals(ReportTransport.MAX_DEADLINE_MILLIS,
                ReportTransport.boundedDeadlineMillis(7L, Long.MAX_VALUE, 7L));
        assertEquals(314_999L,
                ReportTransport.boundedDeadlineMillis(0L, 315_000L, TimeUnit.MILLISECONDS.toNanos(1L)));
        assertEquals(0L, ReportTransport.boundedDeadlineMillis(
                0L, 315_000L, TimeUnit.MILLISECONDS.toNanos(315_000L)));
        assertEquals(315_000L, ReportTransport.boundedDeadlineMillis(10L, 315_000L, 9L));
        assertEquals(0L,
                ReportTransport.boundedDeadlineMillis(Long.MIN_VALUE, 315_000L, Long.MAX_VALUE));
        assertEquals(1L, ReportTransport.boundedDeadlineMillis(
                Long.MAX_VALUE - 500_000L, 2L, Long.MIN_VALUE + 499_999L));
        assertEquals(1L, ReportTransport.boundedDeadlineMillis(0L, 1L, 1L));
        assertEquals(1, ReportTransport.toSocketTimeoutMillis(1L));
        assertEquals(1, ReportTransport.toSocketTimeoutMillis(0L));
        assertEquals(315_000, ReportTransport.toSocketTimeoutMillis(315_000L));
        assertEquals(315_000, ReportTransport.toSocketTimeoutMillis(Long.MAX_VALUE));
    }

    @Test public void exactRemainingBudgetConfiguresBothConnectAndReadTimeouts() throws Exception {
        ReportRequest maximum = ReportRequest.builder().timeoutMs(30_000)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "example.test").attempts(10).build()).build();
        FakeConnection exact = new FakeConnection(200, bytes(VALID));
        transport("https://api.example.test", null, exact, new FakeClock(), new FakeScheduler())
                .newCall(maximum).execute();
        assertEquals(315_000, exact.connectTimeout);
        assertEquals(315_000, exact.readTimeout);

        FakeClock elapsedClock = new FakeClock();
        FakeConnection elapsed = new FakeConnection(200, bytes(VALID));
        ReportTransport elapsedTransport = new ReportTransport(
                ApiConnectionConfig.create("https://api.example.test", false, null),
                url -> { elapsedClock.nanos = TimeUnit.MILLISECONDS.toNanos(1L); return elapsed; },
                elapsedClock, new FakeScheduler());
        elapsedTransport.newCall(maximum).execute();
        assertEquals(314_999, elapsed.connectTimeout);
        assertEquals(314_999, elapsed.readTimeout);
    }

    @Test public void elapsedSetupAndClockRegressionCannotExtendSocketTimeouts() throws Exception {
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        SequenceClock clock = new SequenceClock(0L,
                TimeUnit.MILLISECONDS.toNanos(5_000L),
                TimeUnit.MILLISECONDS.toNanos(4_000L));
        FakeScheduler scheduler = new FakeScheduler();

        assertEquals("r1", transport("https://api.example.test", null, connection, clock, scheduler)
                .newCall(request()).execute().report().id());

        assertEquals(11_000L, scheduler.delayMillis);
        assertEquals(11_000, connection.connectTimeout);
        assertEquals(11_000, connection.readTimeout);
    }

    @Test public void schedulerElapsedTimeIsSubtractedBeforeSocketConfiguration() throws Exception {
        FakeClock clock = new FakeClock();
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        ReportTransport.Scheduler scheduler = (task, delayMillis) -> {
            clock.nanos = TimeUnit.MILLISECONDS.toNanos(1_000L);
            return () -> { };
        };

        assertEquals("r1", transport("https://api.example.test", null, connection, clock, scheduler)
                .newCall(request()).execute().report().id());

        assertEquals(15_000, connection.connectTimeout);
        assertEquals(15_000, connection.readTimeout);
    }

    @Test public void exactDeadlineBoundaryIsMeasuredFromExecuteStart() throws Exception {
        ReportRequest maximum = ReportRequest.builder().timeoutMs(30_000)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "example.test").attempts(10).build()).build();

        FakeClock beforeClock = new FakeClock();
        FakeConnection before = new BoundaryConnection(200, bytes(VALID), beforeClock, 314_999L);
        assertEquals("r1", transport("https://api.example.test", null, before, beforeClock, new FakeScheduler())
                .newCall(maximum).execute().report().id());

        FakeClock atClock = new FakeClock();
        FakeConnection at = new BoundaryConnection(200, bytes(VALID), atClock, 315_000L);
        TransportException timeout = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, at, atClock, new FakeScheduler()).newCall(maximum).execute());
        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
    }

    @Test public void setupTimeReducesScheduledDelayAndExpiryStopsBeforeNetworkProgress() throws Exception {
        FakeClock delayedClock = new FakeClock();
        FakeConnection delayed = new FakeConnection(200, bytes(VALID));
        FakeScheduler delayedScheduler = new FakeScheduler();
        ReportTransport delayedTransport = new ReportTransport(
                ApiConnectionConfig.create("https://api.example.test", false, null),
                url -> { delayedClock.nanos = TimeUnit.MILLISECONDS.toNanos(5_000L); return delayed; },
                delayedClock, delayedScheduler);
        assertEquals("r1", delayedTransport.newCall(request()).execute().report().id());
        assertEquals(11_000L, delayedScheduler.delayMillis);
        assertEquals(11_000, delayed.connectTimeout);
        assertEquals(11_000, delayed.readTimeout);

        FakeClock expiredClock = new FakeClock();
        FakeConnection expired = new FakeConnection(200, bytes(VALID));
        FakeScheduler expiredScheduler = new FakeScheduler();
        ReportTransport expiredTransport = new ReportTransport(
                ApiConnectionConfig.create("https://api.example.test", false, null),
                url -> { expiredClock.nanos = TimeUnit.MILLISECONDS.toNanos(16_000L); return expired; },
                expiredClock, expiredScheduler);
        TransportException timeout = assertThrows(TransportException.class,
                () -> expiredTransport.newCall(request()).execute());
        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertNull(expiredScheduler.task);
        assertNull(expired.method);
        assertEquals(0, expired.output.size());
        assertEquals(1, expired.disconnects);
    }

    @Test public void exactExpiryBeforeConnectionOpenSkipsNetworkEntirely() throws Exception {
        SequenceClock clock = new SequenceClock(0L, TimeUnit.MILLISECONDS.toNanos(16_000L));
        int[] opens = {0};
        ReportTransport transport = new ReportTransport(
                ApiConnectionConfig.create("https://api.example.test", false, null),
                url -> { opens[0]++; throw new AssertionError("expired call must not open a connection"); },
                clock, new FakeScheduler());

        TransportException timeout = assertThrows(TransportException.class, transport.newCall(request())::execute);

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(0, opens[0]);
    }

    @Test public void sharedSessionBudgetLeavesAtMost305SecondsAfterDiscovery() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        clock.nanos = TimeUnit.MILLISECONDS.toNanos(10_000L);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        FakeScheduler scheduler = new FakeScheduler();
        ReportRequest maximum = ReportRequest.builder().timeoutMs(30_000)
                .addTarget(TargetInput.builder(CheckKind.TRACEROUTE, "example.test").attempts(10).build()).build();
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);

        new ReportTransport(config, url -> connection, clock, scheduler, deadline)
                .newCall(maximum).execute();

        assertEquals(305_000L, scheduler.delayMillis);
        assertEquals(305_000, connection.connectTimeout);
        assertEquals(305_000, connection.readTimeout);
    }

    @Test public void shortRequestDeadlineWinsOverLongSharedBudget() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        clock.nanos = TimeUnit.MILLISECONDS.toNanos(10_000L);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        FakeScheduler scheduler = new FakeScheduler();
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);

        new ReportTransport(config, url -> connection, clock, scheduler, deadline)
                .newCall(request()).execute();

        assertEquals(16_000L, scheduler.delayMillis);
        assertEquals(16_000, connection.connectTimeout);
        assertEquals(16_000, connection.readTimeout);
    }

    @Test public void exactSharedExpirySkipsReportConnectionOpen() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        clock.nanos = TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
        int[] opens = {0};
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        ReportTransport transport = new ReportTransport(config, url -> {
            opens[0]++;
            throw new AssertionError("expired report must not open a connection");
        }, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class,
                () -> transport.newCall(request()).execute());

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(0, opens[0]);
    }

    @Test public void sharedExpiryDuringIoAlreadyWinsLateNetworkFailure() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        connection.onResponse = () -> clock.nanos =
                TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
        connection.responseFailure = new IOException("late network failure");
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        ReportTransport transport = new ReportTransport(
                config, url -> connection, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class,
                () -> transport.newCall(request()).execute());

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(1, connection.disconnects);
    }

    @Test public void exactSharedExpiryDuringContentLengthSkipsSuccessBodyOpen() throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(200, bytes(VALID));
        connection.onContentLength = () -> clock.nanos =
                TimeUnit.MILLISECONDS.toNanos(ReportTransport.MAX_DEADLINE_MILLIS);
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        ReportTransport transport = new ReportTransport(
                config, url -> connection, clock, new FakeScheduler(), deadline);

        TransportException timeout = assertThrows(TransportException.class,
                () -> transport.newCall(request()).execute());

        assertEquals(TransportException.Kind.TIMEOUT, timeout.kind());
        assertEquals(0, connection.inputRequests);
        assertEquals(1, connection.disconnects);
    }

    @Test public void exactExpiryAtEverySynchronousBoundaryStopsBeforeTheNextNetworkCall() throws Exception {
        for (int run = 0; run < 100; run++)
            for (Boundary boundary : Boundary.values()) assertReportBoundary(boundary, 16_000L, true);
    }

    @Test public void oneMillisecondRemainingAtEverySynchronousBoundaryStillAllowsTheCurrentCall() throws Exception {
        for (Boundary boundary : Boundary.values()) assertReportBoundary(boundary, 15_999L, false);
    }

    @Test public void classifiesNetworkSocketTimeoutMalformedSuccessAndStructuredErrorsWithoutProse() throws Exception {
        FakeConnection network = new FakeConnection(200, bytes(VALID)); network.responseFailure = new IOException("Bearer stolen");
        assertKind(TransportException.Kind.NETWORK, network);
        FakeConnection timeout = new FakeConnection(200, bytes(VALID)); timeout.responseFailure = new SocketTimeoutException("secret");
        assertKind(TransportException.Kind.TIMEOUT, timeout);
        FakeConnection malformed = new FakeConnection(200, bytes("<html>secret</html>"));
        assertKind(TransportException.Kind.INVALID_RESPONSE, malformed);

        for (int status : new int[]{401, 422, 429, 503}) {
            String message = expectedMessage(status);
            FakeConnection operational = new FakeConnection(status,
                    bytes("{\"error\":{\"code\":\"" + expectedCode(status) + "\",\"message\":\"" + message + "\"}}\n"));
            operational.headers.put("Retry-After", "2");
            TransportException error = assertThrows(TransportException.class,
                    () -> transport("https://api.example.test", "client-secret", operational, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
            assertEquals(TransportException.Kind.API, error.kind());
            assertEquals(status, error.apiError().orElseThrow().status());
            assertEquals(expectedCode(status), error.apiError().orElseThrow().code());
            assertEquals(message, error.apiError().orElseThrow().safeMessage());
            assertFalse(error.toString().contains("client-secret"));
        }
    }

    @Test public void errorBodyIsReadThroughIndependentSmallBound() throws Exception {
        byte[] huge = new byte[ReportTransport.MAX_ERROR_BODY_BYTES + 1];
        FakeConnection connection = new FakeConnection(401, huge); connection.contentLength = -1;
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, connection, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertEquals(TransportException.Kind.API, error.kind());
        assertEquals("invalid_server_response", error.apiError().orElseThrow().code());
        assertTrue(connection.inputBytesRead <= ReportTransport.MAX_ERROR_BODY_BYTES + 1);
    }

    @Test public void strictErrorJsonNeverBecomesNetworkFailureAndHonorsExactBoundsCount100() throws Exception {
        String canonical = "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"}}";
        String duplicateCode = "{\"error\":{\"code\":\"server_busy\",\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"}}";
        String duplicateError = "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"},\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"}}";
        String deep = "{\"error\":" + "{".repeat(12_000) + "}".repeat(12_000) + "}";
        for (int run = 0; run < 100; run++) for (byte[] body : new byte[][]{
                bytes(canonical + canonical), bytes(canonical + " HOSTILE-TRAILING"),
                bytes(duplicateCode), bytes(duplicateError), bytes(deep),
                new byte[]{'{', '"', 'e', 'r', 'r', 'o', 'r', '"', ':', '"', (byte) 0xed,
                        (byte) 0xa0, (byte) 0x80, '"', '}'}}) {
            FakeConnection malformed = new FakeConnection(503, body);
            TransportException failure = assertThrows(TransportException.class,
                    () -> transport("https://api.example.test", null, malformed, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
            assertEquals(TransportException.Kind.API, failure.kind());
            assertInvalidApiError(failure, 503);
        }

        byte[] prefix = bytes(canonical);
        byte[] exact = java.util.Arrays.copyOf(prefix, ReportTransport.MAX_ERROR_BODY_BYTES);
        java.util.Arrays.fill(exact, prefix.length, exact.length, (byte) ' ');
        FakeConnection accepted = new FakeConnection(503, exact);
        TransportException typed = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, accepted, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertEquals("server_busy", typed.apiError().orElseThrow().code());

        FakeConnection over = new FakeConnection(503, java.util.Arrays.copyOf(exact, exact.length + 1));
        TransportException rejected = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", null, over, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertInvalidApiError(rejected, 503);
    }

    private static void assertInvalidApiError(TransportException failure, int status) {
        assertEquals(status, failure.apiError().orElseThrow().status());
        assertEquals("invalid_server_response", failure.apiError().orElseThrow().code());
        assertFalse(failure.apiError().orElseThrow().retryable());
        assertFalse(failure.apiError().orElseThrow().retryAt().isPresent());
        assertFalse(failure.toString().contains("HOSTILE"));
    }

    @Test public void structuredStatusWinsWhenErrorBodyCleanupFails() throws Exception {
        FakeConnection connection = new FakeConnection(401, bytes("{}"));
        connection.input = new ByteArrayInputStream(bytes("{}")) {
            @Override public void close() throws IOException { throw new IOException("Bearer reflected server prose"); }
        };
        TransportException error = assertThrows(TransportException.class,
                () -> transport("https://api.example.test", "client-secret", connection, new FakeClock(), new FakeScheduler()).newCall(request()).execute());
        assertEquals(TransportException.Kind.API, error.kind());
        assertEquals("invalid_server_response", error.apiError().orElseThrow().code());
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
        assertEquals("invalid_server_response", error.apiError().orElseThrow().code());
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

    private static void assertReportBoundary(Boundary boundary, long elapsedMillis, boolean expires)
            throws Exception {
        FakeClock clock = new FakeClock();
        OperationDeadline deadline = new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
        FakeConnection connection = new FakeConnection(boundary == Boundary.HEADER ? 401 : 200, bytes(VALID));
        Runnable advance = () -> clock.nanos = TimeUnit.MILLISECONDS.toNanos(elapsedMillis);
        if (boundary == Boundary.CONFIG) connection.onRequestProperty = advance;
        if (boundary == Boundary.OUTPUT_OPEN) connection.onOutputOpen = advance;
        if (boundary == Boundary.OUTPUT_WRITE) connection.onOutputWrite = advance;
        if (boundary == Boundary.OUTPUT_CLOSE) connection.onOutputClose = advance;
        if (boundary == Boundary.RESPONSE) connection.onResponse = advance;
        if (boundary == Boundary.CONTENT_LENGTH) connection.onContentLength = advance;
        if (boundary == Boundary.INPUT_OPEN) connection.onInputOpen = advance;
        if (boundary == Boundary.INPUT_READ) connection.onInputRead = advance;
        if (boundary == Boundary.INPUT_CLOSE) connection.onInputClose = advance;
        if (boundary == Boundary.HEADER) connection.onHeader = advance;
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        ReportTransport transport = new ReportTransport(config, url -> {
            if (boundary == Boundary.OPEN) advance.run();
            return connection;
        }, clock, new FakeScheduler(), deadline);

        if (expires) {
            TransportException timeout = assertThrows(TransportException.class,
                    () -> transport.newCall(request()).execute());
            assertEquals(boundary.name(), TransportException.Kind.TIMEOUT, timeout.kind());
            switch (boundary) {
                case OPEN, CONFIG -> assertEquals(boundary.name(), 0, connection.outputRequests);
                case OUTPUT_OPEN -> assertEquals(boundary.name(), 0, connection.outputWrites);
                case OUTPUT_WRITE -> assertEquals(boundary.name(), 0, connection.outputCloses);
                case OUTPUT_CLOSE -> assertEquals(boundary.name(), 0, connection.responseRequests);
                case RESPONSE -> assertEquals(boundary.name(), 0, connection.contentLengthRequests);
                case CONTENT_LENGTH -> assertEquals(boundary.name(), 0, connection.inputRequests);
                case INPUT_OPEN -> assertEquals(boundary.name(), 0, connection.inputReadRequests);
                case INPUT_READ -> {
                    assertEquals(boundary.name(), 1, connection.inputReadRequests);
                    assertEquals(boundary.name(), 0, connection.inputCloses);
                }
                case INPUT_CLOSE, HEADER -> { }
            }
        } else if (boundary == Boundary.HEADER) {
            TransportException api = assertThrows(TransportException.class,
                    () -> transport.newCall(request()).execute());
            assertEquals(boundary.name(), TransportException.Kind.API, api.kind());
        } else {
            assertEquals(boundary.name(), "r1", transport.newCall(request()).execute().report().id());
        }
        assertEquals(boundary.name(), 1, connection.disconnects);
    }

    private static String expectedCode(int status) {
        return switch (status) { case 401 -> "unauthorized"; case 422 -> "invalid_request"; case 429 -> "rate_limited"; default -> "server_busy"; };
    }

    private static String expectedMessage(int status) {
        return switch (status) {
            case 401 -> "valid API credentials are required";
            case 422 -> "request is invalid";
            case 429 -> "per-client request limit exceeded";
            default -> "report capacity is temporarily unavailable";
        };
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

    private static String readHttpHeaders(InputStream input) throws IOException {
        ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        int matched = 0;
        while (matched < 4) {
            int value = input.read();
            if (value < 0) throw new IOException("request ended before headers");
            bytes.write(value);
            int expected = switch (matched) { case 0, 2 -> '\r'; default -> '\n'; };
            matched = value == expected ? matched + 1 : (value == '\r' ? 1 : 0);
            if (bytes.size() > 64 * 1024) throw new IOException("request headers too large");
        }
        return bytes.toString(StandardCharsets.US_ASCII);
    }

    private static int httpContentLength(String headers) throws IOException {
        for (String line : headers.split("\\r\\n")) {
            if (line.regionMatches(true, 0, "Content-Length:", 0, "Content-Length:".length())) {
                return Integer.parseInt(line.substring("Content-Length:".length()).trim());
            }
        }
        throw new IOException("missing Content-Length");
    }

    private static byte[] bytes(String value) { return value.getBytes(StandardCharsets.UTF_8); }

    private static class FakeClock implements ReportTransport.Clock {
        long nanos;
        @Override public long nanoTime() { return nanos; }
        @Override public Instant now() { return Instant.EPOCH; }
    }
    private static final class SequenceClock extends FakeClock {
        private final long[] values;
        private int index;
        SequenceClock(long... values) { this.values = values; }
        @Override public long nanoTime() {
            if (index < values.length) nanos = values[index++];
            return nanos;
        }
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
        ByteArrayOutputStream output = new ByteArrayOutputStream() {
            @Override public synchronized void write(byte[] bytes, int offset, int length) {
                outputWrites++; super.write(bytes, offset, length); onOutputWrite.run();
            }
            @Override public void close() throws IOException { outputCloses++; onOutputClose.run(); super.close(); }
        };
        InputStream input;
        int status; long contentLength = -1;
        int outputRequests; int outputWrites; int outputCloses; int responseRequests; int contentLengthRequests;
        int inputRequests; int inputReadRequests; int inputCloses; int inputBytesRead; int disconnects;
        String method; int connectTimeout; int readTimeout; IOException responseFailure; RuntimeException disconnectFailure;
        Runnable onRequestProperty = () -> {}; Runnable onOutputOpen = () -> {}; Runnable onOutputWrite = () -> {};
        Runnable onOutputClose = () -> {}; Runnable onResponse = () -> {}; Runnable onContentLength = () -> {};
        Runnable onInputOpen = () -> {}; Runnable onInputRead = () -> {}; Runnable onInputClose = () -> {};
        Runnable onHeader = () -> {};
        FakeConnection(int status, byte[] body) throws Exception { super(new URL("https://fake.invalid")); this.status=status; this.input=counting(body); }
        private InputStream counting(byte[] body) {
            return new ByteArrayInputStream(body) {
                @Override public synchronized int read(byte[] target, int off, int len) {
                    inputReadRequests++; int count = super.read(target, off, len);
                    if (count > 0) inputBytesRead += count; onInputRead.run(); return count;
                }
                @Override public synchronized int read() {
                    inputReadRequests++; int value=super.read(); if(value>=0) inputBytesRead++;
                    onInputRead.run(); return value;
                }
                @Override public void close() throws IOException { inputCloses++; onInputClose.run(); super.close(); }
            };
        }
        @Override public void setRequestMethod(String method) { this.method=method; }
        @Override public void setRequestProperty(String key,String value) { headers.put(key,value); onRequestProperty.run(); }
        @Override public String getHeaderField(String key) { onHeader.run(); return headers.get(key); }
        @Override public java.util.Map<String,java.util.List<String>> getHeaderFields() {
            onHeader.run();
            java.util.Map<String,java.util.List<String>> values = new java.util.LinkedHashMap<>();
            for (java.util.Map.Entry<String,String> entry : headers.entrySet())
                values.put(entry.getKey(), java.util.List.of(entry.getValue()));
            return values;
        }
        @Override public void setConnectTimeout(int value) { connectTimeout=value; }
        @Override public void setReadTimeout(int value) { readTimeout=value; }
        @Override public java.io.OutputStream getOutputStream() { outputRequests++; onOutputOpen.run(); return output; }
        @Override public int getResponseCode() throws IOException {
            responseRequests++;
            onResponse.run();
            if(responseFailure!=null) throw responseFailure;
            return status;
        }
        @Override public long getContentLengthLong() { contentLengthRequests++; onContentLength.run(); return contentLength; }
        @Override public InputStream getInputStream() { inputRequests++; onInputOpen.run(); return input; }
        @Override public InputStream getErrorStream() { inputRequests++; onInputOpen.run(); return input; }
        @Override public void disconnect() { disconnects++; if (disconnectFailure != null) throw disconnectFailure; }
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() {}
    }

    private static final class BoundaryConnection extends FakeConnection {
        private final FakeClock clock;
        private final long boundaryMillis;
        BoundaryConnection(int status, byte[] body, FakeClock clock, long boundaryMillis) throws Exception {
            super(status, body); this.clock = clock; this.boundaryMillis = boundaryMillis;
        }
        @Override public int getResponseCode() {
            clock.nanos = TimeUnit.MILLISECONDS.toNanos(boundaryMillis);
            return status;
        }
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
