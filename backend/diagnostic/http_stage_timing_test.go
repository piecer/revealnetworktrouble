package diagnostic

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"
)

// Phase duration arithmetic is asserted against an injected fake clock so the
// numbers are deterministic; the live path only asserts presence/absence,
// never exact timings, so loopback variance cannot make the gate flaky.

func TestStageTimingRecorderComputesPhaseDurationsFromInjectedClock(t *testing.T) {
	clockTime := time.Unix(1700000000, 0).UTC()
	clock := func() time.Time { return clockTime }
	observer := newStageTimingObserver(clock)
	trace := observer.trace()

	trace.DNSStart(httptrace.DNSStartInfo{Host: "example.test"})
	clockTime = clockTime.Add(120 * time.Millisecond)
	trace.DNSDone(httptrace.DNSDoneInfo{})

	trace.ConnectStart("tcp", "example.test:443")
	clockTime = clockTime.Add(50 * time.Millisecond)
	trace.ConnectDone("tcp", "example.test:443", nil)

	trace.TLSHandshakeStart()
	clockTime = clockTime.Add(300 * time.Millisecond)
	trace.TLSHandshakeDone(tls.ConnectionState{}, nil)

	trace.WroteRequest(httptrace.WroteRequestInfo{})
	clockTime = clockTime.Add(1700 * time.Millisecond)
	trace.GotFirstResponseByte()

	durations := observer.ObservedDurations()
	if len(durations) != 4 {
		t.Fatalf("expected four observed phases, got %+v", durations)
	}
	if durations[ResultDetailDNSPhaseMS] != 120 ||
		durations[ResultDetailConnectPhaseMS] != 50 ||
		durations[ResultDetailTLSPhaseMS] != 300 ||
		durations[ResultDetailTTFBPhaseMS] != 1700 {
		t.Fatalf("phase durations do not match the injected clock: %+v", durations)
	}
}

// A reused connection never fires ConnectStart, and a cached lookup never fires
// DNSStart; recording zero would be false precision, so unobserved phases must
// be absent.
func TestStageTimingRecorderOmitsUnobservedPhasesForReusedConnection(t *testing.T) {
	clockTime := time.Unix(1700000000, 0).UTC()
	clock := func() time.Time { return clockTime }
	observer := newStageTimingObserver(clock)
	trace := observer.trace()

	trace.WroteRequest(httptrace.WroteRequestInfo{})
	trace.GotFirstResponseByte()

	durations := observer.ObservedDurations()
	if len(durations) != 1 {
		t.Fatalf("only TTFB should be observed on a reused connection, got %+v", durations)
	}
	if _, ok := durations[ResultDetailTTFBPhaseMS]; !ok {
		t.Fatalf("TTFB must be observed: %+v", durations)
	}
	for _, key := range []string{ResultDetailDNSPhaseMS, ResultDetailConnectPhaseMS, ResultDetailTLSPhaseMS} {
		if _, exists := durations[key]; exists {
			t.Fatalf("unobserved phase %q must not be recorded: %+v", key, durations)
		}
	}
}

// A failed phase has no duration to report; the failure is already the result
// outcome, so the timing block must stay empty instead of recording partial work.
func TestStageTimingRecorderKeepsNoPhaseForFailedPhases(t *testing.T) {
	observer := newStageTimingObserver(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	trace := observer.trace()

	trace.DNSStart(httptrace.DNSStartInfo{Host: "example.test"})
	trace.DNSDone(httptrace.DNSDoneInfo{Err: errors.New("lookup failed")})
	trace.ConnectStart("tcp", "example.test:443")
	trace.ConnectDone("tcp", "example.test:443", errors.New("dial failed"))
	trace.TLSHandshakeStart()
	trace.TLSHandshakeDone(tls.ConnectionState{}, errors.New("handshake failed"))
	trace.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("write failed")})
	trace.GotFirstResponseByte()

	if len(observer.ObservedDurations()) != 0 {
		t.Fatalf("failed phases must not record durations: %+v", observer.ObservedDurations())
	}
}

// The live path fires the real httptrace events over loopback. Only presence is
// asserted: an IP literal performs no DNS lookup and plain HTTP performs no TLS
// handshake, so exactly connect and TTFB must appear.
func TestHTTPCheckerLivePathRecordsConnectAndTTFBPhases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result := (HTTPChecker{}).Check(context.Background(), Target{Kind: KindHTTP, Address: server.URL})
	if result.Status != StatusHealthy {
		t.Fatalf("unexpected result: %+v", result)
	}
	blockValue, ok := result.Details[ResultDetailStageTiming]
	if !ok {
		t.Fatalf("live HTTP result must carry the stage timing block, got %+v", result.Details)
	}
	block, ok := blockValue.(map[string]any)
	if !ok {
		t.Fatalf("stage timing block must be an object, got %#v", blockValue)
	}
	if _, ok := block[ResultDetailConnectPhaseMS]; !ok {
		t.Fatalf("connect phase must be observed over loopback: %+v", block)
	}
	if _, ok := block[ResultDetailTTFBPhaseMS]; !ok {
		t.Fatalf("TTFB phase must be observed over loopback: %+v", block)
	}
	for _, key := range []string{ResultDetailDNSPhaseMS, ResultDetailTLSPhaseMS} {
		if _, exists := block[key]; exists {
			t.Fatalf("phase %q must not be recorded for an IP-literal plain HTTP check: %+v", key, block)
		}
	}
	for key, value := range block {
		number, ok := value.(int64)
		if !ok || number < 0 {
			t.Fatalf("phase %q must be a non-negative integer, got %#v", key, value)
		}
	}
}
