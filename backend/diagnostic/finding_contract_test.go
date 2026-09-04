package diagnostic

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestProducerFindingContractIsSortedCompleteAndDeterministic(t *testing.T) {
	first, err := MarshalFindingContract()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || first[len(first)-1] != '\n' || !json.Valid(bytes.TrimSuffix(first, []byte{'\n'})) {
		t.Fatalf("contract is not canonical newline-terminated JSON: %q", first)
	}
	for run := 1; run < 100; run++ {
		next, err := MarshalFindingContract()
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if !bytes.Equal(first, next) {
			t.Fatalf("run %d generated different bytes", run)
		}
	}

	contract := ProducerFindingContract()
	if contract.Schema != FindingContractSchemaV1 {
		t.Fatalf("schema = %q, want %q", contract.Schema, FindingContractSchemaV1)
	}
	codes := make([]string, len(contract.Findings))
	for index, finding := range contract.Findings {
		codes[index] = string(finding.Code)
		if finding.PresentationKey == "" {
			t.Fatalf("finding %q has no semantic presentation key", finding.Code)
		}
		if !validFindingCode(finding.Code) {
			t.Fatalf("fixture finding %q is not accepted by producer registry", finding.Code)
		}
	}
	if !sort.StringsAreSorted(codes) {
		t.Fatalf("finding codes are not sorted: %v", codes)
	}
	if got, want := codes, validFindingCodesForTest(); !reflect.DeepEqual(got, want) {
		t.Fatalf("contract finding set = %v, producer set = %v", got, want)
	}
	if got, want := contract.VerificationScopes, []string{VerificationScopeServerGreeting}; !reflect.DeepEqual(got, want) {
		t.Fatalf("verification scopes = %v, want %v", got, want)
	}
}

func TestProducerFindingContractFreezesClosedResultShapes(t *testing.T) {
	contract := ProducerFindingContract()
	if got, want := len(contract.ResultShapes), 31; got != want {
		t.Fatalf("result shapes=%d, want %d", got, want)
	}
	if got, want := contract.ResultMatrixRows, 136; got != want {
		t.Fatalf("expanded result matrix rows=%d, want %d", got, want)
	}
	byName := make(map[string]ResultShapeContract, len(contract.ResultShapes))
	names := make([]string, len(contract.ResultShapes))
	for index, shape := range contract.ResultShapes {
		names[index] = shape.Name
		if _, duplicate := byName[shape.Name]; duplicate {
			t.Fatalf("duplicate result shape %q", shape.Name)
		}
		kindNames := make([]string, len(shape.Kinds))
		for kindIndex, kind := range shape.Kinds {
			kindNames[kindIndex] = string(kind)
		}
		if !sort.StringsAreSorted(kindNames) {
			t.Fatalf("result shape %q kinds are not sorted: %v", shape.Name, kindNames)
		}
		for label, details := range map[string][]ResultDetailContract{"required": shape.RequiredDetails, "optional": shape.OptionalDetails} {
			if details == nil {
				t.Fatalf("result shape %q has null %s_details", shape.Name, label)
			}
			keys := make([]string, len(details))
			for detailIndex, detail := range details {
				keys[detailIndex] = detail.Key
			}
			if !sort.StringsAreSorted(keys) {
				t.Fatalf("result shape %q %s detail keys are not sorted: %v", shape.Name, label, keys)
			}
		}
		byName[shape.Name] = shape
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("result shapes are not sorted: %v", names)
	}
	for _, name := range []string{
		"service_greeting_unverified", "service_greeting_verified_plain", "service_greeting_verified_tls_certificate",
		"tls_certificate_expired", "tls_certificate_not_yet_valid", "tls_handshake_failed",
		"tls_hostname_mismatch", "tls_untrusted", "tls_downgrade", "traceroute_unavailable",
	} {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing result shape %q", name)
		}
	}
	for _, invented := range []string{
		"http_healthy_tls_certificate", "http_unexpected_status_tls_certificate",
		"traceroute_healthy_enriched", "traceroute_healthy_enrichment_failures",
		"traceroute_degraded_enriched", "traceroute_degraded_enrichment_failures",
		"traceroute_destination_unreached_enriched", "traceroute_destination_unreached_enrichment_failures",
		"traceroute_execution_cancelled_enriched", "traceroute_execution_failed_enriched",
		"traceroute_execution_timeout_enriched", "traceroute_execution_incomplete_enriched",
		"traceroute_execution_incomplete_topology", "traceroute_execution_incomplete_topology_enriched",
		"traceroute_execution_incomplete_topology_enrichment_failures",
	} {
		if _, ok := byName[invented]; ok {
			t.Errorf("fixture encodes optional producer detail combinations as invented shape %q", invented)
		}
	}
	if _, ok := byName["traceroute_execution_cancelled"]; ok {
		t.Fatal("contract accepts unreachable detailed cancellation that the production supervisor replaces with generic no-details cancellation")
	}
	expired := byName["tls_certificate_expired"]
	if expired.Status != StatusUnreachable || expired.ErrorCode != ResultErrorTLSCertificateExpired ||
		!reflect.DeepEqual(expired.RequiredDetails, []ResultDetailContract{{Key: "certificate_not_after", Type: DetailTypeCanonicalUTCRFC3339}, {Key: "certificate_not_before", Type: DetailTypeCanonicalUTCRFC3339}}) ||
		len(expired.OptionalDetails) != 0 {
		t.Fatalf("expired TLS shape drifted: %+v", expired)
	}
	verified := byName["service_greeting_verified_plain"]
	if verified.Status != StatusHealthy || verified.ErrorCode != "" ||
		!reflect.DeepEqual(verified.RequiredDetails, []ResultDetailContract{{Key: "verification_scope", Type: DetailTypeString, Const: VerificationScopeServerGreeting}}) ||
		len(verified.OptionalDetails) != 0 {
		t.Fatalf("plain service shape drifted: %+v", verified)
	}
	unavailable := byName["traceroute_unavailable"]
	if unavailable.Status != StatusUnreachable || unavailable.ErrorCode != "traceroute_unavailable" ||
		!reflect.DeepEqual(unavailable.Kinds, []Kind{KindTraceroute}) || len(unavailable.RequiredDetails) != 0 || len(unavailable.OptionalDetails) != 0 {
		t.Fatalf("traceroute unavailable shape drifted: %+v", unavailable)
	}
	trace := byName["traceroute_healthy"]
	if got, want := detailKeys(trace.RequiredDetails), []string{"attempts_cancelled", "attempts_execution_failed", "attempts_failed", "attempts_reached", "attempts_timed_out", "attempts_total", "attempts_unreached"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("healthy traceroute required semantic counters=%v, want %v", got, want)
	}
	if got, want := detailKeys(trace.OptionalDetails), []string{"attempts", "geoip_enrichment", "geoip_provider_failures", "topology"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("healthy traceroute optional producer/compact alternatives=%v, want %v", got, want)
	}
	for _, name := range []string{"traceroute_execution_failed", "traceroute_execution_timeout"} {
		if got, want := detailKeys(byName[name].OptionalDetails), []string{"attempts", "geoip_enrichment", "geoip_provider_failures", "topology"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s optional producer/compact alternatives=%v, want %v", name, got, want)
		}
	}
}

func detailKeys(details []ResultDetailContract) []string {
	keys := make([]string, len(details))
	for index, detail := range details {
		keys[index] = detail.Key
	}
	return keys
}

// Keep this independent expected set: drift in either the registry or fixture
// generator must fail instead of allowing two views of one accidentally edited list.
func validFindingCodesForTest() []string {
	codes := []string{
		"checker_capacity_unavailable", "checker_panic", "dns_resolution_failed", "endpoint_connect_failed",
		"execution_cancelled", "execution_timeout", "http_unexpected_status", "invalid_target",
		"service_greeting_unverified", "target_policy_blocked", "tls_certificate_expired",
		"tls_certificate_expiring", "tls_certificate_not_yet_valid", "tls_downgrade", "tls_handshake_failed",
		"tls_hostname_mismatch", "tls_untrusted", "traceroute_execution_failed", "traceroute_partial_reachability",
		"traceroute_path_degraded", "traceroute_path_unstable", "traceroute_unavailable", "traceroute_unreachable",
	}
	sort.Strings(codes)
	return codes
}
