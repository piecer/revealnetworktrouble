package com.checknetwork.app.network;

import static org.junit.Assert.*;

import com.checknetwork.app.core.ApiError;
import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.core.TargetInput;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.time.Instant;
import java.util.AbstractMap;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class ApiErrorTransportContractTest {
    private static final Instant NOW = Instant.parse("2026-09-01T12:00:00Z");

    @Test public void bothTransportsConsumeAllSixteenExactFixtureRows() throws Exception {
        JSONArray fixtures = contract().getJSONArray("errors");
        assertEquals(16, fixtures.length());
        int exercised = 0;
        for (int index = 0; index < fixtures.length(); index++) {
            JSONObject fixture = fixtures.getJSONObject(index);
            for (boolean report : new boolean[]{false, true}) {
                FakeConnection connection = response(fixture.getInt("status"), fixture.getString("body"));
                connection.retryAfter("30");
                ApiError error = execute(report, connection);
                assertEquals(fixture.getString("key"), fixture.getInt("status"), error.status());
                assertEquals(fixture.getString("key"), fixture.getString("code"), error.code());
                assertEquals(fixture.getString("key"), fixture.getString("message"), error.safeMessage());
                assertEquals(fixture.getString("key"), fixture.getBoolean("retryable"), error.retryable());
                assertEquals(fixture.getString("key"), fixture.getBoolean("retry_after"), error.retryAt().isPresent());
                if (fixture.getBoolean("retry_after")) assertEquals(NOW.plusSeconds(30), error.retryAt().orElseThrow());
                assertEquals(1, connection.headerCollections);
                assertTrue(connection.headersCollectedBeforeDisconnect);
                assertEquals(1, connection.disconnects);
                exercised++;
            }
        }
        assertEquals(32, exercised);
    }

    @Test public void bothTransportsRunTheIdenticalProducerStructuralMutationCorpus() throws Exception {
        JSONObject fixture = contract();
        JSONArray rows = fixture.getJSONArray("errors");
        JSONArray mutations = fixture.getJSONArray("structural_mutations");
        assertEquals(35 + 3 * rows.length() + 2, mutations.length());
        assertEquals(85, mutations.length());
        int exercised = 0;
        for (int index = 0; index < mutations.length(); index++) {
            JSONObject mutation = mutations.getJSONObject(index);
            assertFalse(mutation.getString("name"), mutation.getBoolean("valid"));
            for (boolean report : new boolean[]{false, true}) {
                FakeConnection connection = response(mutation.getInt("status"), mutation.getString("body"));
                connection.retryAfter("30");
                ApiError error = execute(report, connection);
                assertInvalid(error, mutation.getInt("status"));
                assertDoesNotLeak(error, "HOSTILE");
                exercised++;
            }
        }
        assertEquals(170, exercised);
    }

    @Test public void bothTransportsApplyTheExactRetryAfterFixtureCorpus() throws Exception {
        JSONObject contract = contract();
        JSONArray cases = contract.getJSONArray("retry_after_cases");
        assertEquals(13, cases.length());
        int exercised = 0;
        for (int index = 0; index < cases.length(); index++) {
            JSONObject fixture = cases.getJSONObject(index);
            for (boolean report : new boolean[]{false, true}) {
                FakeConnection connection = response(429,
                        "{\"error\":{\"code\":\"rate_limited\",\"message\":\"per-client request limit exceeded\"}}\n");
                if (!fixture.isNull("header")) connection.retryAfter(fixture.getString("header"));
                ApiError error = execute(report, connection);
                assertEquals("rate_limited", error.code());
                assertTrue(error.retryable());
                assertEquals(fixture.getString("name"), fixture.getBoolean("valid"), error.retryAt().isPresent());
                if (fixture.getBoolean("valid"))
                    assertEquals(NOW.plusSeconds(fixture.getLong("seconds")), error.retryAt().orElseThrow());
                exercised++;
            }
        }
        assertEquals(26, exercised);
    }

    @Test public void duplicateAndInaccessibleHeaderCollectionsRemainAbsentWithoutInvalidatingTypedBody() throws Exception {
        for (boolean report : new boolean[]{false, true}) {
            for (HeaderMutation mutation : HeaderMutation.values()) {
                FakeConnection connection = response(429,
                        "{\"error\":{\"code\":\"rate_limited\",\"message\":\"per-client request limit exceeded\"}}\n");
                mutation.apply(connection);
                ApiError error = execute(report, connection);
                assertEquals(mutation.name(), "rate_limited", error.code());
                assertTrue(mutation.name(), error.retryable());
                assertFalse(mutation.name(), error.retryAt().isPresent());
                assertEquals(1, connection.disconnects);
            }
        }
    }

    @Test public void non2xxStatusSurvivesBodyReadOrCloseFailureInBothTransports() throws Exception {
        for (boolean report : new boolean[]{false, true}) {
            FakeConnection readFailure = response(503,
                    "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"}}\n");
            readFailure.input = new InputStream() {
                @Override public int read() throws IOException { throw new IOException("HOSTILE-READ"); }
                @Override public int read(byte[] bytes, int offset, int length) throws IOException {
                    throw new IOException("HOSTILE-READ");
                }
            };
            ApiError invalid = execute(report, readFailure);
            assertInvalid(invalid, 503);
            assertDoesNotLeak(invalid, "HOSTILE-READ");

            FakeConnection closeFailure = response(503,
                    "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"}}\n");
            byte[] body = closeFailure.body;
            closeFailure.input = new ByteArrayInputStream(body) {
                @Override public void close() throws IOException { throw new IOException("HOSTILE-CLOSE"); }
            };
            ApiError typed = execute(report, closeFailure);
            assertEquals("server_busy", typed.code());
            assertEquals("report capacity is temporarily unavailable", typed.safeMessage());
            assertDoesNotLeak(typed, "HOSTILE-CLOSE");
        }
    }

    private enum HeaderMutation {
        DUPLICATE_VALUES {
            @Override void apply(FakeConnection connection) { connection.headers.put("Retry-After", List.of("1", "2")); }
        },
        DUPLICATE_CASE_VARIANTS {
            @Override void apply(FakeConnection connection) {
                connection.headers.put("Retry-After", List.of("1"));
                connection.headers.put("retry-after", List.of("2"));
            }
        },
        COMMA_COMBINED {
            @Override void apply(FakeConnection connection) { connection.retryAfter("1, 2"); }
        },
        INACCESSIBLE {
            @Override void apply(FakeConnection connection) { connection.headerFailure = new IllegalStateException("HOSTILE-HEADER"); }
        },
        MALFORMED_COLLECTION {
            @Override void apply(FakeConnection connection) {
                connection.headersOverride = new AbstractMap<>() {
                    @Override public Set<Entry<String, List<String>>> entrySet() {
                        throw new IllegalStateException("HOSTILE-MALFORMED-HEADER");
                    }
                };
            }
        },
        ZERO_VALUES {
            @Override void apply(FakeConnection connection) { connection.headers.put("Retry-After", List.of()); }
        };
        abstract void apply(FakeConnection connection);
    }

    private static JSONObject contract() throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata",
                "api-error-contract.json").normalize();
        assertTrue("missing producer fixture at " + fixture, Files.isRegularFile(fixture));
        return new JSONObject(new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8));
    }

    private static FakeConnection response(int status, String body) throws Exception {
        return new FakeConnection(status, body.getBytes(StandardCharsets.UTF_8));
    }

    private static ApiError execute(boolean report, FakeConnection connection) throws Exception {
        ApiConnectionConfig config = ApiConnectionConfig.create("https://api.example.test", false, null);
        FixedClock clock = new FixedClock();
        ReportTransport.Scheduler scheduler = (task, delay) -> () -> { };
        TransportException failure;
        if (report) {
            ReportRequest request = ReportRequest.builder().timeoutMs(1_000)
                    .addTarget(TargetInput.of(CheckKind.DNS, "example.test")).build();
            ReportTransport transport = new ReportTransport(config, url -> connection, clock, scheduler);
            failure = assertThrows(TransportException.class, () -> transport.newCall(request).execute());
        } else {
            CapabilitiesTransport transport = new CapabilitiesTransport(config, url -> connection, clock, scheduler);
            failure = assertThrows(TransportException.class, () -> transport.newCall().execute());
        }
        assertEquals(TransportException.Kind.API, failure.kind());
        return failure.apiError().orElseThrow();
    }

    private static void assertInvalid(ApiError error, int status) {
        assertEquals(status, error.status());
        assertEquals("invalid_server_response", error.code());
        assertEquals("The server returned an invalid error response.", error.safeMessage());
        assertFalse(error.retryable());
        assertFalse(error.retryAt().isPresent());
    }

    private static void assertDoesNotLeak(ApiError error, String canary) {
        assertFalse((error.safeMessage() + error.toString()).contains(canary));
    }

    private static final class FixedClock implements ReportTransport.Clock {
        @Override public long nanoTime() { return 0L; }
        @Override public Instant now() { return NOW; }
    }

    private static final class FakeConnection extends HttpURLConnection {
        final byte[] body;
        final Map<String, List<String>> headers = new LinkedHashMap<>();
        final ByteArrayOutputStream output = new ByteArrayOutputStream();
        InputStream input;
        Map<String, List<String>> headersOverride;
        RuntimeException headerFailure;
        int headerCollections;
        int disconnects;
        boolean headersCollectedBeforeDisconnect;

        FakeConnection(int status, byte[] body) throws Exception {
            super(new URL("https://fake.invalid"));
            this.responseCode = status;
            this.body = body;
            this.input = new ByteArrayInputStream(body);
        }

        void retryAfter(String value) { headers.put("Retry-After", List.of(value)); }
        @Override public int getResponseCode() { return responseCode; }
        @Override public long getContentLengthLong() { return -1L; }
        @Override public InputStream getErrorStream() { return input; }
        @Override public java.io.OutputStream getOutputStream() { return output; }
        @Override public Map<String, List<String>> getHeaderFields() {
            headerCollections++;
            headersCollectedBeforeDisconnect = disconnects == 0;
            if (headerFailure != null) throw headerFailure;
            return headersOverride == null ? headers : headersOverride;
        }
        @Override public void disconnect() { disconnects++; }
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() { }
    }
}
