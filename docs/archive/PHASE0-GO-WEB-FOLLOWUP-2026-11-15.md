# Phase 0 승인 범위 후속 — Go/Web·Compose·저장소 메타데이터

## 범위 — 2026-11-15

- 사용자 승인: Android 제외, GitHub description/topics 변경, 로컬 설정을 보존한 공통 Compose 복구.
- 사용자 보류: 제품명·Go 모듈 경로·주 배포 방향 확정. 현재 이름과 go.mod는 변경하지 않는다.
- 코드 push·태그·릴리스·운영 컨테이너 재생성/재시작은 범위 밖이다. Android 코드·SDK·Wrapper·빌드/테스트 target도 이번 작업에서 변경·실행하지 않는다.
- 기준 HEAD: `7893d04abec5fc66732567c6a5166d6bd4f24b92`. 앞선 검증 기록은 당시의 관측으로 보존한다.

## 공통 Compose 복구와 로컬 보존

변경 전 compose.yaml과 compose.local.yaml의 bytes가 동일함을 확인하고 두 파일·go.mod·기존 백업의 hash를 기록했다. 기존 `TestComposeRuntimeContract`는 공통 환경 변수 override 네 항목 부재로 실제 실패했다.

승인 후 compose.yaml만 기준 HEAD의 공통 파일로 정확히 복구했다. compose.local.yaml·go.mod·백업은 그대로 보존했다. 같은 계약 테스트는 통과했고 공통/로컬 파일 각각의 `docker compose ... config --quiet`도 통과했다. 실제 resolved 설정이나 로컬 인증 값은 출력·문서화하지 않는다.

- 공통 파일: loopback publish와 환경 변수 override를 사용하는 저장소 기본값.
- 로컬 파일: 독립 구성으로 유지한다. 공통 파일과 함께 merge하지 않으며 관리 명령에서 `-f compose.local.yaml`을 명시한다.
- `.gitignore`에 로컬 파일을 추가해 포트·인증 설정이 실수로 커밋되는 것을 방지한다. 로컬 파일 내용은 커밋하지 않는다.
- [README](../../README.md)와 [운영 안내](../OPERATIONS.md#로컬-compose-설정-보존)에 파일 선택을 명시했다.

## GitHub readback — 2026-11-15T12:14:06.519740+00:00

수정한 필드는 description과 topics뿐이며 각 쓰기 후 정확한 API 대상을 다시 읽었다. 기존 topics와 합집합을 사용했고, 조회 당시 기존 topics는 비어 있었다.

Description:

> Network troubleshooting with DNS, TCP, HTTP/TLS and traceroute diagnostics, topology visualization, and IP context.

Topics: dns, golang, javascript, network-diagnostics, network-topology, network-troubleshooting, traceroute.

이번 쓰기 전부터 private vulnerability reporting은 enabled=true였다. 이전 관측의 off 상태를 현재 상태로 재사용하지 않는다. main 브랜치 보호는 false였다. 비공개 신고·브랜치 보호·이름·기본 브랜치·공개 범위·homepage·원격 main SHA는 변경 전후 동일함을 확인했으며, 해당 설정을 직접 변경하지 않았다. [보안 정책](../../SECURITY.md)은 이미 활성화된 신고 창구에 맞췄다.

## 비Android 검증

실제 원래 작업 트리에서 다음 검증이 exit 0이었다. 기본 make test는 Android 환경/Wrapper 정책 검사를 포함하므로 이번에는 비Android 계층만 명시적으로 실행했고 원래 Make/CI 정의를 바꾸지 않았다. Android 또는 전체 CI 통과로 표시하지 않는다.

- `go test -count=1 ./...`: 캐시된 테스트 결과를 사용하지 않은 Go 전체 검사 통과.
- `make web-test`: Node TAP tests 901, pass 901, fail 0, skipped 0.
- `make test-integration web-test-deployment web-test-syntax vet build`: Compose Go/Web 통합·JS 구문·Go 정적 검사·실제 바이너리 빌드 통과.
- 비Android CI 정책: 기존 6개 파일의 테스트 18개 통과, skip 0. Android Wrapper 정책 파일은 사용자 범위에 따라 로드하지 않았다.
- `.gitignore` 추가 전 local 파일의 check-ignore 실패를 확인했고 추가 후에는 성공했다. 공통 Compose bytes는 기준 HEAD와 동일하다.

운영 보존은 대상 Compose label의 컨테이너 ID·실행 상태·StartedAt과 로컬 파일 hash를 작업 전후 대조해 동일함을 확인했다. 새 머신 provisioning·실제 배포·브라우저·기기·릴리스 검증은 범위 밖이다.

## 증거와 남은 상태

증거 디렉터리: `/home/piecer/.hermes/cache/scratch/checknetwork-compose-metadata-1q4zgpyo`.

- before-local.json: 기준 HEAD와 로컬/백업 파일 hash.
- containers-before.json: 대상 실행 컨테이너의 변경 전 ID·상태·시작 시각.
- github-metadata-before.json, github-metadata-after.json: 승인 필드와 비승인 설정 보존의 실제 GET 관측.
- gates.json 및 각 log: 실제 Go/Web·통합·정적·빌드 명령, 입력 기준 SHA와 exit code.
- ci-policy-no-android.json/log: 선택한 6개 정책 파일과 제외한 Android 파일, 실제 테스트 수·결과.
- verification-summary.json, containers-after.json: 실제 테스트 합계와 로컬/백업·모듈·컨테이너 보존 결과.
- verified-inputs.json: 테스트 당시의 명시 7경로 hash. 이후 문서의 완료 상태 갱신은 별도로 문서 계약을 재검사한다. Scratch는 영구 release provenance가 아니다.

Android 제외와 R-1의 명시적 보류를 [ROADMAP](../../ROADMAP.md)에 반영했다. 완료한 승인 항목과 원래 Phase 0의 모든 수용 기준 충족은 구별한다.
