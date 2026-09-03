# CheckNetwork 기능 명세

## 1. 목적

CheckNetwork는 기본 통신, DNS, 네트워크 경로, 해외망 및 특정 서비스의 상태를 한 번에 확인하고 운영자가 비교 가능한 형태로 리포팅하는 애플리케이션이다. 이 문서는 현재 구현된 다중 trace 분석, 목적지 필터, IP 라벨, 런타임 admission, 진단 truth, release identity의 동작 계약을 정의한다.

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
- 식별할 수 없는 홉은 `(result, attempt, hop)` 범위의 독립 unknown node로 유지해 서로 다른 경로의 미응답을 잘못 병합하지 않는다.
- IPv4/IPv6는 canonical 주소, hostname은 lowercase/trailing-dot 제거 identity로 합치며 consecutive canonical self-hop은 route에서 collapse한다.

## 4. 목적지 선택과 비교

- 분석 완료 직후 모든 목적지가 선택된다.
- 목적지별 toggle과 `전체 선택`, `전체 해제`를 제공한다.
- 선택 변경 시 서버 재요청 없이 요약 지표, 노드, 링크, 범례를 즉시 다시 계산한다.
- 경로 색상은 원래 결과 순서에 고정되어 필터 전후에 바뀌지 않는다.
- 아무 목적지도 선택하지 않으면 빈 상태 안내를 표시하고 지표를 0으로 표시한다.
- `응답없음 노드` toggle을 해제하면 각 경로를 첫 unknown 이전의 검증된 directed-link prefix로 제한하고 존재하지 않는 링크는 만들지 않는다.
- 요약은 서버 선택 수와 실제 화면의 displayed/total/omitted 노드·링크·경로를 구분한다.

## 5. 토폴로지 조회

- 같은 주소는 경로가 갈라졌다 합쳐져도 하나의 물리 노드로 표시한다.
- 링크 굵기는 관측 빈도를 나타내며 경로 카드는 원래 result 순서의 색상을 사용한다.
- full topology link의 `latency_delta_ms`는 선택 필드이며 유한한 0~30000 ms 값만 허용한다. 후속 홉 RTT가 감소하면 delta는 0으로 clamp되고 JSON `omitempty`에 따라 생략된다. link의 `latency_ms`는 producer 필드가 아니며 Android parser가 거부한다.
- 노드는 마우스 hover 또는 키보드 focus로 주소, 상태, 홉 범위, 관측 횟수, 평균 지연과 Geo/ASN을 조회할 수 있다.
- 노드 카드는 drag 또는 `Alt+Arrow`로 순서를 재배치할 수 있고 일반 방향키는 roving focus에 사용한다.
- 전체 화면 조회를 지원한다.

## 6. IP 라벨 매핑

각 매핑은 다음 필드를 가진다.

| 필드 | 필수 | 설명 |
| --- | --- | --- |
| `ip` | 예 | 유효한 IPv4 또는 IPv6 주소이며 대소문자 구분 없이 key로 사용 |
| `label` | 예 | 토폴로지에 우선 표시할 운영자 식별 이름 |
| `note` | 아니오 | 위치, 장비 용도 등 tooltip 보조 설명 |

- 직접 추가, 테이블 수정 및 개별 삭제를 지원한다.
- 경로 토폴로지 화면에서는 IP를 직접 입력하거나 개별 IP 노드를 선택해 라벨과 설명을 바로 추가·수정·삭제할 수 있다.
- 동일 IP를 다시 입력하거나 import하면 최신 값으로 갱신한다.
- 매핑은 `checknetwork.ip-labels.v1` key의 브라우저 local storage에 저장한다.

- 저장소를 사용할 수 없는 환경에서도 현재 페이지의 메모리 내 편집은 유지한다.
- 라벨 변경은 이미 렌더링된 통합 진단 및 bounded topology에 즉시 반영한다.
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

- 네 메뉴 각각의 직접 접근과 클릭 전환이 가능하다.
- `IP 라벨` 선택 시 관리 form과 table이 표시되고 나머지 view는 숨겨진다.
- CSV/JSON 파싱, IPv4/IPv6 검증, 라벨 표시를 자동 테스트한다.
- 목적지 선택 상태가 summary와 topology에 일관되게 반영된다.
- 무응답 노드 toggle을 해제하면 첫 unknown 이전의 검증된 경로 prefix만 유지된다.
- compact graph의 canonical node 병합, fair complete-prefix, truncation metadata, 전체 화면과 focus lifecycle이 회귀하지 않는다.

## 10. 설명 가능한 자동 분석

- 모든 신규 report는 측정 결과와 별도로 `analysis`를 제공한다.
- 분석은 안정적인 `error_code`와 구조화된 details만 사용하며 backend 오류 message 문구를 규칙 입력으로 파싱하지 않는다.
- 원인 후보, 관측 evidence, 안전한 다음 action, confidence와 coverage를 분리한다.
- confidence는 확률이 아니며 현재 telemetry가 관측하지 못한 packet loss, bandwidth, Wi-Fi/VPN/proxy 상태나 실제 root cause를 단정하지 않는다.
- malformed 또는 이전 버전 details는 panic이나 낙관적 정상 판정 대신 limitation과 `inconclusive`로 표현한다.
- finding/evidence/action ID와 정렬은 같은 report 입력에 대해 결정적이어야 한다.
- 최대 20 results의 현재 producer 상한은 findings/evidence/actions 각 40, coverage available/missing/provider_failures/limitations 각각 80/60/20/100이다. Web과 Android의 고정 수용 상한은 findings/evidence/actions 각 64와 coverage 각 목록 128이며, 이를 넘는 future response는 report 전체를 거부한다. report 전체의 8 MiB transport와 누적 string/container 제한은 유지한다.
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
- topology는 one-element cards와 roving focus를 사용하고 Geo Canvas는 접근 가능한 위치 목록을 제공한다. IP 라벨 표는 caption과 column scope를 가진다.
- 320/375/400px에서는 document overflow 대신 table, raw JSON, topology/map 구성요소가 자체 horizontal scroll을 소유한다. double focus ring과 reduced-motion 설정을 제공한다.
- topology request는 `topology_mode:"compact"`를 사용한다. compact payload는 nodes 500, links 1,000, response 1 MiB 미만이며 Web은 active view만 mount해 chunk 100/document 1,200 actual elements를 넘지 않는다.
- label import는 1 MiB/500 records/label 256자/note 1024자로 제한하고 한 페이지에 최대 100행을 점진적으로 삽입한다.
- `createApp` instance는 상태·storage·listener를 공유하지 않으며 `destroy()` 이후 async completion이 후속 instance를 변경하지 못한다.

## 13. Android 진단 클라이언트

- Android는 `/api/v1/checks`를 bounded하게 조회한 뒤 server가 광고한 kind/limit/topology mode 안에서만 report를 요청한다.
- 13종 kind를 제공하고 HTTP/HTTPS의 expected status와 traceroute attempts를 kind별로 검증한다. traceroute-only topology 요청은 compact mode를 사용한다.
- capability discovery와 report transport는 사용자 실행마다 하나의 315초 monotonic absolute deadline을 공유한다. discovery는 `min(10s, shared remaining)`, report는 request별 계산 deadline과 shared remaining의 교집합을 connection 생성·socket timeout·scheduler·body I/O·terminal publish 전에 다시 적용한다. 남은 시간이 0이면 새 연결을 열지 않는다.
- report transport는 Content-Length와 stream을 UTF-8 8 MiB로 제한하고 cancel과 exactly-once disconnect를 적용한다.
- 유효한 capability와 local selection의 불일치는 typed `UNSUPPORTED_CAPABILITY` 및 `CHECK_KIND`, `TARGET_COUNT`, `TIMEOUT`, `TRACEROUTE_ATTEMPTS`, `TOPOLOGY_MODE` 중 하나로 반환하고 고정된 UI 문구만 표시한다. malformed capability/report JSON과 schema 위반은 계속 `INVALID_RESPONSE`이며 임의 예외를 capability mismatch로 재분류하지 않는다.
- request state는 owner ID와 canonical signature를 가지며 replacement, input mutation, cancel, recreation과 final destroy 뒤 stale success/error/finally를 게시하지 않는다.
- Android analysis 순서는 status/verdict/coverage → findings → evidence/actions → limitations/provider failures → raw results/compact summary다. confidence를 장애 확률로 표현하지 않는다. human export는 모든 finding code를 exhaustive switch로 매핑하며 `checker_panic`은 `Checker execution failed`, `checker_capacity_unavailable`은 `Checker capacity was unavailable`로 표시하고 traceroute failure로 오표기하지 않는다.
- capability mismatch UI는 reason별 fixed localized copy만 사용한다: unsupported check kind, target count, timeout, traceroute attempts, topology mode. server value나 exception prose는 반사하지 않는다.
- release는 HTTPS만 허용하고 기본 origin은 비어 있다. Bearer는 메모리에서만 사용하며 URL, signature, saved state, preferences, report, log 또는 share에 포함하지 않는다.
- 기본 공유는 식별자·주소·raw observation을 제외한 human summary다. raw JSON은 명시적 경고 확인 후 8 MiB 이하 private-cache file과 non-exported `FileProvider` read grant로만 공유한다.
- form state는 최대 20 targets와 bounded strings만 복원한다. portrait를 강제하지 않으며 320dp/landscape/large font에서 control은 stack되고 touch target은 48dp 이상이다.
- JVM/Robolectric은 schema, lifecycle, resource와 outbound Intent를 검증한다. 실제 TalkBack, Switch Access, OEM share sheet, physical-device network cancellation은 device acceptance 전까지 미검증 limitation이다.

## 14. 런타임 admission과 진단 truth

- accepted connection은 `net/http` handler goroutine 생성 전에 프로세스 전체에서 기본 128개, `CHECKNETWORK_MAX_CONNECTIONS` 설정 hard max 256개로 제한한다. 초과 socket은 즉시 close하고 5 ms interruptible rejection backoff 뒤 accept를 재개한다. 이 경계에서는 HTTP status를 보장하지 않는다.
- authenticated report body decode는 기본 effective report limit, `CHECKNETWORK_MAX_CONCURRENT_BODY_DECODES` 설정 hard max 64개의 독립 slot을 사용한다. 정확한 route/auth/rate 검사 뒤, 선언된 `Content-Length > 1 MiB`를 slot 획득 전에 거부하고, slot은 `MaxBytesReader`를 통한 단일 JSON object+EOF decode 동안만 유지한다. 포화 시 `Retry-After`와 고정 `503 body_decode_capacity_unavailable`을 반환하며 report/checker admission은 시작하지 않는다.
- non-traceroute checker의 terminal arbitration은 checker가 반환한 정확한 timestamp를 parent/supervisor cancellation, report deadline, checker deadline 순으로 판정한다. deadline과 같거나 늦은 healthy 결과는 publish하지 않고 timeout을 반환한다. cleanup cancellation은 판정 뒤에만 실행한다.
- HTTP(S) checker는 header 수신으로 성공하지 않고 최대 32 KiB body observation을 읽은 뒤 latency와 상태를 확정한다. body read의 timeout/cancel은 각각 stable code로 유지하고 그 외 read 오류는 `response_read_failed`이며 healthy가 아니다.

## 15. 운영 probe, telemetry와 release identity

- outer operational handler는 정확한 raw path의 `GET`/`HEAD /livez`와 `/readyz`만 business auth/rate middleware 밖에서 처리한다. `HEAD` body는 비어 있다. liveness body는 `{"status":"live"}`다. readiness는 startup-cached local traceroute executable 상태와 irreversible startup→accepting→draining phase만 사용하고 일시적인 connection/report/body/checker 포화에는 flap하지 않는다.
- readiness body는 ready `{"status":"ready"}`, startup/drain/traceroute 부재 시 각각 `{"status":"not_ready","reason":"starting|draining|traceroute_unavailable"}`이며 non-ready는 503이다. drain이 시작되면 `/livez`는 200을 유지하고 `/readyz`와 모든 business route는 503이며 business handler를 호출하지 않는다. Compose API healthcheck는 `/readyz`를 사용한다.
- telemetry `outcome`은 진단 결과가 아니라 delivery/lifecycle 결과만 나타낸다. 완전히 전달된 valid report의 `report_finish`만 `report_status`, `analysis_verdict`, `total_results`, `failed_results`, `finding_count`를 추가한다. diagnostic tuple이 없거나 malformed/모순이면 extension만 생략하고 valid base event는 유지한다. raw target/IP/path/query/body, credential, provider/error/panic prose와 ID metric label은 금지한다.
- `VERSION`은 `0.1.0`이다. release build의 `/api/v1/health` exact schema는 `{"status":"ok","version":"0.1.0","revision":"<40-lowercase-hex>"}`이며 startup log와 OCI `org.opencontainers.image.version`/`revision` labels가 같은 값을 사용한다. 개발 `go run`과 기본 Compose build args는 명시적으로 `dev` identity다.
- release Dockerfile은 builder/traceroute/runtime image digest, Go 1.22.12 toolchain 확인, Alpine v3.20 `traceroute-2.1.5-r0.apk` URL과 SHA-256을 고정한다. release verifier는 clean exact HEAD와 tracked SemVer VERSION, commit-derived `SOURCE_DATE_EPOCH`, canonical `git archive` bytes를 검증하고 두 no-cache build의 binary/image/rootfs digest 일치를 요구한다. Docker CLI와 daemon 부재는 skip이 아니라 release gate 실패다.
