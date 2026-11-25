package diagnostic

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The web client restates the producer's aggregate limits as its own constants.
// Nothing else ties the two sides together, so a limit can drift on one side while
// the other still accepts a value the producer could never have written. This gate
// pins the client constants to the producer constants and forbids the presentation
// guard from restating them as free-floating literals.
func TestWebClientAggregateLimitsMatchTheProducerLimits(t *testing.T) {
	root := documentationRepositoryRoot(t)
	state, err := os.ReadFile(filepath.Join(root, "frontend", "state.js"))
	if err != nil {
		t.Fatalf("read the web client limits: %v", err)
	}
	source := string(state)
	wants := map[string]int64{
		"MAX_STAGE_TIMING_MS":   int64(MaxRequestBudget / time.Millisecond),
		"MAX_SAMPLES":           int64(MaxSamples),
		"MAX_COMPARISON_VALUES": int64(maxComparisonRecordValues),
	}
	for name, want := range wants {
		match := regexp.MustCompile(`const `+name+` = ([0-9]+);`).FindStringSubmatch(source)
		if match == nil {
			t.Fatalf("%s is not a plain constant in the web client, so the parity gate cannot read it", name)
		}
		got, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			t.Fatalf("%s is not a number: %v", name, err)
		}
		if got != want {
			t.Errorf("the web client sets %s to %d, but the producer limit is %d", name, got, want)
		}
	}

	// The presentation guard reads the same constants the parser exports, so the two
	// client layers cannot enforce different bounds for the same string.
	app, err := os.ReadFile(filepath.Join(root, "frontend", "app.js"))
	if err != nil {
		t.Fatalf("read the web client presentation guard: %v", err)
	}
	appSource := string(app)
	if !strings.Contains(appSource, "AGGREGATE_LIMITS") {
		t.Fatal("the presentation guard must consume the shared limits, not restate them")
	}
	for _, key := range []string{"AGGREGATE_LIMITS.stageTimingMS", "AGGREGATE_LIMITS.samples", "AGGREGATE_LIMITS.comparisonValues"} {
		if !strings.Contains(appSource, key) {
			t.Errorf("the presentation guard does not read %s", key)
		}
	}
	for _, literal := range []string{"301000", "64 &&", "<= 10 &&"} {
		if strings.Contains(appSource, literal) {
			t.Errorf("the presentation guard restates a producer limit as the literal %q", literal)
		}
	}
}
