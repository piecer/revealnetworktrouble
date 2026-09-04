package diagnostic

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"time"
)

type tlsFailureClassification struct {
	errorCode string
	details   map[string]any
}

func frozenTLSVerificationTime(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

func applyTLSFailure(result *Result, err error, now time.Time) {
	classification := classifyTLSFailure(err, now)
	result.Status = StatusUnreachable
	result.ErrorCode = classification.errorCode
	result.Message = "TLS handshake failed"
	result.Details = classification.details
}

func configureTLSVerificationClock(config *tls.Config, now time.Time) {
	config.Time = func() time.Time { return now }
	configuredVerifyConnection := config.VerifyConnection
	config.VerifyConnection = func(state tls.ConnectionState) error {
		if configuredVerifyConnection != nil {
			if err := configuredVerifyConnection(state); err != nil {
				return err
			}
		}
		if config.InsecureSkipVerify || len(state.PeerCertificates) == 0 {
			return nil
		}
		certificate := state.PeerCertificates[0]
		if now.Before(certificate.NotAfter) {
			return nil
		}
		return &tls.CertificateVerificationError{
			UnverifiedCertificates: state.PeerCertificates,
			Err: x509.CertificateInvalidError{
				Cert:   certificate,
				Reason: x509.Expired,
			},
		}
	}
}

func classifyTLSFailure(err error, now time.Time) tlsFailureClassification {
	classification := tlsFailureClassification{errorCode: ResultErrorTLSHandshakeFailed}
	var verificationError *tls.CertificateVerificationError
	if !errors.As(err, &verificationError) || verificationError == nil {
		return classification
	}
	var hostnameError x509.HostnameError
	if errors.As(verificationError.Err, &hostnameError) {
		classification.errorCode = ResultErrorTLSHostnameMismatch
		return classification
	}
	var unknownAuthorityError x509.UnknownAuthorityError
	if errors.As(verificationError.Err, &unknownAuthorityError) {
		classification.errorCode = ResultErrorTLSUntrusted
		return classification
	}
	var invalid x509.CertificateInvalidError
	if !errors.As(verificationError.Err, &invalid) || invalid.Reason != x509.Expired || invalid.Cert == nil {
		return classification
	}
	certificate := invalid.Cert
	switch {
	case !now.Before(certificate.NotAfter):
		classification.errorCode = ResultErrorTLSCertificateExpired
	case now.Before(certificate.NotBefore):
		classification.errorCode = ResultErrorTLSCertificateNotYetValid
	default:
		return classification
	}
	classification.details = map[string]any{
		ResultDetailCertificateBefore: certificate.NotBefore.UTC().Format(time.RFC3339),
		ResultDetailCertificateAfter:  certificate.NotAfter.UTC().Format(time.RFC3339),
	}
	return classification
}
