package diagnostic

import (
	"context"
	"fmt"
	"sort"
)

// The whitelist covers record types whose answers are comparable as plain
// value sets. Types whose content is order- or TTL-sensitive (SOA, DNSSEC)
// are excluded from comparison by design.
var comparableRecordTypes = map[string]bool{
	"A":     true,
	"AAAA":  true,
	"CNAME": true,
	"NS":    true,
	"MX":    true,
	"TXT":   true,
}

// RecordResolver is the seam for per-type record lookups; deterministic tests
// inject fixed answer sets instead of touching the network.
type RecordResolver interface {
	LookupType(ctx context.Context, host string, recordType string) ([]string, error)
}

// recordComparison reports either a verdict (Comparable) or a coverage gap
// (GapSide names the failed side). A failed lookup is never reported as
// divergence.
type recordComparison struct {
	Comparable    bool
	Match         bool
	OnlySystem    []string
	OnlyReference []string
	GapSide       string
}

func compareRecordResolutions(system, reference []string) recordComparison {
	setSystem := dedupeSorted(system)
	setReference := dedupeSorted(reference)
	result := recordComparison{Comparable: true, Match: true}
	for _, value := range setSystem {
		if !contains(setReference, value) {
			result.Match = false
			result.OnlySystem = append(result.OnlySystem, value)
		}
	}
	for _, value := range setReference {
		if !contains(setSystem, value) {
			result.Match = false
			result.OnlyReference = append(result.OnlyReference, value)
		}
	}
	return result
}

func resolveAndCompare(ctx context.Context, system, reference RecordResolver, host, recordType string) (recordComparison, error) {
	if !comparableRecordTypes[recordType] {
		return recordComparison{}, fmt.Errorf("record type %q is not comparable", recordType)
	}
	systemAnswers, err := system.LookupType(ctx, host, recordType)
	if err != nil {
		return recordComparison{GapSide: "system"}, nil
	}
	referenceAnswers, err := reference.LookupType(ctx, host, recordType)
	if err != nil {
		return recordComparison{GapSide: "reference"}, nil
	}
	return compareRecordResolutions(systemAnswers, referenceAnswers), nil
}

func dedupeSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func contains(sorted []string, value string) bool {
	i := sort.SearchStrings(sorted, value)
	return i < len(sorted) && sorted[i] == value
}
