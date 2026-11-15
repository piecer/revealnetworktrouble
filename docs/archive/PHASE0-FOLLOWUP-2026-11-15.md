# Phase 0 커밋·후속 확인 — 2026-11-15

## 커밋과 승인 경계

- 기반 작업을 `857e67da5fa242ea13d977a732ef1748fbed7088` (`fix: establish portable phase zero foundations`)로 로컬 커밋했다.
- rename 감지를 끈 실제 committed path 목록으로 44개 경로를 대조했다. 사용자 로컬 Compose와 `.orig`는 제외했다. 루트 정리에 필요한 기존 PLAN_PROGRESS2.md 삭제는 커밋에 반영했고 Git 이력은 보존했다.
- 코드 push·태그·릴리스·운영 배포와 GitHub 설정 변경은 하지 않았다.
- 이름/모듈, 기존 Compose 파일 재배치, GitHub 쓰기 범위에 대한 확인 질문은 응답 없이 종료됐다. 이 기록을 승인으로 해석하지 않는다.
- [최초 Phase 0 검증 기록](PHASE0-VERIFICATION-2026-11-15.md)은 이전 dirty source 실행 당시의 기록으로 유지한다. 현재 진행 상황은 [ROADMAP](../../ROADMAP.md)을 따른다.

## 실제 커밋 검증

857e67d의 새 로컬 클론에서 `npm ci --offline` 후 Docker를 제외한 PATH로 `make test`가 exit 0이었다. `make test-integration web-test-deployment`와 `make web-test-syntax vet build`도 exit 0이었다. root Markdown은 6개다. 도구와 npm cache를 공유한 소스 클론이며 새 VM을 provisioning한 결과는 아니다.

원래 작업 트리의 compose.yaml은 compose.local.yaml과 byte-identical한 로컬 설정이다. 두 파일과 백업을 그대로 보존했다. 따라서 실제 작업 트리의 기존 Compose 계약 충돌은 이 커밋 검증과 구분한다.

## R-3: 기존 Dependabot 7건 triage

관측: 2026-11-15T11:02:49.853515+00:00 (UTC), 인증된 GitHub REST GET. 모든 페이지를 수집한 PR 목록에서 #1~#7의 merged_at과 merge commit을 확인했고 각 merge commit이 로컬 HEAD의 조상인지 Git으로 검증했다. 새 PR 병합·닫기·재실행은 하지 않았다. #8 역시 기존 병합 상태였다.

| PR | 변경 | 기존 병합 시각 UTC | 현재 코드와 대조한 판단 |
|---|---|---|---|
| #1 | chore(deps): bump nginx from `65645c7` to `165ddde` in /frontend | 2026-11-15T08:40:29Z | nginx base와 archive 검증 계약 불일치: 857e67d에서 기존 pin으로 복원. 새 base의 실제 release 검증은 미완료. |
| #2 | chore(deps): bump the actions group with 3 updates | 2026-11-15T08:40:52Z | 현재 workflow의 고정 ref는 CI 정책 테스트를 통과. 원격 required-check 강제는 별도이며 현재 보호 없음. |
| #3 | chore(deps): bump org.robolectric:robolectric from 4.14.1 to 4.17 in /android | 2026-11-15T08:41:00Z | Robolectric 4.17 반영 확인. 전체 Android 실행은 SDK/JDK 선행 조건으로 미완료. |
| #4 | chore(deps-dev): bump jsdom from 30.0.1 to 31.0.0 in /frontend | 2026-11-15T08:41:18Z | jsdom 31의 제거된 transitive dependency 및 hidden-focus 테스트 회귀를 857e67d에서 보완. 전체 Web suite 통과. |
| #5 | chore(deps): bump gradle-wrapper from 8.11.1 to 9.9.0 in /android | 2026-11-15T08:41:30Z | Wrapper 9.9.0과 기존 8.11.1 검증기 불일치를 추가 발견. 공식 JAR/distribution checksum 대조 후 검증기·문서·네 가지 회귀 보완. |
| #6 | chore(deps): bump androidx.core:core from 1.15.0 to 1.19.1 in /android | 2026-11-15T08:42:14Z | androidx.core 1.19.1은 compileSdk 37 이상을 요구하나 현재 설정은 35. 전체 Android gate 차단 요소로 유지. |
| #7 | chore(deps): bump com.android.application from 8.9.1 to 9.5.1 in /android | 2026-11-15T08:42:23Z | AGP 9.5.1 반영 확인. 기존 build-tools 35 설정과 Gradle/SDK 통합 수용 검증은 미완료. |

7건의 분류와 병합 상태 확인은 끝났지만, 이전 병합을 현재 Android 전체 검증·이미지 취약점 부재·공개 릴리스 승인으로 해석하지 않는다.

## Android Wrapper 회귀 보완

기존 `make android-wrapper-verify`는 실제 JAR과 검증기에 남은 8.11.1 pin이 달라 실패했다. 현재 파일을 무조건 신뢰해 기대값만 복사하지 않고 Gradle 공식 버전별 HTTPS checksum endpoint 두 개를 조회했다.

- JAR: `621008afc4f5d59357fcf06ffa644dd5aade5e091bdc363742710445f73cdaf8`
- 9.9.0 binary distribution: `0b00b0ffe07c4d4789c12d4935a44a7057d55774b7540bd4a3f76f336deb177a`
- 두 값이 이미 체크인된 JAR bytes와 properties의 distribution pin에 일치했다. JAR/properties를 바꾸거나 검증을 느슨하게 하지 않았다.
- 검증기와 현재 문서를 9.9.0에 맞췄다. 실제 verifier를 실행하는 네 가지 회귀는 정상 파일 수용, JAR 변조 거부, properties만으로 독립 checksum 교체 불가, 다른 distribution URL 거부를 검사한다.
- 수정 전 정상 파일 회귀 및 서로 다른 거부 단계 확인이 실패했다. 수정 후 네 건과 CI 정책 전체 22개, 실제 wrapper 검증이 통과했다.
- 깨끗한 커밋 클론에 이 다섯 파일만 overlay한 후 Docker 없는 `make test android-wrapper-verify`, Compose Go/Web 통합 검사, syntax/vet/build가 exit 0이었다.
- 초기 독립 리뷰는 기존 Java Properties/awk 파싱 우회를 재현해 승인을 거부했다. Parent도 실제 Java Properties.load가 선행 공백 override를 최종 URL/hash로 해석하는 동안 검증기는 exit 0인 것을 직접 확인했다. 수정 전에는 선행 공백·tab·form feed·escaped key·다른 separator·continued key·comment 내부 CR의 여덟 거부 회귀가 실제로 실패했다. 네 가지 초기 checksum 회귀 통과는 이 파싱 경계의 안전성을 입증한 결과가 아니었다.
- 첫 별도 수정 컨텍스트의 결과를 직접 읽고 재실행했다. Java Properties의 모호한 형식을 먼저 거부하는 printable ASCII/LF·단일 key=value·중복/연속행 금지 검증을 추가했다. 주석을 건너뛰기 전에 제어문자를 검사한다. 정상 주석·빈 줄은 허용한다. 이 수정의 Wrapper 회귀 7개와 CI 정책 전체 25개, 실제 Wrapper 검증 및 shell syntax 검사는 통과했지만 다음 독립 재리뷰에서 추가 차단 문제가 발견됐다.
- 원래 우회 fixture에 새 검증기만 복사해 다시 실행했다. Java가 최종 변경 URL/hash를 해석하는 사실은 동일했고 검증기는 이제 exit 1로 거부했다. Parent가 별도로 실제 검증기에 넣은 ASCII 제어문자/comment 경계 입력 33개도 모두 거부됐다.
- 수정된 코드·문서 8경로를 기반 커밋 클론에 정확히 overlay하고 hash가 실행 전후 같은지 확인했다. Docker 없는 `make test android-wrapper-verify`와 `make test-integration web-test-deployment web-test-syntax vet build`가 모두 exit 0이었다. 실제 Node TAP 합계는 tests 901, pass 900, fail 0, skipped 1이다. 실행하지 않은 release/Android/browser gate를 이 결과에 포함하지 않는다.
- 재리뷰에서 BusyBox awk가 NUL을 record 경계로 취급해 raw 문자 검사를 피하는 우회가 확인됐다. Parent도 checksum 키 앞에 NUL을 붙인 같은 파일에 대해 검증기 exit 0, 실제 Java Properties의 checksum=null을 직접 확인했다. 앞선 comment 경계 33개 검사는 이 key 경계/다른 awk 구현 조합을 검사한 것이 아니었다. URL/hash의 NUL 키 접두 두 변형을 실제 BusyBox subprocess 회귀에 추가해 RED를 확인했다.
- 두 번째 수정은 POSIX `od`의 실제 byte 출력과 성공 상태를 먼저 확인한 뒤 숫자 AWK 검사·canonical 문법 검사·독립 pin 대조를 수행한다. 원시 NUL은 셸 변수나 AWK 레코드에 넣지 않는다. Parent가 원래 NUL fixture의 거부, od 부재와 출력 후 실패의 거부, Wrapper 회귀 10개·CI 정책 28개 통과를 직접 확인했다. system/BusyBox awk·od 네 조합의 실제 실행을 다시 수행했고, 각 조합에서 256개 byte 경계를 검사해 printable ASCII/LF 96개만 수용하고 나머지 160개는 거부했다. standalone 검증에 Java·Python·Gradle·네트워크는 필요 없고 POSIX od는 필요하다.
- 최종 바이트 수정의 8경로 overlay도 Docker 없는 `make test android-wrapper-verify` 및 Compose Go/Web 통합·syntax·vet·build를 모두 exit 0으로 통과했다. Node TAP은 pass 900, skipped 1, fail 0이었다.
- 최종 독립 재리뷰는 `passed: true`, security_concerns/logic_errors/suggestions 모두 빈 배열이었다. 검토된 5파일 hash는 실행 전후 동일했다. 리뷰어의 실제 결과 파일을 Parent가 읽어 실패 목록이 비어 있고 네 조합의 각 212개 corpus 결과와 Java oracle이 기록돼 있음을 확인했다. 이 승인은 해당 소스 범위의 검토이며 Android 전체나 release acceptance 승인이 아니다.

## S-3/R-5: 원격 조회 결과

동일 관측 시점의 GET 결과:

- private vulnerability reporting: enabled=false.
- description과 homepage: null, topics: 빈 배열.
- main protected=false. classic protection GET은 Branch not protected 404, effective branch rules GET은 빈 배열이었다.
- 관리자 권한이 있는 인증은 확인했지만 저장소 설정은 변경하지 않았다. 브랜치 보호 확인은 수행했고, 보안 신고 활성화와 metadata 작성은 승인 대기다.

## Phase 0 전체 완료가 아닌 이유

1. R-1: CheckNetwork/Android ID는 유지하고 Go 모듈만 실제 저장소 경로로 맞출지 결정이 필요하다.
2. 로컬 Compose 설정을 보존한 채 공통 compose.yaml을 복구할지 결정이 필요하다. 현재 파일은 건드리지 않았다.
3. S-3/R-5의 원격 보안 신고 활성화·description/topics 수정 범위 승인이 필요하다. PR 7건은 모두 이미 병합됐으므로 추가 병합은 필요 없다.
4. Android wrapper 무결성 검증은 통과했지만 JDK 17 요구와 호스트 21, compileSdk 35와 androidx.core의 37 이상 요구는 남아 있다. checksum 통과를 JVM/lint/assemble 통과로 표시하지 않는다.
5. 첫 실행 환경 provisioning, 현재 문서 총량 목표, full release/browser/device acceptance와 Phase 1~4는 이 후속 작업으로 완료되지 않았다.

## 증거

`/home/piecer/.hermes/cache/scratch/checknetwork-phase0-completion-dgh7ldb9`

- github-readonly-state.json: 원격 GET 응답과 관측 시각. raw public repository metadata이며 credential을 저장하지 않았다.
- committed-gates.json과 각 log: 857e67d 소스 클론 검증.
- android-prerequisites.json/log: 변경 전 Wrapper 불일치와 JDK 환경 거부.
- gradle-9.9.0-official-checksums.json: 공식 URL, 최종 HTTPS URL과 실제 checksum 응답.
- wrapper-followup-inputs.json, wrapper-followup-gates.json/log: 다섯 파일 hash와 후속 source 검증.
- properties-bypass-reproduction.json, wrapper-canonical-review-inputs.json: 최초 Java/검증기 불일치 및 수정 후 같은 fixture의 거부와 재리뷰 입력 hash.
- wrapper-canonical-control-results.json: 실제 검증기의 ASCII 제어문자 경계 거부 결과.
- wrapper-busybox-nul-reproduction.json: BusyBox awk가 통과시킨 NUL checksum 키와 실제 Java checksum=null의 부모 직접 재현.
- wrapper-canonical-inputs.json, wrapper-canonical-gates.json/log: canonical 수정과 문서 8경로 overlay의 입력 hash·전체 기본/통합/품질 gate 결과. 뒤이은 문서의 진행 상태 갱신은 별도로 문서 계약을 재검사한다.
- wrapper-byte-review-inputs.json, wrapper-byte-inputs.json, wrapper-byte-gates.json/log: NUL 대응의 현재 입력 hash와 재실행 gate 결과.
- `/home/piecer/.hermes/cache/scratch/wrapper-byte-cycle2-f3bh_40z/results.json` 및 각 unittest.log: Parent가 재실행한 system/BusyBox awk·od 네 조합과 byte 경계 검사. 테스트 수는 원본 unittest 출력과 구분한다.
- `/home/piecer/.hermes/cache/scratch/wrapper-final-independent-review-z6kpnx5q/results.json`: 최종 독립 리뷰의 실제 corpus·Java oracle·도구 실패 전파 결과. 리뷰 승인 메시지는 delegation `deleg_fb60fea4`에 보존된다.

Scratch 증거는 영구 release provenance가 아니다. 원본 체크아웃의 사용자 운영 설정과 백업은 별도 보존한다.
