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
    {"kind": "http", "address": "https://example.com", "expected_status": 200}
  ]
}
```

`timeout_ms`는 100~30000이며 생략 시 5000이다. `kind`는 `dns`, `tcp`, `http` 중 하나다. 응답의 `status`는 `healthy`, `degraded`, `unreachable` 중 하나다.

오류는 다음 형태로 반환한다.

```json
{"error":{"code":"invalid_request","message":"targets must contain 1 to 20 items"}}
```

