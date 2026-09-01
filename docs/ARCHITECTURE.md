# 아키텍처 및 개발 기준

## 구성

```text
Web / Mobile / CLI
        |
   REST JSON API
        |
 Report Service
        |
 Diagnostic Runner
   |      |      |
  DNS    TCP    HTTP   <- 교체 가능한 Checker
```

`internal/api`는 HTTP 전송만 담당하고, `diagnostic` 패키지는 프로토콜이나 UI를 알지 못한다. 새 프론트엔드는 API만 사용하며, 새 검사 방식은 `Checker` 인터페이스 구현으로 추가한다.

## 일관성 기준

- 모든 공개 JSON 필드는 `snake_case`를 사용한다.
- 시간은 UTC RFC3339, 기간은 밀리초 정수로 표현한다.
- 모든 검사는 `context.Context` 취소와 타임아웃을 지킨다.
- 사용자에게 반환하는 실패는 안정적인 `error_code`와 설명을 함께 가진다.
- 한 검사 실패가 다른 검사를 중단시키지 않는다.
- 테스트는 외부 인터넷, 특정 DNS 설정, 관리자 권한에 의존하지 않는다.
- 로그에 요청 본문, 자격 증명, 전체 URL 쿼리를 남기지 않는다.

## 보안 기준

기본 API는 로컬 개발을 위해 모든 origin을 허용할 수 있으나 운영에서는 `CHECKNETWORK_ALLOWED_ORIGINS`를 명시한다. 요청 본문은 1 MiB, 대상은 요청당 20개로 제한한다. 공개 인터넷에 노출할 때는 사설·링크 로컬·메타데이터 주소 접근 차단, 인증, rate limit을 게이트웨이 또는 후속 네트워크 정책 계층에 반드시 추가한다.

