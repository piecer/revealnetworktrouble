package com.checknetwork.app.core;

import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/** Immutable, presentation-neutral report contract. */
public final class Report {
    public enum Status { HEALTHY, DEGRADED, UNREACHABLE }
    public enum Verdict { HEALTHY, ATTENTION, INCONCLUSIVE }
    public enum FindingCode {
        CHECKER_CAPACITY_UNAVAILABLE,
        CHECKER_PANIC,
        DNS_RESOLUTION_FAILED,
        ENDPOINT_CONNECT_FAILED,
        EXECUTION_CANCELLED,
        EXECUTION_TIMEOUT,
        HTTP_UNEXPECTED_STATUS,
        INVALID_TARGET,
        SERVICE_GREETING_UNVERIFIED,
        TARGET_POLICY_BLOCKED,
        TLS_CERTIFICATE_EXPIRED,
        TLS_CERTIFICATE_EXPIRING,
        TLS_CERTIFICATE_NOT_YET_VALID,
        TLS_DOWNGRADE,
        TLS_HANDSHAKE_FAILED,
        TLS_HOSTNAME_MISMATCH,
        TLS_UNTRUSTED,
        TRACEROUTE_EXECUTION_FAILED,
        TRACEROUTE_PARTIAL_REACHABILITY,
        TRACEROUTE_PATH_DEGRADED,
        TRACEROUTE_PATH_UNSTABLE,
        TRACEROUTE_UNAVAILABLE,
        TRACEROUTE_UNREACHABLE;

        static FindingCode fromWire(String value) {
            return switch (value) {
                case "checker_capacity_unavailable" -> CHECKER_CAPACITY_UNAVAILABLE;
                case "checker_panic" -> CHECKER_PANIC;
                case "dns_resolution_failed" -> DNS_RESOLUTION_FAILED;
                case "endpoint_connect_failed" -> ENDPOINT_CONNECT_FAILED;
                case "execution_cancelled" -> EXECUTION_CANCELLED;
                case "execution_timeout" -> EXECUTION_TIMEOUT;
                case "http_unexpected_status" -> HTTP_UNEXPECTED_STATUS;
                case "invalid_target" -> INVALID_TARGET;
                case "service_greeting_unverified" -> SERVICE_GREETING_UNVERIFIED;
                case "target_policy_blocked" -> TARGET_POLICY_BLOCKED;
                case "tls_certificate_expired" -> TLS_CERTIFICATE_EXPIRED;
                case "tls_certificate_expiring" -> TLS_CERTIFICATE_EXPIRING;
                case "tls_certificate_not_yet_valid" -> TLS_CERTIFICATE_NOT_YET_VALID;
                case "tls_downgrade" -> TLS_DOWNGRADE;
                case "tls_handshake_failed" -> TLS_HANDSHAKE_FAILED;
                case "tls_hostname_mismatch" -> TLS_HOSTNAME_MISMATCH;
                case "tls_untrusted" -> TLS_UNTRUSTED;
                case "traceroute_execution_failed" -> TRACEROUTE_EXECUTION_FAILED;
                case "traceroute_partial_reachability" -> TRACEROUTE_PARTIAL_REACHABILITY;
                case "traceroute_path_degraded" -> TRACEROUTE_PATH_DEGRADED;
                case "traceroute_path_unstable" -> TRACEROUTE_PATH_UNSTABLE;
                case "traceroute_unavailable" -> TRACEROUTE_UNAVAILABLE;
                case "traceroute_unreachable" -> TRACEROUTE_UNREACHABLE;
                default -> throw new IllegalArgumentException("unsupported finding code");
            };
        }
    }
    public enum Severity { CRITICAL, WARNING, INFO }
    public enum Category { NAME_RESOLUTION, CONNECTIVITY, APPLICATION, SECURITY, ROUTING, EXECUTION, INPUT }
    public enum Confidence { DIRECT, CORROBORATED, LIMITED }
    public enum Provenance { RESULT, DETAILS }
    public enum CoverageCode { MISSING_DETAILS, MALFORMED_DETAILS, UNSUPPORTED_DETAILS }
    public enum EnrichmentSource { UPSTREAM, CACHE, MIXED, NONE }
    public enum EnrichmentFailureKind { BUSY, CANCELLED, MALFORMED, NOT_FOUND, POLICY, RATE_LIMITED, TIMEOUT, UNAVAILABLE }

    private final String id;
    private final Status status;
    private final Instant startedAt;
    private final long durationMs;
    private final Summary summary;
    private final List<Result> results;
    private final Analysis analysis;
    private final CompactTopology compactTopology;

    Report(String id, Status status, Instant startedAt, long durationMs, Summary summary,
           List<Result> results, Analysis analysis, CompactTopology compactTopology) {
        this.id = id; this.status = status; this.startedAt = startedAt; this.durationMs = durationMs;
        this.summary = summary; this.results = Collections.unmodifiableList(results);
        this.analysis = analysis; this.compactTopology = compactTopology;
    }
    public String id() { return id; }
    public Status status() { return status; }
    public Instant startedAt() { return startedAt; }
    public long durationMs() { return durationMs; }
    public Summary summary() { return summary; }
    public List<Result> results() { return results; }
    public Optional<Analysis> analysis() { return Optional.ofNullable(analysis); }
    public Optional<CompactTopology> compactTopology() { return Optional.ofNullable(compactTopology); }

    public static final class Summary {
        private final int total, passed, failed;
        Summary(int total, int passed, int failed) { this.total = total; this.passed = passed; this.failed = failed; }
        public int total() { return total; } public int passed() { return passed; } public int failed() { return failed; }
    }

    public static final class Result {
        private final CheckKind kind; private final String address; private final Status status;
        private final long latencyMs; private final Instant startedAt; private final String errorCode, message;
        private final Map<String, Object> details;
        Result(CheckKind kind, String address, Status status, long latencyMs, Instant startedAt,
               String errorCode, String message, Map<String, Object> details) {
            this.kind = kind; this.address = address; this.status = status; this.latencyMs = latencyMs;
            this.startedAt = startedAt; this.errorCode = errorCode; this.message = message; this.details = details;
        }
        public CheckKind kind() { return kind; } public String address() { return address; }
        public Status status() { return status; } public long latencyMs() { return latencyMs; }
        public Instant startedAt() { return startedAt; } public String errorCode() { return errorCode; }
        public String message() { return message; } public Map<String, Object> details() { return details; }
    }

    public static final class Analysis {
        private final Verdict verdict; private final List<Finding> findings; private final List<Evidence> evidence;
        private final List<Action> actions; private final Coverage coverage;
        Analysis(Verdict verdict, List<Finding> findings, List<Evidence> evidence, List<Action> actions, Coverage coverage) {
            this.verdict = verdict; this.findings = Collections.unmodifiableList(findings);
            this.evidence = Collections.unmodifiableList(evidence); this.actions = Collections.unmodifiableList(actions); this.coverage = coverage;
        }
        public Verdict verdict() { return verdict; } public List<Finding> findings() { return findings; }
        public List<Evidence> evidence() { return evidence; } public List<Action> actions() { return actions; }
        public Coverage coverage() { return coverage; }
    }

    public static final class Finding {
        private final String id, title, summary; private final FindingCode code; private final Severity severity;
        private final Category category; private final Confidence confidence; private final List<String> evidenceIds, actionIds;
        Finding(String id, FindingCode code, Severity severity, Category category, String title, String summary,
                Confidence confidence, List<String> evidenceIds, List<String> actionIds) {
            this.id=id; this.code=code; this.severity=severity; this.category=category; this.title=title;
            this.summary=summary; this.confidence=confidence; this.evidenceIds=Collections.unmodifiableList(evidenceIds); this.actionIds=Collections.unmodifiableList(actionIds);
        }
        public String id(){return id;} public FindingCode code(){return code;} public Severity severity(){return severity;}
        public Category category(){return category;} public String title(){return title;} public String summary(){return summary;}
        public Confidence confidence(){return confidence;} public List<String> evidenceIds(){return evidenceIds;} public List<String> actionIds(){return actionIds;}
    }

    public static final class Evidence {
        private final String id,address,signal,observed,expected; private final int resultIndex; private final Integer attempt;
        private final CheckKind kind; private final Provenance provenance;
        Evidence(String id,int resultIndex,CheckKind kind,Integer attempt,String address,String signal,String observed,String expected,Provenance provenance){
            this.id=id;this.resultIndex=resultIndex;this.kind=kind;this.attempt=attempt;this.address=address;this.signal=signal;this.observed=observed;this.expected=expected;this.provenance=provenance;
        }
        public String id(){return id;} public int resultIndex(){return resultIndex;} public CheckKind kind(){return kind;}
        public Optional<Integer> attempt(){return Optional.ofNullable(attempt);} public String address(){return address;}
        public String signal(){return signal;} public String observed(){return observed;} public String expected(){return expected;}
        public Provenance provenance(){return provenance;}
    }

    public static final class Action {
        private final String id,title,step,expectedResult,escalationCondition;
        Action(String id,String title,String step,String expectedResult,String escalationCondition){this.id=id;this.title=title;this.step=step;this.expectedResult=expectedResult;this.escalationCondition=escalationCondition;}
        public String id(){return id;} public String title(){return title;} public String step(){return step;}
        public String expectedResult(){return expectedResult;} public String escalationCondition(){return escalationCondition;}
    }

    public static final class CoverageIssue {
        private final CoverageCode code; private final int resultIndex; private final CheckKind kind; private final String signal,reason;
        CoverageIssue(CoverageCode code,int resultIndex,CheckKind kind,String signal,String reason){this.code=code;this.resultIndex=resultIndex;this.kind=kind;this.signal=signal;this.reason=reason;}
        public CoverageCode code(){return code;} public int resultIndex(){return resultIndex;} public CheckKind kind(){return kind;}
        public String signal(){return signal;} public String reason(){return reason;}
    }

    public static final class EnrichmentFailure {
        private final EnrichmentFailureKind kind;
        private final int count;
        private final boolean retryable;
        EnrichmentFailure(EnrichmentFailureKind kind,int count,boolean retryable){
            this.kind=kind;this.count=count;this.retryable=retryable;
        }
        public EnrichmentFailureKind kind(){return kind;}
        public int count(){return count;}
        public boolean retryable(){return retryable;}
    }

    public static final class EnrichmentCoverage {
        private final String provider;
        private final EnrichmentSource source;
        private final int cacheHits,upstreamFetches;
        private final long maxAgeMs;
        private final List<EnrichmentFailure> failures;
        EnrichmentCoverage(String provider,EnrichmentSource source,int cacheHits,int upstreamFetches,long maxAgeMs,List<EnrichmentFailure> failures){
            this.provider=provider;this.source=source;this.cacheHits=cacheHits;this.upstreamFetches=upstreamFetches;this.maxAgeMs=maxAgeMs;
            this.failures=Collections.unmodifiableList(new ArrayList<>(failures));
        }
        public String provider(){return provider;}
        public EnrichmentSource source(){return source;}
        public int cacheHits(){return cacheHits;}
        public int upstreamFetches(){return upstreamFetches;}
        public long maxAgeMs(){return maxAgeMs;}
        public List<EnrichmentFailure> failures(){return failures;}
    }

    public static final class Coverage {
        private final List<String> available,missing; private final List<CoverageIssue> providerFailures,limitations;
        private final List<EnrichmentCoverage> enrichment;
        Coverage(List<String> available,List<String> missing,List<CoverageIssue> providerFailures,List<CoverageIssue> limitations,List<EnrichmentCoverage> enrichment){
            this.available=Collections.unmodifiableList(available);this.missing=Collections.unmodifiableList(missing);
            this.providerFailures=Collections.unmodifiableList(providerFailures);this.limitations=Collections.unmodifiableList(limitations);
            this.enrichment=Collections.unmodifiableList(new ArrayList<>(enrichment));
        }
        public List<String> available(){return available;} public List<String> missing(){return missing;}
        public List<CoverageIssue> providerFailures(){return providerFailures;} public List<CoverageIssue> limitations(){return limitations;}
        public List<EnrichmentCoverage> enrichment(){return enrichment;}
    }

    /** No graph API is exposed; callers receive only summary values and an immutable opaque copy. */
    public static final class CompactTopology {
        private final String schema, selection; private final int nodeCount, linkCount, routeCount; private final boolean truncated;
        private final Map<String,Object> opaqueData;
        CompactTopology(String schema,String selection,int nodeCount,int linkCount,int routeCount,boolean truncated,Map<String,Object> opaqueData){
            this.schema=schema;this.selection=selection;this.nodeCount=nodeCount;this.linkCount=linkCount;this.routeCount=routeCount;this.truncated=truncated;this.opaqueData=opaqueData;
        }
        public String schema(){return schema;} public String selection(){return selection;} public int nodeCount(){return nodeCount;}
        public int linkCount(){return linkCount;} public int routeCount(){return routeCount;} public boolean truncated(){return truncated;}
        public Map<String,Object> opaqueData(){return opaqueData;}
    }
}
