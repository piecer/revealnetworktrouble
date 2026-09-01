# 운영 및 멀티플랫폼 실행

## 환경 변수

- `CHECKNETWORK_ADDR`: API listen 주소, 기본값 `:8080`
- `CHECKNETWORK_ALLOWED_ORIGINS`: 쉼표로 구분한 웹 origin, 기본값은 로컬 3000 포트
- `CHECKNETWORK_GEOIP_URL`: traceroute 공인 IP의 위치/ASN 조회 API base URL, 기본값 `https://ipwho.is/`
- `CARTO_BASE_MAP`: 웹 Geo 경로 지도에 사용할 CARTO access token 또는 `https://` 타일 URL 템플릿. Docker Compose의 웹 컨테이너가 시작할 때 브라우저 런타임 설정으로 주입한다.

traceroute의 공인 IP는 서버에서 GeoIP 제공자에 전달된다. 사내 정책상 외부 조회를 제한해야 한다면 호환되는 내부 프록시를 `CHECKNETWORK_GEOIP_URL`로 지정한다. 조회 결과는 프로세스 메모리에 IP별로 캐시되며 GeoIP 실패는 traceroute 상태에 영향을 주지 않는다.

웹의 Geo 경로 지도는 Leaflet 리소스(`unpkg.com`)와 CARTO/OpenStreetMap 지도 타일(`basemaps.cartocdn.com`)을 사용한다. `CARTO_BASE_MAP`이 토큰이면 CARTO 타일 요청의 `access_token`으로, `https://`로 시작하면 Leaflet 타일 URL 템플릿으로 사용한다. 값이 없으면 공개 다크 베이스맵을 사용한다. 지도 화면에서도 값을 입력할 수 있으며 이 경우 영구 로컬 저장소가 아닌 현재 탭의 `sessionStorage`에만 보관된다. 브라우저에 전달되는 CARTO 토큰은 CARTO에서 허용 Referer를 운영 도메인으로 제한해야 한다. 클라이언트 브라우저에서 지도 호스트로 HTTPS 접근이 가능해야 도시·도로·국가 경계가 포함된 상세 지도가 표시된다. 리소스 접근이 불가능한 환경에서도 식별된 IP, 지역과 좌표 목록은 화면 하단에 유지된다.

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
