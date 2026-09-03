package diagnostic

import "os/exec"

// TracerouteExecutableAvailable reports whether the exact platform traceroute
// command can be resolved. Callers that use this for readiness should cache the
// result during startup rather than probing PATH on each request.
func TracerouteExecutableAvailable() bool {
	_, err := exec.LookPath(traceExecutableName())
	return err == nil
}
