# CheckNetwork

현재 릴리스 버전은 `VERSION`의 `0.1.0`이다. release binary의 `/api/v1/health`, startup log, OCI version/revision labels는 같은 버전과 정확한 40자리 Git revision을 제공한다. 일반 `go run`은 `dev` identity를 사용하고, 기본 Compose 개발 빌드는 Dockerfile 검증을 통과하는 exact sentinel `VERSION=0.1.0-dev`, `REVISION=0000000000000000000000000000000000000000`, `SOURCE_DATE_EPOCH=0`을 사용한다. zero revision은 개발 전용 sentinel이며 release provenance로 사용해서는 안 된다.

Web UI는 진단과 토폴로지를 독립 request lane으로 실행하며 stale-response를 차단한다. Target admission은 `MAX_TARGETS=20`에서 닫히고 hidden retained view까지 포함한 current document 전체를 `MAX_DOCUMENT_ELEMENTS=1200`으로 계산한다. 20번째 target은 exact-fit이면 허용하지만 cap+1 또는 global DOM budget 초과 candidate는 append 전에 atomic 거부한다. topology는 compact-v1 응답(nodes≤500, links≤1,000, body<1 MiB)을 사용하고 active view를 100-element chunk로 점진 렌더링하며 같은 document 1,200-element 경계를 검사한다. Bearer credential은 API base별 현재 탭에만 유지되고 보고서/export에는 포함되지 않는다.

Stage 9은 Stage 8에서 semantic node/link/route를 각각 단일 텍스트 요소로만 렌더링해 데이터는 남았지만 시각 레이어가 사라진 회귀를 복구한다. 토폴로지는 기본 2D와 선택 가능한 3D perspective를 외부 의존성 없는 Canvas로 그리며, 두 모드는 같은 검증된 facts의 view projection일 뿐 report, analysis 또는 diagnosis를 바꾸지 않는다. Pointer drag/wheel, 키보드 화살표·`+`/`-`·`Home`, reset과 fullscreen을 지원하고 interaction/resize 때만 다시 그려 continuous animation을 하지 않는다. 동기화된 bounded semantic inspector는 screen-reader/키보드 탐색과 Canvas 불가 시 fallback으로 계속 남는다. Visual bounds는 nodes 500, links 1,000, routes 1,000, document DOM 1,200, DPR 최대 4이며 현재 자동 Web inventory는 283 tests, 측정된 최대 DOM은 1,151이다. Source 구현은 반영됐지만 final canonical gates와 독립 review 전이므로 Stage 9 상태는 **implementation pending final gates/review**이며, 실제 브라우저와 screen reader 수동 검증도 pending이다.

운영 경계는 pre-handler accepted connection 기본 128(설정 hard max 256), 동시 report 기본 4(hard max 16), body decode 기본 effective report limit(hard max 64), checker 기본 80(hard max 1,024), 프로세스 전체 GeoIP active 8/queued 64, GeoIP LRU 2,048개, 동시 response write 최대 16이다. connection 초과분은 handler 생성 전에 즉시 close되고 interruptible 5 ms backoff가 적용된다. body slot은 인증·rate limit 뒤 JSON decode 동안만 소유하며 포화 시 고정 `503 body_decode_capacity_unavailable`을 반환한다. request body는 1 MiB, header는 64 KiB, full response는 newline 포함 8 MiB 이하, compact response는 1 MiB 미만이다. Web/Android report deadline은 요청 시작부터 315초이며 Android는 capability discovery와 report POST가 이 하나의 deadline을 공유한다. Compose는 6분 graceful stop과 CPU/memory/PID 제한을 적용한다. 자세한 과부하 결과와 관측 필드는 [운영 문서](docs/OPERATIONS.md)에 있다.

백엔드는 시작 시 trusted deployment input인 `PATH`의 platform traceroute를 `127.0.0.1`에서 bounded 기능 probe하고 성공한 exact path+argument grammar를 readiness와 실행에 함께 캐시한다. Linux common/iputils, Alpine BusyBox, Darwin 및 `-n`을 지원하지 않는 GNU inetutils-compatible Unix grammar를 version parsing 없이 선택한다. 후보 전체가 2초/combined 32 KiB 경계 안에서 성공하지 않으면 capability는 nil로 fail closed하고 `/readyz`는 `traceroute_unavailable`이다. nil capability에서 runtime path lookup이나 다른 grammar fallback은 없다. 이 probe는 executable을 실제 시작하므로 production image와 `PATH`는 trusted input이어야 하며 process cleanup 경계도 악성 binary의 side effect를 sandbox하지 않는다. Web/Android의 exact fixed presentation은 `Traceroute capability was unavailable, so no route observation was established.`이고 server의 `traceroute is unavailable` prose나 target은 반사하지 않는다.

CheckNetwork는 기본 통신, DNS, 네트워크 경로, 해외망, 특정 서비스 상태를 한 번에 검사하고 구조화된 리포트를 만드는 멀티플랫폼 애플리케이션입니다.

Stage 8의 구현 계약은 다음과 같다.

- 종료 deadline에 non-cooperative checker가 남으면 프로세스는 성공을 보고하지 않는다. 하나의 atomic snapshot에서 bounded `active`, `stuck`, `remaining`을 기록하고 nonzero로 종료한다.
- 13개 kind 모두 `expected_status` 생략/`0`을 허용한다. 명시적 `100..599`는 `http`/`https`에서만 허용하며 redirect를 모두 따른 **최종 응답 status**와 비교한다.
- SSH/SMTP/submission/SMTPS/IMAP/IMAPS/POP3/POP3S는 client write 없이 bounded server-first greeting만 확인한다. 성공 scope는 `verification_scope="server_greeting"`이며 UI의 정확한 의미는 “Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.”이다.
- TLS 검증 실패는 typed cause로 `tls_certificate_expired`, `tls_certificate_not_yet_valid`, `tls_hostname_mismatch`, `tls_untrusted`, 기타 `tls_handshake_failed`를 구분한다. 만료/미발효는 canonical UTC `certificate_not_before`/`certificate_not_after`를 함께 제공한다.
- producer-owned `finding-contract-v1`의 exact 23 findings, 31 result shapes, expanded 136-row `(kind,status,error_code,details)` semantic compatibility matrix를 Web/Android 두 client가 그대로 소비한다. 두 client는 같은 eight producer traceroute witness fixtures(full/compact 4쌍)도 통과시키고, generic runner-owned `cancelled`는 13 kind 모두 details를 금지한다. `healthy` result는 `error_code`가 absent/empty여야 하며, 알려진 값끼리라도 모순인 tuple이나 details는 publication 전에 report 전체를 거부하고 server prose를 반사하지 않는 fixed local invalid-response로 닫는다. 별도 `presentation-contract-v1`의 10 evidence result kinds를 포함한 cardinality `6/10/21/23/33`은 result-shape 수와 다른 presentation 계약이다. 두 client는 `cause`, `supporting_evidence`, `expectation`, `evidence_directness`, `coverage_limitation`, `next_action` 여섯 semantic key만 privacy-safe typed evidence로 표시한다.
- 실패한 traceroute attempt의 parse 가능한 topology는 full raw `details.attempts`에서만 보존될 수 있고 compact route/node/link/stat 및 Geo aggregate에는 들어가지 않는다. 별도의 completed route가 있으면 full `timeout`/command-failure result가 그 completed route의 optional representative `details.topology`를 유지할 수 있지만, generic `cancelled` result는 topology/counter를 포함한 details 전체를 금지한다.
- compact-v1은 unknown field와 malformed-present/null을 거부하고 canonical empty array를 보존한다. Geo/ASN bundle은 Go `encoding/json` byte 규칙으로 정확히 4,096 bytes까지 허용하며 4,097 bytes를 거부한다.
- Android report 화면은 detached hierarchy를 완성한 뒤 한 번 swap하고 publish 실패는 rollback한다. raw JSON 공유는 process-wide singleton 한 slot, queue 없음, off-main UTF-8/file I/O, file당 8 MiB, 최대 2 files/16 MiB, 32-entry/32-delete reconciliation, read-only `FileProvider` grant를 사용한다.
- Android의 final API-error 분류는 producer fixture의 정확한 16개 `(status,code)` pair만 typed error로 인정한다. producer-derived structural mutation corpus는 85 cases다. parser는 body 64 Ki UTF-16 code units, depth 2, properties 3, tokens 10, wire field-name/value 각각 128 code units의 closed `{"error":{"code","message"}}` shape만 읽고 duplicate/unknown/missing/null/trailing content와 malformed UTF-16을 거부한다. unknown/mismatched/malformed body는 wire status를 보존한 local `invalid_server_response`(`The server returned an invalid error response.`), non-retryable, no retry timestamp로 닫힌다. valid pair의 retry button은 registry `retryable`만 따르고, `Retry-After`는 허용된 row에서 정확히 하나의 ASCII decimal `1..3600` 값일 때만 `now+seconds`를 표시한다. absent/malformed/duplicate/comma/inaccessible/disallowed header는 typed body와 retryability를 바꾸지 않고 timestamp만 생략한다.
- API와 Web release 산출물은 exact `linux/amd64`와 같은 canonical source/commit epoch에서 각각 두 번 만든 tagless Docker archive이며, 두 archive bytes와 validator의 archive/config/manifest/rootfs 및 선택 파일 digest가 같아야 한다. Web pinned nginx base도 private generated `FROM nginx@exactdigest`-only context에서 canonical epoch 0으로 두 번 tagless Buildx export하고 bytes를 비교하므로 `docker image save`의 Docker 29 multi-platform closure나 source/ambient context에 의존하지 않는다. Web outer archive는 512 MiB compressed-file cap과 별도로 regular-member declared-byte aggregate를 512 MiB로 stream-account하고, exact Docker/optional OCI referenced graph와 그 blob ancestor directories만 허용한다. 따라서 implicit tar directories는 허용되지만 `repositories`, `unexpected-secret.txt`, arbitrary directory/file 및 outer link/special은 거부된다. 두 derived archive 모두 shared daemon에 load, tag, import, delete 또는 execute하지 않는다. API verifier가 소유하는 daemon role은 high-entropy name과 independent nonce/owner label을 쓰는 `smoke` container와 API network **정확히 두 개**뿐이다. 검증·안전 추출된 `/checknetwork-api`와 `/traceroute`를 borrowed pinned Alpine base에 read-only mount하는 smoke는 mount compatibility만 증명하고 derived API archive 실행을 증명하지 않는다. Web도 smoke container와 internal network 두 role만 소유하며 borrowed pinned nginx base/cache를 삭제하지 않는다. create 전 pending, normal ID 또는 daemon-success/CLI-response-loss 뒤 exact name+nonce+owner로 복구한 immutable ID, cleanup 직전 재검증으로 그 ID만 지우며 HUP/INT/TERM에서도 caller/mismatch resource는 보존한다.

Compose SemVer regression fix 뒤 현재 Stage 9 Web source inventory는 286 tests다. Producer findings 23, result shapes 31과 expanded result matrix 136, presentation `6/10/21/23/33`, API errors 16와 structural mutations 85, Android debug/release 각각 direct-child XML 297 tests + variant canaries 2, API/Web offline archive validator 28+25=53 tests, exact real release-gate fake cases 12는 유지된다. Production exact gate는 canonical inventory를 기준으로 umask 077/027/000 세 pass를 비교한다. Stage 9 final canonical gates와 독립 review, 새 external manifest, manifest-derived temporary release, commit과 exact-SHA closure는 아직 pending이다. 실제 브라우저와 screen reader 수동 검증도 pending이며 자동 test count로 완료를 주장하지 않는다. Repository root의 ignored `checknetwork-api` binary는 candidate manifest와 검증 증거에서 제외되어 있으나 비파괴 요청 때문에 제거하지 않고 retained 상태다. 이를 final artifact-cleanup 또는 release 성공으로 해석하지 않는다.

신규 리포트는 단순 PASS/FAIL과 함께 원인 후보, 그 판단을 지지하는 관측 증거, 안전한 다음 확인 단계와 분석 한계를 구조화된 `analysis`로 제공한다. 분석 confidence는 장애 확률이 아니며 현재 수집한 telemetry의 근거 수준을 뜻한다.

백엔드는 Go로 작성된 독립 REST API이며, 진단 엔진은 다른 CLI·데스크톱·모바일 프론트엔드에서도 재사용할 수 있습니다. 기본 프론트엔드는 빌드 도구 없이 배포 가능한 웹 앱입니다.

## 빠른 시작

```bash
go run ./cmd/checknetwork-api
```

다른 터미널에서:

```bash
curl http://localhost:8080/api/v1/health
curl http://localhost:8080/livez
curl http://localhost:8080/readyz
curl -X POST http://localhost:8080/api/v1/reports \
  -H 'Content-Type: application/json' \
  -d '{"targets":[{"kind":"dns","address":"example.com"},{"kind":"https","address":"https://example.com"},{"kind":"ssh","address":"example.com"}]}'
```

지원 검사는 DNS, 임의 TCP, HTTP, HTTPS, traceroute와 SSH·SMTP·IMAP·POP3 계열 서비스다. 웹의 `경로 토폴로지`는 목적지별 1~10회 경로를 canonical node와 directed link로 합치고 result/attempt 사이에 공정한 complete-prefix를 표시한다. 서버 제한과 화면 제한, 실제/표시/생략 수를 구분하며 목적지 필터, roving keyboard focus와 전체 화면을 지원한다. `IP 라벨`은 CSV/JSON import와 직접 편집을 지원하되 1 MiB/500 records/100-row page 경계를 적용한다. HTTPS와 암시적 TLS 서비스는 TLS 버전, 암호 스위트, 인증서 제목과 만료 시각도 보고한다.

Android 앱도 `/checks` capability와 13종 검사, HTTPS-only public credential, request owner/cancel/recreation, 설명 가능한 analysis와 compact topology 요약을 사용한다. discovery와 report는 하나의 315초 절대 deadline을 공유하고 각 단계의 더 짧은 local deadline과 교차한다. 유효하지만 축소된 capability와 선택값의 불일치는 typed `UNSUPPORTED_CAPABILITY` 이유와 고정 UI 문구로 표시하며 malformed 응답은 계속 invalid response다. 기본 공유는 redacted human summary이고 원본 JSON은 경고 확인 후 private cache의 bounded content URI로만 공유한다. Interactive Android graph와 실기기 TalkBack 검증은 아직 별도 acceptance 항목이다. raw-share 15분 lease는 앱 프로세스가 `observe()`한 시점에 revoke/cleanup하는 process-observed TTL이며, 프로세스가 죽은 뒤 stock `FileProvider` grant, 이미 열린 descriptor 또는 수신 앱의 복사를 강제로 만료시키는 보장은 아니다.

공인 IP 홉에는 GeoIP 위치와 ASN/사업자 정보를 보강한다. `Geo 경로 지도`는 같은 bounded selection을 외부 tile/credential 없는 Canvas 경로 개요와 접근 가능한 위치 목록으로 표시한다. 위치는 실제 장비 소재지가 아닌 IP 등록 정보 기반 추정치다.

웹 UI는 정적 파일 서버로 별도 실행합니다.

```bash
cd frontend
python3 -m http.server 3000
```

직접 실행 시 브라우저에서 `http://localhost:3000`을 열고 API 주소에 `http://localhost:8080`을 입력합니다. Docker Compose에서는 API 주소가 `http://localhost:9090`입니다.

```bash
docker compose up --build --wait
curl --fail http://127.0.0.1:9090/readyz
curl --fail http://127.0.0.1:9090/api/v1/health
curl --fail http://127.0.0.1:3000/
```

API runtime image는 UID/GID `65532:65532`로 실행되고 Web nginx는 `worker_processes 2`를 사용한다. Compose publish는 두 서비스 모두 host loopback으로 제한된다. Compose API healthcheck는 public mode에서도 credential과 rate quota를 사용하지 않는 `/readyz`를 호출한다.

## 검증

```bash
npm --prefix frontend ci
make test
make web-test-syntax
make test-race
make vet
GOFLAGS=-buildvcs=false make build
make android-wrapper-verify
make android-env
make android-test
make android-lint
make android-assemble
# Compose 정적 계약 및 이미지/health smoke (Docker 필요)
docker compose config
docker compose build api web
docker compose up -d --wait
docker compose down
# Go/Web와 Android 전체 gate
make ci
# commit 후 clean exact HEAD에서만 실행; Docker daemon 필수
make ci-clean-archive
# clean exact HEAD의 tagless API/Web offline archive 및 identity 검증; Docker 필수
make verify-release
# clean temporary candidate에서 umask 077/027/000과 canonical inspect inventory를 확인하는 exact release gate
make verify-release-real REVISION=<exact-40-character-HEAD-SHA>
# component-only Buildx regressions; 위 exact release workflow를 대신하지 않음
make verify-archives-real
```

JVM/Node/Go 자동화는 physical Chrome/Firefox/Safari의 320/375/400 px viewport와 screen reader, Android emulator/physical device의 TalkBack/Switch Access·OEM share sheet·process-death descriptor 동작, TLS reverse proxy의 HTTP/2 deadline/cancellation, registry push/signing/SBOM, hosted CI를 대신하지 않는다. 이 acceptance와 postcommit-only `make ci-clean-archive`는 별도 gate다.

자세한 내용은 [구현 계획](docs/PLAN.md), [아키텍처](docs/ARCHITECTURE.md), [API 명세](docs/API.md), [테스트 기준](docs/TESTING.md)을 참고하세요.
