//go:build !windows

package diagnostic

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunTraceCommandKillsDescendantsHoldingOutputPipes(t *testing.T) {
	script := filepath.Join(t.TempDir(), "forking-traceroute")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n(sleep 1) &\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runTraceCommand(ctx, script)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if elapsed := time.Since(started); elapsed > 400*time.Millisecond {
		t.Fatalf("descendant held output pipes for %s", elapsed)
	}
}
