# Stage 8 Truthful Diagnostics and Client Reliability Implementation Plan

> **For Hermes:** Use task-scoped subagents in the dedicated Stage 8 worktree. Enforce strict RED→GREEN vertical slices, independent review after each task, one verified Stage 8 commit, exact-manifest staging, and exact-SHA clean-archive verification.

**Goal:** Close the post-Stage-7 defects that can still report false operational success, overstate what a diagnostic observed, discard actionable evidence, block Android’s UI thread, retain sensitive share artifacts, or let API/client/release contracts drift.

**Architecture:** Keep source observations distinct from interpretations and presentation. Introduce explicit typed contracts at the producer boundary (request validation, API errors, service greeting scope, TLS verification causes, mixed traceroute facts), consume those contracts strictly in Web and Android, and publish UI/share states only after the corresponding materialization succeeds. Extend exact-SHA release verification to double-built, tagless, offline-validated API and Web archives without daemon-loading either derived archive. Do not claim protocol depth, browser/device coverage, derived-archive execution, or writer cancellation that is not actually exercised.

**Tech Stack:** Go 1.22, `net/http`, Java 17/Android API 26+, Node ESM/jsdom, browser DOM APIs, Docker BuildKit/Compose v2, nginx, POSIX shell, Gradle 8.11.1.

---

## Baseline and audit classification

- Base commit: `36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243` (`feat: enforce runtime and diagnostic truth`).
- Worktree: `/home/piecer/dev/worktrees/checknetwork-stage8`, detached and clean at the base commit.
- Stage 7 exact-SHA closure is historical context only; transient artifact hashes are intentionally not carried into the Stage 8 status.
- P0/P1 findings: none.

### Reproduced runtime/product defects in Stage 8

1. Shutdown returns success and logs completion when a non-cooperative checker remains active/stuck.
2. A real expired HTTPS certificate becomes generic `tls_handshake_failed`, so the live checker cannot produce its existing expiry finding.
3. When completed reached and completed unreached traceroute attempts coexist with timeout/command-error attempts, the execution branch suppresses the independently supported partial-reachability finding. A reached+timeout result with no completed-unreached attempt is correctly timeout-only.
4. Stable API error codes are collapsed differently by Web and Android, creating false retryability and misleading generic classifications. Fixed privacy-safe prose is intentional and remains required.
5. Backend accepts invalid HTTP `expected_status` and irrelevant non-HTTP `expected_status`.
6. Web accepts full-topology link values rejected by the producer/Android and silently treats obsolete `latency_ms` as zero delta.
7. Web reports credential configuration success when storage failed; blank Apply silently deletes an existing credential while reporting it was set.
8. Android publishes `READY` before report view materialization; a render exception can leave “Report ready” with no usable report.
9. Android raw-share encode/write/flush/fsync runs synchronously on the main thread for payloads up to 8 MiB.
10. Android raw-share cleanup tracks only the current in-memory manager, leaving recognized prior-process sensitive cache artifacts without startup reconciliation or bounded retention.
11. Android accepts analysis evidence/coverage whose `kind` contradicts the referenced result.
12. Android compact Geo byte accounting does not match Go’s HTML-escaped `encoding/json` bytes at the 4,096-byte boundary.
13. Android remove-button accessibility names become stale after target-address edits.
14. Exact-SHA release verification did not cover both API and Web as bounded, reproducible offline archives; `frontend/Dockerfile` used a mutable nginx reference and had no revision/version identity.

### Explicit capability gaps, not defect claims in this stage

- HTTP `expected_status` remains a final-response expectation. A bounded redirect-chain model (initial/final status, scheme, destination) is future diagnostic evidence unless the contract is deliberately revised.
- Current named service checks contractually prove only TCP connection, or TCP plus verified implicit TLS. Arbitrary bytes/immediate close are therefore reproduced capability limits rather than current contract violations. Stage 8 deliberately advances this to bounded server-first greeting verification; SMTP command transaction, STARTTLS upgrade, authentication, mailbox access, and application-level end-to-end semantics remain future capabilities.
- Human exports intentionally omit or reduce evidence/actions for privacy, so broader safe substantiation is a capability advancement rather than a violation of the current privacy contract. Stage 8 adds allowlisted evidence visibility without claiming counterfactual inference; supporting versus refuting evidence remains not yet a first-class API schema relationship.
- Compact-v1 currently has strict value/count/reference checks but no explicit unknown-field policy. Stage 8 closes v1 objects consistently across Web/Android; future additive fields require an identified extension point or schema bump.
- Web live `/api/v1/checks` capability-driven forms; Android interactive topology/Geo; client-local Wi-Fi/VPN/proxy/route/resolver facts; history/baselines; multi-vantage checks; packet loss/jitter/throughput/MTU; metrics; hosted CI/SBOM/signing remain future stages.
- Physical Android/OEM/TalkBack/Switch Access, real Chrome/Firefox/Safari screen-reader and viewport, and HTTP/2 reverse-proxy probes remain unverified until actually run.

## Global invariants

- Do not weaken public-mode authentication, rate limiting, resolved-IP policy, redirect revalidation, DNS-rebinding defense, or proxy bypass.
- Never put credentials, raw targets, IPs, URLs, provider/error/panic prose, raw reports, or opaque request/report IDs into metric labels or new telemetry fields.
- Preserve request ≤1 MiB, full response including newline ≤8 MiB, compact response <1 MiB, Web/Android parser limits, report/checker/write/connection admission, and shared Android 315,000 ms operation deadline.
- A timeout/cancellation cannot fabricate termination of a checker, network call, filesystem write, or executor task. Ownership remains until actual return.
- Server-controlled prose is not trusted presentation text. DOM uses safe text APIs; human exports use fixed allowlisted templates and bounded typed values.
- Every production behavior change starts with a focused failing test that demonstrates the exact defect. Record the RED output before writing production code.
- Implement one vertical slice at a time. No task agent stages, commits, resets, switches branches, edits main, or modifies files outside its ownership.
- Generated `frontend/node_modules`, Android `.gradle`/`build`, APKs, binaries, temporary probes, Docker containers/images/networks, and release outputs are excluded from the candidate manifest. The ignored repository-root `checknetwork-api` binary is currently retained rather than removed because this run is explicitly non-destructive; retention is not verification evidence or artifact-cleanup completion.
- One Stage 8 commit only, after all tasks/docs, fresh canonical gates, exact-tree review, and explicit manifest staging.

---

## Task 1: Make residual checker work a shutdown failure

**Objective:** Return nonzero and log a fixed failure when the shared drain deadline expires with active/stuck checker work, while preserving the single end-to-end shutdown budget.

**Files:**
- Modify: `cmd/checknetwork-api/main.go:106-165`
- Modify: `cmd/checknetwork-api/main_test.go`
- Modify: `cmd/checknetwork-api/operational_test.go`

**RED:**
1. Admit a barrier-blocked checker whose HTTP/report owner has already completed.
2. Use a successful HTTP shutdown and a 5 ms shared drain deadline.
3. Assert current behavior returns nil and emits INFO despite `remaining=active=stuck=1`.
4. Add the dual-failure case where HTTP shutdown also fails.

**GREEN contract:**
- Add a fixed sentinel such as `errCheckerDrainIncomplete`; do not include checker/target prose.
- `remaining != 0`, `snapshot.Active != 0`, or `snapshot.Stuck != 0` after shutdown is failure.
- Join HTTP and checker-drain failures without extending the deadline.
- Emit one ERROR terminal shutdown record with bounded counts and fixed reason; never emit “completed” INFO for an incomplete drain.
- `runMain` returns exit code 1. Releasing the barrier later must converge accounting to zero without changing the already truthful process result.

**Verification:**
- Focused `go test -count=100 ./cmd/checknetwork-api -run 'Test.*Shutdown.*(Checker|Incomplete|Stuck)'`
- Race `go test -race -count=20 ./cmd/checknetwork-api -run 'Test.*Shutdown'`

---

## Task 2: Centralize request and API-error contracts

**Objective:** Reject semantically invalid `expected_status` before admission and establish one producer-owned status/code/retryability contract consumed exactly by Web and Android.

**Files:**
- Modify: `backend/diagnostic/runner.go`, `runner_test.go`
- Modify: `backend/api/server.go`, `server_test.go`
- Create or modify: `backend/api/error_contract.go`, `error_contract_test.go`
- Create: `testdata/api-error-contract.json`
- Modify: producer fixture freshness tests/scripts as required

**RED:**
1. `expected_status=99` and `600` on HTTP/HTTPS currently validate.
2. `expected_status=200` on DNS/TCP/traceroute/service kinds currently validates.
3. Enumerate every reachable server error response and prove no single typed table currently defines status, stable code, default retryability, `Retry-After` applicability, and fixed public message.
4. Poison validation, panic, marshal, policy, unmatched-route, and internal-failure paths with target/IP/URL/token/provider/error canaries; prove a free-form writer/message argument can currently reflect prose.
5. Preserve the final-response redirect controls: `302 → 204` with expected 204 is healthy and records `status_code=204`; the same chain with expected 302 is `http_unexpected_status`; a multi-hop HTTPS chain applies scheme and resolved-IP revalidation at every hop while still comparing only the final response.

**GREEN contract:**
- Omitted/zero expected status is valid for all kinds and defaults to 200 only inside HTTP/HTTPS execution.
- Explicit 100..599 is valid only for HTTP/HTTPS. All other values/combinations return 422 `invalid_request` before checker/report admission.
- Define a closed typed registry for every emitted API error. Each registry row owns HTTP status, stable code, default retryability, `Retry-After` applicability, and one fixed privacy-safe public message.
- The response writer accepts a registry key plus only typed bounded metadata needed by that row; it cannot accept a caller-supplied code, message, or error prose. Route validation, panic recovery, marshal/write-capacity/policy failures, and unmatched routes through it. Add an AST/static test that rejects direct public error-DTO construction and direct response-writer calls with prose outside the registry implementation.
- Generate exact-byte, deterministically ordered `testdata/api-error-contract.json` from the producer table, including status, code, retryable, `retry_after` applicability, and the fixed public message. Producer tests prove every registered error is valid/reachable, every emitted error uses a registry key, and every canary is absent from every response body.
- The producer emits `Retry-After` only for rows marked applicable, and only as canonical ASCII decimal delta-seconds in **1..3,600**, with no sign, leading zero, whitespace, date form, comma, or duplicate value. Clamp/reject internal retry durations outside that public bound before header construction. The fixture includes absent, `1`, `3600`, `0`, `3601`, leading-zero, signed, whitespace, HTTP-date, overflow, and comma/duplicate cases. Consumers keep an otherwise valid typed error when the header is absent/malformed/disallowed but expose no retry timestamp; a valid value means exactly `now + seconds` using each test's injected clock.
- Preserve the versioned HTTP rule: `expected_status` compares only the final HTTP response status. Do not add initial-status/redirect-chain fields or change matching semantics without a separately versioned contract. Analysis and both clients label this evidence as “final HTTP response status.”

**Verification:**
- `go test -count=100 ./backend/diagnostic ./backend/api -run 'Test.*(ExpectedStatus|ErrorContract|RedirectFinalStatus|PublicMessage)'`
- `go test -race ./backend/api ./backend/diagnostic`
- Generate the fixture repeatedly and compare exact SHA-256; run static/direct-construction and privacy-canary gates.

---

## Task 3: Advance connection-only service checks to bounded greeting verification

**Objective:** Add one exact, read-only server-first greeting grammar so named SSH/mail checks establish only the bounded fact they actually observe.

**Files:**
- Modify: `backend/diagnostic/service.go`
- Modify/create: `backend/diagnostic/service_test.go`, `checkers_test.go`
- Modify: `backend/diagnostic/model.go`, `analysis.go`, `analysis_test.go`
- Modify: relevant producer fixtures

**RED and protocol grammar:**
- Lock the old capability first: connection, arbitrary bytes, and immediate close are currently healthy only because no greeting is parsed.
- The reader accepts at most **4,096 aggregate bytes**, **8 CRLF-terminated lines**, and **512 bytes per line including CRLF**. A bare LF, missing CRLF, ninth line, 513-byte line, or aggregate byte 4,097 is malformed/oversize; no parser reads past a cap.
- SSH: permit 0..7 CRLF pre-identification lines within the common caps, then require an identification line no longer than 255 bytes including CRLF whose bytes begin exactly `SSH-2.0-`. Eight prelines, `SSH-1.5-`, or EOF before the identification line fails.
- SMTP/submission/SMTPS: require one `220 ` line, or one or more `220-` continuation lines followed within the caps by a final `220 ` line; every line in the multiline greeting must carry code 220.
- IMAP/IMAPS: accept only a first line beginning exactly `* OK` or `* PREAUTH` followed by space or CRLF; explicitly reject `* BYE` and every other status.
- POP3/POP3S: accept only a first line beginning exactly `+OK` followed by space or CRLF.
- Include exact cap/cap+1, valid SSH seven-preline, SMTP multiline, IMAP PREAUTH/BYE, immediate EOF, delayed, timeout, cancel, malformed, and wrong-family controls.

**Exact outcome contract:**
- After vetted TCP or implicit TLS establishment, perform **zero client writes** and parse only the bounded greeting. A grammar success returns `status=healthy`, no error code/finding, and the closed detail `verification_scope=server_greeting`; TLS variants retain only their separately allowlisted TLS metadata.
- EOF before a valid terminal greeting, malformed framing/grammar, wrong protocol, read error, per-line/line-count/aggregate oversize all return `status=degraded`, result `error_code=service_greeting_unverified`, and exactly one analysis finding `service_greeting_unverified`; preserve the independent transport-established fact but never raw greeting bytes/prose.
- Context deadline preserves the existing `error_code=timeout` and timeout finding; cancellation preserves `error_code=cancelled` and cancellation finding, even if EOF/parser/TLS errors race afterward. Neither is remapped to greeting-unverified.
- Apply the original context deadline to dial, handshake, and read; cancellation closes/unblocks exactly once without an unowned reader, and ownership remains until the operation actually returns.
- Preserve the security path exactly: mixed public/private answers in either order and blocked-only answers produce zero dials/reads; an allowed answer produces exactly one policy-vetted-IP dial, no second resolution/dial, and TLS SNI remains the original hostname.
- Producer coverage fixes the limitation: only the expected server-first greeting was observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.
- All Web/Android screen and Markdown service-success wording must be scoped exactly to that limitation; no generic PASS/Healthy/“response is normal” copy may imply full service health.

**Verification:**
- Table-driven local protocol servers and exact boundary matrix, count 100.
- Race/non-cooperative read tests; deadline/cancel closes once and returns within the original deadline; normal return unregisters cancellation callbacks.
- SSRF/mixed-answer/dial-count/original-SNI/zero-write invariance tests and serialized-report raw-greeting privacy canary.
- Task 6 freezes and synchronizes the new result/finding/scope contract before any presentation task.

---

## Task 4: Preserve exact typed TLS verification failures

**Objective:** Classify failed HTTPS/implicit-TLS verification with one closed result/finding matrix and no verifier-prose channel.

**Files:**
- Modify: `backend/diagnostic/http.go`, `http_test.go`
- Modify: `backend/diagnostic/service.go`, service tests through one shared TLS helper
- Modify: `backend/diagnostic/analysis.go`, `analysis_test.go`
- Modify: `backend/diagnostic/model.go` (`validErrorCode`, normalization, result/finding enums)
- Modify: producer result/finding fixtures; Task 6 then synchronizes both clients

**RED:**
- Trusted expired and not-yet-valid local certificates currently collapse to generic `tls_handshake_failed`.
- Add hostname mismatch, unknown authority, other typed verification failure, malformed TLS record/protocol/cipher failure, hostile subject/SAN/issuer/serial/wrapped-error prose, timeout, cancellation, and exact-time-boundary controls for HTTPS and every implicit-TLS service path.

**Exact GREEN matrix:**

| Typed cause (certificate attached to that verification error only) | Status | Result `error_code` | Analysis finding | Permitted failed-result details |
|---|---|---|---|---|
| verification-time `now >= NotAfter` | `unreachable` | `tls_certificate_expired` | `tls_certificate_expired` | exactly `certificate_not_before`, `certificate_not_after` as UTC RFC3339 timestamps |
| verification-time `now < NotBefore` | `unreachable` | `tls_certificate_not_yet_valid` | `tls_certificate_not_yet_valid` | exactly `certificate_not_before`, `certificate_not_after` as UTC RFC3339 timestamps |
| typed hostname mismatch | `unreachable` | `tls_hostname_mismatch` | `tls_hostname_mismatch` | none |
| typed unknown authority/trust root failure | `unreachable` | `tls_untrusted` | `tls_untrusted` | none |
| every other typed verification cause, TLS alert, malformed record, protocol/cipher/handshake failure, or safe fallback | `unreachable` | `tls_handshake_failed` | `tls_handshake_failed` | none |

- Use `tls.CertificateVerificationError` and typed `x509` causes only—never string matching. Distinguish expired from not-yet-valid by comparing an injected checker clock/verification time with `NotBefore`/`NotAfter` on the certificate carried by that typed failure; never inspect an unrelated peer certificate.
- Classification is total and fixed: no optional alternative or raw error fallback. Failed verification emits no subject, SAN, issuer, serial, chain, endpoint, or wrapped-error prose. The two validity rows emit only the two tabled details.
- Context cancellation and original deadline take priority over every late verification/protocol result; keep ownership until handshake return and use the same injected clock in execution and analysis fixtures.
- Preserve successful-handshake metadata under its existing bounded contract; do not conflate successful near-expiry analysis with failed-handshake validity rows.

**Verification:**
- Deterministic local CA/cert fixtures and injected/frozen checker and analysis clocks.
- `go test -count=100 ./backend/diagnostic -run 'Test.*TLS.*(Expired|NotYetValid|Hostname|Untrusted|Handshake|Deadline|Cancel)'`
- Race/deadline-boundary tests plus serialized hostile-certificate privacy canary.
- Task 6 exact producer finding fixture and Web/Android closed enums must include every new result/finding code.

---

## Task 5: Emit independent traceroute execution and reachability findings

**Objective:** Preserve partial/unreachable/path facts when some attempts also timeout, cancel, or fail execution.

**Files:**
- Modify: `backend/diagnostic/analysis.go`, `analysis_test.go`
- Modify: producer analysis fixtures if exact bytes change

**RED/control matrix:**
- Preserve the negative control: reached=1, unreached=0 plus timeout emits timeout only, never partial.
- Reproduce suppression with reached=1, unreached=1 plus each of timeout, command error, and cancellation.
- Add unreached>0 with reached=0 plus timeout and plus command error; all-execution-failed; unstable/degraded path plus each execution class; reached-only plus each execution class; and contradictory/malformed counters.

**Exact GREEN algorithm:**
- Normalize only valid completed counters, then set `completed = reached + unreached`; never use total attempts as the reachability denominator and never turn timeout/cancel/command-error into synthetic unreached attempts.
- Execution findings derive only from execution-failure counters (timeout, cancellation, command error) and are evaluated independently.
- If `completed == 0`, emit no reachability finding.
- If `reached == 0 && unreached > 0`, emit exactly the unreachable reachability finding.
- If `reached > 0 && unreached > 0`, emit exactly the partial-reachability finding.
- If `unreached == 0`, emit no reachability finding regardless of execution failures.
- Path stability/degradation derives only from valid completed path evidence and is evaluated independently of both execution and reachability families.
- Contradictory, negative, overflowed, or otherwise malformed counters emit no derived reachability/path fact and retain explicit inconclusive coverage.
- Emit at most one deterministic finding per independent family, with count-specific evidence and fixed execution→reachability→path ordering. Do not infer packet loss, probability, or causal routing failure.

**Verification:**
- Exhaustive small-counter permutation/property test asserting the algorithm above and malformed controls.
- Existing conservative-language and deterministic-order canaries remain green.

---

## Task 6: Freeze and synchronize the closed finding contract

**Objective:** Give one post-backend owner responsibility for freezing every producer finding code and synchronizing both closed client parsers before presentation work begins.

**Files:**
- Create: `testdata/finding-contract.json`
- Modify: producer fixture generator/freshness tests under `backend/api` or `backend/diagnostic`
- Modify: `backend/diagnostic/compact_topology.go`, `compact_topology_test.go` for canonical empty-ASN omission and non-nil empty-array preservation
- Modify: `backend/api/server.go` compact clone/rollback tests and zero-route producer fixtures
- Modify: `frontend/state.js`, `frontend/state.test.js`
- Modify: `android/app/src/main/java/com/checknetwork/app/core/Report.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/core/ReportParser.java`
- Modify: `android/app/src/test/java/com/checknetwork/app/core/ReportParserTest.java`

**RED:**
- After Tasks 4→3→5 finalize backend semantics, generate the exact producer set and prove Web and Android omit at least `service_greeting_unverified`, `tls_certificate_not_yet_valid`, `tls_hostname_mismatch`, and `tls_untrusted`.
- Mutate one otherwise valid report to an unknown, malformed, oversized, or hostile finding code and show both current client behaviors explicitly.

**GREEN contract:**
- `testdata/finding-contract.json` is exact-byte, sorted, producer-owned, and generated only after Tasks 4, 3, and 5 finish. It enumerates every valid finding code and stable semantic presentation key, plus exact 31 result shapes and the expanded 136-row `(kind,status,error_code,details)` semantic compatibility matrix with `verification_scope`; freshness tests reject producer additions/removals or matrix drift not reflected in the fixture.
- Update Web `FINDING_CODES`/result-detail validation and Android `Report.FindingCode`/model plus `ReportParser` from/against the exact fixture in this single synchronization slice. Both clients consume the producer-owned matrix rather than independent Cartesian allowlists and both consume the exact eight producer traceroute witness fixtures: healthy requires absent/empty `error_code`, generic runner-owned `cancelled` forbids details for every one of the 13 kinds, and known but contradictory tuples/details reject the whole report before UI/state publication with a fixed local `invalid_response`/`INVALID_RESPONSE` and no reflected server prose. Both clients must recognize the exact TLS matrix, validity keys, greeting result/finding, and scope before Tasks 7, 9, or 13 begin. The separate presentation contract's 10 evidence result kinds remains distinct from the 31 result-shape cardinality.
- **Closed-version policy:** an unknown finding code invalidates the analysis/report. There is no internal UNKNOWN value, neutral fallback, or reflected unknown code/title/summary/action prose. Forward additions require synchronized clients and fixture in this version, or a new versioned extension point.
- Preserve finding/evidence/action/result-reference integrity while rejecting malformed, unknown, oversized, and hostile mutations identically.
- Canonicalize a non-nil `ASNInfo` whose number is zero and organization is empty to nil before compact projection/accounting, so Go never emits `"asn":{}`. Add a direct producer test and exact fixture; clients continue rejecting an empty ASN object as non-canonical wire data.
- Preserve non-nil empty `Nodes`, `Links`, and `Routes` slices through `CloneCompactTopology`, response-size probing, and compact rollback. Canonical API output for a valid empty graph is `"nodes":[],"links":[],"routes":[]`, never null. Add focused clone/rollback/serialization RED tests and update the producer-owned zero-route fixtures before Tasks 7 and 9 enforce non-null arrays.

**Verification:**
- Repeated fixture generation has one SHA-256 and exact set equality with the Go registry, Web set, and Android enum.
- Web and Android consume the same valid and invalid mutation corpus; debug and release parser tests pass.

---

## Task 7: Consume API errors and topology schema exactly in Web

**Objective:** Preserve stable server classifications and enforce producer/Android topology bounds without reflecting hostile server prose.

**Files:**
- Modify: `frontend/state.js`
- Modify: `frontend/state.test.js`, `frontend/dom.test.js`
- Consume: `testdata/api-error-contract.json`

**RED:**
- Reproduce `body_decode_capacity_unavailable → server_busy`, `write_capacity_unavailable → server_busy`, and `full_response_too_large → http_500` with false non-JSON wording/retryability.
- Exercise every producer API-error fixture row plus unknown code, unknown/unregistered status, contradictory known status/code, malformed `Retry-After`, and hostile message/code prose.
- Reproduce accepted `latency_delta_ms=-1`, `30001`, and obsolete `latency_ms` normalized to zero.
- Mutate every compact-v1 structural object with one unknown key, null optional value, missing required value, or null array/object.

**GREEN contract:**
- A response is a valid typed API error only when its `(status, code)` exactly matches one registry fixture row; preserve that known code exactly, use its local fixed message/retryability, and apply Task 2's exact canonical delta-seconds `Retry-After` grammar only when that row permits it. Absent or invalid headers leave `retryAt` absent without invalidating the typed body; HTTP-date and combined/multiple values are rejected rather than parsed.
- Unknown code, unregistered/unknown status, and contradictory known status/code all map to the same fixed local **invalid server response** classification/message. Reflect neither code nor server prose, infer no retryability, and ignore `Retry-After`. Task 9 implements this identical policy in Android and both clients run the same mutations.
- Full topology accepts optional finite `latency_delta_ms` only in 0..30,000 and rejects `latency_ms`.
- Compact-v1 uses the following field matrix copied from the Go JSON structs. Every listed structural object is closed (unknown key rejects); every non-`omitempty` key is required and non-null; every `omitempty` key may be absent but, if present, must be non-null and correctly typed; arrays are never null.

| Object | Required keys | Optional `omitempty` keys |
|---|---|---|
| compact root (`CompactTopology`) | `schema`, `selection`, `limits`, `nodes`, `links`, `routes`, `stats`, `result_stats`, `geo`, `truncated` | `truncation_reasons` |
| `limits` (`CompactTopologyLimits`) | `nodes`, `links`, `max_response_bytes_exclusive`, `max_geo_bundle_bytes` | none |
| node (`CompactTopologyNode`) | `id`, `kind`, `status`, `hop_min`, `hop_max`, `observations` | `address`, `latency_ms_avg`, `public_ip`, `geolocation`, `asn` |
| link (`CompactTopologyLink`) | `from`, `to`, `status`, `observations` | none |
| route (`CompactTopologyRoute`) | `result_index`, `attempt`, `status`, `reached`, `complete`, `node_ids` | none |
| count stats (`CompactCountStats`) | `total`, `displayed`, `omitted` | none |
| route stats (`CompactRouteStats`) | `total`, `displayed`, `complete`, `partial`, `omitted` | none |
| topology stats (`CompactTopologyStats`) | `nodes`, `links`, `routes`, `node_observations`, `link_observations` | none |
| result stats (`CompactResultStats`) | `result_index`, `routes`, `node_observations`, `link_observations` | none |
| geo stats (`CompactGeoStats`) | `eligible`, `available`, `included`, `omitted`, `unavailable` | none |
| `geolocation` (`GeoLocation`) | `latitude`, `longitude` | `city`, `region`, `country`, `country_code` |
| `asn` (`ASNInfo`) | none | `number`, `organization` |

- This closure applies recursively to nested count/route-stat, Geo/ASN, limits, and truncation structures in both clients. The **only** additive-object exceptions are non-compact `Result.details` and existing provider response payloads at their already explicit byte/key/depth limits; no compact-v1 object is opaque or additive.
- Canonical-presence rules also apply: present scalar/string `omitempty` fields must be non-zero/non-empty (`address`, Geo text, `public_ip`, `asn.number`, `asn.organization`), and present `truncation_reasons` must be a non-empty ordered array. A present `geolocation` still requires its non-optional latitude/longitude; an `asn` object must contain at least one canonical non-zero/non-empty field. Both clients reject representations that Go canonical serialization cannot emit, including empty optional strings, `public_ip:false`, zero ASN number, empty ASN object, and `truncation_reasons:[]`.
- Preserve error alert/focus and stale-owner behavior.

**Verification:**
- `npm --prefix frontend test -- --test-name-pattern='error|topology|compact|contract'`
- Producer fixture freshness, field-by-field mutation corpus, and Web/Android exact policy parity.

---

## Task 8: Make Web credential updates transactional and truthful

**Objective:** Never erase or claim a credential state that was not successfully committed to the intended tab-local storage.

**Files:**
- Modify: `frontend/app.js`
- Modify: `frontend/dom.test.js`, `frontend/state.test.js` if helper extraction is justified

**RED:**
- `sessionStorage.setItem` throws: current UI clears input and reports configured; next request has no Authorization.
- Existing credential + blank Apply: current UI deletes it and reports configured.
- Add pre-mutation read failure, write/remove failure, post-write read-back failure, rollback mutation/read-back failure, and API-base changes. Distinguish failures that prove storage was unchanged from failures after a mutation whose final state cannot be established.

**GREEN contract:**
- Target-row admission is closed at `MAX_TARGETS=20` and also charges hidden retained views against the global `MAX_DOCUMENT_ELEMENTS=1200`. The exact-fit 20th row may commit; the 21st row or any global DOM cap+1 candidate is rejected before append with no partial row, ownership leak, or stale HMR control mutation.
- Non-clear Apply rejects blank input without mutating storage; explicit Clear is the only deletion path.
- Success is published only after storage write/remove and read-back establish the intended state. Capture and verify the previous value before mutation; if that initial read fails, perform no mutation.
- If mutation fails before changing storage, preserve input and prior state, do not advance auth revision, and publish fixed safe failure. If mutation succeeds but intended-state read-back fails, attempt exactly one rollback to the captured previous value plus exactly one rollback read-back. A verified rollback preserves prior state and reports failure without claiming the requested change.
- If rollback mutation or verification fails, enter a per-canonical-API-base **credential-indeterminate** state: retain the input, announce fixed reconciliation-required error, increment auth revision, cancel/invalidate active lanes for that API base, and block every credential-dependent request before fetch. Never claim the previous value was preserved. A later explicit Apply or Clear performs a fresh write+read-back transaction; only verified success clears the indeterminate state. There is no retry loop or Web Storage atomicity claim.
- Credential values never enter logs, status, errors, URLs, signatures, exports, DOM snapshots beyond the password input, or persisted non-session state.

**Verification:**
- jsdom target admission at exact 20/global 1,200 and both cap+1 boundaries, plus fault-injection for every Storage/rollback operation, verified-versus-indeterminate postconditions, blocked pre-fetch while indeterminate, recovery by Apply/Clear, auth revision/lane invalidation, and stale request owner.
- Real public-mode wrong/correct/clear sequence in a temporary browser fixture when available.

---

## Task 9: Enforce Android API and report semantic parity

**Objective:** Consume the producer error table exactly and reject result-indexed analysis/Geo data that cannot have been produced by the Go contract.

**Files:**
- Modify: `android/app/src/main/java/com/checknetwork/app/core/ApiError.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/network/ReportTransport.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/network/CapabilitiesTransport.java`
- Create or modify: one shared Android response-header extractor used by both transports
- Modify: `android/app/src/main/java/com/checknetwork/app/core/ReportParser.java`
- Modify: `android/app/src/test/java/com/checknetwork/app/core/ApiErrorTest.java` or existing transport tests
- Modify: `ReportParserTest.java`, producer fixture parity tests
- Modify: `scripts/assert-android-test-results.sh` and its contract tests
- Consume: `testdata/api-error-contract.json`, `testdata/finding-contract.json`

**RED:**
- Run every valid API-error fixture row and the identical Web mutation corpus: unknown code, unknown/unregistered status, contradictory status/code, malformed/disallowed `Retry-After`, and hostile prose.
- Evidence and coverage with a `kind` differing from `results[result_index].kind` currently parse.
- Use three distinct Geo cases with `<`, `>`, `&`, U+2028/U+2029: exact 4,096-byte canonical producer output; cap+1 producer input; and an explicitly invalid compact wire mutation containing an injected cap+1 bundle.
- Apply every Task 7 compact-v1 unknown/missing/null field mutation to Android.

**GREEN contract:**
- Match Task 7 exactly: only a fixture-listed `(status, code)` pair is typed; unknown code/status or contradictory pair becomes the same fixed local invalid-server-response result, with no reflected code/prose, no inferred retryability, and ignored `Retry-After`. The parser accepts only the closed `{"error":{"code","message"}}` shape within body 64 Ki UTF-16 code units, depth 2, three total properties, ten tokens, and 128-code-unit names/values; duplicate/unknown/missing/null/trailing/malformed UTF-16 fails closed. Every valid pair preserves exact code/retryability and Task 2's canonical delta-seconds-only header semantics; the shared corpus fixes whitespace, bounds, HTTP-date, overflow, comma/duplicate, absent, and malformed outcomes.
- Both report and capability transports collect **all** `Retry-After` field values before disconnect. The shared extractor accepts exactly one canonical value; zero values means absent, while duplicates, comma-combined values, or inaccessible/malformed header collections yield no retry timestamp. A single-value `getHeaderField` path is not sufficient and is forbidden by transport contract tests.
- Android's final presentation is also closed over those exact 16 pairs. `ApiError.parse` preserves the wire status but maps any unknown/mismatched pair or malformed/HTML/plain/empty error body to exact local code `invalid_server_response`, message `The server returned an invalid error response.`, non-retryable, and no retry timestamp. A valid pair keeps registry retryability even when Retry-After is absent or invalid. `ApiErrorPresentation.resolve` shows retry from registry retryability, appends only an allowed present timestamp, focuses the visible credential field only for `unauthorized`, and focuses the fixed error for every other/invalid classification.
- Evidence and result-indexed coverage must match referenced result kind; attempt references are valid only for traceroute and within producer counters where available.
- Use producer-equivalent Go HTML-escaped JSON byte accounting without an unbounded duplicate allocation and the same 4,096-byte inclusion rule.
- Split Geo fixture assertions exactly: (a) exact-boundary producer output is accepted byte-for-byte by Web and Android; (b) cap+1 producer **input** causes producer omission/truncation and is never emitted as a valid compact bundle; (c) a named invalid-wire cap+1 mutation is rejected by both clients.
- Keep immutable deep copies and all existing aggregate limits. Enforce the complete Task 7 field matrix recursively: closed structural compact-v1 objects, required non-null keys, and optional `omitempty` fields absent or non-null. Only bounded non-compact `Result.details` and existing provider payloads remain additive.

**Verification:**
- Debug/release consumer fixture tests and producer freshness tests.
- Exact/cap+1/invalid-wire Geo corpus and field-by-field compact mutation parity with Web.
- Strengthen `scripts/assert-android-test-results.sh` so only bounded direct-child XML is evidence: every XML has a root `testsuite`, canonical nonzero root count, zero errors/failures, and the expected exact filename/root/testcase class; nested/duplicate/ambiguous results, opposite-variant classes, generic mentions and substrings reject. Permit only an exact `binary/` companion containing regular non-symlink `output.bin`, `output.bin.idx`, `results.bin`, each ≤64 MiB and total ≤128 MiB with no nesting/extra/missing/special/XML entry. This companion is bounded Gradle metadata, never test evidence or a count contribution. Current source inventory is **297 direct-child XML tests / 2 variant-contract tests per variant**; the documentation edit invalidates prior final CI evidence, so fresh clean-environment CI must record actual counts and they are never a permanent verifier threshold. Run both variant collection assertions, lint, assemble, and R8.

---

## Task 10: Publish Android presentation readiness only after an atomic owner-checked swap

**Objective:** Fix the Activity presentation-order defect without moving view concerns into retained network/session state.

**Files:**
- Modify: `android/app/src/main/java/com/checknetwork/app/MainActivity.java`
- Create or modify: a MainActivity-owned presenter/immutable presentation-state class and injectable renderer/view-commit seam only as needed
- Modify: `MainActivityTest.java`, renderer/presenter tests, and strings
- Do **not** modify `DiagnosticsSession.java` unless a separate focused RED test first proves an independent session defect; its listener isolation is not the reproduced defect.

**RED:**
- Inject deterministic exceptions before the first detached view, mid detached-hierarchy build, immediately before owner swap, and during swap/commit. Current status/share/visible hierarchy must expose the false-ready or partial-state defect.
- Cover focus/accessibility publication failure, rotation, owner change during build, stale callback, cancel, retry, and rollback failure containment.

**GREEN contract:**
- `RequestState.READY` remains parsed-network-result truth only. `MainActivity`/its presenter alone owns `RENDERING`, `RENDERED`, and `RENDER_ERROR`; no presentation failure feeds back into `DiagnosticsSession`.
- First build `AnalysisPresentation` and the complete detached Android view hierarchy without mutating the currently visible report tree, status, focus, announcement, or share eligibility.
- Through an injectable renderer and view-commit seam, recheck request owner/signature immediately before a single hierarchy swap. On owner mismatch discard the detached hierarchy. On any swap/commit exception, remove the candidate and restore a known empty fixed-error hierarchy; never leave old/new children mixed.
- Publish report visibility, scoped ready status, focus, live-region announcement, and share eligibility only after the swap succeeds and the same owner still holds. If any post-swap publication step throws, roll back to the known empty/error UI and disable sharing.
- Retry allocates a fresh owner. Rotation rebuilds from retained parsed data through the same detached/swap path; a stale old owner cannot commit or roll back a newer hierarchy.

**Verification:**
- Robolectric injected before-first/mid-build/pre-swap/swap/post-swap fault matrix in debug and release.
- Lifecycle rotation, owner-swap race, stale rollback, cancel, and retry tests remain green.

---

## Task 11: Make raw report sharing a process-wide bounded asynchronous state machine

**Objective:** Keep all 8 MiB and cleanup filesystem work off the UI thread while enforcing one process-wide writer, finite sensitive retention, and exact URI ownership.

**Files:**
- Modify: `android/app/src/main/java/com/checknetwork/app/RawReportShare.java`
- Modify: `android/app/src/main/java/com/checknetwork/app/MainActivity.java`
- Modify: `RawReportShareTest.java`, `MainActivityTest.java`, lifecycle/process-recreation tests
- Modify: strings/resources only for preparing/cleanup/failure states

**RED/state matrix:**
- Prove encode/write/flush/fsync and `readyRaw` UTF-8 sizing currently run on the main looper and the object monitor remains held across I/O.
- Block the first writer, create/destroy/rotate several Activities/managers, and prove a manager-local executor can admit another writer or retain an Activity.
- Seed more than 32 prior-process `shared-reports/raw-<token>.json` and `.raw-<token>.tmp` entries, including many younger than 15 minutes and an aggregate above 16 MiB, plus unrelated files, symlinks, collision paths, and deletion failures.
- Cover `idle → cleaning → preparing → materialized → granted → retired/disposed`, duplicate confirmation, rotation in every state, new report, cancel, destroy, chooser exception, 15-minute boundaries, stale callback, and a non-cooperative writer.

**Exact runtime/admission contract:**
- `RawReportShare` owns one **process-wide singleton runtime** created from application context: exactly one serial filesystem worker/thread and one writer slot for the process. There is **no preparation queue**. While cleanup or preparation owns the slot, reject every duplicate confirmation with fixed busy state; never replace, enqueue, or create another filesystem worker. A bounded lifecycle timer may schedule state invalidation/revocation but cannot admit or execute a second writer.
- Activities own only UI/chooser launch and attach/detach an owner-token listener. Detach/cancel/clear are nonblocking state invalidations and cannot retain a rotated Activity. No monitor/lock is held during UTF-8 encoding, directory traversal, write, flush, fsync, move, revoke, delete, or URI creation.
- Encoding, exact ≤8 MiB revalidation, create/write/flush/fsync, and cleanup run off-main. Operation ownership and the global slot remain held until the actual worker/I/O call returns; cancellation never reports termination or admits another writer early.
- Final process-runtime disposal first closes admission, invalidates listeners, and returns a completion handle that does not complete until actual owned work returns and the singleton executor has terminated. Activity/configuration destruction only detaches; it does not dispose the process runtime.

**Exact files, cleanup, and publication contract:**
- The only recognized names are `raw-<token>.json` and `.raw-<token>.tmp`, where `<token>` matches `[A-Za-z0-9_-]{16,128}`. Do not touch unrelated entries.
- Startup and every pre-share admission run asynchronous reconciliation passes that inspect at most **32 total directory entries and perform at most 32 deletions per pass**, with a resumable deterministic cursor and no file-content reads. Continue bounded passes until a full pass proves convergence; **no new share preparation/publication is admitted until convergence**. Any scan/stat/delete failure is observable/retryable and keeps admission closed.
- At process-runtime startup there are no valid in-memory leases: every recognized prior-process JSON/temp is therefore orphaned and immediately cleanup-eligible regardless of age. Reconciliation may not admit a new share until all such orphans are gone. During one live runtime, at most **2 recognized files** and **16 MiB aggregate recognized bytes** may exist (one granted artifact plus one active candidate/temp); exceeding either bound closes admission and deterministically retires unleased oldest-by-mtime/path entries through the bounded cleanup cursor. Invalid/future metadata is treated as oldest. Current leased work is never deleted to fake capacity, and unrelated files are excluded from both limits and deletion.
- Each issued/granted content URI has a **15-minute process-observed lease**, not an enforceable provider authorization expiry. At the first timer/lifecycle/API boundary observed by the running app at or after expiry, mark it retired and initiate revoke exactly once before admitting another share. Filesystem deletion is then owned exactly once by the serial worker and may complete only after an earlier non-cooperative I/O call actually returns. After process death, stock `FileProvider` can be restarted directly by a recipient without constructing this runtime, so post-death grant expiry and already-open descriptors are explicitly outside the guarantee; next application runtime startup removes every recognized orphan before new sharing.
- Validate the dedicated app-private directory without following links and reject a symlink/non-directory at every boundary. Create the temp with no-follow **CREATE_NEW** semantics, never replace an existing temp or destination, fsync, atomically move without replacement, then revalidate canonical/real-path containment and regular-file/no-link type before URI creation. Directory swaps, live/dangling symlinks, special files, and deterministic token collisions fail with outside bytes untouched.

**Exact lifecycle/URI contract:**
- Revalidate attached Activity owner, report owner/signature/raw identity, and URI state on main before launch. A preparation whose original Activity detached/rotated may never auto-launch after completion; retire its candidate unless a fresh explicit confirmation starts a new operation.
- A URI already in `granted` state is preserved across configuration rotation and remains governed by its original TTL; rotation alone neither revokes nor relaunches it. New report, explicit retire, final destroy, or TTL revokes/deletes exactly once. Stale callbacks cannot affect a newer URI.
- `ActivityNotFoundException`, `SecurityException`, or any chooser-launch failure immediately revokes and deletes the candidate exactly once and publishes fixed safe failure. Never place raw JSON in Bundle, preferences, logs, errors, `EXTRA_TEXT`, or request signatures.
- Every terminal preparation failure after a temp/final file may exist—including encode/write/fsync/move/URI creation failure, cancellation, stale completion, and late non-cooperative return—queues exact-once deletion on the serial filesystem worker **before** releasing the process-wide slot. If revoke/delete fails, retain explicit cleanup-failed ownership, keep admission closed, and retry through the bounded reconciliation state; never abandon a sensitive file merely because it remains under count/byte bounds.
- Preserve unique content URIs, explicit warning/confirmation, and non-exported read-only FileProvider semantics.

**Verification:**
- Deterministic singleton/executor/looper tests; blocked writer across repeated manager creation proves one writer/thread, no queue, duplicate rejection, and slot release only on actual return.
- Exact 8 MiB boundary; >32-entry cleanup convergence; 2-file/16-MiB live-runtime hard bounds including under-15-minute orphans; 15-minute process-observed lease; delete failure admission block; unrelated-file preservation.
- Publication symlink/no-follow/create-new/collision/directory-swap/containment tests and outside-tree byte canaries.
- Rotation state matrix, pending-completion nonlaunch, granted-URI preservation, chooser failures, stale callback, and exact-once revoke/delete.
- Debug/release unit, lint, assemble, and R8.

---

## Task 12: Restore contextual Android accessibility names

**Objective:** Keep dynamic control names synchronized with edited target data without exposing credentials or unbounded text.

**Files:**
- Modify: `android/app/src/main/java/com/checknetwork/app/MainActivity.java`
- Modify: `MainActivityTest.java`, relevant string resources

**RED:**
- Edit a target address after row creation; remove button retains the old address in its accessibility name.
- Cover empty, long, Unicode, duplicate rows, rotation, and row removal/reindexing.

**GREEN contract:**
- Refresh the row’s contextual remove label on address change using a bounded presentation string.
- Empty address falls back to stable row position/neutral label.
- Preserve 48dp targets, deterministic focus, heading/live-region semantics, and no credential reflection.

**Verification:**
- Robolectric accessibility-name tests debug/release.
- Actual TalkBack/Switch Access remains explicitly unverified.

---

## Task 13: Provide privacy-safe evidence and action parity in Web/Android human views

**Objective:** Make shared diagnoses substantiate findings with safe expected/observed/coverage facts while refusing raw server prose and sensitive values.

**Files:**
- Modify: `frontend/app.js`, `dom.test.js`
- Modify: `android/app/src/main/java/com/checknetwork/app/AnalysisPresentation.java`
- Modify: `ReportMarkdownExporter.java` and their tests
- Modify: fixed strings/presentation registries
- Modify: producer-owned diagnostic fixtures where needed

**RED:**
- Web on-screen evidence currently omits `expected`.
- Web human export includes actions but only coverage counts and omits evidence/limitation facts.
- Android screen reduces coverage available/missing to counts; Android human export omits evidence/actions entirely.
- Inject target/IP/token/server-prose canaries and prove any naïve field passthrough would leak them.

**GREEN contract:**
- Define local exhaustive presentation templates keyed by the closed finding fixture’s stable semantic keys, evidence signal, coverage code, and action relationship. Parser rejection handles unknown finding codes; presentation has **no neutral unknown fallback** and never reflects unknown code/title/summary/action prose.
- Show expected/observed only for allowlisted bounded value types: closed enums/codes, validated status/count/attempt values, and safe timestamps. Redact or summarize address/endpoint/provider/raw prose.
- Render fixed coverage signal labels and limitation categories, not untrusted `reason` prose. Include fixed next-action templates for every known finding.
- Web DOM, Web Markdown, Android screen, and Android Markdown implement the same stable semantic fixture keys—not localized prose equality—for `cause`, `supporting_evidence`, `expectation`, `evidence_directness`, `coverage_limitation`, and `next_action`.
- For every healthy named service and the overall verdict, scope the UI/export wording to observed checks. The named-service semantic text must mean exactly: **“Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.”** Never render generic PASS, Healthy, “response is normal,” or overall-success copy that erases this scope. Emit the fixed post-greeting coverage limitation without server prose.
- Keep raw JSON sharing behind its existing stronger warning path.

**Verification:**
- One producer-owned scenario per finding family plus `testdata/finding-contract.json`; assert stable semantic keys/section structure across all four surfaces, allowing localization only after key parity passes.
- Valid-greeting-then-immediate-close fixtures for every named service kind prove a greeting pass but no full-service-health claim.
- Poison report ID, titles, summaries, reasons, actions, target, URL, hostname, IP, credential, provider/error/panic/details prose and prove all Web/Android human surfaces exclude them.
- Keyboard/focus/live-region and large-report bounds remain green.

---

## Task 14: Bind offline API and Web archives to the exact source revision

**Objective:** Produce double-built, byte-equal, tagless API and Web archives; validate them offline within hard resource bounds; and keep both derived archives outside the shared daemon.

**Files:**
- Modify: `frontend/Dockerfile`
- Modify: `compose.yaml`
- Modify: `scripts/verify-release.sh`
- Modify: `cmd/checknetwork-api/operations_contract_test.go` or create a release-contract test owned by scripts
- Modify: `.dockerignore`/frontend ignore only if required

**RED/pre-implementation gate:**
- Prove the old verifier depended on daemon image lifecycle for API and omitted reproducible Web evidence; prove the mutable nginx reference and deterministic-name cleanup gaps.
- Require two no-cache/provenance-disabled, timestamp-rewritten tagless archives from the same canonical source/epoch for each artifact family. Any archive-byte or validator-identity inequality blocks Task 14 rather than weakening reproducibility.
- Snapshot daemon image/tag inventory and prove any derived archive load/tag/import/delete/execute action is observable and forbidden.

**GREEN contract:**
- Both API and Web use `docker buildx build --no-cache --provenance=false --output=type=docker,dest=<archive>,rewrite-timestamp=true` with the exact canonical context and identity args. Each pair is byte-identical and its offline validator outputs are equal.
- API hard limits are archive 512 MiB, members 4,096, member 256 MiB, member total 512 MiB, layers 64, compressed layer 256 MiB, expanded layer 512 MiB, and selected-file total 128 MiB. Before reading layer member semantics, validate the exact pinned Alpine layer digest, diff-ID, and history prefix as the trusted base. The final projection contains only executable `0755` regular `/checknetwork-api` and `/traceroute`; verify pinned traceroute bytes and Go version/path/revision.
- API extraction opens a caller-owned empty real directory by directory FD, uses no-follow exclusive creation, complete writes, fsync, then no-follow size/mode/hash readback. Publication copies immutable validated bytes into a destination-local temporary file, revalidates the original tree, atomically renames, and restores/removes only the transaction-owned path on failure or signal.
- Web performs the analogous bounded offline manifest/config/layer/whiteout projection for exact canonical nginx config and six assets. OCI whiteouts use a layer-local marker set: root opaque removes all lower entries, directory opaque removes lower descendants, and ordinary whiteout removes the lower target/descendants; entries created in the same layer survive regardless of marker tar order. A removed canonical selected file must be recreated in that or a later layer. Both validators reject traversal, duplicate or unreferenced ambiguity, malformed JSON/OCI graphs, unsafe member types and invalid whiteouts.
- **The derived API archive and derived Web archive are never daemon-loaded, daemon-tagged, imported, deleted, or executed.** Each verifier owns exactly two daemon roles: API owns one smoke container and one API network; Web owns one smoke container and one internal network. Pinned Alpine/nginx bases are borrowed and never removed.
- Runtime checks mount validated selected files read-only into the matching pinned borrowed base. These are compatibility smokes only; they do not prove either derived archive was executed.
- Every container/network enters pending before create and uses a high-entropy name, nonce label, independent owner label, and immutable ID. Normal response and daemon-success/CLI-response-loss recovery converge on the same exact name+labels+ID proof; cleanup rechecks that proof and removes only that ID. Collision, replacement, mismatch and caller-owned resources survive ordinary failure and HUP/INT/TERM.
- Success output is exactly six API fields—`api_archive`, `api_config`, `api_manifest`, `api_rootfs`, `api_binary`, `api_traceroute`—and five Web fields—`archive`, `config`, `manifest`, `rootfs`, `asset_manifest`.

**Verification:**
- Mutate every hard limit, trusted-base field, selected path/mode/hash, manifest/config/OCI relationship, whiteout, extraction target, publication boundary and output field; require fail-closed behavior.
- Run two real tagless builds per family and compare archive bytes plus every declared validator identity. Assert daemon image/tag inventory is unchanged and only the two label/ID-proven container/network roles per family are created and removed. The production exact gate runs the verifier under all three caller umasks `077/027/000` and compares canonical Docker inventory; its fake gate has exactly 12 cases.
- Inject collision, response loss, replacement, ordinary failure and HUP/INT/TERM at owned create/build/validation/smoke/publication stages; caller/borrowed resources survive and no success line is emitted on failure.
- `docker compose config --quiet` in trusted-local and public configurations.

---

## Task 15: Documentation and executable acceptance closeout

**Status:** Implementation and documentation source/link/stale/secret/diff focused gates plus all task-level reviews report blocker 0 / major 0. Final post-documentation clean-environment `make ci`, artifact cleanup, manifest/release/five-way precommit/commit/exact-SHA/main-integration gates remain pending below; this is not a final completion claim.

**Objective:** Make implemented contracts and remaining capability/manual gaps explicit after source behavior is final.

**Files:**
- Modify: `README.md`, `SPEC.md`
- Modify: `docs/API.md`, `docs/ARCHITECTURE.md`, `docs/OPERATIONS.md`, `docs/TESTING.md`, `docs/PLAN.md`
- Modify: this Stage 8 plan status only after independent reviews pass

**Documentation requirements:**
- Shutdown nonzero semantics and bounded fields.
- `expected_status` kind/range rule, final-response redirect semantics, and the registry-owned fixed API error contract.
- Exact service greeting grammar/caps/outcomes, `verification_scope=server_greeting`, zero-write/security invariants, and scoped UI language for untested post-greeting semantics.
- Exact typed TLS matrix/validity fields and mixed traceroute independent-family algorithm: failed-attempt topology is full raw-only and excluded from compact/Geo aggregates; full timeout/command-failure may retain a representative topology from a separate completed route; generic runner-owned cancelled forbids details.
- Startup traceroute probes only trusted deployment `PATH`, loopback, fixed grammar candidates, a shared 2-second deadline, and combined 32 KiB output. All failures produce a nil fail-closed capability; runtime never re-resolves PATH or falls back. Both clients use exact fixed `Traceroute capability was unavailable, so no route observation was established.` presentation without reflecting server prose or target.
- Closed business drain ordering is route → public auth → rate limit → `server_draining` → body decode → report/checker admission; drain rejection starts no body/report/checker/SSRF work.
- Closed finding-code/version policy, producer synchronization fixture, field-by-field compact-v1 closure, three-part Geo boundary fixtures, and identical client API-error invalid-response policy.
- Web/Android stable semantic-key parity and privacy boundaries; Android variant-canary collection proof.
- Android Activity-only render/swap/rollback seam and process-wide raw-share singleton, lifecycle, cleanup/TTL, URI, and path-safety contracts.
- Reproducible API+Web artifacts using required buildx timestamp rewriting, tagless offline archive validation, pinned-base read-only-mount runtime smoke, exact revision/version identity, creation-ID-safe container/network cleanup, and verifier output.
- Canonical manifest-before-temporary-commit sequencing and explicit parent/tree identity.
- Explicitly retain unverified physical browser/device/assistive technology/HTTP2 and future capability gaps.

**Verification:**
- Source-to-doc symbol/constant checks.
- No “implemented/verified” plan status before exact task review evidence exists.
- `git diff --check`, shell syntax, Compose config, documentation link/path checks.

**Implementation/review status:** Tasks 1–15 implementation/documentation changes are present. Accepted API/Web offline-archive release changes, Android final API-error classification and bounded closed parser, direct-XML/bounded-`binary/` result verification, JDK/SDK bootstrap changes, and Web target admission max 20/global DOM 1,200 are in the current candidate. Latest parent focused gates and every task-level implementation/documentation review report blocker 0 / major 0. Exact current stable source cardinality is API errors 16 / producer-derived structural mutations 85; findings 23 / result shapes 31 / expanded result matrix 136; separate presentation 6 semantic keys / 10 evidence result kinds / 21 coverage signals / 23 action relationships / 33 scenarios; Web 260 tests with 456 fixture-derived semantic mutations including 13 generic cancelled-detail rejections; Android debug/release each 297 direct-child XML tests + 2 variant-contract tests; API 28 + Web 25 = 53 archive-validator tests; and exact real release-gate fake cases 12. Both clients consume the exact producer matrix and all eight producer traceroute witness fixtures. The production exact gate runs three umasks 077/027/000 against canonical Docker inventory. Documentation changes invalidate prior current-byte CI/count evidence. Final post-doc `env -u JAVA_HOME -u ANDROID_HOME -u ANDROID_SDK_ROOT make ci` remains pending, as do external manifest freeze, a new manifest-derived isolated temporary direct-child commit and release, five-way independent precommit review, explicit staging, the Stage 8 commit, exact-SHA postcommit gates, and main integration. The ignored repository-root `checknetwork-api` binary is excluded from the candidate manifest and verification evidence but remains retained because this run is non-destructive; artifact cleanup is not claimed complete. Previous temporary manifest versions, record counts, and hashes are not current evidence. No final manifest, commit, release, five-way review, exact-SHA, or main-integration claim is made here.

---

## Dependency-ordered implementation and ownership

1. Task 1 may run independently.
2. Task 2 freezes request/API-error/final-redirect contracts before Tasks 7 and 9.
3. One backend owner must execute the overlapping diagnostic sequence **Task 4 → Task 3 → Task 5** (TLS, greeting, traceroute), without parallel edits to `service.go`, `model.go`, `analysis.go`, tests, or producer fixtures.
4. Task 6 runs only after that sequence is final; it freezes the exact producer finding fixture and synchronizes `frontend/state.js`, Android `Report.java`, and `ReportParser.java`. No presentation task starts first.
5. Task 7 follows Tasks 2 and 6 and owns the next `frontend/state.js` pass. Task 8 follows Task 7 or is assigned to the same Web owner because Web files overlap.
6. Task 9 follows Tasks 2, 6, and the shared compact matrix fixed in Task 7; it owns the next Android parser/API pass and the variant-canary gate.
7. Task 10 follows Task 9 and owns only MainActivity/presenter view materialization; `DiagnosticsSession` remains untouched absent a separate RED defect.
8. Task 11 follows Task 10 because `MainActivity.java` overlaps; Task 12 follows Task 11 for the same file.
9. Task 13 follows Tasks 3–12, explicitly including Task 8, and owns the next sequential `frontend/app.js`/`frontend/dom.test.js` pass after Task 8 so all finding, scope, evidence, parser, lifecycle, and presentation contracts are final.
10. Task 14 is source-independent after its mandatory reproducibility spike, but merge it before canonical gates; one release owner also owns all Docker resource creation/cleanup changes.
11. Task 15 follows every code task and independent task review.

Agents may parallelize only genuinely disjoint ownership. Any overlapping file has one sequential owner; no agent overwrites another agent’s work.

## Per-task review gate

After each task:

1. Re-run the focused RED test and show GREEN.
2. Run focused race/property/boundary tests.
3. Dispatch a fresh read-only reviewer for the task’s exact current bytes.
4. Reviewer must reproduce the original defect as fixed and report blocker 0 / major 0.
5. If a finding is accepted, patch via a new strict RED→GREEN slice and invalidate prior evidence.
6. Maximum two fix/review cycles per task; after that stop and reassess architecture rather than layering patches.
7. Do not stage or commit per task.

## Final precommit gate: manifest before temporary commit

After final documentation edits:

1. Leave `JAVA_HOME` undefined to exercise automatic portable discovery of system JDK 17, unless intentionally testing an explicit override. SDK precedence is explicit `ANDROID_HOME`, explicit `ANDROID_SDK_ROOT`, valid `~/Android/Sdk`, then valid project-managed `~/.local/share/checknetwork-android/sdk`; explicit empty/invalid values must fail closed without fallback.
2. Run fresh `make ci`; record actual Go/Web/Android counts, including the two variant-specific Android canaries.
3. Remove every ignored/generated artifact and prove no audit server, Docker artifact, APK, binary, dependency directory, release output, or probe remains.
4. **Before any temporary commit**, freeze the intended candidate—including additions, modifications, deletions, path moves represented canonically as delete+add, and this Stage 8 plan—into `${TMPDIR}/stage8-manifest.jsonl`, outside the repository. The manifest excludes its own external path by construction; no manifest/hash file may be added to the candidate.
5. Canonical manifest v1 is UTF-8 JSON Lines with LF only and no insignificant whitespace. Line 1 has fixed key order `{"format":"stage8-manifest-v1","parent":"36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243"}`. Git has no intrinsic rename object, so the manifest has **no rename operation**: every path move is encoded as one delete of the old path and one add of the new path, even when bytes/modes match. Remaining records are sorted by decoded raw path bytes, then operation rank `add=0`, `modify=1`, `delete=2`. Paths use unpadded base64 of raw Git path bytes and every object uses the exact key order shown:
   - add: `{"op":"add","path_b64":"...","mode":"0644|0755","size":N,"sha256":"64-lower-hex"}`;
   - modify: `{"op":"modify","path_b64":"...","mode":"0644|0755","size":N,"sha256":"64-lower-hex"}`;
   - delete: `{"op":"delete","path_b64":"..."}`;
   Normalize regular executable/non-executable modes only; reject symlinks/submodules/special modes unless explicitly approved and separately encoded. Disable Git similarity/rename inference when deriving and verifying the manifest; duplicate-content paths therefore cannot create ambiguous pairings.
6. Compute and record SHA-256 of the exact saved manifest bytes. A verifier rereads it, rejects duplicate/conflicting paths, confirms parent, add/modify/delete operation, normalized mode, size, content hash, and that the candidate has no unmanifested path (including generated files).
7. Materialize **exactly** that manifest against base `36dc1dca…` in an isolated temporary clone, create one temporary direct-child commit, and prove its parent is exactly the base and its Git-tree fingerprint reconstructed as normalized mode/path/blob bytes equals the manifest-derived fingerprint. Never create this commit in the Stage 8 worktree.
8. In that clean temporary commit, run standalone and supplied-canonical-archive API+Web release verification. Require exact six API fields `api_archive/api_config/api_manifest/api_rootfs/api_binary/api_traceroute`, exact five Web fields `archive/config/manifest/rootfs/asset_manifest`, pairwise build equality, compatibility-smoke bytes, unchanged daemon image/tag inventory, and zero invocation leftovers/caller-resource damage. Both derived archives remain offline and are never daemon-loaded, daemon-tagged, imported, deleted, or executed.
9. Run parallel independent reviews against the immutable manifest SHA and temporary commit tree for:
   - backend runtime/shutdown/protocol/TLS/traceroute;
   - API/error/privacy/telemetry;
   - Web schema/credential/evidence/UI/browser behavior;
   - Android parser/render/share/lifecycle/accessibility/variant collection;
   - release API+Web reproducibility/resource ownership/docs.
10. Any blocker/major or byte change invalidates the manifest, temporary commit, release evidence, and reviews. Resolve with strict TDD, return to step 2, regenerate/hash a new manifest, and review the full new tree.

## Commit and exact-SHA closure

Only after all precommit reviews report blocker 0 / major 0 for the immutable manifest digest:

1. Reverify worktree bytes/status against that exact manifest and base parent, then stage each add/modify destination explicitly and each deletion source explicitly—never `git add .`.
2. Verify index path set, add/modify/delete delta with rename detection disabled, normalized modes, blob sizes/hashes, and reconstructed index-tree fingerprint exactly match the manifest and temporary commit; verify no unstaged or untracked path exists outside the manifest.
3. Create the single Stage 8 commit; do not push. Assert `git rev-parse HEAD^` is exactly `36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243`, and assert committed tree/fingerprint equals both the manifest and reviewed temporary commit.
4. Run exact committed-SHA `make ci-clean-archive`; it must verify Go/Web/Android (including variant canaries), the exact six API and five Web offline output fields, double-build equality, and revision identity. API/Web evidence remains offline archive/config/manifest/rootfs/selected-file evidence, not a daemon-loaded/tagged/imported/deleted/executed derived artifact claim.
5. Run independent exact-SHA product and release closure reviews.
6. Fast-forward `main` with `--ff-only`; verify clean status; do not push.
7. Re-audit the new HEAD and begin the next defect/capability stage until interrupted.

## Manual acceptance that must remain pending unless actually exercised

- Real Chrome, Firefox, and Safari at 320/375/400 px plus keyboard and screen-reader paths.
- Android emulator and physical device; TalkBack/Switch Access; OEM share sheet; process kill with granted URI; stock `FileProvider` post-process-death grant expiry and already-open descriptors; actual 320dp/large-font/IME insets.
- TLS reverse proxy with HTTP/2 deadline and cancellation behavior.
- Production registry push/signature/SBOM and hosted CI.
