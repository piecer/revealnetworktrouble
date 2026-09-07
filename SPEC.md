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
- Stage 8 회귀의 원인은 bounded semantic renderer가 node/link/route마다 단일 텍스트 요소만 만들고 실제 visual layer를 만들지 않은 것이다. 데이터와 접근 가능한 내용은 남았지만 그래프가 보이지 않았다.
- 기본 `2D 그래프`와 선택 가능한 `3D 그래프`는 같은 검증된 topology facts를 Canvas에 투영한다. 전환은 재요청 없이 이루어지고 report, analysis, diagnosis를 변경하지 않는다.
- Pointer drag/wheel, 키보드 화살표·`+`/`-`·`Home`, reset과 fullscreen을 지원한다. Continuous animation은 없고 interaction 또는 resize에서만 redraw한다.
- Canvas와 동기화된 bounded semantic inspector는 키보드·screen-reader 탐색, 라벨 편집과 Canvas context 실패 fallback을 제공한다.
  Graph-first 화면은 Canvas를 먼저 표시하고 노드·링크·경로 카드는 기본 닫힌 native `details`/`summary` 안에 둔다. Inspector shell은 정확히 DOM 2개로 계획·chunk 비용에 포함하며 펼침은 DOM을 추가하지 않는다. 초기·mode 전환·reset은 실제 camera-plane node bounds에 32px padding을 적용해 fit하고 singleton은 정상 node radius를 유지한다. Graph 실패는 고정된 로컬 안내로 상세 보기 펼침을 권하며 exception prose를 반사하지 않는다.
- 시각화 입력 상한은 nodes 500, links 1,000, routes 1,000이고 DPR은 최대 4다. Global document 상한은 DOM 1,200이며 현재 최대 fixture 측정값은 1,152이다.

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
- shutdown은 checker supervisor의 admission을 닫고 shared cancellation을 보낸 뒤 기존 346초 end-to-end deadline 안에서만 기다린다. deadline 시점의 `active`, `stuck`, `remaining`은 하나의 lock 아래 atomic snapshot이며 각각 `0..capacity`(configured hard max 1,024)의 bounded count이고 현재 구현에서 `remaining=active`다. 셋 중 하나라도 nonzero이거나 HTTP shutdown 자체가 실패하면 fixed ERROR terminal record와 exit code 1을 반환한다. “shutdown completed” INFO를 남기지 않고 deadline을 연장하지 않는다. worker가 뒤늦게 실제 반환하면 live accounting은 0으로 수렴하지만 이미 반환한 nonzero process result는 바뀌지 않는다.
- 모든 13 kind는 `expected_status` omitted/0을 허용한다. nonzero `100..599`는 HTTP(S)에만 유효하고 final redirect response status와 비교한다. HTTP(S) omitted/0의 effective default는 200이다.
- service checker는 client write 없이 exact bounded greeting만 읽는다: aggregate 4,096 bytes, 8 CRLF lines, line 512 bytes, SSH identification line 255 bytes. 성공은 `verification_scope=server_greeting`; 실패는 `degraded/service_greeting_unverified`와 details absence다. SMTP는 bounded `220-` continuation 후 `220 `, IMAP은 `* OK`/`* PREAUTH`, POP3는 `+OK`, SSH는 pre-banner 뒤 `SSH-2.0-` grammar만 수용한다.
- TLS verification failure는 typed cause 순서로 hostname mismatch, unknown authority, expired/not-yet-valid, other handshake를 닫는다. expired와 not-yet-valid만 exact UTC RFC3339 `certificate_not_before`/`certificate_not_after`를 가지며 문자열 prose를 분류에 사용하지 않는다.
- traceroute execution 성공 여부와 completed path의 reachability/quality는 독립 집계한다. execution-failed attempt의 parse 가능한 topology는 full raw `details.attempts`에서만 보존할 수 있고 compact route/node/link/stat 및 Geo aggregate에는 들어가지 않는다. 별도의 completed route가 있으면 full `timeout`/command-failure result가 optional representative `details.topology`를 유지할 수 있다. Generic runner-owned `cancelled`는 13 kind 모두 details를 금지한다. completed reached+unreached는 execution finding과 함께 partial reachability를 유지한다.

## 15. Closed producer/consumer와 presentation 계약

- `testdata/finding-contract.json`은 실제 `ProducerFindingContract`가 생성한 newline-terminated `finding-contract-v1` fixture다. producer의 exact 23 finding codes, 31 result shapes, expanded 136-row `(kind,status,error_code,details)` semantic compatibility matrix, presentation key와 유일한 verification scope를 닫고 Web/Android가 같은 bytes를 소비한다. Producer가 이 matrix를 소유하며 두 client의 독립 Cartesian allowlist는 허용하지 않는다. Web/Android 모두 timeout, command-failure, failed-reached-parse, mixed-completed-failed의 full/compact 조합인 exact eight producer traceroute witness fixtures를 소비한다. `healthy`는 `error_code` absent/empty만 허용하고, 알려진 status/code/details라도 matrix와 모순이면 publication 전에 whole report를 atomic 거부해 fixed local `invalid_response`/`INVALID_RESPONSE`로 표시하며 server prose를 반사하지 않는다.
- `testdata/presentation-contract.json`은 실제 checker recipe→`Runner`/`Analyze` 결과로 생성한 `presentation-contract-v1` fixture다. exact field set은 `schema`, `semantic_keys`, `evidence_signals`, `coverage_signals`, `action_relationships`, `scenarios`; 현재 cardinality는 6 semantic keys, 10 evidence signals, 21 coverage signals, 23 action relationships, 33 scenarios다. fixture는 finding/evidence/action references와 producer-reachable coverage를 검증하며 hand-authored parallel truth가 아니다.
- Web/Android presentation registry는 producer 23 codes에 exhaustive하며 unknown code/signal/relationship은 whole report/presentation을 fail closed한다. 화면과 human export는 순서가 고정된 `cause`, `supporting_evidence`, `expectation`, `evidence_directness`, `coverage_limitation`, `next_action`만 사용한다. server title/summary/action/address/provider/error/panic prose, credential, raw target/IP/URL/report ID는 safe presentation에 반사하지 않고 allowlisted typed status/code/count/timestamp만 사용한다.
- greeting 성공의 exact 표시 문장은 `Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.`이다. protocol auth/command/STARTTLS/mailbox/e2e를 검증했다고 표시하지 않는다.
- `compact-v1`의 모든 구조 object는 exact allowed field set을 사용한다. required arrays/objects는 non-null이고 zero cardinality에서도 empty array를 보존한다. optional은 absent 또는 valid value만 허용하며 null/unknown/accessor/inherited Web property는 거부한다. empty ASN object는 금지하고 ASN/Geo 없음은 field absence로 표현한다.
- Geo/ASN bundle은 Go JSON encoder와 동일한 UTF-8/escape/number byte 계산으로 exactly 4,096 bytes까지 atomic 수용하고 4,097 bytes 전체를 거부한다. exact-boundary producer fixture, +1 mutation, ASN-absence fixture를 Web/Android가 같은 결과로 처리한다.
- API error producer는 exact 16-row `(key,status,code,retryable,retry_after,message)` registry와 85-case producer-derived structural mutation corpus(35 fixed shape/encoding cases + row당 message/code/status 3건 + trailing 2건), retry-after cases fixture를 단일 source로 사용한다. Finding fixture cardinality는 exact 23 findings/31 result shapes/136 expanded result-matrix rows이고 presentation cardinality는 별도의 exact 6 semantic keys/10 evidence signals/21 coverage signals/23 action relationships/33 scenarios다. Android `ApiError` parser는 입력 body를 최대 64 Ki UTF-16 code units로 제한하고 depth≤2, 전체 properties≤3, tokens≤10, field name/value≤128 code units인 exact closed `{"error":{"code","message"}}` shape만 수용한다. Duplicate/unknown/missing/null/wrong-type/trailing content와 malformed UTF-16은 모두 reject한다. Android final classifier `ApiError`/`ApiErrorPresentation`은 정확한 `(status,code,message)`와 retry 정책만 local typed UI로 변환하며 Web과 Android 모두 noncanonical message를 `invalid_server_response`로 fail closed한다. Exact `(status,code,message)` tuple만 typed error로 유지하고 registry의 fixed local message/retryable/UI focus를 사용한다. unknown/mismatched pair와 malformed/HTML/plain/empty body는 원래 HTTP status만 보존한 `invalid_server_response`, exact local message `The server returned an invalid error response.`, `retryable=false`, `retryAt` absent로 닫으며 server message/code를 반사하지 않는다. Report/Capabilities transport는 disconnect 전에 모든 `Retry-After` field value를 모은다. 허용 row에서 정확히 하나의 ASCII decimal `1..3600`일 때만 `retryAt=now+seconds`; absent/malformed/leading-zero/signed/whitespace/date/overflow/comma/duplicate/inaccessible/disallowed header는 typed body와 registry retryability를 그대로 두고 timestamp만 생략한다. Final UI는 registry retryable row에 retry control을 보이고, 허용된 valid timestamp만 fixed message 다음 줄에 붙이며, `unauthorized`만 visible credential field에 focus하고 나머지 및 invalid response는 error에 focus한다.

## 16. Web/Android publication과 sensitive raw share

- Web target admission은 `MAX_TARGETS=20`이고 hidden retained view를 포함한 current document 전체에 `MAX_DOCUMENT_ELEMENTS=1200`을 적용한다. 20번째 target은 exact-fit이면 허용하지만 21번째 또는 global budget cap+1 candidate는 append 전에 atomic 거부하며 HMR/destroy 뒤 stale controls도 target을 추가하지 못한다. 최대 producer report의 Web initial DOM은 699 elements, 한 lazy finding panel을 연 peak는 732 elements이며 어떤 commit도 같은 global 1,200 bound를 넘지 않는다. 40 finding panel을 동시에 mount하지 않고 한 slot만 교체한다. 1,200을 넘기는 candidate는 typed presentation failure로 atomic 거부되어 private prose를 DOM에 남기지 않는다.
- Android는 `AnalysisPresentation` 전체 hierarchy를 detached 상태에서 만든다. exact current owner/signature/raw/report/generation을 재확인한 뒤 candidate 한 hierarchy를 한 번 swap한다. visibility, ready status, focus, live announcement, share eligibility 단계마다 소유권을 확인하며 실패/stale 발생 시 candidate만 버리거나 현재 exact candidate를 rollback하고 fixed render error로 전환한다. 빈/부분 report와 share-ready 상태를 게시하지 않는다.
- raw share는 Application process-wide singleton `RawShareRuntime`의 single slot이다. queue가 없고 concurrent prepare는 `BUSY`; UTF-8 sizing, scan, write, force, atomic rename, URI validation, revoke/delete는 caller/main thread 밖 single worker에서 수행한다. raw file은 최대 8 MiB, recognized live set은 최대 2 files/16 MiB다.
- startup/replacement/retry/disposal reconciliation은 한 open scan session에서 step당 32 entries와 32 deletes를 처리하고 cursor가 전진하지 않거나 close/delete/revoke/path 검증이 실패하면 `CLEANUP_FAILED`로 fail closed해 새 share admission을 닫는다. owner identity와 operation/listener generation이 detached/recreated Activity로 stale URI/callback을 전달하지 못하게 한다.
- private `cache/shared-reports`만 사용하고 recognized random token filename, no-follow regular-file/parent checks, create-new temp, fsync, same-parent atomic move를 요구한다. non-exported `FileProvider`는 `FLAG_GRANT_READ_URI_PERMISSION`만 부여한다.
- grant lease는 exact 15분 process-observed TTL이다. `onResume` 등 프로세스가 `observe()`했을 때 expiry를 revoke/cleanup한다. process death 뒤 stock provider grant의 실제 만료, 수신자가 이미 연 descriptor, 수신자 복사본 삭제는 앱이 강제할 수 없으며 device acceptance limitation이다.

## 17. 운영 probe, telemetry와 release identity

- outer operational handler는 정확한 raw path의 `GET`/`HEAD /livez`와 `/readyz`만 business auth/rate middleware 밖에서 처리한다. `HEAD` body는 비어 있다. liveness body는 `{"status":"live"}`다. Startup traceroute capability selection은 trusted deployment `PATH`의 exact platform executable을 resolve한 뒤 `127.0.0.1`에서 fixed candidate grammars를 전체 2초/combined 32 KiB로 기능 probe한다. Unix는 common numeric `-n -q 1 -w 2 -m 30`을 먼저, `-n` 없는 compatible grammar를 다음으로 시도한다. 성공한 exact path+grammar는 immutable하게 캐시되어 readiness와 실행이 공유한다. Timeout/overflow/nonzero/unparseable/loopback 미도달을 포함해 전부 실패하면 capability는 nil이고 executable이 있어도 unavailable이다. nil capability에서 runtime PATH lookup이나 grammar fallback은 없다. Client screen/export는 exact fixed `Traceroute capability was unavailable, so no route observation was established.`만 사용하고 server prose/target을 반사하지 않는다. Probe는 임의 target/GeoIP를 조회하지 않으며 readiness는 일시적인 connection/report/body/checker 포화에 flap하지 않는다.
- readiness body는 ready `{"status":"ready"}`, startup/drain/traceroute 부재 시 각각 `{"status":"not_ready","reason":"starting|draining|traceroute_unavailable"}`이며 non-ready는 503이다. drain이 시작되면 `/livez`는 200을 유지하고 `/readyz`는 operational 503이다. Business request는 outer wrapper가 raw body를 쓰지 않고 closed API boundary로 전달하며 **route → public auth → rate limit → server_draining → body decode → report/checker admission** 순서를 지킨다. 따라서 unauthorized/rate-limited request는 해당 canonical error를 유지하고, 그 뒤의 draining request는 fixed `503 server_draining` + canonical `Retry-After`로 닫히며 body read/report/checker/SSRF work는 시작하지 않는다. Compose API healthcheck는 `/readyz`를 사용한다.
- telemetry `outcome`은 진단 결과가 아니라 delivery/lifecycle 결과만 나타낸다. 완전히 전달된 valid report의 `report_finish`만 `report_status`, `analysis_verdict`, `total_results`, `failed_results`, `finding_count`를 추가한다. diagnostic tuple이 없거나 malformed/모순이면 extension만 생략하고 valid base event는 유지한다. raw target/IP/path/query/body, credential, provider/error/panic prose와 ID metric label은 금지한다.
- `VERSION`은 `0.1.0`이다. release build의 `/api/v1/health` exact schema는 `{"status":"ok","version":"0.1.0","revision":"<40-lowercase-hex>"}`이며 startup log와 OCI `org.opencontainers.image.version`/`revision` labels가 같은 값을 사용한다. 개발 `go run`은 `dev` identity를 사용한다. 기본 Compose build args의 exact 개발 sentinel은 `VERSION=0.1.0-dev`, `REVISION=0000000000000000000000000000000000000000`, `SOURCE_DATE_EPOCH=0`이며 API/Web이 동일한 값을 사용한다. zero revision은 개발 전용이고 실제 commit 또는 release provenance를 나타내지 않으며 release에 사용할 수 없다.
- release Dockerfile은 builder/traceroute/runtime image digest, Go 1.22.12 toolchain 확인, Alpine v3.20 `traceroute-2.1.5-r0.apk` URL과 SHA-256을 고정한다. release verifier는 clean exact HEAD와 tracked SemVer VERSION, commit-derived `SOURCE_DATE_EPOCH`, canonical `git archive` bytes를 검증한다. API는 concrete `docker buildx build --no-cache --provenance=false --output=type=docker,dest=...,rewrite-timestamp=true .`로 tagless archive를 두 번 만들고 archive/config/manifest/rootfs/layer/binary/traceroute/Go identity의 equality를 확인한다. Offline validator limits는 archive 512 MiB, members 4,096, member 256 MiB, member total 512 MiB, layers 64, compressed layer 256 MiB, expanded layer 512 MiB, selected-file total 128 MiB다. Exact pinned Alpine layer/diff-ID/history prefix를 layer-member interpretation 전에 trusted base로 확정하고 final selected filesystem을 executable mode `0755`인 `/checknetwork-api`와 `/traceroute` 정확히 두 regular files로 닫는다. traversal, duplicate, unreferenced ambiguity, unsafe link/device/FIFO/socket/whiteout, malformed manifest/config/OCI identity는 fail closed한다. Caller-owned empty non-symlink directory에 no-follow/create-exclusive/fsync/write/readback hash 방식으로 두 파일만 안전 추출한다.
- Web은 exact `linux/amd64`에서 같은 buildx flags로 tagless derived archive를 두 번 만들고 archive/config/manifest/rootfs/asset-manifest equality를 검증한다. Pinned nginx base는 `docker image save` 없이 mode `0700` private generated context와 `COPY` 없는 exact `FROM nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10` Dockerfile만으로 두 번 tagless export한다. Base `SOURCE_DATE_EPOCH=0`은 immutable base보다 이른 canonical epoch로서 base layer/config timestamps를 유지하고 exporter metadata를 고정하며, 두 base bytes도 exact equality를 요구한다. Web offline hard bounds는 archive 512 MiB, outer/layer members 4,096, outer/layer member 256 MiB, **outer regular-member declared-byte total 512 MiB**, layers 64, compressed/expanded layer 256 MiB, final selected-file total 32 MiB다. Outer tar는 member header의 declared size를 stream 순서로 total에 먼저 더해 cap 초과 member를 retain/read하기 전에 거부하므로 compressed outer archive의 aggregate expansion도 같은 512 MiB bound를 우회하지 못한다. 허용 regular member graph는 `manifest.json`, 그 Docker manifest가 정확히 참조하는 config/layers, 그리고 함께 존재할 때만 허용되는 `index.json`/`oci-layout`과 index가 정확히 참조하는 단일 OCI manifest blob뿐이다. Tagless actual Buildx output에는 `repositories`가 필요하지 않아 금지한다. 명시 directory는 이 referenced blob graph의 실제 ancestor(`blobs`, `blobs/sha256`)만 허용하되 tar의 implicit parent directory는 요구하지 않는다. 그 밖의 unreferenced regular, directory, symlink/hardlink/device/FIFO/socket은 base와 derived outer archive 모두 거부한다. Web validator는 supplied base archive가 pinned nginx config digest와 ordered layer digest/diff-ID의 exact contract인지 인증하고 derived rootfs/layer/history prefix와 함께 layer tar traversal 전에 trust boundary를 닫는다. Position만으로 base layer를 분류하지 않는다. 인증된 inherited base에서 실제 필요한 canonical contained-target symlink/hardlink만 허용하고 base device/FIFO/socket 및 모든 selected special은 거부한다. Post-base layer의 symlink/hardlink/device/FIFO/socket은 selected 여부와 무관하게 모두 거부한다. OCI whiteout은 **layer-local marker set**으로 적용한다: root opaque는 lower entries 전체, directory opaque는 lower descendants, ordinary whiteout은 lower target/descendants만 제거하고, marker와 같은 layer에서 생성된 entry는 tar 순서와 무관하게 보존한다. 제거된 canonical selected file은 같은 layer나 이후 layer에서 재생성되지 않으면 final closure가 실패한다. **두 derived API/Web archive 모두 shared daemon에 load, daemon-tag, import, delete 또는 execute하지 않는다.** API daemon-owned role은 borrowed pinned Alpine base에 validated extracted binary/traceroute를 read-only mount하는 `smoke` container와 API network 정확히 두 개이고, Web daemon-owned role도 borrowed pinned nginx base에 canonical files를 read-only mount하는 `smoke` container와 internal network 정확히 두 개다. 이 compatibility smoke는 pinned base와 selected files의 호환성만 증명하며 어느 derived archive의 실행 증거도 아니다. Base image/cache는 borrowed resource로 삭제하지 않는다. 두 verifier 모두 high-entropy name, nonce label, independent owner label, pre-create pending state를 사용하고 normal response 또는 daemon-success/CLI-response-loss로 얻은 immutable ID를 exact name+labels와 재확인한다. Cleanup 성공은 confirmed name absence 또는 exact-owned immutable ID 제거 뒤 confirmed absence뿐이다. Pending 상태는 exact name+labels+ID를 모두 확인한 경우만 삭제하며 mismatch/replacement/caller는 보존한다. Daemon/inspect/remove ambiguity는 cleanup failure이고, publication commit/success output 전 barrier 실패는 exact publication rollback과 nonzero를 강제한다. EXIT는 idempotent하며 ordinary failure는 nonzero를 유지한다. HUP/INT/TERM은 각각 129/130/143이고 cleanup 실패에도 고정된 비식별 diagnostic과 함께 그 status를 유지한다.
- Production exact real gate는 caller umask 077/027/000 세 pass를 모두 실행하고 exact output/identity를 비교한다. private temp/captured stderr는 `0700`/`0600`이고 canonical extraction만 fixed `022`를 사용한다. Web build 전 `nginx.conf`와 seven assets mode `0644`를 확인한다. Exact real gate daemon snapshot은 canonical inspect projection(image ID/tags/digests, container immutable identity/config labels/network attachments, network identity/labels/membership endpoints)이며 uptime/status와 map formatting은 제외한다. Exact fake gate는 12 cases이고 caller replacement/membership/label mutation과 release-label residue는 실패하며 production child failure는 private stderr를 공개하지 않은 fixed diagnostic만 낸다.
- 성공 output은 API `api_archive`, `api_config`, `api_manifest`, `api_rootfs`, `api_binary`, `api_traceroute` 여섯 field와 Web `archive`, `config`, `manifest`, `rootfs`, `asset_manifest` 다섯 field다. Docker CLI/daemon/buildx/base cache 부재는 release gate 실패다. registry push/signing/SBOM은 이 local verifier의 구현 claim이 아니다.

### Rich graph interaction and label policy

The Canvas restores status-colored curved directed edges and halos without adding synthetic topology. Dragging an individual node changes only session-local presentation offsets; attached edges follow while other node coordinates and raw diagnostic facts remain unchanged. Background drag retains pan/rotation. Hover/focus details are bounded inert text. Click/double-click or bracket selection plus Enter uses the existing canonical IP alias/note editor and local persistence, never writes report facts. Mode/view replacement owns and disposes interaction handlers.

Label placement uses at most nine vertical candidates per visible node, conservative bounded rectangles, and saved-alias-first stable projection order. Crowded visual labels may be omitted; full bounded inspector and hover/focus details remain available. No graph nodes are relocated for label placement and no explanatory connectors are invented. A fixed document-counted tooltip is shared by the active topology owner.

## 18. Stage 9 상태와 남은 acceptance

- Stage 9 2D/3D Canvas 구현과 production asset closure는 source에 반영되었고 Compose SemVer regression fix 뒤 현재 Web inventory는 291 tests다. 그러나 final canonical gates와 독립 review, immutable candidate release 및 exact-SHA closure가 남아 있으므로 Stage 9은 완료가 아니라 **implementation pending final gates/review** 상태다.
- Physical Chrome/Firefox/Safari에서 interaction·DPR·320/375/400 px를 확인하고 실제 screen reader로 semantic inspector/fallback을 확인하는 수동 acceptance는 자동 Node/jsdom test가 대신하지 않는다.

기존 Stage 8 release sequencing 기록은 아래 acceptance의 선행 이력으로 유지한다.

- candidate parent는 정확히 `36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243`이다. 최종 docs와 fresh CI 뒤, **temporary commit보다 먼저** repository 밖 `${TMPDIR}/stage8-manifest.jsonl`에 canonical `stage8-manifest-v1`을 freeze/hash한다. header parent와 sorted add/modify/delete(path=unpadded base64 raw bytes, normalized 0644/0755, size/SHA-256)를 검증한 뒤에만 exact base의 isolated clone에서 manifest 그대로 temporary direct-child commit을 만든다. 그 tree fingerprint/parent를 manifest와 비교하고 release/review를 수행한다. manifest 이전 temporary commit, Stage 8 worktree commit, final SHA claim은 허용하지 않는다.
- 최신 parent focused gates와 Task 1~15 task-level reviews는 blocker 0 / major 0이다. Stage 8 historical source cardinality는 findings 23/result shapes 31/expanded result matrix 136, presentation `6/10/21/23/33`, Web 260(456 semantic mutations including 13 generic cancelled-detail rejections), Android debug/release 각각 direct-child XML 297 + variant canaries 2, archive validators API 28 + Web 25 = 53, exact real release-gate fake cases 12다. Documentation edits 뒤 final clean-environment `make ci`는 아직 pending이며 prior current-byte CI evidence는 이 candidate의 final evidence가 아니다. Repository root의 ignored `checknetwork-api` binary는 candidate manifest/검증에서 제외되어 있으나 비파괴 요청 때문에 제거하지 않고 retained 상태다. New manifest/freeze, manifest-derived temporary release, five-way review, explicit staging/commit 및 exact-SHA closure는 모두 pending이고 final claim은 없다. 이전 임시 manifest version, record count 또는 hash는 current evidence가 아니다.
- physical Chrome/Firefox/Safari 320/375/400 px + keyboard/screen reader, Android emulator/physical device + TalkBack/Switch Access/OEM share sheet/process death/open descriptor, TLS reverse proxy HTTP/2, production registry push/signature/SBOM, hosted CI는 실제 실행 전까지 pending이다.
