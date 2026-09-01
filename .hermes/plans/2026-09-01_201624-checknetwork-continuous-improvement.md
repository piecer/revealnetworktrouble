# CheckNetwork Continuous Improvement Implementation Plan

> **For Hermes:** 이 계획은 단계별 전용 worktree/Subagent, strict TDD, exact-SHA 독립 리뷰, 단계당 한 개의 검증된 commit으로 실행한다.

**Goal:** 네트워크 문제를 사용자가 미리 checker를 이해하지 않아도 스스로 인지·식별·파악하고, 근거와 다음 조치를 분석·공유할 수 있는 Web/Android 제품으로 발전시킨다.

**Architecture:** Go 진단 수집 계층과 별도의 결정적 interpretation 계층을 두고 API에는 additive analysis 계약을 제공한다. Web과 Android는 같은 상태/분석 계약을 소비하되 플랫폼별 presentation만 담당한다. 신뢰 경계, 실행 admission, timeout, telemetry coverage를 서버가 명시적으로 소유한다.

**Tech Stack:** Go 1.22 REST API, standard HTML/CSS/JavaScript, Android Java/Gradle, Go testing, Node test runner, browser accessibility/viewport QA.

---

## 1. 기준과 원칙

- 기준 HEAD: `04e352179dd1d3b64f3861fb884e64918a9e29f9`
- 상세 감사: `docs/REVIEW_2026-09-01.md`
- P0는 없으며 P1 resource/security/timeout/TLS 결함을 제품 기능보다 먼저 닫는다.
- 분석 결과는 확률처럼 꾸미지 않는다. `candidate cause`, `evidence`, `confidence/coverage`, `limitations`를 분리한다.
- provider 실패/미관측은 정상이나 benign이 아니라 `unknown`이다.
- Web/Android API 진화는 additive이며 이전 소비자가 새 필드를 무시해도 동작해야 한다.
- 새 동작은 RED → GREEN → REFACTOR 순서로 구현하고 각 단계 commit 전에 canonical gate와 exact-SHA 독립 리뷰를 통과한다.
- 각 단계의 구현 Subagent는 전용 detached worktree에서 predecessor SHA로 시작한다. 후속 단계는 통합된 predecessor SHA에서만 시작한다.
- 사용자가 interrupt하기 전까지 Stage 7 재감사 결과를 다음 계획 revision으로 연결한다.

## 2. 완료 정의

- 사용자는 증상 또는 preset으로 검사를 시작할 수 있다.
- 결과는 단순 PASS/FAIL이 아니라 우선순위 원인 후보, 증거, 한계, 안전한 다음 조치를 제공한다.
- 모든 화면은 stale 결과를 현재 결과로 오인시키지 않는다.
- Web 320/375/400px 및 키보드 흐름, Android 작은 화면/font scale에서 핵심 분석이 사용 가능하다.
- 공개 모드는 내부망 진단 sink를 노출하지 않고, trusted-local 모드는 사설망 진단 목적을 보존한다.
- 최대 유효 요청은 timeout 계약 안에서 완료되거나 명시적으로 거절/취소된다.
- 최대형 데이터는 payload·DOM·외부 호출·cache budget을 넘지 않고 불완전성을 표시한다.
- `make test`, `make test-race`, `make vet`, `make build`, Web tests, Android tests/build가 canonical gate로 실행된다.

## 3. 고정 구현 순서

아래 제목과 순서는 campaign contract이며 바꾸려면 별도 plan revision commit과 독립 리뷰가 필요하다.

## Stage 1: 실행 경계와 HTTPS 판정 복구

**Objective:** P1-01~P1-04를 닫아 이후 기능이 신뢰 가능한 실행 기반 위에서 동작하게 한다.

**Files:**
- Modify: `backend/api/server.go`, `backend/api/server_test.go`
- Modify: `backend/diagnostic/runner.go`, `backend/diagnostic/runner_test.go`
- Modify: `backend/diagnostic/http.go`, `backend/diagnostic/checkers_test.go`
- Modify: `cmd/checknetwork-api/main.go`, `compose.yaml`, `docs/OPERATIONS.md`, `docs/API.md`

**Vertical slices:**
1. RED: report admission 한도 점유/거절/용량 회복 test.
2. GREEN: bounded admission과 `server_busy` + Retry-After 계약.
3. RED: 유효 최대 traceroute budget이 HTTP timeout보다 길다는 config contract test.
4. GREEN: 공유 budget 계산, attempt별 timeout, server timeout 정렬.
5. RED: HTTPS→HTTP redirect downgrade fixture.
6. GREEN: redirect와 최종 TLS 검증, `tls_downgrade` 안정 코드.
7. RED: 기본 bind, Compose host publish, public-mode 인증, 모든 sink의 direct·redirect 사설/loopback/link-local/metadata 접근, redirect 후 DNS 재해석과 IPv4/IPv6 경계 test.
8. GREEN: 기본 `127.0.0.1:8080`, Compose `127.0.0.1:9090:8080`, `trusted-local`과 `public` mode. `public`은 명시적 API key와 per-client rate limit 없이는 시작하지 않고, resolver가 확정한 모든 dial IP를 정책 검사하며 HTTP redirect마다 같은 검사를 반복한다. `trusted-local`만 사설망 진단을 허용한다. CORS는 인증으로 취급하지 않는다.
9. RED/GREEN: API key 누락/오류, rate-limit, 정책 차단이 각각 안정적 401/429/422 code와 privacy-safe log를 반환한다.

**Gate:** focused tests, `make test`, `make test-race`, `make vet`, `make build`, exact-SHA security/concurrency review.

**Commit:** `fix: bound and secure diagnostic execution`

## Stage 2: 설명 가능한 자동 분석 계약

**Objective:** raw result를 원인 후보·증거·권고 조치로 변환하는 결정적 interpretation 계층을 추가한다.

**Files:**
- Create: `backend/diagnostic/analysis.go`, `backend/diagnostic/analysis_test.go`
- Modify: `backend/diagnostic/model.go`, `backend/diagnostic/runner.go`
- Modify: `docs/API.md`, `SPEC.md`

**Contract:**
- `analysis.verdict`: healthy / attention / inconclusive
- `analysis.findings[]`: stable code, severity, category, title, summary, confidence
- `evidence[]`: result index/kind/address, signal, observed value, provenance
- `actions[]`: 안전한 확인 단계, 예상 결과, escalation 조건
- `coverage`: available/missing signals, provider failures, limitations
- Analyzer input은 checker의 임의 `map[string]any`를 직접 순회하지 않는다. 먼저 `Result`를 stable normalized facts(DNS answers, endpoint, HTTP/TLS facts, typed trace attempts/topology)로 변환하고, malformed/legacy details는 panic 없이 `coverage.missing`으로 보낸다.
- `Report.Analysis`는 `omitempty` additive field이며 legacy JSON fixture가 byte-semantic contract를 유지하는지 검증한다.

**Vertical slices:**
1. DNS failure → name-resolution candidate.
2. DNS healthy + TCP failure → transport/service reachability candidate.
3. HTTP unexpected status → application response candidate.
4. TLS expiry/downgrade/handshake evidence → TLS candidate without overclaim.
5. traceroute all/partial unreachable, path instability, latency jump → route candidate.
6. mixed failures → deterministic priority/dedup and explicit inconclusive coverage.
7. healthy fixture → preventive summary, no fabricated cause.

**Gate:** fixture matrix, JSON additive compatibility, race, canonical Go gates, exact-SHA semantic review.

**Commit:** `feat: add explainable network diagnosis`

## Stage 3: Web 요청 lifecycle과 분석 워크스페이스 UI

**Objective:** Sentry 계열의 데이터 밀도 높은 dark diagnostics UI로 “상태 → 원인 후보 → 증거 → 다음 조치”를 첫 화면에서 이해하게 한다.

**Files:**
- Modify: `frontend/index.html`, `frontend/styles.css`, `frontend/app.js`
- Create: `frontend/package.json`, lockfile, `frontend/state.js`, `frontend/state.test.js`, `frontend/dom.test.js`
- Modify: `frontend/app.test.js`, `Makefile`, `docs/TESTING.md`
- Modify: `README.md`, `SPEC.md`

**Test/runtime choice:** Node 내장 test runner와 pinned `jsdom` dev dependency를 사용한다. source substring/eval 검사는 smoke로만 남기고 요청 state와 DOM 전이를 실제 실행한다.

**UI information hierarchy:**
1. compact status header: verdict, report ID, 실행 시각, vantage/coverage.
2. finding cards: severity, candidate cause, confidence/coverage—not probability.
3. evidence drawer/table: 어떤 checker의 어떤 값이 근거인지.
4. action checklist: 안전한 확인 순서와 escalation 조건.
5. raw metrics/topology/map: 분석을 검증하는 secondary workspace.
6. redacted human report export와 raw JSON export 분리.

**Vertical slices:**
1. A-success → B-loading/failure에서 request ID/input signature로 이전 결과를 제거하거나 명시적으로 “이전 실행”으로 표시.
2. A-late → B-success에서 A publish 차단, AbortController, owner-matched busy cleanup, navigation-away cleanup.
3. non-JSON/empty/429/timeout/cancel 오류 normalization과 idle/loading/ready/error/cancelled 상태.
4. analysis fixture를 semantic HTML로 렌더하고 empty/inconclusive 상태를 구분.
5. keyboard accordion/action navigation과 focus restoration; import를 native focusable control로 전환.
6. 320/375/400px single-column reflow, wide graph/table component scroll.
7. AA contrast, double focus ring, reduced motion, live region/busy state.

**Gate:** executable DOM/state tests, HTML/CSS contract tests, Node syntax, browser AX/keyboard/viewport QA when Chrome available, canonical gates, exact-SHA UI review.

**Commit:** `feat: build web diagnosis workspace`

## Stage 4: Web topology 정확성과 대용량 성능

**Objective:** IPv6 집계 오류와 최대 topology UI 정지를 제거하며 end-to-end 불완전성을 숨기지 않는다.

**Files:**
- Modify: `frontend/app.js`, `frontend/styles.css`, Web tests
- Modify: `backend/diagnostic/model.go`, `backend/diagnostic/traceroute.go`, tests, `docs/API.md`

**Chosen backend contract:** 새 persistence/detail endpoint는 만들지 않는다. 요청에 additive `topology_mode: "compact"`를 추가하고 Web은 이를 사용한다. compact 응답은 기존 full mode를 보존하면서 deduplicated graph 최대 500 nodes/1,000 links, 실제 total, displayed, truncated, truncation_reason, omitted counts를 제공한다. 기존 client의 mode 미지정은 full로 유지한다.

**Vertical slices:**
1. IPv6 128-bit prefix normalization.
2. compact topology graph와 deterministic truncation metadata.
3. Web progressive chunk render와 명시적 incomplete notice.
4. maximum fixture에서 response < 1MiB, topology DOM ≤ 1,200 elements, 한 chunk ≤ 100 nodes. wall-clock render time은 환경 변동 때문에 hard gate가 아니라 기록 지표로 남기고, chunk 사이 event-loop yield를 executable test로 검증한다.

**Gate:** lifecycle deferred-response tests, performance probes, keyboard/reflow regression, canonical gates, exact-SHA review.

**Commit:** `fix: make web diagnostics stateful and bounded`

## Stage 5: Android 기능 동등성과 lifecycle 아키텍처

**Objective:** Android에서 공통 analysis 계약과 핵심 검사 capability를 안전하고 복원 가능하게 사용한다.

**Files:**
- Add Gradle Wrapper 8.11.1(`gradle-wrapper.jar` checksum/provenance 문서 포함) and Android test source sets
- Refactor: `android/app/src/main/java/com/checknetwork/app/MainActivity.java`
- Create: API client/model/state/renderer classes and unit tests
- Modify: Android layouts/resources/README, root Makefile/docs

**Test/runtime choice:** 먼저 Android framework 비의존 pure Java transport-model/state reducer를 JVM unit test로 검증한다. Robolectric은 lifecycle/rotation과 view binding에만 pinned dependency로 사용한다. emulator/physical TalkBack은 자동 gate와 분리해 수동 미완료 항목으로 보고한다.

**Vertical slices:**
1. `/checks` capability → 13종 kind/expected status/attempts payload.
2. bounded response parsing and structured API errors.
3. request owner/cancel + rotation/saved state; old callback publish 금지.
4. analysis verdict/findings/evidence/actions native renderer.
5. shareable redacted human report + optional raw JSON.
6. 320dp, large font, TalkBack names, focus/order, progress/error live announcements.
7. debug/release assemble and unit/instrumented smoke gate.

**Gate:** JVM unit tests, Robolectric or equivalent, lint, debug/release build, optional emulator smoke, canonical root gates, exact-SHA Android review.

**Commit:** `feat: bring explainable diagnostics to Android`

## Stage 6: 외부 보강·관측성·운영 성능 강화

**Objective:** 장기 실행과 운영 분석에 필요한 cache/fan-out/telemetry 경계를 닫는다.

**Files:**
- Modify: GeoIP and runner lifecycle code/tests
- Modify: API structured logging/metrics and operations docs
- Modify: compose/deployment examples

**Vertical slices:**
1. bounded TTL/LRU GeoIP cache.
2. duplicate miss single-flight and global provider concurrency.
3. provider failure/freshness/provenance가 coverage에 노출.
4. report ID 연계 submit/admit/reject/start/finish/cancel/duration/payload logs.
5. privacy-safe logging과 redaction; raw query/local address policy.
6. baseline load test budgets and graceful shutdown capacity accounting.

**Gate:** cache/concurrency fault injection, race, load probe, log contract test, canonical gates, exact-SHA operations review.

**Commit:** `feat: harden enrichment and diagnostics observability`

## Stage 7: 교차 플랫폼 재감사와 다음 루프 계획

**Objective:** 새 HEAD에서 제품 목표·보안·성능·UI·Android를 다시 감사하고 다음 bounded campaign을 확정한다.

**Files:**
- Create: 다음 revision의 `docs/REVIEW_*.md`
- Update: 이 plan의 completion matrix 또는 후속 `.hermes/plans/*.md`

**Steps:**
1. clean tracked HEAD export에서 독립 backend/Web/Android/security/product audits.
2. 모든 delayed worker finding을 current HEAD에서 재현·dedupe.
3. canonical gate collection integrity와 exact commit chain 검증.
4. browser 실측이 여전히 불가능하면 명시적 blocker로 남기고 physical Android/TalkBack도 과장하지 않음.
5. 신규 P1/P2와 사용자 가치가 높은 다음 세로 slice를 우선순위화.

**Commit:** `docs: record cross-platform improvement review`

## 4. 후속 릴리스 후보

Stage 7에서 telemetry 타당성을 재검토한 뒤에만 아래를 다음 campaign으로 승격한다.

- 증상 기반 preset과 guided interview.
- incident history, baseline, 변화 전후/회복 검증.
- 여러 vantage agent와 비교 분석.
- opt-in Wi-Fi/VPN/proxy/interface telemetry와 보존·redaction 정책.
- packet loss/jitter/bandwidth처럼 현재 관측 불가능한 신호의 bounded collector.
- 공유 링크/팀 annotation은 인증·보존·삭제 정책과 함께 설계.

## 5. 단계별 검증/커밋 규율

각 Stage에 대해 다음 순서를 고정한다.

1. predecessor SHA와 clean status 기록.
2. detached worktree 생성, owned-file manifest 고정.
3. failing behavioral test 실행 및 예상 RED 확인.
4. 최소 구현과 focused GREEN.
5. stage canonical gates와 `git diff --check`.
6. stage commit 생성.
7. 독립 reviewer가 exact SHA의 scope/spec/security/logic을 검토.
8. finding이 있으면 같은 stage commit amend 후 새 SHA 재검토·fresh gates.
9. main에 순서대로 fast-forward/cherry-pick하고 status 확인.
10. 다음 Stage는 통합된 SHA에서 시작.

## 6. 위험과 trade-off

- 내부망 접근 차단은 제품 목적과 충돌하므로 deployment mode를 분리한다.
- 원인 추론은 관측 경계를 넘지 않으며 confidence와 coverage를 별도 표시한다.
- 대용량 topology의 상세를 제한할 때 원본 수집과 표시 수를 분리하고 truncation을 숨기지 않는다.
- Android 리팩터링은 새 외부 library를 최소화하되 테스트 가능성을 위해 transport/state 분리를 우선한다.
- 시각 QA는 현재 Chrome 부재로 blocked이며 browser가 준비되는 즉시 Stage 3/4의 미완료 gate로 실행한다.
