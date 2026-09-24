# Architecture audit remediation campaign

## Authority / baseline

- User authorized sequential execution of the reviewed improvements; continue after each verified stage without another routine approval.
- Base: c6e812d1d3e9bcb59a19bc4ef32036539d19ce2d, main.
- Preserve user-owned compose.yaml host-port 8090 (SHA-256 e171643639a2bdc59407296ad63c98df18803e1d96abfa06027f1b559150fa55). No commits/pushes or deployed-service interruption are authorized by this turn.
- Frozen audit: /home/piecer/.hermes/reports/revealnetworktroble/2026-09-24/REVIEW.md. Android A1 uses reachable input-invalidation interleaving, not the fake concurrent new-READY ordering.
- Each stage: vertical RED→GREEN regressions, scoped tests, independent read-only review, current canonical gates, then next stage. Tests accompany fixes rather than waiting until stage 4.
- Preserve all worker/queue/byte/DOM bounds, policy enforcement, explicit raw-share confirmation, and caller-owned external resources.

## Stage 1 — Observation integrity (verified)

Findings B1/B3/B4/B5/B2/B6.

Contracts:
- Completed trace attempts survive optional GeoIP timeout and later resolver failures. Parent cancellation retains its existing higher priority; blocked destinations never execute.
- Geo enrichment is best effort within a child deadline strictly preceding the report cutoff; no mutable worker result may be read concurrently after cutoff. Never merely increase the whole report timeout.
- Later resolution failure is recorded as a failed attempt in existing truthful wire vocabulary, retaining completed observations. Preserve fail-closed public API policy behavior.
- Only measured responsive RTT samples enter averages; explicit measured zero remains a sample, unknown/synthetic values do not.
- Coordinates must be explicitly present numeric latitude/longitude; missing/null yields no geolocation. Valid ASN-only metadata may remain; a response with no usable location or ASN is malformed and is not success-cached. Explicit (0,0) remains valid.
- Path variation is evaluated independently of aggregate healthy status; failed attempts do not establish path variation.
- RTT delta classification must not skip a responsive predecessor just because it was already marked degraded.

Gates: producer/Runner/analysis/compact regressions; exact producer fixture checks and Web/Android consumers where wire bytes change; make test, make test-race, make vet, make build; independent review.

Ownership: parent owns GeoIP decode/cache regression and compact RTT aggregation; isolated worker owns traceroute execution/enrichment/analysis and associated tests. Overlap is serialized at integration.

## Stage 2 — Web persistence and presentation (verified)

Findings FE-1/FE-2/FE-3/FE-5.
- Preserve existing labels during bounded import, allow updates at capacity, reject excess new keys with truthful counts.
- Derive usable label pagination from retained document budget; all labels remain reachable without raising 1,200 element cap.
- Separate empty user selection from selected-but-unobserved/failed execution; show fixed safe diagnostic reason.
- Ignore empty textarea separators, preserve strict target validation and 20 actual target maximum.
Gates: real app DOM events, maximum-input cross-view matrix, actual-producer unavailable response, owned Chromium replay, make test/build/vet and Web syntax; independent review.

## Stage 3 — Android lifecycle and recovery (verified)

Findings A1/A2/A3/A4/A5.
- Dispatch only current transition snapshots after input changes, attach changes and destruction.
- Raw share eligibility derives from current READY owner and runtime phase; every new share still requires confirmation.
- Connect bounded cleanup retry without reopening admission before verified cleanup.
- Submit only kind-applicable options while preserving validation of malformed direct inputs.
- Strict capability JSON preflight rejects duplicates/non-JSON grammar/depth violations before materialization; keep intentional forward-compatible capability names.
Gates: deterministic publication barriers, Activity share failure/expiry/recovery/kind switching, discovery→POST rejection matrix; make android-test/lint/assemble plus make test/build; independent review.

Integration status: A1/A5 and A2/A3/A4 integrated, preserving Stage1 producer-consumer tests. Both independent blocking reviews PASS; parent verified all ten reported source hashes. Parent final Android tests passed with 331 tests per variant and no failures/errors/skips; lint passed with 28 debug/26 release warnings; assemble, make test/vet/build passed. Reviewer-only additional corpus/probes remain supplemental evidence, not canonical test counts. No physical-device or deployed-service acceptance claimed. Closing-record canonical rerun precedes Stage4.

## Stage 4 — Boundary/deployment verification (verified)

Findings FE-4/API-1/OPS-1/OPS-2/OPS-3.
- Repair obsolete graph palette gate and expose explicit reproducible browser target with blank-graph negative canary.
- Expose Retry-After on allowed CORS responses, verify actual two-origin Go-handler/browser fetch.
- Preserve operator's 8090 port and provide coherent client deployment configuration rather than resetting compose.
- Reproduce constant-epoch stale validator with actual isolated nginx before correction; use an explicit freshness policy without weakening reproducibility.
- Reject invalid derived image runtime command/env in offline archive validators; preserve borrowed-base/resource-ownership limits.
Gates: browser/Go HTTP integration, archive mutation corpus, effective config checks, make test/race/vet/build/Web syntax. Real Docker release or service restart remains unperformed unless separately authorized/available.

Ownership: parent repairs CORS/browser gates; isolated deployment worker owns static freshness and coherent Compose default; isolated archive worker owns offline runtime-config validation. Worktrees start from the verified Stage1-3 overlay, not merely old HEAD. Parent CORS unit and native browser reproduced RED (hidden Retry-After), then GREEN. Graph-first reproduced eight obsolete-palette failures, then passed with actual node/link pixels and blank-pixel canaries. Stage4 integration and blocking reviews are complete; deployed services are untouched.

OPS integration: ten delegated file hashes verified and integrated. Compose now uses CHECKNETWORK_API_PORT with preserved 8090 default for publish and frontend build; its original hash above remains the historical Stage1-3 preservation receipt, not the new Stage4 hash. Canonical frontend default stays 9090. Parent added quoted-path harness regression (RED→GREEN), permanent JS/Python test collection, explicit deployment/live targets and reconciled archive inventory 33+30=63 without deleting historical counts. Stage4 deployment/archive blocking reviews are dispatched as deleg_9fe9e71b; browser/CORS review was dispatched separately as deleg_0dc12505. Those reviews are reconciled by the closure records below.

Browser review follow-up: deleg_0dc12505 found a timeout false PASS when Playwright cleanup exits zero. Parent permanent wrapper regressions reproduced RED; timeout is now latched before SIGTERM and rejects regardless of exit code. Five lifecycle tests pass and the exact reviewer real-Chromium probe now stops after one timeout with exit 1 and zero PASS messages. Closure review deleg_3eb284dd PASS: parent verified all 22 source/probe hashes. The independent real-Chromium negative probe fails for the intended timeout after BROWSER_READY; ordinary browser/CORS gates pass.

Deployment/archive review follow-up: deleg_9fe9e71b passed product configuration/freshness and runtime validators, but found a post-unmask/pre-initialization signal cleanup gap and a stale current documentation-contract count of 53. Parent reproduced HUP/INT/TERM leaks with real process signals against a stateful fake Docker, then enclosed unmask and initialization in one cleanup scope; all four ownership tests now pass. The current coverage bullet is checked against the collected archive total (63), not merely a matching phrase elsewhere; that assertion reproduced RED before the documentation correction. Historical count records remain unchanged. Closure review deleg_e4950bb8 PASS: parent verified 260 snapshot hashes and four archive-runtime source hashes. Independent subprocess signals prove exact-ID removal once and absence; private stale-current-count mutation fails while historical counts remain intact. Parent canonical make test (382 Web), race/vet/build/syntax/deployment and actual nginx gates passed; exact live container ID/name absence was read back. Closing-record rerun precedes Stage5.

## Stage 5 — Bounded modularization and final verification (verified)

- Selected minimal extraction: move Android capability lexical/token preflight from CheckCapabilities into a package-private capability-specific helper. Leave capability semantics/public methods/constants, error class/message, 64 KiB/eight-container-depth bounds, strict lexemes/UTF-16/duplicate checks and materialization order unchanged. Do not generalize or migrate unrelated parsers.
- Establish behavior characterization before extraction, retain Stage3 hostile discovery zero-POST tests and add direct helper boundary tests. Compare the candidate against the frozen Stage4 parser on identical accepted/rejected inputs and errors, not only test counts.
- Keep the current Web label module unchanged: adding a browser module would expand the deliberately closed nine-asset release contract without a correctness benefit in this final bounded stage.
- Preserve wire bytes, state ownership, limits, production asset closure and exports. No framework rewrite, DB, queue, or microservice introduction.
- Avoid broad archive/core rewrites if they add unverified trust-boundary changes; record remaining architectural recommendations separately.
Gates: behavior-equivalence tests, exact asset/archive closure updates if adding modules, independent review, all canonical test/build/lint gates and maintained browser acceptance.

Stage4 accepted overlay: /home/piecer/.hermes/cache/scratch/reveal-stages1-4-verified-yw4lsu6x (75 changed files, manifest verified). Stage5 isolated implementation worktree: /home/piecer/.hermes/cache/scratch/reveal-stage5-preflight-tuwmowol. Parent owns documentation/integration/final gates; worker owns only Android preflight extraction and focused tests, with no commits or parent edits. Stage4 closing-record test/race/vet/build/syntax rerun passed before this stage began.

## Evidence and completion rules

Stage5 implementation integrated: four worker file hashes verified against stage5-manifest.json. Package-private CapabilityJsonPreflight now owns lexical/token admission, CheckCapabilities retains semantics/public API. Worker evidence: baseline characterization GREEN, missing-seam assertion RED, extraction GREEN; exact predecessor/candidate parity across 2,173 entries per variant; Android 340 tests per variant, lint/assemble PASS with existing 28/26 warnings. Independent review deleg_a22d804c PASS: parent verified all 292 reviewed file hashes, predecessor recovery and oracle isolation. Parent make test/race/vet/build/Web syntax/browser/deployment and Android test/lint/assemble passed; parent XML confirms 340 tests per variant with no failures/errors/skips and exact corpus results. Existing lint warnings remain 28 debug/26 release. This closing-record edit is followed by fresh canonical gates. No Web asset/Go wire changes or deployment were introduced.

- A finding is closed only when its regression and integration path pass on the integrated source.
- Saved audit probes are starting evidence, not a replacement for collected repository tests.
- Fixture/JVM/isolated-browser results are not physical-device or deployed-service verification.
- Stage evidence/status are recorded below only after review and verification. Product commits are not created without explicit authorization.

## Historical execution record

- Campaign opened; baseline is unchanged from audit except the existing compose.yaml edit. Stage 1 started.
- Stage 1 implementation integrated: B1 bounded optional enrichment; B3 ordinary later DNS failure retains earlier attempts; B2 healthy path variation and B6 consecutive responsive RTT deltas; B4 measured-only RTT in both Go compact and Web legacy/full; B5 presence-aware GeoIP/ASN decoding/cache. Metadata independent review passed after its legacy-sibling finding was reproduced and repaired. Trace integrated review was pending at this point; its later PASS is recorded below.
- Explicit existing security exception: actual policy rejection keeps the closed no-details result and public 422; prior observations are not exposed or retained as a new internal policy-event contract. This is not presented as universal preservation across rejected requests. Further internal policy-event retention is outside this bounded repair, not silently implemented.
- Real producer full/compact observation witnesses and four trace scenario families are collected by Go and both Web/Android consumers. Parent integrated trace tests observed RED before applying delegated source, then GREEN. Final gates follow all edits/review.
- Stage 1 independent integrated review PASS: /home/piecer/.hermes/reports/revealnetworktroble/2026-09-24/stage1-integrated-review.md. Parent canonical make test (331 Web), test-race, vet, build, web-test-syntax and android-test (299 debug + 299 release) passed. Legacy reviewer reproduction independently GREEN; compose SHA unchanged. No commits or deployment. Closing record followed by canonical rerun; Stage 2 begins only on GREEN.
- Stage 2 FE1/2/3/5 implemented and verified. Independent review found cross-view destroy/recreate orphan rows; parent reproduced RED, repaired owner-safe unmount, added complete/partial × three views and successor tests. Independent closure PASS: stage2-closure-review.md (0 hidden rows / 20 targets;179 scoped tests). Parent make test/vet/build/Web syntax and both Chromium gates PASS. Label browser 20 cases, 20 targets/500 labels reachable in 8 pages, peak1196/chunk90; empty-state browser375/1440 PASS. No deployed-service claim. Stage3 begins after closing-record canonical rerun.

## Final scope and remaining acceptance

- All five bounded remediation stages have implementation, regression and independent-review evidence; no new commit or deployment is claimed. Final source manifest and consolidated report are saved outside the repository.
- Stage5 intentionally extracts only capability admission; broader Web controller/repository, typed backend details and shared archive machinery refactors remain future structural recommendations, not implemented work.
- Actual policy-rejection internal observation retention remains outside scope pending a privacy-safe internal event contract. Public details-free 422/fail-closed behavior is unchanged.
- Actual deployed GUI/API diagnostics, physical Android/OEM chooser, Windows/Firefox/Safari and canonical exact-SHA Docker release acceptance were not performed. Isolated real Go/Chromium CORS and real nginx freshness evidence must not be substituted for them.
- All changes remain unstaged/uncommitted; the original operator API port 8090 is preserved through a shared Compose setting. Existing services, borrowed images and prior artifacts remain untouched by product deployment.
