package api

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestAPIErrorContractRegistryIsClosedValidAndSorted(t *testing.T) {
	rows := apiErrorContractRows()
	if len(rows) != 16 || len(apiErrorRegistry) != 16 {
		t.Fatalf("rows=%d registry=%d", len(rows), len(apiErrorRegistry))
	}
	keys := make([]string, len(rows))
	seenCodes := make(map[string]bool)
	for index, row := range rows {
		keys[index] = string(row.Key)
		if row.Key == "" || row.Status < 400 || row.Status > 599 || row.Code == "" || row.Message == "" {
			t.Fatalf("invalid row: %+v", row)
		}
		if row.RetryAfter && !row.Retryable {
			t.Fatalf("Retry-After applies to non-retryable row: %+v", row)
		}
		seenCodes[row.Code] = true
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("registry rows are not sorted: %v", keys)
	}
	for _, code := range []string{
		"body_decode_capacity_unavailable", "compact_response_too_large", "full_response_too_large",
		"internal_error", "invalid_json", "invalid_request", "network_policy_blocked", "rate_limited",
		"request_too_large", "response_serialization_failed", "server_busy", "server_draining", "unauthorized", "unmatched",
		"write_capacity_unavailable",
	} {
		if !seenCodes[code] {
			t.Errorf("reachable code %q is absent", code)
		}
	}
}

func TestEveryAPIErrorRegistryKeyIsReachableFromProductionSource(t *testing.T) {
	identifiers := map[apiErrorKey]string{
		apiErrorBodyDecodeCapacity: "apiErrorBodyDecodeCapacity", apiErrorCompactTooLarge: "apiErrorCompactTooLarge",
		apiErrorFullTooLarge: "apiErrorFullTooLarge", apiErrorInternal: "apiErrorInternal",
		apiErrorInvalidJSON: "apiErrorInvalidJSON", apiErrorInvalidRequest: "apiErrorInvalidRequest",
		apiErrorMethodNotAllowed: "apiErrorMethodNotAllowed", apiErrorNetworkPolicy: "apiErrorNetworkPolicy",
		apiErrorRateLimited: "apiErrorRateLimited", apiErrorRequestTooLarge: "apiErrorRequestTooLarge",
		apiErrorRouteNotFound: "apiErrorRouteNotFound", apiErrorSerialization: "apiErrorSerialization",
		apiErrorServerBusy: "apiErrorServerBusy", apiErrorServerDraining: "apiErrorServerDraining",
		apiErrorUnauthorized: "apiErrorUnauthorized", apiErrorWriteCapacity: "apiErrorWriteCapacity",
	}
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	uses := make(map[string]int, len(identifiers))
	for _, file := range packages["api"].Files {
		if set.Position(file.Pos()).Filename == "error_contract.go" {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if identifier, ok := node.(*ast.Ident); ok {
				uses[identifier.Name]++
			}
			return true
		})
	}
	for key := range apiErrorRegistry {
		identifier, declared := identifiers[key]
		if !declared || uses[identifier] == 0 {
			t.Errorf("registry key %q is unreachable from production source", key)
		}
	}
}

func TestAPIErrorContractFixtureMatchesProducerExactBytes(t *testing.T) {
	got, err := marshalAPIErrorContractFixture()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "testdata", "api-error-contract.json")
	if os.Getenv("UPDATE_API_ERROR_CONTRACT") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("committed API error contract is stale (-want +got)\nwant: %s\n got: %s", want, got)
	}
}

func TestAPIErrorWireCorpusIsProducerDerivedExactAndClosed(t *testing.T) {
	rows := apiErrorContractRows()
	if len(rows) != 16 || len(apiErrorRegistry) != 16 {
		t.Fatalf("valid wire rows=%d registry=%d", len(rows), len(apiErrorRegistry))
	}
	for _, row := range rows {
		if row.Body != string(marshalAPIError(row.Key)) {
			t.Fatalf("row %s body is not exact producer bytes", row.Key)
		}
		if !strings.HasSuffix(row.Body, "\n") || len(row.Body) > apiErrorMaxBodyUTF16Units {
			t.Fatalf("row %s has noncanonical or oversized body", row.Key)
		}
	}
	mutations := apiErrorStructuralMutations()
	const fixedMutations = 35
	const trailingMutations = 2
	wantMutations := fixedMutations + 3*len(rows) + trailingMutations
	if len(mutations) != wantMutations || len(mutations) != 85 {
		t.Fatalf("structural mutations=%d want fixed(%d)+3*rows(%d)+trailing(%d)=%d", len(mutations), fixedMutations, len(rows), trailingMutations, wantMutations)
	}
	seen := make(map[string]bool, len(mutations))
	for _, mutation := range mutations {
		if mutation.Name == "" || seen[mutation.Name] || mutation.Valid {
			t.Fatalf("invalid mutation metadata: %+v", mutation)
		}
		seen[mutation.Name] = true
	}
	last := mutations[len(mutations)-2]
	if last.Name != "body_units_limit_plus_one" || len(last.Body) != apiErrorMaxBodyUTF16Units+1 {
		t.Fatalf("body cap mutation units=%d name=%q", len(last.Body), last.Name)
	}
}

func TestAPIErrorContractWriterUsesOnlyRegistryAndCanonicalRetryAfter(t *testing.T) {
	canaries := []string{"SECRET-TARGET.example", "203.0.113.77", "https://token@example.invalid", "PROVIDER_ERROR_CANARY", "PANIC_CANARY"}
	for key, definition := range apiErrorRegistry {
		recorder := httptest.NewRecorder()
		recorder.Header().Add("Retry-After", "7")
		recorder.Header().Add("Retry-After", "8")
		writer := &responseWriter{raw: recorder}
		writer.writeAPIError(key, apiErrorMetadata{RetryAfter: 24 * time.Hour})
		if recorder.Code != definition.Status || recorder.Body.String() != string(marshalAPIError(key)) {
			t.Errorf("key=%s status=%d body=%q", key, recorder.Code, recorder.Body.String())
		}
		values := recorder.Header().Values("Retry-After")
		if definition.RetryAfter {
			if !reflect.DeepEqual(values, []string{"3600"}) {
				t.Errorf("key=%s Retry-After=%q", key, values)
			}
		} else if len(values) != 0 {
			t.Errorf("disallowed key=%s emitted Retry-After=%q", key, values)
		}
		for _, canary := range canaries {
			if strings.Contains(recorder.Body.String(), canary) {
				t.Errorf("key=%s reflected canary %q", key, canary)
			}
		}
	}
}

func TestAPIErrorContractCanonicalRetryAfterClampsInternalDuration(t *testing.T) {
	for _, test := range []struct {
		duration time.Duration
		want     string
	}{
		{-time.Second, "1"}, {0, "1"}, {time.Nanosecond, "1"}, {time.Second, "1"},
		{1500 * time.Millisecond, "2"}, {3600 * time.Second, "3600"}, {24 * time.Hour, "3600"},
	} {
		if got := canonicalRetryAfter(test.duration); got != test.want {
			t.Errorf("canonicalRetryAfter(%s)=%q want %q", test.duration, got, test.want)
		}
	}
}

func TestAPIErrorContractRetryAfterCorpusExpectedSemantics(t *testing.T) {
	want := []retryAfterContractCase{
		{Name: "absent", Header: nil, Valid: false, Seconds: nil},
		{Name: "minimum", Header: stringPointer("1"), Valid: true, Seconds: intPointer(1)},
		{Name: "maximum", Header: stringPointer("3600"), Valid: true, Seconds: intPointer(3600)},
		{Name: "zero", Header: stringPointer("0"), Valid: false, Seconds: nil},
		{Name: "above_maximum", Header: stringPointer("3601"), Valid: false, Seconds: nil},
		{Name: "leading_zero", Header: stringPointer("01"), Valid: false, Seconds: nil},
		{Name: "positive_sign", Header: stringPointer("+1"), Valid: false, Seconds: nil},
		{Name: "negative_sign", Header: stringPointer("-1"), Valid: false, Seconds: nil},
		{Name: "leading_space", Header: stringPointer(" 1"), Valid: false, Seconds: nil},
		{Name: "trailing_space", Header: stringPointer("1 "), Valid: false, Seconds: nil},
		{Name: "http_date", Header: stringPointer("Thu, 03 Sep 2026 00:00:01 GMT"), Valid: false, Seconds: nil},
		{Name: "overflow", Header: stringPointer("999999999999999999999999999999"), Valid: false, Seconds: nil},
		{Name: "comma_duplicate", Header: stringPointer("1, 2"), Valid: false, Seconds: nil},
	}
	if got := retryAfterContractCases(); !reflect.DeepEqual(got, want) {
		t.Fatalf("retry-after corpus\n got: %#v\nwant: %#v", got, want)
	}
}
