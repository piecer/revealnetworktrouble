# 운영 및 멀티플랫폼 실행

## 환경 변수

- `CHECKNETWORK_ADDR`: API listen 주소, 기본값 `127.0.0.1:8080`
- `CHECKNETWORK_MODE`: `trusted-local`(기본) 또는 `public`
- `CHECKNETWORK_MAX_CONCURRENT_REPORTS`: 동시에 실행할 report 상한, 기본 4
- `CHECKNETWORK_API_KEY`: `public` 모드에서 필수인 Bearer API key
- `CHECKNETWORK_RATE_LIMIT_PER_MINUTE`: `public` 모드에서 필수인 source IP별 분당 요청 상한. 활성 client window는 프로세스당 최대 4096개로 제한된다.
- `CHECKNETWORK_ALLOWED_ORIGINS`: 쉼표로 구분한 웹 origin, 기본값은 로컬 3000 포트
- `CHECKNETWORK_GEOIP_URL`: traceroute 공인 IP의 위치/ASN 조회 API base URL, 기본값 `https://ipwho.is/`
- `CARTO_BASE_MAP`: 웹 Geo 경로 지도에 사용할 CARTO Basemaps API key 또는 `https://` 타일 URL 템플릿. Docker Compose의 웹 컨테이너가 시작할 때 브라우저 런타임 설정으로 주입한다.

`trusted-local`은 사설망 진단을 허용하므로 loopback에만 bind하는 것이 기본이다. `public`은 API key와 rate limit 없이는 시작하지 않으며 DNS, TCP, HTTP(S) redirect, 서비스, traceroute의 모든 실제 dial 주소를 재검사해 사설·loopback·link-local·metadata·특수 목적 대역을 차단한다. CORS 허용은 인증이 아니다. reverse proxy 뒤에서는 현재 직접 연결 source IP가 rate-limit key이므로 프록시 자체의 인증/rate limit도 함께 사용한다.

Public client는 `Authorization: Bearer <CHECKNETWORK_API_KEY>`를 모든 API 요청에 보낸다. 현재 정적 Web UI는 credential 입력을 제공하지 않으므로 `trusted-local` 모드용이다. public Web 배포용 session-only credential UX는 별도 UI 단계에서 제공한다.

traceroute의 공인 IP는 서버에서 GeoIP 제공자에 전달된다. 사내 정책상 외부 조회를 제한해야 한다면 호환되는 내부 프록시를 `CHECKNETWORK_GEOIP_URL`로 지정한다. 성공 조회는 프로세스 메모리에 최대 2048개, 24시간 TTL의 LRU 캐시로 보관되며 GeoIP 실패는 traceroute 상태에 영향을 주지 않는다.

웹의 Geo 경로 지도는 Leaflet 리소스(`unpkg.com`)와 CARTO/OpenStreetMap 지도 타일(`basemaps.cartocdn.com`)을 사용한다. `CARTO_BASE_MAP`이 키이면 CARTO 타일 요청의 `key` 쿼리 파라미터로, `https://`로 시작하면 Leaflet 타일 URL 템플릿으로 사용한다. CARTO의 Basemaps API key는 일반 CARTO API의 `access_token`과 다른 자격 증명이며 `https://carto.com/basemaps/apikey/`에서 발급한다. 값이 없으면 지도는 표시되지만 CARTO의 API key 안내 워터마크가 나타날 수 있다. 지도 화면에서도 값을 입력할 수 있으며 이 경우 영구 로컬 저장소가 아닌 현재 탭의 `sessionStorage`에만 보관된다. 브라우저에 전달되는 키는 공개 클라이언트 자격 증명이므로 운영 도메인용 키를 사용하고 저장소에 직접 커밋하지 않는다. 클라이언트 브라우저에서 지도 호스트로 HTTPS 접근이 가능해야 도시·도로·국가 경계가 포함된 상세 지도가 표시된다. 리소스 접근이 불가능한 환경에서도 식별된 IP, 지역과 좌표 목록은 화면 하단에 유지된다.

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

Compose API는 host의 `127.0.0.1:9090`, 웹은 `localhost:3000`에서 접근한다. 컨테이너 API는 `trusted-local`로 실행되며 host publish도 loopback으로 제한한다. 외부 공개 시에는 `public` 모드와 TLS reverse proxy를 사용하고 proxy 계층에도 인증/rate limit을 둔다.
