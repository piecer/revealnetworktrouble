package diagnostic

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIPContextRDAPEventDateAdmission(t *testing.T) {
	for _, action := range []string{"registration", "last changed"} {
		t.Run(action, func(t *testing.T) {
			for _, tc := range []struct {
				name, date, normalized string
			}{
				{"single-hour-zero", "2020-01-01T0:00:00Z", ""},
				{"single-hour-offset", "2020-01-01T1:02:03+01:00", ""},
				{"single-hour-fraction-offset", "2020-02-29T9:02:03.5-02:30", ""},
				{"two-digit-zero", "2020-01-01T00:00:00Z", "2020-01-01T00:00:00.000Z"},
				{"two-digit-offset", "2020-01-01T01:02:03+01:00", "2020-01-01T00:02:03.000Z"},
				{"fraction-positive-offset-leap-day", "2020-02-29T01:02:03.123456789+01:00", "2020-02-29T00:02:03.123Z"},
				{"fraction-negative-offset", "2020-02-29T09:02:03.5-02:30", "2020-02-29T11:32:03.500Z"},
				{"two-digit-late-hour", "2020-01-01T23:59:59Z", "2020-01-01T23:59:59.000Z"},
				{"empty", "", ""},
				{"missing-zone", "2020-01-01T00:00:00", ""},
				{"invalid-gregorian-date", "2019-02-29T00:00:00Z", ""},
				{"comma-fraction", "2020-01-01T00:00:00,1Z", ""},
				{"offset-hour-range", "2020-01-01T00:00:00+24:00", ""},
				{"offset-minute-range", "2020-01-01T00:00:00-00:60", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var object map[string]any
					if err := json.Unmarshal([]byte(contextRDAPFixture), &object); err != nil {
						t.Fatal(err)
					}
					otherAction := "last changed"
					if action == "last changed" {
						otherAction = "registration"
					}
					// A malformed recognized event must discard even earlier valid facts.
					object["events"] = []any{
						map[string]any{"eventAction": otherAction, "eventDate": "2021-06-01T00:00:00Z"},
						map[string]any{"eventAction": action, "eventDate": tc.date},
					}
					rdap, err := json.Marshal(object)
					if err != nil {
						t.Fatal(err)
					}
					var reverse, forward, registration, bgp, rpki atomic.Int32
					now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
					var clock atomic.Int64
					clock.Store(now.UnixNano())
					s := NewIPContextService(IPContextOptions{
						Now: func() time.Time { return time.Unix(0, clock.Load()) },
						Resolver: contextTestResolver{
							reverse: func(context.Context, string) ([]string, error) {
								reverse.Add(1)
								return []string{"One.Example."}, nil
							},
							forward: func(context.Context, string) ([]net.IPAddr, error) {
								forward.Add(1)
								return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
							},
						},
						HTTP: func(_ context.Context, u string) (int, []byte, error) {
							switch {
							case strings.Contains(u, "network-info"):
								bgp.Add(1)
								return 200, []byte(`{"status":"ok","data":{"prefix":"1.1.1.0/24","asns":[13335]}}`), nil
							case strings.Contains(u, "rpki-validation"):
								rpki.Add(1)
								return 200, []byte(`{"status":"ok","data":{"resource":"13335","prefix":"1.1.1.0/24","status":"valid"}}`), nil
							default:
								registration.Add(1)
								if u != "https://rdap.apnic.net/ip/1.1.1.1" {
									t.Error(u)
								}
								return 200, rdap, nil
							}
						},
					})
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						defer cancel()
						if err := s.Close(ctx); err != nil {
							t.Error(err)
						}
					})
					got, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
					if err != nil {
						t.Fatal(err)
					}
					wire, err := MarshalIPContext(got)
					if err != nil {
						t.Fatal(err)
					}
					const fetchedAt = "2026-01-02T03:04:05.000Z"
					wantRegistration := IPContextRegistration{Status: "invalid_response", Source: "rdap", Registry: "apnic", FetchedAt: fetchedAt}
					wantExpiry := "2026-01-02T03:04:35.000Z"
					if tc.normalized != "" {
						wantRegistration.Status = "ok"
						wantRegistration.StartAddress, wantRegistration.EndAddress = "1.1.1.0", "1.1.1.255"
						wantRegistration.Handle, wantRegistration.Name = "h", "Network <>&"
						wantRegistration.Type, wantRegistration.Country, wantRegistration.Organization = "ALLOCATED", "AU", "Organization"
						wantRegistration.RegisteredAt, wantRegistration.UpdatedAt = "2021-06-01T00:00:00.000Z", "2021-06-01T00:00:00.000Z"
						if action == "registration" {
							wantRegistration.RegisteredAt = tc.normalized
						} else {
							wantRegistration.UpdatedAt = tc.normalized
						}
						wantExpiry = "2026-01-02T03:09:05.000Z"
					}
					if got.Registration != wantRegistration {
						t.Errorf("event %q: registration=%+v; want %+v", tc.date, got.Registration, wantRegistration)
					}
					if got.Source != "upstream" || got.FetchedAt != fetchedAt || got.ExpiresAt != wantExpiry {
						t.Errorf("source/expiry: source=%s fetched=%s expiry=%s; want upstream/%s/%s", got.Source, got.FetchedAt, got.ExpiresAt, fetchedAt, wantExpiry)
					}
					wantReverse := IPContextReverse{Status: "ok", Source: "system_resolver", FetchedAt: fetchedAt, Names: []IPContextName{{Name: "one.example", ForwardStatus: "confirmed"}}}
					wantRouting := IPContextRouting{Status: "ok", Source: "ripe_ris", FetchedAt: fetchedAt, Prefix: "1.1.1.0/24", Origins: []IPContextOrigin{{ASN: 13335, RPKI: IPContextRPKI{Status: "ok", Source: "ripe_rpki", CheckedAt: fetchedAt, Validity: "valid"}}}}
					if !reflect.DeepEqual(got.ReverseDNS, wantReverse) || !reflect.DeepEqual(got.Routing, wantRouting) {
						t.Errorf("unrelated facts changed: reverse=%+v routing=%+v", got.ReverseDNS, got.Routing)
					}
					if tc.normalized == "" {
						var decoded struct {
							Registration map[string]any `json:"registration"`
						}
						if err := json.Unmarshal(wire, &decoded); err != nil {
							t.Fatal(err)
						}
						want := map[string]any{"status": "invalid_response", "source": "rdap", "registry": "apnic", "fetched_at": fetchedAt}
						if !reflect.DeepEqual(decoded.Registration, want) {
							t.Errorf("registration facts must be absent on wire: %+v", decoded.Registration)
						}
					}
					calls := func() [5]int32 {
						return [5]int32{reverse.Load(), forward.Load(), registration.Load(), bgp.Load(), rpki.Load()}
					}
					if got := calls(); got != [5]int32{1, 1, 1, 1, 1} {
						t.Fatalf("cold calls=%v", got)
					}
					clock.Store(now.Add(time.Second).UnixNano())
					cached, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
					if err != nil || cached.Source != "cache" {
						t.Fatalf("second lookup: source=%s err=%v", cached.Source, err)
					}
					cachedWire, err := MarshalIPContext(cached)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(cachedWire, bytes.Replace(wire, []byte(`"source":"upstream"`), []byte(`"source":"cache"`), 1)) {
						t.Errorf("cache changed snapshot beyond source: %s", cachedWire)
					}
					if got := calls(); got != [5]int32{1, 1, 1, 1, 1} {
						t.Errorf("cache initiated provider work: %v", got)
					}
				})
			}
		})
	}
}
