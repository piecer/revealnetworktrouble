package diagnostic

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"
)

func contextDNSName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return false
			}
		}
	}
	return true
}
func contextFailure(err error) string {
	if errors.Is(err, ErrIPContextJSON) {
		return "invalid_response"
	}
	var n net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &n) && n.Timeout()) {
		return "timeout"
	}
	return "unavailable"
}
func contextDNSFailure(err error) string {
	var d *net.DNSError
	if errors.As(err, &d) && d.IsNotFound {
		return "not_found"
	}
	return contextFailure(err)
}
func (s *IPContextService) reverse(ctx context.Context, address string) (out IPContextReverse) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out = IPContextReverse{Status: "not_found", Source: "system_resolver", Names: []IPContextName{}}
	defer func() { out.FetchedAt = ipContextTime(s.now()) }()
	raw, err := contextOwnedCall(ctx, func() ([]string, error) { return s.resolver.LookupAddr(ctx, address) })
	if err != nil {
		out.Status = contextDNSFailure(err)
		return
	}
	if len(raw) > 64 {
		out.Status = "invalid_response"
		return
	}
	unique := map[string]bool{}
	for _, name := range raw {
		if len(name) == 0 || len(name) > 254 {
			out.Status = "invalid_response"
			return
		}
		for i := 0; i < len(name); i++ {
			if name[i] >= 128 {
				out.Status = "invalid_response"
				return
			}
		}
		name = strings.ToLower(strings.TrimSuffix(name, "."))
		if !contextDNSName(name) {
			out.Status = "invalid_response"
			return
		}
		unique[name] = true
	}
	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return
	}
	out.Status = "ok"
	if len(names) > 8 {
		out.Status = "limited"
		out.Omitted = len(names) - 8
		names = names[:8]
	}
	for _, name := range names {
		status := "timeout"
		if ctx.Err() == nil {
			ips, err := contextOwnedCall(ctx, func() ([]net.IPAddr, error) { return s.resolver.LookupIPAddr(ctx, name+".") })
			status = forwardContextStatus(address, ips, err)
		}
		out.Names = append(out.Names, IPContextName{name, status})
	}
	return
}
func forwardContextStatus(address string, ips []net.IPAddr, err error) string {
	if err != nil {
		return contextDNSFailure(err)
	}
	if len(ips) > 16 {
		return "limited"
	}
	if len(ips) == 0 {
		return "not_found"
	}
	confirmed := false
	for _, ip := range ips {
		if ip.Zone != "" || ip.IP == nil || ip.IP.To16() == nil {
			return "invalid_response"
		}
		if ip.IP.Equal(net.ParseIP(address)) {
			confirmed = true
		}
	}
	if confirmed {
		return "confirmed"
	}
	return "mismatch"
}
