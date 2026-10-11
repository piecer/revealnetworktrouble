# 구현 이력과 남은 검증

개별 작업 계획에서 계속 필요한 구현 순서, 계약과 미완료 항목을 모은 문서입니다.
과거 체크포인트를 보존한 것이며, 문서 정리나 테스트 통과만으로 미수행 검증을 완료 처리하지 않습니다.
현재 사용법은 [README](../README.md), 상세 검증 방법은 [테스트 기준](TESTING.md)을 참고하세요.

## Stage 1: 실행 경계와 HTTPS 판정 복구

당시 범위: 요청·실행 자원 제한, 공개·로컬 배포 경계와 HTTPS 판정 복구.

## Stage 2: 설명 가능한 자동 분석 계약

당시 범위: 관측 결과에서 원인 후보, 근거, 관측 한계와 다음 확인 단계를 분리하는 분석 계약.

## Stage 3: Web 요청 lifecycle과 분석 워크스페이스 UI

당시 범위: 입력 변경·취소·늦은 응답을 구분하는 요청 소유권과 분석 화면.

## Stage 4: Web topology 정확성과 대용량 성능

당시 범위: IPv6 경로 집계, 제한된 크기의 topology 응답과 화면, 생략된 정보의 명시적 표시.

## Stage 5: Android 기능 동등성과 lifecycle 아키텍처

당시 범위: 네이티브 클라이언트의 검사 요청, 결과 표시, 화면 수명주기와 공유.
자동화된 구현 검증과 실기기·접근성 검증은 별도입니다.

## Stage 6: 외부 보강·관측성·운영 성능 강화

당시 범위: GeoIP 동시 작업·캐시·제공자 실패 표시, 진단 자원 수명과 개인정보를 보호하는 운영 관측.
후속 Stage 7에 기록된 Stage 6 exact-SHA closure는 과거 완료 근거이며 현재 소스의 새 검증 결과는 아닙니다.

## Stage 7: 교차 플랫폼 재감사와 다음 루프 계획

재감사에서 발견한 연결 수 제한, deadline, readiness, 진단 결과와 전송 결과의 구분,
Android 공유·capability 문제를 정리했습니다. 구현 이력: `36dc1dc`.
Stage 7 exact-SHA closure는 후속 Stage 8 기록에서 역사적 완료 근거로 취급합니다.

## Stage 8: 진단 사실과 클라이언트·릴리스 계약

기준 부모 커밋은 `36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243`, 구현 커밋은 `43689b3`입니다.
구현 커밋의 존재와 전체 릴리스 수용 완료는 구분합니다. 아래는 이전 계약에서 유지하는 세부 조건입니다.

- Android의 알 수 없거나 모순된 오류 응답은 exact local code `invalid_server_response`로 처리합니다. HTTP 상태는 보존하지만 재시도 가능 여부를 임의로 추정하지 않습니다.
- Android 테스트 결과의 exact `binary/` companion은 테스트 성공의 증거가 아닌 제한된 부속 메타데이터입니다.
- API hard limits are archive 512 MiB, members 4,096, member bytes 256 MiB, member total 512 MiB, layers 64, layer bytes 256 MiB, expanded layer bytes 512 MiB, selected bytes 128 MiB. 추출 대상은 caller-owned empty real directory여야 합니다.
- 파생 API/Web archive는 never daemon-loaded, daemon-tagged, imported, deleted, or executed. 빌린 base에서의 mount 호환성 검사는 파생 archive의 실행 증거가 아닙니다.
- API owns one smoke container and one API network. Success output is exactly six API fields: `api_archive`, `api_config`, `api_manifest`, `api_rootfs`, `api_binary`, `api_traceroute`.
- 재현성 검증은 three umasks 077/027/000 및 canonical Docker inventory 비교를 포함합니다.
- Web 입력과 화면의 계약은 `MAX_TARGETS=20`, `MAX_DOCUMENT_ELEMENTS=1200`입니다.
- Documentation changes invalidate prior current-byte CI/count evidence. 최종 문서 수정 이후의 소스로 검증해야 합니다.

Historical record: Web 260 tests; result shapes 31; expanded result matrix 136; 456 fixture-derived semantic mutations; 13 generic cancelled-detail rejections; all eight producer traceroute witness fixtures; Android 297 direct-child XML tests + 2 variant-contract tests; exact real release-gate fake cases 12.

당시 제외 항목은 ignored repository-root `checknetwork-api` binary였습니다. 해당 파일의 보존과 릴리스 산출물 검증은 별개입니다.
다음 두 문장은 당시 최종 수용을 열어 두었던 기록이며, 이후 구현 커밋이 없다는 뜻은 아닙니다.

> Final post-doc `env -u JAVA_HOME -u ANDROID_HOME -u ANDROID_SDK_ROOT make ci` remains pending.
>
> No final manifest, commit, release, five-way review, exact-SHA, or main-integration claim is made here.

최종 clean-environment CI, 불변 후보에서의 릴리스 검증, five-way review 및 exact-SHA 수용을
묶어 닫는 기록은 별도 확인이 필요합니다. 이번 문서 통합으로 완료됐다고 주장하지 않습니다.

## Stage 9: 2D/3D 토폴로지 복원

구현 커밋은 `f0793ed`입니다. 기본 선택은 `2D 그래프`이며,
`3D 그래프`를 선택하면 동일한 node/link set을 다른 시점에서 보여줍니다.
데이터나 분석 결과를 바꾸지 않는 순수 view projection으로, 진단 결과를 변경하지 않는다.
interaction event와 resize에서만 redraw하며 지속 애니메이션을 추가하지 않습니다.

기존 계약은 Canvas 하나와 고정 control 수만 추가하고, document-wide 1,200 element 한도와
nodes 500/links 1,000/routes bounded를 지키며 DPR을 bounded finite 값으로 clamp하는 것입니다.
배포 시 exact Web production asset set을 확인해야 합니다.
Chrome이 없는 환경에서는 실제 브라우저/화면 판독기 검증을 완료했다고 주장하지 않는다.

이전 전체 수용 체크포인트는 “Stage 9 implementation is pending final gates/review, not complete”였습니다.
후속 기능별 자동 테스트나 커밋만으로 전체 독립 검토·불변 후보 릴리스·실기기 검증을 대신하지 않습니다.

## 후속 개선에서 보존할 항목

### 아키텍처 개선

`b6e0902`는 관측 보존, Web 저장·표시, Android 수명주기·복구, 배포 경계 검증,
capability 입력 검사 분리의 다섯 제한된 개선 단계를 통합했습니다.
더 큰 구조 변경은 수행 범위가 아니었으며 다음 권고가 남아 있습니다.

- [ ] Web controller/repository 분리, backend details 타입 명확화, 공통 archive 처리 정리.
- [ ] 개인정보를 보호하는 내부 event 계약을 정한 뒤 policy rejection 시 관측 보존 검토. 공개 응답의 details-free 422 및 fail-closed 동작은 그대로 유지.

### 무응답 홉 표시

`0511b93`에 연속 무응답 접기, 숨긴 구간의 점선 연결과 응답한 뒤쪽 홉 보존이 구현돼 있습니다.
기능 회귀 테스트와 해당 작업 전체의 최종 독립 검토 기록은 구분합니다.

- [ ] 해당 작업의 최종 local gate 및 독립 검토 완료 기록 연결.
- 원본 경로의 prefix 예산이 표시 변환보다 먼저 적용됩니다. 무응답 구간을 경로별로 펼치는 과정에서도 추가 표시 생략이 생길 수 있습니다.
- 점선 우회 연결은 직접 관측한 연결이 아닙니다. 설명은 범례와 상세 목록에 있으며 Canvas edge-hover 기능을 구현했다고 주장하지 않습니다.

### 전체 제품 수용과 후속 기능

- [ ] 증상별 guided preset의 범위를 확정하고 구현·수용 기준과 일치시키기.
- [ ] 실제 배포된 GUI/API에서 진단 확인.
- [ ] 실제 Chrome/Firefox/Safari, Windows 환경과 스크린 리더 검증.
- [ ] Android 실기기, TalkBack/Switch Access, OEM 공유 화면과 프로세스 종료 후 공유 파일 접근 검증.
- [ ] TLS reverse proxy의 HTTP/2 deadline/cancellation 검증.
- [ ] canonical exact-SHA Docker release 검증과 운영 registry push/signing/SBOM, hosted CI.

추가 기능 후보와 전체 계획은 [구현 계획](PLAN.md)에 유지합니다.
