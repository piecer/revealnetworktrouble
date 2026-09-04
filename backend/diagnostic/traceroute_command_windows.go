//go:build windows

package diagnostic

import (
	"strconv"
	"time"
)

func traceExecutableName() string { return "tracert.exe" }

func windowsTraceArguments(timeout time.Duration, address string) []string {
	timeoutMS := timeout.Milliseconds()
	if timeoutMS < 1 {
		timeoutMS = 1
	}
	if maxTimeoutMS := MaxTimeout.Milliseconds(); timeoutMS > maxTimeoutMS {
		timeoutMS = maxTimeoutMS
	}
	return []string{"-d", "-w", strconv.FormatInt(timeoutMS, 10), "-h", strconv.Itoa(MaxTraceHops), address}
}

func traceCommandGrammars() []traceCommandGrammar {
	return []traceCommandGrammar{{arguments: windowsTraceArguments}}
}

func traceCommandSpec(timeout time.Duration, address string) (string, []string) {
	return traceExecutableName(), windowsTraceArguments(timeout, address)
}
