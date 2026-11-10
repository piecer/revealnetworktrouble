# Changelog

이 파일은 구현 변경과 릴리스 발행을 구분한다. `VERSION=0.1.0`은 source version이며 아직 공개 배포 일자나 태그 발행을 기록하지 않는다.

## [Unreleased]

### 보안·자동화 — 734885a

- trusted-local의 non-loopback bind에 exact `CHECKNETWORK_EXPOSE_TRUSTED_LOCAL=yes` 승인과 startup WARN을 요구한다. 기본 Compose의 host loopback 공개를 유지한다.
- 실제 startup WARN, proxy forwarded-header 인증/rate-limit 경계 및 release smoke 실행 인자의 회귀 검증을 추가했다.
- PR Quick/Security, main/merge queue/정기 Full, fail-closed aggregate와 검증 전용 exact-SHA release candidate workflow, 고정 toolchain 및 Dependabot 설정을 추가했다.
- source-map-js 1.2.2와 undici 8.12.4, CI Go 1.26.10으로 보안 findings를 최소 보완했다. vendor advisory 수정 버전 하한도 정책 테스트로 검사한다.
- production verifier의 inventory 기준점 전에 exact borrowed Alpine/nginx base를 준비하고 identity/platform을 검증한다. 파생 archive를 daemon에 load하지 않는다.
- 로컬 Full/Quick/Security 및 독립 리뷰 기록을 추가했다. GitHub required-check 원격 활성화와 실제 clean-SHA 릴리스 성공으로 간주하지 않는다.

### 문서 정비 — 진행 중

- 현재 아키텍처와 과거 Stage 기록을 분리하고 한국어/영어 사용 안내, 기여·보안 정책을 정리한다.
- README에 사전부터 있던 공통 경로 초안은 실제 지원 기능과 구분해 [이력](docs/IMPLEMENTATION-HISTORY.md)에 보존한다.
- 사용자 결정에 따라 Apache Software Foundation의 Apache-2.0 원문을 `LICENSE`에 추가했다.
- 비공개 보안 신고 채널, 원격 metadata/CI 반영, 실기기 acceptance 및 공개 릴리스는 별도 결정/검증 항목이다. 사용자는 이번 작업의 원격 변경 없이 로컬 커밋만 승인했다.

과거 기능·계약·수치의 원문은 [구현 이력](docs/IMPLEMENTATION-HISTORY.md), 최신 실행 증거는 [Phase 2 검증](docs/PHASE2-VERIFICATION.md)을 참조한다.
