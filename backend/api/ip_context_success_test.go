package api

import (
	"context"
	"encoding/json"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type contextResolver struct{ calls atomic.Int32 }

func (r *contextResolver) LookupAddr(ctx context.Context, address string) ([]string, error) {
	r.calls.Add(1)
	return []string{"ONE.Example."}, nil
}
func (r *contextResolver) LookupIPAddr(ctx context.Context, name string) ([]net.IPAddr, error) {
	r.calls.Add(1)
	if name != "one.example." {
		panic("not absolute")
	}
	return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
}
func TestIPContextHTTPReverseSuccess(t *testing.T) {
	resolver := &contextResolver{}
	var sends atomic.Int32
	service := diagnostic.NewIPContextService(diagnostic.IPContextOptions{Resolver: resolver, HTTP: func(ctx context.Context, url string) (int, []byte, error) { sends.Add(1); return 404, nil, nil }})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	checker := &ipContextChecker{}
	handler, err := NewServerWithConfig(diagnostic.NewRunner(checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{IPContext: service})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	response, err := srv.Client().Post(srv.URL+"/api/v1/ip-context", "application/json", strings.NewReader(`{"address":"1.1.1.1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	var got diagnostic.IPContext
	if err = json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Address != "1.1.1.1" || got.Source != "upstream" || got.ReverseDNS.Status != "ok" || len(got.ReverseDNS.Names) != 1 || got.ReverseDNS.Names[0].Name != "one.example" || got.ReverseDNS.Names[0].ForwardStatus != "confirmed" || got.Registration.Status != "not_found" || got.Routing.Status != "unavailable" {
		t.Fatalf("wrong snapshot: %s", body)
	}
	if resolver.calls.Load() != 2 || sends.Load() != 2 || checker.calls.Load() != 0 {
		t.Fatalf("calls DNS=%d HTTP=%d Runner=%d", resolver.calls.Load(), sends.Load(), checker.calls.Load())
	}
	if len(body) > 16384 || body[len(body)-1] != '\n' || strings.Contains(string(body), "null") {
		t.Fatalf("not bounded canonical: %s", body)
	}
}
