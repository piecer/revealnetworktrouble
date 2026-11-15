package main

import (
	"os"
	"os/exec"
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
	if err != nil || !strings.Contains(string(output), "CHECKNETWORK_RUN_COMPOSE_TESTS=1 go test -count=1 ./cmd/checknetwork-api -run '^TestComposeResolved'") {
		t.Fatalf("test-integration must explicitly run the Compose contract tests: %v\n%s", err, output)
	}
}

func TestComposeChecksAreOptionalWithoutDocker(t *testing.T) {
	for _, name := range []string{
		"TestComposeResolvedExposureBoundary",
		"TestComposeResolvedBuildIdentityIsValidSharedAndOverrideable",
	} {
		t.Run(name, func(t *testing.T) {
			// Execute the actual tests with no Docker, not a mock of LookPath.
			command := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v")
			command.Env = append(os.Environ(), "PATH="+t.TempDir(), "CHECKNETWORK_RUN_COMPOSE_TESTS=")
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "--- SKIP: "+name) {
				t.Fatalf("optional Compose check must skip without Docker: %v\n%s", err, output)
			}
		})
	}
}

func TestRequestedComposeChecksFailWithoutDocker(t *testing.T) {
	for _, name := range []string{
		"TestComposeResolvedExposureBoundary",
		"TestComposeResolvedBuildIdentityIsValidSharedAndOverrideable",
	} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v")
			command.Env = append(os.Environ(), "PATH="+t.TempDir(), "CHECKNETWORK_RUN_COMPOSE_TESTS=1")
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "integration checks require Docker") {
				t.Fatalf("requested integration check must fail with an actionable prerequisite error: %v\n%s", err, output)
			}
		})
	}
}
