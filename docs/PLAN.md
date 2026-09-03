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

## 6. 현재 단계: Stage 7 runtime admission과 diagnostic truth

초기 단계 목록은 당시의 범위와 의사결정 기록으로 유지한다. 현재 Stage 7은 다음 runtime defects를 구현하고 focused source/contract test 및 blocker/major 0 독립 review로 검증했다.

- [x] pre-handler connection default 128/hard 256/env admission, immediate-close overflow와 interruptible backoff
- [x] auth/rate 뒤 body-decode effective-report default/hard 64/env admission, declared oversize preflight, decode-only lifetime와 fixed 503
- [x] exact completion timestamp deadline arbitration/no late healthy 및 HTTP bounded body/read-failure truth
- [x] unauthenticated/no-rate exact `/livez`와 `/readyz`, fixed startup/ready/drain states, startup-cached traceroute readiness, Compose `/readyz`
- [x] delivery-only telemetry outcome와 valid complete `report_finish` diagnostic extension; malformed extension omission/base retention/privacy
- [x] `VERSION=0.1.0`, exact health/startup/OCI identity, pinned/checksummed release inputs, canonical archive 및 deterministic release verifier
- [x] full topology `latency_delta_ms` producer/Android parity와 invalid `latency_ms` rejection
- [x] Android exhaustive checker export labels, shared discovery+report 315초 deadline, typed unsupported-capability reasons와 fixed UI

구현된 defect closure와 future capability는 구분한다. Web live capability-driven form, Android topology/Geo UI parity, history/baseline, packet loss/jitter/throughput/MTU/local-link/VPN/proxy facts, multi-vantage/cross-check ranking, metrics exporter, hosted CI/SBOM/signing은 후속 capability 범위이며 Stage 7 runtime defect가 아니다.

다음 acceptance는 현재 자동 완료로 표시하지 않는다.

- [ ] Stage 7 commit 후 clean exact-SHA `make ci-clean-archive` 및 deterministic release verification
- [ ] physical Android device/emulator, TalkBack/Switch Access, OEM share sheet/cache/network cancellation
- [ ] 실제 Chrome/Firefox/Safari viewport/screen-reader 및 TLS reverse-proxy HTTP/2 deadline probe

