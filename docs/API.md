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

readiness는 startup-cached local traceroute executable 상태와 administrative phase만 사용한다. 일시적인 connection/report/body/checker 포화, GeoIP 또는 임의 target을 조회하지 않는다. drain 중 `/livez`는 200이지만 `/readyz`와 모든 business route는 503 `{"status":"unavailable","reason":"draining"}`이다. 다른 method는 405와 `Allow: GET, HEAD`; suffix/escaped alias는 operational route로 취급하지 않는다.

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

`timeout_ms`는 100~30000이며 생략 시 5000이다. `kind`는 `dns`, `tcp`, `http`, `https`, `traceroute`, `ssh`, `smtp`, `submission`, `smtps`, `imap`, `imaps`, `pop3`, `pop3s` 중 하나다. 서비스 주소에 포트를 생략하면 표준 포트를 사용하며 `host:port`로 재정의할 수 있다. traceroute 대상은 `attempts`로 1~10회 반복 실행할 수 있으며, 생략하면 기본 5회 실행한다. `timeout_ms`는 각 반복 실행의 제한 시간으로 적용된다. 실행별 결과는 `details.attempts`에, 전체 횟수·도달·실패 집계는 `details.attempts_total`, `details.attempts_reached`, `details.attempts_failed`에 저장된다. 이전 클라이언트 호환을 위한 대표 경로는 `details.topology`에 유지한다. 각 토폴로지는 `nodes`, `links`, `reached`를 제공하며 노드/구간 상태는 `healthy`, `degraded`, `unknown`, `failure`로 구분한다. 공인 IP 노드에는 `public_ip: true`가 표시되고 식별에 성공하면 `geolocation`(도시·지역·국가·위도·경도)과 `asn`(번호·사업자)이 추가된다. GeoIP 조회 실패는 이 필드만 생략하며 진단 상태를 바꾸지 않는다. HTTPS와 암시적 TLS 메일 서비스는 TLS 버전, 암호 스위트, 인증서 제목과 만료 시각을 결과에 포함한다. 응답의 `status`는 `healthy`, `degraded`, `unreachable` 중 하나다.

HTTP request body hard limit은 정확히 1 MiB이다. 선언된 `Content-Length` 초과는 body-decode slot 획득 전에 `413 request_too_large`이며, streamed 초과도 413, surplus/malformed JSON은 `400 invalid_json`이다. server header parser limit은 64 KiB이다. Full response는 마지막 newline을 포함해 `<= 8 MiB`, compact response는 `< 1 MiB`이다.

accepted connection은 handler 생성 전에 기본 128, `CHECKNETWORK_MAX_CONNECTIONS` hard max 256으로 제한된다. pre-handler overflow socket은 HTTP response 없이 즉시 close되고 accept loop에는 interruptible 5 ms backoff가 적용된다. body decode는 route/auth/rate 뒤에 별도 nonblocking slot을 획득하고 기본값은 effective concurrent-report limit, `CHECKNETWORK_MAX_CONCURRENT_BODY_DECODES` hard max는 64다. slot은 단일 object와 EOF를 decode하는 동안만 유지하며 validation/checker/serialization/write에는 유지하지 않는다. 포화 응답은 `Retry-After`를 포함한 고정 `503 body_decode_capacity_unavailable`이다.

### Compact topology 협상

`topology_mode`를 생략하거나 `"full"`로 지정하면 legacy/raw 응답을 그대로 반환한다. `"compact"`는 traceroute-only 요청에서만 허용되며, 다른 문자열·빈 값은 `422 invalid_request`, JSON string이 아닌 값은 `400 invalid_json`이다.

Compact 응답은 분석이 raw attempts 전체를 처리한 뒤 생성된다. top-level `compact_topology`은 `schema: "compact-v1"`, `selection: "fair-complete-prefix-v1"`, 최대 500 nodes/1,000 directed links와 route당 최대 32 nodes(local + 30 hops + synthetic destination), global/per-result 통계, Geo coverage와 truncation metadata를 제공한다. consecutive canonical self-hop은 route에서 collapse하되 node aggregate observations에는 보존한다.

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

오류는 다음 형태로 반환한다.

```json
{"error":{"code":"invalid_request","message":"targets must contain 1 to 20 items"}}
```

Unix는 `traceroute -n -q 1 -w 2 -m 30`, Windows는 현재 attempt timeout을 millisecond로 제한한 `tracert.exe -d -w ... -h 30`을 사용한다. Windows의 세 probe에서는 finite latency 최솟값을 node latency로 사용하고 `*` timeout은 제외한다.

응답 JSON은 직렬화에 성공한 뒤 status/header와 함께 기록되며 현재 호환성을 위해 마지막 newline을 유지한다. 서버 응답 값을 직렬화할 수 없으면 부분 `200` 대신 `500`과 `response_serialization_failed` JSON 오류를 반환한다.

운영 오류 계약:

- `401 unauthorized`: public 모드 Bearer credential 누락/오류
- `413 request_too_large`: 1 MiB request body 상한 초과
- `429 rate_limited`: source IP 요청 상한 초과, `Retry-After` 포함
- `503 body_decode_capacity_unavailable`: 인증/rate 통과 후 동시 JSON body decode slot 포화, `Retry-After` 포함
- `503 server_busy`: 동시 report 실행 상한 초과, `Retry-After` 포함
- `503 write_capacity_unavailable`: 실행이 끝난 bounded payload에 response-write slot을 즉시 확보하지 못함
- `422 network_policy_blocked`: public 모드에서 허용되지 않은 주소 대역
- `500 full_response_too_large`: full response를 8 MiB 이하 closed DTO로 만들 수 없음
- `500 compact_response_too_large`: compact base response를 1 MiB 미만으로 축소할 수 없음

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
