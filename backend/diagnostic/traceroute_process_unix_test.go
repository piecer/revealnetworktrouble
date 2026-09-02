//go:build !windows

package diagnostic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunTraceCommandBoundsOutputAndKillsDescendants(t *testing.T) {
	script := filepath.Join(t.TempDir(), "noisy-traceroute")
	body := fmt.Sprintf("#!/bin/sh\n(sleep 5) &\ndd if=/dev/zero bs=%d count=1 2>/dev/null\nwait\n", MaxTraceOutputBytes+1)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	output, err := runTraceCommand(context.Background(), script)
	if !errors.Is(err, ErrTraceOutputLimit) {
		t.Fatalf("error = %v, want %v", err, ErrTraceOutputLimit)
	}
	if len(output) != MaxTraceOutputBytes {
		t.Fatalf("output bytes = %d, want %d", len(output), MaxTraceOutputBytes)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("overflow did not promptly clean descendants and pipes: %s", elapsed)
	}
}

func TestRunTraceCommandNormalParentExitKillsChildAndGrandchildHoldingPipes(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "exiting-parent")
	childPIDFile := filepath.Join(dir, "child.pid")
	grandchildPIDFile := filepath.Join(dir, "grandchild.pid")
	body := `#!/bin/sh
sh -c 'echo $$ > "$1"; sh -c '\''echo $$ > "$1"; sleep 30'\'' sh "$2" & wait' sh "$1" "$2" &
while [ ! -s "$1" ] || [ ! -s "$2" ]; do sleep 0.01; done
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	_, runErr := runTraceCommand(context.Background(), script, childPIDFile, grandchildPIDFile)
	if !errors.Is(runErr, exec.ErrWaitDelay) {
		t.Fatalf("error = %v, want %v", runErr, exec.ErrWaitDelay)
	}

	for _, pidFile := range []string{childPIDFile, grandchildPIDFile} {
		pid := readTestPID(t, pidFile)
		defer syscall.Kill(pid, syscall.SIGKILL)
		deadline := time.Now().Add(time.Second)
		for processPIDExists(pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if processPIDExists(pid) {
			t.Fatalf("descendant PID %d from %s survived runTraceCommand", pid, filepath.Base(pidFile))
		}
	}
}

func readTestPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("invalid PID file %s: %v", path, err)
	}
	return pid
}

func processPIDExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func TestRunTraceCommandKillsDescendantsHoldingOutputPipes(t *testing.T) {
	script := filepath.Join(t.TempDir(), "forking-traceroute")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n(sleep 1) &\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runTraceCommand(ctx, script)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want %v", err, context.DeadlineExceeded)
	}
	if elapsed := time.Since(started); elapsed > 400*time.Millisecond {
		t.Fatalf("descendant held output pipes for %s", elapsed)
	}
}
