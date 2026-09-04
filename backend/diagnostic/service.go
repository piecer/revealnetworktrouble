package diagnostic

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	serviceGreetingAggregateLimit = 4096
	serviceGreetingLineLimit      = 512
	serviceGreetingLineCountLimit = 8
)

var errServiceGreetingUnverified = errors.New("service greeting was not verified")

type ServiceChecker struct {
	ServiceKind Kind
	DefaultPort int
	UseTLS      bool
	Dialer      Dialer
	TLSConfig   *tls.Config
	Policy      *NetworkPolicy
	Now         func() time.Time
}

func (c ServiceChecker) Kind() Kind { return c.ServiceKind }

func (c ServiceChecker) Check(ctx context.Context, target Target) Result {
	started := time.Now().UTC()
	verificationTime := frozenTLSVerificationTime(c.Now)
	address := serviceAddress(target.Address, c.DefaultPort)
	var dialer Dialer = c.Dialer
	if c.Policy != nil {
		dialer = c.Policy
	} else if dialer == nil {
		dialer = &net.Dialer{}
	}
	var conn net.Conn
	var err error
	tlsHandshakeFailed := false
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
		configureTLSVerificationClock(config, verificationTime)
		var raw net.Conn
		raw, err = dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			secured := tls.Client(raw, config)
			err = secured.HandshakeContext(ctx)
			if contextErr := ctx.Err(); contextErr != nil {
				err = contextErr
			}
			if err != nil {
				tlsHandshakeFailed = ctx.Err() == nil
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
		if tlsHandshakeFailed {
			applyTLSFailure(&result, err, verificationTime)
		}
		return result
	}
	var closeOnce sync.Once
	closeConn := func() { closeOnce.Do(func() { _ = conn.Close() }) }
	stopCancelClose := context.AfterFunc(ctx, closeConn)
	defer func() {
		stopCancelClose()
		closeConn()
	}()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	result.Details = map[string]any{}
	if secured, ok := conn.(*tls.Conn); ok {
		state := secured.ConnectionState()
		result.Details[ResultDetailTLSVersion] = tlsVersionName(state.Version)
		result.Details[ResultDetailCipherSuite] = tlsCipherSuiteName(state.CipherSuite)
		if len(state.PeerCertificates) > 0 {
			certificate := state.PeerCertificates[0]
			result.Details[ResultDetailCertificateSubject] = certificate.Subject.String()
			result.Details[ResultDetailCertificateExpires] = certificate.NotAfter.UTC()
		}
	}
	if err = verifyServerGreeting(conn, c.ServiceKind); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return networkPolicyResult(c.ServiceKind, target.Address, started, contextErr)
		}
		result.Status = StatusDegraded
		result.ErrorCode = ResultErrorServiceGreetingUnverified
		result.Message = errServiceGreetingUnverified.Error()
		result.Details = nil
		return result
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return networkPolicyResult(c.ServiceKind, target.Address, started, contextErr)
	}
	result.Details[ResultDetailVerificationScope] = VerificationScopeServerGreeting
	return result
}

func verifyServerGreeting(reader io.Reader, kind Kind) error {
	switch kind {
	case KindSSH:
		return readServiceGreeting(reader, func(line []byte, index int) (bool, bool) {
			if strings.HasPrefix(string(line), "SSH-") {
				return len(line) <= 255 && strings.HasPrefix(string(line), "SSH-2.0-"), true
			}
			return false, index < 7
		})
	case KindSMTP, KindSubmission, KindSMTPS:
		return readServiceGreeting(reader, func(line []byte, _ int) (bool, bool) {
			if len(line) < 6 || string(line[:3]) != "220" {
				return false, false
			}
			switch line[3] {
			case ' ':
				return true, true
			case '-':
				return false, true
			default:
				return false, false
			}
		})
	case KindIMAP, KindIMAPS:
		return readServiceGreeting(reader, func(line []byte, _ int) (bool, bool) {
			valid := greetingPrefixWithDelimiter(line, "* OK") || greetingPrefixWithDelimiter(line, "* PREAUTH")
			return valid, valid
		})
	case KindPOP3, KindPOP3S:
		return readServiceGreeting(reader, func(line []byte, _ int) (bool, bool) {
			valid := greetingPrefixWithDelimiter(line, "+OK")
			return valid, valid
		})
	default:
		return errServiceGreetingUnverified
	}
}

func greetingPrefixWithDelimiter(line []byte, prefix string) bool {
	if !strings.HasPrefix(string(line), prefix) || len(line) < len(prefix)+2 {
		return false
	}
	return line[len(prefix)] == ' ' || (line[len(prefix)] == '\r' && line[len(prefix)+1] == '\n')
}

func readServiceGreeting(reader io.Reader, accept func([]byte, int) (done, valid bool)) error {
	line := make([]byte, 0, serviceGreetingLineLimit)
	total := 0
	lineIndex := 0
	var one [1]byte
	for total < serviceGreetingAggregateLimit {
		n, err := reader.Read(one[:])
		if n > 0 {
			total++
			line = append(line, one[0])
			if len(line) > serviceGreetingLineLimit || (one[0] == '\n' && (len(line) < 2 || line[len(line)-2] != '\r')) {
				return errServiceGreetingUnverified
			}
			if len(line) >= 2 && line[len(line)-2] == '\r' && line[len(line)-1] == '\n' {
				done, valid := accept(line, lineIndex)
				if !valid {
					return errServiceGreetingUnverified
				}
				if done {
					return nil
				}
				lineIndex++
				if lineIndex == serviceGreetingLineCountLimit {
					return errServiceGreetingUnverified
				}
				line = line[:0]
			}
		}
		if err != nil {
			return errServiceGreetingUnverified
		}
	}
	return errServiceGreetingUnverified
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
