package diagnostic

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestIPContextRoutingRPKIProjection(t *testing.T) {
	for _, tc := range []struct {
		name, bgp      string
		status         string
		count, omitted int
	}{
		{"origins", `{"prefix":"1.1.1.0/24","asns":["4294967295",13335,2,1,3,2]}`, "limited", 4, 1},
		{"none", `{"prefix":"","asns":[]}`, "not_found", 0, 0},
		{"over-raw-origins", `{"prefix":"1.1.1.0/24","asns":[` + strings.TrimSuffix(strings.Repeat("1,", 65), ",") + `]}`, "invalid_response", 0, 0},
		{"foreign", `{"prefix":"8.8.8.0/24","asns":[13335]}`, "invalid_response", 0, 0},
		{"host-bits", `{"prefix":"1.1.1.1/24","asns":[13335]}`, "invalid_response", 0, 0},
		{"zero-asn", `{"prefix":"1.1.1.0/24","asns":[0]}`, "invalid_response", 0, 0},
		{"fractional-asn", `{"prefix":"1.1.1.0/24","asns":[1.00000000000000001]}`, "invalid_response", 0, 0},
		{"integer-exponent", `{"prefix":"1.1.1.0/24","asns":[1.3335e4]}`, "ok", 1, 0},
		{"contradiction", `{"prefix":"1.1.1.0/24","asns":[]}`, "invalid_response", 0, 0},
		{"missing", `{}`, "invalid_response", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, HTTP: func(c context.Context, u string) (int, []byte, error) {
				if strings.Contains(u, "network-info") {
					return 200, []byte(`{"status":"ok","data":` + tc.bgp + `}`), nil
				}
				if strings.Contains(u, "rpki-validation") {
					calls++
					parsed, _ := url.Parse(u)
					asn := parsed.Query().Get("resource")
					prefix := parsed.Query().Get("prefix")
					validity := []string{"valid", "invalid_asn", "invalid_length", "unknown"}[(calls-1)%4]
					return 200, []byte(fmt.Sprintf(`{"status":"ok","data":{"resource":%q,"prefix":%q,"status":%q}}`, asn, prefix, validity)), nil
				}
				return 404, nil, nil
			}})
			got, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			v := got.Routing
			if v.Status != tc.status || len(v.Origins) != tc.count || v.Omitted != tc.omitted || calls != tc.count {
				t.Fatalf("got %+v calls %d", v, calls)
			}
			for i, o := range v.Origins {
				if o.RPKI.Status != "ok" || o.RPKI.Validity != []string{"valid", "invalid_asn", "invalid_length", "unknown"}[i%4] || (i > 0 && v.Origins[i-1].ASN >= o.ASN) {
					t.Fatalf("origins %+v", v.Origins)
				}
			}
		})
	}
}
func TestIPContextRPKIIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		code       int
		want       string
	}{
		{"foreign-asn", `{"resource":"13336","prefix":"1.1.1.0/24","status":"valid"}`, 200, "invalid_response"},
		{"foreign-prefix", `{"resource":"13335","prefix":"1.1.0.0/16","status":"valid"}`, 200, "invalid_response"},
		{"unknown-validity", `{"resource":"13335","prefix":"1.1.1.0/24","status":"healthy"}`, 200, "invalid_response"},
		{"missing-identity", `{"status":"valid"}`, 200, "invalid_response"},
		{"rate", `{}`, 429, "rate_limited"}, {"404", `{}`, 404, "unavailable"}, {"redirect", `{}`, 302, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, HTTP: func(c context.Context, u string) (int, []byte, error) {
				if strings.Contains(u, "network-info") {
					return 200, []byte(`{"status":"ok","data":{"prefix":"1.1.1.0/24","asns":[13335]}}`), nil
				}
				if strings.Contains(u, "rpki-validation") {
					return tc.code, []byte(`{"status":"ok","data":` + tc.data + `}`), nil
				}
				return 404, nil, nil
			}})
			got, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if got.Routing.Status != "ok" || len(got.Routing.Origins) != 1 || got.Routing.Origins[0].RPKI.Status != tc.want || got.Routing.Origins[0].RPKI.Validity != "" {
				t.Fatalf("got %+v", got.Routing)
			}
		})
	}
}
