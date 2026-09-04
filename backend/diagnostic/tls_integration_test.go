package diagnostic

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"
)

const tlsTestServerName = "tls-matrix.example.test"

func TestHTTPSCheckerUsesFrozenVerificationClockForExpiredCertificate(t *testing.T) {
	now := time.Date(2035, 4, 5, 6, 7, 8, 0, time.FixedZone("checker", -7*60*60))
	server, roots, certificate := newHTTPSCertificateServer(t, now.Add(-24*time.Hour), now)
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: tlsTestServerName, MinVersion: tls.VersionTLS12}
	client.Transport = transport

	result := (HTTPSChecker{Client: client, Now: func() time.Time { return now }}).Check(context.Background(), Target{Kind: KindHTTPS, Address: server.URL})
	assertTLSFailureResult(t, result, "tls_certificate_expired", certificate)
}

func TestImplicitTLSServiceCheckersUseFrozenVerificationClockForExpiredCertificate(t *testing.T) {
	now := time.Date(2035, 4, 5, 6, 7, 8, 0, time.UTC)
	server, roots, certificate := newHTTPSCertificateServer(t, now.Add(-24*time.Hour), now)
	runTLSCheckerMatrix(t, server, func() *tls.Config {
		return &tls.Config{RootCAs: roots, ServerName: tlsTestServerName, MinVersion: tls.VersionTLS12}
	}, now, "tls_certificate_expired", certificate)
}

func TestTLSCheckersNotYetValidAtExactNotBeforeBoundary(t *testing.T) {
	now := time.Date(2035, 4, 5, 6, 7, 8, 0, time.UTC)
	// X.509 validity is encoded with whole-second precision. Keep NotBefore one
	// encoded second after the frozen verification clock.
	server, roots, certificate := newHTTPSCertificateServer(t, now.Add(time.Second), now.Add(24*time.Hour))
	runTLSCheckerMatrix(t, server, func() *tls.Config {
		return &tls.Config{RootCAs: roots, ServerName: tlsTestServerName, MinVersion: tls.VersionTLS12}
	}, now, "tls_certificate_not_yet_valid", certificate)
}

func TestTLSCheckersHostnameMismatchHasNoFailedDetails(t *testing.T) {
	now := time.Date(2035, 4, 5, 6, 7, 8, 0, time.UTC)
	server, roots, certificate := newHTTPSCertificateServer(t, now.Add(-time.Hour), now.Add(time.Hour))
	runTLSCheckerMatrix(t, server, func() *tls.Config {
		return &tls.Config{RootCAs: roots, ServerName: "HOST_CANARY.invalid", MinVersion: tls.VersionTLS12}
	}, now, "tls_hostname_mismatch", certificate)
}

func TestTLSCheckersUntrustedHasNoFailedDetails(t *testing.T) {
	now := time.Date(2035, 4, 5, 6, 7, 8, 0, time.UTC)
	server, _, certificate := newHTTPSCertificateServer(t, now.Add(-time.Hour), now.Add(time.Hour))
	runTLSCheckerMatrix(t, server, func() *tls.Config {
		return &tls.Config{ServerName: tlsTestServerName, MinVersion: tls.VersionTLS12}
	}, now, "tls_untrusted", certificate)
}

func TestTLSCheckersMalformedProtocolHasNoFailedDetailsOrProse(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("HOSTILE_TLS_PROTOCOL_CANARY"))
	}))
	t.Cleanup(plain.Close)
	httpsAddress := "https" + strings.TrimPrefix(plain.URL, "http")
	assertTLSFailureResult(t, (HTTPSChecker{}).Check(context.Background(), Target{Kind: KindHTTPS, Address: httpsAddress}), ResultErrorTLSHandshakeFailed, nil)
	for _, kind := range []Kind{KindSMTPS, KindIMAPS, KindPOP3S} {
		result := (ServiceChecker{ServiceKind: kind, DefaultPort: 443, UseTLS: true}).Check(context.Background(), Target{Kind: kind, Address: plain.Listener.Addr().String()})
		assertTLSFailureResult(t, result, ResultErrorTLSHandshakeFailed, nil)
	}
}

func runTLSCheckerMatrix(t *testing.T, server *httptest.Server, config func() *tls.Config, now time.Time, wantCode string, certificate *x509.Certificate) {
	t.Helper()
	t.Run("https", func(t *testing.T) {
		client := server.Client()
		transport := client.Transport.(*http.Transport).Clone()
		transport.TLSClientConfig = config()
		client.Transport = transport
		result := (HTTPSChecker{Client: client, Now: func() time.Time { return now }}).Check(context.Background(), Target{Kind: KindHTTPS, Address: server.URL})
		assertTLSFailureResult(t, result, wantCode, certificate)
	})
	for _, kind := range []Kind{KindSMTPS, KindIMAPS, KindPOP3S} {
		t.Run(string(kind), func(t *testing.T) {
			result := (ServiceChecker{
				ServiceKind: kind,
				DefaultPort: 443,
				UseTLS:      true,
				TLSConfig:   config(),
				Now:         func() time.Time { return now },
			}).Check(context.Background(), Target{Kind: kind, Address: server.Listener.Addr().String()})
			assertTLSFailureResult(t, result, wantCode, certificate)
		})
	}
}

func TestHTTPSCheckerClassifiesAnyObservedTLSHandshakeFailureWithoutProse(t *testing.T) {
	const canary = "ALERT_RECORD_PROTOCOL_CIPHER_FALLBACK_CANARY"
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		if trace != nil && trace.TLSHandshakeDone != nil {
			trace.TLSHandshakeDone(tls.ConnectionState{}, errors.New(canary))
		}
		return nil, errors.New(canary)
	})}
	result := (HTTPSChecker{Client: client}).Check(context.Background(), Target{Kind: KindHTTPS, Address: "https://example.test"})
	assertTLSFailureResult(t, result, "tls_handshake_failed", nil)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("serialized result leaked TLS prose: %s", encoded)
	}
}

func TestHTTPSCheckerTLSDeadlineCancelContextWinsAndOwnershipRemainsUntilReturn(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				close(entered)
				<-request.Context().Done()
				<-release
				trace := httptrace.ContextClientTrace(request.Context())
				if trace != nil && trace.TLSHandshakeDone != nil {
					trace.TLSHandshakeDone(tls.ConnectionState{}, errors.New("LATE_TLS_CANARY"))
				}
				return nil, errors.New("LATE_TLS_CANARY")
			})}
			var ctx context.Context
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			results := make(chan Result, 1)
			go func() {
				results <- (HTTPSChecker{Client: client}).Check(ctx, Target{Kind: KindHTTPS, Address: "https://example.test"})
			}()
			<-entered
			if !deadline {
				cancel()
			}
			<-ctx.Done()
			select {
			case result := <-results:
				t.Fatalf("checker relinquished ownership before RoundTrip returned: %+v", result)
			default:
			}
			close(release)
			result := <-results
			want := "cancelled"
			if deadline {
				want = "timeout"
			}
			if result.ErrorCode != want || result.Status != StatusUnreachable {
				t.Fatalf("result = %+v, want %s", result, want)
			}
		})
	}
}

func TestImplicitTLSServiceDeadlineCancelContextWinsAndOwnsHandshakeUntilReturn(t *testing.T) {
	now := time.Date(2035, 4, 5, 6, 7, 8, 0, time.UTC)
	server, _, _ := newHTTPSCertificateServer(t, now.Add(-time.Hour), now.Add(time.Hour))
	for _, kind := range []Kind{KindSMTPS, KindIMAPS, KindPOP3S} {
		for _, deadline := range []bool{false, true} {
			name := string(kind) + "/cancel"
			if deadline {
				name = string(kind) + "/deadline"
			}
			t.Run(name, func(t *testing.T) {
				entered := make(chan struct{})
				release := make(chan struct{})
				config := &tls.Config{
					InsecureSkipVerify: true,
					MinVersion:         tls.VersionTLS12,
					VerifyConnection: func(tls.ConnectionState) error {
						close(entered)
						<-release
						return errors.New("LATE_SERVICE_TLS_CANARY")
					},
				}
				var ctx context.Context
				var cancel context.CancelFunc
				if deadline {
					ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
				} else {
					ctx, cancel = context.WithCancel(context.Background())
				}
				defer cancel()
				results := make(chan Result, 1)
				go func() {
					results <- (ServiceChecker{
						ServiceKind: kind, DefaultPort: 443, UseTLS: true, TLSConfig: config,
					}).Check(ctx, Target{Kind: kind, Address: server.Listener.Addr().String()})
				}()
				<-entered
				if !deadline {
					cancel()
				}
				<-ctx.Done()
				select {
				case result := <-results:
					t.Fatalf("checker relinquished ownership before verification callback returned: %+v", result)
				default:
				}
				close(release)
				result := <-results
				want := "cancelled"
				if deadline {
					want = "timeout"
				}
				if result.Status != StatusUnreachable || result.ErrorCode != want || strings.Contains(result.Message, "LATE_SERVICE_TLS_CANARY") || len(result.Details) != 0 {
					t.Fatalf("result = %+v, want private %s", result, want)
				}
			})
		}
	}
}

func newHTTPSCertificateServer(t *testing.T, notBefore, notAfter time.Time) (*httptest.Server, *x509.CertPool, *x509.Certificate) {
	t.Helper()
	serverCertificate, roots, parsed := newTLSCertificateFixture(t, notBefore, notAfter)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCertificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, roots, parsed
}

func newTLSCertificateFixture(t *testing.T, notBefore, notAfter time.Time) (tls.Certificate, *x509.CertPool, *x509.Certificate) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ISSUER_CANARY"},
		NotBefore: notBefore.Add(-time.Hour), NotAfter: notAfter.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(8675309), Subject: pkix.Name{CommonName: "SUBJECT_CANARY"},
		Issuer: pkix.Name{CommonName: "ISSUER_CANARY"}, DNSNames: []string{tlsTestServerName, "SAN_CANARY.invalid"},
		NotBefore: notBefore, NotAfter: notAfter,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}),
	)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return certificate, roots, leaf
}

func assertTLSFailureResult(t *testing.T, result Result, wantCode string, certificate *x509.Certificate) {
	t.Helper()
	if result.Status != StatusUnreachable || result.ErrorCode != wantCode {
		t.Fatalf("result = %+v, want unreachable/%s", result, wantCode)
	}
	if result.Message != "TLS handshake failed" {
		t.Fatalf("message = %q", result.Message)
	}
	for _, canary := range []string{"SUBJECT_CANARY", "SAN_CANARY", "ISSUER_CANARY", "8675309"} {
		if strings.Contains(result.Message, canary) {
			t.Fatalf("message leaked %q: %q", canary, result.Message)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"SUBJECT_CANARY", "SAN_CANARY", "ISSUER_CANARY", "8675309", "HOSTILE_TLS_PROTOCOL_CANARY", "LATE_SERVICE_TLS_CANARY"} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("serialized failed result leaked %q: %s", canary, encoded)
		}
	}
	if wantCode == "tls_certificate_expired" || wantCode == "tls_certificate_not_yet_valid" {
		if len(result.Details) != 2 || result.Details["certificate_not_before"] != certificate.NotBefore.UTC().Format(time.RFC3339) || result.Details["certificate_not_after"] != certificate.NotAfter.UTC().Format(time.RFC3339) {
			t.Fatalf("details = %#v", result.Details)
		}
	} else if len(result.Details) != 0 {
		t.Fatalf("failed details = %#v, want none", result.Details)
	}
}
