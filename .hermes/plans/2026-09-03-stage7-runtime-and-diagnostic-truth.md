# Stage 7 Runtime Admission and Diagnostic Truth Implementation Plan

> **For Hermes:** Use subagent-driven development with strict RED→GREEN slices, independent exact-tree review, one verified Stage 7 commit, and exact-SHA clean-archive verification.

**Goal:** Close the eight reproducible post-Stage-6 runtime/product defects while keeping transport success distinct from diagnostic truth, bounding pre-handler resources, and preserving Web/Android privacy and schema parity.

**Architecture:** Add admission at the earliest resource boundary (listener accept), make all deadlines one absolute operation budget, preserve typed errors through producer→API→consumer layers, and keep liveness/readiness independent from authenticated/rate-limited business APIs. Extend telemetry and release metadata only with closed, low-cardinality schemas. Do not bundle broader packet-loss/jitter/history/multi-vantage capability work into this defect-remediation stage.

**Tech Stack:** Go 1.22 `net/http` (matching `go.mod` and the current builder), Java 17/Android API 26+, Node.js browser modules, Docker Compose v2, nginx, POSIX shell, Gradle 8.11.1.

---

## Baseline and finding classification

- Base commit: `7dc21afe22f9e5264ebaeddd4beb9481a0bc3da8`.
- Exact-SHA Stage 6 closure: blocker 0, major 0; clean archive passed Go 407, Web 163, Android debug/release 147 each.
- Runtime defects in this stage:
  1. unbounded pre-handler HTTP connection admission;
  2. post-check-deadline checker/HTTP body results can be committed healthy;
  3. public mode breaks or self-rate-limits the supplied healthcheck;
  4. diagnostic failures are invisible in otherwise-successful transport telemetry;
  5. release image/binary reports version `dev`;
  6. Android human export mislabels checker runtime failures as traceroute failures;
  7. Android capability discovery and report submission use separate end-to-end budgets;
  8. valid reduced capabilities are reported as malformed responses.
- Horizontal parity fix included with defect 6: Android full-topology link parser must validate producer field `latency_delta_ms`, not unrelated `latency_ms`.
- Deferred capability gaps, not defects: Web live capability-driven form, Android topology/Geo UI parity, raw-download warning parity, history/baselines, packet loss/jitter/throughput/MTU/local-link/VPN/proxy facts, cross-check hypothesis ranking, metrics exporter, hosted CI/SBOM/signing, physical browser/device/HTTP2 validation.

## Global constraints

- Do not weaken public-mode SSRF/DNS-rebinding/proxy-bypass protections.
- Do not claim a Go goroutine, injected checker, or transport can be forcibly terminated.
- Credential, target, IP, raw path/query/body, provider prose, panic/error prose, request ID, and report ID must not become metric labels or diagnostic telemetry fields.
- Keep existing full response `<=8 MiB`, compact response `<1 MiB`, request body `<=1 MiB`, and Web/Android parser limits.
- Admission tokens remain held until the underlying resource actually ends; timeout/cancellation alone must not fabricate release.
- Every behavior change follows a recorded focused RED, minimal GREEN, fresh focused tests, then full gates.
- Agents edit only the assigned Stage 7 worktree and owned files; no staging, commit, reset, or main-tree edits.

---

## Task 1: Bound connections before `net/http` creates handler goroutines

**Status:** ✅ Implemented and verified by focused connection/body admission tests and blocker/major 0 review. Postcommit clean-archive verification remains pending below.

**Objective:** Enforce a hard process-wide active accepted-connection ceiling before `http.Server.Serve` spawns per-connection goroutines, and separately bound authenticated requests waiting to decode bodies.

**Files:**
- Modify: `cmd/checknetwork-api/main.go`
- Modify: `cmd/checknetwork-api/main_test.go`
- Modify: `backend/api/server.go`
- Modify: `backend/api/server_test.go`
- Modify: `SPEC.md`
- Modify: `compose.yaml`
- Modify: `docs/OPERATIONS.md`, `docs/ARCHITECTURE.md`, `docs/TESTING.md`, `README.md`

**RED tests:**
1. Start production server through a counting/barrier listener with ceiling 32; open 32 incomplete headers, then 16 surplus connections.
2. Assert active accepted connections never exceed 32, surplus sockets close promptly without handler allocation, and capacity recovers after close/header timeout.
3. Run public mode with no Authorization and prove the bound applies before authentication/rate/report admission.
4. After valid authentication, block 8 request bodies with body-admission ceiling 8; the ninth receives a fixed non-reflecting overload response or connection close according to the documented boundary, without report/checker admission.
5. Cancellation, TLS/listener errors, panic-free accept retry, shutdown, and normal-request recovery release leases exactly once.
6. Reject declared `Content-Length >1 MiB` before body-token acquisition. After one request has decoded and blocks in its checker, a second request must acquire the already-released body token.
7. Sustained overflow must not create a tight accept/close CPU loop; bounded backoff must remain interruptible by listener close/shutdown.

**Implementation:**
- Replace production `ListenAndServe` with explicit `net.Listen`, wrap that exact listener, and call `server.Serve(wrappedListener)`. Tests must fail if production bypasses the wrapper.
- Add a small `net.Listener` wrapper that nonblockingly acquires a token immediately after `Accept`; if unavailable, close the socket, apply bounded interruptible rejection backoff, and continue without returning the socket to `net/http`. Wrap admitted connections so `Close` returns the lease exactly once.
- Use a conservative fixed/default ceiling tied to Compose PID/memory/FD assumptions and an audited hard maximum. Validate env configuration before allocation.
- Add an independent authenticated body-decode semaphore after route/auth/rate checks. Reject declared oversized `Content-Length` before acquiring it; then nonblockingly acquire, install `MaxBytesReader`, decode exactly one object through EOF, and release immediately after decode on every success/error/panic path—never through validation, execution, serialization, or writing.
- Ensure `http.Server.Shutdown` closes the listener and all leases converge to zero.

**GREEN commands:**
- `go test -count=100 ./cmd/checknetwork-api -run 'Test.*ConnectionAdmission'`
- `go test -race -count=20 ./cmd/checknetwork-api ./backend/api -run 'Test.*(Connection|Body).*Admission'`
- deterministic 256-socket probe must show active/goroutine growth bounded by configured ceiling.

---

## Task 2: Make per-check deadlines terminal and propagate HTTP body failures

**Status:** ✅ Implemented and verified by focused deadline/HTTP read tests and blocker/major 0 review.

**Objective:** Never publish healthy results after a checker-specific deadline, and never discard bounded HTTP response-body read failures.

**Files:**
- Modify: `backend/diagnostic/runner.go`
- Modify: `backend/diagnostic/runner_test.go`
- Modify: `backend/diagnostic/http.go`
- Modify: `backend/diagnostic/http_test.go`
- Modify: `backend/api/server_test.go` or `telemetry_test.go`

**RED tests:**
1. Context-aware checker waits for its 100ms `checkCtx.Done()` then returns healthy before the 1s report grace ends; require `unreachable/timeout`.
2. Coordinate checker return and deadline with barriers to prove terminal arbitration is deterministic and late success cannot win after observable expiry.
3. HTTP server sends `200 Content-Length: 100`, flushes one byte, stalls through deadline; require timeout, non-healthy analysis, and timeout diagnostic telemetry.
4. Body read error unrelated to timeout becomes a stable non-reflecting transport/read code rather than healthy.
5. Latency is measured only after the bounded observation required by the checker contract, not immediately after headers.

**Implementation:**
- Carry the checker-specific context into terminal completion arbitration and call `terminal.complete(result, checkCtx)` before `cancelCheck`, `cancelWorker`, or any locally initiated cleanup cancellation. Priority at `Check` return is external parent/supervisor cancellation → cancelled, checker deadline → timeout, otherwise returned result. Cleanup cancellation must never become a cause.
- In HTTP checker, inspect bounded body read error and context state. Preserve size-limit classification separately from timeout/cancel/read failure.
- Keep active checker lease until actual checker return/panic.
- Add a normal pre-deadline healthy control plus parent-cancel, supervisor-cancel, checker-deadline, and simultaneous-barrier cases.

**GREEN commands:**
- `go test -count=100 ./backend/diagnostic -run 'Test.*(CheckerDeadline|HTTPBodyDeadline)'`
- `go test -race -count=20 ./backend/diagnostic ./backend/api -run 'Test.*(Deadline|Timeout|Telemetry)'`

---

## Task 3: Separate unauthenticated liveness/readiness from business APIs

**Status:** ✅ Implemented and verified by focused operational/public-mode/Compose contract tests and blocker/major 0 review.

**Objective:** Make container liveness/readiness reliable in public mode without consuming authentication or client rate quota.

**Files:**
- Modify: `backend/api/server.go`
- Modify: `backend/api/server_test.go`
- Modify: `backend/api/telemetry.go`, `telemetry_test.go` only if fixed routes/events change
- Modify: `cmd/checknetwork-api/main.go`, `cmd/checknetwork-api/main_test.go`
- Modify: `backend/diagnostic/runner.go`, `backend/diagnostic/runner_test.go`
- Modify: `backend/diagnostic/traceroute_command_unix.go`, `backend/diagnostic/traceroute_command_windows.go` and platform command tests through one shared executable-availability contract
- Modify: `SPEC.md`
- Modify: `compose.yaml`
- Modify: `docs/API.md`, `docs/OPERATIONS.md`, `docs/ARCHITECTURE.md`, `README.md`

**RED tests:**
1. In public mode with rate 1/minute, repeatedly call `/livez` and `/readyz` without credentials: always 200 and rate-window count unchanged.
2. Existing `/api/v1/*` still requires Bearer and rate limit.
3. Readiness becomes non-200 when report/checker/body/connection admission is administratively closed or a required local executable is unavailable; temporary `active == capacity` saturation must not flap readiness. It must not call GeoIP or arbitrary targets.
4. Compose public-mode smoke reaches healthy and lets Web start.
5. Shutdown transition, missing executable, repeated readiness, and concurrent snapshots expose only fixed local enums and never paths/errors/IDs.

**Implementation:**
- Route `/livez` and `/readyz` through an outer fixed-route handler before the business mux/auth/rate middleware. `/livez` reports process liveness only.
- Assemble one injected concurrency-safe readiness snapshot in `main` from administrative-open flags for listener/report/body/checker admission and a startup-cached platform executable check owned by diagnostic platform command code. Do not infer unready from temporary saturation.
- `/readyz` emits only a closed enum DTO; never paths, raw errors, targets, request/report IDs, provider prose, or credentials.
- Point Compose healthcheck at readiness. Do not include secrets in health commands.

---

## Task 4: Separate delivery telemetry from diagnostic outcome

**Status:** ✅ Implemented and verified by focused telemetry contract/privacy tests and blocker/major 0 review.

**Objective:** Preserve HTTP/write outcome while emitting bounded, privacy-safe diagnostic aggregates for successful report delivery.

**Files:**
- Modify: `backend/api/telemetry.go`
- Modify: `backend/api/telemetry_test.go`
- Modify: `backend/api/server.go`, `server_test.go`
- Modify: `docs/API.md`, `docs/OPERATIONS.md`, `docs/ARCHITECTURE.md`

**RED tests:**
1. DNS failure report is `unreachable/attention`; the existing `outcome` remains delivery/lifecycle `ok`, while report-finish includes fixed diagnostic status/verdict and total/failed-result/finding counts.
2. Healthy/degraded/unreachable reports map exactly; counts are bounded and relationship-validated before clamping.
3. Dominant finding code/category, if included, comes from fixed allowlists with deterministic priority and no target/provider/error prose.
4. IDs remain log correlation fields only, never metric labels.

**Implementation:**
- Keep the existing `outcome` as the sole delivery/lifecycle outcome. Remove diagnostic overloading from `reportFinishOutcome`: a completely written HTTP 200 report has `outcome=ok`; write, serialization, admission, and cancellation failures retain existing outcomes. Do not add a contradictory second delivery field.
- Add closed `report_status` and `analysis_verdict` enums plus `total_results`, `failed_results`, and bounded `finding_count` only to successfully delivered `report_finish` records.
- Validate `failed_results <= total_results` before clamping, enforce report-status/count relationships, bound finding count to the producer contract, and reject diagnostic fields on all other events.
- Assert report-finish and HTTP-terminal separation for healthy, degraded, unreachable, timeout, panic, capacity, partial-write, and serialization paths. Sink remains best-effort.

---

## Task 5: Inject immutable release identity

**Status:** ✅ Implementation and source-contract verification complete with blocker/major 0 review. Clean committed-tree deterministic image/archive execution remains pending below.

**Objective:** Tie binary health output, startup logs, and OCI metadata to the exact commit/version.

**Files:**
- Create: `VERSION`
- Modify: `Makefile`
- Modify: `Dockerfile`
- Modify: `cmd/checknetwork-api/main.go`, `main_test.go`
- Modify: `compose.yaml` only if build args are needed
- Modify: `scripts/ci-inner.sh`, `scripts/ci-clean-archive.sh`
- Modify: `docs/OPERATIONS.md`, `docs/TESTING.md`, `README.md`
- Modify: `SPEC.md`

**RED tests:**
1. A tracked `VERSION` contains valid SemVer. Build with exact clean 40-hex HEAD revision and that version; health DTO and startup log expose the same immutable values.
2. OCI labels `org.opencontainers.image.revision` and `org.opencontainers.image.version` match.
3. Release/CI build rejects empty, malformed, or mismatched revision; local `go run` may explicitly identify itself as `dev`.
4. Dirty worktrees cannot claim a clean commit revision; malformed VERSION/revision fails before build.
5. Two clean release builds from the same source and deterministic epoch produce identical binary/image digests.
6. The required release verifier fails—not skips—when Docker is unavailable and always cleans its unique image/container/network.

**Implementation:**
- Use `-ldflags -X main.version=... -X main.revision=...` with validated build variables.
- Add OCI labels/build args without embedding secrets or timestamps that break reproducibility.
- Pin build/runtime images by digest and pin traceroute package/repository inputs; derive any required epoch from the commit rather than wall time.
- Add a release target requiring clean HEAD, exact revision, and tracked SemVer VERSION. Make exact-SHA clean archive build a uniquely tagged image, inspect OCI labels, run it in trusted-local mode, and assert startup-log/health identity agreement. Docker unavailability is a hard release-gate failure.

---

## Task 6: Correct Android exported findings and full-topology link validation

**Status:** ✅ Implemented and verified by Android JVM parser/export tests and blocker/major 0 review.

**Objective:** Make human share labels exhaustive and align Android full-topology link schema with the producer.

**Files:**
- Modify: `android/app/src/main/java/com/checknetwork/app/core/ReportMarkdownExporter.java`
- Modify: `android/app/src/test/java/com/checknetwork/app/core/ReportMarkdownExporterTest.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/core/ReportParser.java`
- Modify: `android/app/src/test/java/com/checknetwork/app/core/ReportParserTest.java`
- Modify: `SPEC.md`

**RED tests:**
1. Parse `checker-execution-report.json`; export must contain `Checker execution failed` and `Checker capacity was unavailable`, never `Traceroute execution failed` for those codes.
2. Every `FindingCode` has an explicit label; adding an enum without mapping fails a test.
3. Full topology link validates `latency_delta_ms` as finite nonnegative numeric and rejects string/negative/nonfinite/cap+1; it must not validate a nonexistent `latency_ms` field.

**Implementation:**
- Exhaustively switch over finding codes with a neutral unknown fallback only where parser compatibility requires it.
- Correct the link field name and immutable copy validation.

---

## Task 7: Use one Android session-level absolute deadline

**Status:** ✅ Implemented and verified by Android transport/session deadline tests and blocker/major 0 review.

**Objective:** Include capability discovery and report submission in the same maximum 315-second user operation.

**Files:**
- Create: `android/app/src/main/java/com/checknetwork/app/network/OperationDeadline.java`
- Create: `android/app/src/test/java/com/checknetwork/app/network/OperationDeadlineTest.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/DiagnosticsSession.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/network/CapabilitiesTransport.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/network/ReportTransport.java`
- Modify corresponding tests under `android/app/src/test/java/...`
- Modify: `SPEC.md`

**RED tests:**
1. Shared fake monotonic clock advances 10s during capabilities; report receives `min(request-specific deadline, shared remaining)`, never a new 315s. A maximum traceroute receives at most 305s; a short request-specific deadline remains short.
2. Capabilities consume the entire operation deadline: no report POST/network progress occurs and terminal state is timeout exactly once.
3. Clock regression/wraparound cannot extend the original deadline.
4. Cancel during either transport aborts both ownership paths and stale callbacks cannot publish.

**Implementation:**
- Create one `OperationDeadline` at session start with one injected monotonic clock, checked/saturating arithmetic, and nonincreasing remaining time across clock regression/wrap.
- Capabilities use `min(10s, shared remaining)`. ReportTransport uses `min(deadlineMillis(request), shared remaining)` and recomputes that intersection before connection creation, socket timeout assignment, scheduling, body I/O, and terminal publication.
- Zero remaining opens no connection. Shared expiry/cancel wins exactly once without stale publication.

---

## Task 8: Distinguish valid capability mismatch from malformed response

**Status:** ✅ Implemented and verified by Android capability/transport/session/UI tests and blocker/major 0 review.

**Objective:** Report unsupported local selections as a typed, stable capability mismatch, not server corruption.

**Files:**
- Modify: `android/app/src/main/java/com/checknetwork/app/core/CheckCapabilities.java`
- Modify: `android/app/src/test/java/com/checknetwork/app/core/CheckCapabilitiesTest.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/network/TransportException.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/network/ReportTransport.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/DiagnosticsSession.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/MainActivity.java`
- Modify: `android/app/src/main/res/values/strings.xml`
- Modify corresponding transport/session/activity tests
- Modify: `SPEC.md`

**RED tests:**
1. Valid DNS-only capabilities + selected TCP: no POST; typed `UNSUPPORTED_CAPABILITY` with closed reason `CHECK_KIND`; fixed localized UI copy identifies the category without reflecting server values/prose.
2. Reduced timeout/targets/attempts/topology mode cases receive the same typed family and field-specific fixed reason.
3. Malformed capability JSON remains `INVALID_RESPONSE`; unrelated `IllegalArgumentException` is not mislabeled as capability mismatch.
4. Retry after changing to supported input succeeds under the same ownership rules.

**Implementation:**
- Make `CheckCapabilities.validate` throw a dedicated typed mismatch carrying only `CHECK_KIND`, `TARGET_COUNT`, `TIMEOUT`, `TRACEROUTE_ATTEMPTS`, or `TOPOLOGY_MODE`.
- Add `TransportException.UNSUPPORTED_CAPABILITY` carrying only that enum; update every exhaustive `Kind` switch and test. Catch only the dedicated mismatch in `DiagnosticsSession`; malformed parsing and unrelated programming errors remain distinct.
- Render fixed localized copy per reason. Do not echo arbitrary provider/server values or exception prose.
- Disabling unsupported form choices remains a later product enhancement unless it can be added without expanding this defect slice.

---

## Integration order and ownership

1. Task 1 connection/body admission.
2. Task 2 deadline/data truth.
3. Task 3 liveness/readiness (depends on admission state).
4. Task 4 diagnostic telemetry (depends on corrected report outcomes).
5. Task 5 release identity.
6. Tasks 6 and 7 may run in parallel after backend contracts stabilize.
7. Task 8 follows Task 7 because it shares session terminal ownership.
8. Producer→Web→Android fixtures and docs are refreshed only after all schemas settle.

Each slice receives:
- implementation agent with narrow file ownership;
- focused independent adversarial review;
- blocker/major closure before integration;
- no stage/commit by agents.

## Canonical verification

After the final edit:

```bash
make ci

docker compose config
# collision-free loopback override for build/health/public-mode smoke

git diff --check
```

Required evidence:
- Go tests and race with nonzero machine-count collection;
- Web tests/syntax with current TAP pass count and zero failures;
- Android debug/release XML each nonzero, lint, debug/release assemble/R8;
- 256 incomplete-header connection load stays at configured active ceiling and recovers;
- delayed HTTP body and post-check-deadline cases become timeout, never healthy;
- public readiness repeats without auth/rate consumption;
- diagnostic failure telemetry keeps delivery success separate;
- health/startup/OCI release identity exact agreement;
- Android shared deadline and capability mismatch end-to-end tests;
- generated `.gradle`, `build`, `node_modules`, `bin`, APKs and probe files absent from final manifest.

## Commit and closure

Current closure state (the steps below remain future actions and are not rewritten as completed history): Tasks 1–8 implementation/focused verification and independent blocker/major 0 review are complete. No Stage 7 commit has been created in this worktree.

1. Freeze exact changed-path SHA-256 manifest.
2. Run independent core/security, API/telemetry, Android/Web, and operations/spec reviews against that exact tree.
3. Require blocker 0 and major 0.
4. Stage exactly manifest paths; verify index bytes equal manifest and no unstaged changes.
5. Create one Stage 7 commit.
6. Run `ci-clean-archive` against the exact commit SHA with explicit JDK 17/SDK 35 environment.
7. Perform exact-SHA independent closure review.
8. Fast-forward `main`; do not push unless requested.
9. Re-audit new HEAD and create the next defect/capability plan.

Pending acceptance:

- [ ] one Stage 7 commit and postcommit clean exact-SHA `make ci-clean-archive`
- [ ] clean committed-tree deterministic Docker release execution and exact health/startup/OCI identity check
- [ ] physical Android/emulator, TalkBack/Switch Access, OEM share/cache/network behavior
- [ ] real Chrome/Firefox/Safari viewport/screen-reader and TLS reverse-proxy HTTP/2 deadline behavior

## Risks and tradeoffs

- Listener rejection by immediate close provides bounded resources but no HTTP status for clients whose headers were never admitted; document this truthfully.
- Readiness must avoid expensive/network work and must not become an information leak.
- A single Android operation deadline requires careful stale-owner fencing across two transports.
- Diagnostic telemetry must not overload the existing transport `outcome` meaning; additive fields are preferred.
- Version injection must remain reproducible and must not embed build-time secrets or timestamps.
- Physical Android, real screen readers/browsers, and HTTP/2 gateway behavior remain explicit release-validation gaps, not automated-completion claims.
