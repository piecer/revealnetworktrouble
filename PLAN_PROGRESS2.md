# PLAN 진행 현황 및 후속 작업 2차 점검

- 점검일: 2026-11-10 (KST)
- 기준 계획: 저장소 루트 `PLAN.md`의 개선 실행계획 v1.1. `docs/PLAN.md`는 과거 Stage별 구현 계획이므로 두 문서의 단계를 혼합하지 않는다.
- 기준 HEAD: `734885ae9b5bfa22070675c7d2ea17b87e8cf0c5` (`main`, 커밋일 2026-11-07).
- 점검 대상: 위 HEAD와 기존 미커밋 작업 트리. 문서에 적힌 상태뿐 아니라 실제 파일·구현·회귀 테스트·이번 실행 결과를 대조했다.
- 변경 범위: 이 보고서만 추가한다. 기존 코드·문서·로컬 Compose·백업 파일을 수정하거나 commit/push/tag/운영 재배포하지 않는다.

위 범위와 1~10절은 최초 감사 시점의 기록이다. 이어진 사용자 요청으로 남은 작업을 실행했으며, 로컬 수정·격리 검증·관련 파일의 로컬 커밋까지 별도 승인받았다. 후속 현황은 11절에 기록한다. 원격 쓰기·태그/릴리스 발행·운영 재배포는 이번 승인 범위 밖이다.

## 1. 핵심 결론

**기준선·배포 안전성 및 CI 구현은 상당 부분 끝났고, Phase 3의 문서 정비도 이미 작업 트리에 있다. 그러나 GitHub 강제 게이트, 문서 변경의 확정·반영, 실제 플랫폼 수용 시험과 최종 릴리스 검증은 남아 있다. 공개 릴리스 가능 상태로 판정할 수는 없다.**

| 계획 단계 | 점검 결과 | 완료 판정의 경계 |
|---|---|---|
| Phase 1. 기준선·배포 안전성 | 로컬 구현 및 자동 검증 완료 | 과거 HEAD 아카이브 기준선과 이번 변경 트리 CI를 확보했다. 최종 후보의 clean exact-SHA/실제 배포·프록시 검증은 별도다. |
| Phase 2. CI/CD 자동화 | 로컬 구현·검증 완료, 원격 활성화 미확인 | 워크플로·정책·Dependabot은 HEAD에 있다. 실제 GitHub required check의 merge 차단을 완료했다는 증거는 확보하지 못했다. |
| Phase 3. 문서·공개 프로젝트 정비 | 로컬 산출물 대부분 구현, 확정·원격 작업 잔여 | 한국어/영어 README, 아키텍처/이력 분리, 화면 4개, 운영 파일이 있다. 상당수가 미커밋이며 원격 metadata와 보안 신고 채널은 별도다. |
| Phase 4. 실환경·UX | 구현·자동 회귀 일부 확보, 수동 수용 미완료 | Web/Android 표현·취소·공유 계약은 자동 검증된다. 물리 브라우저·화면 판독기·실단말 결과로 확대할 수 없다. |
| Phase 5. 릴리스 후보 확정 | 검증 도구 구현, 최종 후보·발행 미완료 | Docker builder 갱신, 최종 바이너리 보안 검사, clean SHA 재현 검증, 후보 태그·배포가 남았다. |

여기서 “완료”는 해당 범위의 구현과 실행 증거가 있다는 뜻이다. “미확인”은 이 점검에서 완료 증거를 얻지 못했다는 뜻이며, 원격 상태나 과거 작업이 반드시 없다는 단정이 아니다. 항목별 작업량과 수용 조건이 다르므로 임의의 전체 진행률은 계산하지 않는다.

## 2. Phase 1 — 기준선 확립 및 배포 안전성

### 작업 1-1. 현재 HEAD 검증

확인된 진행 사항:

- [x] 기존 기준 HEAD `f90949587d3a45580ec937b02d1acdecb5fe02f7`를 별도 소스 아카이브로 추출한 Full CI 기록이 있다. 공유 호스트 캐시를 사용한 소스 격리이며 새 VM 검증은 아니다. 근거: `docs/PHASE1-VERIFICATION.md:13`.
- [x] 이번에는 현재 작업 트리의 소스 431개를 저장소 밖으로 복사해 `make ci`를 실제 재실행했다. Go/Web/race/vet/build, Android debug/release JVM tests·lint·assemble를 통과했다. 구체적인 결과와 skip은 7절에 기록한다.
- [x] SHA·툴체인·소스 해시·명령 종료 코드·실행 시간·테스트 집계를 확보했다.
- [x] 실행된 테스트의 실패와 opt-in 미실행을 분리했다. 이번 Go 최상위 테스트는 522 pass / 6 skip이며 skip을 성공 수에 넣지 않았다.

남은 작업:

- [ ] 공개 후보를 확정한 후 그 clean commit의 정확한 SHA에서 `make ci-clean-archive`를 실행하고 증거를 다시 보존한다. 이번 검사는 미커밋 문서를 포함한 소스 snapshot이지 exact-SHA 릴리스 증거가 아니다.
- [ ] 새 VM/독립 호스트의 검증이 필요한 경우 별도 실행한다. 같은 호스트의 캐시와 SDK를 사용한 이번 결과를 독립 환경 설치·실행으로 보고하지 않는다.

### 작업 1-2. Trusted-local/Public 모드 안전성

확인된 진행 사항:

- [x] trusted-local + non-loopback bind를 기본 거부하고 정확한 `CHECKNETWORK_EXPOSE_TRUSTED_LOCAL=yes`만 승인한다. Public의 API key·양수 rate limit 요구는 유지된다. 근거: `cmd/checknetwork-api/main.go:267`.
- [x] 실제 startup 자식 프로세스에서 WARN의 정확한 문구·주소·단일 발생을 검사한다. 근거: `cmd/checknetwork-api/exposure_startup_test.go:15`.
- [x] 기본 Compose의 컨테이너 내부 `:8080`과 host loopback API `8090`/Web `3000`을 분리해 유지한다. 근거: `compose.yaml:9`, `compose.yaml:18`, `compose.yaml:39`.
- [x] Public IP 대역 제한, mixed DNS, 검증한 IP로 dial, DNS 재해석, redirect 및 custom transport 우회 방지 회귀가 있다. 이번 Full 및 별도 `make security-test`도 통과했다. 근거: `backend/diagnostic/network_policy_test.go:68`, `backend/diagnostic/network_policy_test.go:104`, `backend/diagnostic/network_policy_test.go:286`, `backend/diagnostic/network_policy_test.go:554`.
- [x] forwarded header와 허용 CORS Origin으로 인증·직접 연결 IP별 quota를 우회하지 못함을 검증한다. 근거: `backend/api/proxy_boundary_test.go:13`.
- [x] 직접 실행·컨테이너·프록시의 신뢰 경계와 프록시 측 TLS/사용자별 제한 책임을 문서화했다. 근거: `docs/OPERATIONS.md:32`.
- [x] 격리 컨테이너에서 실제 바이너리 시작 거부·WARN·health/readiness·Public 인증/제한을 검증한 기존 기록이 있다. 새 릴리스 이미지를 배포한 결과는 아니다. 근거: `docs/PHASE1-VERIFICATION.md:94`.

남은 작업:

- [ ] 최종 Dockerfile 산출물과 Compose의 실제 build/start/readiness를 격리 환경에서 검증한다. resolved config 테스트와 borrowed-base mount smoke만으로 최종 이미지 실행을 보장하지 않는다.
- [ ] 실제 TLS reverse proxy에서 인증·quota·HTTP/2 deadline/cancellation을 검증한다.
- [ ] 기존 LAN trusted-local 구성의 노출 정책을 운영자가 별도 승인·점검한다. 과거 기록의 `compose.local.yaml`은 기본 Compose와 merge하는 파일이 아니며, 이 점검에서는 운영 상태를 재조회하거나 재시작하지 않았다. 근거: `docs/PHASE1-VERIFICATION.md:117`.

## 3. Phase 2 — CI/CD 자동화

확인된 진행 사항:

- [x] PR Quick/Security, main·merge_group·정기·수동 Full과 `always()` 기반 `CI required` 완료 판정이 구현됐다. 근거: `.github/workflows/ci.yml:3`, `.github/workflows/ci.yml:41`, `.github/workflows/ci.yml:90`.
- [x] 기존 Make 진입점을 재사용한다. Quick은 test/syntax/vet/build, Full은 Android 포함 CI, Security는 보안 회귀·Go/npm/secret scanner를 실행하도록 구성됐다. 근거: `Makefile:155`, `Makefile:158`, `Makefile:170`, `scripts/ci-inner.sh:37`.
- [x] Go 1.26.10, Node 22.23.1, JDK 17.0.21+9, Android 도구 버전을 고정하고 외부 Actions를 SHA에 고정했다. 근거: `.github/actions/toolchain/action.yml:18`.
- [x] 로그·Android 결과·후보 artifact의 업로드 및 보존 기간이 설정됐다. 후보 업로드는 이전 검사 성공 뒤에만 수행한다. 근거: `.github/workflows/ci.yml:57`, `.github/workflows/release.yml:60`.
- [x] Dependabot의 Actions/Go/npm/Gradle/Docker 주간 점검 설정이 있다. 근거: `.github/dependabot.yml:3`.
- [x] 실패·취소·누락·예기치 않은 skip, scanner 실패, 잘못된 release 입력을 fail-closed로 검사하는 정책 테스트가 있다. 이번 Full의 `ci-policy`와 별도 actionlint도 통과했다.
- [x] release의 exact-SHA/clean/VERSION 검사, borrowed base 준비, clean archive, production verifier, 추출 binary scanner 순서가 구현됐다. 자동 release 게시나 registry push는 구현된 동작으로 주장하지 않는다. 근거: `.github/workflows/release.yml:36`.
- [x] Go/npm 취약점 검출과 최소 수정, scanner 재실행 및 독립 리뷰 기록이 있다. 이는 2026-11-07 당시의 기록이다. 근거: `docs/PHASE2-VERIFICATION.md:22`, `docs/PHASE2-VERIFICATION.md:58`.

남은 작업:

- [ ] 승인된 변경을 원격에 반영하고 PR/main/필요 시 merge_group에서 Quick/Full/Security와 `CI required`가 실제 생성·실행되는지 확인한다.
- [ ] main branch protection/ruleset에 필수 check를 지정하고, 의도적인 실패 PR이 merge되지 않는지 확인한 뒤 설정을 readback한다.
- [ ] cold runner에서 Release workflow 전체를 실행해 base pull·archive 생성·smoke·산출물 업로드까지 확인한다.
- [ ] 최종 후보 SHA에서 `make ci-security`를 다시 실행한다. 이번 점검은 `security-test`만 재실행했으며 govulncheck/npm audit/Gitleaks의 최신 결과를 새로 확보하지 않았다.

원격 상태 주의: 기존 마지막 조회는 2026-11-07의 `Branch not protected (HTTP 404)`다(`docs/PHASE2-VERIFICATION.md:76`). 이번에는 GitHub API를 재조회하지 않았으므로 이를 2026-11-10 현재 설정이라고 단정하지 않는다. 로컬 YAML과 정책 테스트 통과만으로 Phase 2의 최종 완료 조건을 충족했다고 표시하지 않는다.

## 4. Phase 3 — 문서 및 공개 프로젝트 정비

루트 `PLAN.md:55`에는 pending으로 남아 있지만, 현재 작업 트리에는 아래 산출물이 이미 있다. 따라서 “착수 전”이 아니라 “로컬 산출물 구현, 확정·반영 필요”로 보는 것이 맞다.

### 작업 3-1. 아키텍처 문서 재구성

- [x] 시스템 구성, 데이터 흐름, Checker/Runner/Analysis, 보안 경계, 동시성·시간제한, Web/Android, 확장 및 설계상 한계를 8개 절로 정리했다. 계획의 7가지 주제를 모두 포함하고 한계를 별도 절로 분리한 구성이다. 근거: `docs/ARCHITECTURE.md:5`, `docs/ARCHITECTURE.md:18`, `docs/ARCHITECTURE.md:36`, `docs/ARCHITECTURE.md:45`, `docs/ARCHITECTURE.md:53`, `docs/ARCHITECTURE.md:68`, `docs/ARCHITECTURE.md:78`, `docs/ARCHITECTURE.md:84`.
- [x] 과거 README/아키텍처 설명과 Stage별 수치를 `docs/IMPLEMENTATION-HISTORY.md`에 historical snapshot으로 보존했다. 근거: `docs/IMPLEMENTATION-HISTORY.md:3`.
- [x] 기존 문서 계약 검사를 이력 분리와 연결하고 현재 진입점·로컬 링크용 테스트를 추가했다. 이번 Full CI에서 통과했다. 근거: `backend/diagnostic/documentation_contract_test.go:453`, `backend/diagnostic/public_documentation_contract_test.go:12`.
- [ ] 변경 문서·이력·테스트를 함께 검토·확정하고 승인된 커밋에 반영한다. 이력 파일 없이 변경된 문서 계약 테스트만 반영하면 필요한 파일이 빠진다.

### 작업 3-2. README 개선

- [x] 한국어 README와 영어 `README.en.md`가 있다.
- [x] DNS 장애/HTTPS 장애/경로 관측 이상 시나리오, 서버 측 관측 위치, confidence 해석, Docker/Go/Android 빠른 시작을 설명한다. 근거: `README.md:9`, `README.md:13`, `README.md:29`, `README.md:53`, `README.md:77`.
- [x] 직접 Go 실행 8080, 기본 Compose 8090, 정적/canonical Web 기본값 9090 및 Web 3000의 차이를 설명한다. 근거: `README.md:25`, `README.md:43`.
- [x] 진단·토폴로지·Geo 지도·공통 CIDR 화면의 PNG 4개가 존재하고 두 README에서 참조한다. PNG signature·크기·해시를 이번에 확인했다. 근거: `README.md:67`, `README.en.md:65`, `docs/images/`.
- [x] fixture 화면과 실제 API 진단의 의미를 문구상 구분하고 현재 기능과 과거 공통 경로 초안을 분리한다. 근거: `README.md:65`, `README.md:75`.
- [ ] 신규 사용자가 문서만 보고 실행하는 독립 환경 acceptance를 수행한다. 이번 Full CI의 링크/문구 계약 성공은 실제 Docker 빠른 시작·외부 진단 성공을 대신하지 않는다.
- [ ] 스크린샷 출처와 재현 로그를 최종 문서 변경과 함께 보존한다. 이번에는 PNG 존재/형식과 생성 스크립트를 확인했지만 생성 실행을 재현하거나 과거 스크린샷의 실제 요청을 독립 검증하지 않았다.

### 작업 3-3. 오픈소스 운영 파일

- [x] `LICENSE`, `CONTRIBUTING.md`, `SECURITY.md`, `CHANGELOG.md`가 존재한다. LICENSE는 Apache-2.0 본문을 포함하고 공개 문서 계약 검사도 통과했다.
- [x] 기존 문서에는 Apache-2.0을 사용자 결정에 따라 추가했다고 기록돼 있다. 이 점검이 라이선스를 새로 선택하거나 원래 승인 대화를 검증한 것은 아니다. 근거: `CHANGELOG.md:20`, `CONTRIBUTING.md:36`.
- [x] README에 CI 배지 링크가 있다. 링크가 있다는 사실과 hosted workflow가 통과했다는 사실은 별개다. 근거: `README.md:5`.
- [ ] 문서·라이선스·이미지·테스트·브라우저 스크립트를 검토 후 함께 반영한다. 새 파일 대부분은 현재 미추적이다.
- [ ] GitHub Description/Topics의 설정과 공개 README/배지 노출을 확인한다. 이번에는 원격 metadata를 재조회·변경하지 않았다.
- [ ] 비공개 취약점 신고 채널·담당자·보안 지원 범위를 확정하고 확인 가능한 경로로 안내한다. 현재 SECURITY 문서는 이 항목이 미확정이라고 명시한다. 근거: `SECURITY.md:5`.

## 5. Phase 4 — 실제 환경 검증 및 UX 개선

### 작업 4-1. Web 사용성 검증

확인된 진행 사항:

- [x] 2D/3D Canvas, drag/zoom/선택, semantic inspector, keyboard/fallback 및 상태 처리에 대한 source·DOM 회귀 검사가 있다. 이번 Node TAP 892개와 별도 계약 스크립트가 통과했다. 근거: `frontend/package.json:9`, `docs/TESTING.md:108`.
- [x] 라벨 충돌·최대 입력·stale cleanup·빈 상태·브라우저 replay용 스크립트와 기존 검증 범위 문서가 있다. 근거: `docs/TESTING.md:55`, `docs/TESTING.md:72`, `docs/TESTING.md:80`.
- [x] `frontend/documentation.browser.cjs`는 1440/320/375/400px에서 진단·2D·3D·Geo·라벨·공통 CIDR을 점검하도록 작성돼 있다. 진단은 실제 API, topology/Geo는 명시적인 fixture replay 경계다. 이번에는 syntax만 검사됐고 브라우저 실행은 하지 않았다. 근거: `frontend/documentation.browser.cjs:16`, `frontend/documentation.browser.cjs:37`, `frontend/documentation.browser.cjs:43`.

남은 작업:

- [ ] 실제 Chrome/Firefox/Safari에서 PC와 320/375/400px 화면의 조작·가로 overflow·확대/선택·fullscreen을 확인한다.
- [ ] 고밀도 노드·라벨의 가독성, keyboard-only 흐름, 실제 screen reader 및 Canvas fallback을 수동 검증한다. 좁은 화면의 밀집 라벨·노드 가독성은 기존 문서에도 제한으로 남아 있어 해결 완료로 표시하지 않는다. 근거: `docs/TESTING.md:78`, `docs/TESTING.md:94`.
- [ ] 실패·시간초과·취소·미관측 시나리오의 설명과 복구 행동을 실제 UI에서 확인하고 발견한 결함만 보완한다.
- [ ] 필요 시 선택적 Chromium gate를 별도 실행한다. 이번 기본 CI에서 browser opt-in이 skip됐다는 사실을 브라우저 통과로 바꾸지 않는다.

### 작업 4-2. Android 실환경 검증

- [x] debug/release JVM·Robolectric 테스트 각각 764개, 두 variant lint·assemble가 이번에 통과했다.
- [x] 네트워크 정책, owner/cancel/recreation, detached render, raw share/FileProvider/lease 경계의 자동 계약과 테스트가 있다. 근거: `docs/TESTING.md:117`, `docs/TESTING.md:122`, `docs/TESTING.md:139`.
- [ ] Emulator 및 실제 단말에서 debug/release 네트워크 정책, 회전·재생성·취소를 확인한다.
- [ ] OEM share sheet, FileProvider, 프로세스 종료 후 grant와 이미 열린 descriptor를 검증한다.
- [ ] TalkBack/Switch Access와 공유 UI 접근성을 실제로 확인한다.

JVM 테스트나 APK 생성은 위 실단말 수용 시험의 대체 증거가 아니다. 원래 운영 정책의 별도 수동 gate를 유지한다(`docs/OPERATIONS.md:24`, `docs/TESTING.md:150`).

### 작업 4-3. 진단 결과 이해도

- [x] observed/inferred/unavailable, supporting evidence/coverage/next action, 서버 측 관측 위치와 confidence의 한계를 현재 문서·producer/consumer 계약에서 다룬다. Web/Android에도 근거 직접성·관측 한계·다음 행동의 presentation 경로가 있다. 근거: `docs/ARCHITECTURE.md:34`, `docs/ARCHITECTURE.md:40`, `README.md:9`, `frontend/app.js:502`, `android/app/src/main/java/com/checknetwork/app/core/AnalysisPresentationRegistry.java:145`.
- [ ] Observed/Inferred/Unavailable 구분이 모든 관련 화면·상태·내보내기에서 일관적인지 대조한다. 일부 presentation 경로와 계약 테스트의 존재만으로 전체 화면 일관성을 완료 처리하지 않는다.
- [ ] 실제 사용자가 관측/추정/미관측과 다음 행동을 구분하는지 주요 장애 시나리오로 수용 검증한다. 자동 schema/문구 검사가 이해도까지 입증하지는 않는다.

## 6. Phase 5 — 릴리스 후보 확정

### 작업 5-1. 릴리스 검증

확인된 기반:

- [x] `make ci-clean-archive`, `make verify-release-real`, `make release`와 offline archive/재현성/resource ownership 검증 도구가 있다. 근거: `Makefile:101`, `Makefile:107`, `Makefile:147`.
- [x] clean exact SHA, 두 번 빌드한 archive 비교, umask 077/027/000의 production 검증과 추출 binary 취약점 검사까지 workflow에 연결됐다. 근거: `.github/workflows/release.yml:36`.
- [x] VERSION과 태그 일치 및 잘못된 revision/dirty tree 거부 정책이 있다. 근거: `docs/CI.md:59`.

남은 작업 및 차단 요인:

- [ ] **Docker Go builder와 validator 계약 갱신:** CI는 Go 1.26.10이지만 Dockerfile은 여전히 Go 1.22.12를 요구한다. pinned image digest, archive validator/fixture, 문서와 실제 재현 빌드를 함께 검토해야 한다. 근거: `Dockerfile:1`, `Dockerfile:13`, `docs/CI.md:51`.
- [ ] **최종 binary 보안 검증:** 수정된 source 툴체인 검사만으로 Docker release binary를 안전하다고 판단하지 않는다. 실제 추출 산출물의 `make release-vuln` 통과가 필요하다. 이번에는 최신 취약점 DB 조회나 최종 binary scan을 하지 않았다.
- [ ] **후보 SHA 확정:** 관련 미커밋 작업만 승인 후 커밋하고 clean 상태를 확보한다. 이 보고서 작성 요청을 commit/push/태그 승인으로 해석하지 않는다.
- [ ] 최종 후보에서 `make ci` → `make ci-clean-archive` → `make verify-release-real REVISION=<exact SHA>`를 실행하고 raw log·digest·실제 종료 코드를 보존한다.
- [ ] Docker/Compose의 실제 호환성·readiness와 독립 환경 설치·실행, 재현 산출물 일치를 검증한다.
- [ ] 최종 보안 회귀와 독립 리뷰를 수행하고 Phase 4의 수동 acceptance를 닫는다. 이번 진행 현황 점검은 최종 릴리스 코드 리뷰 승인이 아니다.

### 작업 5-2. 최초 릴리스 발행

- [ ] `v0.1.0-rc.1` 후보용 VERSION/tag 정합성, 릴리스 노트·체크섬·지원 플랫폼을 준비하고 승인 후 후보를 발행한다.
- [ ] 후보 수용 후 검증한 정확한 커밋에 `v0.1.0`을 태그하고 승인된 binary/container를 배포한다. 현재 workflow는 후보 검증·artifact 보관이지 GitHub Release 작성/registry push 자동화가 아니다.

이번 `git tag --list` 결과는 비어 있었다. 이는 **로컬 태그 부재**만 확인하며 원격 태그/릴리스 부재를 독립적으로 입증하지 않는다. `VERSION=0.1.0` 자체도 공개 릴리스 완료를 뜻하지 않는다.

## 7. 이번에 실제 실행한 검증

### 환경과 소스 경계

- Linux/amd64, Go `1.26.10`을 `GOTOOLCHAIN`으로 명시 적용했다. 기본 `go version`은 `1.26.8`이므로 기본 설치와 검증에 사용한 버전을 구분한다.
- Node `v22.23.1`, npm `10.9.8`, Temurin JDK `17.0.21+9`.
- Android platform 35, build-tools 35.0.0 및 기존 Gradle wrapper 사용. Docker Compose CLI `v5.7.0`.
- 저장소 밖에 tracked + nonignored untracked 소스 431개를 복사했다. `.git`, 기존 node_modules, Android build 출력은 복사하지 않았고 Full CI가 의존성과 산출물을 새로 준비했다.
- `GOFLAGS=-buildvcs=false`를 적용했다. 호스트 툴체인·Go/npm/Gradle 캐시·Android SDK는 공유한다. 새 VM·원격 runner·clean HEAD archive 검증이 아니다.
- 기존 소스와 snapshot의 해시가 CI 전후 일치했다. 이 보고서는 snapshot 생성 이후 추가했으며 별도의 문서 검사를 적용한다.

### 실행 결과

| 검사 | 결과 | 실행 시간 / 범위 |
|---|---|---|
| 격리 snapshot `make ci` | 종료 0 | 332.741초, CI_OK 15개, CI_FAILED 없음 |
| Go 일반 테스트 | 522 최상위 pass / 6 skip / 실패 없음 | 하위 테스트 포함 pass event는 1,298개. 최상위와 합산하지 않는다. |
| Web Node TAP | 892 pass / 0 fail / 0 skip | `app.test.js`, `common-prefix.test.js` 별도 계약 스크립트도 성공 |
| Go race / vet / build | 모두 통과 | Full CI에 포함 |
| Web syntax | 통과 | 신규 `documentation.browser.cjs` 구문 검사 포함. 브라우저 실행 아님 |
| Android debug JVM | 764 tests / 0 failure / 0 error / 0 skip | direct-child XML 44개에서 집계 |
| Android release JVM | 764 tests / 0 failure / 0 error / 0 skip | direct-child XML 44개에서 집계 |
| Android wrapper / env / lint / assemble | 통과 | debug/release 모두 수행 |
| `make ci-workflows` | 종료 0 | 0.515초, 원래 작업 트리에서 actionlint 실행 |
| 격리 snapshot `make security-test` | 종료 0 | 19.194초, Public/SSRF/배포 노출 회귀. 의존성 scanner 전체 아님 |
| `git diff --check` | 종료 0 | 기존 tracked 변경의 whitespace 검사. 신규 보고서는 별도 최종 검사 |
| README PNG inventory | 정상 PNG 4개 | signature·크기·SHA-256 확인. 화면 생성 재실행 아님 |

기본 Go 검사에서 skip된 항목:

1. `TestCORSRetryAfterBrowser`
2. `TestLiveGNUInetutilsTracerouteLoopbackIsReadyAndSucceeds`
3. `TestReleaseVerifierResourceOwnershipLifecycleRealDocker`
4. `TestAPIOfflineArchiveRealBuildx`
5. `TestWebOfflineArchiveRealBuildx`
6. `TestLiveGNUInetutilsReadinessAndTracerouteShareWorkingCapability`

이번 미실행: 의존성 취약점/secret scanner 전체, 선택적 실제 브라우저 gate, 실제 Docker/Buildx release gate, HTTP/2 TLS proxy, hosted CI/branch protection, emulator/실기기·screen reader, 독립 환경 설치·공개 배포. 과거 기록이나 이번 자동 테스트 수로 이들을 완료 처리하지 않는다.

증거 디렉터리:

`/home/piecer/.hermes/cache/scratch/checknetwork-plan-progress2-20261110-4jfpbcs1/`

- `metadata.json`: 기준 SHA, 기존 git status, source 목록·SHA-256과 격리 범위.
- `make-ci.log`, `ci-evidence/`: Full 원본 로그, Go JSON, Node TAP, Android 검사 로그.
- `ci-summary.json`: 실제 make 종료 코드·시간·게이트·테스트 집계·소스 불변성.
- `workflow-lint.log`, `security-regressions.log`, `extra-summary.json`: 별도 실행 결과.
- `screenshot-inventory.json`: 기존 PNG 4개의 크기·해시.
- `source/android/app/build/test-results/`: 이번 debug/release JVM 원본 XML.

scratch는 임시 보관 영역이다. 최종 후보에서는 증거를 다시 생성해 장기 보존 가능한 CI artifact나 승인된 저장소로 옮겨야 한다.

## 8. 진행 상태 기록에서 주의할 점

1. `PLAN.md`의 Phase 3 pending은 현재 로컬 산출물 수준을 충분히 반영하지 못한다. 다만 파일 존재만으로 공개 프로젝트 정비 완료라고 바꾸면 안 된다. 원본 계획은 이번에 수정하지 않는다.
2. `docs/PHASE2-VERIFICATION.md:5`의 “기준 HEAD f909495, 미커밋”은 2026-11-07 실행 당시 기록이다. 현재 HEAD는 `734885a`이며 CI/보안/노출 가드는 해당 커밋에 있다. 역사적 기록을 현재 Git 상태로 그대로 인용하지 않는다.
3. README/아키텍처의 변경과 새 운영 문서·라이선스·이미지는 현재 미커밋 작업이다. `backend/diagnostic/public_documentation_contract_test.go`와 `frontend/documentation.browser.cjs`도 함께 검토해야 한다.
4. 과거 Web 298/620, Android 297+2 등은 당시 계층·시점의 수치다. 이번 실행의 Node 892, Android 각 764와 합산하지 않는다. Offline archive validators 63도 별도 fixture 계층이지 전체 테스트 총합이 아니다.
5. 문서의 Apache-2.0 결정 기록, 실제 LICENSE 파일 존재, 커밋/원격 반영, 공개 배포 승인을 구분한다. 이번 요청은 진행 보고서 작성에 한정된다.
6. `compose.local.yaml`, `frontend/dom.test.js.orig` 등 기존 로컬·백업 파일은 자동 정리하거나 커밋 대상으로 포함하지 않는다.

## 9. 권장 후속 순서와 완료 증거

| 우선순위 | 후속 작업 | 완료 확인 방법 |
|---|---|---|
| P0 | Docker builder·validator 계약 갱신 및 최종 binary 보안 검증 | exact pinned builder/fixture 정합성, 재현 빌드, 추출 binary scanner 통과 로그 |
| P0 | GitHub CI 반영과 required check 강제 | 실제 PR/main 결과, 의도적 실패 PR merge 차단, ruleset/protection readback |
| P1 | Phase 3 산출물 검토·확정 | README만으로 독립 실행, 문서/이미지/테스트의 승인된 커밋, 라이선스·신고 창구·metadata 확인 |
| P1 | Phase 4 수동 acceptance | 브라우저/화면 크기·screen reader·Android 단말/공유·TLS proxy별 pass/fail/미검증 표와 재현 증거 |
| P1 | clean exact-SHA 후보 gate | `make ci`, `make ci-clean-archive`, `make verify-release-real`, 보안 검사·독립 리뷰 결과 |
| P2 | RC 및 최초 정식 릴리스 | 승인된 태그/SHA/VERSION 일치, 릴리스 노트·체크섬·지원 플랫폼, 독립 설치·실행 증거 |

원래 릴리스 범위에서 제외한 신규 Checker 대규모 확장, multi-vantage, packet loss/jitter/throughput/MTU, 이력 저장·비교, AI 원인 추론, Kubernetes/SaaS 기능은 이번 잔여 작업에 다시 추가하지 않는다. 다음 작업의 초점은 새 기능보다 기존 구현의 검증·원격 강제·신뢰 가능한 배포다.

## 10. 보고서 최종 점검

- Phase 3/4의 별도 읽기 전용 대조 결과를 받았으며, 핵심 분류를 부모 점검에서 직접 확인한 문서·소스와 대조했다. 이는 진행 현황 분류 검토이지 최종 릴리스의 독립 코드 리뷰 승인이 아니다.
- 원본 계획의 작업 1-1~5-2를 빠짐없이 대응하고, source `path:line` 참조·테스트 집계·skip 목록·문서 whitespace를 검사한다.
- 기존 소스 파일의 SHA-256, HEAD/branch 및 기존 git status를 기준 snapshot과 대조해 이번 저장소 변경이 `PLAN_PROGRESS2.md` 추가뿐인지 확인한다.
- 최종 기계 검사 결과는 위 증거 디렉터리의 `final-checks.json`에 보존한다. 원격·운영·실기기 미검증 항목은 여전히 미검증으로 남긴다.

## 11. 후속 실행 — 2026-11-10

### 로컬 구현·검증 완료

- [x] P0 Docker builder와 offline validator·fixture·lifecycle fake를 CI와 같은 Go 1.26.10에 맞췄다. 공식 image index digest를 조회해 고정하고 계약 퇴행 검사를 추가했다. 기존 1.22.12 binary 수용과 버전 불일치를 먼저 실패시킨 뒤 수정했다.
- [x] 실제 Docker archive를 빌드해 offline validator로 추출했다. Go build-info 1.26.10 및 기존 Alpine/traceroute/runtime 정책이 검증됐고 추출 binary의 govulncheck도 종료 0이었다. 아직 working-tree snapshot 산출물이지 최종 exact-SHA release가 아니다.
- [x] 격리 Full `make ci`: 종료 0, 323.165초, CI_OK 15개. Go 최상위 522 pass/6 skip, 하위 포함 1,298 pass, Node 892 pass, Android debug/release 각 764 tests/실패·오류 0. CI policy는 11개, archive validator는 기존 63개 모두 통과했다.
- [x] 실제 `make ci-security`: 종료 0. 보안 회귀, Go source, dev dependency 포함 npm audit, 전체 Git 이력 secret scan을 모두 실행했다. 이 실행 시 Go/npm/secret findings는 0건이었다. 이후 커밋은 이력 검사를 다시 받아야 한다.
- [x] `make ci-workflows`와 문서 계약 테스트도 통과했다.
- [x] 기존 `make web-test-browser`: graph-first, route-visual, label-integrity, topology-empty, IP context와 실제 handler CORS/Retry-After 검사를 통과했다. 기본 CI의 browser skip과 구분되는 별도 opt-in 실행이다.
- [x] 실제 API binary를 소유한 격리 컨테이너에서 실행하고 `/readyz` 200을 확인한 뒤 documentation browser gate 24개 사례를 통과했다. 1440/320/375/400px, 2D/3D·Geo·라벨·공통 CIDR·실제 DNS/HTTPS 요청을 검사했다. topology/Geo는 명시적인 fixture replay다.
- [x] 화면 4개를 재생성하고 hash/fixture/script/browser provenance를 `docs/images/PROVENANCE.md`와 `provenance.json`에 보존했다. 격리 API 컨테이너·네트워크의 exact-ID 제거와 이름 부재도 확인했다.
- [x] 독립 리뷰는 차단 보안/논리 문제 없이 승인됐다. 비차단 제안인 capability 조회 도식도 실제 Web의 직접 POST와 Android 사전 조회를 구분하도록 보완했다. 이는 원격·실기기·최종 릴리스 승인이 아니다.

브라우저 첫 시도는 Playwright 1.64.1이 요구한 Chromium revision 1248 부재로 실패했다. 앱 결함으로 처리하지 않았고, 실제 설치된 Playwright 1.63.0과 Chromium revision 1243을 대조해 재실행했다. 실패 기록도 증거 디렉터리에 보존한다.

### 아직 필요한 작업

- [ ] 승인된 로컬 커밋의 clean exact SHA에서 `make ci-clean-archive`, production umask 3-pass, 추출 binary scan, 실제 Compose/readiness 검증. 결과는 후속 기록으로 추가한다.
- [ ] 원격 반영과 GitHub required check 강제. 2026-11-10 read-only 조회에서 main protected=false, branch protection 404, workflows/runs 0, Description=null/Topics=[], private vulnerability reporting=false를 확인했다. 권한은 있으나 이번에는 원격 쓰기가 승인되지 않았다.
- [ ] 비공개 신고 채널/담당자·지원 정책 확정, 실제 PR 실패 merge 차단과 cold-runner Full/Release.
- [ ] 실제 Firefox/Safari·물리 모바일·screen reader·Android 실단말/TalkBack/OEM 공유/프로세스 종료 수용 시험. 이 호스트의 adb 연결 단말은 0개이고 SDK emulator 실행 파일도 없었다. Chromium 자동화로 대체 완료 처리하지 않는다.
- [ ] 실제 TLS reverse proxy HTTP/2 deadline/cancellation, 새 VM/독립 호스트의 README-only 설치·실행, 사용자 이해도 수용 시험.
- [ ] 승인된 RC/정식 VERSION/tag 일치, 노트·체크섬·지원 플랫폼·binary/container 공개 배포. 로컬 산출물 검증과 발행은 별개다.

후속 원본 증거: `/home/piecer/.hermes/cache/scratch/checknetwork-progress2-execution-20261110-8enljsk9/`. `baseline.json`, `red-summary.json`, `green-summary.json`, `verification-summary.json`, `github-readonly.json`, 실제 archive/binary 검사 로그와 브라우저 JSON을 보존한다. 기존 `compose.local.yaml`과 `frontend/dom.test.js.orig`는 수정·커밋 대상에서 제외한다.
