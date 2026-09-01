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

type IPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type NetworkPolicy struct {
	resolver IPResolver
	dialer   Dialer
}

func NewNetworkPolicy(resolver IPResolver, dialer Dialer) *NetworkPolicy {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	return &NetworkPolicy{resolver: resolver, dialer: dialer}
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
		return nil, err
	}
	return p.dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
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
