# Changelog

이 파일은 구현 변경과 릴리스 발행을 구분한다. `VERSION=0.1.0`은 source version이며 아직 공개 배포 일자나 태그 발행을 기록하지 않는다.

## [Unreleased]

### 첫 사용 경험 — 선택적 내장 UI

- `CHECKNETWORK_WEB_UI=on`으로 같은 바이너리·listener에서 정적 Web UI와 API를 제공한다. 기존 API-only 기본값과 standalone Web/API 두 이미지는 유지한다.
- 내장 UI는 API 주소 입력 없이 현재 페이지 origin으로 연결하며 CSP·nosniff·frame/referrer 정책과 명시적인 production asset 경계를 적용한다. API 인증·readiness 계약은 유지한다.
- 실제 시작 경로와 분리 배포 보존 회귀, 선택적 실제 바이너리 Chromium gate를 추가한다. 공개 릴리스 발행·Android·운영 배포는 이번 추가에 포함하지 않는다.

### 배포 — 단일 포트 serving 기본값

- Compose 기본 구성을 service 하나로 전환한다: 같은 listener에서 Web workspace와 API를 같은 origin으로 제공하고 host loopback publish를 하나의 포트(기본 `127.0.0.1:8090`)로만 노출한다.
- 화면과 API를 분리하는 두 포트 구성(Web `3000`, API `8090`)은 `standalone-web` profile로 선택할 때만 시작된다.
- Compose 노출 경계와 build identity 계약 테스트를 단일 포트 기본값과 profile 경로 양쪽에 대해 회귀 검사로 갱신했다. binary 기본값 `off`와 릴리스 검증 경계는 유지한다.

### Go 표준 라이브러리 보안 갱신 — 2026-11-11

- 실제 source/추출 binary 검사에서 GO-2026-6859가 검출된 Go 1.26.10 후보를 제외하고 CI·Docker builder·offline validator·fixture를 Go 1.26.11로 함께 갱신한다.
- 공식 builder index digest를 고정하고 취약한 1.26.10 산출물 거부 및 README 실행 툴체인의 회귀 검사를 추가한다. 과거 검증 기록은 당시 버전과 수치를 보존한다.
- 변경된 정확한 후보 SHA에서 보안·재현 빌드·전체 CI를 다시 통과해야 하며, 버전 갱신 자체를 공개 릴리스 승인으로 간주하지 않는다.

### 보안·자동화 — 734885a

- trusted-local의 non-loopback bind에 exact `CHECKNETWORK_EXPOSE_TRUSTED_LOCAL=yes` 승인과 startup WARN을 요구한다. 기본 Compose의 host loopback 공개를 유지한다.
- 실제 startup WARN, proxy forwarded-header 인증/rate-limit 경계 및 release smoke 실행 인자의 회귀 검증을 추가했다.
- PR Quick/Security, main/merge queue/정기 Full, fail-closed aggregate와 검증 전용 exact-SHA release candidate workflow, 고정 toolchain 및 Dependabot 설정을 추가했다.
- source-map-js 1.2.2와 undici 8.12.4, CI Go 1.26.10으로 보안 findings를 최소 보완했다. vendor advisory 수정 버전 하한도 정책 테스트로 검사한다.
- production verifier의 inventory 기준점 전에 exact borrowed Alpine/nginx base를 준비하고 identity/platform을 검증한다. 파생 archive를 daemon에 load하지 않는다.
- 로컬 Full/Quick/Security 및 독립 리뷰 기록을 추가했다. GitHub required-check 원격 활성화와 실제 clean-SHA 릴리스 성공으로 간주하지 않는다.

### 문서 정비 — 진행 중

- 현재 아키텍처와 과거 Stage 기록을 분리하고 한국어/영어 사용 안내, 기여·보안 정책을 정리한다.
- README에 사전부터 있던 공통 경로 초안은 실제 지원 기능과 구분해 [이력](docs/archive/IMPLEMENTATION-HISTORY.md)에 보존한다.
- 사용자 결정에 따라 Apache Software Foundation의 Apache-2.0 원문을 `LICENSE`에 추가했다.
- 비공개 보안 신고 채널, 원격 metadata/CI 반영, 실기기 acceptance 및 공개 릴리스는 별도 결정/검증 항목이다. 사용자는 이번 작업의 원격 변경 없이 로컬 커밋만 승인했다.

과거 기능·계약·수치의 원문은 [구현 이력](docs/archive/IMPLEMENTATION-HISTORY.md), 과거 실행 증거는 [이전 Phase 2 검증](docs/archive/PHASE2-VERIFICATION.md)을 참조한다.
