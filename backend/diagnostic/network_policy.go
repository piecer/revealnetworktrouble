package diagnostic

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

var ErrNetworkPolicyBlocked = errors.New("target is not allowed in public mode")

const (
	maxResolvedDiagnosticAddresses = 16
	maxDialDiagnosticAddresses     = 16
	maxPreservedDialFailures       = 4
	maxCandidateDialTime           = 5 * time.Second
	maxDetachedPolicyDials         = 32
)

type networkDialFailures struct{ causes []error }

func (networkDialFailures) Error() string     { return "network connection failed" }
func (e networkDialFailures) Unwrap() []error { return e.causes }

type IPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type NetworkPolicy struct {
	resolver  IPResolver
	dialer    Dialer
	dialSlots chan struct{}
}

func NewNetworkPolicy(resolver IPResolver, dialer Dialer) *NetworkPolicy {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	return &NetworkPolicy{resolver: resolver, dialer: dialer, dialSlots: make(chan struct{}, maxDetachedPolicyDials)}
}

func (p *NetworkPolicy) Resolve(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.TrimSpace(strings.TrimSuffix(host, "."))
	withoutZone := host
	if index := strings.LastIndexByte(withoutZone, '%'); index >= 0 {
		withoutZone = withoutZone[:index]
	}
	var addresses []net.IP
	if literal := net.ParseIP(withoutZone); literal != nil {
		addresses = []net.IP{literal}
	} else {
		resolved, err := p.resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(resolved) > maxResolvedDiagnosticAddresses {
			return nil, ErrNetworkPolicyBlocked
		}
		addresses = make([]net.IP, 0, len(resolved))
		for _, address := range resolved {
			if address.IP != nil {
				addresses = append(addresses, address.IP)
			}
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("target could not be resolved")
	}
	for _, address := range addresses {
		if !IsPublicDiagnosticIP(address) {
			return nil, ErrNetworkPolicyBlocked
		}
	}
	return addresses, nil
}

func (p *NetworkPolicy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid network address")
	}
	addresses, err := p.Resolve(ctx, host)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	candidates := addresses
	if len(candidates) > maxDialDiagnosticAddresses {
		candidates = candidates[:maxDialDiagnosticAddresses]
	}
	failures := make([]error, 0, min(len(candidates), maxPreservedDialFailures))
	for index, ip := range candidates {
		attemptCtx, cancel := candidateDialContext(ctx, len(candidates)-index)
		conn, dialErr := p.dialWithContext(attemptCtx, network, net.JoinHostPort(ip.String(), port))
		attemptErr := attemptCtx.Err()
		cancel()
		if err := ctx.Err(); err != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return nil, err
		}
		if attemptErr != nil {
			if conn != nil {
				_ = conn.Close()
			}
			if len(failures) < maxPreservedDialFailures {
				failures = append(failures, attemptErr)
			}
			continue
		}
		if dialErr == nil && conn != nil {
			return conn, nil
		}
		if conn != nil {
			_ = conn.Close()
		}
		if dialErr == nil {
			dialErr = errors.New("dialer returned no connection")
		}
		if len(failures) < maxPreservedDialFailures {
			failures = append(failures, dialErr)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, networkDialFailures{causes: failures}
}

func candidateDialContext(ctx context.Context, remainingCandidates int) (context.Context, context.CancelFunc) {
	timeout := maxCandidateDialTime
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.WithCancel(ctx)
		}
		share := remaining / time.Duration(max(1, remainingCandidates))
		if share < timeout {
			timeout = share
		}
	}
	return context.WithTimeout(ctx, timeout)
}

type dialResult struct {
	conn net.Conn
	err  error
}

func (p *NetworkPolicy) dialWithContext(ctx context.Context, network, address string) (net.Conn, error) {
	select {
	case p.dialSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	result := make(chan dialResult)
	go func() {
		defer func() { <-p.dialSlots }()
		conn, err := p.dialer.DialContext(ctx, network, address)
		select {
		case result <- dialResult{conn: conn, err: err}:
		case <-ctx.Done():
			if conn != nil {
				_ = conn.Close()
			}
		}
	}()
	select {
	case outcome := <-result:
		return outcome.conn, outcome.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Conservative denylist from the IANA IPv4/IPv6 Special-Purpose registries.
// Keep special-purpose ranges blocked even when an OS considers them global unicast.
var blockedDiagnosticNetworks = mustCIDRs(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.31.196.0/24",
	"192.52.193.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"192.175.48.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"100.100.100.200/32",
	"::/128",
	"::1/128",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"100:0:0:1::/64",
	"2001::/23",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
	"2001:db8::/32",
	"2002::/16",
	"2620:4f:8000::/48",
	"3fff::/20",
	"5f00::/16",
)

func mustCIDRs(values ...string) []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			panic(err)
		}
		networks = append(networks, network)
	}
	return networks
}

func IsPublicDiagnosticIP(ip net.IP) bool {
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, network := range blockedDiagnosticNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func networkPolicyResult(kind Kind, address string, started time.Time, err error) Result {
	result := baseResult(kind, address, started, err)
	if errors.Is(err, ErrNetworkPolicyBlocked) {
		result.ErrorCode = "network_policy_blocked"
		result.Message = ErrNetworkPolicyBlocked.Error()
	}
	return result
}
