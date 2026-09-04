# CheckNetwork 전체 구현 계획

## 1. 목표

네트워크트러블슈팅회사의 미션에 맞춰 기본 통신, DNS, TCP 연결, 해외망, HTTP 서비스 상태를 단일 요청으로 검사하고 사람이 읽을 수 있는 요약과 기계가 처리할 수 있는 JSON 리포트를 제공한다.

## 2. 기술 선정

- 백엔드: Go 1.22. 단일 실행 파일, 낮은 런타임 의존성, 강한 동시성·네트워크 표준 라이브러리, 교차 컴파일을 활용한다.
- 프론트엔드: 표준 HTML/CSS/JavaScript. 백엔드와 별도 배포하고 다른 프론트엔드가 동일 API를 사용할 수 있게 한다.
- 프로토콜: 버전이 명시된 REST/JSON API (`/api/v1`).
- 테스트: Go 표준 `testing`, 로컬 가짜 DNS/TCP/HTTP 서버로 외부망에 의존하지 않는 회귀 테스트를 구성한다.

## 3. 단계와 완료 기준

1. 기반 설계: 진단 요청·결과 모델과 플러그형 검사 인터페이스를 정의한다.
2. 진단 엔진: DNS, TCP, HTTP 검사를 컨텍스트 타임아웃과 함께 병렬 실행한다.
3. 리포팅: 전체 상태, 소요 시간, 개별 측정값과 오류 코드를 일관된 JSON으로 반환한다.
4. API: 입력 검증, 요청 크기 제한, CORS 허용 목록, 정상 종료, 헬스체크를 제공한다.
5. 웹: 사용자 지정 검사 대상, 결과 요약, 상세 결과와 JSON 내보내기를 제공한다.
6. 품질: 단위·통합·회귀·race 테스트가 가능하고 주요 실패 경로를 검증한다.
7. 배포: 로컬 실행, Docker, Windows/macOS/Linux 빌드 방법을 문서화한다.

## 4. 범위 기준

초기 버전은 관리자 권한 없이 동작하는 DNS, TCP, HTTP 기반 검사를 우선한다. ICMP ping과 OS별 traceroute는 권한·플랫폼 차이가 크므로 코어 인터페이스를 해치지 않는 후속 어댑터로 추가한다. 해외망은 운영자가 지정한 해외 리전 엔드포인트에 대한 TCP/HTTP 지연과 성공 여부로 측정한다.

## 5. 리뷰 포인트

- API 응답이 차후 모바일·데스크톱 클라이언트에도 충분한가
- 검사 대상과 타임아웃 상한이 운영 환경에 적합한가
- 공개 배포 시 SSRF 방지 정책을 어떤 네트워크 대역 기준으로 적용할 것인가
- 리포트 영구 보관과 공유 기능을 다음 릴리스에 포함할 것인가

## 6. 현재 단계: Stage 8 truthful diagnostics와 client/release reliability

초기 단계 목록은 당시 범위와 의사결정 기록으로 유지한다. Stage 8 base parent는 `36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243`이다. Task 1~15 구현과 현재 documentation source/link/stale/secret/diff focused gates 및 모든 task-level review는 blocker 0 / major 0이다. Current stable source cardinality는 exact API errors 16/structural mutations 85, findings 23/result shapes 31/expanded result matrix 136, presentation `6/10/21/23/33`, Web 260(456 semantic mutations including 13 generic cancelled-detail rejections), Android debug/release 각각 direct-child XML 297 + variant-contract canaries 2, archive validators API 28 + Web 25 = 53, exact real release-gate fake cases 12다. Production exact gate는 umask 077/027/000 세 pass와 canonical Docker inventory equality를 요구한다. 이 documentation edit 뒤 final pre-manifest clean-environment `make ci`는 아직 pending이므로 prior current-byte CI/count evidence는 final candidate evidence가 아니며 **invalid**다. Repository root의 ignored `checknetwork-api` binary는 candidate manifest/검증에서 제외되어 있으나 비파괴 요청 때문에 제거하지 않고 retained 상태다. New external canonical manifest, manifest-derived isolated temporary direct-child commit/release, five-way precommit review, explicit staging, Stage 8 commit, exact-SHA postcommit gates와 main integration은 모두 pending이며 어떤 final claim도 하지 않는다. 이전 임시 manifest version/record count/hash는 current evidence가 아니다.

- [x] residual checker shutdown을 atomic bounded `active/stuck/remaining` + nonzero exit로 종료
- [x] 13-kind omitted/0 및 HTTP(S)-only explicit 100..599 final-response `expected_status`
- [x] exact 16-row API error registry/Retry-After와 Android final local classification/UI semantics (`server_draining` 포함)
- [x] zero-write bounded server-first greeting grammar, `server_greeting` scope와 no auth/command/STARTTLS/mailbox/e2e UI limitation
- [x] typed TLS expired/not-yet-valid/hostname/untrusted/other matrix와 validity fields
- [x] traceroute execution/reachability/path-quality independent facts/analysis; failed-attempt topology는 full raw-only이고 compact/Geo aggregate에서 제외, full timeout/command-failure는 별도 completed representative topology를 optional 유지, generic cancelled는 details 금지
- [x] producer-generated exact 23 findings/31 result shapes/136 expanded semantic matrix와 eight traceroute witness fixtures를 Web/Android가 공통 소비; healthy는 empty error_code만 허용하고 contradictory known tuple/details는 whole-report pre-publication rejection + fixed local invalid response; 별도 presentation `6/10/21/23/33` fixture와 six-key privacy-safe registry
- [x] strict compact-v1 closure, canonical non-null empties/ASN absence, exact Geo 4,096/+1 cross-client policy
- [x] Web target admission `MAX_TARGETS=20`, hidden retained views를 포함한 global `MAX_DOCUMENT_ELEMENTS=1200`, exact-fit/cap+1 atomic boundary
- [x] Web maximum-report lazy DOM 699 initial/732 peak/1,200 guard
- [x] Android direct-XML variant-canary collection + exact bounded non-evidence `binary/`, detached single-swap render/rollback, process singleton bounded raw share/cleanup/ownership
- [x] canonical API+Web release verifier: double-build-equal tagless offline archives, bounded/trusted-base projection, safe API extraction/publication, exact two container/network daemon roles per artifact family, pinned-base mount-only compatibility smoke, response-loss/signal-safe ID recovery, six API/five Web output fields
- [x] Stage 8 implementation docs와 executable source/link/stale/secret/diff focused verification
- [x] latest parent focused gates와 Task 1~15 task-level reviews (blocker 0 / major 0)
- [x] current cardinality inventory: API errors 16/mutations 85, findings 23/result shapes 31/expanded matrix 136, presentation `6/10/21/23/33`, Web 260 with 456 semantic mutations including 13 generic cancelled-detail rejections, Android debug/release each 297 + 2 variant canaries, archive validators 53, exact real release-gate fake cases 12
- [ ] documentation edit 뒤 final clean-environment `make ci`; ignored root `checknetwork-api` binary는 candidate에서 제외하되 비파괴 요청으로 retained
- [ ] new manifest-derived isolated temporary direct-child commit/release verification and five-way independent precommit review
- [ ] explicit manifest staging, one Stage 8 commit, exact-SHA `make ci-clean-archive`/closure review, main fast-forward

구현된 defect closure와 future capability를 구분한다. Web live capability-driven form, Android interactive topology/Geo UI, history/baseline, packet loss/jitter/throughput/MTU/local-link/Wi-Fi/VPN/proxy facts, multi-vantage/cross-check ranking, metrics exporter는 후속 capability다.

다음 manual/capability acceptance는 실행 전까지 완료로 표시하지 않는다.

- [ ] physical Chrome/Firefox/Safari 320/375/400 px viewport, keyboard 및 screen reader
- [ ] Android emulator/physical device, TalkBack/Switch Access, OEM share sheet, process death와 stock `FileProvider`/already-open descriptor
- [ ] TLS reverse proxy HTTP/2 deadline/cancellation
- [ ] production registry push/signature/SBOM 및 hosted CI

