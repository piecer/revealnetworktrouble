package diagnostic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const contextRDAPFixture = `{"objectClassName":"ip network","startAddress":"1.1.1.0","endAddress":"1.1.1.255","handle":"h","name":"Network <>&","type":"ALLOCATED","country":"AU","events":[{"eventAction":"registration","eventDate":"2020-01-01T00:00:00Z"},{"eventAction":"registration","eventDate":"2019-01-01T00:00:00Z"},{"eventAction":"last changed","eventDate":"2024-01-01T00:00:00+01:00"}],"entities":[{"roles":["registrant"],"vcardArray":["vcard",[["kind",{},"text","individual"],["fn",{},"text","PERSON_CANARY"]]]},{"roles":["registrant"],"vcardArray":["vcard",[["kind",{},"text","org"],["fn",{},"text","Organization"],["email",{},"text","CONTACT_CANARY"]]]}],"links":[{"href":"RAW_URL_CANARY"}]}`

func TestIPContextRDAPProjection(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		status     string
	}{
		{"success", contextRDAPFixture, 200, "ok"},
		{"foreign-range", strings.ReplaceAll(contextRDAPFixture, "1.1.1.", "8.8.8."), 200, "invalid_response"},
		{"event-offset-hour", strings.Replace(contextRDAPFixture, "2020-01-01T00:00:00Z", "2020-01-01T00:00:00+24:00", 1), 200, "invalid_response"},
		{"event-comma-fraction", strings.Replace(contextRDAPFixture, "2020-01-01T00:00:00Z", "2020-01-01T00:00:00,1Z", 1), 200, "invalid_response"},
		{"null-name", strings.Replace(contextRDAPFixture, `"name":"Network <>&"`, `"name":null`, 1), 200, "invalid_response"},
		{"vcard-kind-type", strings.Replace(contextRDAPFixture, `["kind",{},"text","org"]`, `["kind",{},"uri","org"]`, 1), 200, "invalid_response"},
		{"vcard-fn-parameters", strings.Replace(contextRDAPFixture, `["fn",{},"text","Organization"]`, `["fn",null,"text","Organization"]`, 1), 200, "invalid_response"},
		{"duplicate", strings.Replace(contextRDAPFixture, `"handle":"h"`, `"handle":"h","handle":"x"`, 1), 200, "invalid_response"},
		{"rate", contextRDAPFixture, 429, "rate_limited"}, {"missing", "", 404, "not_found"}, {"redirect", contextRDAPFixture, 302, "unavailable"},
		{"too-big", contextRDAPFixture + strings.Repeat(" ", 262144), 200, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, HTTP: func(c context.Context, u string) (int, []byte, error) {
				if strings.Contains(u, "network-info") {
					return 404, nil, nil
				}
				if u != "https://rdap.apnic.net/ip/1.1.1.1" {
					t.Error(u)
				}
				return tc.code, []byte(tc.body), nil
			}})
			got, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			v := got.Registration
			if v.Status != tc.status || v.Registry != "apnic" {
				t.Fatalf("got %+v", v)
			}
			b, _ := json.Marshal(v)
			if strings.Contains(string(b), "CANARY") {
				t.Fatal(string(b))
			}
			if tc.status == "ok" && (v.Organization != "Organization" || v.RegisteredAt != "2019-01-01T00:00:00.000Z" || v.UpdatedAt != "2023-12-31T23:00:00.000Z") {
				t.Fatalf("projection %+v", v)
			}
		})
	}
}
func TestIPContextBootstrapSelection(t *testing.T) {
	for _, tc := range []struct{ address, base, registry string }{
		{"8.8.8.8", "https://rdap.arin.net/registry/", "arin"}, {"1.1.1.1", "https://rdap.apnic.net/", "apnic"}, {"41.1.1.1", "https://rdap.afrinic.net/rdap/", "afrinic"}, {"177.1.1.1", "https://rdap.lacnic.net/rdap/", "lacnic"}, {"2.1.1.1", "https://rdap.db.ripe.net/", "ripe"}, {"2001:4860:4860::8888", "https://rdap.arin.net/registry/", "arin"}, {"4000::1", "", ""},
	} {
		t.Run(tc.address, func(t *testing.T) {
			calls := 0
			s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, HTTP: func(c context.Context, u string) (int, []byte, error) {
				if !strings.Contains(u, "network-info") {
					calls++
					if u != tc.base+"ip/"+tc.address {
						t.Error(u)
					}
				}
				return 404, nil, nil
			}})
			got, err := s.Lookup(context.Background(), tc.address, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if tc.base == "" {
				want = 0
			}
			if got.Registration.Registry != tc.registry || calls != want {
				t.Fatalf("registry=%s calls=%d", got.Registration.Registry, calls)
			}
		})
	}
}
