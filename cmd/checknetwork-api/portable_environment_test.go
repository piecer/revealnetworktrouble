package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireComposeIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("CHECKNETWORK_RUN_COMPOSE_TESTS") != "1" {
		t.Skip("Compose configuration checks are opt-in; run make test-integration")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("integration checks require Docker with the Compose plugin: %v", err)
	}
}

func TestComposeIntegrationMakeTargetRunsExplicitChecks(t *testing.T) {
	command := exec.Command("make", "-n", "test-integration")
	command.Dir = "../.."
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "CHECKNETWORK_RUN_COMPOSE_TESTS=1 go test -count=1 ./cmd/checknetwork-api -run '^TestComposeResolved|^TestRealComposeCLI'") {
		t.Fatalf("test-integration must explicitly run the pure Compose contract and the real-CLI cross-check: %v\n%s", err, output)
	}
}

func TestComposeChecksAreOptionalWithoutDocker(t *testing.T) {
	t.Run("TestRealComposeCLIResolvesIdenticallyToPureResolver", func(t *testing.T) {
		// Only the cross-check depends on Docker; execute it with no Docker rather than
		// mocking LookPath. The pure contract itself must not be optional.
		command := exec.Command(os.Args[0], "-test.run=^TestRealComposeCLIResolvesIdenticallyToPureResolver$", "-test.v")
		command.Env = append(os.Environ(), "PATH="+t.TempDir(), "CHECKNETWORK_RUN_COMPOSE_TESTS=")
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "--- SKIP: TestRealComposeCLIResolvesIdenticallyToPureResolver") {
			t.Fatalf("optional cross-check must skip without Docker: %v\n%s", err, output)
		}
	})
}

func TestRequestedComposeChecksFailWithoutDocker(t *testing.T) {
	t.Run("TestRealComposeCLIResolvesIdenticallyToPureResolver", func(t *testing.T) {
		command := exec.Command(os.Args[0], "-test.run=^TestRealComposeCLIResolvesIdenticallyToPureResolver$", "-test.v")
		command.Env = append(os.Environ(), "PATH="+t.TempDir(), "CHECKNETWORK_RUN_COMPOSE_TESTS=1")
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "integration checks require Docker") {
			t.Fatalf("requested cross-check must fail with an actionable prerequisite error: %v\n%s", err, output)
		}
	})
}

func TestComposeContractIsEnforcedWithoutDocker(t *testing.T) {
	// The default suite must verify the Compose contract itself, not skip it.
	// A recording shim on PATH proves the contract tests never invoke Docker.
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "docker-invoked")
	shim := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$*\" >>\"$FAKE_DOCKER_MARKER\"\nexit 69\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "docker"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestComposeResolved", "-test.v")
	command.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "FAKE_DOCKER_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Compose contract must pass without Docker: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "--- SKIP: TestComposeResolved") {
		t.Fatalf("Compose contract must not skip without Docker:\n%s", output)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("Compose contract invoked the Docker CLI:\n%s", output)
	}
}
