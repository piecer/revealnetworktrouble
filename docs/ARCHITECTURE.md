# 아키텍처

현재 구조와 신뢰 경계를 설명한다. Stage별 당시 수치·승인 전 상태·기존 문구는 [구현 이력](IMPLEMENTATION-HISTORY.md)에 보존한다. 실행 설정은 [운영](OPERATIONS.md), wire schema는 [API](API.md), 검증 범위는 [테스트](TESTING.md)와 [CI](CI.md)를 따른다.

## 1. 시스템 구성

| 구성 | 실제 위치 | 책임 |
|---|---|---|
| 프로세스 진입점 | `cmd/checknetwork-api` | 환경 검증, traceroute capability probe, listener admission, readiness/drain/shutdown |
| HTTP 경계 | `backend/api` | route/auth/rate, bounded decode·admission·serialization, CORS, telemetry |
| 진단 엔진 | `backend/diagnostic` | Checker 실행·deadline·결과·deterministic Analyze·compact topology |
| Web | `frontend` | 정적 ESM 앱, strict response parser, request ownership, Canvas·semantic inspector |
| Android | `android/app` | Java Activity, core schema, network transport, state/session, private-cache 공유 |
| 운영·검증 | `scripts`, `Makefile`, `.github` | 로컬/hosted 공통 gate, archive 검증, 실패 신호·증거 보존 |

별도 `ReportService` 타입이나 `internal/api` 패키지는 없다. 현재 HTTP `api.Server`가 `diagnostic.Runner`를 직접 호출한다. `ServiceChecker`는 SSH/메일 계열 server-first greeting 검사이며 report orchestration 서비스가 아니다. `IPContextService`는 선택한 IP의 on-demand 부가 정보를 다루는 별도 경계다.

## 2. 진단 데이터 흐름

```text
# 실제 호출 순서; 분석은 Checker를 실행하기 전이 아니라 결과 수집 후 수행한다.
Web / Android / 다른 API client
  -> [Android: GET /api/v1/checks capability 사전 조회]
  -> POST /api/v1/reports (Web은 capability 사전 조회 없이 직접 요청)
  -> api.Server: auth/rate -> body decode -> validation -> report admission
  -> Runner.RunWithID -> CheckerSupervisor -> Checker.Check
  -> 결과/summary -> Analyze -> 선택적 compact topology
  -> API bounded serialization -> response-write admission
  -> client strict schema -> 현재 request owner에게만 publication
```

`Runner.finalizeReport`는 raw results로 summary와 `Analyze`를 만든 다음 compact graph를 생성한다. transport는 원본 report를 바꾸지 않는 응답 copy에서 raw attempts/대표 topology를 제거한다. compact 응답은 nodes 500, links 1,000, body 1 MiB 미만이고 full JSON은 newline 포함 8 MiB 이하로 제한한다.

보고서의 `healthy`/`degraded`/`unreachable`과 analysis의 설명은 서로 다른 층이다. 실패한 traceroute의 부분 출력만으로 도달 실패나 링크 장애를 확정하지 않는다. 실패 attempt는 full raw attempts에 남을 수 있지만 compact/Geo 관측 집계에 성공 경로처럼 합성되지 않는다.

## 3. Checker / Runner / Analysis 역할

- `Checker`는 `Kind()`와 `Check(context.Context, Target) Result`를 구현한다. DNS, TCP, HTTP(S), traceroute 및 SSH/메일 계열 검사는 이 seam으로 연결된다.
- `Runner`는 target validation, request budget, checker lease, 독립 결과 수집과 종료 시점 arbitration을 소유한다. 한 검사의 실패가 다른 검사 결과를 없애지 않는다.
- `Analyze`는 stable `error_code`와 typed normalized facts에 결정적 규칙을 적용한다. 자유 형식 서버 오류 문자열을 원인 증거로 파싱하지 않는다.
- producer의 finding/presentation/API-error fixtures를 Web/Android가 함께 소비한다. 알려진 값끼리라도 모순인 tuple/details는 report 전체의 publication을 거부한다.
- `confidence`는 장애 원인 확률이 아니라 해당 finding을 지지하는 근거 수준이다. observed/inferred/unavailable과 coverage limitation을 구분한다.
- 서비스 검사의 성공은 `server_greeting` 관측 범위다. 로그인·명령·STARTTLS·mailbox·end-to-end 서비스 동작 성공을 뜻하지 않는다.

## 4. 보안 신뢰 경계

직접 실행 기본값은 `127.0.0.1:8080`, `trusted-local`이다. loopback 밖 trusted-local bind는 정확한 `CHECKNETWORK_EXPOSE_TRUSTED_LOCAL=yes` 없이는 거부하고, 승인하면 인증 없는 노출 WARN을 남긴다. hostname을 loopback 예외로 간주하지 않는다. 기본 Compose는 컨테이너 내부 `:8080` 승인과 호스트 loopback publish를 분리한다.

`public`은 API key와 양수 rate limit을 요구하고 NetworkPolicy가 DNS 재해석·redirect를 포함한 실제 dial 주소를 검증한다. 사설·loopback·link-local·metadata·특수 목적 주소 접근을 차단한다. CORS는 인증이 아니다. rate-limit key는 실제 연결 `RemoteAddr`이고 `Forwarded`/`X-Forwarded-For`/`X-Real-IP`로 바꿀 수 없다. reverse proxy 뒤 사용자는 backend quota를 공유하므로 TLS·사용자별 인증/제한은 proxy에서도 필요하다.

Web Bearer는 API base별 현재 탭의 session storage에만 두고 report/export/URL에 넣지 않는다. Android도 credential과 raw report를 saved state에 직렬화하지 않는다. IP/Geo 부가 조회는 외부 공급자에게 조회 대상이 전달되는 별도 경계이며 offline 지도는 그 조회까지 없애는 기능이 아니다.

## 5. 동시성·시간제한 모델

| 자원 | 기본값 / 상한 | 소유·반환 경계 |
|---|---|---|
| accepted connection | 128 / 256 | handler 생성 전 lease, 실제 Close 때 반환 |
| body decode | effective report limit / 64 | auth/rate 뒤 JSON 한 객체+EOF decode 동안 |
| report | 4 / 16 | 검사와 bounded serialization까지 |
| checker | 80 / 1,024 | goroutine 생성 전 획득, 실제 return/panic 뒤 반환 |
| GeoIP | active 8, queued 64, LRU 2,048 | 동일 IP generation coalescing, 성공 cache만 보존 |
| response write | 최대 16 | report slot과 독립된 write token |

request는 최대 20 targets, traceroute는 target별 1~10 attempts다. checker timeout은 100~30,000 ms, report budget은 가장 긴 검사/반복 시간과 grace로 정하고 최대 301초다. Web/Android client deadline은 315초다. Android capability discovery와 report POST는 하나의 monotonic deadline을 공유한다.

HTTP/1 server의 source contract는 ReadTimeout 35초, WriteTimeout 336초, 공유 shutdown deadline 346초다. HTTP/2 gateway의 stream/deadline 의미를 같은 것으로 가정하지 않는다. context를 무시하는 goroutine을 Go가 강제 종료할 수는 없다. shutdown 후 남은 checker가 있으면 bounded active/stuck/remaining snapshot과 함께 실패 종료하며 late completion이 이미 반환한 report를 바꾸지 않는다.

## 6. Web/Android 구조

Web의 diagnostics와 topology는 독립 request lane이다. owner/signature와 generation 검증으로 이전 요청의 success/finalize, 입력 변경 뒤 callback, navigation/destroy 뒤 작업이 현재 화면을 덮지 못하게 한다. 동적 DOM은 active view가 소유한다. `MAX_TARGETS=20`, `MAX_DOCUMENT_ELEMENTS=1200`, render chunk 100 경계를 적용한다.

토폴로지는 기본 2D와 선택 가능한 3D perspective로 같은 검증된 facts를 그린다. mode는 report/analysis/diagnosis를 바꾸지 않는다. nodes 500/links 1,000/routes 1,000, DPR≤4를 적용하고 continuous animation 없이 interaction/resize에 반응한다. Canvas와 동기화된 bounded semantic inspector를 keyboard·screen-reader 경로와 Canvas 불가 시 fallback으로 유지한다. Geo 좌표와 ASN 문맥은 등록 정보/추정치이지 장비 소재지·소유권 증명이 아니다.

현재 `공통 구간`은 최대 64개 IP를 family별 CIDR/범위로 계산하는 로컬 기능이다. CIDR 일치가 실제 공통 route segment 관측을 뜻하지 않는다. 이전 README의 추가 경로 비교 초안은 [이력](IMPLEMENTATION-HISTORY.md)에 별도로 보존했다.

Android는 Activity presentation, `core`, `network`, `state`를 분리한다. `RequestCoordinator` owner와 `DiagnosticsSession` state identity가 취소/재생성 후 stale publication을 막는다. detached hierarchy를 검증 후 한 번 교체하고 publication 실패는 rollback한다. raw share는 Application process singleton의 single slot/no queue, off-main I/O, 8 MiB/file, 2 files/16 MiB, bounded reconciliation을 사용한다. non-exported `FileProvider`의 read grant를 쓰며 15분 lease는 프로세스가 관측할 때 revoke/cleanup하는 정책이다. 프로세스 종료 후 이미 열린 descriptor나 수신 앱 복사본의 강제 만료를 보장하지 않는다.

## 7. 확장 방법

새 검사 추가 시 기존 `Checker` 정의와 이웃 구현을 먼저 읽고 `buildCheckers` 등록, validation/capability, typed error/details, analysis 규칙, producer fixture, 두 client의 strict parser/presentation을 함께 검토한다. 외부 네트워크 없이 재현하는 timeout/cancel·malformed·최대 경계 테스트를 먼저 작성한다. 새 프런트엔드는 HTTP API를 사용하며 진단 엔진에 UI 의존성을 넣지 않는다.

새 정적 asset은 Docker COPY, checksum/mtime 목록, archive allowlist, smoke mount와 served-byte 검증까지 함께 갱신해야 한다. 릴리스는 pinned exact base와 tagless archive, 안전 추출, borrowed-base mount compatibility, 소유한 자원만 정리하는 계약을 유지한다. source scan의 통과를 별도 Docker compiler나 최종 이미지의 검증으로 대체하지 않는다.

## 8. 설계상 한계

- 진단의 관측 위치는 API 서버다. 원격 Web/Android 기기의 로컬 Wi-Fi·통신사 경로를 직접 측정하지 않는다.
- traceroute 무응답·RTT 차이·ASN/Geo 문맥은 원인 확정이 아니다. unavailable과 inferred를 숨기지 않는다.
- startup traceroute는 trusted deployment PATH의 실행 파일을 실제 loopback probe해 캐시한다. 임의 바이너리를 sandbox하는 기능이 아니며 nil capability는 readiness 실패다.
- raw 공유·로그·공급자 조회에는 개인정보 경계가 남는다. human redaction이 모든 민감 정보를 자동 식별한다는 보장은 없다.
- 기본 CI는 실제 Chrome/Firefox/Safari·screen reader·Android 실기기·TLS reverse proxy·registry 배포 검증을 대신하지 않는다.
- Docker builder와 offline validator는 CI와 같은 Go 1.26.10을 요구한다. exact image digest 고정과 최종 binary 취약점 검사는 별도 계약이며, source scan만으로 최종 산출물을 승인하지 않는다. [릴리스 경계](CI.md)를 확인한다.

Offline archive validators: API 33 + Web 30 = 63. 이는 archive 검증기 fixture 계층의 수치이며 현재 전체 테스트 합계가 아니다. 실행별 수치는 검증 기록을 기준으로 한다.
