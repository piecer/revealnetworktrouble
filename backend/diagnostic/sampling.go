package diagnostic

import (
	"math"
	"sort"
)

// sampleObservation is one execution of one checker. Success covers every
// completed observation regardless of which error code it carried, so an
// intermittently failing target still shows its latency distribution.
type sampleObservation struct {
	Success   bool
	LatencyMS int64
}

// SampleStats reports integer-only values: percentiles use nearest-rank and the
// success rate rounds to the nearest integer percent, so the report never
// contains a rounded double that could disagree across consumers.
type SampleStats struct {
	Count       int   `json:"count"`
	Successes   int   `json:"successes"`
	Failures    int   `json:"failures"`
	SuccessRate int   `json:"success_rate"`
	P50MS       int64 `json:"p50_ms"`
	P95MS       int64 `json:"p95_ms"`
	JitterMS    int64 `json:"jitter_ms"`
	// LatencyDistributionAvailable is false when no sample succeeded: a zero
	// percentile would read as "fast" rather than "no successful observation".
	LatencyDistributionAvailable bool `json:"latency_distribution_available"`
}

func computeSampleStats(observations []sampleObservation) SampleStats {
	stats := SampleStats{Count: len(observations)}
	var successes []int64
	for _, obs := range observations {
		if obs.Success {
			stats.Successes++
			successes = append(successes, obs.LatencyMS)
		} else {
			stats.Failures++
		}
	}
	if stats.Count > 0 {
		stats.SuccessRate = int(math.Round(float64(stats.Successes) * 100 / float64(stats.Count)))
	}
	if len(successes) == 0 {
		return stats
	}
	sort.Slice(successes, func(i, j int) bool { return successes[i] < successes[j] })
	stats.LatencyDistributionAvailable = true
	p50 := nearestRank(successes, 0.5)
	p95 := nearestRank(successes, 0.95)
	stats.P50MS = p50
	stats.P95MS = p95
	stats.JitterMS = p95 - p50
	return stats
}

// DetailValue renders the aggregate as the registered closed detail block, so
// the wire report, the web client, and the Android client read the same keys
// instead of an unexported field. Numbers are the wire-canonical JSON number,
// so an in-process block and a decoded wire block carry the same type.
func (s SampleStats) DetailValue() map[string]any {
	return map[string]any{
		"count":                          float64(s.Count),
		"successes":                      float64(s.Successes),
		"failures":                       float64(s.Failures),
		"success_rate":                   float64(s.SuccessRate),
		"p50_ms":                         float64(s.P50MS),
		"p95_ms":                         float64(s.P95MS),
		"jitter_ms":                      float64(s.JitterMS),
		"latency_distribution_available": s.LatencyDistributionAvailable,
	}
}

// nearestRank is the ceiling-rank percentile over a sorted ascending slice.
func nearestRank(sorted []int64, quantile float64) int64 {
	rank := int(math.Ceil(quantile * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}

// An observation succeeds when its checker reported no error code; status
// wording (healthy vs degraded) is analysis's judgment, not the observation's.
func observationFromResult(result Result) sampleObservation {
	return sampleObservation{
		Success:   result.ErrorCode == "",
		LatencyMS: result.LatencyMS,
	}
}
