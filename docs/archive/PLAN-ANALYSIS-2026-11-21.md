# CheckNetwork 프로젝트 분석 및 개선·추가 개발 계획

| 항목 | 내용 |
|---|---|
| 대상 저장소 | https://github.com/piecer/revealnetworktrouble (제품명 **CheckNetwork**) |
| 분석 기준일 | 2026-11-21 |
| 분석 방식 | 공개 문서 기반 정적 분석 (빌드·실행·소스 라인 단위 리뷰는 수행하지 않음) |
| 읽은 자료 | `README.md`, `docs/PLAN.md`, `docs/IMPLEMENTATION-HISTORY.md`(과거 README·ARCHITECTURE 스냅샷 포함), `docs/TESTING.md`, 루트 파일 목록 |
| 읽지 못한 자료 | 소스 코드, `SPEC.md`, 현행 `docs/API.md`·`OPERATIONS.md`·`ARCHITECTURE.md`, `android/README.md`, Issues/PR 본문, CI 워크플로 파일 |

**표기 규칙**
- `[문서]` 문서에 직접 적힌 사실
- `[추론]` 문서 정황에서 유추한 내용 (착수 전 코드로 확인 필요)
- `[제안]` 분석자의 개선 제안

> ⚠️ 소스 코드를 직접 읽지 않았으므로 이 문서의 "변경 대상 경로"는 추정입니다. 각 작업 착수 전에 실제 코드 구조를 먼저 확인하세요.

---

## 1. 요약

CheckNetwork는 DNS·TCP·HTTP(S)·traceroute·메일/SSH 서비스 응답을 한 번에 검사하고, **"관측 사실 → 원인 후보 → 다음 확인 단계"**를 분리해서 보여주는 네트워크 진단 도구입니다. Go API 서버 + 정적 웹(빌드 도구 없음) + Android 앱으로 구성되어 있습니다.

**한 줄 평가:** 진단 결과를 과장하지 않는 설계 철학(관측 ≠ 확정 원인)과 경계 안전성(자원 제한, SSRF 방어, 계약 테스트)은 매우 높은 수준입니다. 반면 ① 측정 위치가 항상 *서버*라서 사용자 본인 네트워크를 직접 진단하기 어렵고, ② 검증·문서 프로세스가 기능 개발 속도를 앞지를 만큼 무거우며, ③ 동기식 장시간 API와 클라이언트별 수작업 파서 등 구조적 부채가 쌓이고 있고, ④ 처음 접하는 사용자를 위한 진입 장벽(스크린샷·LICENSE·릴리스 부재, 포트 3종)이 남아 있습니다.

### 최우선 권고 7가지

| 순위 | 항목 | 이유 | 규모 |
|---|---|---|---|
| 1 | **Hosted CI 도입 + 게이트 3계층화** (QA-1) | 모든 수용 검증이 로컬 수작업·"pending" 상태. PR마다 자동 검증이 없음 | M |
| 2 | **문서 슬림화·수치 하드코딩 제거** (DOC-1~3) | 문서 간 수치 불일치, 문서 수정 때마다 검증 증거가 무효화되는 구조 | L |
| 3 | **증상별 가이드 프리셋** (PRD-1) | 문서에 미완료로 남은 핵심 UX. 일반 사용자 가치가 가장 큼 | M |
| 4 | **측정 위치(vantage) 명시 + 로컬 CLI** (PRD-2, PRD-3) | "내 네트워크 문제"를 서버에서 측정하는 근본 한계 완화 | M~L |
| 5 | **저장소 기본 위생** (REPO-1~3) | LICENSE·설명·토픽·릴리스·스크린샷 없음, 열린 PR 정리 | S |
| 6 | **비동기 Job API + SSE** (ARC-1) | 최대 315초 동기 요청이 타임아웃 산술·프록시 이슈의 근원 | L |
| 7 | **단일 스키마 소스(OpenAPI) + 코드 생성** (ARC-2) | Go·Web·Android가 같은 계약을 수작업 파서로 3중 구현 | L |

---

## 2. 프로젝트 개요

### 2.1 구성

| 경로 | 역할 |
|---|---|
| `cmd/checknetwork-api` | API 서버 진입점 (`go run ./cmd/checknetwork-api`) |
| `backend/` | Go 백엔드. 테스트 명령 기준 `backend/api`(HTTP 전송), `backend/diagnostic`(진단 엔진·분석) [문서] |
| `frontend/` | 정적 HTML/CSS/ESM JavaScript. Node 내장 test runner + jsdom, Playwright는 opt-in [문서] |
| `android/` | 네이티브 Android 앱 (API 26+, JDK 17, SDK 35, Gradle 8.11.1 wrapper) [문서] |
| `docs/` | API, 운영, 아키텍처, 테스트, Geo, IP 컨텍스트, 구현 이력 등 |
| `scripts/`, `testdata/` | 릴리스 검증 스크립트, 계약 fixture |
| `Dockerfile`, `compose.yaml`, `Makefile`, `VERSION`(0.1.0), `SPEC.md` | 빌드·배포·버전 |

### 2.2 기능

**검사 종류 13종** [문서]: `dns`, `tcp`, `http`, `https`, `traceroute`, `ssh`, `smtp`, `submission`, `smtps`, `imap`, `imaps`, `pop3`, `pop3s` (한 번에 최대 20개 대상)

**화면/기능** [문서]
| 기능 | 내용 |
|---|---|
| 통합 진단 | 연결·DNS·HTTP·TLS 인증서·SSH/메일 서버 초기 응답 |
| 경로 토폴로지 | 2D/3D Canvas 그래프, 대상별 선택, 연속 무응답 홉 접기 |
| Geo 경로 지도 | 오프라인 Natural Earth 배경, GeoIP 추정 위치, 상세 패널 |
| IP 추가 정보 | PTR, RDAP, BGP/ASN, RPKI (버튼 클릭 시에만 조회) |
| IP 라벨 | 별칭 부여, CSV/JSON import |
| 공통 구간 | IPv4/IPv6 공통 접두사 계산 (서버 요청 없음) |
| 보고서 | 사람용 요약 + 원본 JSON |

### 2.3 아키텍처 (문서 기준)

```
Web (정적)  /  Android  /  (CLI·데스크톱 — 미구현)
              │
        REST JSON API  (/api/v1, trusted-local | public 모드)
              │
        Report Service ── Analysis Interpreter (결정적 규칙, confidence = 증거 직접성)
              │
        Diagnostic Runner (checker supervisor, 동시성·deadline 중재)
        ├─ DNS  ├─ TCP  ├─ HTTP(S)/TLS  ├─ Service greeting  └─ Traceroute(Unix/Windows 어댑터)
              │
        GeoIP/ASN 보강 (외부 공급자, active 8 / queue 64 / LRU 2,048 / 24h)
```

### 2.4 핵심 설계 원칙 [문서]
- 결과는 "수행한 검사 범위에서의 정상"이며 확정 원인이 아님. `confidence`는 장애 확률이 아니라 증거의 직접성
- 홉 무응답만으로 장애 위치를 단정하지 않음. 생략 구간 연결은 직접 관측 연결과 구분 표시
- 기본은 `trusted-local`(127.0.0.1 바인딩). 공개 시 `public` 모드 + API 키 + rate limit + dial 시점 SSRF 차단
- 모든 경계에 상한: 연결 128/256, 동시 report 4/16, 요청 본문 1 MiB, 응답 8 MiB(compact 1 MiB 미만), nodes 500 / links 1,000 등
- 자동 테스트는 외부 인터넷에 의존하지 않고, 실기기·실브라우저 검증은 별도로 구분

### 2.5 저장소 현황 지표 [문서]
- 커밋 33개, Star 0 / Fork 0, 설명(description)·토픽·릴리스·태그 없음
- 루트 파일 목록에서 `LICENSE` 미확인 (목록이 일부 생략되어 있어 직접 확인 필요) [추론]
- 문서 분량: `IMPLEMENTATION-HISTORY.md` 320줄/50.9 KB, `TESTING.md` 158줄/35.3 KB, `PLAN.md` 93줄/11.1 KB
- 문서 열람 시 PR 4건 표기 (내용 미확인)

---

## 3. 평가

### 3.1 강점

| 영역 | 내용 |
|---|---|
| 진단의 정직성 | 관측/원인 후보/한계/다음 단계 분리. "정상 ≠ 서비스 정상"을 UI 문구까지 고정 |
| 보안 경계 | 모드 분리, SSRF를 실제 dial 직전에 차단, CORS ≠ 인증을 명시, 로그에 본문·자격증명 미기록 |
| 자원·종료 안전성 | 단계별 admission, noncooperative checker를 `active/stuck/remaining`으로 보고하고 nonzero 종료, graceful drain |
| 계약 중심 테스트 | producer가 `finding-contract-v1`·`compact-v1`·API 오류 레지스트리를 소유하고 Web/Android가 동일 fixture를 소비 |
| 재현 가능한 릴리스 | tagless archive 이중 빌드 바이트 동일성, pinned base, umask 3종 비교 |
| 외부 의존 최소화 | 프런트 런타임 의존성 없음, 지도 타일/API 키 불필요 |
| 문서의 솔직함 | 미검증 범위를 "완료"로 표기하지 않는 태도 |

### 3.2 약점 (근거 포함)

| # | 약점 | 근거 |
|---|---|---|
| W1 | **측정 위치가 서버**라 사용자 본인 네트워크/기기를 측정하지 못함 | README: 검사는 API 서버 네트워크에서 수행되며 사용자 PC/휴대전화 측정값으로 해석 금지. Android도 동일 API 사용 [문서] |
| W2 | **게이트 부채**: Stage 8·9의 최종 clean `make ci`, 독립 리뷰, exact-SHA closure가 계속 "pending"인데 그 위에 Geo·ASN·IP 컨텍스트·공통 구간 기능이 병합됨 | IMPLEMENTATION-HISTORY, PLAN [문서] |
| W3 | **문서↔테스트 결합**: 문서 문구·수치를 고정하는 `documentation_contract_test.go`가 있고, 문서 수정이 이전 CI 증거를 무효화함 | TESTING.md [문서] |
| W4 | **문서 내 수치·사실 불일치** (부록 A 참고) | TESTING/PLAN/ARCHITECTURE 스냅샷 [문서] |
| W5 | **Hosted CI 없음 추정**: 레지스트리 push/서명/SBOM과 함께 "hosted full CI"가 미수행 항목으로 기록 | TESTING "아직 gate가 대신하지 않는 acceptance" [문서] |
| W6 | **동기식 장시간 API**: report 요청이 최대 315초, 이를 위해 `H/B/E/R/D` 타임아웃 산술(35/336/346초)과 HTTP/2 deadline 미검증 이슈가 따라옴 | ARCHITECTURE 스냅샷 [문서] |
| W7 | **계약 3중 구현**: Go producer, Web, Android가 각각 strict 파서를 수작업 구현 (fixture로 동기화) | 456 mutation, 85 구조 mutation 등 [문서] |
| W8 | **프런트 구조 압박**: 문서 전체 DOM 1,200 요소 상한, 활성 view만 mount, 100행 pagination 등 자체 제약 관리에 복잡도가 집중. Web controller/repository 분리는 미완료 권고 | IMPLEMENTATION-HISTORY [문서] |
| W9 | **진입 장벽**: 기본 포트 8080/8090/9090 혼재, 소스 실행 시 UI에서 API 주소를 수동 변경해야 함, README에 스크린샷 없음 | README [문서] |
| W10 | **핵심 UX 미완**: 증상별 guided preset 범위 미확정 | IMPLEMENTATION-HISTORY 체크리스트 [문서] |
| W11 | **미구현 진단 능력**: 이력/기준선, 패킷 손실·지터·처리량·MTU, 로컬 링크/Wi-Fi/VPN/프록시 사실, 멀티 vantage, 메트릭 엑스포터 | PLAN "후속 capability" [문서] |
| W12 | **실환경 검증 공백**: 실 브라우저(Chrome/Firefox/Safari)·스크린리더·Android 실기기·TalkBack, Windows/macOS 런타임 | TESTING [문서] |
| W13 | 외부 GeoIP/ASN 공급자 의존 (공급자명·대체 경로·비활성화 옵션은 문서에서 확인하지 못함) | README [문서] |

---

## 4. 개선 항목 상세

우선순위: **P0**(즉시) · **P1**(다음 마일스톤) · **P2**(그다음) · **P3**(장기)
규모: **S**(≤1일) · **M**(2~5일) · **L**(1~2주) · **XL**(2주 초과)
검증 명령은 문서에 있는 기존 `make` 타깃을 기준으로 적었습니다.

### 4.1 저장소·문서 (P0 중심)

#### REPO-1. 저장소 기본 정보 정비 — P0 / S
- **현황** 설명·토픽 없음, LICENSE 미확인, SECURITY/CONTRIBUTING 미확인
- **제안**
  - GitHub 설명: "Network troubleshooting tool: DNS/TCP/HTTP(S)/traceroute diagnostics, topology & geo path visualization (Go + Web + Android)"
  - 토픽: `network-diagnostics`, `traceroute`, `dns`, `golang`, `android`, `troubleshooting`
  - `LICENSE`(의도에 맞는 라이선스 선택), `SECURITY.md`(취약점 제보 경로 — 이 도구는 SSRF/외부 노출 이슈가 현실적), `CONTRIBUTING.md`(`make test` 한 줄 안내)
- **완료 기준** 저장소 첫 화면에서 설명·라이선스·보안 정책이 보임

#### REPO-2. 열린 PR 4건 정리 — P0 / S
- **제안** 각 PR을 머지/종료/재작업 중 하나로 결정. 장기 방치 PR이 있으면 이유를 코멘트로 남김
- **완료 기준** 열린 PR 0건 또는 각각 상태 코멘트 존재

#### REPO-3. v0.1.0 릴리스와 CHANGELOG — P1 / S
- **현황** `VERSION=0.1.0`이 있으나 태그·Release 없음
- **제안** `CHANGELOG.md`(Keep a Changelog 형식) 신설 → 현 상태를 `v0.1.0`으로 태깅, 릴리스 노트에 "알려진 한계"(측정 위치, 미검증 환경) 명시
- **완료 기준** Releases에 v0.1.0 존재, 바이너리(linux/amd64 등) 또는 Docker 이미지 첨부

#### DOC-1. 문서 슬림화·이력 분리 — P0 / L
- **현황** `IMPLEMENTATION-HISTORY.md`가 현재 상태 문서와 과거 스냅샷(과거 README·ARCHITECTURE 전문)을 함께 담고 있고, 한 줄이 매우 긺. Git 이력으로 보존 가능한 내용이 대부분
- **제안**
  1. 과거 스냅샷 전문은 `docs/archive/`로 이동하거나 삭제(Git 이력이 보존)
  2. 변경 이력은 `CHANGELOG.md`, 설계 결정은 ADR(`docs/adr/NNNN-*.md`)로 분리
  3. 계획은 `docs/roadmap.md` 하나로 통합 (루트 `PLAN.md` 링크와 `docs/PLAN.md` 혼재 해소)
  4. 목표 구조는 부록 C 참고
- **완료 기준** 현행 문서에 "Historical" 블록이 없고, 각 문서는 한 가지 목적만 가짐

#### DOC-2. 문서 불일치 수정·수치 하드코딩 제거 — P0 / M
- **제안**
  - 부록 A의 불일치 항목을 모두 수정
  - 테스트 개수·asset 개수·DOM 최대값 같은 **변동 수치는 문서에 쓰지 않고** CI 요약(Job summary)이나 생성 파일로 대체
  - 문서에는 *불변 계약*(상한 값, 에러 코드 등)만 남김
- **완료 기준** 문서에서 "Web NNN tests" 형태의 수치가 사라짐

#### DOC-3. 문서 계약 테스트 범위 축소 — P1 / M
- **현황** `backend/diagnostic/documentation_contract_test.go`가 소스 심볼·문서 문구·현재 수치까지 고정 [문서]
- **제안** 유지: 문서 내부 링크 유효성, 코드가 소유한 상한값(예: `MAX_TARGETS`)과 문서 값의 일치. 제거: 문구 존재 검사, 개수·SHA·상태 선언 검사
- **완료 기준** 문서 오탈자 수정이 Go 테스트 실패를 일으키지 않음

#### DOC-4. README 개선 — P1 / S~M
- 화면 스크린샷/GIF 4장(통합 진단, 토폴로지 2D/3D, Geo 지도, IP 상세)
- `README.en.md` 추가 (또는 영어 요약 섹션)
- "누가 어떤 상황에서 쓰는가" 3줄 시나리오
- **완료 기준** README 첫 스크롤 안에서 제품이 무엇인지 시각적으로 이해 가능

### 4.2 CI·테스트·릴리스

#### QA-1. Hosted CI(GitHub Actions) 3계층 — P0 / M
- **현황** hosted CI 미수행으로 기록 [문서]. 로컬 `make ci`는 이미 잘 정리되어 있음
- **제안** 기존 `make` 타깃을 재사용해 계층화

| 계층 | 트리거 | 내용 | 목표 시간 |
|---|---|---|---|
| Tier 1 | 모든 PR | `npm ci`, `make test`, `make web-test-syntax`, `make vet`, 문서 링크 검사 | ≤ 10분 |
| Tier 2 | main 병합 / nightly | `make test-race`, `make android-check`(JDK17+SDK35), Playwright Chromium gate, windows/macos `go test` | ≤ 40분 |
| Tier 3 | 태그 푸시 | `make ci-clean-archive`, `make verify-release`, SBOM, 서명, 레지스트리 push | 제한 없음 |

- **완료 기준** PR에 필수 체크가 표시되고 main 브랜치 보호 규칙에 Tier 1이 포함됨

#### QA-2. OS 매트릭스 — P1 / S
- **현황** Windows `tracert.exe` 어댑터는 컴파일(386/amd64/arm64)만 검증, 실행은 수동 [문서]
- **제안** `windows-latest`, `macos-latest`에서 `go test ./...` (traceroute 실행 테스트는 fake executable 기반이라 이식 가능한지 확인)
- **완료 기준** 3개 OS에서 `go test` 통과

#### QA-3. 브라우저 E2E를 nightly로 — P1 / M
- **현황** graph-first / rich-topology / label-integrity / topology-empty / CORS gate가 있으나 Playwright 별도 설치가 필요한 opt-in [문서]
- **제안** nightly에서 Playwright+Chromium 설치 후 전 gate 실행, 스크린샷을 artifact로 보관. Firefox/WebKit 프로젝트 추가는 후속
- **완료 기준** nightly에서 자동 실행, 실패 시 스크린샷 확인 가능

#### QA-4. 커버리지·퍼징 — P2 / M
- Go: `go test -cover` 요약, 파서(traceroute 출력, 서버 greeting, compact JSON)에 Go native fuzz 추가
- Web: Node test runner의 `--experimental-test-coverage` 요약
- Android: JaCoCo 요약
- **완료 기준** PR 요약에 커버리지 표시, 퍼즈 corpus가 저장소에 보관

#### QA-5. 릴리스 게이트 단순화 — P1 / M
- **현황** "five-way review / manifest / exact-SHA closure"는 매우 엄격하지만 1인 개발 속도에는 과함. Stage 8/9가 아직 닫히지 않음
- **제안**
  1. Stage 8/9 게이트를 *공식 종료*(남은 항목은 이슈로 이관)
  2. 릴리스 게이트는 "Tier 3 통과 + 체크리스트 1장"으로 단순화
  3. 엄격 검증(`verify-release-real`)은 **릴리스 태그에서만** 실행
- **완료 기준** 일반 기능 PR이 문서상 "pending gate"를 만들지 않음

#### QA-6. 수동 수용 체크리스트 — P2 / M
- 실 브라우저 3종 × 320/375/400px, 스크린리더 1종, Android 실기기 1대(TalkBack), process death 후 공유 파일 접근, TLS 프록시 뒤 HTTP/2 deadline
- 체크리스트를 `docs/testing.md`에 표로 두고 **날짜·환경·결과**를 기록 (자동 테스트 통과와 분리)
- **완료 기준** 최초 1회 수행 기록이 존재

### 4.3 제품·기능

#### PRD-1. 증상별 가이드 프리셋 — P0 / M
- **현황** "증상별 guided preset의 범위를 확정"이 미완료 항목 [문서]
- **제안** 사용자가 증상을 고르면 대상·검사 종류가 자동 구성되고, 결과 요약이 그 증상에 맞는 언어로 나옴

| 프리셋 | 자동 구성 (예시) |
|---|---|
| 인터넷이 안 돼요 | 공개 DNS 2종 + 임의 TCP(443) + 게이트웨이 외부 경로 traceroute |
| 특정 사이트만 안 열려요 | 해당 도메인 DNS(여러 리졸버 비교) + HTTPS + TLS 인증서 + traceroute |
| 느려요 | 경로 다회 측정(RTT 편차) + HTTP 응답 시간 |
| 메일이 안 돼요 | MX 조회 + SMTP/submission/IMAP/POP3 greeting + TLS |
| 해외 접속이 느려요 | 운영자가 지정한 국내/해외 엔드포인트 세트 비교 (PLAN의 "해외망" 목표 반영) |
| 서버 접속(SSH) 문제 | DNS + TCP(22) + SSH greeting + traceroute |

- **변경 대상(추정)** `frontend/` 폼, 프리셋 정의 JSON(서버 또는 클라이언트), Android 폼(이미 `/checks` capability discovery 사용)
- **완료 기준**
  - 프리셋 선택 → 1클릭 진단 → 요약에 "이 증상에서 확인한 것 / 못 한 것 / 다음 단계"가 표시
  - 기존 원칙 유지: 프리셋도 *확정 원인*을 말하지 않음
- **검증** `make test`, `make web-test-syntax`, `make android-test`

#### PRD-2. 측정 위치(vantage) 명시 — P0 / S~M
- **현황** 서버 네트워크에서 측정한다는 경고가 README에만 있음
- **제안**
  - 보고서에 `vantage` 블록 추가: 서버 egress 공인 IP(+ASN), 사용 중인 DNS 리졸버, 서버 호스트명(선택), 측정 시각
  - Web/Android 결과 화면 상단에 상시 배지: "이 결과는 **{서버}** 에서 측정됨 (내 기기 아님)"
  - API 주소가 `localhost`/사설 대역이면 "내 PC 기준 측정" 배지로 구분
- **주의** egress IP 조회는 외부 요청을 수반하므로 기본 off 또는 opt-in으로 설계하고 개인정보 문서에 반영
- **완료 기준** 보고서 export에 vantage 정보 포함, 두 클라이언트가 동일 계약으로 표시 (계약 fixture 갱신)

#### PRD-3. 로컬 CLI / 에이전트 — P1 / M~L
- **현황** 설계상 `diagnostic` 패키지는 프로토콜/UI를 모름 [문서] → CLI 재사용이 용이 [추론]
- **제안** `cmd/checknetwork`: `checknetwork run --preset slow --target example.com --format json|text`
  - 출력은 API와 동일한 report JSON → 웹 UI에서 "JSON 불러오기"로 시각화
  - 또는 `checknetwork-api`를 로컬 실행하는 현재 방식을 "내 PC 진단 모드"로 문서화·배포(단일 바이너리)
- **완료 기준** CLI가 Linux/macOS/Windows에서 동작, 웹 UI가 CLI 출력 JSON을 import 가능

#### PRD-4. Android 온디바이스 프로브 — P1(스파이크) → P2 / L~XL
- **현황** Android는 서버 API 클라이언트일 뿐 *휴대전화 자체의 네트워크*는 측정하지 않음
- **제안** 단계적 접근
  1. 스파이크(2~3일): 비루트 환경에서 가능한 측정 범위 확인 — DNS(`DnsResolver`), TCP connect, HTTP/TLS, `ConnectivityManager`의 네트워크 종류·VPN·검증 상태·캡티브 포털, (선택) Wi-Fi 링크 속도/RSSI
  2. traceroute 가능성 점검: 비루트에서 `ping -t <TTL>` 출력 파싱 등 대안 조사. 불가하면 "서버 경로 검사"로 대체하고 문서화
  3. 온디바이스 결과를 서버 결과와 **나란히 비교**하는 화면 ("내 폰 vs 서버")
  4. Go 엔진 재사용(gomobile) vs Kotlin 재구현 장단점 비교 후 결정
- **완료 기준** 스파이크 결과 ADR 작성 후 구현 여부 결정

#### PRD-5. 경로 품질 지표 (MTR 방식) — P1 / M
- **현황** 목적지당 1~10회 traceroute를 수행해 attempt별 홉 응답이 이미 존재 [문서]
- **제안** attempt 집계로 홉별 응답률·RTT 평균/최소/최대/표준편차(지터) 계산 → 토폴로지/지도 상세에 표시
  - 해석 규칙 고정: *중간 홉 손실이 이후 홉에 이어지지 않으면* ICMP 응답 제한 가능성으로 표시, *목적지까지 지속되면* 실제 손실 후보로 표시 (README의 "무응답만으로 단정 금지" 원칙 유지)
- **계약 영향** `compact-v1`·finding 계약 변경 필요 → producer fixture와 Web/Android 소비 코드 동시 갱신
- **완료 기준** 홉 상세에 loss%/jitter 표시, 분석 finding에 "경로 품질" 근거 추가, 계약 테스트 통과

#### PRD-6. DNS 진단 심화 — P1 / M
- **제안(현재 구현 범위를 코드로 확인한 뒤 부족분만)**
  - 여러 리졸버(시스템·공개 DNS) 응답 비교 → "리졸버별 답이 다름" finding
  - 레코드 타입 선택(A/AAAA/CNAME/MX/TXT/NS), NXDOMAIN/SERVFAIL/타임아웃 구분, TTL·응답 시간
  - IPv4 vs IPv6 연결 비교(듀얼스택 문제 진단)
- **완료 기준** 신규 finding이 계약 레지스트리(23개)에 등록되고 Web/Android 표시 확인

#### PRD-7. TLS·HTTP 심화 — P2 / M
- 인증서 체인/SAN/남은 유효기간 경고 임계치, TLS 버전 하한 경고, HTTP/2·HTTP/3 지원 탐지, 리다이렉트 체인 표시
- **완료 기준** 신규 finding 등록 + 테스트

#### PRD-8. 공유용 익명화(redaction) 내보내기 — P1 / M
- **현황** 원본 JSON에 IP/경로가 포함되어 "공유 전 확인" 안내에 의존. Android는 redacted 요약이 기본 [문서]
- **제안** Web 내보내기에도 "공유용(익명화)" 옵션: 사설 IP 마스킹, 공인 IP 일부 마스킹(IPv4 마지막 옥텟 등), 사용자 라벨/메모 제거, 미리보기 후 다운로드
- **완료 기준** 익명화 결과에 원본 사설 IP·라벨이 남지 않음을 테스트로 보장 (privacy canary 테스트 패턴 재사용)

#### PRD-9. 이력·기준선 — P2 / XL
- **제안** 선택형 로컬 저장(SQLite, 기본 off)으로 과거 보고서 비교: "어제는 정상이었는데 오늘 3번째 홉 RTT +80ms"
- **전제** 보관 기간·삭제·익명화 정책 선결정 (리포트에 네트워크 정보가 포함됨)
- **완료 기준** 두 보고서 diff 화면, 보관 TTL 설정

#### PRD-10 / PRD-11. 스케줄·알림, 멀티 vantage — P3 / XL
- 정기 진단 + webhook 알림, 서로 다른 위치의 API 인스턴스 결과를 교차 비교(`vantage` 계약 위에 구축)

### 4.4 아키텍처·API

#### ARC-1. 비동기 Job API + SSE — P1 / L
- **현황** `POST /api/v1/reports`가 최대 315초 동기 응답. 이 때문에 deadline 산술, 프록시 타임아웃, HTTP/2 미검증 문제가 발생 [문서]
- **제안** 기존 엔드포인트는 호환 유지하고 Job 모델을 추가 (스케치는 부록 B)
  - 진행 상황·대상별 부분 결과를 스트리밍해 UI에서 *먼저 끝난 결과부터* 표시
  - 취소는 `DELETE` 한 번으로 (현재 클라이언트의 abort 의존 완화)
- **트레이드오프** 서버에 *상태*가 생김 → 메모리 상한·TTL·소유권(키 단위) 설계가 필수. 기존 "무상태 + 엄격한 상한" 철학과 충돌하지 않도록 in-memory + 짧은 TTL로 시작
- **완료 기준** 20대상 진단에서 첫 결과가 수 초 내 표시, 연결 끊김 후 재접속으로 결과 복구, 기존 동기 API 테스트 전부 통과

#### ARC-2. OpenAPI + 코드 생성 — P1 / L
- **현황** `docs/API.md` 수작업 명세, Go/Web/Android가 fixture로만 동기화 [문서]
- **제안** `openapi.yaml`(또는 JSON Schema)을 단일 소스로 삼고
  - Go: 서버 응답을 스키마로 검증하는 테스트
  - Web: JSDoc 타입 또는 생성된 `.d.ts`
  - Android: 생성된 Kotlin 모델 (strict 파서는 유지하되 모델·상수는 생성)
  - 기존 fixture/mutation corpus는 **유지** (스키마가 표현 못 하는 의미 계약 담당)
- **완료 기준** 스키마 변경 시 3개 클라이언트 중 하나라도 빌드/테스트가 깨지면 CI에서 감지

#### ARC-3. 기본 UX 마찰 제거: 프런트 내장 서빙·포트 통일 — P0~P1 / M
- **현황** 소스 실행 시 `python3 -m http.server` 별도 실행 + UI에서 API 주소를 `8080`으로 수동 변경. 포트 8080/8090/9090 혼재 [문서]
- **제안**
  - `go:embed`로 `frontend/`를 API 바이너리에 포함하는 `--serve-web`(또는 기본) 옵션 → **단일 바이너리 + 동일 origin**(CORS 불필요)
  - 기본 포트를 문서·정적 HTML·Compose에서 일관되게 통일 (Compose는 호스트 포트만 다르게 노출하더라도 UI의 기본 API 주소는 same-origin 상대 경로)
  - 정적 HTML의 초기 API 주소 `9090` 제거
- **주의** 현재 nginx 기반 Web 이미지·release 검증 파이프라인(asset closure 등)에 영향 → 별도 브랜치에서 점진 전환
- **완료 기준** `go run ./cmd/checknetwork-api` 한 번으로 브라우저에서 바로 사용 가능

#### ARC-4. Web controller/repository 분리 — P2 / L
- **현황** 문서에 남은 권고로 기재 [문서]. DOM 상한·pagination·lifecycle 로직이 한곳에 몰릴 위험 [추론]
- **제안** state/model/renderer는 이미 분리되어 있으므로 *저장소(localStorage·import/export)*와 *컨트롤러(요청 lifecycle)*를 분리
- **완료 기준** 기존 Web 테스트 전부 통과 + 모듈 간 의존 방향 문서화

#### ARC-5. TypeScript(점진) — P2 / L
- 먼저 `// @ts-check` + JSDoc + `tsc --noEmit`을 CI에 추가(빌드 산출물 없이 유지), 이후 핵심 모델부터 `.ts` 전환 검토
- "빌드 도구 없이 배포" 장점을 보존하는 방식이 우선

#### ARC-6. Geo/ASN 공급자 추상화 — P2 / M
- **제안** 공급자 인터페이스 + 설정으로 선택 가능, 오프라인 DB(MaxMind GeoLite2, DB-IP Lite 등 라이선스 확인 후) 옵션, "외부 조회 전면 비활성" 모드 (폐쇄망·개인정보 민감 환경)
- **완료 기준** 공급자 장애 시 기존 "실패 표시" 동작 유지, 비활성 모드에서 외부 요청 0건 테스트

#### ARC-7. 메트릭 엑스포터 — P2 / M
- `/metrics`(Prometheus 형식)로 active report, admission 거부 수, checker 포화, GeoIP 큐 길이 등 노출. 기존 privacy-safe telemetry 원칙 준수(라벨에 대상/IP 금지)

### 4.5 보안·운영

| ID | 항목 | 우선순위/규모 | 내용 |
|---|---|---|---|
| SEC-1 | 파서·SSRF 퍼징과 회귀 | P1 / M | IPv4-mapped IPv6, 10진/16진/8진 IP 표기, DNS rebinding, 리다이렉트 경유 사설 접근 등 SSRF 우회 케이스 테이블 테스트 + fuzz |
| SEC-2 | 의존성·취약점 스캔 + SBOM | P1 / S | `govulncheck`, `npm audit`, Dependabot, Gradle 의존성 검증. SBOM은 릴리스에 첨부 |
| SEC-3 | API 키 운영 | P2 / M | 복수 키·키별 rate limit·폐기/로테이션 절차 문서화 (현재는 단일 Bearer 키 구조로 추정 [추론]) |
| SEC-4 | 레퍼런스 TLS 프록시 구성 | P2 / M | Caddy 또는 nginx 예제(`compose.proxy.yaml`)와 HTTP/2 deadline·취소 실측 결과 문서화 — 미검증 항목 해소 |
| SEC-5 | 외부 조회 데이터 문서화 | P2 / S | 어떤 데이터(대상 IP)가 어느 외부 공급자에 전달되는지 표로 정리 (GeoIP, RDAP, RIPEstat 등) |

---

## 5. 추가 개발 로드맵

> 1인 개발·주 20시간 내외를 가정한 대략의 기간입니다. 팀 규모에 따라 조정하세요.

### Phase 0 — 위생과 가시성 (약 1주)
- [ ] REPO-1 설명·토픽·LICENSE·SECURITY·CONTRIBUTING
- [ ] REPO-2 열린 PR 4건 정리
- [ ] DOC-2 문서 불일치 수정 (부록 A)
- [ ] DOC-4 README 스크린샷 추가
- [ ] QA-1 Tier 1 CI 최소 구성 (`make test` + `make vet` + syntax)

**종료 조건** 외부인이 README만 보고 5분 안에 실행·이해 가능, PR에 자동 체크 표시

### Phase 1 — 게이트 단순화와 문서 다이어트 (약 2~3주)
- [ ] QA-1 Tier 2/3 추가
- [ ] QA-5 Stage 8/9 공식 종료, 릴리스 게이트 단순화
- [ ] DOC-1 문서 구조 재편 (부록 C), DOC-3 문서 계약 테스트 축소
- [ ] QA-2 OS 매트릭스, QA-3 nightly 브라우저 E2E
- [ ] REPO-3 `v0.1.0` 릴리스 + CHANGELOG
- [ ] SEC-2 취약점 스캔·Dependabot

**종료 조건** 문서 수정이 CI를 깨뜨리지 않음, v0.1.0 공개

### Phase 2 — 사용자 가치 기능 (약 3~5주)
- [ ] PRD-1 증상별 가이드 프리셋
- [ ] PRD-2 vantage 명시
- [ ] ARC-3 단일 바이너리 + 포트 통일
- [ ] PRD-5 경로 품질(MTR 방식)
- [ ] PRD-6 DNS 심화
- [ ] PRD-8 공유용 익명화 내보내기
- [ ] SEC-1 SSRF·파서 퍼징

**종료 조건** "증상 선택 → 1클릭 진단 → 쉬운 요약"이 Web/Android 양쪽에서 동작, v0.2.0 릴리스

### Phase 3 — 구조 개선 (약 4~6주)
- [ ] ARC-1 비동기 Job API + SSE (Web 먼저, Android 후속)
- [ ] ARC-2 OpenAPI 단일 소스 + 코드 생성
- [ ] ARC-4 Web controller/repository 분리, ARC-5 `@ts-check`
- [ ] ARC-7 메트릭 엑스포터, SEC-4 레퍼런스 TLS 프록시
- [ ] PRD-3 로컬 CLI

**종료 조건** 첫 결과 표시 지연이 대상 수와 무관하게 짧아짐, 스키마 드리프트가 CI에서 자동 감지, v0.3.0

### Phase 4 — 확장 (6주 이상, 선택)
- [ ] PRD-4 Android 온디바이스 프로브 (스파이크 → ADR → 구현)
- [ ] PRD-9 이력·기준선
- [ ] ARC-6 Geo 공급자 추상화·오프라인 DB
- [ ] PRD-7 TLS/HTTP 심화
- [ ] PRD-10/11 스케줄·알림·멀티 vantage
- [ ] Android 인터랙티브 토폴로지/Geo UI (문서상 후속 capability)
- [ ] QA-6 실기기·스크린리더 수동 수용

---

## 6. 백로그 요약표

| ID | 제목 | 우선순위 | 규모 | 선행 |
|---|---|---|---|---|
| REPO-1 | 저장소 기본 정보 정비 | P0 | S | — |
| REPO-2 | 열린 PR 정리 | P0 | S | — |
| REPO-3 | v0.1.0 릴리스·CHANGELOG | P1 | S | QA-1 |
| DOC-1 | 문서 슬림화·이력 분리 | P0 | L | — |
| DOC-2 | 문서 불일치 수정·수치 제거 | P0 | M | — |
| DOC-3 | 문서 계약 테스트 축소 | P1 | M | DOC-2 |
| DOC-4 | README 스크린샷·영문판 | P1 | S~M | — |
| QA-1 | Hosted CI 3계층 | P0 | M | — |
| QA-2 | OS 매트릭스 | P1 | S | QA-1 |
| QA-3 | 브라우저 E2E nightly | P1 | M | QA-1 |
| QA-4 | 커버리지·퍼징 | P2 | M | QA-1 |
| QA-5 | 릴리스 게이트 단순화 | P1 | M | QA-1 |
| QA-6 | 수동 수용 체크리스트 | P2 | M | — |
| PRD-1 | 증상별 가이드 프리셋 | P0 | M | — |
| PRD-2 | vantage 명시 | P0 | S~M | — |
| PRD-3 | 로컬 CLI | P1 | M~L | — |
| PRD-4 | Android 온디바이스 프로브 | P1→P2 | L~XL | PRD-2 |
| PRD-5 | 경로 품질(MTR) | P1 | M | — |
| PRD-6 | DNS 심화 | P1 | M | — |
| PRD-7 | TLS·HTTP 심화 | P2 | M | — |
| PRD-8 | 공유용 익명화 | P1 | M | — |
| PRD-9 | 이력·기준선 | P2 | XL | PRD-8 |
| PRD-10/11 | 스케줄·알림·멀티 vantage | P3 | XL | PRD-2, ARC-1 |
| ARC-1 | 비동기 Job + SSE | P1 | L | — |
| ARC-2 | OpenAPI + 코드 생성 | P1 | L | — |
| ARC-3 | 내장 서빙·포트 통일 | P0~P1 | M | — |
| ARC-4 | Web controller/repository 분리 | P2 | L | — |
| ARC-5 | `@ts-check`/TypeScript | P2 | L | ARC-2 |
| ARC-6 | Geo 공급자 추상화 | P2 | M | — |
| ARC-7 | 메트릭 엑스포터 | P2 | M | — |
| SEC-1 | SSRF·파서 퍼징 | P1 | M | — |
| SEC-2 | 취약점 스캔·SBOM | P1 | S | QA-1 |
| SEC-3 | API 키 운영 | P2 | M | — |
| SEC-4 | 레퍼런스 TLS 프록시 | P2 | M | — |
| SEC-5 | 외부 조회 데이터 문서화 | P2 | S | — |

---

## 7. 리스크와 열린 질문

| # | 질문/리스크 | 영향 |
|---|---|---|
| 1 | **주 사용자는 누구인가?** (일반 사용자 / 고객지원 담당 / 네트워크 엔지니어) | 프리셋·문구·CLI 우선순위 결정 |
| 2 | **배포 모델은?** (self-hosted만 vs 공개 SaaS) | public 모드·키 운영·rate limit 투자 규모 |
| 3 | **ARC-1(Job API)이 기존 "무상태 + 엄격 상한" 철학과 충돌하지 않는가** | in-memory·짧은 TTL·키 단위 한도로 시작, 영속 저장은 PRD-9로 분리 |
| 4 | **계약 변경의 파급** (PRD-2, PRD-5, PRD-6은 `compact-v1`·finding 계약 수정) | Go·Web·Android·fixture 동시 갱신 필요 → ARC-2를 앞당기면 비용 감소 |
| 5 | **GeoIP/ASN/RDAP 외부 공급자의 약관·비용·가용성** | 기능 중단·라이선스 위반 가능성 (ARC-6, SEC-5) |
| 6 | **Android 비루트 traceroute 가능 여부** | 불가 시 온디바이스는 DNS/TCP/HTTP/네트워크 상태 중심으로 범위 축소 |
| 7 | **이력 저장 시 개인정보** | 보관 기간·삭제·익명화 정책 없이 PRD-9 착수 금지 |
| 8 | **1인 개발 대비 검증 비용** | QA-5로 일상 PR은 Tier 1만, 엄격 검증은 릴리스 시에만 |
| 9 | **go:embed 전환(ARC-3)이 기존 Web release 검증 파이프라인과 충돌** | nginx 이미지·asset closure 검증을 유지하는 방향으로 점진 전환 |

---

## 8. 첫 2주 실행 체크리스트

**1주차**
- [ ] 열린 PR 4건 내용 확인 후 정리 (REPO-2)
- [ ] LICENSE/SECURITY/CONTRIBUTING, 저장소 설명·토픽 (REPO-1)
- [ ] 부록 A의 문서 불일치 수정 (DOC-2)
- [ ] `.github/workflows/ci.yml` 작성: Tier 1 (`npm ci` → `make test` → `make web-test-syntax` → `make vet`) (QA-1)
- [ ] README에 스크린샷 4장 추가 (DOC-4)

**2주차**
- [ ] Tier 2 워크플로 추가: `make test-race`, Android check, Playwright nightly (QA-1, QA-3)
- [ ] Stage 8/9 공식 종료 결정과 남은 항목 이슈 이관 (QA-5)
- [ ] `IMPLEMENTATION-HISTORY.md`의 과거 스냅샷 분리, `CHANGELOG.md` 시작 (DOC-1)
- [ ] 증상별 프리셋 범위 합의 및 설계 1페이지 (PRD-1)
- [ ] vantage 블록 계약 초안 작성 (PRD-2)

---

## 부록 A. 문서 간 불일치 목록 (수정 대상)

| # | 항목 | 불일치 내용 | 위치 |
|---|---|---|---|
| 1 | Web 테스트 수 | 260 / 283 / 284 / 286 / 291 / 298 / 328 / 620 이 "현재" 또는 "historical"로 혼재 | TESTING, PLAN, IMPLEMENTATION-HISTORY |
| 2 | 오프라인 아카이브 검증기 수 | "API 33 + Web 30 = 63"과 "API 34 + Web 30 = 64"가 같은 문서에 공존 | TESTING |
| 3 | 프로덕션 Web asset 수 | "nine-asset"과 "ten assets"(+ 과거 eight) 혼재 | TESTING, PLAN |
| 4 | 최대 보고서 DOM | 초기/피크 751/784 vs 699/732 | ARCHITECTURE 스냅샷 vs TESTING |
| 5 | Go 버전 | README·PLAN은 Go 1.22 이상, TESTING 릴리스 계약은 Go 1.26.11 고정 → `go.mod`의 `go`/`toolchain`과 함께 정리 필요 | README, PLAN, TESTING |
| 6 | API 기본 포트 | 소스 실행 8080 / Compose 8090 / 정적 HTML 초기값 9090 | README |
| 7 | 계획 문서 위치 | 루트 `PLAN.md` 링크와 `docs/PLAN.md` 링크 혼재 (루트 목록에는 `PLAN.md` 미확인) | IMPLEMENTATION-HISTORY |
| 8 | 게이트 상태 | Stage 8·9가 "pending"으로 반복 기재되나 이후 기능이 병합되어 현재 상태가 불명확 | IMPLEMENTATION-HISTORY, PLAN, TESTING |

> 항목 1~4는 DOC-2의 원칙(변동 수치를 문서에 쓰지 않음)을 적용하면 재발하지 않습니다.

## 부록 B. 비동기 Job API 스케치 (ARC-1)

```
POST   /api/v1/jobs                  요청 본문은 /reports와 동일
  → 202 { "job_id": "<opaque-128bit>", "status_url": "...", "events_url": "..." }

GET    /api/v1/jobs/{id}
  → 200 { "status": "queued|running|completed|failed|cancelled",
          "progress": { "done": 7, "total": 20 },
          "report": { ... }   // completed 일 때만 (compact 옵션 동일 적용)
        }

GET    /api/v1/jobs/{id}/events      text/event-stream (SSE)
  event: result     data: { "target_index": 3, "result": { ... } }
  event: progress   data: { "done": 4, "total": 20 }
  event: done       data: { "status": "completed" }

DELETE /api/v1/jobs/{id}             취소 (idempotent)

GET    /api/v1/reports (POST)        기존 동기 엔드포인트 유지 (내부적으로 Job + 대기)
```

설계 제약(기존 철학 유지)
- 동일한 인증·rate limit·admission 상한 적용. Job 수/메모리 상한과 키 단위 소유권
- 보관은 **in-memory, TTL 15분** 기본. 영속화는 별도 옵션(PRD-9)
- `job_id`는 추측 불가능한 난수. 로그에는 opaque ID만 기록 (대상·IP 금지)
- SSE 이벤트의 `result`는 기존 `finding-contract-v1` 검증을 통과한 형태만 전송

## 부록 C. 권장 문서 구조 (DOC-1)

```
README.md                  사용자용: 한 줄 소개, 스크린샷, 빠른 시작, 결과 읽는 법
README.en.md
CHANGELOG.md               버전별 변경 (구현 이력 이관)
LICENSE / SECURITY.md / CONTRIBUTING.md
docs/
  architecture.md          현재 구조만 (과거 스냅샷 제외)
  api.md  + openapi.yaml   요청/응답/오류 코드
  operations.md            설정, 보안 모드, 자원 제한, 배포
  testing.md               명령·계층·수용 기준·수동 체크리스트(날짜/환경/결과 기록)
  roadmap.md               PLAN.md 대체. 이 문서의 로드맵과 통합
  geo.md, ip-context.md    기능별 계약·한계
  adr/                     설계 결정 기록 (예: 0001-job-api-vs-sync.md)
  archive/                 불가피하게 보존할 과거 문서만
```

문서 작성 원칙
1. 변동 수치(테스트 수, asset 수, DOM 최대값)는 쓰지 않는다. 필요하면 CI가 생성한다
2. "완료/미완료" 선언은 문서가 아니라 이슈·릴리스 노트가 소유한다
3. 한 문서는 한 가지 질문에 답한다 (사용법 / 설계 / 운영 / 검증)
4. 불변 계약(상한값, 에러 코드)만 코드와 자동 대조한다

## 부록 D. 분석에서 확인하지 못한 항목 (착수 전 확인)

- 실제 소스 구조와 코드 품질 (패키지 경계, 순환 의존, 에러 처리, 동시성 구현)
- `.github/workflows` 존재 여부 (hosted CI가 부분적으로 있을 수 있음)
- `go.mod`의 `go`/`toolchain` 지시자, `go.sum` 및 외부 의존성 목록
- GeoIP/ASN 공급자 이름과 비활성화 옵션
- `SPEC.md`와 현행 `API.md`/`OPERATIONS.md`의 최신 내용
- 열린 PR 4건과 Issues의 내용
- LICENSE 파일 존재 여부
- DNS·HTTP 검사의 현재 세부 구현 범위 (PRD-6/7의 중복 작업 방지를 위해 확인)

