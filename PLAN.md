# CheckNetwork 개선 실행계획 v1.1

**대상 저장소:** `piecer/revealnetworktrouble`
**목표:** 공개 릴리스 가능 상태 달성
**개발 기간:** 5주, 약 20~25인일(추정)
**핵심 원칙:** 기존 진단 정확성·보안 계약 보존, 기능 확장 최소화

## 진행 기록

원래의 5단계 범위와 완료 조건을 유지한다. 실행 결과·환경·정확한 SHA·실패 원인·미검증 항목은 [Phase 1 검증 기록](docs/PHASE1-VERIFICATION.md)에 분리한다. 작업 트리 검증, HEAD 아카이브 검증, 릴리스 검증은 서로 대체하지 않는다.

2026-11-07 재개: Phase 1의 실제 startup WARN 회귀를 보완했고, Phase 2 워크플로·Make 진입점·보안 게이트·Dependabot을 추가했다. 독립 리뷰에서 확인한 undici 추가 advisory와 cold runner의 borrowed base 준비 누락도 보완했고, 로컬 Full/Quick/보안 검사를 다시 통과했다. 두 지적의 독립 재검토도 승인됐다. GitHub required check 활성화·원격 실행 확인은 아직 미완료이며 Phase 2 전체 완료로 간주하지 않는다. [CI 운영 절차](docs/CI.md), [Phase 2 검증 기록](docs/PHASE2-VERIFICATION.md)을 참조한다.

이전 작성 중 들어간 "compose.yaml 누락", "Web 298 tests", "클린 환경 완료" 주장은 잘못된 기록이었다. 최초 실패는 로컬 LAN 바인딩과 기본 loopback 계약의 불일치였고, 당시 Node 테스트 결과는 892개였다. 새 범위로 추가했던 CSP 및 Android INTERNET permission 제거 항목은 원래 계획에 없으므로 제외한다.

## Phase 1. 기준선 확립 및 배포 안전성 (1주 차)

### 작업 1-1. 현재 HEAD 검증

- 새 클린 환경에서 `make ci` 실행
- `make test-race`, Web syntax, 빌드 결과 검증
- Android debug/release 테스트 및 lint 결과 수집
- 통과·실패·미실행 항목을 서로 구분해 기록
- 현재 테스트 개수, 총 실행시간, 실패율 측정

**완료 조건:** 커밋 SHA와 실행 환경이 명시된 검증 결과 확보. 필수 검사 실패는 릴리스 차단.

### 작업 1-2. Trusted-local/Public 모드 안전성 강화

- 직접 실행 시 trusted-local과 non-loopback 바인딩의 위험을 명시적으로 처리
- 기존 Compose의 컨테이너 내부 바인딩과 호스트 loopback 공개 설정은 유지
- Public 모드의 사설 IP, metadata IP, IPv4/IPv6, DNS 재해석, HTTP redirect 차단 재검증
- Reverse proxy 사용 시 인증 및 rate limit 경계를 문서화
- 위험한 배포 조합에 대한 자동 회귀 테스트 추가

**완료 조건:** 기존 Compose 실행이 정상 동작하고, 직접 실행·컨테이너·프록시 배포의 위험 조합을 검증했으며 인증·SSRF 우회 회귀가 없음.

## Phase 2. CI/CD 자동화 (2주 차) — 로컬 구현·검증, 원격 활성화 대기

GitHub Actions 구축. 기존 Makefile 명령을 재사용하고 중복 검증 로직은 작성하지 않는다.

| 실행 계층 | 검사 | 시점 |
|---|---|---|
| Quick | Go tests, Web tests, syntax, vet, build | 모든 PR |
| Full | Race, Android JVM tests, Android lint/build | Main 및 릴리스 후보 |
| Security | 의존성 취약점, secret scanning, SSRF 회귀 | PR 및 정기 실행 |
| Release | Clean SHA, Docker archive, 재현 빌드 | 릴리스 후보 |

- `.github/workflows/ci.yml`, `release.yml` 생성; Node/Go/JDK·SDK 버전 고정.
- Test failure 시 merge 차단(필수 status check), CI 로그·산출물 업로드.
- Dependabot 설정 및 취약점 대응 기준 정리.

**완료 조건:** GitHub 필수 status checks가 정상 작동하며, 테스트 실패·빌드 실패·위험 설정이 성공 상태로 처리되지 않음.

## Phase 3. 문서 및 공개 프로젝트 정비 (3주 차) — pending

### 작업 3-1. 아키텍처 문서 재구성

`docs/ARCHITECTURE.md`를 다음 구조로 정리: 1) 시스템 구성·컴포넌트, 2) 진단 데이터 흐름, 3) Checker/Runner/Analysis 역할, 4) 보안 신뢰 경계, 5) 동시성·시간제한 모델, 6) Web/Android 구조, 7) 확장 방법·설계상 한계. Stage별 개발 기록과 과거 수치는 `docs/IMPLEMENTATION-HISTORY.md`에 보존한다.

### 작업 3-2. README 개선

Web UI 스크린샷 3~4개, 사용 시나리오(DNS 장애/HTTPS 장애/경로 관측 이상), 서버 측 측정 vs 사용자 기기 측정 차이, Docker/Go/Android 빠른 시작, 한국어 유지 + 영어 README 추가, API 포트 기본값·실행 방식별 설정 정리.

### 작업 3-3. 오픈소스 운영 파일

`LICENSE`(배포 정책 결정 후 MIT 또는 Apache-2.0), `CONTRIBUTING.md`, `SECURITY.md`, `CHANGELOG.md`, GitHub Description/Topics/README 배지.

**완료 조건:** 신규 사용자가 README만으로 기본 진단을 실행할 수 있고, 문서에서 현재 구현·과거 이력·미완료 기능을 명확히 구분할 수 있음.

## Phase 4. 실제 환경 검증 및 UX 개선 (4주 차) — pending

### 작업 4-1. Web 사용성 검증

Chrome/Firefox/Safari, PC 및 320/375/400px 모바일 화면, 2D/3D Topology 드래그·확대·선택, 대용량 노드 라벨 겹침, keyboard navigation·screen reader, 실패·시간초과·취소·데이터 미관측 상태 안내 개선.

### 작업 4-2. Android 실환경 검증

Emulator 및 실제 단말, debug/release 네트워크 정책 차이, 화면 회전·앱 재생성·요청 취소, report 공유/FileProvider/프로세스 종료 시나리오, TalkBack·공유 UI 접근성.

### 작업 4-3. 진단 결과 이해도 개선

Observed/Inferred/Unavailable 구분 일관 사용, 서버 측 관측 위치 명시, 오류 메시지에 다음 점검 단계 연결, confidence가 원인 확률로 오해되지 않게 안내.

**완료 조건:** 주요 시나리오에서 심각한 사용 불가·잘못된 진단 표현·보고서 공유 결함이 없으며, 미검증 플랫폼은 "지원 검증 완료"로 표시하지 않음. (실기기 acceptance는 자동화 게이트 밖의 별도 수동 gate — OPERATIONS.md 정책 유지)

## Phase 5. 릴리스 후보 확정 (5주 차) — pending

### 작업 5-1. 릴리스 검증

`make ci` → clean exact-SHA archive 검증(`make ci-clean-archive`) → `make verify-release-real`(exact SHA, umask 3 pass) → Docker/Compose 호환성·readiness → 재현 빌드·산출물 일치 → 보안 회귀 확인 → 독립 리뷰.

### 작업 5-2. 최초 릴리스 발행

`v0.1.0-rc.1` 후보 생성 → 차단 결함 수정·후보 재검증 → 릴리스 노트·체크섬·지원 플랫폼 안내 → 승인된 바이너리/컨테이너 산출물 배포(`make release`) → 수용 완료 시 `v0.1.0` 태그.

**완료 조건:** 최종 태그가 검증한 정확한 커밋과 일치하고, 모든 필수 게이트 통과, 릴리스 산출물을 독립 환경에서 설치·실행 가능.

## 릴리스 범위에서 제외할 항목

다음은 신규 개발 프로젝트로 분리한다:

- 새로운 진단 Checker의 대규모 확장
- 다중 관측 지점(Multi-vantage) 기능
- Packet loss, jitter, throughput, MTU 측정 추가
- 진단 보고서 이력 저장·비교 기능
- AI 기반 장애 원인 추론
- Kubernetes/대규모 SaaS 운영 기능

이번 릴리스에서는 새로운 기능보다 **기존 기능의 신뢰할 수 있는 배포**를 우선한다.

## 실행 시 주의사항

1. **fail-first**: 동작 변경은 먼저 실패하는 테스트를 작성한 뒤 구현한다.
2. **게이트**: 해당 플랫폼 gate + 최종 `make ci` 전체 통과. Android는 `make android-check`.
3. **증거**: 종료 코드, 실제 로그, 테스트 집계로 성공 여부를 판단한다. 파이프라인 마지막 명령(`tee` 등)의 성공을 검사 성공으로 간주하지 않는다.
4. **승인 경계**: commit, push, 태그, 라이선스 선택, 운영 재배포는 별도 사용자 지시 없이 수행하지 않는다.
5. **기존 작업 보존**: 무관한 수정·미추적 파일은 삭제하거나 되돌리지 않는다. 검증은 격리된 소스/프로세스에서 수행한다.
