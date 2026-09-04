package api

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type apiErrorKey string

const (
	apiErrorBodyDecodeCapacity apiErrorKey = "body_decode_capacity_unavailable"
	apiErrorCompactTooLarge    apiErrorKey = "compact_response_too_large"
	apiErrorFullTooLarge       apiErrorKey = "full_response_too_large"
	apiErrorInternal           apiErrorKey = "internal_error"
	apiErrorInvalidJSON        apiErrorKey = "invalid_json"
	apiErrorInvalidRequest     apiErrorKey = "invalid_request"
	apiErrorMethodNotAllowed   apiErrorKey = "method_not_allowed"
	apiErrorNetworkPolicy      apiErrorKey = "network_policy_blocked"
	apiErrorRateLimited        apiErrorKey = "rate_limited"
	apiErrorRequestTooLarge    apiErrorKey = "request_too_large"
	apiErrorRouteNotFound      apiErrorKey = "route_not_found"
	apiErrorSerialization      apiErrorKey = "response_serialization_failed"
	apiErrorServerBusy         apiErrorKey = "server_busy"
	apiErrorServerDraining     apiErrorKey = "server_draining"
	apiErrorUnauthorized       apiErrorKey = "unauthorized"
	apiErrorWriteCapacity      apiErrorKey = "write_capacity_unavailable"
)

type apiErrorDefinition struct {
	Status     int
	Code       string
	Retryable  bool
	RetryAfter bool
	Message    string
	Outcome    TelemetryOutcome
}

var apiErrorRegistry = map[apiErrorKey]apiErrorDefinition{
	apiErrorBodyDecodeCapacity: {503, "body_decode_capacity_unavailable", true, true, "request body decode capacity is temporarily unavailable", TelemetryOutcomeBodyCapacity},
	apiErrorCompactTooLarge:    {500, "compact_response_too_large", true, false, "compact report response exceeds the size limit", TelemetryOutcomeCompactSize},
	apiErrorFullTooLarge:       {500, "full_response_too_large", true, false, "full report response exceeds the size limit", TelemetryOutcomeFullSize},
	apiErrorInternal:           {500, "internal_error", true, false, "report could not be generated", TelemetryOutcomePanicSafeFailure},
	apiErrorInvalidJSON:        {400, "invalid_json", false, false, "request body must be a valid JSON report request", TelemetryOutcomeInvalidJSON},
	apiErrorInvalidRequest:     {422, "invalid_request", false, false, "request is invalid", TelemetryOutcomeInvalidRequest},
	apiErrorMethodNotAllowed:   {405, "unmatched", false, false, "route not found", TelemetryOutcomeUnmatched},
	apiErrorNetworkPolicy:      {422, "network_policy_blocked", false, false, "target is not allowed in public mode", TelemetryOutcomePolicy},
	apiErrorRateLimited:        {429, "rate_limited", true, true, "per-client request limit exceeded", TelemetryOutcomeRateLimited},
	apiErrorRequestTooLarge:    {413, "request_too_large", false, false, "request body exceeds the size limit", TelemetryOutcomeRequestTooLarge},
	apiErrorRouteNotFound:      {404, "unmatched", false, false, "route not found", TelemetryOutcomeUnmatched},
	apiErrorSerialization:      {500, "response_serialization_failed", true, false, "report response could not be serialized", TelemetryOutcomeSerialization},
	apiErrorServerBusy:         {503, "server_busy", true, true, "report capacity is temporarily unavailable", TelemetryOutcomeServerCapacity},
	apiErrorServerDraining:     {503, "server_draining", true, true, "server is draining and temporarily unavailable", TelemetryOutcomeServerDraining},
	apiErrorUnauthorized:       {401, "unauthorized", false, false, "valid API credentials are required", TelemetryOutcomeUnauthorized},
	apiErrorWriteCapacity:      {503, "write_capacity_unavailable", true, false, "report response write capacity is temporarily unavailable", TelemetryOutcomeWriteCapacity},
}

type apiErrorContractRow struct {
	Key        apiErrorKey `json:"key"`
	Status     int         `json:"status"`
	Code       string      `json:"code"`
	Retryable  bool        `json:"retryable"`
	RetryAfter bool        `json:"retry_after"`
	Message    string      `json:"message"`
	Body       string      `json:"body"`
}

func apiErrorContractRows() []apiErrorContractRow {
	keys := make([]string, 0, len(apiErrorRegistry))
	for key := range apiErrorRegistry {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	rows := make([]apiErrorContractRow, 0, len(keys))
	for _, text := range keys {
		key := apiErrorKey(text)
		definition := apiErrorRegistry[key]
		rows = append(rows, apiErrorContractRow{
			Key: key, Status: definition.Status, Code: definition.Code, Retryable: definition.Retryable,
			RetryAfter: definition.RetryAfter, Message: definition.Message, Body: string(marshalAPIError(key)),
		})
	}
	return rows
}

type retryAfterContractCase struct {
	Name    string  `json:"name"`
	Header  *string `json:"header"`
	Valid   bool    `json:"valid"`
	Seconds *int    `json:"seconds"`
}

func stringPointer(value string) *string { return &value }
func intPointer(value int) *int          { return &value }

func retryAfterContractCases() []retryAfterContractCase {
	return []retryAfterContractCase{
		{Name: "absent"},
		{Name: "minimum", Header: stringPointer("1"), Valid: true, Seconds: intPointer(1)},
		{Name: "maximum", Header: stringPointer("3600"), Valid: true, Seconds: intPointer(3600)},
		{Name: "zero", Header: stringPointer("0")},
		{Name: "above_maximum", Header: stringPointer("3601")},
		{Name: "leading_zero", Header: stringPointer("01")},
		{Name: "positive_sign", Header: stringPointer("+1")},
		{Name: "negative_sign", Header: stringPointer("-1")},
		{Name: "leading_space", Header: stringPointer(" 1")},
		{Name: "trailing_space", Header: stringPointer("1 ")},
		{Name: "http_date", Header: stringPointer("Thu, 03 Sep 2026 00:00:01 GMT")},
		{Name: "overflow", Header: stringPointer("999999999999999999999999999999")},
		{Name: "comma_duplicate", Header: stringPointer("1, 2")},
	}
}

type apiErrorContractFixture struct {
	Schema              string                   `json:"schema"`
	Limits              apiErrorWireLimits       `json:"limits"`
	Errors              []apiErrorContractRow    `json:"errors"`
	StructuralMutations []apiErrorWireMutation   `json:"structural_mutations"`
	RetryAfter          []retryAfterContractCase `json:"retry_after_cases"`
}

type apiErrorWireLimits struct {
	BodyUTF16Units   int `json:"body_utf16_units"`
	Depth            int `json:"depth"`
	Tokens           int `json:"tokens"`
	Properties       int `json:"properties"`
	StringUTF16Units int `json:"string_utf16_units"`
}

type apiErrorWireMutation struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	Body   string `json:"body"`
	Valid  bool   `json:"valid"`
}

const (
	apiErrorMaxBodyUTF16Units   = 64 * 1024
	apiErrorMaxDepth            = 2
	apiErrorMaxTokens           = 10
	apiErrorMaxProperties       = 3
	apiErrorMaxStringUTF16Units = 128
)

func apiErrorStructuralMutations() []apiErrorWireMutation {
	definition := apiErrorDefinitionFor(apiErrorServerBusy)
	status := definition.Status
	canonical := string(marshalAPIError(apiErrorServerBusy))
	quotedCode, _ := json.Marshal(definition.Code)
	quotedMessage, _ := json.Marshal(definition.Message)
	errorObject := `{"code":` + string(quotedCode) + `,"message":` + string(quotedMessage) + `}`
	mutation := func(name, value string) apiErrorWireMutation {
		return apiErrorWireMutation{Name: name, Status: status, Body: value, Valid: false}
	}
	mutations := []apiErrorWireMutation{
		mutation("empty", ""),
		mutation("malformed_object", "{oops"),
		mutation("root_null", "null"),
		mutation("root_array", "[]"),
		mutation("root_string", `"error"`),
		mutation("missing_error", `{}`),
		mutation("null_error", `{"error":null}`),
		mutation("array_error", `{"error":[]}`),
		mutation("string_error", `{"error":"HOSTILE"}`),
		mutation("unknown_root_property", `{"error":`+errorObject+`,"HOSTILE":0}`),
		mutation("duplicate_error", `{"error":`+errorObject+`,"error":`+errorObject+`}`),
		mutation("missing_code", `{"error":{"message":`+string(quotedMessage)+`}}`),
		mutation("missing_message", `{"error":{"code":`+string(quotedCode)+`}}`),
		mutation("unknown_error_property", `{"error":`+strings.TrimSuffix(errorObject, "}")+`,"HOSTILE":0}}`),
		mutation("duplicate_code", `{"error":{"code":`+string(quotedCode)+`,"code":`+string(quotedCode)+`,"message":`+string(quotedMessage)+`}}`),
		mutation("duplicate_message", `{"error":{"code":`+string(quotedCode)+`,"message":`+string(quotedMessage)+`,"message":`+string(quotedMessage)+`}}`),
		mutation("null_code", `{"error":{"code":null,"message":`+string(quotedMessage)+`}}`),
		mutation("number_code", `{"error":{"code":7,"message":`+string(quotedMessage)+`}}`),
		mutation("array_code", `{"error":{"code":[],"message":`+string(quotedMessage)+`}}`),
		mutation("object_code_depth_three", `{"error":{"code":{},"message":`+string(quotedMessage)+`}}`),
		mutation("null_message", `{"error":{"code":`+string(quotedCode)+`,"message":null}}`),
		mutation("number_message", `{"error":{"code":`+string(quotedCode)+`,"message":7}}`),
		mutation("array_message", `{"error":{"code":`+string(quotedCode)+`,"message":[]}}`),
		mutation("object_message_depth_three", `{"error":{"code":`+string(quotedCode)+`,"message":{}}}`),
		mutation("noncanonical_message", `{"error":{"code":`+string(quotedCode)+`,"message":"HOSTILE"}}`),
		mutation("trailing_garbage", canonical+"HOSTILE"),
		mutation("concatenated_objects", canonical+canonical),
		mutation("bad_escape", `{"error":{"code":"server\qbusy","message":`+string(quotedMessage)+`}}`),
		mutation("truncated_unicode_escape", `{"error":{"code":"server\u12","message":`+string(quotedMessage)+`}}`),
		mutation("escaped_unpaired_high_surrogate", `{"error":{"code":`+string(quotedCode)+`,"message":"\uD800"}}`),
		mutation("escaped_unpaired_low_surrogate", `{"error":{"code":`+string(quotedCode)+`,"message":"\uDC00"}}`),
		mutation("code_units_limit_plus_one", `{"error":{"code":"`+strings.Repeat("x", apiErrorMaxStringUTF16Units+1)+`","message":`+string(quotedMessage)+`}}`),
		mutation("message_units_limit_plus_one", `{"error":{"code":`+string(quotedCode)+`,"message":"`+strings.Repeat("x", apiErrorMaxStringUTF16Units+1)+`"}}`),
		mutation("deep_array_12000", `{"error":`+strings.Repeat("[", 12_000)+strings.Repeat("]", 12_000)+`}`),
		mutation("deep_object_4000", `{"error":`+strings.Repeat(`{"x":`, 4_000)+`0`+strings.Repeat("}", 4_001)),
	}
	for _, row := range apiErrorContractRows() {
		wrongMessage, _ := json.Marshal(apiErrorResponse{Error: apiErrorBody{Code: row.Code, Message: "HOSTILE-NONCANONICAL-MESSAGE"}})
		wrongCode, _ := json.Marshal(apiErrorResponse{Error: apiErrorBody{Code: "HOSTILE-" + string(row.Key), Message: row.Message}})
		contradictoryStatus := 500
		if row.Status == contradictoryStatus {
			contradictoryStatus = 503
		}
		mutations = append(mutations,
			mutation("row_message_mismatch_"+string(row.Key), string(append(wrongMessage, '\n'))),
			mutation("row_unknown_code_"+string(row.Key), string(append(wrongCode, '\n'))),
			apiErrorWireMutation{Name: "row_contradictory_status_" + string(row.Key), Status: contradictoryStatus, Body: row.Body, Valid: false},
		)
	}
	mutations = append(mutations,
		mutation("body_units_limit_plus_one", canonical+strings.Repeat(" ", apiErrorMaxBodyUTF16Units-len(canonical)+1)),
		apiErrorWireMutation{Name: "unregistered_status", Status: 599, Body: canonical, Valid: false},
	)
	return mutations
}

func marshalAPIErrorContractFixture() ([]byte, error) {
	payload, err := json.MarshalIndent(apiErrorContractFixture{
		Schema: "api-error-wire-v1",
		Limits: apiErrorWireLimits{
			BodyUTF16Units: apiErrorMaxBodyUTF16Units, Depth: apiErrorMaxDepth, Tokens: apiErrorMaxTokens,
			Properties: apiErrorMaxProperties, StringUTF16Units: apiErrorMaxStringUTF16Units,
		},
		Errors: apiErrorContractRows(), StructuralMutations: apiErrorStructuralMutations(), RetryAfter: retryAfterContractCases(),
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

type apiErrorMetadata struct {
	RetryAfter time.Duration
	Allow      apiAllowMethods
}

type apiAllowMethods uint8

const (
	apiAllowOptions apiAllowMethods = iota + 1
	apiAllowGet
	apiAllowPost
)

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiErrorResponse struct {
	Error apiErrorBody `json:"error"`
}

func apiErrorDefinitionFor(key apiErrorKey) apiErrorDefinition {
	definition, ok := apiErrorRegistry[key]
	if !ok {
		panic("unknown API error registry key")
	}
	return definition
}

func marshalAPIError(key apiErrorKey) []byte {
	definition := apiErrorDefinitionFor(key)
	payload, err := json.Marshal(apiErrorResponse{Error: apiErrorBody{Code: definition.Code, Message: definition.Message}})
	if err != nil {
		panic("fixed API error response did not marshal")
	}
	return append(payload, '\n')
}

type responseHeader interface {
	Set(string, string)
	Del(string)
}

func applyAPIErrorHeaders(header responseHeader, definition apiErrorDefinition, metadata apiErrorMetadata) {
	header.Del("Retry-After")
	header.Del("Allow")
	if definition.RetryAfter {
		header.Set("Retry-After", canonicalRetryAfter(metadata.RetryAfter))
	}
	if definition.Status == 405 {
		switch metadata.Allow {
		case apiAllowGet:
			header.Set("Allow", "GET, OPTIONS")
		case apiAllowPost:
			header.Set("Allow", "POST, OPTIONS")
		default:
			header.Set("Allow", "OPTIONS")
		}
	}
}

func canonicalRetryAfter(duration time.Duration) string {
	seconds := int64(math.Ceil(duration.Seconds()))
	if seconds < 1 {
		seconds = 1
	} else if seconds > 3600 {
		seconds = 3600
	}
	return strconv.FormatInt(seconds, 10)
}
