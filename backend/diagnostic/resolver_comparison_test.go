package diagnostic

import (
	"context"
	"errors"
	"testing"
)

// A resolver comparison is only meaningful when both sides actually answered:
// a failed side is a coverage gap, not evidence of divergence. Order is
// irrelevant because record sets have no canonical order.

func TestCompareRecordResolutionsIgnoresOrder(t *testing.T) {
	result := compareRecordResolutions([]string{"1.1.1.1", "2.2.2.2"}, []string{"2.2.2.2", "1.1.1.1"})
	if !result.Match || result.OnlySystem != nil || result.OnlyReference != nil {
		t.Fatalf("identical sets in different order must match: %+v", result)
	}
}

func TestCompareRecordResolutionsReportsDivergence(t *testing.T) {
	result := compareRecordResolutions([]string{"9.9.9.9", "9.9.9.9"}, []string{"8.8.8.8"})
	if result.Match {
		t.Fatalf("different sets must not match: %+v", result)
	}
	if len(result.OnlySystem) != 1 || result.OnlySystem[0] != "9.9.9.9" {
		t.Fatalf("duplicate answers collapse to one set member: %+v", result)
	}
	if len(result.OnlyReference) != 1 || result.OnlyReference[0] != "8.8.8.8" {
		t.Fatalf("reference-only records missing: %+v", result)
	}
}

// A failed lookup on either side must produce a comparison gap, never a
// divergence claim.
func TestResolverComparisonRefusesToCompareFailedLookups(t *testing.T) {
	failed := &recordResolverFunc{err: errors.New("lookup failed")}
	ok := &recordResolverFunc{answers: []string{"8.8.8.8"}}

	result, err := resolveAndCompare(context.Background(), ok, failed, "example.test", "A")
	if err != nil {
		t.Fatal(err)
	}
	if result.Comparable {
		t.Fatalf("a failed lookup must not be compared as divergence: %+v", result)
	}
	if result.GapSide != "reference" {
		t.Fatalf("the failing side must be identified for coverage reporting: %+v", result)
	}

	if _, err := resolveAndCompare(context.Background(), ok, ok, "example.test", "SOA"); err == nil {
		t.Fatal("record types outside the whitelist must be rejected")
	}
}

type recordResolverFunc struct {
	answers []string
	err     error
}

func (r *recordResolverFunc) LookupType(_ context.Context, _ string, _ string) ([]string, error) {
	return r.answers, r.err
}
