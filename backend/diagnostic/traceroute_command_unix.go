//go:build !windows

package diagnostic

import (
	"strconv"
	"time"
)

func traceCommandSpec(_ time.Duration, address string) (string, []string) {
	return "traceroute", []string{"-n", "-q", "1", "-w", "2", "-m", strconv.Itoa(MaxTraceHops), address}
}
