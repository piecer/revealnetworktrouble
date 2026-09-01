package diagnostic

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPChecker struct {
	Client *http.Client
}

func (HTTPChecker) Kind() Kind { return KindHTTP }

func (c HTTPChecker) Check(ctx context.Context, target Target) Result {
	return checkHTTP(ctx, target, KindHTTP, "", c.Client)
}

type HTTPSChecker struct {
	Client *http.Client
}

func (HTTPSChecker) Kind() Kind { return KindHTTPS }

func (c HTTPSChecker) Check(ctx context.Context, target Target) Result {
	return checkHTTP(ctx, target, KindHTTPS, "https", c.Client)
}

func checkHTTP(ctx context.Context, target Target, kind Kind, scheme string, configuredClient *http.Client) Result {
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
		client = &http.Client{CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		}}
	}
	resp, err := client.Do(req)
	result := baseResult(kind, target.Address, started, err)
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32*1024))
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
