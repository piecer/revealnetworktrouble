package diagnostic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const (
	stage8BaseParent = "36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243"
	stage8PlanPath   = ".hermes/plans/2026-09-03_100000-stage8-truthful-diagnostics-and-client-reliability.md"
	stage9PlanPath   = ".hermes/plans/2026-09-07_090438-topology-2d-3d-restoration.md"
)

func TestDocumentationContractMatchesCurrentSourceAndFixtures(t *testing.T) {
	root := documentationRepositoryRoot(t)
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(data)
	}

	currentDocPaths := []string{"README.md", "SPEC.md", "docs/API.md", "docs/ARCHITECTURE.md", "docs/OPERATIONS.md", "docs/TESTING.md", "docs/PLAN.md", stage9PlanPath}
	docPaths := append(append([]string(nil), currentDocPaths...), stage8PlanPath)
	var joined strings.Builder
	for _, path := range docPaths {
		joined.WriteString("\n--- " + path + " ---\n")
		joined.WriteString(read(path))
	}
	docs := joined.String()
	var currentJoined strings.Builder
	for _, path := range currentDocPaths {
		currentJoined.WriteString("\n--- " + path + " ---\n")
		currentJoined.WriteString(read(path))
	}
	currentDocs := currentJoined.String()

	sourceClaims := map[string][]string{
		"backend/diagnostic/service.go": {
			"serviceGreetingAggregateLimit = 4096",
			"serviceGreetingLineLimit      = 512",
			"serviceGreetingLineCountLimit = 8",
			"len(line) <= 255",
		},
		"backend/diagnostic/finding_contract.go": {
			`VerificationScopeServerGreeting = "server_greeting"`,
			`ResultDetailCertificateBefore   = "certificate_not_before"`,
			`ResultDetailCertificateAfter    = "certificate_not_after"`,
		},
		"backend/diagnostic/compact_topology.go": {
			`CompactTopologySchemaV1    CompactTopologySchema    = "compact-v1"`,
			"CompactTopologyMaxGeoBundleBytes = 4096",
			"CompactTopologyMaxNodes = 500",
			"CompactTopologyMaxLinks = 1000",
		},
		"backend/diagnostic/traceroute_available.go": {
			"tracerouteProbeTimeout     = 2 * time.Second",
			"tracerouteProbeOutputBytes = 32 << 10",
			`tracerouteProbeAddress     = "127.0.0.1"`,
			"executable, err := exec.LookPath(traceExecutableName())",
			"if capability == nil || capability.grammar.arguments == nil",
			"return nil, ErrTracerouteUnavailable",
		},
		"android/app/src/main/java/com/checknetwork/app/RawShareRuntime.java": {
			"RECONCILE_ENTRY_LIMIT=32",
			"RECONCILE_DELETE_LIMIT=32",
			"LEASE_MILLIS=15L*60L*1000L",
			"MAX_LIVE_BYTES=16L*1024L*1024L",
			"MAX_LIVE_FILES=2",
		},
		"frontend/app.js": {
			"export const MAX_DOCUMENT_ELEMENTS = 1200",
			"export const MAX_TARGETS = 20",
			"targetsEl.children.length >= MAX_TARGETS",
			"import { createViewTransform, resetViewTransform, updateViewTransform, normalizeViewport } from './topology-visualizer.js';",
			"const topologyViewState = { mode: '2d', transform: resetViewTransform() };",
			"function updateTopologyView({ mode = topologyViewState.mode, transform = topologyViewState.transform, viewport, message } = {})",
			"const topologyViewControls = doc.querySelector('#topology-view-controls');",
			"const delta = topologyViewState.mode === '3d' ? { yaw: dx * 0.01, pitch: dy * 0.01 } : { panX: dx, panY: dy };",
			"'cause', 'supporting_evidence', 'expectation', 'evidence_directness', 'coverage_limitation', 'next_action'",
			"Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.",
		},
		"frontend/topology-visualizer.js": {
			"export const VISUAL_NODES = 500;",
			"export const VISUAL_LINKS = 1000;",
			"export const VISUAL_ROUTES = 1000;",
			"maxDPR: 4,",
			"export function normalizeViewport(input = {})",
			"export function createViewTransform(input = {})",
			"export function updateViewTransform(input, delta = {})",
			"export function resetViewTransform()",
			"export function projectTopology(input, inputOptions = {})",
			"const mode = options.mode ?? MODE_2D;",
		},
		"frontend/topology-renderer.js": {
			"case 'topology-canvas':",
			"canvas.className = 'topology-canvas';",
			"canvas.setAttribute('role', 'img');",
			"canvas.setAttribute('aria-label', '경로 토폴로지 그래프');",
			"canvas.setAttribute('aria-describedby', 'topology-view-help topology-render-status');",
			"updateTopologyView({ ownerId, inputSignature, generation, mode, transform, viewport } = {})",
			"session.root.querySelector('canvas.topology-canvas')",
			"canvas.setAttribute('aria-label', `${session.mode === '3d' ? '3D' : '2D'} 경로 토폴로지 그래프`);",
			"session.localLimitation = 'canvas_context_unavailable';",
		},
		"frontend/index.html": {
			`<fieldset id="topology-view-controls" class="topology-view-controls">`,
			`<input type="radio" name="topology-view-mode" value="2d" checked>`,
			`<input type="radio" name="topology-view-mode" value="3d">`,
			`id="topology-view-reset"`,
			`id="topology-view-status"`,
		},
		"frontend/Dockerfile": {
			"COPY index.html styles.css app.js state.js topology-model.js topology-renderer.js topology-visualizer.js /usr/share/nginx/html/",
			"sha256sum app.js index.html state.js styles.css topology-model.js topology-renderer.js topology-visualizer.js | sort -k2 > .checknetwork-assets.sha256",
		},
		"frontend/state.test.js": {
			"assert.equal(contract.result_shapes.length, 31)",
			"assert.equal(contract.result_matrix_rows, 136)",
			"assert.equal(normalized.size, 8)",
			"assert.equal(mutations.length, 456)",
			"assert.equal(cancelledDetailMutations.length, 13",
		},
		"android/app/src/test/java/com/checknetwork/app/core/ReportParserTest.java": {
			"assertEquals(31, shapes.length())",
			"assertEquals(136,validRows)",
			"assertEquals(8,witnesses.length)",
		},
		"backend/api/traceroute_witness_fixture_test.go": {
			`name := "traceroute-" + witness.name + "-" + mode + "-report.json"`,
			"compact result retained raw attempts",
			"compact result retained representative topology",
			"raw failed topology present=%t",
		},
		"scripts/verify-release.sh": {
			"docker buildx build --no-cache --provenance=false",
			`--output=type=docker,dest="$api_archive",rewrite-timestamp=true`,
			`--output=type=docker,dest="$web_archive",rewrite-timestamp=true`,
			"API and Web archives are never loaded into the daemon",
			"api_base=alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc",
			"api_network_name=checknetwork-api-${api_nonce}-network",
			"Web verified offline: archive=%s config=%s manifest=%s rootfs=%s asset_manifest=%s",
			"release verified: api_archive=%s api_config=%s api_manifest=%s api_rootfs=%s api_binary=%s api_traceroute=%s",
			"container_name=checknetwork-api-${api_nonce}-smoke",
			"api_container_role=smoke",
			"API archive byte reproducibility mismatch",
			"API archive reproducibility mismatch for ",
			"only proves pinned-base mount compatibility",
			"it does not execute the derived API archive",
			`cp "$api_extract_one/checknetwork-api" "$publish_tmp"`,
			"publication_state=publish_pending",
			"cleanup_owned_resources",
			"release cleanup failed",
			"trap 'handle_signal 129' HUP",
			"trap 'handle_signal 130' INT",
			"trap 'handle_signal 143' TERM",
			"for asset in app.js index.html state.js styles.css topology-model.js topology-renderer.js topology-visualizer.js; do",
		},
		"scripts/verify_api_archive.py": {
			"archive_bytes: int = 512 * 1024 * 1024",
			"members: int = 4096",
			"member_bytes: int = 256 * 1024 * 1024",
			"member_total_bytes: int = 512 * 1024 * 1024",
			"layers: int = 64",
			"layer_bytes: int = 256 * 1024 * 1024",
			"expanded_layer_bytes: int = 512 * 1024 * 1024",
			"selected_bytes: int = 128 * 1024 * 1024",
			`EXPECTED_FILES = {"checknetwork-api", "traceroute"}`,
			"Establish the exact rootfs/history trust boundary before interpreting any",
			"extraction target must be caller-owned",
			"extraction target must be empty",
			"os.O_EXCL | getattr(os, \"O_NOFOLLOW\", 0)",
			"os.fsync(fd)",
			"extracted bytes/hash mismatch",
		},
		"scripts/verify_web_archive.py": {
			"archive_bytes: int = 512 * 1024 * 1024",
			"members: int = 4096",
			"member_bytes: int = 256 * 1024 * 1024",
			"member_total_bytes: int = 512 * 1024 * 1024",
			"PINNED_NGINX_CONFIG_DIGEST",
			"PINNED_NGINX_LAYER_DIGESTS",
			"PINNED_NGINX_DIFF_IDS",
			"PINNED_NGINX_HISTORY_DIGEST",
			"Only after the exact config/layer/rootfs/history base boundary is proven",
			"post-base layer links/devices/FIFO/socket are forbidden",
			"selected special file is forbidden",
			"base layer device/FIFO/socket is forbidden",
			"archive contains unexpected/missing regular members",
			"archive contains unexpected directories",
		},
		"scripts/release-resource-ownership.sh": {
			"api_network_state=create_pending",
			"api_container_state=create_pending",
			"recover_pending_api_container_id",
			"recover_pending_api_network_id",
		},
		"Makefile": {
			"ifeq ($(origin JAVA_HOME),undefined)",
			"JAVA_HOME := $(shell $(_derive_java_home))",
			"[ \"$$hops\" -lt 40 ] || exit 0",
			"target=$$(readlink \"$$resolved\" 2>/dev/null) || exit 0",
			"ifneq ($(_android_home_origin),undefined)",
			"else ifneq ($(_android_sdk_root_origin),undefined)",
			"_android_standard_sdk := $(HOME)/Android/Sdk",
			"_android_managed_sdk := $(HOME)/.local/share/checknetwork-android/sdk",
		},
		"android/app/src/main/java/com/checknetwork/app/core/ApiError.java": {
			"MAX_ERROR_BODY_CHARS = 64 * 1024",
			"MAX_DEPTH = 2",
			"MAX_PROPERTIES = 3",
			"MAX_TOKENS = 10",
			"MAX_WIRE_STRING_CHARS = 128",
			`private static final String INVALID_CODE = "invalid_server_response"`,
			`private static final String INVALID_MESSAGE = "The server returned an invalid error response."`,
			`if (!"error".equals(name)) throw malformed()`,
			`if ("code".equals(name)) code = nextBoundedString()`,
			`else if ("message".equals(name)) message = nextBoundedString()`,
			"if (code == null || message == null) throw malformed()",
			"if (parsed == null || nextToken() != JsonToken.END_DOCUMENT) throw malformed()",
			"if (definition == null) return invalid(status)",
			"return new ApiError(status, INVALID_CODE, INVALID_MESSAGE, false, null)",
			"definition.retryAfter ? parseRetryAfter(retryAfter, now) : null",
		},
		"android/app/src/main/java/com/checknetwork/app/ApiErrorPresentation.java": {
			"if (INVALID_CODE.equals(error.code())) return INVALID",
			`row(401, "unauthorized", R.string.api_error_unauthorized, false, false, true)`,
			"entry.retryable() != error.retryable()",
			"!entry.retryAfterAllowed() && error.retryAt().isPresent()",
		},
		"android/app/src/main/java/com/checknetwork/app/core/AnalysisPresentationRegistry.java": {
			"Traceroute capability was unavailable, so no route observation was established.",
			"Restore the supported traceroute capability, then repeat the bounded check.",
		},
		"android/app/src/main/java/com/checknetwork/app/MainActivity.java": {
			"retry.setVisibility(presentation.retryable()?View.VISIBLE:View.GONE)",
			"presentation.retryAfterAllowed()&&api.retryAt().isPresent()",
			"presentation.credentialFocus()&&bearer.getVisibility()==View.VISIBLE",
		},
		"android/app/src/main/java/com/checknetwork/app/network/RetryAfterHeaderExtractor.java": {
			"connection.getHeaderFields()",
			`!"Retry-After".equalsIgnoreCase(name)`,
			"value == null || ++count != 1",
			"return count == 1 ? found : null",
		},
		"scripts/assert_android_test_results.py": {
			`BINARY_METADATA_DIRECTORY = "binary"`,
			`BINARY_METADATA_FILES = frozenset(("output.bin", "output.bin.idx", "results.bin"))`,
			"MAX_BINARY_METADATA_FILE_BYTES = 64 * 1024 * 1024",
			"MAX_BINARY_METADATA_TOTAL_BYTES = 128 * 1024 * 1024",
			"Android unit-test XML must be a direct result child, not binary metadata",
			"Android unit-test XML basenames must be unique",
		},
	}
	for path, snippets := range sourceClaims {
		source := read(path)
		for _, snippet := range snippets {
			if !strings.Contains(source, snippet) {
				t.Errorf("source symbol/constant drift: %s lacks %q", path, snippet)
			}
		}
	}

	pythonTestMethod := regexp.MustCompile(`(?m)^\s+def test_[A-Za-z0-9_]+\(`)
	validatorCounts := map[string]int{
		"scripts/verify_api_archive_test.py": 28,
		"scripts/verify_web_archive_test.py": 25,
	}
	validatorTotal := 0
	for path, want := range validatorCounts {
		got := len(pythonTestMethod.FindAllString(read(path), -1))
		if got != want {
			t.Errorf("archive validator test count for %s=%d, want %d", path, got, want)
		}
		validatorTotal += got
	}
	if validatorTotal != 53 {
		t.Errorf("archive validator total=%d, want 53", validatorTotal)
	}
	realGateFakeCases := len(pythonTestMethod.FindAllString(read("scripts/verify_release_real_test_test.py"), -1))
	if realGateFakeCases != 12 {
		t.Errorf("exact real release-gate fake cases=%d, want 12", realGateFakeCases)
	}

	for _, path := range []string{"scripts/release-resource-ownership.sh", "scripts/verify-release.sh"} {
		releaseSource := read(path)
		for _, forbidden := range []string{"build_owned_api_image", "recover_pending_api_image_id", "cleanup_current_api_image", "docker image rm", "docker load", "docker import", "docker rmi", "--iidfile", "--tag", "api_image"} {
			if strings.Contains(releaseSource, forbidden) {
				t.Errorf("%s retains forbidden derived-image daemon lifecycle %q", path, forbidden)
			}
		}
	}

	docClaims := []string{
		"active", "stuck", "remaining", "exit 1", "13", "expected_status", "100..599", "final redirect response",
		"4,096", "8 lines", "512", "255", "server_greeting",
		"tls_certificate_expired", "tls_certificate_not_yet_valid", "tls_hostname_mismatch", "tls_untrusted", "tls_handshake_failed",
		"certificate_not_before", "certificate_not_after", "traceroute_execution_incomplete",
		"finding-contract-v1", "presentation-contract-v1", "23", "cause", "supporting_evidence", "expectation",
		"evidence_directness", "coverage_limitation", "next_action", "compact-v1", "4,097", "ASN",
		"699", "732", "MAX_TARGETS=20", "MAX_DOCUMENT_ELEMENTS=1200", "1,200", "single slot", "off-main", "8 MiB", "2 files/16 MiB", "32 entries/32 deletes", "15분",
		"--no-cache --provenance=false", "rewrite-timestamp=true", "tagless", "read-only", "trusted base", "archive/config/manifest/rootfs/asset_manifest",
		"api_archive", "api_config", "api_manifest", "api_rootfs", "api_binary", "api_traceroute",
		"512 MiB", "4,096", "256 MiB", "128 MiB", "/checknetwork-api", "/traceroute", "caller-owned empty", "fsync",
		"derived API", "derived Web", "daemon-tag", "compatibility", "정확히 두 개", "297+2", "pending",
		"pinned nginx config/layer/rootfs/history prefix", "post-base symlink/hardlink/device/FIFO/socket",
		"env -u JAVA_HOME -u ANDROID_HOME -u ANDROID_SDK_ROOT make ci", "Web 283", "ignored repository-root `checknetwork-api` binary",
		"result shapes 31", "result matrix 136", "structural mutations 85", "456", "cancelled-detail", "eight", "archive validators 53", "exact real release-gate fake cases 12", "blocker 0 / major 0",
		"full raw", "compact", "Geo aggregate", "optional representative", "runner-owned `cancelled`", "details",
		"umask 077/027/000", "canonical Docker inventory", "final clean-environment `make ci`", "manifest-derived temporary release",
		"trusted deployment", "nil capability", "runtime fallback", "Traceroute capability was unavailable, so no route observation was established.",
		"server_draining", "route → public auth → rate limit", "layer-local marker set",
		"system JDK 17", "portable JAVA_HOME", "SDK precedence", "fail closed",
		stage8BaseParent, "temporary commit보다 먼저", "physical Chrome/Firefox/Safari", "TalkBack/Switch Access", "HTTP/2", "signing/SBOM", "hosted CI",
	}
	for _, claim := range docClaims {
		if !strings.Contains(docs, claim) {
			t.Errorf("documentation lacks exact claim token %q", claim)
		}
	}
	currentDocClaims := []string{
		"Stage 9", "Web 283", "default 2D/optional 3D", "기본 2D", "선택 가능한 3D",
		"같은 validated", "같은 검증된", "report/analysis/diagnosis", "진단 결과를 변경하지 않는다",
		"interaction event와 resize에서만 redraw", "continuous animation", "no-continuous-animation redraw",
		"nodes 500", "links 1,000", "routes 1,000", "DOM 1,200", "DPR 4", "1,151",
		"Chrome이 없는 환경에서는 실제 브라우저/화면 판독기 검증을 완료했다고 주장하지 않는다",
		"physical Chrome/Firefox/Safari", "screen reader",
	}
	for _, claim := range currentDocClaims {
		if !strings.Contains(currentDocs, claim) {
			t.Errorf("current Stage 9 documentation lacks exact claim token %q", claim)
		}
	}
	producerFinding := ProducerFindingContract()
	if len(producerFinding.Findings) != 23 || len(producerFinding.ResultShapes) != 31 || producerFinding.ResultMatrixRows != 136 {
		t.Fatalf("producer finding source cardinality=%d/%d/%d, want 23/31/136",
			len(producerFinding.Findings), len(producerFinding.ResultShapes), producerFinding.ResultMatrixRows)
	}

	requiredByDoc := map[string][]string{
		"README.md": {
			"두 archive bytes", "load, tag, import, delete 또는 execute하지 않는다",
			"API network **정확히 두 개**", "derived API archive 실행을 증명하지 않는다",
			"daemon-success/CLI-response-loss", "HUP/INT/TERM",
			"body 64 Ki UTF-16 code units", "depth 2", "properties 3", "tokens 10", "128 code units",
			"`invalid_server_response`", "typed body와 retryability를 바꾸지 않고 timestamp만 생략",
			"`MAX_TARGETS=20`", "`MAX_DOCUMENT_ELEMENTS=1200`", "Stage 9 Web source inventory는 283 tests", "result shapes 31", "expanded result matrix 136", "eight producer traceroute witness fixtures", "runner-owned `cancelled`", "publication 전에 report 전체를 거부", "exact real release-gate fake cases 12",
			"기본 2D와 선택 가능한 3D perspective", "같은 검증된 facts", "report, analysis 또는 diagnosis를 바꾸지 않는다", "interaction/resize 때만 다시 그려 continuous animation을 하지 않는다",
			"nodes 500, links 1,000, routes 1,000", "document DOM 1,200", "DPR 최대 4", "측정된 최대 DOM은 1,151", "실제 브라우저와 screen reader 수동 검증도 pending",
			"ignored `checknetwork-api` binary", "비파괴 요청 때문에 제거하지 않고 retained",
		},
		"SPEC.md": {
			"Offline validator limits는 archive 512 MiB", "selected-file total 128 MiB", "layer-member interpretation 전에 trusted base",
			"`/checknetwork-api`와 `/traceroute` 정확히 두 regular files", "no-follow/create-exclusive/fsync/write/readback hash",
			"두 derived API/Web archive 모두", "API daemon-owned role", "정확히 두 개", "derived archive의 실행 증거도 아니다",
			"`api_archive`, `api_config`, `api_manifest`, `api_rootfs`, `api_binary`, `api_traceroute`", "HUP/INT/TERM은 각각 129/130/143",
			"depth≤2", "properties≤3", "tokens≤10", "field name/value≤128 code units",
			"`retryAt=now+seconds`", "`unauthorized`만 visible credential field에 focus",
			"`MAX_TARGETS=20`", "`MAX_DOCUMENT_ELEMENTS=1200`", "현재 Web inventory는 283 tests", "31 result shapes", "136-row", "exact eight producer traceroute witness fixtures", "full raw", "Geo aggregate", "optional representative", "runner-owned `cancelled`", "whole report를 atomic 거부", "fake cases 12",
			"기본 `2D 그래프`와 선택 가능한 `3D 그래프`", "같은 검증된 topology facts", "report, analysis, diagnosis를 변경하지 않는다", "interaction 또는 resize에서만 redraw",
			"nodes 500, links 1,000, routes 1,000", "DOM 1,200", "DPR은 최대 4", "현재 최대 fixture 측정값은 1,151", "Physical Chrome/Firefox/Safari", "실제 screen reader",
			"ignored `checknetwork-api` binary", "비파괴 요청 때문에 제거하지 않고 retained",
		},
		"docs/ARCHITECTURE.md": {
			"exact pinned Alpine layer/diff-ID/history prefix", "dir-fd, no-follow, exclusive create, fsync 및 readback hash",
			"두 derived API/Web archive는 daemon에 load, daemon-tag, import, delete 또는 execute하지 않는다",
			"API가 소유하는 daemon role은 isolated API network와 smoke container 정확히 두 개", "fixed signal exit status는 129/130/143",
			"body≤64 Ki UTF-16 code units", "depth≤2", "properties≤3", "tokens≤10", "wire name/value≤128 code units",
			"Header 실패는 typed body를 invalid로 바꾸지 않는다",
			"`MAX_TARGETS=20`", "`MAX_DOCUMENT_ELEMENTS=1200`", "31 result shapes", "136-row", "presentation 10은 31 result shapes와 별개", "exact eight producer traceroute witness fixtures", "full raw attempts", "Geo aggregate", "optional representative topology", "runner-owned `cancelled`", "fake cases 12",
			"Stage 9은 동일한 validated model을 기본 2D 또는 optional 3D perspective Canvas로 투영", "mode는 report/analysis/diagnosis를 바꾸지 않는다", "continuous animation 없이 pointer/keyboard/reset/resize interaction 때만 redraw",
			"nodes 500/links 1,000/routes 1,000", "global `MAX_DOCUMENT_ELEMENTS=1200`", "DPR≤4", "maximum fixture는 1,151 DOM elements",
			"umask 077/027/000 세 pass", "canonical Docker inventory equality", "ignored `checknetwork-api` binary",
		},
		"docs/API.md": {
			"code `invalid_server_response`", "message `The server returned an invalid error response.`",
			"`retryable=false`", "no `retryAt`", "exactly one canonical header", "registry retryability를 무효화하지 않는다",
			"`unauthorized`만 credential control이 visible할 때",
			"`targets`는 1~20개", "`MAX_TARGETS=20`", "global `MAX_DOCUMENT_ELEMENTS=1200`", "31 result shapes", "136-row", "eight traceroute witness fixtures", "full raw", "Geo aggregate", "optional representative", "runner-owned `cancelled`", "healthy", "error_code", "whole report를 atomic 거부",
		},
		"docs/OPERATIONS.md": {
			"hard bounds는 archive 512 MiB", "outer/layer members 4,096", "expanded selected-file total 128 MiB",
			"layer tar member 해석 전에 trusted base", "caller-owned empty real directory", "no-follow + create-exclusive", "stateful rollback",
			"API network와 `smoke` container **정확히 두 개**", "mount compatibility만 증명", "derived API archive 실행을 증명하지 않는다",
			"Derived API archive와 derived Web archive", "load, daemon-tag, import, delete 또는 execute하지 않는다",
			"`api_archive`, `api_config`, `api_manifest`, `api_rootfs`, `api_binary`, `api_traceroute`", "daemon-success/CLI-response-loss",
			"system JDK 17", "portable JAVA_HOME", "SDK precedence", "첫 explicit override", "fail closed",
			"umask 077/027/000 세 pass", "canonical Docker inspect JSON projection", "Exact fake gate는 12 cases",
			"Web 260 tests", "direct-child XML 297 tests + variant canaries 2", "result shapes 31", "result matrix 136", "ignored `checknetwork-api` binary", "비파괴 요청 때문에 제거하지 않고 retained",
		},
		"docs/TESTING.md": {
			"direct-child `TEST-*.xml`", "exact `binary/` direct directory", "`output.bin`, `output.bin.idx`, `results.bin`",
			"≤64 MiB", "≤128 MiB", "bounded **non-evidence companion**", "duplicate/ambiguous basename",
			"각 variant direct-child XML 297 / variant-contract 2", "`297+2`", "pending clean-environment canonical gate",
			"env -u JAVA_HOME -u ANDROID_HOME -u ANDROID_SDK_ROOT make ci", "Stage 9 source inventory는 Web 283 tests", "456 fixture-derived semantic mutations", "generic cancelled-detail rejection 13", "eight witness fixtures", "ignored `checknetwork-api` binary",
			"exact 512 MiB/4,096/256 MiB/512 MiB/64/256 MiB/512 MiB/128 MiB bounds",
			"smoke container+API network 정확히 두 개", "load/daemon-tag/import/delete/execute되지 않음",
			"api_archive/api_config/api_manifest/api_rootfs/api_binary/api_traceroute", "current source와 fixture cardinality `283`", "`23/31/136`", "archive validators 53",
			"`MAX_TARGETS=20`", "global `MAX_DOCUMENT_ELEMENTS=1200`", "exact real release-gate fake cases 12",
			"default 2D/optional 3D Canvas projection", "같은 facts", "no-refetch/no-diagnosis-change", "no-continuous-animation redraw",
			"Nodes 500/links 1,000/routes 1,000/DPR≤4", "global DOM≤1,200", "현재 최대 fixture 1,151", "physical Chrome/Firefox/Safari", "실제 screen-reader",
		},
		"docs/PLAN.md": {
			"canonical API+Web release verifier", "**invalid**", "final pre-manifest clean-environment `make ci`", "Android debug/release each 297 + 2 variant canaries",
			"blocker 0 / major 0", "ignored root `checknetwork-api` binary", "archive validators 53",
			"double-build-equal tagless offline archives", "safe API extraction/publication", "six API/five Web output fields",
			"five-way independent precommit review", "어떤 final claim도 하지 않는다",
			"`MAX_TARGETS=20`", "`MAX_DOCUMENT_ELEMENTS=1200`", "source-level Web inventory 283", "result shapes 31", "expanded matrix 136", "456 semantic mutations", "13 generic cancelled-detail rejections", "eight traceroute witness fixtures", "raw-only", "compact/Geo aggregate", "optional", "generic cancelled", "fake cases 12", "ignored root `checknetwork-api` binary",
			"default 2D/optional 3D Canvas", "같은 validated facts", "report/analysis/diagnosis는 바꾸지 않는다", "no continuous animation",
			"nodes 500, links 1,000, routes 1,000, DOM 1,200(현재 최대 1,151), DPR 4", "physical browser 및 screen-reader manual acceptance",
		},
		stage8PlanPath: {
			"exact local code `invalid_server_response`", "exact `binary/` companion", "297 direct-child XML tests + 2 variant-contract tests",
			"API hard limits are archive 512 MiB", "caller-owned empty real directory", "never daemon-loaded, daemon-tagged, imported, deleted, or executed",
			"API owns one smoke container and one API network", "Success output is exactly six API fields",
			"Documentation changes invalidate prior current-byte CI/count evidence", "Web 260 tests", "result shapes 31", "expanded result matrix 136", "456 fixture-derived semantic mutations", "13 generic cancelled-detail rejections", "all eight producer traceroute witness fixtures", "ignored repository-root `checknetwork-api` binary",
			"Final post-doc `env -u JAVA_HOME -u ANDROID_HOME -u ANDROID_SDK_ROOT make ci` remains pending",
			"No final manifest, commit, release, five-way review, exact-SHA, or main-integration claim is made here.",
			"`MAX_TARGETS=20`", "`MAX_DOCUMENT_ELEMENTS=1200`", "fake cases 12", "three umasks 077/027/000", "canonical Docker inventory",
		},
		stage9PlanPath: {
			"Stage 9 implementation is pending final gates/review, not complete", "기본 선택은 `2D 그래프`", "`3D 그래프`를 선택하면 동일한 node/link set",
			"데이터나 분석 결과를 바꾸지 않는 순수 view projection", "진단 결과를 변경하지 않는다", "interaction event와 resize에서만 redraw",
			"Canvas 하나와 고정 control 수만 추가", "document-wide 1,200 element 한도", "nodes 500/links 1,000/routes bounded", "DPR을 bounded finite 값으로 clamp",
			"exact Web production asset set", "Chrome이 없는 환경에서는 실제 브라우저/화면 판독기 검증을 완료했다고 주장하지 않는다",
		},
	}
	for path, claims := range requiredByDoc {
		text := read(path)
		for _, claim := range claims {
			if !strings.Contains(text, claim) {
				t.Errorf("%s lacks exact documentation contract phrase %q", path, claim)
			}
		}
	}

	var errors struct {
		Errors []struct {
			Key     string `json:"key"`
			Status  int    `json:"status"`
			Message string `json:"message"`
		} `json:"errors"`
		StructuralMutations []json.RawMessage `json:"structural_mutations"`
	}
	if err := json.Unmarshal([]byte(read("testdata/api-error-contract.json")), &errors); err != nil {
		t.Fatal(err)
	}
	if len(errors.Errors) != 16 {
		t.Fatalf("API error fixture rows=%d, want 16", len(errors.Errors))
	}
	if len(errors.StructuralMutations) != 85 {
		t.Fatalf("API error structural mutations=%d, want 85", len(errors.StructuralMutations))
	}
	apiDoc := read("docs/API.md")
	for _, row := range errors.Errors {
		if !strings.Contains(apiDoc, "| `"+row.Key+"` | ") {
			t.Errorf("docs/API.md lacks API error registry key %q", row.Key)
		}
		if !strings.Contains(apiDoc, row.Message) {
			t.Errorf("docs/API.md lacks fixed API error message for %q", row.Key)
		}
	}

	var finding struct {
		Findings         []json.RawMessage `json:"findings"`
		ResultShapes     []json.RawMessage `json:"result_shapes"`
		ResultMatrixRows int               `json:"result_matrix_rows"`
	}
	if err := json.Unmarshal([]byte(read("testdata/finding-contract.json")), &finding); err != nil {
		t.Fatal(err)
	}
	if len(finding.Findings) != 23 {
		t.Fatalf("finding fixture rows=%d, want 23", len(finding.Findings))
	}
	if len(finding.ResultShapes) != 31 || finding.ResultMatrixRows != 136 {
		t.Fatalf("finding fixture result cardinality=%d/%d, want 31/136", len(finding.ResultShapes), finding.ResultMatrixRows)
	}
	if len(finding.Findings) != len(producerFinding.Findings) || len(finding.ResultShapes) != len(producerFinding.ResultShapes) || finding.ResultMatrixRows != producerFinding.ResultMatrixRows {
		t.Fatalf("finding source/fixture cardinality drift: source=%d/%d/%d fixture=%d/%d/%d",
			len(producerFinding.Findings), len(producerFinding.ResultShapes), producerFinding.ResultMatrixRows,
			len(finding.Findings), len(finding.ResultShapes), finding.ResultMatrixRows)
	}
	var presentation struct {
		SemanticKeys        []string          `json:"semantic_keys"`
		EvidenceSignals     []json.RawMessage `json:"evidence_signals"`
		CoverageSignals     []string          `json:"coverage_signals"`
		ActionRelationships []json.RawMessage `json:"action_relationships"`
		Scenarios           []json.RawMessage `json:"scenarios"`
	}
	if err := json.Unmarshal([]byte(read("testdata/presentation-contract.json")), &presentation); err != nil {
		t.Fatal(err)
	}
	if len(presentation.SemanticKeys) != 6 || len(presentation.EvidenceSignals) != 10 || len(presentation.CoverageSignals) != 21 || len(presentation.ActionRelationships) != 23 || len(presentation.Scenarios) != 33 {
		t.Fatalf("presentation fixture cardinality=%d/%d/%d/%d/%d, want 6/10/21/23/33",
			len(presentation.SemanticKeys), len(presentation.EvidenceSignals), len(presentation.CoverageSignals), len(presentation.ActionRelationships), len(presentation.Scenarios))
	}
}

func TestDocumentationContractHasNoStaleStageOrBrokenRelativeLinks(t *testing.T) {
	root := documentationRepositoryRoot(t)
	paths := []string{"README.md", "SPEC.md", "docs/API.md", "docs/ARCHITECTURE.md", "docs/OPERATIONS.md", "docs/TESTING.md", "docs/PLAN.md", stage8PlanPath, stage9PlanPath}
	stale := []string{
		"현재 단계: Stage 7",
		"현재 uncommitted Stage 7",
		"Stage 7 commit 후",
		"Web archive/image/config/rootfs/asset-manifest digests",
		"exact Web archive/image/config/rootfs equality",
		"deterministic Web archive/image/config/rootfs/assets",
		"predictable release names",
		"predictable resource names",
		"PID-derived release names",
		"PID-derived resource names",
		"PID-based ownership",
		"PID 기반 소유권",
		"name alone proves ownership",
		"cleanup unconditionally removes",
		"unconditionally deletes owned resources",
		"무조건 삭제한다",
		"조건 없이 삭제한다",
		"API image에 random ownership label을 넣는다",
		"API image config에 random ownership label을 넣는다",
		"API image에 nonce/owner label을 주입한다",
		"API resource role은 정확히 6개",
		"Shared daemon의 정확한 API resource role 6개",
		"Shared daemon의 API role graph는 정확히 6개",
		"API exact six roles",
		"exactly six roles",
		"API six/Web two daemon roles",
		"external tag/IID",
		"durable `--iidfile`",
		"temporary external `--iidfile`",
		"image_one",
		"api_image",
		"total 282 / variant-contract 2",
		"282/2 per variant",
		"282 total / 2 variant-contract tests per variant",
		"API binary/image digest",
		"API artifact/image",
		"Web 252",
		"Web252",
		"Web 255",
		"Web255",
		"Web 256",
		"Web256",
		"stage8-manifest-v5",
		"stage8-manifest-v8",
		"124-record",
		"124 records",
		"124-record manifest",
		"current manifest hash",
		"current manifest SHA",
		"final manifest hash",
		"final manifest SHA",
		"Android 288",
		"Android288",
		"Android 290",
		"Android290",
		"288/2",
		"288 total",
		"result shapes 10",
		"10 result shapes",
		"Web 258",
		"Web258",
		"Android 291",
		"Android291",
		"direct-child XML 291",
		"each 291",
		"291+2",
		"22-finding",
		"22 finding",
		"22 findings",
		"exact 15 `(status,code)`",
		"those 15 pairs",
		"replacement current-byte",
		"current-byte pre-manifest observation",
		"Replacement current-byte evidence",
		"`make ci`: exit 0",
		"`make ci`로 실행했고 exit 0",
	}
	linkPattern := regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	oldNameOwnershipPattern := regexp.MustCompile(`(?i)(predictable|pid[- ](?:derived|based)).{0,100}(ownership|resource name|release name|docker name)|(ownership|resource name|release name|docker name).{0,100}(predictable|pid[- ](?:derived|based))`)
	for _, relative := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, phrase := range stale {
			if strings.Contains(text, phrase) {
				t.Errorf("%s contains stale phrase %q", relative, phrase)
			}
		}
		stage9Section := false
		for _, line := range strings.Split(text, "\n") {
			lower := strings.ToLower(line)
			trimmed := strings.TrimSpace(lower)
			if strings.HasPrefix(trimmed, "#") {
				if strings.Contains(trimmed, "stage 9") {
					stage9Section = true
				} else if strings.Contains(trimmed, "stage 8") {
					stage9Section = false
				}
			}
			if strings.Contains(lower, "stage 8") &&
				(strings.Contains(lower, "historical") || strings.Contains(lower, "record") || strings.Contains(line, "기록") || strings.Contains(line, "기존")) {
				stage9Section = false
			}
			if staleArchiveValidatorClaim(line) {
				t.Errorf("%s contains a stale archive-validator cardinality claim: %q", relative, line)
			}
			if staleCurrentCardinalityClaim(line) {
				t.Errorf("%s contains a stale current-cardinality claim: %q", relative, line)
			}
			if staleStage9WebCardinalityClaim(line, stage9Section || strings.Contains(lower, "stage 9")) {
				t.Errorf("%s contains stale Stage 9 Web cardinality: %q", relative, line)
			}
			if oldNameOwnershipPattern.MatchString(line) && !strings.Contains(lower, "reject") && !strings.Contains(line, "거부") {
				t.Errorf("%s contains an old predictable/PID-derived Docker ownership claim: %q", relative, line)
			}
			if !strings.Contains(lower, "random") || !strings.Contains(lower, "label") ||
				(!strings.Contains(lower, "api image") && !strings.Contains(lower, "image config")) {
				continue
			}
			negative := strings.Contains(lower, "never inject") || strings.Contains(lower, "does not") ||
				strings.Contains(line, "넣지 않") || strings.Contains(line, "주입하지 않") ||
				strings.Contains(line, "전달하지 않") || strings.Contains(line, "들어가지 않") ||
				strings.Contains(lower, "outside") || strings.Contains(line, "밖")
			if !negative {
				t.Errorf("%s contains a random API-image ownership-label claim without deterministic-config independence: %q", relative, line)
			}
		}
		for _, match := range linkPattern.FindAllStringSubmatch(text, -1) {
			target := strings.SplitN(match[1], "#", 2)[0]
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			resolved := filepath.Join(root, filepath.Dir(filepath.FromSlash(relative)), filepath.FromSlash(target))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s has broken relative link %q: %v", relative, match[1], err)
			}
		}
	}
}

func TestStaleArchiveValidatorClaimDetection(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		stale bool
	}{
		{name: "spaced bare combination", line: "archive validators 28 + 20", stale: true},
		{name: "compact bare combination", line: "archive validators 28+20", stale: true},
		{name: "labeled combination", line: "archive validators API 28 + Web 20", stale: true},
		{name: "postfixed labels", line: "archive validators 28 API + 20 Web = 48", stale: true},
		{name: "obsolete total only", line: "archive validator total = 48", stale: true},
		{name: "obsolete unpunctuated total", line: "archive validator total 48", stale: true},
		{name: "obsolete inverted total", line: "48 archive-validator tests", stale: true},
		{name: "obsolete web count phrase", line: "archive validator suite has Web 20 tests", stale: true},
		{name: "obsolete postfix web count phrase", line: "archive validator suite has 20 Web tests", stale: true},
		{name: "current cardinality", line: "archive validators API 28 + Web 25 = 53", stale: false},
		{name: "historical unrelated api level", line: "Android API 28 support followed API 20 support in 48 releases", stale: false},
		{name: "unrelated archive limits", line: "archive format history includes 20 layers and 48 members", stale: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := staleArchiveValidatorClaim(test.line); got != test.stale {
				t.Fatalf("staleArchiveValidatorClaim(%q)=%t, want %t", test.line, got, test.stale)
			}
		})
	}
}

func TestStaleCurrentCardinalityClaimDetection(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		stale bool
	}{
		{name: "old result shape and matrix", line: "Current stable source cardinality is findings 23 / result shapes 32 / expanded result matrix 137", stale: true},
		{name: "old compact cardinality", line: "current source inventory is `23/32/137`", stale: true},
		{name: "old web count", line: "현재 stable source inventory는 Web 259 tests다.", stale: true},
		{name: "old android count", line: "Current source cardinality: Android debug/release each 294 + 2 variant canaries", stale: true},
		{name: "old android korean count", line: "현재 source inventory는 Android 각각 direct-child XML 294 tests + variant canaries 2다.", stale: true},
		{name: "current cardinality", line: "Current stable source cardinality is 23 findings / 31 result shapes / 136 rows, Web 260, Android each 297 + 2", stale: false},
		{name: "historical web record", line: "Historical record: current source inventory was Web 259 tests before the synchronization slice.", stale: false},
		{name: "historical matrix record", line: "이전 current source cardinality는 result shapes 32 / result matrix 137이었다.", stale: false},
		{name: "explicit stale rejection documentation", line: "The documentation contract rejects stale current-cardinality 23/32/137, Web 259, and Android 294 claims.", stale: false},
		{name: "unrelated numbers", line: "The current API accepts Android API 32 and Web port 259 while matrix row 137 is discussed historically.", stale: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := staleCurrentCardinalityClaim(test.line); got != test.stale {
				t.Fatalf("staleCurrentCardinalityClaim(%q)=%t, want %t", test.line, got, test.stale)
			}
		})
	}
}

func TestStaleStage9WebCardinalityClaimDetection(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		stage9Context bool
		stale         bool
	}{
		{name: "explicit current Stage 9", line: "현재 Stage 9 source inventory는 Web 260 tests다.", stage9Context: true, stale: true},
		{name: "current line inside Stage 9 section", line: "Current stable source inventory is Web260.", stage9Context: true, stale: true},
		{name: "current Stage 9 count", line: "현재 Stage 9 source inventory는 Web 283 tests다.", stage9Context: true, stale: false},
		{name: "historical Stage 8 section", line: "Current stable source cardinality was Web 260.", stage9Context: false, stale: false},
		{name: "explicit historical record", line: "Historical record: current source inventory was Web 260 tests.", stage9Context: true, stale: false},
		{name: "Stage 8 plan keeps its required count", line: "Documentation changes invalidate prior current-byte CI/count evidence; Web 260 tests.", stage9Context: false, stale: false},
		{name: "stale rejection documentation", line: "Stage 9 current documentation rejects stale Web 260 claims.", stage9Context: true, stale: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := staleStage9WebCardinalityClaim(test.line, test.stage9Context); got != test.stale {
				t.Fatalf("staleStage9WebCardinalityClaim(%q, %t)=%t, want %t", test.line, test.stage9Context, got, test.stale)
			}
		})
	}
}

var (
	archiveValidatorContextPattern = regexp.MustCompile(`(?i)\barchive[- ]validators?(?:\s+suite)?\b`)
	staleArchiveValidatorPatterns  = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:api\s*)?28(?:\s*api)?\s*\+\s*(?:web\s*)?20(?:\s*web)?\b`),
		regexp.MustCompile(`(?i)=\s*48\b`),
		regexp.MustCompile(`(?i)\b(?:web\s*20|20\s*web)\b`),
		regexp.MustCompile(`(?i)\b(?:total|cardinality)(?:\s+(?:is|of))?\s*[:=]?\s*48\b`),
		regexp.MustCompile(`(?i)\b48\s*(?:tests?|total|archive[- ]validators?)\b`),
	}
	currentCardinalityContextPattern   = regexp.MustCompile(`(?i)(?:\bcurrent\b|현재).{0,80}(?:\bcardinality\b|\binventory\b)|\b(?:current|stable)\s+source\s+(?:cardinality|inventory)\b`)
	historicalCardinalityPrefixPattern = regexp.MustCompile(`(?i)^\s*(?:[-*]\s*)?(?:(?:historical|previous|prior|formerly|obsolete|old)\b|(?:과거|이전|당시|역사적)(?:\s|:))`)
	stage9Web260Pattern                = regexp.MustCompile(`(?i)\bweb\s*260\b`)
	staleCurrentCardinalityPatterns    = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bresult\s+shapes?\s*32\b|\b32\s+result\s+shapes?\b`),
		regexp.MustCompile(`(?i)\b(?:expanded\s+(?:semantic\s+)?(?:result\s+)?matrix|result\s+matrix)\s*137\b|\b137-row\b`),
		regexp.MustCompile(`\b23\s*/\s*32\s*/\s*137\b`),
		regexp.MustCompile(`(?i)\bweb\s*259\b`),
		regexp.MustCompile(`(?i)\bandroid\b.{0,120}\b294\b|\bdirect-child\s+XML\s*294\b|\b294\s*(?:\+\s*2|tests?)\b`),
	}
)

func staleArchiveValidatorClaim(line string) bool {
	if !archiveValidatorContextPattern.MatchString(line) {
		return false
	}
	for _, pattern := range staleArchiveValidatorPatterns {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

func staleCurrentCardinalityClaim(line string) bool {
	if !currentCardinalityContextPattern.MatchString(line) || historicalCardinalityPrefixPattern.MatchString(line) {
		return false
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "stale") && (strings.Contains(lower, "reject") || strings.Contains(line, "거부")) {
		return false
	}
	for _, pattern := range staleCurrentCardinalityPatterns {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

func staleStage9WebCardinalityClaim(line string, stage9Context bool) bool {
	if !stage9Context || !currentCardinalityContextPattern.MatchString(line) || historicalCardinalityPrefixPattern.MatchString(line) {
		return false
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "stale") && (strings.Contains(lower, "reject") || strings.Contains(line, "거부")) {
		return false
	}
	return stage9Web260Pattern.MatchString(line)
}

func documentationRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate documentation contract test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}
