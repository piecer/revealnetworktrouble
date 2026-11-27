package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Compose contract is resolved in pure Go so the default suite enforces it
// without the Docker CLI. The parser accepts exactly the YAML subset compose.yaml
// uses; anything outside that subset is rejected rather than silently misread, so
// this resolver cannot quietly diverge from the real CLI. The cross-check test
// compares the two when Docker is available.

type composeYAMLKind int

const (
	composeScalar composeYAMLKind = iota
	composeMapping
	composeSequence
)

type composeYAMLValue struct {
	kind    composeYAMLKind
	scalar  string
	keys    []string
	mapping map[string]composeYAMLValue
	items   []composeYAMLValue
}

func (v composeYAMLValue) child(key string) (composeYAMLValue, bool) {
	if v.kind != composeMapping {
		return composeYAMLValue{}, false
	}
	value, ok := v.mapping[key]
	return value, ok
}

type composeYAMLLine struct {
	number int
	indent int
	text   string
}

// composeYAMLKeyPattern is the key form the subset accepts: unquoted plain keys.
var composeYAMLKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// composeExpansionNamePattern is the variable-name form the interpolation rules
// support.
var composeExpansionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func parseComposeYAML(source string) (composeYAMLValue, error) {
	var lines []composeYAMLLine
	for number, raw := range strings.Split(source, "\n") {
		if strings.ContainsRune(raw, '\t') {
			return composeYAMLValue{}, fmt.Errorf("line %d: tab characters are outside the supported subset", number+1)
		}
		if strings.ContainsRune(raw, '\r') {
			return composeYAMLValue{}, fmt.Errorf("line %d: carriage returns are outside the supported subset", number+1)
		}
		body := stripComposeYAMLComment(strings.TrimRight(raw, " "))
		if body == "" {
			continue
		}
		if body == "---" || body == "..." {
			return composeYAMLValue{}, fmt.Errorf("line %d: document separators are outside the supported subset", number+1)
		}
		indent := len(body) - len(strings.TrimLeft(body, " "))
		lines = append(lines, composeYAMLLine{number + 1, indent, strings.TrimLeft(body, " ")})
	}
	if len(lines) == 0 {
		return composeYAMLValue{}, fmt.Errorf("document is empty after comments")
	}
	if lines[0].indent != 0 {
		return composeYAMLValue{}, fmt.Errorf("line %d: top-level entries must start at column 1", lines[0].number)
	}
	parsed, _, err := parseComposeYAMLBlock(lines, 0, 0)
	if err != nil {
		return composeYAMLValue{}, err
	}
	return parsed, nil
}

func parseComposeYAMLBlock(lines []composeYAMLLine, index, indent int) (composeYAMLValue, int, error) {
	if index >= len(lines) || lines[index].indent < indent {
		return composeYAMLValue{}, index, fmt.Errorf("block at indent %d is empty", indent)
	}
	if strings.HasPrefix(lines[index].text, "- ") {
		sequence := composeYAMLValue{kind: composeSequence}
		for index < len(lines) && lines[index].indent == indent {
			line := lines[index]
			if !strings.HasPrefix(line.text, "- ") {
				return composeYAMLValue{}, index, fmt.Errorf("line %d: sequence item mixes with mapping keys", line.number)
			}
			item := strings.TrimSpace(strings.TrimPrefix(line.text, "- "))
			if item == "" {
				return composeYAMLValue{}, index, fmt.Errorf("line %d: empty sequence item", line.number)
			}
			value, err := parseComposeYAMLFlowOrScalar(line.number, item)
			if err != nil {
				return composeYAMLValue{}, index, err
			}
			sequence.items = append(sequence.items, value)
			index++
		}
		return sequence, index, nil
	}

	mapping := composeYAMLValue{kind: composeMapping, mapping: map[string]composeYAMLValue{}}
	for index < len(lines) {
		line := lines[index]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return composeYAMLValue{}, index, fmt.Errorf("line %d: unexpected indentation inside the block", line.number)
		}
		key, rest, ok := strings.Cut(line.text, ":")
		if !ok {
			return composeYAMLValue{}, index, fmt.Errorf("line %d: mapping entry has no colon: %q", line.number, line.text)
		}
		if !composeYAMLKeyPattern.MatchString(key) || (rest != "" && !strings.HasPrefix(rest, " ")) {
			return composeYAMLValue{}, index, fmt.Errorf("line %d: malformed mapping entry: %q", line.number, line.text)
		}
		if _, exists := mapping.mapping[key]; exists {
			return composeYAMLValue{}, index, fmt.Errorf("line %d: duplicate key %q", line.number, key)
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			if index+1 < len(lines) && lines[index+1].indent > indent {
				child, next, err := parseComposeYAMLBlock(lines, index+1, lines[index+1].indent)
				if err != nil {
					return composeYAMLValue{}, next, err
				}
				mapping.keys = append(mapping.keys, key)
				mapping.mapping[key] = child
				index = next
				continue
			}
			mapping.keys = append(mapping.keys, key)
			mapping.mapping[key] = composeYAMLValue{kind: composeScalar}
			index++
			continue
		}
		value, err := parseComposeYAMLFlowOrScalar(line.number, rest)
		if err != nil {
			return composeYAMLValue{}, index, err
		}
		mapping.keys = append(mapping.keys, key)
		mapping.mapping[key] = value
		index++
	}
	return mapping, index, nil
}

// stripComposeYAMLComment removes a comment that starts at the beginning of the
// body or after whitespace outside a quoted scalar.
func stripComposeYAMLComment(body string) string {
	if body == "" {
		return ""
	}
	inQuote := byte(0)
	for i := 0; i < len(body); i++ {
		c := body[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			continue
		}
		if c == '#' && (i == 0 || body[i-1] == ' ') {
			return strings.TrimRight(body[:i], " ")
		}
	}
	return body
}

func parseComposeYAMLFlowOrScalar(lineNumber int, text string) (composeYAMLValue, error) {
	switch {
	case strings.HasPrefix(text, "["):
		if !strings.HasSuffix(text, "]") {
			return composeYAMLValue{}, fmt.Errorf("line %d: flow sequence must close on the same line", lineNumber)
		}
		sequence := composeYAMLValue{kind: composeSequence}
		inner := strings.TrimSpace(text[1 : len(text)-1])
		if inner == "" {
			return sequence, nil
		}
		for _, element := range splitFlowElements(inner) {
			value, err := parseComposeYAMLFlowOrScalar(lineNumber, element)
			if err != nil {
				return composeYAMLValue{}, err
			}
			if value.kind != composeScalar {
				return composeYAMLValue{}, fmt.Errorf("line %d: nested flow collections are outside the supported subset", lineNumber)
			}
			sequence.items = append(sequence.items, value)
		}
		return sequence, nil
	case strings.HasPrefix(text, "{"):
		return composeYAMLValue{}, fmt.Errorf("line %d: flow mappings are outside the supported subset", lineNumber)
	case strings.HasPrefix(text, "|") || strings.HasPrefix(text, ">"):
		return composeYAMLValue{}, fmt.Errorf("line %d: block scalars are outside the supported subset", lineNumber)
	case strings.HasPrefix(text, "&") || strings.HasPrefix(text, "*") || strings.HasPrefix(text, "!"):
		return composeYAMLValue{}, fmt.Errorf("line %d: anchors, aliases and tags are outside the supported subset", lineNumber)
	case strings.HasPrefix(text, "\"") || strings.HasPrefix(text, "'"):
		quote := text[0]
		end := strings.IndexByte(text[1:], quote)
		if end < 0 {
			return composeYAMLValue{}, fmt.Errorf("line %d: quoted scalar is not closed on the same line", lineNumber)
		}
		return composeYAMLValue{kind: composeScalar, scalar: text[1 : 1+end]}, nil
	default:
		if text == "<<" || strings.HasPrefix(text, "<< ") {
			return composeYAMLValue{}, fmt.Errorf("line %d: merge keys are outside the supported subset", lineNumber)
		}
		if strings.Contains(text, ": ") {
			return composeYAMLValue{}, fmt.Errorf("line %d: compact mapping is outside the supported subset: %q", lineNumber, text)
		}
		return composeYAMLValue{kind: composeScalar, scalar: text}, nil
	}
}

func splitFlowElements(inner string) []string {
	var elements []string
	start := 0
	inQuote := byte(0)
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			continue
		}
		if c == ',' {
			elements = append(elements, strings.TrimSpace(inner[start:i]))
			start = i + 1
		}
	}
	elements = append(elements, strings.TrimSpace(inner[start:]))
	return elements
}

// interpolateComposeValue applies the Compose variable rules for the supported
// forms: ${NAME}, ${NAME:-default}, $NAME and the $$ escape.
func interpolateComposeValue(text string, env map[string]string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '$' {
			out.WriteByte(text[i])
			i++
			continue
		}
		if i+1 >= len(text) {
			return "", fmt.Errorf("unterminated expansion at the end of %q", text)
		}
		if text[i+1] == '$' {
			out.WriteByte('$')
			i += 2
			continue
		}
		if text[i+1] != '{' {
			name := readExpansionName(text, i+1)
			if name == "" {
				return "", fmt.Errorf("bare expansion at offset %d is outside the supported subset", i)
			}
			out.WriteString(env[name])
			i += 1 + len(name)
			continue
		}
		end := strings.IndexByte(text[i+1:], '}')
		if end < 0 {
			return "", fmt.Errorf("expansion %q is not closed", text[i:])
		}
		expression := text[i+2 : i+1+end]
		name, modifier := expression, ""
		if cut := strings.IndexAny(expression, ":#%/?"); cut >= 0 {
			name, modifier = expression[:cut], expression[cut:]
		}
		if name == "" || !composeExpansionNamePattern.MatchString(name) {
			return "", fmt.Errorf("expansion %q has no plain variable name", text[i:])
		}
		switch {
		case modifier == "":
			out.WriteString(env[name])
		case strings.HasPrefix(modifier, ":-"):
			if env[name] == "" {
				out.WriteString(modifier[2:])
			} else {
				out.WriteString(env[name])
			}
		default:
			return "", fmt.Errorf("expansion %q uses an unsupported modifier", text[i:])
		}
		i += 1 + end + 1
	}
	return out.String(), nil
}

func readExpansionName(text string, start int) string {
	end := start
	for end < len(text) && (text[end] == '_' || (text[end] >= 'A' && text[end] <= 'Z') || (text[end] >= 'a' && text[end] <= 'z') || (text[end] >= '0' && text[end] <= '9')) {
		end++
	}
	return text[start:end]
}

type resolvedComposePort struct {
	HostIP    string
	Published string
	Target    string
	Protocol  string
}

type resolvedComposeService struct {
	Environment map[string]string
	BuildArgs   map[string]string
	Ports       []resolvedComposePort
	Profiles    []string
	Healthcheck []string
}

func resolveComposeConfig(source string, env map[string]string, profiles []string) (map[string]resolvedComposeService, error) {
	root, err := parseComposeYAML(source)
	if err != nil {
		return nil, err
	}
	services, ok := root.child("services")
	if !ok || services.kind != composeMapping {
		return nil, fmt.Errorf("compose document must define a services mapping")
	}
	resolved := map[string]resolvedComposeService{}
	for _, name := range services.keys {
		service, _ := services.child(name)
		if service.kind != composeMapping {
			return nil, fmt.Errorf("service %q must be a mapping", name)
		}
		profilesValue, hasProfiles := service.child("profiles")
		if hasProfiles && profilesValue.kind != composeSequence {
			return nil, fmt.Errorf("service %q profiles must be a sequence", name)
		}
		if hasProfiles && !profilesIntersect(profilesValue, profiles) {
			continue
		}
		entry := resolvedComposeService{
			Environment: map[string]string{},
			BuildArgs:   map[string]string{},
		}
		if hasProfiles {
			for _, item := range profilesValue.items {
				entry.Profiles = append(entry.Profiles, item.scalar)
			}
		}
		if build, ok := service.child("build"); ok {
			if build.kind != composeMapping {
				return nil, fmt.Errorf("service %q build must be a mapping", name)
			}
			args, ok := build.child("args")
			if ok {
				if args.kind != composeMapping {
					return nil, fmt.Errorf("service %q build args must be a mapping", name)
				}
				for _, key := range args.keys {
					value, err := interpolateComposeValue(args.mapping[key].scalar, env)
					if err != nil {
						return nil, fmt.Errorf("service %q build arg %s: %w", name, key, err)
					}
					entry.BuildArgs[key] = value
				}
			}
		}
		if environment, ok := service.child("environment"); ok {
			if environment.kind != composeMapping {
				return nil, fmt.Errorf("service %q environment must be a mapping", name)
			}
			for _, key := range environment.keys {
				value, err := interpolateComposeValue(environment.mapping[key].scalar, env)
				if err != nil {
					return nil, fmt.Errorf("service %q environment %s: %w", name, key, err)
				}
				entry.Environment[key] = value
			}
		}
		ports, ok := service.child("ports")
		if ok {
			if ports.kind != composeSequence {
				return nil, fmt.Errorf("service %q ports must be a sequence", name)
			}
			for _, item := range ports.items {
				port, err := parseResolvedComposePort(item.scalar, env)
				if err != nil {
					return nil, fmt.Errorf("service %q port: %w", name, err)
				}
				entry.Ports = append(entry.Ports, port)
			}
		}
		if healthcheck, ok := service.child("healthcheck"); ok {
			if healthcheck.kind != composeMapping {
				return nil, fmt.Errorf("service %q healthcheck must be a mapping", name)
			}
			test, ok := healthcheck.child("test")
			if !ok || test.kind != composeSequence {
				return nil, fmt.Errorf("service %q healthcheck must use the exec form sequence", name)
			}
			for _, item := range test.items {
				value, err := interpolateComposeValue(item.scalar, env)
				if err != nil {
					return nil, fmt.Errorf("service %q healthcheck: %w", name, err)
				}
				entry.Healthcheck = append(entry.Healthcheck, value)
			}
		}
		resolved[name] = entry
	}
	return resolved, nil
}

func profilesIntersect(declared composeYAMLValue, enabled []string) bool {
	for _, item := range declared.items {
		for _, profile := range enabled {
			if item.scalar == profile {
				return true
			}
		}
	}
	return false
}

func parseResolvedComposePort(text string, env map[string]string) (resolvedComposePort, error) {
	resolved, err := interpolateComposeValue(text, env)
	if err != nil {
		return resolvedComposePort{}, err
	}
	parts := strings.Split(resolved, ":")
	port := resolvedComposePort{Protocol: "tcp"}
	switch len(parts) {
	case 1:
		port.Target = parts[0]
	case 2:
		port.Published, port.Target = parts[0], parts[1]
	case 3:
		port.HostIP, port.Published, port.Target = parts[0], parts[1], parts[2]
	default:
		return resolvedComposePort{}, fmt.Errorf("port %q has more than three fields", resolved)
	}
	for _, field := range []string{port.Published, port.Target} {
		if field == "" {
			return resolvedComposePort{}, fmt.Errorf("port %q has an empty published or target field", resolved)
		}
		if strings.ContainsAny(field, "-/") {
			return resolvedComposePort{}, fmt.Errorf("port %q uses a port range outside the supported subset", resolved)
		}
	}
	return port, nil
}

func sortedComposeNames(services map[string]resolvedComposeService) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func normalizeResolvedCompose(services map[string]resolvedComposeService) map[string]resolvedComposeService {
	for name, service := range services {
		sort.Slice(service.Ports, func(a, b int) bool {
			if service.Ports[a].HostIP != service.Ports[b].HostIP {
				return service.Ports[a].HostIP < service.Ports[b].HostIP
			}
			if service.Ports[a].Published != service.Ports[b].Published {
				return service.Ports[a].Published < service.Ports[b].Published
			}
			return service.Ports[a].Target < service.Ports[b].Target
		})
		services[name] = service
	}
	return services
}

// TestComposeYAMLSubsetValidatorRejectsUnsupportedConstructs validates the
// validator itself: a construct the parser cannot read faithfully must fail, not
// resolve to a different value than the real CLI would produce.
func TestComposeYAMLSubsetValidatorRejectsUnsupportedConstructs(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		wantKeys []string
	}{
		{name: "exact document", source: "services:\n  api:\n    ports:\n      - \"127.0.0.1:8090:8080\"\n      - 3000:80\n    profiles: [\"standalone-web\"]\n    healthcheck:\n      test: [\"CMD\", \"wget\"]\n", wantKeys: []string{"services"}},
		{name: "comment after quoted scalar", source: "services:\n  api:\n    restart: unless-stopped # comment\n", wantKeys: []string{"services"}},
		{name: "hash inside quoted scalar", source: "services:\n  api:\n    environment:\n      PATH: \"/bin#fragment\"\n", wantKeys: []string{"services"}},
		{name: "anchor", source: "services:\n  api:\n    build: &base\n      args: {}\n", wantKeys: nil},
		{name: "alias", source: "services:\n  api:\n    build: *base\n", wantKeys: nil},
		{name: "tag", source: "services:\n  api:\n    build: !!str x\n", wantKeys: nil},
		{name: "merge key", source: "services:\n  api:\n    <<: *base\n", wantKeys: nil},
		{name: "multi document", source: "---\nservices:\n  api: {}\n", wantKeys: nil},
		{name: "block scalar", source: "services:\n  api:\n    restart: |\n      unless-stopped\n", wantKeys: nil},
		{name: "flow mapping", source: "services:\n  api:\n    build: {args: {VERSION: x}}\n", wantKeys: nil},
		{name: "unclosed flow sequence", source: "services:\n  api:\n    profiles: [\"standalone-web\"\n", wantKeys: nil},
		{name: "unclosed quote", source: "services:\n  api:\n    restart: \"unless\n", wantKeys: nil},
		{name: "tab indentation", source: "services:\n  api:\n\t  restart: unless-stopped\n", wantKeys: nil},
		{name: "carriage return", source: "services:\n  api:\n    restart: x\r\n", wantKeys: nil},
		{name: "compact sequence mapping", source: "services:\n  api:\n    ports:\n      - target: 8080\n", wantKeys: nil},
		{name: "duplicate key", source: "services:\n  api:\n    restart: a\n    restart: b\n", wantKeys: nil},
		{name: "quoted key", source: "services:\n  \"api\":\n    restart: x\n", wantKeys: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := parseComposeYAML(testCase.source)
			if len(testCase.wantKeys) == 0 {
				if err == nil {
					t.Fatalf("unsupported construct was accepted: %q", testCase.source)
				}
				return
			}
			if err != nil {
				t.Fatalf("supported construct was rejected: %v", err)
			}
			if parsed.kind != composeMapping || len(parsed.keys) != len(testCase.wantKeys) {
				t.Fatalf("parsed=%#v", parsed)
			}
			for i, key := range testCase.wantKeys {
				if parsed.keys[i] != key {
					t.Fatalf("keys=%v, want %v", parsed.keys, testCase.wantKeys)
				}
			}
		})
	}
	if _, err := parseComposeYAML(repositoryFile(t, "compose.yaml")); err != nil {
		t.Fatalf("the shipped compose.yaml must stay inside the validated subset: %v", err)
	}
}

// posixEREExists evaluates a POSIX ERE in pure Go so the contract runs in a clean
// environment with no external binary. Only constructs whose match-existence
// semantics coincide with POSIX ERE are accepted; anything else fails closed
// instead of quietly meaning something else. The contract asserts existence, not
// submatch or greediness, so leftmost-longest and Go's leftmost-first cannot
// disagree. grep -E cross-checks the same pattern when it is present.
func posixEREExists(pattern, candidate string) (bool, error) {
	compiled, err := posixERETranslation(pattern)
	if err != nil {
		return false, err
	}
	return compiled.MatchString(candidate), nil
}

// posixERETranslation maps a POSIX ERE onto a Go regexp that means the same thing.
// The construct where the two languages disagree about existence is a backslash
// inside a bracket expression: POSIX makes it an ordinary member, Go reads it as an
// escape and drops it from the set. Keeping the backslash as a member is what the
// reference implementation does, so the translation writes it as a literal member
// instead of silently checking a different set.
func posixERETranslation(pattern string) (*regexp.Regexp, error) {
	const members = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.^$|?*+()[]{}-"
	var translated strings.Builder
	inBracket := false
	openedAt := -1
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if inBracket {
			if c == ']' && i > openedAt+1 {
				inBracket = false
				translated.WriteByte(c)
				continue
			}
			if c == '\\' {
				translated.WriteString(`\\`)
				continue
			}
			if strings.IndexByte(members, c) < 0 {
				return nil, fmt.Errorf("unsupported ERE construct %q", pattern[i:i+1])
			}
			translated.WriteByte(c)
			continue
		}
		if c == '[' {
			inBracket = true
			openedAt = i
			translated.WriteByte(c)
			continue
		}
		if c == '\\' {
			if i+1 >= len(pattern) || strings.IndexByte(".+?^$()|[]{}*-", pattern[i+1]) < 0 {
				return nil, fmt.Errorf("unsupported ERE escape %q", pattern[i:i+2])
			}
			translated.WriteString(pattern[i : i+2])
			i++
			continue
		}
		if strings.IndexByte(members, c) < 0 {
			return nil, fmt.Errorf("unsupported ERE construct %q", pattern[i:i+1])
		}
		translated.WriteByte(c)
	}
	compiled, err := regexp.Compile(translated.String())
	if err != nil {
		return nil, fmt.Errorf("translated ERE %q is not executable: %v", translated.String(), err)
	}
	return compiled, nil
}

// Canonical SemVer ERE acceptance corpus, shared by the pure-Go contract test and
// the POSIX grep cross-check.
var canonicalSemverValidCorpus = []string{"0.1.0-dev", "1.0.0-alpha.1", "1.0.0+build.5", "1.0.0-0"}
var canonicalSemverInvalidCorpus = []string{
	"dev", "v1.0.0", "01.0.0", "1.01.0", "1.0.01", "1.0.0-01",
	"1..0", "1.0.0-alpha..1", "1.0.0-", "1.0.0-alpha.", "1.0.0+build..1",
	"1.0.0-alpha_beta",
}

// POSIX ERE treats every character inside a bracket expression as an ordinary
// member, so [\.-] contains a literal backslash. Go reads "\." as an escape and
// would mean a different set. The evaluator must keep the sets identical,
// otherwise the pure-Go contract silently checks a pattern grep would not match.
func TestBracketExpressionsKeepBackslashAsLiteralMember(t *testing.T) {
	const bracketERE = `^1\.0\.0[\.-]7$`
	for _, candidate := range []string{"1.0.0\\7", "1.0.0.7", "1.0.0-7"} {
		accepted, err := posixEREExists(bracketERE, candidate)
		if err != nil {
			t.Fatalf("bracket expression %q rejected: %v", bracketERE, err)
		}
		if !accepted {
			t.Fatalf("candidate %q must match %q: a backslash inside a bracket expression is a POSIX member, not an escape", candidate, bracketERE)
		}
	}
	accepted, err := posixEREExists(bracketERE, "1.0.0x7")
	if err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatalf("a member set must not accept characters outside it")
	}
}

// TestCanonicalERECrossChecksPOSIXGrepWhenPresent keeps the pure-Go evaluation honest
// against the reference POSIX implementation. Absent grep skips only this
// cross-check; the enforced contract already ran in pure Go.
func TestCanonicalERECrossChecksPOSIXGrepWhenPresent(t *testing.T) {
	grepPath := declaredToolPath(t, "grep")
	grepAccepts := func(candidate string) bool {
		t.Helper()
		command := exec.Command(grepPath, "-Eq", canonicalSemverERE)
		command.Stdin = strings.NewReader(candidate + "\n")
		runErr := command.Run()
		if runErr == nil {
			return true
		}
		if exitErr, ok := runErr.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false
		}
		t.Fatalf("POSIX grep failed on %q: %v", candidate, runErr)
		return false
	}
	for _, candidate := range canonicalSemverValidCorpus {
		accepted, err := posixEREExists(canonicalSemverERE, candidate)
		if err != nil {
			t.Fatal(err)
		}
		if !accepted || !grepAccepts(candidate) {
			t.Fatalf("pure-Go and POSIX grep disagree on valid corpus member %q: pure=%v grep=%v", candidate, accepted, grepAccepts(candidate))
		}
	}
	for _, candidate := range canonicalSemverInvalidCorpus {
		accepted, err := posixEREExists(canonicalSemverERE, candidate)
		if err != nil {
			t.Fatal(err)
		}
		if accepted || grepAccepts(candidate) {
			t.Fatalf("pure-Go and POSIX grep disagree on invalid corpus member %q: pure=%v grep=%v", candidate, accepted, grepAccepts(candidate))
		}
	}

	// The bracket expression is the one construct where the two languages can
	// diverge, so the agreement must be checked on it as well as on the corpus.
	const bracketERE = `^1\.0\.0[\.-]7$`
	for _, candidate := range []string{"1.0.0\\7", "1.0.0.7", "1.0.0-7", "1.0.0x7"} {
		accepted, err := posixEREExists(bracketERE, candidate)
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(grepPath, "-Eq", bracketERE)
		command.Stdin = strings.NewReader(candidate + "\n")
		runErr := command.Run()
		grepHit := runErr == nil
		if runErr != nil {
			exitErr, ok := runErr.(*exec.ExitError)
			if !ok || exitErr.ExitCode() != 1 {
				t.Fatalf("POSIX grep failed on bracket candidate %q: %v", candidate, runErr)
			}
		}
		if accepted != grepHit {
			t.Fatalf("pure-Go and POSIX grep disagree on bracket member %q: pure=%v grep=%v", candidate, accepted, grepHit)
		}
	}
}

// TestComposeInterpolationFollowsComposeVariableRules pins the semantics the
// resolver claims for the forms compose.yaml uses.
func TestComposeInterpolationFollowsComposeVariableRules(t *testing.T) {
	env := map[string]string{"SET": "value", "EMPTY": ""}
	cases := []struct {
		name  string
		text  string
		want  string
		error string
	}{
		{name: "default when unset", text: "${SET:-fallback}", want: "value"},
		{name: "default when empty", text: "${EMPTY:-fallback}", want: "fallback"},
		{name: "unset with default", text: "${ABSENT:-fallback}", want: "fallback"},
		{name: "bare expansion unset is empty", text: "${ABSENT}", want: ""},
		{name: "bare expansion set", text: "$SET", want: "value"},
		{name: "escape", text: "$$SET", want: "$SET"},
		{name: "literal text", text: "http://127.0.0.1:8080/readyz", want: "http://127.0.0.1:8080/readyz"},
		{name: "required modifier rejected", text: "${SET:?message}", error: "unsupported modifier"},
		{name: "substring modifier rejected", text: "${SET#v}", error: "unsupported modifier"},
		{name: "unclosed expansion", text: "${SET", error: "not closed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := interpolateComposeValue(testCase.text, env)
			if testCase.error != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.error) {
					t.Fatalf("error=%v, want %q", err, testCase.error)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("resolved %q to %q, want %q", testCase.text, got, testCase.want)
			}
		})
	}
}

// TestComposeResolverAppliesDefaultsProfilesAndPublications is the docker-free
// core of the M-1 contract: single-port default, opt-in profile, loopback-only
// publication, and caller overrides flowing verbatim into shared build args.
func TestComposeResolverAppliesDefaultsProfilesAndPublications(t *testing.T) {
	source := repositoryFile(t, "compose.yaml")

	defaults, err := resolveComposeConfig(source, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedComposeNames(defaults); len(got) != 1 || got[0] != "api" {
		t.Fatalf("default services=%v, want only api", got)
	}
	if len(defaults["api"].Ports) != 1 || defaults["api"].Ports[0].HostIP != "127.0.0.1" || defaults["api"].Ports[0].Published != "8090" || defaults["api"].Ports[0].Target != "8080" {
		t.Fatalf("default publication=%+v", defaults["api"].Ports)
	}

	profiled, err := resolveComposeConfig(source, nil, []string{"standalone-web"})
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedComposeNames(profiled); len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Fatalf("profiled services=%v, want api and web", got)
	}
	if len(profiled["web"].Ports) != 1 || profiled["web"].Ports[0].HostIP != "127.0.0.1" || profiled["web"].Ports[0].Published != "3000" {
		t.Fatalf("web publication=%+v", profiled["web"].Ports)
	}

	overrides := map[string]string{"CHECKNETWORK_API_PORT": "18090", "CHECKNETWORK_WEB_PORT": "13000"}
	caller, err := resolveComposeConfig(source, overrides, []string{"standalone-web"})
	if err != nil {
		t.Fatal(err)
	}
	if caller["api"].Ports[0].Published != "18090" || caller["web"].Ports[0].Published != "13000" {
		t.Fatalf("overrides did not flow through: api=%+v web=%+v", caller["api"].Ports, caller["web"].Ports)
	}
}

func TestComposeResolverFailsClosedOnUnsupportedConstructs(t *testing.T) {
	// A construct the resolver cannot read faithfully must fail the gate instead
	// of resolving to a value the real CLI would not produce.
	for _, source := range []string{
		"services:\n  api:\n    build: &base\n      args: {}\n",
		"services:\n  api:\n    build:\n      args: {VERSION: x}\n",
		"services:\n  api:\n    ports:\n      - \"127.0.0.1:${SET#v}:8080\"\n",
	} {
		if _, err := resolveComposeConfig(source, nil, nil); err == nil {
			t.Fatalf("resolver accepted a construct it cannot resolve faithfully:\n%s", source)
		}
	}
}

type realComposeCLIStyle struct {
	Services map[string]struct {
		Environment map[string]string `json:"environment"`
		Build       struct {
			Args map[string]string `json:"args"`
		} `json:"build"`
		Healthcheck struct {
			Test []string `json:"test"`
		} `json:"healthcheck"`
		Ports []struct {
			HostIP    string      `json:"host_ip"`
			Published string      `json:"published"`
			Target    json.Number `json:"target"`
			Protocol  string      `json:"protocol"`
		} `json:"ports"`
	} `json:"services"`
}

func runRealComposeCLI(t *testing.T, env map[string]string, profiles ...string) realComposeCLIStyle {
	t.Helper()
	requireComposeIntegration(t)
	args := []string{"compose", "--env-file", "/dev/null", "-f", "compose.yaml"}
	for _, profile := range profiles {
		args = append(args, "--profile", profile)
	}
	args = append(args, "config", "--format", "json")
	command := exec.Command("docker", args...)
	command.Dir = filepath.Join("..", "..")
	command.Env = append([]string{"PATH=" + os.Getenv("PATH")}, mapToEnvList(env)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real docker compose config: %v\n%s", err, output)
	}
	var resolved realComposeCLIStyle
	if err := json.Unmarshal(output, &resolved); err != nil {
		t.Fatalf("decode real docker compose config: %v\n%s", err, output)
	}
	return resolved
}

func mapToEnvList(env map[string]string) []string {
	entries := make([]string, 0, len(env))
	for name, value := range env {
		entries = append(entries, name+"="+value)
	}
	sort.Strings(entries)
	return entries
}

// TestRealComposeCLIResolvesIdenticallyToPureResolver is the cross-check: it
// proves the pure resolver's claimed semantics match the CLI, but only when
// Docker is present and the integration run was explicitly requested.
func TestRealComposeCLIResolvesIdenticallyToPureResolver(t *testing.T) {
	source := repositoryFile(t, "compose.yaml")
	for _, profile := range []string{"", "standalone-web"} {
		t.Run(strings.ReplaceAll(profile, "-", "_"), func(t *testing.T) {
			env := map[string]string{"CHECKNETWORK_API_PORT": "18090"}
			var profiles []string
			if profile != "" {
				profiles = []string{profile}
			}
			pure := normalizeResolvedCompose(mustResolve(t, source, env, profiles))
			real := runRealComposeCLI(t, env, profiles...)
			if len(real.Services) != len(pure) {
				t.Fatalf("services differ: real=%v pure=%v", sortedRealServices(real), sortedComposeNames(pure))
			}
			for name, want := range pure {
				got := real.Services[name]
				if !equalStringMaps(got.Environment, want.Environment) {
					t.Errorf("%s environment differs:\n real=%v\n pure=%v", name, got.Environment, want.Environment)
				}
				if !equalStringMaps(got.Build.Args, want.BuildArgs) {
					t.Errorf("%s build args differ:\n real=%v\n pure=%v", name, got.Build.Args, want.BuildArgs)
				}
				if strings.Join(got.Healthcheck.Test, " ") != strings.Join(want.Healthcheck, " ") {
					t.Errorf("%s healthcheck differs:\n real=%q\n pure=%q", name, got.Healthcheck.Test, want.Healthcheck)
				}
				if len(got.Ports) != len(want.Ports) {
					t.Errorf("%s ports differ: real=%d pure=%d", name, len(got.Ports), len(want.Ports))
					continue
				}
				for i := range want.Ports {
					realPort := got.Ports[i]
					if realPort.HostIP != want.Ports[i].HostIP || realPort.Published != want.Ports[i].Published || realPort.Target.String() != want.Ports[i].Target || realPort.Protocol != want.Ports[i].Protocol {
						t.Errorf("%s port %d differs: real=%+v pure=%+v", name, i, realPort, want.Ports[i])
					}
				}
			}
		})
	}
}

func mustResolve(t *testing.T, source string, env map[string]string, profiles []string) map[string]resolvedComposeService {
	t.Helper()
	resolved, err := resolveComposeConfig(source, env, profiles)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func sortedRealServices(resolved realComposeCLIStyle) []string {
	names := make([]string, 0, len(resolved.Services))
	for name := range resolved.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
