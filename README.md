# CheckNetwork

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

지원 검사는 DNS, 임의 TCP, HTTP, HTTPS, traceroute와 SSH·SMTP·IMAP·POP3 계열 서비스다. traceroute 결과는 홉과 구간을 topology로 구성하고 응답 없음, 50ms 이상 지연 증가, 대상 도달 실패를 구분한다. 웹의 `경로 토폴로지` 화면은 목적지별로 기본 5회(1~10회 설정 가능) 경로를 수집해 일시적 실패와 대체 경로를 하나의 맵으로 합친다. 결과 위의 목적지 토글로 비교할 trace만 선별할 수 있으며 선택에 맞춰 요약 지표와 맵도 다시 계산된다. `응답없음 노드` 토글을 끄면 무응답 범위 노드를 즉시 숨기고 전후 응답 노드를 직접 연결한다. 동일 주소는 하나의 노드로 통합하고 같은 홉 계층의 공통 IPv4 /24, IPv6 /48 또는 호스트 도메인은 집계 그룹으로 접는다. 연속된 무응답 홉도 하나의 범위 노드로 접되 원래 홉 범위와 개수를 툴팁에 보존한다. 최대 HOP 단계와 고유 응답 NODE를 별도 지표로 표시하며, 노드를 드래그해 배치를 조정하거나 전체 화면에서 경로를 검토할 수 있다. 노드에 마우스를 올리거나 키보드로 포커스하면 관측 상세가 표시된다. 개별 IP 노드뿐 아니라 `N` 집계 노드도 맵에서 선택해 라벨과 설명을 지정할 수 있고 즉시 topology에 반영된다. `IP 라벨` 화면에서는 개별 IP 매핑을 직접 추가하거나 `ip,label,note` CSV 및 JSON 데이터를 가져와 테이블로 수정·삭제할 수 있다. 매핑은 브라우저 로컬 저장소에 보관된다. HTTPS와 암시적 TLS 메일 서비스는 연결 여부뿐 아니라 TLS 버전, 암호 스위트, 인증서 제목과 만료 시각도 보고한다.

공인 IP 홉에는 GeoIP 위치와 ASN/사업자 정보를 보강한다. 별도 `Geo 경로 지도` 메뉴는 위치가 식별된 홉과 traceroute 관측 순서를 확대·이동 가능한 상세 지도에 표시한다. 연결선 중간의 화살표로 다음 홉 방향을 구분하고, 경로 전체가 보이도록 자동으로 화면을 맞추며 홉 마커에서 지역·좌표·ASN을 확인하거나 전체 화면으로 검토할 수 있다. 위치는 실제 장비 소재지가 아닌 IP 등록 정보 기반 추정치다.

Geo 지도는 `CARTO_BASE_MAP` 환경 변수로 CARTO Basemaps API key 또는 HTTPS 타일 URL을 받을 수 있다. Docker Compose는 이 값을 웹 런타임 설정으로 전달하며, 미설정 시 지도 화면에서 현재 탭에만 적용되는 값을 직접 입력할 수 있다.

웹 UI는 정적 파일 서버로 별도 실행합니다.

```bash
cd frontend
python3 -m http.server 3000
```

직접 실행 시 브라우저에서 `http://localhost:3000`을 열고 API 주소에 `http://localhost:8080`을 입력합니다. Docker Compose에서는 API 주소가 `http://localhost:9090`입니다.

## 검증

```bash
go test ./...
go vet ./...
```

자세한 내용은 [구현 계획](docs/PLAN.md), [아키텍처](docs/ARCHITECTURE.md), [API 명세](docs/API.md), [테스트 기준](docs/TESTING.md)을 참고하세요.
