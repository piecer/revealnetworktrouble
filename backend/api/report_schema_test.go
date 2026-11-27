package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

// ARC-2 gate: the committed report schema is the machine-readable single source
// for the wire structure, and every producer-witnessed report must validate
// against it. The semantic corpus still owns what a schema cannot express
// (relationships, canonical spelling, block-internal key sets), so this gate
// checks structure only and must not be treated as the whole contract.
//
// The validator is deliberately a JSON Schema subset — it supports exactly the
// constructs the generator emits. A gate that reports zero findings because it
// never looked is a false pass, so the tests below also prove the validator
// rejects a key the registered shape does not allow.
func TestProducerWitnessedReportsValidateAgainstCommittedSchema(t *testing.T) {
	schema := readCommittedReportSchema(t)
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "presentation-contract.json"))
	if err != nil {
		t.Fatalf("read committed presentation contract: %v", err)
	}
	var contract struct {
		Scenarios []struct {
			Name   string         `json:"name"`
			Report map[string]any `json:"report"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("decode presentation contract: %v", err)
	}
	if len(contract.Scenarios) == 0 {
		t.Fatal("committed presentation contract carries no producer-witnessed reports")
	}
	reportSchema := schema["types"].(map[string]any)["report"]
	for _, scenario := range contract.Scenarios {
		violations := validateNode(reportSchema, scenario.Report, "report", schema)
		if len(violations) != 0 {
			t.Fatalf("producer-witnessed report %q does not validate against the committed schema: %v", scenario.Name, violations)
		}
	}
}

// The gate must be able to see. A key the registered shape never lists cannot
// appear in a producer report, so an extra detail key must fail validation.
func TestReportSchemaRejectsDetailKeyOutsideRegisteredShape(t *testing.T) {
	schema := readCommittedReportSchema(t)
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "presentation-contract.json"))
	if err != nil {
		t.Fatalf("read committed presentation contract: %v", err)
	}
	var contract struct {
		Scenarios []struct {
			Name   string         `json:"name"`
			Report map[string]any `json:"report"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("decode presentation contract: %v", err)
	}
	reportSchema := schema["types"].(map[string]any)["report"]
	witnessed := false
	for _, scenario := range contract.Scenarios {
		results, ok := scenario.Report["results"].([]any)
		if !ok || len(results) == 0 {
			continue
		}
		result, ok := results[0].(map[string]any)
		if !ok {
			continue
		}
		details, ok := result["details"].(map[string]any)
		if !ok {
			continue
		}
		witnessed = true
		details["unregistered_key"] = 1
		if len(validateNode(reportSchema, scenario.Report, "report", schema)) == 0 {
			t.Fatalf("scenario %q accepted a detail key outside the registered shape", scenario.Name)
		}
	}
	if !witnessed {
		t.Fatal("no producer-witnessed scenario carries a details object, so the probe proved nothing")
	}
}

// Prove the validator reads the schema rather than the registry it was derived
// from: removing a registered optional key from the in-memory schema must make
// the report that actually carries that key fail.
func TestReportSchemaEnforcesItsOwnRegisteredOptionalKeys(t *testing.T) {
	schema := readCommittedReportSchema(t)
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "presentation-contract.json"))
	if err != nil {
		t.Fatalf("read committed presentation contract: %v", err)
	}
	var contract struct {
		Scenarios []struct {
			Name   string         `json:"name"`
			Report map[string]any `json:"report"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("decode presentation contract: %v", err)
	}
	resultSchema := schema["types"].(map[string]any)["result"]
	reportSchema := schema["types"].(map[string]any)["report"]
	removed := 0
	hit := false
	for _, scenario := range contract.Scenarios {
		results, ok := scenario.Report["results"].([]any)
		if !ok || len(results) == 0 {
			continue
		}
		result, ok := results[0].(map[string]any)
		if !ok {
			continue
		}
		details, ok := result["details"].(map[string]any)
		if !ok || len(details) == 0 {
			continue
		}
		// Take the branch this producer-witnessed result matches, then forget one
		// key the result actually carries. If validation still passes, the schema
		// is not the thing enforcing the key set.
		var branch map[string]any
		for _, branchValue := range resultSchema.(map[string]any)["oneOf"].([]any) {
			if len(validateNode(branchValue, result, "result", schema)) == 0 {
				branch = branchValue.(map[string]any)
				break
			}
		}
		if branch == nil {
			continue
		}
		detailsNode := branch["properties"].(map[string]any)["details"].(map[string]any)
		key := sortedFirstKey(detailsNode["properties"].(map[string]any))
		if _, carried := details[key]; !carried {
			continue
		}
		delete(detailsNode["properties"].(map[string]any), key)
		kept := []any{}
		for _, entry := range detailsNode["required"].([]any) {
			if entry != key {
				kept = append(kept, entry)
			}
		}
		detailsNode["required"] = kept
		removed++
		if len(validateNode(reportSchema, scenario.Report, "report", schema)) == 0 {
			t.Fatalf("scenario %q still validated after its registered detail key was removed from the schema", scenario.Name)
		}
		hit = true
	}
	if removed == 0 || !hit {
		t.Fatal("no producer-witnessed report carries a registered detail key the probe could remove, so the probe proved nothing")
	}
}

// The committed schema is generated from the model tags and the shape registry,
// so it is compared byte-for-byte against the generator. A hand-edited schema is
// the exact failure mode ARC-2 exists to prevent, and the freshness probe below
// shows the byte gate is not satisfied by a schema that drifted.
func TestCommittedReportSchemaMatchesGeneratedRegistry(t *testing.T) {
	generated, err := diagnostic.MarshalReportSchema()
	if err != nil {
		t.Fatalf("generate report schema: %v", err)
	}
	path := filepath.Join("..", "..", "testdata", "report-schema.json")
	if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
		if err := os.WriteFile(path, generated, 0o644); err != nil {
			t.Fatalf("write committed report schema: %v", err)
		}
		return
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed report schema: %v", err)
	}
	if string(committed) != string(generated) {
		t.Fatalf("committed report schema is not what the model and shape registry produce")
	}
}

// A gate that reports nothing because it never looked is a false pass: mutate
// one byte of the committed bytes and confirm the comparison still fails.
func TestReportSchemaFreshnessRejectsOneByteMutation(t *testing.T) {
	generated, err := diagnostic.MarshalReportSchema()
	if err != nil {
		t.Fatalf("generate report schema: %v", err)
	}
	mutated := append([]byte(nil), generated...)
	mutated[len(mutated)-2] = 'x'
	if string(mutated) == string(generated) {
		t.Fatal("the byte comparison cannot see a mutated schema")
	}
}

func readCommittedReportSchema(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "report-schema.json"))
	if err != nil {
		t.Fatalf("read committed report schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode committed report schema: %v", err)
	}
	if _, ok := schema["types"].(map[string]any); !ok {
		t.Fatal("committed report schema has no types block")
	}
	return schema
}

func sortedFirstKey(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys[0]
}

// validateNode enforces the subset the generator emits: type, const, enum,
// pattern, minLength, minimum, required, properties, additionalProperties,
// items, oneOf and $ref.
func validateNode(node any, value any, path string, root map[string]any) []string {
	if ref, ok := node.(map[string]any)["$ref"].(string); ok {
		types := root["types"].(map[string]any)
		target := types[ref[len("#/types/"):]]
		if target == nil {
			return []string{path + ": $ref " + ref + " does not resolve"}
		}
		return validateNode(target, value, path, root)
	}
	n, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	var violations []string
	if want, ok := n["const"].(string); ok {
		if got, ok := value.(string); !ok || got != want {
			violations = append(violations, path+": must equal "+want)
		}
	}
	if options, ok := n["enum"].([]any); ok {
		got, ok := value.(string)
		if !ok {
			violations = append(violations, path+": must be one of the registered enum values")
		} else {
			found := false
			for _, option := range options {
				if option == got {
					found = true
					break
				}
			}
			if !found {
				violations = append(violations, path+": "+got+" is outside the registered enum")
			}
		}
	}
	if oneOf, ok := n["oneOf"].([]any); ok {
		matched := 0
		for _, branch := range oneOf {
			if len(validateNode(branch, value, path, root)) == 0 {
				matched++
			}
		}
		if matched != 1 {
			violations = append(violations, path+": matched "+jsonNumber(matched)+" branches of the registered shape set, want exactly 1")
		}
		return violations
	}
	switch typed := value.(type) {
	case map[string]any:
		if n["type"] != "object" {
			return append(violations, path+": object where the schema declares "+jsonValue(n["type"]))
		}
		for _, key := range stringList(n["required"]) {
			if _, ok := typed[key]; !ok {
				violations = append(violations, path+"."+key+": required key is absent")
			}
		}
		properties, _ := n["properties"].(map[string]any)
		strict, declared := n["additionalProperties"].(bool)
		for key, child := range typed {
			if childSchema, ok := properties[key]; ok {
				violations = append(violations, validateNode(childSchema, child, path+"."+key, root)...)
			} else if declared && !strict {
				violations = append(violations, path+"."+key+": key is outside the registered set")
			}
		}
	case []any:
		if n["type"] != "array" {
			return append(violations, path+": array where the schema declares "+jsonValue(n["type"]))
		}
		if n["items"] != nil {
			for i, item := range typed {
				violations = append(violations, validateNode(n["items"], item, path+"["+jsonNumber(i)+"]", root)...)
			}
		}
	case string:
		if n["type"] != "string" {
			return append(violations, path+": string where the schema declares "+jsonValue(n["type"]))
		}
		if minLength, ok := n["minLength"].(float64); ok && float64(len(typed)) < minLength {
			violations = append(violations, path+": empty where the schema requires a nonempty string")
		}
		if pattern, ok := n["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(typed) {
			violations = append(violations, path+": "+typed+" does not match the declared pattern")
		}
	case float64:
		if n["type"] != "integer" && n["type"] != "number" {
			return append(violations, path+": number where the schema declares "+jsonValue(n["type"]))
		}
		if minimum, ok := n["minimum"].(float64); ok && typed < minimum {
			violations = append(violations, path+": "+jsonNumber(int(typed))+" is below the declared minimum")
		}
	case bool:
		if n["type"] != "boolean" {
			return append(violations, path+": boolean where the schema declares "+jsonValue(n["type"]))
		}
	case nil:
		return append(violations, path+": null is not a schema value")
	}
	return violations
}

func stringList(node any) []string {
	list, ok := node.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func jsonValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "<unmarshalable>"
	}
	return string(data)
}

func jsonNumber(value int) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "0"
	}
	return string(data)
}
