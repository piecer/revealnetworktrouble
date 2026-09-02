# CheckNetwork

Web UI는 진단과 토폴로지를 독립 request lane으로 실행하며 stale-response를 차단한다. topology는 compact-v1 응답(nodes≤500, links≤1,000, body<1 MiB)을 사용하고 active view를 100-element chunk로 점진 렌더링하며 document 1,200-element 경계를 검사한다. Bearer credential은 API base별 현재 탭에만 유지되고 보고서/export에는 포함되지 않는다.

CheckNetwork는 기본 통신, DNS, 네트워크 경로, 해외망, 특정 서비스 상태를 한 번에 검사하고 구조화된 리포트를 만드는 멀티플랫폼 애플리케이션입니다.

신규 리포트는 단순 PASS/FAIL과 함께 원인 후보, 그 판단을 지지하는 관측 증거, 안전한 다음 확인 단계와 분석 한계를 구조화된 `analysis`로 제공한다. 분석 confidence는 장애 확률이 아니며 현재 수집한 telemetry의 근거 수준을 뜻한다.

백엔드는 Go로 작성된 독립 REST API이며, 진단 엔진은 다른 CLI·데스크톱·모바일 프론트엔드에서도 재사용할 수 있습니다. 기본 프론트엔드는 빌드 도구 없이 배포 가능한 웹 앱입니다.

## 빠른 시작

```bash
go run ./cmd/checknetwork-api
```

다른 터미널에서:

```bash
curl http://localhost:8080/api/v1/health
curl -X POST http://localhost:8080/api/v1/reports \
  -H 'Content-Type: application/json' \
  -d '{"targets":[{"kind":"dns","address":"example.com"},{"kind":"https","address":"https://example.com"},{"kind":"ssh","address":"example.com"}]}'
```

지원 검사는 DNS, 임의 TCP, HTTP, HTTPS, traceroute와 SSH·SMTP·IMAP·POP3 계열 서비스다. 웹의 `경로 토폴로지`는 목적지별 1~10회 경로를 canonical node와 directed link로 합치고 result/attempt 사이에 공정한 complete-prefix를 표시한다. 서버 제한과 화면 제한, 실제/표시/생략 수를 구분하며 목적지 필터, roving keyboard focus와 전체 화면을 지원한다. `IP 라벨`은 CSV/JSON import와 직접 편집을 지원하되 1 MiB/500 records/100-row page 경계를 적용한다. HTTPS와 암시적 TLS 서비스는 TLS 버전, 암호 스위트, 인증서 제목과 만료 시각도 보고한다.

Android 앱도 `/checks` capability와 13종 검사, HTTPS-only public credential, request owner/cancel/recreation, 설명 가능한 analysis와 compact topology 요약을 사용한다. 기본 공유는 redacted human summary이고 원본 JSON은 경고 확인 후 private cache의 bounded content URI로만 공유한다. Interactive Android graph와 실기기 TalkBack 검증은 아직 별도 acceptance 항목이다.

공인 IP 홉에는 GeoIP 위치와 ASN/사업자 정보를 보강한다. `Geo 경로 지도`는 같은 bounded selection을 외부 tile/credential 없는 Canvas 경로 개요와 접근 가능한 위치 목록으로 표시한다. 위치는 실제 장비 소재지가 아닌 IP 등록 정보 기반 추정치다.

웹 UI는 정적 파일 서버로 별도 실행합니다.

```bash
cd frontend
python3 -m http.server 3000
```

직접 실행 시 브라우저에서 `http://localhost:3000`을 열고 API 주소에 `http://localhost:8080`을 입력합니다. Docker Compose에서는 API 주소가 `http://localhost:9090`입니다.

## 검증

```bash
npm --prefix frontend ci
make test
make web-test-syntax
make test-race
make vet
GOFLAGS=-buildvcs=false make build
make android-wrapper-verify
make android-env
make android-test
make android-lint
make android-assemble
# Go/Web와 Android 전체 gate
make ci
```

자세한 내용은 [구현 계획](docs/PLAN.md), [아키텍처](docs/ARCHITECTURE.md), [API 명세](docs/API.md), [테스트 기준](docs/TESTING.md)을 참고하세요.
