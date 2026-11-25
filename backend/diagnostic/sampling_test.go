package diagnostic

import (
	"context"
	"strings"
	"testing"
)

// Repeat sampling is additive: an omitted or zero samples value preserves the
// legacy single-observation behavior, so existing wire fixtures are untouched.
// The bound is its own constant because MaxTraceAttempts governs the traceroute
// execution path, not a general sampling budget.

func TestValidateSamplesBounds(t *testing.T) {
	cases := []struct {
		name    string
		target  Target
		wantErr string
	}{
		{"omitted keeps legacy single observation", Target{Kind: KindTCP, Address: "example.test:443"}, ""},
		{"explicit zero is legacy behavior", Target{Kind: KindTCP, Address: "example.test:443", Samples: 0}, ""},
		{"explicit one is allowed", Target{Kind: KindTCP, Address: "example.test:443", Samples: 1}, ""},
		{"explicit bound is allowed", Target{Kind: KindTCP, Address: "example.test:443", Samples: 10}, ""},
		{"above bound is rejected", Target{Kind: KindTCP, Address: "example.test:443", Samples: 11}, "samples must be between 1 and 10"},
		{"negative is rejected", Target{Kind: KindTCP, Address: "example.test:443", Samples: -1}, "samples must be between 1 and 10"},
		{"traceroute keeps attempts as its sampling field", Target{Kind: KindTraceroute, Address: "example.test", Samples: 2}, "samples is only supported for non-traceroute kinds"},
	}

	runner := NewRunner(
		fakeChecker{kind: KindTCP, status: StatusHealthy},
		fakeChecker{kind: KindTraceroute, status: StatusHealthy},
	)
	for _, tc := range cases {
		err := runner.Validate(Request{Targets: []Target{tc.target}})
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: error=%v, want containing %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestSampleStatsUsesNearestRankPercentiles(t *testing.T) {
	observations := []sampleObservation{
		{Success: true, LatencyMS: 100},
		{Success: true, LatencyMS: 120},
		{Success: false, LatencyMS: 500},
		{Success: true, LatencyMS: 105},
		{Success: true, LatencyMS: 110},
		{Success: true, LatencyMS: 115},
	}

	stats := computeSampleStats(observations)
	if stats.Count != 6 || stats.Successes != 5 || stats.Failures != 1 {
		t.Fatalf("outcome counts do not match: %+v", stats)
	}
	if stats.SuccessRate != 83 {
		t.Fatalf("success rate must round to nearest integer percent: %+v", stats)
	}
	if stats.P50MS != 110 || stats.P95MS != 120 || stats.JitterMS != 10 {
		t.Fatalf("nearest-rank distribution must exclude failed samples: %+v", stats)
	}
}

func TestSampleStatsSingleSampleHasZeroJitter(t *testing.T) {
	stats := computeSampleStats([]sampleObservation{{Success: true, LatencyMS: 42}})
	if stats.P50MS != 42 || stats.P95MS != 42 || stats.JitterMS != 0 {
		t.Fatalf("single sample distribution must collapse to the observation: %+v", stats)
	}
	if stats.SuccessRate != 100 {
		t.Fatalf("single success must report 100: %+v", stats)
	}
}

// A distribution cannot be computed from zero successes; reporting zero would
// read as "fast" rather than "no successful observation".
func TestSampleStatsDistributionUnavailableWithoutSuccesses(t *testing.T) {
	stats := computeSampleStats([]sampleObservation{
		{Success: false, LatencyMS: 30},
		{Success: false, LatencyMS: 40},
	})
	if stats.LatencyDistributionAvailable {
		t.Fatalf("distribution must be unavailable without successes: %+v", stats)
	}
	if stats.SuccessRate != 0 || stats.Count != 2 || stats.Successes != 0 || stats.Failures != 2 {
		t.Fatalf("outcome counts must still be reported: %+v", stats)
	}
}

// The representative result is the last observation (current state); the
// aggregate block tells the distribution story separately.
func TestSamplingExecutesRequestedObservationsAndAggregates(t *testing.T) {
	scripted := []Result{
		{Kind: KindTCP, Status: StatusHealthy, LatencyMS: 100},
		{Kind: KindTCP, Status: StatusUnreachable, LatencyMS: 200, ErrorCode: "connection_failed", Message: "connection failed"},
		{Kind: KindTCP, Status: StatusHealthy, LatencyMS: 110},
	}
	calls := 0
	fake := checkerFunc{kind: KindTCP, fn: func(ctx context.Context, target Target) Result {
		result := scripted[calls]
		calls++
		return result
	}}
	runner := NewRunner(fake)

	report, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindTCP, Address: "example.test:443", Samples: 3}}})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if calls != 3 {
		t.Fatalf("checker must run once per sample, got %d calls", calls)
	}
	result := report.Results[0]
	block, ok := result.Details[ResultDetailSampleStats].(map[string]any)
	if !ok {
		t.Fatalf("sampled result must carry the aggregate as a wire detail block, got %+v", result.Details)
	}
	if block["count"] != 3.0 || block["successes"] != 2.0 || block["failures"] != 1.0 || block["success_rate"] != 67.0 {
		t.Fatalf("aggregate mismatch: %+v", block)
	}
	if block["p50_ms"] != 100.0 || block["p95_ms"] != 110.0 || block["jitter_ms"] != 10.0 {
		t.Fatalf("nearest-rank distribution over successes [100,110] must be p50=100 p95=110 jitter=10: %+v", block)
	}
	if result.LatencyMS != 110 || result.ErrorCode != "" {
		t.Fatalf("representative must be the last observation: %+v", result)
	}
}

// Single observation stays byte-identical to the legacy path: no aggregate is
// attached when sampling was not requested.
func TestSamplingWithoutSamplesKeepsLegacySingleResult(t *testing.T) {
	fake := checkerFunc{kind: KindTCP, fn: func(ctx context.Context, target Target) Result {
		return Result{Kind: KindTCP, Status: StatusHealthy, LatencyMS: 100}
	}}
	runner := NewRunner(fake)

	report, err := runner.Run(context.Background(), Request{Targets: []Target{{Kind: KindTCP, Address: "example.test:443"}}})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if _, exists := report.Results[0].Details[ResultDetailSampleStats]; exists {
		t.Fatalf("legacy single observation must not carry aggregate stats")
	}
}
