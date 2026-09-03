//go:build !windows

package diagnostic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTracerouteExecutableAvailableUsesExactPlatformCommand(t *testing.T) {
	if name := traceExecutableName(); name != "traceroute" {
		t.Fatalf("Unix executable name=%q", name)
	}
	t.Setenv("PATH", t.TempDir())
	if TracerouteExecutableAvailable() {
		t.Fatal("reported traceroute available with an empty PATH")
	}

	directory := t.TempDir()
	executable := filepath.Join(directory, "traceroute")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	if !TracerouteExecutableAvailable() {
		t.Fatal("did not find exact traceroute executable")
	}

	if err := os.Rename(executable, filepath.Join(directory, "tracert.exe")); err != nil {
		t.Fatal(err)
	}
	if TracerouteExecutableAvailable() {
		t.Fatal("accepted Windows executable name on Unix")
	}
}
