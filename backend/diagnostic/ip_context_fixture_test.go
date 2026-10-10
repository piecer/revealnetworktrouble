package diagnostic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type contextCorpusCase struct {
	ID         string          `json:"id"`
	Origin     string          `json:"origin"`
	Address    string          `json:"requested_address"`
	Accept     bool            `json:"accept"`
	Wire       string          `json:"wire_base64"`
	Projection json.RawMessage `json:"projection,omitempty"`
}
type contextCorpus struct {
	SchemaVersion  int                 `json:"schema_version"`
	ContractSHA256 string              `json:"contract_sha256"`
	Cases          []contextCorpusCase `json:"cases"`
}
type contextFixture struct {
	address, reverse, forward, registration, routing, rpki, validity string
	cache, maximum                                                   bool
	now                                                              time.Time
}

func contextFixtureBytes(t *testing.T, f contextFixture) []byte {
	t.Helper()
	if f.address == "" {
		f.address = "1.1.1.1"
	}
	if f.now.IsZero() {
		f.now = time.Date(2026, 1, 2, 3, 4, 5, 6000000, time.UTC)
	}
	defaults := []*string{&f.reverse, &f.registration, &f.routing, &f.rpki}
	for _, s := range defaults {
		if *s == "" {
			*s = "ok"
		}
	}
	if f.forward == "" {
		f.forward = "confirmed"
	}
	if f.validity == "" {
		f.validity = "valid"
	}
	resolver := contextTestResolver{
		reverse: func(context.Context, string) ([]string, error) {
			switch f.reverse {
			case "not_found":
				return nil, nil
			case "timeout":
				return nil, context.DeadlineExceeded
			case "unavailable":
				return nil, errors.New("PRIVATE_DNS_FAILURE")
			case "invalid_response":
				return make([]string, 65), nil
			case "limited":
				names := []string{}
				for i := 0; i < 64; i++ {
					names = append(names, fmt.Sprintf("n%02d.example.", i))
				}
				return names, nil
			}
			if f.maximum {
				name := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
				if len(name) != 253 {
					t.Fatal("name boundary")
				}
				return []string{name}, nil
			}
			return []string{"One.Example.", "one.example."}, nil
		},
		forward: func(context.Context, string) ([]net.IPAddr, error) {
			switch f.forward {
			case "not_found":
				return nil, nil
			case "mismatch":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
			case "limited":
				return make([]net.IPAddr, 17), nil
			case "invalid_response":
				return []net.IPAddr{{IP: nil}}, nil
			case "timeout":
				return nil, context.DeadlineExceeded
			case "unavailable":
				return nil, errors.New("PRIVATE_FORWARD_FAILURE")
			}
			ips := []net.IPAddr{{IP: net.ParseIP(f.address)}}
			if f.maximum {
				for len(ips) < 16 {
					ips = append(ips, ips[0])
				}
			}
			return ips, nil
		},
	}
	provider := func(_ context.Context, rawURL string) (int, []byte, error) {
		u, _ := url.Parse(rawURL)
		status := f.registration
		if strings.Contains(u.Path, "network-info") {
			status = f.routing
		}
		if strings.Contains(u.Path, "rpki-validation") {
			status = f.rpki
		}
		switch status {
		case "timeout":
			return 0, nil, context.DeadlineExceeded
		case "unavailable":
			return 503, []byte("PRIVATE_HTTP_FAILURE"), nil
		case "rate_limited":
			return 429, nil, nil
		case "invalid_response":
			return 200, []byte(`{"duplicate":1,"duplicate":2}`), nil
		}
		if strings.Contains(u.Path, "network-info") {
			if status == "not_found" {
				return 200, []byte(`{"status":"ok","data":{"prefix":"","asns":[]}}`), nil
			}
			prefix := f.address + "/32"
			if strings.Contains(f.address, ":") {
				prefix = f.address + "/128"
			}
			asns := []uint32{1, 13335, 4294967295}
			if status == "limited" {
				asns = nil
				for i := 1; i <= 64; i++ {
					asns = append(asns, uint32(i))
				}
			}
			data, _ := json.Marshal(map[string]any{"status": "ok", "data": map[string]any{"prefix": prefix, "asns": asns}})
			return 200, data, nil
		}
		if strings.Contains(u.Path, "rpki-validation") {
			data, _ := json.Marshal(map[string]any{"status": "ok", "data": map[string]any{"resource": u.Query().Get("resource"), "prefix": u.Query().Get("prefix"), "status": f.validity}})
			return 200, data, nil
		}
		if status == "not_found" {
			return 404, nil, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(contextRDAPFixture), &m); err != nil {
			t.Fatal(err)
		}
		m["startAddress"], m["endAddress"] = f.address, f.address
		if f.maximum {
			m["handle"] = strings.Repeat("<", 256)
			m["name"] = strings.Repeat("&", 256)
			m["type"] = strings.Repeat("\x00", 256)
			m["entities"] = []any{map[string]any{"roles": []string{"registrant"}, "vcardArray": []any{"vcard", []any{[]any{"kind", map[string]any{}, "text", "org"}, []any{"fn", map[string]any{}, "text", strings.Repeat("é", 128)}}}}}
		}
		b, _ := json.Marshal(m)
		return 200, b, nil
	}
	s := NewIPContextService(IPContextOptions{Resolver: resolver, HTTP: provider, Now: func() time.Time { return f.now }})
	v, err := s.Lookup(context.Background(), f.address, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if f.cache {
		v, err = s.Lookup(context.Background(), f.address, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := MarshalIPContext(v)
	if err != nil {
		t.Fatalf("fixture %+v: %v snapshot %+v", f, err, v)
	}
	return b
}
func contextMutate(t *testing.T, wire []byte, path string, fn func(map[string]any)) []byte {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(wire))
	d.UseNumber()
	var root map[string]any
	if d.Decode(&root) != nil {
		t.Fatal("mutation base")
	}
	var node any = root
	if path != "" {
		for _, key := range strings.Split(path, ".") {
			if key == "0" {
				node = node.([]any)[0]
			} else {
				node = node.(map[string]any)[key]
			}
		}
	}
	fn(node.(map[string]any))
	b, e := json.Marshal(root)
	if e != nil {
		t.Fatal(e)
	}
	return append(b, '\n')
}
func buildIPContextCorpus(t *testing.T) []byte {
	t.Helper()
	corpus := contextCorpus{SchemaVersion: 1, ContractSHA256: "0f3e987b665c231a82d2c20fdaac4cfb8f54593fe82c4262012df4829be339b0", Cases: []contextCorpusCase{}}
	add := func(id, origin, address string, wire []byte, accept bool) {
		row := contextCorpusCase{ID: id, Origin: origin, Address: address, Wire: base64.StdEncoding.EncodeToString(wire), Accept: accept}
		if accept {
			v, e := ParseIPContext(wire, address)
			if e != nil {
				t.Fatalf("valid control %s rejected: %v", id, e)
			}
			b, e := MarshalIPContext(v)
			if e != nil {
				t.Fatal(e)
			}
			row.Projection = bytes.TrimSpace(b)
		}
		corpus.Cases = append(corpus.Cases, row)
	}
	produce := func(id string, f contextFixture) {
		a := f.address
		if a == "" {
			a = "1.1.1.1"
		}
		add(id, "producer", a, contextFixtureBytes(t, f), true)
	}
	produce("ipv4", contextFixture{})
	produce("ipv6", contextFixture{address: "2001:4860:4860::8888"})
	produce("cache", contextFixture{cache: true})
	produce("maximum-text-and-forward-count", contextFixture{maximum: true})
	for _, a := range []string{"8.8.8.8", "41.1.1.1", "177.1.1.1", "2.1.1.1", "4000::1"} {
		produce("registry-"+a, contextFixture{address: a})
	}
	for _, status := range []string{"ok", "limited", "not_found", "timeout", "unavailable", "invalid_response"} {
		produce("reverse-"+status, contextFixture{reverse: status})
	}
	for _, status := range []string{"confirmed", "mismatch", "not_found", "timeout", "unavailable", "invalid_response", "limited"} {
		produce("forward-"+status, contextFixture{forward: status})
	}
	for _, status := range []string{"ok", "not_found", "timeout", "unavailable", "rate_limited", "invalid_response"} {
		produce("registration-"+status, contextFixture{registration: status})
	}
	for _, status := range []string{"ok", "limited", "not_found", "timeout", "unavailable", "rate_limited", "invalid_response"} {
		produce("routing-"+status, contextFixture{routing: status})
	}
	for _, status := range []string{"ok", "timeout", "unavailable", "rate_limited", "invalid_response"} {
		produce("rpki-"+status, contextFixture{rpki: status})
	}
	for _, validity := range []string{"valid", "invalid_asn", "invalid_length", "unknown"} {
		produce("validity-"+validity, contextFixture{validity: validity})
	}
	produce("year-min", contextFixture{now: time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)})
	produce("year-max", contextFixture{now: time.Date(9999, 12, 31, 23, 54, 59, 999000000, time.UTC)})
	base := contextFixtureBytes(t, contextFixture{})
	mutate := func(id, path string, fn func(map[string]any), accept bool) {
		add(id, "mutation", "1.1.1.1", contextMutate(t, base, path, fn), accept)
	}
	for _, shape := range []struct{ path, required, optional string }{
		{"", "schema_version address source fetched_at expires_at reverse_dns registration routing", ""},
		{"reverse_dns", "status source fetched_at names omitted", ""},
		{"reverse_dns.names.0", "name forward_status", ""},
		{"registration", "status source fetched_at start_address end_address", "registry handle name type country organization registered_at updated_at"},
		{"routing", "status source fetched_at prefix origins omitted", ""},
		{"routing.origins.0", "asn rpki", ""},
		{"routing.origins.0.rpki", "status source checked_at validity", ""},
	} {
		prefix := shape.path
		if prefix == "" {
			prefix = "root"
		}
		mutate(prefix+"-unknown", shape.path, func(m map[string]any) { m["unknown"] = false }, false)
		for _, key := range strings.Fields(shape.required) {
			mutate(prefix+"-missing-"+key, shape.path, func(m map[string]any) { delete(m, key) }, false)
			mutate(prefix+"-null-"+key, shape.path, func(m map[string]any) { m[key] = nil }, false)
		}
		for _, key := range strings.Fields(shape.optional) {
			mutate(prefix+"-absent-"+key, shape.path, func(m map[string]any) { delete(m, key) }, true)
			mutate(prefix+"-null-optional-"+key, shape.path, func(m map[string]any) { m[key] = nil }, false)
		}
	}
	for _, n := range []struct {
		label, value string
		accept       bool
	}{
		{"decimal", "1.0", true}, {"exponent", "1e0", true}, {"long-integer", "1.000000000000000000000000000000", true}, {"rounded-fraction", "1.00000000000000001", false}, {"overflow", "4294967296", false}, {"underflow", "1e-100000", false}, {"string", `"1"`, false}, {"zero", "0", false}, {"negative", "-1", false}, {"nonfinite", "1e999999999999999999999999", false},
	} {
		wire := bytes.Replace(base, []byte(`"schema_version":1`), []byte(`"schema_version":`+n.value), 1)
		add("version-"+n.label, "mutation", "1.1.1.1", wire, n.accept)
	}
	for _, n := range []struct {
		value  string
		accept bool
	}{{"0.0e999999999999999999999", true}, {"-0e3", true}, {"0.00000000000000000001", false}, {`"0"`, false}, {"57", false}} {
		wire := bytes.Replace(base, []byte(`"omitted":0`), []byte(`"omitted":`+n.value), 1)
		add("omitted-number-"+n.value, "mutation", "1.1.1.1", wire, n.accept)
	}
	mutate("foreign-root-address", "", func(m map[string]any) { m["address"] = "8.8.8.8" }, false)
	mutate("range-foreign", "registration", func(m map[string]any) { m["start_address"] = "8.8.8.0"; m["end_address"] = "8.8.8.255" }, false)
	mutate("range-reversed", "registration", func(m map[string]any) { m["start_address"] = "1.1.1.2"; m["end_address"] = "1.1.1.0" }, false)
	mutate("range-family", "registration", func(m map[string]any) { m["end_address"] = "2001:4860::1" }, false)
	mutate("prefix-foreign", "routing", func(m map[string]any) { m["prefix"] = "8.8.8.0/24" }, false)
	mutate("prefix-host-bits", "routing", func(m map[string]any) { m["prefix"] = "1.1.1.1/24" }, false)
	mutate("asn-string", "routing.origins.0", func(m map[string]any) { m["asn"] = "1" }, false)
	mutate("asn-zero", "routing.origins.0", func(m map[string]any) { m["asn"] = 0 }, false)
	mutate("asn-overflow", "routing.origins.0", func(m map[string]any) { m["asn"] = uint64(4294967296) }, false)
	mutate("asn-fraction", "routing.origins.0", func(m map[string]any) { m["asn"] = json.Number("1.00000000000000001") }, false)
	mutate("asn-duplicate", "routing", func(m map[string]any) { a := m["origins"].([]any); a[1] = a[0] }, false)
	mutate("asn-descending", "routing", func(m map[string]any) { a := m["origins"].([]any); a[0], a[1] = a[1], a[0] }, false)
	mutate("name-too-long", "reverse_dns.names.0", func(m map[string]any) { m["name"] = strings.Repeat("a", 254) }, false)
	mutate("name-uppercase", "reverse_dns.names.0", func(m map[string]any) { m["name"] = "ONE.example" }, false)
	mutate("name-duplicate", "reverse_dns", func(m map[string]any) { a := m["names"].([]any); m["names"] = append(a, a[0]) }, false)
	mutate("text-257-bytes", "registration", func(m map[string]any) { m["name"] = strings.Repeat("a", 257) }, false)
	mutate("country-lowercase", "registration", func(m map[string]any) { m["country"] = "au" }, false)
	mutate("country-fabricated", "registration", func(m map[string]any) { m["country"] = "ABC" }, false)
	for _, path := range []string{"reverse_dns", "registration", "routing", "routing.origins.0.rpki"} {
		mutate(path+"-unknown-status", path, func(m map[string]any) { m["status"] = "unknown" }, false)
		mutate(path+"-wrong-source", path, func(m map[string]any) { m["source"] = "invented" }, false)
	}
	for _, path := range []string{"reverse_dns", "registration", "routing"} {
		mutate(path+"-failure-with-facts", path, func(m map[string]any) { m["status"] = "unavailable" }, false)
	}
	mutate("rpki-failure-with-validity", "routing.origins.0.rpki", func(m map[string]any) { m["status"] = "unavailable" }, false)
	mutate("rpki-unknown-validity", "routing.origins.0.rpki", func(m map[string]any) { m["validity"] = "secure" }, false)
	mutate("expires-wrong", "", func(m map[string]any) { m["expires_at"] = m["fetched_at"] }, false)
	for _, stamp := range []string{"2026-02-30T00:00:00.000Z", "0000-01-01T00:00:00.000Z", "2026-01-02T03:04:05Z", "2026-01-02T03:04:05.006+00:00"} {
		mutate("bad-time-"+stamp, "", func(m map[string]any) { m["fetched_at"] = stamp }, false)
	}
	mutate("bundle-after-root", "registration", func(m map[string]any) { m["fetched_at"] = "2027-01-01T00:00:00.000Z" }, false)
	mutate("rpki-after-root", "routing.origins.0.rpki", func(m map[string]any) { m["checked_at"] = "2027-01-01T00:00:00.000Z" }, false)
	add("duplicate-root-key", "mutation", "1.1.1.1", bytes.Replace(base, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_\u0076ersion":1`), 1), false)
	add("unpaired-surrogate", "mutation", "1.1.1.1", bytes.Replace(base, []byte(`"one.example"`), []byte(`"\ud800"`), 1), false)
	add("invalid-utf8", "mutation", "1.1.1.1", bytes.Replace(base, []byte(`"one.example"`), []byte{'"', 255, '"'}, 1), false)
	add("extra-document", "mutation", "1.1.1.1", append(append([]byte{}, base...), []byte(`{}`)...), false)
	padded := append(append([]byte{}, base...), bytes.Repeat([]byte(" "), 16384-len(base))...)
	add("wire-exact-16384", "mutation", "1.1.1.1", padded, true)
	add("wire-over-16384", "mutation", "1.1.1.1", append(padded, ' '), false)
	b, e := json.MarshalIndent(corpus, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	return append(b, '\n')
}
func TestIPContextProducerCorpus(t *testing.T) {
	first := buildIPContextCorpus(t)
	second := buildIPContextCorpus(t)
	if !bytes.Equal(first, second) {
		t.Fatal("non-deterministic producer corpus")
	}
	path := filepath.Join("..", "..", "testdata", "ip-context-corpus.json")
	if os.Getenv("CHECKNETWORK_UPDATE_IP_CONTEXT_FIXTURES") == "1" {
		if err := os.WriteFile(path, first, 0644); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, saved) {
		t.Fatal("stale corpus; run CHECKNETWORK_UPDATE_IP_CONTEXT_FIXTURES=1 go test ./backend/diagnostic -run ^TestIPContextProducerCorpus$ -count=1")
	}
	var corpus contextCorpus
	if json.Unmarshal(saved, &corpus) != nil {
		t.Fatal("corpus JSON")
	}
	seen := map[string]bool{}
	for _, row := range corpus.Cases {
		t.Run(row.ID, func(t *testing.T) {
			if seen[row.ID] {
				t.Fatal("duplicate ID")
			}
			seen[row.ID] = true
			wire, err := base64.StdEncoding.DecodeString(row.Wire)
			if err != nil {
				t.Fatal(err)
			}
			v, err := ParseIPContext(wire, row.Address)
			if (err == nil) != row.Accept {
				t.Fatalf("accept=%v expected %v error=%v", err == nil, row.Accept, err)
			}
			if row.Accept {
				b, e := MarshalIPContext(v)
				var projection bytes.Buffer
				if err := json.Compact(&projection, row.Projection); err != nil {
					t.Fatal(err)
				}
				if e != nil || !bytes.Equal(bytes.TrimSpace(b), projection.Bytes()) {
					t.Fatal("normalized projection differs")
				}
			}
		})
	}
	t.Logf("corpus_cases=%d", len(corpus.Cases))
}
