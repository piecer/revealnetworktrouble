# Phase 1 — 선택적 내장 UI 구현·검증

## 범위 — 2026-11-17

- 기준 HEAD: `03207ad7ae7b91a5290cf2380ba3831251c13773`, tree `65d0ba30cea3fdf52c81fcddeae99c663cc9a8c4`, branch `main`.
- 대상은 [ROADMAP](../../ROADMAP.md)의 U-1 중 선택적 내장 모드 추가와 S-1 내장 정적 응답 보안 정책이다. 기본 배포 전환까지 완료한 것으로 표시하지 않는다.
- 이름·모듈 경로·주 배포 방향의 기존 보류와 Android 제외를 유지했다. Android 코드·SDK·Wrapper·빌드/테스트 target을 변경·실행하지 않았다. 기본 `make test`는 Android bootstrap/Wrapper 정책도 실행하므로 비Android 계층을 명시적으로 실행했다. 전체 CI 통과 주장이 아니다.
- 이 작업의 명령에서는 운영 컨테이너/프록시/로컬 Compose를 변경하거나 재시작하지 않았다. commit/push·브랜치 보호·태그·릴리스 발행은 수행하지 않았다. 기존 `.orig` 백업과 ignored `compose.local.yaml`을 보존한다. 단, 최종 점검에서 API/Web 컨테이너 재생성이 관측되어 운영 보존의 전체 구간 동등성은 실패로 기록한다(아래 참조).

## 구현과 호환 경계

- `CHECKNETWORK_WEB_UI=on`일 때만 같은 바이너리/listener에서 Web UI를 제공한다. 기본 `off`, 기존 API-only 동작, standalone Web/API 두 이미지는 유지한다. `on/off` 이외 설정은 시작 오류로 거부한다.
- `frontend/embed.go`는 명시적인 production asset만 `go:embed`한다. `/`, `/index.html`과 정확한 asset 경로만 GET/HEAD로 제공한다. 디렉터리 목록·소스·테스트·임의 SPA 경로를 열거나 API 오류를 HTML로 변환하지 않는다.
- 내장 HTML의 API 필드는 읽기 전용이고 `frontend/app.js`가 브라우저의 실제 `location.origin`을 사용한다. 서버의 Host·forwarded header는 HTML에 삽입하지 않는다. standalone의 기존 API 주소·수동 설정은 그대로다.
- 정적 응답은 no-store, 정확한 MIME/Content-Length, HEAD 무본문, 불허 메서드 405와 Allow를 적용한다. CSP·nosniff·frame DENY·no-referrer를 설정한다. 인라인 script는 금지하며 기존 범례·동적 색/위치의 style 속성만 좁은 CSP 예외로 허용한다.
- CSP `connect-src 'self'` 때문에 내장 UI는 다른 origin의 API를 사용하지 않는다. 별도 API 설정은 standalone Web으로 유지한다. root 경로의 same-origin 프록시 구성은 가능하지만 prefix 배포나 기존 lab ingress의 자동 전환은 이번 범위가 아니다.
- public 모드에서도 UI 파일은 공개하되 API Bearer 인증·rate limit·기존 JSON 응답 경계는 그대로다. operational handler는 가장 바깥에 남는다. traceroute 부재의 readiness 503도 고치거나 숨기지 않았다.
- API Dockerfile/.dockerignore에 내장 Go adapter와 production asset만 빌드 입력으로 추가했다. standalone Web의 asset 목록·manifest·API_PORT 기본값과 릴리스의 두 이미지 구조는 바꾸지 않았다.

## 실제 RED → GREEN

원본 로그 디렉터리: `/home/piecer/.hermes/cache/scratch/rnt-phase1-swfa6oks`.

| 회귀 | 구현 전 관측 | 구현 후 관측 |
|---|---|---|
| 실제 시작 경로의 UI 옵션 | on에서 root 404, public에서 root 401, 잘못된 모드가 수용됨 | default/off API-only, on UI, public UI+API 인증 모두 통과 |
| 내장 브라우저 API 주소 | 빈 주소로 동일 출처 assertion 실패 | HTTP/HTTPS/IPv6 origin과 실제 app 요청 경로 통과, standalone 설정 보존 |
| 정적 보안 헤더 | CSP/frame/referrer 부재로 실패 | GET/HEAD/405 정책과 API pass-through 통과 |
| API Docker build 입력 | frontend COPY 목록이 비어 실패 | 명시적 production 입력과 Docker ignore 예외 통과 |

로그: `red-startup.log`/`green-startup.log`, `red-origin.log`/`green-origin.log`, `red-security.log`/`green-security.log`, `red-packaging.log`/`green-packaging.log`.

## 실행한 검증

툴체인은 Go `1.26.11`, Node `v22.23.1`, npm `10.9.8`이다. 부모/하위 테스트는 raw Go JSON과 Node TAP에서 구분해 집계했다.

| 실행 | 실제 결과 | 증거 |
|---|---|---|
| 변경 전 `go test -json -count=1 ./...` | exit 0; 부모 pass 522, 하위 pass 742, 부모 skip 8 | `baseline-go.log`, `baseline-receipts.json` |
| 변경 전 `make web-test` | exit 0; TAP pass 901, fail/skip 0 | `baseline-web.log` |
| 변경 후 `go test -json -count=1 ./...` | exit 0; 부모 pass 528, 하위 pass 773, 부모 skip 8, 실패 0 | `go.log`, `verify-receipts.json` |
| `make web-test` | exit 0; TAP pass 903, fail/skip 0; 별도 app/common-prefix 스크립트도 실행 | `web.log` |
| `make web-test-syntax vet build` | exit 0 | `static-build.log` |
| `make test-integration web-test-deployment` | exit 0; Compose 해석·실제 post-COPY recipe·request 경로·ownership 회귀 | `integration.log` |
| `make test-race` | exit 0 | `race.log` |
| 실제 바이너리 Chromium | exit 0; 1440/375 px 화면과 public 인증 검사 | `browser.log`, `checknetwork-embedded-RcPkmS/receipt.json` |
| 실제 Docker buildx API archive | exit 0; pinned builder와 실제 `.dockerignore`/COPY를 사용 | `docker-build.log`, `api-working-tree.tar` |
| 기존 strict offline API archive validator | exit 0; 원래 runtime/file/Go build-info 계약 통과 | `api-archive-validation.json`, `extracted-api/` |
| Docker archive 추출 바이너리 Chromium | exit 0; 별도 Web 파일 없는 작업 디렉터리에서 같은 검사 통과 | `archive-browser.log`, 해당 고유 browser receipt |
| 문서 갱신 후 Go 문서/운영/시작 경로 focused 검사 | exit 0 | `documentation-final.log` |
| 기존 `make web-test-browser` | exit 0; standalone graph/route/labels/empty/IP-context 및 실제 Go-handler CORS gate | `standalone-browser.log`, `checknetwork-browser-sPJCrF/` |
| 선택한 비Android CI 정책 | exit 0; 기존 6개 모듈 18 tests, skip 0 | `nonandroid-policy-retry.log` |

Chromium은 기존 외부 Playwright 설치와 일치하는 revision으로 실행했으며 실제 `browser.version()`은 `149.0.7827.55`였다. 두 바이너리별로 13개 화면/인증 사례를 각각 실행했다. 통합 진단은 실제 Go API와 소유한 로컬 TCP endpoint를 사용했다. topology 2D/3D·Geo는 producer fixture replay이고 외부 경로 측정으로 집계하지 않는다. 정상 작업의 CSP 위반·스크립트 오류·외부 origin 요청 없음과 고의 inline script/cross-origin fetch 차단을 확인했다. 소유한 API 두 프로세스는 각 실행에서 정상 exit 0으로 종료했다.

### 검증 실행상의 재시도

- offline validator 첫 실행은 extraction target 디렉터리를 미리 만들지 않아 exit 1이었다. 도구 계약에 맞게 private 디렉터리를 만든 뒤 원래 validator를 다시 실행해 exit 0이었다. validator 정책을 완화하지 않았다.
- 비Android 정책의 첫 실행은 `scripts.test_ci_security_floors`가 sibling `test_ci_definition`을 import할 경로가 없어 loader error였다. 원래 `scripts` 경로를 Python path로 지정해 선택한 기존 비Android 6개 모듈을 재실행했다. Android 정책 파일은 로드하지 않았다. 재실행은 18 tests, skip 0, exit 0이었으며 `nonandroid-policy-retry.log`에 보존했다.

## 독립 리뷰·최종 확인

- 독립 리뷰 `deleg_249e0601`은 2026-11-17 21:25 KST에 완료됐다. 실제 reviewer transcript의 판정은 `passed=true`, `security_concerns=[]`, `logic_errors=[]`였다. tracked diff와 새 Go adapter/tests·시작 경로 tests·browser gate 전체 bytes가 대상이다. `review-inputs.json`의 모든 파일 hash와 기준 HEAD가 재개 시점에도 일치함을 직접 확인했다.
- 리뷰의 비차단 권고에는 browser.close 실패 시에도 API 종료/receipt 기록을 보장하는 cleanup 강화와 내장 시작 경로의 readiness·rate-limit 통합 검사 확대가 있었다. 이번 변경에서는 별도 API/operational 회귀와 정상 종료 검증을 유지하고 해당 권고는 후속 개선으로 남겼다. 차단 finding을 무시하거나 구현했다고 표시하지 않는다.
- 원본 리뷰 manifest/transcript는 `/home/piecer/.hermes/cache/delegation/live/deleg_249e0601/`에 있다. 재개 시 회수한 transcript에는 전체 제안 목록이 잘려 있으므로, 이를 온전한 JSON review receipt로 재구성하지 않았다. 위 판정 필드와 확인 가능한 권고만 기록한다.
- 비Android CI 정책은 18 tests, skip 0으로 통과했다. 별도 standalone Chromium graph-first·route-visual·label-integrity·topology-empty·IP-context 및 실제 CORS gate도 모두 exit 0이었다.
- 문서 로컬 링크·UTF-8/whitespace 검사와 파일 보존 readback은 통과했다. 기존 `.orig`·`compose.local.yaml`·`go.mod` hash는 동일하다. 초기 중간 readback(`containers-after.txt`)에서는 RNT API/Web·ingress 컨테이너 ID/StartedAt도 같았다.
- 재개 후 최종 `finish.py` 실행은 Go/Web·구문/vet/build·whitespace 네 명령이 모두 exit 0인 뒤 운영 컨테이너 동등성 assertion에서 exit 1이었다. 초기 API/Web ID와 달리 API는 2026-11-17 21:34:14 KST에 시작한 새 컨테이너였고 Web은 새 ID의 `created` 상태였다. ingress ID/시작 시각은 유지됐다. 이 실패를 테스트 통과나 운영 보존 성공으로 바꾸지 않는다.
- 2026-11-17 21:36~21:37 KST 서버 측 TLS/명시 DNS 매핑 HTTP 확인에서 `rnt.lab.internal/`은 504, `rnt-api.lab.internal/readyz`는 200이었다. 사용자 10.0.0.1의 실제 DNS/CA 상태를 검증한 것은 아니다. 제한된 Docker event 조회에는 관련 event가 없어 변경 주체는 확인하지 못했다. 이번 작업의 도구 명령에는 운영 Compose up/recreate/restart가 없으며, 새 스택을 자동 복구하거나 중단하지 않았다. 로컬 기능 검증과 이 운영 불일치를 별도 상태로 남긴다.
- Scratch 로그/아카이브는 임시 증거이며 영구 release provenance가 아니다. 최종 검증 명령/상태와 선택 입력 hash는 같은 증거 디렉터리의 `completion.json`에 보관한다.

## 릴리스·운영 경계와 남은 항목

실제 Docker build는 수정된 working tree의 개발용 `0.1.0-dev` 아카이브이며, build argument의 revision은 기준 HEAD를 사용했다. 그 라벨이 현재 수정 bytes의 exact-SHA provenance를 증명하지 않는다. 아카이브를 daemon에 load하거나 registry에 push하지 않았고 두 번의 exact-SHA 재현 build·전체 release workflow도 실행하지 않았다.

- U-1 남음: exact-SHA 이중 이미지 릴리스 경로 검증 후 기본값 전환. 선택적 모드가 추가됐다는 사실과 기본값 전환은 구별한다.
- U-2/U-3/R-2 남음: 결과 재구성·ko/en 사전, 증상 프리셋, 공개 릴리스.
- R-1/Android는 기존 보류·제외대로다. 이 기록은 Phase 0 또는 Phase 1 전체 완료 선언이 아니다.
- 기존 `rnt.lab.internal` 운영 화면에 이 작업이 직접 배포한 것은 없다. 이후 감지된 운영 API/Web 재생성 및 Web 504의 복구는 별도 승인/조사 대상이며, 어느 source bytes가 배포됐는지 이 검증으로 단정하지 않는다. standalone Web이 복구된 경우 API 주소에는 올바른 HTTPS API 도메인을 사용한다.
