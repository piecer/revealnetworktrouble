package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

var errResponseWritePanic = errors.New("response write panic")

// responder is the complete response capability visible to routing,
// middleware, and business handlers. It intentionally has no generic status,
// body, writer, encoder, or unwrap operation.
type responder interface {
	bindObservation(*requestObservation)
	setSecurityHeaders()
	setCORSHeaders(string)
	limitRequestBody(*http.Request, int64)
	setReportWriteDeadline()
	writeHealth(HealthResponse)
	writeChecks(checksResponse)
	writeReport(reportJSON)
	writeNoContent()
	writeAPIError(apiErrorKey, apiErrorMetadata)
	responseState() (attempted bool, committed bool, status int)
}

// reportJSON is the closed, pre-validated report serialization accepted by the
// sole report success capability. It is not an arbitrary response body API.
type reportJSON struct {
	payload []byte
}

// netHTTPAdapter is the only net/http entry point. Every request gets exactly
// one response capability before any middleware or route code runs.
type netHTTPAdapter struct {
	server *Server
}

func newNetHTTPAdapter(server *Server) http.Handler {
	return netHTTPAdapter{server: server}
}

func (adapter netHTTPAdapter) ServeHTTP(raw http.ResponseWriter, request *http.Request) {
	adapter.server.serveHTTP(&responseWriter{raw: raw}, request)
}

// responseWriter is the sole raw http.ResponseWriter boundary. State is set
// before forwarding calls so panic recovery can distinguish a panic before a
// response attempt from one after an attempt. Unwrap preserves optional
// ResponseController support without falsely advertising optional interfaces.
type responseWriter struct {
	raw         http.ResponseWriter
	observation *requestObservation
	attempted   bool
	committed   bool
	status      int
}

func (writer *responseWriter) Unwrap() http.ResponseWriter { return writer.raw }

func (writer *responseWriter) Header() http.Header { return writer.raw.Header() }

func (writer *responseWriter) WriteHeader(status int) {
	if !writer.attempted {
		writer.attempted = true
		writer.status = status
	}
	writer.raw.WriteHeader(status)
	writer.committed = true
}

func (writer *responseWriter) Write(payload []byte) (int, error) {
	if !writer.attempted {
		writer.attempted = true
		writer.status = http.StatusOK
	}
	count, err := writer.raw.Write(payload)
	if count > 0 || err == nil {
		writer.committed = true
	}
	return count, err
}

func (writer *responseWriter) bindObservation(observation *requestObservation) {
	writer.observation = observation
}

func (writer *responseWriter) responseState() (bool, bool, int) {
	return writer.attempted, writer.committed, writer.status
}

func (writer *responseWriter) setSecurityHeaders() {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Cache-Control", "no-store")
}

func (writer *responseWriter) setCORSHeaders(origin string) {
	header := writer.Header()
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Vary", "Origin")
	header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	header.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

func (writer *responseWriter) limitRequestBody(request *http.Request, limit int64) {
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
}

func (writer *responseWriter) setReportWriteDeadline() {
	defer func() { _ = recover() }()
	_ = http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(reportWriteTimeout))
}

func (writer *responseWriter) writeHealth(response HealthResponse) {
	writer.writeTypedOK(response)
}

func (writer *responseWriter) writeChecks(response checksResponse) {
	writer.writeTypedOK(response)
}

func (writer *responseWriter) writeReport(response reportJSON) {
	writer.writeJSONPayload(http.StatusOK, TelemetryOutcomeOK, response.payload)
}

func (writer *responseWriter) writeNoContent() {
	writer.writeEmpty(http.StatusNoContent, TelemetryOutcomeOK)
}

func (writer *responseWriter) writeAPIError(key apiErrorKey, metadata apiErrorMetadata) {
	definition := apiErrorDefinitionFor(key)
	defer func() {
		if recover() != nil && writer.observation != nil {
			writer.observation.status = definition.Status
			writer.observation.outcome = TelemetryOutcomeWriteFailedZero
		}
	}()
	applyAPIErrorHeaders(writer.Header(), definition, metadata)
	writer.writeJSONPayload(definition.Status, definition.Outcome, marshalAPIError(key))
}

func (writer *responseWriter) writeTypedOK(value any) {
	started := time.Now()
	payload, err := json.Marshal(value)
	if writer.observation != nil {
		writer.observation.marshalDuration += time.Since(started)
	}
	if err != nil {
		writer.writeAPIError(apiErrorSerialization, apiErrorMetadata{})
		return
	}
	writer.writeJSONPayload(http.StatusOK, TelemetryOutcomeOK, append(payload, '\n'))
}

func (writer *responseWriter) writeEmpty(status int, outcome TelemetryOutcome) {
	if writer.observation != nil {
		writer.observation.status = status
		writer.observation.outcome = outcome
	}
	defer func() {
		if recover() != nil && writer.observation != nil {
			writer.observation.outcome = TelemetryOutcomeWriteFailedZero
		}
	}()
	writer.WriteHeader(status)
}

func (writer *responseWriter) writeJSONPayload(status int, outcome TelemetryOutcome, payload []byte) {
	observation := writer.observation
	if observation != nil {
		observation.status = status
		observation.outcome = outcome
	}
	started := time.Now()
	attempted, actual, err := writer.writePayload(status, payload)
	if observation == nil {
		return
	}
	observation.writeDuration += time.Since(started)
	observation.responseAttempted += int64(attempted)
	observation.responseActual += int64(actual)
	if err != nil {
		if actual == 0 {
			observation.outcome = TelemetryOutcomeWriteFailedZero
		} else {
			observation.outcome = TelemetryOutcomeWriteFailedPartial
		}
	}
}

// writePayload is the only generic body/status primitive in production.
func (writer *responseWriter) writePayload(status int, payload []byte) (attempted, actual int, err error) {
	attempted = len(payload)
	defer func() {
		if recover() != nil {
			actual = 0
			err = errResponseWritePanic
			return
		}
		actual = max(0, min(actual, attempted))
		if err == nil && actual != attempted {
			err = io.ErrShortWrite
		}
	}()
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	writer.WriteHeader(status)
	actual, err = writer.Write(payload)
	return attempted, actual, err
}
