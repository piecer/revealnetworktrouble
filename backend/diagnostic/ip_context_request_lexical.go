package diagnostic

import (
	"bytes"
	"encoding/json"
)

// Fixed-byte lexical pass only; it does not build an input value tree. This
// preserves duplicate-key error precedence even in semantically invalid shapes.
// The subsequent projection admits at most depth 2 / four parsed tokens.
func contextRequestUniqueKeys(data []byte) bool {
	var stack [128]map[string]bool
	depth := 0
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '{', '[':
			if depth == len(stack) {
				return false
			}
			stack[depth] = nil
			if data[i] == '{' {
				stack[depth] = map[string]bool{}
			}
			depth++
		case '}', ']':
			depth--
			if depth < 0 {
				return false
			}
		case '"':
			start := i
			i++
			for i < len(data) {
				if data[i] == '\\' {
					i += 2
					continue
				}
				if data[i] == '"' {
					break
				}
				i++
			}
			if i >= len(data) {
				return false
			}
			next := bytes.TrimLeft(data[i+1:], " \t\r\n")
			if len(next) > 0 && next[0] == ':' {
				if depth == 0 || stack[depth-1] == nil {
					return false
				}
				var key string
				if json.Unmarshal(data[start:i+1], &key) != nil {
					return false
				}
				if stack[depth-1][key] {
					return false
				}
				stack[depth-1][key] = true
			}
		}
	}
	return depth == 0
}
