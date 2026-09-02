# Stage 6 — Enrichment, Execution Lifecycle, Observability, and Operations

Base: `f38398404a98ff7d9d68936f534cbdd109811b7f`

## Goal

Close reproduced long-running service defects without weakening public-network policy or client resource contracts. GeoIP work must be globally bounded and coalesced; the runner must return at its declared budget even when a checker panics or ignores cancellation; report execution capacity must not be held by response delivery; every terminal HTTP/report outcome must be privacy-safe and correlated; deployment and CI time/resource contracts must match executable behavior.

## Non-goals

- No incident-history persistence, shared links, multi-vantage agents, packet-loss collector, or Android interactive map.
- No provider URL, raw target/address, Authorization value, query, upstream error prose, or client IP in telemetry/provenance.
- No claim of physical Android/browser/device validation.
- Do not extend the existing strict `analysis.evidence[].provenance` enum; enrichment provenance is additive coverage/detail data.

## Reproduced runtime defects

1. Same-key GeoIP misses issue duplicate upstream calls; a late old response can overwrite an earlier new response for 24 hours.
2. GeoIP concurrency is per traceroute result, not process-wide: default theoretical peak is `4 reports × 20 targets × 6 = 480` provider calls; configured report concurrency has no hard maximum.
3. GeoIP accepts max+1 bodies, a valid JSON prefix plus trailing data, and multiple JSON values.
4. Provider strings and repeated full-topology metadata produced a deterministic 51,903,589-byte full payload; only compact mode has an HTTP response cap.
5. `Runner.Run` waits forever for a checker that ignores context, despite the declared request budget.
6. A checker panic occurs in a child goroutine and bypasses `net/http` panic recovery, terminating the process.
7. Report admission remains occupied through response writing, so a blocked client can deny execution capacity after computation is done.
8. Response write errors/partial writes are discarded and logged like success.
9. Compose kills the container after its default 10 seconds while the application promises a multi-minute drain.
10. Write/shutdown timeout composition omits up to 35 seconds of request-body reading.
11. `make ci` is not clean-checkout self-contained and omits race, vet, build, and Web syntax gates.
12. A context-ignoring injected GeoIP transport can return success after its caller is cancelled and publish that abandoned value to cache (`first_err=nil`, `cached_city=late`).

## Capability gaps

- No bounded active/queued GeoIP lifecycle, typed outcome, cache/upstream freshness aggregate, or provider metrics.
- No request/report correlation or submit/admit/reject/start/finish/cancel lifecycle telemetry.
- No status, stable outcome, active capacity, request/response bytes, runner/marshal/write duration, or write-failure telemetry.
- No explicit provider/checker/header/report-concurrency/load envelope in deployment docs.

## Fixed contracts

### Runner

- `main` constructs one service-owned checker supervisor; do not hide an independent 80-slot pool in every `Runner`. The supervisor has a fixed capacity (default 80, configurable only within a hard maximum), a shared cancellation context, and observable active/capacity/stuck counts.
- A worker slot is released only when the actual checker returns or panics; a context-ignoring checker remains accounted even after its request returns.
- Acquire a slot before creating a goroutine. Each worker sends exactly one indexed immutable result through a `len(targets)` buffered channel; only the collector mutates the report result slice. `Run` returns no later than `RequestBudget(req)` plus scheduler tolerance. Unfinished targets become deterministic `cancelled` when the parent context won, otherwise `timeout`; late sends cannot mutate the returned report and synthetic completion never releases a worker slot.
- Recover each checker panic at the goroutine boundary. Return a generic stable `checker_panic` result/coverage limitation; never expose panic text, target, or stack in API data. A bounded privacy-safe event hook may record kind and correlation ID only.
- Saturated checker admission returns a deterministic bounded result; no new goroutine is started without a slot.
- Shutdown stops new admission, cancels the supervisor context, waits only for a bounded drain, records the remaining worker count, and never claims to forcibly terminate a context-ignoring goroutine.

### GeoIP

- Coalesce each key into one in-flight generation identified by pointer identity and a monotonic generation ID. Each entry owns a background-derived shared context/cancel, `done`, immutable terminal result, waiter count, and terminal/abandoned flags. Cache lookup, join/create, and waiter acquisition occur under one mutex; the first caller context is never the shared context parent.
- Every caller releases exactly one waiter lease. Completion versus caller cancellation has one lock-arbitrated winner. When the last waiter leaves, mark the entry abandoned and invoke shared cancel exactly once under the same state transition. New callers never join an abandoned generation and may create its successor.
- A generation may remove itself only when the map still points to that exact entry. It may publish cache only when terminal success, not abandoned, still-current generation, valid data, and shared context not cancelled. An old provider that ignores cancellation cannot delete or overwrite a successor. Queue and active leases are separate: the queue lease is released when execution starts or pre-start cancellation wins; the active lease is released only after the actual provider call returns or panics. Timeout/abandonment publishes a terminal result to waiters and cancels shared context but cannot release an active lease while a context-ignoring call is still running. Thus execution count remains bounded even though Go cannot forcibly terminate a noncooperative injected transport.
- Enforce process-wide active and active+queued limits. Duplicate waiters do not consume queue entries. Queue saturation fails fast with a typed retryable outcome; waiting for active capacity respects cancellation.
- Enforce an independent provider timeout for owned HTTP transports and require production transports to honor context/I/O deadlines. An arbitrary injected test transport may ignore both and live indefinitely; its lifetime is not falsely claimed as bounded, but it continues to consume one hard active lease until it really returns.
- Do not cache transient failures, cancellations, malformed data, or results abandoned by all waiters.
- Read at most `max+1`; accept exactly one JSON object followed only by EOF/whitespace. Enforce bounded strings, finite/ranged coordinates, valid country code, and a bounded total Geo/ASN bundle before cache publication.
- Clone cached pointer-bearing metadata on store/return so callers cannot mutate shared cache entries.
- Preserve successful TTL/LRU semantics and public-mode redirect/dial policy.
- Return internal typed outcome/source/fetched/expiry metadata. Traceroute emits only bounded aggregates in result details and `analysis.coverage.enrichment[]`. Exact additive schema: at most one record per bounded provider ID; `{provider:"geoip", source:"upstream|cache|mixed|none", cache_hits, upstream_fetches, max_age_ms, failures:[{kind:"not_found|rate_limited|timeout|policy|malformed|unavailable|cancelled|busy", count, retryable}]}`. Counts are bounded by valid topology observations, strings/enums are fixed, `max_age_ms` is bounded by cache TTL, and failures have a fixed enum/cardinality. It emits no provider URL, IP, target, timestamp finer than age, provider prose, or credentials. Existing evidence provenance enums remain unchanged.

### HTTP/report transport

- Decode the request with exact byte accounting. A `MaxBytesError` returns HTTP 413 plus stable `request_too_large`; malformed/surplus JSON remains 400.
- Generate a server-owned opaque request ID before auth and an opaque report ID before execution. Add a shared `RunWithID` contract so pre-execution lifecycle events and final `Report.ID` use the same report ID; IDs are never derived from client data and are never metric labels.
- Convert `Report` into a closed transport DTO before full serialization. `Result.Details` accepts only explicitly enumerated JSON-safe primitives and production types (`nil`, bool, bounded signed/unsigned integers, finite float, bounded string, `time.Time`, string slices, topology/trace/enrichment DTOs, and recursively bounded maps/slices produced by those adapters). Reject cycles, excess depth/nodes, arbitrary pointers, unsupported dynamic types, and any custom `json.Marshaler`. Then encode the closed DTO with a custom incremental JSON writer that appends only bounded tokens/leaves to a stop-on-limit buffer; do not delegate a giant value to `json.Marshal`/`Encoder` and check afterward. String encoding uses the same escaping rules as `encoding/json`, including keys, punctuation, HTML escaping, invalid UTF-8 replacement, `omitempty`, and final newline. Property tests require the calculated/written length never to undercount canonical `encoding/json` for admitted random DTOs.
- Full JSON response, including newline, is `<= 8*1024*1024` bytes. Compact remains `< 1 MiB`. Admission failure stops before large serialization and generates a small fixed 500 `full_response_too_large` response.
- Produce one admitted bounded response payload, then acquire an execution-independent large-write token before returning execution admission. Large-write active capacity is hard-bounded; if unavailable, discard the payload, release execution capacity, set a short write deadline, and return a fixed `write_capacity_unavailable` response rather than retaining or queueing 8 MiB bodies. Every large-write token is released exactly once.
- The response observer records first/implicit status, attempted bytes, actual bytes, and zero/partial errors. It provides `Unwrap()` and exposes `Flusher`, `Hijacker`, `Pusher`, legacy `CloseNotifier`, and `io.ReaderFrom` if and only if the underlying writer supports each interface; capability combinations and `ResponseController` forwarding are tested. Never falsely advertise an optional interface.
- Emit fixed-schema JSON lifecycle events through a typed positive-allowlist record, never `map[string]any` or raw `error`: only event, request_id, report_id, mux route pattern (or fixed `unmatched`), method, status, bounded outcome enum, counters/capacity/bytes, and durations. Events: HTTP terminal for every request plus report submit/admit/reject/start/computed/finish/cancel.
- Telemetry uses route templates, not arbitrary URL paths. It excludes raw query, Authorization, request body, targets/addresses, client IP, provider URL/prose, response body, panic text and stacks.
- 401, 413, 422, 429, 503, serialization/size failures, cancellation, zero/partial write failures and success have deterministic outcomes and tests.
- Partial/zero write changes telemetry only to `write_failed_partial|write_failed_zero`; it never attempts a second error body or rewrites an already committed HTTP status, and raw network error text is not logged.
- Error compatibility table is fixed in API docs: 413 `request_too_large`; 500 `full_response_too_large`; 503 `write_capacity_unavailable`; existing codes unchanged. Web and Android safe error allowlists are updated, or an explicit tested generic fallback is retained for a code that must not expose server prose.

### Runtime/deployment/CI

- Use one explicit HTTP/1 budget model rather than mixing deadline strategies: header `H=5s`; body allowance `B=30s`; execution `E=301s`; admitted marshal/write grace `R=5s`; shutdown margin `D=5s`. Configure `ReadHeaderTimeout=H`, accept-based `ReadTimeout=H+B=35s`, post-header `WriteTimeout>=B+E+R=336s`, and `ShutdownTimeout>=H+B+E+R+D=346s`. HTTP/2 behavior is separately probed/documented because `WriteTimeout` semantics differ. Normal Web/Android clients send bounded bodies promptly and use a 315-second report deadline measured from client request start: `E+R=306s` plus 9 seconds reserved for DNS/connect/TLS/upload/download/scheduler variance. Hostile slow-body allowance is not added to the normal client contract. Exact 314999/315000 boundary and server-near-budget completion tests are added in both clients.
- Configure `MaxHeaderBytes` to a documented fixed ceiling and hard-cap `CHECKNETWORK_MAX_CONCURRENT_REPORTS`.
- Compose uses the same documented constants and `stop_grace_period >= ShutdownTimeout + signal/exit margin`; with the fixed values it is 6 minutes. API and Web have healthchecks and bounded PID/CPU/memory settings. `depends_on` waits for API health. The static Web container uses a fixed small nginx worker count.
- Define `ci-inner` as the non-recursive fresh gate: lockfile `npm ci`, `go test -count=1 ./...`, Web tests/syntax, `go test -count=1 -race ./...`, vet, build, and Android wrapper/env/test/lint/assemble. `make ci` invokes `ci-inner` directly. A distinct `ci-clean-archive`/external acceptance script creates an artifact-free archive and invokes `ci-inner` exactly once—never recursively. It verifies actual Go, Node TAP and Android XML nonzero counts rather than trusting marker text; archive-local generated output is allowed and original-worktree artifacts are checked separately.
- Operations docs state active report/checker/provider/queue/PID/FD/time/body/response bounds, overload outcomes, privacy fields, graceful drain, and device/browser limitations.

## Strict TDD vertical slices

1. Runner panic boundary: subprocess RED proving process death → generic per-target result and process survival GREEN.
2. Runner deadline/noncooperative accounting: barrier RED → budget return, immutable late result, retained worker slot, saturation, later recovery GREEN.
3. GeoIP same-key barrier RED → one upstream call, no stale overwrite, independent waiter cancellation GREEN. Include cancel-vs-complete in both orders, partial waiter cancellation, last waiter cancel while queued, provider panic, and last waiter cancel → provider ignores cancel → successor succeeds → old generation completes without deleting/publishing.
4. GeoIP active/queue barrier RED → global peak and queued work stay at limits; saturation/cancellation/failure releases capacity GREEN.
5. GeoIP strict body/schema RED matrix → max boundary, max+1, second object, trailing byte, strings/coordinates/bundle GREEN.
6. GeoIP provenance RED → fresh/cache/expiry and typed failures become privacy-safe bounded aggregate details/coverage GREEN.
7. Full-response amplification RED → closed transport DTO conversion, unsupported/cyclic/custom-marshaler rejection, incremental escaped JSON writing, bounded provider fields, 51 MiB fixture stopped at the buffer ceiling, and exact `<=8 MiB` full marshal/error contract GREEN.
8. Admission/write barrier RED → computed payload releases report slot before blocked writer GREEN.
9. Write-fault RED → exact implicit/explicit status, attempted/actual bytes, optional-interface parity, `write_failed_zero|partial` telemetry and no false success GREEN.
10. Lifecycle logging RED matrix → identical opaque IDs and all terminal outcomes with canary non-disclosure GREEN.
11. Runtime timeout/config RED → composed deadlines, header/concurrency hard caps GREEN.
12. Compose stop/health/resource and nginx worker contract RED → deployment config GREEN.
13. Clean archive `ci-inner` RED → lockfile bootstrap and mechanically verified Go/Node/Android counts GREEN; outer archive target runs it once without recursion.
14. Deterministic load probe records peak report/checker/provider work, allocations and p95 completion under a reduced-time fixture; hard gates are resource counts/bytes, not unstable wall-clock latency.

## Work ownership

- Runner owner: `backend/diagnostic/runner.go`, runner/model/analysis tests required for panic/deadline/capacity.
- GeoIP owner: `backend/diagnostic/geoip.go`, `traceroute.go`, `analysis.go`, related tests and additive model fields.
- API owner: `backend/api/server.go`, server tests, bounded response/logging helpers.
- Consumer-contract owner: Web state/UI tests and Android core/presentation tests for the exact optional `coverage.enrichment` schema and safe new error outcomes; legacy fixtures must continue to parse.
- Operations owner (after code contracts settle): `cmd/checknetwork-api`, `Makefile`, Compose/Docker/nginx config, docs and clean-archive tests.
- Reviewers are read-only and inspect exact manifests/SHA. Implementers do not stage, commit, reset, or edit another owner's files.

## Gates

1. Focused RED/GREEN tests with deterministic barriers, subprocess panic probes and write-fault writers.
2. `go test -count=1 ./...`
3. `go test -count=1 -race ./...`
4. `go vet ./...`
5. `make build`
6. Web 128+ tests and syntax.
7. Android debug/release 134+ tests, lint and assemble.
8. Clean archive non-recursive `ci-inner` with no pre-existing `node_modules`, `.git`, or build outputs, and machine-checked nonzero collections.
9. Docker Compose config/health/stop-contract tests and reduced-time graceful drain probe.
10. `git diff --check`, generated-artifact cleanup, exact changed-path/hash manifest.
11. Independent runner/GeoIP, API/security/telemetry, and deployment/load reviews must each report blocker/major 0.
12. One Stage 6 commit, then exact-SHA clean detached-worktree gates and review before main fast-forward.

## Operations completion matrix (working-tree closure)

| Contract | Implemented/tested source of truth | Documentation | Closure state |
|---|---|---|---|
| report active default 4 / hard max 16 | `api.MaxConcurrentReportsLimit`; runtime 16 accept/17 reject | README/API/ARCHITECTURE/OPERATIONS/TESTING | complete |
| checker active default 80 / hard max 1,024 | `diagnostic.DefaultCheckerCapacity` / `MaxCheckerCapacity` | ARCHITECTURE/OPERATIONS/TESTING | complete |
| GeoIP active 8 / queued 64 / cache 2,048 | GeoIP config defaults and lifecycle tests | README/API/ARCHITECTURE/OPERATIONS | complete |
| response write hard max 16 | API admission/write tests | README/ARCHITECTURE/OPERATIONS | complete |
| header 64 KiB / request 1 MiB | runtime/API boundary tests | README/API/OPERATIONS/TESTING | complete |
| full `<=8 MiB` / compact `<1 MiB` | exact response boundary tests | README/API/ARCHITECTURE/OPERATIONS | complete |
| H/B/E/R/D = 5/30/301/5/5s, HTTP/1 35/336 equations, and one shared end-to-end 346s shutdown deadline | `TestRuntimeBudgetEquationsAndHTTPServerContract`, `TestShutdownServiceUsesOneEndToEndDeadline` | ARCHITECTURE/OPERATIONS/TESTING | complete |
| normal Web/Android client deadline 315s | Web/Android boundary contract tests | README/ARCHITECTURE/OPERATIONS | complete; physical clients pending |
| Compose stop 6m + CPU/memory/PID + health | Compose/runtime contract tests | README/OPERATIONS/TESTING | source complete; daemon smoke recorded separately |
| telemetry events/exact fields/outcomes | typed positive allowlist and lifecycle/write-fault tests | API/ARCHITECTURE/OPERATIONS | complete |
| telemetry privacy exclusions | canary tests and no extension/error fields | plan/ARCHITECTURE/OPERATIONS | complete |
| enrichment exact schema/privacy | producer/analysis/consumer fixtures | API/ARCHITECTURE/OPERATIONS | complete |
| overload outcomes | API/checker/Geo/write tests | API/OPERATIONS | complete |
| noncooperative worker limitation | supervisor/Geo lifecycle tests | ARCHITECTURE/OPERATIONS | complete |
| nginx workers 2 / non-root API | Docker source contracts | README/OPERATIONS/TESTING | source complete; image smoke recorded separately |
| nonrecursive clean CI, exact-SHA clean-only, machine counts | operations contract + `sh -n` | OPERATIONS/TESTING | precommit source complete; clean archive is postcommit-only |
| actual Android/device/browser/HTTP/2 behavior | not established by JVM/jsdom/HTTP/1 tests | README/ARCHITECTURE/OPERATIONS/TESTING | explicitly pending |

This matrix does not mark load/performance probe, physical-device/browser acceptance, hosted CI, independent final reviews, commit, or postcommit exact-SHA archive as complete. Verification evidence must be appended after final edits; `ci-clean-archive` must not run on this uncommitted tree.

## Explicit deferred items

- Rate-limit window cleanup remains bounded at 4096 but O(n) under one mutex; optimize only if a benchmark demonstrates operational contention after higher-priority lifecycle fixes.
- Dependency locking/verification metadata, hosted CI workflow, signed release artifacts and physical-device/browser acceptance remain separate follow-ups unless required to make the clean archive gate deterministic.
