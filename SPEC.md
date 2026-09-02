# CheckNetwork 기능 명세

## 1. 목적

CheckNetwork는 기본 통신, DNS, 네트워크 경로, 해외망 및 특정 서비스의 상태를 한 번에 확인하고 운영자가 비교 가능한 형태로 리포팅하는 애플리케이션이다. 이 문서는 CMP-18에서 추가한 다중 trace 분석, 목적지 필터 및 IP 라벨 매핑의 동작 계약을 정의한다.

## 2. 화면과 탐색

상단 메뉴는 `통합 진단`, `경로 토폴로지`, `Geo 경로 지도`, `IP 라벨`의 네 화면을 제공한다.

- 메뉴 클릭은 해당 화면을 즉시 표시하고 URL hash를 동기화한다.
- `#diagnostics`, `#topology`, `#geo-map`, `#ip-labels` 직접 접근과 브라우저 앞·뒤 이동을 지원한다.
- 알 수 없는 hash는 `통합 진단`으로 안전하게 복귀한다.
- 활성 메뉴에는 시각적 상태와 `aria-current="page"`를 함께 제공한다.

## 3. 다중 trace 수집

- 사용자는 한 줄 또는 쉼표로 구분한 목적지를 최대 20개 입력할 수 있다.
- 중복 목적지는 한 번만 요청한다.
- 목적지별 반복 횟수는 1~10회이며 기본값은 5회다.
- API에는 각 목적지를 `traceroute` target으로 전달한다.
- 수집된 여러 실행은 주소가 같은 응답 노드를 하나로 합쳐 공통 구간과 분기 구간을 표현한다.
- 식별할 수 없는 연속 무응답 홉은 하나의 범위 노드로 접되 원래 홉 범위와 개수를 보존한다.
- 동일 홉 계층의 IPv4 `/24`, IPv6 `/48`, 호스트 도메인 형제 노드는 집계 노드로 표현할 수 있다.

## 4. 목적지 선택과 비교

- 분석 완료 직후 모든 목적지가 선택된다.
- 목적지별 toggle과 `전체 선택`, `전체 해제`를 제공한다.
- 선택 변경 시 서버 재요청 없이 요약 지표, 노드, 링크, 범례를 즉시 다시 계산한다.
- 경로 색상은 원래 결과 순서에 고정되어 필터 전후에 바뀌지 않는다.
- 아무 목적지도 선택하지 않으면 빈 상태 안내를 표시하고 지표를 0으로 표시한다.
- `응답없음 노드` toggle을 해제하면 서버 재요청 없이 무응답 범위 노드를 숨기고 인접한 응답 노드 사이의 링크를 다시 연결한다.
- 요약은 선택된 목적지 수, 총 실행, 도달/미도달 수, 최대 홉 단계, 고유 응답 노드를 제공한다.

## 5. 토폴로지 조회

- 같은 주소는 경로가 갈라졌다 합쳐져도 하나의 물리 노드로 표시한다.
- 링크 굵기는 관측 빈도를 나타내며 색상은 경로 소속을 나타낸다.
- 노드는 마우스 hover 또는 키보드 focus로 주소, 홉, 관측 횟수, 평균 지연, 관련 목적지를 조회할 수 있다.
- 노드는 drag로 재배치할 수 있으며 캔버스 범위를 벗어나지 않는다.
- 전체 화면 조회를 지원한다.
- 집계 노드 tooltip에는 포함된 개별 노드 또는 해당 IP 라벨을 표시한다.

## 6. IP 라벨 매핑

각 매핑은 다음 필드를 가진다.

| 필드 | 필수 | 설명 |
| --- | --- | --- |
| `ip` | 예 | 유효한 IPv4 또는 IPv6 주소이며 대소문자 구분 없이 key로 사용 |
| `label` | 예 | 토폴로지에 우선 표시할 운영자 식별 이름 |
| `note` | 아니오 | 위치, 장비 용도 등 tooltip 보조 설명 |

- 직접 추가, 테이블 수정 및 개별 삭제를 지원한다.
- 경로 토폴로지 화면에서는 IP를 직접 입력하거나 개별 IP 노드를 선택해 라벨과 설명을 바로 추가·수정·삭제할 수 있다.
- 집계 노드는 여러 IP를 포함하므로 하나의 IP 편집 대상으로 취급하지 않는다.
- 동일 IP를 다시 입력하거나 import하면 최신 값으로 갱신한다.
- 매핑은 `checknetwork.ip-labels.v1` key의 브라우저 local storage에 저장한다.
- IPv4 `/24`, IPv6 `/48`, 동일 도메인 형제의 `N` 집계 노드는 맵에서 직접 선택해 별도 라벨과 설명을 지정할 수 있다.
- 집계 라벨은 홉이나 렌더링 순서가 아닌 집계 기준 key로 식별하고 `checknetwork.aggregate-labels.v1`에 저장한다. 개별 IP 라벨 데이터와 충돌하지 않는다.
- 저장소를 사용할 수 없는 환경에서도 현재 페이지의 메모리 내 편집은 유지한다.
- 라벨 변경은 이미 렌더링된 통합 진단 및 standalone topology에 즉시 반영한다.
- 라벨이 있는 노드는 라벨을 대표 이름으로, 원본 IP를 보조 정보로 표시한다.

## 7. Import 형식

CSV는 header 포함 또는 미포함 형식을 지원한다.

```csv
ip,label,note
203.0.113.10,서울 IDC 경계 라우터,본사 회선
2001:db8::1,IPv6 게이트웨이,
```

Header 별칭은 `address`, `name`, `description`이다. 따옴표 안의 쉼표와 이중 따옴표 escape를 처리한다.

JSON은 배열 또는 IP-key 객체를 지원한다.

```json
[
  {"ip":"203.0.113.10","label":"서울 IDC 경계 라우터","note":"본사 회선"}
]
```

```json
{
  "203.0.113.10":"서울 IDC 경계 라우터",
  "2001:db8::1":{"label":"IPv6 게이트웨이","note":"해외망"}
}
```

- 유효한 행은 저장하고 잘못된 IP 또는 빈 라벨 행은 제외한다.
- 결과 메시지에 저장 개수와 제외 개수를 표시한다.
- JSON 문법 오류 등 파일 전체를 읽을 수 없는 경우 기존 데이터는 변경하지 않고 오류를 표시한다.

## 8. 데이터와 보안 경계

- IP 라벨 데이터는 현재 브라우저에만 저장되며 서버나 다른 사용자와 자동 동기화되지 않는다.
- 가져온 값과 API 응답은 HTML escape 후 화면에 삽입한다.
- topology JSON 다운로드는 원본 진단 결과를 보존하며 UI의 필터 상태나 로컬 라벨을 원본 응답에 덮어쓰지 않는다.

## 9. 검증 기준

- 세 메뉴 각각의 직접 접근과 클릭 전환이 가능하다.
- `IP 라벨` 선택 시 관리 form과 table이 표시되고 나머지 view는 숨겨진다.
- CSV/JSON 파싱, IPv4/IPv6 검증, 라벨 표시를 자동 테스트한다.
- 목적지 선택 상태가 summary와 topology에 일관되게 반영된다.
- 무응답 노드 toggle을 해제하면 무응답 노드는 제거되고 전후 응답 노드가 연결된다.
- 기존 노드 병합, 네트워크 집계, 무응답 홉 접기, drag 및 전체 화면 기능이 회귀하지 않는다.

## 10. 설명 가능한 자동 분석

- 모든 신규 report는 측정 결과와 별도로 `analysis`를 제공한다.
- 분석은 안정적인 `error_code`와 구조화된 details만 사용하며 backend 오류 message 문구를 규칙 입력으로 파싱하지 않는다.
- 원인 후보, 관측 evidence, 안전한 다음 action, confidence와 coverage를 분리한다.
- confidence는 확률이 아니며 현재 telemetry가 관측하지 못한 packet loss, bandwidth, Wi-Fi/VPN/proxy 상태나 실제 root cause를 단정하지 않는다.
- malformed 또는 이전 버전 details는 panic이나 낙관적 정상 판정 대신 limitation과 `inconclusive`로 표현한다.
- finding/evidence/action ID와 정렬은 같은 report 입력에 대해 결정적이어야 한다.
- TLS downgrade/만료, DNS 실패, endpoint 연결 실패, HTTP status 불일치, 실행 timeout/cancel, traceroute 도달·부분 도달·producer 분류 경로 저하를 회귀 fixture로 검증한다.

## 11. Web 요청 lifecycle과 응답 신뢰 경계

- 통합 진단과 경로 토폴로지는 서로 독립된 request lane이며 `idle`, `loading`, `ready`, `error`, `cancelled` 상태를 가진다.
- 각 요청은 `ownerId`, canonical input signature, `AbortController`로 소유권을 증명한다. 교체된 요청의 늦은 성공·오류·`finally`는 새 요청의 결과, busy 상태, live message 또는 focus를 바꾸지 못한다.
- 새 실행과 측정 입력 변경은 이전 report, Geo/topology 파생 상태, download 가능 상태를 즉시 제거한다. download·Geo·topology는 현재 signature의 `ready` report만 사용한다.
- 명시적 취소, 화면 이탈, 입력 변경, `beforeunload`, client budget timeout에서 진행 중 fetch를 abort한다. 브라우저 abort는 별도 서버 cancel API가 아니며 HTTP request context에 전달되는 범위만 보장한다.
- 응답 body는 한 번만 소비해 JSON을 parse한다. 빈/malformed 성공과 schema 위반은 `invalid_response`; HTML/plain/empty 오류는 원문을 노출하지 않는 status 기반 오류로 정규화한다. 401, 422, 429, 503 및 `Retry-After`를 구분한다.
- Web client는 `Content-Length`와 streaming read 모두에서 응답을 8 MiB로 제한한다. reader cancellation이 실패해도 `response_too_large`가 우선하며 multibyte 문자열도 encoded byte로 계산한다.
- 정규화는 report 전체에 8,192 topology nodes, 16,384 links, 1,000,000 string value characters와 32,768 containers의 누적 예산을 적용한다. 반복되는 JSON key 이름은 string value budget에 중복 과금하지 않으며, 정상 최대 20 traceroutes × 10 attempts × 30 hops와 GeoIP/ASN 및 대표 topology를 수용한다.
- exported normalizer는 plain JSON object/array만 허용하고 custom prototype 및 getter/accessor를 실행하지 않고 거부한다.

## 12. 연결 credential과 분석 UI

- API base는 공용 연결 설정 하나에서 관리한다. Bearer는 명시적으로 활성화하며 normalized API base별 `sessionStorage`에만 저장한다.
- raw token은 URL, input signature, report state, DOM text, download/export, `localStorage`에 포함하지 않는다. 원격 HTTP에는 credential을 보내지 않고 HTTPS를 요구하며 loopback HTTP만 예외다.
- 서버의 structured error가 Bearer를 반사해도 client는 안정적인 HTTP status/code 메시지만 state와 DOM에 저장한다.
- 원본 JSON export는 현재 signature가 소유한 normalized ready report만 저장한다. 사람용 Markdown export는 generic filename을 사용하고 대소문자 변형·겹치는 target·report ID의 target 값을 redaction하며 raw evidence를 제외한다.
- 분석 정보 계층은 compact 상태·관측 위치·coverage header → 원인 후보 → 근거와 안전한 조치 → coverage/한계 → 접힌 원시 결과 순이다. 서버 문자열은 `textContent`로만 삽입하고 severity/status class는 allowlist에서 선택한다.
- `analysis`가 없는 legacy report는 측정 결과를 ready로 유지하되 자동 분석을 미지원/판단 보류로 명시한다.
- 상태 알림은 짧은 live region만 사용한다. 성공은 분석 heading, 오류는 alert, 명시적 취소는 실행 button, 초기 deep-link와 hash 탐색은 현재 보이는 화면의 `h2`로 focus를 이동한다.
- topology SVG는 accessible name을 제공하고 IP 라벨 표는 caption과 column scope를 가진다. 파일 import는 숨기지 않은 native file input을 제공한다.
- 320/375/400px에서는 document overflow 대신 table, raw JSON, topology/map 구성요소가 자체 horizontal scroll을 소유한다. double focus ring과 reduced-motion 설정을 제공한다.
- CARTO 설정은 배포 환경 `CARTO_BASE_MAP`이 현재 탭 입력보다 우선하며 화면 설명과 runtime 선택 순서가 일치한다.
