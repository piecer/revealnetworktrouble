package diagnostic

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

const (
	tracerouteProbeTimeout     = 2 * time.Second
	tracerouteProbeOutputBytes = 32 << 10
	tracerouteProbeAddress     = "127.0.0.1"
)

var ErrTracerouteUnavailable = errors.New("no functional traceroute command grammar available")

type traceCommandGrammar struct {
	arguments func(time.Duration, string) []string
}

// TracerouteCapability is the immutable executable path and argument grammar
// selected by a bounded startup loopback probe.
type TracerouteCapability struct {
	executable string
	grammar    traceCommandGrammar
}

func (capability *TracerouteCapability) commandSpec(timeout time.Duration, address string) (string, []string) {
	if capability == nil || capability.grammar.arguments == nil {
		return "", nil
	}
	return capability.executable, capability.grammar.arguments(timeout, address)
}

// ProbeTracerouteCapability tries only the platform's fixed argument grammars
// against IPv4 loopback, under one total deadline and a bounded combined output.
func ProbeTracerouteCapability() (*TracerouteCapability, error) {
	executable, err := exec.LookPath(traceExecutableName())
	if err != nil {
		return nil, errors.Join(ErrTracerouteUnavailable, err)
	}

	probeCtx, cancel := context.WithTimeout(context.Background(), tracerouteProbeTimeout)
	defer cancel()
	for _, grammar := range traceCommandGrammars() {
		args := grammar.arguments(tracerouteProbeTimeout, tracerouteProbeAddress)
		output, runErr := runTraceCommandWithOutputLimit(probeCtx, tracerouteProbeOutputBytes, executable, args...)
		if runErr != nil {
			continue
		}
		topology, parseErr := parseTraceroute(string(output), tracerouteProbeAddress)
		if parseErr == nil && topology.Reached {
			return &TracerouteCapability{executable: executable, grammar: grammar}, nil
		}
	}
	return nil, ErrTracerouteUnavailable
}

// TracerouteExecutableAvailable reports whether a bounded startup loopback
// probe can select a functional platform command grammar. Callers should retain
// the returned capability from ProbeTracerouteCapability when they also execute
// traceroute, rather than probing again.
func TracerouteExecutableAvailable() bool {
	_, err := ProbeTracerouteCapability()
	return err == nil
}
