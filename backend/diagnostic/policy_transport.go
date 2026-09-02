package diagnostic

import "net/http"

// newPolicyTransport preserves ordinary HTTP/TLS tuning while replacing every
// connection-establishment extension point with the validated network policy.
func newPolicyTransport(base http.RoundTripper, policy *NetworkPolicy) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if configured, ok := base.(*http.Transport); ok && configured != nil {
		transport = configured.Clone()
	} else if base != nil {
		return nil, ErrNetworkPolicyBlocked
	}
	transport.Proxy = nil
	transport.DialContext = policy.DialContext
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	transport.TLSNextProto = nil
	return transport, nil
}
