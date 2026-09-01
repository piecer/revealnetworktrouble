# 아키텍처 및 개발 기준

## 구성

```text
Web / Mobile / CLI
        |
   REST JSON API
        |
 Report Service
        |
 Analysis Interpreter  <- normalized facts / deterministic evidence rules
        |
 Diagnostic Runner
   |      |       |         |
  DNS    TCP   HTTP(S)   Services   <- 교체 가능한 Checker
```

`internal/api`는 HTTP 전송만 담당하고, `diagnostic` 패키지는 프로토콜이나 UI를 알지 못한다. 새 프론트엔드는 API만 사용하며, 새 검사 방식은 `Checker` 인터페이스 구현으로 추가한다.

진단 checker가 만든 `Result`는 먼저 typed normalized facts로 변환한 뒤 결정적 analysis 규칙에 입력된다. 분석은 backend의 자유 형식 오류 message를 파싱하지 않고 stable `error_code`와 구조화된 details만 사용한다. `confidence`는 원인 확률이 아니라 finding 문장을 지지하는 evidence의 직접성이고, 관측 불가·malformed·legacy 데이터는 별도 coverage limitation으로 보존한다.

## 일관성 기준

- 모든 공개 JSON 필드는 `snake_case`를 사용한다.
- 시간은 UTC RFC3339, 기간은 밀리초 정수로 표현한다.
- 모든 검사는 `context.Context` 취소와 타임아웃을 지킨다.
- 사용자에게 반환하는 실패는 안정적인 `error_code`와 설명을 함께 가진다.
- 한 검사 실패가 다른 검사를 중단시키지 않는다.
- 테스트는 외부 인터넷, 특정 DNS 설정, 관리자 권한에 의존하지 않는다.
- 로그에 요청 본문, 자격 증명, 전체 URL 쿼리를 남기지 않는다.

## 보안 기준

기본 API는 `127.0.0.1:8080`의 `trusted-local` 모드이며 사설망 진단을 허용한다. 공개 인터넷에서는 `public` 모드가 API key와 source IP rate limit을 요구하고, DNS 재해석과 HTTP redirect를 포함한 실제 dial 직전에 사설·loopback·link-local·metadata·특수 목적 주소를 차단한다. CORS는 인증으로 취급하지 않는다. 요청 본문은 1 MiB, 대상은 요청당 20개, 동시 report는 기본 4개로 제한한다. 운영에서는 TLS gateway의 인증/rate limit도 중첩한다.
