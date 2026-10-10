package diagnostic

import (
	"encoding/json"
	"strings"
	"time"
)

const contextTimeLayout = "2006-01-02T15:04:05.000Z"

func contextObject(v any, required, optional string) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	allowed := map[string]bool{}
	for _, k := range strings.Fields(required) {
		allowed[k] = true
		if x, exists := m[k]; !exists || x == nil {
			return nil, false
		}
	}
	for _, k := range strings.Fields(optional) {
		allowed[k] = true
	}
	for k, v := range m {
		if !allowed[k] || v == nil {
			return nil, false
		}
	}
	return m, true
}
func contextStamp(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok || len(s) != 24 {
		return time.Time{}, false
	}
	t, err := time.Parse(contextTimeLayout, s)
	return t, err == nil && t.Year() >= 1 && t.Year() <= 9999 && t.Format(contextTimeLayout) == s
}
func contextString(v any) string { s, _ := v.(string); return s }
func contextTimed(m map[string]any, key, source string, root time.Time) bool {
	stamp, ok := contextStamp(m[key])
	return ok && !stamp.After(root) && m["source"] == source
}
func contextNormalizeInteger(m map[string]any, key string, max uint64) (uint64, bool) {
	n, ok := contextInteger(m[key], max)
	if ok {
		m[key] = n
	}
	return n, ok
}

// ParseIPContext is the producer's closed wire validator, including request
// identity. Consumers use the same exact-byte corpus, not a second schema here.
func ParseIPContext(data []byte, requested string) (IPContext, error) {
	bad := func() (IPContext, error) { return IPContext{}, ErrIPContextInvalid }
	if len(data) > IPContextMaxResponseBytes || !validContextAddress(requested) {
		return bad()
	}
	raw, err := ipContextJSON(data, 16, 16384)
	if err != nil {
		return bad()
	}
	root, ok := contextObject(raw, "schema_version address source fetched_at expires_at reverse_dns registration routing", "")
	if !ok || root["address"] != requested {
		return bad()
	}
	version, ok := contextNormalizeInteger(root, "schema_version", 1)
	if !ok || version != 1 || !contextMember(contextString(root["source"]), "upstream", "cache") {
		return bad()
	}
	fetched, ok := contextStamp(root["fetched_at"])
	if !ok {
		return bad()
	}
	expires, ok := contextStamp(root["expires_at"])
	if !ok {
		return bad()
	}
	reverse, ok := contextObject(root["reverse_dns"], "status source fetched_at names omitted", "")
	if !ok || !contextTimed(reverse, "fetched_at", "system_resolver", fetched) {
		return bad()
	}
	reverseStatus := contextString(reverse["status"])
	if !contextMember(reverseStatus, "ok", "limited", "not_found", "timeout", "unavailable", "invalid_response") {
		return bad()
	}
	names, ok := reverse["names"].([]any)
	if !ok || len(names) > 8 {
		return bad()
	}
	omitted, ok := contextNormalizeInteger(reverse, "omitted", 56)
	if !ok {
		return bad()
	}
	if !contextCountShape(reverseStatus, len(names), omitted, 8) {
		return bad()
	}
	previous := ""
	for _, rawName := range names {
		name, ok := contextObject(rawName, "name forward_status", "")
		if !ok {
			return bad()
		}
		s, ok := name["name"].(string)
		if !ok || !contextDNSName(s) || s <= previous || !contextMember(contextString(name["forward_status"]), "confirmed", "mismatch", "not_found", "timeout", "unavailable", "invalid_response", "limited") {
			return bad()
		}
		previous = s
	}
	reg, ok := contextObject(root["registration"], "status source fetched_at", "registry start_address end_address handle name type country organization registered_at updated_at")
	if !ok || !contextTimed(reg, "fetched_at", "rdap", fetched) {
		return bad()
	}
	regStatus := contextString(reg["status"])
	if !contextMember(regStatus, "ok", "not_found", "timeout", "unavailable", "rate_limited", "invalid_response") {
		return bad()
	}
	if registry, present := reg["registry"]; present && !contextMember(contextString(registry), "arin", "apnic", "ripe", "lacnic", "afrinic") {
		return bad()
	}
	if regStatus != "ok" {
		for _, key := range strings.Fields("start_address end_address handle name type country organization registered_at updated_at") {
			if _, exists := reg[key]; exists {
				return bad()
			}
		}
	} else {
		start, aok := canonicalContextAddress(contextString(reg["start_address"]))
		end, bok := canonicalContextAddress(contextString(reg["end_address"]))
		q, _ := canonicalContextAddress(requested)
		if !aok || !bok || start.BitLen() != q.BitLen() || end.BitLen() != q.BitLen() || start.Compare(q) > 0 || end.Compare(q) < 0 {
			return bad()
		}
		total := 0
		for _, key := range strings.Fields("handle name type country organization") {
			if v, present := reg[key]; present {
				s, ok := contextText(v)
				if !ok || s == "" {
					return bad()
				}
				total += len(s)
				if key == "country" && !contextCountry(s) {
					return bad()
				}
			}
		}
		if total > 1536 {
			return bad()
		}
		for _, key := range []string{"registered_at", "updated_at"} {
			if v, present := reg[key]; present {
				if _, ok := contextStamp(v); !ok {
					return bad()
				}
			}
		}
	}
	routing, ok := contextObject(root["routing"], "status source fetched_at origins omitted", "prefix")
	if !ok || !contextTimed(routing, "fetched_at", "ripe_ris", fetched) {
		return bad()
	}
	status := contextString(routing["status"])
	if !contextMember(status, "ok", "limited", "not_found", "timeout", "unavailable", "rate_limited", "invalid_response") {
		return bad()
	}
	origins, ok := routing["origins"].([]any)
	if !ok || len(origins) > 4 {
		return bad()
	}
	omitted, ok = contextNormalizeInteger(routing, "omitted", 60)
	if !ok || !contextCountShape(status, len(origins), omitted, 4) {
		return bad()
	}
	if status == "ok" || status == "limited" {
		if !contextPrefix(contextString(routing["prefix"]), requested) {
			return bad()
		}
	} else {
		if _, present := routing["prefix"]; present {
			return bad()
		}
	}
	var previousASN uint64
	for _, rawOrigin := range origins {
		origin, ok := contextObject(rawOrigin, "asn rpki", "")
		if !ok {
			return bad()
		}
		asn, ok := contextNormalizeInteger(origin, "asn", 4294967295)
		if !ok || asn == 0 || asn <= previousASN {
			return bad()
		}
		previousASN = asn
		rpki, ok := contextObject(origin["rpki"], "status source checked_at", "validity")
		if !ok || !contextTimed(rpki, "checked_at", "ripe_rpki", fetched) {
			return bad()
		}
		status := contextString(rpki["status"])
		if !contextMember(status, "ok", "timeout", "unavailable", "rate_limited", "invalid_response") {
			return bad()
		}
		validity, present := rpki["validity"]
		if status == "ok" {
			if !present || !contextMember(contextString(validity), "valid", "invalid_asn", "invalid_length", "unknown") {
				return bad()
			}
		} else if present {
			return bad()
		}
	}
	normalized, err := json.Marshal(root)
	if err != nil {
		return bad()
	}
	var out IPContext
	if json.Unmarshal(normalized, &out) != nil {
		return bad()
	}
	if !expires.Equal(fetched.Add(contextSnapshotTTL(out))) {
		return bad()
	}
	canonical, err := json.Marshal(out)
	if err != nil || len(canonical)+1 > IPContextMaxResponseBytes {
		return bad()
	}
	return out, nil
}
func contextCountShape(status string, count int, omitted uint64, max int) bool {
	switch status {
	case "ok":
		return count >= 1 && count <= max && omitted == 0
	case "limited":
		return count == max && omitted > 0
	default:
		return count == 0 && omitted == 0
	}
}
