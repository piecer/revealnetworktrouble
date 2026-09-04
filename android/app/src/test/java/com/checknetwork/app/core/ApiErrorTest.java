package com.checknetwork.app.core;

import static org.junit.Assert.*;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.time.Instant;
import java.util.LinkedHashSet;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
public final class ApiErrorTest {
    private static final Instant NOW = Instant.parse("2026-09-01T12:00:00Z");

    private static JSONObject contract() throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata",
                "api-error-contract.json").normalize();
        assertTrue("missing producer fixture at " + fixture, Files.isRegularFile(fixture));
        return new JSONObject(new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8));
    }

    @Test public void consumesAllSixteenExactProducerBodiesUsingOnlyLocalPolicy() throws Exception {
        JSONArray errors = contract().getJSONArray("errors");
        assertEquals(16, errors.length());
        for (int index = 0; index < errors.length(); index++) {
            JSONObject fixture = errors.getJSONObject(index);
            ApiError error = ApiError.parse(fixture.getInt("status"),
                    fixture.getString("body"),
                    "30", NOW);
            assertEquals(fixture.getString("key"), fixture.getInt("status"), error.status());
            assertEquals(fixture.getString("key"), fixture.getString("code"), error.code());
            assertEquals(fixture.getString("key"), fixture.getString("message"), error.safeMessage());
            assertEquals(fixture.getString("key"), fixture.getBoolean("retryable"), error.retryable());
            assertEquals(fixture.getString("key"), fixture.getBoolean("retry_after"), error.retryAt().isPresent());
            if (fixture.getBoolean("retry_after")) assertEquals(NOW.plusSeconds(30), error.retryAt().orElseThrow());
            assertTrue(fixture.getString("key"), fixture.getString("body").endsWith("\n"));
        }
    }

    @Test public void sharedProducerMutationCorpusHasOneFixedInvalidServerResponseOutcome() throws Exception {
        JSONObject fixture = contract();
        assertEquals("api-error-wire-v1", fixture.getString("schema"));
        JSONObject limits = fixture.getJSONObject("limits");
        assertEquals(65536, limits.getInt("body_utf16_units"));
        assertEquals(2, limits.getInt("depth"));
        assertEquals(10, limits.getInt("tokens"));
        assertEquals(3, limits.getInt("properties"));
        assertEquals(128, limits.getInt("string_utf16_units"));
        JSONArray mutations = fixture.getJSONArray("structural_mutations");
        assertEquals(35 + fixture.getJSONArray("errors").length() * 3 + 2, mutations.length());
        assertEquals(85, mutations.length());
        Set<String> names = new LinkedHashSet<>();
        for (int index = 0; index < mutations.length(); index++) {
            JSONObject mutation = mutations.getJSONObject(index);
            assertTrue(mutation.getString("name"), names.add(mutation.getString("name")));
            assertFalse(mutation.getString("name"), mutation.getBoolean("valid"));
            ApiError error = ApiError.parse(mutation.getInt("status"), mutation.getString("body"), "30", NOW);
            assertInvalid(error, mutation.getInt("status"));
        }
        assertEquals(mutations.length(), names.size());
    }

    @Test public void malformedAndUntypedBodiesNeverInferFromStatusOrReflectHostileData() {
        for (String body : new String[]{"<html>proxy secret</html>", "", "{oops", "{\"error\":null}",
                "{\"error\":{}}", "{\"error\":{\"code\":7}}"}) {
            ApiError error = ApiError.parse(503, body, "30", NOW);
            assertInvalid(error, 503);
            assertFalse((error.safeMessage() + error.toString()).contains("secret"));
        }
    }

    @Test public void retryAfterFixtureCorpusAppliesOnlyToRowsThatPermitIt() throws Exception {
        JSONObject contract = contract();
        JSONArray errors = contract.getJSONArray("errors");
        JSONArray cases = contract.getJSONArray("retry_after_cases");
        assertEquals(13, cases.length());
        int exercised = 0;
        for (int errorIndex = 0; errorIndex < errors.length(); errorIndex++) {
            JSONObject fixture = errors.getJSONObject(errorIndex);
            String body = fixture.getString("body");
            for (int caseIndex = 0; caseIndex < cases.length(); caseIndex++) {
                JSONObject retryCase = cases.getJSONObject(caseIndex);
                String header = retryCase.isNull("header") ? null : retryCase.getString("header");
                ApiError error = ApiError.parse(fixture.getInt("status"), body, header, NOW);
                boolean applicable = fixture.getBoolean("retry_after") && retryCase.getBoolean("valid");
                assertEquals(fixture.getString("key") + "/" + retryCase.getString("name"),
                        applicable, error.retryAt().isPresent());
                if (applicable) assertEquals(NOW.plusSeconds(retryCase.getLong("seconds")), error.retryAt().orElseThrow());
                assertEquals(fixture.getString("code"), error.code());
                assertEquals(fixture.getBoolean("retryable"), error.retryable());
                exercised++;
            }
        }
        assertEquals(errors.length() * cases.length(), exercised);
    }

    @Test @Config(sdk = 26) public void retryAfterAcceptsOnlyAsciiDecimalOneThroughThreeThousandSixHundred() {
        assertEquals(NOW.plusSeconds(1), ApiError.parseRetryAfter("1", NOW));
        assertEquals(NOW.plusSeconds(3600), ApiError.parseRetryAfter("3600", NOW));
        for (String malformed : new String[]{null, "", "0", "3601", "01", "+1", "-1", " 1", "1 ",
                "Thu, 03 Sep 2026 00:00:01 GMT", "999999999999999999999999999999", "1, 2",
                "12x", "１２", "1\t", "1\n", "1,2", "1, 1"}) {
            assertNull(String.valueOf(malformed), ApiError.parseRetryAfter(malformed, NOW));
        }
    }

    @Test public void acceptsExactlyOneClosedCanonicalObjectAndRejectsTrailingValuesAndDuplicates() {
        String canonical = errorBody("server_busy", "report capacity is temporarily unavailable");
        assertEquals("server_busy", ApiError.parse(503, canonical + " \n\t", "30", NOW).code());
        for (String malformed : new String[]{
                canonical + canonical,
                canonical + " HOSTILE-TRAILING-TEXT",
                "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"},"
                        + "\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"}}",
                "{\"error\":{\"code\":\"server_busy\",\"code\":\"server_busy\","
                        + "\"message\":\"report capacity is temporarily unavailable\"}}",
                "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\","
                        + "\"message\":\"report capacity is temporarily unavailable\"}}",
                "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\"},\"extra\":0}",
                "{\"error\":{\"code\":\"server_busy\",\"message\":\"report capacity is temporarily unavailable\",\"extra\":0}}",
                "{\"error\":{\"code\":\"server_busy\"}}",
                "{\"error\":{\"message\":\"report capacity is temporarily unavailable\"}}",
                "{\"error\":{\"code\":\"server_busy\",\"message\":7}}"
        }) assertInvalid(ApiError.parse(503, malformed, "30", NOW), 503);
    }

    @Test public void enforcesInclusiveCharacterAndStructuralBoundsWithoutRecursionOrErrors() {
        String canonical = errorBody("server_busy", "report capacity is temporarily unavailable");
        int maxBodyChars = 64 * 1024;
        String exact = canonical + " ".repeat(maxBodyChars - canonical.length());
        assertEquals(maxBodyChars, exact.length());
        assertEquals("server_busy", ApiError.parse(503, exact, "30", NOW).code());
        assertInvalid(ApiError.parse(503, exact + " ", "30", NOW), 503);

        String depthTwo = canonical;
        String depthThree = "{\"error\":{\"code\":\"server_busy\",\"message\":{}}}";
        assertEquals("server_busy", ApiError.parse(503, depthTwo, null, NOW).code());
        assertInvalid(ApiError.parse(503, depthThree, "30", NOW), 503);

        int nesting = 12_000;
        String deepArray = "{\"error\":" + "[".repeat(nesting) + "]".repeat(nesting) + "}";
        String deepObject = "{\"error\":" + "{\"x\":".repeat(4_000) + "0" + "}".repeat(4_001);
        assertTrue(deepArray.length() >= 24 * 1024 - 1024 && deepArray.length() <= 24 * 1024 + 1024);
        for (int run = 0; run < 100; run++) {
            assertInvalid(ApiError.parse(503, deepArray, "30", NOW), 503);
            assertInvalid(ApiError.parse(503, deepObject, "30", NOW), 503);
        }
    }

    @Test public void rejectsMalformedUtf16AndEscapedUnpairedSurrogates() {
        String message = "report capacity is temporarily unavailable";
        for (String malformed : new String[]{
                "{\"error\":{\"code\":\"server_busy\",\"message\":\"\\uD800\"}}",
                "{\"error\":{\"code\":\"server_busy\",\"message\":\"\\uDC00\"}}",
                errorBody("server_busy", message) + "\uD800",
                "{\"error\":{\"code\":\"server_busy\uD800\",\"message\":\"" + message + "\"}}"
        }) assertInvalid(ApiError.parse(503, malformed, "30", NOW), 503);
    }

    private static String errorBody(String code, String message) {
        return "{\"error\":{\"code\":\"" + code + "\",\"message\":\"" + message + "\"}}";
    }

    private static void assertInvalid(int status, String code, String retryAfter) throws Exception {
        String hostile = "HOSTILE-PROSE-" + code;
        ApiError error = ApiError.parse(status, new JSONObject().put("error", new JSONObject()
                .put("code", code).put("message", hostile)).toString(), retryAfter, NOW);
        assertInvalid(error, status);
        assertFalse((error.safeMessage() + error.toString()).contains(hostile));
        assertFalse((error.safeMessage() + error.toString()).contains(code));
    }

    private static void assertInvalid(ApiError error, int status) {
        assertEquals(status, error.status());
        assertEquals("invalid_server_response", error.code());
        assertEquals("The server returned an invalid error response.", error.safeMessage());
        assertFalse(error.retryable());
        assertFalse(error.retryAt().isPresent());
    }
}
