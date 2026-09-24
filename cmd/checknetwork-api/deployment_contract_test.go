package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Collect the fake ownership regressions without requiring a Docker daemon.
func TestDeploymentProbeOwnership(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", filepath.Join(repo, "scripts", "test_deployment_ownership.py"), "-v")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("deployment probe ownership suite: %v\n%s", err, output)
	}
}
