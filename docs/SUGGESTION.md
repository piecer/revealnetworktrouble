# SUGGESTION.md — 기능 검토·개선 제안 (현재 코드 대비 재검증본)

원 검토는 ROADMAP을 읽기 전 작성됐습니다. 2026-11-22에 ROADMAP 전문과 코드에 직접 대조해 확정·정정했습니다.
표기: [확인] 대조로 확정, [정정] 원문 오류 수정, [신규] ROADMAP에 없는 항목.
이 문서 자신의 인용도 아래 제안 패턴에 걸리지 않게 작성했고, 게이트 통과로 확인했습니다.

## 기능 총평

진단 엔진은 해석 정직성과 경계 안전성이 강한 반면, 원인을 좁히는 진단 깊이가 얕다.
기준: "어떤 증상에서 원인 후보를 더 좁혀 주지 못하는가".

## 기능 우선순위 (ROADMAP 대조 완료)

| # | 기능 | ROADMAP 대조 | 코드 검증 | 제안 |
|---|---|---|---|---|
| 1 | HTTP(S) 단계별 시간 분해 | = D-2 | [구현] httptrace가 DNS/connect/TLS/TTFB 네 phase를 관측하고 producer가 `details.stage_timing` block을 기록(`backend/diagnostic/http.go:175`), rule이 dominant phase를 slow-server 판정에 사용(`analysis.go:133`), Web·Android contract가 같은 block을 pin | D-2 구현 완료. phase별 finding code 분리(단일 slow-server rule)와 IP 단위 attribution(#2)이 남아 있다 |
| 2 | 해석된 IP(A/AAAA)별 연결 결과 | D-3(family 비교)과 부분 중복 | [확인] 전체 IP 순회 로직 없음 | D-3에 흡수. MaxTargets=20(`runner.go:18`), IP 수 상한 필요. 반복 window rule(`runner.go:48`)이 samples를 계산에 넣는다 |
| 3 | 반복 샘플링(성공률·p50/p95·지터) | = D-3 ①(TCP 반복, 권한 불필요) | [구현] `model.go:45` `Samples`(1~10, traceroute 금지)와 `sample_stats` block이 contract에 존재 | [처리] 실질적 gap은 admission 계산이 아니라 window였다: 반복 window가 `samples × timeout`이 아니라 `timeout` 하나분이라 뒤 샘플이 observation 대신 budget artifact로 채워졌다. window와 deadline을 같은 rule로 gate에 pin. IP 수 상한(#2)은 별도 항목으로 남아 있다 |
| 4 | 이름 기반 vs IP 기반 교차 판정 | D-1(리졸버 비교) 인접, 규칙 없음 | [확인] analysis.go에 규칙 없음 | D-1에 흡수 |
| 5 | traceroute 프로브 방식 선택(UDP/ICMP/TCP) | Phase 4(ICMP·MTU)와 일부 | [확인] RunTraceCommand는 외부 바이너리, capability는 kinds만 노출(`server.go:256`) | [신규] 비루트 UID 65532(`Dockerfile:34`) — raw 소켓 권한 확인이 선행. 지원 방식만 capability로 알리는 기존 구조에 맞춤 |
| 6 | TLS 체인: 중간 인증서 누락 vs 미신뢰 구분 | 미등록 | [확인] `analysis.go:319` FindingTLSUntrusted는 미분류 | [신규] AIA 조회는 새 외부 요청 → 기존 SSRF dial 정책 통과 필수 |
| 7 | 메일 심화(STARTTLS·MX/SPF/DMARC) | MX/TXT 조회 부분은 D-1 | [확인] `analysis.go:479` greeting만 관측, coverage 문구로 명시됨 | D-1과 결합. 새 verification_scope 값 |
| 8 | 보고서 비교(JSON import diff) | = D-5 | [확인] 클라이언트 diff 없음. `common-prefix.js:138`의 firstDiffBit는 IP 접두 그룹핑이고 보고서 diff 아님 | D-5 유지 |
| 9 | 문의용 요약 내보내기(익명화) | = 아카이브 분석 PRD-8(P1/M, 미구현) | `docs/archive/PLAN-ANALYSIS-2026-11-21.md:292` | PRD-8로 유지 |
| 10 | 캡티브 포털·DNS 변조 탐지 | 미등록(U-4 인접) | [확인] 로직 없음 | [신규] 로컬 실행 모드에서 가치가 큼 |

MTU/PMTUD 보류 — ROADMAP Phase 4 판단(플랫폼 의존성)과 일치.

## 순서 조정 권고

ROADMAP 순서는 Phase 2(기능 D-*) → Phase 3(M-3). 원 제안 순서는:

1. M-3 Details 타입화(`analysis.go` 1,309줄 [확인], 상당 부분이 map[string]any 재추출)
2. 계약 변경 일괄 묶음(finding-contract-v2; Go·Web·Android 동시 변경) — ARC-2(OpenAPI 단일 소스)는 ROADMAP이 아니라 `docs/archive/PLAN-ANALYSIS-2026-11-21.md:315`에 있고 [ ] 미완료. 선행 시 비용 감소
3. 기능 1·2·3

기능 1~10은 대부분 새 finding 추가라 M-3 구조 위에 얹으면 untyped 추출 코드가 증가한다. D-2/D-3와 M-3 선후 결정이 문서가 해소해야 할 핵심 결정이다.

## 기존 기능 보완점

- ECMP: 문서 어디에도 구분 설명 없음 [확인]. "분산 경로 가능성" 표시 또는 흐름 고정 옵션 권고.
- latency_delta_ms는 이미 존재(`traceroute.go:58`). 크게 뛰는 구간 강조 + ASN 경계 + "ICMP 응답 지연·복귀 경로 영향일 수 있음" 단서 함께.
- Android 대화형 그래프 부재는 `android/README.md:64`에 device acceptance 후속 범위로 기록 [확인]. 위 1~4번보다 뒤.

## 잘 된 점 (재확인으로 유지)

- 카운트 금지가 문구 고정 아니라 규칙. 과거 수치는 docs/archive/, 현재는 게이트 실행으로 관측 (`documentation_contract_test.go:77`, TestLiveDocumentationDoesNotAssertTestCounts). [확인]
- required_tools 선언 + command -v 사전 검사 (`operations_contract_test.go:142`). 선언 도구만 PATH에 둔 fixture 실행으로 숨은 tar 의존을 실증. [확인]
- Go 버전 표기 정리, description/topics 적용 기록 확인(ROADMAP:51, 235). [확인]

## 지적 사항 (중요도 순, 재검증)

1. 카운트 패턴이 좁다 (`documentation_contract_test.go`) [구현] — 권장 확장 form을 gate에 추가했다.
   - `숫자 tests` 형태만 잡으니 단어 앞 형태(`tests` + 숫자)를 놓친다 — ROADMAP.md:359 관측 기록. 한국어 형태(숫자+개 테스트, 테스트 + 숫자)도 놓친다.
   - 합 형태 패턴은 + 뒤 라벨이 낀 실행 문서에 즉시 걸린다: docs/TESTING.md:155에 미검증 합 인벤토리 서술이 현존.
   - 단어 tests 없는 변동 수치(검증기 수·inventory·DOM 최대치)는 정책 밖.
   - 권장 확장:
     `[0-9][0-9,]*\s*(개\s*)?(tests?|테스트|cases?|케이스)`
     `(테스트|tests?)\s*(수|개수)?\s*[0-9]+`
     `[0-9]+\s*\+\s*(\S+\s+)?[0-9]+\s*=\s*[0-9]+`
   - 확장 시 즉시 감지되는 현존 위치: ROADMAP.md:333(종류 셀 수), ROADMAP.md:359(관측 기록값), docs/TESTING.md:155(합 서술), docs/API.md:157(계약 고정값 — producer fixture로 enforced), docs/IP-CONTEXT-ANDROID.md:27(계약 고정값 — corpus hash로 enforced).
     결정(2026-11-27): 예외 없이 문장 재작성. contract-pinned cardinality는 test가 enforcement하고 live prose는 enforcing gate를 참조한다. 원 문서 자신의 인용도 게이트를 실제로 깼다(실행 확인) — code-span 처리로 해소됨.
   - ROADMAP 현황 표는 패턴에 안 걸려도 인벤토리 수치를 담고, "과거 reading은 archive에만" 원칙과 충돌했다. → 구현: 부록 A 스냅샷을 `docs/archive/ROADMAP-APPENDIX-A-SNAPSHOT.md`로 옮기고 live 문서에는 gate 실행으로 관측한다는 pointer만 남겼다.
2. Fatal vs skip 정책 양립 (`operations_contract_test.go` — 도구 부재 시 t.Fatalf) [구현] — `declaredToolPath` 하나의 policy로 통일(부재 skip, 요청 시 실패).
   - Compose 쪽은 부재 시 skip, 게이트 플래그 요청 시 실패(`compose_config_test.go:588-591`, `portable_environment_test.go:12`). 정책 통일: 부재 skip, 요청 시 실패.
   - 플래그명도 grep 교차검증 게이트에까지 쓰이는 issue였다. → 구현: `CHECKNETWORK_RUN_INTEGRATION_TESTS`로 renaming(test 파일, Makefile `test-integration`·deployment target, `frontend/deployment.test.js`)하고 `TestComposeIntegrationMakeTargetRunsExplicitChecks`의 Makefile pin과 `TestDeclaredToolAbsencePolicySkipsUnlessGateRequested`가 함께 pin한다.
3. pin 복수 중복으로 Dependabot 막힘 [확인] — ROADMAP:234의 "mirror 변경(검증기·contract test) 필요, 단파일 PR 통과 불가" 기록. versions.env 단일 소스에 Dockerfile·검증기·테스트가 읽는 구조 권고 유지.
4. 검증 인프라 투자 집중 [정정]: ERE 부분집합 평가기는 약 100줄이 아니라 15줄(`compose_config_test.go:557-571`). 비용 우려는 원문보다 작고, 사용자에게 보이는 기능 우선 결론은 유지.
5. 사소한 점
   - ROADMAP 깨진 문장(146, 150) → 정리 완료. 같은 부류의 "실행complete"이 `docs/TESTING.md:28`에도 있어 함께 정리했다.
   - `posixEREExists`는 대괄호 안 `\`를 구분하지 못했다: POSIX 리터럴 vs Go 이스케이프로 `[\.]` 의미가 갈린다. → 구현: `posixERETranslation`이 대괄호 안 `\`를 POSIX member로 번역하고 `TestBracketExpressionsKeepBackslashAsLiteralMember`가 pin한다. grep 교차검증도 같은 패턴으로 disagreement를 확인한다.
   - 스크립트 본문의 `command -v "$tool"` 문자열 검색 단언은 구현 텍스트 고정이었다 → 구현: text pin을 제거하고 omitted tool을 fixture로 실행해 actionable named error를 요구하는 behavior pin으로 전환(`git`과 `awk` 두 도구로 확인; probe에서 script의 사전 검사 block을 제거하면 RED).
   - HOME을 도구 symlink 디렉터리로 지정(`operations_contract_test.go:201`) — [측정] 해당 계약 테스트 단독 실행 3.034s. 허용 범위.
   - CI 매트릭스에 go.mod 최소 버전이 없었다(toolchain action이 고정 patch 버전만 사용) → 구현: `min_go` job이 `go-version-file: go.mod`로 선언 최소 버전을 읽고 build/vet를 실행한다. `scripts/ci-status.py`는 그 job result를 필수로 요구하고 `scripts/test_ci_definition.py`가 job과 toolchain≥go.mod 관계를 pin한다. post-1.22 API scan: 없음. 로컬 실행은 toolchain download 차단으로 불가 — CI 첫 실행으로 확인된다.
   - 루트 Markdown 게이트(6개 이하, `documentation_contract_test.go:38`): entry point 6개가 슬롯을 채운 상태라 이 문서가 루트의 7번째 md면 게이트가 깨진다 — 실제로 깨져 있었고(실행 확인), 문서를 `docs/`로 옮겨 해소.

## 앞선 분석 정정

- "hosted CI 없음 추정" 틀림: Actions 26회 실행 기록(ROADMAP:56).
- PR 8건은 11-15 시점 기록(당시 open/병합 0). 후속 갱신: #10·#11·#12 병합, 앞선 Dependabot 7건 모두 병합(ROADMAP:10, 234).
- 문서 계약 파일에 기록됐던 줄 수 inventory는 live 문서에 두지 않는다. 과거 reading은 archive에만 남기고 현재 크기는 gate 실행으로 관측한다.
- ROADMAP 기능 항목(U-*, D-*) 미대조였던 항목은 위 표로 대조 완료.

## 검증 기록 (2026-11-22)

- ROADMAP.md 전문 대조, 인용 위치 grep/read 재확인.
- 제안 패턴을 live 문서 전체에 적용: 위 5곳 즉시 감지(원 SUGGESTION.md 자체는 재작성으로 해소).
- `go test ./backend/diagnostic -run TestLiveDocumentationDoesNotAssertTestCounts` — 재작성 후 통과.
- `go test ./cmd/checknetwork-api -run TestCleanArchiveContractDeclaresAndChecksItsToolPrerequisites` — 3.034s 통과.

## 구현 상태 (2026-11-25)

- producer scope gate: `analysis.go`의 `closedBlockLegalFor`가 kind과 outcome이 만들 수 없는 block을 read 전에 거부하고, 그 block을 자기 signal로 기록한다. Web의 `aggregateKeysForKind`가 같은 rule이고 양쪽 test가 pin한다.
- shape block list는 kinds 전체의 union이며 개별 result는 자기 kind의 projection만 가질 수 있다. Go와 Web test가 union 유지와 per-kind 거부를 각각 단언한다.
- control 분리: ratio 단독(절대 floor만 넘는 케이스)과 threshold 단독(contract-valid block)로 나눠 각 분기가 단독으로 발화함을 test로 증명한다. Web 단언은 발화한 check의 exact message를 pin한다.
- client 상수는 producer 상수를 재진술하고, parity gate가 app.js의 literal 재기술을 금지한다.
- reachability prose를 실측 기준으로 정정: `resolver_comparison` block은 current version에서 checker가 write하지 않으므로 fixture scenario로만 reachable하다.
- Android는 이번 검증 범위에서 제외했다(정적 finding만 유지, 게이트 실행 불가).

## 지적 사항 처리 결과 (2026-11-27)

- [x] 1 카운트 패턴 확장 + live 문서 수치 제거 + archive move. decision = 예외 없는 문장 재작성. gate가 실제로 패턴을 보는지 probe로 확인(probe 실패 → RED, restore → GREEN).
- [x] 2 tool-absence policy: `declaredToolPath`로 skip/fail 한 policy 통일, flag name 정합성 확보. `TestDeclaredToolAbsencePolicySkipsUnlessGateRequested`가 양쪽 거동을 pin.
- [x] 3 문자열 검색 pin: required_tools declaration + omitted-tool fixture 실행(behavior)으로 전환. 남은 string pin(`TestDocumentationLinksResolve`의 file+line, `test_ci_definition.py`의 shell literal)은 의도된 선언 pin으로 유지 — 문서가 "본문 문자열 검색"을 금지하는 부류가 아니다.
- [x] 4 정정 유지(ERI 평가기 15줄).
- [x] 5 깨진 문장 정리, bracket backslash 처리, text pin → behavior pin, CI toolchain floor.

## 기능 항목 구현 상태 (2026-11-27 실측)

- [x] #3 반복 샘플링 — `Samples` 필드와 `p50_ms`/`p95_ms`/`jitter_ms` block이 contract에 존재. window 계산이 admission 계산과 같은 rule을 따르게 했다: window가 `timeout` 하나분이라 `samples × timeout`을 담지 못했고, 뒤 샘플은 observation이 아니라 window artifact로 집계됐다. window = `timeout × samples`, deadline = `timeout × max(attempts, samples) + grace`. RED-first로 gate를 쓴 뒤 mutation probe(창을 `timeout` 하나로 되돌리기)에서 RED가 재현되는지 확인했다. control: samples 없는 대상은 `timeout` 하나로 유지.
- [x] #1 HTTP 단계별 시간 분해 — observer가 DNS/connect/TLS/TTFB를 관측하고 `backend/diagnostic/http.go:175`이 block을 result에 기록한다. gate는 live HTTP run으로 block이 실제로 생산됨을 확인한다(`TestHTTPCheckerLivePathRecordsConnectAndTTFBPhases`): producer 기록을 지우면 RED로 실패함을 mutation probe로 재현했다. reused connection에서 connect phase가 없고 cached lookup에서 DNS phase가 없음도 별도 gate로 pin된다 — absence는 zero 값이 아니라 생략이다. 남은 것은 phase별 finding code 분리다(현재 단일 slow-server rule, `analysis.go:133`).
- [ ] #2 해석된 IP별 연결 결과 — target 개수 validation만 있고 IP 단위 traversing rule 없음(`runner.go:256`). D-3과 겹침.
- [ ] #4 이름 vs IP 교차 판정 — rule 없음.
- [ ] #5 traceroute probe choice — external binary 경로(`RunTraceCommand`)만; non-root UID raw socket 권한 확인 선행 필요.
- [ ] #6 TLS chain vs trust — `FindingTLSUntrusted`은 있으나 chain-completeness 구분 없음. AIA 조회는 추가 external request → SSRF dial policy 통과 필수.
- [ ] #7 mail depth — greeting observation만(`analysis.go:523`).
- [ ] #8 보고서 diff — `common-prefix.js:138`은 IP prefix grouping만, report diff 없음.
- [ ] #9 anonymized export — 구현 없음.
- [ ] #10 captive portal / tamper — rule 없음.

순서 권고 그대로: M-3 상세 타입화 → contract change bundle → 기능 #1/#2/#3. 진행 판단은 사용자 결정.
