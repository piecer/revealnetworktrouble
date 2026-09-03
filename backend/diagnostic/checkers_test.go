package diagnostic

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const httpBodySampleLimit = 32 * 1024

type readCloseStub struct {
	reader   io.Reader
	closeErr error
}

func (body *readCloseStub) Read(payload []byte) (int, error) { return body.reader.Read(payload) }
func (body *readCloseStub) Close() error                     { return body.closeErr }

type bytesThenErrorReader struct {
	remaining int
	err       error
}

func (reader *bytesThenErrorReader) Read(payload []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, reader.err
	}
	n := min(len(payload), reader.remaining)
	for index := 0; index < n; index++ {
		payload[index] = 'x'
	}
	reader.remaining -= n
	return n, nil
}

type pipeDialer struct{}

func (pipeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		<-time.After(time.Second)
		_ = server.Close()
	}()
	return client, nil
}

func TestTCPCheckerWithLocalListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
	}()
	result := (TCPChecker{}).Check(context.Background(), Target{Kind: KindTCP, Address: listener.Addr().String()})
	if result.Status != StatusHealthy {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestHTTPCheckerExpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	result := (HTTPChecker{}).Check(context.Background(), Target{Kind: KindHTTP, Address: server.URL, ExpectedStatus: http.StatusNoContent})
	if result.Status != StatusHealthy {
		t.Fatalf("unexpected result: %+v", result)
	}
	failed := (HTTPChecker{}).Check(context.Background(), Target{Kind: KindHTTP, Address: server.URL, ExpectedStatus: http.StatusOK})
	if failed.ErrorCode != "unexpected_status" {
		t.Fatalf("unexpected mismatch result: %+v", failed)
	}
}

func TestHTTPCheckerBodyReadDeadlineIsNotHealthyAndIncludesObservationLatency(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), MinTimeout)
	defer cancel()
	result := (HTTPChecker{}).Check(ctx, Target{Kind: KindHTTP, Address: server.URL})
	if result.Status != StatusUnreachable || result.ErrorCode != "timeout" {
		t.Fatalf("result=%+v", result)
	}
	if result.LatencyMS < MinTimeout.Milliseconds()-20 {
		t.Fatalf("latency=%dms does not include bounded body observation", result.LatencyMS)
	}
	analysis := Analyze([]Result{result}, analysisTestNow)
	if analysis.Verdict == VerdictHealthy || len(analysis.Findings) == 0 || analysis.Findings[0].Code != FindingExecutionTimeout {
		t.Fatalf("analysis=%+v", analysis)
	}
}

func TestHTTPCheckerBodyReadCancellationHasStableCancelledResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: http.StatusOK, Proto: "HTTP/1.1", Header: make(http.Header), Body: &readCloseStub{reader: &bytesThenErrorReader{err: context.Canceled}}}, nil
	})}
	result := (HTTPChecker{Client: client}).Check(ctx, Target{Kind: KindHTTP, Address: "http://example.test"})
	if result.Status != StatusUnreachable || result.ErrorCode != "cancelled" {
		t.Fatalf("result=%+v", result)
	}
}

func TestHTTPCheckerBodyReadFailureIsStableAndDoesNotReflectErrorProse(t *testing.T) {
	const canary = "BODY_READ_ERROR_CANARY"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := &readCloseStub{reader: &bytesThenErrorReader{remaining: 10, err: errors.New(canary)}}
		return &http.Response{StatusCode: http.StatusOK, Proto: "HTTP/1.1", Header: make(http.Header), Body: body}, nil
	})}
	result := (HTTPChecker{Client: client}).Check(context.Background(), Target{Kind: KindHTTP, Address: "http://example.test"})
	if result.Status != StatusUnreachable || result.ErrorCode != "response_read_failed" || strings.Contains(result.Message, canary) {
		t.Fatalf("result=%+v", result)
	}
	analysis := Analyze([]Result{result}, analysisTestNow)
	if analysis.Verdict != VerdictInconclusive || len(analysis.Coverage.Limitations) == 0 {
		t.Fatalf("analysis=%+v", analysis)
	}
}

func TestHTTPCheckerBoundedSampleDoesNotRequireEOFAt32KiB(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := &readCloseStub{reader: &bytesThenErrorReader{remaining: httpBodySampleLimit, err: errors.New("must not be observed")}}
		return &http.Response{StatusCode: http.StatusOK, Proto: "HTTP/1.1", Header: make(http.Header), Body: body}, nil
	})}
	result := (HTTPChecker{Client: client}).Check(context.Background(), Target{Kind: KindHTTP, Address: "http://example.test"})
	if result.Status != StatusHealthy {
		t.Fatalf("result=%+v", result)
	}
}

func TestHTTPCheckerBodyCloseErrorDoesNotOverrideCompletedObservation(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := &readCloseStub{reader: strings.NewReader("complete"), closeErr: errors.New("close canary")}
		return &http.Response{StatusCode: http.StatusOK, Proto: "HTTP/1.1", Header: make(http.Header), Body: body}, nil
	})}
	result := (HTTPChecker{Client: client}).Check(context.Background(), Target{Kind: KindHTTP, Address: "http://example.test"})
	if result.Status != StatusHealthy || result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestHTTPSCheckerReportsStableTLSHandshakeFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	result := (HTTPSChecker{}).Check(context.Background(), Target{Kind: KindHTTPS, Address: server.URL})
	if result.Status != StatusUnreachable || result.ErrorCode != "tls_handshake_failed" {
		t.Fatalf("result = %+v", result)
	}
}

func TestHTTPSCheckerReportsTLSDetails(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	result := (HTTPSChecker{Client: server.Client()}).Check(context.Background(), Target{Kind: KindHTTPS, Address: server.URL})
	if result.Status != StatusHealthy || result.Details["tls_version"] == nil || result.Details["certificate_expires_at"] == nil {
		t.Fatalf("unexpected HTTPS result: %+v", result)
	}
}

func TestHTTPSRejectsHTTPURL(t *testing.T) {
	if result := (HTTPSChecker{}).Check(context.Background(), Target{Kind: KindHTTPS, Address: "http://example.test"}); result.ErrorCode != "invalid_url" {
		t.Fatalf("HTTPS accepted HTTP URL: %+v", result)
	}
}

func TestHTTPSRejectsRedirectDowngradeWithStableError(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	defer secure.Close()

	result := (HTTPSChecker{Client: secure.Client()}).Check(context.Background(), Target{Kind: KindHTTPS, Address: secure.URL})
	if result.Status != StatusUnreachable || result.ErrorCode != "tls_downgrade" || result.Message != "HTTPS redirect or response did not preserve TLS" {
		t.Fatalf("downgrade result: %+v", result)
	}
}

func TestServiceCheckerUsesDefaultAndExplicitPorts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
	}()
	result := (ServiceChecker{ServiceKind: KindSSH, DefaultPort: 22}).Check(context.Background(), Target{Kind: KindSSH, Address: listener.Addr().String()})
	if result.Status != StatusHealthy || result.Details["endpoint"] != listener.Addr().String() {
		t.Fatalf("unexpected service result: %+v", result)
	}
	if got := serviceAddress("example.test", 22); got != "example.test:22" {
		t.Fatalf("serviceAddress() = %q", got)
	}
}

func TestTLSServiceCheckerReportsHandshake(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	address := server.Listener.Addr().String()
	result := (ServiceChecker{ServiceKind: KindIMAPS, DefaultPort: 993, UseTLS: true, TLSConfig: &tls.Config{InsecureSkipVerify: true}}).Check(context.Background(), Target{Kind: KindIMAPS, Address: address})
	if result.Status != StatusHealthy || result.Details["tls_version"] == nil {
		t.Fatalf("unexpected TLS service result: %+v", result)
	}
}

func TestTLSServiceCheckerPreservesHandshakeTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := (ServiceChecker{ServiceKind: KindIMAPS, DefaultPort: 993, UseTLS: true, Dialer: pipeDialer{}}).Check(ctx, Target{Kind: KindIMAPS, Address: "example.test"})
	if result.ErrorCode != "timeout" {
		t.Fatalf("result = %+v", result)
	}
}

func TestTLSServiceCheckerReportsStableHandshakeFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_, _ = conn.Write([]byte("not tls"))
			_ = conn.Close()
		}
	}()
	result := (ServiceChecker{ServiceKind: KindIMAPS, DefaultPort: 993, UseTLS: true}).Check(context.Background(), Target{Kind: KindIMAPS, Address: listener.Addr().String()})
	if result.Status != StatusUnreachable || result.ErrorCode != "tls_handshake_failed" {
		t.Fatalf("result = %+v", result)
	}
}
