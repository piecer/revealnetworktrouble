package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type ipContextPolicyResolver struct{}

func (ipContextPolicyResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
}

type ipContextMappedDialer struct{ address string }

func (d ipContextMappedDialer) DialContext(c context.Context, n, a string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(c, n, d.address)
}
func TestIPContextActualAPIToControlledTLSProviders(t *testing.T) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"rdap.apnic.net", "stat.ripe.net"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	var sends atomic.Int32
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.Header.Get("Authorization") != "" || r.TLS == nil || r.TLS.ServerName != r.Host {
			t.Error("credential leak or SNI mismatch")
		}
		switch r.URL.Path {
		case "/ip/1.1.1.1":
			io.WriteString(w, `{"objectClassName":"ip network","startAddress":"1.1.1.0","endAddress":"1.1.1.255","name":"Fixture Network"}`)
		case "/data/network-info/data.json":
			if r.URL.Query().Get("resource") != "1.1.1.1" {
				t.Error("wrong BGP resource")
			}
			io.WriteString(w, `{"status":"ok","data":{"prefix":"1.1.1.0/24","asns":[13335,13336]}}`)
		case "/data/rpki-validation/data.json":
			validity := "valid"
			if r.URL.Query().Get("resource") == "13336" {
				validity = "invalid_asn"
			}
			if r.URL.Query().Get("prefix") != "1.1.1.0/24" {
				t.Error("wrong RPKI prefix")
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": map[string]any{"resource": r.URL.Query().Get("resource"), "prefix": r.URL.Query().Get("prefix"), "status": validity}})
		default:
			t.Error("unapproved provider path")
			w.WriteHeader(404)
		}
	}))
	provider.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	provider.StartTLS()
	defer provider.Close()
	service := diagnostic.NewIPContextService(diagnostic.IPContextOptions{Resolver: &contextResolver{}, PolicyResolver: ipContextPolicyResolver{}, Dialer: ipContextMappedDialer{provider.Listener.Addr().String()}, TLSRoots: roots})
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := service.Close(c); e != nil {
			t.Error(e)
		}
	}()
	checker := &ipContextChecker{}
	handler, e := NewServerWithConfig(diagnostic.NewRunner(checker), slog.New(slog.NewTextHandler(io.Discard, nil)), "test", ServerConfig{IPContext: service, Mode: ModePublic, APIKey: "AUTH_PRIVATE_CANARY", RateLimitPerMinute: 10, AllowedOrigins: []string{"https://client.example"}})
	if e != nil {
		t.Fatal(e)
	}
	api := httptest.NewServer(handler)
	defer api.Close()
	checks := func() []byte {
		req, _ := http.NewRequest("GET", api.URL+"/api/v1/checks", nil)
		req.Header.Set("Authorization", "Bearer AUTH_PRIVATE_CANARY")
		res, e := api.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	before := string(checks())
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("POST", api.URL+"/api/v1/ip-context", strings.NewReader(`{"address":"1.1.1.1"}`))
		req.Header.Set("Authorization", "Bearer AUTH_PRIVATE_CANARY")
		req.Header.Set("Origin", "https://client.example")
		res, e := api.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		v, e := diagnostic.ParseIPContext(b, "1.1.1.1")
		if e != nil || res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != "https://client.example" {
			t.Fatalf("response=%d %s %v", res.StatusCode, b, e)
		}
		want := "upstream"
		if i == 1 {
			want = "cache"
		}
		if v.Source != want || v.Registration.Status != "ok" || v.Routing.Prefix != "1.1.1.0/24" || len(v.Routing.Origins) != 2 || v.Routing.Origins[0].RPKI.Validity != "valid" || v.Routing.Origins[1].RPKI.Validity != "invalid_asn" {
			t.Fatalf("snapshot %+v", v)
		}
	}
	if sends.Load() != 4 || checker.calls.Load() != 0 || before != string(checks()) {
		t.Fatalf("provider calls=%d Runner=%d or checks mutation", sends.Load(), checker.calls.Load())
	}
}
