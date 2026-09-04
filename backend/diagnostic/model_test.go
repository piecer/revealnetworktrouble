package diagnostic

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestTopologyModeJSONPresenceAndType(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want TopologyMode
	}{
		{name: "omitted", body: `{"targets":[]}`, want: ""},
		{name: "full", body: `{"targets":[],"topology_mode":"full"}`, want: TopologyModeFull},
		{name: "compact", body: `{"targets":[],"topology_mode":"compact"}`, want: TopologyModeCompact},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var request Request
			if err := json.Unmarshal([]byte(tt.body), &request); err != nil {
				t.Fatal(err)
			}
			if request.TopologyMode != tt.want {
				t.Fatalf("TopologyMode = %q, want %q", request.TopologyMode, tt.want)
			}
		})
	}
	for _, body := range []string{
		`{"targets":[],"topology_mode":null}`,
		`{"targets":[],"topology_mode":1}`,
		`{"targets":[],"topology_mode":{}}`,
		`{"targets":[],"topology_mode":[]}`,
		`{"targets":[],"topology_mode":true}`,
	} {
		var request Request
		if err := json.Unmarshal([]byte(body), &request); err == nil {
			t.Fatalf("json.Unmarshal(%s) accepted invalid topology_mode type", body)
		}
	}
}

func TestReportCompactTopologyIsAdditiveAndOmittedWhenNil(t *testing.T) {
	report := Report{ID: "r", Results: []Result{}}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "compact_topology") {
		t.Fatalf("nil compact topology changed full report JSON: %s", encoded)
	}
	report.CompactTopology = &CompactTopology{Schema: CompactTopologySchemaV1, Selection: CompactTopologySelectionV1}
	encoded, err = json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"compact_topology":{"schema":"compact-v1","selection":"fair-complete-prefix-v1"`) {
		t.Fatalf("compact topology field missing or malformed: %s", encoded)
	}
}

func TestRequestTopologyModeOmittedFromJSON(t *testing.T) {
	encoded, err := json.Marshal(Request{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "topology_mode") {
		t.Fatalf("empty topology mode was serialized: %s", encoded)
	}
}

func TestTLSResultAndFindingCodeEnumsAreClosed(t *testing.T) {
	resultCodes := []string{
		ResultErrorTLSCertificateExpired,
		ResultErrorTLSCertificateNotYetValid,
		ResultErrorTLSHostnameMismatch,
		ResultErrorTLSUntrusted,
		ResultErrorTLSHandshakeFailed,
	}
	wantResultCodes := []string{
		"tls_certificate_expired",
		"tls_certificate_not_yet_valid",
		"tls_hostname_mismatch",
		"tls_untrusted",
		"tls_handshake_failed",
	}
	for index := range wantResultCodes {
		if resultCodes[index] != wantResultCodes[index] {
			t.Fatalf("result code[%d] = %q, want %q", index, resultCodes[index], wantResultCodes[index])
		}
	}

	for _, code := range []FindingCode{
		FindingTLSCertificateExpired,
		FindingTLSCertificateNotYetValid,
		FindingTLSHostnameMismatch,
		FindingTLSUntrusted,
		FindingTLSHandshakeFailed,
	} {
		if !validFindingCode(code) {
			t.Fatalf("TLS finding code %q is not registered", code)
		}
	}
	if validFindingCode(FindingCode("tls_HOSTILE_CANARY")) {
		t.Fatal("unknown TLS finding code was accepted")
	}
}

func TestServiceGreetingResultAndFindingCodeEnumsAreClosed(t *testing.T) {
	if ResultErrorServiceGreetingUnverified != "service_greeting_unverified" {
		t.Fatalf("result code = %q", ResultErrorServiceGreetingUnverified)
	}
	if FindingServiceGreetingUnverified != FindingCode("service_greeting_unverified") {
		t.Fatalf("finding code = %q", FindingServiceGreetingUnverified)
	}
	if !validFindingCode(FindingServiceGreetingUnverified) {
		t.Fatal("service greeting finding code is not registered")
	}
	if validFindingCode(FindingCode("service_greeting_unverified_HOSTILE_CANARY")) {
		t.Fatal("unknown service greeting finding code was accepted")
	}
}
