package diagnostic

import (
	"context"
	"testing"
	"time"
)

func TestIPContextTypedTextRejectsInvalidUTF8(t *testing.T) {
	wire := contextFixtureBytes(t, contextFixture{})
	base, err := ParseIPContext(wire, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"handle", "name", "type", "organization"} {
		t.Run(field, func(t *testing.T) {
			v := cloneIPContext(base)
			fields := map[string]*string{"handle": &v.Registration.Handle, "name": &v.Registration.Name, "type": &v.Registration.Type, "organization": &v.Registration.Organization}
			*fields[field] = string([]byte{255})
			if _, err := MarshalIPContext(v); err == nil {
				t.Fatal("invalid UTF-8 repaired by encoding/json before validation")
			}
		})
	}
}

func TestIPContextRejectsInconsistentTypedSnapshot(t *testing.T) {
	s := contextTestService(contextTestResolver{})
	v, err := s.Lookup(context.Background(), "1.1.1.1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*IPContext)
	}{
		{"version", func(v *IPContext) { v.SchemaVersion = 2 }}, {"private", func(v *IPContext) { v.Address = "127.0.0.1" }},
		{"root-source", func(v *IPContext) { v.Source = "invented" }}, {"null-names", func(v *IPContext) { v.ReverseDNS.Names = nil }},
		{"false-ok", func(v *IPContext) { v.ReverseDNS.Status = "ok" }}, {"failure-facts", func(v *IPContext) { v.Registration.Name = "leak" }},
		{"false-route", func(v *IPContext) { v.Routing.Status = "ok"; v.Routing.Prefix = "8.8.8.0/24" }},
		{"wrong-expiry", func(v *IPContext) { v.ExpiresAt = v.FetchedAt }}, {"late-bundle", func(v *IPContext) { v.Registration.FetchedAt = "9999-01-01T00:00:00.000Z" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := cloneIPContext(v)
			tc.mutate(&copy)
			if _, err := MarshalIPContext(copy); err == nil {
				t.Fatal("inconsistent snapshot published")
			}
		})
	}
}
