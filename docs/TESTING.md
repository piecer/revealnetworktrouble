# 테스트 및 회귀 기준

## 로컬 검증

```bash
npm --prefix frontend ci
make test                 # Go/Web + CI 정책; Docker·외부망·Android 불필요(의존성 사전 설치)
make test-integration     # pure Compose contract + 실제 Compose CLI 교차 검증; daemon·컨테이너 시작 없음
make web-test-syntax      # 모든 Web JavaScript 구문 검사
make test-race
make vet
GOFLAGS=-buildvcs=false make build
make android-wrapper-verify # 고정 Gradle wrapper/JAR/distribution checksum
make android-env-bootstrap-test # Make의 JDK/SDK 탐색·override 전달 회귀 검사
make android-env            # JDK 17, Android platform 35/build-tools 35.0.0
make android-test           # debug/release JVM tests + nonzero collection
make android-lint
make android-assemble       # debug + minified release
# 전체 로컬 CI gate: make ci
go test ./cmd/checknetwork-api -run 'Test.*(ConnectionAdmission|Ready|Release|Operations|Identity)'
go test ./backend/api ./backend/diagnostic -run 'Test.*(Body|Deadline|HTTP|Telemetry|LatencyDelta)'
go test ./... -run 'TestDocumentationContract|Test.*(ExpectedStatus|Greeting|TLS|Trace.*Independent|APIError|PresentationContract|Compact.*Geo|Shutdown)'
git diff --check
```

`make`는 `JAVA_HOME`이 undefined일 때 system `PATH`의 executable `javac`를 시작점으로 portable JAVA_HOME을 자동 유도한다. Plain `readlink <path>`만 사용해 symlink를 최대 40 hops까지 순회하고 lexical `.`/`..`를 정규화한다. Absolute/relative/chained target과 공백·shell metacharacter path를 literal하게 보존하며 cycle, 40 hops 초과, dangling 또는 non-executable terminal target은 빈 derived value로 fail closed한다. `readlink -f`나 `realpath`에는 의존하지 않는다. 유도된 home은 후속 `bin/java` 검사에서 system JDK 17이어야 한다. 반면 caller가 명시한 빈/잘못된 `JAVA_HOME`도 자동 탐색으로 덮어쓰거나 fallback하지 않고 그대로 strict 검사에서 실패한다. SDK precedence는 명시 `ANDROID_HOME` > 명시 `ANDROID_SDK_ROOT` > valid `~/Android/Sdk` > valid `~/.local/share/checknetwork-android/sdk`다. 첫 explicit override가 빈/잘못된 값이거나 두 explicit 값이 충돌해도 `ANDROID_HOME`을 우선해 두 SDK 변수에 exact value를 export하며 다음 후보로 fallback하지 않고 platform 35/build-tools 35.0.0 검사에서 fail closed한다.

`make ci-inner`는 fresh gate로 `npm ci`, uncached Go tests, 명시적 Compose config 통합 검사(`make test-integration`), Web tests/syntax, Go race/vet/build, Android wrapper/env bootstrap/env/debug+release tests/lint/assemble를 한 번씩 수행한다. `make ci`는 이를 직접 호출하며 재귀 archive를 만들지 않는다. `make ci-clean-archive`는 **commit 후 clean exact HEAD SHA에서만** 실행하는 외부 acceptance다. 현재 uncommitted tree에서 실행하면 의도적으로 실패하므로 commit 전에 실행하지 않는다. 두 clean-archive 스크립트는 work 전에 스스로 선언한 tool prerequisite set(`required_tools`)을 검사하고 부재하면 tool 이름을 지정한 actionable error로 실패한다. fixture는 선언된 tool만으로 구성한 PATH에서 실행된다 — undeclared binary 의존이 없음을 이 실행으로 증명한다(`TestCleanArchiveContractDeclaresAndChecksItsToolPrerequisites`). harness는 `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`로 toolchain download을 시도하지 않는다. `make verify-release`와 `make release`도 clean exact current HEAD 및 Docker CLI+daemon을 필수로 요구하며 Docker 부재를 skip으로 처리하지 않는다. Exact workflow gate인 `make verify-release-real REVISION=<exact-40-character-HEAD-SHA>`은 production `scripts/verify-release.sh`를 umask 077/027/000으로 실행한다. 세 status 0, exact two success lines의 11 digest field equality, private `0700` temp/`0600` stderr captures, canonical Web source modes `0644`, canonical inspect inventory equality와 release label zero를 요구한다. Fake mutation coverage는 uptime/status와 map-order noise를 무시하되 image/container replacement, config/network label, attachment/membership 변화를 검출한다. 이 gate는 manifest-derived clean temporary candidate에서만 나중에 실행한다. `make verify-api-archive-real`, `make verify-web-archive-real`, `make verify-archives-real`은 opt-in actual Buildx **component** regressions이며 exact production release workflow gate를 대신하지 않는다.

Historical record: Compose SemVer regression fix 뒤의 source inventory reading은 live 문서에 고정하지 않고 archived record([implementation history](archive/IMPLEMENTATION-HISTORY.md))가 과거 values를 보존한다. current cardinality는 gate 실행으로 관측한다. Producer contract cardinality는 contract test가 단언하는 fixture 값만 live 문서에 남긴다. 최신 parent focused gates와 Task 1~15 task-level reviews는 blocker 0 / major 0이지만 documentation edit 뒤 `env -u JAVA_HOME -u ANDROID_HOME -u ANDROID_SDK_ROOT make ci`는 아직 pending이다. Prior current-byte CI/count evidence는 final evidence가 아니다. Repository root의 ignored `checknetwork-api` binary는 candidate manifest/검증에서 제외되어 있으나 비파괴 요청 때문에 제거하지 않고 retained 상태다. New manifest/temp release/five-way review/commit/exact-SHA closure도 pending이며 이전 임시 manifest version/record count/hash는 current evidence가 아니다.

Route-visual slice와 Geo/shared-prefix source inventory의 과거 reading은 live 문서에 고정하지 않고 archived record([implementation history](archive/IMPLEMENTATION-HISTORY.md))에만 있다. production Web closure는 self-contained `geo-map.js`와 `common-prefix.js`를 포함한다. ASN context는 presentation-only inference이며 private-IP ownership나 Geo enrichment가 아니다. See [ASN context evidence](ASN-CONTEXT.md). Independent review/release remains pending. See [Geo map evidence and reproduction](GEO-MAP.md) for the separate actual API, saved replay, no-Geo and pixel-seam browser gates.

프런트엔드는 ESM과 Node 내장 test runner를 사용하고 `jsdom`은 `package.json`과 lockfile에 고정한다. state/model/renderer/DOM tests가 schema, planning, progressive ownership과 실제 DOM event를 실행한다.

## 테스트 계층

### Compose 구성 해석 계약

`compose.yaml` 계약은 기본 Go suite에서 순수 Go resolver로 강제한다: single-port default, loopback-only host publication, opt-in `standalone-web` profile, documented environment와 shared build args, exec-form healthcheck. 기본 suite는 Docker CLI를 호출하지 않는다. 회귀 검사는 PATH에 기록용 fake docker만 놓고(다른 실행 파일 없이) 실행해 skip도 CLI 호출도 없음을 확인한다(`TestComposeContractIsEnforcedWithoutDocker`).
canonical SemVer ERE 판정도 pure Go로 한다: POSIX ERE와 semantics가 같은 construct만 허용하고 그 밖은 fail-closed로 실패한다. `grep -E`는 존재할 때만 같은 corpus로 교차 검증한다(`TestCanonicalERECrossChecksPOSIXGrepWhenPresent`) — 부재는 이 교차 검증만 skip하고 enforce된 판정 자체는 이미 pure Go로 실행된다.
resolver가 정확히 읽을 수 없는 구성(anchors, merge keys, tags, multi-document, block scalar, flow mapping, tab 들여쓰기, CRLF, compact mapping, duplicate key)은 다른 값으로 조용히 해석되지 않고 gate가 실패한다.
실제 Compose CLI는 Docker가 존재할 때만 같은 계약을 교차 검증한다(`make test-integration`; `--env-file /dev/null`로 caller `.env`를 discover하지 않는다). CLI가 없으면 교차 검증만 skip되고 pure contract는 계속 실행된다. 요청한 opt-in gate의 CLI 부재는 actionable error로 실패한다. 두 경로 모두 구성만 resolve하며 daemon·컨테이너 시작·image build가 없다.

### 선택적 통합 Chromium gate

`PLAYWRIGHT_PATH=/path/to/playwright make web-test-browser`는 graph-first, route-visual(상호작용 포함), 라벨 최대 입력, empty-state replay와 실제 Go-handler CORS 검사를 실행한다. Playwright 및 Chromium은 별도 설치가 필요하며 부재는 이 opt-in target의 실패다. 기본 `make test`는 브라우저 설치를 요구하지 않는다. `BROWSER_OUTPUT_DIR`를 지정하면 그 아래 고유 증거 디렉터리를 만들고, 생략하면 `TMPDIR`/OS temporary directory 아래에 만든다. 기존 증거를 덮어쓰지 않는다.

Graph-first는 latency fill과 route-colored link의 실제 pixels를 확인한다. 모든 화면 크기·모드에서 draw-state/semantic DOM/recorded commands를 그대로 둔 채 pixels만 지우는 negative canary가 같은 oracle에서 실패해야 한다. Fixture replay는 배포 서비스 또는 실제 네트워크 진단의 증거가 아니다.

Browser wrapper는 120초 deadline을 넘으면 SIGTERM 전 timeout failure를 기록하고, cleanup이 exit 0으로 끝나도 PASS를 허용하지 않는다. 2초 내 종료하지 않으면 SIGKILL로 승격한다. 기본 Web suite는 정상 종료·nonzero·spawn error·graceful-zero timeout·강제 종료를 검증한다.

`PLAYWRIGHT_PATH=/path/to/playwright make web-test-cors-browser`는 CORS 부분만 실행한다. Go가 소유한 ephemeral loopback UI/API origins에서 interception 없이 native preflight/fetch와 production Web parser를 실행한다. 실제 429/503의 `Retry-After` 노출·`retryAt` 계산, 별도 거부 Origin 차단을 검사한다. 이것도 외부 배포나 프록시의 CORS 설정 검증을 대신하지 않는다.

### 선택적 내장 UI 실제 바이너리 gate

`PLAYWRIGHT_PATH=/path/to/playwright make web-test-embedded-browser`는 바이너리를 빌드하고 별도 작업 디렉터리에서 ephemeral loopback listener로 시작한다. Python/static 서버 없이 동일 출처로 실제 Go API와 소유한 TCP fixture를 검사하고, public 모드의 UI 공개·API 401/인증 후 200을 확인한다. 1440/375 px의 topology 2D/3D와 Geo는 producer fixture replay로 명시한다. 정상 화면 동작의 CSP 위반/스크립트 오류 없음, 같은 origin 요청, 고의 inline script·외부 origin fetch 차단도 검사한다. 실제 외부 경로 측정·운영 HTTPS·다른 기기의 DNS/CA 검증은 아니다.

`BROWSER_OUTPUT_DIR` 아래 고유 디렉터리에 JSON receipt·로그·스크린샷을 보관한다. 대상 바이너리 외 Web 파일을 런타임 작업 디렉터리에 복사하지 않고, 소유한 API process만 종료해 정상 exit를 확인한다. Playwright/Chromium은 선택적 외부 의존성이고 기본 Go/Web suite에는 요구하지 않는다. 기본 Go suite에는 내장 파일 허용 목록·MIME/HEAD/no-store·보안 헤더·API/probe pass-through와 실제 시작 경로의 on/off/public 회귀가 포함된다.

### 배포 설정과 static freshness

`make web-test-deployment`는 Docker Compose CLI의 실제 config resolution, post-COPY Dockerfile recipe, jsdom `createApp` request path, malformed port rejection, canonical nine-asset bytes/manifest/mtime 및 harness ownership tests를 실행한다. Docker daemon이나 image build는 사용하지 않는다. Deployment JS tests는 기본 Web suite에도 수집되지만 Compose CLI가 없으면 config-resolution case만 skip한다. 명시적 deployment target에서는 CLI 부재가 실패다. 이 skip은 compose 계약 자체를 미검증으로 만들지 않는다: default single-port, opt-in profile과 loopback-only publication은 같은 suite의 Go pure resolver gate에서 CLI와 무관하게 실행된다. Ownership Python tests는 Go canonical suite에서도 실행한다.

실제 nginx gate는 `make web-test-deployment-live DEPLOYMENT_RECEIPTS=/absolute/new/evidence-directory`로 별도 실행한다. Docker daemon과 이미 캐시된 pinned nginx base가 필요하며 기존 디렉터리를 증거 경로로 재사용하지 않는다. 자체 nonce-owned container/ephemeral loopback port만 만들고 read-only config/assets를 mount한다. 동일 epoch와 길이의 두 세대에 대한 18 conditional responses가 모두 새 bytes의 200/no-store여야 하며 exact container ID/name/owner 재확인 후 제거·absence readback을 요구한다. Borrowed image와 기존 서비스는 변경하지 않는다. 이 gate는 실제 nginx 응답 검증이지 production deployment나 두 release build의 재현성 검증은 아니다.

Ownership regression은 signal unmask 도중과 직후의 HUP/INT/TERM을 구분한다. 직후 schedule은 실제 Python process signal과 stateful fake Docker를 사용해 deterministic exit, 성공한 create receipt, exact-ID removal 및 ID/name absence readback을 검증하며 실제 daemon에는 호출하지 않는다.

### 관측 보존과 Web 데이터 접근 회귀

`make test`는 Go 실제 producer/handler에서 생성한 full·compact 관측 fixture의 exact bytes, Web RTT presence와 topology empty-state, 라벨 기존 key 보존·최대 입력 pagination·textarea 구분자 회귀를 수집한다. `make android-test`도 동일 관측 fixture를 읽는다. 라벨 회귀는 startup 작업을 끝낸 뒤 1/4/20 targets × 0/100/500 labels의 모든 페이지·수정·삭제를 확인한다.

다음 Chromium gate는 자체 ephemeral loopback 정적 서버만 소유한다. 별도 Playwright 설치와 Chromium이 필요하며, API fixture replay 또는 intercepted request payload를 검증한다. 실제 배포나 외부 네트워크 진단 검증을 대신하지 않는다.

```bash
PLAYWRIGHT_PATH=/path/to/playwright node frontend/label-integrity.browser.cjs OUTPUT_LABELS
PLAYWRIGHT_PATH=/path/to/playwright node frontend/topology-empty.browser.cjs OUTPUT_EMPTY
```

첫 gate는 native JSON/CSV import, localStorage readback, 모든 label page와 입력 admission을 검사한다. 두 번째는 실제 Go HTTP 경로에서 생성된 unavailable 응답을 375/1440 px 화면에서 재생하고 선택 해제·재선택·다운로드 활성 상태·DOM/overflow를 검사한다. 증거 JSON과 screenshot은 지정한 출력 디렉터리에 저장한다.

### Android capability preflight 분리 회귀

`CheckCapabilitiesPreflightTest`는 test-only `FrozenCheckCapabilities`에 보존한 분리 직전 구현과 현재 parser를 같은 deterministic corpus에 통과시킨다. 각 variant에서 2,173 entries의 acceptance, exact exception class/message, capability sets/limits와 accepted entry별 90 request-validation projections를 비교한다. Corpus entries는 unique-document 수가 아니다. Frozen class는 production helper에 위임하지 않으며 의도한 계약 변경 없이 새 구현에 맞춰 갱신하지 않는다. 별도 직접 helper tests는 UTF-8 byte exact/+1, object root, depth 8/9, decoded duplicate keys, unknown nested values, strict lexemes 및 malformed UTF-16을 검증한다. 기존 discovery 실패 시 zero POST와 Activity/session 회귀는 그대로 수집한다. 모두 `make android-test`의 debug/release JVM/Robolectric 검증이며 실제 기기 acceptance를 대신하지 않는다.

### Rich topology replay

`frontend/rich-topology.browser.cjs` uses the same external Playwright installation and private static URL as the graph-first gate. It replays the linked fixture and optional captured REPORT log at 1440/375 px, checks real Canvas glyph rectangles in both modes (also after drag/edit/reload), attached curves, unchanged other nodes, inert tooltip/editor text, saved alias/note persistence, stale-handler cleanup, document budget and overflow. No live network diagnostic success or deployed-image acceptance is implied.

Example: `PLAYWRIGHT_PATH=/path/to/playwright node frontend/rich-topology.browser.cjs http://127.0.0.1:18763 /tmp/rich-topology-evidence [REPORT_LOG]` (omit the optional final argument if unavailable).

Labels use nine bounded vertical candidates and conservative 160 px maximum-width rectangles with saved aliases first. Crowding hides visual text only, not nodes, links or inspector/tooltip facts. Dense mobile graphs still require zoom/filter/hover; label-to-node proximity and edge/node occlusion are not solved by label-label collision avoidance. The fixed tooltip increases the maximum-report baseline from historical 711 to 712 elements and measured navigation peak from historical 1,151 to 1,152.

### Graph-first browser replay

Historical Stage 9 record: 회귀 추가 전후의 Node inventory reading은 archived record([implementation history](archive/IMPLEMENTATION-HISTORY.md))에만 있고 app contract script는 별도로 실행한다. 기존 Stage 8/9 historical 수치는 archived record로 유지한다.

`frontend/graph-first.browser.cjs`는 별도 설치된 Playwright Chromium으로 committed singleton/linked fixtures와 선택적인 captured `REPORT ` log를 replay한다. Production dependency나 실제 network probe를 추가하지 않는다.

```bash
# 별도 terminal에서 이 worktree의 frontend만 private loopback server로 제공
python3 -m http.server 18763 --bind 127.0.0.1 --directory frontend
# 다른 terminal: OUTPUT_DIR와 optional CAPTURED_REPORT_LOG는 caller가 지정
PLAYWRIGHT_PATH=/path/to/playwright node frontend/graph-first.browser.cjs \
  http://127.0.0.1:18763 OUTPUT_DIR CAPTURED_REPORT_LOG
```

1440/375 px × 2D/3D에서 screenshots, 실제 link midpoint 색상 pixels, visible Canvas, 기본 닫힌 inspector, keyboard Enter 펼침, DOM≤1,200, horizontal overflow 없음, mode/reset 추가 report request 0을 검사한다. 초기 fit은 두 모드의 실제 projected finite bounds에서 계산하며 node/link 데이터는 바꾸지 않는다. Desktop target checkbox overflow는 label-relative 1px input으로 제한하고 전역 overflow 숨김으로 덮지 않는다. Dense multi-route labels는 좁은 화면에서 여전히 겹칠 수 있어 zoom/filter/inspector를 사용한다. 이 isolated Chromium replay는 physical devices, Firefox/Safari, 실제 screen reader나 live deployment 검증을 대체하지 않는다.

- 단위: 입력 정규화, 상태 집계, checker 오류 분류
- 통합: `httptest` 기반 API 요청·응답과 CORS
- 회귀: 성공과 일부 실패가 섞인 리포트, 잘못된 JSON, 과도한 대상 수
- 분석 계약: typed/legacy details 정규화, deterministic finding/evidence/action, confidence/coverage 분리, malformed 입력의 inconclusive 처리
- 분석 cardinality: Go가 생성·직렬화한 최대 HTTPS fixture를 Web `normalizeReport`와 Android `ReportParser`에 직접 통과시키고, fixture의 finding/evidence/action/available counts는 gate가 pin하고 문서에는 적지 않는다. 64/64/64 및 coverage 각 128의 cap+1은 양쪽에서 거부
- 생산자 정확성: traceroute 부분 출력과 command error 동시 발생, attempt별 timeout, 전체 latency
- topology link schema: optional `latency_delta_ms`의 finite/nonnegative/30000 cap, RTT 감소 시 zero clamp+omit, Android의 `latency_ms` rejection
- 런타임 경계: traceroute 30-hop/256 KiB parser 경계, 실제 subprocess overflow 취소·prefix 상한·process-group pipe 정리, Unix/Windows compile
- Windows traceroute: `tracert.exe` command spec과 세 probe parser fixture, 386/amd64/arm64 compile을 검증한다. 명령은 `CREATE_SUSPENDED`로 시작하고 `KILL_ON_JOB_CLOSE` Job Object에 할당한 뒤 primary thread를 재개한다.
- API 직렬화: non-finite 값과 실패 Marshaler가 부분 200을 만들지 않고 안정적인 `response_serialization_failed` 500을 반환
- Compact API: full 호환, transport pruning, 500/1,000, 1 MiB exclusive boundary, Geo/transaction rollback, deterministic bytes와 통계·참조 무결성
- Compact 성능: 최대 Geo fixture의 logarithmic marshal/build 횟수, linear reference와 byte-identical 최대 보존, raw/cache 불변
- 프런트엔드 model/renderer: strict schema, malformed no-fallback, bounded legacy canonicalization, fair prefix, owner-safe chunk≤100/document≤1,200, active-view unmount와 Canvas cleanup
- Web topology visualizer: Stage 8의 semantic single-element/no-visual-layer 회귀를 고정하고, 같은 facts에 대한 default 2D/optional 3D Canvas projection, no-refetch/no-diagnosis-change, pointer/wheel/keyboard/reset/fullscreen interaction과 no-continuous-animation redraw를 검증한다. Nodes 500/links 1,000/routes 1,000/DPR≤4, semantic inspector/context-failure fallback, global DOM≤1,200(현재 최대 fixture 1,152)을 함께 검사한다.
- Web state: 독립 lane, owner/signature, stale success/finalize 차단, invalidation/cancel, error/Retry-After, ready-only report와 credential-free signature
- Web DOM/jsdom: A/B stale ownership, 입력 변경·navigation·`beforeunload`·timeout abort, 초기 deep-link focus, same-lane stale alert cleanup, ready-only export와 session credential 격리
- Web target admission: `MAX_TARGETS=20`, remove/re-add/HMR ownership, hidden retained view를 포함한 global `MAX_DOCUMENT_ELEMENTS=1200`, exact-fit 20번째 허용과 cap+1 pre-append atomic rejection을 검증한다.
- Web response/schema: 8 MiB `Content-Length`/stream/multibyte/cancel-failure, 누적 topology/string/container budget, 정상 최대 traceroute 수용, hostile multiplicative report 거부, non-plain/accessor 거부
- Web 보안/계층: reflected Bearer 비저장, 대소문자·겹침·report ID human redaction, XSS inert DOM, legacy analysis, compact header→analysis→folded raw 순서
- Web label/lifecycle: import 1 MiB/500 stored records/256 label/1024 note, 기존 key 보존과 정확한 omissions, 남은 document budget에 따른 최대 100-row pagination, active-view-only mount, A/B 격리, destroy/HMR와 timer/scheduler/import/fullscreen late-completion faults
- Web 접근성/반응형: concise live/alert/busy, native import, roving topology focus, Canvas accessible list, caption/scope 표, fullscreen focus, 320/375/400 reflow, reduced motion
- Android JVM/Robolectric: request/report/error/state contracts, debug/release network policy, lifecycle/resource/accessibility contracts. Evidence는 각 result directory의 direct-child `TEST-*.xml`만 bounded XML parser로 읽어 root `testsuite`의 canonical non-negative `tests`를 정확히 한 번 합산한다. 모든 direct XML은 well-formed root suite, nonzero tests, `errors=0`, `failures=0`이어야 하며 symlink/special/non-XML entry, unexpected/nested directory, duplicate/ambiguous basename을 거부한다. Debug에는 exact `TEST-com.checknetwork.app.DebugNetworkSecurityContractTest.xml` root/class만 하나 필요하고 release class를 금지하며 release는 정확히 반대다. Expected suite의 testcase count와 classname도 root tests와 정확히 일치해야 하므로 generic totals, nested suite, filename/class substring 또는 copied opposite-variant result는 evidence가 아니다.
- Gradle가 같은 result directory에 만드는 유일한 예외 companion은 exact `binary/` direct directory다. 그 안에는 regular non-symlink `output.bin`, `output.bin.idx`, `results.bin` 세 파일만 정확히 있어야 하며 nested directory/XML/extra/missing/duplicate/special entry를 거부한다. 각 파일은 ≤64 MiB, 세 파일 합계는 ≤128 MiB, depth는 1로 제한한다. 이 binary metadata는 layout compatibility용 bounded **non-evidence companion**이고 XML test total이나 variant-canary count에 절대 더하지 않는다.
- Stage 8 historical source inventory는 각 variant direct-child XML 297 / variant-contract 2다. 이 `297+2`는 permanent protocol constant나 verifier acceptance threshold가 아니며 documentation edit 뒤 pending clean-environment canonical gate가 actual direct-child XML count를 다시 기록해야 한다. 구조적 계약은 nonzero total/variant evidence를 요구한다.
- Android Stage 7: checker panic/capacity finding의 exhaustive export labels, discovery+report 단일 315초 `OperationDeadline`, discovery `min(10s, remaining)` 및 report local intersection, clock regression/cancel/zero-remaining, typed `UNSUPPORTED_CAPABILITY` 5 reasons와 fixed UI, malformed `INVALID_RESPONSE` 분리를 검증한다.
- Android architecture-remediation regressions: `DiagnosticsSessionProductionTest`가 실제 single worker의 ERROR commit → delayed dispatch → input invalidation/IDLE 순서를 재현하고 stale delivery 차단을 확인한다. `CheckCapabilitiesTest`는 duplicate decoded names, unknown nested fields, strict string/number/keyword grammar, malformed UTF-16, depth 8/9, UTF-8 64 KiB exact/+1을 검사하며 production discovery의 malformed body는 `INVALID_RESPONSE`와 report POST 0회를 검증한다.
- `RawShareActivityTest`는 chooser/materialization 실패, observed lease expiry, GRANTED replacement, 경고 취소/중복/회전, cleanup retry 버튼의 중복 클릭과 반복 실패/회복을 실제 Activity에서 검증한다. `FormProjectionActivityTest`는 spinner 변경 및 저장/재생성 후 production transport가 작성한 POST body에 current-kind 옵션만 들어가고 inactive draft는 보존됨을 검사한다. 외부 인터넷이나 physical-device acceptance를 대신하지 않는다.
- Android build: Gradle Wrapper checksum, JDK/SDK 환경, debug/release lint와 assemble을 독립 gate로 실행한다.
- pre-handler/body admission: connection default 128/hard 256/env validation, 256 incomplete-header bounded/recovery probe, immediate-close overflow와 interruptible backoff, body effective-report default/hard 64/env, auth/rate 뒤 acquisition, declared oversize preflight, decode-only lifetime, fixed 503/recovery를 검증한다.
- checker/HTTP truth: exact completion timestamp의 parent/supervisor/report/check deadline arbitration과 no-late-healthy, 32 KiB bounded body observation, timeout/cancel 및 `response_read_failed`를 검증한다.
- 반복 샘플링 window: target window가 `timeout × samples`를 담는지, 요청 deadline이 최대 valid request의 `timeout × max(attempts, samples)`를 담는지 behavior gate로 확인한다. window가 samples를 못 담으면 aggregate가 observation이 아니라 budget artifact로 채워지므로 gate가 실패해야 한다. samples 없는 대상은 timeout 하나로 control되므로 계산이 모든 window를 부풀리지 않는지도 같은 suite로 확인한다(`TestSampledTargetWindowCanHoldEverySample`, `TestUnsampledTargetWindowIsOneTimeout`, `TestRequestBudgetCoversRepeatedSamples`).
- operational probes: exact GET/HEAD path/method, public unauth/no-rate repeated `/livez`/`/readyz`, fixed startup/ready/drain/traceroute-unavailable bodies, loopback-only startup capability probe와 temporary saturation no-flap, outer business pass-through, exact route→auth→rate→`server_draining`→body/report/checker ordering을 검증한다. Unix fake executable로 trusted PATH resolution, `-n` rejection 뒤 compatible grammar selection, all-reject nil capability fail-closed, 전체 2초 timeout, 32 KiB overflow, selected execution parity와 no runtime fallback을 검증한다. Web/Android는 exact fixed `Traceroute capability was unavailable, so no route observation was established.`를 표시하고 raw server prose/target을 반사하지 않는다. GNU inetutils가 설치된 host에서는 live loopback readiness+execution을 수행한다. Linux common/iputils, Alpine BusyBox 및 Darwin numeric grammar는 platform command contract로 유지한다.
- telemetry: delivery-only outcome, valid complete-write `report_finish`의 5 diagnostic fields, enum/count/status 관계 및 20/40 bounds, malformed tuple extension omission+base retention, privacy canary와 write/serialization/capacity paths를 검증한다.
- 운영 config: connection 1/256 accepted 및 257 rejected, body 1/64 accepted 및 65 rejected, report 16 accepted/17 rejected, checker 1/1,024 accepted 및 1,025 rejected를 확인한다. `H=5s`, `B=30s`, `E=301s`, `R=5s`, `D=5s`와 HTTP/1 식(35/336/346초), 64 KiB header를 exact field로 검증한다.
- 배포 contract: Compose 6분 stop, CPU/memory/PID, `/readyz` API health dependency, nginx worker 2, API non-root image를 source contract와 `docker compose config`로 검증한다. resolved API/Web 기본 build args가 exact 개발 sentinel `VERSION=0.1.0-dev`, `REVISION=0000000000000000000000000000000000000000`, `SOURCE_DATE_EPOCH=0`으로 같고 frontend Dockerfile의 SemVer/40-lowercase-hex validator를 통과하며, 명시 override가 verbatim 공유되는지도 executable regression으로 검증한다. zero revision은 개발 전용이며 release provenance가 아니다. Docker daemon이 있으면 개발 API/Web image build와 health smoke도 수행한다.
- release contract: `VERSION=0.1.0`, exact health/startup/OCI identity, digest-pinned bases, Go 1.26.11, checksum-pinned traceroute APK, clean exact SHA/commit epoch/canonical `git archive`, API/Web tagless archive double-build byte equality와 offline archive/config/manifest/rootfs/selected-file determinism을 검증한다. Docker는 mandatory이며 unavailable이면 실패한다.
- Stage 8 shutdown: HTTP drain 성공/실패와 noncooperative checker를 조합해 terminal exit 1, 한 ERROR, no completed INFO, one atomic `active=stuck=remaining=1`, later release 후 accounting 0을 검증한다.
- request/status: omitted/0 `expected_status`를 13 kind 전체에서 수용하고 explicit 100/599 HTTP(S), 99/600 및 non-HTTP nonzero pre-admission rejection, redirect의 final response status를 검증한다.
- service greeting: zero client writes, exact aggregate 4,096/+1, 8 lines/+1, CRLF 포함 512/+1, SSH 255/+1, SMTP continuation/terminal, IMAP/POP3 delimiters, EOF/read/cancel과 exact `server_greeting` scope/result shape를 검증한다.
- TLS/traceroute: typed expired exact NotAfter boundary, not-yet-valid, hostname, untrusted, wrapped causes, generic prose fallback와 validity fields를 검증한다. Failed traceroute attempt의 parseable topology는 full raw attempts에만 남고 compact route/node/link/stat 및 Geo aggregate에서는 제외된다. 별도 completed route가 있는 full timeout/command-failure는 optional representative topology를 유지할 수 있고 generic runner-owned cancelled는 13 kind 모두 closed aggregate block을 금지하고, traceroute의 attempt aggregate은 kind 자체의 block이므로 attempt이 실제로 실행된 cancellation에서는 허용된다(producer-witnessed mixed scenario가 그것을 carry한다). reached/unreached/execution-failed 혼합에서 execution finding과 independent partial/path findings 및 order-independent top-level code도 검증한다.
- `testdata/report-schema.json`은 model JSON tag과 `producerResultShapeRegistry`에서 generator가 produce하는 machine-readable single source이고 wire structure만 표현한다. canonical spelling, block 내부 key set, threshold semantics처럼 JSON Schema가 표현할 수 없는 relationship은 producer fixture와 mutation corpus가 owner로 유지되고 generator own notes가 그것을 명시한다. gate는 committed bytes를 generator output과 byte-for-byte 비교해 hand-edit를 거부하고, producer-witnessed report이 registered shape set에서 정확히 한 branch와 match되지 않으면 실패한다. branch가 알지 못하게 된 registered detail key를 지운 뒤 그 report이 여전히 validate되면 gate가 실패해야 하고(presence rule이 아니라 schema itself가 단언해야 한다), unknown key는 registered set 밖이므로 거부되어야 한다. registered optional key를 `required`에서도 함께 지운 mutation probe가 그 구분의 원인임을 proves. traceroute attempt aggregate은 closed aggregate block이 아니라 kind 자체의 block이므로 generic shape에서는 attempt이 실제로 실행된 outcome에서만 allowed다. generator가 shape registry가 실제로 쓸 수 있는 형태로 shape을 등록하지 않거나 multi-kind shape를 첫 kind에 pin하면 consumer가 잘못된 값에 pin되므로 gate가 그것을 거부해야 한다. gate 이름: `TestProducerWitnessedReportsValidateAgainstCommittedSchema`, `TestReportSchemaRejectsDetailKeyOutsideRegisteredShape`, `TestReportSchemaEnforcesItsOwnRegisteredOptionalKeys`, `TestCommittedReportSchemaMatchesGeneratedRegistry`, `TestReportSchemaFreshnessRejectsOneByteMutation`.
- closed producer fixtures: `finding-contract.json`의 findings/result shapes와 expanded `(kind,status,error_code,details)` matrix cardinality를 `ProducerFindingContract()` source와 source-generated bytes 모두에 비교해 강제한다. 그 cardinality는 gate가 pin하고 이 문서에는 수치를 적지 않는다. Web/Android 양쪽은 그 exact producer matrix와 timeout/command-failure/failed-reached-parse/mixed-completed-failed full+compact의 eight witness fixtures를 소비한다. Web의 fixture-derived corpus는 semantic mutation 전체와 generic cancelled-detail rejection 케이스를 포함한다(cardinality는 gate가 pin한다). healthy+nonempty `error_code` 또는 알려진 값의 contradictory tuple/details는 whole-report 단위로 publication 전에 거부해 fixed local invalid response만 남긴다. 별도 `presentation-contract.json`의 cardinality(evidence signals 포함)와 scenario sources가 producer pipeline을 통해 만든 references를 비교한다. `resolver_comparison` block은 current version에서 checker가 write하지 않으므로 fixture scenario로만 reachable하다. result shape의 optional_details block list는 kinds 전체의 union이며, 하나의 result가 담을 수 있는 block은 그 kind와 outcome이 producer로서 만들 수 있는 projection만이다. producer gate(`closedBlockLegalFor`)와 Web gate(`aggregateKeysForKind`)가 같은 per-kind rule을 pin하고, union-only shape(traceroute을 포함한 cancelled/timeout/connection_failed/invalid_address)에서 per-kind rule이 union을 거부하는 케이스를 test가 직접 단언한다. API error fixture의 rows와 structural mutations도 같은 방식으로 test가 강제한다. fixture를 자동 regenerate하지 않고 drift 시 test가 실패한다.
- strict compact: top-level/nested 14 object boundary unknown-key mutation, required collection null/absence, canonical zero-node/zero-route/zero-attempt arrays, optional absence, empty ASN rejection을 Web/Android 양쪽에서 실행한다. exact Geo fixture와 ASCII/multibyte/HTML-escape/mixed bundle 4,096 acceptance 및 4,097 rejection이 Go/Web/Android에서 일치한다.
- client API errors/presentation: `(status,code)` rows와 Retry-After cases(cardinality는 gate가 pin한다)를 Web/Android가 공통 fixture로 소비한다. Android final classifier는 invalid pair/body를 status-preserving `invalid_server_response`, fixed local message, non-retryable/no timestamp로 닫고, valid pair의 local retryability/UI focus를 exhaustive하게 검사한다. Report/Capabilities transports 모두 disconnect 전 full header collection을 한 번 읽으며 single canonical `1..3600`만 allowed row의 `now+seconds`가 된다. Absent/malformed/duplicate case variants/comma/inaccessible/disallowed Retry-After는 typed body/retry button을 유지하고 timestamp만 없앤다. 모든 finding은 semantic key set과 fixed typed evidence/action relationship만 screen/export에 내고 poison target/IP/URL/Bearer/provider/error/panic prose를 노출하지 않는다.
- Web maximum DOM: producer maximum 40-finding report는 lazy panel 0→1 ownership으로 initial 699, measured peak 732, guard 1,200 이하를 유지한다. forced oversized document는 report/share commit 없이 fixed presentation failure가 된다.
- Android Activity/raw share: detached render failure, pre/post single-swap checkpoint failure, stale owner/generation, rollback/focus/share eligibility를 Robolectric에서 검증한다. process singleton one-slot/no-queue, off-main UTF-8 8 MiB exact/+1, 2 files/16 MiB, 32-entry/32-delete multi-pass scan, generation/detach, atomic/path/link checks, revoke/delete/URI/cleanup failure와 15-minute observed boundary를 검증한다.
- Android variant collection canary: bounded XML parser는 debug 결과에 정확히 하나의 `DebugNetworkSecurityContractTest`를 요구하고 release class를 금지하며, release 결과에는 정확히 하나의 `ReleaseNetworkSecurityContractTest`를 요구하고 debug class를 금지한다. 각 suite의 testcase count/class identity, total nonzero, errors/failures=0를 독립 확인하므로 한 variant 결과를 복사하거나 누락해도 실패한다.
- API offline release artifact: required buildx flags/rewrite timestamp, tagless single-manifest archive, exact 512 MiB/4,096/256 MiB/512 MiB/64/256 MiB/512 MiB/128 MiB bounds, trusted pinned Alpine layer/diff-ID/history prefix, traversal/duplicate/ambiguity/member-type/whiteout rejection, exact `/checknetwork-api`+`/traceroute` selected set/mode/hash/Go identity, safe empty caller-owned directory extraction과 rollback을 검사한다. OCI layer-local marker set에서 root/directory opaque와 ordinary whiteout은 lower target/descendants만 제거하고 same-layer recreation은 marker의 tar 순서와 무관하게 보존하는지 synthetic projection으로 회귀 검사한다. 두 build의 archive bytes와 archive/config/manifest/rootfs/layers/binary/traceroute/Go fields가 같아야 한다. Validator count는 gate를 실행해 관측하고 문서로 단언하지 않는다.
- API+Web release ownership: derived API/Web archives는 daemon에 load/daemon-tag/import/delete/execute되지 않음을 static fake-daemon과 real inventory tests로 검사한다. API owned roles는 smoke container+API network 정확히 두 개이고 Web도 smoke container+internal network 정확히 두 개다. Borrowed pinned bases와 read-only selected-file mounts는 compatibility smoke일 뿐 derived archive execution evidence가 아니다. 각 container/network는 normal response와 daemon-success/CLI-response-loss에서 pending name+nonce+owner→immutable ID proof를 요구하고 ordinary failure/HUP/INT/TERM에서 owned cleanup, caller/collision/replacement/mismatch preservation, 129/130/143 status를 확인한다.
- Web release artifact: exact `linux/amd64`, buildx required flags/rewrite timestamp와 tagless archive를 검증한다. Real gate는 `SOURCE_DATE_EPOCH=0`, private generated `FROM nginx@exactdigest`-only context로 base를 두 번 export하고 base bytes, derived bytes, validator result 및 daemon image/tag/container/network inventories를 각각 비교하며 EXIT/HUP/INT/TERM에서 private archives를 정리한다. Exact 512 MiB/4,096/256 MiB/512 MiB/64/256 MiB/32 MiB bounds에서 outer declared-byte aggregate exact/+1, compressed outer expansion bomb, 4,096/4,097 small-member boundary를 검사한다. Actual Docker-media Buildx OCI index/layout shape는 허용하지만 `manifest.json`, exact referenced config/layers, optional coupled index/layout와 그 index의 single referenced manifest blob 외 regular(`repositories`, `unexpected-secret.txt` 포함), non-ancestor directory, link/special은 거부하며 implicit tar parent directories는 요구하지 않는다. Full-validator mutation은 authenticated exact pinned nginx config/layer/diff-ID/history prefix가 traversal 전에 성립하는지, spoofed diff-ID/history/reorder와 crafted positional layer가 base 권한을 얻지 못하는지, inherited base의 legitimate canonical symlink/hardlink는 통과하는지 확인한다. Base device/FIFO/socket 및 모든 selected special을 거부하고, 모든 post-base unselected symlink/hardlink/device/FIFO/socket도 selection 전에 거부한다. Root opaque, nginx/html directory opaque, parent-directory/individual ordinary whiteout, nested descendants, same-layer recreation의 marker 전후 tar 순서를 synthetic layer와 multi-layer Docker archive로 검사하며 재생성되지 않은 canonical selected closure는 실패한다. Offline traversal/member/budget/whiteout/canonical file-set과 two-build output/config/manifest/rootfs/asset digest equality를 보존한다. Success output은 exact six API fields `api_archive/api_config/api_manifest/api_rootfs/api_binary/api_traceroute`와 five Web fields `archive/config/manifest/rootfs/asset_manifest`다.
- documentation contract: 필수 현재 문서와 보관 문서의 존재, root 진입 문서 한도, inline 상대 문서·이미지 링크를 검사한다. `docs/API.md`의 오류 표는 producer fixture의 registry key, HTTP status, wire code, retryable, Retry-After, message를 행 단위로 대조한다. 산문·Stage 상태·변동하는 테스트 개수·소스 코드 문자열은 고정하지 않는다. API/finding/presentation producer bytes, 보안·자원 경계와 client 회귀는 각 제품 테스트가 계속 검증한다. Fragment anchor와 reference-style Markdown 링크는 이 검사 범위 밖이다.
- CI script contract: POSIX `sh -n`, no recursive archive, no `eval`, exact 40-char lowercase SHA/current clean HEAD, trap cleanup, canonical archive equality, archive preexisting generated directory rejection, Go JSON/Node TAP/Android XML machine count를 검증한다.
- 수동: Windows/macOS/Linux에서 실행, 브라우저 UI, 실제 DNS/TCP/HTTPS 대상

외부 인터넷 대상은 수동·스테이징 시험에서만 사용한다. 자동 테스트는 로컬 리스너와 테스트 서버로 결정적이어야 한다.

## 아직 자동 gate가 대신하지 않는 acceptance

- physical Chrome/Firefox/Safari의 320/375/400 px viewport, Canvas pointer/wheel/keyboard/DPR behavior와 실제 screen-reader semantic inspector/fallback path
- Android emulator와 physical device, TalkBack/Switch Access, OEM share sheet, process kill 뒤 stock `FileProvider` grant expiry 및 already-open descriptor
- TLS reverse proxy의 HTTP/2 deadline/cancellation probe
- production registry push/signature/SBOM과 hosted full CI
- postcommit exact-SHA `make ci-clean-archive` 및 clean committed tree에서만 가능한 deterministic `make verify-release`/`make release`

따라서 JVM/jsdom/contract test 통과를 물리 기기/브라우저 검증으로 보고하지 않는다.
