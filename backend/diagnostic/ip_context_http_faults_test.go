package diagnostic

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func contextFixtureOptions(server *httptest.Server) IPContextOptions {
	return IPContextOptions{Resolver: contextTestResolver{}, PolicyResolver: contextPolicyFunc(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
	}), Dialer: contextDialFunc(func(c context.Context, n, a string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(c, n, server.Listener.Addr().String())
	})}
}
func TestIPContextRealHTTPNon200Classification(t *testing.T) {
	server, roots := contextTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "rdap.apnic.net" {
			io.WriteString(w, contextRDAPFixture)
			return
		}
		w.Header().Set("Content-Length", "262145")
		w.WriteHeader(201)
	}))
	options := contextFixtureOptions(server)
	options.TLSRoots = roots
	s := NewIPContextService(options)
	v, e := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if e != nil || v.Routing.Status != "unavailable" || v.Registration.Status != "ok" {
		t.Fatalf("non200 mapping %+v %v", v, e)
	}
	contextWaitIdle(t, s)
}
func TestIPContextRealHTTPNoRedirectRetryProxyOrInsecureFallback(t *testing.T) {
	for _, mode := range []string{"redirect", "untrusted-tls", "early-eof", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			var sends, proxyHits, dials atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyHits.Add(1) }))
			defer proxy.Close()
			t.Setenv("HTTPS_PROXY", proxy.URL)
			t.Setenv("HTTP_PROXY", proxy.URL)
			server, roots := contextTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				if mode == "redirect" {
					w.Header().Set("Location", proxy.URL)
					w.Header().Set("Content-Length", "999999999")
					w.WriteHeader(302)
					return
				}
				if mode == "early-eof" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				io.WriteString(w, `{}`)
			}))
			options := contextFixtureOptions(server)
			dial := options.Dialer
			options.Dialer = contextDialFunc(func(c context.Context, n, a string) (net.Conn, error) { dials.Add(1); return dial.DialContext(c, n, a) })
			if mode != "untrusted-tls" {
				options.TLSRoots = roots
			}
			s := NewIPContextService(options)
			v, e := s.Lookup(context.Background(), "1.1.1.1", time.Now())
			if e != nil {
				t.Fatal(e)
			}
			contextWaitIdle(t, s)
			wantSends := int32(2)
			if mode == "untrusted-tls" {
				wantSends = 0
			}
			if dials.Load() != 2 || sends.Load() != wantSends || proxyHits.Load() != 0 {
				t.Fatalf("dials=%d sends=%d proxy=%d", dials.Load(), sends.Load(), proxyHits.Load())
			}
			if mode != "proxy" && (v.Registration.Status != "unavailable" || v.Routing.Status != "unavailable") {
				t.Fatalf("failure mapping %+v", v)
			}
		})
	}
}

type contextBlockedReadConn struct {
	net.Conn
	armed   *atomic.Bool
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
	closed  *atomic.Int32
}

func (c *contextBlockedReadConn) Read(p []byte) (int, error) {
	if c.armed.Load() {
		c.once.Do(func() { c.entered <- struct{}{} })
		<-c.release
	}
	return c.Conn.Read(p)
}
func (c *contextBlockedReadConn) Close() error { c.closed.Add(1); return c.Conn.Close() }
func TestIPContextRealHTTPBodyCallbackRetainsLease(t *testing.T) {
	var armed atomic.Bool
	var closed atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	entered := make(chan struct{}, 4)
	server, roots := contextTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		armed.Store(true)
		<-r.Context().Done()
	}))
	options := contextFixtureOptions(server)
	baseDial := options.Dialer
	options.Dialer = contextDialFunc(func(c context.Context, n, a string) (net.Conn, error) {
		conn, err := baseDial.DialContext(c, n, a)
		if err != nil {
			return nil, err
		}
		return &contextBlockedReadConn{Conn: conn, armed: &armed, entered: entered, release: release, closed: &closed}, nil
	})
	options.TLSRoots = roots
	s := NewIPContextService(options)
	done := make(chan IPContext, 1)
	go func() {
		v, _ := s.Lookup(context.Background(), "1.1.1.1", time.Now().Add(-5700*time.Millisecond))
		done <- v
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("body callback not blocked")
	}
	v := <-done
	if v.Registration.Status != "timeout" || v.Routing.Status != "timeout" {
		t.Fatalf("snapshot %+v", v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("false complete %v", err)
	}
	once.Do(func() { close(release) })
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := s.Close(ctx2); err != nil {
		t.Fatal(err)
	}
	if closed.Load() < 2 {
		t.Fatal("late bodies/connections not closed")
	}
}
