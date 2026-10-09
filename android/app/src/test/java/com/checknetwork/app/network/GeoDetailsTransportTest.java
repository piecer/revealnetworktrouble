package com.checknetwork.app.network;

import static org.junit.Assert.*;

import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.GeoDetailsTest;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.core.TargetInput;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.SocketTimeoutException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class GeoDetailsTransportTest {
    @Test public void oneRealPostOptsInAndOldServerIgnoringQueryIsAcceptedWithoutRetry() throws Exception {
        exchange(GeoDetailsTest.fixture("enrichment-upstream-report.json"), false, false);
    }

    @Test public void extendedResponsePreservesExactRawBytesOnRealTransport() throws Exception {
        exchange(GeoDetailsTest.fixture("geo-details-rich-compact-report.json"), true, false);
    }

    @Test public void malformedPresentSidecarPublishesNeitherRawNorModelAndNeverRetries() throws Exception {
        String raw = GeoDetailsTest.fixture("geo-details-rich-full-report.json");
        exchange(raw.substring(0, raw.indexOf("\"geo_details\":")) + "\"geo_details\":null}", false, true);
    }

    private static void exchange(String raw, boolean extended, boolean invalid) throws Exception {
        ReportRequest request = ReportRequest.builder().timeoutMs(1000)
                .addTarget(TargetInput.of(CheckKind.DNS, "example.test")).build();
        byte[] responseBytes = raw.getBytes(StandardCharsets.UTF_8);
        ExecutorService worker = Executors.newSingleThreadExecutor();
        try (ServerSocket server = new ServerSocket(0, 2, InetAddress.getByName("127.0.0.1"))) {
            server.setSoTimeout(500);
            Future<List<String>> observed = worker.submit(() -> {
                List<String> messages = new ArrayList<>();
                while (true) {
                    try (Socket socket = server.accept()) {
                        socket.setSoTimeout(3000);
                        InputStream input = socket.getInputStream();
                        ByteArrayOutputStream headerBytes = new ByteArrayOutputStream();
                        String headers;
                        do {
                            int next = input.read();
                            if (next < 0 || headerBytes.size() >= 16384) throw new AssertionError("incomplete headers");
                            headerBytes.write(next);
                            headers = headerBytes.toString(StandardCharsets.US_ASCII.name());
                        } while (!headers.endsWith("\r\n\r\n"));
                        int length = -1;
                        for (String line : headers.split("\r\n")) if (line.toLowerCase(java.util.Locale.ROOT).startsWith("content-length:"))
                            length = Integer.parseInt(line.substring(line.indexOf(':') + 1).trim());
                        assertTrue(length >= 0 && length < 16384);
                        byte[] body = input.readNBytes(length);
                        assertEquals(length, body.length);
                        assertArrayEquals(request.toJson().getBytes(StandardCharsets.UTF_8), body);
                        messages.add(headers);
                        socket.getOutputStream().write(("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: "
                                + responseBytes.length + "\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.US_ASCII));
                        socket.getOutputStream().write(responseBytes);
                        socket.getOutputStream().flush();
                    } catch (SocketTimeoutException done) { return messages; }
                }
            });
            ApiConnectionConfig config = ApiConnectionConfig.create("http://127.0.0.1:" + server.getLocalPort(), true, null);
            ReportTransport.Call call = new ReportTransport(config).newCall(request);
            if (invalid) {
                TransportException failure = assertThrows(TransportException.class, call::execute);
                assertEquals(TransportException.Kind.INVALID_RESPONSE, failure.kind());
            } else {
                ReportTransport.Response response = call.execute();
                assertEquals(raw, response.rawJson());
                assertEquals(extended, response.report().geoDetails().isPresent());
            }
            List<String> messages = observed.get(5, TimeUnit.SECONDS);
            assertEquals("no fallback diagnostic or retry", 1, messages.size());
            String headers = messages.get(0);
            assertTrue(headers, headers.startsWith("POST /api/v1/reports?geo_details=1 HTTP/1.1\r\n"));
            assertTrue(headers, headers.contains("Content-Type: application/json; charset=utf-8\r\n"));
            assertTrue(headers, headers.contains("Accept: application/json\r\n"));
            assertFalse(headers.contains("X-Geo"));
        } finally {
            worker.shutdownNow();
            assertTrue(worker.awaitTermination(5, TimeUnit.SECONDS));
        }
    }
}
