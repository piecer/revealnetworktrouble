package com.checknetwork.app.core;

import static org.junit.Assert.*;

import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class ReportParserTest {
    private static String fixture(String name) throws Exception {
        try (InputStream stream = ReportParserTest.class.getResourceAsStream("/core/" + name)) {
            assertNotNull(stream);
            return new String(stream.readAllBytes(), StandardCharsets.UTF_8);
        }
    }

    private static String maximumGoAnalysisFixture() throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata", "maximum-analysis-report.json").normalize();
        assertTrue("missing Go-produced fixture at " + fixture, Files.isRegularFile(fixture));
        return new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8);
    }

    private static String checkerExecutionGoFixture() throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata", "checker-execution-report.json").normalize();
        assertTrue("missing Go-produced fixture at " + fixture, Files.isRegularFile(fixture));
        return new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8);
    }

    private static String producerFixture(String name) throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata", name).normalize();
        assertTrue("missing Go-produced fixture at " + fixture, Files.isRegularFile(fixture));
        return new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8);
    }

    private static JSONObject findingContract() throws Exception {
        return new JSONObject(producerFixture("finding-contract.json"));
    }

    private static String enrichmentGoFixture(String name) throws Exception {
        Path fixture = Paths.get(System.getProperty("user.dir"), "..", "..", "testdata", "enrichment-" + name + "-report.json").normalize();
        assertTrue("missing Go-produced fixture at " + fixture, Files.isRegularFile(fixture));
        return new String(Files.readAllBytes(fixture), StandardCharsets.UTF_8);
    }

    private static JSONObject enrichmentEntry(JSONObject report) throws Exception {
        return report.getJSONObject("analysis").getJSONObject("coverage").getJSONArray("enrichment").getJSONObject(0);
    }

    private static void rejectsEnrichment(Object enrichment) throws Exception {
        JSONObject report = new JSONObject(enrichmentGoFixture("upstream"));
        report.getJSONObject("analysis").getJSONObject("coverage").put("enrichment", enrichment);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report.toString()));
    }

    @Test public void parsesBackendShapedEnrichmentFixturesIntoImmutableValues() throws Exception {
        String[] names = {"upstream", "cache", "mixed", "failures"};
        Report.EnrichmentSource[] sources = {Report.EnrichmentSource.UPSTREAM, Report.EnrichmentSource.CACHE, Report.EnrichmentSource.MIXED, Report.EnrichmentSource.NONE};
        int[][] values = {{0, 2, 12, 0}, {3, 0, 60000, 0}, {2, 1, 42000, 0}, {0, 0, 0, 8}};
        for (int i = 0; i < names.length; i++) {
            Report.Coverage coverage = ReportParser.parse(enrichmentGoFixture(names[i])).analysis().orElseThrow().coverage();
            assertEquals(1, coverage.enrichment().size());
            Report.EnrichmentCoverage entry = coverage.enrichment().get(0);
            assertEquals("geoip", entry.provider());
            assertEquals(sources[i], entry.source());
            assertEquals(values[i][0], entry.cacheHits());
            assertEquals(values[i][1], entry.upstreamFetches());
            assertEquals(values[i][2], entry.maxAgeMs());
            assertEquals(values[i][3], entry.failures().size());
            assertThrows(UnsupportedOperationException.class, () -> coverage.enrichment().clear());
            assertThrows(UnsupportedOperationException.class, () -> entry.failures().clear());
        }
        assertTrue(ReportParser.parse(fixture("https-downgrade-report.json")).analysis().orElseThrow().coverage().enrichment().isEmpty());
    }

    @Test public void rejectsMalformedContradictoryAndOverBudgetEnrichment() throws Exception {
        for (Object wrong : new Object[]{JSONObject.NULL, new JSONObject(), "none", new JSONArray().put(new JSONObject()).put(new JSONObject())}) rejectsEnrichment(wrong);
        JSONObject validReport = new JSONObject(enrichmentGoFixture("upstream"));
        JSONObject valid = enrichmentEntry(validReport);
        for (String key : new String[]{"provider", "source", "cache_hits", "upstream_fetches", "max_age_ms", "failures"}) {
            JSONObject missing = new JSONObject(valid.toString()); missing.remove(key); rejectsEnrichment(new JSONArray().put(missing));
            JSONObject nil = new JSONObject(valid.toString()).put(key, JSONObject.NULL); rejectsEnrichment(new JSONArray().put(nil));
        }
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("url", "URL-CANARY").put("ip", "IP-CANARY").put("target", "TARGET-CANARY").put("error", "ERROR-CANARY")));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("provider", "PROVIDER-CANARY")));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("source", "future")));
        for (String key : new String[]{"cache_hits", "upstream_fetches", "max_age_ms"}) {
            rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put(key, -1)));
            rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put(key, 1.5)));
            rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put(key, "1")));
        }
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("max_age_ms", 86400001)));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("upstream_fetches", 6201)));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("upstream_fetches", 6200)
                .put("failures", new JSONArray().put(new JSONObject().put("kind", "timeout").put("count", 1).put("retryable", true)))));
        for (String source : new String[]{"none", "cache", "mixed"}) rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("source", source)));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("source", "upstream").put("cache_hits", 1)));

        JSONObject timeout = new JSONObject().put("kind", "timeout").put("count", 1).put("retryable", true);
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("failures", new JSONArray().put(timeout).put(new JSONObject(timeout.toString())))));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("failures", new JSONArray()
                .put(timeout).put(new JSONObject().put("kind", "rate_limited").put("count", 1).put("retryable", true)))));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("failures", new JSONArray().put(new JSONObject(timeout.toString()).put("count", 0)))));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("failures", new JSONArray().put(new JSONObject(timeout.toString()).put("retryable", false)))));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("failures", new JSONArray().put(new JSONObject(timeout.toString()).put("detail", "ERROR-CANARY")))));
        rejectsEnrichment(new JSONArray().put(new JSONObject(valid.toString()).put("failures", new JSONArray().put(new JSONObject().put("kind", "timeout").put("count", 1)))));
    }

    @Test public void enforcesEveryFailureKindAndExactRetryability() throws Exception {
        String[] kinds = {"busy", "cancelled", "malformed", "not_found", "policy", "rate_limited", "timeout", "unavailable"};
        boolean[] retryable = {true, false, false, false, false, true, true, true};
        for (int i = 0; i < kinds.length; i++) {
            JSONObject report = new JSONObject(enrichmentGoFixture("failures"));
            JSONObject entry = enrichmentEntry(report);
            entry.put("failures", new JSONArray().put(new JSONObject().put("kind", kinds[i]).put("count", 1).put("retryable", retryable[i])));
            Report.EnrichmentCoverage parsed = ReportParser.parse(report.toString()).analysis().orElseThrow().coverage().enrichment().get(0);
            assertEquals(Report.EnrichmentFailureKind.valueOf(kinds[i].toUpperCase()), parsed.failures().get(0).kind());
            entry.getJSONArray("failures").getJSONObject(0).put("retryable", !retryable[i]);
            assertThrows(ReportParseException.class, () -> ReportParser.parse(report.toString()));
        }
    }

    @Test public void parsesHttpsDowngradeAndCompactTracerouteFixtures() throws Exception {
        Report https = ReportParser.parse(fixture("https-downgrade-report.json"));
        assertEquals(CheckKind.HTTPS, https.results().get(0).kind());
        assertEquals(Report.FindingCode.TLS_DOWNGRADE, https.analysis().orElseThrow().findings().get(0).code());
        Report compact = ReportParser.parse(fixture("compact-traceroute-report.json"));
        assertEquals(CheckKind.TRACEROUTE, compact.results().get(0).kind());
        assertEquals(1, compact.compactTopology().orElseThrow().routeCount());
    }

    @Test public void rejectsHealthyDnsWithConnectionFailedBeforeReportConstruction() throws Exception {
        JSONObject contradictory = oneResultReport(resultObject("dns", "healthy")
                .put("error_code", "connection_failed")
                .put("details", new JSONObject().put("addresses", new JSONArray().put("203.0.113.10"))
                        .put("answer_count", 1)));
        ReportParseException failure = assertThrows(ReportParseException.class,
                () -> ReportParser.parse(contradictory.toString()));
        assertFalse(failure.getMessage().contains("203.0.113.10"));
    }

    @Test public void acceptsSerializedMaximumGoAnalysisAndRejectsEveryCapPlusOne() throws Exception {
        assertEquals(64, ContractLimits.MAX_FINDINGS);
        assertEquals(64, ContractLimits.MAX_EVIDENCE);
        assertEquals(64, ContractLimits.MAX_ACTIONS);
        assertEquals(128, ContractLimits.MAX_COVERAGE_ITEMS);

        String serialized = maximumGoAnalysisFixture();
        Report.Analysis maximum = ReportParser.parse(serialized).analysis().orElseThrow();
        assertEquals(40, maximum.findings().size());
        assertEquals(40, maximum.evidence().size());
        assertEquals(40, maximum.actions().size());
        assertEquals(80, maximum.coverage().available().size());

        for (String name : new String[]{"findings", "evidence", "actions"}) {
            JSONObject report = new JSONObject(serialized);
            JSONArray values = report.getJSONObject("analysis").getJSONArray(name);
            int limit = name.equals("findings") ? ContractLimits.MAX_FINDINGS
                    : name.equals("evidence") ? ContractLimits.MAX_EVIDENCE : ContractLimits.MAX_ACTIONS;
            while (values.length() <= limit) {
                values.put(new JSONObject(values.getJSONObject(0).toString()).put("id", name + "-" + values.length()));
            }
            assertThrows(name, ReportParseException.class, () -> ReportParser.parse(report.toString()));
        }

        JSONObject issue = new JSONObject().put("code", "missing_details").put("result_index", 0)
                .put("kind", "https").put("signal", "fixture").put("reason", "fixture coverage issue");
        for (String name : new String[]{"available", "missing", "provider_failures", "limitations"}) {
            JSONObject report = new JSONObject(serialized);
            JSONArray values = report.getJSONObject("analysis").getJSONObject("coverage").getJSONArray(name);
            while (values.length() <= ContractLimits.MAX_COVERAGE_ITEMS) {
                values.put(name.equals("available") || name.equals("missing")
                        ? "fixture-" + values.length() : new JSONObject(issue.toString()));
            }
            assertThrows(name, ReportParseException.class, () -> ReportParser.parse(report.toString()));
        }
    }

    @Test public void parsesExactGoCheckerExecutionCodesPreservesTextAndRejectsUnknownCode() throws Exception {
        String serialized = checkerExecutionGoFixture();
        Report parsed = ReportParser.parse(serialized);
        assertEquals(Report.FindingCode.CHECKER_PANIC, parsed.analysis().orElseThrow().findings().get(0).code());
        assertEquals(Report.FindingCode.CHECKER_CAPACITY_UNAVAILABLE, parsed.analysis().orElseThrow().findings().get(1).code());

        JSONObject hostile = new JSONObject(serialized);
        JSONObject panic = hostile.getJSONObject("analysis").getJSONArray("findings").getJSONObject(0);
        panic.put("title", "private target and panic prose").put("summary", "Bearer secret-token");
        Report.Finding parsedCanary = ReportParser.parse(hostile.toString()).analysis().orElseThrow().findings().get(0);
        assertEquals("private target and panic prose", parsedCanary.title());
        assertEquals("Bearer secret-token", parsedCanary.summary());
        panic.put("code", "checker_future_unknown");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(hostile.toString()));
    }

    @Test public void producerFindingFixtureDrivesSharedValidAndInvalidMutationCorpus() throws Exception {
        JSONArray contractFindings = findingContract().getJSONArray("findings");
        Set<String> producerCodes = new HashSet<>();
        for (int i = 0; i < contractFindings.length(); i++) {
            String code = contractFindings.getJSONObject(i).getString("code");
            assertTrue("duplicate producer finding code " + code, producerCodes.add(code));
            Report.Finding finding = ReportParser.parse(reportWithFindingCode(code).toString())
                    .analysis().orElseThrow().findings().get(0);
            assertEquals(code.toUpperCase(java.util.Locale.ROOT), finding.code().name());
            assertEquals(java.util.List.of("e-shared"), finding.evidenceIds());
            assertEquals(java.util.List.of("a-shared"), finding.actionIds());
        }
        Set<String> androidCodes = new HashSet<>();
        for (Report.FindingCode code : Report.FindingCode.values()) {
            androidCodes.add(code.name().toLowerCase(java.util.Locale.ROOT));
        }
        assertEquals("Android FindingCode must equal the exact producer fixture", producerCodes, androidCodes);
        assertEquals(23, androidCodes.size());

        Object[] invalidCodes = {"future_finding", JSONObject.NULL, 7, "x".repeat(129),
                "tls_untrusted\nBearer HOSTILE_SECRET_CANARY"};
        for (Object invalidCode : invalidCodes) {
            JSONObject mutated = reportWithFindingCode("tls_untrusted");
            mutated.getJSONObject("analysis").getJSONArray("findings").getJSONObject(0).put("code", invalidCode);
            ReportParseException failure = assertThrows(String.valueOf(invalidCode), ReportParseException.class,
                    () -> ReportParser.parse(mutated.toString()));
            assertFalse("hostile finding data must not be reflected", failure.getMessage().contains("HOSTILE_SECRET_CANARY"));
        }
    }

    @Test public void producerResultShapeFixtureDrivesSharedTlsAndServiceCorpus() throws Exception {
        JSONObject contract=findingContract();
        JSONArray shapes = contract.getJSONArray("result_shapes");
        assertEquals(31, shapes.length());
        int validRows=0;
        for (int i = 0; i < shapes.length(); i++) {
            JSONObject shape = shapes.getJSONObject(i);
            JSONArray kinds=shape.getJSONArray("kinds");
            for(int kindIndex=0;kindIndex<kinds.length();kindIndex++){
                JSONObject valid = reportForResultShape(shape,kinds.getString(kindIndex));
                Report parsed;
                try{parsed=ReportParser.parse(valid.toString());}
                catch(ReportParseException failure){throw new AssertionError(shape.getString("name")+"/"+kinds.getString(kindIndex)+": "+valid,failure);}
                assertEquals(shape.getString("status"), parsed.results().get(0).status().name().toLowerCase(java.util.Locale.ROOT));
                assertEquals(kinds.getString(kindIndex),parsed.results().get(0).kind().name().toLowerCase(java.util.Locale.ROOT));
                validRows++;
            }

            JSONObject valid = reportForResultShape(shape);

            JSONObject wrongStatus = new JSONObject(valid.toString());
            String incompatibleStatus = "healthy".equals(shape.getString("status")) ? "degraded" : "healthy";
            setSingleResultStatus(wrongStatus, incompatibleStatus);
            assertThrows(shape.getString("name") + " status", ReportParseException.class,
                    () -> ReportParser.parse(wrongStatus.toString()));

            JSONObject wrongError = new JSONObject(valid.toString());
            JSONObject wrongErrorResult = wrongError.getJSONArray("results").getJSONObject(0);
            if (!shape.has("error_code")) wrongErrorResult.put("error_code", "service_greeting_unverified");
            else if (shape.getString("error_code").equals("tls_certificate_expired")
                    || shape.getString("error_code").equals("tls_certificate_not_yet_valid")) {
                wrongErrorResult.put("error_code", "tls_handshake_failed");
            } else {
                wrongErrorResult.put("error_code", "tls_certificate_expired");
            }
            assertThrows(shape.getString("name") + " error", ReportParseException.class,
                    () -> ReportParser.parse(wrongError.toString()));

            String incompatibleKind = incompatibleKnownKind(shape);
            if (incompatibleKind != null) {
                JSONObject wrongKind = new JSONObject(valid.toString());
                wrongKind.getJSONArray("results").getJSONObject(0).put("kind", incompatibleKind);
                assertThrows(shape.getString("name") + " kind", ReportParseException.class,
                        () -> ReportParser.parse(wrongKind.toString()));
            }

            JSONObject extraDetail = new JSONObject(valid.toString());
            JSONObject extraDetailResult = extraDetail.getJSONArray("results").getJSONObject(0);
            extraDetailResult.put("kind",detailRejectingAllowedKind(shape));
            if (!extraDetailResult.has("details")) extraDetailResult.put("details", new JSONObject());
            extraDetailResult.getJSONObject("details").put("HOSTILE_DETAIL_CANARY", "Bearer secret");
            ReportParseException failure = assertThrows(shape.getString("name") + " details", ReportParseException.class,
                    () -> ReportParser.parse(extraDetail.toString()));
            assertFalse(failure.getMessage().contains("Bearer secret"));
            }
            assertEquals(contract.getInt("result_matrix_rows"),validRows);
    }

    @Test public void fixtureDerivedResultShapeMutationCorpusRejectsEveryInvalidWholeReport() throws Exception {
        JSONArray shapes = findingContract().getJSONArray("result_shapes");
        int acceptedShapes = 0;
        int rejectedMutations = 0;
        for (int i = 0; i < shapes.length(); i++) {
            JSONObject shape = shapes.getJSONObject(i);
            JSONObject canonical = reportForResultShape(shape);
            ReportParser.parse(canonical.toString());
            acceptedShapes++;

            if (shape.has("error_code")) {
                JSONObject hostile = new JSONObject(canonical.toString());
                hostile.getJSONArray("results").getJSONObject(0)
                        .put("error_code", "unknown_HOSTILE_ERROR_CODE_CANARY");
                assertThrows(shape.getString("name") + " hostile error code", ReportParseException.class,
                        () -> ReportParser.parse(hostile.toString()));
                rejectedMutations++;
            }

            if (shape.getJSONArray("required_details").length() == 0
                    && shape.getJSONArray("optional_details").length() == 0) {
                for (Object invalid : new Object[]{new JSONObject(), JSONObject.NULL}) {
                    JSONObject mutation = new JSONObject(canonical.toString());
                    mutation.getJSONArray("results").getJSONObject(0)
                            .put("kind",detailRejectingAllowedKind(shape)).put("details", invalid);
                    assertThrows(shape.getString("name") + " present no-detail value", ReportParseException.class,
                            () -> ReportParser.parse(mutation.toString()));
                    rejectedMutations++;
                }
            }
        }
        assertEquals("all producer shapes exercised", shapes.length(), acceptedShapes);
        int expectedRejectedMutations = 0;
        for (int i = 0; i < shapes.length(); i++) {
            JSONObject shape = shapes.getJSONObject(i);
            if (shape.has("error_code")) expectedRejectedMutations++;
            if (shape.getJSONArray("required_details").length() == 0
                    && shape.getJSONArray("optional_details").length() == 0) expectedRejectedMutations += 2;
        }
        assertEquals("fixture-derived hostile error and forbidden-details mutations",
                expectedRejectedMutations, rejectedMutations);

        for (String shapeName : new String[]{"tls_certificate_expired", "tls_certificate_not_yet_valid"}) {
            JSONObject reversed = reportForResultShape(resultShape(shapeName));
            JSONObject details = reversed.getJSONArray("results").getJSONObject(0).getJSONObject("details");
            details.put("certificate_not_before", "2028-01-01T00:00:00Z")
                    .put("certificate_not_after", "2027-01-01T00:00:00Z");
            assertThrows(shapeName + " reversed validity", ReportParseException.class,
                    () -> ReportParser.parse(reversed.toString()));
        }

        JSONObject verifiedTLS = reportForResultShape(resultShape("service_greeting_verified_tls_certificate"));
        for (String invalid : new String[]{"", " \t\n "}) {
            JSONObject mutation = new JSONObject(verifiedTLS.toString());
            mutation.getJSONArray("results").getJSONObject(0).getJSONObject("details").put("tls_version", invalid);
            assertThrows("tls_version must be nonblank", ReportParseException.class,
                    () -> ReportParser.parse(mutation.toString()));
        }
    }

    @Test public void fixtureDerivedResultShapeMutationCorpusMatchesWebSemantics() throws Exception {
        JSONArray shapes = findingContract().getJSONArray("result_shapes");
        Set<String> generatedMutations = new HashSet<>();
        int rejected = 0;
        for (int i = 0; i < shapes.length(); i++) {
            JSONObject shape = shapes.getJSONObject(i);
            String shapeName = shape.getString("name");
            JSONObject canonical = reportForResultShape(shape);

            assertTrue("duplicate generated mutation", generatedMutations.add(shapeName + ": wrong status"));
            JSONObject wrongStatus = new JSONObject(canonical.toString());
            setSingleResultStatus(wrongStatus, "healthy".equals(shape.getString("status")) ? "degraded" : "healthy");
            assertThrows(shapeName + " wrong status", ReportParseException.class,
                    () -> ReportParser.parse(wrongStatus.toString()));
            rejected++;

            String incompatibleKind = incompatibleKnownKind(shape);
            if (incompatibleKind != null) {
                assertTrue("duplicate generated mutation", generatedMutations.add(shapeName + ": wrong kind"));
                JSONObject wrongKind = new JSONObject(canonical.toString());
                wrongKind.getJSONArray("results").getJSONObject(0).put("kind", incompatibleKind);
                assertThrows(shapeName + " wrong kind", ReportParseException.class,
                        () -> ReportParser.parse(wrongKind.toString()));
                rejected++;
            }

            assertTrue("duplicate generated mutation", generatedMutations.add(shapeName + ": error presence"));
            JSONObject errorMutation = new JSONObject(canonical.toString());
            JSONObject errorResult = errorMutation.getJSONArray("results").getJSONObject(0);
            if (shape.has("error_code")) {
                errorResult.remove("error_code");
            } else {
                errorResult.put("error_code", "service_greeting_unverified");
            }
            assertThrows(shapeName + " error presence", ReportParseException.class,
                    () -> ReportParser.parse(errorMutation.toString()));
            rejected++;

            if (shape.has("error_code")) {
                assertTrue("duplicate generated mutation", generatedMutations.add(shapeName + ": contradictory typed error"));
                JSONObject contradictory = new JSONObject(canonical.toString());
                contradictory.getJSONArray("results").getJSONObject(0).put("error_code",
                        shape.getString("error_code").startsWith("tls_certificate_")
                                ? "tls_handshake_failed" : "tls_certificate_expired");
                assertThrows(shapeName + " contradictory typed error", ReportParseException.class,
                        () -> ReportParser.parse(contradictory.toString()));
                rejected++;

                assertTrue("duplicate generated mutation", generatedMutations.add(shapeName + ": unknown hostile error"));
                JSONObject hostile = new JSONObject(canonical.toString());
                hostile.getJSONArray("results").getJSONObject(0)
                        .put("error_code", "unknown_HOSTILE_ERROR_CODE_CANARY");
                assertThrows(shapeName + " unknown hostile error", ReportParseException.class,
                        () -> ReportParser.parse(hostile.toString()));
                rejected++;
            }

            JSONArray required = shape.getJSONArray("required_details");
            JSONArray optional = shape.getJSONArray("optional_details");
            if (required.length() == 0 && optional.length() == 0) {
                Object[] invalidDetails = {new JSONObject(), JSONObject.NULL};
                String[] mutationNames = {"forbidden details object", "forbidden null details"};
                for (int mutationIndex = 0; mutationIndex < invalidDetails.length; mutationIndex++) {
                    assertTrue("duplicate generated mutation",
                            generatedMutations.add(shapeName + ": " + mutationNames[mutationIndex]));
                    JSONObject mutation = new JSONObject(canonical.toString());
                    mutation.getJSONArray("results").getJSONObject(0)
                            .put("kind",detailRejectingAllowedKind(shape)).put("details", invalidDetails[mutationIndex]);
                    assertThrows(shapeName + " forbidden details", ReportParseException.class,
                            () -> ReportParser.parse(mutation.toString()));
                    rejected++;
                }
            } else {
                boolean detailsMayBeOmitted = required.length() == 0
                        || shapeName.equals("traceroute_execution_timeout");
                Object[] invalidDetails = detailsMayBeOmitted
                        ? new Object[]{JSONObject.NULL}
                        : new Object[]{null, JSONObject.NULL};
                String[] mutationNames = detailsMayBeOmitted
                        ? new String[]{"null details"}
                        : new String[]{"missing details", "null details"};
                for (int mutationIndex = 0; mutationIndex < invalidDetails.length; mutationIndex++) {
                    assertTrue("duplicate generated mutation",
                            generatedMutations.add(shapeName + ": " + mutationNames[mutationIndex]));
                    JSONObject mutation = new JSONObject(canonical.toString());
                    JSONObject result = mutation.getJSONArray("results").getJSONObject(0);
                    if (invalidDetails[mutationIndex] == null) result.remove("details");
                    else result.put("details", invalidDetails[mutationIndex]);
                    assertThrows(shapeName + " missing/null details", ReportParseException.class,
                            () -> ReportParser.parse(mutation.toString()));
                    rejected++;
                }
                assertTrue("duplicate generated mutation", generatedMutations.add(shapeName + ": unknown details"));
                JSONObject unknown = new JSONObject(canonical.toString());
                unknown.getJSONArray("results").getJSONObject(0).getJSONObject("details")
                        .put("unknown_canary", "HOSTILE_DETAIL");
                assertThrows(shapeName + " unknown details", ReportParseException.class,
                        () -> ReportParser.parse(unknown.toString()));
                rejected++;
                for (int detailIndex = 0; detailIndex < required.length(); detailIndex++) {
                    String key = required.getJSONObject(detailIndex).getString("key");
                    Object[] invalidValues = {null, JSONObject.NULL};
                    String[] requiredMutationNames = {"missing required ", "null required "};
                    for (int mutationIndex = 0; mutationIndex < invalidValues.length; mutationIndex++) {
                        if(mutationIndex==0&&removalSelectsAnotherValidShape(shape,key))continue;
                        assertTrue("duplicate generated mutation",
                                generatedMutations.add(shapeName + ": " + requiredMutationNames[mutationIndex] + key));
                        JSONObject mutation = new JSONObject(canonical.toString());
                        JSONObject details = mutation.getJSONArray("results").getJSONObject(0).getJSONObject("details");
                        if (invalidValues[mutationIndex] == null) details.remove(key);
                        else details.put(key, invalidValues[mutationIndex]);
                        assertThrows(shapeName + " required " + key, ReportParseException.class,
                                () -> ReportParser.parse(mutation.toString()));
                        rejected++;
                    }
                }
                for (int detailIndex = 0; detailIndex < optional.length(); detailIndex++) {
                    String key = optional.getJSONObject(detailIndex).getString("key");
                    assertTrue("duplicate generated mutation",
                            generatedMutations.add(shapeName + ": null optional " + key));
                    JSONObject mutation = new JSONObject(canonical.toString());
                    mutation.getJSONArray("results").getJSONObject(0).getJSONObject("details").put(key, JSONObject.NULL);
                    assertThrows(shapeName + " optional null " + key, ReportParseException.class,
                            () -> ReportParser.parse(mutation.toString()));
                    rejected++;
                }
            }
        }
        assertFalse("fixture must generate mutations", generatedMutations.isEmpty());
        assertEquals("all unique fixture-derived Web semantic mutations rejected",
                generatedMutations.size(), rejected);
    }

    @Test public void exactProducerMatrixAcceptsAll136RowsAndRejectsEveryIncompatibleAxis() throws Exception {
        JSONObject contract = findingContract();
        JSONArray shapes = contract.getJSONArray("result_shapes");
        assertEquals(31, shapes.length());
        assertEquals(136, contract.getInt("result_matrix_rows"));
        Set<String> errors = new LinkedHashSet<>(); errors.add("");
        for (int i=0;i<shapes.length();i++) if(shapes.getJSONObject(i).has("error_code"))
            errors.add(shapes.getJSONObject(i).getString("error_code"));
        errors.add("partial_answer"); errors.add("unknown_HOSTILE_ERROR_CODE_CANARY");
        int validRows=0,rejected=0;
        for(int i=0;i<shapes.length();i++){
            JSONObject shape=shapes.getJSONObject(i); Set<String> kinds=jsonStringSet(shape.getJSONArray("kinds"));
            for(String kind:kinds){ReportParser.parse(reportForResultShape(shape,kind).toString());validRows++;}
            JSONObject canonical=reportForResultShape(shape);
            for(CheckKind candidate:CheckKind.values()){
                String kind=candidate.name().toLowerCase(java.util.Locale.ROOT);if(kinds.contains(kind))continue;
                JSONObject mutation=new JSONObject(canonical.toString());mutation.getJSONArray("results").getJSONObject(0).put("kind",kind);
                if(!fixtureAllowsResult(shapes,mutation.getJSONArray("results").getJSONObject(0))){
                    assertThrows(shape.getString("name")+" kind "+kind,ReportParseException.class,()->ReportParser.parse(mutation.toString()));rejected++;}
            }
            for(String status:new String[]{"healthy","degraded","unreachable"}){
                if(status.equals(shape.getString("status")))continue;
                JSONObject mutation=new JSONObject(canonical.toString());setSingleResultStatus(mutation,status);
                if(!fixtureAllowsResult(shapes,mutation.getJSONArray("results").getJSONObject(0))){
                    assertThrows(shape.getString("name")+" status "+status,ReportParseException.class,()->ReportParser.parse(mutation.toString()));rejected++;}
            }
            for(String error:errors){
                if(error.equals(shape.optString("error_code","")))continue;
                JSONObject mutation=new JSONObject(canonical.toString());JSONObject result=mutation.getJSONArray("results").getJSONObject(0);
                if(error.isEmpty())result.remove("error_code");else result.put("error_code",error);
                if(!fixtureAllowsResult(shapes,result)){
                    assertThrows(shape.getString("name")+" error "+error,ReportParseException.class,()->ReportParser.parse(mutation.toString()));rejected++;}
            }
            JSONObject unknownResult=new JSONObject(canonical.toString());unknownResult.getJSONArray("results").getJSONObject(0)
                    .put("unknown_result_key","HOSTILE_RESULT_VALUE_CANARY");
            ReportParseException failure=assertThrows(shape.getString("name")+" result key",ReportParseException.class,
                    ()->ReportParser.parse(unknownResult.toString()));
            assertFalse(failure.getMessage().contains("HOSTILE_RESULT_VALUE_CANARY"));rejected++;
            JSONArray required=shape.getJSONArray("required_details"),optional=shape.getJSONArray("optional_details");
            if(required.length()>0){
                JSONObject missing=new JSONObject(canonical.toString());missing.getJSONArray("results").getJSONObject(0).remove("details");
                if(!fixtureAllowsResult(shapes,missing.getJSONArray("results").getJSONObject(0))){
                    assertThrows(shape.getString("name")+" missing details",ReportParseException.class,()->ReportParser.parse(missing.toString()));rejected++;}
                for(int d=0;d<required.length();d++)for(boolean useNull:new boolean[]{false,true}){
                    String key=required.getJSONObject(d).getString("key");JSONObject mutation=new JSONObject(canonical.toString());
                    JSONObject details=mutation.getJSONArray("results").getJSONObject(0).getJSONObject("details");
                    if(useNull)details.put(key,JSONObject.NULL);else details.remove(key);
                    if(!fixtureAllowsResult(shapes,mutation.getJSONArray("results").getJSONObject(0))){
                        assertThrows(shape.getString("name")+" required "+key,ReportParseException.class,()->ReportParser.parse(mutation.toString()));rejected++;}
                }
            }
            if(canonical.getJSONArray("results").getJSONObject(0).has("details")){
                JSONObject unknown=new JSONObject(canonical.toString());unknown.getJSONArray("results").getJSONObject(0)
                        .getJSONObject("details").put("unknown_detail_key","HOSTILE_DETAIL_VALUE_CANARY");
                assertThrows(shape.getString("name")+" unknown detail",ReportParseException.class,()->ReportParser.parse(unknown.toString()));rejected++;
                for(int d=0;d<optional.length();d++){
                    String key=optional.getJSONObject(d).getString("key");JSONObject mutation=new JSONObject(canonical.toString());
                    mutation.getJSONArray("results").getJSONObject(0).getJSONObject("details").put(key,JSONObject.NULL);
                    assertThrows(shape.getString("name")+" null optional "+key,ReportParseException.class,()->ReportParser.parse(mutation.toString()));rejected++;
                }
            }
        }
        assertEquals(136,validRows);assertTrue("broad fixture-derived incompatible corpus",rejected>700);
    }

    @Test public void reviewReproductionsMatchWebAndAllEightTracerouteWitnessesParse() throws Exception {
        for(String plain:new String[]{"imap","pop3","smtp","ssh","submission"}){
            JSONObject invalid=oneResultReport(resultObject(plain,"unreachable").put("error_code","invalid_address"));
            assertThrows(plain,ReportParseException.class,()->ReportParser.parse(invalid.toString()));
        }
        JSONObject partial=oneResultReport(resultObject("dns","degraded").put("error_code","partial_answer")
                .put("details",new JSONObject().put("addresses",new JSONArray()).put("answer_count",0)));
        assertThrows(ReportParseException.class,()->ReportParser.parse(partial.toString()));
        for(String kind:new String[]{"dns","tcp"})for(String code:new String[]{"cancelled","checker_capacity_unavailable",
                "checker_panic","connection_failed","network_policy_blocked","timeout"}){
            JSONObject legacy=oneResultReport(resultObject(kind,"unreachable").put("error_code",code)
                    .put("details",new JSONObject().put("legacy_observation","retained")));
            assertEquals(code,ReportParser.parse(legacy.toString()).results().get(0).errorCode());
        }
        String[] witnesses={"traceroute-command-failure-compact-report.json","traceroute-command-failure-full-report.json",
                "traceroute-failed-reached-parse-compact-report.json","traceroute-failed-reached-parse-full-report.json",
                "traceroute-mixed-completed-failed-compact-report.json","traceroute-mixed-completed-failed-full-report.json",
                "traceroute-timeout-compact-report.json","traceroute-timeout-full-report.json"};
        for(String witness:witnesses)assertEquals(witness,CheckKind.TRACEROUTE,
                ReportParser.parse(producerFixture(witness)).results().get(0).kind());
        assertEquals(8,witnesses.length);
    }

    private static Set<String> jsonStringSet(JSONArray values)throws Exception{
        Set<String> result=new LinkedHashSet<>();if(values==null)return result;
        for(int i=0;i<values.length();i++)result.add(values.getString(i));return result;
    }

    private static boolean fixtureAllowsResult(JSONArray shapes,JSONObject result)throws Exception{
        Set<String> actual=result.has("details")&&!result.isNull("details")?jsonStringSet(result.getJSONObject("details").names()):Set.of();
        boolean hasDetails=result.has("details"),hasError=result.has("error_code");
        if(hasDetails&&hasError&&"unreachable".equals(result.getString("status"))
                &&Set.of("cancelled","checker_capacity_unavailable","checker_panic","connection_failed",
                "network_policy_blocked","timeout").contains(result.getString("error_code"))
                &&Set.of("dns","tcp").contains(result.getString("kind")))return true;
        if(hasDetails&&hasError&&"traceroute".equals(result.getString("kind"))
                &&"unreachable".equals(result.getString("status"))&&"cancelled".equals(result.getString("error_code"))
                &&Set.of("attempts","attempts_cancelled","attempts_execution_failed","attempts_failed",
                "attempts_reached","attempts_timed_out","attempts_total","attempts_unreached",
                "geoip_enrichment","geoip_provider_failures","topology").containsAll(actual))return true;
        for(int i=0;i<shapes.length();i++){
            JSONObject shape=shapes.getJSONObject(i);
            if(!jsonStringSet(shape.getJSONArray("kinds")).contains(result.getString("kind"))
                    ||!shape.getString("status").equals(result.getString("status"))||shape.has("error_code")!=hasError)continue;
            if(hasError&&!shape.getString("error_code").equals(result.getString("error_code")))continue;
            Set<String> required=new LinkedHashSet<>(),allowed=new LinkedHashSet<>();
            JSONArray requiredJSON=shape.getJSONArray("required_details"),optionalJSON=shape.getJSONArray("optional_details");
            for(int d=0;d<requiredJSON.length();d++)required.add(requiredJSON.getJSONObject(d).getString("key"));allowed.addAll(required);
            for(int d=0;d<optionalJSON.length();d++)allowed.add(optionalJSON.getJSONObject(d).getString("key"));
            if(required.isEmpty()&&!hasDetails)return true;
            if(required.isEmpty()&&allowed.isEmpty()){if(!hasDetails)return true;}
            else if(hasDetails&&actual.containsAll(required)&&allowed.containsAll(actual))return true;
        }
        return false;
    }

    private static boolean removalSelectsAnotherValidShape(JSONObject shape,String key)throws Exception{
        return false;
    }

    private static String detailRejectingAllowedKind(JSONObject shape)throws Exception{
        JSONArray kinds=shape.getJSONArray("kinds");
        for(int i=0;i<kinds.length();i++)if(!Set.of("dns","tcp").contains(kinds.getString(i)))return kinds.getString(i);
        return kinds.getString(0);
    }

    @Test public void exactNamedShapesPreserveLegitimateProducerGeneralErrors() throws Exception {
        Map<String, String[]> errorsByKind = Map.of(
                "http", new String[]{"network_policy_blocked", "invalid_url", "timeout", "cancelled",
                        "connection_failed", "response_read_failed", "checker_panic",
                        "checker_capacity_unavailable", "unexpected_status"},
                "https", new String[]{"network_policy_blocked", "invalid_url", "timeout", "cancelled",
                        "connection_failed", "response_read_failed", "checker_panic",
                        "checker_capacity_unavailable", "unexpected_status"},
                "smtp", new String[]{"network_policy_blocked", "timeout", "cancelled",
                        "connection_failed", "checker_panic", "checker_capacity_unavailable"});
        for (Map.Entry<String, String[]> family : errorsByKind.entrySet()) {
            for (String errorCode : family.getValue()) {
                JSONObject result = resultObject(family.getKey(), "unreachable").put("error_code", errorCode);
                if ("unexpected_status".equals(errorCode)) {
                    result.put("details", new JSONObject().put("status_code", 503).put("expected_status", 200));
                }
                assertEquals(errorCode, ReportParser.parse(oneResultReport(result).toString())
                        .results().get(0).errorCode());
            }
        }

        for (String kind : new String[]{"http", "https"}) {
            assertEquals(Report.Status.HEALTHY, ReportParser.parse(
                    oneResultReport(resultObject(kind, "healthy")).toString()).results().get(0).status());
        }

        for (String invalidError : new String[]{"", "timeout"}) {
            JSONObject degradedService = resultObject("smtp", "degraded");
            if (!invalidError.isEmpty()) degradedService.put("error_code", invalidError);
            assertThrows("degraded service requires exact greeting error", ReportParseException.class,
                    () -> ReportParser.parse(oneResultReport(degradedService).toString()));
        }
    }

    @Test public void exactTlsAndServiceShapeMutationsInvalidateWholeReport() throws Exception {
        JSONObject expired = reportForResultShape(resultShape("tls_certificate_expired"));
        for (String invalid : new String[]{"2025-01-01T00:00:00+00:00", "2025-01-01T00:00:00.000Z", "not-a-time"}) {
            JSONObject mutation = new JSONObject(expired.toString());
            mutation.getJSONArray("results").getJSONObject(0).getJSONObject("details")
                    .put("certificate_not_after", invalid);
            assertThrows(invalid, ReportParseException.class, () -> ReportParser.parse(mutation.toString()));
        }
        for (String key : new String[]{"certificate_not_before", "certificate_not_after"}) {
            JSONObject mutation = new JSONObject(expired.toString());
            mutation.getJSONArray("results").getJSONObject(0).getJSONObject("details").remove(key);
            assertThrows(key, ReportParseException.class, () -> ReportParser.parse(mutation.toString()));
        }

        JSONObject plain = reportForResultShape(resultShape("service_greeting_verified_plain"));
        JSONObject noScope = new JSONObject(plain.toString());
        noScope.getJSONArray("results").getJSONObject(0).remove("details");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(noScope.toString()));
        JSONObject wrongScope = new JSONObject(plain.toString());
        wrongScope.getJSONArray("results").getJSONObject(0).getJSONObject("details")
                .put("verification_scope", "full_service");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(wrongScope.toString()));

        JSONObject greetingFailure = reportForResultShape(resultShape("service_greeting_unverified"));
        JSONObject wrongStatus = new JSONObject(greetingFailure.toString());
        wrongStatus.put("status", "unreachable");
        wrongStatus.getJSONArray("results").getJSONObject(0).put("status", "unreachable");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(wrongStatus.toString()));

        JSONObject implicitTls = reportForResultShape(resultShape("service_greeting_verified_tls_certificate"));
        for (Map.Entry<String, Object> invalid : Map.<String, Object>of(
                "tls_version", "   ",
                "cipher_suite", 7,
                "certificate_subject", JSONObject.NULL,
                "certificate_expires_at", "2027-01-01T00:00:00+00:00").entrySet()) {
            JSONObject mutation = new JSONObject(implicitTls.toString());
            mutation.getJSONArray("results").getJSONObject(0).getJSONObject("details")
                    .put(invalid.getKey(), invalid.getValue());
            assertThrows(invalid.getKey(), ReportParseException.class, () -> ReportParser.parse(mutation.toString()));
        }

        JSONObject noDetailFailure = reportForResultShape(resultShape("tls_hostname_mismatch"));
        noDetailFailure.getJSONArray("results").getJSONObject(0).put("details", JSONObject.NULL);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(noDetailFailure.toString()));
    }

    @Test public void parsesCanonicalCompactProducerFixturesAndRejectsNullArraysAndEmptyAsn() throws Exception {
        for (String name : new String[]{"compact-zero-route-report.json", "compact-zero-node-attempt-report.json", "compact-empty-asn-report.json"}) {
            Report report = ReportParser.parse(producerFixture(name));
            assertTrue(name, report.compactTopology().isPresent());
        }
        assertEquals(0, ReportParser.parse(producerFixture("compact-zero-route-report.json"))
                .compactTopology().orElseThrow().routeCount());
        assertEquals(0, ReportParser.parse(producerFixture("compact-zero-node-attempt-report.json"))
                .compactTopology().orElseThrow().nodeCount());

        for (String key : new String[]{"nodes", "links", "routes"}) {
            JSONObject mutation = new JSONObject(producerFixture("compact-zero-route-report.json"));
            mutation.getJSONObject("compact_topology").put(key, JSONObject.NULL);
            assertThrows(key, ReportParseException.class, () -> ReportParser.parse(mutation.toString()));
        }
        JSONObject emptyAsn = new JSONObject(producerFixture("compact-empty-asn-report.json"));
        JSONObject publicNode = emptyAsn.getJSONObject("compact_topology").getJSONArray("nodes").getJSONObject(1);
        assertFalse("producer must omit empty ASN", publicNode.has("asn"));
        assertTrue("absent ASN remains accepted", ReportParser.parse(emptyAsn.toString()).compactTopology().isPresent());
        publicNode.put("asn", new JSONObject());
        assertThrows(ReportParseException.class, () -> ReportParser.parse(emptyAsn.toString()));
    }

    @Test public void compactSchemaIsClosedAtEveryProducerObjectBoundary() throws Exception {
        String[] boundaries = {"root", "limits", "node", "link", "route", "stats", "count",
                "route stats", "result stats", "result count stats", "result route stats", "geo stats", "geolocation", "asn"};
        for (String boundary : boundaries) {
            JSONObject report = validClosedCompactReport();
            compactBoundaryTarget(report, boundary).put("unknown_canary", "HOSTILE_COMPACT_VALUE");
            assertThrows(boundary, ReportParseException.class, () -> ReportParser.parse(report.toString()));
        }
        assertEquals(14, boundaries.length);
    }

    @Test public void compactOptionalFieldsAcceptAbsenceButRejectPresentJsonNull() throws Exception {
        String[] optionalFields = {"truncation_reasons", "address", "latency_ms_avg", "public_ip",
                "geolocation", "asn", "city", "region", "country", "country_code", "number", "organization"};
        for (String field : optionalFields) {
            JSONObject absent = validClosedCompactReport();
            compactOptionalTarget(absent, field).remove(field);
            ReportParser.parse(absent.toString());

            JSONObject presentNull = validClosedCompactReport();
            compactOptionalTarget(presentNull, field).put(field, JSONObject.NULL);
            assertThrows(field, ReportParseException.class, () -> ReportParser.parse(presentNull.toString()));
        }
        assertEquals(12, optionalFields.length);

        JSONObject onlyNumber = validClosedCompactReport();
        JSONObject numberASN = onlyNumber.getJSONObject("compact_topology").getJSONArray("nodes")
                .getJSONObject(1).getJSONObject("asn");
        numberASN.remove("organization");
        assertEquals(2, ReportParser.parse(onlyNumber.toString()).compactTopology().orElseThrow().nodeCount());

        JSONObject onlyOrganization = validClosedCompactReport();
        JSONObject organizationASN = onlyOrganization.getJSONObject("compact_topology").getJSONArray("nodes")
                .getJSONObject(1).getJSONObject("asn");
        organizationASN.remove("number");
        assertEquals(2, ReportParser.parse(onlyOrganization.toString()).compactTopology().orElseThrow().nodeCount());

        for (JSONObject emptyASN : new JSONObject[]{new JSONObject(), new JSONObject().put("number", 0),
                new JSONObject().put("organization", ""), new JSONObject().put("number", 0).put("organization", "")}) {
            JSONObject report = validClosedCompactReport();
            report.getJSONObject("compact_topology").getJSONArray("nodes").getJSONObject(1).put("asn", emptyASN);
            assertThrows("empty canonical ASN " + emptyASN, ReportParseException.class,
                    () -> ReportParser.parse(report.toString()));
        }
    }

    @Test public void compactGeoContributionMatchesGoEncodingJsonAtExactStringBoundaries() throws Exception {
        assertGeoOrganizationBoundary("x", 4059);
        assertGeoOrganizationBoundary("한", 1353);
        for (String escaped : new String[]{"\"", "\\", "\b", "\t", "\n", "\f", "\r"}) {
            assertGeoOrganizationBoundary(escaped, 2000, 59);
        }
        assertGeoOrganizationBoundary("\u0001", 600, 459);
        for (String escaped : new String[]{"<", ">", "&", "\u2028", "\u2029"}) {
            assertGeoOrganizationBoundary(escaped, 676, 3);
        }

        ReportParser.parse(geoASNOnlyReport("😀").toString());
        for (String malformed : new String[]{"\ud800", "\udfff", "ok\ud800x", "ok\udfffx"}) {
            JSONObject report = geoASNOnlyReport(malformed);
            assertThrows("malformed UTF-16", ReportParseException.class,
                    () -> ReportParser.parse(report.toString()));
        }
    }

    @Test public void acceptsExactProducerGeoBoundaryFixtureAndRejectsNamedInvalidWire4097Mutation() throws Exception {
        String serialized = producerFixture("compact-geo-exact-boundary-report.json");
        JSONObject exact = new JSONObject(serialized);
        String organization = exact.getJSONObject("compact_topology").getJSONArray("nodes").getJSONObject(1)
                .getJSONObject("asn").getString("organization");
        assertEquals("<>&\u2028\u2029".repeat(135) + "boundary!", organization);
        assertEquals(1, ReportParser.parse(serialized).compactTopology().orElseThrow().nodeCount() - 1);

        JSONObject invalidWire4097 = new JSONObject(serialized);
        invalidWire4097.getJSONObject("compact_topology").getJSONArray("nodes").getJSONObject(1)
                .getJSONObject("asn").put("organization", organization + "x");
        ReportParseException failure = assertThrows("invalid-wire-geo-bundle-4097", ReportParseException.class,
                () -> ReportParser.parse(invalidWire4097.toString()));
        assertFalse(failure.getMessage().contains(organization));
    }

    @Test public void compactRequiredFieldMatrixRejectsMissingAndNullRecursively() throws Exception {
        String[][] matrix = {
                {"root", "schema", "selection", "limits", "nodes", "links", "routes", "stats", "result_stats", "geo", "truncated"},
                {"limits", "nodes", "links", "max_response_bytes_exclusive", "max_geo_bundle_bytes"},
                {"node", "id", "kind", "status", "hop_min", "hop_max", "observations"},
                {"link", "from", "to", "status", "observations"},
                {"route", "result_index", "attempt", "status", "reached", "complete", "node_ids"},
                {"stats", "nodes", "links", "routes", "node_observations", "link_observations"},
                {"count", "total", "displayed", "omitted"},
                {"route stats", "total", "displayed", "complete", "partial", "omitted"},
                {"result stats", "result_index", "routes", "node_observations", "link_observations"},
                {"result count stats", "total", "displayed", "omitted"},
                {"result route stats", "total", "displayed", "complete", "partial", "omitted"},
                {"geo stats", "eligible", "available", "included", "omitted", "unavailable"},
                {"geolocation", "latitude", "longitude"}
        };
        int mutations = 0;
        for (String[] row : matrix) {
            for (int fieldIndex = 1; fieldIndex < row.length; fieldIndex++) {
                String field = row[fieldIndex];
                JSONObject missing = validClosedCompactReport();
                compactBoundaryTarget(missing, row[0]).remove(field);
                assertThrows(row[0] + " missing " + field, ReportParseException.class,
                        () -> ReportParser.parse(missing.toString()));
                mutations++;

                JSONObject nil = validClosedCompactReport();
                compactBoundaryTarget(nil, row[0]).put(field, JSONObject.NULL);
                assertThrows(row[0] + " null " + field, ReportParseException.class,
                        () -> ReportParser.parse(nil.toString()));
                mutations++;
            }
        }
        assertEquals(124, mutations);
    }

    @Test public void compactCanonicalOptionalPresenceRejectsValuesGoWouldOmit() throws Exception {
        for (String field : new String[]{"address", "city", "region", "country", "country_code", "organization"}) {
            JSONObject mutation = validClosedCompactReport();
            compactOptionalTarget(mutation, field).put(field, "");
            assertThrows(field + " empty", ReportParseException.class, () -> ReportParser.parse(mutation.toString()));
        }
        JSONObject falsePublic = validClosedCompactReport();
        compactOptionalTarget(falsePublic, "public_ip").put("public_ip", false);
        assertThrows("public_ip false", ReportParseException.class, () -> ReportParser.parse(falsePublic.toString()));

        JSONObject zeroASN = validClosedCompactReport();
        compactOptionalTarget(zeroASN, "number").put("number", 0);
        assertThrows("ASN number zero", ReportParseException.class, () -> ReportParser.parse(zeroASN.toString()));

        JSONObject emptyReasons = validClosedCompactReport();
        emptyReasons.getJSONObject("compact_topology").put("truncation_reasons", new JSONArray());
        assertThrows("present empty truncation_reasons", ReportParseException.class,
                () -> ReportParser.parse(emptyReasons.toString()));
    }

    @Test public void compactRouteAttemptMustExistInReferencedTracerouteInventory() throws Exception {
        JSONObject mutation = new JSONObject(producerFixture("compact-geo-exact-boundary-report.json"));
        mutation.getJSONObject("compact_topology").getJSONArray("routes").getJSONObject(0).put("attempt", 2);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(mutation.toString()));
    }

    @Test public void rejectsMalformedCompactNodeAndSummaryTypes() throws Exception {
        String compact = fixture("compact-traceroute-report.json");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(compact.replace("\"hop_min\": 1", "\"hop_min\": \"1\"")));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(compact.replace("\"total\": 1, \"displayed\": 1", "\"total\": \"1\", \"displayed\": 1")));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(compact.replace("\"kind\": \"hostname\"", "\"kind\": \"router\"")));
    }

    @Test public void rejectsMalformedKnownTopologyFields() {
        String topology = "{\"reached\":true,\"nodes\":[{\"id\":\"n\",\"hop\":1,\"status\":\"healthy\",\"public_ip\":\"yes\"}],\"links\":[]}";
        String withBadPublic = result("traceroute", "healthy").replace("}", ",\"details\":{\"topology\":" + topology + "}}");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("healthy", "{\"total\":1,\"passed\":1,\"failed\":0}", "[" + withBadPublic + "]", "")));
        String badLink = topology.replace("\"public_ip\":\"yes\"", "\"public_ip\":true").replace("\"links\":[]", "\"links\":[{\"from\":\"n\",\"to\":\"n\",\"status\":\"evil\"}]");
        String withBadLink = result("traceroute", "healthy").replace("}", ",\"details\":{\"topology\":" + badLink + "}}");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("healthy", "{\"total\":1,\"passed\":1,\"failed\":0}", "[" + withBadLink + "]", "")));
    }

    @Test public void fullTopologyAcceptsProducerOptionalLatencyDeltaAndItsExactBound() throws Exception {
        JSONObject omittedZero = new JSONObject(enrichmentGoFixture("upstream"));
        JSONObject firstProducerLink = omittedZero.getJSONArray("results").getJSONObject(0).getJSONObject("details")
                .getJSONObject("topology").getJSONArray("links").getJSONObject(0);
        assertFalse("Go omitempty must omit the zero delta", firstProducerLink.has("latency_delta_ms"));
        assertEquals(CheckKind.TRACEROUTE, ReportParser.parse(omittedZero.toString()).results().get(0).kind());

        JSONObject atBound = new JSONObject(enrichmentGoFixture("upstream"));
        fullTopologyLinkWithDelta(atBound).put("latency_delta_ms", 30_000);
        Report parsed = ReportParser.parse(atBound.toString());
        Map<?, ?> details = parsed.results().get(0).details();
        assertThrows(UnsupportedOperationException.class, () -> details.clear());
        Map<?, ?> topology = (Map<?, ?>) details.get("topology");
        assertThrows(UnsupportedOperationException.class, () -> topology.clear());
        Map<?, ?> link = (Map<?, ?>) ((java.util.List<?>) topology.get("links")).get(1);
        assertEquals(30_000, ((Number) link.get("latency_delta_ms")).intValue());
        assertThrows(UnsupportedOperationException.class, () -> ((Map) link).put("latency_delta_ms", 1));
    }

    @Test public void fullTopologyRejectsMalformedOrOutOfRangeProducerLatencyDelta() throws Exception {
        for (Object invalid : new Object[]{"1", -1, 30_001, JSONObject.NULL}) {
            JSONObject report = new JSONObject(enrichmentGoFixture("upstream"));
            fullTopologyLinkWithDelta(report).put("latency_delta_ms", invalid);
            assertThrows(String.valueOf(invalid), ReportParseException.class, () -> ReportParser.parse(report.toString()));
        }

        String overflow = enrichmentGoFixture("upstream").replace("\"latency_delta_ms\":1", "\"latency_delta_ms\":1e309");
        assertNotEquals(enrichmentGoFixture("upstream"), overflow);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(overflow));
    }

    @Test public void nonexistentLinkLatencyMsCannotSubstituteForProducerLatencyDelta() throws Exception {
        JSONObject report = new JSONObject(enrichmentGoFixture("upstream"));
        JSONObject link = fullTopologyLinkWithDelta(report);
        link.remove("latency_delta_ms");
        link.put("latency_ms", 1);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report.toString()));
    }

    private static JSONObject resultShape(String name) throws Exception {
        JSONArray shapes = findingContract().getJSONArray("result_shapes");
        for (int i = 0; i < shapes.length(); i++) {
            if (name.equals(shapes.getJSONObject(i).getString("name"))) return shapes.getJSONObject(i);
        }
        fail("missing producer result shape " + name);
        throw new AssertionError();
    }

    private static String incompatibleKnownKind(JSONObject shape)throws Exception{
        Set<String> allowed=new HashSet<>();
        JSONArray kinds=shape.getJSONArray("kinds");
        for(int i=0;i<kinds.length();i++)allowed.add(kinds.getString(i));
        for(CheckKind candidate:CheckKind.values()){
            String wireKind=candidate.name().toLowerCase(java.util.Locale.ROOT);
            if(!allowed.contains(wireKind))return wireKind;
        }
        return null;
    }

    private static void setSingleResultStatus(JSONObject report, String status) throws Exception {
        report.put("status", status);
        report.getJSONArray("results").getJSONObject(0).put("status", status);
        boolean healthy = "healthy".equals(status);
        report.put("summary", new JSONObject().put("total", 1)
                .put("passed", healthy ? 1 : 0).put("failed", healthy ? 0 : 1));
    }

    private static JSONObject reportForResultShape(JSONObject shape) throws Exception {
        return reportForResultShape(shape,shape.getJSONArray("kinds").getString(0));
    }

    private static JSONObject reportForResultShape(JSONObject shape,String kind) throws Exception {
        String status = shape.getString("status");
        JSONObject result = resultObject(kind, status);
        if (shape.has("error_code")) result.put("error_code", shape.getString("error_code"));
        JSONObject details = new JSONObject();
        JSONArray required = shape.getJSONArray("required_details");
        if(shape.getString("name").startsWith("traceroute_")&&required.length()>0){
            details=traceContractDetails(shape);
        }else{
            for (int i = 0; i < required.length(); i++) putContractDetail(details, required.getJSONObject(i), shape.getString("name"));
            JSONArray optional = shape.getJSONArray("optional_details");
            for (int i = 0; i < optional.length(); i++) putContractDetail(details, optional.getJSONObject(i), shape.getString("name"));
        }
        if (required.length() > 0 || shape.getJSONArray("optional_details").length() > 0) result.put("details", details);
        return oneResultReport(result);
    }

    private static JSONObject traceContractDetails(JSONObject shape)throws Exception{
        String name=shape.getString("name");
        boolean healthy=name.startsWith("traceroute_healthy");
        boolean degraded=name.startsWith("traceroute_degraded");
        boolean destination=name.startsWith("traceroute_destination_unreached");
        boolean incomplete=name.startsWith("traceroute_execution_incomplete");
        boolean topology=name.contains("topology")||healthy||degraded||destination;
        int total=incomplete?(topology?3:2):1,reached=healthy||degraded?1:0;
        int unreached=destination||incomplete&&topology?1:0;
        int execution=total-reached-unreached;
        int timedOut=name.contains("timeout")?execution:incomplete&&execution>1?1:0;
        int cancelled=name.contains("cancelled")?execution:0;
        JSONArray attempts=new JSONArray();
        if(topology){
            JSONObject route=new JSONObject().put("reached",healthy||degraded)
                    .put("nodes",new JSONArray()).put("links",new JSONArray());
            if(degraded)route.getJSONArray("nodes").put(new JSONObject().put("id","n1").put("hop",1).put("status","unknown"));
            attempts.put(new JSONObject().put("attempt",1).put("status",healthy?"healthy":degraded?"degraded":"unreachable").put("topology",route));
        }
        for(int i=attempts.length();i<total;i++){
            String code;
            if(incomplete)code=i==total-1?"timeout":"traceroute_failed";
            else code=cancelled>0?"cancelled":timedOut>0?"timeout":"traceroute_failed";
            attempts.put(new JSONObject().put("attempt",i+1).put("status","unreachable").put("error_code",code));
        }
        JSONObject details=new JSONObject().put("attempts",attempts).put("attempts_total",total)
                .put("attempts_reached",reached).put("attempts_failed",total-reached)
                .put("attempts_unreached",unreached).put("attempts_execution_failed",execution)
                .put("attempts_timed_out",timedOut).put("attempts_cancelled",cancelled);
        Set<String> keys=new HashSet<>();JSONArray required=shape.getJSONArray("required_details");
        for(int i=0;i<required.length();i++)keys.add(required.getJSONObject(i).getString("key"));
        if(keys.contains("topology"))details.put("topology",new JSONObject().put("reached",healthy||degraded)
                .put("nodes",new JSONArray()).put("links",new JSONArray()));
        if(keys.contains("geoip_enrichment"))details.put("geoip_enrichment",new JSONObject()
                .put("provider","geoip").put("source","none").put("cache_hits",0).put("upstream_fetches",0)
                .put("max_age_ms",0).put("failures",new JSONArray()));
        if(keys.contains("geoip_provider_failures"))details.put("geoip_provider_failures",1);
        return details;
    }

    private static void putContractDetail(JSONObject details, JSONObject detail, String shapeName) throws Exception {
        String key = detail.getString("key");
        String type=detail.getString("type");
        if (detail.has("const")) details.put(key, detail.getString("const"));
        else if ("certificate_not_before".equals(key)) details.put(key,
                "tls_certificate_not_yet_valid".equals(shapeName) ? "2027-01-01T00:00:00Z" : "2023-01-01T00:00:00Z");
        else if ("certificate_not_after".equals(key)) details.put(key,
                "tls_certificate_not_yet_valid".equals(shapeName) ? "2028-01-01T00:00:00Z" : "2024-01-01T00:00:00Z");
        else if ("certificate_expires_at".equals(key)) details.put(key, "2027-01-01T00:00:00Z");
        else if ("string_array".equals(type)) details.put(key,new JSONArray().put("192.0.2.1"));
        else if ("nonnegative_integer".equals(type)) details.put(key,key.equals("answer_count")?1:200);
        else if ("nonempty_string".equals(type)) details.put(key,"TLS 1.3");
        else details.put(key, key + "-value");
    }

    private static JSONObject reportWithFindingCode(String code) throws Exception {
        JSONObject root = oneResultReport(resultObject("https", "unreachable")
                .put("error_code", "connection_failed"));
        JSONObject analysis = new JSONObject().put("verdict", "attention")
                .put("findings", new JSONArray().put(new JSONObject()
                        .put("id", "f-shared").put("code", code).put("severity", "warning")
                        .put("category", "security").put("title", "fixture title").put("summary", "fixture summary")
                        .put("confidence", "direct").put("evidence_ids", new JSONArray().put("e-shared"))
                        .put("action_ids", new JSONArray().put("a-shared"))))
                .put("evidence", new JSONArray().put(new JSONObject()
                        .put("id", "e-shared").put("result_index", 0).put("kind", "https").put("address", "secret.example.test")
                        .put("signal", "fixture").put("observed", "fixture").put("provenance", "result")))
                .put("actions", new JSONArray().put(new JSONObject()
                        .put("id", "a-shared").put("title", "fixture action").put("step", "fixture step")
                        .put("expected_result", "fixture result").put("escalation_condition", "fixture escalation")))
                .put("coverage", new JSONObject().put("available", new JSONArray()).put("missing", new JSONArray())
                        .put("provider_failures", new JSONArray()).put("limitations", new JSONArray()));
        return root.put("analysis", analysis);
    }

    private static JSONObject fullTopologyLinkWithDelta(JSONObject report) throws Exception {
        return report.getJSONArray("results").getJSONObject(0).getJSONObject("details")
                .getJSONObject("topology").getJSONArray("links").getJSONObject(1);
    }

    private static String result(String kind, String status) {
        return "{\"kind\":\"" + kind + "\",\"address\":\"secret.example.test\",\"status\":\"" + status
                + "\",\"latency_ms\":12,\"started_at\":\"2026-09-02T00:00:00Z\"}";
    }

    private static String report(String status, String summary, String results, String extra) {
        return "{\"id\":\"private-report-id\",\"status\":\"" + status
                + "\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":14,\"summary\":"
                + summary + ",\"results\":" + results + extra + "}";
    }

    @Test public void parsesLegacyReportAsDeeplyImmutableModel() {
        Report parsed = ReportParser.parse(report("healthy", "{\"total\":1,\"passed\":1,\"failed\":0}", "[" + result("https", "healthy") + "]", ""));
        assertEquals(Report.Status.HEALTHY, parsed.status());
        assertEquals(CheckKind.HTTPS, parsed.results().get(0).kind());
        assertFalse(parsed.analysis().isPresent());
        assertThrows(UnsupportedOperationException.class, () -> parsed.results().clear());
    }

    @Test public void acceptsHttpsDowngradeAnalysisAndChecksReferences() {
        String analysis = ",\"analysis\":{" +
                "\"verdict\":\"attention\"," +
                "\"findings\":[{\"id\":\"f-1\",\"code\":\"tls_downgrade\",\"severity\":\"critical\",\"category\":\"security\",\"title\":\"HTTPS downgraded\",\"summary\":\"Redirect used HTTP\",\"confidence\":\"direct\",\"evidence_ids\":[\"e-1\"],\"action_ids\":[\"a-1\"]}]," +
                "\"evidence\":[{\"id\":\"e-1\",\"result_index\":0,\"kind\":\"https\",\"address\":\"secret.example.test\",\"signal\":\"error_code\",\"observed\":\"tls_downgrade\",\"expected\":\"TLS\",\"provenance\":\"result\"}]," +
                "\"actions\":[{\"id\":\"a-1\",\"title\":\"Keep TLS\",\"step\":\"Inspect redirect\",\"expected_result\":\"HTTPS\",\"escalation_condition\":\"Still downgraded\"}]," +
                "\"coverage\":{\"available\":[\"error_code\"],\"missing\":[],\"provider_failures\":[],\"limitations\":[]}}";
        String downgradeResult = result("https", "unreachable").replace("}",
                ",\"error_code\":\"tls_downgrade\"}");
        Report parsed = ReportParser.parse(report("unreachable", "{\"total\":1,\"passed\":0,\"failed\":1}",
                "[" + downgradeResult + "]", analysis));
        assertEquals(Report.Verdict.ATTENTION, parsed.analysis().orElseThrow().verdict());
        assertEquals(Report.FindingCode.TLS_DOWNGRADE, parsed.analysis().orElseThrow().findings().get(0).code());
    }

    @Test public void rejectsMultipleValuesWrongTypesUnknownEnumsAndNegativeNumbers() {
        String valid = report("healthy", "{\"total\":0,\"passed\":0,\"failed\":0}", "[]", "");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(valid + " {}"));
        assertThrows(ReportParseException.class, () -> ReportParser.parse("[]"));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(valid.replace("\"duration_ms\":14", "\"duration_ms\":\"14\"")));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(valid.replace("\"healthy\"", "\"mystery\"")));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(valid.replace("\"duration_ms\":14", "\"duration_ms\":-1")));
    }

    @Test public void rejectsUppercaseWireEnumsInsteadOfNormalizingThem() throws Exception {
        String valid = report("healthy", "{\"total\":1,\"passed\":1,\"failed\":0}", "[" + result("dns", "healthy") + "]", "");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(valid.replace("\"kind\":\"dns\"", "\"kind\":\"DNS\"")));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(valid.replace("\"status\":\"healthy\"", "\"status\":\"HEALTHY\"")));

        JSONObject analyzed = new JSONObject(fixture("https-downgrade-report.json"));
        analyzed.getJSONObject("analysis").getJSONArray("findings").getJSONObject(0).put("severity", "CRITICAL");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(analyzed.toString()));
    }

    @Test public void rejectsSummaryAndOverallStatusContradictions() {
        String oneHealthy = "[" + result("dns", "healthy") + "]";
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("healthy", "{\"total\":1,\"passed\":0,\"failed\":1}", oneHealthy, "")));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("degraded", "{\"total\":1,\"passed\":1,\"failed\":0}", oneHealthy, "")));
    }

    @Test public void acceptsActualGoFullTracerouteAtGlobalDepthNine() throws Exception {
        Report parsed = ReportParser.parse(fixture("go-full-traceroute-report.json"));
        assertEquals(CheckKind.TRACEROUTE, parsed.results().get(0).kind());
        assertEquals(1, ((java.util.List<?>) parsed.results().get(0).details().get("attempts")).size());
    }

    @Test public void rejectsOversizedUtf8AndExcessiveLexicalDepthBeforeMaterializingJson() {
        String valid = report("healthy", "{\"total\":0,\"passed\":0,\"failed\":0}", "[]", "");
        String oversizedUtf8 = "{\"padding\":\"" + "한".repeat(ContractLimits.MAX_TRANSPORT_BYTES / 3) + "\"}";
        assertTrue(oversizedUtf8.length() < ContractLimits.MAX_TRANSPORT_BYTES);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oversizedUtf8));

        String twelveThousandDeep = "[".repeat(12_000) + "0" + "]".repeat(12_000);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(twelveThousandDeep));

        String depthSixteen = valid.substring(0, valid.length() - 1) + ",\"nested\":"
                + "[".repeat(ContractLimits.MAX_JSON_DEPTH - 1) + "0"
                + "]".repeat(ContractLimits.MAX_JSON_DEPTH - 1) + "}";
        assertEquals("private-report-id", ReportParser.parse(depthSixteen).id());
        String depthSeventeen = valid.substring(0, valid.length() - 1) + ",\"nested\":"
                + "[".repeat(ContractLimits.MAX_JSON_DEPTH) + "0"
                + "]".repeat(ContractLimits.MAX_JSON_DEPTH) + "}";
        assertThrows(ReportParseException.class, () -> ReportParser.parse(depthSeventeen));
    }

    @Test public void rejectsProducerDerivedDuplicatesAtEveryObjectBoundaryAndKeepsExactBudgetsCount100() throws Exception {
        List<String> canonicalReports = List.of(
                maximumGoAnalysisFixture(),
                checkerExecutionGoFixture(),
                enrichmentGoFixture("upstream"),
                enrichmentGoFixture("failures"),
                producerFixture("compact-zero-route-report.json"),
                producerFixture("compact-zero-node-attempt-report.json"),
                producerFixture("compact-geo-exact-boundary-report.json"),
                fixture("go-full-traceroute-report.json"));
        int objectBoundaries = 0;
        for (String canonical : canonicalReports) {
            ReportParser.parse(canonical);
            List<String> mutations = duplicateFirstFieldAtEveryObject(canonical);
            assertFalse("producer fixture must contain object boundaries", mutations.isEmpty());
            objectBoundaries += mutations.size();
            for (String mutation : mutations) assertDuplicateRejected(mutation);
        }
        assertTrue("corpus must span the complete nested producer contract", objectBoundaries > 100);

        String valid = report("healthy", "{\"total\":0,\"passed\":0,\"failed\":0}", "[]", "");
        String duplicateRoot = valid.replaceFirst("\\{", "{\"id\":null,");
        String duplicateSummary = valid.replace("\"summary\":{", "\"summary\":{\"total\":null,");
        for (int run = 0; run < 100; run++) {
            assertDuplicateRejected(duplicateRoot);
            assertDuplicateRejected(duplicateSummary);
        }

        int baseStringChars = "private-report-id".length() + "healthy".length()
                + "2026-09-02T00:00:00Z".length();
        String exactStrings = valid.substring(0, valid.length() - 1) + ",\"padding\":\""
                + "x".repeat(ContractLimits.MAX_REPORT_STRING_CHARS - baseStringChars) + "\"}";
        assertEquals("private-report-id", ReportParser.parse(exactStrings).id());
        assertThrows(ReportParseException.class, () -> ReportParser.parse(
                exactStrings.substring(0, exactStrings.length() - 2) + "x\"}"));

        String exactContainers = valid.substring(0, valid.length() - 1) + ",\"padding\":["
                + "{},".repeat(ContractLimits.MAX_REPORT_CONTAINERS - 5) + "{}]}";
        assertEquals("private-report-id", ReportParser.parse(exactContainers).id());
        assertThrows(ReportParseException.class, () -> ReportParser.parse(
                exactContainers.substring(0, exactContainers.length() - 2) + ",{}]}"));

        String exactNodes = valid.substring(0, valid.length() - 1) + ",\"padding\":{\"nodes\":["
                + "null,".repeat(ContractLimits.MAX_TOPOLOGY_NODES_TOTAL - 1) + "null]}}";
        assertEquals("private-report-id", ReportParser.parse(exactNodes).id());
        assertThrows(ReportParseException.class, () -> ReportParser.parse(
                exactNodes.replace("null]}}", "null,null]}}")));

        String exactLinks = valid.substring(0, valid.length() - 1) + ",\"padding\":{\"links\":["
                + "null,".repeat(ContractLimits.MAX_TOPOLOGY_LINKS_TOTAL - 1) + "null]}}";
        assertEquals("private-report-id", ReportParser.parse(exactLinks).id());
        assertThrows(ReportParseException.class, () -> ReportParser.parse(
                exactLinks.replace("null]}}", "null,null]}}")));

        String deeplyMalformed = "{\"id\":\"r\",\"nested\":" + "[".repeat(12_000);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(deeplyMalformed));
    }

    private static void assertDuplicateRejected(String mutation) {
        ReportParseException failure = assertThrows(ReportParseException.class,
                () -> ReportParser.parse(mutation));
        assertTrue(failure.getMessage(), failure.getMessage().contains("duplicate field"));
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

    @Test public void closedDetailsRejectUnknownNestingWithinAndBeyondGenericDepthBudget() throws Exception {
        JSONObject valid = resultObject("dns", "healthy").put("details", new JSONObject()
                .put("addresses", new JSONArray().put("203.0.113.10")).put("answer_count", 1));
        assertEquals(CheckKind.DNS, ReportParser.parse(oneResultReport(valid).toString()).results().get(0).kind());

        JSONObject withinBudget = resultObject("dns", "healthy").put("details", new JSONObject().put(
                "nested", new JSONArray("[".repeat(ContractLimits.MAX_DETAIL_DEPTH) + "0"
                        + "]".repeat(ContractLimits.MAX_DETAIL_DEPTH))));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(withinBudget).toString()));

        JSONObject beyondBudget = resultObject("dns", "healthy").put("details", new JSONObject().put(
                "nested", new JSONArray("[".repeat(ContractLimits.MAX_DETAIL_DEPTH + 1) + "0"
                        + "]".repeat(ContractLimits.MAX_DETAIL_DEPTH + 1))));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(beyondBudget).toString()));
    }

    @Test public void lexicalDepthGuardIgnoresBracketsAndEscapesInsideStrings() {
        String valid = report("healthy", "{\"total\":0,\"passed\":0,\"failed\":0}", "[]", "");
        String text = "[{}] \\\" quoted \\\\ [ still text";
        String withBracketsInString = valid.substring(0, valid.length() - 1)
                + ",\"noise\":" + JSONObject.quote(text) + "}";
        assertEquals("private-report-id", ReportParser.parse(withBracketsInString).id());
    }

    @Test public void rejectsMoreThanTwentyResultsAndOversizedStringsDetailsOrContainers() {
        StringBuilder results = new StringBuilder("[");
        for (int i = 0; i < 21; i++) { if (i > 0) results.append(','); results.append(result("dns", "healthy")); }
        results.append(']');
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("healthy", "{\"total\":20,\"passed\":20,\"failed\":0}", results.toString(), "")));
        String oversized = "x".repeat(ContractLimits.MAX_STRING_CHARS + 1);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("healthy", "{\"total\":1,\"passed\":1,\"failed\":0}", "[" + result("dns", "healthy").replace("secret.example.test", oversized) + "]", "")));
        String deep = "0"; for (int i = 0; i < ContractLimits.MAX_DETAIL_DEPTH + 2; i++) deep = "[" + deep + "]";
        String withDetails = result("dns", "healthy").replace("}", ",\"details\":{\"nested\":" + deep + "}}");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("healthy", "{\"total\":1,\"passed\":1,\"failed\":0}", "[" + withDetails + "]", "")));
    }

    @Test public void knownTraceAttemptsOnlyBelongToTracerouteResults() throws Exception {
        JSONObject details = validTraceDetails();
        JSONObject nonTrace = resultObject("dns", "healthy").put("details", details);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(nonTrace).toString()));
    }

    @Test public void knownTraceAttemptsValidateFieldsErrorTopologyAndStatus() throws Exception {
        JSONObject badNumber = validTraceDetails();
        badNumber.getJSONArray("attempts").getJSONObject(0).put("attempt", 11);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(resultObject("traceroute", "healthy").put("details", badNumber)).toString()));

        JSONObject missingTopology = validTraceDetails();
        missingTopology.getJSONArray("attempts").getJSONObject(0).remove("topology");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(resultObject("traceroute", "healthy").put("details", missingTopology)).toString()));

        JSONObject badErrorStatus = validTraceDetails();
        badErrorStatus.getJSONArray("attempts").getJSONObject(0).put("error_code", "timeout");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(resultObject("traceroute", "healthy").put("details", badErrorStatus)).toString()));

        JSONObject badTopologyStatus = validTraceDetails();
        badTopologyStatus.getJSONArray("attempts").getJSONObject(0).put("status", "unreachable")
                .getJSONObject("topology").put("reached", true);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(resultObject("traceroute", "unreachable").put("details", badTopologyStatus)).toString()));

        JSONObject unsupportedError = validTraceDetails();
        unsupportedError.getJSONArray("attempts").getJSONObject(0).put("status", "unreachable").put("error_code", "secret_backend_error");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(resultObject("traceroute", "unreachable").put("details", unsupportedError)).toString()));
    }

    @Test public void traceCountersWhenPresentMatchAttemptsAndResultStatus() throws Exception {
        JSONObject details = validTraceDetails();
        details.put("attempts_total", 1).put("attempts_reached", 1).put("attempts_failed", 0)
                .put("attempts_unreached", 0).put("attempts_execution_failed", 0)
                .put("attempts_timed_out", 0).put("attempts_cancelled", 0);
        assertEquals(CheckKind.TRACEROUTE, ReportParser.parse(oneResultReport(resultObject("traceroute", "healthy").put("details", details)).toString()).results().get(0).kind());

        JSONObject mismatch = new JSONObject(details.toString()).put("attempts_reached", 0);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(resultObject("traceroute", "healthy").put("details", mismatch)).toString()));

        JSONObject partialCounters = validTraceDetails().put("attempts_total", 1);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(resultObject("traceroute", "healthy").put("details", partialCounters)).toString()));
    }

    private static JSONObject validTraceDetails() throws Exception {
        JSONObject topology = new JSONObject().put("reached", true)
                .put("nodes", new JSONArray())
                .put("links", new JSONArray());
        return new JSONObject().put("attempts", new JSONArray().put(new JSONObject()
                .put("attempt", 1).put("status", "healthy").put("topology", topology)));
    }

    private static JSONObject resultObject(String kind,String status) throws Exception {
        return new JSONObject(result(kind,status));
    }

    private static JSONObject oneResultReport(JSONObject value) throws Exception {
        String status = value.getString("status");
        boolean healthy = "healthy".equals(status);
        return new JSONObject(report(status,
                healthy?"{\"total\":1,\"passed\":1,\"failed\":0}":"{\"total\":1,\"passed\":0,\"failed\":1}",
                new JSONArray().put(value).toString(),""));
    }

    @Test public void rejectsDuplicateAnalysisIdsUnknownReferencesAndResultReferences() {
        String base = ",\"analysis\":{\"verdict\":\"attention\",\"findings\":[{\"id\":\"f\",\"code\":\"tls_downgrade\",\"severity\":\"warning\",\"category\":\"security\",\"title\":\"t\",\"summary\":\"s\",\"confidence\":\"direct\",\"evidence_ids\":[\"%s\"],\"action_ids\":[]}],\"evidence\":[{\"id\":\"e\",\"result_index\":%d,\"kind\":\"https\",\"address\":\"a\",\"signal\":\"s\",\"observed\":\"o\",\"provenance\":\"result\"}],\"actions\":[],\"coverage\":{\"available\":[],\"missing\":[],\"provider_failures\":[],\"limitations\":[]}}";
        String prefixResult = "[" + result("https", "unreachable") + "]";
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("unreachable", "{\"total\":1,\"passed\":0,\"failed\":1}", prefixResult, String.format(base, "missing", 0))));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("unreachable", "{\"total\":1,\"passed\":0,\"failed\":1}", prefixResult, String.format(base, "e", 1))));
    }

    @Test public void resultIndexedAnalysisKindMustMatchReferencedResultWithoutReflectingProse() throws Exception {
        JSONObject evidenceMismatch = reportWithFindingCode("tls_untrusted");
        JSONObject evidence = evidenceMismatch.getJSONObject("analysis").getJSONArray("evidence").getJSONObject(0);
        evidence.put("kind", "dns").put("observed", "HOSTILE_EVIDENCE_PROSE_CANARY");
        ReportParseException evidenceFailure = assertThrows(ReportParseException.class,
                () -> ReportParser.parse(evidenceMismatch.toString()));
        assertFalse(evidenceFailure.getMessage().contains("HOSTILE_EVIDENCE_PROSE_CANARY"));

        for (String collection : new String[]{"provider_failures", "limitations"}) {
            JSONObject coverageMismatch = reportWithFindingCode("tls_untrusted");
            coverageMismatch.getJSONObject("analysis").getJSONObject("coverage").getJSONArray(collection)
                    .put(new JSONObject().put("code", "unsupported_details").put("result_index", 0)
                            .put("kind", "dns").put("signal", "fixture")
                            .put("reason", "HOSTILE_COVERAGE_PROSE_CANARY"));
            ReportParseException coverageFailure = assertThrows(collection, ReportParseException.class,
                    () -> ReportParser.parse(coverageMismatch.toString()));
            assertFalse(coverageFailure.getMessage().contains("HOSTILE_COVERAGE_PROSE_CANARY"));
        }
    }

    @Test public void evidenceAttemptReferencesOnlyExistingTracerouteAttempts() throws Exception {
        JSONObject nonTrace = reportWithFindingCode("tls_untrusted");
        nonTrace.getJSONObject("analysis").getJSONArray("evidence").getJSONObject(0)
                .put("attempt", 1).put("observed", "HOSTILE_NON_TRACE_ATTEMPT_CANARY");
        ReportParseException nonTraceFailure = assertThrows(ReportParseException.class,
                () -> ReportParser.parse(nonTrace.toString()));
        assertFalse(nonTraceFailure.getMessage().contains("HOSTILE_NON_TRACE_ATTEMPT_CANARY"));

        JSONObject traceDetails = validTraceDetails();
        traceDetails.put("attempts_total", 1).put("attempts_reached", 1).put("attempts_failed", 0)
                .put("attempts_unreached", 0).put("attempts_execution_failed", 0)
                .put("attempts_timed_out", 0).put("attempts_cancelled", 0);
        JSONObject trace = oneResultReport(resultObject("traceroute", "healthy").put("details", traceDetails));
        JSONObject analysis = new JSONObject(reportWithFindingCode("tls_untrusted").getJSONObject("analysis").toString());
        JSONObject traceEvidence = analysis.getJSONArray("evidence").getJSONObject(0);
        traceEvidence.put("kind", "traceroute").put("attempt", 1);
        trace.put("analysis", analysis);
        assertEquals(Integer.valueOf(1), ReportParser.parse(trace.toString()).analysis().orElseThrow()
                .evidence().get(0).attempt().orElseThrow());

        for (int outOfRange : new int[]{2, 10}) {
            JSONObject mutation = new JSONObject(trace.toString());
            mutation.getJSONObject("analysis").getJSONArray("evidence").getJSONObject(0)
                    .put("attempt", outOfRange).put("observed", "HOSTILE_OUT_OF_RANGE_ATTEMPT_CANARY");
            ReportParseException failure = assertThrows("attempt " + outOfRange, ReportParseException.class,
                    () -> ReportParser.parse(mutation.toString()));
            assertFalse(failure.getMessage().contains("HOSTILE_OUT_OF_RANGE_ATTEMPT_CANARY"));
        }
    }

    @Test public void attemptReferencesRejectWhenTracerouteHasNoInventoryOrTotal() throws Exception {
        JSONObject evidenceReport = oneResultReport(resultObject("traceroute", "healthy"));
        JSONObject analysis = new JSONObject(reportWithFindingCode("tls_untrusted").getJSONObject("analysis").toString());
        analysis.getJSONArray("evidence").getJSONObject(0).put("kind", "traceroute").put("attempt", 10);
        evidenceReport.put("analysis", analysis);
        assertThrows("evidence attempt without source inventory", ReportParseException.class,
                () -> ReportParser.parse(evidenceReport.toString()));

        JSONObject compactReport = new JSONObject(fixture("compact-traceroute-report.json"));
        compactReport.getJSONArray("results").getJSONObject(0).remove("details");
        compactReport.getJSONObject("compact_topology").getJSONArray("routes").getJSONObject(0).put("attempt", 10);
        assertThrows("compact route attempt without source inventory", ReportParseException.class,
                () -> ReportParser.parse(compactReport.toString()));
    }

    @Test public void attemptInventoryIsAuthoritativeAndPreservesValidDuplicateOrder() throws Exception {
        JSONObject details = validTraceDetails();
        JSONObject first = details.getJSONArray("attempts").getJSONObject(0)
                .put("attempt", 10).put("message", "first duplicate");
        details.getJSONArray("attempts").put(new JSONObject(first.toString()).put("message", "second duplicate"));
        details.put("attempts_total", 2).put("attempts_reached", 2).put("attempts_failed", 0)
                .put("attempts_unreached", 0).put("attempts_execution_failed", 0)
                .put("attempts_timed_out", 0).put("attempts_cancelled", 0);
        JSONObject report = oneResultReport(resultObject("traceroute", "healthy").put("details", details));
        JSONObject analysis = new JSONObject(reportWithFindingCode("tls_untrusted").getJSONObject("analysis").toString());
        analysis.getJSONArray("evidence").getJSONObject(0).put("kind", "traceroute").put("attempt", 10);
        report.put("analysis", analysis);

        Report parsed = ReportParser.parse(report.toString());
        java.util.List<?> attempts = (java.util.List<?>) parsed.results().get(0).details().get("attempts");
        assertEquals(2, attempts.size());
        assertEquals("first duplicate", ((Map<?, ?>) attempts.get(0)).get("message"));
        assertEquals("second duplicate", ((Map<?, ?>) attempts.get(1)).get("message"));

        JSONObject counterOnly = new JSONObject(report.toString());
        counterOnly.getJSONArray("results").getJSONObject(0).getJSONObject("details").remove("attempts");
        counterOnly.getJSONObject("analysis").getJSONArray("evidence").getJSONObject(0).put("attempt", 2);
        assertEquals(Integer.valueOf(2), ReportParser.parse(counterOnly.toString()).analysis().orElseThrow()
                .evidence().get(0).attempt().orElseThrow());

        JSONObject inventoryWins = new JSONObject(report.toString());
        inventoryWins.getJSONObject("analysis").getJSONArray("evidence").getJSONObject(0).put("attempt", 2);
        assertThrows("inventory must take precedence over attempts_total", ReportParseException.class,
                () -> ReportParser.parse(inventoryWins.toString()));
    }

    private static JSONObject validClosedCompactReport() throws Exception {
        JSONObject root = twoNodeCompactReport();
        JSONObject compact = root.getJSONObject("compact_topology");
        JSONObject node = compact.getJSONArray("nodes").getJSONObject(1);
        node.put("latency_ms_avg", 2.5).put("public_ip", true)
                .put("geolocation", new JSONObject().put("city", "Seoul").put("region", "Seoul")
                        .put("country", "South Korea").put("country_code", "KR")
                        .put("latitude", 37.5).put("longitude", 127.0))
                .put("asn", new JSONObject().put("number", 64500).put("organization", "Example Network"));
        compact.put("geo", new JSONObject().put("eligible", 1).put("available", 1)
                .put("included", 1).put("omitted", 0).put("unavailable", 0));
        return root;
    }

    private static JSONObject compactBoundaryTarget(JSONObject report, String boundary) throws Exception {
        JSONObject compact = report.getJSONObject("compact_topology");
        JSONObject resultStats = compact.getJSONArray("result_stats").getJSONObject(0);
        return switch (boundary) {
            case "root" -> compact;
            case "limits" -> compact.getJSONObject("limits");
            case "node" -> compact.getJSONArray("nodes").getJSONObject(1);
            case "link" -> compact.getJSONArray("links").getJSONObject(0);
            case "route" -> compact.getJSONArray("routes").getJSONObject(0);
            case "stats" -> compact.getJSONObject("stats");
            case "count" -> compact.getJSONObject("stats").getJSONObject("nodes");
            case "route stats" -> compact.getJSONObject("stats").getJSONObject("routes");
            case "result stats" -> resultStats;
            case "result count stats" -> resultStats.getJSONObject("node_observations");
            case "result route stats" -> resultStats.getJSONObject("routes");
            case "geo stats" -> compact.getJSONObject("geo");
            case "geolocation" -> compact.getJSONArray("nodes").getJSONObject(1).getJSONObject("geolocation");
            case "asn" -> compact.getJSONArray("nodes").getJSONObject(1).getJSONObject("asn");
            default -> throw new AssertionError(boundary);
        };
    }

    private static JSONObject compactOptionalTarget(JSONObject report, String field) throws Exception {
        JSONObject compact = report.getJSONObject("compact_topology");
        JSONObject firstNode = compact.getJSONArray("nodes").getJSONObject(0);
        JSONObject metadataNode = compact.getJSONArray("nodes").getJSONObject(1);
        return switch (field) {
            case "truncation_reasons" -> compact;
            case "address", "public_ip" -> firstNode;
            case "latency_ms_avg", "geolocation", "asn" -> metadataNode;
            case "city", "region", "country", "country_code" -> metadataNode.getJSONObject("geolocation");
            case "number", "organization" -> metadataNode.getJSONObject("asn");
            default -> throw new AssertionError(field);
        };
    }

    private static JSONObject geoASNOnlyReport(String organization) throws Exception {
        JSONObject report = validClosedCompactReport();
        JSONObject node = report.getJSONObject("compact_topology").getJSONArray("nodes").getJSONObject(1);
        node.remove("geolocation");
        node.put("asn", new JSONObject().put("number", 1).put("organization", organization));
        return report;
    }

    private static void assertGeoOrganizationBoundary(String repeated, int count) throws Exception {
        assertGeoOrganizationBoundary(repeated, count, 0);
    }

    private static void assertGeoOrganizationBoundary(String repeated, int count, int padding) throws Exception {
        JSONObject exact = geoASNOnlyReport(repeated.repeat(count) + "x".repeat(padding));
        ReportParser.parse(exact.toString());
        exact.getJSONObject("compact_topology").getJSONArray("nodes").getJSONObject(1)
                .getJSONObject("asn").put("organization", repeated.repeat(count) + "x".repeat(padding + 1));
        assertThrows("Go encoding/json contribution 4097", ReportParseException.class,
                () -> ReportParser.parse(exact.toString()));
    }

    private static JSONObject twoNodeCompactReport() throws Exception {
        JSONObject root = new JSONObject(fixture("compact-traceroute-report.json"));
        JSONObject compact = root.getJSONObject("compact_topology");
        compact.getJSONArray("nodes").put(new JSONObject()
                .put("id", "n2").put("kind", "ip").put("address", "192.0.2.1")
                .put("status", "healthy").put("hop_min", 2).put("hop_max", 2).put("observations", 1));
        compact.getJSONArray("links").put(new JSONObject()
                .put("from", "n1").put("to", "n2").put("status", "healthy").put("observations", 1));
        compact.getJSONArray("routes").getJSONObject(0).getJSONArray("node_ids").put("n2");
        JSONObject stats = compact.getJSONObject("stats");
        stats.put("nodes", count(2, 2, 0));
        stats.put("links", count(1, 1, 0));
        stats.put("node_observations", count(2, 2, 0));
        stats.put("link_observations", count(1, 1, 0));
        JSONObject resultStats = compact.getJSONArray("result_stats").getJSONObject(0);
        resultStats.put("node_observations", count(2, 2, 0));
        resultStats.put("link_observations", count(1, 1, 0));
        return root;
    }

    private static JSONObject count(int total, int displayed, int omitted) throws Exception {
        return new JSONObject().put("total", total).put("displayed", displayed).put("omitted", omitted);
    }

    @Test public void rejectsLegacyNilCompactCollections() throws Exception {
        assertThrows(ReportParseException.class, () -> ReportParser.parse(fixture("go-zero-route-report.json")));
    }

    @Test public void compactCollectionNormalizationRejectsOtherWrongTypes() throws Exception {
        for (String key : new String[]{"nodes", "links", "routes"}) {
            JSONObject wrong = new JSONObject(producerFixture("compact-zero-route-report.json"));
            wrong.getJSONObject("compact_topology").put(key, new JSONObject());
            assertThrows(key, ReportParseException.class, () -> ReportParser.parse(wrong.toString()));
        }
        JSONObject wrongReasons = new JSONObject(producerFixture("compact-zero-route-report.json"));
        wrongReasons.getJSONObject("compact_topology").put("truncation_reasons", "none");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(wrongReasons.toString()));
    }

    @Test public void compactResultStatsAreMandatoryForZeroAndOneResults() throws Exception {
        JSONObject one = new JSONObject(producerFixture("compact-zero-route-report.json"));
        one.getJSONObject("compact_topology").remove("result_stats");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(one.toString()));

        JSONObject zero = new JSONObject(producerFixture("compact-zero-route-report.json"));
        zero.put("status", "healthy").put("summary", new JSONObject()
                .put("total", 0).put("passed", 0).put("failed", 0)).put("results", new JSONArray());
        zero.getJSONObject("compact_topology").put("result_stats", new JSONArray());
        assertEquals(0, ReportParser.parse(zero.toString()).results().size());
        zero.getJSONObject("compact_topology").remove("result_stats");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(zero.toString()));
    }

    @Test public void compactLinksAndRouteEdgesAreCanonicalAndReal() throws Exception {
        JSONObject self = twoNodeCompactReport();
        self.getJSONObject("compact_topology").getJSONArray("links").getJSONObject(0).put("to", "n1");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(self.toString()));

        JSONObject duplicate = twoNodeCompactReport();
        JSONObject topology = duplicate.getJSONObject("compact_topology");
        topology.getJSONArray("links").put(new JSONObject(topology.getJSONArray("links").getJSONObject(0).toString()));
        topology.getJSONObject("stats").put("links", count(2, 2, 0));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(duplicate.toString()));

        JSONObject missingEdge = twoNodeCompactReport();
        JSONObject missingTopology = missingEdge.getJSONObject("compact_topology");
        missingTopology.put("links", new JSONArray());
        missingTopology.getJSONObject("stats").put("links", count(0, 0, 0));
        missingTopology.getJSONObject("stats").put("link_observations", count(1, 0, 1));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(missingEdge.toString()));

        JSONObject consecutiveDuplicate = new JSONObject(fixture("compact-traceroute-report.json"));
        JSONObject duplicateTopology = consecutiveDuplicate.getJSONObject("compact_topology");
        duplicateTopology.getJSONArray("routes").getJSONObject(0).getJSONArray("node_ids").put("n1");
        duplicateTopology.getJSONObject("stats").put("node_observations", count(2, 2, 0));
        duplicateTopology.getJSONArray("result_stats").getJSONObject(0).put("node_observations", count(2, 2, 0));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(consecutiveDuplicate.toString()));
    }

    @Test public void compactResultStatsAreMandatoryOrderedAndMatchRoutesAndObservations() throws Exception {
        JSONObject missing = new JSONObject(fixture("compact-traceroute-report.json"));
        missing.getJSONObject("compact_topology").remove("result_stats");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(missing.toString()));

        JSONObject wrongIndex = new JSONObject(fixture("compact-traceroute-report.json"));
        wrongIndex.getJSONObject("compact_topology").getJSONArray("result_stats").getJSONObject(0).put("result_index", 1);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(wrongIndex.toString()));

        JSONObject nonTraceRoute = new JSONObject(fixture("compact-traceroute-report.json"));
        nonTraceRoute.getJSONArray("results").getJSONObject(0).put("kind", "dns");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(nonTraceRoute.toString()));

        JSONObject badObservation = new JSONObject(fixture("compact-traceroute-report.json"));
        JSONObject topology = badObservation.getJSONObject("compact_topology");
        topology.getJSONObject("stats").put("node_observations", count(2, 0, 2));
        topology.getJSONArray("result_stats").getJSONObject(0).put("node_observations", count(2, 0, 2));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(badObservation.toString()));
    }

    @Test public void compactGlobalAndPerResultStatsMatchArraysAndRouteKinds() throws Exception {
        JSONObject badComplete = new JSONObject(fixture("compact-traceroute-report.json"));
        badComplete.getJSONObject("compact_topology").getJSONArray("routes").getJSONObject(0).put("complete", false);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(badComplete.toString()));

        JSONObject badGlobal = new JSONObject(fixture("compact-traceroute-report.json"));
        badGlobal.getJSONObject("compact_topology").getJSONObject("stats").put("node_observations", count(2, 2, 0));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(badGlobal.toString()));

        JSONObject badLinkAggregate = twoNodeCompactReport();
        badLinkAggregate.getJSONObject("compact_topology").getJSONArray("links").getJSONObject(0).put("observations", 2);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(badLinkAggregate.toString()));
    }

    @Test public void compactRouteReachedAndStatusMustAgree() throws Exception {
        JSONObject reachedUnreachable = new JSONObject(fixture("compact-traceroute-report.json"));
        reachedUnreachable.getJSONObject("compact_topology").getJSONArray("routes").getJSONObject(0).put("status", "unreachable");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(reachedUnreachable.toString()));

        JSONObject unreachedHealthy = new JSONObject(fixture("compact-traceroute-report.json"));
        unreachedHealthy.getJSONObject("compact_topology").getJSONArray("routes").getJSONObject(0).put("reached", false);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(unreachedHealthy.toString()));
    }

    @Test public void compactGeoCountsAndBundlesMatchEligiblePublicNodes() throws Exception {
        JSONObject valid = new JSONObject(fixture("compact-traceroute-report.json"));
        JSONObject topology = valid.getJSONObject("compact_topology");
        JSONObject node = topology.getJSONArray("nodes").getJSONObject(0);
        node.put("public_ip", true).put("geolocation", new JSONObject()
                .put("city", "Seoul").put("region", "Seoul").put("country", "KR").put("country_code", "KR")
                .put("latitude", 37.5).put("longitude", 127.0));
        topology.put("geo", new JSONObject().put("eligible", 1).put("available", 1).put("included", 1).put("omitted", 0).put("unavailable", 0));
        assertEquals(1, ReportParser.parse(valid.toString()).compactTopology().orElseThrow().nodeCount());

        JSONObject wrongEligible = new JSONObject(valid.toString());
        wrongEligible.getJSONObject("compact_topology").getJSONObject("geo").put("eligible", 0);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(wrongEligible.toString()));

        JSONObject invalidBundle = new JSONObject(valid.toString());
        invalidBundle.getJSONObject("compact_topology").getJSONArray("nodes").getJSONObject(0)
                .getJSONObject("geolocation").put("latitude", 91);
        assertThrows(ReportParseException.class, () -> ReportParser.parse(invalidBundle.toString()));

        JSONObject nonPublicBundle = new JSONObject(valid.toString());
        nonPublicBundle.getJSONObject("compact_topology").getJSONArray("nodes").getJSONObject(0).put("public_ip", false);
        nonPublicBundle.getJSONObject("compact_topology").put("geo", new JSONObject().put("eligible", 0).put("available", 0).put("included", 0).put("omitted", 0).put("unavailable", 0));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(nonPublicBundle.toString()));

        JSONObject oversizedBundle = new JSONObject(valid.toString());
        oversizedBundle.getJSONObject("compact_topology").getJSONArray("nodes").getJSONObject(0)
                .getJSONObject("geolocation").put("city", "x".repeat(ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oversizedBundle.toString()));
    }

    @Test public void keepsCompactTopologyAsBoundedOpaqueDataAndSummaryOnly() {
        String compact = ",\"compact_topology\":{\"schema\":\"compact-v1\",\"selection\":\"fair-complete-prefix-v1\",\"limits\":{\"nodes\":500,\"links\":1000,\"max_response_bytes_exclusive\":1048576,\"max_geo_bundle_bytes\":4096},\"nodes\":[],\"links\":[],\"routes\":[],\"stats\":{\"nodes\":{\"total\":0,\"displayed\":0,\"omitted\":0},\"links\":{\"total\":0,\"displayed\":0,\"omitted\":0},\"routes\":{\"total\":0,\"displayed\":0,\"complete\":0,\"partial\":0,\"omitted\":0},\"node_observations\":{\"total\":0,\"displayed\":0,\"omitted\":0},\"link_observations\":{\"total\":0,\"displayed\":0,\"omitted\":0}},\"result_stats\":[],\"geo\":{\"eligible\":0,\"available\":0,\"included\":0,\"omitted\":0,\"unavailable\":0},\"truncated\":false}";
        Report parsed = ReportParser.parse(report("healthy", "{\"total\":0,\"passed\":0,\"failed\":0}", "[]", compact));
        Report.CompactTopology value = parsed.compactTopology().orElseThrow();
        assertEquals(0, value.nodeCount());
        assertEquals("compact-v1", value.schema());
        Map<String, Object> opaque = value.opaqueData();
        assertThrows(UnsupportedOperationException.class, () -> opaque.clear());
    }
}
