package com.checknetwork.app.core;

import static org.junit.Assert.*;

import java.time.Instant;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
public final class ApiErrorTest {
    @Test public void parsesFourOperationalStatusesIntoStableSafeErrors() {
        assertEquals("unauthorized", ApiError.parse(401, "{\"error\":{\"code\":\"unauthorized\",\"message\":\"Bearer SECRET\"}}", null, Instant.EPOCH).code());
        assertEquals("invalid_request", ApiError.parse(422, "{\"error\":{\"code\":\"invalid_request\",\"message\":\"bad\"}}", null, Instant.EPOCH).code());
        assertEquals("rate_limited", ApiError.parse(429, "{\"error\":{\"code\":\"rate_limited\",\"message\":\"slow\"}}", "3", Instant.EPOCH).code());
        assertEquals("server_busy", ApiError.parse(503, "{\"error\":{\"code\":\"server_busy\",\"message\":\"busy\"}}", "3", Instant.EPOCH).code());
    }

    @Test public void neverReflectsServerMessageOrUnknownCode() {
        String secret = "SUPER-secret-token-123";
        ApiError error = ApiError.parse(401, "{\"error\":{\"code\":\"" + secret + "\",\"message\":\"Bearer " + secret + "\"}}", null, Instant.EPOCH);
        assertEquals("unauthorized", error.code());
        assertFalse(error.safeMessage().contains(secret));
        assertFalse(error.toString().contains(secret));
    }

    @Test public void parsesRetryAfterSecondsAndHttpDateOnlyForRetryableErrors() {
        Instant now = Instant.parse("2026-09-02T00:00:00Z");
        assertEquals(now.plusSeconds(12), ApiError.parse(429, "", "12", now).retryAt().orElseThrow());
        assertEquals(Instant.parse("2026-09-02T00:01:00Z"), ApiError.parse(503, "", "Wed, 02 Sep 2026 00:01:00 GMT", now).retryAt().orElseThrow());
        assertFalse(ApiError.parse(401, "", "12", now).retryAt().isPresent());
        assertFalse(ApiError.parse(429, "", "-2", now).retryAt().isPresent());
        assertFalse(ApiError.parse(429, "", "2305843009213693952", now).retryAt().isPresent());
    }

    @Test @Config(sdk = 26) public void numericRetryAfterRunsOnMinSdkAndRejectsOverflow() {
        Instant now = Instant.parse("2026-09-02T00:00:00Z");
        assertEquals(now.plusSeconds(12), ApiError.parseRetryAfter("12", now));
        assertNull(ApiError.parseRetryAfter("9223372036854775808", now));
        assertNull(ApiError.parseRetryAfter("2305843009213693952", now));
    }

    @Test public void acceptsPolicyBlockedAndDocumentedInternalErrorsWithoutStatusCodeConfusion() {
        assertEquals("network_policy_blocked", ApiError.parse(422, "{\"error\":{\"code\":\"network_policy_blocked\"}}", null, Instant.EPOCH).code());
        assertEquals("unauthorized", ApiError.parse(401, "{\"error\":{\"code\":\"server_busy\"}}", null, Instant.EPOCH).code());
        for (String code : new String[]{"internal_error", "compact_response_too_large", "response_serialization_failed"}) {
            ApiError error = ApiError.parse(500, "{\"error\":{\"code\":\"" + code + "\",\"message\":\"SECRET\"}}", "2", Instant.EPOCH);
            assertEquals(code, error.code());
            assertTrue(error.retryable());
            assertEquals(Instant.EPOCH.plusSeconds(2), error.retryAt().orElseThrow());
            assertFalse(error.safeMessage().contains("SECRET"));
        }
    }

    @Test public void anyOtherHttpStatusBecomesBoundedGenericNonReflectingErrorWithExplicitRetryPolicy() {
        String secret = "REFLECTED-private-backend-value";
        ApiError client = ApiError.parse(418, "{\"error\":{\"code\":\"" + secret + "\",\"message\":\"" + secret + "\"}}", "9", Instant.EPOCH);
        assertEquals("http_418", client.code());
        assertFalse(client.retryable());
        assertFalse(client.retryAt().isPresent());
        assertFalse((client.safeMessage() + client.toString()).contains(secret));

        ApiError server = ApiError.parse(599, secret, "9", Instant.EPOCH);
        assertEquals("http_599", server.code());
        assertTrue(server.retryable());
        assertEquals(Instant.EPOCH.plusSeconds(9), server.retryAt().orElseThrow());
        assertTrue(server.code().length() <= 32);
    }
}
