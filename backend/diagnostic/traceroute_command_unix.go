//go:build !windows

package diagnostic

import (
	"strconv"
	"time"
)

func traceExecutableName() string { return "traceroute" }

func unixTraceArguments(numeric bool) func(time.Duration, string) []string {
	return func(_ time.Duration, address string) []string {
		args := make([]string, 0, 8)
		if numeric {
			args = append(args, "-n")
		}
		return append(args, "-q", "1", "-w", "2", "-m", strconv.Itoa(MaxTraceHops), address)
	}
}

func traceCommandGrammars() []traceCommandGrammar {
	return []traceCommandGrammar{
		{arguments: unixTraceArguments(true)},
		{arguments: unixTraceArguments(false)},
	}
}

func traceCommandSpec(timeout time.Duration, address string) (string, []string) {
	grammar := traceCommandGrammars()[0]
	return traceExecutableName(), grammar.arguments(timeout, address)
}
