package diagnostic

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

func TestPresentationContractRegistriesExhaustActualAnalysisSignals(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "analysis.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	coverage := map[string]struct{}{}
	evidence := map[string]struct{}{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CompositeLit:
			identifier, ok := value.Type.(*ast.Ident)
			if !ok || identifier.Name != "CoverageIssue" {
				return true
			}
			for _, element := range value.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				key, keyOK := fieldKey(field)
				if ok && keyOK && key == "Signal" {
					coverage[staticSignalValue(t, field.Value)] = struct{}{}
				}
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "add" || len(value.Args) != 15 {
				return true
			}
			evidence[staticSignalValue(t, value.Args[7])] = struct{}{}
		}
		return true
	})

	gotCoverage := sortedSet(coverage)
	if !reflect.DeepEqual(gotCoverage, presentationCoverageSignalRegistry) {
		t.Fatalf("CoverageIssue signal registry = %v, actual Analyze emitters = %v", presentationCoverageSignalRegistry, gotCoverage)
	}
	gotEvidence := sortedSet(evidence)
	wantEvidence := make([]string, len(presentationEvidenceSignalRegistry))
	for index, entry := range presentationEvidenceSignalRegistry {
		wantEvidence[index] = entry.Signal
	}
	if !reflect.DeepEqual(gotEvidence, wantEvidence) {
		t.Fatalf("evidence signal registry = %v, actual Analyze emitters = %v", wantEvidence, gotEvidence)
	}
}

func TestPresentationContractRegistryCardinalityAndOrdering(t *testing.T) {
	if got, want := presentationSemanticKeys, []string{"cause", "supporting_evidence", "expectation", "evidence_directness", "coverage_limitation", "next_action"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic keys = %v, want %v", got, want)
	}
	if len(presentationEvidenceSignalRegistry) != 10 || len(presentationCoverageSignalRegistry) != 21 {
		t.Fatalf("registry counts = evidence %d coverage %d", len(presentationEvidenceSignalRegistry), len(presentationCoverageSignalRegistry))
	}
	if !sort.StringsAreSorted(presentationCoverageSignalRegistry) {
		t.Fatalf("coverage registry is not sorted: %v", presentationCoverageSignalRegistry)
	}
	evidenceNames := make([]string, len(presentationEvidenceSignalRegistry))
	for index, entry := range presentationEvidenceSignalRegistry {
		evidenceNames[index] = entry.Signal
	}
	if !sort.StringsAreSorted(evidenceNames) {
		t.Fatalf("evidence registry is not sorted: %v", evidenceNames)
	}
}

func fieldKey(field *ast.KeyValueExpr) (string, bool) {
	if field == nil {
		return "", false
	}
	identifier, ok := field.Key.(*ast.Ident)
	return identifier.Name, ok
}

func staticSignalValue(t *testing.T, expression ast.Expr) string {
	t.Helper()
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			break
		}
		decoded, err := strconv.Unquote(value.Value)
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	case *ast.Ident:
		if value.Name == "ResultDetailCertificateExpires" {
			return ResultDetailCertificateExpires
		}
	}
	t.Fatalf("analysis signal emitter must use a statically auditable closed value: %T", expression)
	return ""
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
