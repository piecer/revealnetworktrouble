package diagnostic

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// One-shot HTTP/1.1 deliberately does not use net/http.Transport. Its detached
// dial/retry paths cannot establish this feature's transitive two-flight bound.
// Every resolve, dial, TLS, send, and body read runs inside the charged primitive.
// There are no redirects, retries, proxies, pools, alternate protocols or idle
// connections. The adapter instance is owned by the service, not the report path.
type ipContextHTTPAdapter struct {
	resolver IPResolver
	dialer   Dialer
	roots    *x509.CertPool
	closed   atomic.Bool
}

func newIPContextHTTPAdapter(resolver IPResolver, dialer Dialer, roots *x509.CertPool) *ipContextHTTPAdapter {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	return &ipContextHTTPAdapter{resolver: resolver, dialer: dialer, roots: roots}
}
func (a *ipContextHTTPAdapter) Close() { a.closed.Store(true) }
func approvedContextURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Port() != "" {
		return false
	}
	if u.Host == "stat.ripe.net" {
		return u.Path == "/data/network-info/data.json" || u.Path == "/data/rpki-validation/data.json"
	}
	for base := range contextRegistryBases {
		b, _ := url.Parse(base)
		if u.Host == b.Host && strings.HasPrefix(u.Path, b.Path+"ip/") && u.RawQuery == "" {
			return true
		}
	}
	return false
}
func (a *ipContextHTTPAdapter) Get(ctx context.Context, rawURL string) (int, []byte, error) {
	if a.closed.Load() {
		return 0, nil, ErrIPContextBusy
	}
	if ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	u, err := url.Parse(rawURL)
	if err != nil || !approvedContextURL(u) {
		return 0, nil, ErrNetworkPolicyBlocked
	}
	addresses, err := a.resolver.LookupIPAddr(ctx, u.Hostname())
	if ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	if err != nil {
		return 0, nil, errors.New("provider resolve failed")
	}
	if len(addresses) == 0 || len(addresses) > 16 {
		return 0, nil, ErrNetworkPolicyBlocked
	}
	for _, ip := range addresses {
		if ip.Zone != "" || !IsPublicDiagnosticIP(ip.IP) {
			return 0, nil, ErrNetworkPolicyBlocked
		}
	}
	var conn net.Conn
	for i, ip := range addresses {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		candidate, cancel := candidateDialContext(ctx, len(addresses)-i)
		// Synchronous on purpose: do not start a replacement while this dial lives.
		c, e := a.dialer.DialContext(candidate, "tcp", net.JoinHostPort(ip.IP.String(), "443"))
		expired := candidate.Err()
		cancel()
		if ctx.Err() != nil {
			if c != nil {
				c.Close()
			}
			return 0, nil, ctx.Err()
		}
		if e == nil && expired == nil && c != nil {
			conn = c
			break
		}
		if c != nil {
			c.Close()
		}
	}
	if conn == nil {
		return 0, nil, errors.New("provider dial failed")
	}
	defer conn.Close()
	if ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	// The watcher is joined before the primitive exits, including a blocking Close.
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stop:
		}
	}()
	defer func() { close(stop); <-stopped }()
	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(deadline); err != nil {
			return 0, nil, errors.New("provider deadline failed")
		}
	}
	secure := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), RootCAs: a.roots, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	if err = secure.HandshakeContext(ctx); err != nil {
		return 0, nil, err
	}
	if ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, ErrIPContextJSON
	}
	request.Close = true
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	if err = request.Write(secure); err != nil {
		return 0, nil, err
	}
	reader := bufio.NewReader(secure)
	headers := make([]byte, 0, 1024)
	for {
		line, e := reader.ReadSlice('\n')
		if len(headers)+len(line) > 16384 {
			return 0, nil, ErrIPContextJSON
		}
		headers = append(headers, line...)
		if e != nil {
			return 0, nil, e
		}
		if bytes.Equal(line, []byte("\r\n")) {
			break
		}
	}
	response, err := http.ReadResponse(bufio.NewReader(io.MultiReader(bytes.NewReader(headers), reader)), request)
	if err != nil {
		return 0, nil, ErrIPContextJSON
	}
	// Close the connection before Body.Close: no unbounded redirect/error drain.
	defer func() { conn.Close(); response.Body.Close() }()
	status := response.StatusCode
	if status < 200 || status >= 300 || (u.Host == "stat.ripe.net" && status != 200) {
		return status, nil, nil
	}
	if enc := response.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return status, nil, ErrIPContextJSON
	}
	if response.ContentLength > ipContextUpstreamBytes {
		return status, nil, ErrIPContextJSON
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, ipContextUpstreamBytes+1))
	if ctx.Err() != nil {
		return status, nil, ctx.Err()
	}
	if err != nil || len(body) > ipContextUpstreamBytes {
		return status, nil, ErrIPContextJSON
	}
	return status, body, nil
}
