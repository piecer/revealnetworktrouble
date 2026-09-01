package diagnostic

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
