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

오류는 다음 형태로 반환한다.

```json
{"error":{"code":"invalid_request","message":"targets must contain 1 to 20 items"}}
```
