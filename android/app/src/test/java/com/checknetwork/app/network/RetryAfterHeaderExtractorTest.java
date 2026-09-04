package com.checknetwork.app.network;

import static org.junit.Assert.*;

import java.io.IOException;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.AbstractMap;
import java.util.Arrays;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class RetryAfterHeaderExtractorTest {
    @Test public void extractsExactlyOneValueAcrossAllCaseInsensitiveHeaderFields() throws Exception {
        assertEquals("30", RetryAfterHeaderExtractor.extract(connection(map("Retry-After", List.of("30")))));
        assertEquals("45", RetryAfterHeaderExtractor.extract(connection(map("retry-after", List.of("45")))));
        Map<String, List<String>> withStatusLine = new LinkedHashMap<>();
        withStatusLine.put(null, List.of("HTTP/1.1 429 Too Many Requests"));
        withStatusLine.put("Content-Type", List.of("application/json"));
        withStatusLine.put("RETRY-AFTER", List.of("60"));
        assertEquals("60", RetryAfterHeaderExtractor.extract(connection(withStatusLine)));
    }

    @Test public void duplicateCommaCombinedZeroAndNullValuesAreNotCanonical() throws Exception {
        assertNull(RetryAfterHeaderExtractor.extract(connection(map("Retry-After", Arrays.asList("1", "2")))));
        Map<String, List<String>> splitCase = new LinkedHashMap<>();
        splitCase.put("Retry-After", List.of("1"));
        splitCase.put("retry-after", List.of("2"));
        assertNull(RetryAfterHeaderExtractor.extract(connection(splitCase)));
        assertEquals("1, 2", RetryAfterHeaderExtractor.extract(connection(map("Retry-After", List.of("1, 2")))));
        assertNull(RetryAfterHeaderExtractor.extract(connection(Collections.emptyMap())));
        assertNull(RetryAfterHeaderExtractor.extract(connection(map("Retry-After", List.of()))));
        assertNull(RetryAfterHeaderExtractor.extract(connection(map("Retry-After", null))));
        assertNull(RetryAfterHeaderExtractor.extract(connection(map("Retry-After", Arrays.asList((String) null)))));
    }

    @Test public void inaccessibleOrMalformedHeaderCollectionsAreTreatedAsAbsent() throws Exception {
        HttpURLConnection inaccessible = connection(Collections.emptyMap());
        inaccessible = new StubConnection() {
            @Override public Map<String, List<String>> getHeaderFields() {
                throw new IllegalStateException("hostile inaccessible collection");
            }
        };
        assertNull(RetryAfterHeaderExtractor.extract(inaccessible));

        Map<String, List<String>> malformed = new AbstractMap<>() {
            @Override public Set<Entry<String, List<String>>> entrySet() {
                throw new IllegalStateException("hostile malformed collection");
            }
        };
        assertNull(RetryAfterHeaderExtractor.extract(connection(malformed)));
    }

    @Test public void transportSourcesUseSharedAllValuesExtractorAndForbidSingleValueLookup() throws Exception {
        Path sourceRoot = Paths.get(System.getProperty("user.dir"), "src", "main", "java",
                "com", "checknetwork", "app", "network");
        for (String file : List.of("ReportTransport.java", "CapabilitiesTransport.java")) {
            String source = new String(Files.readAllBytes(sourceRoot.resolve(file)), StandardCharsets.UTF_8);
            assertFalse(file + " must not use single-value header lookup", source.contains(".getHeaderField("));
            assertTrue(file + " must use the shared extractor", source.contains("RetryAfterHeaderExtractor.extract("));
        }
    }

    private static Map<String, List<String>> map(String name, List<String> values) {
        Map<String, List<String>> result = new LinkedHashMap<>();
        result.put(name, values);
        return result;
    }

    private static HttpURLConnection connection(Map<String, List<String>> fields) throws Exception {
        return new StubConnection() {
            @Override public Map<String, List<String>> getHeaderFields() { return fields; }
        };
    }

    private abstract static class StubConnection extends HttpURLConnection {
        StubConnection() throws Exception { super(new URL("https://fake.invalid")); }
        @Override public void disconnect() { }
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() throws IOException { }
    }
}
