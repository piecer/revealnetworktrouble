package diagnostic

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const PresentationContractSchemaV1 = "presentation-contract-v1"

const (
	PresentationPurposeFinding = "finding"
	PresentationPurposeControl = "control"

	EvidenceShapeNone                   = "none"
	EvidenceShapeClosedErrorCode        = "closed_error_code"
	EvidenceShapeHTTPStatus             = "http_status"
	EvidenceShapeUTCTimestamp           = "utc_timestamp"
	EvidenceShapeCount                  = "count"
	EvidenceShapeExecutionFailureCounts = "execution_failure_counts"
	EvidenceShapeCompletedFraction      = "completed_fraction"
	EvidenceShapePathCount              = "path_count"
)

// PresentationEvidenceSignal defines the safe typed projection that consumers
// may apply to one evidence signal. The producer prose itself is not a client
// presentation contract.
type PresentationEvidenceSignal struct {
	Signal        string `json:"signal"`
	ObservedShape string `json:"observed_shape"`
	ExpectedShape string `json:"expected_shape"`
}

type PresentationActionRelationship struct {
	FindingPresentationKey string `json:"finding_presentation_key"`
	Key                    string `json:"key"`
}

type PresentationScenarioFinding struct {
	Code               FindingCode                  `json:"code"`
	PresentationKey    string                       `json:"presentation_key"`
	Evidence           []PresentationEvidenceSignal `json:"evidence"`
	ActionRelationship string                       `json:"action_relationship"`
	CoverageSignals    []string                     `json:"coverage_signals"`
}

type PresentationScenario struct {
	Name     string                        `json:"name"`
	Purpose  string                        `json:"purpose"`
	Findings []PresentationScenarioFinding `json:"findings"`
	Report   Report                        `json:"report"`
}

type PresentationContract struct {
	Schema              string                           `json:"schema"`
	SemanticKeys        []string                         `json:"semantic_keys"`
	EvidenceSignals     []PresentationEvidenceSignal     `json:"evidence_signals"`
	CoverageSignals     []string                         `json:"coverage_signals"`
	ActionRelationships []PresentationActionRelationship `json:"action_relationships"`
	Scenarios           []PresentationScenario           `json:"scenarios"`
}

// PresentationScenarioSource is the external fixture boundary. Results may be
// injected by checker recipes, but the supplied report and its Analysis must be
// the real producer structures. PrimaryFinding is empty only for control cases.
type PresentationScenarioSource struct {
	Name           string
	PrimaryFinding FindingCode
	Report         Report
}

var presentationSemanticKeys = []string{
	"cause",
	"supporting_evidence",
	"expectation",
	"evidence_directness",
	"coverage_limitation",
	"next_action",
}

var presentationEvidenceSignalRegistry = []PresentationEvidenceSignal{
	{Signal: "error_code", ObservedShape: EvidenceShapeClosedErrorCode, ExpectedShape: EvidenceShapeNone},
	{Signal: "http.status_code", ObservedShape: EvidenceShapeHTTPStatus, ExpectedShape: EvidenceShapeHTTPStatus},
	{Signal: "tls.certificate_expires_at", ObservedShape: EvidenceShapeUTCTimestamp, ExpectedShape: EvidenceShapeNone},
	{Signal: "traceroute.attempts_cancelled", ObservedShape: EvidenceShapeCount, ExpectedShape: EvidenceShapeCount},
	{Signal: "traceroute.attempts_execution_failed", ObservedShape: EvidenceShapeCount, ExpectedShape: EvidenceShapeCount},
	{Signal: "traceroute.attempts_reached", ObservedShape: EvidenceShapeCompletedFraction, ExpectedShape: EvidenceShapeCompletedFraction},
	{Signal: "traceroute.attempts_timed_out", ObservedShape: EvidenceShapeCount, ExpectedShape: EvidenceShapeCount},
	{Signal: "traceroute.execution_failures", ObservedShape: EvidenceShapeExecutionFailureCounts, ExpectedShape: EvidenceShapeCount},
	{Signal: "traceroute.path_signatures", ObservedShape: EvidenceShapePathCount, ExpectedShape: EvidenceShapeNone},
	{Signal: "traceroute.path_status", ObservedShape: EvidenceShapePathCount, ExpectedShape: EvidenceShapeNone},
}

// These are the exact CoverageIssue.Signal values Analyze can emit. Available
// and missing paths remain in each production Report and are not flattened into
// this issue-signal registry.
var presentationCoverageSignalRegistry = []string{
	"certificate_expires_at",
	"details",
	"dns_answers",
	"endpoint",
	"error_code",
	"geoip",
	"geoip_enrichment",
	"http_status",
	"kind",
	"response_body",
	"result",
	"service_verification_details",
	"service_verification_scope",
	"status",
	"tls",
	"tls_certificate",
	"tls_failure_details",
	"trace_attempts",
	"trace_error",
	"trace_paths",
	"trace_topology",
}

func actionRelationshipKey(code FindingCode) string {
	return "action." + string(code)
}

func evidenceSignalContract(signal string) (PresentationEvidenceSignal, bool) {
	index := sort.Search(len(presentationEvidenceSignalRegistry), func(index int) bool {
		return presentationEvidenceSignalRegistry[index].Signal >= signal
	})
	if index == len(presentationEvidenceSignalRegistry) || presentationEvidenceSignalRegistry[index].Signal != signal {
		return PresentationEvidenceSignal{}, false
	}
	return presentationEvidenceSignalRegistry[index], true
}

// BuildPresentationContract projects producer reports only after checking their
// finding/evidence/action references. Coverage probes are analyzed here so the
// emitted coverage registry is demonstrated by Analyze rather than copied into
// the fixture as an unexercised list.
func BuildPresentationContract(sources []PresentationScenarioSource, coverageProbes []Result, now time.Time) (PresentationContract, error) {
	findingContract := ProducerFindingContract()
	presentationByCode := make(map[FindingCode]string, len(findingContract.Findings))
	for _, finding := range findingContract.Findings {
		presentationByCode[finding.Code] = finding.PresentationKey
	}

	contract := PresentationContract{
		Schema:              PresentationContractSchemaV1,
		SemanticKeys:        append([]string(nil), presentationSemanticKeys...),
		EvidenceSignals:     append([]PresentationEvidenceSignal(nil), presentationEvidenceSignalRegistry...),
		CoverageSignals:     []string{},
		ActionRelationships: make([]PresentationActionRelationship, 0, len(findingContract.Findings)),
		Scenarios:           make([]PresentationScenario, 0, len(sources)),
	}
	for _, finding := range findingContract.Findings {
		contract.ActionRelationships = append(contract.ActionRelationships, PresentationActionRelationship{
			FindingPresentationKey: finding.PresentationKey,
			Key:                    actionRelationshipKey(finding.Code),
		})
	}

	observedCoverage := make(map[string]struct{}, len(presentationCoverageSignalRegistry))
	for _, probe := range coverageProbes {
		analysis := Analyze([]Result{probe}, now.UTC())
		for _, issue := range append(append([]CoverageIssue(nil), analysis.Coverage.ProviderFailures...), analysis.Coverage.Limitations...) {
			if issue.Signal != "" {
				observedCoverage[issue.Signal] = struct{}{}
			}
		}
	}
	for signal := range observedCoverage {
		contract.CoverageSignals = append(contract.CoverageSignals, signal)
	}
	sort.Strings(contract.CoverageSignals)
	if !equalStrings(contract.CoverageSignals, presentationCoverageSignalRegistry) {
		return PresentationContract{}, fmt.Errorf("coverage probe set = %v, want exact producer registry %v", contract.CoverageSignals, presentationCoverageSignalRegistry)
	}

	primaryCounts := make(map[FindingCode]int, len(findingContract.Findings))
	names := make(map[string]struct{}, len(sources))
	observedEvidence := make(map[string]struct{}, len(presentationEvidenceSignalRegistry))
	for _, source := range sources {
		if strings.TrimSpace(source.Name) == "" {
			return PresentationContract{}, fmt.Errorf("presentation scenario name is required")
		}
		if _, exists := names[source.Name]; exists {
			return PresentationContract{}, fmt.Errorf("duplicate presentation scenario %q", source.Name)
		}
		names[source.Name] = struct{}{}
		if source.Report.Analysis == nil {
			return PresentationContract{}, fmt.Errorf("scenario %q has no producer analysis", source.Name)
		}
		purpose := PresentationPurposeControl
		if source.PrimaryFinding != "" {
			if _, exists := presentationByCode[source.PrimaryFinding]; !exists {
				return PresentationContract{}, fmt.Errorf("scenario %q has unknown primary finding %q", source.Name, source.PrimaryFinding)
			}
			purpose = PresentationPurposeFinding
			primaryCounts[source.PrimaryFinding]++
		}

		scenario := PresentationScenario{
			Name: source.Name, Purpose: purpose,
			Findings: make([]PresentationScenarioFinding, 0, len(source.Report.Analysis.Findings)),
			Report:   source.Report,
		}
		evidenceByID := make(map[string]Evidence, len(source.Report.Analysis.Evidence))
		for _, evidence := range source.Report.Analysis.Evidence {
			evidenceByID[evidence.ID] = evidence
		}
		actionByID := make(map[string]Action, len(source.Report.Analysis.Actions))
		for _, action := range source.Report.Analysis.Actions {
			actionByID[action.ID] = action
		}
		primaryFound := source.PrimaryFinding == ""
		for _, finding := range source.Report.Analysis.Findings {
			presentationKey, exists := presentationByCode[finding.Code]
			if !exists {
				return PresentationContract{}, fmt.Errorf("scenario %q emitted unregistered finding %q", source.Name, finding.Code)
			}
			if finding.Code == source.PrimaryFinding {
				primaryFound = true
			}
			if len(finding.EvidenceIDs) == 0 || len(finding.ActionIDs) != 1 {
				return PresentationContract{}, fmt.Errorf("scenario %q finding %q has non-closed evidence/action cardinality", source.Name, finding.Code)
			}
			projected := PresentationScenarioFinding{
				Code: finding.Code, PresentationKey: presentationKey,
				Evidence:           make([]PresentationEvidenceSignal, 0, len(finding.EvidenceIDs)),
				ActionRelationship: actionRelationshipKey(finding.Code),
				CoverageSignals:    relevantCoverageSignals(source.Report.Analysis.Coverage, finding, evidenceByID),
			}
			for _, signal := range projected.CoverageSignals {
				if !registeredCoverageSignal(signal) {
					return PresentationContract{}, fmt.Errorf("scenario %q emitted unregistered coverage signal %q", source.Name, signal)
				}
			}
			seenSignals := make(map[string]struct{}, len(finding.EvidenceIDs))
			for _, evidenceID := range finding.EvidenceIDs {
				evidence, exists := evidenceByID[evidenceID]
				if !exists {
					return PresentationContract{}, fmt.Errorf("scenario %q finding %q references unknown evidence %q", source.Name, finding.Code, evidenceID)
				}
				shape, exists := evidenceSignalContract(evidence.Signal)
				if !exists {
					return PresentationContract{}, fmt.Errorf("scenario %q emitted unregistered evidence signal %q", source.Name, evidence.Signal)
				}
				if err := validateEvidenceShape(shape, evidence); err != nil {
					return PresentationContract{}, fmt.Errorf("scenario %q finding %q: %w", source.Name, finding.Code, err)
				}
				observedEvidence[evidence.Signal] = struct{}{}
				if _, duplicate := seenSignals[evidence.Signal]; !duplicate {
					projected.Evidence = append(projected.Evidence, shape)
					seenSignals[evidence.Signal] = struct{}{}
				}
			}
			for _, actionID := range finding.ActionIDs {
				if _, exists := actionByID[actionID]; !exists {
					return PresentationContract{}, fmt.Errorf("scenario %q finding %q references unknown action %q", source.Name, finding.Code, actionID)
				}
			}
			sort.Slice(projected.Evidence, func(left, right int) bool { return projected.Evidence[left].Signal < projected.Evidence[right].Signal })
			scenario.Findings = append(scenario.Findings, projected)
		}
		if !primaryFound {
			return PresentationContract{}, fmt.Errorf("scenario %q did not produce primary finding %q", source.Name, source.PrimaryFinding)
		}
		if purpose == PresentationPurposeFinding && len(scenario.Findings) != 1 {
			return PresentationContract{}, fmt.Errorf("finding scenario %q emitted %d findings, want exactly one", source.Name, len(scenario.Findings))
		}
		contract.Scenarios = append(contract.Scenarios, scenario)
	}

	for _, finding := range findingContract.Findings {
		if primaryCounts[finding.Code] != 1 {
			return PresentationContract{}, fmt.Errorf("primary scenario count for %q = %d, want 1", finding.Code, primaryCounts[finding.Code])
		}
	}
	observedEvidenceSignals := make([]string, 0, len(observedEvidence))
	for signal := range observedEvidence {
		observedEvidenceSignals = append(observedEvidenceSignals, signal)
	}
	sort.Strings(observedEvidenceSignals)
	registeredEvidenceSignals := make([]string, len(presentationEvidenceSignalRegistry))
	for index, signal := range presentationEvidenceSignalRegistry {
		registeredEvidenceSignals[index] = signal.Signal
	}
	if !equalStrings(observedEvidenceSignals, registeredEvidenceSignals) {
		return PresentationContract{}, fmt.Errorf("scenario evidence signals = %v, want exhaustive producer registry %v", observedEvidenceSignals, registeredEvidenceSignals)
	}

	sort.Slice(contract.ActionRelationships, func(left, right int) bool {
		return contract.ActionRelationships[left].FindingPresentationKey < contract.ActionRelationships[right].FindingPresentationKey
	})
	sort.Slice(contract.Scenarios, func(left, right int) bool { return contract.Scenarios[left].Name < contract.Scenarios[right].Name })
	return contract, nil
}

func relevantCoverageSignals(coverage Coverage, finding Finding, evidenceByID map[string]Evidence) []string {
	resultIndexes := make(map[int]struct{}, len(finding.EvidenceIDs))
	for _, evidenceID := range finding.EvidenceIDs {
		if evidence, exists := evidenceByID[evidenceID]; exists {
			resultIndexes[evidence.ResultIndex] = struct{}{}
		}
	}
	values := make(map[string]struct{})
	for _, issue := range append(append([]CoverageIssue(nil), coverage.ProviderFailures...), coverage.Limitations...) {
		if _, relevant := resultIndexes[issue.ResultIndex]; relevant && issue.Signal != "" {
			values[issue.Signal] = struct{}{}
		}
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func registeredCoverageSignal(signal string) bool {
	index := sort.SearchStrings(presentationCoverageSignalRegistry, signal)
	return index < len(presentationCoverageSignalRegistry) && presentationCoverageSignalRegistry[index] == signal
}

func validateEvidenceShape(shape PresentationEvidenceSignal, evidence Evidence) error {
	if !safeEvidenceValue(shape.ObservedShape, evidence.Observed) {
		return fmt.Errorf("evidence signal %q has unsafe observed value %q for shape %q", evidence.Signal, evidence.Observed, shape.ObservedShape)
	}
	if shape.ExpectedShape == EvidenceShapeNone {
		return nil
	}
	if !safeEvidenceValue(shape.ExpectedShape, evidence.Expected) {
		return fmt.Errorf("evidence signal %q has unsafe expected value %q for shape %q", evidence.Signal, evidence.Expected, shape.ExpectedShape)
	}
	return nil
}

func safeEvidenceValue(shape, value string) bool {
	switch shape {
	case EvidenceShapeClosedErrorCode:
		return value != "" && len(value) <= 64 && strings.IndexFunc(value, func(r rune) bool {
			return !(r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) == -1
	case EvidenceShapeHTTPStatus:
		parsed, err := strconv.Atoi(value)
		return err == nil && strconv.Itoa(parsed) == value && parsed >= 100 && parsed <= 599
	case EvidenceShapeUTCTimestamp:
		parsed, err := time.Parse(time.RFC3339, value)
		return err == nil && parsed.UTC().Format(time.RFC3339) == value
	case EvidenceShapeCount:
		parsed, err := strconv.Atoi(value)
		if err == nil && strconv.Itoa(parsed) == value {
			return parsed >= 0 && parsed <= MaxTraceAttempts
		}
		return value == "0 execution failures"
	case EvidenceShapeExecutionFailureCounts:
		var total, timedOut, cancelled, commandErrors int
		count, err := fmt.Sscanf(value, "%d execution failures: %d timed out, %d cancelled, %d command errors", &total, &timedOut, &cancelled, &commandErrors)
		return err == nil && count == 4 && value == fmt.Sprintf("%d execution failures: %d timed out, %d cancelled, %d command errors", total, timedOut, cancelled, commandErrors) && total >= 0 && total <= MaxTraceAttempts && timedOut >= 0 && cancelled >= 0 && commandErrors >= 0 && total == timedOut+cancelled+commandErrors
	case EvidenceShapeCompletedFraction:
		var reached, completed int
		count, err := fmt.Sscanf(value, "%d/%d completed attempts reached", &reached, &completed)
		return err == nil && count == 2 && value == fmt.Sprintf("%d/%d completed attempts reached", reached, completed) && reached >= 0 && completed >= 0 && reached <= completed && completed <= MaxTraceAttempts
	case EvidenceShapePathCount:
		var count int
		if matched, _ := fmt.Sscanf(value, "degraded segment observed among %d completed attempt", &count); matched == 1 && value == fmt.Sprintf("degraded segment observed among %d completed attempt", count) && count >= 0 && count <= MaxTraceAttempts {
			return true
		}
		if matched, _ := fmt.Sscanf(value, "degraded segment observed among %d completed attempts", &count); matched == 1 && value == fmt.Sprintf("degraded segment observed among %d completed attempts", count) && count >= 0 && count <= MaxTraceAttempts {
			return true
		}
		if matched, _ := fmt.Sscanf(value, "multiple successful completed path signatures among %d reached completed attempts", &count); matched == 1 && value == fmt.Sprintf("multiple successful completed path signatures among %d reached completed attempts", count) && count >= 0 && count <= MaxTraceAttempts {
			return true
		}
		return false
	case EvidenceShapeNone:
		return value == ""
	default:
		return false
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func MarshalPresentationContract(contract PresentationContract) ([]byte, error) {
	encoded, err := json.Marshal(contract)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}
