package diagnostic

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

var errTLSDowngrade = errors.New("HTTPS redirect did not preserve TLS")

const maxHTTPResponseSampleBytes = 32 * 1024

type HTTPChecker struct {
	Client *http.Client
	Policy *NetworkPolicy
}

func (HTTPChecker) Kind() Kind { return KindHTTP }

func (c HTTPChecker) Check(ctx context.Context, target Target) Result {
	return checkHTTP(ctx, target, KindHTTP, "", c.Client, c.Policy)
}

type HTTPSChecker struct {
	Client *http.Client
	Policy *NetworkPolicy
}

func (HTTPSChecker) Kind() Kind { return KindHTTPS }

func (c HTTPSChecker) Check(ctx context.Context, target Target) Result {
	return checkHTTP(ctx, target, KindHTTPS, "https", c.Client, c.Policy)
}

func checkHTTP(ctx context.Context, target Target, kind Kind, scheme string, configuredClient *http.Client, policy *NetworkPolicy) Result {
	started := time.Now().UTC()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.Address, nil)
	if err != nil {
		result := baseResult(kind, target.Address, started, err)
		result.ErrorCode = "invalid_url"
		return result
	}
	validScheme := strings.EqualFold(req.URL.Scheme, "http") || strings.EqualFold(req.URL.Scheme, "https")
	if !validScheme || req.URL.Host == "" || (scheme != "" && !strings.EqualFold(req.URL.Scheme, scheme)) {
		result := baseResult(kind, target.Address, started, nil)
		result.Status = StatusUnreachable
		result.ErrorCode = "invalid_url"
		result.Message = "address must be a valid HTTP URL"
		if scheme != "" {
			result.Message = "address must be a valid " + scheme + " URL"
		}
		return result
	}
	req.Header.Set("User-Agent", "CheckNetwork/1.0")
	client := configuredClient
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	if policy != nil {
		transport, transportErr := newPolicyTransport(client.Transport, policy)
		if transportErr != nil {
			result := networkPolicyResult(kind, target.Address, started, ErrNetworkPolicyBlocked)
			return result
		}
		clientCopy.Transport = transport
		defer transport.CloseIdleConnections()
	}
	configuredRedirect := client.CheckRedirect
	clientCopy.CheckRedirect = func(redirect *http.Request, via []*http.Request) error {
		if kind == KindHTTPS && !strings.EqualFold(redirect.URL.Scheme, "https") {
			return errTLSDowngrade
		}
		if policy != nil {
			if _, err := policy.Resolve(redirect.Context(), redirect.URL.Hostname()); err != nil {
				return err
			}
		}
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		if configuredRedirect != nil {
			return configuredRedirect(redirect, via)
		}
		return nil
	}
	client = &clientCopy
	resp, err := client.Do(req)
	result := networkPolicyResult(kind, target.Address, started, err)
	if err != nil {
		if errors.Is(err, errTLSDowngrade) {
			result.ErrorCode = "tls_downgrade"
			result.Message = "HTTPS redirect or response did not preserve TLS"
		} else if kind == KindHTTPS && isTLSHandshakeError(err) {
			result.ErrorCode = "tls_handshake_failed"
			result.Message = "TLS handshake failed"
		}
		return result
	}
	defer resp.Body.Close()
	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxHTTPResponseSampleBytes))
	if readErr != nil {
		return httpResponseReadFailure(kind, target.Address, started, readErr)
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	if kind == KindHTTPS && resp.TLS == nil {
		result.Status = StatusUnreachable
		result.ErrorCode = "tls_downgrade"
		result.Message = "HTTPS redirect or response did not preserve TLS"
		return result
	}
	expected := target.ExpectedStatus
	if expected == 0 {
		expected = http.StatusOK
	}
	result.Details = map[string]any{
		"status_code":     resp.StatusCode,
		"expected_status": expected,
		"protocol":        resp.Proto,
		"content_type":    resp.Header.Get("Content-Type"),
	}
	if resp.TLS != nil {
		result.Details["tls_version"] = tlsVersionName(resp.TLS.Version)
		result.Details["cipher_suite"] = tlsCipherSuiteName(resp.TLS.CipherSuite)
		if len(resp.TLS.PeerCertificates) > 0 {
			certificate := resp.TLS.PeerCertificates[0]
			result.Details["certificate_subject"] = certificate.Subject.String()
			result.Details["certificate_expires_at"] = certificate.NotAfter.UTC()
		}
	}
	if resp.StatusCode != expected {
		result.Status = StatusUnreachable
		result.ErrorCode = "unexpected_status"
		result.Message = "service returned an unexpected HTTP status"
	}
	return result
}

func httpResponseReadFailure(kind Kind, address string, started time.Time, err error) Result {
	result := baseResult(kind, address, started, err)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result.ErrorCode = "timeout"
		result.Message = "HTTP response body read timed out"
	case errors.Is(err, context.Canceled):
		result.ErrorCode = "cancelled"
		result.Message = "HTTP response body read was cancelled"
	default:
		result.ErrorCode = "response_read_failed"
		result.Message = "HTTP response body could not be read"
	}
	return result
}

func isTLSHandshakeError(err error) bool {
	var verificationError *tls.CertificateVerificationError
	var recordHeaderError tls.RecordHeaderError
	return errors.As(err, &verificationError) || errors.As(err, &recordHeaderError)
}
