package diagnostic

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Exact decimal arithmetic without converting untrusted numbers through float64.
func contextInteger(value any, max uint64) (uint64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	s := string(n)
	negative := strings.HasPrefix(s, "-")
	if negative {
		s = s[1:]
	}
	exponent := int64(0)
	expOK := true
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.ParseInt(s[i+1:], 10, 64)
		exponent = e
		expOK = err == nil
		s = s[:i]
	}
	fraction := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		fraction = len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return 0, true
	}
	if negative || !expOK || exponent > int64(len(s)+fraction+10) || exponent < -int64(len(s)+fraction+10) {
		return 0, false
	}
	shift := exponent - int64(fraction)
	if shift < 0 {
		remove := -shift
		if remove > int64(len(s)) {
			return 0, false
		}
		if strings.Trim(s[len(s)-int(remove):], "0") != "" {
			return 0, false
		}
		s = s[:len(s)-int(remove)]
	} else {
		if int64(len(s))+shift > 10 {
			return 0, false
		}
		s += strings.Repeat("0", int(shift))
	}
	if len(s) > 10 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	return v, err == nil && v <= max
}
func contextProviderASN(v any) (uint32, bool) {
	if s, ok := v.(string); ok {
		if s == "" {
			return 0, false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		v = json.Number(s)
	}
	n, ok := contextInteger(v, 4294967295)
	return uint32(n), ok && n > 0
}
func contextPrefix(prefix, address string) bool {
	p, err := netip.ParsePrefix(prefix)
	a, ok := canonicalContextAddress(address)
	return err == nil && ok && p.Addr().Zone() == "" && !p.Addr().Is4In6() && p.String() == prefix && p == p.Masked() && p.Addr().BitLen() == a.BitLen() && p.Contains(a)
}
func (s *IPContextService) routing(ctx context.Context, address string) (out IPContextRouting) {
	out = IPContextRouting{Status: "invalid_response", Source: "ripe_ris", Origins: []IPContextOrigin{}}
	defer func() { out.FetchedAt = ipContextTime(s.now()) }()
	bgp, cancel := context.WithTimeout(ctx, 2*time.Second)
	m, status := s.providerObject(bgp, "https://stat.ripe.net/data/network-info/data.json?resource="+url.QueryEscape(address), false)
	cancel()
	if status != "ok" {
		out.Status = status
		return
	}
	if m["status"] != "ok" {
		return
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return
	}
	prefix, pok := data["prefix"].(string)
	asns, aok := data["asns"].([]any)
	if !pok || !aok || len(asns) > 64 {
		return
	}
	if prefix == "" && len(asns) == 0 {
		out.Status = "not_found"
		return
	}
	if !contextPrefix(prefix, address) || len(asns) == 0 {
		return
	}
	unique := map[uint32]bool{}
	for _, raw := range asns {
		asn, ok := contextProviderASN(raw)
		if !ok {
			return
		}
		unique[asn] = true
	}
	ordered := make([]uint32, 0, len(unique))
	for asn := range unique {
		ordered = append(ordered, asn)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	out.Status = "ok"
	if len(ordered) > 4 {
		out.Status = "limited"
		out.Omitted = len(ordered) - 4
		ordered = ordered[:4]
	}
	out.Prefix = prefix
	rpki, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, asn := range ordered {
		out.Origins = append(out.Origins, IPContextOrigin{ASN: asn, RPKI: s.rpki(rpki, prefix, asn)})
	}
	return
}
func (s *IPContextService) rpki(ctx context.Context, prefix string, asn uint32) (out IPContextRPKI) {
	out = IPContextRPKI{Status: "invalid_response", Source: "ripe_rpki"}
	defer func() { out.CheckedAt = ipContextTime(s.now()) }()
	m, status := s.providerObject(ctx, "https://stat.ripe.net/data/rpki-validation/data.json?resource="+strconv.FormatUint(uint64(asn), 10)+"&prefix="+url.QueryEscape(prefix), false)
	if status != "ok" {
		out.Status = status
		return
	}
	if m["status"] != "ok" {
		return
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return
	}
	echo, ok := contextProviderASN(data["resource"])
	if !ok || echo != asn || data["prefix"] != prefix {
		return
	}
	validity, ok := data["status"].(string)
	if !ok || !contextMember(validity, "valid", "invalid_asn", "invalid_length", "unknown") {
		return
	}
	out.Status = "ok"
	out.Validity = validity
	return
}
func contextMember(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
