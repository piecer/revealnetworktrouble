package diagnostic

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

const ipContextUpstreamBytes = 262144

func (s *IPContextService) providerObject(ctx context.Context, url string, rdap bool) (map[string]any, string) {
	if ctx.Err() != nil {
		return nil, "timeout"
	}
	type reply struct {
		status int
		body   []byte
	}
	result, err := contextOwnedCall(ctx, func() (reply, error) { status, body, err := s.http(ctx, url); return reply{status, body}, err })
	status, body := result.status, result.body
	if err != nil {
		return nil, contextFailure(err)
	}
	if status == 429 {
		return nil, "rate_limited"
	}
	if rdap && status == 404 {
		return nil, "not_found"
	}
	if (rdap && (status < 200 || status >= 300)) || (!rdap && status != 200) {
		return nil, "unavailable"
	}
	if len(body) > ipContextUpstreamBytes {
		return nil, "invalid_response"
	}
	v, err := ipContextJSON(body, 16, 16384)
	if err != nil {
		return nil, "invalid_response"
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, "invalid_response"
	}
	return m, "ok"
}
func (s *IPContextService) registration(ctx context.Context, address string) (out IPContextRegistration) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	base, registry := contextRDAPRoute(address)
	out = IPContextRegistration{Status: "not_found", Source: "rdap", Registry: registry}
	defer func() { out.FetchedAt = ipContextTime(s.now()) }()
	if base == "" {
		return
	}
	m, status := s.providerObject(ctx, base+"ip/"+address, true)
	out.Status = status
	if status != "ok" {
		return
	}
	projected, ok := projectContextRDAP(m, address)
	if !ok {
		out.Status = "invalid_response"
		return
	}
	projected.Registry = registry
	projected.Source = "rdap"
	projected.Status = "ok"
	out = projected
	return
}
func contextText(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && utf8.ValidString(s) && len(s) <= 256
}
func projectContextRDAP(m map[string]any, address string) (out IPContextRegistration, ok bool) {
	if m["objectClassName"] != "ip network" {
		return out, false
	}
	start, sok := m["startAddress"].(string)
	end, eok := m["endAddress"].(string)
	a, aok := canonicalContextAddress(start)
	b, bok := canonicalContextAddress(end)
	q, qok := canonicalContextAddress(address)
	if !sok || !eok || !aok || !bok || !qok || a.BitLen() != q.BitLen() || b.BitLen() != q.BitLen() || a.Compare(q) > 0 || b.Compare(q) < 0 {
		return out, false
	}
	out.StartAddress, out.EndAddress = start, end
	if v, present := m["ipVersion"]; present {
		want := "v6"
		if q.Is4() {
			want = "v4"
		}
		if v != want {
			return out, false
		}
	}
	for _, field := range []struct {
		key string
		dst *string
	}{{"handle", &out.Handle}, {"name", &out.Name}, {"type", &out.Type}, {"country", &out.Country}} {
		if raw, present := m[field.key]; present {
			s, valid := contextText(raw)
			if !valid {
				return out, false
			}
			*field.dst = s
		}
	}
	if out.Country != "" && !contextCountry(out.Country) {
		return out, false
	}
	if raw, present := m["events"]; present {
		events, valid := raw.([]any)
		if !valid || len(events) > 32 {
			return out, false
		}
		for _, raw := range events {
			event, valid := raw.(map[string]any)
			if !valid {
				return out, false
			}
			action, valid := event["eventAction"].(string)
			if !valid {
				return out, false
			}
			if action != "registration" && action != "last changed" {
				continue
			}
			date, valid := event["eventDate"].(string)
			if !valid {
				return out, false
			}
			// time.Parse accepts a one-digit hour; RFC3339 requires two.
			// Keep calendar and remaining time validation in the parser below.
			if len(date) < len("2006-01-02T15:04:05Z") || date[13] != ':' {
				return out, false
			}
			if strings.ContainsRune(date, ',') {
				return out, false
			}
			if len(date) >= 6 && date[len(date)-1] != 'Z' {
				offset := date[len(date)-6:]
				if (offset[0] == '+' || offset[0] == '-') && (offset[1:3] > "23" || offset[4:] > "59") {
					return out, false
				}
			}
			parsed, err := time.Parse(time.RFC3339Nano, date)
			if err != nil || parsed.UTC().Year() < 1 || parsed.UTC().Year() > 9999 {
				return out, false
			}
			fixed := ipContextTime(parsed)
			if action == "registration" && (out.RegisteredAt == "" || fixed < out.RegisteredAt) {
				out.RegisteredAt = fixed
			}
			if action == "last changed" && (out.UpdatedAt == "" || fixed > out.UpdatedAt) {
				out.UpdatedAt = fixed
			}
		}
	}
	if raw, present := m["entities"]; present {
		entities, valid := raw.([]any)
		if !valid || len(entities) > 32 {
			return out, false
		}
		for _, raw := range entities {
			entity, valid := raw.(map[string]any)
			if !valid {
				return out, false
			}
			registrant := false
			if roles, present := entity["roles"]; present {
				list, valid := roles.([]any)
				if !valid {
					return out, false
				}
				for _, role := range list {
					role, valid := role.(string)
					if !valid {
						return out, false
					}
					if role == "registrant" {
						registrant = true
					}
				}
			}
			rawCard, present := entity["vcardArray"]
			if !present {
				continue
			}
			card, valid := rawCard.([]any)
			if !valid || len(card) != 2 || card[0] != "vcard" {
				return out, false
			}
			rows, valid := card[1].([]any)
			if !valid || len(rows) > 16 {
				return out, false
			}
			kind, fn := "", ""
			for _, rawRow := range rows {
				row, valid := rawRow.([]any)
				if !valid || len(row) != 4 {
					return out, false
				}
				key, valid := row[0].(string)
				if !valid {
					return out, false
				}
				if key == "kind" || key == "fn" {
					if _, valid := row[1].(map[string]any); !valid || row[2] != "text" {
						return out, false
					}
				}
				if key == "kind" {
					s, valid := row[3].(string)
					if !valid || kind != "" {
						return out, false
					}
					kind = s
				}
				if key == "fn" && registrant {
					value, valid := contextText(row[3])
					if !valid {
						return out, false
					}
					if fn == "" {
						fn = value
					}
				}
			}
			if registrant && kind == "org" && out.Organization == "" {
				out.Organization = fn
			}
		}
	}
	return out, true
}
func contextCountry(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}
