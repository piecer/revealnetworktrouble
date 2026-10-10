package api

import (
	"errors"
	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
	"io"
	"net/http"
)

func (s *Server) createIPContext(response responder, request *http.Request) {
	if request.Context().Err() != nil {
		observationFromRequest(request).status = 499
		observationFromRequest(request).outcome = TelemetryOutcomeCancel
		return
	}
	fail := func(key apiErrorKey) {
		if request.Context().Err() == nil {
			response.writeAPIError(key, apiErrorMetadata{RetryAfter: s.busyRetryAfter})
		} else {
			observationFromRequest(request).status = 499
			observationFromRequest(request).outcome = TelemetryOutcomeCancel
		}
	}
	select {
	case s.contextHandlers <- struct{}{}:
		observationFromRequest(request).contextAdmitted = true
	default:
		fail(apiErrorServerBusy)
		return
	}
	if request.ContentLength > 256 {
		fail(apiErrorRequestTooLarge)
		return
	}
	select {
	case s.bodyDecodes <- struct{}{}:
	default:
		fail(apiErrorBodyDecodeCapacity)
		return
	}
	var body []byte
	var err error
	func() {
		defer func() { <-s.bodyDecodes }()
		response.limitRequestBody(request, 256)
		body, err = io.ReadAll(request.Body)
	}()
	if err != nil {
		if isRequestTooLarge(err) {
			fail(apiErrorRequestTooLarge)
		} else {
			fail(apiErrorInvalidJSON)
		}
		return
	}
	address, parseErr := diagnostic.ParseIPContextRequest(body)
	if parseErr != nil {
		if errors.Is(parseErr, diagnostic.ErrIPContextJSON) {
			fail(apiErrorInvalidJSON)
		} else {
			fail(apiErrorInvalidRequest)
		}
		return
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		fail(apiErrorInvalidRequest)
		return
	}
	if s.ipContext == nil {
		fail(apiErrorServerBusy)
		return
	}
	valueContext, lookupErr := s.ipContext.Lookup(request.Context(), address, observationFromRequest(request).started)
	if request.Context().Err() != nil {
		observationFromRequest(request).status = 499
		observationFromRequest(request).outcome = TelemetryOutcomeCancel
		return
	}
	if lookupErr != nil {
		if errors.Is(lookupErr, diagnostic.ErrIPContextBusy) {
			fail(apiErrorServerBusy)
			return
		}
		fail(apiErrorInternal)
		return
	}
	payload, marshalErr := diagnostic.MarshalIPContext(valueContext)
	if marshalErr != nil {
		fail(apiErrorSerialization)
		return
	}
	select {
	case s.responseWrites <- struct{}{}:
	default:
		fail(apiErrorWriteCapacity)
		return
	}
	defer func() { <-s.responseWrites }()
	response.writeIPContext(ipContextJSON{payload: payload})
}
