# CI / 릴리스 후보 검증

## 실행 계층

워크플로는 `.github/workflows/ci.yml`과 `release.yml`이다. 모든 외부 Actions는 커밋 SHA로 고정하며 토큰 권한은 `contents: read`, checkout credential 보존은 비활성화한다. PR에서 secret이나 쓰기 권한을 쓰는 `pull_request_target`은 사용하지 않는다.

| 이벤트 | Quick | Full | Security | 필수 완료 판정 |
|---|---|---|---|---|
| 모든 PR | 실행 | 의도적으로 skip | 실행 | Quick·Security 성공 + Full의 의도된 skip만 허용 |
| main push | 실행 | 실행 | 실행 | 세 작업 전부 성공 |
| merge_group | 실행 | 실행 | 실행 | 세 작업 전부 성공 |
| 주간 schedule / 수동 CI 실행 | 실행 | 실행 | 실행 | 세 작업 전부 성공 |
| v* tag / 수동 release 후보 | release job에서 전체 아카이브·보안·릴리스 검증 | 포함 | 포함 | 후보 job의 모든 게이트 성공 |

`CI required` 작업은 `if: always()`로 선행 작업 실패 후에도 결과를 검사한다. `scripts/ci-status.py`는 누락·실패·취소·예기치 않은 skip을 실패로 반환한다. 워크플로 자체에는 paths 필터를 두지 않아 문서 PR에서도 필수 상태가 누락되지 않는다. gate 스크립트는 실제 종료 코드를 보존하고, Bash의 pipefail로 `tee`가 검사 실패를 숨기지 않게 한다.

## 로컬 실행

Git, Docker CLI/Compose, Python 3, Go 및 Node가 필요하다. 기존 Go 테스트 중 Compose config 계약은 daemon 변경 없이 Docker Compose CLI를 사용한다. Full에는 JDK 17과 Android platform 35/build-tools 35.0.0이 추가로 필요하다.

```sh
# GitHub와 같은 수정 Go 툴체인만 해당 명령에 적용한다. 시스템 Go를 변경하지 않는다.
export GOTOOLCHAIN=go1.26.11
export JAVA_HOME="$HOME/.local/java/temurin-17"
export ANDROID_HOME="$HOME/.local/share/checknetwork-android/sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
# 검사 로그와 scanner binary는 저장소 밖에 둔다.
export CI_EVIDENCE_DIR="$(mktemp -d "${TMPDIR:?}/checknetwork-ci.XXXXXX")"
export CI_TOOLS_DIR="${TMPDIR}/checknetwork-ci-tools"
make ci-quick ci-workflows
make ci
make ci-security
```

- `ci-quick`: 기존 `frontend-deps`, `test`, `web-test-syntax`, `vet`, `build` 재사용.
- `ci-policy`: stdlib Python unittest로 필수 작업 상태 조합, 보안 게이트 실패 주입, release 입력, 워크플로 계약, 검토된 npm 보안 수정의 최소 버전 및 release base 준비를 검증한다. `make test`와 `make ci`에도 포함된다.
- `ci-workflows`: actionlint 1.7.12로 GitHub 표현식·스키마·실행 스크립트를 검사한다.
- `ci-security`: 도구를 고정 버전으로 준비한 뒤 `security-test`, `go-vuln`, `npm-audit`, `secret-scan`을 모두 실행한다. 하나가 실패해도 나머지 결과를 수집하지만 최종 성공으로 처리하지 않는다.

## 버전과 보안 검사 범위

공유 composite action `.github/actions/toolchain/action.yml`이 Go 1.26.11, Node 22.23.1, Temurin JDK 17.0.21+9, Android CLI tools 12266719/platform 35/build-tools 35.0.0을 고정한다. SDK license 자동 수락은 CI용 설정이다. 호스트 기본 JDK/Go를 바꿀 필요가 없다. Ubuntu runner는 `ubuntu-24.04`이며 runner image 전체가 재현 가능한 digest로 고정된 환경은 아니다.

Security 도구:

- govulncheck 1.9.0: 현재 빌드 툴체인의 Go 코드 호출 경로 검사. 취약점 DB 접속/분석 오류도 실패다.
- npm audit: dev dependency를 포함하고 high 이상이면 차단한다. API 오류도 실패이며 `--omit=dev`로 테스트 의존성 취약점을 감추지 않는다.
- Gitleaks 8.30.1: `fetch-depth: 0`으로 받은 전체 Git 이력(`--all`)을 검사하고 출력은 100% redact한다. 로컬 미커밋 파일은 Git 이력 검사 범위가 아니므로 GitHub에 반영한 SHA에서 재검증이 필요하다. 임의의 `.env`나 credential 파일을 읽는 디렉토리 전체 스캔으로 바꾸지 않는다.
- `security-test`: 기존 Public 인증, quota, SSRF·redirect·DNS 재해석과 이번 노출 가드 회귀를 재사용한다.

Go source gate의 통과는 Dockerfile의 별도 builder 또는 컨테이너 OS 취약점 해소를 증명하지 않는다. Dockerfile builder와 offline archive validator는 CI와 같은 Go 1.26.11을 요구한다. builder는 공식 `golang:1.26.11-bookworm`의 index digest `sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0`에 고정하며, binary build-info·synthetic fixtures·lifecycle fake도 같은 버전을 검사한다. `ci-policy`는 이 계약의 퇴행을 거부한다. release에서 추출한 최종 `bin/checknetwork-api`에도 `make release-vuln`을 실행하고 통과 전에 후보를 업로드하지 않는다. Android/Gradle 또는 OS 전체 취약점 스캔은 이 Go/npm 검사 범위와 구분한다.

2026-11-11 source/추출 binary 재검사에서 Go 1.26.10의 GO-2026-6859가 검출됐다. 공식 수정 버전인 1.26.11로 위 계약을 갱신하고 1.26.10 binary를 명시적으로 거부한다. 과거의 0건 검사나 재현 빌드 통과만으로 이 취약점이 있는 후보를 승인하지 않는다.

Dependabot은 Actions, Go module, frontend npm, Android Gradle, 두 Dockerfile에 대해 주간 갱신 PR을 설정했다. 자동 merge를 설정하지 않았다. Makefile의 Go 실행 도구 버전과 composite의 툴체인 patch 버전은 정기 검토에서 수동으로 확인한다.

## 릴리스 후보 (자동 발행 아님)

`Release candidate`는 기존 verifier를 다음 순서로 사용한다.

1. 입력 SHA는 정확히 40자리 소문자 hex여야 하고 checkout HEAD와 일치하며 작업 트리는 clean이어야 한다.
2. tag push이면 tag 이름이 `v` + tracked VERSION과 일치해야 한다. 예: `v0.1.0-rc.1`은 VERSION도 `0.1.0-rc.1`이어야 한다. 태그는 이 워크플로가 생성하지 않는다.
3. Security 통과 후 `make release-bases`로 verifier가 요구하는 exact-digest Alpine/nginx Linux/amd64 base를 daemon에 준비하고 reference·immutable ID·OS·architecture를 검증한다. 이 단계는 모든 verifier의 inventory 기준점보다 먼저 실행한다. 이어 `make ci-clean-archive`로 canonical archive에서 Full CI와 reproducible Docker archive verifier를 실행한다.
4. `make verify-release-real REVISION=<exact SHA>`로 umask 3개에 대해 production verifier를 실행한다.
5. `make release`가 검증한 archive의 binary를 추출한다. `make release-vuln` 통과 후에만 binary, REVISION, VERSION 및 검증한 SHA256SUMS를 후보 artifact로 업로드한다.

Buildx 0.31.1과 BuildKit 0.27.0의 image digest를 고정했다. 위 verifier는 Docker base/cache 요구를 fail-closed로 처리하므로 cold runner의 pull, archive 생성, 실제 smoke를 통과해야 성공이다. Docker archive의 재현성을 검사하지만 archive 자체는 기존 verifier의 private 임시 산출물로 정리되며 artifact로 게시하지 않는다. 다운로드한 executable의 권한은 artifact 전송 시 달라질 수 있으므로 설치 시 실행 권한을 확인한다.

`release-bases`는 borrowed base만 pull하며 파생 archive를 daemon에 load하지 않는다. 별도 BuildKit 저장소에 image가 있거나 tar export가 성공했다는 이유로 daemon cache가 준비됐다고 판단하지 않는다. 준비한 borrowed base는 verifier cleanup에서 제거할 소유 자원이 아니다. 로컬에서 이 준비 단계의 실제 실행·identity 검증은 통과했지만 전체 hosted cold-runner release 성공을 뜻하지 않는다.

GitHub Release 작성, container registry push, 배포, 태그 생성은 없다. 후보 성공도 실기기·브라우저·reverse proxy 검증을 대신하지 않는다.

## 로그 보존

Quick/Security/Full은 성공·실패 모두 runner temp 아래 증거를 artifact로 보관한다(14일). Full에는 Android test XML/lint report도 포함한다. release 로그와 성공 후보는 30일 보관한다. 후보 artifact 업로드는 기본 success 조건을 사용하며, 앞선 gate나 로그 업로드가 실패하면 실행되지 않는다. Secret scanner는 redacted 로그만 보존하고 credential 자체를 산출물로 남기지 않는다.

## Merge 차단 활성화 — 관리자 작업

저장소 파일만으로 GitHub branch protection을 바꿀 수는 없다. 워크플로 반영 후 실제 PR의 `CI required` check가 생성되는지 확인하고, main ruleset/branch protection의 required status checks에 이 check를 지정해야 한다. strict/up-to-date 요구와 관리자 우회 여부도 저장소 정책에 맞춰 설정한다. merge queue를 사용하면 merge_group 이벤트 결과까지 확인한다.

2026-11-07 로컬 재개 시 read-only API 조회는 `Branch not protected (HTTP 404)`를 반환했다. 따라서 현재 merge 차단을 활성화했다고 주장하지 않는다. 워크플로 push, 의도적인 실패 PR, branch protection readback은 별도 승인 후 수행해야 한다.

## 취약점 대응 기준

- 실제 key/token 탐지: PR 차단. 먼저 폐기·교체하고 영향 범위를 확인한다. 로그/보고서에 원문을 붙이지 않는다. 이력 재작성은 별도 승인 절차다.
- 호출 가능한 Go 취약점 또는 npm high/critical: 차단. 해당 툴체인 patch/잠금 의존성을 최소 갱신하고 전체 테스트·audit를 재실행한다.
- 도구 설치·DB/API·권한 오류: 미검증이지 안전한 상태가 아니므로 차단한다.
- vendor advisory와 audit feed가 다르면 공식 affected range·수정 버전을 직접 확인한다. 이번 undici GHSA-mqg3-3v4r-cm32는 audit 0건 이후 독립 리뷰로 확인했으며 8.12.4로 보완했다. `ci-policy`의 최소 버전 회귀는 feed 반영 지연 중 해당 수정의 퇴행도 차단한다.
- 오탐 예외: advisory/정확한 위치, 도달 불가 근거, 검토자와 만료 조건을 기록한 좁은 변경만 검토한다. 이번 추가에서는 예외나 경고 수준 하향을 사용하지 않았다.
- advisory DB는 변하므로 과거 0건 결과를 릴리스의 현재 검증으로 대체하지 않는다.
