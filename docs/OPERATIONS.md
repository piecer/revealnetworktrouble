# 운영 및 멀티플랫폼 실행

## 환경 변수

- `CHECKNETWORK_ADDR`: API listen 주소, 기본값 `127.0.0.1:8080`
- `CHECKNETWORK_MODE`: `trusted-local`(기본) 또는 `public`
- `CHECKNETWORK_MAX_CONCURRENT_REPORTS`: 동시에 실행할 report 상한, 기본 4
- `CHECKNETWORK_API_KEY`: `public` 모드에서 필수인 Bearer API key
- `CHECKNETWORK_RATE_LIMIT_PER_MINUTE`: `public` 모드에서 필수인 source IP별 분당 요청 상한. 활성 client window는 프로세스당 최대 4096개로 제한된다.
- `CHECKNETWORK_ALLOWED_ORIGINS`: 쉼표로 구분한 웹 origin, 기본값은 로컬 3000 포트
- `CHECKNETWORK_GEOIP_URL`: traceroute 공인 IP의 위치/ASN 조회 API base URL, 기본값 `https://ipwho.is/`


`trusted-local`은 사설망 진단을 허용하므로 loopback에만 bind하는 것이 기본이다. `public`은 API key와 rate limit 없이는 시작하지 않으며 DNS, TCP, HTTP(S) redirect, 서비스, traceroute의 모든 실제 dial 주소를 재검사해 사설·loopback·link-local·metadata·특수 목적 대역을 차단한다. CORS 허용은 인증이 아니다. reverse proxy 뒤에서는 현재 직접 연결 source IP가 rate-limit key이므로 프록시 자체의 인증/rate limit도 함께 사용한다.

Public client는 모든 API 요청에 설정된 API key를 Bearer credential로 보낸다. Web UI의 공용 연결 설정에서 API base별 Bearer를 명시적으로 활성화할 수 있으며, 값은 해당 탭의 `sessionStorage`에만 보관된다. 원격 API에는 HTTPS가 필요하고 loopback HTTP만 예외다. credential은 URL, request signature, report, DOM text, JSON/Markdown export 또는 `localStorage`에 포함하지 않는다.

Android release는 빈 API base로 시작해 운영 HTTPS origin을 명시적으로 요구한다. debug에서만 emulator/localhost HTTP를 허용한다. Android Bearer는 Activity/session 메모리에만 존재하고 saved state나 일반 preferences에 쓰지 않는다. 원본 report 공유는 확인 대화상자 뒤 private cache `FileProvider` URI로만 수행하며 앱 종료·새 요청에서 cache artifact를 제거한다.

traceroute의 공인 IP는 서버에서 GeoIP 제공자에 전달된다. 사내 정책상 외부 조회를 제한해야 한다면 호환되는 내부 프록시를 `CHECKNETWORK_GEOIP_URL`로 지정한다. 성공 조회는 프로세스 메모리에 최대 2048개, 24시간 TTL의 LRU 캐시로 보관되며 GeoIP 실패는 traceroute 상태에 영향을 주지 않는다.

웹 Geo 보기는 외부 지도 runtime, tile host 또는 지도 credential을 사용하지 않는다. 서버가 선택한 public-IP 좌표를 bounded Canvas에 투영하고 같은 selection의 접근 가능한 위치 목록을 제공한다. 위치는 IP 등록 정보 기반 추정치이며 실제 장비 소재지를 보장하지 않는다.

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

Android build에는 JDK 17, platform 35와 build-tools 35.0.0이 필요하다. `make android-check`가 wrapper checksum, debug/release JVM tests, lint와 assemble을 실행한다. Emulator/physical device, TalkBack·Switch Access와 OEM share-sheet 동작은 이 gate에 포함되지 않는다.
