package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

type geoDetailsCountingChecker struct{ calls atomic.Int32 }

func geoDetailsReportForTest(count int, city string) diagnostic.Report {
	report := diagnostic.Report{ID: "abababababababababababab", Status: diagnostic.StatusHealthy, StartedAt: fixtureTime, Results: []diagnostic.Result{}}
	for start := 0; start < count; start += 30 {
		nodes := []diagnostic.TopologyNode{{ID: "local", Hop: 0, Address: "local", Status: "healthy"}}
		links := []diagnostic.TopologyLink{}
		for i := start; i < min(count, start+30); i++ {
			address := fmt.Sprintf("11.%d.%d.%d", i>>16, (i>>8)&255, i&255)
			node := diagnostic.TopologyNode{ID: fmt.Sprintf("hop-%d", i-start+1), Hop: i - start + 1, Address: address, Status: "healthy", PublicIP: true,
				GeoDetails: diagnostic.GeoDetailSnapshot{Provider: "ipwho.is", Source: diagnostic.GeoIPSourceUpstream, City: city}}
			links = append(links, diagnostic.TopologyLink{From: nodes[len(nodes)-1].ID, To: node.ID, Status: "healthy"})
			nodes = append(nodes, node)
		}
		topology := &diagnostic.Topology{Reached: true, Nodes: nodes, Links: links}
		report.Results = append(report.Results, diagnostic.Result{Kind: diagnostic.KindTraceroute, Address: nodes[len(nodes)-1].Address, Status: diagnostic.StatusHealthy, StartedAt: fixtureTime,
			Details: map[string]any{"attempts": []diagnostic.TraceAttempt{{Attempt: 1, Status: diagnostic.StatusHealthy, Topology: topology}}, "attempts_total": 1, "attempts_reached": 1, "attempts_failed": 0, "attempts_unreached": 0, "attempts_execution_failed": 0, "attempts_timed_out": 0, "attempts_cancelled": 0}})
	}
	report.Summary = diagnostic.Summary{Total: len(report.Results), Passed: len(report.Results)}
	return report
}

func assertGeoDetailsLegacyBytes(t *testing.T, legacy, extended []byte) *diagnostic.GeoDetailsSidecar {
	t.Helper()
	var parsed struct {
		GeoDetails *diagnostic.GeoDetailsSidecar `json:"geo_details"`
	}
	if err := json.Unmarshal(extended, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.GeoDetails == nil {
		if !bytes.Equal(legacy, extended) {
			t.Fatal("omitted sidecar changed legacy body")
		}
		return nil
	}
	position := bytes.LastIndex(extended, []byte(`,"geo_details":`))
	if position < 0 || !bytes.Equal(legacy, append(append([]byte{}, extended[:position]...), '}', '\n')) {
		t.Fatal("sidecar fitting changed legacy report bytes")
	}
	return parsed.GeoDetails
}

func TestGeoDetailsCompletePrefixBoundsPreserveLegacyBytes(t *testing.T) {
	for _, city := range []string{"short", strings.Repeat("<", 256)} {
		for _, mode := range []diagnostic.TopologyMode{diagnostic.TopologyModeFull, diagnostic.TopologyModeCompact} {
			t.Run(fmt.Sprintf("%s/text-%d", mode, len(city)), func(t *testing.T) {
				report := geoDetailsReportForTest(600, city)
				before := report.Results[0].Details["attempts"].([]diagnostic.TraceAttempt)[0].Topology.Nodes[1].GeoDetails
				legacy, err := marshalReportResponse(report, mode, false)
				if err != nil {
					t.Fatal(err)
				}
				body, err := marshalReportResponse(report, mode, true)
				if err != nil {
					t.Fatal(err)
				}
				side := assertGeoDetailsLegacyBytes(t, legacy, body)
				if side == nil || side.Total != 600 || side.Total != len(side.Entries)+side.Omitted || len(side.Entries) > 500 {
					t.Fatalf("unbounded/wrong counts: %+v", side)
				}
				encoded, _ := json.Marshal(side)
				if len(encoded) > diagnostic.GeoDetailsMaxBytes {
					t.Fatalf("standalone sidecar exceeds cap: %d", len(encoded))
				}
				all := diagnostic.BuildGeoDetails(report)
				if !reflect.DeepEqual(side.Entries, all.Entries[:len(side.Entries)]) {
					t.Fatal("not the sorted raw complete prefix")
				}
				if len(side.Entries) < 500 {
					next := *all
					next.Entries = next.Entries[:len(side.Entries)+1]
					next.Omitted = next.Total - len(next.Entries)
					nextBytes, _ := json.Marshal(next)
					if len(nextBytes) <= diagnostic.GeoDetailsMaxBytes {
						t.Fatal("fitter did not retain maximal complete prefix")
					}
				}
				if report.Results[0].Details["attempts"].([]diagnostic.TraceAttempt)[0].Topology.Nodes[1].GeoDetails != before {
					t.Fatal("hidden raw snapshot mutated")
				}
			})
		}
	}
}

func (*geoDetailsCountingChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (c *geoDetailsCountingChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	c.calls.Add(1)
	return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusHealthy}
}

func TestGeoDetailsOptInFullAndCompactFromLiveProviderProtocol(t *testing.T) {
	for _, mode := range []string{"full", "compact"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/8.8.8.8" {
					t.Errorf("unexpected provider query: %s", r.URL)
				}
				_, _ = w.Write([]byte(`{"success":true,"city":"서울 <>& 😀","latitude":0,"longitude":0,"timezone":{"id":"Asia/Seoul"},"connection":{"asn":15169,"org":"Owner","isp":"Different ISP"}}`))
			}))
			defer provider.Close()
			fetched := time.Date(2026, 10, 9, 1, 2, 3, 456789000, time.FixedZone("test", 9*3600))
			lookup := diagnostic.NewIPWhoIsLookupWithConfig(provider.Client(), provider.URL, nil, diagnostic.GeoIPCacheConfig{Now: func() time.Time { return fetched }, TTL: time.Hour})
			checker := diagnostic.NewInjectedTracerouteChecker(func(context.Context, string, ...string) ([]byte, error) {
				return []byte(fixtureTraceOutput([]string{"8.8.8.8"})), nil
			}, lookup, nil)
			supervisor, err := diagnostic.NewCheckerSupervisor(1)
			if err != nil {
				t.Fatal(err)
			}
			defer supervisor.Shutdown(context.Background())
			handler := NewServer(diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", nil)
			for _, query := range []string{"", "?geo_details=1", "?%67eo_details=%31"} {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/reports"+query, strings.NewReader(`{"targets":[{"kind":"traceroute","address":"8.8.8.8","attempts":1}],"topology_mode":"`+mode+`"}`)))
				if rec.Code != 200 {
					t.Fatalf("status %d: %s", rec.Code, rec.Body)
				}
				var body map[string]json.RawMessage
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				detail, exists := body["geo_details"]
				if query == "" {
					if exists {
						t.Fatal("sidecar leaked without opt-in")
					}
					continue
				}
				if !exists {
					t.Fatal("opt-in omitted validated supplemental text")
				}
				var side struct {
					SchemaVersion int              `json:"schema_version"`
					Total         int              `json:"total"`
					Omitted       int              `json:"omitted"`
					Entries       []map[string]any `json:"entries"`
				}
				if err := json.Unmarshal(detail, &side); err != nil {
					t.Fatal(err)
				}
				if side.SchemaVersion != 1 || side.Total != 1 || side.Omitted != 0 || len(side.Entries) != 1 {
					t.Fatalf("bad sidecar: %s", detail)
				}
				entry := side.Entries[0]
				if entry["address"] != "8.8.8.8" || entry["provider"] != "ipwho.is" || entry["source"] != "cache" || entry["city"] != "서울 <>& 😀" || entry["isp"] != "Different ISP" || entry["fetched_at"] != "2026-10-08T16:02:03.456Z" || entry["expires_at"] != "2026-10-08T17:02:03.456Z" {
					t.Fatalf("wrong snapshot: %s", detail)
				}
				if _, ok := entry["latitude"]; ok {
					t.Fatal("coordinates merged into supplemental snapshot")
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("duplicate provider queries: %d", calls.Load())
			}
		})
	}
}

func TestGeoDetailsQueryRejectsBeforeAdmission(t *testing.T) {
	checker := &geoDetailsCountingChecker{}
	supervisor, err := diagnostic.NewCheckerSupervisor(1)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Shutdown(context.Background())
	server, err := newServer(diagnostic.NewRunnerWithClockAndSupervisor(nil, supervisor, checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{MaxConcurrentReports: 1})
	if err != nil {
		t.Fatal(err)
	}
	// A saturated admission slot must not hide malformed query validation.
	server.admission <- struct{}{}
	defer func() { <-server.admission }()
	handler := newNetHTTPAdapter(server)
	for _, query := range []string{"geo_details", "geo_details=", "geo_details=0", "geo_details=true", "geo_details=01", "geo_details=1.0", "geo_details=1&geo_details=1", "geo_details=1&%67eo_details=1", "geo_details=%zz", "%zz=1", "geo_details=1&broken=%zz", "geo_details=1;other=1", "geo_details=+1"} {
		t.Run(query, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/reports?"+query, strings.NewReader(`{"targets":[{"kind":"dns","address":"example.com"}]}`))
			handler.ServeHTTP(rec, req)
			if rec.Code != 422 || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || checker.calls.Load() != 0 || len(server.admission) != 1 || len(server.bodyDecodes) != 0 {
				t.Fatalf("query admitted/wrong failure: %d %s calls=%d", rec.Code, rec.Body, checker.calls.Load())
			}
		})
	}
}
