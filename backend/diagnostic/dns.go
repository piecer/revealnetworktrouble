package diagnostic

import (
	"context"
	"net"
	"time"
)

type DNSChecker struct {
	Resolver *net.Resolver
	Policy   *NetworkPolicy
}

func (DNSChecker) Kind() Kind { return KindDNS }

func (c DNSChecker) Check(ctx context.Context, target Target) Result {
	started := time.Now().UTC()
	var addresses []string
	var err error
	if c.Policy != nil {
		var resolved []net.IP
		resolved, err = c.Policy.Resolve(ctx, target.Address)
		for _, address := range resolved {
			addresses = append(addresses, address.String())
		}
	} else {
		resolver := c.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		addresses, err = resolver.LookupHost(ctx, target.Address)
	}
	result := networkPolicyResult(KindDNS, target.Address, started, err)
	if err == nil {
		result.Details = map[string]any{"addresses": addresses, "answer_count": len(addresses)}
	}
	return result
}
