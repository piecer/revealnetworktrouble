package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func TestGeoDetailsRetainsExactLegacyRollbackWinner(t *testing.T) {
	report := maximumGeoCompactReport()
	sequence := 0
	for ri := range report.Results {
		for _, attempt := range report.Results[ri].Details["attempts"].([]diagnostic.TraceAttempt) {
			for ni := range attempt.Topology.Nodes {
				node := &attempt.Topology.Nodes[ni]
				if node.Hop == 0 {
					continue
				}
				node.Address = fmt.Sprintf("11.%d.%d.%d", sequence>>16, (sequence>>8)&255, sequence&255)
				sequence++
				node.GeoDetails = diagnostic.GeoDetailSnapshot{Provider: "ipwho.is", Source: diagnostic.GeoIPSourceUpstream, City: "hidden immutable"}
			}
		}
	}
	build := diagnostic.BuildCompactTopologyWithOptions(report, -1, true)
	report.SetCompactTopologyBuild(build)
	cacheBefore := diagnostic.CloneCompactTopology(build.Topology)
	hiddenBefore := report.Results[0].Details["attempts"].([]diagnostic.TraceAttempt)[0].Topology.Nodes[1].GeoDetails
	var winner diagnostic.Report
	builds, calls := 0, 0
	legacy, err := marshalCompactResponseRetainingSnapshot(report, func(r diagnostic.Report) ([]byte, error) {
		calls++
		return marshalClosedResponse(r, diagnostic.CompactTopologyMaxResponseBytes-1, false)
	}, func(r diagnostic.Report, n int, g bool) diagnostic.CompactTopologyBuildResult {
		builds++
		return diagnostic.BuildCompactTopologyWithOptions(r, n, g)
	}, &winner)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := marshalClosedResponse(winner, diagnostic.CompactTopologyMaxResponseBytes-1, true)
	if err != nil || !bytes.Equal(selected, legacy) {
		t.Fatal("retained typed winner differs from fitted bytes")
	}
	if winner.CompactTopology.Geo.Omitted == 0 || calls > 12 || builds != 0 {
		t.Fatalf("rollback/cache precondition: geo=%+v calls=%d builds=%d", winner.CompactTopology.Geo, calls, builds)
	}
	body, err := marshalReportResponse(report, diagnostic.TopologyModeCompact, true)
	if err != nil {
		t.Fatal(err)
	}
	side := assertGeoDetailsLegacyBytes(t, legacy, body)
	if side == nil || side.Total != 6000 || side.Total != len(side.Entries)+side.Omitted {
		t.Fatal("raw total lost after rollback")
	}
	if !reflect.DeepEqual(cacheBefore, build.Topology) || report.Results[0].Details["attempts"].([]diagnostic.TraceAttempt)[0].Topology.Nodes[1].GeoDetails != hiddenBefore {
		t.Fatal("fitting mutated cached projection or hidden snapshot")
	}
	t.Logf("legacy rollback: %d marshals, %d builds; sidecar retained %d / %d", calls, builds, len(side.Entries), side.Total)
}

func TestGeoDetailsFittingMatchesLinearOracleAndBoundedCalls(t *testing.T) {
	for count := 0; count <= 8; count++ {
		report := geoDetailsReportForTest(count, `서울 <>&"\ 😀`)
		raw := diagnostic.BuildGeoDetails(report)
		legacy, err := marshalFullResponse(report)
		if err != nil {
			t.Fatal(err)
		}
		caps := []int{len(legacy), len(legacy) + 1}
		for keep := 0; keep <= count; keep++ {
			side := *raw
			side.Entries = raw.Entries[:keep]
			side.Omitted = side.Total - keep
			candidate := report
			candidate.GeoDetails = &side
			body, err := marshalFullResponse(candidate)
			if err != nil {
				t.Fatal(err)
			}
			caps = append(caps, len(body)-1, len(body), len(body)+1)
		}
		for _, limit := range caps {
			marshal := func(r diagnostic.Report) ([]byte, error) { return marshalClosedResponse(r, limit, true) }
			want := legacy
			for keep := 0; keep <= count; keep++ {
				side := *raw
				side.Entries = raw.Entries[:keep]
				side.Omitted = side.Total - keep
				candidate := report
				candidate.GeoDetails = &side
				if body, err := marshal(candidate); err == nil {
					want = body
				}
			}
			calls, totalBytes := 0, 0
			got, err := fitGeoDetails(report, legacy, raw, func(r diagnostic.Report) ([]byte, error) {
				calls++
				b, e := marshal(r)
				totalBytes += len(b)
				return b, e
			})
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("count=%d cap=%d differs from linear oracle: %v", count, limit, err)
			}
			if calls > 4 || totalBytes > 4*limit {
				t.Fatalf("nonlogarithmic probes: %d/%d", calls, totalBytes)
			}
		}
	}
	report := geoDetailsReportForTest(600, "text")
	legacy, err := marshalFullResponse(report)
	if err != nil {
		t.Fatal(err)
	}
	calls, totalBytes := 0, 0
	body, err := fitGeoDetails(report, legacy, diagnostic.BuildGeoDetails(report), func(r diagnostic.Report) ([]byte, error) {
		calls++
		b, e := marshalFullResponse(r)
		totalBytes += len(b)
		return b, e
	})
	if err != nil || calls > 9 || totalBytes > 9*len(body) {
		t.Fatalf("max workload probes=%d cumulative=%d bytes=%d err=%v", calls, totalBytes, len(body), err)
	}
	t.Logf("600 raw / 500 retained: %d full marshal calls, %d cumulative bytes", calls, totalBytes)
}

func TestGeoDetailsEmptyEnvelopeAndWholeResponseBoundaries(t *testing.T) {
	for _, mode := range []diagnostic.TopologyMode{diagnostic.TopologyModeFull, diagnostic.TopologyModeCompact} {
		for _, headroom := range []int{0, 1, 80, 300} {
			report := geoDetailsReportForTest(1, "city")
			limit := maxFullResponseBytes
			if mode == diagnostic.TopologyModeCompact {
				limit = diagnostic.CompactTopologyMaxResponseBytes - 1
			} else {
				// Long numerical leaves allow the byte cap to bind before the independent
				// decoded-string cap; every container and node remains under its limit.
				groups := make([][]int64, 6)
				for i := range groups {
					groups[i] = make([]int64, 30000)
					for j := range groups[i] {
						groups[i][j] = math.MaxInt64
					}
				}
				report.Results[0].Details["numeric_padding"] = groups
			}
			report.Results[0].Message = "a"
			baseline, err := marshalReportResponse(report, mode, false)
			if err != nil {
				t.Fatal(err)
			}
			padding := limit - headroom - len(baseline) + 1
			report.Results[0].Message = strings.Repeat("<", padding/6) + strings.Repeat("a", padding%6)
			legacy, err := marshalReportResponse(report, mode, false)
			if err != nil || len(legacy) != limit-headroom {
				t.Fatalf("setup %s legacy=%d wanted=%d err=%v", mode, len(legacy), limit-headroom, err)
			}
			body, err := marshalReportResponse(report, mode, true)
			if err != nil {
				t.Fatalf("optional sidecar broke fitting legacy: %v", err)
			}
			if len(body) > limit {
				t.Fatalf("whole cap exceeded: %d > %d", len(body), limit)
			}
			side := assertGeoDetailsLegacyBytes(t, legacy, body)
			if headroom < 2 && side != nil {
				t.Fatal("impossible empty envelope should be omitted")
			}
			if headroom == 80 && (side == nil || side.Entries == nil || len(side.Entries) != 0 || side.Total != 1 || side.Omitted != 1) {
				t.Fatalf("empty envelope must retain truthful omissions: %+v", side)
			}
			if headroom == 300 && (side == nil || len(side.Entries) != 1) {
				t.Fatal("entry with room was lost")
			}
		}
	}
}

func TestGeoDetailsClosedBudgetsKeepLegacyAndRejectDynamicEscape(t *testing.T) {
	for _, budget := range []string{"strings", "containers", "nodes"} {
		t.Run(budget, func(t *testing.T) {
			report := geoDetailsReportForTest(1, "city")
			switch budget {
			case "strings":
				v := fullResponseValidator{active: make(map[fullResponseIdentity]struct{}), stringKeys: make(map[string]struct{})}
				if err := v.validate(reflect.ValueOf(report), 0, false); err != nil {
					t.Fatal(err)
				}
				report.Results[0].Message = strings.Repeat("a", maxFullResponseStringBytes-v.stringBytes)
			case "containers":
				// Determine the exact remaining container budget with the actual closed
				// validator (all retained values are legitimate dynamic primitive arrays).
				report.Results[0].Details["padding"] = [][]bool{}
				v := fullResponseValidator{active: make(map[fullResponseIdentity]struct{}), stringKeys: make(map[string]struct{})}
				if err := v.validate(reflect.ValueOf(report), 0, false); err != nil {
					t.Fatal(err)
				}
				padding := make([][]bool, maxFullResponseContainers-v.containers)
				for i := range padding {
					padding[i] = []bool{}
				}
				report.Results[0].Details["padding"] = padding
			case "nodes":
				groups := make([][]bool, 8)
				report.Results[0].Details["padding"] = groups
				v := fullResponseValidator{active: make(map[fullResponseIdentity]struct{}), stringKeys: make(map[string]struct{})}
				if err := v.validate(reflect.ValueOf(report), 0, false); err != nil {
					t.Fatal(err)
				}
				remaining := maxFullResponseNodes - v.nodes
				for i := range groups {
					n := min(remaining, maxFullResponseContainerElements)
					groups[i] = make([]bool, n)
					remaining -= n
				}
				if remaining != 0 {
					t.Fatal("bad node-budget setup")
				}
			}
			legacy, err := marshalFullResponse(report)
			if err != nil {
				t.Fatal(err)
			}
			body, err := marshalReportResponse(report, diagnostic.TopologyModeFull, true)
			if err != nil {
				t.Fatal(err)
			}
			side := assertGeoDetailsLegacyBytes(t, legacy, body)
			if side != nil && len(side.Entries) != 0 {
				t.Fatalf("%s budget ignored", budget)
			}
			if budget != "strings" && side != nil {
				t.Fatal("closed structural budget should omit envelope")
			}
		})
	}
	report := geoDetailsReportForTest(1, "city")
	report.Results[0].Details["escape"] = &diagnostic.GeoDetailsSidecar{SchemaVersion: 1, Entries: []diagnostic.GeoDetailsEntry{}}
	if _, err := marshalReportResponse(report, diagnostic.TopologyModeFull, true); !errors.Is(err, errFullResponseUnsupported) {
		t.Fatalf("static DTO escaped dynamic allowlist: %v", err)
	}
}

func TestGeoDetailsPrunedRawSnapshotSurvivesCompactProjection(t *testing.T) {
	report := geoDetailsReportForTest(600, "city")
	legacy, err := marshalReportResponse(report, diagnostic.TopologyModeCompact, false)
	if err != nil {
		t.Fatal(err)
	}
	body, err := marshalReportResponse(report, diagnostic.TopologyModeCompact, true)
	if err != nil {
		t.Fatal(err)
	}
	side := assertGeoDetailsLegacyBytes(t, legacy, body)
	var decoded diagnostic.Report
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	retained := map[string]bool{}
	for _, n := range decoded.CompactTopology.Nodes {
		retained[n.Address] = true
	}
	absent := 0
	for _, entry := range side.Entries {
		if !retained[entry.Address] {
			absent++
		}
	}
	if side.Total != 600 || len(side.Entries) != 500 || absent == 0 {
		t.Fatal("sidecar incorrectly scoped to compact survival")
	}
	t.Logf("raw-sidecar entries without retained compact nodes: %d", absent)
}
