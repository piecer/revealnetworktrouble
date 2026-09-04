package api

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func TestMarshalCompactResponseSerializesValidEmptyGraphAsExactArrays(t *testing.T) {
	report := diagnostic.Report{
		ID: "zero-route", Status: diagnostic.StatusHealthy,
		StartedAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		Results:   []diagnostic.Result{},
	}
	got, err := marshalCompactResponse(report)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("{\"id\":\"zero-route\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":0,\"summary\":{\"total\":0,\"passed\":0,\"failed\":0},\"results\":[],\"compact_topology\":{\"schema\":\"compact-v1\",\"selection\":\"fair-complete-prefix-v1\",\"limits\":{\"nodes\":500,\"links\":1000,\"max_response_bytes_exclusive\":1048576,\"max_geo_bundle_bytes\":4096},\"nodes\":[],\"links\":[],\"routes\":[],\"stats\":{\"nodes\":{\"total\":0,\"displayed\":0,\"omitted\":0},\"links\":{\"total\":0,\"displayed\":0,\"omitted\":0},\"routes\":{\"total\":0,\"displayed\":0,\"complete\":0,\"partial\":0,\"omitted\":0},\"node_observations\":{\"total\":0,\"displayed\":0,\"omitted\":0},\"link_observations\":{\"total\":0,\"displayed\":0,\"omitted\":0}},\"geo\":{\"eligible\":0,\"available\":0,\"included\":0,\"omitted\":0,\"unavailable\":0},\"truncated\":false}}\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("empty compact API bytes drifted\n got: %s\nwant: %s", got, want)
	}
}

func TestMarshalCompactResponsePreservesEmptyArraysThroughProbeAndRollback(t *testing.T) {
	topology := &diagnostic.Topology{
		Reached: true,
		Nodes: []diagnostic.TopologyNode{
			{ID: "local", Hop: 0, Address: "local", Status: "healthy"},
			{ID: "remote", Hop: 1, Address: "192.0.2.1", Status: "healthy"},
		},
		Links: []diagnostic.TopologyLink{{From: "local", To: "remote", Status: "healthy"}},
	}
	report := diagnostic.Report{Results: []diagnostic.Result{{
		Kind: diagnostic.KindTraceroute, Status: diagnostic.StatusHealthy,
		Details: map[string]any{"attempts": []diagnostic.TraceAttempt{{Attempt: 1, Status: diagnostic.StatusHealthy, Topology: topology}}},
	}}}
	calls := 0
	body, err := marshalCompactResponseForTest(report, func(value diagnostic.Report) ([]byte, error) {
		calls++
		compact := value.CompactTopology
		if compact.Nodes == nil || compact.Links == nil || compact.Routes == nil {
			t.Fatalf("probe %d observed null graph slice: nodes=%#v links=%#v routes=%#v", calls, compact.Nodes, compact.Links, compact.Routes)
		}
		if calls == 1 {
			return bytes.Repeat([]byte{'x'}, diagnostic.CompactTopologyMaxResponseBytes-1), nil
		}
		return json.Marshal(value)
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("rollback was not exercised: calls=%d", calls)
	}
	var decoded diagnostic.Report
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("rollback body: %v", err)
	}
	if decoded.CompactTopology == nil || decoded.CompactTopology.Nodes == nil || decoded.CompactTopology.Links == nil || decoded.CompactTopology.Routes == nil {
		t.Fatalf("rollback response emitted null graph arrays: %+v", decoded.CompactTopology)
	}
}
