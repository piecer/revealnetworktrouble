package diagnostic

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

type contextTestResolver struct {
	reverse func(context.Context, string) ([]string, error)
	forward func(context.Context, string) ([]net.IPAddr, error)
}

func (r contextTestResolver) LookupAddr(c context.Context, a string) ([]string, error) {
	if r.reverse != nil {
		return r.reverse(c, a)
	}
	return nil, nil
}
func (r contextTestResolver) LookupIPAddr(c context.Context, a string) ([]net.IPAddr, error) {
	if r.forward != nil {
		return r.forward(c, a)
	}
	return nil, nil
}
func contextTestService(r IPContextResolver) *IPContextService {
	return NewIPContextService(IPContextOptions{Resolver: r, HTTP: func(context.Context, string) (int, []byte, error) { return 404, nil, nil }})
}
func TestIPContextReverseProjection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		names    []string
		err      error
		status   string
		retained int
		omitted  int
	}{
		{"sort-dedupe", []string{"Z.example.", "a.EXAMPLE.", "z.example", "_srv.example."}, nil, "ok", 3, 0},
		{"empty", nil, nil, "not_found", 0, 0},
		{"exact-eight", []string{"h.ex", "g.ex", "f.ex", "e.ex", "d.ex", "c.ex", "b.ex", "a.ex"}, nil, "ok", 8, 0}, {"nxdomain", nil, &net.DNSError{IsNotFound: true}, "not_found", 0, 0},
		{"invalid-label", []string{"-bad.example"}, nil, "invalid_response", 0, 0},
		{"long-label", []string{strings.Repeat("x", 64) + ".example"}, nil, "invalid_response", 0, 0},
		{"double-dot", []string{"a.example.."}, nil, "invalid_response", 0, 0},
		{"over-raw", make([]string, 65), nil, "invalid_response", 0, 0},
		{"limited", []string{"i.ex", "h.ex", "g.ex", "f.ex", "e.ex", "d.ex", "c.ex", "b.ex", "a.ex"}, nil, "limited", 8, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			service := contextTestService(contextTestResolver{reverse: func(context.Context, string) ([]string, error) { return tc.names, tc.err }, forward: func(c context.Context, n string) ([]net.IPAddr, error) {
				calls++
				if !strings.HasSuffix(n, ".") {
					t.Error("search expansion")
				}
				return nil, nil
			}})
			got, err := service.Lookup(context.Background(), "1.1.1.1", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			v := got.ReverseDNS
			if v.Status != tc.status || len(v.Names) != tc.retained || v.Omitted != tc.omitted || calls != tc.retained {
				t.Fatalf("got %+v forwards %d", v, calls)
			}
			for i, n := range v.Names {
				if n.ForwardStatus != "not_found" || (i > 0 && v.Names[i-1].Name >= n.Name) {
					t.Fatalf("wrong names %+v", v.Names)
				}
			}
		})
	}
}
func TestIPContextForwardProjection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ips    []net.IPAddr
		err    error
		status string
	}{
		{"confirmed", []net.IPAddr{{IP: net.ParseIP("::ffff:1.1.1.1")}}, nil, "confirmed"},
		{"mismatch", []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil, "mismatch"},
		{"malformed", []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: nil}}, nil, "invalid_response"},
		{"too-many", make([]net.IPAddr, 17), nil, "limited"},
		{"not-found", nil, &net.DNSError{IsNotFound: true}, "not_found"},
		{"timeout", nil, context.DeadlineExceeded, "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := contextTestService(contextTestResolver{reverse: func(context.Context, string) ([]string, error) { return []string{"one.ex"}, nil }, forward: func(context.Context, string) ([]net.IPAddr, error) { return tc.ips, tc.err }})
			got, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if got.ReverseDNS.Status != "ok" || got.ReverseDNS.Names[0].ForwardStatus != tc.status {
				t.Fatalf("got %+v", got.ReverseDNS)
			}
		})
	}
}
