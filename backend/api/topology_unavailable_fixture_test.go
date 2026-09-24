package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

func TestTopologyUnavailableHTTPFixtureMatchesProducer(t *testing.T) {
	runner := diagnostic.NewRunnerWithSupervisor(mustAPICheckerSupervisor(t, 1), diagnostic.NewTracerouteChecker(nil, nil, nil))
	handler := NewServer(runner, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(`{"topology_mode":"compact","targets":[{"kind":"traceroute","address":"unavailable.example.test","attempts":1}]}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status=%d body=%s", recorder.Code, recorder.Body)
	}
	var report diagnostic.Report
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Results[0].ErrorCode != "traceroute_unavailable" || report.CompactTopology == nil || len(report.CompactTopology.Routes) != 0 || report.Analysis.Findings[0].Code != diagnostic.FindingTracerouteUnavailable {
		t.Fatalf("unavailable HTTP observation lost: %+v", report)
	}
	// Freeze only nondeterministic envelope fields after traversing the actual
	// handler/Runner/checker; retain the observed analysis and compact body.
	report.ID = "efefefefefefefefefefefef"
	report.StartedAt, report.DurationMS = fixtureTime, 1
	for i := range report.Results {
		report.Results[i].StartedAt, report.Results[i].LatencyMS = fixtureTime, 0
	}
	generated, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	generated = append(generated, '\n')
	path := "../../testdata/topology-unavailable-http-report.json"
	if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
		if err := os.WriteFile(path, generated, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, generated) {
		t.Fatal("unavailable HTTP fixture differs from producer")
	}
}
