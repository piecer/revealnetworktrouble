package diagnostic

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"time"
)

type ServiceChecker struct {
	ServiceKind Kind
	DefaultPort int
	UseTLS      bool
	Dialer      Dialer
	TLSConfig   *tls.Config
	Policy      *NetworkPolicy
}

func (c ServiceChecker) Kind() Kind { return c.ServiceKind }

func (c ServiceChecker) Check(ctx context.Context, target Target) Result {
	started := time.Now().UTC()
	address := serviceAddress(target.Address, c.DefaultPort)
	var dialer Dialer = c.Dialer
	if c.Policy != nil {
		dialer = c.Policy
	} else if dialer == nil {
		dialer = &net.Dialer{}
	}
	var conn net.Conn
	var err error
	if c.UseTLS {
		host, _, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			result := baseResult(c.ServiceKind, target.Address, started, splitErr)
			result.ErrorCode = "invalid_address"
			return result
		}
		config := c.TLSConfig
		if config == nil {
			config = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			config = config.Clone()
		}
		if config.ServerName == "" {
			config.ServerName = host
		}
		var raw net.Conn
		raw, err = dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			secured := tls.Client(raw, config)
			err = secured.HandshakeContext(ctx)
			if err != nil {
				_ = raw.Close()
			} else {
				conn = secured
			}
		}
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	result := networkPolicyResult(c.ServiceKind, target.Address, started, err)
	if err != nil {
		return result
	}
	defer conn.Close()
	result.Details = map[string]any{
		"endpoint":       address,
		"local_address":  conn.LocalAddr().String(),
		"remote_address": conn.RemoteAddr().String(),
	}
	if secured, ok := conn.(*tls.Conn); ok {
		state := secured.ConnectionState()
		result.Details["tls_version"] = tlsVersionName(state.Version)
		result.Details["cipher_suite"] = tlsCipherSuiteName(state.CipherSuite)
		if len(state.PeerCertificates) > 0 {
			certificate := state.PeerCertificates[0]
			result.Details["certificate_subject"] = certificate.Subject.String()
			result.Details["certificate_expires_at"] = certificate.NotAfter.UTC()
		}
	}
	return result
}

func serviceAddress(address string, defaultPort int) string {
	address = strings.TrimSpace(address)
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(strings.Trim(address, "[]"), strconv.Itoa(defaultPort))
}

func DefaultServiceCheckers() []Checker {
	return []Checker{
		ServiceChecker{ServiceKind: KindSSH, DefaultPort: 22},
		ServiceChecker{ServiceKind: KindSMTP, DefaultPort: 25},
		ServiceChecker{ServiceKind: KindSubmission, DefaultPort: 587},
		ServiceChecker{ServiceKind: KindSMTPS, DefaultPort: 465, UseTLS: true},
		ServiceChecker{ServiceKind: KindIMAP, DefaultPort: 143},
		ServiceChecker{ServiceKind: KindIMAPS, DefaultPort: 993, UseTLS: true},
		ServiceChecker{ServiceKind: KindPOP3, DefaultPort: 110},
		ServiceChecker{ServiceKind: KindPOP3S, DefaultPort: 995, UseTLS: true},
	}
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return "unknown"
	}
}

func tlsCipherSuiteName(suite uint16) string {
	if name := tls.CipherSuiteName(suite); name != "" {
		return name
	}
	return "unknown"
}
