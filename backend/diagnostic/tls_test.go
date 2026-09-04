package diagnostic

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"
)

func TestClassifyTLSFailureExpiredAtExactNotAfterBoundary(t *testing.T) {
	now := time.Date(2026, 9, 3, 1, 2, 3, 0, time.FixedZone("verification", 9*60*60))
	certificate := &x509.Certificate{
		NotBefore: now.Add(-24 * time.Hour),
		NotAfter:  now,
	}
	err := &tls.CertificateVerificationError{
		UnverifiedCertificates: []*x509.Certificate{certificate},
		Err: x509.CertificateInvalidError{
			Cert:   certificate,
			Reason: x509.Expired,
		},
	}

	classification := classifyTLSFailure(err, now)
	if classification.errorCode != "tls_certificate_expired" {
		t.Fatalf("error code = %q", classification.errorCode)
	}
	wantBefore := certificate.NotBefore.UTC().Format(time.RFC3339)
	wantAfter := certificate.NotAfter.UTC().Format(time.RFC3339)
	if len(classification.details) != 2 || classification.details["certificate_not_before"] != wantBefore || classification.details["certificate_not_after"] != wantAfter {
		t.Fatalf("details = %#v, want exact UTC validity timestamps", classification.details)
	}
}

func TestClassifyTLSFailureUsesOnlyTypedVerificationCauseMatrix(t *testing.T) {
	now := time.Date(2026, 9, 3, 1, 2, 3, 0, time.UTC)
	current := &x509.Certificate{NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	notYetValid := &x509.Certificate{NotBefore: now.Add(time.Nanosecond), NotAfter: now.Add(time.Hour)}
	hostileExpired := &x509.Certificate{
		NotBefore:    now.Add(-2 * time.Hour),
		NotAfter:     now.Add(-time.Hour),
		Subject:      pkix.Name{CommonName: "SUBJECT_CANARY"},
		Issuer:       pkix.Name{CommonName: "ISSUER_CANARY"},
		DNSNames:     []string{"SAN_CANARY.invalid"},
		SerialNumber: big.NewInt(8675309),
	}

	tests := []struct {
		name        string
		err         error
		wantCode    string
		wantDetails bool
	}{
		{
			name:     "not yet valid immediately before NotBefore",
			err:      verificationFailure(notYetValid, x509.CertificateInvalidError{Cert: notYetValid, Reason: x509.Expired}),
			wantCode: "tls_certificate_not_yet_valid", wantDetails: true,
		},
		{
			name:     "hostname mismatch",
			err:      verificationFailure(hostileExpired, x509.HostnameError{Certificate: hostileExpired, Host: "HOST_CANARY.invalid"}),
			wantCode: "tls_hostname_mismatch",
		},
		{
			name:     "unknown authority ignores unrelated expired unverified certificate",
			err:      &tls.CertificateVerificationError{UnverifiedCertificates: []*x509.Certificate{hostileExpired}, Err: x509.UnknownAuthorityError{Cert: current}},
			wantCode: "tls_untrusted",
		},
		{
			name:     "other typed verification cause",
			err:      verificationFailure(hostileExpired, x509.CertificateInvalidError{Cert: hostileExpired, Reason: x509.IncompatibleUsage, Detail: "DETAIL_CANARY"}),
			wantCode: "tls_handshake_failed",
		},
		{
			name:     "wrapped typed hostname mismatch",
			err:      verificationFailure(hostileExpired, fmt.Errorf("WRAPPED_CANARY: %w", x509.HostnameError{Certificate: hostileExpired, Host: "HOST_CANARY.invalid"})),
			wantCode: "tls_hostname_mismatch",
		},
		{
			name:     "unwrapped typed cause is safe fallback",
			err:      x509.UnknownAuthorityError{Cert: hostileExpired},
			wantCode: "tls_handshake_failed",
		},
		{
			name:     "hostile prose is safe fallback",
			err:      errors.New("certificate expired SUBJECT_CANARY SAN_CANARY ISSUER_CANARY SERIAL_CANARY"),
			wantCode: "tls_handshake_failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyTLSFailure(tt.err, now)
			if got.errorCode != tt.wantCode {
				t.Fatalf("error code = %q, want %q", got.errorCode, tt.wantCode)
			}
			if tt.wantDetails {
				if len(got.details) != 2 || got.details["certificate_not_before"] != notYetValid.NotBefore.UTC().Format(time.RFC3339) || got.details["certificate_not_after"] != notYetValid.NotAfter.UTC().Format(time.RFC3339) {
					t.Fatalf("details = %#v", got.details)
				}
			} else if len(got.details) != 0 {
				t.Fatalf("unexpected failed details = %#v", got.details)
			}
		})
	}
}

func verificationFailure(certificate *x509.Certificate, cause error) error {
	return &tls.CertificateVerificationError{UnverifiedCertificates: []*x509.Certificate{certificate}, Err: cause}
}
