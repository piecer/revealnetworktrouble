# REST API v1

## `GET /api/v1/health`

프로세스 상태와 버전을 반환한다.

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

### Compact topology 협상

`topology_mode`를 생략하거나 `"full"`로 지정하면 legacy/raw 응답을 그대로 반환한다. `"compact"`는 traceroute-only 요청에서만 허용되며, 다른 문자열·빈 값은 `422 invalid_request`, JSON string이 아닌 값은 `400 invalid_json`이다.

Compact 응답은 분석이 raw attempts 전체를 처리한 뒤 생성된다. top-level `compact_topology`은 `schema: "compact-v1"`, `selection: "fair-complete-prefix-v1"`, 최대 500 nodes/1,000 directed links와 route당 최대 32 nodes(local + 30 hops + synthetic destination), global/per-result 통계, Geo coverage와 truncation metadata를 제공한다. consecutive canonical self-hop은 route에서 collapse하되 node aggregate observations에는 보존한다.

Compact transport copy에서는 traceroute `details.attempts`와 대표 `details.topology`만 제거한다. result scalar, attempts/provider counters, 기타 details, summary와 전체 `analysis`는 유지한다. newline을 포함한 응답은 1,048,576 bytes 미만이다. 초과하면 reverse node 순서로 Geo bundle을 제거한 뒤 최신 accepted route transaction을 deterministic하게 rollback한다. `truncation_reasons` 순서는 `node_limit`, `link_limit`, `response_size`, `geo_metadata_limit`이다. 축소할 수 없는 base response는 `500 compact_response_too_large`다.

새 traceroute producer는 실패 집계를 목적지 미도달과 실행 실패로 분리한다. `attempts_unreached`, `attempts_execution_failed`, `attempts_timed_out`, `attempts_cancelled`가 각각 기록되며, 혼합 실행 오류의 top-level code는 순서와 무관한 `traceroute_execution_incomplete`다. GeoIP enrichment 실패가 있으면 `geoip_provider_failures`가 기록되고 분석 coverage에 반영된다. 명령에서 수용하는 최대 hop은 30이고, stdout/stderr 합산 출력은 256 KiB로 제한된다. 관측 latency는 요청별 최대 timeout과 같은 30000 ms 이하의 유한한 음이 아닌 값만 수용한다. 제한 초과나 잘못된 latency는 기존 `traceroute_failed` 결과로 처리되며 제한을 넘는 원문은 응답에 보존하지 않는다.

새 report는 additive `analysis`를 제공한다. 이전 저장 report처럼 이 필드가 없는 JSON도 유효하다.

- `analysis.verdict`: `healthy`, `attention`, `inconclusive`
- `analysis.findings[]`: stable `code`, severity/category, 제목·요약, `confidence`, evidence/action 참조
- `analysis.evidence[]`: 원본 result index/kind/address, 관측 signal/value, 기대값과 provenance
- `analysis.actions[]`: 안전한 확인 단계, 기대 결과와 escalation 조건
- `analysis.coverage`: 사용한 signal, 누락 signal, provider failure와 limitation

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
- `429 rate_limited`: source IP 요청 상한 초과, `Retry-After` 포함
- `503 server_busy`: 동시 report 실행 상한 초과, `Retry-After` 포함
- `422 network_policy_blocked`: public 모드에서 허용되지 않은 주소 대역
- `500 compact_response_too_large`: compact base response를 1 MiB 미만으로 축소할 수 없음

HTTPS 검사는 redirect 전체가 HTTPS를 유지하고 최종 응답에 검증된 TLS 연결이 있어야 정상이다. downgrade는 결과의 `error_code: "tls_downgrade"`로 보고한다.
