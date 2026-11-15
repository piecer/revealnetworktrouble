# 새 로드맵 Phase 0 구현·검증 — 2026-11-15

## 범위와 결론

[현재 로드맵](../../ROADMAP.md)의 Phase 0 중 로컬 기반 작업을 구현했다. 이전 [실행계획 v1.1](IMPROVEMENT-PLAN-v1.1.md)의 Phase 1/2와는 다른 계획이다. Phase 0 전체 또는 공개 릴리스 완료를 뜻하지 않는다.

- 기준 HEAD: `36f4c7028665d18083fa09742dd25b8793853e77`, 브랜치 `main`, dirty working tree.
- 실행 도구: Go 1.26.8, Node 22.23.1, npm 10.9.8. CI가 고정한 Go 1.26.11 검증이나 출시 산출물 검증으로 확대하지 않는다.
- 검증 입력: 현재 tracked 파일과 새 소스·테스트·보관 문서를 복사한 후보. 사용자의 `compose.yaml` 대신 후보에서만 `HEAD:compose.yaml`의 기본 배포 설정을 사용했다. 후보는 새 머신이나 clean exact-SHA archive가 아니며 호스트의 도구·캐시를 공유한다.
- 사용자 Compose 설정, `compose.local.yaml`, `.orig` 백업은 변경하지 않았다. 기존 `PLAN_PROGRESS2.md` 삭제도 유지했다. commit/push/tag/원격 설정/운영 재배포는 하지 않았다.

## 로드맵 항목별 상태

| 항목 | 구현·관측 상태 | 남은 경계 |
|---|---|---|
| M-1 | Linux 좀비 판정, Docker 검사 분리, archive fixture의 최소 Go 버전 및 오프라인 compiler 선택, 문서 안내 구현·검증 | 의존성 최초 설치와 새 머신 전체 provisioning까지 오프라인이라는 의미는 아님 |
| R-1 | 변경 보류 | 제품명·모듈 경로 결정에 응답이 없어 기존 이름과 모듈 경로 유지 |
| DOC-1 + M-2 일부 | 현재 계획을 ROADMAP으로 통합, 과거 문서 보관, 루트 Markdown 6개, 문서 계약 축소·링크·오류 표 검사 통과 | 현재 문서 약 200KB 이하 목표와 Phase 3의 나머지 검증 정책 재조정은 미완료 |
| R-3 | 로컬에 반영된 의존성 갱신의 CI/DOM/이미지 pin 불일치를 확인·보완 | 원격 PR triage·병합은 수행하지 않음. 새 nginx base의 독립 trust metadata·실제 archive 검증은 별도 작업 |
| S-3·R-5 | 수행하지 않음 | 보안 신고 채널·저장소 metadata·브랜치 보호의 원격 확인·설정 필요 |
| M-5 일부 | Android versionName을 root VERSION에 연결, debug/release 패키징 manifest의 0.1.0 확인 | JVM·lint·APK 전체 gate와 기기 검증은 미완료 |

따라서 Phase 0의 전체 완료 기준은 아직 충족하지 않았다. Phase 1~4의 UI 내장·번역·프리셋·진단 확장·릴리스는 이번 구현 범위가 아니다.

## 구현과 실패 선행 검증

### M-1: 환경 독립적인 기본 검사

- `backend/diagnostic/traceroute_process_unix_test.go`는 Linux `/proc/<pid>/stat`의 Z/X를 종료로 취급한다. 다른 플랫폼·procfs 읽기 실패·알 수 없는 상태에서는 기존 kill(pid, 0) 판정을 보수적으로 유지한다.
- `traceroute_process_linux_test.go`는 자신의 자식을 종료시킨 뒤 Wait 전 좀비 상태를 관측한다. PID 1의 수거 동작에 의존하지 않으며 수정 전 실제로 실패했다. 수정 후 기존 descendant 정리 회귀와 함께 반복 통과했다.
- Compose Go 검사 두 건은 기본 suite에서 opt-in skip한다. `make test-integration`은 명시적으로 실행하며 Docker 부재를 실패로 처리한다. Full의 `ci-inner.sh`에서도 이 target을 호출한다. 두 검사 모두 구성만 resolve하며 기존 stack이나 daemon resource를 시작·교체하지 않는다.
- 별도 subprocess 회귀로 Docker 없는 기본 skip과 명시적 실행 실패를 확인했다.
- clean-archive adversarial fixture의 Go 요구를 module 최소 버전 1.22와 맞췄다. 해당 회귀 subprocess는 `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`로 실행한다. 실제 fixture build와 GOFLAGS 보존 사례가 통과했다.

### DOC-1/M-2: 보관과 실제 계약

- root `SPEC.md`, 이전 `docs/PLAN.md`, 구현 이력, 이전 Phase 1/2 검증, 2026-09-01 검토 및 README 기술 기록을 `docs/archive/`로 옮겼다. 안내와 상대 링크를 조정하고 본문을 보존했다.
- 현재 사용자가 작성한 PLAN은 `ROADMAP.md`로 이동했다. Git HEAD의 별도 이전 계획은 `IMPROVEMENT-PLAN-v1.1.md`로 보존해 서로 다른 Phase 번호를 혼동하지 않도록 했다.
- 문서 계약은 858줄에서 164줄로 축소했다. 산문·Stage·변동하는 테스트 수·제품 소스 문자열을 고정하지 않는다. 기존 제품 producer/consumer·보안·자원 경계 테스트는 유지했다.
- 현재/보관 문서 존재와 inline 상대 문서·이미지 링크를 검사한다. fragment anchor와 reference-style 링크는 검사하지 않는다.
- API 오류 표는 producer fixture의 registry key/status/code/retryable/retry_after/message를 행 단위로 대조한다. `same` code 표기와 서로 다른 key의 `unmatched` wire code도 구분한다.
- 후보 문서에 HTTP status, wire code, Retry-After, archive 링크를 각각 잘못 넣는 네 가지 negative control이 모두 실제 테스트 실패를 일으켰고, 복원 후 통과했다.

### 현재 의존성 트리의 추가 회귀

- jsdom 31 lockfile에는 source-map-js가 없었다. 기존 CI 보안 하한 검사는 고정 root key를 읽다가 KeyError를 냈다. 제거된 transitive dependency는 허용하되 설치된 모든 중첩 복사본을 검사하도록 수정했다. source-map-js 1.2.2와 undici 8.12.4의 기존 하한은 유지했다. 제거·안전/취약 중첩 복사본·취약 undici·해석 불가 버전 회귀를 실행했다.
- DOM 포커스 실패 두 건은 숨겨진 topology 화면 또는 아직 게시하지 않은 report panel의 버튼에 focus를 요구하던 테스트 준비 단계 문제였다. 실제 해당 view와 게시된 compact report를 준비하도록 고쳤고 기존 focus assertion은 유지했다. production Web 동작은 바꾸지 않았다.
- nginx 갱신 커밋 `0f8a751`은 Dockerfile FROM만 변경했지만 borrowed-base identity와 offline archive validator는 이전 pin을 요구했다. Dockerfile을 기존 독립 검증 계약의 exact pin으로 되맞췄다. 새 base를 느슨하게 허용하거나 테스트 기대값만 갱신하지 않았다. 이것은 이미지 취약점 부재나 실제 release archive 통과의 증거가 아니다.

## 실행 결과

다음 자동 검증은 별도 후보 디렉터리에서 수행했다.

| 명령·관측 | 실제 결과 |
|---|---|
| 사전 `npm ci --offline` | 실패: whatwg-url cache miss. 일반 `npm ci --no-audit --no-fund`로 의존성 준비 성공 |
| Docker를 제외한 PATH + local Go/GOPROXY off에서 `make test` | exit 0. Android 환경 탐색 회귀, CI 정책 18개, Go 세 package, Web suite 통과 |
| Web Node TAP | tests 901, pass 900, fail 0, skipped 1. 별도 app/common-prefix contract script도 통과 |
| `make test-integration` | exit 0, Go Compose config 검사 실행 |
| `make web-test-deployment` | exit 0, JS 7개 및 ownership Python 4개 통과. 기본 Web suite에서 skip한 Compose 사례도 별도로 실행 |
| `make web-test-syntax vet build` | exit 0. 후보 `bin/checknetwork-api` 생성 |
| `go test -count=1 -race ./...` | exit 0, Go 세 package 모두 uncached 통과 |
| 문서 negative control 네 가지 | 각각 의도한 실패 exit 1, 복원 후 문서 계약 exit 0 |
| 독립 리뷰 | scoped 42개 경로의 해시 대조 및 검토 통과. security_concerns/logic_errors 없음. 전체 Android/실제 release acceptance 승인은 아님 |

`make test`의 네트워크 syscall trace에서 확인한 IP 목적지는 loopback뿐이었다. 별도 network namespace 생성은 호스트 권한으로 거부됐으므로 완전한 network sandbox 실행이라고 주장하지 않는다. 최초 npm 설치 자체에는 네트워크를 사용했다.

### 실제 바이너리 스모크와 readiness 한계

생성된 dev 바이너리를 자체 ephemeral loopback 주소로 실행했다. 외부 서비스나 기존 배포를 사용하지 않았다.

- `/livez`, `/api/v1/health`, `/api/v1/checks`: 200.
- 자체 HTTP listener, localhost DNS, 열린/닫힌 TCP에 대한 report: 200, total 4/passed 3/failed 1, verdict attention. 닫힌 포트 실패는 의도한 관측이다.
- SIGTERM 정상 종료: exit 0. 소유한 프로세스와 listener를 정리했다.
- `/readyz`: 503, reason traceroute_unavailable. 호스트에 traceroute가 없어 startup capability가 없었다. 이를 readiness 통과나 traceroute 종단 실행 성공으로 표시하지 않는다.

## 남은 실패·미검증

1. 실제 작업 트리의 `go test ./... -count=1`은 기존 `compose.yaml` 로컬 설정 때문에 `TestComposeRuntimeContract`에서 계속 실패한다. 네 개의 기본 환경 변수 override 계약이 없다. 사용자 설정을 되돌리거나 테스트를 약화하지 않았으며, 후보의 통과를 실제 작업 트리 통과로 바꾸어 표현하지 않는다.
2. Android의 실제 JVM 실행 시도는 androidx.core 1.19.1이 compileSdk 37 이상을 요구하는데 현재 설정은 35여서 실패했다. canonical 환경 검사는 호스트 JDK 21도 요구 버전 17과 다르다고 거부했다. 별도 Gradle 호출에서 debug/release 패키징 manifest의 versionName=0.1.0은 확인했지만 새 Robolectric 버전 검사를 포함한 JVM·lint·assemble 전체 통과는 아니다. Gradle 실행 중 Build Tools 36.0.0이 사용자 SDK cache에 추가됐으며 프로젝트의 compileSdk 설정을 바꾸지는 않았다.
3. 브라우저·기기 acceptance, Docker 이미지 build·실제 archive verifier, full `make ci`, 보안 scanner 전체, clean exact-SHA·원격 required checks·공개 릴리스는 수행하지 않았다.
4. 이름·배포 방향 결정과 모듈 경로 정렬, 현재 문서 총량 목표, GitHub 운영 항목은 남아 있다.

## 원본 증거 위치

이번 실행의 scratch 디렉터리:

`/home/piecer/.hermes/cache/scratch/checknetwork-phase0-3fxoizo1`

- `candidate-inputs-v4.json`: 최종 코드 검증 당시 source와 후보의 path/hash 목록. Compose 한 파일만 의도적으로 다르다. 이후 진행 상황·보관 index·이 검증 기록을 갱신했으므로 최종 문서까지 포함한 working-tree fingerprint는 아니다.
- `review-scope-v2.json`: 독립 리뷰한 42개 경로. 이후 변경은 이 기록과 ROADMAP 진행 상황 등 최종 문서뿐이다.
- `gate-receipts.json`, `gate-receipts-v2.json`, `gate-receipts-v3.json`: 초기 실패와 최종 child exit code/log 경로를 보존했다.
- `make-test-no-docker-v3.log`, `make-test-network-v3.trace`, `final-test-summary.json`.
- `final-race.json`, `final-race.log`, `web-deployment.json`, `web-deployment.log`.
- `document-canaries.json`, `canary-*.log`.
- `worktree-go-test.json`, `worktree-go-test.log`: 사용자 로컬 Compose와의 기존 충돌.
- `runtime-smoke.json`: readiness 503에서 중단한 최초 스모크. `runtime-smoke-diagnostics.json`은 이 한계를 유지하고 실제 report까지 확인한 후속 관측이다.
- `protected-files.json`, `relocations.json`, `original-documents/`: 사용자 파일 hash와 이동 전 문서 보존본.

Scratch는 정리될 수 있는 로컬 실행 증거이며 영구 release provenance가 아니다. 이 문서도 실행 당시의 기록이다.
