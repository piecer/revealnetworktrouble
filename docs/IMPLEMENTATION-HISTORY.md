# 구현 이력과 남은 검증

개별 작업 계획에서 계속 필요한 구현 순서, 계약과 미완료 항목을 모은 문서입니다.
과거 체크포인트를 보존한 것이며, 문서 정리나 테스트 통과만으로 미수행 검증을 완료 처리하지 않습니다.
현재 사용법은 [README](../README.md), 상세 검증 방법은 [테스트 기준](TESTING.md)을 참고하세요.

## Stage 1: 실행 경계와 HTTPS 판정 복구

당시 범위: 요청·실행 자원 제한, 공개·로컬 배포 경계와 HTTPS 판정 복구.

## Stage 2: 설명 가능한 자동 분석 계약

당시 범위: 관측 결과에서 원인 후보, 근거, 관측 한계와 다음 확인 단계를 분리하는 분석 계약.

## Stage 3: Web 요청 lifecycle과 분석 워크스페이스 UI

당시 범위: 입력 변경·취소·늦은 응답을 구분하는 요청 소유권과 분석 화면.

## Stage 4: Web topology 정확성과 대용량 성능

당시 범위: IPv6 경로 집계, 제한된 크기의 topology 응답과 화면, 생략된 정보의 명시적 표시.

## Stage 5: Android 기능 동등성과 lifecycle 아키텍처

당시 범위: 네이티브 클라이언트의 검사 요청, 결과 표시, 화면 수명주기와 공유.
자동화된 구현 검증과 실기기·접근성 검증은 별도입니다.

## Stage 6: 외부 보강·관측성·운영 성능 강화

당시 범위: GeoIP 동시 작업·캐시·제공자 실패 표시, 진단 자원 수명과 개인정보를 보호하는 운영 관측.
후속 Stage 7에 기록된 Stage 6 exact-SHA closure는 과거 완료 근거이며 현재 소스의 새 검증 결과는 아닙니다.

## Stage 7: 교차 플랫폼 재감사와 다음 루프 계획

재감사에서 발견한 연결 수 제한, deadline, readiness, 진단 결과와 전송 결과의 구분,
Android 공유·capability 문제를 정리했습니다. 구현 이력: `36dc1dc`.
Stage 7 exact-SHA closure는 후속 Stage 8 기록에서 역사적 완료 근거로 취급합니다.

## Stage 8: 진단 사실과 클라이언트·릴리스 계약

기준 부모 커밋은 `36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243`, 구현 커밋은 `43689b3`입니다.
구현 커밋의 존재와 전체 릴리스 수용 완료는 구분합니다. 아래는 이전 계약에서 유지하는 세부 조건입니다.

- Android의 알 수 없거나 모순된 오류 응답은 exact local code `invalid_server_response`로 처리합니다. HTTP 상태는 보존하지만 재시도 가능 여부를 임의로 추정하지 않습니다.
- Android 테스트 결과의 exact `binary/` companion은 테스트 성공의 증거가 아닌 제한된 부속 메타데이터입니다.
- API hard limits are archive 512 MiB, members 4,096, member bytes 256 MiB, member total 512 MiB, layers 64, layer bytes 256 MiB, expanded layer bytes 512 MiB, selected bytes 128 MiB. 추출 대상은 caller-owned empty real directory여야 합니다.
- 파생 API/Web archive는 never daemon-loaded, daemon-tagged, imported, deleted, or executed. 빌린 base에서의 mount 호환성 검사는 파생 archive의 실행 증거가 아닙니다.
- API owns one smoke container and one API network. Success output is exactly six API fields: `api_archive`, `api_config`, `api_manifest`, `api_rootfs`, `api_binary`, `api_traceroute`.
- 재현성 검증은 three umasks 077/027/000 및 canonical Docker inventory 비교를 포함합니다.
- Web 입력과 화면의 계약은 `MAX_TARGETS=20`, `MAX_DOCUMENT_ELEMENTS=1200`입니다.
- Documentation changes invalidate prior current-byte CI/count evidence. 최종 문서 수정 이후의 소스로 검증해야 합니다.

Historical record: Web 260 tests; result shapes 31; expanded result matrix 136; 456 fixture-derived semantic mutations; 13 generic cancelled-detail rejections; all eight producer traceroute witness fixtures; Android 297 direct-child XML tests + 2 variant-contract tests; exact real release-gate fake cases 12.

당시 제외 항목은 ignored repository-root `checknetwork-api` binary였습니다. 해당 파일의 보존과 릴리스 산출물 검증은 별개입니다.
다음 두 문장은 당시 최종 수용을 열어 두었던 기록이며, 이후 구현 커밋이 없다는 뜻은 아닙니다.

> Final post-doc `env -u JAVA_HOME -u ANDROID_HOME -u ANDROID_SDK_ROOT make ci` remains pending.
>
> No final manifest, commit, release, five-way review, exact-SHA, or main-integration claim is made here.

최종 clean-environment CI, 불변 후보에서의 릴리스 검증, five-way review 및 exact-SHA 수용을
묶어 닫는 기록은 별도 확인이 필요합니다. 이번 문서 통합으로 완료됐다고 주장하지 않습니다.

## Stage 9: 2D/3D 토폴로지 복원

구현 커밋은 `f0793ed`입니다. 기본 선택은 `2D 그래프`이며,
`3D 그래프`를 선택하면 동일한 node/link set을 다른 시점에서 보여줍니다.
데이터나 분석 결과를 바꾸지 않는 순수 view projection으로, 진단 결과를 변경하지 않는다.
interaction event와 resize에서만 redraw하며 지속 애니메이션을 추가하지 않습니다.

기존 계약은 Canvas 하나와 고정 control 수만 추가하고, document-wide 1,200 element 한도와
nodes 500/links 1,000/routes bounded를 지키며 DPR을 bounded finite 값으로 clamp하는 것입니다.
배포 시 exact Web production asset set을 확인해야 합니다.
Chrome이 없는 환경에서는 실제 브라우저/화면 판독기 검증을 완료했다고 주장하지 않는다.

이전 전체 수용 체크포인트는 “Stage 9 implementation is pending final gates/review, not complete”였습니다.
후속 기능별 자동 테스트나 커밋만으로 전체 독립 검토·불변 후보 릴리스·실기기 검증을 대신하지 않습니다.

## 후속 개선에서 보존할 항목

### 아키텍처 개선

`b6e0902`는 관측 보존, Web 저장·표시, Android 수명주기·복구, 배포 경계 검증,
capability 입력 검사 분리의 다섯 제한된 개선 단계를 통합했습니다.
더 큰 구조 변경은 수행 범위가 아니었으며 다음 권고가 남아 있습니다.

- [ ] Web controller/repository 분리, backend details 타입 명확화, 공통 archive 처리 정리.
- [ ] 개인정보를 보호하는 내부 event 계약을 정한 뒤 policy rejection 시 관측 보존 검토. 공개 응답의 details-free 422 및 fail-closed 동작은 그대로 유지.

### 무응답 홉 표시

`0511b93`에 연속 무응답 접기, 숨긴 구간의 점선 연결과 응답한 뒤쪽 홉 보존이 구현돼 있습니다.
기능 회귀 테스트와 해당 작업 전체의 최종 독립 검토 기록은 구분합니다.

- [ ] 해당 작업의 최종 local gate 및 독립 검토 완료 기록 연결.
- 원본 경로의 prefix 예산이 표시 변환보다 먼저 적용됩니다. 무응답 구간을 경로별로 펼치는 과정에서도 추가 표시 생략이 생길 수 있습니다.
- 점선 우회 연결은 직접 관측한 연결이 아닙니다. 설명은 범례와 상세 목록에 있으며 Canvas edge-hover 기능을 구현했다고 주장하지 않습니다.

### 전체 제품 수용과 후속 기능

- [ ] 증상별 guided preset의 범위를 확정하고 구현·수용 기준과 일치시키기.
- [ ] 실제 배포된 GUI/API에서 진단 확인.
- [ ] 실제 Chrome/Firefox/Safari, Windows 환경과 스크린 리더 검증.
- [ ] Android 실기기, TalkBack/Switch Access, OEM 공유 화면과 프로세스 종료 후 공유 파일 접근 검증.
- [ ] TLS reverse proxy의 HTTP/2 deadline/cancellation 검증.
- [ ] canonical exact-SHA Docker release 검증과 운영 registry push/signing/SBOM, hosted CI.

추가 기능 후보와 전체 계획은 [구현 계획](PLAN.md)에 유지합니다.
# 구현 이력 보관

Historical snapshot — 2026-11-07, Phase 1/2 commit `734885a` 직후의 문서다.

아래 원문은 Stage별 당시 설명과 검증 전 상태를 보존한 기록이며, 현재 지원·검증 완료 선언이 아니다. 최신 구조는 [아키텍처](ARCHITECTURE.md), 실제 실행 결과는 [Phase 2 검증 기록](PHASE2-VERIFICATION.md), 남은 작업은 [실행 계획](../PLAN.md)을 따른다.

README에 사전부터 있던 공통 traceroute segment·청록 halo·유사 인터페이스 후보 설명도 원문으로 남긴다. 현재 소스에서 확인한 `공통 구간`은 IP 비트/CIDR 로컬 계산이며, 이 초안의 추가 알고리즘을 지원 완료로 간주하지 않는다.

## Historical snapshot: README.md

# CheckNetwork

현재 릴리스 버전은 `VERSION`의 `0.1.0`이다. release binary의 `/api/v1/health`, startup log, OCI version/revision labels는 같은 버전과 정확한 40자리 Git revision을 제공한다. 일반 `go run`은 `dev` identity를 사용하고, 기본 Compose 개발 빌드는 Dockerfile 검증을 통과하는 exact sentinel `VERSION=0.1.0-dev`, `REVISION=0000000000000000000000000000000000000000`, `SOURCE_DATE_EPOCH=0`을 사용한다. zero revision은 개발 전용 sentinel이며 release provenance로 사용해서는 안 된다.

Web UI는 진단과 토폴로지를 독립 request lane으로 실행하며 stale-response를 차단한다. Target admission은 `MAX_TARGETS=20`에서 닫히고 hidden retained view까지 포함한 current document 전체를 `MAX_DOCUMENT_ELEMENTS=1200`으로 계산한다. 20번째 target은 exact-fit이면 허용하지만 cap+1 또는 global DOM budget 초과 candidate는 append 전에 atomic 거부한다. topology는 compact-v1 응답(nodes≤500, links≤1,000, body<1 MiB)을 사용하고 active view를 100-element chunk로 점진 렌더링하며 같은 document 1,200-element 경계를 검사한다. Bearer credential은 API base별 현재 탭에만 유지되고 보고서/export에는 포함되지 않는다.

Stage 9은 Stage 8에서 semantic node/link/route를 각각 단일 텍스트 요소로만 렌더링해 데이터는 남았지만 시각 레이어가 사라진 회귀를 복구한다. 토폴로지는 기본 2D와 선택 가능한 3D perspective를 외부 의존성 없는 Canvas로 그리며, 두 모드는 같은 검증된 facts의 view projection일 뿐 report, analysis 또는 diagnosis를 바꾸지 않는다. Pointer drag/wheel, 키보드 화살표·`+`/`-`·`Home`, reset과 fullscreen을 지원하고 interaction/resize 때만 다시 그려 continuous animation을 하지 않는다. 동기화된 bounded semantic inspector는 screen-reader/키보드 탐색과 Canvas 불가 시 fallback으로 계속 남는다. Visual bounds는 nodes 500, links 1,000, routes 1,000, document DOM 1,200, DPR 최대 4이며 현재 자동 Web inventory는 283 tests, 측정된 최대 DOM은 1,152이다. Source 구현은 반영됐지만 final canonical gates와 독립 review 전이므로 Stage 9 상태는 **implementation pending final gates/review**이며, 실제 브라우저와 screen reader 수동 검증도 pending이다.

운영 경계는 pre-handler accepted connection 기본 128(설정 hard max 256), 동시 report 기본 4(hard max 16), body decode 기본 effective report limit(hard max 64), checker 기본 80(hard max 1,024), 프로세스 전체 GeoIP active 8/queued 64, GeoIP LRU 2,048개, 동시 response write 최대 16이다. connection 초과분은 handler 생성 전에 즉시 close되고 interruptible 5 ms backoff가 적용된다. body slot은 인증·rate limit 뒤 JSON decode 동안만 소유하며 포화 시 고정 `503 body_decode_capacity_unavailable`을 반환한다. request body는 1 MiB, header는 64 KiB, full response는 newline 포함 8 MiB 이하, compact response는 1 MiB 미만이다. Web/Android report deadline은 요청 시작부터 315초이며 Android는 capability discovery와 report POST가 이 하나의 deadline을 공유한다. Compose는 6분 graceful stop과 CPU/memory/PID 제한을 적용한다. 자세한 과부하 결과와 관측 필드는 [운영 문서](OPERATIONS.md)에 있다.

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

Historical record: Compose SemVer regression fix 뒤 현재 Stage 9 Web source inventory는 298 tests다. Producer findings 23, result shapes 31과 expanded result matrix 136, presentation `6/10/21/23/33`, API errors 16와 structural mutations 85, Android debug/release 각각 direct-child XML 297 tests + variant canaries 2, API/Web offline archive validator 28+25=53 tests, exact real release-gate fake cases 12는 유지된다. Production exact gate는 canonical inventory를 기준으로 umask 077/027/000 세 pass를 비교한다. Stage 9 final canonical gates와 독립 review, 새 external manifest, manifest-derived temporary release, commit과 exact-SHA closure는 아직 pending이다. 실제 브라우저와 screen reader 수동 검증도 pending이며 자동 test count로 완료를 주장하지 않는다. Repository root의 ignored `checknetwork-api` binary는 candidate manifest와 검증 증거에서 제외되어 있으나 비파괴 요청 때문에 제거하지 않고 retained 상태다. 이를 final artifact-cleanup 또는 release 성공으로 해석하지 않는다.

Historical route-visual slice source inventory: **Web 328 tests** (pre-density route-visual 326 and ASN-context 320 are historical). Merged Geo/shared-prefix source inventory: **Web 620 tests**, plus two separate contract scripts (`app.test.js` and `common-prefix.test.js`). Geo inventories 308/304 and pre-Geo 298 are historical. Offline archive validators: API 33 + Web 30 = 63; production Web closure now contains **ten assets**, including self-contained `geo-map.js` and `common-prefix.js`. ASN context is presentation-only inference, never private-IP ownership or Geo enrichment. See [ASN context evidence](ASN-CONTEXT.md). Independent review/release remains pending.

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

지원 검사는 DNS, 임의 TCP, HTTP, HTTPS, traceroute와 SSH·SMTP·IMAP·POP3 계열 서비스다. 웹의 `경로 토폴로지`는 목적지별 1~10회 경로를 canonical node와 directed link로 합치고 result/attempt 사이에 공정한 complete-prefix를 표시한다. 선택한 목적지가 2개 이상이면 실제 route contiguous subsequence 기준 exact 공통 구간과 반복 지지도를 별도 요약하고, 해당 observed edge의 목적지별 색 위에 굵은 청록 halo를 표시한다. 서버 제한과 화면 제한, 실제/표시/생략 수를 구분하며 목적지 필터, roving keyboard focus와 전체 화면을 지원한다. `IP 라벨`은 CSV/JSON import와 직접 편집을 지원하되 1 MiB/500 records/100-row page 경계를 적용한다. `공통 구간`은 주소 비트 기준 CIDR/범위와 실제 traceroute 공통 경로를 함께 표시한다. CIDR은 최대 64개를 로컬 계산하고, 경로 비교는 canonical 중복 제거 후 2~20개 IP를 목적지별 5회 수집한다. 모든 목적지의 실제 route contiguous subsequence를 교차해 branch에서도 잘리지 않은 maximal exact 공통 segment만 표시하며 attempt 간 edge를 합성하지 않고, 각 segment의 목적지별 최소 반복 횟수와 최저 표시-route 비율을 함께 보여준다. 목적지별 exact IP 교집합은 없지만 같은 IPv4 `/24` 또는 IPv6 `/64` 위치가 반복되는 경우는 별도의 낮은 신뢰도 `유사 인터페이스 경로 후보`로 표시하며, 동일 장비나 인터페이스로 확정하지 않는다. HTTPS와 암시적 TLS 서비스는 TLS 버전, 암호 스위트, 인증서 제목과 만료 시각도 보고한다.

Android 앱도 `/checks` capability와 13종 검사, HTTPS-only public credential, request owner/cancel/recreation, 설명 가능한 analysis와 compact topology 요약을 사용한다. discovery와 report는 하나의 315초 절대 deadline을 공유하고 각 단계의 더 짧은 local deadline과 교차한다. 유효하지만 축소된 capability와 선택값의 불일치는 typed `UNSUPPORTED_CAPABILITY` 이유와 고정 UI 문구로 표시하며 malformed 응답은 계속 invalid response다. 기본 공유는 redacted human summary이고 원본 JSON은 경고 확인 후 private cache의 bounded content URI로만 공유한다. Interactive Android graph와 실기기 TalkBack 검증은 아직 별도 acceptance 항목이다. raw-share 15분 lease는 앱 프로세스가 `observe()`한 시점에 revoke/cleanup하는 process-observed TTL이며, 프로세스가 죽은 뒤 stock `FileProvider` grant, 이미 열린 descriptor 또는 수신 앱의 복사를 강제로 만료시키는 보장은 아니다.

공인 IP 홉에는 GeoIP 위치와 ASN/사업자 정보를 보강한다. `Geo 경로 지도`는 같은 bounded selection을 외부 tile/credential 없는 Natural Earth 해안선·국경·국가명·주요 도시 기반 오프라인 Canvas 지도(드래그/확대/경로 맞춤)와 접근 가능한 위치 목록으로 표시한다. 지명은 확대 수준에 맞춰 겹치지 않게 표시하고, 정확히 같은 좌표의 IP는 개수 배지와 반복 선택으로 구분한다. 경로는 토폴로지와 같은 색을 사용하며 선택한 노드의 경로를 강조한다. 위치는 실제 장비 소재지가 아닌 IP 등록 정보 기반 추정치이며, 같은 좌표도 같은 장비를 뜻하지 않는다.

지도 점 또는 위치 목록을 선택하면 전체 IP/별칭, 위치·ASN·관측 RTT·홉 범위·경로/시도 소속을 지속 상세 패널에서 확인한다. 기존 공급자가 반환한 대륙·지역 코드·우편번호·시간대·별도 ISP·조직 도메인과 조회/캐시 출처도 Web/Android에 보존한다. 추가 정보는 `POST /api/v1/reports?geo_details=1`로 요청하며 구서버도 재진단 없이 호환된다. 좌표 없는 정보는 지도에 추측하여 배치하지 않고, 조회 시각은 공급자 DB 갱신 시각이 아니다. [확장 계약](GEO-DETAILS.md)과 [지도 설명](GEO-MAP.md)을 참고한다.

웹 UI는 정적 파일 서버로 별도 실행합니다.

```bash
cd frontend
python3 -m http.server 3000
```

직접 실행 시 브라우저에서 `http://localhost:3000`을 열고 API 주소에 `http://localhost:8080`을 입력합니다. Docker Compose의 기본 API 주소는 `http://localhost:8090`이며 Web 이미지도 같은 기본 주소로 빌드됩니다. `CHECKNETWORK_API_PORT`를 설정하면 host publish와 Web build argument가 함께 바뀝니다. 변경 후에는 Web 이미지를 다시 빌드해야 합니다. Compose 밖의 정적 HTML 및 canonical release 기본값은 기존 `http://localhost:9090`을 유지합니다.

```bash
docker compose up --build --wait
curl --fail http://127.0.0.1:8090/readyz
curl --fail http://127.0.0.1:8090/api/v1/health
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

자세한 내용은 [구현 계획](PLAN.md), [아키텍처](ARCHITECTURE.md), [API 명세](API.md), [테스트 기준](TESTING.md)을 참고하세요.

## Historical snapshot: docs/ARCHITECTURE.md

# 아키텍처 및 개발 기준

## 구성

Web label 저장 admission은 기존 key 보존 후 신규 key를 한도 내에서 병합한다. 표는 활성 view만 mount하고, 숨겨진 입력 DOM까지 센 남은 예산에서 page size를 결정한다. Topology empty-state는 graph cardinality만 보지 않고 명시적 선택 수와 실행 outcome을 받아 선택 해제·관측 불가·projection 제한을 구분한다.

```text
Web / Mobile / CLI
        |
   REST JSON API
        |
 Report Service
        |
 Analysis Interpreter  <- normalized facts / deterministic evidence rules
        |
 Diagnostic Runner
   |      |       |         |
  DNS    TCP   HTTP(S)   Services   <- 교체 가능한 Checker
```

production은 explicit `net.Listen`을 default 128/hard 256의 leased listener로 감싼 뒤 `http.Server.Serve`에 전달한다. 따라서 accepted connection token은 handler goroutine보다 먼저 획득되고 실제 connection `Close`에서 exactly once 반환된다. overflow socket은 즉시 close되고 accept loop는 close/shutdown으로 중단 가능한 5 ms backoff를 거친다.

Authenticated body-decode admission, report admission과 response-write admission은 서로 분리된다. body slot은 route/auth/rate 확인 및 declared oversize preflight 뒤 nonblocking으로 획득하고, `MaxBytesReader`에서 JSON object 하나와 EOF를 decode하는 동안만 유지한다. 기본 body capacity는 effective report limit이고 hard max는 64이며 포화는 고정 `body_decode_capacity_unavailable` 503이다. report slot은 checker 실행과 bounded serialization까지만 소유하고, 최대 16개의 독립 write token을 얻은 뒤 report slot을 반환한다. write token이 없으면 큰 payload를 보존/queue하지 않고 작은 `write_capacity_unavailable` 응답을 시도한다. API는 request ID를 auth 이전에, report ID를 실행 이전에 생성하며 submit/admit/reject/start/computed/finish/cancel과 모든 HTTP terminal event에서 같은 opaque ID를 사용한다.

Traceroute command/parser는 Unix `traceroute`와 Windows `tracert.exe` adapter를 분리한다. compact 요청에서는 Runner가 raw results 전체로 `analysis`를 먼저 만든 다음 deterministic compact graph를 생성한다. API transport는 raw report를 mutate하지 않는 copy에서 attempts와 대표 topology만 제거하고 final JSON을 header 이전에 marshal한다. Geo suffix와 route transaction을 단조 이진 탐색으로 축소해 1 MiB 미만을 보장한다.

Web은 strict compact schema, compact-first/legacy-bounded model, owner-safe progressive coordinator로 분리된다. Stage 8에서 semantic node/link/route마다 단일 텍스트 element만 materialize하고 visual item을 만들지 않아 facts는 유지됐지만 보이는 graph가 사라졌다. Stage 9은 동일한 validated model을 기본 2D 또는 optional 3D perspective Canvas로 투영하며 mode는 report/analysis/diagnosis를 바꾸지 않는다. Canvas는 nodes 500/links 1,000/routes 1,000과 DPR≤4에서 deterministic layout을 쓰고 continuous animation 없이 pointer/keyboard/reset/resize interaction 때만 redraw한다. Bounded semantic inspector는 Canvas와 동기화되어 keyboard/screen-reader path, label editing과 context failure fallback을 유지한다. Target rows는 `MAX_TARGETS=20`에서 닫히고 append admission은 hidden retained view를 포함한 current document 전체를 global `MAX_DOCUMENT_ELEMENTS=1200`으로 계산한다. Exact-fit 20번째 row만 commit하고 target cap+1 또는 document cap+1 candidate는 partial append 없이 거부한다. topology/Geo/labels 중 active view만 dynamic DOM을 소유하며 현재 maximum fixture는 1,152 DOM elements다. 앱 state와 listener는 `createApp` instance에 귀속되고 `destroy()`가 request, timer, scheduler, Canvas와 listener를 정리한다.

Android는 `MainActivity` presentation과 schema/model `core`, `network`, `state`를 분리한다. production session은 bounded `/checks` discovery로 server capability를 확인한 다음 report POST를 수행한다. 한 `OperationDeadline`이 두 단계의 315초 monotonic budget을 소유하고 discovery local 10초 및 request별 report deadline과 매 지점 교차한다. `RequestCoordinator`의 owner/signature가 교체·입력 무효화·취소·recreation 후 stale callback을 차단하며 transport는 Content-Length/stream 8 MiB와 exactly-once disconnect를 소유한다. valid reduced capability mismatch는 closed `UNSUPPORTED_CAPABILITY` reason으로 전달하고 malformed success response는 `INVALID_RESPONSE`로 유지한다. Non-2xx는 별도 final `ApiError` classifier가 producer fixture의 exact 16 `(status,code)` pairs만 인정한다. Parser는 body≤64 Ki UTF-16 code units, depth≤2, properties≤3, tokens≤10, wire name/value≤128 code units의 exact closed `error/code/message` tree만 허용하며 duplicate/unknown/missing/null/trailing/malformed UTF-16을 거부한다. Invalid body/pair는 status를 보존한 `invalid_server_response`, fixed local message, non-retryable/no timestamp이고, valid pair는 local message/retryability를 쓴다. 두 transport가 disconnect 전에 모든 Retry-After values를 수집하며 허용 row+single canonical `1..3600`만 injected `now+seconds`를 만든다. Header 실패는 typed body를 invalid로 바꾸지 않는다. Activity는 `ApiErrorPresentation`의 retry visibility, timestamp applicability와 unauthorized-only credential focus를 최종 적용한다. bounded form만 saved state에 기록하며 Bearer와 raw report는 retained memory 밖으로 직렬화하지 않는다. 사람용 redacted text와 확인 후 생성하는 private-cache raw content URI를 별도 공유한다.

Android session은 dispatch 실행 시점의 immutable state identity까지 확인한다. capability parser는 Android `JsonReader` token walk 앞에 strict lexical preflight를 두고 duplicate decoded names, unknown-field depth 및 malformed UTF-16을 materialization 전에 거부한다. UI draft persistence와 kind-applicable request/signature projection은 별개이며 direct core validation은 그대로 유지한다.

Capability의 lexical/token admission은 package-private `CapabilityJsonPreflight`가 소유하고 `CheckCapabilities`는 public capability API, semantic validation, local limit intersection과 request compatibility를 소유한다. 호출 방향은 `CheckCapabilities`→preflight 한 방향이며 64 KiB UTF-8/root-inclusive depth 8 및 고정 오류 계약을 유지한다. 다른 parser로 일반화하거나 공유하지 않는다. Test-only `FrozenCheckCapabilities`는 분리 직전 구현을 독립 보존해 같은 입력의 acceptance, exact error 및 request projection을 비교하며 production APK에는 포함되지 않는다.

`internal/api`는 HTTP 전송만 담당하고, `diagnostic` 패키지는 프로토콜이나 UI를 알지 못한다. 새 프론트엔드는 API만 사용하며, 새 검사 방식은 `Checker` 인터페이스 구현으로 추가한다.

진단 checker가 만든 `Result`는 먼저 typed normalized facts로 변환한 뒤 결정적 analysis 규칙에 입력된다. 분석은 backend의 자유 형식 오류 message를 파싱하지 않고 stable `error_code`와 구조화된 details만 사용한다. `confidence`는 원인 확률이 아니라 finding 문장을 지지하는 evidence의 직접성이고, 관측 불가·malformed·legacy 데이터는 별도 coverage limitation으로 보존한다.

서비스는 checker supervisor 하나를 공유한다. slot은 goroutine 생성 전에 획득하며 checker가 실제 return/panic할 때만 반환된다. non-traceroute checker가 끝나면 return 직후 기록한 timestamp를 parent/supervisor cancellation → report deadline → checker deadline → returned result 순으로 arbitration한다. timestamp가 deadline과 같거나 늦으면 healthy result를 commit하지 않는다. cleanup cancellation은 arbitration 뒤에만 발생한다. request budget이 먼저 끝나면 collector는 deterministic timeout/cancel result를 반환하고 late result가 이미 반환된 report를 mutate하지 못한다. HTTP(S) checker도 header만으로 성공하지 않고 최대 32 KiB body observation을 완료한 뒤 latency/status를 확정하며 read failure를 `response_read_failed`로 보존한다. Go는 context를 무시하는 checker나 injected HTTP transport를 강제 종료할 수 없다. 이런 noncooperative worker는 shutdown 뒤에도 남을 수 있고 실제 반환 때까지 checker/Geo active slot을 점유한다. shutdown은 deadline 직후 한 lock에서 `active/stuck/remaining` atomic snapshot을 얻고 어느 값이든 nonzero면 fixed failure와 exit 1을 반환한다. count는 process/target prose가 없는 bounded integer이며 `remaining=active`; 늦은 실제 return은 accounting만 0으로 수렴시키고 이미 보고한 실패를 성공으로 바꾸지 않는다.

HTTP(S) request의 `expected_status` validation은 Runner admission에서 닫힌다. omitted/0은 13 kind 모두 허용하고, nonzero 100..599는 HTTP(S)만 허용한다. checker는 redirect chain을 따른 최종 response status만 비교한다. service checker는 write API를 호출하지 않는 reader-only path로 4,096-byte/8-line/512-byte(SSH identification 255-byte) server-first grammar를 판정하고 성공에 `verification_scope=server_greeting`을 붙인다. protocol transaction/auth/STARTTLS/mailbox/e2e는 이 component의 범위 밖이다.

TLS failure adapter는 하나의 frozen verification clock과 typed Go error chain을 사용한다. hostname→untrusted→`x509.Expired` validity boundary→generic handshake 순으로 분류하며 expired/not-yet-valid에서만 canonical `certificate_not_before`/`certificate_not_after`를 내보낸다. Traceroute adapter는 attempt execution, completed reachability, path quality를 별도 facts로 유지한다. timeout/cancel/command error가 붙은 parseable failed-attempt topology는 full raw attempts에만 남고 compact route/node/link/stat 및 Geo aggregate에서 제외된다. 별도 completed route가 있으면 full timeout/command-failure result가 optional representative topology를 유지할 수 있지만 generic runner-owned `cancelled`는 13 kind 모두 details를 금지한다. completed reached+unreached만 partial reachability를 지지한다.

GeoIP lookup은 프로세스 전체 active 8, queued 64로 제한되고 같은 IP generation의 waiter는 coalesce된다. abandoned generation은 successor를 지우거나 cache를 overwrite할 수 없다. 성공만 2,048-entry/24-hour LRU에 clone해 저장한다. 결과의 privacy-safe source/failure aggregate는 topology details에서 분석의 `coverage.enrichment` schema로 승격된다.

HTTP/1 timeout 모델은 `H=5s`, `B=30s`, `E=301s`, `R=5s`, `D=5s`이다. 따라서 accept 기준 `ReadTimeout=H+B=35s`, post-header `WriteTimeout=B+E+R=336s`이며 HTTP drain과 checker supervisor shutdown은 하나의 end-to-end `ShutdownTimeout=H+B+E+R+D=346s`를 공유한다. 정상 client deadline 315초는 `E+R=306s`에 DNS/connect/TLS/upload/download/scheduler용 9초를 더한 값이며 hostile slow-body `B`를 더하지 않는다. Go HTTP/2에서 `WriteTimeout`의 stream/deadline 의미는 HTTP/1과 같다고 보장할 수 없으므로 TLS gateway/실제 브라우저 조합을 별도 probe해야 하며, 이 산술식은 HTTP/1 server contract이다.

Operational handler는 business middleware의 바깥에서 exact GET/HEAD `/livez`와 `/readyz`만 처리하고 그 외 요청은 응답을 쓰지 않은 채 API boundary로 전달한다. Startup은 version prose 대신 trusted deployment `PATH`의 exact platform traceroute 실행 파일을 resolve한 뒤 `127.0.0.1` 대상 fixed grammar candidates를 하나의 2초 deadline과 32 KiB combined-output cap 안에서 기능 probe한다. Unix candidate는 common numeric `-n -q 1 -w 2 -m 30`과 `-n`을 지원하지 않는 구현용 `-q 1 -w 2 -m 30`이다. 성공한 exact path와 grammar를 private immutable capability로 캐시하고 readiness와 TracerouteChecker가 같은 capability를 사용한다. Executable 부재 또는 모든 grammar의 timeout/overflow/nonzero/unparseable/loopback 미도달은 nil capability와 `traceroute_unavailable`로 fail closed한다. Runtime PATH 재조회나 probe에서 선택하지 않은 grammar fallback은 없다. Web/Android screen/export는 exact fixed `Traceroute capability was unavailable, so no route observation was established.`만 사용한다. Probe destination은 loopback뿐이고 임의 target/GeoIP를 사용하지 않는다. Transient saturation은 readiness에 반영하지 않는다. drain transition은 irreversible phase를 먼저 닫는다. Operational readiness는 즉시 503이 되고 business request는 route와 public auth/rate를 지난 뒤 `server_draining`에서 닫혀 body/report/checker admission보다 앞서 종료된다. HTTP와 checker는 공유 346초 deadline으로 종료한다. Compose는 `/readyz`를 healthcheck로 사용한다.

Telemetry `outcome`은 delivery/lifecycle 전용이다. 완전한 HTTP 200 report write의 `report_finish`만 검증된 `report_status`, `analysis_verdict`, total/failed/finding counts를 조건부 확장한다. tuple이 없거나 모순이면 확장을 생략하고 base event는 유지한다. typed positive allowlist 외 raw request/diagnostic/provider/error 값은 구조상 입력할 필드가 없다.

Producer contract layer는 `findingRegistry` exact 23개, 31 result shapes와 expanded 136-row `(kind,status,error_code,details)` semantic compatibility matrix에서 `finding-contract-v1`을 생성한다. Producer가 matrix를 소유하고 Web/Android는 같은 checked-in fixture bytes와 exact eight producer traceroute witness fixtures를 strict parser/registry에 통과시킨다. `healthy`는 empty/absent `error_code`만 허용하며, known value로 구성됐어도 모순인 tuple/details는 UI/state publication 전에 report 전체를 atomic 거부하고 fixed local invalid-response만 게시한다. 별도 `BuildPresentationContract`는 실제 Runner/Analyze report와 coverage probes에서 exact 6 semantic keys, 10 evidence result kinds, 21 coverage signals, 23 action relationships, 33 scenarios를 생성하고 references/cardinality를 검증한다. 이 presentation 10은 31 result shapes와 별개다. API error도 `apiErrorRegistry`의 exact 16 definitions과 producer-derived structural mutation corpus 85 cases를 response writer, Web, Android가 공유한다.

Compact producer/consumers는 `compact-v1` object별 exact field closure를 공유한다. required collection은 non-null empty를 보존하고 optional absent와 present-invalid/null을 구분한다. public node Geo/ASN bundle은 field 단위가 아니라 one atomic bundle이며 Go JSON escape bytes가 4,096 이하면 유지하고 +1이면 거부/producer omission한다. ASN 정보가 없으면 empty object 대신 field를 생략한다.

Web presentation은 maximum 40-finding producer report에서 751-element initial DOM과 한 lazy panel을 연 784-element peak를 사용하며 global 1,200-element guard 아래 한 panel slot만 소유한다. Android Activity는 complete detached hierarchy를 만든 뒤 exact owner/generation을 확인해 single swap하고, post-swap visibility/status/focus/live/share publication이 실패하면 exact installed candidate를 rollback한다.

Android raw sharing은 Activity manager가 아니라 Application process singleton이 소유한다. single slot/no queue, one off-main worker, owner+listener generation, 8 MiB/file, 2 files/16 MiB live ceiling, 32-entry/32-delete incremental reconciliation을 사용한다. cleanup/path/revoke failure는 admission을 닫고 retry 전 secret owner/raw references를 버린다. FileProvider는 non-exported read grant뿐이며 15분 lease는 process observation 시점의 lifecycle이지 kernel/provider descriptor 강제 만료가 아니다. Activity는 current rendered READY와 runtime IDLE/GRANTED에서 새 raw-share 경고를 허용하고 매번 재확인을 요구한다. CLEANUP_FAILED의 별도 cleanup retry control만 기존 bounded runtime retry에 연결하며 준비 queue나 자동 retry를 추가하지 않는다.

Release identity는 tracked `VERSION=0.1.0`, exact revision ldflags, startup log, health DTO와 OCI labels를 한 source of truth로 묶는다. pinned builder/traceroute/runtime images와 checksum-pinned traceroute APK, empty build ID, trimpath, commit-derived epoch를 사용한다. API와 Web은 exact `linux/amd64`, required buildx `--no-cache --provenance=false`와 `--output=type=docker,...,rewrite-timestamp=true`로 각각 tagless archive를 두 번 만들고 archive bytes와 offline validator identity를 pass 간 비교한다. Web pinned nginx base도 project source와 분리된 mode `0700` generated context의 `FROM nginx@exactdigest`-only Dockerfile로 두 번 export한다. Revision-independent base epoch는 0이며 two-export byte equality와 exact config/layer/diff-ID/history/platform validation을 요구한다. API projector는 exact pinned Alpine layer/diff-ID/history prefix를 trusted base로 먼저 확정한 뒤 bounded layers에서 `/checknetwork-api`와 `/traceroute`만 선택하고, Web projector는 bounded layers에서 canonical nginx config/ten assets만 선택한다. Web outer reader는 physical archive 512 MiB와 별개로 regular-member declared bytes를 stream 순서에서 retain 전 512 MiB로 합산하며, exact Docker manifest→config/layers 및 optional coupled OCI index/layout→single manifest blob graph만 허용한다. Explicit blob ancestor directory만 허용하고 implicit tar parents는 요구하지 않아 unknown regular/directory/link/special과 compressed aggregate expansion을 함께 닫는다. 두 projector 모두 OCI whiteout을 layer-aware한 **layer-local marker set**으로 적용한다. root opaque는 모든 lower entry를, directory opaque는 해당 lower descendants를, ordinary whiteout은 lower target과 descendants를 제거하지만 같은 layer에서 생성한 entry는 marker의 tar 순서와 무관하게 보존한다. 최종 selected closure는 제거된 canonical file이 같은 layer 또는 이후 layer에서 재생성되지 않으면 실패한다. API 선택 파일은 caller-owned empty real directory에 dir-fd, no-follow, exclusive create, fsync 및 readback hash로 추출된다.

두 derived API/Web archive는 daemon에 load, daemon-tag, import, delete 또는 execute하지 않는다. 따라서 derived daemon image ownership/cleanup role은 없다. API가 소유하는 daemon role은 isolated API network와 smoke container 정확히 두 개이며 Web도 isolated internal network와 smoke container 정확히 두 개다. 각각 borrowed pinned Alpine/nginx base에 검증된 selected files를 read-only mount하므로 compatibility만 증명하고 derived archive 실행을 증명하지 않는다; borrowed base/cache는 삭제하지 않는다. 두 role set은 pending-before-create와 exact name+nonce+independent owner+immutable ID proof를 쓰며 normal response와 daemon-success/CLI-response-loss를 동일하게 닫는다. Cleanup은 exact-owned ID 제거 후 name absence까지 확인해야 완료되고, identity mismatch/replacement 또는 daemon/inspect/remove ambiguity는 caller를 보존한 cleanup failure다. Publication commit과 success output 앞의 barrier가 네 owned resource absence를 닫으며 실패 시 publication rollback과 nonzero를 강제한다. Ordinary failure와 HUP/INT/TERM 경계는 같은 idempotent EXIT path를 사용한다. fixed signal exit status는 129/130/143이며 cleanup 실패에도 변하지 않는다.

Stage 8 release review source는 base parent `36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243`에 대한 future external canonical manifest다. 최신 parent focused gates와 Task 1~15 task-level reviews는 blocker 0 / major 0이다. Stage 8 historical source cardinality는 findings 23/result shapes 31/expanded result matrix 136, presentation `6/10/21/23/33`, Web 260(456 semantic mutations including 13 generic cancelled-detail rejections), Android debug/release 각각 direct-child XML 297 + variant canaries 2, archive validators API 28 + Web 25 = 53, exact real release-gate fake cases 12다. Compose SemVer fix 뒤 현재 Web source inventory는 298 tests다. Production exact gate는 umask 077/027/000 세 pass와 canonical Docker inventory equality를 요구한다. 이 documentation edit 뒤 final clean-environment `make ci`는 pending이다. Repository root의 ignored `checknetwork-api` binary는 candidate manifest/검증에서 제외되어 있으나 비파괴 요청 때문에 제거하지 않고 retained 상태다. 그 뒤의 new manifest, manifest-derived temporary release, five-way review, Stage 8 commit, exact-SHA closure도 pending이다. 이전 임시 manifest version/record count/hash를 current evidence로 사용하지 않으며 worktree에서 final commit/SHA를 선제 주장하지 않는다.

Historical route-visual slice source inventory: **Web 328 tests** (pre-density route-visual 326 and ASN-context 320 are historical). Merged Geo/shared-prefix source inventory: **Web 620 tests**, plus two separate contract scripts (`app.test.js` and `common-prefix.test.js`). Geo inventories 308/304 and pre-Geo 298 are historical. Offline archive validators: API 33 + Web 30 = 63; production Web closure now contains **ten assets**, including self-contained `geo-map.js` and `common-prefix.js`. ASN context is presentation-only inference, never private-IP ownership or Geo enrichment. See [ASN context evidence](ASN-CONTEXT.md). Independent review/release remains pending.

## 일관성 기준

- 모든 공개 JSON 필드는 `snake_case`를 사용한다.
- 시간은 UTC RFC3339, 기간은 밀리초 정수로 표현한다.
- 모든 검사는 `context.Context` 취소와 타임아웃을 지킨다.
- 사용자에게 반환하는 실패는 안정적인 `error_code`와 설명을 함께 가진다.
- 한 검사 실패가 다른 검사를 중단시키지 않는다.
- 테스트는 외부 인터넷, 특정 DNS 설정, 관리자 권한에 의존하지 않는다.
- 로그에 요청 본문, 자격 증명, 전체 URL 쿼리를 남기지 않는다.
- compact response는 nodes 500, links 1,000, body 1 MiB 미만이고 Web commit은 chunk 100, document 1,200 elements를 넘지 않는다.
- Android response도 UTF-8 8 MiB에서 중단하고 strict report/analysis/compact schema를 atomic하게 검증한 뒤 현재 owner에게만 게시한다.

## 보안 기준

기본 API는 `127.0.0.1:8080`의 `trusted-local` 모드이며 사설망 진단을 허용한다. 공개 인터넷에서는 `public` 모드가 API key와 source IP rate limit을 요구하고, DNS 재해석과 HTTP redirect를 포함한 실제 dial 직전에 사설·loopback·link-local·metadata·특수 목적 주소를 차단한다. CORS는 인증으로 취급하지 않는다. 요청 본문은 1 MiB, 대상은 요청당 20개, 동시 report는 기본 4개로 제한한다. 운영에서는 TLS gateway의 인증/rate limit도 중첩한다.

- header parser는 64 KiB, accepted connection은 기본 128/hard max 256, body decode는 effective report default/hard max 64, report 설정 hard max는 16, checker는 기본 80/hard max 1,024, GeoIP는 active 8/queued 64/cache 2,048, response write hard max는 16이다.
- full response는 newline 포함 8 MiB 이하이고 compact response는 1 MiB 미만이다.
