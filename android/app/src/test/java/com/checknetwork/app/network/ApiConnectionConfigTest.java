package com.checknetwork.app.network;

import static org.junit.Assert.*;

import java.net.URI;
import org.junit.Test;

public final class ApiConnectionConfigTest {
    @Test public void acceptsOnlyStrictHttpsOriginsForRelease() {
        ApiConnectionConfig config = ApiConnectionConfig.create("https://API.Example.test:8443/", false, null);
        assertEquals(URI.create("https://api.example.test:8443"), config.baseUri());
        assertEquals(URI.create("https://api.example.test:8443/api/v1/checks"), config.checksEndpoint());
        assertEquals(URI.create("https://api.example.test:8443/api/v1/reports"), config.reportsEndpoint());

        for (String invalid : new String[]{
                "http://api.example.test", "https://api.example.test/path", "https://api.example.test/?q=1",
                "https://api.example.test/#fragment", "https://user@api.example.test", "https://api.example.test/../x",
                "https://api.example.test:99999", "https://api.example.test:0", "https://", "not a url"}) {
            assertThrows(IllegalArgumentException.class, () -> ApiConnectionConfig.create(invalid, false, null));
        }
    }

    @Test public void debugAllowsOnlyExactLocalCleartextHosts() {
        for (String allowed : new String[]{
                "http://localhost:8080", "http://127.0.0.1", "http://[::1]:8080", "http://10.0.2.2:8080", "http://10.0.3.2:8080"}) {
            assertEquals("http", ApiConnectionConfig.create(allowed, true, null).baseUri().getScheme());
        }
        for (String blocked : new String[]{
                "http://example.test", "http://localhost.example.test", "http://127.0.0.2",
                "http://10.0.2.20", "http://[::ffff:127.0.0.1]"}) {
            assertThrows(IllegalArgumentException.class, () -> ApiConnectionConfig.create(blocked, true, null));
        }
    }

    @Test public void bearerIsBoundedSanitizedAndAvailableOnlyForHttps() {
        ApiConnectionConfig secure = ApiConnectionConfig.create("https://api.example.test", false, "  secret-token  ");
        assertEquals("Bearer secret-token", secure.authorizationHeader().orElseThrow());
        assertFalse(ApiConnectionConfig.create("http://localhost:8080", true, "secret-token").authorizationHeader().isPresent());
        assertFalse(ApiConnectionConfig.create("https://api.example.test", false, "   ").authorizationHeader().isPresent());
        assertThrows(IllegalArgumentException.class, () -> ApiConnectionConfig.create("https://api.example.test", false, "abc\r\nInjected: yes"));
        assertThrows(IllegalArgumentException.class, () -> ApiConnectionConfig.create("https://api.example.test", false, "x".repeat(ApiConnectionConfig.MAX_BEARER_BYTES + 1)));
        assertEquals(ApiConnectionConfig.MAX_BEARER_BYTES,
                ApiConnectionConfig.create("https://api.example.test", false, "x".repeat(ApiConnectionConfig.MAX_BEARER_BYTES)).authorizationHeader().orElseThrow().substring(7).length());
    }

    @Test public void credentialNeverAppearsInIdentityOrDiagnostics() {
        String secret = "credential-that-must-not-leak";
        ApiConnectionConfig first = ApiConnectionConfig.create("https://api.example.test", false, secret);
        ApiConnectionConfig second = ApiConnectionConfig.create("https://api.example.test", false, "different-secret");
        assertEquals(first.signature(), second.signature());
        assertFalse(first.signature().contains(secret));
        assertFalse(first.toString().contains(secret));
        IllegalArgumentException error = assertThrows(IllegalArgumentException.class,
                () -> ApiConnectionConfig.create("invalid", false, secret));
        assertFalse(error.toString().contains(secret));
    }
}
