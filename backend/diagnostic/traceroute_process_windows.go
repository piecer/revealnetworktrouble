//go:build windows

package diagnostic

import (
	"context"
	"os/exec"
	"time"
)

func runTraceCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 200 * time.Millisecond
	return cmd.CombinedOutput()
}
