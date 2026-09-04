package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifyReleaseRealGateFakeSuite(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	cmd := exec.Command("python3", filepath.Join(repo, "scripts", "verify_release_real_test_test.py"))
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real release gate fake suite failed: %v\n%s", err, output)
	}
}
