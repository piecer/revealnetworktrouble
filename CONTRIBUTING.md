# 기여 가이드

현재 source contract와 신뢰 경계를 먼저 확인한다: [README](README.md), [아키텍처](docs/ARCHITECTURE.md), [API](docs/API.md), [테스트](docs/TESTING.md), [실행 계획](PLAN.md). 과거 Stage 수치를 현재 테스트 합계나 릴리스 승인으로 해석하지 않는다.

## 환경

Go module 최소 버전은 1.22이고 CI는 수정 툴체인 Go 1.26.10을 사용한다. Node 22+ 및 npm lockfile, Python 3와 Docker Compose CLI가 필요하다. Full은 JDK 17, Android platform 35/build-tools 35.0.0을 추가로 요구한다. 실제 Docker/브라우저/기기 검증은 [CI](docs/CI.md)와 [테스트](docs/TESTING.md)의 별도 조건을 따른다.

## 변경 절차

1. 이슈와 acceptance를 좁히고 현재 동작을 재현한다. 기존 사용자 수정·로컬 설정·백업 파일을 되돌리거나 함께 stage하지 않는다.
2. 버그를 잡는 실패 테스트를 먼저 실행한 뒤 최소 구현으로 통과시킨다. 외부 인터넷·특정 DNS·관리자 권한에 의존하지 않는 fixture를 기본으로 한다.
3. 새 Checker/field/asset은 validation, producer fixture, Web/Android strict consumer 및 패키징/체크섬/문서를 함께 검토한다. raw error prose를 신뢰하거나 한쪽 client만 느슨하게 바꾸지 않는다.
4. 관련 gate를 실행하고 명령·실제 종료 코드·환경·skip을 기록한다. `tee`의 성공을 upstream 명령의 성공으로 간주하지 않는다.
5. diff와 보안 검토 뒤 관련 파일만 commit한다. commit/push/tag/운영 배포는 각각 별도 작업이다.

```sh
# 빠른 검증: 기존 Make 진입점을 사용한다.
export GOTOOLCHAIN=go1.26.10
make ci-quick
make ci-policy
make ci-workflows
# 의존성·Git 이력 secret·보안 회귀. 실제 credential은 fixture로 쓰지 않는다.
make ci-security
# JDK/SDK를 준비한 뒤 Android를 포함한 전체 gate를 실행한다.
make ci
git diff --check
```

기본 `make test`는 Go/Web와 CI 정책을 실행한다. `make test-race`, `make vet`, `make build`와 Android gate를 생략한 경우 명시한다. 이 저장소에는 `make lint` target이 없으며 Go vet, Web syntax, actionlint, Android lint를 각 명령으로 수행한다. exact-SHA 릴리스 검증은 clean commit에서만 실행하며 관련 명령은 [CI 절차](docs/CI.md)를 따른다.

## 코드와 개인정보

공개 JSON은 snake_case, 시간은 UTC RFC3339, 기간은 ms를 사용한다. cancellation과 bounded resource ownership을 보존한다. API key/Bearer, 쿠키, 실제 report와 내부 주소를 이슈·테스트·로그·스크린샷에 넣지 않는다. 예시는 공개 도메인·고정 fixture 또는 명시적으로 익명화한 데이터를 사용한다. synthetic fixture를 실제 진단 결과처럼 설명하지 않는다.

취약점은 [SECURITY.md](SECURITY.md)의 비공개 처리 절차를 따른다. 프로젝트 라이선스는 [Apache-2.0](LICENSE)이다. 기여 시 외부 코드·데이터의 출처와 기존 라이선스/고지를 보존하고, 실제로 제출할 권한이 있는 변경만 포함한다. 사용자 작성 내용을 정비할 때는 실제 코드와 대조하고, 미구현 초안은 버리지 말고 현재 지원과 구분해 이력에 보존한다.

## PR/리뷰 체크리스트

- 목적·현재 동작·수정 후 동작과 회귀 테스트가 연결되어 있는가?
- auth/SSRF, owner/cancel, parsing/response/resource bounds가 유지되는가?
- 실패·취소·skip·도구 오류가 성공으로 처리되지 않는가?
- 실제 실행한 gate와 미실행 플랫폼, evidence 경로를 구분했는가?
- generated output·local Compose·credential·무관한 변경을 제외했는가?
- 문서와 코드·fixture·packaging 목록이 일치하는가?

GitHub required checks는 workflow 파일만 추가했다고 활성화되지 않는다. 관리자 설정과 실제 실패 PR의 merge 차단을 원격에서 확인해야 한다.
