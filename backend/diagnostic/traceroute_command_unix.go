//go:build !windows

package diagnostic

import (
	"strconv"
	"time"
)

func traceExecutableName() string { return "traceroute" }

func traceCommandSpec(_ time.Duration, address string) (string, []string) {
	return traceExecutableName(), []string{"-n", "-q", "1", "-w", "2", "-m", strconv.Itoa(MaxTraceHops), address}
}
