package diagnostic

import (
	"context"
	"net"
	"time"
)

type DNSChecker struct {
	Resolver *net.Resolver
}

func (DNSChecker) Kind() Kind { return KindDNS }

func (c DNSChecker) Check(ctx context.Context, target Target) Result {
	started := time.Now().UTC()
	resolver := c.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupHost(ctx, target.Address)
	result := baseResult(KindDNS, target.Address, started, err)
	if err == nil {
		result.Details = map[string]any{"addresses": addresses, "answer_count": len(addresses)}
	}
	return result
}
