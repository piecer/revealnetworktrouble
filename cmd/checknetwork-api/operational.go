package main

import (
	"net/http"
	"strconv"
	"sync/atomic"
)

type operationalStatus string
type operationalReason string

const (
	operationalReady    operationalStatus = "ready"
	operationalNotReady operationalStatus = "not_ready"

	liveBody              = "{\"status\":\"live\"}\n"
	readyBody             = "{\"status\":\"ready\"}\n"
	startingBody          = "{\"status\":\"not_ready\",\"reason\":\"starting\"}\n"
	drainingBody          = "{\"status\":\"not_ready\",\"reason\":\"draining\"}\n"
	tracerouteMissingBody = "{\"status\":\"not_ready\",\"reason\":\"traceroute_unavailable\"}\n"
	businessDrainingBody  = "{\"status\":\"unavailable\",\"reason\":\"draining\"}\n"

	reasonStarting              operationalReason = "starting"
	reasonDraining              operationalReason = "draining"
	reasonTracerouteUnavailable operationalReason = "traceroute_unavailable"

	operationalPhaseStarting  uint32 = 0
	operationalPhaseAccepting uint32 = 1
	operationalPhaseDraining  uint32 = 2
)

type operationalSnapshot struct {
	Status operationalStatus
	Reason operationalReason
}

// operationalState contains only process-owned, administrative readiness state.
// Transient connection, report, body, response, or checker saturation is not a
// readiness signal and therefore is deliberately absent.
type operationalState struct {
	phase               atomic.Uint32
	tracerouteAvailable bool
}

func newOperationalState(tracerouteAvailable bool) *operationalState {
	return &operationalState{tracerouteAvailable: tracerouteAvailable}
}

// MarkAccepting performs the sole startup transition. It cannot reopen a state
// that has already begun draining.
func (state *operationalState) MarkAccepting() {
	state.phase.CompareAndSwap(operationalPhaseStarting, operationalPhaseAccepting)
}

// BeginDrain permanently closes business admission before shutdown begins.
func (state *operationalState) BeginDrain() {
	state.phase.Store(operationalPhaseDraining)
}

func (state *operationalState) Snapshot() operationalSnapshot {
	switch state.phase.Load() {
	case operationalPhaseStarting:
		return operationalSnapshot{Status: operationalNotReady, Reason: reasonStarting}
	case operationalPhaseDraining:
		return operationalSnapshot{Status: operationalNotReady, Reason: reasonDraining}
	default:
		if !state.tracerouteAvailable {
			return operationalSnapshot{Status: operationalNotReady, Reason: reasonTracerouteUnavailable}
		}
		return operationalSnapshot{Status: operationalReady}
	}
}

type operationalHandler struct {
	state    *operationalState
	business http.Handler
}

func newOperationalHandler(state *operationalState, business http.Handler) http.Handler {
	if state == nil {
		panic("operational state is required")
	}
	if business == nil {
		business = http.NotFoundHandler()
	}
	return &operationalHandler{state: state, business: business}
}

func (handler *operationalHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	exactPath := request.URL.EscapedPath() == path
	if exactPath && (path == "/livez" || path == "/readyz") {
		handler.serveOperational(writer, request, path)
		return
	}
	if handler.state.phase.Load() == operationalPhaseDraining {
		writeOperationalJSON(writer, request.Method, http.StatusServiceUnavailable, businessDrainingBody)
		return
	}
	handler.business.ServeHTTP(writer, request)
}

func (handler *operationalHandler) serveOperational(writer http.ResponseWriter, request *http.Request, path string) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		writeOperationalJSON(writer, request.Method, http.StatusMethodNotAllowed, "{\"status\":\"method_not_allowed\"}\n")
		return
	}
	if path == "/livez" {
		writeOperationalJSON(writer, request.Method, http.StatusOK, liveBody)
		return
	}

	snapshot := handler.state.Snapshot()
	if snapshot.Status == operationalReady {
		writeOperationalJSON(writer, request.Method, http.StatusOK, readyBody)
		return
	}
	body := startingBody
	switch snapshot.Reason {
	case reasonDraining:
		body = drainingBody
	case reasonTracerouteUnavailable:
		body = tracerouteMissingBody
	}
	writeOperationalJSON(writer, request.Method, http.StatusServiceUnavailable, body)
}

func writeOperationalJSON(writer http.ResponseWriter, method string, status int, body string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	if method != http.MethodHead {
		_, _ = writer.Write([]byte(body))
	}
}
