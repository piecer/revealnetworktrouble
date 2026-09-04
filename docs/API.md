# REST API v1

## `GET /api/v1/health`

business API의 상태와 immutable build identity를 다음 exact schema로 반환한다. 이 route는 `public` 모드에서 다른 `/api/v1/*`와 동일하게 Bearer 인증과 source rate limit을 사용한다. 개발 실행은 `version`/`revision`이 명시적 `dev`이고 release는 `VERSION`의 `0.1.0`과 정확한 40자리 lowercase Git SHA를 사용한다.

```json
{"status":"ok","version":"0.1.0","revision":"0123456789abcdef0123456789abcdef01234567"}
```

추가 필드는 없다.

## `GET|HEAD /livez`, `GET|HEAD /readyz`

두 operational route는 이 exact path/method에서만 business auth/rate middleware 밖에서 처리되며 rate quota를 소비하지 않는다. `HEAD`는 GET과 같은 status/header 및 빈 body를 반환한다. `/livez`는 process liveness만 나타내며 항상 `200 {"status":"live"}`다. `/readyz`의 고정 응답은 다음뿐이다.

- `200 {"status":"ready"}`
- `503 {"status":"not_ready","reason":"starting"}`
- `503 {"status":"not_ready","reason":"draining"}`
- `503 {"status":"not_ready","reason":"traceroute_unavailable"}`

readiness는 startup에서 trusted deployment `PATH`를 resolve하고 `127.0.0.1`만 대상으로 전체 2초/combined-output 32 KiB의 기능 probe가 선택한 exact local traceroute path+argument grammar와 administrative phase만 사용한다. 실행 파일 부재 또는 모든 fixed grammar의 timeout/overflow/nonzero/unparseable/loopback 미도달은 nil capability와 `traceroute_unavailable`로 fail closed한다. 선택 결과는 immutable하게 캐시되며 실제 traceroute 실행은 정확히 그 grammar를 사용한다. nil capability에서 runtime PATH lookup이나 다른 form fallback은 없다. Client screen/export의 exact fixed copy는 `Traceroute capability was unavailable, so no route observation was established.`이고 server prose/target을 반사하지 않는다. 일시적인 connection/report/body/checker 포화, GeoIP 또는 임의 target을 조회하지 않는다. drain 중 `/livez`는 200이고 `/readyz`는 fixed operational 503을 반환한다. Business route는 outer wrapper가 raw body를 쓰지 않고 API closed boundary로 전달하며 **route → public auth → rate limit → server_draining → body decode → report/checker admission** 순서에서 `503 server_draining`과 canonical `Retry-After`를 반환한다. Unauthorized/rate-limited request는 먼저 해당 canonical error로 닫히고 draining request는 body read, report admission, checker/SSRF work를 시작하지 않는다. 다른 method는 405와 `Allow: GET, HEAD`; suffix/escaped alias는 operational route로 취급하지 않는다.

## `GET /api/v1/checks`

지원하는 검사 종류와 제한을 반환한다.

## `POST /api/v1/reports`

요청 예시:

```json
{
  "timeout_ms": 5000,
  "targets": [
    {"kind": "dns", "address": "example.com"},
    {"kind": "tcp", "address": "1.1.1.1:443"},
    {"kind": "https", "address": "https://example.com", "expected_status": 200},
    {"kind": "traceroute", "address": "example.com", "attempts": 5},
    {"kind": "ssh", "address": "example.com"},
    {"kind": "imaps", "address": "mail.example.com"}
  ]
}
```

`targets`는 1~20개이며 server와 Web `MAX_TARGETS=20` admission이 같은 upper bound를 사용한다. Web은 target append 전에 hidden retained view를 포함한 current document 전체를 global `MAX_DOCUMENT_ELEMENTS=1200`으로 계산해 20번째 exact fit만 허용하고 cap+1 candidate를 atomic 거부한다. `timeout_ms`는 100~30000이며 생략 시 5000이다. `kind`는 `dns`, `tcp`, `http`, `https`, `traceroute`, `ssh`, `smtp`, `submission`, `smtps`, `imap`, `imaps`, `pop3`, `pop3s` 중 하나다. 13개 kind 모두 `expected_status` 생략 또는 JSON 정수 `0`을 허용한다. 명시적 expectation은 `http`/`https`에만 허용되는 정수 `100..599`이며, 다른 kind의 nonzero 값과 HTTP(S)의 99/600 경계 밖 값은 checker/report admission 전에 `422 invalid_request`다. HTTP client가 redirect를 따른 경우 비교 대상 `details.status_code`는 중간 응답이 아닌 **최종 redirect 응답**의 status다. 생략/0은 기존 default expectation 200을 사용한다.

서비스 주소에 포트를 생략하면 표준 포트를 사용하며 `host:port`로 재정의할 수 있다. traceroute 대상은 `attempts`로 1~10회 반복 실행할 수 있으며, 생략하면 기본 5회 실행한다. `timeout_ms`는 각 반복 실행의 제한 시간으로 적용된다. 실행별 결과는 `details.attempts`에, 전체 횟수·도달·실패 집계는 `details.attempts_total`, `details.attempts_reached`, `details.attempts_failed`에 저장된다. 이전 클라이언트 호환을 위한 대표 경로는 `details.topology`에 유지한다. 각 토폴로지는 `nodes`, `links`, `reached`를 제공하며 노드/구간 상태는 `healthy`, `degraded`, `unknown`, `failure`로 구분한다. 공인 IP 노드에는 `public_ip: true`가 표시되고 식별에 성공하면 `geolocation`(도시·지역·국가·위도·경도)과 `asn`(번호·사업자)이 추가된다. GeoIP 조회 실패는 이 필드만 생략하며 진단 상태를 바꾸지 않는다. 응답의 `status`는 `healthy`, `degraded`, `unreachable` 중 하나다.

### Server-first service greeting

SSH/SMTP/submission/SMTPS/IMAP/IMAPS/POP3/POP3S checker는 연결(implicit TLS kind는 검증된 TLS 포함) 뒤 **0 client bytes**를 쓰고 server-first CRLF line만 읽는다. 전체 read는 정확히 최대 4,096 bytes, 최대 8 lines, line당 CRLF 포함 최대 512 bytes다. bare LF, line/aggregate/line-count 초과, EOF/read error, grammar 불일치는 `degraded` + `service_greeting_unverified`이며 details는 생략된다.

- SSH: 최대 8번째 line까지 pre-banner를 허용하되 `SSH-`로 시작한 첫 line만 판정한다. 그 line은 CRLF 포함 최대 255 bytes이고 `SSH-2.0-`로 시작해야 한다.
- SMTP/submission/SMTPS: `220-...` continuation 0~7개 뒤 `220 ...` terminal line이어야 한다. 다른 code/delimiter는 즉시 실패한다.
- IMAP/IMAPS: 첫 line이 `* OK` 또는 `* PREAUTH` 뒤 space 또는 즉시 CRLF delimiter를 가져야 한다.
- POP3/POP3S: 첫 line이 `+OK` 뒤 space 또는 즉시 CRLF delimiter를 가져야 한다.

성공 details에는 정확한 scope `"verification_scope":"server_greeting"`가 있다. 이 결과의 UI/Markdown 의미는 정확히 **“Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.”**이다. greeting 성공은 auth, 명령 transaction, STARTTLS, mailbox 또는 application end-to-end 성공이 아니다.

### Typed TLS failure result

HTTPS와 implicit-TLS service는 frozen verification time에서 Go typed `tls.CertificateVerificationError` cause만 분류한다. hostname cause는 `tls_hostname_mismatch`, unknown authority는 `tls_untrusted`, `x509.Expired`의 verified certificate에 대해 `now >= NotAfter`는 `tls_certificate_expired`, `now < NotBefore`는 `tls_certificate_not_yet_valid`, 나머지 handshake/callback/prose 오류는 `tls_handshake_failed`다. 문자열 오류 문구나 unrelated unverified certificate를 추측에 사용하지 않는다. expired/not-yet-valid 두 shape만 exact details `certificate_not_before`, `certificate_not_after`를 canonical UTC RFC3339 string으로 요구하며 `not_before < not_after`여야 한다. hostname/untrusted/other failure는 details가 없어야 한다.

### Independent traceroute facts

각 attempt의 execution과 path reachability는 독립 facts다. parse 가능한 topology라도 timeout/cancel/command error가 있으면 그 failed attempt topology는 full raw `details.attempts`에서만 보존될 수 있으며 completed reachability, representative selection, compact route/node/link/stat, 경로 품질 및 Geo aggregate에 참여하지 않는다. completed attempts만 `attempts_reached` 또는 `attempts_unreached`에 들어가고, execution failures는 `attempts_execution_failed` 및 `attempts_timed_out`/`attempts_cancelled`에 별도 집계된다. 별도 completed route가 있으면 full `timeout` 또는 command-failure result가 그 route의 optional representative `details.topology`를 유지할 수 있다. Generic runner-owned `cancelled` result는 13 kind 모두 details를 금지한다. 대표 경로는 첫 completed topology이며, 뒤의 첫 completed reached topology만 앞선 completed unreached 대표를 승격한다. result status는 reached=0이면 unreachable, 일부만 reach하거나 completed reached path가 degraded면 degraded, 모두 reach하면 healthy다. unreachable execution code는 전부 timeout→`timeout`, 전부 cancel→`cancelled`, 모두 기타→`traceroute_failed`, 혼합→순서 독립 `traceroute_execution_incomplete`다. 분석은 completed reached/unreached가 함께 있으면 execution finding과 별개로 partial reachability를 유지하지만 reached+execution-failure만 있고 completed-unreached가 없으면 partial reachability를 만들지 않는다.

HTTP request body hard limit은 정확히 1 MiB이다. 선언된 `Content-Length` 초과는 body-decode slot 획득 전에 `413 request_too_large`이며, streamed 초과도 413, surplus/malformed JSON은 `400 invalid_json`이다. server header parser limit은 64 KiB이다. Full response는 마지막 newline을 포함해 `<= 8 MiB`, compact response는 `< 1 MiB`이다.

accepted connection은 handler 생성 전에 기본 128, `CHECKNETWORK_MAX_CONNECTIONS` hard max 256으로 제한된다. pre-handler overflow socket은 HTTP response 없이 즉시 close되고 accept loop에는 interruptible 5 ms backoff가 적용된다. body decode는 route/auth/rate 뒤에 별도 nonblocking slot을 획득하고 기본값은 effective concurrent-report limit, `CHECKNETWORK_MAX_CONCURRENT_BODY_DECODES` hard max는 64다. slot은 단일 object와 EOF를 decode하는 동안만 유지하며 validation/checker/serialization/write에는 유지하지 않는다. 포화 응답은 `Retry-After`를 포함한 고정 `503 body_decode_capacity_unavailable`이다.

### Compact topology 협상

`topology_mode`를 생략하거나 `"full"`로 지정하면 legacy/raw 응답을 그대로 반환한다. `"compact"`는 traceroute-only 요청에서만 허용되며, 다른 문자열·빈 값은 `422 invalid_request`, JSON string이 아닌 값은 `400 invalid_json`이다.

Compact 응답은 분석이 raw attempts 전체를 처리한 뒤 생성된다. top-level `compact_topology`은 `schema: "compact-v1"`, `selection: "fair-complete-prefix-v1"`, 최대 500 nodes/1,000 directed links와 route당 최대 32 nodes(local + 30 hops + synthetic destination), global/per-result 통계, Geo coverage와 truncation metadata를 제공한다. consecutive canonical self-hop은 route에서 collapse하되 node aggregate observations에는 보존한다.

`compact-v1`은 additive/open object가 아니라 closed shape다. top-level 허용 필드는 `schema`, `selection`, `limits`, `nodes`, `links`, `routes`, `stats`, `result_stats`, `geo`, `truncated`, optional nonempty `truncation_reasons`뿐이다. nested exact sets는 limits=`nodes,links,max_response_bytes_exclusive,max_geo_bundle_bytes`; link=`from,to,status,observations`; route=`result_index,attempt,status,reached,complete,node_ids`; stats=`nodes,links,routes,node_observations,link_observations`; count=`total,displayed,omitted`; route-count=`total,displayed,complete,partial,omitted`; result-stat=`result_index,routes,node_observations,link_observations`; geo=`eligible,available,included,omitted,unavailable`이다. node required는 `id,kind,status,hop_min,hop_max,observations`, optional은 `address,latency_ms_avg,public_ip,geolocation,asn`; geolocation required는 `latitude,longitude`, optional은 `city,region,country,country_code`; ASN optional은 `number,organization`뿐이다. 모든 required collection은 null/생략 대신 non-null array/object여야 하고 zero case도 `nodes:[]`, `links:[]`, `routes:[]`, `result_stats:[...]`처럼 canonical empty를 유지한다. optional field는 absent 또는 유효한 값이어야 하며 explicit null/undefined와 unknown field는 reject된다. `public_ip`가 present하면 true여야 하고 Geo/ASN은 public node에서만 허용된다. ASN은 number≥1 또는 nonempty organization 중 적어도 하나가 있어야 하므로 `{}`와 `{number:0,organization:""}`는 canonical하지 않다. metadata가 없는 public node는 `asn`/`geolocation`을 **생략**하고 geo counters로 unavailable을 표현한다.

Geo/ASN bundle budget은 node JSON에서 `,"geolocation":{...}`와 `,"asn":{...}`가 차지하는 Go `encoding/json` bytes다. UTF-8, quotes/backslash/control, `<>&`, U+2028/U+2029 HTML escape와 숫자 spelling까지 포함해 exact 4,096 bytes를 허용하고 4,097 bytes를 거부한다. producer fixture는 (1) exact boundary, (2) boundary+1 rejection mutation, (3) metadata 없는 public node의 ASN absence를 Web/Android 양쪽에 동일하게 적용한다.

Compact transport copy에서는 traceroute `details.attempts`와 대표 `details.topology`만 제거한다. result scalar, attempts/provider counters, 기타 details, summary와 전체 `analysis`는 유지한다. newline을 포함한 응답은 1,048,576 bytes 미만이다. 초과하면 reverse node 순서로 Geo bundle을 제거한 뒤 최신 accepted route transaction을 deterministic하게 rollback한다. `truncation_reasons` 순서는 `node_limit`, `link_limit`, `response_size`, `geo_metadata_limit`이다. 축소할 수 없는 base response는 `500 compact_response_too_large`다.

새 traceroute producer는 실패 집계를 목적지 미도달과 실행 실패로 분리한다. `attempts_unreached`, `attempts_execution_failed`, `attempts_timed_out`, `attempts_cancelled`가 각각 기록되며, 혼합 실행 오류의 top-level code는 순서와 무관한 `traceroute_execution_incomplete`다. GeoIP enrichment 실패가 있으면 `geoip_provider_failures`가 기록되고 분석 coverage에 반영된다. 명령에서 수용하는 최대 hop은 30이고, stdout/stderr 합산 출력은 256 KiB로 제한된다. 관측 latency는 요청별 최대 timeout과 같은 30000 ms 이하의 유한한 음이 아닌 값만 수용한다. 제한 초과나 잘못된 latency는 기존 `traceroute_failed` 결과로 처리되며 제한을 넘는 원문은 응답에 보존하지 않는다.

Full topology의 directed link는 선택적인 `latency_delta_ms`를 가진다. 값은 `current hop RTT - previous hop RTT`를 0.01 ms로 반올림한 유한한 0~30000 값이다. RTT가 감소하면 계산값은 0으로 clamp되고 `omitempty` 때문에 field를 생략한다. link의 `latency_ms`는 schema에 없으며 Android consumer도 이를 invalid response로 거부한다.

non-traceroute checker가 반환할 때 server는 그 정확한 completion timestamp로 parent/supervisor cancellation, report deadline, checker deadline을 우선 arbitration한다. timestamp가 deadline과 같거나 늦으면 반환값이 healthy여도 timeout이다. HTTP(S)는 headers 뒤 최대 32 KiB body를 bounded observation하고 나서 latency/status를 확정한다. body read timeout/cancel은 해당 stable code, 그 외 read 실패는 `response_read_failed`로 반환하며 healthy로 처리하지 않는다.

새 report는 additive `analysis`를 제공한다. 이전 저장 report처럼 이 필드가 없는 JSON도 유효하다.

- `analysis.verdict`: `healthy`, `attention`, `inconclusive`
- `analysis.findings[]`: stable `code`, severity/category, 제목·요약, `confidence`, evidence/action 참조
- `analysis.evidence[]`: 원본 result index/kind/address, 관측 signal/value, 기대값과 provenance
- `analysis.actions[]`: 안전한 확인 단계, 기대 결과와 escalation 조건
- `analysis.coverage`: 사용한 signal, 누락 signal, provider failure와 limitation

GeoIP 집계는 `analysis.coverage.enrichment`에 additive하게 나타난다. provider별 최대 한 record이며 현재 정확한 schema는 다음과 같다.

```json
{
  "provider": "geoip",
  "source": "upstream|cache|mixed|none",
  "cache_hits": 0,
  "upstream_fetches": 0,
  "max_age_ms": 0,
  "failures": [
    {"kind": "not_found|rate_limited|timeout|policy|malformed|unavailable|cancelled|busy", "count": 1, "retryable": true}
  ]
}
```

`retryable`은 failure kind에 의해 결정되고 counts/age는 topology 관측 수와 24시간 cache TTL 안에서 제한된다. schema에는 provider URL, IP/target, provider message, credential 또는 wall-clock fetch timestamp가 없다. 기존 `analysis.evidence[].provenance` enum은 바뀌지 않는다.

분석 배열은 bounded consumer 계약이다. 현재 `Analyze`가 유효한 최대 20 results에서 만들 수 있는 상한은 HTTPS result당 certificate와 HTTP status finding을 함께 내는 경우의 findings/evidence/actions 각 40개다. coverage의 현재 계산 상한은 `available` 80, `missing` 60, `provider_failures` 20, `limitations` 100개다. Web과 Android는 향후 무제한 증가를 수용하지 않고 findings/evidence/actions를 각각 64개, coverage의 각 목록을 각각 128개에서 제한하며 초과 report 전체를 거부한다. 기존 8 MiB transport, 1,000,000 string characters, 32,768 containers 누적 제한도 그대로 적용된다.

`coverage.missing`은 입력에 존재하지 않아 관측할 수 없었던 신호만 포함한다. 키가 존재하지만 타입·범위·상호 관계가 잘못된 신호는 missing으로 중복 표시하지 않고 `coverage.limitations`의 `malformed_details`로 구분한다.

`confidence`는 원인 확률이 아니라 현재 문장에 대한 관측 근거의 직접성(`direct`, `corroborated`, `limited`)이다. `inconclusive`는 취소, 미지원/malformed details 또는 관측 부족으로 상태를 확정할 수 없음을 뜻한다. traceroute 분석은 도달 여부, 실행 실패, 성공 경로 간 변동, producer가 분류한 경로 저하만 설명하며 packet loss나 root cause를 단정하지 않는다. TLS transport가 연결된 뒤 handshake가 실패하면 `tls_handshake_failed`로 구분한다.

오류는 다음 형태로 반환한다. API response capability는 registry key만 받아 fixed body/header/status를 만들며 business code가 free-form error DTO를 쓰지 않는다.

```json
{"error":{"code":"invalid_request","message":"request is invalid"}}
```

Unix runtime은 startup probe가 선택한 exact path와 common numeric `-n -q 1 -w 2 -m 30` 또는 compatible `-q 1 -w 2 -m 30` grammar만 사용하며 runtime fallback은 없다. Windows는 startup-selected `tracert.exe -d -w ... -h 30` grammar와 현재 attempt timeout을 millisecond로 제한해 사용한다. Windows의 세 probe에서는 finite latency 최솟값을 node latency로 사용하고 `*` timeout은 제외한다.

응답 JSON은 직렬화에 성공한 뒤 status/header와 함께 기록되며 현재 호환성을 위해 마지막 newline을 유지한다. 서버 응답 값을 직렬화할 수 없으면 부분 `200` 대신 `500`과 `response_serialization_failed` JSON 오류를 반환한다.

closed registry는 정확히 16 rows이고 producer-derived structural mutation corpus는 85 cases다. Producer-owned finding fixture는 exact 23 findings, 31 result shapes와 expanded 136-row `(kind,status,error_code,details)` semantic compatibility matrix를 제공하며 Web/Android 두 client가 같은 exact producer matrix bytes와 eight traceroute witness fixtures를 소비한다. `healthy` result는 `error_code` absent/empty만 허용한다. 알려진 status/code/details라도 matrix와 모순이면 두 client는 publication 전에 whole report를 atomic 거부하고 server prose를 반사하지 않는 fixed local `invalid_response`/`INVALID_RESPONSE`를 게시한다. 별도 presentation fixture cardinality `6/10/21/23/33`의 10 evidence result kinds는 result-shape cardinality가 아니다.

| registry key | HTTP | wire code | retryable | `Retry-After` | fixed wire message |
|---|---:|---|---|---|---|
| `body_decode_capacity_unavailable` | 503 | same | yes | yes | `request body decode capacity is temporarily unavailable` |
| `compact_response_too_large` | 500 | same | yes | no | `compact report response exceeds the size limit` |
| `full_response_too_large` | 500 | same | yes | no | `full report response exceeds the size limit` |
| `internal_error` | 500 | same | yes | no | `report could not be generated` |
| `invalid_json` | 400 | same | no | no | `request body must be a valid JSON report request` |
| `invalid_request` | 422 | same | no | no | `request is invalid` |
| `method_not_allowed` | 405 | `unmatched` | no | no | `route not found` |
| `network_policy_blocked` | 422 | same | no | no | `target is not allowed in public mode` |
| `rate_limited` | 429 | same | yes | yes | `per-client request limit exceeded` |
| `request_too_large` | 413 | same | no | no | `request body exceeds the size limit` |
| `response_serialization_failed` | 500 | same | yes | no | `report response could not be serialized` |
| `route_not_found` | 404 | `unmatched` | no | no | `route not found` |
| `server_busy` | 503 | same | yes | yes | `report capacity is temporarily unavailable` |
| `server_draining` | 503 | same | yes | yes | `server is draining and temporarily unavailable` |
| `unauthorized` | 401 | same | no | no | `valid API credentials are required` |
| `write_capacity_unavailable` | 503 | same | yes | no | `report response write capacity is temporarily unavailable` |

server가 발행하는 `Retry-After`는 retry-after registry row에서만 존재하고 duration을 ceil한 canonical decimal seconds `1..3600`으로 clamp한다. Web/Android는 header를 strict하게 동일 해석한다: ASCII decimal `1..3600`만 valid하며 `0`, `3601`, leading zero, sign, whitespace, HTTP-date, overflow, comma/duplicate는 무시한다.

Android의 final non-2xx classification은 `ApiError.parse`와 `ApiErrorPresentation.resolve` 두 단계다. `ApiError.parse`는 body를 최대 64 Ki UTF-16 code units로 제한하고 depth≤2, 전체 properties≤3, tokens≤10, field name/value≤128 code units인 exact closed `{"error":{"code":"...","message":"..."}}` shape만 수용한다. Duplicate/unknown/missing/null/wrong-type property, trailing JSON/token과 malformed UTF-16은 모두 invalid다. 정확한 16개 `(status,code)` fixture pair만 typed error이며 wire `message` 대신 registry fixed local message/retryability를 사용한다. Unknown/unregistered status, unknown code, contradictory pair, body read failure, malformed/HTML/plain/empty body는 원래 HTTP status를 보존하지만 code `invalid_server_response`, message `The server returned an invalid error response.`, `retryable=false`, no `retryAt`인 하나의 local result가 된다. Server prose/code는 UI/export에 반사하지 않는다. Report/Capabilities transport는 disconnect 전에 case-insensitive 전체 header collection을 한 번 수집한다. 허용 row에서 exactly one canonical header일 때만 `retryAt=now+seconds`; zero values, duplicate field/case variants, comma-combined, inaccessible/malformed collection, invalid value 또는 retry-after 비허용 row는 timestamp만 absent로 만들고 otherwise-valid typed error와 registry retryability를 무효화하지 않는다. Final Activity UI는 registry retryable이면 header 유무와 무관하게 retry control을 보이고, allowed+present timestamp만 local message 다음 줄에 표시한다. `unauthorized`만 credential control이 visible할 때 그곳에 focus하며 다른 typed error와 `invalid_server_response`는 fixed error에 focus한다.

`server_busy`는 report checker 실행 전에 fail-fast하고, checker pool 포화는 각 대상의 stable `checker_capacity_unavailable` 결과로 bounded completion한다. GeoIP queue 포화는 enrichment failure kind `busy`로 처리되며 본 진단 상태를 덮어쓰지 않는다. write capacity failure는 계산된 큰 body를 queue에 보존하지 않는다. client 취소/timeout은 이미 commit된 status를 재작성하지 않으며 partial/zero network write는 telemetry만 `write_failed_partial`/`write_failed_zero`로 바뀐다.

HTTPS 검사는 redirect 전체가 HTTPS를 유지하고 최종 응답에 검증된 TLS 연결이 있어야 정상이다. downgrade는 결과의 `error_code: "tls_downgrade"`로 보고한다.

## Telemetry 계약

`api_telemetry.outcome`은 transport delivery/lifecycle만 뜻한다. 진단 결과가 unreachable이어도 HTTP 200 body가 완전히 기록되면 `report_finish.outcome`은 `ok`다. 유효하고 완전히 전달된 report의 `report_finish`에만 다음 extension을 함께 기록한다.

- `report_status`: `healthy|degraded|unreachable`
- `analysis_verdict`: `healthy|attention|inconclusive`
- `total_results`: 1~20
- `failed_results`: 0~`total_results`, report status와 관계가 일치해야 함
- `finding_count`: 0~40

nil analysis, invalid enum/count 관계, incomplete/failed write에서는 extension을 전부 생략하되 이미 유효한 base lifecycle event는 그대로 남긴다. telemetry는 positive allowlist이며 target/address/IP, raw path/query/body, credential/header, provider/error/panic prose를 기록하지 않는다. request/report ID는 opaque correlation field일 뿐 metric label이 아니다.
