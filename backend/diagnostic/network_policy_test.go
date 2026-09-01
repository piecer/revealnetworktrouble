package diagnostic

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type resolverSequence struct {
	mu        sync.Mutex
	addresses [][]net.IPAddr
	calls     int
}

func (r *resolverSequence) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.calls
	r.calls++
	if index >= len(r.addresses) {
		index = len(r.addresses) - 1
	}
	return append([]net.IPAddr(nil), r.addresses[index]...), nil
}

type dialCall struct {
	network string
	address string
}

type recordingDialer struct {
	mu    sync.Mutex
	calls []dialCall
}

func (d *recordingDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.calls = append(d.calls, dialCall{network: network, address: address})
	d.mu.Unlock()
	return nil, errors.New("recording dialer stopped connection")
}

func ipAnswers(values ...string) []net.IPAddr {
	answers := make([]net.IPAddr, 0, len(values))
	for _, value := range values {
		answers = append(answers, net.IPAddr{IP: net.ParseIP(value)})
	}
	return answers
}

func TestPublicNetworkPolicyRejectsNonPublicRanges(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1":            false,
		"10.0.0.1":             false,
		"169.254.169.254":      false,
		"224.0.0.1":            false,
		"0.0.0.0":              false,
		"192.0.2.1":            false,
		"198.51.100.1":         false,
		"203.0.113.1":          false,
		"100.100.100.200":      false,
		"::1":                  false,
		"fe80::1":              false,
		"ff02::1":              false,
		"::":                   false,
		"2001:db8::1":          false,
		"64:ff9b::127.0.0.1":   false,
		"64:ff9b:1::7f00:1":    false,
		"100::1":               false,
		"100:0:0:1::1":         false,
		"2001::1":              false,
		"2620:4f:8000::1":      false,
		"2002:7f00:1::":        false,
		"3fff::1":              false,
		"5f00::1":              false,
		"fd00:ec2::254":        false,
		"8.8.8.8":              true,
		"2606:4700:4700::1111": true,
	}
	for address, want := range tests {
		if got := IsPublicDiagnosticIP(net.ParseIP(address)); got != want {
			t.Errorf("IsPublicDiagnosticIP(%q)=%v want %v", address, got, want)
		}
	}
}

func TestPublicNetworkPolicyDialsVettedIPAndRechecksDNS(t *testing.T) {
	resolver := &resolverSequence{addresses: [][]net.IPAddr{
		ipAnswers("93.184.216.34"),
		ipAnswers("127.0.0.1"),
	}}
	dialer := &recordingDialer{}
	policy := NewNetworkPolicy(resolver, dialer)

	_, firstErr := policy.DialContext(context.Background(), "tcp", "example.test:443")
	if firstErr == nil || errors.Is(firstErr, ErrNetworkPolicyBlocked) {
		t.Fatalf("first dial error=%v", firstErr)
	}
	if len(dialer.calls) != 1 || dialer.calls[0].address != "93.184.216.34:443" {
		t.Fatalf("dial calls=%+v", dialer.calls)
	}

	_, secondErr := policy.DialContext(context.Background(), "tcp", "example.test:443")
	if !errors.Is(secondErr, ErrNetworkPolicyBlocked) {
		t.Fatalf("rebound address was not blocked: %v", secondErr)
	}
	if len(dialer.calls) != 1 {
		t.Fatalf("blocked IP reached dialer: %+v", dialer.calls)
	}
	if secondErr.Error() != "target is not allowed in public mode" {
		t.Fatalf("policy error leaked target details: %q", secondErr)
	}
}

func TestPublicPolicyBlocksEveryDiagnosticSink(t *testing.T) {
	newPolicy := func() *NetworkPolicy {
		return NewNetworkPolicy(&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("127.0.0.1")}}, &recordingDialer{})
	}
	checks := []struct {
		name string
		run  func() Result
	}{
		{"dns", func() Result {
			return (DNSChecker{Policy: newPolicy()}).Check(context.Background(), Target{Kind: KindDNS, Address: "internal.test"})
		}},
		{"tcp", func() Result {
			return (TCPChecker{Policy: newPolicy()}).Check(context.Background(), Target{Kind: KindTCP, Address: "internal.test:80"})
		}},
		{"service", func() Result {
			return (ServiceChecker{ServiceKind: KindSSH, DefaultPort: 22, Policy: newPolicy()}).Check(context.Background(), Target{Kind: KindSSH, Address: "internal.test"})
		}},
		{"http", func() Result {
			return (HTTPChecker{Policy: newPolicy()}).Check(context.Background(), Target{Kind: KindHTTP, Address: "http://internal.test"})
		}},
		{"traceroute", func() Result {
			called := false
			result := (TracerouteChecker{Policy: newPolicy(), Command: func(context.Context, string, ...string) ([]byte, error) {
				called = true
				return nil, nil
			}}).Check(context.Background(), Target{Kind: KindTraceroute, Address: "internal.test", Attempts: 1})
			if called {
				t.Error("blocked traceroute invoked command")
			}
			return result
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			result := check.run()
			if result.Status != StatusUnreachable || result.ErrorCode != "network_policy_blocked" || result.Message != "target is not allowed in public mode" {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

type mappingDialer struct {
	target string
	mu     sync.Mutex
	calls  []dialCall
}

func (d *mappingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.calls = append(d.calls, dialCall{network: network, address: address})
	d.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, d.target)
}

func TestPublicHTTPPreservesHostAndSNIWhileDialingVettedIP(t *testing.T) {
	hosts := make(chan string, 1)
	sni := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts <- r.Host
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		sni <- hello.ServerName
		return nil, nil
	}}
	server.StartTLS()
	defer server.Close()

	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.InsecureSkipVerify = true
	client.Transport = transport
	dialer := &mappingDialer{target: server.Listener.Addr().String()}
	policy := NewNetworkPolicy(&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("93.184.216.34")}}, dialer)
	result := (HTTPSChecker{Client: client, Policy: policy}).Check(context.Background(), Target{Kind: KindHTTPS, Address: "https://secure.test/health"})
	if result.Status != StatusHealthy {
		t.Fatalf("result=%+v", result)
	}
	if got := <-hosts; got != "secure.test" {
		t.Fatalf("Host=%q", got)
	}
	if got := <-sni; got != "secure.test" {
		t.Fatalf("SNI=%q", got)
	}
	if len(dialer.calls) != 1 || dialer.calls[0].address != "93.184.216.34:443" {
		t.Fatalf("dial calls=%+v", dialer.calls)
	}
}

func TestPublicHTTPRechecksRedirectAtDecisionAndDial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "first.test" {
			http.Redirect(w, r, "http://second.test/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	resolver := &resolverSequence{addresses: [][]net.IPAddr{
		ipAnswers("93.184.216.34"),
		ipAnswers("93.184.216.35"),
		ipAnswers("127.0.0.1"),
	}}
	dialer := &mappingDialer{target: server.Listener.Addr().String()}
	policy := NewNetworkPolicy(resolver, dialer)
	result := (HTTPChecker{Policy: policy}).Check(context.Background(), Target{Kind: KindHTTP, Address: "http://first.test/start"})
	if result.ErrorCode != "network_policy_blocked" {
		t.Fatalf("redirect rebinding result=%+v", result)
	}
	if resolver.calls != 3 {
		t.Fatalf("resolver calls=%d, want initial dial + redirect decision + redirect dial", resolver.calls)
	}
	if len(dialer.calls) != 1 {
		t.Fatalf("rebound redirect reached dialer: %+v", dialer.calls)
	}
}

func TestPublicTracerouteRechecksResolutionForEveryAttempt(t *testing.T) {
	resolver := &resolverSequence{addresses: [][]net.IPAddr{
		ipAnswers("93.184.216.34"),
		ipAnswers("127.0.0.1"),
	}}
	commands := 0
	checker := TracerouteChecker{
		Policy: NewNetworkPolicy(resolver, &recordingDialer{}),
		Command: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			commands++
			return []byte("traceroute to example.test (93.184.216.34), 30 hops max\n1  93.184.216.34  1.0 ms"), nil
		},
	}
	result := checker.Check(context.Background(), Target{Kind: KindTraceroute, Address: "example.test", Attempts: 2})
	if result.ErrorCode != "network_policy_blocked" || commands != 1 || resolver.calls != 2 {
		t.Fatalf("result=%+v commands=%d resolver_calls=%d", result, commands, resolver.calls)
	}
}
