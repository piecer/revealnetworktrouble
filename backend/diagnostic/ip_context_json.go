package diagnostic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"unicode/utf8"
)

var ErrIPContextJSON = errors.New("invalid context JSON")
var errIPContextJSONBudget = errors.New("context JSON budget exceeded")

// Lexical admission precedes projection; decoded duplicate keys are never lost.
func ipContextJSON(data []byte, maxDepth, maxTokens int) (any, error) {
	if !utf8.Valid(data) || !validJSONSurrogateEscapes(data) || !json.Valid(data) {
		return nil, ErrIPContextJSON
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	count := 0
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		token, err := d.Token()
		if err != nil {
			return nil, ErrIPContextJSON
		}
		count++
		if count > maxTokens || depth > maxDepth {
			return nil, errIPContextJSONBudget
		}
		switch token {
		case json.Delim('{'):
			m := map[string]any{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return nil, ErrIPContextJSON
				}
				s, ok := key.(string)
				if !ok {
					return nil, ErrIPContextJSON
				}
				if _, exists := m[s]; exists {
					return nil, ErrIPContextJSON
				}
				count++
				if count > maxTokens {
					return nil, errIPContextJSONBudget
				}
				v, e := read(depth + 1)
				if e != nil {
					return nil, e
				}
				m[s] = v
			}
			end, e := d.Token()
			count++
			if e != nil || end != json.Delim('}') {
				return nil, ErrIPContextJSON
			}
			if count > maxTokens {
				return nil, errIPContextJSONBudget
			}
			return m, nil
		case json.Delim('['):
			a := []any{}
			for d.More() {
				v, e := read(depth + 1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			end, e := d.Token()
			count++
			if e != nil || end != json.Delim(']') {
				return nil, ErrIPContextJSON
			}
			if count > maxTokens {
				return nil, errIPContextJSONBudget
			}
			return a, nil
		default:
			return token, nil
		}
	}
	v, err := read(1)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, ErrIPContextJSON
	}
	return v, nil
}
func canonicalContextAddress(address string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(address)
	return ip, err == nil && ip.Zone() == "" && !ip.Is4In6() && ip.String() == address
}
func validContextAddress(address string) bool {
	_, ok := canonicalContextAddress(address)
	return ok && IsPublicDiagnosticIP(net.ParseIP(address))
}
func ParseIPContextRequest(data []byte) (string, error) {
	if len(data) > 256 {
		return "", ErrIPContextInvalid
	}
	if !utf8.Valid(data) || !validJSONSurrogateEscapes(data) || !json.Valid(data) || !contextRequestUniqueKeys(data) {
		return "", ErrIPContextJSON
	}
	v, err := ipContextJSON(data, 2, 4)
	if err != nil {
		if errors.Is(err, errIPContextJSONBudget) {
			return "", ErrIPContextInvalid
		}
		return "", err
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return "", ErrIPContextInvalid
	}
	a, ok := m["address"].(string)
	if !ok || !validContextAddress(a) {
		return "", ErrIPContextInvalid
	}
	return a, nil
}
