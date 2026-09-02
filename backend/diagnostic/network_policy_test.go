package diagnostic

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

type dialFunc func(context.Context, string, string) (net.Conn, error)

func (fn dialFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return fn(ctx, network, address)
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

func TestPublicNetworkPolicyRejectsMixedAnswersBeforeDial(t *testing.T) {
	for _, answers := range [][]net.IPAddr{
		ipAnswers("127.0.0.1", "8.8.8.8"),
		ipAnswers("8.8.8.8", "127.0.0.1"),
	} {
		dialer := &recordingDialer{}
		resolver := &resolverSequence{addresses: [][]net.IPAddr{answers}}
		policy := NewNetworkPolicy(resolver, dialer)
		_, err := policy.DialContext(context.Background(), "tcp", "mixed.test:443")
		if !errors.Is(err, ErrNetworkPolicyBlocked) {
			t.Fatalf("answers=%v err=%v", answers, err)
		}
		if resolver.calls != 1 {
			t.Fatalf("answers=%v resolver calls=%d, want one snapshot", answers, resolver.calls)
		}
		if len(dialer.calls) != 0 {
			t.Fatalf("answers=%v reached dialer: %+v", answers, dialer.calls)
		}
	}
}

func TestPublicNetworkPolicyTriesVettedAnswersSequentially(t *testing.T) {
	var calls []string
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	firstFailure := errors.New("first address failed")
	policy := NewNetworkPolicy(
		&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("8.8.8.8", "1.1.1.1")}},
		dialFunc(func(_ context.Context, _ string, address string) (net.Conn, error) {
			calls = append(calls, address)
			if len(calls) == 1 {
				return nil, firstFailure
			}
			return client, nil
		}),
	)
	conn, err := policy.DialContext(context.Background(), "tcp", "multi.test:443")
	if err != nil || conn != client {
		t.Fatalf("conn=%v err=%v", conn, err)
	}
	_ = conn.Close()
	if got := strings.Join(calls, ","); got != "8.8.8.8:443,1.1.1.1:443" {
		t.Fatalf("dial order=%q", got)
	}
}

func TestPublicNetworkPolicyBudgetsEachCandidateSoABlackholeCannotStarveFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	var calls atomic.Int32
	policy := NewNetworkPolicy(
		&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("8.8.8.8", "1.1.1.1")}},
		dialFunc(func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			if calls.Add(1) == 1 {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return client, nil
		}),
	)
	conn, err := policy.DialContext(ctx, "tcp", "blackhole.test:443")
	if err != nil || conn != client {
		t.Fatalf("conn=%v error=%v calls=%d", conn, err, calls.Load())
	}
	_ = conn.Close()
}

func TestPublicNetworkPolicyReturnsWhenInjectedDialerIgnoresCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	policy := NewNetworkPolicy(
		&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("8.8.8.8")}},
		dialFunc(func(context.Context, string, string) (net.Conn, error) {
			close(started)
			<-release
			return nil, errors.New("late failure")
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := policy.DialContext(ctx, "tcp", "stuck.test:443"); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(release)
		t.Fatal("context-ignoring dialer retained the caller")
	}
	close(release)
}

func TestPublicNetworkPolicyCancellationStopsFurtherDials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	firstFailure := errors.New("dial failed while cancelling")
	calls := 0
	policy := NewNetworkPolicy(
		&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("8.8.8.8", "1.1.1.1")}},
		dialFunc(func(context.Context, string, string) (net.Conn, error) {
			calls++
			cancel()
			return nil, firstFailure
		}),
	)
	_, err := policy.DialContext(ctx, "tcp", "cancel.test:443")
	if !errors.Is(err, context.Canceled) || errors.Is(err, firstFailure) {
		t.Fatalf("error=%v, want context cancellation precedence", err)
	}
	if calls != 1 {
		t.Fatalf("dials=%d, want 1", calls)
	}
}

func TestPublicNetworkPolicyJoinsAllDialFailures(t *testing.T) {
	firstFailure := errors.New("first failure")
	secondFailure := errors.New("second failure")
	calls := 0
	policy := NewNetworkPolicy(
		&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("8.8.8.8", "1.1.1.1")}},
		dialFunc(func(context.Context, string, string) (net.Conn, error) {
			calls++
			if calls == 1 {
				return nil, firstFailure
			}
			return nil, secondFailure
		}),
	)
	_, err := policy.DialContext(context.Background(), "tcp", "fail.test:443")
	if !errors.Is(err, firstFailure) || !errors.Is(err, secondFailure) {
		t.Fatalf("joined error=%v", err)
	}
	if err.Error() != "network connection failed" {
		t.Fatalf("external error leaked dial details: %q", err)
	}
}

func TestPublicNetworkPolicyRejectsExcessiveDNSAnswersBeforeDial(t *testing.T) {
	answers := make([]net.IPAddr, maxResolvedDiagnosticAddresses+1)
	for index := range answers {
		answers[index] = net.IPAddr{IP: net.ParseIP(fmt.Sprintf("8.1.0.%d", index+1))}
	}
	dialer := &recordingDialer{}
	policy := NewNetworkPolicy(&resolverSequence{addresses: [][]net.IPAddr{answers}}, dialer)
	_, err := policy.DialContext(context.Background(), "tcp", "many.test:443")
	if !errors.Is(err, ErrNetworkPolicyBlocked) || len(dialer.calls) != 0 {
		t.Fatalf("error=%v dials=%v", err, dialer.calls)
	}
}

func TestPolicyTransportClearsEveryCustomConnectionHook(t *testing.T) {
	custom := &http.Transport{}
	alternateCalled := false
	custom.RegisterProtocol("https", roundTripFunc(func(*http.Request) (*http.Response, error) {
		alternateCalled = true
		return nil, errors.New("alternate protocol bypassed policy")
	}))
	custom.Proxy = func(*http.Request) (*url.URL, error) { return nil, errors.New("proxy called") }
	custom.DialTLS = func(string, string) (net.Conn, error) { return nil, errors.New("DialTLS called") }
	custom.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("DialTLSContext called")
	}
	custom.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{
		"policy-bypass": func(string, *tls.Conn) http.RoundTripper { return http.DefaultTransport },
	}
	policy := NewNetworkPolicy(&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("127.0.0.1")}}, &recordingDialer{})
	secured, err := newPolicyTransport(custom, policy)
	if err != nil {
		t.Fatal(err)
	}
	if secured.Proxy != nil || secured.DialTLS != nil || secured.DialTLSContext != nil || secured.TLSNextProto != nil {
		t.Fatalf("custom connection hooks survived")
	}
	request, err := http.NewRequest(http.MethodGet, "https://blocked.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = secured.RoundTrip(request)
	if alternateCalled {
		t.Fatal("registered alternate protocol bypassed policy")
	}
}

func TestPublicHTTPPolicyClosesPerCheckIdleTransport(t *testing.T) {
	var openConnections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			openConnections.Add(1)
		case http.StateClosed, http.StateHijacked:
			openConnections.Add(-1)
		}
	}
	server.Start()
	defer server.Close()
	policy := NewNetworkPolicy(
		&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("8.8.8.8")}},
		dialFunc(func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		}),
	)
	for index := 0; index < 8; index++ {
		result := (HTTPChecker{Policy: policy}).Check(context.Background(), Target{Kind: KindHTTP, Address: "http://public.test/"})
		if result.Status != StatusHealthy {
			t.Fatalf("check %d: %+v", index, result)
		}
	}
	deadline := time.Now().Add(time.Second)
	for openConnections.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := openConnections.Load(); got != 0 {
		t.Fatalf("idle policy connections still open: %d", got)
	}
}

func TestPublicNetworkPolicyClosesFailedConnectionAndReturnsSuccess(t *testing.T) {
	failedClient, failedServer := net.Pipe()
	successClient, successServer := net.Pipe()
	t.Cleanup(func() { _ = failedServer.Close(); _ = successServer.Close() })
	calls := 0
	policy := NewNetworkPolicy(
		&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("8.8.8.8", "1.1.1.1")}},
		dialFunc(func(context.Context, string, string) (net.Conn, error) {
			calls++
			if calls == 1 {
				return failedClient, errors.New("connection failed after allocation")
			}
			return successClient, nil
		}),
	)
	conn, err := policy.DialContext(context.Background(), "tcp", "close.test:443")
	if err != nil || conn != successClient {
		t.Fatalf("conn=%v err=%v", conn, err)
	}
	_ = conn.Close()
	_ = failedServer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := failedServer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("failed connection was not closed: %v", err)
	}
}

func TestPublicNetworkPolicyCIDRBoundariesAndPublicControls(t *testing.T) {
	for _, network := range blockedDiagnosticNetworks {
		for _, address := range []net.IP{network.IP, lastIP(network)} {
			if IsPublicDiagnosticIP(address) {
				t.Errorf("blocked CIDR %s boundary %s classified public", network, address)
			}
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "9.9.9.9", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if !IsPublicDiagnosticIP(net.ParseIP(address)) {
			t.Errorf("public control %s classified blocked", address)
		}
	}
}

func lastIP(network *net.IPNet) net.IP {
	ip := append(net.IP(nil), network.IP...)
	for i := range ip {
		ip[i] |= ^network.Mask[i]
	}
	return ip
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

func TestPublicHTTPPolicyDisablesLegacyDialTLSBypass(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	serverAddress := server.Listener.Addr().String()

	for _, checker := range []Checker{
		HTTPChecker{Client: legacyDialTLSClient(serverAddress), Policy: NewNetworkPolicy(nil, nil)},
		HTTPSChecker{Client: legacyDialTLSClient(serverAddress), Policy: NewNetworkPolicy(nil, nil)},
	} {
		result := checker.Check(context.Background(), Target{Kind: checker.Kind(), Address: "https://127.0.0.1/"})
		if result.ErrorCode != "network_policy_blocked" {
			t.Fatalf("%s legacy DialTLS bypassed policy: %+v", checker.Kind(), result)
		}
	}
}

func TestPublicHTTPPolicyPreservesContextErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		code string
	}{
		{name: "timeout", ctx: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 20*time.Millisecond)
		}, code: "timeout"},
		{name: "cancel", ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}, code: "cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.ctx()
			defer cancel()
			policy := NewNetworkPolicy(
				&resolverSequence{addresses: [][]net.IPAddr{ipAnswers("8.8.8.8")}},
				dialFunc(func(ctx context.Context, _, _ string) (net.Conn, error) {
					<-ctx.Done()
					return nil, errors.New("underlying dial failure")
				}),
			)
			result := (HTTPChecker{Policy: policy}).Check(ctx, Target{Kind: KindHTTP, Address: "http://public.test/"})
			if result.ErrorCode != test.code {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func legacyDialTLSClient(target string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialTLS: func(_, _ string) (net.Conn, error) {
			return tls.Dial("tcp", target, &tls.Config{InsecureSkipVerify: true})
		},
	}}
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
