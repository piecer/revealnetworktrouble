package com.checknetwork.app.core;

import static org.junit.Assert.*;

import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.Map;
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
        Report parsed = ReportParser.parse(report("unreachable", "{\"total\":1,\"passed\":0,\"failed\":1}", "[" + result("https", "unreachable") + "]", analysis));
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

    @Test public void detailsDepthIsBoundedRelativeToDetails() throws Exception {
        JSONObject accepted = resultObject("dns", "healthy").put("details", new JSONObject().put(
                "nested", new JSONArray("[".repeat(ContractLimits.MAX_DETAIL_DEPTH) + "0"
                        + "]".repeat(ContractLimits.MAX_DETAIL_DEPTH))));
        assertEquals(CheckKind.DNS, ReportParser.parse(oneResultReport(accepted).toString()).results().get(0).kind());

        JSONObject rejected = resultObject("dns", "healthy").put("details", new JSONObject().put(
                "nested", new JSONArray("[".repeat(ContractLimits.MAX_DETAIL_DEPTH + 1) + "0"
                        + "]".repeat(ContractLimits.MAX_DETAIL_DEPTH + 1))));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(oneResultReport(rejected).toString()));
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
        badNumber.getJSONArray("attempts").getJSONObject(0).put("attempt", 2);
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
        boolean healthy="healthy".equals(value.getString("status"));
        return new JSONObject(report(healthy?"healthy":"unreachable",
                healthy?"{\"total\":1,\"passed\":1,\"failed\":0}":"{\"total\":1,\"passed\":0,\"failed\":1}",
                new JSONArray().put(value).toString(),""));
    }

    @Test public void rejectsDuplicateAnalysisIdsUnknownReferencesAndResultReferences() {
        String base = ",\"analysis\":{\"verdict\":\"attention\",\"findings\":[{\"id\":\"f\",\"code\":\"tls_downgrade\",\"severity\":\"warning\",\"category\":\"security\",\"title\":\"t\",\"summary\":\"s\",\"confidence\":\"direct\",\"evidence_ids\":[\"%s\"],\"action_ids\":[]}],\"evidence\":[{\"id\":\"e\",\"result_index\":%d,\"kind\":\"https\",\"address\":\"a\",\"signal\":\"s\",\"observed\":\"o\",\"provenance\":\"result\"}],\"actions\":[],\"coverage\":{\"available\":[],\"missing\":[],\"provider_failures\":[],\"limitations\":[]}}";
        String prefixResult = "[" + result("https", "unreachable") + "]";
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("unreachable", "{\"total\":1,\"passed\":0,\"failed\":1}", prefixResult, String.format(base, "missing", 0))));
        assertThrows(ReportParseException.class, () -> ReportParser.parse(report("unreachable", "{\"total\":1,\"passed\":0,\"failed\":1}", prefixResult, String.format(base, "e", 1))));
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

    @Test public void acceptsGoNilCompactCollectionsAndOmittedFalseTruncationReasons() throws Exception {
        Report parsed = ReportParser.parse(fixture("go-zero-route-report.json"));
        Report.CompactTopology compact = parsed.compactTopology().orElseThrow();
        assertEquals(0, compact.nodeCount());
        assertEquals(0, compact.linkCount());
        assertEquals(0, compact.routeCount());
        for (String key : new String[]{"nodes", "links", "routes"}) {
            assertEquals(java.util.List.of(), compact.opaqueData().get(key));
        }
    }

    @Test public void compactCollectionNormalizationRejectsOtherWrongTypes() throws Exception {
        for (String key : new String[]{"nodes", "links", "routes"}) {
            JSONObject wrong = new JSONObject(fixture("go-zero-route-report.json"));
            wrong.getJSONObject("compact_topology").put(key, new JSONObject());
            assertThrows(key, ReportParseException.class, () -> ReportParser.parse(wrong.toString()));
        }
        JSONObject wrongReasons = new JSONObject(fixture("go-zero-route-report.json"));
        wrongReasons.getJSONObject("compact_topology").put("truncation_reasons", "none");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(wrongReasons.toString()));
    }

    @Test public void compactResultStatsAreMandatoryForZeroAndOneResults() throws Exception {
        JSONObject one = new JSONObject(fixture("go-zero-route-report.json"));
        one.getJSONObject("compact_topology").remove("result_stats");
        assertThrows(ReportParseException.class, () -> ReportParser.parse(one.toString()));

        JSONObject zero = new JSONObject(fixture("go-zero-route-report.json"));
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
        String compact = ",\"compact_topology\":{\"schema\":\"compact-v1\",\"selection\":\"fair-complete-prefix-v1\",\"limits\":{\"nodes\":500,\"links\":1000,\"max_response_bytes_exclusive\":1048576,\"max_geo_bundle_bytes\":4096},\"nodes\":[],\"links\":[],\"routes\":[],\"stats\":{\"nodes\":{\"total\":0,\"displayed\":0,\"omitted\":0},\"links\":{\"total\":0,\"displayed\":0,\"omitted\":0},\"routes\":{\"total\":0,\"displayed\":0,\"complete\":0,\"partial\":0,\"omitted\":0},\"node_observations\":{\"total\":0,\"displayed\":0,\"omitted\":0},\"link_observations\":{\"total\":0,\"displayed\":0,\"omitted\":0}},\"result_stats\":[],\"geo\":{\"eligible\":0,\"available\":0,\"included\":0,\"omitted\":0,\"unavailable\":0},\"truncated\":false,\"truncation_reasons\":[]}";
        Report parsed = ReportParser.parse(report("healthy", "{\"total\":0,\"passed\":0,\"failed\":0}", "[]", compact));
        Report.CompactTopology value = parsed.compactTopology().orElseThrow();
        assertEquals(0, value.nodeCount());
        assertEquals("compact-v1", value.schema());
        Map<String, Object> opaque = value.opaqueData();
        assertThrows(UnsupportedOperationException.class, () -> opaque.clear());
    }
}
