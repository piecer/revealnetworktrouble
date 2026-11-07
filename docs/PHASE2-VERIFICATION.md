# Phase 2 구현·검증 기록 — 2026-11-07

## 범위

PLAN.md Phase 2의 워크플로 코드, 공통 Make 진입점, 실패 판정 및 보안 검사, 버전 고정, 증거 보존, Dependabot과 운영 절차를 구현했다. 기준 HEAD는 `f90949587d3a45580ec937b02d1acdecb5fe02f7`이며 모든 추가 사항은 아직 미커밋 작업 트리다. GitHub push, workflow_dispatch, required check 설정 변경, tag/release 발행, 운영 재배포는 하지 않았다.

동작·명령과 관리자가 해야 할 원격 활성화는 [CI.md](CI.md)를 참조한다. 원래 Phase 3~5의 실환경·라이선스·배포 요구를 이 작업으로 완료했다고 표시하지 않는다.

## 구현

- `.github/workflows/ci.yml`: PR Quick/Security, main·merge_group·schedule·수동 실행 Full, `always()` 기반 `CI required` 완료 판정.
- `.github/workflows/release.yml`: 정확한 SHA/clean/VERSION-tag 일치 → Security → exact borrowed base 준비·identity 검증 → 기존 archive/production verifier → 추출 binary 취약점 검사 → 검증된 후보 artifact. 자동 게시 없음.
- `.github/actions/toolchain/action.yml`: 수정 툴체인 Go 1.26.10, Node 22.23.1, JDK 17.0.21+9 및 Android 도구 버전 공유. 외부 Actions의 tag를 공식 GitHub API로 조회한 커밋 SHA에 고정.
- Buildx 0.31.1, BuildKit 0.27.0의 원격 manifest digest 고정. 실제 release 이미지 재현 빌드가 성공했다고 주장하는 항목은 아니다.
- `.github/dependabot.yml`: Actions/Go/npm/Gradle/Docker 주간 검사.
- Makefile: 기존 test/syntax/vet/build/ci/release 진입점 재사용. `ci-policy`, `ci-workflows`, `ci-security` 및 개별 scanner target 추가.
- `scripts/ci-status.py`, `ci-security.sh`, `ci-release-ref.py`: 누락·취소·skip·도구 오류·잘못된 revision을 fail-closed로 처리.
- `scripts/test_ci_*.py`: 위 정책과 실제 subprocess 경계 회귀. GitHub expression/schema는 actionlint로 별도 검증.
- `cmd/checknetwork-api/exposure_startup_test.go`: Phase 1 독립 리뷰의 WARN 호출 경로 제안을 실제 자식 프로세스 테스트로 보완.
- `frontend/package-lock.json`: 취약한 transitive 두 패키지만 갱신. jsdom 직접 의존성과 앱 코드는 변경하지 않았다.

## 실제 검출 및 수정

### npm

첫 `npm audit --audit-level=high`가 high 2개를 보고하고 실패했다. 원본 JSON은 `checknetwork-phase2-npm-audit-before.json`에 보존했다.

- source-map-js 1.2.1 → 1.2.2
- undici 8.10.1 → 8.12.2(1차) → 8.12.4(독립 리뷰 후 최종)

공식 npm registry의 해당 버전 tarball URL 및 integrity로 lockfile만 최소 수정했다. 1차 수정의 `npm ci` 및 audit는 통과했지만, 독립 리뷰가 GHSA-mqg3-3v4r-cm32를 지적했다. 공식 nodejs/undici advisory API를 직접 확인한 결과 2026-11-05T10:27:17Z 공개, high, 영향 버전 `>= 8.12.2, < 8.12.4`, 수정 8.12.4였다. 따라서 이전 audit 0건을 취약점 해소의 증명으로 사용하지 않는다. 원본 영수증은 보존하고 lock을 8.12.4로 최소 보완한 뒤 실제 npm ci·전체 테스트·audit를 재실행했다. 최소 수정 버전 미만을 거부하는 정책 회귀도 먼저 실패시킨 뒤 추가했다. dev dependency 제외나 advisory 무시는 없다.

### 독립 리뷰의 cold runner 지적

첫 Phase 2 리뷰(`deleg_11fc9b63`)는 위 undici와 release base 준비 누락의 두 지적으로 승인하지 않았다. 기존 verifier가 borrowed Alpine/nginx의 daemon cache를 요구하지만 BuildKit tar export만으로는 준비되지 않는다는 지적도 현재 코드에서 확인했다.

`scripts/prepare-release-bases.py` / `make release-bases`를 추가해 기존 verifier의 exact-digest 두 reference만 Linux/amd64로 pull하고 immutable ID·OS·architecture·RepoDigests를 검사한다. workflow는 archive/production verifier의 inventory 기준점 전에 이 target을 실행한다. 실패 전파·잘못된 ID/platform/digest/inspect 응답·실행 순서를 회귀 테스트로 검증했다. 파생 archive를 load하거나 borrowed base를 cleanup하는 동작은 추가하지 않았다.

실제 Docker로 `make release-bases`를 실행해 종료 0, 7.295초, 두 identity 일치를 확인했다. 준비 전후 및 최종 검사 시점의 기존 API/Web container ID·image·StartedAt·ports·running 상태도 같았다. 이 준비 단계의 통과를 hosted cold runner의 전체 릴리스 검증 성공으로 간주하지 않는다.

### Go

Go 1.26.8로 실행한 govulncheck 1.9.0은 호출 가능한 표준 라이브러리 취약점 10개를 보고하고 종료 3을 반환했다. 상위 make/보안 driver도 실패로 처리했으며 나머지 npm·secret gate 결과는 수집했다.

주요 수정 버전은 Go 1.26.9 및 crypto/x509의 1.26.10으로 보고됐다. 공식 Go 다운로드 목록에서 1.26.10을 확인하고 CI 고정을 변경했다. 로컬은 `GOTOOLCHAIN=go1.26.10`의 검증된 모듈 툴체인을 내려받아 사용했으며 시스템 `/usr/bin/go`는 바꾸지 않았다. 수정 툴체인의 source scan은 `No vulnerabilities found`로 종료 0이었다.

기존 Go 1.26.8로 빌드된 로컬 binary도 `govulncheck -mode=binary`에서 실제 종료 3/10건을 확인했다. 따라서 source scan만으로 다른 Docker compiler가 만든 release binary까지 안전하다고 판정하지 않도록 `release-vuln` 게이트를 추가했다. Dockerfile의 Go 1.22.12 고정 builder/validator 갱신과 재현 빌드는 별도 릴리스 차단 점검으로 남겨 두었다.

### 실패 신호·권한

- Quick/Full/Security 및 Full-required 여부의 상태 조합 648개 검사 통과.
- security-test/go-vuln/npm-audit/secret-scan 각각에 종료 42를 주입했다. 네 gate 모두 실행되고 하나라도 실패하면 성공 코드가 나오지 않았다.
- 잘못된 SHA, shell 형태 문자열, tag/VERSION 불일치, dirty tree, 다른 checkout HEAD는 release 입력 검사에서 거부한다. 실제 현재 dirty 작업 트리로 실행한 검사도 종료 1이었다.
- 실제 Gitleaks stdin에 일회성 합성 토큰을 제공했을 때 종료 1로 탐지했다. 로그에서 원문 토큰 부재를 확인했다.
- 실제 Git 전체 이력 검사는 32 commits를 읽고 탐지 0건이었다. 미커밋 추가 파일을 검사했다는 의미는 아니다.
- actionlint가 처음에는 job env의 runner context 사용을 거부했다. composite의 실행 단계에서 RUNNER_TEMP/GITHUB_ENV로 설정하게 고친 뒤 통과했다.

## 검증 상태

- `make ci-policy`: 최종 10개 unittest 통과(내부에 648개 조합, scanner 실패 주입, 보안 버전 하한 및 base 준비 검사 포함).
- `make ci-workflows`: actionlint 1.7.12 통과.
- WARN 프로세스 회귀 `-race -count=10`: 통과.
- 수정 Go 툴체인의 `make ci-security-tools` + 실제 `sh scripts/ci-security.sh`: 네 gate 모두 통과, 최종 종료 0. Go/npm/secret 검사 결과 각각 0건.
- 리뷰 지적 보완 후 최종 Full `make ci`: 종료 0, 308.912초, 15개 CI_OK(기존 14개 + ci-policy), CI_FAILED 0. Go 최상위 521 pass / 6 skip, 하위 포함 1,289 pass / 6 skip. Node 892 pass / 0 fail, Android debug/release 각각 764 tests / 0 fail / 0 error. Full 실행 전후의 소스 SHA-256이 일치했다.
- 보완 후 실제 `make ci-quick`: 저장소 밖 동일 소스 snapshot에서 종료 0, 118.113초. 기존 test/Web syntax/vet/build 및 CI policy를 실제 실행했다. Full과 node_modules를 공유하지 않았다. snapshot의 413개 source hash와 당시 작업 트리의 일치도 확인했다.
- 해당 Quick이 Go 1.26.10으로 만든 binary의 `make release-vuln`: 종료 0, 0.524초. 검증한 Docker release에서 추출한 binary라고 주장하지 않는다.
- 보완 후 Security 재실행: 네 gate 모두 CI_OK, 최종 종료 0. 실제 설치된 undici도 8.12.4임을 확인했다.
- Phase 2 독립 리뷰: 첫 리뷰 승인 불가 → 두 지적 직접 확인·보완 → `deleg_e0bbcb42` 재검토 승인(`passed=true`, 보안·논리 차단 및 제안 모두 빈 목록). 검토자는 공식 advisory/registry와 tarball integrity를 직접 검증했고, 정책 테스트 10개·actionlint 및 준비 단계의 실패 전파도 확인했다. 이 승인은 로컬 코드 검토 범위이며 실제 hosted cold-runner 릴리스 실행을 승인 완료한 결과가 아니다.

Full에서 skip한 6개 opt-in browser/real-Buildx/실제 GNU traceroute 검사는 Phase 1 기록과 같으며 통과 수에 넣지 않는다. 성공한 CI의 실패율 0%는 실행한 테스트에만 해당한다. 현 시점의 vulnerability DB를 사용한 0건 결과도 미래의 안전성을 보증하지 않는다.

소스 변경 전 시작했던 Go 1.26.8 Full 실행은 수정 툴체인을 적용하기 위해 중단했다. 중단 결과를 성공으로 계산하지 않는다.

## 원격 및 릴리스 미검증 경계

2026-11-07 `gh api repos/piecer/revealnetworktrouble/branches/main/protection`의 read-only 조회는 `Branch not protected (HTTP 404)`였다. 관리자가 workflow를 반영하고 실패 PR, main/merge_group, Full/Release 실행, `CI required` branch ruleset을 확인해야 Phase 2의 원격 완료 조건을 충족한다. 원격 설정을 몰래 변경하거나 local test 결과를 GitHub status check 성공으로 표기하지 않았다.

현재 작업 트리는 기존 사용자 수정/미추적 파일을 포함하므로 exact clean-SHA production release verifier를 실행할 수 없다. Release workflow의 end-to-end cold runner/Docker archive/umask 3-pass, 오래된 builder의 binary 취약점 게이트 통과, 실제 배포 readiness, 브라우저/실기기 acceptance도 아직 미검증이다. 후보 artifact/공식 릴리스는 생성하지 않았다. 로컬 수신 거부·scanner 음성/양성 제어 및 actionlint는 이 경계를 대체하지 않는다.

## 증거 디렉토리

접두 경로 `/home/piecer/.hermes/cache/scratch/`:

- `checknetwork-phase2-security-20261107/`: 최초 실제 scanner 실패(npm 및 Go)와 나머지 gate 결과.
- `checknetwork-phase2-security-fixed-20261107/`: npm 수정 후 0건, 아직 Go 1.26.8 취약점 차단 결과.
- `checknetwork-phase2-security-patched-20261107/`: 수정 Go 툴체인 4개 보안 gate 통과 및 도구 설치 로그.
- `checknetwork-phase2-controls-20261107/`: 합성 token redaction/탐지, dirty release 거부, 기존 binary 취약점 검출.
- `checknetwork-phase2-full-patched-20261107/`: 독립 리뷰 전 Full metadata/소스 SHA-256/각 gate 로그/집계.
- `checknetwork-phase2-quick-20261107/`: 독립 리뷰 전 격리 Quick source-manifest, 실제 Quick/binary scanner 로그와 종료·시간 summary.json.
- `checknetwork-phase2-review-fixes-20261107/`: 공식 advisory·registry 원문, 두 finding ledger 및 실제 release-bases 실행·운영 container 불변 영수증.
- `checknetwork-phase2-full-review-fixed-20261107/`: 두 지적 보완 후 최종 Full metadata/소스 SHA-256/각 gate 로그/집계.
- `checknetwork-phase2-quick-review-fixed-20261107/`: 최종 격리 Quick source-manifest, Quick/binary scanner 로그 및 summary.json.
- `checknetwork-phase2-security-review-fixed-20261107/`: 최종 Security 네 gate CI_OK 로그.
- `checknetwork-actions-pins.json`: 외부 Actions의 공식 tag → commit 조회값.

scratch 원본은 만료될 수 있다. 최종 merge/release 시 정확한 SHA에서 증거를 새로 생성해 보존해야 한다.
