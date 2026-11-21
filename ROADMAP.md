# CheckNetwork 프로젝트 검토 및 개선 로드맵

## 구현 진행 — 2026-11-15

원본 `PLAN.md`를 이 파일로 통합했다(원문 분석 기록은 [보관](docs/archive/PLAN-ANALYSIS-2026-11-21.md)). 아래 분석 본문은 작성 당시의 기준과 한계를 보존하며 원격 PR·CI 상태를 현재 시점에 다시 확인했다는 뜻은 아니다.

- 로컬 구현·검증: M-1, DOC-1 + M-2 일부, Android versionName 단일 출처화. 반영된 의존성 갱신의 CI/DOM/이미지 pin 불일치도 보완했다.
- 별도 후보에서 Docker 없는 `make test`, Compose Go/Web 통합 검사, race, syntax/vet/build와 독립 리뷰 통과. 후보에서만 기본 Compose 설정을 사용했으며 사용자 로컬 설정은 보존했다.
- 앞선 작업 트리의 Go 전체 테스트를 막던 로컬 Compose 계약 충돌은 사용자 승인에 따라 공통 파일을 복구해 해소했다. 기존 로컬 파일과 컨테이너는 별도로 보존한다. Android 추가 작업·검증은 사용자 요청으로 제외한다. 이전 Android 환경 차단과 traceroute 부재의 readiness 503을 이번 수정으로 해결했다고 표시하지 않는다.
- 기반 작업은 `857e67d`로 로컬 커밋했고 실제 커밋의 새 로컬 클론도 검증했다. 기존 Dependabot 7건은 모두 병합 상태였으며 추가 병합은 하지 않았다. Wrapper 검증기의 버전 불일치와 Java Properties/BusyBox NUL 파싱 우회를 보완하고 독립 재리뷰를 통과했다. [커밋·후속 확인](docs/archive/PHASE0-FOLLOWUP-2026-11-15.md)에 원격 조회 시점과 검증 범위를 기록했다.
- 사용자 승인 범위의 GitHub description/topics 변경은 적용 후 재조회했다. 비공개 신고는 이번 쓰기 전부터 활성화돼 있었고 브랜치 보호는 없는 상태임을 확인했으며 두 설정은 변경하지 않았다. 로컬 Compose 분리와 비Android 후속 검증은 [승인 범위 후속 기록](docs/archive/PHASE0-GO-WEB-FOLLOWUP-2026-11-15.md)에 남긴다.
- 이름·모듈 경로·주 배포 방향 확정은 사용자 요청으로 나중에 결정한다. Android는 현 작업 범위에서 제외하고 릴리스 발행과 Phase 1~4는 미완료다. 모듈 경로 일치 등의 원래 수용 조건이 남으므로 **Phase 0 전체 완료로 표시하지 않는다.**
- 실행 범위·원본 로그·보관 경로: [Phase 0 구현·검증 기록](docs/archive/PHASE0-VERIFICATION-2026-11-15.md). 과거 기록 목록은 [보관 안내](docs/archive/README.md)를 따른다.

## Phase 1 진행 — 2026-11-17

- U-1의 추가 단계: `CHECKNETWORK_WEB_UI=on`으로 같은 바이너리·listener에 정적 UI를 내장하고 API 주소를 브라우저의 현재 origin으로 고정했다. 기본값은 `off`로 유지하며 기존 standalone Web/API 두 구성과 로컬 배포 파일은 바꾸지 않았다.
- S-1: 내장 UI에 CSP·nosniff·frame/referrer 정책과 명시적 production asset 경계를 적용했다. 기존 API의 public 인증·rate limit·JSON 오류와 operational probe는 유지한다. standalone nginx의 보안 정책 전환은 포함하지 않는다.
- 실제 Go/Web·race·구문/vet/build·Compose 설정 검증과 로컬/실제 Docker 아카이브 추출 바이너리의 Chromium 검증을 실행했다. 독립 리뷰의 차단 finding은 없었으며 기존 standalone 브라우저·비Android CI 정책도 통과했다. 최종 범위와 증거는 [내장 UI 검증 기록](docs/archive/PHASE1-EMBEDDED-UI-2026-11-17.md)에 구분한다.
- U-1의 기본값 전환·exact-SHA 이중 이미지 릴리스와 R-2는 남아 있다. U-2·U-3 구현은 완료(commit까지만 기록; push는 사용자 명시적 지시가 있을 때만). Android 제외, 이름·모듈 경로 보류, 운영 재배포·태그·릴리스 발행 제외 범위를 유지한다. Phase 1 전체 완료가 아니다.

## 분석 당시의 로드맵

- 대상: <https://github.com/piecer/revealnetworktrouble> (제품명 CheckNetwork)
- 작성일: 2026-11-15
- 분석 기준: `main` @ `129fc68` (2026-11-11 커밋)
- **분석 범위 주의:** 열려 있는 PR #8(`verify/presentation-20261114-7386c9d`, 11-14 생성)의 변경은 포함하지 않았습니다. 이후 해당 PR이 병합되면 일부 항목은 달라질 수 있습니다.

---

## 1. 한 장 요약

**평가:** 진단 엔진(Go API)의 안전성·정확성·테스트는 이미 상당히 탄탄합니다. 반면 "처음 쓰는 사람이 몇 분 안에 가치를 얻는 경로", "결과를 한눈에 이해하는 화면", "저장소의 정돈 상태"는 엔진 품질에 못 미칩니다. 다음 단계의 레버리지는 새 진단 기능이 아니라 **첫 사용 경험 → 결과 표현 → 정리·릴리스** 순서입니다.

**가장 중요한 개선 3가지**

1. **첫 실행 마찰 제거 (U-1).** UI와 API가 분리돼 있고 포트가 3종(9090/8080/8090)이며, 소스 실행 시 UI에서 API 주소를 손으로 바꿔야 합니다. 이미 `go:embed`를 쓰고 있으므로 정적 UI를 API 바이너리에 내장해 "명령 1개, 포트 1개"로 만들 수 있습니다.
2. **결과 화면을 사람 중심으로 (U-2).** 한국어 UI 안에 영어 판정 문장이 크게 노출되고 `coverage_limitation` 같은 내부 식별자가 제목으로 보입니다. "정상/주의/실패 한 줄 + 원인 후보 + 다음 단계"로 재구성하고 면책·커버리지는 접어야 합니다.
3. **정리와 첫 릴리스 (DOC-1, R-1, R-2).** 마크다운이 약 452KB(Go 프로덕션 코드 약 11.5k줄 대비 과다)이고 작업 기록이 루트에 섞여 있습니다. 모듈 경로·저장소명·제품명이 서로 다르고, 태그·바이너리·이미지가 없어 `v0.1.0`을 쓸 수 없습니다.

**의사결정이 먼저 필요한 것:** 이 도구의 주 사용자와 배포 형태(7절). 같은 계획이라도 "개인·소규모 운영자가 로컬에서 실행"이냐 "공개 호스팅"이냐에 따라 우선순위가 달라집니다.

---

## 2. 검토 방법과 한계

직접 실행해서 확인한 것과 코드·문서로만 확인한 것을 구분했습니다.

| 항목 | 결과 | 비고 |
|---|---|---|
| `go build ./...`, `go vet ./...` | 통과 | Go 1.22.2 (`go.mod` 최소 버전). CI는 1.26.11 사용 |
| `go test ./...` | `backend/api` 통과 / `backend/diagnostic` 1건 실패 / `cmd/checknetwork-api` 3건 실패 | 4건 모두 환경 의존. 상세는 M-1 |
| `npm test` (Node 22.23) | 892 tests 중 891 pass, 1 skip, 0 fail, 약 64초 | 스크립트형 2종(`app.test.js`, `common-prefix.test.js`)이 먼저 통과 |
| API 로컬 스모크 | 통과 | TCP·HTTP·DNS·닫힌 포트 4건을 한 번에 요청, 4ms, 응답 2.4KB. CORS 허용 목록 동작, `no-store`·`nosniff` 확인 |
| UI 확인 | `docs/images/diagnostics.png` 검토 | U-2 근거 |
| 원격 상태 | Actions 26회 실행, PR 8건 모두 open(병합 0) | 비로그인 웹 페이지로 확인. 브랜치 보호·보안 신고 설정은 **확인 불가** |
| **미검증** | Android 빌드·테스트(SDK 없음), Docker/Compose 실행, 브라우저 게이트(Chromium), 외부망 대상 실제 진단·traceroute 종단 실행, 외부 GeoIP/RDAP 호출 | 이 항목들에 대한 결론은 코드·문서 기반의 추정입니다 |

---

## 3. 현황

**규모**

| 구분 | 규모 |
|---|---|
| Go | 프로덕션 약 11.5k줄 / 테스트 약 22.2k줄 (테스트 함수 529개) |
| Web | JS(테스트 포함) 약 15.9k줄, 빌드 스텝 없는 정적 ES 모듈, 런타임 의존성 0 |
| Android | Java 약 17.5k줄(테스트 포함), 외부 의존 `androidx.core` 1개 |
| 문서 | 마크다운 약 452KB (README 한국어 19KB / 영어 6.5KB) |
| 검증 스크립트 | `scripts/` 36개 파일 |

**구조:** Go API(표준 라이브러리만 사용, 13종 검사: DNS·TCP·HTTP·HTTPS·SSH·SMTP·Submission·SMTPS·IMAP·IMAPS·POP3·POP3S·traceroute) → 분석(verdict/findings/evidence/actions/coverage) → Web 5개 화면(통합 진단, 경로 토폴로지, Geo 지도, IP 라벨, 공통 구간) 및 Android 클라이언트.

**강점 (유지해야 할 것)**

- 서드파티 Go 의존성이 없고(`go.mod`에 `require` 없음), 웹 런타임 의존성도 없습니다. 공급망 표면이 매우 작습니다.
- 방어적 설계가 일관됩니다: public 모드 SSRF 정책(DNS 재해석·redirect 포함), 동시성·본문·연결 수 상한, 정상 종료, `/readyz`, 비루트 컨테이너(65532), digest 고정 이미지, 재현 가능 빌드, CI의 govulncheck·gitleaks.
- 결과 표현이 정직합니다. "정상은 검사 범위 내 정상", "confidence는 확률이 아님", "관측 지점은 API 서버"를 일관되게 명시합니다. 이것은 차별점이므로 지키되 **표현 방식**만 개선하면 됩니다(U-2).
- 스모크 테스트에서 API 계약이 안정적이었습니다. 30일 이내 인증서 만료 경고(`FindingTLSCertificateExpiring`) 같은 실용 규칙도 이미 있습니다.
- 원격 CI가 실제로 동작 중입니다(PR·수동 실행 다수 성공). 단, 저장소 내부 문서(`PLAN_PROGRESS2.md`)는 "workflow 0건"으로 적혀 있어 현실보다 낡았습니다(R-4).

---

## 4. 검토 결과

우선순위: **P1** = 사용자 가치·신뢰에 직접 영향 또는 다른 개선의 선행 조건 / **P2** = 중요하지만 우회 가능 / **P3** = 개선 사항.
근거 표기: **[실행]** 직접 실행·측정, **[코드]** 코드·설정 확인, **[문서]** 문서 기재, **[추정]** 추론.

### 4.1 사용성·제품

#### U-1. 첫 실행 마찰 (P1)
- **현상:** 소스 실행은 터미널 2개(API + `python3 -m http.server`)가 필요하고, UI의 기본 API 주소는 `http://localhost:9090`인데 실제 포트는 소스 실행 8080, Compose 8090입니다. 직접 실행하면 UI에서 주소를 수동 변경해야 하며 README가 이를 직접 안내합니다. 웹과 API가 분리돼 CORS 허용 목록도 맞춰야 합니다.
- **근거:** [코드] `frontend/index.html:28`, `cmd/checknetwork-api/main.go:252`, `compose.yaml`. [문서] README "빠른 시작" 표.
- **부수 문제:** Compose는 `ALLOWED_ORIGINS=http://localhost:3000`만 허용해 `http://127.0.0.1:3000`으로 접속하면 CORS로 막힐 수 있습니다(소스 실행 기본값은 둘 다 허용 [실행]). [코드]
- **권고:** 정적 UI를 `go:embed`로 API 바이너리에 내장하고 동일 출처(`/`)로 서빙. 기본 API 주소를 `location.origin`으로 두면 포트 혼동·CORS가 사라집니다. 별도 호스팅은 옵션으로 유지.
- **주의:** 릴리스 검증 스크립트(`verify_web_archive.py`, `docker_inventory.py` 등)가 "API 이미지 + Web 이미지" 2구성을 전제로 합니다. 처음에는 **내장 모드를 추가**하고 기존 구성은 유지한 뒤, 검증 체계를 맞춰 기본값을 전환하세요.

#### U-2. 결과 화면의 언어·표현 (P1)
- **현상:** 한국어 메뉴·제목 안에 영어 판정 문장(예: "All configured checks completed within their observed scope; no broader service-health claim is made.")이 가장 큰 글씨로 표시됩니다. 요약 라벨도 영어(`Checks`, `Completed observations`, `Observation point`)이고, `coverage_limitation` 같은 내부 코드가 섹션 제목으로 노출됩니다. 정상 결과인데도 면책 문장이 헤드라인이라 "그래서 되는 건가?"를 즉시 얻기 어렵습니다.
- **근거:** [실행] `docs/images/diagnostics.png`, API 응답의 `title/summary/step`이 영문. [코드] `frontend/app.js:588`, `<html lang="ko">`.
- **권고:**
  1. 상단에 **상태 한 줄**("정상 — DNS·HTTPS 모두 응답", "주의 — 대상 포트 연결 실패")을 크게.
  2. 그 아래 원인 후보 1~3개와 **다음 확인 단계**.
  3. 면책·커버리지·원시 결과는 접힘(progressive disclosure). 정직성 원칙은 유지하되 첫 화면의 비중을 줄입니다.
  4. API는 안정적인 `code`와 영문 fallback을 유지하고, **UI·Android는 `code` 기반 ko/en 사전**으로 번역. 내부 코드는 사용자에게 직접 노출하지 않습니다.
- **연관:** `README.en.md`가 있어도 UI가 한국어 위주라 영어 사용자는 결과를 읽기 어렵습니다(DOC-2).

#### U-3. 증상 기반 진입점 부재 (P2)
- **현상:** 사용자가 검사 종류와 주소를 직접 골라야 합니다. `docs/REVIEW_2026-09-01.md`가 "증상/영향 입력 또는 목적 기반 preset"을 목표 여정 1번으로 적었지만 원래 UI에 preset이 없었습니다. [구현: `frontend/lexicon.js`의 `SYMPTOM_PRESETS`(code-keyed) + `app.js`의 `renderSymptomPresets`/`applySymptomPreset`]
- **권고:** "웹사이트가 안 열려요 / 메일이 안 가요 / SSH 접속이 안 돼요 / 특정 서버까지 느려요" 프리셋. 호스트 하나만 입력하면 DNS → TCP → HTTPS(+TLS) → traceroute를 묶어 실행하고 결과를 증상 기준으로 요약.
- **구현 범위(2026-11-22):** 프리셋 4종(웹사이트/메일/SSH/느림)을 사전 코드 매핑으로 도입. 프리셋 클릭은 요청을 실행하지 않고 검사 행을 계약 검사 종류로 사전선택합니다(호스트 자동 입력과 증상 기준 요약은 미완).

#### U-4. 관측 지점 한계 (P2, 전략)
- **현상:** 모든 검사는 API 서버에서 수행됩니다. 가장 흔한 요청인 "내 PC/폰 네트워크가 이상하다"를 원격 서버로는 진단할 수 없습니다. README는 이를 정직하게 밝히고 있으나 제품 가치의 가장 큰 한계입니다. [문서]
- **권고:** (단기) U-1의 단일 바이너리를 "**문제가 있는 PC에서 직접 실행하는 도구**"로 포지셔닝하고 README 첫 시나리오로 안내. (중기) Android 앱을 "내 네트워크에서 실행되는 프로브"로 위치 정리. (장기) 경량 프로브/에이전트 모드(Phase 4). 다중 관측 지점은 기존 `PLAN.md`가 릴리스 범위 밖으로 둔 항목입니다.

### 4.2 진단 기능

#### D-1. DNS 진단이 얕음 (P1)
- **현상:** DNS 검사는 시스템 리졸버로 `LookupHost` 한 번(약 35줄)입니다. 레코드 타입(MX/TXT/CNAME/NS), 리졸버 간 비교, 오류 세분화가 없습니다. 장애 분류의 첫 분기가 "DNS 문제인가?"인 만큼 근거가 부족합니다. [코드: `backend/diagnostic/dns.go`]
- **권고:** (a) 시스템 vs 공용 리졸버(예: 1.1.1.1, 8.8.8.8) 응답 비교 — 결과가 다르면 "로컬 리졸버 문제" 후보로 분석. (b) MX/TXT/CNAME/NS 선택 조회. (c) NXDOMAIN/SERVFAIL/timeout 구분과 응답 시간. 표준 라이브러리의 `net.Resolver{PreferGo: true, Dial: …}`로 서드파티 없이 가능한지 먼저 설계 검토하세요(미검증).

#### D-2. 지연 단계 분해 없음 (P2)
- **현상:** `httptrace`는 TLS 핸드셰이크 완료 관측에만 쓰이고, 결과는 단일 `latency_ms`입니다. DNS·연결·TLS·첫 바이트 시간이 분리되지 않아 "느림"의 원인을 말하기 어렵습니다. [코드: `http.go:24`, `:135`]
- **권고:** HTTP(S) 결과 `details`에 단계별 시간을 추가(기존 필드는 유지, additive). 분석 규칙에 "연결은 빠르나 TTFB 지연" 같은 후보 추가.

#### D-3. 빠진 신호 (P2)
- **현상:** ICMP ping/패킷 손실·지터, IPv4 vs IPv6 비교, MTU가 없습니다. [코드: 해당 로직 없음] 이는 `PLAN.md`가 의도적으로 릴리스 이후로 미룬 범위입니다.
- **권고(우선순위순):** ① **TCP 반복 연결 N회로 성공률·RTT 분포·지터**(권한 불필요, 의존성 0). ② IPv4/IPv6 family별 연결 비교. ③ ICMP ping은 권한·플랫폼 차이와 `golang.org/x/net` 도입 여부(7절 결정 사항)에 따라 이후 단계.

#### D-4. 단일 GeoIP 공급자 고정 (P2)
- **현상:** 기본 공급자가 `ipwho.is`이고 결과 검증 코드가 `Provider == "ipwho.is"`를 요구해 URL만 바꿔서는 다른 공급자로 교체하기 어렵습니다. 경로의 공인 IP가 제3자로 전송되며, 끄는 설정은 찾지 못했습니다(URL 변경만 가능). 오프라인·폐쇄망에서는 지도 기능이 비게 됩니다. [코드: `geo_details.go:145`, `main.go:253`]
- **권고:** 공급자 인터페이스화, `CHECKNETWORK_GEOIP=off` 스위치, 선택적 오프라인 DB(MaxMind GeoLite2 / DB-IP Lite) 지원. 개인정보 경계 문서(`docs/IP-CONTEXT*.md`)와 함께 갱신.

#### D-5. 보고서 이력·비교 없음 (P3)
- **현상:** 상태 비저장입니다. "아까는 됐는데" 시나리오(회복 확인·기준선 비교)를 지원하지 못합니다. [문서: `REVIEW` 목표 여정 7번]
- **권고:** 서버 저장 없이 **클라이언트 측**(IndexedDB 또는 JSON 파일)에서 이전 보고서와 diff. 개인정보 부담이 적습니다.

### 4.3 구조·유지보수

#### M-1. 클린 환경에서 `go test ./...`가 실패 (P1)
- **현상 [실행]:**
  - `TestRunTraceCommandNormalParentExitKillsChildAndGrandchildHoldingPipes` — 자식 프로세스는 실제로 종료됐지만 PID 1이 수거하지 않아 좀비(`Z`)로 남았고, 헬퍼 `processPIDExists`가 `kill(pid, 0)`만 확인해 **좀비를 생존으로 오판**했습니다(3회 재실행 모두 동일). `docker run`을 `--init` 없이 쓰는 환경에서도 같은 문제가 날 수 있습니다. [추정]
  - `TestComposeResolvedExposureBoundary`, `TestComposeResolvedBuildIdentityIsValidSharedAndOverrideable` — `docker` 바이너리가 없으면 skip이 아니라 **실패**.
  - `TestCleanArchiveDisablesAmbientVCSStampingWithoutDroppingCallerGOFLAGS` — 오래된 로컬 Go에서 더 높은 toolchain을 내려받으려다 외부망 차단 시 실패.
- **문제:** README는 `make test`에 Go·Node·npm·make만 요구한다고 안내하지만 실제로는 Docker와 외부망이 필요합니다. 기여자가 클론 직후 빨간 테스트를 보게 됩니다.
- **권고:** Docker·외부망 의존 테스트는 `exec.LookPath("docker")` 실패 시 `t.Skip` 하거나 `//go:build integration` 태그로 분리. `processPIDExists`는 `/proc/<pid>/stat`의 상태가 `Z`면 종료로 간주. `make test`(빠른 계층)와 `make test-integration`을 문서에 구분.

#### M-2. 계약 테스트·검증 체계의 과중함 (P2)
- **현상:** Go 테스트 코드가 프로덕션의 약 1.9배이고, 일부 테스트가 **문서 문구 자체를 고정**합니다. `documentation_contract_test.go`(858줄)는 "Offline archive validators: API 33 + Web 30 = 63" 같은 산문 속 숫자를 단언하고, 숫자가 낡았는지 탐지하는 테스트(`TestStaleArchiveValidatorClaimDetection` 등)까지 있습니다. 문서 한 줄을 고치려면 테스트도 고쳐야 합니다. [코드]
- **영향:** 문서 정비(DOC-1)와 기능 개선 속도를 모두 떨어뜨립니다.
- **권고:** 문서 테스트는 링크 무결성·필수 파일 존재·코드와 직접 대응되는 사실(API 오류 코드 목록 등)로 축소. 테스트 개수 같은 변동 수치는 문서에서 제거하거나 CI가 생성. PR 단위 CI는 Quick 계층에 집중하고 exact-SHA 재현 검증은 릴리스 후보에서만 수행(현재 구조를 유지하되 의무 범위만 조정).

#### M-3. 타입 없는 `Details`로 인한 분석 코드 비대 (P2)
- **현상:** `Result.Details`가 `map[string]any`라서 `analysis.go`(1,309줄)의 상당 부분이 **서버가 직접 만든 값**을 다시 untyped map에서 꺼내 검증하는 코드입니다(`normalizeResults`, `stringDetail`/`integerDetail`/`timeDetail` 등). [코드]
- **권고:** 검사 종류별 typed 구조체를 내부 표현으로 도입하고 JSON 출력만 기존과 동일하게 유지. `testdata/`의 골든 픽스처(42개)가 호환성 안전망이 됩니다. 컴파일 타임 보장과 분석 코드·테스트 축소가 기대 효과입니다. 다만 일부 검증은 모순된 입력을 걸러내려는 **의도된 방어 로직**일 수 있으므로, 제거 전에 `docs/API.md`·`finding-contract` 픽스처의 계약을 확인하세요. (Web·Android는 별도 검증 필요하므로 wire 형식 불변을 원칙으로.)

#### M-4. 프런트엔드 모놀리식 구조와 수작업 테스트 목록 (P2)
- **현상:** `app.js` 1,906줄(함수 97개, 최대 약 170줄), `state.js` 1,798줄. `package.json`의 `test`와 `test:syntax`가 파일 목록을 **수작업으로 나열**(`node --check` 40여 개)해, 새 파일을 추가하고 목록에 넣지 않으면 조용히 검증에서 빠집니다. [코드]
- **권고:** `node --test "*.test.js"` 글롭 + eslint(또는 `node --check` 글롭)로 대체. `app.js`를 화면별 모듈(diagnostics·topology·geo·labels·prefix)로 분리. "빌드 없는 정적 구성"은 강점이므로 유지.
- **참고:** 현재 `innerHTML` 사용이 없고 안전한 DOM 구성을 쓰고 있어 XSS 측면은 양호합니다. [코드]

#### M-5. Android 정리 (P3)
- **현상:** `versionName "1.0.0"`이 저장소 `VERSION 0.1.0`과 불일치합니다. Dependabot의 Android 업데이트 PR 4건(AGP 8.9.1→9.5.1, Gradle wrapper 8.11.1→9.9.0, androidx.core 1.15.0→1.19.1, Robolectric 4.14.1→4.17)이 방치돼 있습니다. CI는 통과했지만 AGP·Gradle은 메이저 업데이트입니다. 실기기·에뮬레이터 수용 검증은 문서상 미완료입니다. [코드·원격·문서] (이번 분석에서 Android는 빌드·실행하지 못했습니다.)
- **권고:** 버전 단일 출처화, 메이저 업데이트는 한 번에 하나씩 검증 후 병합, 릴리스 전 에뮬레이터 1종 + 실기기 1종 체크리스트 수행.
- **업데이트 [실행 2026-11-21]:** Full gate `android-test` 실패를 두 layer로 진단. Layer 1: AGP 9 기본값 `android.onlyEnableUnitTestForTheTestedBuildType=true`가 `:app:testReleaseUnitTest` task를 없앰 — gate가 debug·release 결과를 모두 세므로 `android/gradle.properties`에 explicit pin + `scripts/test_ci_definition.py` 회귀 test로 해결(run 44904791766에서 Layer 1 통과 확인). Layer 2: `androidx.core 1.19.1`(이미 병합)이 compileSdk ≥ 37을 요구하는데 프로젝트는 `compileSdk = 35`와 gates(platform android-35, build-tools 35.0.0, env script·bootstrap fixtures·README)에 고정. 방향 결정 대기(compileSdk 상향 동기화 / bump 롤백 / Full gate에서 android-test 제외).

### 4.4 보안·운영

#### S-1. Web 보안 헤더 부재 (P2)
- **현상:** API는 `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`를 보냅니다 [실행]. 그러나 Web 서버 설정(`frontend/nginx.conf`)에는 CSP·`X-Frame-Options`·`Referrer-Policy`·`nosniff`가 없습니다. `PLAN.md`는 CSP를 원래 계획 밖으로 명시했습니다. [코드]
- **권고:** 공개 호스팅 가능성이 있으므로 재검토. `index.html`에 인라인 스크립트가 없고(`<script type="module" src>` 1개) 인라인 `style=` 속성도 1곳뿐이라 엄격한 CSP 적용 난도가 비교적 낮을 것으로 보입니다. [추정: 런타임 스타일 주입은 확인하지 못함] U-1로 UI를 API에 내장하면 Go 쪽 미들웨어에서 한 번에 설정할 수 있습니다.

#### S-2. 인증 모델 단순 (P2, 용도에 따라)
- **현상:** public 모드는 단일 공유 API 키 + 전역 rate limit이며, 프록시 헤더를 신뢰하지 않으므로 프록시 뒤에서는 클라이언트를 구분하지 못합니다(`SECURITY.md`가 명시). 사용자별 키·쿼터·감사 로그가 없습니다. [코드·문서]
- **권고:** 사내·개인 용도라면 현재로 충분. 공개 서비스를 목표로 한다면 다중 키·키별 한도·요청 로그 최소 필드부터 설계. (7절 결정 사항에 따름)

#### S-3. 보안 신고 채널 미확정 (P3)
- **현상:** `SECURITY.md`가 비공개 신고 채널이 "확정되지 않았다"고 기재합니다. 11-11 시점 기록으로는 private vulnerability reporting이 꺼져 있었습니다. 현재 상태는 확인하지 못했습니다. [문서]
- **권고:** GitHub 설정에서 활성화(수 분 작업)하고 `SECURITY.md`를 갱신.

### 4.5 릴리스·프로젝트 운영

#### R-1. 이름·모듈 경로 불일치 (P1)
- **현상:** 모듈 `github.com/network-troubleshooting-company/checknetwork` ≠ 저장소 `piecer/revealnetworktrouble` ≠ 제품명 CheckNetwork ≠ Android `com.checknetwork.app`. 현재 모듈 경로로는 `go install github.com/piecer/revealnetworktrouble/...@latest`를 쓸 수 없습니다. [코드]
- **권고:** 이름 확정 후 모듈 경로·저장소 설명·바이너리명·패키지명을 정렬. 모듈 경로 변경은 import 일괄 치환이라 **일찍 할수록 저렴**합니다(문서 계약 테스트의 경로 문자열도 함께 수정).

#### R-2. 릴리스 산출물 없음 (P1)
- **현상:** 태그·GitHub Release·사전 빌드 바이너리·컨테이너 이미지가 없어 사용자는 Docker 빌드(Go 컴파일 + Alpine 패키지 다운로드) 또는 Go 설치 후 소스 빌드가 필요합니다. `VERSION=0.1.0`은 "소스 버전일 뿐 발행 아님"으로 문서화돼 있습니다. [문서·원격]
- **권고:** Release candidate 워크플로가 이미 있고 11-14에 성공했습니다. 이를 이어 `v0.1.0` 태그 + 플랫폼별 바이너리(linux amd64/arm64, macOS arm64, windows amd64)·체크섬·GHCR 이미지 발행.

#### R-3. Dependabot PR 적체 (P2)
- **현상:** PR 7건(#1~#7)이 11-11부터 미병합. 2건은 CI 실패(#1 nginx digest, #4 jsdom 30→31)이고, Android 4건 중 AGP 8→9·Gradle 8→9는 메이저 업데이트입니다(나머지 둘은 마이너). [원격]
- **권고:** 주 1회 triage 규칙, 실패 PR은 원인 확인 후 닫거나 수정, 개발 의존성은 그룹화.

#### R-4. 문서의 원격 상태 기술이 낡음 (P2)
- **현상:** `PLAN_PROGRESS2.md`는 11-11 22:11 기준 "workflow/run 0건, protected=false"로 기록하지만, 11-14에는 CI·릴리스 후보 워크플로가 다수 성공했습니다. 브랜치 보호(required check) 현황은 이번에 확인하지 못했습니다. [문서·원격]
- **권고:** "현재 상태" 서술은 문서에 박지 말고 배지·링크로 대체. 브랜치 보호는 설정을 직접 확인.

#### R-5. 저장소 메타 (P3)
- **현상:** Description·Topics·Website가 비어 있습니다(첫 페이지에서 확인). 발견성이 낮습니다. [원격]
- **권고:** 한 줄 설명, topics(`network-diagnostics`, `traceroute`, `troubleshooting`, `golang` 등), 스크린샷 포함 README 상단 요약.

### 4.6 문서

#### DOC-1. 문서 과적재와 작업 기록의 혼재 (P1)
- **현상:** 루트에 `PLAN.md`, `PLAN_PROGRESS2.md`(46KB), `SPEC.md`(48KB)가 있고 `docs/`에 `PLAN.md`(과거 Stage 기록), `IMPLEMENTATION-HISTORY.md`(52KB), `PHASE1/2-VERIFICATION.md`, `REVIEW_2026-09-01.md`, `README-TECHNICAL-NOTES.md` 등 **작업 기록과 현재 문서가 섞여 있습니다.** `docs/PLAN.md`는 "현재 단계: Stage 9"를 말하지만 같은 파일 안에 Stage 8과 "Historical record" 주석이 겹쳐 있어 무엇이 현재 사실인지 알기 어렵습니다. [코드·문서]
- **권고:** `docs/archive/`로 이동(삭제가 아님, git 이력 보존)하고 루트에는 `README / CONTRIBUTING / SECURITY / CHANGELOG / LICENSE / ROADMAP`만 둡니다. **이 문서를 `ROADMAP.md`로 대체**하여 `PLAN.md`·`PLAN_PROGRESS2.md`·`docs/PLAN.md`의 역할을 하나로 합치는 것을 제안합니다. 단, M-2의 문서 계약 테스트가 파일 위치·문구를 단언하므로 이동과 테스트 축소를 **같은 PR**에서 처리하세요.

#### DOC-2. 영어 문서의 실효성 (P3)
- **현상:** `README.en.md`는 섹션 8개로 `README.md`(16개)의 일부만 담고, 영어 사용자가 열어도 UI·분석 문구가 한국어 위주입니다.
- **권고:** U-2의 ko/en 사전 도입과 함께 영어 README를 "빠른 시작 + 한계 + API 링크" 최소 구성으로 동기화.

---

## 5. 개선 로드맵

원칙: ① 새 기능보다 **첫 사용 경험과 결과 이해도** 우선 ② 기존의 안전·정확성 계약은 **깨지 않기**(골든 픽스처와 기존 테스트가 안전망) ③ 큰 변경은 "추가 → 검증 → 기본값 전환" 순서 ④ 정리는 삭제가 아니라 **이동·통합**.
규모는 추정치이며 1인 작업 기준 인일입니다.

### Phase 0 — 정리와 기반 (1주, 약 3~4인일)

목표: 기여자·사용자가 클론 직후 막히지 않고, 이후 변경이 싸지는 상태.

- [x] M-1: Docker·외부망 의존 테스트 skip/태그 분리, 좀비 판정 수정, `GOTOOLCHAIN` 안내
- [ ] R-1: 이름·모듈 경로 확정 및 일괄 치환(결정 1) — 사용자 요청으로 보류
- [x] DOC-1 + M-2(일부): `docs/archive/` 이동, 루트 문서 정리, 문서 계약 테스트 축소(같은 변경 묶음, PR 미생성)
- [x] R-3: Dependabot 7건 상태·위험 분류 — 모두 기존 병합 확인, #1/#4 회귀 및 #5 Wrapper 검증기 보완. Android 전체 통합 검증은 별도 미완료
  - [x] R-3 2차 triage (2026-11-21): #10(robolectric 4.18)·#11(jsdom 31.0.1) 병합. #12(Gradle 9.9.1)는 checksum을 Gradle 공식 endpoint에서 독립 확인(9.9.1 bin `d8a18a7a…`=CI 실제값; wrapper jar `621008…`은 버전 독립이라 불변) 후 pin·검증기·test·provenance README 동기화로 content already reflected 처리. #9(Alpine)는 `d9e853e`=3.20.10 → `f59abe6`=3.24.4의 major 라인 Upgrade로 독립 재감사(amd64 layer `881fc932…`, diff-ID `75f1d965…`, history created 2026-11-05, config `3ea06e4c…` 확인) 후 재감사 값 반영으로 처리. pin 기반 identity bump은 같은 변경 묶음의 mirror 변경(검증기·contract test)이 필요 — 단파일 PR은 설계상 통과 불가
- [x] S-3·R-5: private vulnerability reporting 활성 상태·브랜치 보호 현황 확인, 승인된 Description·Topics 적용 후 재조회. 보안 신고/브랜치 보호 자체의 변경은 없음
- [x] M-5(일부): 이전 Android 버전 단일 출처화 — 패키징 manifest 검증. 이후 Android 작업·검증은 사용자 요청으로 제외
- [ ] M-5 잔여(Full gate red): Layer 1(task model)은 pin + 회귀 test로 해결·push 완료. Layer 2(compileSdk)는 방향 결정 대기

**완료 기준:** Docker·외부망이 없는 새 머신에서 `git clone && make test`가 통과하고, 루트 마크다운이 6개 이내이며, 모듈 경로가 저장소와 일치.

### Phase 1 — 첫 사용 경험 (2~3주, 약 8~10인일)

목표: 새 사용자가 **명령 1개로 3분 안에 첫 진단 결과**를 보고 이해한다.

- [ ] U-1: 정적 UI 내장 모드 추가(`go:embed`), 기본 API 주소를 동일 출처로, 포트 단일화. 기존 2구성은 유지하며 검증 스크립트를 맞춘 뒤 기본값 전환
  - [x] 선택적 내장 모드·동일 출처 API·단일 listener 추가, 기존 분리 기본값 유지
  - [x] Go/Web 회귀·실제 Docker build/offline archive·추출 바이너리 브라우저 검증
  - [x] 포트 단일화: Compose 기본값을 service 하나로 전환(UI+API 같은 origin), 기존 2구성은 `standalone-web` profile로 유지, 노출/빌드 계약 테스트와 문서 동기화
  - [ ] exact-SHA 이중 이미지 릴리스 검증 후 기본값 전환(선택적 추가와 구분)
- [x] U-2: 결과 화면 재구성(상태 한 줄 → 원인 후보 → 다음 단계, 면책·커버리지 접기), ko/en 사전, 내부 코드 비노출. Android 문구도 같은 코드 매핑 사용
- [x] U-3: 증상 프리셋 3~4종(웹사이트 / 메일 / SSH / 느림)
- [x] S-1: 내장 UI와 함께 보안 헤더(CSP, nosniff, frame, referrer) 적용 — 정적 응답 경계·실제 Chromium 정책 검증. standalone nginx 정책은 기존대로 유지
- [ ] R-2: `v0.1.0` 발행 — 바이너리 4종 + 체크섬 + GHCR 이미지. README 첫 화면을 "다운로드 → 실행 → 브라우저" 3단계로

**완료 기준:** 사전 빌드 바이너리를 내려받아 실행 후 `http://localhost:<포트>` 하나로 프리셋 진단 가능. 결과 화면의 한국어 비율 100%(원시 JSON 제외). 기존 Go·Web 테스트 및 골든 픽스처 통과.

### Phase 2 — 진단 깊이 (3주, 약 10~12인일)

목표: "어디가 문제인가"에 대한 **근거의 폭**을 넓힌다. 모두 additive 변경으로 기존 JSON 계약을 유지.

- [ ] D-1: DNS 리졸버 비교·레코드 타입·오류 세분화 → 분석 규칙 추가(예: "시스템 리졸버만 실패")
- [ ] D-2: HTTP(S) 단계별 시간(DNS·연결·TLS·TTFB) → "느림" 후보 규칙
- [ ] D-3: TCP 반복 연결 기반 성공률·RTT 분포·지터, IPv4/IPv6 비교
- [ ] D-4: GeoIP 공급자 인터페이스, `off` 스위치, 선택적 오프라인 DB

**완료 기준:** 각 신규 신호마다 (a) 픽스처 기반 결정적 테스트, (b) 분석 규칙 1개 이상, (c) UI 표시, (d) "무엇을 단정할 수 없는지" 한계 문구가 함께 들어갈 것. 오탐 방지를 위해 "단정 불가" 원칙(기존 철학)을 유지.

### Phase 3 — 구조 개선 (2주, 약 6~8인일)

목표: 이후 기능 추가 비용을 낮춘다. 사용자 가시 변화는 없어야 정상.

- [ ] M-3: 검사 종류별 typed 결과 구조 도입(JSON wire 불변, 골든 픽스처로 보증)
- [ ] M-4: `app.js` 화면별 분리, 테스트·구문 검사 글롭화, eslint 도입
- [ ] M-2(잔여): 검증 의무 범위 재조정(PR=Quick, 릴리스 후보=exact-SHA)
- [ ] M-5(잔여): Android 메이저 업데이트 순차 검증, 에뮬레이터·실기기 체크리스트 실행

**완료 기준:** `analysis.go`의 untyped 파싱 헬퍼 제거, 새 테스트 파일이 목록 수정 없이 자동 실행, 모든 골든 픽스처 무변경 통과.

### Phase 4 — 확장 후보 (이후, 우선순위는 7절 결정에 따름)

- D-5: 클라이언트 측 보고서 이력·diff
- U-4: 경량 프로브/에이전트 모드(사용자 PC·거점에서 실행 → 중앙에 결과 전송), 다중 관측 지점
- S-2: 다중 API 키, 키별 한도, 최소 감사 로그
- ICMP ping(권한 처리 포함)·MTU

---

## 6. 성공 지표

| 지표 | 현재 | 목표 |
|---|---|---|
| 새 사용자의 첫 진단까지 | Docker 빌드 또는 터미널 2개 + API 주소 수동 변경 | 바이너리/이미지 실행 1회, 3분 이내 |
| 클린 머신 `make test` | 이번 분석 환경에서 4건 실패 | Docker·외부망 없이 통과 |
| 결과 화면 언어 일관성 | 한국어 UI 안에 영어 판정·내부 코드 혼재 | 원시 JSON 외 100% 한국어(영어 전환 지원) |
| 마크다운 총량 | 약 452KB, 루트에 기록 문서 혼재 | 현재 문서 약 200KB 이하 + `archive/` 분리 |
| 릴리스 | 태그·바이너리·이미지 없음 | `v0.1.0` + 바이너리 4종 + GHCR 이미지 |
| Dependabot 적체 | 7건 미병합 | 보안 관련 7일 내 처리 |
| 진단 신호 | 13종 검사, DNS 단일 조회 | DNS 비교·지연 분해·반복 연결 통계 추가 |

---

## 7. 먼저 결정해야 할 사항

| # | 질문 | 영향 |
|---|---|---|
| 1 | **제품명·저장소명·모듈 경로**를 무엇으로 통일할 것인가 | Phase 0 전체, 배포 이름 |
| 2 | **주 사용자와 배포 형태**는? (개인·소규모 운영자의 로컬 실행 / 사내 공용 서버 / 공개 호스팅) | 로컬이면 U-1·U-4 최우선, 공개면 S-1·S-2·rate limit 비중 증가 |
| 3 | 외부 GeoIP·RDAP 등 **제3자 조회를 기본 허용**할 것인가 (기본 on/off) | D-4, 개인정보 문서 |
| 4 | **서드파티 Go 의존성**을 허용할 것인가 (`x/net/icmp`, `miekg/dns`, `maxminddb` 등) | 지금의 "의존성 0" 강점과 D-1·D-3·D-4 구현 방식의 트레이드오프. 표준 라이브러리만으로 먼저 시도하는 것을 권장 |

---

## 8. 리스크와 하지 않을 것

**리스크**

- **검증 체계와의 결합:** 이중 이미지·재현 빌드 검증 스크립트(`scripts/` 36개)가 구조 변경(U-1)에 민감합니다. "추가 → 기본값 전환" 순서와 같은 PR 내 스크립트 수정이 필요합니다.
- **문서 계약 테스트:** 문서 이동·수정이 테스트를 깨뜨립니다. DOC-1과 M-2(일부)를 분리하지 마세요.
- **정직성 원칙 훼손:** U-2에서 요약을 단순화하다가 "정상=서비스 전체 정상"으로 읽히면 안 됩니다. 한 줄 판정에도 "검사 범위 내" 한정어를 유지하고 상세는 접어서 보존합니다.
- **분석 범위의 한계:** PR #8 미반영, Android·Docker·브라우저 게이트 미검증. 해당 영역의 결론은 재확인이 필요합니다.

**이번 로드맵에서 하지 않을 것**

- AI 기반 원인 추론, Kubernetes·대규모 SaaS 운영 기능(기존 `PLAN.md`의 범위 제외 항목과 동일)
- 서버 측 보고서 영구 저장
- 프런트엔드 프레임워크·번들러 도입(현재의 빌드 없는 구성이 강점)

---

## 9. 이번 주 체크리스트

1. 7절의 결정 1·2를 확정한다 (나머지는 Phase 1 시작 전까지).
2. `processPIDExists`가 좀비(`Z`)를 종료로 판정하도록 수정하고, Docker 의존 테스트 2종에 `LookPath` 기반 `t.Skip` 추가.
3. `cmd/checknetwork-api/operations_contract_test.go`의 toolchain 다운로드 의존을 `GOTOOLCHAIN=local` 또는 skip 조건으로 정리.
4. GitHub 설정: private vulnerability reporting 활성화, Description·Topics 입력, `main` 브랜치 보호(필수 체크) 현황 확인.
5. Dependabot #1, #4(실패)의 원인 확인; 통과한 Android PR은 하나씩 검토.
6. `docs/archive/` 생성 후 기록성 문서 이동 + 문서 계약 테스트 축소를 한 PR로 준비.
7. 결과 화면의 영어 문구 목록(코드 → 한국어)을 작성해 U-2 사전의 초안으로 사용.
8. PR #8 병합 후 이 문서의 "분석 범위 주의"와 R-4 항목을 재점검.

---

## 부록 A. 직접 확인한 실행 결과 요약

```text
go build ./...                      OK
go vet ./...                        OK
go test ./...
  backend/api                       ok   (12.1s)
  backend/diagnostic                FAIL TestRunTraceCommandNormalParentExitKillsChildAndGrandchildHoldingPipes
                                         "descendant PID ... survived runTraceCommand"
                                         (ps: sh/sh/sleep 이 PPID 1 의 Z 상태 — 종료되었으나 미수거)
  cmd/checknetwork-api              FAIL TestComposeResolvedExposureBoundary
                                         exec: "docker": executable file not found in $PATH
                                    FAIL TestComposeResolvedBuildIdentityIsValidSharedAndOverrideable
                                         docker compose config: exec: "docker": not found
                                    FAIL TestCleanArchiveDisablesAmbientVCSStampingWithoutDroppingCallerGOFLAGS
                                         go: downloading go1.25 ... lookup proxy.golang.org: no such host
npm ci && npm test (Node 22.23.4)   tests 892 / pass 891 / skipped 1 / fail 0 / 64.4s
API smoke (127.0.0.1)               /readyz 200, /api/v1/health 200, /api/v1/checks 13 kinds
                                    POST /api/v1/reports (tcp ok, http ok, tcp closed, dns localhost)
                                      -> 200, 4ms, 2,389B, status=degraded, analysis.verdict=attention
                                    CORS: localhost:3000 / 127.0.0.1:3000 허용, 임의 origin 거부
                                    Headers: Cache-Control: no-store, X-Content-Type-Options: nosniff
```

## 부록 B. 근거 위치 인덱스

| 항목 | 위치 |
|---|---|
| UI 기본 API 주소 9090 | `frontend/index.html:28` |
| API 기본 주소·GeoIP 기본값 | `cmd/checknetwork-api/main.go:252-253` |
| Compose 포트·CORS | `compose.yaml` |
| 영어 요약 라벨 | `frontend/app.js:588` |
| DNS 검사 | `backend/diagnostic/dns.go` |
| TLS 핸드셰이크 trace만 사용 | `backend/diagnostic/http.go:24,135` |
| GeoIP 공급자 고정 검증 | `backend/diagnostic/geo_details.go:145` |
| 인증서 만료 임박(30일) 규칙 | `backend/diagnostic/analysis.go:120` |
| 좀비 오판 헬퍼 | `backend/diagnostic/traceroute_process_unix_test.go:84` |
| 문서 문구 고정 테스트 | `backend/diagnostic/documentation_contract_test.go` |
| 수작업 테스트·구문 검사 목록 | `frontend/package.json` (`test`, `test:syntax`) |
| Android 버전 불일치 | `android/app/build.gradle.kts:15` vs `VERSION` |
| 웹 서버 설정(보안 헤더 없음) | `frontend/nginx.conf` |
| 범위 제외 항목(멀티 vantage 등) | `PLAN.md` "릴리스 범위에서 제외할 항목" |
| 목표 여정·기능 갭 | `docs/REVIEW_2026-09-01.md` |
| 낡은 원격 상태 기술 | `PLAN_PROGRESS2.md` 12-5절 말미 |
