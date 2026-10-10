package diagnostic

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type contextDialFunc func(context.Context, string, string) (net.Conn, error)

func (f contextDialFunc) DialContext(c context.Context, n, a string) (net.Conn, error) {
	return f(c, n, a)
}

type contextPolicyFunc func(context.Context, string) ([]net.IPAddr, error)

func (f contextPolicyFunc) LookupIPAddr(c context.Context, h string) ([]net.IPAddr, error) {
	return f(c, h)
}
func contextTLSFixture(t *testing.T, handler http.Handler) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture"}, DNSNames: []string{"rdap.apnic.net", "stat.ripe.net"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, roots
}
func TestIPContextRealHTTPSAdapter(t *testing.T) {
	var sends atomic.Int32
	server, roots := contextTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.TLS == nil || r.TLS.ServerName != r.Host {
			t.Error("Host/SNI lost")
		}
		if r.Host == "rdap.apnic.net" {
			io.WriteString(w, contextRDAPFixture)
		} else {
			io.WriteString(w, `{"status":"ok","data":{"prefix":"","asns":[]}}`)
		}
	}))
	var dials atomic.Int32
	s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, PolicyResolver: contextPolicyFunc(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
	}), Dialer: contextDialFunc(func(c context.Context, n, a string) (net.Conn, error) {
		dials.Add(1)
		if a != "1.1.1.1:443" {
			t.Error(a)
		}
		return (&net.Dialer{}).DialContext(c, n, server.Listener.Addr().String())
	}), TLSRoots: roots})
	got, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Registration.Status != "ok" || got.Routing.Status != "not_found" || sends.Load() != 2 || dials.Load() != 2 {
		t.Fatalf("got %+v sends=%d dials=%d", got, sends.Load(), dials.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

type contextLateConn struct {
	net.Conn
	closed *atomic.Int32
}

func (c *contextLateConn) Close() error { c.closed.Add(1); return c.Conn.Close() }
func TestIPContextRealAdapterNoncooperativeOwnership(t *testing.T) {
	for _, stage := range []string{"policy-resolver", "underlying-dialer"} {
		t.Run(stage, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var callbacks, dials, closed atomic.Int32
			resolver := contextPolicyFunc(func(context.Context, string) ([]net.IPAddr, error) {
				callbacks.Add(1)
				if stage == "policy-resolver" {
					<-release
				}
				return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("8.8.8.8")}}, nil
			})
			dialer := contextDialFunc(func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				<-release
				a, b := net.Pipe()
				b.Close()
				return &contextLateConn{a, &closed}, nil
			})
			s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, PolicyResolver: resolver, Dialer: dialer})
			for _, address := range []string{"1.1.1.1", "8.8.8.8"} {
				got, err := s.Lookup(context.Background(), address, time.Now().Add(-5800*time.Millisecond))
				if err != nil || got.Registration.Status != "timeout" || got.Routing.Status != "timeout" {
					t.Fatalf("timeout snapshot %+v err=%v", got, err)
				}
			}
			if callbacks.Load() != 4 {
				t.Fatalf("policy callbacks=%d want 4", callbacks.Load())
			}
			_, err := s.Lookup(context.Background(), "9.9.9.9", time.Now())
			if !errors.Is(err, ErrIPContextBusy) || callbacks.Load() != 4 {
				t.Fatalf("third callback admitted %v %d", err, callbacks.Load())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if s.Close(ctx) == nil {
				t.Fatal("false complete shutdown")
			}
			once.Do(func() { close(release) })
			ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
			defer cancel2()
			if err = s.Close(ctx2); err != nil {
				t.Fatal(err)
			}
			if stage == "policy-resolver" && dials.Load() != 0 {
				t.Fatal("dial after late resolve")
			}
			if stage == "underlying-dialer" && (dials.Load() != 4 || closed.Load() != 4) {
				t.Fatalf("replacement/late-close dials=%d closed=%d", dials.Load(), closed.Load())
			}
		})
	}
}
func TestIPContextRealAdapterRejectsMixedAnswers(t *testing.T) {
	var dials atomic.Int32
	s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, PolicyResolver: contextPolicyFunc(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}), Dialer: contextDialFunc(func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("PRIVATE_CANARY")
	})})
	v, e := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	b, _ := MarshalIPContext(v)
	if e != nil || dials.Load() != 0 || strings.Contains(string(b), "PRIVATE_CANARY") || v.Registration.Status != "unavailable" {
		t.Fatalf("got %+v %v dials=%d", v, e, dials.Load())
	}
}
