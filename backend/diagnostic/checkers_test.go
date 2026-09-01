package diagnostic

import (
	"context"
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
