package diagnostic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type Kind string

const (
	KindDNS        Kind = "dns"
	KindTCP        Kind = "tcp"
	KindHTTP       Kind = "http"
	KindHTTPS      Kind = "https"
	KindSSH        Kind = "ssh"
	KindSMTP       Kind = "smtp"
	KindSubmission Kind = "submission"
	KindSMTPS      Kind = "smtps"
	KindIMAP       Kind = "imap"
	KindIMAPS      Kind = "imaps"
	KindPOP3       Kind = "pop3"
	KindPOP3S      Kind = "pop3s"
	KindTraceroute Kind = "traceroute"
)

type Status string

const (
	StatusHealthy     Status = "healthy"
	StatusDegraded    Status = "degraded"
	StatusUnreachable Status = "unreachable"
)

type Target struct {
	Kind           Kind   `json:"kind"`
	Address        string `json:"address"`
	ExpectedStatus int    `json:"expected_status,omitempty"`
	Attempts       int    `json:"attempts,omitempty"`
}

type TopologyMode string

const (
	TopologyModeFull    TopologyMode = "full"
	TopologyModeCompact TopologyMode = "compact"
)

type Request struct {
	Targets      []Target     `json:"targets"`
	TimeoutMS    int          `json:"timeout_ms,omitempty"`
	TopologyMode TopologyMode `json:"topology_mode,omitempty"`

	topologyModeSet bool
}

// UnmarshalJSON preserves topology_mode presence so an explicitly empty value
// can be rejected while an omitted value retains the legacy full behavior.
func (r *Request) UnmarshalJSON(data []byte) error {
	var wire struct {
		Targets      []Target        `json:"targets"`
		TimeoutMS    int             `json:"timeout_ms,omitempty"`
		TopologyMode json.RawMessage `json:"topology_mode"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request must contain one JSON object")
	}
	*r = Request{Targets: wire.Targets, TimeoutMS: wire.TimeoutMS}
	if wire.TopologyMode == nil {
		return nil
	}
	r.topologyModeSet = true
	if bytes.Equal(wire.TopologyMode, []byte("null")) {
		return fmt.Errorf("topology_mode must be a string")
	}
	if err := json.Unmarshal(wire.TopologyMode, &r.TopologyMode); err != nil {
		return fmt.Errorf("topology_mode must be a string: %w", err)
	}
	return nil
}

type Result struct {
	Kind      Kind           `json:"kind"`
	Address   string         `json:"address"`
	Status    Status         `json:"status"`
	LatencyMS int64          `json:"latency_ms"`
	StartedAt time.Time      `json:"started_at"`
	ErrorCode string         `json:"error_code,omitempty"`
	Message   string         `json:"message,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

type Report struct {
	ID              string           `json:"id"`
	Status          Status           `json:"status"`
	StartedAt       time.Time        `json:"started_at"`
	DurationMS      int64            `json:"duration_ms"`
	Summary         Summary          `json:"summary"`
	Results         []Result         `json:"results"`
	Analysis        *Analysis        `json:"analysis,omitempty"`
	CompactTopology *CompactTopology `json:"compact_topology,omitempty"`
	compactBuild    *CompactTopologyBuildResult
}

// SetCompactTopologyBuild caches the compact projection and its non-transport
// selection metadata on a report. The metadata is intentionally not JSON.
func (r *Report) SetCompactTopologyBuild(build CompactTopologyBuildResult) {
	r.CompactTopology = build.Topology
	r.compactBuild = &build
}

// CachedCompactTopologyBuild returns the projection already produced by the
// runner, avoiding a second full build in the transport layer.
func (r Report) CachedCompactTopologyBuild() (CompactTopologyBuildResult, bool) {
	if r.compactBuild == nil || r.compactBuild.Topology == nil || r.compactBuild.Topology != r.CompactTopology {
		return CompactTopologyBuildResult{}, false
	}
	return *r.compactBuild, true
}

type Summary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type Verdict string

const (
	VerdictHealthy      Verdict = "healthy"
	VerdictAttention    Verdict = "attention"
	VerdictInconclusive Verdict = "inconclusive"
)

type FindingCode string

const (
	FindingDNSResolutionFailed           FindingCode = "dns_resolution_failed"
	FindingEndpointConnectFailed         FindingCode = "endpoint_connect_failed"
	FindingHTTPUnexpectedStatus          FindingCode = "http_unexpected_status"
	FindingInvalidTarget                 FindingCode = "invalid_target"
	FindingExecutionTimeout              FindingCode = "execution_timeout"
	FindingExecutionCancelled            FindingCode = "execution_cancelled"
	FindingTLSDowngrade                  FindingCode = "tls_downgrade"
	FindingTLSCertificateExpired         FindingCode = "tls_certificate_expired"
	FindingTLSCertificateExpiring        FindingCode = "tls_certificate_expiring"
	FindingTLSHandshakeFailed            FindingCode = "tls_handshake_failed"
	FindingTargetPolicyBlocked           FindingCode = "target_policy_blocked"
	FindingTracerouteUnreachable         FindingCode = "traceroute_unreachable"
	FindingTraceroutePartialReachability FindingCode = "traceroute_partial_reachability"
	FindingTraceroutePathDegraded        FindingCode = "traceroute_path_degraded"
	FindingTraceroutePathUnstable        FindingCode = "traceroute_path_unstable"
	FindingTracerouteExecutionFailed     FindingCode = "traceroute_execution_failed"
)

type FindingSeverity string

const (
	SeverityCritical FindingSeverity = "critical"
	SeverityWarning  FindingSeverity = "warning"
	SeverityInfo     FindingSeverity = "info"
)

type FindingCategory string

const (
	CategoryNameResolution FindingCategory = "name_resolution"
	CategoryConnectivity   FindingCategory = "connectivity"
	CategoryApplication    FindingCategory = "application"
	CategorySecurity       FindingCategory = "security"
	CategoryRouting        FindingCategory = "routing"
	CategoryExecution      FindingCategory = "execution"
	CategoryInput          FindingCategory = "input"
)

// Confidence describes evidence support, not a probability or likelihood.
type Confidence string

const (
	ConfidenceDirect       Confidence = "direct"
	ConfidenceCorroborated Confidence = "corroborated"
	ConfidenceLimited      Confidence = "limited"
)

type EvidenceProvenance string

const (
	ProvenanceResult  EvidenceProvenance = "result"
	ProvenanceDetails EvidenceProvenance = "details"
)

type Finding struct {
	ID          string          `json:"id"`
	Code        FindingCode     `json:"code"`
	Severity    FindingSeverity `json:"severity"`
	Category    FindingCategory `json:"category"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Confidence  Confidence      `json:"confidence"`
	EvidenceIDs []string        `json:"evidence_ids"`
	ActionIDs   []string        `json:"action_ids"`
}

type Evidence struct {
	ID          string             `json:"id"`
	ResultIndex int                `json:"result_index"`
	Kind        Kind               `json:"kind"`
	Attempt     int                `json:"attempt,omitempty"`
	Address     string             `json:"address"`
	Signal      string             `json:"signal"`
	Observed    string             `json:"observed"`
	Expected    string             `json:"expected,omitempty"`
	Provenance  EvidenceProvenance `json:"provenance"`
}

type Action struct {
	ID                  string `json:"id"`
	Title               string `json:"title"`
	Step                string `json:"step"`
	ExpectedResult      string `json:"expected_result"`
	EscalationCondition string `json:"escalation_condition"`
}

type CoverageIssueCode string

const (
	CoverageMissingDetails     CoverageIssueCode = "missing_details"
	CoverageMalformedDetails   CoverageIssueCode = "malformed_details"
	CoverageUnsupportedDetails CoverageIssueCode = "unsupported_details"
)

type CoverageIssue struct {
	Code        CoverageIssueCode `json:"code"`
	ResultIndex int               `json:"result_index"`
	Kind        Kind              `json:"kind"`
	Signal      string            `json:"signal,omitempty"`
	Reason      string            `json:"reason"`
}

type Coverage struct {
	Available        []string        `json:"available"`
	Missing          []string        `json:"missing"`
	ProviderFailures []CoverageIssue `json:"provider_failures"`
	Limitations      []CoverageIssue `json:"limitations"`
}

type Analysis struct {
	Verdict  Verdict    `json:"verdict"`
	Findings []Finding  `json:"findings"`
	Evidence []Evidence `json:"evidence"`
	Actions  []Action   `json:"actions"`
	Coverage Coverage   `json:"coverage"`
}
