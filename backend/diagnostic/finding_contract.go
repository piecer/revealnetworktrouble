package diagnostic

import (
	"encoding/json"
	"sort"
)

const (
	FindingContractSchemaV1         = "finding-contract-v1"
	VerificationScopeServerGreeting = "server_greeting"
	ResultDetailVerificationScope   = "verification_scope"
	ResultDetailTLSVersion          = "tls_version"
	ResultDetailCipherSuite         = "cipher_suite"
	ResultDetailCertificateSubject  = "certificate_subject"
	ResultDetailCertificateExpires  = "certificate_expires_at"
	ResultDetailCertificateBefore   = "certificate_not_before"
	ResultDetailCertificateAfter    = "certificate_not_after"

	DetailTypeString              = "string"
	DetailTypeNonEmptyString      = "nonempty_string"
	DetailTypeCanonicalUTCRFC3339 = "canonical_utc_rfc3339"
	DetailTypeUTCTimestamp        = "utc_timestamp"
	DetailTypeNonNegativeInteger  = "nonnegative_integer"
	DetailTypeStringArray         = "string_array"
	DetailTypeTraceAttempts       = "trace_attempts"
	DetailTypeTopology            = "topology"
	DetailTypeEnrichment          = "enrichment"
)

type FindingContractEntry struct {
	Code            FindingCode `json:"code"`
	PresentationKey string      `json:"presentation_key"`
}

type ResultDetailContract struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Const string `json:"const,omitempty"`
}

type ResultShapeContract struct {
	Name            string                 `json:"name"`
	Kinds           []Kind                 `json:"kinds"`
	Status          Status                 `json:"status"`
	ErrorCode       string                 `json:"error_code,omitempty"`
	RequiredDetails []ResultDetailContract `json:"required_details"`
	OptionalDetails []ResultDetailContract `json:"optional_details"`
}

type FindingContract struct {
	Schema             string                 `json:"schema"`
	Findings           []FindingContractEntry `json:"findings"`
	ResultShapes       []ResultShapeContract  `json:"result_shapes"`
	ResultMatrixRows   int                    `json:"result_matrix_rows"`
	VerificationScopes []string               `json:"verification_scopes"`
}

var findingRegistry = []FindingContractEntry{
	{FindingCheckerCapacityUnavailable, "finding.checker_capacity_unavailable"},
	{FindingCheckerPanic, "finding.checker_panic"},
	{FindingDNSResolutionFailed, "finding.dns_resolution_failed"},
	{FindingEndpointConnectFailed, "finding.endpoint_connect_failed"},
	{FindingExecutionCancelled, "finding.execution_cancelled"},
	{FindingExecutionTimeout, "finding.execution_timeout"},
	{FindingHTTPUnexpectedStatus, "finding.http_unexpected_status"},
	{FindingInvalidTarget, "finding.invalid_target"},
	{FindingServiceGreetingUnverified, "finding.service_greeting_unverified"},
	{FindingTargetPolicyBlocked, "finding.target_policy_blocked"},
	{FindingTLSCertificateExpired, "finding.tls_certificate_expired"},
	{FindingTLSCertificateExpiring, "finding.tls_certificate_expiring"},
	{FindingTLSCertificateNotYetValid, "finding.tls_certificate_not_yet_valid"},
	{FindingTLSDowngrade, "finding.tls_downgrade"},
	{FindingTLSHandshakeFailed, "finding.tls_handshake_failed"},
	{FindingTLSHostnameMismatch, "finding.tls_hostname_mismatch"},
	{FindingTLSUntrusted, "finding.tls_untrusted"},
	{FindingTracerouteExecutionFailed, "finding.traceroute_execution_failed"},
	{FindingTraceroutePartialReachability, "finding.traceroute_partial_reachability"},
	{FindingTraceroutePathDegraded, "finding.traceroute_path_degraded"},
	{FindingTraceroutePathUnstable, "finding.traceroute_path_unstable"},
	{FindingTracerouteUnavailable, "finding.traceroute_unavailable"},
	{FindingTracerouteUnreachable, "finding.traceroute_unreachable"},
}

var producerResultShapeRegistry = []ResultShapeContract{
	// Runner-owned terminal outcomes are reachable for every registered checker
	// kind and intentionally contain no checker detail payload.
	noDetailShape("cancelled", allKinds(), StatusUnreachable, "cancelled"),
	noDetailShape("checker_capacity_unavailable", allKinds(), StatusUnreachable, "checker_capacity_unavailable"),
	noDetailShape("checker_panic", allKinds(), StatusUnreachable, "checker_panic"),
	noDetailShape("connection_failed", allKinds(), StatusUnreachable, "connection_failed"),
	noDetailShape("network_policy_blocked", allKinds(), StatusUnreachable, "network_policy_blocked"),
	noDetailShape("timeout", allKinds(), StatusUnreachable, "timeout"),

	shape("dns_healthy", []Kind{KindDNS}, StatusHealthy, "", []ResultDetailContract{
		{Key: "addresses", Type: DetailTypeStringArray},
		{Key: "answer_count", Type: DetailTypeNonNegativeInteger},
	}),
	shape("tcp_healthy", []Kind{KindTCP}, StatusHealthy, "", []ResultDetailContract{
		{Key: "local_address", Type: DetailTypeString},
		{Key: "remote_address", Type: DetailTypeString},
	}),

	// HTTP observations are semantically one outcome per kind/status. Legacy
	// reports may omit observations, while full producer reports include the
	// core HTTP fields and may include TLS/certificate observations after a
	// redirect. Keep those observations optional instead of inventing one shape
	// for every concrete key set.
	httpShape("http_healthy", KindHTTP, StatusHealthy, ""),
	httpShape("http_unexpected_status", KindHTTP, StatusUnreachable, "unexpected_status"),
	httpShape("https_healthy", KindHTTPS, StatusHealthy, ""),
	httpShape("https_unexpected_status", KindHTTPS, StatusUnreachable, "unexpected_status"),
	noDetailShape("invalid_url", []Kind{KindHTTP, KindHTTPS}, StatusUnreachable, "invalid_url"),
	noDetailShape("response_read_failed", []Kind{KindHTTP, KindHTTPS}, StatusUnreachable, "response_read_failed"),
	noDetailShape("tls_downgrade", []Kind{KindHTTPS}, StatusUnreachable, "tls_downgrade"),

	noDetailShape("invalid_address", []Kind{KindIMAPS, KindPOP3S, KindSMTPS, KindTraceroute}, StatusUnreachable, "invalid_address"),
	noDetailShape("service_greeting_unverified", serviceKinds(), StatusDegraded, ResultErrorServiceGreetingUnverified),
	shape("service_greeting_verified_plain", plainServiceKinds(), StatusHealthy, "", []ResultDetailContract{
		{Key: ResultDetailVerificationScope, Type: DetailTypeString, Const: VerificationScopeServerGreeting},
	}),
	shape("service_greeting_verified_tls_certificate", tlsServiceKinds(), StatusHealthy, "", tlsServiceDetails(true)),

	tlsFailureShape(ResultErrorTLSCertificateExpired, []ResultDetailContract{
		{Key: ResultDetailCertificateAfter, Type: DetailTypeCanonicalUTCRFC3339},
		{Key: ResultDetailCertificateBefore, Type: DetailTypeCanonicalUTCRFC3339},
	}),
	tlsFailureShape(ResultErrorTLSCertificateNotYetValid, []ResultDetailContract{
		{Key: ResultDetailCertificateAfter, Type: DetailTypeCanonicalUTCRFC3339},
		{Key: ResultDetailCertificateBefore, Type: DetailTypeCanonicalUTCRFC3339},
	}),
	tlsFailureShape(ResultErrorTLSHandshakeFailed, []ResultDetailContract{}),
	tlsFailureShape(ResultErrorTLSHostnameMismatch, []ResultDetailContract{}),
	tlsFailureShape(ResultErrorTLSUntrusted, []ResultDetailContract{}),

	noDetailShape("traceroute_unavailable", []Kind{KindTraceroute}, StatusUnreachable, "traceroute_unavailable"),
	traceShape("traceroute_healthy", StatusHealthy, "", true),
	traceShape("traceroute_degraded", StatusDegraded, "", true),
	traceShape("traceroute_destination_unreached", StatusUnreachable, "destination_unreached", true),
	traceShape("traceroute_execution_failed", StatusUnreachable, "traceroute_failed", true),
	traceShape("traceroute_execution_timeout", StatusUnreachable, "timeout", true),
	traceShape("traceroute_execution_incomplete", StatusUnreachable, "traceroute_execution_incomplete", true),
}

func shape(name string, kinds []Kind, status Status, code string, required []ResultDetailContract) ResultShapeContract {
	return ResultShapeContract{Name: name, Kinds: kinds, Status: status, ErrorCode: code,
		RequiredDetails: required, OptionalDetails: []ResultDetailContract{}}
}

func noDetailShape(name string, kinds []Kind, status Status, code string) ResultShapeContract {
	return shape(name, kinds, status, code, []ResultDetailContract{})
}

func allKinds() []Kind {
	return []Kind{KindDNS, KindHTTP, KindHTTPS, KindIMAP, KindIMAPS, KindPOP3, KindPOP3S, KindSMTP, KindSMTPS, KindSSH, KindSubmission, KindTCP, KindTraceroute}
}

func httpShape(name string, kind Kind, status Status, code string) ResultShapeContract {
	optional := []ResultDetailContract{
		{Key: "content_type", Type: DetailTypeString},
		{Key: "expected_status", Type: DetailTypeNonNegativeInteger},
		{Key: "protocol", Type: DetailTypeString},
		{Key: "status_code", Type: DetailTypeNonNegativeInteger},
		{Key: ResultDetailCipherSuite, Type: DetailTypeString},
		{Key: ResultDetailTLSVersion, Type: DetailTypeNonEmptyString},
		{Key: ResultDetailCertificateExpires, Type: DetailTypeUTCTimestamp},
		{Key: ResultDetailCertificateSubject, Type: DetailTypeString},
	}
	sort.Slice(optional, func(i, j int) bool { return optional[i].Key < optional[j].Key })
	return ResultShapeContract{Name: name, Kinds: []Kind{kind}, Status: status, ErrorCode: code,
		RequiredDetails: []ResultDetailContract{}, OptionalDetails: optional}
}

func tlsServiceDetails(certificate bool) []ResultDetailContract {
	details := []ResultDetailContract{
		{Key: ResultDetailCipherSuite, Type: DetailTypeString},
		{Key: ResultDetailTLSVersion, Type: DetailTypeNonEmptyString},
		{Key: ResultDetailVerificationScope, Type: DetailTypeString, Const: VerificationScopeServerGreeting},
	}
	if certificate {
		details = append(details,
			ResultDetailContract{Key: ResultDetailCertificateExpires, Type: DetailTypeUTCTimestamp},
			ResultDetailContract{Key: ResultDetailCertificateSubject, Type: DetailTypeString})
	}
	sort.Slice(details, func(i, j int) bool { return details[i].Key < details[j].Key })
	return details
}

func traceShape(name string, status Status, code string, topologyAllowed bool) ResultShapeContract {
	required := []ResultDetailContract{
		{Key: "attempts_cancelled", Type: DetailTypeNonNegativeInteger},
		{Key: "attempts_execution_failed", Type: DetailTypeNonNegativeInteger},
		{Key: "attempts_failed", Type: DetailTypeNonNegativeInteger},
		{Key: "attempts_reached", Type: DetailTypeNonNegativeInteger},
		{Key: "attempts_timed_out", Type: DetailTypeNonNegativeInteger},
		{Key: "attempts_total", Type: DetailTypeNonNegativeInteger},
		{Key: "attempts_unreached", Type: DetailTypeNonNegativeInteger},
	}
	optional := []ResultDetailContract{
		{Key: "attempts", Type: DetailTypeTraceAttempts},
		{Key: "geoip_enrichment", Type: DetailTypeEnrichment},
		{Key: "geoip_provider_failures", Type: DetailTypeNonNegativeInteger},
	}
	if topologyAllowed {
		optional = append(optional, ResultDetailContract{Key: "topology", Type: DetailTypeTopology})
	}
	sort.Slice(optional, func(i, j int) bool { return optional[i].Key < optional[j].Key })
	return ResultShapeContract{Name: name, Kinds: []Kind{KindTraceroute}, Status: status, ErrorCode: code,
		RequiredDetails: required, OptionalDetails: optional}
}

func tlsFailureShape(code string, required []ResultDetailContract) ResultShapeContract {
	return ResultShapeContract{
		Name: code, Kinds: tlsKinds(), Status: StatusUnreachable, ErrorCode: code,
		RequiredDetails: required, OptionalDetails: []ResultDetailContract{},
	}
}

func serviceKinds() []Kind {
	return []Kind{KindIMAP, KindIMAPS, KindPOP3, KindPOP3S, KindSMTP, KindSMTPS, KindSSH, KindSubmission}
}

func plainServiceKinds() []Kind {
	return []Kind{KindIMAP, KindPOP3, KindSMTP, KindSSH, KindSubmission}
}

func tlsServiceKinds() []Kind {
	return []Kind{KindIMAPS, KindPOP3S, KindSMTPS}
}

func tlsKinds() []Kind {
	return []Kind{KindHTTPS, KindIMAPS, KindPOP3S, KindSMTPS}
}

func validFindingCode(code FindingCode) bool {
	index := sort.Search(len(findingRegistry), func(index int) bool { return findingRegistry[index].Code >= code })
	return index < len(findingRegistry) && findingRegistry[index].Code == code
}

func ProducerFindingContract() FindingContract {
	contract := FindingContract{
		Schema:             FindingContractSchemaV1,
		Findings:           append([]FindingContractEntry(nil), findingRegistry...),
		ResultShapes:       make([]ResultShapeContract, len(producerResultShapeRegistry)),
		VerificationScopes: []string{VerificationScopeServerGreeting},
	}
	for index, shape := range producerResultShapeRegistry {
		shape.Kinds = append(make([]Kind, 0, len(shape.Kinds)), shape.Kinds...)
		shape.RequiredDetails = append(make([]ResultDetailContract, 0, len(shape.RequiredDetails)), shape.RequiredDetails...)
		shape.OptionalDetails = append(make([]ResultDetailContract, 0, len(shape.OptionalDetails)), shape.OptionalDetails...)
		contract.ResultShapes[index] = shape
		contract.ResultMatrixRows += len(shape.Kinds)
	}
	sort.Slice(contract.ResultShapes, func(left, right int) bool {
		return contract.ResultShapes[left].Name < contract.ResultShapes[right].Name
	})
	return contract
}

func MarshalFindingContract() ([]byte, error) {
	encoded, err := json.Marshal(ProducerFindingContract())
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}
