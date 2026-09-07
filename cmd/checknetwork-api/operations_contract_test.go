package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	for _, script := range []string{"scripts/ci-inner.sh", "scripts/ci-clean-archive.sh", "scripts/ci-clean-archive-test.sh"} {
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

func TestCleanArchiveDisablesAmbientVCSStampingWithoutDroppingCallerGOFLAGS(t *testing.T) {
	command := exec.Command("sh", filepath.Join("..", "..", "scripts", "ci-clean-archive-test.sh"))
	tempRoot := filepath.Join(t.TempDir(), "test tmp with spaces")
	if err := os.Mkdir(tempRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "TMPDIR="+tempRoot)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("clean archive adversarial regression: %v\n%s", err, output)
	}
	if got := strings.Count(string(output), "clean archive verified at "); got != 3 {
		t.Fatalf("clean archive case count=%d, want 3\n%s", got, output)
	}
	if !strings.Contains(string(output), "ci-clean-archive adversarial cases: 3 passed") {
		t.Fatalf("clean archive regression did not report its exact case count:\n%s", output)
	}
}

func TestAPIOfflineArchiveFoundationIsTaglessAndDaemonReadOnly(t *testing.T) {
	realTest := repositoryFile(t, "scripts/verify_api_archive_real_test.sh")
	exactPrefix := "docker buildx build --platform linux/amd64 --no-cache --provenance=false"
	if count := strings.Count(realTest, exactPrefix); count != 2 {
		t.Fatalf("real API archive test must define exactly two no-cache tagless buildx builds, got %d", count)
	}
	for _, required := range []string{
		"--build-arg \"VERSION=$version\"",
		"--build-arg \"REVISION=$revision\"",
		"--build-arg \"SOURCE_DATE_EPOCH=$source_date_epoch\"",
		"--output=type=docker,dest=\"$archive_one\",rewrite-timestamp=true .",
		"--output=type=docker,dest=\"$archive_two\",rewrite-timestamp=true .",
		"verify_api_archive.py", "--extract-dir", "--binary-sha256",
		"daemon image inventory changed", "daemon tag inventory changed",
	} {
		if !strings.Contains(realTest, required) {
			t.Errorf("offline API archive real test missing %q", required)
		}
	}
	for _, forbidden := range []string{"--load", "--tag", "docker load", "docker import", "docker image rm", "docker rmi"} {
		if strings.Contains(realTest, forbidden) {
			t.Errorf("offline API archive build must not use %q", forbidden)
		}
	}
	validator := repositoryFile(t, "scripts/verify_api_archive.py")
	for _, forbidden := range []string{"import subprocess", "import docker", "extractall(", ".extract("} {
		if strings.Contains(validator, forbidden) {
			t.Errorf("offline validator contains forbidden dependency/unsafe extraction %q", forbidden)
		}
	}
}

func TestWebOfflineArchiveRealBuildxGateIsSinglePlatformTaglessAndDaemonReadOnly(t *testing.T) {
	realTest := repositoryFile(t, "scripts/verify_web_archive_real_test.sh")
	for _, required := range []string{
		"web-base-context", "chmod 0700", "FROM nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10",
		"--platform linux/amd64", "--no-cache --provenance=false", "SOURCE_DATE_EPOCH=0",
		"rewrite-timestamp=true", "verify_web_archive.py", "cmp -s \"$base_archive_one\" \"$base_archive_two\"",
		"cmp -s \"$archive_one\" \"$archive_two\"", "daemon image inventory changed",
		"daemon tag inventory changed", "daemon container inventory changed", "daemon network inventory changed",
		"trap cleanup EXIT", "trap 'handle_signal 129' HUP", "trap 'handle_signal 130' INT", "trap 'handle_signal 143' TERM",
		"rm -rf -- \"$tmp\"", "real_test_hook base_archive_1",
	} {
		if !strings.Contains(realTest, required) {
			t.Errorf("real Web archive gate missing %q", required)
		}
	}
	if count := strings.Count(realTest, "docker buildx build --platform linux/amd64 --no-cache --provenance=false"); count != 4 {
		t.Fatalf("real Web gate must perform two base and two derived single-platform Buildx exports, got %d", count)
	}
	for _, forbidden := range []string{"--load", "--tag", "docker load", "docker import", "docker image save", "docker image rm", "docker rmi"} {
		if strings.Contains(realTest, forbidden) {
			t.Errorf("real Web archive gate must not use %q", forbidden)
		}
	}
	validator := repositoryFile(t, "scripts/verify_web_archive.py")
	for _, required := range []string{"PINNED_NGINX_CONFIG_DIGEST", "PINNED_NGINX_LAYER_DIGESTS", "PINNED_NGINX_DIFF_IDS", "PINNED_NGINX_HISTORY_DIGEST", "base archive platform is not exact linux/amd64"} {
		if !strings.Contains(validator, required) {
			t.Errorf("Web validator exact base contract missing %q", required)
		}
	}
}

func TestOptInRealArchiveBuildxGoAndMakeGatesAreWired(t *testing.T) {
	makefile := repositoryFile(t, "Makefile")
	for _, required := range []string{
		"verify-api-archive-real:", "verify-web-archive-real:", "verify-archives-real:",
		"CHECKNETWORK_RUN_REAL_BUILDX_TESTS=1", "TestAPIOfflineArchiveRealBuildx", "TestWebOfflineArchiveRealBuildx",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile real archive gate missing %q", required)
		}
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
		`VERSION: ${CHECKNETWORK_VERSION:-0.1.0-dev}`,
		`REVISION: ${CHECKNETWORK_REVISION:-0000000000000000000000000000000000000000}`,
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

func TestComposeResolvedBuildIdentityIsValidSharedAndOverrideable(t *testing.T) {
	type composeConfig struct {
		Services map[string]struct {
			Build struct {
				Args map[string]string `json:"args"`
			} `json:"build"`
		} `json:"services"`
	}

	resolve := func(overrides ...string) composeConfig {
		t.Helper()
		command := exec.Command("docker", "compose", "config", "--format", "json")
		command.Dir = filepath.Join("..", "..")
		for _, entry := range os.Environ() {
			if strings.HasPrefix(entry, "CHECKNETWORK_VERSION=") ||
				strings.HasPrefix(entry, "CHECKNETWORK_REVISION=") ||
				strings.HasPrefix(entry, "SOURCE_DATE_EPOCH=") {
				continue
			}
			command.Env = append(command.Env, entry)
		}
		command.Env = append(command.Env, overrides...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("docker compose config: %v\n%s", err, output)
		}
		var config composeConfig
		if err := json.Unmarshal(output, &config); err != nil {
			t.Fatalf("decode docker compose config: %v\n%s", err, output)
		}
		return config
	}

	assertShared := func(config composeConfig, want map[string]string) {
		t.Helper()
		for _, service := range []string{"api", "web"} {
			resolved, ok := config.Services[service]
			if !ok {
				t.Fatalf("resolved Compose config has no %s service", service)
			}
			for name, value := range want {
				if got := resolved.Build.Args[name]; got != value {
					t.Errorf("%s build arg %s=%q, want exact %q", service, name, got, value)
				}
			}
		}
	}

	const canonicalSemverERE = `^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`
	validSemvers := []string{"0.1.0-dev", "1.0.0-alpha.1", "1.0.0+build.5", "1.0.0-0"}
	invalidSemvers := []string{
		"dev", "v1.0.0", "01.0.0", "1.01.0", "1.0.01", "1.0.0-01",
		"1..0", "1.0.0-alpha..1", "1.0.0-", "1.0.0-alpha.", "1.0.0+build..1",
		"1.0.0-alpha_beta",
	}
	acceptsERE := func(pattern, candidate string) bool {
		t.Helper()
		command := exec.Command("grep", "-Eq", pattern)
		command.Stdin = strings.NewReader(candidate + "\n")
		err := command.Run()
		if err == nil {
			return true
		}
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false
		}
		t.Fatalf("execute canonical SemVer ERE for %q: %v", candidate, err)
		return false
	}
	for _, candidate := range validSemvers {
		if !acceptsERE(canonicalSemverERE, candidate) {
			t.Fatalf("independent canonical SemVer ERE rejected valid corpus member %q", candidate)
		}
	}
	for _, candidate := range invalidSemvers {
		if acceptsERE(canonicalSemverERE, candidate) {
			t.Fatalf("independent canonical SemVer ERE accepted invalid corpus member %q", candidate)
		}
	}

	version := strings.TrimSpace(repositoryFile(t, "VERSION")) + "-dev"
	const zeroRevision = "0000000000000000000000000000000000000000"
	defaults := map[string]string{
		"VERSION":           version,
		"REVISION":          zeroRevision,
		"SOURCE_DATE_EPOCH": "0",
	}
	resolvedDefaults := resolve()
	assertShared(resolvedDefaults, defaults)

	// The independently specified canonical ERE, rather than either production
	// copy, decides whether resolved Compose defaults are valid.
	hex40 := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, service := range []string{"api", "web"} {
		args := resolvedDefaults.Services[service].Build.Args
		if !acceptsERE(canonicalSemverERE, args["VERSION"]) {
			t.Errorf("resolved %s default VERSION %q is not canonical SemVer", service, args["VERSION"])
		}
		if !hex40.MatchString(args["REVISION"]) {
			t.Errorf("resolved %s default REVISION %q is not 40 lowercase hexadecimal characters", service, args["REVISION"])
		}
	}

	frontendMatch := regexp.MustCompile(`grep -Eq '([^']+)'`).FindStringSubmatch(repositoryFile(t, "frontend/Dockerfile"))
	releaseMatch := regexp.MustCompile(`(?m)^semver='([^']+)'$`).FindStringSubmatch(repositoryFile(t, "scripts/verify-release.sh"))
	if len(frontendMatch) != 2 || len(releaseMatch) != 2 {
		t.Fatal("frontend Dockerfile and release verifier must each expose one single-quoted SemVer ERE")
	}
	if frontendMatch[1] != canonicalSemverERE || releaseMatch[1] != canonicalSemverERE || frontendMatch[1] != releaseMatch[1] {
		t.Fatalf("SemVer ERE duplication drift: frontend=%q release=%q canonical=%q", frontendMatch[1], releaseMatch[1], canonicalSemverERE)
	}

	overrides := map[string]string{
		"VERSION":           "9.8.7-explicit.1+caller",
		"REVISION":          "0123456789abcdef0123456789abcdef01234567",
		"SOURCE_DATE_EPOCH": "1234567890",
	}
	assertShared(resolve(
		"CHECKNETWORK_VERSION="+overrides["VERSION"],
		"CHECKNETWORK_REVISION="+overrides["REVISION"],
		"SOURCE_DATE_EPOCH="+overrides["SOURCE_DATE_EPOCH"],
	), overrides)
}

func TestReleaseDockerfileIsImmutableAndCarriesExactIdentity(t *testing.T) {
	dockerfile := repositoryFile(t, "Dockerfile")
	const alpineDigest = "sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc"
	if count := strings.Count(dockerfile, "alpine@"+alpineDigest); count != 2 {
		t.Fatalf("Dockerfile must use the exact audited Alpine digest for traceroute and final runtime stages, got %d", count)
	}
	validator := repositoryFile(t, "scripts/verify_api_archive.py")
	if !strings.Contains(validator, `PINNED_ALPINE_IMAGE_DIGEST = "`+alpineDigest+`"`) {
		t.Fatal("offline archive validator Alpine digest contract must match Dockerfile")
	}
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

func TestReleaseVerifierPublishesOnlyTheVerifiedArchiveBinary(t *testing.T) {
	script := repositoryFile(t, "scripts/verify-release.sh")
	for _, required := range []string{
		"verify_api_archive.py", "--extract-dir \"$api_extract\"",
		"API binary bytes differ between no-cache archives", "cp \"$api_extract_one/checknetwork-api\" \"$publish_tmp\"",
		"mv -f \"$publish_tmp\" \"$release_output\"", "working tree changed during release verification",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("archive binary publication contract missing %q", required)
		}
	}
	for _, forbidden := range []string{"docker cp", "docker load", "docker import", "docker image rm", "docker rmi", "--load", "--tag", "--iidfile", "api_image"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("release verifier retains forbidden derived API image lifecycle %q", forbidden)
		}
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
		"for pass in 1 2", "sha256sum", "docker info", "docker buildx build --no-cache --provenance=false", "rewrite-timestamp=true",
		"verify_api_archive.py", "api_archive", "api_config", "api_manifest", "api_rootfs", "api_binary", "api_traceroute", "/livez", "/readyz", "/api/v1/health",
		"trap cleanup EXIT", "trap 'handle_signal 129' HUP", "trap 'handle_signal 130' INT", "trap 'handle_signal 143' TERM",
		"create_owned_api_network", "create_owned_api_container", "docker container start \"$api_smoke_container_id\"",
		"only proves pinned-base mount compatibility; it does not execute the derived API archive",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("release verifier missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"docker run ",
		"docker cp", "docker load", "docker import", "docker image rm", "docker rmi", "--load", "--tag", "--iidfile",
		"api_image", "api_container_role=extract",
		"docker network rm -- \"$network_name\"",
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("release verifier retains unsafe shared-daemon lifecycle %q", forbidden)
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
	for _, name := range []string{"VERSION", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"} {
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
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"}, {"commit", "-qm", "fixture"}} {
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
	for _, name := range []string{"VERSION", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"} {
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
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "README.fixture", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"}, {"commit", "-qm", "fixture"}} {
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
	for _, name := range []string{"VERSION", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"} {
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
	fakeDocker := releaseLifecycleFakeDocker()
	if err := os.WriteFile(filepath.Join(fakeBin, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "verify_api_archive.py"), []byte(releaseLifecycleFakeValidator()), 0o755); err != nil {
		t.Fatal(err)
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "payload", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"}, {"commit", "-qm", "fixture"}} {
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
	if strings.Contains(calls, "PWD="+repo+" ARGS=buildx build --no-cache") {
		t.Fatalf("standalone release built the mutable checkout:\n%s", calls)
	}
	for _, forbidden := range []string{"ARGS=build ", "ARGS=load", "ARGS=import", "ARGS=image rm", "ARGS=image tag", "--tag", "--iidfile", "--load"} {
		if strings.Contains(calls, forbidden) {
			t.Fatalf("standalone release attempted derived API daemon action %q:\n%s", forbidden, calls)
		}
	}
	stateBytes, err := os.ReadFile(filepath.Join(fakeBin, "docker-state", "state.json"))
	if err != nil || string(stateBytes) != `{"containers": {}, "images": {}, "networks": {}}` {
		t.Fatalf("standalone daemon inventory changed: err=%v state=%s", err, stateBytes)
	}
	if built, err := os.ReadFile(cleanOutput); err != nil || string(built) != "canonical-image-binary" {
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
	for _, name := range []string{"VERSION", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"} {
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

	fakeDocker := releaseLifecycleFakeDocker()
	fakeCP := `#!/bin/sh
set -eu
for argument in "$@"; do
    case "$argument" in
        */api-extract-one/checknetwork-api)
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
	if err := os.WriteFile(filepath.Join(repo, "scripts", "verify_api_archive.py"), []byte(releaseLifecycleFakeValidator()), 0o755); err != nil {
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
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "payload", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"}, {"commit", "-qm", "fixture"}} {
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
	for _, name := range []string{"VERSION", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"} {
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
	fakeDocker := releaseLifecycleFakeDocker()
	fakeCP := `#!/bin/sh
set -eu
"$REAL_CP" "$@"
source_path=
destination=
for argument in "$@"; do
    [ "$argument" = "-p" ] || { source_path=$destination; destination=$argument; }
done
case "$source_path:$destination:$FAKE_SIGNAL_POINT" in
    */api-extract-one/checknetwork-api:*/.checknetwork-release-output.*:copy)
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
	if err := os.WriteFile(filepath.Join(repo, "scripts", "verify_api_archive.py"), []byte(releaseLifecycleFakeValidator()), 0o755); err != nil {
		t.Fatal(err)
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"}, {"commit", "-qm", "fixture"}} {
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

func (fixture releaseSignalFixture) run(t *testing.T, name, signal, point string, existing, hostile bool, extraEnv ...string) (string, error, string) {
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
		"FAKE_DOCKER_BINARY=canonical-image-binary",
		"FAKE_SIGNAL="+signal,
		"FAKE_SIGNAL_POINT="+point,
		"FAKE_HOSTILE_DESTINATION="+map[bool]string{false: "false", true: "true"}[hostile],
		"REAL_CP="+fixture.realCP,
		"REAL_MV="+fixture.realMV,
		"CHECKNETWORK_RELEASE_OUTPUT="+outputPath,
	)
	command.Env = append(command.Env, extraEnv...)
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

func TestReleaseVerifierCleanupBarrierRollsBackPublication(t *testing.T) {
	for _, test := range []struct {
		name string
		env  string
	}{
		{"container-removal-failure", "FAKE_CLEANUP_REMOVE_FAILURE=container"},
		{"pre-inspect-daemon-error", "FAKE_CLEANUP_INSPECT_ERROR=pre"},
		{"post-inspect-daemon-error", "FAKE_CLEANUP_INSPECT_ERROR=post"},
		{"container-replacement", "FAKE_CLEANUP_REPLACEMENT=container"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReleaseSignalFixture(t)
			output, runErr, outputPath := fixture.run(t, "cleanup-"+test.name, "TERM", "none", true, false, test.env)
			if runErr == nil || strings.Contains(output, "release verified:") {
				t.Fatalf("cleanup ambiguity reported release success: err=%v output=%s", runErr, output)
			}
			if strings.Count(output, "release cleanup failed") != 1 {
				t.Fatalf("cleanup failure diagnostic was not fixed and singular: %q", output)
			}
			contents, err := os.ReadFile(outputPath)
			if err != nil || string(contents) != "prior-output" {
				t.Fatalf("cleanup failure did not restore exact publication bytes=%q err=%v", contents, err)
			}
			info, err := os.Lstat(outputPath)
			if err != nil || info.Mode().Perm() != 0o751 {
				t.Fatalf("cleanup failure did not restore publication mode=%v err=%v", info.Mode().Perm(), err)
			}
			assertNoPublicationTemps(t, fixture.root)
		})
	}
}

func TestReleaseVerifierCleanupFailurePreservesOrdinaryAndSignalStatus(t *testing.T) {
	for _, signal := range []struct {
		name string
		code int
	}{{"HUP", 129}, {"INT", 130}, {"TERM", 143}} {
		t.Run(strings.ToLower(signal.name), func(t *testing.T) {
			fixture := newReleaseSignalFixture(t)
			output, runErr, outputPath := fixture.run(t, "cleanup-signal-"+strings.ToLower(signal.name), signal.name, "post-mv", true, false, "FAKE_CLEANUP_REMOVE_FAILURE=container")
			assertFixedSignalExit(t, runErr, signal.code, output)
			if strings.Contains(output, "release verified:") || strings.Count(output, "release cleanup failed") != 1 {
				t.Fatalf("signal cleanup failure output was unsafe or successful: %q", output)
			}
			contents, err := os.ReadFile(outputPath)
			if err != nil || string(contents) != "prior-output" {
				t.Fatalf("signal cleanup failure did not restore publication bytes=%q err=%v", contents, err)
			}
		})
	}

	fixture := newReleaseSignalFixture(t)
	output, runErr, _ := fixture.run(t, "cleanup-ordinary", "TERM", "none", true, false,
		"CHECKNETWORK_RELEASE_FAIL_AT=api_identity_smoke", "FAKE_CLEANUP_REMOVE_FAILURE=container")
	exitError, ok := runErr.(*exec.ExitError)
	if !ok || exitError.ExitCode() == 0 || strings.Contains(output, "release verified:") || strings.Count(output, "release cleanup failed") != 1 {
		t.Fatalf("ordinary failure cleanup contract changed: err=%v output=%q", runErr, output)
	}
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
	for _, name := range []string{"VERSION", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"} {
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
	fakeDocker := releaseLifecycleFakeDocker()
	for name, body := range map[string]string{"go": fakeGo, "docker": fakeDocker} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "verify_api_archive.py"), []byte(releaseLifecycleFakeValidator()), 0o755); err != nil {
		t.Fatal(err)
	}
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	for _, args := range [][]string{{"init", "-q"}, {"add", "VERSION", "scripts/verify-release.sh", "scripts/release-resource-ownership.sh", "scripts/verify_api_archive.py"}, {"commit", "-qm", "fixture"}} {
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
	stateDir := filepath.Join(fakeBin, "docker-state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	callerID := "sha256:" + strings.Repeat("c", 64)
	initialDaemonState := `{"containers": {}, "images": {"` + callerID + `": {"id": "` + callerID + `", "labels": {}}}, "networks": {}}`
	baseEnv := append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "FAKE_DOCKER_LOG="+logPath, "FAKE_VERSION=0.1.0", "FAKE_REVISION="+head)
	run := func(extra ...string) (string, string, error) {
		_ = os.Remove(logPath)
		statePath := filepath.Join(stateDir, "state.json")
		if err := os.WriteFile(statePath, []byte(initialDaemonState), 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("sh", "scripts/verify-release.sh", head, archivePath)
		command.Dir = repo
		command.Env = append(append([]string(nil), baseEnv...), extra...)
		output, runErr := command.CombinedOutput()
		calls, readErr := os.ReadFile(logPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		after, readErr := os.ReadFile(statePath)
		if readErr != nil || string(after) != initialDaemonState {
			t.Fatalf("daemon inventory or original tagless caller ID changed byte-for-byte: id=%s err=%v state=%s", callerID, readErr, after)
		}
		return string(output), string(calls), runErr
	}

	output, calls, runErr := run()
	if runErr != nil {
		t.Fatalf("stubbed verifier failed: %v\n%s\n%s", runErr, output, calls)
	}
	if strings.Count(calls, "buildx build --no-cache") != 2 {
		t.Fatalf("Docker builds != 2:\n%s", calls)
	}
	if strings.Contains(calls, "PWD="+repo+" ARGS=buildx build --no-cache") {
		t.Fatalf("archive release built in the caller checkout instead of its private extraction:\n%s", calls)
	}
	if strings.Contains(calls, " -p ") || strings.Contains(calls, "--publish") {
		t.Fatalf("smoke published a port:\n%s", calls)
	}
	for _, forbidden := range []string{"ARGS=build ", "ARGS=load", "ARGS=import", "ARGS=image rm", "ARGS=image tag", "--tag", "--iidfile", "--load"} {
		if strings.Contains(calls, forbidden) {
			t.Fatalf("derived API daemon action %q was attempted:\n%s", forbidden, calls)
		}
	}
	if strings.Count(calls, "ARGS=image inspect alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc --format {{.Id}}") != 1 {
		t.Fatalf("borrowed-base inspection was not exact:\n%s", calls)
	}
	successLines := regexp.MustCompile(`(?m)^release verified: api_archive=[0-9a-f]{64} api_config=sha256:[0-9a-f]{64} api_manifest=sha256:[0-9a-f]{64} api_rootfs=sha256:[0-9a-f]{64} api_binary=[0-9a-f]{64} api_traceroute=[0-9a-f]{64}$`).FindAllString(output, -1)
	if len(successLines) != 1 || strings.Contains(output, "api_image=") {
		t.Fatalf("release output schema is not the exact six-field API contract: %s", output)
	}
	for exact, want := range map[string]int{"container rm -f --": 1, "network rm --": 1} {
		if strings.Count(calls, exact) != want {
			t.Fatalf("cleanup %q count = %d, want %d:\n%s", exact, strings.Count(calls, exact), want, calls)
		}
	}
	releaseOutput := filepath.Join(root, "published-api")
	output, _, runErr = run("CHECKNETWORK_RELEASE_OUTPUT=" + releaseOutput)
	if runErr != nil {
		t.Fatalf("release output run failed: %v\n%s", runErr, output)
	}
	published, err := os.ReadFile(releaseOutput)
	if err != nil || string(published) != "canonical-image-binary" {
		t.Fatalf("published archive binary=%q err=%v", published, err)
	}

	output, calls, runErr = run("FAKE_BINARY_MISMATCH=true")
	if runErr == nil || !strings.Contains(output, "binary SHA-256 does not match expected value") {
		t.Fatalf("binary mismatch not detected: err=%v output=%s", runErr, output)
	}
	if strings.Count(calls, "container rm -f --") != 0 || strings.Count(calls, "network rm --") != 0 {
		t.Fatalf("binary failure cleanup missing:\n%s", calls)
	}

	output, calls, runErr = run("FAKE_IMAGE_MISMATCH=true")
	if runErr == nil || !strings.Contains(output, "API archive byte reproducibility mismatch") {
		t.Fatalf("archive mismatch not detected: err=%v output=%s\n%s", runErr, output, calls)
	}
	for exact, want := range map[string]int{"container rm -f --": 0, "network rm --": 0} {
		if strings.Count(calls, exact) != want {
			t.Fatalf("mismatch cleanup %q count = %d, want %d:\n%s", exact, strings.Count(calls, exact), want, calls)
		}
	}
	for _, stage := range []string{"api_archive_build_1", "api_archive_validation_1", "api_archive_build_2", "api_archive_validation_2"} {
		for _, signalAndCode := range []struct {
			name string
			code int
		}{{"HUP", 129}, {"INT", 130}, {"TERM", 143}} {
			output, calls, runErr = run("CHECKNETWORK_RELEASE_SIGNAL_AT="+stage, "CHECKNETWORK_RELEASE_SIGNAL="+signalAndCode.name)
			exitError, ok := runErr.(*exec.ExitError)
			if !ok || exitError.ExitCode() != signalAndCode.code || strings.Contains(output, "release verified:") {
				t.Fatalf("%s/%s signal contract failed: err=%v output=%s", stage, signalAndCode.name, runErr, output)
			}
			for _, forbidden := range []string{"ARGS=load", "ARGS=import", "ARGS=image rm", "ARGS=image tag", "--tag", "--iidfile", "--load"} {
				if strings.Contains(calls, forbidden) {
					t.Fatalf("%s/%s attempted derived API daemon action %q:\n%s", stage, signalAndCode.name, forbidden, calls)
				}
			}
		}
	}
}

func TestReleaseLifecycleFakeCleanupRestoresInventoryAndRecordsEventsSeparately(t *testing.T) {
	fakeBin := t.TempDir()
	dockerPath := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(dockerPath, []byte(releaseLifecycleFakeDocker()), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(fakeBin, "docker-state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	callerImageID := "sha256:" + strings.Repeat("c", 64)
	baseline := `{"containers": {"caller-container": {"id": "container-caller", "image": "` + callerImageID + `", "labels": {}, "name": "caller-container"}}, "images": {"caller-image": {"id": "` + callerImageID + `", "labels": {}}}, "networks": {"caller-network": {"id": "network-caller", "labels": {}, "name": "caller-network"}}}`
	statePath := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(statePath, []byte(baseline), 0o600); err != nil {
		t.Fatal(err)
	}
	runDocker := func(args ...string) string {
		t.Helper()
		command := exec.Command(dockerPath, args...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fake docker %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	ownedNetworkID := runDocker("network", "create", "--label", "com.checknetwork.release.owner=test", "owned-network")
	ownedContainerID := runDocker("container", "create", "--name", "owned-container", "--label", "com.checknetwork.release.owner=test", callerImageID)
	runDocker("logs", ownedContainerID)
	runDocker("container", "rm", "-f", "--", ownedContainerID)
	runDocker("network", "rm", "--", ownedNetworkID)

	after, err := os.ReadFile(statePath)
	if err != nil || string(after) != baseline {
		t.Fatalf("successful cleanup did not restore exact caller daemon inventory: image=%s err=%v\nbefore=%s\nafter=%s", callerImageID, err, baseline, after)
	}
	events, err := os.ReadFile(filepath.Join(stateDir, "events.log"))
	if err != nil || string(events) != "smoke_complete\ncleanup_removed:containers\ncleanup_removed:networks\n" {
		t.Fatalf("cleanup event log=%q err=%v", events, err)
	}
}

type generatedArtifactIgnoreRequirement struct {
	pattern string
	path    string
	isDir   bool
}

var generatedArtifactIgnoreRequirements = []generatedArtifactIgnoreRequirement{
	{pattern: "/checknetwork-api", path: "checknetwork-api"},
	{pattern: ".*.swp", path: ".SPEC.md.swp"},
	{pattern: "/scripts/__pycache__/", path: "scripts/__pycache__", isDir: true},
}

func validateGeneratedArtifactIgnoreRules(t *testing.T, gitignorePath string) error {
	t.Helper()
	contents, err := os.ReadFile(gitignorePath)
	if err != nil {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("find git: %w", err)
	}

	repo := t.TempDir()
	gitEnv := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(name, "GIT_") || name == "HOME" || name == "XDG_CONFIG_HOME" {
			continue
		}
		gitEnv = append(gitEnv, entry)
	}
	gitEnv = append(gitEnv,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"HOME="+repo,
		"XDG_CONFIG_HOME="+filepath.Join(repo, ".config"),
	)
	runGit := func(args ...string) ([]byte, error) {
		command := exec.Command("git", args...)
		command.Dir = repo
		command.Env = gitEnv
		return command.CombinedOutput()
	}
	if output, err := runGit("init", "-q"); err != nil {
		return fmt.Errorf("initialize private Git repository: %w: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), contents, 0o600); err != nil {
		return fmt.Errorf("write candidate .gitignore: %w", err)
	}
	for _, required := range generatedArtifactIgnoreRequirements {
		artifact := filepath.Join(repo, filepath.FromSlash(required.path))
		if required.isDir {
			if err := os.MkdirAll(artifact, 0o700); err != nil {
				return fmt.Errorf("create representative directory %s: %w", required.path, err)
			}
		} else if err := os.WriteFile(artifact, []byte("generated\n"), 0o600); err != nil {
			return fmt.Errorf("create representative file %s: %w", required.path, err)
		}
	}

	for _, required := range generatedArtifactIgnoreRequirements {
		output, err := runGit("check-ignore", "-v", "--no-index", "--", required.path)
		if err != nil {
			return fmt.Errorf("%s must be ignored after complete .gitignore rule ordering: %w: %s", required.path, err, output)
		}
		line := strings.TrimSuffix(string(output), "\n")
		parts := strings.SplitN(line, "	", 2)
		metadata := strings.SplitN(parts[0], ":", 3)
		if len(parts) != 2 || len(metadata) != 3 {
			return fmt.Errorf("unexpected git check-ignore output for %s: %q", required.path, output)
		}
		pattern := metadata[2]
		matchedPath := parts[1]
		if pattern != required.pattern || matchedPath != required.path {
			return fmt.Errorf("%s must be the exact effective non-negated .gitignore rule (effective rule %q for %q)", required.pattern, pattern, matchedPath)
		}
	}
	return nil
}

func TestGeneratedArtifactIgnoreRulesRejectInvalidFiles(t *testing.T) {
	valid := "/checknetwork-api\n.*.swp\n/scripts/__pycache__/\n"
	tests := []struct {
		name     string
		contents string
		wantOK   bool
	}{
		{name: "exact rules", contents: "\n# generated files\n" + valid, wantOK: true},
		{name: "escaped comment is a literal pattern", contents: "\\# not a comment\n" + valid, wantOK: true},
		{name: "missing", contents: "/checknetwork-api\n.*.swp\n"},
		{name: "commented", contents: "/checknetwork-api\n# .*.swp\n/scripts/__pycache__/\n"},
		{name: "negated", contents: "/checknetwork-api\n!.*.swp\n/scripts/__pycache__/\n"},
		{name: "escaped leading negation", contents: "/checknetwork-api\n\\!.*.swp\n/scripts/__pycache__/\n"},
		{name: "overridden", contents: valid + "!/checknetwork-api\n"},
		{name: "unanchored override", contents: valid + "!scripts/__pycache__/\n"},
		{name: "character class override", contents: valid + "!/checknetwork-[!b]pi\n"},
		{name: "POSIX character class override", contents: valid + "!/checknetwork-[[:alpha:]]pi\n"},
		{name: "escaped pattern override", contents: valid + "!/checknetwork-a\\pi\n"},
		{name: "root globstar override", contents: valid + "!/**\n/checknetwork-api\n.*.swp\n"},
		{name: "unescaped trailing spaces do not disable override", contents: valid + "!/checknetwork-api   \n"},
		{name: "escaped trailing space does not override", contents: valid + "!/checknetwork-api\\ \n", wantOK: true},
		{name: "later exact rule restores ignore", contents: valid + "!/checknetwork-api\n/checknetwork-api\n", wantOK: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gitignore := filepath.Join(t.TempDir(), ".gitignore")
			if err := os.WriteFile(gitignore, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			err := validateGeneratedArtifactIgnoreRules(t, gitignore)
			if (err == nil) != test.wantOK {
				t.Fatalf("validateGeneratedArtifactIgnoreRules() error = %v, wantOK %t", err, test.wantOK)
			}
		})
	}
}

func TestCurrentGitignoreExcludesGeneratedArtifactsWithoutRepositoryMetadata(t *testing.T) {
	if err := validateGeneratedArtifactIgnoreRules(t, filepath.Join("..", "..", ".gitignore")); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseVerifierResourceOwnershipLifecycle(t *testing.T) {
	command := exec.Command("sh", filepath.Join("scripts", "verify-release-resource-ownership-test.sh"))
	command.Dir = filepath.Join("..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("release resource ownership lifecycle: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "release resource ownership tests passed") {
		t.Fatalf("release resource ownership lifecycle omitted success evidence: %s", output)
	}
}

func TestReleaseVerifierAPIResourceOwnershipMatrix(t *testing.T) {
	command := exec.Command("python3", filepath.Join("scripts", "verify_release_api_resource_ownership_test.py"))
	command.Dir = filepath.Join("..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("API release resource ownership matrix: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "API release resource ownership matrix passed") {
		t.Fatalf("API release resource ownership matrix omitted success evidence: %s", output)
	}
}

func TestReleaseVerifierResourceOwnershipLifecycleRealDocker(t *testing.T) {
	if os.Getenv("CHECKNETWORK_RUN_REAL_DOCKER_TESTS") != "1" {
		t.Skip("set CHECKNETWORK_RUN_REAL_DOCKER_TESTS=1 for focused real-daemon ownership coverage")
	}
	command := exec.Command("sh", filepath.Join("scripts", "verify-release-resource-ownership-real-test.sh"))
	command.Dir = filepath.Join("..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Docker release resource ownership lifecycle: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "real Docker API ownership tests passed") {
		t.Fatalf("real Docker release resource ownership lifecycle omitted success evidence: %s", output)
	}
}

func TestAPIOfflineArchiveRealBuildx(t *testing.T) {
	if os.Getenv("CHECKNETWORK_RUN_REAL_BUILDX_TESTS") != "1" {
		t.Skip("set CHECKNETWORK_RUN_REAL_BUILDX_TESTS=1 for the real API Buildx archive gate")
	}
	command := exec.Command("sh", filepath.Join("scripts", "verify_api_archive_real_test.sh"))
	command.Dir = filepath.Join("..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real API Buildx archive gate: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte(`"first"`)) || !bytes.Contains(output, []byte(`"second"`)) {
		t.Fatalf("real API Buildx archive gate omitted comparison evidence: %s", output)
	}
}

func TestWebOfflineArchiveRealBuildx(t *testing.T) {
	if os.Getenv("CHECKNETWORK_RUN_REAL_BUILDX_TESTS") != "1" {
		t.Skip("set CHECKNETWORK_RUN_REAL_BUILDX_TESTS=1 for the real Web Buildx archive gate")
	}
	command := exec.Command("sh", filepath.Join("scripts", "verify_web_archive_real_test.sh"))
	command.Dir = filepath.Join("..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Web Buildx archive gate: %v\n%s", err, output)
	}
	for _, field := range []string{`"base_archive"`, `"derived_archive"`, `"validation"`} {
		if !bytes.Contains(output, []byte(field)) {
			t.Fatalf("real Web Buildx archive gate omitted %s: %s", field, output)
		}
	}
}
