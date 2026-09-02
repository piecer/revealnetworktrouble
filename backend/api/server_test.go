package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

type successChecker struct{}

func (successChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (successChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusHealthy}
}

func newTestHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(diagnostic.NewRunner(successChecker{}), logger, "test", []string{"http://localhost:3000"})
}

func TestWriteJSONReturnsStableErrorBeforeCommittingNonFiniteValue(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSON(rec, http.StatusOK, map[string]any{"value": math.Inf(1)})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON error body %q: %v", rec.Body.String(), err)
	}
	if response.Error.Code != "response_serialization_failed" {
		t.Fatalf("error code = %q, body = %q", response.Error.Code, rec.Body.String())
	}
}

type failingJSONValue struct{}

func (failingJSONValue) MarshalJSON() ([]byte, error) {
	return []byte(`{"partial":`), io.ErrUnexpectedEOF
}

func TestWriteJSONReturnsStableErrorBeforeCommittingFailingMarshaler(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSON(rec, http.StatusOK, failingJSONValue{})

	want := "{\"error\":{\"code\":\"response_serialization_failed\",\"message\":\"report response could not be serialized\"}}\n"
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != want {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
}

func TestWriteJSONKeepsOrdinarySuccessAndErrorResponsesValid(t *testing.T) {
	tests := []struct {
		name   string
		status int
		write  func(http.ResponseWriter)
	}{
		{name: "success", status: http.StatusOK, write: func(w http.ResponseWriter) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}},
		{name: "error", status: http.StatusBadRequest, write: func(w http.ResponseWriter) {
			writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.write(rec)
			if rec.Code != tt.status {
				t.Fatalf("status = %d", rec.Code)
			}
			if contentType := rec.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q", contentType)
			}
			var body any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid JSON body %q: %v", rec.Body.String(), err)
			}
			if !strings.HasSuffix(rec.Body.String(), "\n") {
				t.Fatalf("body no longer has compatibility newline: %q", rec.Body.String())
			}
		})
	}
}

func TestHealthAndCORS(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
}

func TestChecksAdvertisesHTTPSAndServices(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/checks", nil)
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Kinds []string `json:"kinds"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"https": false, "traceroute": false, "ssh": false, "imaps": false}
	for _, kind := range response.Kinds {
		if _, ok := want[kind]; ok {
			want[kind] = true
		}
	}
	for kind, found := range want {
		if !found {
			t.Errorf("%q missing from kinds: %v", kind, response.Kinds)
		}
	}
}

func TestChecksAdvertisesTopologyModesAndCompactLimits(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/checks", nil))
	var response struct {
		TopologyModes []string `json:"topology_modes"`
		Limits        struct {
			CompactTopologyNodes          int `json:"compact_topology_nodes"`
			CompactTopologyLinks          int `json:"compact_topology_links"`
			CompactResponseBytesExclusive int `json:"compact_response_bytes_exclusive"`
			CompactGeoBundleBytes         int `json:"compact_geo_bundle_bytes"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response.TopologyModes, []string{"full", "compact"}) {
		t.Fatalf("topology modes = %#v", response.TopologyModes)
	}
	if response.Limits.CompactTopologyNodes != diagnostic.CompactTopologyMaxNodes ||
		response.Limits.CompactTopologyLinks != diagnostic.CompactTopologyMaxLinks ||
		response.Limits.CompactResponseBytesExclusive != diagnostic.CompactTopologyMaxResponseBytes ||
		response.Limits.CompactGeoBundleBytes != diagnostic.CompactTopologyMaxGeoBundleBytes {
		t.Fatalf("compact limits = %+v", response.Limits)
	}
}

func TestCreateReport(t *testing.T) {
	body := bytes.NewBufferString(`{"targets":[{"kind":"dns","address":"example.test"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var report diagnostic.Report
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil || report.Status != diagnostic.StatusHealthy || report.Analysis == nil {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

type detailsChecker struct {
	details map[string]any
}

func (detailsChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (checker detailsChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusHealthy, Details: checker.details}
}

func fullReportHandler(t *testing.T, details map[string]any) http.Handler {
	t.Helper()
	handler, err := NewServerWithConfig(diagnostic.NewRunner(detailsChecker{details: details}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{
		MaxConcurrentReports: 1,
		Mode:                 ModeTrustedLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assertReportError(t *testing.T, handler http.Handler, wantStatus int, wantCode string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, reportRequest(context.Background()))
	if recorder.Code != wantStatus {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Error.Code != wantCode {
		t.Fatalf("response=%+v err=%v body=%q", response, err, recorder.Body.String())
	}
	return recorder
}

func TestCreateReportMapsFullResponseBudgetAndSerializationErrorsToFixedSmallResponses(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	tests := []struct {
		name     string
		details  map[string]any
		wantCode string
	}{
		{name: "string budget", details: map[string]any{"value": strings.Repeat("x", maxFullResponseStringBytes+1)}, wantCode: "full_response_too_large"},
		{name: "cycle budget", details: cyclic, wantCode: "full_response_too_large"},
		{name: "unsupported", details: map[string]any{"value": make(chan int)}, wantCode: "response_serialization_failed"},
		{name: "non-finite", details: map[string]any{"value": math.Inf(1)}, wantCode: "response_serialization_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := assertReportError(t, fullReportHandler(t, test.details), http.StatusInternalServerError, test.wantCode)
			if recorder.Body.Len() > 256 {
				t.Fatalf("error response retained large data: %d bytes", recorder.Body.Len())
			}
		})
	}
}

func TestCreateReportRejectsFiftyOneMiBAmplificationOverRealHTTPWithoutAmplifiedAllocation(t *testing.T) {
	shared := strings.Repeat("g", 256<<10)
	geo := &diagnostic.GeoLocation{City: shared, Latitude: 1, Longitude: 2}
	nodes := make([]diagnostic.TopologyNode, 204) // 204 shared copies encode to exactly 51 MiB before structural JSON.
	for index := range nodes {
		nodes[index] = diagnostic.TopologyNode{ID: "node", Status: "healthy", Geolocation: geo}
	}
	handler := fullReportHandler(t, map[string]any{"topology": diagnostic.Topology{Nodes: nodes}})
	server := httptest.NewServer(handler)
	defer server.Close()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	response, err := http.Post(server.URL+"/api/v1/reports", "application/json", strings.NewReader(`{"targets":[{"kind":"dns","address":"example.test"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	runtime.ReadMemStats(&after)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusInternalServerError || len(body) > 256 {
		t.Fatalf("status=%d response bytes=%d body=%q", response.StatusCode, len(body), body)
	}
	var decoded struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.Error.Code != "full_response_too_large" {
		t.Fatalf("response=%+v err=%v body=%q", decoded, err, body)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 32<<20 {
		t.Fatalf("51 MiB logical response allocated %d bytes", allocated)
	}
	t.Logf("51 MiB logical response: response=%d bytes allocated=%d bytes", len(body), allocated)
}

type compactTransportChecker struct {
	result diagnostic.Result
}

func (compactTransportChecker) Kind() diagnostic.Kind { return diagnostic.KindTraceroute }
func (checker compactTransportChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	result := checker.result
	result.Address = target.Address
	return result
}

func TestCreateReportCompactPrunesRawTopologyAndLeavesFullAndSourceUnchanged(t *testing.T) {
	topology := &diagnostic.Topology{Reached: true, Nodes: []diagnostic.TopologyNode{
		{ID: "l", Hop: 0, Address: "local", Status: "healthy"},
		{ID: "a", Hop: 1, Address: "8.8.8.8", Status: "healthy", PublicIP: true},
	}}
	source := diagnostic.Result{Kind: diagnostic.KindTraceroute, Status: diagnostic.StatusHealthy, LatencyMS: 12, Message: "kept", Details: map[string]any{
		"attempts": []diagnostic.TraceAttempt{{Attempt: 1, Status: diagnostic.StatusHealthy, Topology: topology}},
		"topology": topology, "attempts_total": 1, "geoip_provider_failures": 2, "bounded_other": "kept",
	}}
	before, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(diagnostic.NewRunner(compactTransportChecker{result: source}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", nil)

	request := func(mode string) *httptest.ResponseRecorder {
		body := `{"targets":[{"kind":"traceroute","address":"example.test"}]` + mode + `}`
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body)))
		return rec
	}
	compact := request(`,"topology_mode":"compact"`)
	if compact.Code != http.StatusOK {
		t.Fatalf("compact status=%d body=%s", compact.Code, compact.Body.String())
	}
	var compactBody map[string]any
	if err := json.Unmarshal(compact.Body.Bytes(), &compactBody); err != nil {
		t.Fatal(err)
	}
	compactResult := compactBody["results"].([]any)[0].(map[string]any)
	compactDetails := compactResult["details"].(map[string]any)
	if _, ok := compactDetails["attempts"]; ok {
		t.Fatalf("compact attempts retained: %s", compact.Body.String())
	}
	if _, ok := compactDetails["topology"]; ok {
		t.Fatalf("compact representative topology retained: %s", compact.Body.String())
	}
	for _, key := range []string{"attempts_total", "geoip_provider_failures", "bounded_other"} {
		if _, ok := compactDetails[key]; !ok {
			t.Fatalf("compact detail %q removed: %s", key, compact.Body.String())
		}
	}
	if _, ok := compactBody["compact_topology"]; !ok {
		t.Fatalf("compact_topology missing: %s", compact.Body.String())
	}

	full := request(`,"topology_mode":"full"`)
	if full.Code != http.StatusOK {
		t.Fatalf("full status=%d body=%s", full.Code, full.Body.String())
	}
	var fullBody map[string]any
	if err := json.Unmarshal(full.Body.Bytes(), &fullBody); err != nil {
		t.Fatal(err)
	}
	fullDetails := fullBody["results"].([]any)[0].(map[string]any)["details"].(map[string]any)
	if _, ok := fullDetails["attempts"]; !ok {
		t.Fatalf("full attempts pruned: %s", full.Body.String())
	}
	if _, ok := fullDetails["topology"]; !ok {
		t.Fatalf("full topology pruned: %s", full.Body.String())
	}
	if _, ok := fullBody["compact_topology"]; ok {
		t.Fatalf("full response gained compact field: %s", full.Body.String())
	}
	after, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("HTTP compact projection mutated checker source")
	}
}

func exactJSONPayload(t *testing.T, size int) []byte {
	t.Helper()
	prefix, suffix := []byte(`{"padding":"`), []byte(`"}`)
	if size < len(prefix)+len(suffix) {
		t.Fatalf("payload size %d is too small", size)
	}
	payload := append(append(append([]byte(nil), prefix...), bytes.Repeat([]byte("x"), size-len(prefix)-len(suffix))...), suffix...)
	if len(payload) != size || !json.Valid(payload) {
		t.Fatalf("constructed payload len=%d valid=%v", len(payload), json.Valid(payload))
	}
	return payload
}

func compactResponseTestReport() diagnostic.Report {
	geoA := &diagnostic.GeoLocation{City: "Seoul"}
	geoB := &diagnostic.GeoLocation{City: "Busan"}
	topology := &diagnostic.Topology{Reached: true, Nodes: []diagnostic.TopologyNode{
		{ID: "l", Hop: 0, Address: "local", Status: "healthy"},
		{ID: "a", Hop: 1, Address: "8.8.8.8", Status: "healthy", PublicIP: true, Geolocation: geoA},
		{ID: "b", Hop: 2, Address: "1.1.1.1", Status: "healthy", PublicIP: true, Geolocation: geoB},
	}}
	return diagnostic.Report{ID: "report", Status: diagnostic.StatusHealthy, Results: []diagnostic.Result{{
		Kind: diagnostic.KindTraceroute, Address: "example.test", Status: diagnostic.StatusHealthy,
		Details: map[string]any{"attempts": []diagnostic.TraceAttempt{{Attempt: 1, Status: diagnostic.StatusHealthy, Topology: topology}}, "attempts_total": 1},
	}}}
}

func maximumCompactResult(address string) diagnostic.Result {
	attempts := make([]diagnostic.TraceAttempt, diagnostic.MaxTraceAttempts)
	for attemptIndex := range attempts {
		nodes := make([]diagnostic.TopologyNode, 0, diagnostic.MaxTraceHops+1)
		nodes = append(nodes, diagnostic.TopologyNode{ID: "local", Hop: 0, Address: "local", Status: "healthy"})
		for hop := 1; hop <= diagnostic.MaxTraceHops; hop++ {
			nodes = append(nodes, diagnostic.TopologyNode{
				ID: fmt.Sprintf("h%d", hop), Hop: hop,
				Address: fmt.Sprintf("%s-a%d-h%d.example", address, attemptIndex, hop),
				Status:  "healthy", LatencyMS: float64(hop),
			})
		}
		attempts[attemptIndex] = diagnostic.TraceAttempt{Attempt: attemptIndex + 1, Status: diagnostic.StatusHealthy, Topology: &diagnostic.Topology{Reached: true, Nodes: nodes}}
	}
	return diagnostic.Result{Kind: diagnostic.KindTraceroute, Address: address, Status: diagnostic.StatusHealthy, Details: map[string]any{
		"attempts": attempts, "topology": attempts[0].Topology, "attempts_total": diagnostic.MaxTraceAttempts,
	}}
}

type maximumCompactChecker struct{}

func (maximumCompactChecker) Kind() diagnostic.Kind { return diagnostic.KindTraceroute }
func (maximumCompactChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	return maximumCompactResult(target.Address)
}

func maximumCompactReport() diagnostic.Report {
	results := make([]diagnostic.Result, diagnostic.MaxTargets)
	for index := range results {
		results[index] = maximumCompactResult(fmt.Sprintf("target-%02d", index))
	}
	return diagnostic.Report{ID: "stable", Status: diagnostic.StatusHealthy, Results: results}
}

func maximumGeoCompactReport() diagnostic.Report {
	report := maximumCompactReport()
	for resultIndex := range report.Results {
		attempts := report.Results[resultIndex].Details["attempts"].([]diagnostic.TraceAttempt)
		for attemptIndex := range attempts {
			for nodeIndex := 1; nodeIndex < len(attempts[attemptIndex].Topology.Nodes); nodeIndex++ {
				node := &attempts[attemptIndex].Topology.Nodes[nodeIndex]
				node.PublicIP = true
				node.Geolocation = &diagnostic.GeoLocation{City: fmt.Sprintf("city-%02d-%02d-%02d", resultIndex, attemptIndex, nodeIndex)}
				node.ASN = &diagnostic.ASNInfo{Number: 64500, Organization: strings.Repeat("a", 3900)}
			}
		}
		report.Results[resultIndex].Details["attempts"] = attempts
	}
	return report
}

func cloneCompactTopologyForTest(t *testing.T, topology *diagnostic.CompactTopology) *diagnostic.CompactTopology {
	t.Helper()
	encoded, err := json.Marshal(topology)
	if err != nil {
		t.Fatal(err)
	}
	var clone diagnostic.CompactTopology
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}

func linearGeoRollbackReference(t *testing.T, report diagnostic.Report) []byte {
	t.Helper()
	topology := cloneCompactTopologyForTest(t, diagnostic.BuildCompactTopologyWithOptions(report, -1, true).Topology)
	responseLimited, geoLimited := false, false
	for {
		setCompactTruncationReasons(topology, responseLimited, geoLimited)
		payload, err := json.Marshal(diagnostic.BuildCompactReport(report, topology))
		if err != nil {
			t.Fatal(err)
		}
		payload = append(payload, '\n')
		if len(payload) < diagnostic.CompactTopologyMaxResponseBytes {
			return payload
		}
		responseLimited = true
		removed := false
		for index := len(topology.Nodes) - 1; index >= 0; index-- {
			node := &topology.Nodes[index]
			if node.Geolocation == nil && node.ASN == nil {
				continue
			}
			node.Geolocation, node.ASN = nil, nil
			topology.Geo.Included--
			topology.Geo.Omitted++
			geoLimited, removed = true, true
			break
		}
		if !removed {
			t.Fatal("maximum Geo fixture unexpectedly required transaction rollback")
		}
	}
}

func TestMarshalCompactResponseMaximumGeoRollbackIsBoundedAndMaximal(t *testing.T) {
	report := maximumGeoCompactReport()
	rawBefore, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	want := linearGeoRollbackReference(t, report)
	calls, buildCalls, cumulativeBytes := 0, 0, 0
	started := time.Now()
	body, err := marshalCompactResponseWithBuilder(report, func(value diagnostic.Report) ([]byte, error) {
		calls++
		payload, marshalErr := json.Marshal(value)
		cumulativeBytes += len(payload)
		return payload, marshalErr
	}, func(value diagnostic.Report, accepted int, includeGeo bool) diagnostic.CompactTopologyBuildResult {
		buildCalls++
		return diagnostic.BuildCompactTopologyWithOptions(value, accepted, includeGeo)
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if calls > 24 || buildCalls > 1 || calls+buildCalls > 24 || cumulativeBytes > 32<<20 {
		t.Fatalf("rollback amplification: builds=%d marshals=%d cumulative=%d elapsed=%s", buildCalls, calls, cumulativeBytes, elapsed)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("bounded rollback differs from linear reference: got=%d bytes want=%d", len(body), len(want))
	}
	if len(body) >= diagnostic.CompactTopologyMaxResponseBytes {
		t.Fatalf("body len=%d is not strictly below limit", len(body))
	}
	var final diagnostic.Report
	if err := json.Unmarshal(body, &final); err != nil {
		t.Fatal(err)
	}
	assertAPICompactReferences(t, final.CompactTopology)
	initial := diagnostic.BuildCompactTopologyWithOptions(report, -1, true).Topology
	if final.CompactTopology.Stats != initial.Stats || !reflect.DeepEqual(final.CompactTopology.ResultStats, initial.ResultStats) {
		t.Fatalf("Geo rollback changed topology stats: final=%+v initial=%+v", final.CompactTopology.Stats, initial.Stats)
	}
	if got, wantReasons := final.CompactTopology.TruncationReasons, []diagnostic.CompactTruncationReason{
		diagnostic.CompactTruncationNodeLimit,
		diagnostic.CompactTruncationResponseSize,
		diagnostic.CompactTruncationGeoLimit,
	}; !reflect.DeepEqual(got, wantReasons) {
		t.Fatalf("reasons=%v want=%v", got, wantReasons)
	}
	if final.CompactTopology.Geo.Included+final.CompactTopology.Geo.Omitted != initial.Geo.Available || final.CompactTopology.Geo.Included <= 0 || final.CompactTopology.Geo.Omitted <= 0 {
		t.Fatalf("Geo stats=%+v initial=%+v", final.CompactTopology.Geo, initial.Geo)
	}
	maximal := cloneCompactTopologyForTest(t, final.CompactTopology)
	for index := len(maximal.Nodes) - 1; index >= 0; index-- {
		if maximal.Nodes[index].Geolocation != nil || maximal.Nodes[index].ASN != nil {
			continue
		}
		if initial.Nodes[index].Geolocation == nil && initial.Nodes[index].ASN == nil {
			continue
		}
		maximal.Nodes[index].Geolocation = initial.Nodes[index].Geolocation
		maximal.Nodes[index].ASN = initial.Nodes[index].ASN
		maximal.Geo.Included++
		maximal.Geo.Omitted--
		payload, marshalErr := json.Marshal(diagnostic.BuildCompactReport(report, maximal))
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if len(payload)+1 < diagnostic.CompactTopologyMaxResponseBytes {
			t.Fatalf("one more reverse-ordered Geo bundle fits: len=%d", len(payload)+1)
		}
		break
	}
	rawAfter, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawBefore, rawAfter) {
		t.Fatal("marshalCompactResponse mutated raw report")
	}
	t.Logf("maximum Geo rollback: calls=%d cumulative=%d body=%d elapsed=%s", calls, cumulativeBytes, len(body), elapsed)
}

func BenchmarkMarshalCompactResponseMaximumGeoRollback(b *testing.B) {
	report := maximumGeoCompactReport()
	var totalCalls, totalMarshaledBytes int64
	b.ReportAllocs()
	b.ResetTimer()
	for run := 0; run < b.N; run++ {
		body, err := marshalCompactResponseForTest(report, func(value diagnostic.Report) ([]byte, error) {
			totalCalls++
			payload, marshalErr := json.Marshal(value)
			totalMarshaledBytes += int64(len(payload))
			return payload, marshalErr
		})
		if err != nil {
			b.Fatal(err)
		}
		if len(body) >= diagnostic.CompactTopologyMaxResponseBytes {
			b.Fatalf("body len=%d", len(body))
		}
	}
	b.ReportMetric(float64(totalCalls)/float64(b.N), "marshals/op")
	b.ReportMetric(float64(totalMarshaledBytes)/float64(b.N), "marshaled-B/op")
}

func TestMaximumCompactHTTPResponseIsStrictlyBelowLimitWithExactLength(t *testing.T) {
	targets := make([]diagnostic.Target, diagnostic.MaxTargets)
	for index := range targets {
		targets[index] = diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: fmt.Sprintf("target-%02d", index), Attempts: diagnostic.MaxTraceAttempts}
	}
	requestBody, err := json.Marshal(diagnostic.Request{Targets: targets, TopologyMode: diagnostic.TopologyModeCompact})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(diagnostic.NewRunner(maximumCompactChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewReader(requestBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() >= diagnostic.CompactTopologyMaxResponseBytes || rec.Header().Get("Content-Length") != fmt.Sprint(rec.Body.Len()) {
		t.Fatalf("body len=%d Content-Length=%q", rec.Body.Len(), rec.Header().Get("Content-Length"))
	}
	var decoded diagnostic.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	assertAPICompactReferences(t, decoded.CompactTopology)
	for index, result := range decoded.Results {
		if _, exists := result.Details["attempts"]; exists {
			t.Fatalf("results[%d] retained attempts", index)
		}
		if _, exists := result.Details["topology"]; exists {
			t.Fatalf("results[%d] retained topology", index)
		}
	}
}

func TestCompactResponseBytesAreDeterministicAcrossOneHundredBuilds(t *testing.T) {
	report := maximumGeoCompactReport()
	first, err := marshalCompactResponse(report)
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run < 100; run++ {
		next, err := marshalCompactResponse(report)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if !bytes.Equal(first, next) {
			t.Fatalf("run %d produced different bytes", run)
		}
	}
}

func TestMarshalCompactResponseExactExclusiveBoundary(t *testing.T) {
	report := compactResponseTestReport()
	calls := 0
	body, err := marshalCompactResponseForTest(report, func(diagnostic.Report) ([]byte, error) {
		calls++
		return exactJSONPayload(t, diagnostic.CompactTopologyMaxResponseBytes-2), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != diagnostic.CompactTopologyMaxResponseBytes-1 || calls != 1 || body[len(body)-1] != '\n' {
		t.Fatalf("accepted body len=%d calls=%d", len(body), calls)
	}
}

func TestMarshalCompactResponseRollsBackAtOneMiBBoundaryAndKeepsValidStats(t *testing.T) {
	report := compactResponseTestReport()
	var included []int
	var lastNodeHasGeo []bool
	var reasons [][]diagnostic.CompactTruncationReason
	var final diagnostic.Report
	body, err := marshalCompactResponseForTest(report, func(value diagnostic.Report) ([]byte, error) {
		topology := value.CompactTopology
		included = append(included, topology.Geo.Included)
		last := topology.Nodes[len(topology.Nodes)-1]
		lastNodeHasGeo = append(lastNodeHasGeo, last.Geolocation != nil || last.ASN != nil)
		reasons = append(reasons, append([]diagnostic.CompactTruncationReason(nil), topology.TruncationReasons...))
		final = value
		if len(included) == 1 {
			return exactJSONPayload(t, diagnostic.CompactTopologyMaxResponseBytes-1), nil
		}
		return json.Marshal(value)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) >= diagnostic.CompactTopologyMaxResponseBytes || len(included) < 2 {
		t.Fatalf("body len=%d snapshots=%d", len(body), len(included))
	}
	if included[0] != 2 || included[1] != 1 || !lastNodeHasGeo[0] || lastNodeHasGeo[1] {
		t.Fatalf("Geo fallback order: included=%v last-node-geo=%v", included, lastNodeHasGeo)
	}
	if got, want := reasons[1], []diagnostic.CompactTruncationReason{diagnostic.CompactTruncationResponseSize, diagnostic.CompactTruncationGeoLimit}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reason order = %#v, want %#v", got, want)
	}
	var decoded diagnostic.Report
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("fallback body is invalid JSON: %v", err)
	}
	assertAPICompactReferences(t, decoded.CompactTopology)
	assertAPICompactReferences(t, final.CompactTopology)
}

func linearCompactResponseReference(t *testing.T, report diagnostic.Report, marshal compactReportMarshaler) []byte {
	t.Helper()
	build := diagnostic.BuildCompactTopologyWithOptions(report, -1, true)
	topology := cloneCompactTopologyForTest(t, build.Topology)
	responseLimited, geoLimited := false, false
	for {
		setCompactTruncationReasons(topology, responseLimited, geoLimited)
		payload, err := marshal(diagnostic.BuildCompactReport(report, topology))
		if err != nil {
			t.Fatal(err)
		}
		payload = append(payload, '\n')
		if len(payload) < diagnostic.CompactTopologyMaxResponseBytes {
			return payload
		}
		responseLimited = true
		removed := false
		for index := len(topology.Nodes) - 1; index >= 0; index-- {
			node := &topology.Nodes[index]
			if node.Geolocation == nil && node.ASN == nil {
				continue
			}
			node.Geolocation, node.ASN = nil, nil
			topology.Geo.Included--
			topology.Geo.Omitted++
			geoLimited, removed = true, true
			break
		}
		if removed {
			continue
		}
		if build.AcceptedTransactions <= 0 {
			t.Fatal("linear reference found no fitting response")
		}
		build = diagnostic.BuildCompactTopologyWithOptions(report, build.AcceptedTransactions-1, false)
		topology = cloneCompactTopologyForTest(t, build.Topology)
	}
}

func TestMarshalCompactResponseBinarySearchesTransactionsAndReusesRunnerBuild(t *testing.T) {
	report := compactResponseTestReport()
	initial := diagnostic.BuildCompactTopologyWithOptions(report, -1, true)
	report.SetCompactTopologyBuild(initial)
	rawBefore, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	marshalForOneLink := func(value diagnostic.Report) ([]byte, error) {
		if value.CompactTopology.Stats.Links.Displayed > 1 {
			return exactJSONPayload(t, diagnostic.CompactTopologyMaxResponseBytes-1), nil
		}
		return json.Marshal(value)
	}
	want := linearCompactResponseReference(t, report, marshalForOneLink)
	buildCalls, marshalCalls := 0, 0
	body, err := marshalCompactResponseWithBuilder(report, func(value diagnostic.Report) ([]byte, error) {
		marshalCalls++
		return marshalForOneLink(value)
	}, func(value diagnostic.Report, accepted int, includeGeo bool) diagnostic.CompactTopologyBuildResult {
		buildCalls++
		if accepted < 0 {
			t.Fatal("transport repeated the runner's full initial compact build")
		}
		return diagnostic.BuildCompactTopologyWithOptions(value, accepted, includeGeo)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("binary transaction rollback differs from linear reference: got=%s want=%s", body, want)
	}
	if buildCalls > 2 || marshalCalls > 6 {
		t.Fatalf("small rollback amplification: builds=%d marshals=%d", buildCalls, marshalCalls)
	}
	var final diagnostic.Report
	if err := json.Unmarshal(body, &final); err != nil {
		t.Fatal(err)
	}
	if final.CompactTopology.Stats.Links.Displayed != 1 || final.CompactTopology.Geo.Included != 0 {
		t.Fatalf("did not retain greatest fitting transaction: %+v", final.CompactTopology)
	}
	if got, wantReasons := final.CompactTopology.TruncationReasons, []diagnostic.CompactTruncationReason{
		diagnostic.CompactTruncationResponseSize,
		diagnostic.CompactTruncationGeoLimit,
	}; !reflect.DeepEqual(got, wantReasons) {
		t.Fatalf("reason order=%v want=%v", got, wantReasons)
	}
	rawAfter, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawBefore, rawAfter) {
		t.Fatal("binary probes mutated cached topology or raw report")
	}
}

func TestMarshalCompactResponseMaximumTransactionRollbackIsLogarithmicAndExact(t *testing.T) {
	report := maximumCompactReport()
	rawBefore, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	const greatestFittingTransactions = 237
	buildCalls, marshalCalls := 0, 0
	body, err := marshalCompactResponseWithBuilder(report, func(value diagnostic.Report) ([]byte, error) {
		marshalCalls++
		if value.CompactTopology.Stats.LinkObservations.Displayed > greatestFittingTransactions {
			return exactJSONPayload(t, diagnostic.CompactTopologyMaxResponseBytes-1), nil
		}
		return json.Marshal(value)
	}, func(value diagnostic.Report, accepted int, includeGeo bool) diagnostic.CompactTopologyBuildResult {
		buildCalls++
		return diagnostic.BuildCompactTopologyWithOptions(value, accepted, includeGeo)
	})
	if err != nil {
		t.Fatal(err)
	}
	if buildCalls > 11 || marshalCalls > 12 {
		t.Fatalf("transaction rollback amplification: builds=%d marshals=%d", buildCalls, marshalCalls)
	}
	var final diagnostic.Report
	if err := json.Unmarshal(body, &final); err != nil {
		t.Fatal(err)
	}
	if got := final.CompactTopology.Stats.LinkObservations.Displayed; got != greatestFittingTransactions {
		t.Fatalf("accepted transactions=%d want=%d", got, greatestFittingTransactions)
	}
	if got := final.CompactTopology.TruncationReasons; !reflect.DeepEqual(got, []diagnostic.CompactTruncationReason{
		diagnostic.CompactTruncationResponseSize,
	}) {
		t.Fatalf("reasons=%v", got)
	}
	assertAPICompactReferences(t, final.CompactTopology)
	rawAfter, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawBefore, rawAfter) {
		t.Fatal("transaction probes mutated raw report")
	}
}

func TestMarshalCompactResponseRejectsIrreducibleBaseAndMarshalFailure(t *testing.T) {
	base := diagnostic.Report{ID: "base", Results: []diagnostic.Result{{Kind: diagnostic.KindTraceroute}}}
	_, err := marshalCompactResponseForTest(base, func(diagnostic.Report) ([]byte, error) {
		return exactJSONPayload(t, diagnostic.CompactTopologyMaxResponseBytes-1), nil
	})
	if !errors.Is(err, errCompactResponseTooLarge) {
		t.Fatalf("irreducible error = %v", err)
	}
	_, err = marshalCompactResponseForTest(base, func(diagnostic.Report) ([]byte, error) { return nil, io.ErrUnexpectedEOF })
	if !errors.Is(err, errResponseSerialization) {
		t.Fatalf("serialization error = %v", err)
	}
}

func assertAPICompactReferences(t *testing.T, topology *diagnostic.CompactTopology) {
	t.Helper()
	if topology == nil {
		t.Fatal("compact topology is nil")
	}
	ids := make(map[string]bool, len(topology.Nodes))
	for _, node := range topology.Nodes {
		ids[node.ID] = true
	}
	for _, link := range topology.Links {
		if !ids[link.From] || !ids[link.To] {
			t.Fatalf("dangling link %+v", link)
		}
	}
	for _, route := range topology.Routes {
		for _, id := range route.NodeIDs {
			if !ids[id] {
				t.Fatalf("dangling route ref %q", id)
			}
		}
	}
}

func TestRejectsUnknownField(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewBufferString(`{"targets":[],"secret":true}`))
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

type countingChecker struct {
	mu    sync.Mutex
	calls int
}

func (checker *countingChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (checker *countingChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	checker.mu.Lock()
	checker.calls++
	checker.mu.Unlock()
	return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusHealthy}
}
func (checker *countingChecker) callCount() int {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	return checker.calls
}

func TestCreateReportDistinguishesBodyLimitFromMalformedAndMultipleJSON(t *testing.T) {
	checker := &countingChecker{}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{
		MaxConcurrentReports: 1,
		Mode:                 ModeTrustedLocal,
	})
	if err != nil {
		t.Fatal(err)
	}

	valid := `{"targets":[{"kind":"dns","address":"example.test"}]}`
	tests := []struct {
		name      string
		body      string
		status    int
		errorCode string
	}{
		{name: "limit during first decode", body: `{"targets":[{"kind":"dns","address":"` + strings.Repeat("x", maxBodyBytes) + `"}]}`, status: http.StatusRequestEntityTooLarge, errorCode: "request_too_large"},
		{name: "limit during surplus decode", body: valid + ` "` + strings.Repeat("x", maxBodyBytes) + `"`, status: http.StatusRequestEntityTooLarge, errorCode: "request_too_large"},
		{name: "malformed", body: `{`, status: http.StatusBadRequest, errorCode: "invalid_json"},
		{name: "multiple values", body: valid + ` {}`, status: http.StatusBadRequest, errorCode: "invalid_json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := checker.callCount()
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(test.body)))
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &response); decodeErr != nil || response.Error.Code != test.errorCode {
				t.Fatalf("response=%+v decodeErr=%v body=%q", response, decodeErr, recorder.Body.String())
			}
			if got := checker.callCount(); got != before {
				t.Fatalf("checker calls changed from %d to %d", before, got)
			}
		})
	}

	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(valid)))
	if recovered.Code != http.StatusOK || checker.callCount() != 1 {
		t.Fatalf("admission did not recover: status=%d calls=%d body=%s", recovered.Code, checker.callCount(), recovered.Body.String())
	}
}

type blockingChecker struct {
	started chan struct{}
	release chan struct{}
}

type barrierResponseWriter struct {
	header        http.Header
	started       chan struct{}
	release       chan struct{}
	startedOnce   sync.Once
	mu            sync.Mutex
	status        int
	body          bytes.Buffer
	writeCalls    int
	deadline      time.Time
	deadlineCalls int
}

func newBarrierResponseWriter() *barrierResponseWriter {
	return &barrierResponseWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
}

func (writer *barrierResponseWriter) Header() http.Header { return writer.header }
func (writer *barrierResponseWriter) WriteHeader(status int) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.status == 0 {
		writer.status = status
	}
}
func (writer *barrierResponseWriter) Write(payload []byte) (int, error) {
	writer.startedOnce.Do(func() { close(writer.started) })
	<-writer.release
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.writeCalls++
	return writer.body.Write(payload)
}
func (writer *barrierResponseWriter) SetWriteDeadline(deadline time.Time) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.deadlineCalls++
	writer.deadline = deadline
	return nil
}
func (writer *barrierResponseWriter) snapshot() (status, writes, deadlineCalls int, deadline time.Time, body string) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.status, writer.writeCalls, writer.deadlineCalls, writer.deadline, writer.body.String()
}

func TestBlockedReportWriteReleasesExecutionAdmissionAndWriteCapacityRecovers(t *testing.T) {
	checker := &countingChecker{}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{
		MaxConcurrentReports:        1,
		MaxConcurrentResponseWrites: 1,
		Mode:                        ModeTrustedLocal,
	})
	if err != nil {
		t.Fatal(err)
	}

	blocked := newBarrierResponseWriter()
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(blocked, reportRequest(context.Background()))
		close(firstDone)
	}()
	<-blocked.started

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, reportRequest(context.Background()))
	if checker.callCount() != 2 {
		t.Fatalf("blocked writer retained execution admission: checker calls=%d", checker.callCount())
	}
	if second.Code != http.StatusServiceUnavailable || second.Body.Len() > 256 {
		t.Fatalf("second status=%d bytes=%d body=%s", second.Code, second.Body.Len(), second.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &response); err != nil || response.Error.Code != "write_capacity_unavailable" {
		t.Fatalf("second response=%+v err=%v body=%q", response, err, second.Body.String())
	}

	close(blocked.release)
	<-firstDone
	status, writes, deadlineCalls, deadline, _ := blocked.snapshot()
	if status != http.StatusOK || writes != 1 || deadlineCalls != 1 {
		t.Fatalf("first status=%d writes=%d deadline calls=%d", status, writes, deadlineCalls)
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > reportWriteTimeout {
		t.Fatalf("write deadline remaining=%s timeout=%s", remaining, reportWriteTimeout)
	}

	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, reportRequest(context.Background()))
	if recovered.Code != http.StatusOK || checker.callCount() != 3 {
		t.Fatalf("write capacity did not recover: status=%d calls=%d body=%s", recovered.Code, checker.callCount(), recovered.Body.String())
	}
}

func TestResponseWriteCapacityConfigurationHasDefaultAndHardMaximum(t *testing.T) {
	runner := diagnostic.NewRunner(successChecker{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, config := range []ServerConfig{
		{MaxConcurrentReports: 1, MaxConcurrentResponseWrites: -1, Mode: ModeTrustedLocal},
		{MaxConcurrentReports: 1, MaxConcurrentResponseWrites: hardMaxConcurrentResponseWrites + 1, Mode: ModeTrustedLocal},
	} {
		if _, err := NewServerWithConfig(runner, logger, "test", config); err == nil {
			t.Fatalf("response-write config %+v was accepted", config)
		}
	}
	if _, err := NewServerWithConfig(runner, logger, "test", ServerConfig{MaxConcurrentReports: 2, Mode: ModeTrustedLocal}); err != nil {
		t.Fatalf("default response-write capacity was rejected: %v", err)
	}
}

func TestServerConcurrencyHardLimitsAccept16AndReject17(t *testing.T) {
	runner := diagnostic.NewRunner(successChecker{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if MaxConcurrentReportsLimit != 16 || MaxConcurrentResponseWritesLimit > MaxConcurrentReportsLimit {
		t.Fatalf("hard limits reports=%d writes=%d", MaxConcurrentReportsLimit, MaxConcurrentResponseWritesLimit)
	}
	if _, err := NewServerWithConfig(runner, logger, "test", ServerConfig{Mode: ModeTrustedLocal, MaxConcurrentReports: 16, MaxConcurrentResponseWrites: 16}); err != nil {
		t.Fatalf("limits at 16 rejected: %v", err)
	}
	for _, config := range []ServerConfig{
		{Mode: ModeTrustedLocal, MaxConcurrentReports: 17, MaxConcurrentResponseWrites: 1},
		{Mode: ModeTrustedLocal, MaxConcurrentReports: 1, MaxConcurrentResponseWrites: 17},
	} {
		if _, err := NewServerWithConfig(runner, logger, "test", config); err == nil {
			t.Fatalf("config above hard limit accepted: %+v", config)
		}
	}
}

type failingResponseWriter struct {
	header         http.Header
	status         int
	writeCalls     int
	actualPerWrite int
	err            error
	body           bytes.Buffer
}

func (writer *failingResponseWriter) Header() http.Header { return writer.header }
func (writer *failingResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
}
func (writer *failingResponseWriter) Write(payload []byte) (int, error) {
	writer.writeCalls++
	actual := min(writer.actualPerWrite, len(payload))
	_, _ = writer.body.Write(payload[:actual])
	return actual, writer.err
}

func TestReportWriteFailureDoesNotAppendSecondErrorAndReleasesCapacity(t *testing.T) {
	tests := []struct {
		name   string
		actual int
	}{
		{name: "zero", actual: 0},
		{name: "partial", actual: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checker := &countingChecker{}
			handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{
				MaxConcurrentReports:        1,
				MaxConcurrentResponseWrites: 1,
				Mode:                        ModeTrustedLocal,
			})
			if err != nil {
				t.Fatal(err)
			}
			writer := &failingResponseWriter{header: make(http.Header), actualPerWrite: test.actual, err: io.ErrClosedPipe}
			handler.ServeHTTP(writer, reportRequest(context.Background()))
			if writer.status != http.StatusOK || writer.writeCalls != 1 || writer.body.Len() != test.actual {
				t.Fatalf("status=%d writes=%d actual=%d body=%q", writer.status, writer.writeCalls, writer.body.Len(), writer.body.String())
			}
			if bytes.Contains(writer.body.Bytes(), []byte(`"error"`)) {
				t.Fatalf("write failure appended an error response: %q", writer.body.String())
			}

			recovered := httptest.NewRecorder()
			handler.ServeHTTP(recovered, reportRequest(context.Background()))
			if recovered.Code != http.StatusOK || checker.callCount() != 2 {
				t.Fatalf("capacity did not recover: status=%d calls=%d body=%s", recovered.Code, checker.callCount(), recovered.Body.String())
			}
		})
	}
}

func TestWriteJSONPayloadReportsAttemptedActualAndShortWrite(t *testing.T) {
	writer := &failingResponseWriter{header: make(http.Header), actualPerWrite: 3}
	attempted, actual, err := writeJSONPayload(writer, http.StatusOK, []byte("12345"))
	if attempted != 5 || actual != 3 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("attempted=%d actual=%d err=%v", attempted, actual, err)
	}
}

type panickingResponseWriter struct {
	header           http.Header
	panicWriteHeader bool
	panicWrite       bool
	panicDeadline    bool
	status           int
	writeCalls       int
	body             bytes.Buffer
}

func (writer *panickingResponseWriter) Header() http.Header { return writer.header }
func (writer *panickingResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	if writer.panicWriteHeader {
		panic("WRITE_HEADER_PANIC_CANARY")
	}
}
func (writer *panickingResponseWriter) Write(payload []byte) (int, error) {
	writer.writeCalls++
	if writer.panicWrite {
		_, _ = writer.body.Write(payload[:min(7, len(payload))])
		panic("WRITE_PANIC_CANARY")
	}
	return writer.body.Write(payload)
}
func (writer *panickingResponseWriter) SetWriteDeadline(time.Time) error {
	if writer.panicDeadline {
		panic("DEADLINE_PANIC_CANARY")
	}
	return nil
}

func TestWriteJSONPayloadRecoversWriterPanicsAsFixedSentinel(t *testing.T) {
	tests := []struct {
		name   string
		writer *panickingResponseWriter
	}{
		{name: "WriteHeader", writer: &panickingResponseWriter{header: make(http.Header), panicWriteHeader: true}},
		{name: "Write after hidden partial mutation", writer: &panickingResponseWriter{header: make(http.Header), panicWrite: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempted, actual, err := writeJSONPayload(test.writer, http.StatusCreated, []byte("123456789"))
			if attempted != 9 || actual != 0 || !errors.Is(err, errResponseWritePanic) {
				t.Fatalf("attempted=%d actual=%d err=%v", attempted, actual, err)
			}
			if strings.Contains(err.Error(), "CANARY") {
				t.Fatalf("panic value escaped in error: %v", err)
			}
			if test.writer.status != http.StatusCreated {
				t.Fatalf("committed status=%d", test.writer.status)
			}
		})
	}
}

type countingDetailsChecker struct {
	counter *countingChecker
	details map[string]any
}

func (countingDetailsChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (checker countingDetailsChecker) Check(ctx context.Context, target diagnostic.Target) diagnostic.Result {
	result := checker.counter.Check(ctx, target)
	result.Details = checker.details
	return result
}

func TestFullResponseBudgetErrorReleasesAdmissionBeforeErrorWrite(t *testing.T) {
	checker := &countingChecker{}
	oversized := strings.Repeat("x", maxFullResponseStringBytes+1)
	handler, err := NewServerWithConfig(diagnostic.NewRunner(countingDetailsChecker{counter: checker, details: map[string]any{"value": oversized}}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{
		MaxConcurrentReports:        1,
		MaxConcurrentResponseWrites: 1,
		Mode:                        ModeTrustedLocal,
	})
	if err != nil {
		t.Fatal(err)
	}

	blocked := newBarrierResponseWriter()
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(blocked, reportRequest(context.Background()))
		close(firstDone)
	}()
	<-blocked.started

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, reportRequest(context.Background()))
	if second.Code != http.StatusInternalServerError || checker.callCount() != 2 {
		t.Fatalf("error write retained admission: status=%d calls=%d body=%s", second.Code, checker.callCount(), second.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &response); err != nil || response.Error.Code != "full_response_too_large" {
		t.Fatalf("response=%+v err=%v body=%q", response, err, second.Body.String())
	}
	close(blocked.release)
	<-firstDone
}

func (blockingChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (c blockingChecker) Check(ctx context.Context, target diagnostic.Target) diagnostic.Result {
	select {
	case c.started <- struct{}{}:
	case <-ctx.Done():
		return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusUnreachable, ErrorCode: "cancelled"}
	}
	select {
	case <-c.release:
		return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusHealthy}
	case <-ctx.Done():
		return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusUnreachable, ErrorCode: "cancelled"}
	}
}

func reportRequest(ctx context.Context) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", bytes.NewBufferString(`{"targets":[{"kind":"dns","address":"example.test"}]}`))
	return req.WithContext(ctx)
}

func TestReportAdmissionRejectsWhenFullAndRecoversAfterCompletion(t *testing.T) {
	checker := blockingChecker{started: make(chan struct{}, 2), release: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), logger, "test", ServerConfig{
		AllowedOrigins:       []string{"http://localhost:3000"},
		MaxConcurrentReports: 1,
		BusyRetryAfter:       7 * time.Second,
		Mode:                 ModeTrustedLocal,
	})
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.ServeHTTP(first, reportRequest(context.Background()))
	}()
	<-checker.started

	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, reportRequest(context.Background()))
	if busy.Code != http.StatusServiceUnavailable || busy.Header().Get("Retry-After") != "7" {
		t.Fatalf("busy status=%d retry=%q body=%s", busy.Code, busy.Header().Get("Retry-After"), busy.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(busy.Body).Decode(&response); err != nil || response.Error.Code != "server_busy" {
		t.Fatalf("busy response=%+v err=%v", response, err)
	}

	close(checker.release)
	wg.Wait()
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}

	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, reportRequest(context.Background()))
	if recovered.Code != http.StatusOK {
		t.Fatalf("capacity was not released: status=%d body=%s", recovered.Code, recovered.Body.String())
	}
}

func TestReportAdmissionRecoversAfterCancellation(t *testing.T) {
	checker := blockingChecker{started: make(chan struct{}, 2), release: make(chan struct{})}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{
		MaxConcurrentReports: 1,
		BusyRetryAfter:       time.Second,
		Mode:                 ModeTrustedLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(first, reportRequest(ctx))
		close(done)
	}()
	<-checker.started
	cancel()
	<-done

	close(checker.release)
	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, reportRequest(context.Background()))
	if recovered.Code != http.StatusOK {
		t.Fatalf("capacity was not released after cancellation: status=%d body=%s", recovered.Code, recovered.Body.String())
	}
}

func publicServerConfig() ServerConfig {
	return ServerConfig{
		Mode:                 ModePublic,
		APIKey:               "test-secret",
		RateLimitPerMinute:   10,
		MaxConcurrentReports: 1,
	}
}

func TestPublicModeRequiresAPIKeyAndPositiveRateLimit(t *testing.T) {
	runner := diagnostic.NewRunner(successChecker{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, config := range []ServerConfig{
		{Mode: ModePublic, RateLimitPerMinute: 1},
		{Mode: ModePublic, APIKey: "secret"},
		{Mode: ModePublic, APIKey: "secret", RateLimitPerMinute: -1},
	} {
		if _, err := NewServerWithConfig(runner, logger, "test", config); err == nil {
			t.Fatalf("public config %+v was accepted", config)
		}
	}
}

func TestPublicModeAuthenticatesEvenWhenCORSAllowsOrigin(t *testing.T) {
	config := publicServerConfig()
	config.AllowedOrigins = []string{"https://ui.example"}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(successChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", config)
	if err != nil {
		t.Fatal(err)
	}
	for _, authorization := range []string{"", "Bearer wrong"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set("Origin", "https://ui.example")
		req.Header.Set("Authorization", authorization)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("Access-Control-Allow-Origin") != "https://ui.example" {
			t.Fatalf("auth=%q status=%d cors=%q body=%s", authorization, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"), rec.Body.String())
		}
		var response struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&response)
		if response.Error.Code != "unauthorized" {
			t.Fatalf("response=%+v", response)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPublicModeRateLimitsEachClient(t *testing.T) {
	config := publicServerConfig()
	config.RateLimitPerMinute = 1
	handler, err := NewServerWithConfig(diagnostic.NewRunner(successChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", config)
	if err != nil {
		t.Fatal(err)
	}
	request := func(remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = remote
		req.Header.Set("Authorization", "Bearer test-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if got := request("198.51.100.10:1234"); got.Code != http.StatusOK {
		t.Fatalf("first status=%d", got.Code)
	}
	limited := request("198.51.100.10:5678")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("limited status=%d retry=%q body=%s", limited.Code, limited.Header().Get("Retry-After"), limited.Body.String())
	}
	if got := request("198.51.100.11:1234"); got.Code != http.StatusOK {
		t.Fatalf("other client status=%d", got.Code)
	}
}

func TestPublicModeBoundsRateLimitClientCardinality(t *testing.T) {
	config := publicServerConfig()
	config.MaxRateLimitClients = 1
	handler, err := NewServerWithConfig(diagnostic.NewRunner(successChecker{}), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", config)
	if err != nil {
		t.Fatal(err)
	}
	request := func(remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = remote
		req.Header.Set("Authorization", "Bearer test-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if got := request("[2001:db8::1]:1234"); got.Code != http.StatusOK {
		t.Fatalf("first status=%d", got.Code)
	}
	if got := request("[2001:db8::2]:1234"); got.Code != http.StatusTooManyRequests {
		t.Fatalf("new client bypassed cardinality bound: status=%d body=%s", got.Code, got.Body.String())
	}
}

type policyBlockedChecker struct{}

func (policyBlockedChecker) Kind() diagnostic.Kind { return diagnostic.KindDNS }
func (policyBlockedChecker) Check(_ context.Context, target diagnostic.Target) diagnostic.Result {
	return diagnostic.Result{Kind: target.Kind, Address: target.Address, Status: diagnostic.StatusUnreachable, ErrorCode: "network_policy_blocked", Message: "target is not allowed in public mode"}
}

func TestPublicPolicyBlockReturnsStablePrivacySafe422(t *testing.T) {
	var logs bytes.Buffer
	handler, err := NewServerWithConfig(diagnostic.NewRunner(policyBlockedChecker{}), slog.New(slog.NewTextHandler(&logs, nil)), "test", publicServerConfig())
	if err != nil {
		t.Fatal(err)
	}
	req := reportRequest(context.Background())
	req.URL.RawQuery = "token=query-secret"
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || bytes.Contains(rec.Body.Bytes(), []byte("example.test")) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{"example.test", "query-secret", "test-secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs leaked %q: %s", secret, logs.String())
		}
	}
	var response struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&response)
	if response.Error.Code != "network_policy_blocked" || response.Error.Message != "target is not allowed in public mode" {
		t.Fatalf("response=%+v", response)
	}
}
