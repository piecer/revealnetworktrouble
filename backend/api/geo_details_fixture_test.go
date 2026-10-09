package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

const geoDetailsRichProviderJSON = `{"success":true,"city":"서울 <>&\"\\\n\u2028\u2029 😀","region":"Seoul","country":"South Korea","country_code":"KR","continent":"Asia","continent_code":"AS","region_code":"11","postal":"04527","timezone":{"id":"Asia/Seoul"},"connection":{"asn":15169,"org":"Network owner","isp":"Different access ISP","domain":"example.net"},"latitude":0,"longitude":0}`

type geoDetailsProviderReply struct {
	status int
	body   string
}

// Real loopback provider HTTP, strict decoder, lookup/cache, trace parser,
// enrichment, supervised Runner, analysis, projection and closed serializer.
// Trace command output is injected; this is not an external network witness.
func geoDetailsProducerFixture(t *testing.T, scenario string) diagnostic.Report {
	t.Helper()
	replies := map[string]geoDetailsProviderReply{}
	outputs := map[string]string{}
	targets := []diagnostic.Target{}
	warm := []string{}
	switch scenario {
	case "rich":
		replies["8.8.8.8"] = geoDetailsProviderReply{200, geoDetailsRichProviderJSON}
		replies["1.1.1.1"] = geoDetailsProviderReply{200, `{"success":true,"city":"Text without coordinates","continent":"Oceania","timezone":{"id":"Australia/Sydney"}}`}
		replies["9.9.9.9"] = geoDetailsProviderReply{200, `{"success":true,"latitude":37.5,"longitude":127,"city":"Legacy preserved","continent":17,"connection":{"asn":19281,"org":"Legacy owner"}}`}
		replies["208.67.222.222"] = geoDetailsProviderReply{500, `{"success":true,"city":"must never be stamped"}`}
		replies["2606:4700:4700::1111"] = geoDetailsProviderReply{200, `{"success":true,"city":"IPv6 text","country":"Australia","connection":{"asn":13335,"isp":"IPv6 ISP","domain":"ipv6.example"}}`}
		warm = []string{"8.8.8.8"}
		destination := "2606:4700:4700::1111"
		outputs[destination] = fixtureTraceOutput([]string{"8.8.8.8", "1.1.1.1", "9.9.9.9", "208.67.222.222", destination})
		targets = []diagnostic.Target{{Kind: diagnostic.KindTraceroute, Address: destination, Attempts: 2}}
	case "empty":
		// Legacy enrichment still queries a synthetic public destination, but the
		// sidecar must not treat it as a responsive observed hop.
		replies["8.8.4.4"] = geoDetailsProviderReply{200, geoDetailsRichProviderJSON}
		outputs["8.8.4.4"] = "traceroute to 8.8.4.4 (8.8.4.4), 30 hops max\n1 192.168.1.1 1 ms\n2 *\n"
		targets = []diagnostic.Target{{Kind: diagnostic.KindTraceroute, Address: "8.8.4.4", Attempts: 1}}
	case "truncated":
		for start := 0; start < 501; start += 30 {
			addresses := []string{}
			for i := start; i < min(start+30, 501); i++ {
				address := fmt.Sprintf("11.%d.%d.%d", i>>16, (i>>8)&255, i&255)
				replies[address] = geoDetailsProviderReply{200, `{"success":true,"city":"Cache city","connection":{"asn":1,"org":"Fixture owner"}}`}
				addresses = append(addresses, address)
				warm = append(warm, address)
			}
			destination := addresses[len(addresses)-1]
			outputs[destination] = fixtureTraceOutput(addresses)
			targets = append(targets, diagnostic.Target{Kind: diagnostic.KindTraceroute, Address: destination, Attempts: 1})
		}
	default:
		t.Fatalf("unknown fixture scenario %q", scenario)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		address := strings.TrimPrefix(r.URL.Path, "/")
		mu.Lock()
		calls[address]++
		mu.Unlock()
		reply, ok := replies[address]
		if !ok || r.Method != "GET" || r.URL.RawQuery != "" {
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(reply.status)
		_, _ = w.Write([]byte(reply.body))
	}))
	defer provider.Close()
	fetched := time.Date(2026, 10, 9, 1, 2, 3, 456789000, time.UTC)
	lookup := diagnostic.NewIPWhoIsLookupWithConfig(provider.Client(), provider.URL, nil, diagnostic.GeoIPCacheConfig{Now: func() time.Time { return fetched }, TTL: 24 * time.Hour})
	for _, address := range warm {
		if _, err := lookup.Lookup(context.Background(), net.ParseIP(address)); err != nil {
			t.Fatal(err)
		}
	}
	commands := map[string]int{}
	checker := diagnostic.NewInjectedTracerouteChecker(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		destination := args[len(args)-1]
		mu.Lock()
		commands[destination]++
		call := commands[destination]
		mu.Unlock()
		if scenario == "rich" && call == 2 {
			return []byte("traceroute to 2606:4700:4700::1111 (2606:4700:4700::1111), 30 hops max\n1 4.2.2.1 1 ms\n"), context.DeadlineExceeded
		}
		output, ok := outputs[destination]
		if !ok {
			t.Errorf("unexpected trace destination %s", destination)
		}
		return []byte(output), nil
	}, lookup, nil)
	supervisor, err := diagnostic.NewCheckerSupervisor(diagnostic.MaxTargets)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Shutdown(context.Background())
	runner := diagnostic.NewRunnerWithClockAndSupervisor(func() time.Time { return fixtureTime }, supervisor, checker)
	report, err := runner.RunWithID(context.Background(), "cdcdcdcdcdcdcdcdcdcdcdcd", diagnostic.Request{Targets: targets, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// Freeze nondeterministic checker duration and derived wall-clock cache age;
	// no provider/observation/detail values are injected or replaced here.
	for i := range report.Results {
		if coverage, ok := report.Results[i].Details["geoip_enrichment"].(diagnostic.EnrichmentCoverage); ok {
			coverage.MaxAgeMS = 0
			report.Results[i].Details["geoip_enrichment"] = coverage
		}
	}
	normalizeFixtureReport(&report, 10)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != len(replies) {
		t.Fatalf("provider call set: got %d want %d", len(calls), len(replies))
	}
	for address, count := range calls {
		if count != 1 {
			t.Fatalf("duplicate query for %s: %d", address, count)
		}
	}
	side := diagnostic.BuildGeoDetails(report)
	switch scenario {
	case "rich":
		if side.Total != 3 || report.Results[0].Status != diagnostic.StatusDegraded {
			t.Fatalf("rich observations not retained: %+v", side)
		}
		coverage := report.Results[0].Details["geoip_enrichment"].(diagnostic.EnrichmentCoverage)
		if coverage.CacheHits != 1 || coverage.UpstreamFetches != 2 || len(coverage.Failures) != 2 {
			t.Fatalf("legacy accounting changed: %+v", coverage)
		}
	case "empty":
		if side.Total != 0 {
			t.Fatal("synthetic destination acquired sidecar")
		}
	case "truncated":
		if side.Total != 501 {
			t.Fatalf("truncation raw total=%d", side.Total)
		}
	}
	return report
}

func TestGeoDetailsStandaloneExactCanonicalBoundary(t *testing.T) {
	data, err := os.ReadFile("../../testdata/geo-details-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus geoDetailsCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	var side diagnostic.GeoDetailsSidecar
	if err := json.Unmarshal([]byte(corpus.Bases["bytes131072"]), &side); err != nil {
		t.Fatal(err)
	}
	encoded, err := marshalGeoDetailsSidecar(side)
	if err != nil || len(encoded) != diagnostic.GeoDetailsMaxBytes {
		t.Fatalf("inclusive standalone boundary: bytes=%d err=%v", len(encoded), err)
	}
	report := geoDetailsReportForTest(1, "city")
	legacy, err := marshalFullResponse(report)
	if err != nil {
		t.Fatal(err)
	}
	body, err := fitGeoDetails(report, legacy, &side, marshalFullResponse)
	if err != nil {
		t.Fatal(err)
	}
	if got := assertGeoDetailsLegacyBytes(t, legacy, body); got == nil || len(got.Entries) != 500 {
		t.Fatal("exact boundary prefix not retained")
	}
	changed := false
	for i := range side.Entries {
		if len(side.Entries[i].City) == 1 {
			side.Entries[i].City += "a"
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("missing +1 mutation field")
	}
	if encoded, err := json.Marshal(side); err != nil || len(encoded) != diagnostic.GeoDetailsMaxBytes+1 {
		t.Fatal("bad +1 boundary setup")
	}
	if _, err := marshalGeoDetailsSidecar(side); !errors.Is(err, errFullResponseTooLarge) {
		t.Fatalf("+1 sidecar admitted: %v", err)
	}
	body, err = fitGeoDetails(report, legacy, &side, marshalFullResponse)
	if err != nil {
		t.Fatal(err)
	}
	if got := assertGeoDetailsLegacyBytes(t, legacy, body); got == nil || len(got.Entries) != 499 || got.Omitted != 1 {
		t.Fatal("+1 must drop a complete final entry")
	}
}

func TestGeoDetailsCommittedFixturesMatchProducer(t *testing.T) {
	generated := map[string][]byte{}
	for _, scenario := range []string{"rich", "empty", "truncated"} {
		report := geoDetailsProducerFixture(t, scenario)
		for _, mode := range []diagnostic.TopologyMode{diagnostic.TopologyModeFull, diagnostic.TopologyModeCompact} {
			if scenario == "truncated" && mode == diagnostic.TopologyModeFull {
				continue
			}
			body, err := marshalReportResponse(report, mode, true)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := marshalReportResponse(report, mode, false)
			if err != nil {
				t.Fatal(err)
			}
			side := assertGeoDetailsLegacyBytes(t, legacy, body)
			if side == nil || side.Entries == nil {
				t.Fatal("canonical sidecar absent/null")
			}
			if scenario == "truncated" && (side.Total != 501 || len(side.Entries) != 500 || side.Omitted != 1) {
				t.Fatalf("wrong truncation: total %d entries %d omitted %d", side.Total, len(side.Entries), side.Omitted)
			}
			name := fmt.Sprintf("geo-details-%s-%s-report.json", scenario, mode)
			generated[name] = body
		}
	}
	corpus := geoDetailsConsumerCorpus(t, generated["geo-details-rich-full-report.json"])
	generated["geo-details-corpus.json"] = corpus
	names := make([]string, 0, len(generated))
	for name := range generated {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := generated[name]
		if len(body) == 0 || body[len(body)-1] != '\n' || !json.Valid(body) {
			t.Fatalf("noncanonical fixture %s", name)
		}
		if os.Getenv("UPDATE_GEO_DETAILS_FIXTURES") == "1" {
			if os.Getenv("CI") != "" {
				t.Fatal("fixture updates are forbidden in CI")
			}
			if err := os.WriteFile("../../testdata/"+name, body, 0644); err != nil {
				t.Fatal(err)
			}
		}
		committed, err := os.ReadFile("../../testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := requireFreshFixtureBytes(committed, body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		mutated := append([]byte{}, body...)
		mutated[len(mutated)/2] ^= 1
		if requireFreshFixtureBytes(mutated, body) == nil {
			t.Fatal("one-byte stale fixture accepted")
		}
		t.Logf("%s: %d exact producer bytes", name, len(body))
	}
}

// Corpus documents share a real producer report template, and store reusable
// sidecar bases plus exact byte edits. This retains lexical number/duplicate-key
// cases without committing dozens of copies of a large report.
type geoDetailsCorpusEdit struct {
	Old string `json:"old"`
	New string `json:"new"`
}
type geoDetailsCorpusCase struct {
	Name          string                 `json:"name"`
	Base          string                 `json:"base"`
	Accept        bool                   `json:"accept"`
	Edits         []geoDetailsCorpusEdit `json:"edits"`
	DuplicateRoot bool                   `json:"duplicate_root,omitempty"`
}
type geoDetailsCorpus struct {
	SchemaVersion int                    `json:"schema_version"`
	ReportPrefix  string                 `json:"report_prefix"`
	ReportSuffix  string                 `json:"report_suffix"`
	Bases         map[string]string      `json:"bases"`
	Cases         []geoDetailsCorpusCase `json:"cases"`
}

func geoDetailsConsumerCorpus(t *testing.T, report []byte) []byte {
	t.Helper()
	position := bytes.LastIndex(report, []byte(`"geo_details":`))
	if position < 0 {
		t.Fatal("missing real sidecar")
	}
	corpus := geoDetailsCorpus{SchemaVersion: 1, ReportPrefix: string(report[:position+len(`"geo_details":`)]), ReportSuffix: "}\n", Bases: map[string]string{}, Cases: []geoDetailsCorpusCase{}}
	addBase := func(name string, side diagnostic.GeoDetailsSidecar) {
		b, err := marshalGeoDetailsSidecar(side)
		if err != nil {
			t.Fatal(err)
		}
		canonical, _ := json.Marshal(side)
		if !bytes.Equal(b, canonical) {
			t.Fatal("closed sidecar encoding differs from Go")
		}
		corpus.Bases[name] = string(b)
	}
	minimal := diagnostic.GeoDetailsSidecar{SchemaVersion: 1, Total: 1, Entries: []diagnostic.GeoDetailsEntry{{Address: "8.8.8.8", Provider: "ipwho.is", Source: diagnostic.GeoIPSourceUpstream, City: "city"}}}
	addBase("minimal", minimal)
	addBase("empty", diagnostic.GeoDetailsSidecar{SchemaVersion: 1, Entries: []diagnostic.GeoDetailsEntry{}})
	var parsed struct {
		GeoDetails diagnostic.GeoDetailsSidecar `json:"geo_details"`
	}
	if err := json.Unmarshal(report, &parsed); err != nil {
		t.Fatal(err)
	}
	addBase("rich", parsed.GeoDetails)
	boundary := minimal
	boundary.Entries = append([]diagnostic.GeoDetailsEntry{}, minimal.Entries...)
	boundary.Entries[0].City = strings.Repeat("😀", 64)
	addBase("text256", boundary)
	entry := &boundary.Entries[0]
	entry.Region = entry.City
	entry.Country = entry.City
	entry.Continent = entry.City
	entry.Postal = entry.City
	entry.ISP = entry.City
	addBase("bundle1536", boundary)
	many := *diagnostic.BuildGeoDetails(geoDetailsReportForTest(500, "a"))
	addBase("entries500", many)
	// Construct an exact 128 KiB canonical sidecar using only allowed field
	// lengths and 500 distinct canonical addresses, not byte padding outside JSON.
	canonical, _ := json.Marshal(many)
	remaining := diagnostic.GeoDetailsMaxBytes - len(canonical)
	for i := range many.Entries {
		add := min(255, remaining)
		many.Entries[i].City += strings.Repeat("a", add)
		remaining -= add
	}
	if remaining != 0 {
		t.Fatal("cannot construct exact sidecar limit")
	}
	addBase("bytes131072", many)
	if len(corpus.Bases["bytes131072"]) != 131072 {
		t.Fatal("wrong canonical boundary length")
	}
	add := func(name, base string, accept bool, edits ...geoDetailsCorpusEdit) {
		if edits == nil {
			edits = []geoDetailsCorpusEdit{}
		}
		corpus.Cases = append(corpus.Cases, geoDetailsCorpusCase{Name: name, Base: base, Accept: accept, Edits: edits})
	}
	edit := func(old, new string) geoDetailsCorpusEdit { return geoDetailsCorpusEdit{old, new} }
	for _, base := range []string{"minimal", "empty", "rich", "text256", "bundle1536", "entries500", "bytes131072"} {
		add(base, base, true)
	}
	for _, field := range []string{"schema_version", "total", "omitted"} {
		original := "1"
		if field == "omitted" {
			original = "0"
		}
		for _, equivalent := range []string{original + ".0", original + "e0", original + "e+0"} {
			add(field+"-numeric-"+equivalent, "minimal", true, edit(`"`+field+`":`+original, `"`+field+`":`+equivalent))
		}
		for _, bad := range []string{"null", "true", `"1"`, "-1", "1.5", "1e100"} {
			add(field+"-invalid-"+bad, "minimal", false, edit(`"`+field+`":`+original, `"`+field+`":`+bad))
		}
		add(field+"-missing", "minimal", false, edit(`"`+field+`":`+original+`,`, ""))
	}
	add("unknown-version", "minimal", false, edit(`"schema_version":1`, `"schema_version":2`))
	add("total-max", "minimal", true, edit(`"total":1,"omitted":0`, `"total":6200,"omitted":6199`))
	add("total-over", "minimal", false, edit(`"total":1,"omitted":0`, `"total":6201,"omitted":6200`))
	add("count-mismatch", "minimal", false, edit(`"omitted":0`, `"omitted":1`))
	add("root-null", "minimal", false, edit(corpus.Bases["minimal"], "null"))
	add("root-array", "minimal", false, edit(corpus.Bases["minimal"], "[]"))
	add("unknown-key", "minimal", false, edit(`"schema_version":1`, `"unknown":1,"schema_version":1`))
	add("duplicate-decoded-key", "minimal", false, edit(`"schema_version":1`, `"schema_version":1,"schema_\u0076ersion":1`))
	add("duplicate-root", "minimal", false)
	corpus.Cases[len(corpus.Cases)-1].DuplicateRoot = true
	add("entries-null", "empty", false, edit(`"entries":[]`, `"entries":null`))
	add("entries-missing", "empty", false, edit(`,"entries":[]`, ""))
	add("entry-null", "minimal", false, edit(`[{"address":"8.8.8.8","provider":"ipwho.is","source":"upstream","city":"city"}]`, `[null]`))
	for _, field := range []string{"address", "provider", "source"} {
		value := map[string]string{"address": "8.8.8.8", "provider": "ipwho.is", "source": "upstream"}[field]
		add(field+"-missing", "minimal", false, edit(`"`+field+`":"`+value+`",`, ""))
		add(field+"-null", "minimal", false, edit(`"`+field+`":"`+value+`"`, `"`+field+`":null`))
	}
	for _, address := range []string{"192.168.1.1", "127.0.0.1", "203.0.113.1", "2001:db8::1", "::ffff:8.8.8.8", "2606:4700:4700:0:0:0:0:1111", "2606:4700:4700::ABCD", "fe80::1%eth0", "example.net", "08.8.8.8", "8.8.8.8 "} {
		add("noncanonical-or-private-"+address, "minimal", false, edit(`"address":"8.8.8.8"`, `"address":"`+address+`"`))
	}
	add("canonical-ipv6", "minimal", true, edit(`"address":"8.8.8.8"`, `"address":"2606:4700:4700::1111"`))
	add("source-cache", "minimal", true, edit(`"source":"upstream"`, `"source":"cache"`))
	add("source-other", "minimal", false, edit(`"source":"upstream"`, `"source":"other"`))
	add("provider-other", "minimal", false, edit(`"provider":"ipwho.is"`, `"provider":"other"`))
	add("entry-unknown", "minimal", false, edit(`"city":"city"`, `"city":"city","latitude":0`))
	add("city-null", "minimal", false, edit(`"city":"city"`, `"city":null`))
	add("city-empty", "minimal", false, edit(`"city":"city"`, `"city":""`))
	add("all-text-absent", "minimal", true, edit(`,"city":"city"`, ""))
	add("city-unpaired-surrogate", "minimal", false, edit(`"city":"city"`, `"city":"\ud800"`))
	add("duplicate-entry-key", "minimal", false, edit(`"city":"city"`, `"city":"city","city":"city"`))
	add("text257", "text256", false, edit(`"city":"`+strings.Repeat("😀", 64)+`"`, `"city":"`+strings.Repeat("😀", 64)+`x"`))
	add("bundle1537", "bundle1536", false, edit(`"isp":`, `"timezone":"x","isp":`))
	add("bytes131073", "bytes131072", false, edit(`"city":"a"`, `"city":"aa"`))
	// Append a distinct sorted entry for count +1, then preserve arithmetic.
	minimalEntry, _ := json.Marshal(minimal.Entries[0])
	extra := strings.Replace(string(minimalEntry), "8.8.8.8", "99.0.0.1", 1)
	add("entries501", "entries500", false, edit(`"total":500`, `"total":501`), edit(`]}`, `,`+extra+`]}`))
	add("duplicate-address", "minimal", false, edit(`"total":1`, `"total":2`), edit(`]}`, `,`+string(minimalEntry)+`]}`))
	add("unsorted-address", "minimal", false, edit(`"total":1`, `"total":2`), edit(`]}`, `,`+strings.Replace(string(minimalEntry), "8.8.8.8", "1.1.1.1", 1)+`]}`))
	times := `,"fetched_at":"2026-10-09T01:02:03.456Z","expires_at":"2026-10-10T01:02:03.456Z"`
	add("timestamps-valid", "minimal", true, edit(`"city":"city"`, `"city":"city"`+times))
	add("timestamps-equal", "minimal", true, edit(`"city":"city"`, `"city":"city"`+strings.Replace(times, "2026-10-10", "2026-10-09", 1)))
	add("timestamp-half-pair", "minimal", false, edit(`"city":"city"`, `"city":"city","fetched_at":"2026-10-09T01:02:03.456Z"`))
	for _, bad := range []string{"2026-02-30T01:02:03.456Z", "2026-10-09T01:02:03Z", "2026-10-09T01:02:03.456+00:00", "2026-10-09T01:02:03.4560Z", "2026-10-09T01:02:60.456Z", "2026-10-11T01:02:03.456Z"} {
		add("timestamp-invalid-"+bad, "minimal", false, edit(`"city":"city"`, `"city":"city"`+strings.Replace(times, "2026-10-09T01:02:03.456Z", bad, 1)))
	}
	add("bytes-boundary-numeric-equivalent", "bytes131072", true, edit(`"schema_version":1`, `"schema_version":1.0`))
	add("bytes-boundary-escape-equivalent", "bytes131072", true, edit(`"city":"a"`, `"city":"\u0061"`))
	add("numeric-scaled-integer", "minimal", true, edit(`"total":1`, `"total":10e-1`))
	add("negative-zero", "minimal", true, edit(`"omitted":0`, `"omitted":-0.0e0`))
	add("whitespace-text", "minimal", true, edit(`"city":"city"`, `"city":" "`))
	for _, field := range []string{"city", "region", "country", "country_code", "continent", "continent_code", "region_code", "postal", "timezone", "isp", "network_domain"} {
		for _, value := range []string{"null", "7", "true", "[]", "{}", `""`} {
			replacement := `"city":"city","` + field + `":` + value
			if field == "city" {
				replacement = `"city":` + value
			}
			add("optional-"+field+"-invalid-"+value, "minimal", false, edit(`"city":"city"`, replacement))
		}
	}
	add("timestamp-null-pair", "minimal", false, edit(`"city":"city"`, `"city":"city","fetched_at":null,"expires_at":null`))
	add("timestamp-expiry-only", "minimal", false, edit(`"city":"city"`, `"city":"city","expires_at":"2026-10-10T01:02:03.456Z"`))
	add("timestamp-empty-pair", "minimal", false, edit(`"city":"city"`, `"city":"city","fetched_at":"","expires_at":""`))
	names := map[string]bool{}
	for _, c := range corpus.Cases {
		if names[c.Name] {
			t.Fatalf("duplicate corpus name %s", c.Name)
		}
		names[c.Name] = true
		wire, ok := corpus.Bases[c.Base]
		if !ok {
			t.Fatal("missing base")
		}
		for _, e := range c.Edits {
			if !strings.Contains(wire, e.Old) {
				t.Fatalf("corpus edit misses base: %s", c.Name)
			}
			wire = strings.Replace(wire, e.Old, e.New, 1)
		}
		if c.Name == "bytes131073" && len(wire) != 131073 {
			t.Fatal("wrong +1 canonical boundary")
		}
		// The consumer reconstructs exactly these bytes; never parse/re-encode the
		// sidecar before lexical validation (duplicates and 1e0 must survive).
		full := corpus.ReportPrefix + wire + corpus.ReportSuffix
		if c.DuplicateRoot {
			full = corpus.ReportPrefix + wire + `,"geo_details":` + wire + corpus.ReportSuffix
		}
		if !json.Valid([]byte(full)) {
			t.Fatalf("invalid JSON corpus structure %s", c.Name)
		}
	}
	t.Logf("consumer corpus: %d reusable bases, %d exact-byte cases", len(corpus.Bases), len(corpus.Cases))
	body, err := json.Marshal(corpus)
	if err != nil {
		t.Fatal(err)
	}
	return append(body, '\n')
}
