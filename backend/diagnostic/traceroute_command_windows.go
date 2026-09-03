//go:build windows

package diagnostic

import (
	"strconv"
	"time"
)

func traceExecutableName() string { return "tracert.exe" }

func traceCommandSpec(timeout time.Duration, address string) (string, []string) {
	timeoutMS := timeout.Milliseconds()
	if timeoutMS < 1 {
		timeoutMS = 1
	}
	if maxTimeoutMS := MaxTimeout.Milliseconds(); timeoutMS > maxTimeoutMS {
		timeoutMS = maxTimeoutMS
	}
	return traceExecutableName(), []string{"-d", "-w", strconv.FormatInt(timeoutMS, 10), "-h", strconv.Itoa(MaxTraceHops), address}
}
