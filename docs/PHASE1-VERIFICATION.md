# Phase 1 검증 기록 — 2026-11-06

## 범위와 상태

원본 PLAN.md의 작업 1-1(기준선)과 1-2(배포 안전성)를 진행했다. Phase 2~5, 라이선스 선택, commit/push/tag 및 새 바이너리의 운영 배포는 완료 범위가 아니다.

- 정확한 HEAD의 새 소스 아카이브에서 `make ci` 기준선 확보: 통과.
- 변경 작업 트리의 `make ci`, `make test`, `make build`, `git diff --check`: 통과.
- trusted-local 노출 가드와 Public 보안·프록시 경계 회귀 검증: 통과.
- 독립 코드 리뷰: 두 번째 리뷰(`deleg_aab64133`)가 `passed=true`, 보안·논리 차단 문제 없음으로 완료됐다. 최초 리뷰는 중단되어 승인으로 계산하지 않는다. 비차단 제안인 실제 시작 경로의 WARN 테스트는 2026-11-07 보완했다.
- 전체 릴리스 이미지 재현 빌드, 새 이미지로 전체 Compose 재배포, 실제 TLS reverse proxy, 실제 기기/브라우저 acceptance: 미실행. 공개 릴리스 승인 아님.

## 기준선과 환경

기준 커밋: `f90949587d3a45580ec937b02d1acdecb5fe02f7`.

기준선은 `git archive`로 별도 디렉토리에 추출했다. `.git`, 기존 node_modules 및 Android build 출력이 없는 새 소스를 사용했고, `GOFLAGS=-buildvcs=false`를 적용했다. 같은 호스트의 툴체인 및 의존성 캐시는 공유하므로 새 VM/독립 호스트 검증이라고 주장하지 않는다. 변경 검증은 같은 HEAD 위의 미커밋 작업 트리이며 exact-SHA 릴리스 산출물이 아니다. 각 CI의 `metadata.json`에 소스 파일별 SHA-256이 있고 실행 전후 일치 여부를 확인했다.

| 항목 | 사용 값 |
|---|---|
| 플랫폼 | Linux amd64 |
| Go | 1.26.8 — Dockerfile에 고정된 릴리스 Go 1.22.12와 다름 |
| Node / npm | v22.23.1 / 10.9.8 |
| JDK | Temurin 17.0.21+9 |
| JAVA_HOME | `/home/piecer/.local/java/temurin-17` |
| Android SDK | `/home/piecer/.local/share/checknetwork-android/sdk`, platform 35, build-tools 35.0.0 |
| Gradle wrapper | 8.11.1, 기존 wrapper 검증기 통과 |
| Docker Compose | v5.7.0 |

시스템 기본 JDK 21을 바꾸지 않았다. Android 검증 시 `JAVA_HOME`과 SDK 경로를 명시했다.

## 실행 결과

| 검사 | HEAD 아카이브 기준선 | 변경 작업 트리 |
|---|---:|---:|
| `make ci` 종료 코드 | 0 | 0 |
| CI_OK 게이트 | 14 | 14 |
| 전체 CI 경과 시간 | 316.284초 | 292.004초 |
| Go 최상위 테스트 통과 / skip | 512 / 6 | 520 / 6 |
| Go 하위 테스트 포함 pass event / skip | 1,280 / 6 | 1,288 / 6 |
| Node 테스트 통과 / 실패 / skip | 892 / 0 / 0 | 892 / 0 / 0 |
| Android debug JVM tests / 실패 / 오류 | 764 / 0 / 0 | 764 / 0 / 0 |
| Android release JVM tests / 실패 / 오류 | 764 / 0 / 0 | 764 / 0 / 0 |
| Android debug/release lint·assemble | 통과 | 통과 |

실행된 테스트 실패율은 양쪽 모두 0%다. Go 최상위와 하위 포함 event를 합산하지 않는다. Node 수치는 `node --test` 요약이며, 그 앞의 `app.test.js`, `common-prefix.test.js` 별도 계약 스크립트도 성공해야 npm test가 통과한다. 실행 시간은 이 호스트의 해당 1회 실행값이지 성능 보장이 아니다.

CI 게이트 목록: frontend-deps, go-test, web-test, web-syntax, go-race, go-vet, go-build, android-wrapper, android-env-bootstrap, android-env, android-test, android-results, android-lint, android-assemble.

추가 검사:

- `make test`: 종료 0, 117.881초.
- `make build`: 종료 0, 0.272초.
- `git diff --check`: 종료 0.
- Public/Policy/노출/Compose 관련 집중 Go 검사: 최상위 52개 통과.
- 변경된 Go 파일 `gofmt -l`: 출력 없음.

### 건너뛴 Go 검사

다음 6개는 통과로 계산하지 않는다. CI 로그의 skip event로 확인했다.

- `TestCORSRetryAfterBrowser`
- `TestLiveGNUInetutilsTracerouteLoopbackIsReadyAndSucceeds`
- `TestReleaseVerifierResourceOwnershipLifecycleRealDocker`
- `TestAPIOfflineArchiveRealBuildx`
- `TestWebOfflineArchiveRealBuildx`
- `TestLiveGNUInetutilsReadinessAndTracerouteShareWorkingCapability`

아래 임시 컨테이너 검사는 이 opt-in release/Buildx/browser gate를 대신하지 않는다.

## 구현 및 회귀 검증

### 직접 실행 노출 가드

`cmd/checknetwork-api/main.go`의 config 로딩에서 trusted-local + 비loopback bind를 기본 거부한다. IPv4/IPv6 loopback literal은 기존대로 허용한다. 전체 인터페이스·사설 IP·IPv6 ULA/link-local·hostname은 `CHECKNETWORK_EXPOSE_TRUSTED_LOCAL=yes`가 필요하다. 승인해도 인증이 추가되는 것은 아니며 시작 WARN을 기록한다.

`cmd/checknetwork-api/exposure_contract_test.go`에서 비loopback/loopback/mapped IPv4/hostname, exact 승인값, Public 필수 설정, WARN 기록, Compose resolved config, release smoke 실제 argv를 검사한다. `backend/api/proxy_boundary_test.go`는 전달 헤더와 CORS Origin이 인증 또는 직접 연결 IP별 quota를 우회하지 못함을 고정한다.

Public의 사설·metadata·IPv4/IPv6 차단, mixed DNS 응답 거부, 검증한 literal로 dial, DNS 재해석, redirect 재검증, custom transport 우회 차단은 기존 보안 테스트를 실제로 재실행했다. 정책을 완화하지 않았다.

### 추가 발견 및 RED → GREEN

1. 승인값을 `TrimSpace`한 뒤 비교해 ` yes`도 허용하는 문제를 테스트로 재현했다. 원문 `yes` exact 비교로 고쳤고 앞뒤 공백·대문자·유사 Unicode는 거부한다.
2. `scripts/release-resource-ownership.sh`의 smoke 컨테이너는 `:8080`에 bind하지만 승인 변수가 없었다. 기존 fake Docker가 기록한 실제 create argv를 `loadRuntimeConfig`에 넣는 회귀 검사에서 실패를 확인했다. smoke 실행 인자에 trusted-local mode와 승인 yes를 명시해 수정했다. 이미지의 기본 환경이나 실제 외부 공개 정책을 완화하지 않는다.

초기 가드 구현 자체는 테스트보다 먼저 작성됐다. 따라서 작업 전체를 엄격한 TDD로 진행했다고 주장하지 않는다. 위 두 보완사항에는 수정 전 실패 로그와 수정 후 통과 로그가 있다.

### Compose와 reverse proxy

기본 Compose의 컨테이너 bind `:8080`은 유지하고 승인을 명시했다. 호스트 API `127.0.0.1:8090`, Web `127.0.0.1:3000`도 resolved config 검사로 확인했다. 같은 Docker 네트워크의 다른 컨테이너까지 차단한다는 보장은 없다.

`docs/OPERATIONS.md`에는 실행 방식별 신뢰 경계, hostname 취급, 승인값, Public 자격증명 유지, backend `RemoteAddr` 기반 제한, 프록시 TLS/인증/사용자별 제한 책임을 기록했다. 실제 TLS reverse proxy 제품과의 통합 검증은 미실행이다.

## 실제 바이너리 검증 — 운영 배포 아님

현재 소스를 CGO_ENABLED=0으로 빌드한 바이너리를 기존 로컬 API 런타임 이미지에 read-only mount했다. 컨테이너마다 `--network none`, host port 미공개, UID/GID 65532, capability 제거를 적용했다. 각 컨테이너의 nonce label/immutable ID를 확인하고 종료 후 정확한 이름의 부재를 재검증했다.

| 시나리오 | 실제 결과 |
|---|---|
| trusted-local `:8080`, 승인 없음 | exit 1, 기동 거부 |
| trusted-local `:8080`, 승인값 ` yes` | exit 1, 기동 거부 |
| Public, 승인 yes만 있고 API key 없음 | exit 1, 기동 거부 |
| trusted-local `:8080`, 승인 yes | WARN 1회, `/livez` 및 `/readyz` 200 |
| 기본 설정 | `127.0.0.1:8080`, WARN 0회, `/livez` 및 `/readyz` 200 |
| Public 정상 설정 | 무인증 401, 인증 health 200, 내부 대상 6종 모두 422 |
| Public 분당 1회 제한 | 첫 요청 200, X-Forwarded-For 변경 후 요청 429 |

내부 대상 6종은 loopback IPv4, RFC1918 IPv4, metadata IPv4, loopback IPv6, metadata ULA, IPv4-mapped loopback이다. 네트워크 차단 컨테이너이므로 외부 대상으로 실제 진단 트래픽을 보내지 않았다. 이 검사는 빌드한 바이너리의 런타임 호환성만 증명하며 Dockerfile로 새 릴리스 이미지를 만든 검증은 아니다.

7개 시나리오 모두 통과했고 테스트 컨테이너 정리도 확인했다. 검증 전후 기존 운영 API/Web의 immutable ID·이미지 ID·포트·running/healthy 상태가 동일했다.

## 이전 실행의 오류 및 로컬 배포 상태

- 최초 CI 실패 원인은 compose.yaml 누락이 아니었다. 작업 시작 전에 있던 LAN 설정 `10.0.0.2`가 테스트 기대값 `127.0.0.1`과 달라 Web 892개 중 1개가 실패했다.
- 당시 `make ci | tee ...; echo $?`는 make 실패 대신 tee의 종료 0을 출력했다. 최종 기록은 Python `subprocess.run`의 실제 make 종료 코드를 보존했다.
- 앞선 변경분 검증은 JAVA_HOME 누락으로 JDK 21을 선택해 android-env에서 실패했다. JDK 17을 명시한 재실행은 성공했지만 종료 로그 경로에 오타가 있었으므로 위 기준선/변경분 검증에서 별도 결과 파일로 다시 확인했다.
- 초기 진행 중 기존 LAN 설정을 `compose.local.yaml`로 분리하고 운영 스택을 재시작했다. 처음에는 override ports가 합쳐져 Open WebUI가 사용하던 3000 포트와 충돌했고 Web이 잠시 중단됐다. 이후 독립 local Compose 파일로 기존 API `10.0.0.2:9090`, Web `10.0.0.2:3002`에 복구됐다. 이 재시작은 사전 확인 없이 수행한 작업상 오류다.
- 현재 로컬 실행 설정은 `docker compose -f compose.local.yaml ...` 기준이다. 기본 compose.yaml과 함께 merge하는 파일이 아니다. 최종 재검증에서는 운영 컨테이너를 재시작하거나 새 바이너리를 배포하지 않았다. 현재 운영의 trusted-local LAN 노출 위험은 그대로 남아 있으며 Public/TLS 전환은 별도 승인 대상이다.
- 기존 README.md 수정과 `frontend/dom.test.js.orig`는 보존했다. commit/push/tag를 만들지 않았다.

## 2026-11-07 재개 보완

`cmd/checknetwork-api/exposure_startup_test.go`는 별도 자식 프로세스에서 실제 `runMain()`을 실행하고 노출 WARN의 정확한 메시지·주소·단일 기록을 검증한다. `localhost:0`으로 로컬 임시 포트만 사용하며 테스트가 만든 자식만 종료·회수한다. `go test -race ./cmd/checknetwork-api -run '^TestExposureStartupProcess$' -count=10`이 통과했다. 기존 reviewer가 지적한 helper 단독 테스트의 호출 경로 누락을 보완하며, 운영 서버를 시작하거나 재시작하지 않는다.

이어지는 Phase 2에서 보안 게이트를 실제 실행하자 Go 1.26.8 표준 라이브러리와 npm lock 의존성의 취약점이 검출됐다. 위 Phase 1 테스트 통과는 취약점 없음의 보장이 아니며, 수정과 현재 검사 범위는 [CI 운영 문서](CI.md) 및 Phase 2 검증 기록으로 별도 관리한다.

## 원본 증거 위치 (Phase 1)

접두 경로: `/home/piecer/.hermes/cache/scratch/`

- `checknetwork-phase1-baseline-20261106/`: source.tar, 새 source/, metadata.json, summary.json, CI별 원본 로그.
- `checknetwork-phase1-candidate-20261106/`: metadata.json, summary.json, CI별 원본 로그.
- `checknetwork-phase1-extra-20261106/`: make test/build/diff-check 로그와 summary.json.
- `checknetwork-phase1-runtime-20261106/`: 실제 컨테이너 시나리오·정리·운영 불변성 summary.json 및 시작 로그.
- `checknetwork-phase1-red.log`, `checknetwork-phase1-green.log`, `checknetwork-phase1-security.log`.

scratch 원본은 임시 보존 영역으로 만료될 수 있다. 이 문서의 수치와 범위는 영속 기록이며, 릴리스 증거는 최종 커밋에서 다시 생성·보존해야 한다.
