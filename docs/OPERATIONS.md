# 운영 및 멀티플랫폼 실행

## 환경 변수

- `CHECKNETWORK_ADDR`: API listen 주소, 기본값 `:8080`
- `CHECKNETWORK_ALLOWED_ORIGINS`: 쉼표로 구분한 웹 origin, 기본값은 로컬 3000 포트

예를 들어 API를 9090 포트로 실행한다.

```bash
CHECKNETWORK_ADDR=:9090 go run ./cmd/checknetwork-api
```

특정 인터페이스에만 바인딩할 수도 있다. 로컬에서만 접근하게 하려면
`CHECKNETWORK_ADDR=127.0.0.1:9090`, 모든 인터페이스에서 접근하게 하려면
`CHECKNETWORK_ADDR=:9090`을 사용한다.

## 교차 빌드

```bash
GOOS=linux GOARCH=amd64 go build -o bin/checknetwork-linux-amd64 ./cmd/checknetwork-api
GOOS=darwin GOARCH=arm64 go build -o bin/checknetwork-darwin-arm64 ./cmd/checknetwork-api
GOOS=windows GOARCH=amd64 go build -o bin/checknetwork-windows-amd64.exe ./cmd/checknetwork-api
```

## Docker

```bash
docker compose up --build
```

API는 `localhost:8080`, 웹은 `localhost:3000`에서 접근한다. 운영 환경에서는 TLS 프록시, 인증, rate limit, SSRF 차단 정책을 앞단에 둔다.

컨테이너 내부 포트는 그대로 두고 호스트 포트만 9090으로 바꾸려면
`compose.yaml`의 API 포트 매핑을 `"9090:8080"`으로 변경한다. 컨테이너
내부 포트도 바꾸려면 `CHECKNETWORK_ADDR: :9090`과 `"9090:9090"`을 함께
설정해야 한다.
