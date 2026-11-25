package diagnostic

import (
	"encoding/json"
	"testing"
	"time"
)

func contractV2FindingPresent(analysis Analysis, code FindingCode) bool {
	for _, finding := range analysis.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func contractV2CoverageIssue(analysis Analysis, code CoverageIssueCode) *CoverageIssue {
	for i, issue := range analysis.Coverage.Limitations {
		if issue.Code == code {
			return &analysis.Coverage.Limitations[i]
		}
	}
	for i, issue := range analysis.Coverage.ProviderFailures {
		if issue.Code == code {
			return &analysis.Coverage.ProviderFailures[i]
		}
	}
	return nil
}

func TestSlowServerResponseRuleFiresWhenDelayDominatesObservedPhases(t *testing.T) {
	analysis := Analyze([]Result{{
		Kind:   KindHTTP,
		Status: StatusHealthy,
		Details: map[string]any{
			ResultDetailStageTiming: map[string]int64{
				ResultDetailDNSPhaseMS:     120,
				ResultDetailConnectPhaseMS: 50,
				ResultDetailTLSPhaseMS:     300,
				ResultDetailTTFBPhaseMS:    1500,
			},
		},
	}}, time.Now().UTC())
	if !contractV2FindingPresent(analysis, FindingHTTPSlowServerResponse) {
		t.Fatalf("delay dominating the observed phases must fire http_slow_server_response, got %v", analysis.Findings)
	}
}

func TestSlowServerResponseRuleControlStaysQuietBelowThresholds(t *testing.T) {
	// ttfb is under the absolute floor, so a short observation cannot be
	// inflated into a slow-response finding by ratio arithmetic alone.
	analysis := Analyze([]Result{{
		Kind:   KindHTTP,
		Status: StatusHealthy,
		Details: map[string]any{
			ResultDetailStageTiming: map[string]int64{
				ResultDetailDNSPhaseMS:     10,
				ResultDetailConnectPhaseMS: 10,
				ResultDetailTLSPhaseMS:     10,
				ResultDetailTTFBPhaseMS:    60,
			},
		},
	}}, time.Now().UTC())
	if contractV2FindingPresent(analysis, FindingHTTPSlowServerResponse) {
		t.Fatalf("below-floor timings must not fire http_slow_server_response, got %v", analysis.Findings)
	}
}

func TestIntermittentConnectivityRuleFiresOnMixedSamples(t *testing.T) {
	analysis := Analyze([]Result{{
		Kind:   KindTCP,
		Status: StatusHealthy,
		Details: map[string]any{
			"remote_address": "203.0.113.7:80",
			ResultDetailSampleStats: map[string]any{
				"count": int64(3), "successes": int64(1), "failures": int64(2),
				"success_rate": int64(33), "p50_ms": int64(100), "p95_ms": int64(110),
				"jitter_ms": int64(10), "latency_distribution_available": true,
			},
		},
	}}, time.Now().UTC())
	if !contractV2FindingPresent(analysis, FindingIntermittentConnectivity) {
		t.Fatalf("mixed outcomes across three samples must fire intermittent_connectivity, got %v", analysis.Findings)
	}
}

func TestIntermittentConnectivityRuleControlsStayQuiet(t *testing.T) {
	// Too few samples: a pattern cannot be claimed from two observations. The block
	// must be contract-valid here, otherwise the control passes because the block is
	// refused as malformed instead of because the sample-count gate held.
	analysis := Analyze([]Result{{
		Kind:   KindTCP,
		Status: StatusHealthy,
		Details: map[string]any{
			"remote_address": "203.0.113.7:80",
			ResultDetailSampleStats: map[string]any{
				"count": int64(2), "successes": int64(1), "failures": int64(1),
				"success_rate": int64(50), "p50_ms": int64(100), "p95_ms": int64(110),
				"jitter_ms": int64(10), "latency_distribution_available": true,
			},
		},
	}}, time.Now().UTC())
	if contractV2CoverageIssue(analysis, CoverageMalformedDetails) != nil {
		t.Fatalf("the control block must be read as a whole valid aggregate, got %+v", analysis.Coverage.Limitations)
	}
	if contractV2FindingPresent(analysis, FindingIntermittentConnectivity) {
		t.Fatalf("two samples must not fire intermittent_connectivity, got %v", analysis.Findings)
	}
	// Single failure out of four samples is not an intermittent pattern.
	analysis = Analyze([]Result{{
		Kind:   KindTCP,
		Status: StatusHealthy,
		Details: map[string]any{
			"remote_address": "203.0.113.7:80",
			ResultDetailSampleStats: map[string]any{
				"count": int64(4), "successes": int64(3), "failures": int64(1),
				"success_rate": int64(75), "p50_ms": int64(100), "p95_ms": int64(110),
				"jitter_ms": int64(10), "latency_distribution_available": true,
			},
		},
	}}, time.Now().UTC())
	if contractV2FindingPresent(analysis, FindingIntermittentConnectivity) {
		t.Fatalf("one failure in four samples must not fire intermittent_connectivity, got %v", analysis.Findings)
	}
}

func TestLocalResolverDivergenceRuleFiresOnSetMismatch(t *testing.T) {
	analysis := Analyze([]Result{{
		Kind:   KindDNS,
		Status: StatusHealthy,
		Details: map[string]any{
			"addresses":    []any{"8.8.8.8"},
			"answer_count": int64(1),
			"resolver_comparison": map[string]any{
				"comparable": true, "match": false,
				"only_system":    []any{"8.8.8.8"},
				"only_reference": []any{"9.9.9.9"},
				"gap_side":       "",
			},
		},
	}}, time.Now().UTC())
	if !contractV2FindingPresent(analysis, FindingLocalResolverDivergence) {
		t.Fatalf("record-set mismatch must fire local_resolver_divergence, got %v", analysis.Findings)
	}
}

func TestLocalResolverDivergenceRuleControlsStayQuiet(t *testing.T) {
	// A matching comparison is an observation, not a finding.
	analysis := Analyze([]Result{{
		Kind:   KindDNS,
		Status: StatusHealthy,
		Details: map[string]any{
			"addresses":    []any{"1.1.1.1"},
			"answer_count": int64(1),
			ResultDetailResolverComparison: map[string]any{
				"comparable": true, "match": true,
				"only_system":    []any{},
				"only_reference": []any{},
				"gap_side":       "",
			},
		},
	}}, time.Now().UTC())
	if contractV2FindingPresent(analysis, FindingLocalResolverDivergence) {
		t.Fatalf("a matching comparison must not fire local_resolver_divergence, got %v", analysis.Findings)
	}

	// A failed side is reported as a coverage gap, never as divergence.
	analysis = Analyze([]Result{{
		Kind:   KindDNS,
		Status: StatusHealthy,
		Details: map[string]any{
			"addresses":    []any{"1.1.1.1"},
			"answer_count": int64(1),
			ResultDetailResolverComparison: map[string]any{
				"comparable": false, "match": false,
				"only_system":    []any{},
				"only_reference": []any{},
				"gap_side":       "reference",
			},
		},
	}}, time.Now().UTC())
	if contractV2FindingPresent(analysis, FindingLocalResolverDivergence) {
		t.Fatalf("a failed side must not be reported as divergence, got %v", analysis.Findings)
	}
	gap := contractV2CoverageIssue(analysis, CoverageMissingDetails)
	if gap == nil || gap.Signal != "resolver_comparison" {
		t.Fatalf("a failed comparison side must be recorded as a coverage gap on signal resolver_comparison, got %+v", analysis.Coverage)
	}
}

func TestGeoIPDisabledIsCoverageLimitationNotProviderFailure(t *testing.T) {
	analysis := Analyze([]Result{{
		Kind:   KindTraceroute,
		Status: StatusHealthy,
		Details: map[string]any{
			"attempts_total": int64(1), "attempts_reached": int64(1),
			"attempts_timed_out": int64(0), "attempts_unreached": int64(0),
			"geoip_enrichment": map[string]any{
				"provider":         "geoip",
				"source":           string(EnrichmentSourceDisabled),
				"cache_hits":       int64(0),
				"upstream_fetches": int64(0),
				"max_age_ms":       int64(0),
				"failures":         []any{},
			},
		},
	}}, time.Now().UTC())
	issue := contractV2CoverageIssue(analysis, CoverageConfigurationDisabled)
	if issue == nil {
		t.Fatalf("a disabled geolocation lookup must be recorded as a coverage limitation, got %+v", analysis.Coverage)
	}
	if len(analysis.Coverage.ProviderFailures) != 0 {
		t.Fatalf("a configuration skip is not a provider failure, got %v", analysis.Coverage.ProviderFailures)
	}
}

func TestSampleStatsBlockIsSerializedOnTheWire(t *testing.T) {
	// The aggregate must travel as a registered detail block so the three
	// clients can render it without reading unexported state.
	result := Result{Kind: KindTCP, Status: StatusHealthy, Details: map[string]any{}}
	stats := computeSampleStats([]sampleObservation{
		{Success: true, LatencyMS: 100},
		{Success: false},
		{Success: true, LatencyMS: 110},
	})
	result.Details[ResultDetailSampleStats] = stats.DetailValue()
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal wire result: %v", err)
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("decode wire result: %v", err)
	}
	block, ok := decoded["details"].(map[string]any)
	if !ok {
		t.Fatalf("sample_stats must be present as a wire detail block, got %s", wire)
	}
	entry, ok := block["sample_stats"].(map[string]any)
	if !ok || entry["count"] != float64(3) || entry["failures"] != float64(1) {
		t.Fatalf("sample_stats block must carry the registered fields, got %+v", block)
	}
}

// A computed aggregate carries every registered field, so a block missing one is
// recorded as coverage and never read as a partial claim. The phase block is the
// single exception: a phase that never happened has no duration the producer could
// have written, so a live plain-HTTP check legitimately carries only the phases it
// observed.
func TestComputedAggregateBlockMissingFieldIsCoverageNotPartialClaim(t *testing.T) {
	analysis := Analyze([]Result{{
		Kind:   KindTCP,
		Status: StatusHealthy,
		Details: map[string]any{
			"local_address":  "203.0.113.7:80",
			"remote_address": "203.0.113.7:80",
			ResultDetailSampleStats: map[string]any{
				"count": int64(3), "successes": int64(2), "failures": int64(1),
				"success_rate": int64(67), "p50_ms": int64(100), "p95_ms": int64(110), "jitter_ms": int64(10),
			},
		},
	}}, time.Now().UTC())
	issue := contractV2CoverageIssue(analysis, CoverageMalformedDetails)
	if issue == nil || issue.Signal != "sample_stats" {
		t.Fatalf("a computed aggregate missing a registered field must be a coverage limitation on sample_stats, got %+v", analysis.Coverage)
	}
	if contractV2FindingPresent(analysis, FindingIntermittentConnectivity) {
		t.Fatalf("a drifted aggregate block must not be read as a claim, got %v", analysis.Findings)
	}
	for _, path := range analysis.Coverage.Available {
		if path == "results[0].details.sample_stats" {
			t.Fatalf("a refused block cannot be reported as available coverage, got %v", analysis.Coverage.Available)
		}
	}
}

func TestObservedPhaseSubsetIsReadAsTheWholeObservedBlock(t *testing.T) {
	analysis := Analyze([]Result{{
		Kind:   KindHTTP,
		Status: StatusHealthy,
		Details: map[string]any{
			"status_code":     200,
			"expected_status": 200,
			ResultDetailStageTiming: map[string]int64{
				ResultDetailConnectPhaseMS: 50,
				ResultDetailTTFBPhaseMS:      1500,
			},
		},
	}}, time.Now().UTC())
	if !contractV2FindingPresent(analysis, FindingHTTPSlowServerResponse) {
		t.Fatalf("an observed phase subset with a dominant response phase must fire the rule, got %v", analysis.Findings)
	}
	found := false
	for _, path := range analysis.Coverage.Available {
		if path == "results[0].details.stage_timing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the observed phase block must be recorded as available coverage, got %v", analysis.Coverage.Available)
	}
	analysis = Analyze([]Result{{
		Kind:   KindHTTP,
		Status: StatusHealthy,
		Details: map[string]any{
			"status_code":     200,
			"expected_status": 200,
			ResultDetailStageTiming: map[string]int64{ResultDetailTTFBPhaseMS: 1500, "unregistered": 1},
		},
	}}, time.Now().UTC())
	issue := contractV2CoverageIssue(analysis, CoverageMalformedDetails)
	if issue == nil || issue.Signal != "stage_timing" {
		t.Fatalf("a key outside the registered phase set must be a coverage limitation on stage_timing, got %+v", analysis.Coverage)
	}
}

func contractV2LimitationForSignal(analysis Analysis, signal string) *CoverageIssue {
	for i, issue := range analysis.Coverage.Limitations {
		if issue.Signal == signal {
			return &analysis.Coverage.Limitations[i]
		}
	}
	return nil
}

func contractV2CoverageHas(analysis Analysis, path string) bool {
	for _, available := range analysis.Coverage.Available {
		if available == path {
			return true
		}
	}
	return false
}

// The response phase must be large on its own and clearly larger than everything
// before it. A control that clears only the absolute floor isolates the ratio half
// of the rule, so removing the ratio check would fail this test.
func TestSlowServerResponseRatioControlStaysQuietAboveTheFloor(t *testing.T) {
	analysis := Analyze([]Result{{
		Kind:   KindHTTP,
		Status: StatusHealthy,
		Details: map[string]any{
			"status_code":     200,
			"expected_status": 200,
			ResultDetailStageTiming: map[string]int64{
				ResultDetailConnectPhaseMS: 1000,
				ResultDetailTTFBPhaseMS:      2000,
			},
		},
	}}, time.Now().UTC())
	if contractV2FindingPresent(analysis, FindingHTTPSlowServerResponse) {
		t.Fatalf("a response phase under three times its preceding phases must stay quiet, got %v", analysis.Findings)
	}
	// The block was read as a whole observed block, so the quiet result comes from
	// the ratio and not from the block being refused.
	if !contractV2CoverageHas(analysis, "results[0].details.stage_timing") {
		t.Fatalf("the control must exercise the ratio gate on a block that was actually read, got %v", analysis.Coverage.Available)
	}
}

// The observer records stage timings for HTTPS too and the contract allows the
// block there, so a rule that only covers plain HTTP leaves the same symptom
// undiagnosed on the other kind. The rule scope must match the contract scope.
func TestSlowServerResponseRuleCoversHTTPSWhichTheContractAllows(t *testing.T) {
	analysis := Analyze([]Result{{
		Kind:   KindHTTPS,
		Status: StatusHealthy,
		Details: map[string]any{
			"status_code":     200,
			"expected_status": 200,
			ResultDetailStageTiming: map[string]int64{
				ResultDetailConnectPhaseMS: 20,
				ResultDetailTLSPhaseMS:     40,
				ResultDetailTTFBPhaseMS:    2000,
			},
		},
	}}, time.Now().UTC())
	if !contractV2FindingPresent(analysis, FindingHTTPSlowServerResponse) {
		t.Fatalf("the contract allows this block for https, so the rule must cover it, got %v", analysis.Findings)
	}
	if contractV2LimitationForSignal(analysis, ResultDetailStageTiming) != nil {
		t.Fatalf("an https block inside its scope must be read, not refused, got %+v", analysis.Coverage.Limitations)
	}
}

func TestClosedBlockOutsideItsKindScopeIsRefusedAndNeverRead(t *testing.T) {
	// A record comparison can only come from a lookup that completed, so a well
	// formed block from another kind's scope cannot become evidence or a finding.
	analysis := Analyze([]Result{{
		Kind:   KindHTTP,
		Status: StatusHealthy,
		Details: map[string]any{
			"status_code":     200,
			"expected_status": 200,
			ResultDetailResolverComparison: map[string]any{
				"comparable": true, "match": false,
				"only_system": []string{"8.8.8.8"}, "only_reference": []string{"9.9.9.9"}, "gap_side": "",
			},
		},
	}}, time.Now().UTC())
	issue := contractV2LimitationForSignal(analysis, ResultDetailResolverComparison)
	if issue == nil || issue.Code != CoverageUnsupportedDetails {
		t.Fatalf("an out-of-scope block must be named as itself, got %+v", analysis.Coverage.Limitations)
	}
	if contractV2FindingPresent(analysis, FindingLocalResolverDivergence) {
		t.Fatalf("an out-of-scope block must not become a finding, got %v", analysis.Findings)
	}
	if contractV2CoverageHas(analysis, "results[0].details.resolver_comparison") {
		t.Fatalf("a refused block must not be reported as available, got %v", analysis.Coverage.Available)
	}

	// Control: the identical block inside its own scope is read as evidence.
	analysis = Analyze([]Result{{
		Kind:   KindDNS,
		Status: StatusHealthy,
		Details: map[string]any{
			"addresses": []string{"203.0.113.7"}, "answer_count": 1,
			ResultDetailResolverComparison: map[string]any{
				"comparable": true, "match": false,
				"only_system": []string{"8.8.8.8"}, "only_reference": []string{"9.9.9.9"}, "gap_side": "",
			},
		},
	}}, time.Now().UTC())
	if !contractV2FindingPresent(analysis, FindingLocalResolverDivergence) {
		t.Fatalf("the same block inside its own scope must still fire, got %v", analysis.Findings)
	}

	// The sampling aggregate is inside every non-traceroute kind's scope, so a
	// traceroute result carrying one is refused rather than read.
	analysis = Analyze([]Result{{
		Kind:   KindTraceroute,
		Status: StatusHealthy,
		Details: map[string]any{
			"attempts_total": int64(3), "attempts_reached": int64(3), "attempts_failed": int64(0),
			"attempts_unreached": int64(0), "attempts_execution_failed": int64(0),
			"attempts_timed_out": int64(0), "attempts_cancelled": int64(0),
			ResultDetailSampleStats: map[string]any{
				"count": int64(3), "successes": int64(3), "failures": int64(0), "success_rate": int64(100),
				"p50_ms": int64(10), "p95_ms": int64(20), "jitter_ms": int64(10), "latency_distribution_available": true,
			},
		},
	}}, time.Now().UTC())
	if contractV2LimitationForSignal(analysis, ResultDetailSampleStats) == nil {
		t.Fatalf("a traceroute aggregate must be refused as out of scope, got %+v", analysis.Coverage)
	}
	if contractV2CoverageHas(analysis, "results[0].details.sample_stats") {
		t.Fatalf("a refused block must not be reported as available, got %v", analysis.Coverage.Available)
	}

	// A counters disagreement must not swallow the scope refusal: the block that
	// this kind could never have written is still named as itself, alongside the
	// counters limitation, and is still never read.
	analysis = Analyze([]Result{{
		Kind:   KindTraceroute,
		Status: StatusHealthy,
		Details: map[string]any{
			"attempts_total": int64(7), "attempts_reached": int64(5), "attempts_failed": int64(2),
			"attempts_unreached": int64(0), "attempts_execution_failed": int64(0),
			"attempts_timed_out": int64(0), "attempts_cancelled": int64(0), "success_rate": int64(60),
			ResultDetailSampleStats: map[string]any{
				"count": int64(7), "successes": int64(5), "failures": int64(2), "success_rate": int64(71),
				"p50_ms": int64(10), "p95_ms": int64(20), "jitter_ms": int64(10), "latency_distribution_available": true,
			},
		},
	}}, time.Now().UTC())
	if contractV2LimitationForSignal(analysis, "trace_attempts") == nil {
		t.Fatalf("the counters disagreement must still be recorded, got %+v", analysis.Coverage)
	}
	blockLimitation := contractV2LimitationForSignal(analysis, ResultDetailSampleStats)
	if blockLimitation == nil || blockLimitation.Reason != "closed block is outside the scope this kind and outcome could produce" {
		t.Fatalf("the counters limitation must not absorb the scope refusal, got %+v", analysis.Coverage.Limitations)
	}
	if contractV2FindingPresent(analysis, FindingIntermittentConnectivity) {
		t.Fatalf("a refused block must not feed a finding, got %v", analysis.Findings)
	}
}

// A shape lists the union of blocks its kinds may carry, so one row can name a
// block that only another kind in the same shape could have written. The per-kind
// projection is what a single result may hold, and it must refuse the union here.
func TestShapeBlockUnionDoesNotLicenseEveryKindInIt(t *testing.T) {
	contract := ProducerFindingContract()
	var cancelled *ResultShapeContract
	for i, shape := range contract.ResultShapes {
		if shape.ErrorCode == "cancelled" {
			cancelled = &contract.ResultShapes[i]
			break
		}
	}
	if cancelled == nil {
		t.Fatalf("the cancelled shape is missing from the producer contract")
	}
	if !ContainsKind(cancelled.Kinds, KindTraceroute) {
		t.Fatalf("the union pin needs a shape that spans traceroute and another kind, got %v", cancelled.Kinds)
	}
	unionHasAggregate := false
	for _, detail := range cancelled.OptionalDetails {
		if detail.Key == ResultDetailSampleStats {
			unionHasAggregate = true
		}
	}
	if !unionHasAggregate {
		t.Fatalf("the fixture must list the block union over its kinds, got %v", cancelled.OptionalDetails)
	}
	if closedBlockLegalFor(KindTraceroute, cancelled.Status, cancelled.ErrorCode, ResultDetailSampleStats) {
		t.Fatalf("traceroute reports attempts, so it cannot carry the aggregate the union lists")
	}
	if !closedBlockLegalFor(KindTCP, cancelled.Status, cancelled.ErrorCode, ResultDetailSampleStats) {
		t.Fatalf("a kind that can repeat an observation must keep its own block, got %v", cancelled.Kinds)
	}
}
