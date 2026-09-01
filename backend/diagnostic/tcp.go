package diagnostic

import (
	"context"
	"net"
	"time"
)

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type TCPChecker struct {
	Dialer Dialer
	Policy *NetworkPolicy
}

func (TCPChecker) Kind() Kind { return KindTCP }

func (c TCPChecker) Check(ctx context.Context, target Target) Result {
	started := time.Now().UTC()
	var dialer Dialer = c.Dialer
	if c.Policy != nil {
		dialer = c.Policy
	} else if dialer == nil {
		dialer = &net.Dialer{}
	}
	conn, err := dialer.DialContext(ctx, "tcp", target.Address)
	result := networkPolicyResult(KindTCP, target.Address, started, err)
	if err == nil {
		result.Details = map[string]any{"local_address": conn.LocalAddr().String(), "remote_address": conn.RemoteAddr().String()}
		_ = conn.Close()
	}
	return result
}
