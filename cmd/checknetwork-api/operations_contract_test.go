package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repositoryFile(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestCIIsFreshSelfContainedAndNonRecursive(t *testing.T) {
	makefile := repositoryFile(t, "Makefile")
	for _, required := range []string{
		"frontend-deps:",
		"npm --prefix frontend ci",
		"ci-inner: frontend-deps",
		"./scripts/ci-inner.sh",
		"ci: ci-inner",
		"ci-clean-archive:",
		"./scripts/ci-clean-archive.sh",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile contract missing %q", required)
		}
	}
	for _, script := range []string{"scripts/ci-inner.sh", "scripts/ci-clean-archive.sh"} {
		body := repositoryFile(t, script)
		if !strings.HasPrefix(body, "#!/bin/sh\nset -eu\n") {
			t.Errorf("%s must be strict POSIX sh", script)
		}
		command := exec.Command("sh", "-n", filepath.Join("..", "..", script))
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("sh -n %s: %v\n%s", script, err, output)
		}
		if strings.Contains(body, "$(MAKE)") {
			t.Errorf("%s contains a recursive make expansion", script)
		}
		if strings.Contains(body, "eval ") || strings.Contains(body, "eval	") {
			t.Errorf("%s must not evaluate constructed commands", script)
		}
	}

	inner := repositoryFile(t, "scripts/ci-inner.sh")
	for _, command := range []string{
		"go test -count=1 -json ./...",
		"npm --prefix frontend test",
		"npm --prefix frontend run test:syntax",
		"go test -count=1 -race ./...",
		"go vet ./...",
		"go build -trimpath",
		"./scripts/verify-android-wrapper.sh",
		"./scripts/verify-android-env.sh",
		":app:testDebugUnitTest",
		":app:testReleaseUnitTest",
		":app:lintDebug",
		":app:lintRelease",
		":app:assembleDebug",
		":app:assembleRelease",
		"CI_OK:",
	} {
		if !strings.Contains(inner, command) {
			t.Errorf("ci-inner missing %q", command)
		}
	}

	archive := repositoryFile(t, "scripts/ci-clean-archive.sh")
	for _, required := range []string{
		"git status --porcelain --untracked-files=all",
		"git archive",
		"make ci-inner",
		"node pass count",
		"Android debug test count",
		"Android release test count",
		"134",
		"original worktree HEAD changed during archive gate",
		"original worktree changed during archive gate",
	} {
		if !strings.Contains(archive, required) {
			t.Errorf("clean archive script missing %q", required)
		}
	}
	if strings.Count(archive, "make ci-inner") != 1 {
		t.Errorf("clean archive must invoke ci-inner exactly once")
	}
}

func TestDockerImagesUseNonRootAPIAndFixedNginxWorkers(t *testing.T) {
	apiDockerfile := repositoryFile(t, "Dockerfile")
	if !strings.Contains(apiDockerfile, "USER 65532:65532") {
		t.Errorf("API runtime image must declare the audited non-root UID/GID")
	}
	nginx := repositoryFile(t, "frontend/nginx.conf")
	if !strings.Contains(nginx, "worker_processes 2;") {
		t.Errorf("nginx must use exactly two workers")
	}
}

func TestComposeRuntimeContract(t *testing.T) {
	compose := repositoryFile(t, "compose.yaml")
	for _, required := range []string{
		"stop_grace_period: 6m",
		"restart: unless-stopped",
		"cpus: 2.0",
		"mem_limit: 512m",
		"pids_limit: 256",
		`http://127.0.0.1:8080/api/v1/health`,
		"cpus: 0.25",
		"mem_limit: 64m",
		"pids_limit: 64",
		`http://127.0.0.1/`,
		"condition: service_healthy",
	} {
		if !strings.Contains(compose, required) {
			t.Errorf("compose contract missing %q", required)
		}
	}
	if strings.Count(compose, "healthcheck:") != 2 {
		t.Errorf("healthcheck count=%d", strings.Count(compose, "healthcheck:"))
	}
	if strings.Count(compose, "restart: unless-stopped") != 2 {
		t.Errorf("restart count=%d", strings.Count(compose, "restart: unless-stopped"))
	}
	if strings.Count(compose, `wget", "--no-verbose", "--tries=1", "--spider"`) != 2 {
		t.Errorf("health commands must be exec-form wget --spider: %s", compose)
	}
}
