package diagnostic

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"
)

// ReportSchemaV1 is the machine-readable wire structure derived from the model
// JSON tags and the result shape registry. It is the single source ARC-2 asks
// for: consumers read the same key sets the producer is allowed to write.
//
// The schema expresses structure only. Relationships a JSON Schema cannot state
// (canonical spelling, block-internal key sets, threshold semantics) stay owned
// by the producer fixtures and the mutation corpus, and the schema says so in
// its own notes rather than pretending otherwise.
const ReportSchemaV1 = "report-schema-v1"

// canonicalUTCFormatPattern is the format the producer writes; the schema keeps
// consumers from accepting a value the producer would not have produced.
const canonicalUTCFormatPattern = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$`

var enumValuesByTypeName = map[string][]string{
	"Kind":               kindsToStrings(allKinds()),
	"Status":             {string(StatusHealthy), string(StatusDegraded), string(StatusUnreachable)},
	"Verdict":            {string(VerdictHealthy), string(VerdictAttention), string(VerdictInconclusive)},
	"FindingCode":        findingCodeValues(),
	"FindingSeverity":    {string(SeverityCritical), string(SeverityWarning), string(SeverityInfo)},
	"FindingCategory":    {string(CategoryNameResolution), string(CategoryConnectivity), string(CategoryApplication), string(CategorySecurity), string(CategoryRouting), string(CategoryExecution), string(CategoryInput)},
	"Confidence":         {string(ConfidenceDirect), string(ConfidenceCorroborated), string(ConfidenceLimited)},
	"EvidenceProvenance": {string(ProvenanceResult), string(ProvenanceDetails)},
	"CoverageIssueCode":  {string(CoverageMissingDetails), string(CoverageMalformedDetails), string(CoverageUnsupportedDetails), string(CoverageConfigurationDisabled)},
	"EnrichmentSource":   {string(EnrichmentSourceUpstream), string(EnrichmentSourceCache), string(EnrichmentSourceMixed), string(EnrichmentSourceNone), string(EnrichmentSourceDisabled)},
	"TopologyMode":       {string(TopologyModeFull), string(TopologyModeCompact)},
}

// detailTypeNodes maps the registered detail types to the JSON constructs they
// mean. The stage timing block is derived from the phase constants the observer
// can write, so the schema and the producer cannot disagree about a phase key.
func detailTypeNodes() map[string]any {
	phaseProperties := map[string]any{}
	for _, phase := range []string{ResultDetailDNSPhaseMS, ResultDetailConnectPhaseMS, ResultDetailTLSPhaseMS, ResultDetailTTFBPhaseMS} {
		phaseProperties[phase] = map[string]any{"type": "integer", "minimum": 0}
	}
	return map[string]any{
		DetailTypeString:                  map[string]any{"type": "string"},
		DetailTypeNonEmptyString:          map[string]any{"type": "string", "minLength": 1},
		DetailTypeCanonicalUTCRFC3339:     map[string]any{"type": "string", "pattern": canonicalUTCFormatPattern},
		DetailTypeUTCTimestamp:            map[string]any{"type": "string", "pattern": canonicalUTCFormatPattern},
		DetailTypeNonNegativeInteger:      map[string]any{"type": "integer", "minimum": 0},
		DetailTypeStringArray:             map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		DetailTypeTraceAttempts:           map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		DetailTypeTopology:                map[string]any{"type": "object"},
		DetailTypeEnrichment:              map[string]any{"type": "object"},
		DetailTypeStageTimingBlock:        map[string]any{"type": "object", "additionalProperties": false, "properties": phaseProperties},
		DetailTypeSampleStatsBlock:        map[string]any{"type": "object"},
		DetailTypeResolverComparisonBlock: map[string]any{"type": "object"},
	}
}

// tracerouteGenericShapeCanCarryAggregate reports the one generic outcome where an
// attempt could actually have run for a traceroute result. The executed outcomes
// have their own traceroute shapes; invalid_address and connection_failed are
// recorded before any attempt ran, and the unobserved codes are recorded before
// any observation, so no aggregate could exist for them. A cancellation is the
// generic case where an attempt ran and was still recorded as a runner outcome —
// that is the producer-witnessed case the schema must not reject. Registering the
// aggregate for a code where no attempt could run would let a consumer accept a
// block the producer can never write.
func tracerouteGenericShapeCanCarryAggregate(code string) bool {
	return code == "cancelled"
}

func MarshalReportSchema() ([]byte, error) {
	types := map[string]any{}
	buildTypeNode(reflect.TypeOf(Report{}), types)
	resultNode, ok := types["result"].(map[string]any)
	if !ok {
		return nil, nil
	}
	resultNode["oneOf"] = []any{}
	for _, shape := range producerResultShapeRegistry {
		kindNode := map[string]any{"type": "string", "const": string(shape.Kinds[0])}
		if len(shape.Kinds) > 1 {
			// The producer may write any kind the shape registers, so the schema
			// must not pin consumers to the first entry.
			kindNode = map[string]any{"type": "string", "enum": kindsToStrings(shape.Kinds)}
		}
		branch := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"kind":       kindNode,
				"status":     map[string]any{"type": "string", "const": string(shape.Status)},
				"address":    map[string]any{"type": "string"},
				"latency_ms": map[string]any{"type": "integer", "minimum": 0},
				"started_at": map[string]any{"type": "string", "pattern": canonicalUTCFormatPattern},
				"message":    map[string]any{"type": "string"},
			},
			"shape": shape.Name,
		}
		required := []string{"kind", "status", "address", "latency_ms", "started_at"}
		detailNodes := detailTypeNodes()
		detailProperties := map[string]any{}
		for _, detail := range shape.RequiredDetails {
			detailProperties[detail.Key] = detailSchemaNode(detail, detailNodes)
		}
		for _, detail := range shape.OptionalDetails {
			detailProperties[detail.Key] = detailSchemaNode(detail, detailNodes)
		}
		// The attempt aggregate is the traceroute kind's own block, not a closed
		// aggregate block, so it is never in OptionalDetails. A generic shape may
		// only claim it for the outcome where an attempt could actually have run.
		if ContainsKind(shape.Kinds, KindTraceroute) && tracerouteGenericShapeCanCarryAggregate(shape.ErrorCode) {
			for _, key := range traceAggregateKeys {
				detailProperties[key] = detailSchemaNode(ResultDetailContract{Key: key, Type: traceAggregateType(key)}, detailNodes)
			}
		}
		if len(detailProperties) > 0 {
			branch["properties"].(map[string]any)["details"] = map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             sortedDetailKeys(shape.RequiredDetails),
				"properties":           detailProperties,
			}
		}
		if shape.ErrorCode != "" {
			branch["properties"].(map[string]any)["error_code"] = map[string]any{"type": "string", "const": shape.ErrorCode}
			required = append(required, "error_code")
		}
		branch["required"] = required
		types["result"].(map[string]any)["oneOf"] = append(types["result"].(map[string]any)["oneOf"].([]any), branch)
	}

	document := map[string]any{
		"schema_version": ReportSchemaV1,
		"generated_from": []any{"model JSON tags", "result shape registry"},
		"notes": []string{
			"structure only: canonical spelling, thresholds and block-internal key sets remain owned by the producer fixtures and the mutation corpus",
			"detail blocks whose key sets are not registered in the contract are expressed as objects, not as guessed key lists",
			"a shape never lists a detail key its producer could not have written",
		},
		"types": types,
	}
	return json.Marshal(document)
}

// buildTypeNode derives a JSON Schema node from the struct's JSON tags. The
// tag is the single source: a field that becomes omitempty becomes optional in
// the schema without anyone editing a list by hand.
func buildTypeNode(t reflect.Type, types map[string]any) any {
	if t.Kind() != reflect.Struct {
		return nil
	}
	name := strings.ToLower(t.Name())
	if existing, ok := types[name]; ok {
		return existing
	}
	node := map[string]any{"type": "object", "additionalProperties": false}
	types[name] = node
	properties := map[string]any{}
	var required []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		key, opts, _ := strings.Cut(tag, ",")
		properties[key] = nodeForField(field.Type, types)
		if opts == "" {
			required = append(required, key)
		}
	}
	node["properties"] = properties
	sort.Strings(required)
	node["required"] = required
	return node
}

func nodeForField(t reflect.Type, types map[string]any) any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if enum, ok := enumValuesByTypeName[t.Name()]; ok {
		return map[string]any{"type": "string", "enum": enum}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer", "minimum": 0}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": nodeForField(t.Elem(), types)}
	case reflect.Struct:
		if t == reflect.TypeOf(time.Time{}) {
			return map[string]any{"type": "string", "pattern": canonicalUTCFormatPattern}
		}
		// One node per type: an inline copy would make the committed schema hold
		// two versions of the same contract, and a consumer editing one would not
		// change the other.
		buildTypeNode(t, types)
		return map[string]any{"$ref": "#/types/" + strings.ToLower(t.Name())}
	default:
		return map[string]any{"type": "object"}
	}
}

func detailSchemaNode(detail ResultDetailContract, detailNodes map[string]any) map[string]any {
	if detail.Const != "" {
		return map[string]any{"type": "string", "const": detail.Const}
	}
	if node, ok := detailNodes[string(detail.Type)].(map[string]any); ok {
		return node
	}
	return map[string]any{"type": "object"}
}

func sortedDetailKeys(details []ResultDetailContract) []string {
	keys := make([]string, 0, len(details))
	for _, detail := range details {
		keys = append(keys, detail.Key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func findingCodeValues() []string {
	codes := make([]string, 0, len(findingRegistry))
	for _, entry := range findingRegistry {
		codes = append(codes, string(entry.Code))
	}
	return codes
}

func kindsToStrings(kinds []Kind) []string {
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, string(kind))
	}
	return out
}
