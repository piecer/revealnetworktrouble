package api

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var allowedResponderCalls = map[string]bool{
	"bindObservation":        true,
	"setSecurityHeaders":     true,
	"setCORSHeaders":         true,
	"limitRequestBody":       true,
	"setReportWriteDeadline": true,
	"writeHealth":            true,
	"writeChecks":            true,
	"writeReport":            true,
	"writeNoContent":         true,
	"writeAPIError":          true,
	"responseState":          true,
}

func importedPackage(file *ast.File, localName string) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == localName {
			return filepath.Base(path)
		}
	}
	return ""
}

func selectorPackage(file *ast.File, selector *ast.SelectorExpr) string {
	identifier, _ := selector.X.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return importedPackage(file, identifier.Name)
}

func positiveResponseCapabilityViolations(set *token.FileSet, file *ast.File) []string {
	var violations []string
	position := func(node ast.Node, message string) {
		violations = append(violations, fmt.Sprintf("%s: %s", set.Position(node.Pos()), message))
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SelectorExpr:
			pkg := selectorPackage(file, value)
			switch {
			case pkg == "http" && value.Sel.Name == "ResponseWriter":
				position(value, "raw http.ResponseWriter type is outside the response boundary")
			case pkg == "http" && (value.Sel.Name == "Error" || value.Sel.Name == "HandlerFunc" || value.Sel.Name == "NewServeMux"):
				position(value, "net/http response or route adapter bypass")
			case pkg == "json" && value.Sel.Name == "NewEncoder":
				position(value, "encoder-to-writer response bypass")
			case pkg == "fmt" && strings.HasPrefix(value.Sel.Name, "Fprint"):
				position(value, "fmt writer response bypass")
			case pkg == "io" && (value.Sel.Name == "WriteString" || value.Sel.Name == "Writer" || value.Sel.Name == "StringWriter"):
				position(value, "io writer response bypass")
			case value.Sel.Name == "Write" || value.Sel.Name == "WriteHeader" || value.Sel.Name == "WriteString" || value.Sel.Name == "WriteTo":
				position(value, "generic response write primitive is outside the boundary")
			case value.Sel.Name == "raw" || value.Sel.Name == "ResponseWriter" || value.Sel.Name == "Unwrap":
				position(value, "raw response writer field access is outside the boundary")
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			genericEscape := map[string]bool{"writePayload": true, "writeJSONPayload": true, "writeJSON": true, "writeError": true, "writeEmpty": true, "writeTypedOK": true}
			if ok && genericEscape[selector.Sel.Name] && !allowedResponderCalls[selector.Sel.Name] {
				position(value, "generic body/status response escape is not a capability")
			}
		case *ast.FuncDecl:
			if value.Name.Name == "ServeHTTP" || value.Name.Name == "Handle" || value.Name.Name == "HandleFunc" {
				position(value, "additional route adapter is forbidden")
			}
		}
		return true
	})
	return violations
}

func TestProductionUsesPositiveResponseCapabilityBoundary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || base == "response_writer.go" {
			continue
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, violation := range positiveResponseCapabilityViolations(set, file) {
			t.Error(violation)
		}
	}
}

type operationalBoundaryInspection struct {
	violations    []string
	rawTypes      int
	businessCalls int
}

func inspectOperationalBoundary(set *token.FileSet, file *ast.File) operationalBoundaryInspection {
	inspection := operationalBoundaryInspection{}
	position := func(node ast.Node, message string) {
		inspection.violations = append(inspection.violations, fmt.Sprintf("%s: %s", set.Position(node.Pos()), message))
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		functionName := function.Name.Name
		ast.Inspect(function, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				pkg := selectorPackage(file, value)
				if pkg == "http" && value.Sel.Name == "ResponseWriter" {
					inspection.rawTypes++
				}
				if pkg == "http" && value.Sel.Name == "Error" {
					position(value, "operational wrapper may not use http.Error")
				}
				if functionName != "writeOperationalJSON" && (value.Sel.Name == "Write" || value.Sel.Name == "WriteHeader" || value.Sel.Name == "WriteString" || value.Sel.Name == "WriteTo") {
					position(value, "raw response writes are restricted to fixed operational serialization")
				}
				if functionName == "ServeHTTP" && value.Sel.Name == "state" {
					position(value, "outer wrapper may not make business admission decisions")
				}
			case *ast.CallExpr:
				switch call := value.Fun.(type) {
				case *ast.Ident:
					if call.Name == "writeOperationalJSON" && functionName != "serveOperational" {
						position(value, "fixed operational writer is callable only from operational routes")
					}
				case *ast.SelectorExpr:
					if functionName == "ServeHTTP" && call.Sel.Name == "ServeHTTP" {
						if business, ok := call.X.(*ast.SelectorExpr); ok && business.Sel.Name == "business" {
							inspection.businessCalls++
						}
					}
				}
			}
			return true
		})
	}
	return inspection
}

func TestWholeProcessResponseArchitectureIncludesOperationalWrapper(t *testing.T) {
	directory := filepath.Join("..", "..", "cmd", "checknetwork-api")
	path := filepath.Join(directory, "operational.go")
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	inspection := inspectOperationalBoundary(set, file)
	for _, violation := range inspection.violations {
		t.Error(violation)
	}
	if inspection.rawTypes != 3 {
		t.Errorf("operational raw ResponseWriter occurrences=%d, want only wrapper, route, and fixed writer", inspection.rawTypes)
	}
	if inspection.businessCalls != 1 {
		t.Errorf("operational business pass-through calls=%d, want exactly one", inspection.businessCalls)
	}
	paths, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, productionPath := range paths {
		if productionPath == path || strings.HasSuffix(productionPath, "_test.go") {
			continue
		}
		productionSet := token.NewFileSet()
		productionFile, parseErr := parser.ParseFile(productionSet, productionPath, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, violation := range positiveResponseCapabilityViolations(productionSet, productionFile) {
			t.Errorf("cmd response bypass: %s", violation)
		}
	}
}

func TestOperationalWrapperArchitectureMutationCorpusRejectsBusinessWrites(t *testing.T) {
	mutations := map[string]string{
		"draining state interception": `package main; import "net/http"; type operationalHandler struct{ state struct{ phase int }; business http.Handler }; func (handler *operationalHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) { if handler.state.phase == 2 { writeOperationalJSON(writer, request.Method, 503, "draining"); return }; handler.business.ServeHTTP(writer, request) }`,
		"direct business write":       `package main; import "net/http"; type operationalHandler struct{ business http.Handler }; func (handler *operationalHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(503) }`,
		"http error":                  `package main; import "net/http"; type operationalHandler struct{ business http.Handler }; func (handler *operationalHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) { http.Error(writer, "draining", 503) }`,
	}
	for name, source := range mutations {
		t.Run(name, func(t *testing.T) {
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, "operational_mutation.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if inspection := inspectOperationalBoundary(set, file); len(inspection.violations) == 0 {
				t.Fatal("business response bypass escaped whole-process architecture gate")
			}
		})
	}
}

func TestResponseBoundaryExposesOnlyFixedCapabilities(t *testing.T) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "response_writer.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var interfaceMethods, writerMethods []string
	var rawTypes, adapters int
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.TypeSpec:
			if value.Name.Name != "responder" {
				return true
			}
			contract, ok := value.Type.(*ast.InterfaceType)
			if !ok {
				t.Fatal("responder is not an interface capability")
			}
			for _, field := range contract.Methods.List {
				for _, name := range field.Names {
					interfaceMethods = append(interfaceMethods, name.Name)
				}
			}
		case *ast.SelectorExpr:
			if selectorPackage(file, value) == "http" && value.Sel.Name == "ResponseWriter" {
				rawTypes++
			}
		case *ast.FuncDecl:
			if value.Name.Name == "ServeHTTP" {
				adapters++
			}
			if value.Recv != nil && len(value.Recv.List) == 1 {
				receiver := value.Recv.List[0].Type
				if pointer, ok := receiver.(*ast.StarExpr); ok {
					receiver = pointer.X
				}
				if identifier, ok := receiver.(*ast.Ident); ok && identifier.Name == "responseWriter" {
					writerMethods = append(writerMethods, value.Name.Name)
				}
			}
		case *ast.CompositeLit:
			if identifier, ok := value.Type.(*ast.Ident); ok && (identifier.Name == "apiErrorResponse" || identifier.Name == "apiErrorBody") {
				t.Errorf("%s: boundary constructs a free-form error DTO", set.Position(value.Pos()))
			}
		}
		return true
	})
	if rawTypes != 3 {
		t.Fatalf("raw ResponseWriter occurrences=%d want exactly field, Unwrap, and sole adapter", rawTypes)
	}
	if adapters != 1 {
		t.Fatalf("ServeHTTP adapters=%d want 1", adapters)
	}
	if len(interfaceMethods) != len(allowedResponderCalls) {
		t.Fatalf("responder methods=%v", interfaceMethods)
	}
	for _, method := range interfaceMethods {
		if !allowedResponderCalls[method] {
			t.Errorf("generic/unapproved responder method %q", method)
		}
	}
	wantWriterMethods := []string{
		"Header", "Unwrap", "Write", "WriteHeader", "bindObservation", "limitRequestBody",
		"responseState", "setCORSHeaders", "setReportWriteDeadline", "setSecurityHeaders",
		"writeAPIError", "writeChecks", "writeEmpty", "writeHealth", "writeJSONPayload",
		"writeNoContent", "writePayload", "writeReport", "writeTypedOK",
	}
	sort.Strings(writerMethods)
	if strings.Join(writerMethods, ",") != strings.Join(wantWriterMethods, ",") {
		t.Fatalf("response boundary methods=%v want exact allowlist %v", writerMethods, wantWriterMethods)
	}
	contents, err := os.ReadFile("response_writer.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("http.Error("), []byte("json.NewEncoder("), []byte("fmt.Fprint"), []byte("io.WriteString(")} {
		if bytes.Contains(contents, forbidden) {
			t.Errorf("boundary contains alternate writer primitive %q", forbidden)
		}
	}
}

func TestResponseCapabilityMutationCorpusRejectsReviewerBypasses(t *testing.T) {
	mutations := map[string]string{
		"http.Error":                `package api; import "net/http"; func bypass(w http.ResponseWriter) { http.Error(w, "oops", 500) }`,
		"json encoder":              `package api; import ("encoding/json"; "net/http"); func bypass(w http.ResponseWriter) { _ = json.NewEncoder(w).Encode(map[string]string{"error":"oops"}) }`,
		"method value Write":        `package api; import "net/http"; func bypass(w http.ResponseWriter) { write := w.Write; _, _ = write([]byte("oops")) }`,
		"io.StringWriter assertion": `package api; import ("io"; "net/http"); func bypass(w http.ResponseWriter) { if sink, ok := w.(io.StringWriter); ok { _, _ = sink.WriteString("oops") } }`,
		"bytes.Buffer.WriteTo":      `package api; import ("bytes"; "net/http"); func bypass(w http.ResponseWriter) { var body bytes.Buffer; _, _ = body.WriteTo(w) }`,
		"indirect io.Writer":        `package api; import ("fmt"; "io"); func emit(dst io.Writer) { _, _ = fmt.Fprint(dst, "oops") }`,
		"struct field indirection":  `package api; import "net/http"; type bypass struct { writer http.ResponseWriter }`,
		"generic capability escape": `package api; func bypass(r responder, status int, body []byte) { r.writePayload(status, body) }`,
		"alternate route adapter":   `package api; import "net/http"; var bypass = http.HandlerFunc(func(http.ResponseWriter, *http.Request){})`,
	}
	for name, source := range mutations {
		t.Run(name, func(t *testing.T) {
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, "mutation.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if violations := positiveResponseCapabilityViolations(set, file); len(violations) == 0 {
				t.Fatal("reviewer bypass escaped the positive capability guard")
			}
		})
	}
}

type panicProbeResponder struct {
	panicBefore bool
	panicAfter  bool
	attempted   bool
	committed   bool
	status      int
	errorCalls  int
}

func (*panicProbeResponder) bindObservation(*requestObservation) {}
func (probe *panicProbeResponder) setSecurityHeaders() {
	if probe.panicBefore {
		panic("BEFORE_CANARY")
	}
}
func (*panicProbeResponder) setCORSHeaders(string)                 {}
func (*panicProbeResponder) limitRequestBody(*http.Request, int64) {}
func (*panicProbeResponder) setReportWriteDeadline()               {}
func (probe *panicProbeResponder) writeHealth(HealthResponse) {
	probe.attempted, probe.committed, probe.status = true, true, http.StatusOK
	if probe.panicAfter {
		panic("AFTER_CANARY")
	}
}
func (*panicProbeResponder) writeChecks(checksResponse) {}
func (*panicProbeResponder) writeReport(reportJSON)     {}
func (*panicProbeResponder) writeNoContent()            {}
func (probe *panicProbeResponder) writeAPIError(key apiErrorKey, _ apiErrorMetadata) {
	probe.errorCalls++
	probe.attempted, probe.committed, probe.status = true, true, apiErrorDefinitionFor(key).Status
}
func (probe *panicProbeResponder) responseState() (bool, bool, int) {
	return probe.attempted, probe.committed, probe.status
}

func TestMiddlewareContainsPanicsWithoutAppendingAfterResponseAttempt(t *testing.T) {
	for _, test := range []struct {
		name       string
		probe      *panicProbeResponder
		wantStatus int
		wantErrors int
	}{
		{name: "before attempt", probe: &panicProbeResponder{panicBefore: true}, wantStatus: http.StatusInternalServerError, wantErrors: 1},
		{name: "after attempt", probe: &panicProbeResponder{panicAfter: true}, wantStatus: http.StatusOK, wantErrors: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			server := &Server{
				logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
				version:   "test",
				admission: make(chan struct{}, 1),
			}
			server.serveHTTP(test.probe, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
			if test.probe.status != test.wantStatus || test.probe.errorCalls != test.wantErrors {
				t.Fatalf("status=%d errors=%d", test.probe.status, test.probe.errorCalls)
			}
			records := decodeTelemetryLines(t, logs.Bytes())
			if len(records) != 1 || records[0]["event"] != string(TelemetryEventHTTPTerminal) ||
				records[0]["status"] != float64(test.wantStatus) || records[0]["outcome"] != string(TelemetryOutcomePanicSafeFailure) {
				t.Fatalf("terminal records=%v", records)
			}
			if strings.Contains(logs.String(), "CANARY") {
				t.Fatalf("panic value leaked: %s", logs.String())
			}
		})
	}
}
