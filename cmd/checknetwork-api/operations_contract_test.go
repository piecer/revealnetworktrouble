package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		`CHECKNETWORK_MODE: ${CHECKNETWORK_MODE:-trusted-local}`,
		`CHECKNETWORK_API_KEY: ${CHECKNETWORK_API_KEY:-}`,
		`CHECKNETWORK_RATE_LIMIT_PER_MINUTE: ${CHECKNETWORK_RATE_LIMIT_PER_MINUTE:-}`,
		`VERSION: ${CHECKNETWORK_VERSION:-dev}`,
		`REVISION: ${CHECKNETWORK_REVISION:-dev}`,
		`SOURCE_DATE_EPOCH: ${SOURCE_DATE_EPOCH:-0}`,
		`http://127.0.0.1:8080/readyz`,
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
	if strings.Contains(compose, "Authorization") || strings.Contains(compose, "Bearer ") {
		t.Errorf("healthcheck must not carry credentials: %s", compose)
	}
}

func TestReleaseDockerfileIsImmutableAndCarriesExactIdentity(t *testing.T) {
	dockerfile := repositoryFile(t, "Dockerfile")
	fromDigest := regexp.MustCompile(`(?m)^FROM [^[:space:]]+@sha256:[0-9a-f]{64}( AS (build|traceroute))?$`)
	if matches := fromDigest.FindAllString(dockerfile, -1); len(matches) != 3 {
		t.Fatalf("Dockerfile must pin builder, traceroute extraction, and runtime FROM images by digest, got %d: %s", len(matches), dockerfile)
	}
	for _, forbidden := range []string{"apk update", "apk add", "apt-get", "curl ", "wget http", "git clone"} {
		if strings.Contains(dockerfile, forbidden) {
			t.Fatalf("Dockerfile contains mutable network/package input %q", forbidden)
		}
	}
	for _, required := range []string{
		"ARG VERSION", "ARG REVISION", "ARG SOURCE_DATE_EPOCH", "CGO_ENABLED=0", "-trimpath", "-buildid=",
		"-X main.version=${VERSION}", "-X main.revision=${REVISION}",
		"org.opencontainers.image.version=$VERSION", "org.opencontainers.image.revision=$REVISION",
		"touch -d \"@${SOURCE_DATE_EPOCH}\" /checknetwork-api",
		"test \"$(go version -m /checknetwork-api | sed -n '1p')\" = '/checknetwork-api: go1.22.12'",
		"ADD --checksum=sha256:0409f348956dc74b83701cf48294f58ab6424e6f717e0f912687bb89d09026de https://dl-cdn.alpinelinux.org/alpine/v3.20/community/x86_64/traceroute-2.1.5-r0.apk /tmp/traceroute.apk",
		"tar -xzf /tmp/traceroute.apk -C / usr/bin/traceroute", "rm -f /tmp/traceroute.apk",
		"AS traceroute", "COPY --from=traceroute /usr/bin/traceroute /traceroute",
		"ENV PATH=\"/:${PATH}\"", "test -x /traceroute",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile release contract missing %q", required)
		}
	}
}

func TestReleaseVerifierPublishesOnlyTheVerifiedImageBinary(t *testing.T) {
	script := repositoryFile(t, "scripts/verify-release.sh")
	for _, required := range []string{
		"docker create --name", "docker cp", ":/checknetwork-api", "checknetwork-api-image-1", "checknetwork-api-image-2",
		"binary reproducibility mismatch", "cp \"$tmp/checknetwork-api-image-1\" \"$publish_tmp\"",
		"mv -f \"$publish_tmp\" \"$release_output\"", "working tree changed during release verification",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("image binary publication contract missing %q", required)
		}
	}
	if strings.Contains(script, " go build ") || strings.Contains(script, "\ngo build ") {
		t.Fatal("release verifier must not publish a separately host-built binary")
	}
}

func TestReleaseVerifierRunsBoundedFunctionalTraceroute(t *testing.T) {
	script := repositoryFile(t, "scripts/verify-release.sh")
	if !strings.Contains(script, "traceroute -n -m 1 -w 1 127.0.0.1") {
		t.Fatal("release smoke must execute a bounded loopback traceroute")
	}
	if !strings.Contains(script, "grep -F '127.0.0.1'") {
		t.Fatal("release smoke must verify traceroute reached loopback")
	}
}

func TestReleaseVerifierAndMakeTargetsAreStrict(t *testing.T) {
	makefile := repositoryFile(t, "Makefile")
	for _, required := range []string{"release:", "verify-release:", "./scripts/verify-release.sh"} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile release contract missing %q", required)
		}
	}
	script := repositoryFile(t, "scripts/verify-release.sh")
	if !strings.HasPrefix(script, "#!/bin/sh\nset -eu\n") {
		t.Fatal("release verifier must be strict POSIX sh")
	}
	for _, required := range []string{
		"usage: %s EXACT_HEAD_SHA [GIT_ARCHIVE_TAR]", "git status --porcelain --untracked-files=all",
		"git ls-files --error-unmatch -- VERSION", "git show \"$revision:VERSION\"", "cmp -s - VERSION",
		"git get-tar-commit-id", "git archive \"$revision\"", "cmp -s \"$archive_tar\" \"$canonical_archive\"",
		"tar -xf \"$canonical_archive\"", "archive differs from canonical git archive", "SOURCE_DATE_EPOCH",
		"for pass in 1 2", "sha256sum", "docker info", "DOCKER_BUILDKIT=1 docker build", "--no-cache", "--provenance=false", "docker image inspect", "RootFS.Layers",
		"org.opencontainers.image.version", "org.opencontainers.image.revision", "test -x /traceroute", "/livez", "/readyz", "/api/v1/health",
		"trap cleanup EXIT", "trap 'handle_signal 129' HUP", "trap 'handle_signal 130' INT", "trap 'handle_signal 143' TERM",
		"docker container rm -f --", "docker image rm -f --", "docker network rm --",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("release verifier missing %q", required)
		}
	}
	if strings.Contains(script, "eval ") || strings.Contains(script, "eval	") {
		t.Fatal("release verifier must not use eval")
	}
	for _, forbidden := range []string{"CHECKNETWORK_RELEASE_ARCHIVE_SHA", "CHECKNETWORK_RELEASE_SOURCE_DATE_EPOCH"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("release verifier trusts ambient archive provenance %q", forbidden)
		}
	}
	archive := repositoryFile(t, "scripts/ci-clean-archive.sh")
	if strings.Count(archive, "./scripts/verify-release.sh") != 1 {
		t.Fatalf("clean archive must invoke release verifier exactly once")
	}
	if !strings.Contains(archive, "./scripts/verify-release.sh \"$requested_sha\" \"$tmp/source.tar\"") {
		t.Fatal("clean archive must invoke the verifier from the original checkout with its git archive")
	}
}

func TestReleaseVerifierRejectsMalformedVersionSHAAndDirtyTree(t *testing.T) {
	temp := t.TempDir()
	if err := os.Mkdir(filepath.Join(temp, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "verify-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temp, "scripts", "verify-release.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temp, "VERSION"), []byte("0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		command := exec.Command("sh", append([]string{"scripts/verify-release.sh"}, args...)...)
		command.Dir = temp
		output, runErr := command.CombinedOutput()
		return string(output), runErr
	}
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = temp
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if output, gitErr := command.CombinedOutput(); gitErr != nil {
			t.Fatalf("git %v: %v\n%s", args, gitErr, output)
		}
	}
	git("init", "-q")
	git("add", "VERSION", "scripts/verify-release.sh")
	git("commit", "-qm", "fixture")
	headCommand := exec.Command("git", "rev-parse", "HEAD")
	headCommand.Dir = temp
	headBytes, err := headCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))

	if output, runErr := run("-bad"); runErr == nil || !strings.Contains(output, "40 lowercase hexadecimal") {
		t.Fatalf("option-like SHA was not rejected safely: err=%v output=%s", runErr, output)
	}
	if output, runErr := run(strings.Repeat("0", 40)); runErr == nil || !strings.Contains(output, "resolve exactly") {
		t.Fatalf("mismatched SHA was not rejected: err=%v output=%s", runErr, output)
	}
	if err := os.WriteFile(filepath.Join(temp, "VERSION"), []byte("v1;touch INJECTED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, runErr := run(head); runErr == nil || !strings.Contains(output, "working tree is not clean") {
		t.Fatalf("modified malformed VERSION was not rejected: err=%v output=%s", runErr, output)
	}
	if _, err := os.Stat(filepath.Join(temp, "INJECTED")); !os.IsNotExist(err) {
		t.Fatal("malformed VERSION was interpreted as shell input")
	}
	if err := os.Remove(filepath.Join(temp, "VERSION")); err != nil {
		t.Fatal(err)
	}
	if output, runErr := run(head); runErr == nil || !strings.Contains(output, "working tree is not clean") {
		t.Fatalf("missing VERSION was not rejected: err=%v output=%s", runErr, output)
	}
	if err := os.WriteFile(filepath.Join(temp, "VERSION"), []byte("0.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, runErr := run(head); runErr == nil || !strings.Contains(output, "working tree is not clean") {
		t.Fatalf("tracked dirty tree was not rejected: err=%v output=%s", runErr, output)
	}
	if err := os.WriteFile(filepath.Join(temp, "VERSION"), []byte("0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temp, "untracked"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, runErr := run(head); runErr == nil || !strings.Contains(output, "working tree is not clean") {
		t.Fatalf("dirty tree was not rejected: err=%v output=%s", runErr, output)
	}
}

func TestReleaseVerifierRejectsIgnoredUntrackedVersion(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "verify-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "verify-release.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("VERSION\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeGo := "#!/bin/sh\nset -eu\nout=\nwhile [ \"$#\" -gt 0 ]; do [ \"$1\" = -o ] && { shift; out=$1; }; shift; done\nprintf one >\"$out\"\n"
	fakeDocker := `#!/bin/sh
set -eu
case "$*" in
  info) ;;
  *"{{.Id}}"*) printf 'sha256:same\n' ;;
  *"RootFS.Layers"*) printf '["sha256:root"]\n' ;;
  *"org.opencontainers.image.version"*) printf '9.9.9\n' ;;
  *"org.opencontainers.image.revision"*) printf '%s\n' "$FAKE_REVISION" ;;
  *"/readyz") printf '{"status":"ready"}\n' ;;
  *"/livez") printf '{"status":"live"}\n' ;;
  *"/api/v1/health") printf '{"status":"ok","version":"9.9.9","revision":"%s"}\n' "$FAKE_REVISION" ;;
  logs*) printf '{"msg":"server started","version":"9.9.9","revision":"%s"}\n' "$FAKE_REVISION" ;;
esac
`
	for name, body := range map[string]string{"go": fakeGo, "docker": fakeDocker} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", ".gitignore", "scripts/verify-release.sh"}, {"commit", "-qm", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir, command.Env = repo, gitEnv
		if output, gitErr := command.CombinedOutput(); gitErr != nil {
			t.Fatalf("git %v: %v\n%s", args, gitErr, output)
		}
	}
	headBytes, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	command := exec.Command("sh", "scripts/verify-release.sh", head)
	command.Dir = repo
	command.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "FAKE_REVISION="+head)
	output, runErr := command.CombinedOutput()
	if runErr == nil || !strings.Contains(string(output), "VERSION must be tracked") {
		t.Fatalf("ignored untracked VERSION was trusted: err=%v output=%s", runErr, output)
	}
}

func TestReleaseVerifierRejectsStandaloneTarDespiteAmbientClaims(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VERSION", "scripts/verify-release.sh"} {
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(repo, name), body, mode); err != nil {
			t.Fatal(err)
		}
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "scripts/verify-release.sh"}, {"commit", "-qm", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir, command.Env = repo, gitEnv
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	headBytes, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	standaloneTar := filepath.Join(root, "standalone.tar")
	tarCommand := exec.Command("tar", "-cf", standaloneTar, "VERSION")
	tarCommand.Dir = repo
	if output, err := tarCommand.CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, output)
	}
	command := exec.Command("sh", "scripts/verify-release.sh", head, standaloneTar)
	command.Dir = repo
	command.Env = append(os.Environ(), "CHECKNETWORK_RELEASE_ARCHIVE_SHA="+head, "CHECKNETWORK_RELEASE_SOURCE_DATE_EPOCH=1")
	output, runErr := command.CombinedOutput()
	if runErr == nil || !strings.Contains(string(output), "archive has no valid Git commit ID") {
		t.Fatalf("standalone tar bypassed archive provenance: err=%v output=%s", runErr, output)
	}
}

func TestReleaseVerifierRejectsMutatedGitArchiveBeforeDocker(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VERSION", "scripts/verify-release.sh"} {
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(repo, name), body, mode); err != nil {
			t.Fatal(err)
		}
	}
	const originalMember = "ARCHIVE_MEMBER_ORIGINAL\n"
	if err := os.WriteFile(filepath.Join(repo, "README.fixture"), []byte(originalMember), 0o644); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(root, "docker-called")
	fakeDocker := "#!/bin/sh\nset -eu\n: >\"$FAKE_DOCKER_CANARY\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "README.fixture", "scripts/verify-release.sh"}, {"commit", "-qm", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir, command.Env = repo, gitEnv
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	headBytes, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	archiveCommand := exec.Command("git", "-C", repo, "archive", head)
	archiveBytes, err := archiveCommand.Output()
	if err != nil {
		t.Fatalf("git archive: %v", err)
	}
	needle := []byte(originalMember)
	if count := bytes.Count(archiveBytes, needle); count != 1 {
		t.Fatalf("fixture member payload count=%d, want 1", count)
	}
	memberOffset := bytes.Index(archiveBytes, needle)
	archiveBytes[memberOffset] = 'X'
	commitCommand := exec.Command("git", "get-tar-commit-id")
	commitCommand.Stdin = bytes.NewReader(archiveBytes)
	archiveHead, err := commitCommand.Output()
	if err != nil {
		t.Fatalf("mutated genuine archive lost pax commit comment: %v", err)
	}
	if got := strings.TrimSpace(string(archiveHead)); got != head {
		t.Fatalf("mutated archive commit ID=%q, want exact HEAD %q", got, head)
	}
	archivePath := filepath.Join(root, "mutated-source.tar")
	if err := os.WriteFile(archivePath, archiveBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("sh", "scripts/verify-release.sh", head, archivePath)
	command.Dir = repo
	command.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "FAKE_DOCKER_CANARY="+canary)
	output, runErr := command.CombinedOutput()
	_, statErr := os.Stat(canary)
	dockerInvoked := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		t.Fatalf("inspect Docker canary: %v", statErr)
	}
	if runErr == nil || !strings.Contains(string(output), "archive differs from canonical git archive") || dockerInvoked {
		t.Fatalf("mutated archive rejection: err=%v dockerInvoked=%t output=%s", runErr, dockerInvoked, output)
	}
}

func TestReleaseVerifierStandaloneUsesCanonicalTreeAndRejectsCheckoutMutation(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VERSION", "scripts/verify-release.sh"} {
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(repo, name), body, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "payload"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeDocker := `#!/bin/sh
set -eu
printf 'PWD=%s ARGS=%s\n' "$PWD" "$*" >>"$FAKE_DOCKER_LOG"
case "$*" in
  info) if [ "${FAKE_MUTATE_CHECKOUT:-}" = true ]; then printf 'mutated\n' >"$FAKE_REPO/payload"; fi ;;
  build*) test "$PWD" != "$FAKE_REPO"; test "$(cat payload)" = original ;;
  cp*) destination=
       for argument in "$@"; do destination=$argument; done
       printf one >"$destination" ;;
  *"{{.Id}}"*) printf 'sha256:same\n' ;;
  *"RootFS.Layers"*) printf '["sha256:root"]\n' ;;
  *"org.opencontainers.image.version"*) printf '0.1.0\n' ;;
  *"org.opencontainers.image.revision"*) printf '%s\n' "$FAKE_REVISION" ;;
  *"/readyz") printf '{"status":"ready"}\n' ;;
  *"/livez") printf '{"status":"live"}\n' ;;
  *"/api/v1/health") printf '{"status":"ok","version":"0.1.0","revision":"%s"}\n' "$FAKE_REVISION" ;;
  *"traceroute -n -m 1 -w 1 127.0.0.1") printf '1  127.0.0.1  0.01 ms\n' ;;
  logs*) printf '{"msg":"server started","version":"0.1.0","revision":"%s"}\n' "$FAKE_REVISION" ;;
esac
`
	if err := os.WriteFile(filepath.Join(fakeBin, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "payload", "scripts/verify-release.sh"}, {"commit", "-qm", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir, command.Env = repo, gitEnv
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	headBytes, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	logPath := filepath.Join(root, "docker.log")
	run := func(outputPath string, mutate bool) (string, string, error) {
		_ = os.Remove(logPath)
		command := exec.Command("sh", "scripts/verify-release.sh", head)
		command.Dir = repo
		command.Env = append(os.Environ(),
			"PATH="+fakeBin+":"+os.Getenv("PATH"),
			"FAKE_DOCKER_LOG="+logPath,
			"FAKE_REPO="+repo,
			"FAKE_REVISION="+head,
			"FAKE_MUTATE_CHECKOUT="+map[bool]string{false: "false", true: "true"}[mutate],
			"CHECKNETWORK_RELEASE_OUTPUT="+outputPath,
		)
		output, runErr := command.CombinedOutput()
		calls, readErr := os.ReadFile(logPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(output), string(calls), runErr
	}

	cleanOutput := filepath.Join(root, "clean-api")
	output, calls, runErr := run(cleanOutput, false)
	if runErr != nil {
		t.Fatalf("clean standalone release failed: %v\n%s\n%s", runErr, output, calls)
	}
	if strings.Contains(calls, "PWD="+repo+" ARGS=build --no-cache") {
		t.Fatalf("standalone release built the mutable checkout:\n%s", calls)
	}
	if built, err := os.ReadFile(cleanOutput); err != nil || string(built) != "one" {
		t.Fatalf("clean standalone output=%q err=%v", built, err)
	}

	existingOutput := filepath.Join(root, "existing-api")
	if err := os.WriteFile(existingOutput, []byte("keep"), 0o751); err != nil {
		t.Fatal(err)
	}
	output, calls, runErr = run(existingOutput, true)
	if runErr == nil || !strings.Contains(output, "working tree changed during release verification") {
		t.Fatalf("checkout mutation was not rejected before publication: err=%v\n%s\n%s", runErr, output, calls)
	}
	if built, err := os.ReadFile(existingOutput); err != nil || string(built) != "keep" {
		t.Fatalf("failed release overwrote existing output=%q err=%v", built, err)
	}
	info, err := os.Stat(existingOutput)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o751 {
		t.Fatalf("failed release changed existing output mode=%v", info.Mode().Perm())
	}
	if err := os.WriteFile(filepath.Join(repo, "payload"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	absentOutput := filepath.Join(root, "absent-api")
	output, _, runErr = run(absentOutput, true)
	if runErr == nil || !strings.Contains(output, "working tree changed during release verification") {
		t.Fatalf("second checkout mutation was not rejected: err=%v output=%s", runErr, output)
	}
	if _, err := os.Stat(absentOutput); !os.IsNotExist(err) {
		t.Fatalf("failed release published output: %v", err)
	}
}

func TestReleaseVerifierPublicationMutationPreservesExistingOutput(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VERSION", "scripts/verify-release.sh"} {
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(repo, name), body, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "payload"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeDocker := `#!/bin/sh
set -eu
case "$*" in
  info) ;;
  cp*) destination=
       for argument in "$@"; do destination=$argument; done
       printf one >"$destination" ;;
  *"{{.Id}}"*) printf 'sha256:same\n' ;;
  *"RootFS.Layers"*) printf '["sha256:root"]\n' ;;
  *"org.opencontainers.image.version"*) printf '0.1.0\n' ;;
  *"org.opencontainers.image.revision"*) printf '%s\n' "$FAKE_REVISION" ;;
  *"/readyz") printf '{"status":"ready"}\n' ;;
  *"/livez") printf '{"status":"live"}\n' ;;
  *"/api/v1/health") printf '{"status":"ok","version":"0.1.0","revision":"%s"}\n' "$FAKE_REVISION" ;;
  *"traceroute -n -m 1 -w 1 127.0.0.1") printf '1  127.0.0.1  0.01 ms\n' ;;
  logs*) printf '{"msg":"server started","version":"0.1.0","revision":"%s"}\n' "$FAKE_REVISION" ;;
esac
`
	fakeCP := `#!/bin/sh
set -eu
for argument in "$@"; do
    case "$argument" in
        */checknetwork-api-image-1)
            printf 'mutated by cp\n' >"$FAKE_REPO/payload"
            break
            ;;
    esac
done
exec "$REAL_CP" "$@"
`
	fakeMV := `#!/bin/sh
set -eu
if [ ! -e "$FAKE_MV_ONCE" ]; then
    : >"$FAKE_MV_ONCE"
    if [ "$FAKE_MUTATION_POINT" = mv-before ]; then
        printf 'mutated before mv\n' >"$FAKE_REPO/payload"
    fi
    "$REAL_MV" "$@"
    if [ "$FAKE_MUTATION_POINT" = mv-after ]; then
        printf 'mutated after mv\n' >"$FAKE_REPO/payload"
    fi
    exit 0
fi
exec "$REAL_MV" "$@"
`
	for name, body := range map[string]string{"docker": fakeDocker, "cp": fakeCP, "mv": fakeMV} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	realCP, err := exec.LookPath("cp")
	if err != nil {
		t.Fatal(err)
	}
	realMV, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "payload", "scripts/verify-release.sh"}, {"commit", "-qm", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir, command.Env = repo, gitEnv
		if output, gitErr := command.CombinedOutput(); gitErr != nil {
			t.Fatalf("git %v: %v\n%s", args, gitErr, output)
		}
	}
	headBytes, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))

	cases := []struct {
		name          string
		mutationPoint string
		existing      bool
	}{
		{name: "cp-existing", mutationPoint: "cp", existing: true},
		{name: "mv-before-existing", mutationPoint: "mv-before", existing: true},
		{name: "mv-after-existing", mutationPoint: "mv-after", existing: true},
		{name: "mv-before-absent", mutationPoint: "mv-before", existing: false},
		{name: "mv-after-absent", mutationPoint: "mv-after", existing: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(repo, "payload"), []byte("original\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			outputPath := filepath.Join(root, "api-"+testCase.name)
			if testCase.existing {
				if err := os.WriteFile(outputPath, []byte("keep"), 0o751); err != nil {
					t.Fatal(err)
				}
			}
			mvOnce := filepath.Join(root, "mv-once-"+testCase.name)
			command := exec.Command("sh", "scripts/verify-release.sh", head)
			command.Dir = repo
			command.Env = append(os.Environ(),
				"PATH="+fakeBin+":"+os.Getenv("PATH"),
				"FAKE_REPO="+repo,
				"FAKE_REVISION="+head,
				"FAKE_MUTATION_POINT="+testCase.mutationPoint,
				"FAKE_MV_ONCE="+mvOnce,
				"REAL_CP="+realCP,
				"REAL_MV="+realMV,
				"CHECKNETWORK_RELEASE_OUTPUT="+outputPath,
			)
			output, runErr := command.CombinedOutput()
			if runErr == nil || !strings.Contains(string(output), "working tree changed during release verification") {
				t.Fatalf("publication mutation was not rejected: err=%v output=%s", runErr, output)
			}
			if strings.Contains(string(output), "release verified:") {
				t.Fatalf("failed release printed success: %s", output)
			}
			if testCase.existing {
				contents, err := os.ReadFile(outputPath)
				if err != nil || string(contents) != "keep" {
					t.Fatalf("existing output bytes=%q err=%v", contents, err)
				}
				info, err := os.Lstat(outputPath)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o751 {
					t.Fatalf("existing output mode=%v, want 0751", info.Mode().Perm())
				}
			} else if _, err := os.Lstat(outputPath); !os.IsNotExist(err) {
				t.Fatalf("new output was not removed after failed acceptance: %v", err)
			}
			leftovers, err := filepath.Glob(filepath.Join(root, ".checknetwork-release-*.??????"))
			if err != nil {
				t.Fatal(err)
			}
			if len(leftovers) != 0 {
				t.Fatalf("publication left atomic temporary files: %v", leftovers)
			}
		})
	}
}

type releaseSignalFixture struct {
	root    string
	repo    string
	fakeBin string
	head    string
	realCP  string
	realMV  string
}

func newReleaseSignalFixture(t *testing.T) releaseSignalFixture {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VERSION", "scripts/verify-release.sh"} {
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(repo, name), body, mode); err != nil {
			t.Fatal(err)
		}
	}
	fakeDocker := `#!/bin/sh
set -eu
case "$*" in
  info) ;;
  cp*) destination=
       for argument in "$@"; do destination=$argument; done
       printf canonical-image-binary >"$destination" ;;
  *"{{.Id}}"*) printf 'sha256:same\n' ;;
  *"RootFS.Layers"*) printf '["sha256:root"]\n' ;;
  *"org.opencontainers.image.version"*) printf '0.1.0\n' ;;
  *"org.opencontainers.image.revision"*) printf '%s\n' "$FAKE_REVISION" ;;
  *"/readyz") printf '{"status":"ready"}\n' ;;
  *"/livez") printf '{"status":"live"}\n' ;;
  *"/api/v1/health") printf '{"status":"ok","version":"0.1.0","revision":"%s"}\n' "$FAKE_REVISION" ;;
  *"traceroute -n -m 1 -w 1 127.0.0.1") printf '1  127.0.0.1  0.01 ms\n' ;;
  logs*) printf '{"msg":"server started","version":"0.1.0","revision":"%s"}\n' "$FAKE_REVISION" ;;
esac
`
	fakeCP := `#!/bin/sh
set -eu
"$REAL_CP" "$@"
source_path=
destination=
for argument in "$@"; do
    [ "$argument" = "-p" ] || { source_path=$destination; destination=$argument; }
done
case "$source_path:$destination:$FAKE_SIGNAL_POINT" in
    */checknetwork-api-image-1:*/.checknetwork-release-output.*:copy)
        kill -s "$FAKE_SIGNAL" "$PPID"
        ;;
esac
`
	fakeMV := `#!/bin/sh
set -eu
source_path=
destination=
for argument in "$@"; do
    case "$argument" in
        -f|--) ;;
        *) source_path=$destination; destination=$argument ;;
    esac
done
case "$source_path:$destination" in
    */.checknetwork-release-output.*:"$CHECKNETWORK_RELEASE_OUTPUT")
        if [ "$FAKE_SIGNAL_POINT" = pre-mv ]; then
            kill -s "$FAKE_SIGNAL" "$PPID"
            exit 0
        fi
        "$REAL_MV" "$@"
        if [ "$FAKE_SIGNAL_POINT" = post-mv ]; then
            if [ "${FAKE_HOSTILE_DESTINATION:-false}" = true ]; then
                rm -f -- "$CHECKNETWORK_RELEASE_OUTPUT"
                mkdir -- "$CHECKNETWORK_RELEASE_OUTPUT"
                printf keep >"$CHECKNETWORK_RELEASE_OUTPUT/marker"
            fi
            kill -s "$FAKE_SIGNAL" "$PPID"
        fi
        exit 0
        ;;
esac
exec "$REAL_MV" "$@"
`
	for name, body := range map[string]string{"docker": fakeDocker, "cp": fakeCP, "mv": fakeMV} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "scripts/verify-release.sh"}, {"commit", "-qm", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir, command.Env = repo, gitEnv
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	headBytes, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	realCP, err := exec.LookPath("cp")
	if err != nil {
		t.Fatal(err)
	}
	realMV, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	return releaseSignalFixture{root: root, repo: repo, fakeBin: fakeBin, head: strings.TrimSpace(string(headBytes)), realCP: realCP, realMV: realMV}
}

func (fixture releaseSignalFixture) run(t *testing.T, name, signal, point string, existing, hostile bool) (string, error, string) {
	t.Helper()
	outputPath := filepath.Join(fixture.root, "published-"+name)
	if existing {
		if err := os.WriteFile(outputPath, []byte("prior-output"), 0o751); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("sh", "scripts/verify-release.sh", fixture.head)
	command.Dir = fixture.repo
	command.Env = append(os.Environ(),
		"PATH="+fixture.fakeBin+":"+os.Getenv("PATH"),
		"FAKE_REVISION="+fixture.head,
		"FAKE_SIGNAL="+signal,
		"FAKE_SIGNAL_POINT="+point,
		"FAKE_HOSTILE_DESTINATION="+map[bool]string{false: "false", true: "true"}[hostile],
		"REAL_CP="+fixture.realCP,
		"REAL_MV="+fixture.realMV,
		"CHECKNETWORK_RELEASE_OUTPUT="+outputPath,
	)
	output, err := command.CombinedOutput()
	return string(output), err, outputPath
}

func assertFixedSignalExit(t *testing.T, err error, want int, output string) {
	t.Helper()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != want {
		t.Fatalf("signal exit=%v, want fixed code %d; output=%s", err, want, output)
	}
}

func assertNoPublicationTemps(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".checknetwork-release-") {
			t.Fatalf("publication left temporary path %s", filepath.Join(directory, entry.Name()))
		}
	}
}

func TestReleaseVerifierSignalsRollbackPublicationTransaction(t *testing.T) {
	fixture := newReleaseSignalFixture(t)
	signals := []struct {
		name string
		code int
	}{{"HUP", 129}, {"INT", 130}, {"TERM", 143}}
	for _, signal := range signals {
		for _, point := range []string{"pre-mv", "post-mv"} {
			for _, existing := range []bool{false, true} {
				name := strings.ToLower(signal.name) + "-" + point + "-" + map[bool]string{false: "absent", true: "existing"}[existing]
				t.Run(name, func(t *testing.T) {
					output, runErr, outputPath := fixture.run(t, name, signal.name, point, existing, false)
					assertFixedSignalExit(t, runErr, signal.code, output)
					if strings.Contains(output, "release verified:") {
						t.Fatalf("interrupted release printed success: %s", output)
					}
					if existing {
						contents, err := os.ReadFile(outputPath)
						if err != nil || string(contents) != "prior-output" {
							t.Fatalf("existing output bytes=%q err=%v", contents, err)
						}
						info, err := os.Lstat(outputPath)
						if err != nil || info.Mode().Perm() != 0o751 {
							t.Fatalf("existing output mode=%v err=%v, want 0751", info.Mode().Perm(), err)
						}
					} else if _, err := os.Lstat(outputPath); !os.IsNotExist(err) {
						t.Fatalf("interrupted release left new output: %v", err)
					}
					assertNoPublicationTemps(t, fixture.root)
				})
			}
		}
	}
}

func TestReleaseVerifierSignalDuringPublicationCopyCleansPreparedTransaction(t *testing.T) {
	fixture := newReleaseSignalFixture(t)
	for _, existing := range []bool{false, true} {
		name := map[bool]string{false: "absent", true: "existing"}[existing]
		t.Run(name, func(t *testing.T) {
			output, runErr, outputPath := fixture.run(t, "copy-"+name, "TERM", "copy", existing, false)
			assertFixedSignalExit(t, runErr, 143, output)
			if existing {
				contents, err := os.ReadFile(outputPath)
				if err != nil || string(contents) != "prior-output" {
					t.Fatalf("copy interruption changed output bytes=%q err=%v", contents, err)
				}
				info, err := os.Lstat(outputPath)
				if err != nil || info.Mode().Perm() != 0o751 {
					t.Fatalf("copy interruption changed mode=%v err=%v", info.Mode().Perm(), err)
				}
			} else if _, err := os.Lstat(outputPath); !os.IsNotExist(err) {
				t.Fatalf("copy interruption published output: %v", err)
			}
			assertNoPublicationTemps(t, fixture.root)
		})
	}
}

func TestReleaseVerifierRollbackFailureIsFailClosedAndNonRecursive(t *testing.T) {
	fixture := newReleaseSignalFixture(t)
	output, runErr, outputPath := fixture.run(t, "hostile-restore", "TERM", "post-mv", true, true)
	assertFixedSignalExit(t, runErr, 143, output)
	if !strings.Contains(output, "existing output could not be restored safely") {
		t.Fatalf("restore failure was not reported: %s", output)
	}
	marker, err := os.ReadFile(filepath.Join(outputPath, "marker"))
	if err != nil || string(marker) != "keep" {
		t.Fatalf("rollback recursively damaged hostile destination marker=%q err=%v", marker, err)
	}
	entries, err := os.ReadDir(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	backups := 0
	outputs := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".checknetwork-release-backup.") {
			backups++
			contents, readErr := os.ReadFile(filepath.Join(fixture.root, entry.Name()))
			if readErr != nil || string(contents) != "prior-output" {
				t.Fatalf("retained recovery backup bytes=%q err=%v", contents, readErr)
			}
		}
		if strings.HasPrefix(entry.Name(), ".checknetwork-release-output.") {
			outputs++
		}
	}
	if backups != 1 || outputs != 0 {
		t.Fatalf("restore failure backups=%d output temps=%d, want retained backup only", backups, outputs)
	}
}

func TestReleaseVerifierSuccessfulPublicationCommitsAndRemovesBackup(t *testing.T) {
	fixture := newReleaseSignalFixture(t)
	output, runErr, outputPath := fixture.run(t, "success", "TERM", "none", true, false)
	if runErr != nil {
		t.Fatalf("successful publication failed: %v output=%s", runErr, output)
	}
	if !strings.Contains(output, "release verified:") {
		t.Fatalf("successful publication omitted success: %s", output)
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil || string(contents) != "canonical-image-binary" {
		t.Fatalf("published canonical artifact bytes=%q err=%v", contents, err)
	}
	info, err := os.Lstat(outputPath)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("published mode=%v err=%v, want 0755", info.Mode().Perm(), err)
	}
	assertNoPublicationTemps(t, fixture.root)
}

func TestReleaseVerifierBuildsTwiceDetectsMismatchAndCleans(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VERSION", "scripts/verify-release.sh"} {
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(repo, name), body, mode); err != nil {
			t.Fatal(err)
		}
	}
	fakeGo := `#!/bin/sh
set -eu
out=
while [ "$#" -gt 0 ]; do
    if [ "$1" = "-o" ]; then shift; out=$1; fi
    shift
done
case "${FAKE_BINARY_MISMATCH:-}:$out" in true:*-2) printf two >"$out" ;; *) printf one >"$out" ;; esac
`
	fakeDocker := `#!/bin/sh
set -eu
printf 'PWD=%s ARGS=%s\n' "$PWD" "$*" >>"$FAKE_DOCKER_LOG"
case "$*" in
  info) exit 0 ;;
  cp*) destination=
       for argument in "$@"; do destination=$argument; done
       case "${FAKE_BINARY_MISMATCH:-}:$*" in true:*extract-two*) printf two >"$destination" ;; *) printf one >"$destination" ;; esac ;;
  *"{{.Id}}"*) case "${FAKE_IMAGE_MISMATCH:-}:$*" in true:*:two*) printf 'sha256:two\n' ;; *) printf 'sha256:same\n' ;; esac ;;
  *"RootFS.Layers"*) printf '["sha256:root"]\n' ;;
  *"org.opencontainers.image.version"*) printf '%s\n' "$FAKE_VERSION" ;;
  *"org.opencontainers.image.revision"*) printf '%s\n' "$FAKE_REVISION" ;;
  *"/readyz") printf '{"status":"ready"}\n' ;;
  *"/livez") printf '{"status":"live"}\n' ;;
  *"/api/v1/health") printf '{"status":"ok","version":"%s","revision":"%s"}\n' "$FAKE_VERSION" "$FAKE_REVISION" ;;
  *"traceroute -n -m 1 -w 1 127.0.0.1") printf '1  127.0.0.1  0.01 ms\n' ;;
  logs*) printf '{"msg":"server started","version":"%s","revision":"%s"}\n' "$FAKE_VERSION" "$FAKE_REVISION" ;;
esac
`
	for name, body := range map[string]string{"go": fakeGo, "docker": fakeDocker} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "scripts/verify-release.sh"}, {"commit", "-qm", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir, command.Env = repo, gitEnv
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	headCommand := exec.Command("git", "rev-parse", "HEAD")
	headCommand.Dir = repo
	headBytes, err := headCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	archivePath := filepath.Join(root, "source.tar")
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archiveCommand := exec.Command("git", "archive", head)
	var archiveErrors bytes.Buffer
	archiveCommand.Dir, archiveCommand.Stdout, archiveCommand.Stderr = repo, archiveFile, &archiveErrors
	if archiveErr := archiveCommand.Run(); archiveErr != nil {
		_ = archiveFile.Close()
		t.Fatalf("git archive: %v\n%s", archiveErr, archiveErrors.String())
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "docker.log")
	baseEnv := append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "FAKE_DOCKER_LOG="+logPath, "FAKE_VERSION=0.1.0", "FAKE_REVISION="+head)
	run := func(extra ...string) (string, string, error) {
		_ = os.Remove(logPath)
		command := exec.Command("sh", "scripts/verify-release.sh", head, archivePath)
		command.Dir = repo
		command.Env = append(append([]string(nil), baseEnv...), extra...)
		output, runErr := command.CombinedOutput()
		calls, readErr := os.ReadFile(logPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(output), string(calls), runErr
	}

	output, calls, runErr := run()
	if runErr != nil {
		t.Fatalf("stubbed verifier failed: %v\n%s\n%s", runErr, output, calls)
	}
	if strings.Count(calls, "build --no-cache") != 2 {
		t.Fatalf("Docker builds != 2:\n%s", calls)
	}
	if strings.Contains(calls, "PWD="+repo+" ARGS=build --no-cache") {
		t.Fatalf("archive release built in the caller checkout instead of its private extraction:\n%s", calls)
	}
	if strings.Contains(calls, " -p ") || strings.Contains(calls, "--publish") {
		t.Fatalf("smoke published a port:\n%s", calls)
	}
	for _, exact := range []string{"container rm -f --", "image rm -f --", "network rm --"} {
		if strings.Count(calls, exact) != 1 {
			t.Fatalf("cleanup %q count != 1:\n%s", exact, calls)
		}
	}
	releaseOutput := filepath.Join(root, "published-api")
	output, _, runErr = run("CHECKNETWORK_RELEASE_OUTPUT=" + releaseOutput)
	if runErr != nil {
		t.Fatalf("release output run failed: %v\n%s", runErr, output)
	}
	published, err := os.ReadFile(releaseOutput)
	if err != nil || string(published) != "one" {
		t.Fatalf("published image binary=%q err=%v", published, err)
	}

	output, calls, runErr = run("FAKE_BINARY_MISMATCH=true")
	if runErr == nil || !strings.Contains(output, "binary reproducibility mismatch") {
		t.Fatalf("binary mismatch not detected: err=%v output=%s", runErr, output)
	}
	if strings.Count(calls, "container rm -f --") != 1 {
		t.Fatalf("binary failure cleanup missing:\n%s", calls)
	}

	output, calls, runErr = run("FAKE_IMAGE_MISMATCH=true")
	if runErr == nil || !strings.Contains(output, "image/config digest mismatch") {
		t.Fatalf("image mismatch not detected: err=%v output=%s\n%s", runErr, output, calls)
	}
	for _, exact := range []string{"container rm -f --", "image rm -f --", "network rm --"} {
		if strings.Count(calls, exact) != 1 {
			t.Fatalf("mismatch cleanup %q count != 1:\n%s", exact, calls)
		}
	}
}
