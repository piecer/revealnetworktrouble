# 운영 및 멀티플랫폼 실행

## 환경 변수

- `CHECKNETWORK_ADDR`: API listen 주소, 기본값 `127.0.0.1:8080`
- `CHECKNETWORK_MODE`: `trusted-local`(기본) 또는 `public`
- `CHECKNETWORK_MAX_CONNECTIONS`: handler 생성 전 active accepted connection 상한, 기본 128, 허용 범위 1~256
- `CHECKNETWORK_MAX_CONCURRENT_REPORTS`: 동시에 실행할 report 상한, 기본 4, 허용 범위 1~16. 17 이상은 `loadRuntimeConfig` 단계에서 시작을 거부한다.
- `CHECKNETWORK_MAX_CONCURRENT_BODY_DECODES`: 인증된 report JSON 동시 decode 상한, 미설정 시 effective report 상한과 동일, 허용 범위 1~64
- `CHECKNETWORK_MAX_CONCURRENT_CHECKS`: 프로세스 checker supervisor capacity, 기본 80, 허용 범위 1~1024
- `CHECKNETWORK_API_KEY`: `public` 모드에서 필수인 Bearer API key
- `CHECKNETWORK_RATE_LIMIT_PER_MINUTE`: `public` 모드에서 필수인 source IP별 분당 요청 상한. 활성 client window는 프로세스당 최대 4096개로 제한된다.
- `CHECKNETWORK_ALLOWED_ORIGINS`: 쉼표로 구분한 웹 origin, 기본값은 로컬 3000 포트
- `CHECKNETWORK_GEOIP_URL`: traceroute 공인 IP의 위치/ASN 조회 API base URL, 기본값 `https://ipwho.is/`


`trusted-local`은 사설망 진단을 허용하므로 loopback에만 bind하는 것이 기본이다. `public`은 API key와 rate limit 없이는 시작하지 않으며 DNS, TCP, HTTP(S) redirect, 서비스, traceroute의 모든 실제 dial 주소를 재검사해 사설·loopback·link-local·metadata·특수 목적 대역을 차단한다. CORS 허용은 인증이 아니다. reverse proxy 뒤에서는 현재 직접 연결 source IP가 rate-limit key이므로 프록시 자체의 인증/rate limit도 함께 사용한다.

Public client는 모든 API 요청에 설정된 API key를 Bearer credential로 보낸다. Web UI의 공용 연결 설정에서 API base별 Bearer를 명시적으로 활성화할 수 있으며, 값은 해당 탭의 `sessionStorage`에만 보관된다. 원격 API에는 HTTPS가 필요하고 loopback HTTP만 예외다. credential은 URL, request signature, report, DOM text, JSON/Markdown export 또는 `localStorage`에 포함하지 않는다.

Android release는 빈 API base로 시작해 운영 HTTPS origin을 명시적으로 요구한다. debug에서만 emulator/localhost HTTP를 허용한다. Android Bearer는 Activity/session 메모리에만 존재하고 saved state나 일반 preferences에 쓰지 않는다. 원본 report 공유는 확인 대화상자 뒤 private cache `FileProvider` URI로만 수행하며 앱 종료·새 요청에서 cache artifact를 제거한다.

traceroute의 공인 IP는 서버에서 GeoIP 제공자에 전달된다. 사내 정책상 외부 조회를 제한해야 한다면 호환되는 내부 프록시를 `CHECKNETWORK_GEOIP_URL`로 지정한다. 성공 조회는 프로세스 메모리에 최대 2048개, 24시간 TTL의 LRU 캐시로 보관되며 GeoIP 실패는 traceroute 상태에 영향을 주지 않는다.

## 실행 및 자원 envelope

| 계층 | 기본/고정 경계 | 포화·초과 결과 |
|---|---:|---|
| accepted connections | 기본 128, 설정 hard max 256 | handler 생성 전 socket 즉시 close + interruptible 5 ms accept backoff; HTTP status 없음 |
| authenticated body decode | effective report limit 기본, 설정 hard max 64 | `503 body_decode_capacity_unavailable` + `Retry-After` |
| active reports | 기본 4, 설정 hard max 16 | 실행 전 `503 server_busy` + `Retry-After` |
| active checker goroutines | 기본 80, 설정 hard max 1,024 | target별 `checker_capacity_unavailable` |
| GeoIP provider | process-wide active 8, queued 64 | queue 포화는 retryable enrichment `busy`; duplicate waiter는 queue slot을 쓰지 않음 |
| GeoIP cache | LRU 2,048, TTL 24h | oldest entry eviction; failure/cancel/abandoned result는 cache하지 않음 |
| response writes | hard max 16, report limit 이하 | 큰 payload 폐기 후 `503 write_capacity_unavailable` |
| HTTP input | header 64 KiB, body 1 MiB | parser rejection / `413 request_too_large` |
| HTTP output | full `<=8 MiB`, compact `<1 MiB` | `500 full_response_too_large` / `compact_response_too_large` |
| public rate windows | active source windows 4,096 | bounded eviction; proxy rate-limit도 중첩 필요 |

Full transport validation은 depth 64, containers 32,768, nodes 262,144, cumulative string bytes 1,000,000, topology nodes 8,192/links 16,384도 제한한다. Compose API는 CPU 2.0, memory 512 MiB, PID 256이고 Web은 CPU 0.25, memory 64 MiB, PID 64이다. nginx는 worker 2개, worker당 connection 256개로 고정한다. 애플리케이션이 별도 FD ulimit을 설정하지 않으므로 실제 FD ceiling은 container runtime/host의 `nofile` limit이다. 배포자는 host FD와 memory pressure를 함께 감시해야 한다.

Connection lease는 실제 accepted socket `Close`에서 exactly once 반환된다. body admission은 exact report route와 auth/rate limit을 통과한 뒤 적용되고, 선언된 `Content-Length >1 MiB`는 body slot 전에 거부한다. 획득한 slot은 `MaxBytesReader`로 JSON object 하나와 EOF를 decode하는 동안만 유지하며 malformed/stream overflow/cancel/panic에서도 반환한다. validation, report/checker 실행, serialization 또는 response write가 body slot을 점유하지 않는다.

checker/GeoIP worker가 context cancellation을 지키지 않으면 Go process가 그 goroutine 또는 injected transport를 강제로 종료할 수 없다. request는 budget에서 synthetic timeout/cancel로 반환되지만 worker는 실제 종료 때까지 active slot을 점유한다. HTTP drain과 checker supervisor shutdown은 하나의 346초 end-to-end deadline을 공유한다. graceful shutdown은 remaining/stuck count를 기록하며 이 deadline 뒤 process/container 경계에서 종료될 수 있다.

## Deadline 모델과 graceful drain

HTTP/1 상수는 header `H=5s`, body `B=30s`, execution `E=301s`, marshal/write grace `R=5s`, shutdown margin `D=5s`이다.

- `ReadHeaderTimeout = H = 5s`
- accept 기준 `ReadTimeout = H+B = 35s`
- post-header `WriteTimeout = B+E+R = 336s`
- HTTP drain과 checker supervisor가 공유하는 end-to-end `ShutdownTimeout = H+B+E+R+D = 346s`
- 정상 Web/Android report deadline = 315s = `E+R` 306s + network/scheduler reserve 9s

315초 client 계약에는 hostile slow-body 30초를 더하지 않는다. Go HTTP/2 stream의 `WriteTimeout` 의미는 HTTP/1 connection deadline과 다를 수 있으므로, TLS reverse proxy와 실제 Chrome/Firefox/Safari 조합은 별도 staging probe 대상이다. Compose `stop_grace_period: 6m`은 단일 346초 drain deadline 뒤 signal/exit를 위한 14초 margin을 포함한다.

checker terminal arbitration은 반환 직후의 exact timestamp를 사용한다. parent/supervisor cancellation, report deadline, checker deadline이 returned result보다 우선하며 timestamp가 deadline과 같거나 늦으면 late healthy를 timeout으로 대체한다. HTTP(S)는 headers 후 최대 32 KiB body를 읽어 bounded observation을 완료해야 성공하며 timeout/cancel 외 read 오류는 stable `response_read_failed`다.

## Liveness, readiness와 drain

정확한 `GET`/`HEAD /livez`와 `/readyz`는 business auth/rate middleware보다 바깥에 있어 public mode에서도 credential이 필요 없고 source quota를 소비하지 않는다. 고정 JSON state는 다음과 같다.

- liveness: `200 {"status":"live"}`
- accepting readiness: `200 {"status":"ready"}`
- startup: `503 {"status":"not_ready","reason":"starting"}`
- drain: `503 {"status":"not_ready","reason":"draining"}`
- startup에서 캐시한 traceroute executable 부재: `503 {"status":"not_ready","reason":"traceroute_unavailable"}`

Readiness는 외부 네트워크/GeoIP/target을 호출하지 않고 일시적인 connection/report/body/checker capacity 포화에도 변하지 않는다. shutdown은 먼저 irreversible drain phase로 전환한다. 이후 `/livez`만 200을 유지하고 `/readyz` 및 business route는 business handler를 호출하지 않은 채 503 고정 state를 반환한다. Compose API healthcheck는 `/readyz`다.

## Telemetry 및 privacy

JSON log `msg=api_telemetry`의 event allowlist는 `http_terminal`, `report_submit`, `report_admit`, `report_reject`, `report_start`, `report_computed`, `report_finish`, `report_cancel`이다. base payload fields는 `event`, `outcome`, `request_id`, `report_id`, `method`, `route`, `status`, `active`, `capacity`, `request_bytes`, `response_attempted_bytes`, `response_bytes`, `duration_ms`, `runner_duration_ms`, `marshal_duration_ms`, `write_duration_ms`이며 `msg`는 항상 `api_telemetry`다. outcome allowlist는 `ok`, `unauthorized`, `rate_limited`, `invalid_json`, `invalid_request`, `request_too_large`, `server_capacity_unavailable`, `body_decode_capacity_unavailable`, `checker_capacity_unavailable`, `write_capacity_unavailable`, `policy_blocked`, `timeout`, `cancelled`, `panic_safe_failure`, `serialization_failed`, `full_response_too_large`, `compact_response_too_large`, `write_failed_zero`, `write_failed_partial`, `unmatched`이다. outcome은 진단 성공 여부가 아니라 delivery/lifecycle만 뜻한다. route는 실제 path가 아니라 `GET /api/v1/health`, `GET /api/v1/checks`, `POST /api/v1/reports`, `OPTIONS /api/v1/`, `unmatched` 중 하나다.

유효한 report가 HTTP 200으로 전부 기록된 `report_finish`에만 `report_status`, `analysis_verdict`, `total_results`, `failed_results`, `finding_count`가 함께 추가된다. status/verdict는 closed enum이고 total은 1~20, failed는 total 이하이며 status 관계를 만족하고 finding은 0~40이다. 진단이 unreachable이어도 delivery가 완전하면 `outcome=ok`다. analysis 부재나 enum/count 모순 등 diagnostic tuple이 malformed이면 extension 전체를 생략하지만 valid base `report_finish`는 유지한다. incomplete/failed write에도 extension은 없다.

Positive allowlist이므로 다음 값은 **기록하지 않는다**: raw URL/path/query, `Authorization`/Bearer/API key, request/response body, target/address/IP와 client IP, provider URL·응답·오류 prose, panic text/stack. request/report ID는 server-owned opaque correlation ID이고 metric label로 사용하지 않는다. write failure는 attempted/actual byte와 `write_failed_zero|write_failed_partial`만 남기며 network error text는 제외한다.

`analysis.coverage.enrichment`도 provider ID/source/cache hit/upstream fetch/max age/fixed failure kind·count·retryable만 허용한다. IP, target, provider URL/prose, credential, precise fetch timestamp는 포함하지 않는다.

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

Compose API는 host의 `127.0.0.1:9090`, 웹은 `localhost:3000`에서 접근한다. 컨테이너 API는 `trusted-local`로 실행되며 host publish도 loopback으로 제한한다. 외부 공개 시에는 `public` 모드와 TLS reverse proxy를 사용하고 proxy 계층에도 인증/rate limit을 둔다. 기본 Compose build args `CHECKNETWORK_VERSION=dev`, `CHECKNETWORK_REVISION=dev`, `SOURCE_DATE_EPOCH=0`은 개발 identity다. release artifact로 배포하지 않는다.

API image는 non-root `65532:65532`로 실행하고 Web image는 nginx `worker_processes 2`를 사용한다. 두 service 모두 exec-form healthcheck가 있으며 Web은 API `/readyz` health를 기다린다. 운영 smoke는 `docker compose config`, `docker compose build api web`, `docker compose up -d --wait`, API `/readyz`와 Web URL 확인, `docker compose down` 순서로 수행한다.

## Release identity와 재현성

tracked `VERSION`은 `0.1.0`이다. release binary는 `-trimpath -buildvcs=false -buildid=`와 ldflags로 version 및 exact 40자리 lowercase revision을 주입한다. `/api/v1/health`의 exact schema `{"status":"ok","version":"0.1.0","revision":"<revision>"}`, `server started` JSON log, OCI `org.opencontainers.image.version`/`revision` labels가 일치해야 한다.

Dockerfile의 Go builder, Alpine traceroute extraction, runtime base는 모두 image digest로 고정된다. builder는 Go `1.22.12`를 확인하며 traceroute는 Alpine v3.20 `traceroute-2.1.5-r0.apk` URL과 SHA-256 checksum으로 고정된다. `SOURCE_DATE_EPOCH`은 wall clock이나 ambient env가 아니라 exact commit timestamp에서 만든다.

```bash
# 개발 전체 gate
make ci
# commit 후 clean exact current HEAD에서 canonical archive + 전체 CI + release를 검증
make ci-clean-archive
# clean exact current HEAD deterministic release만 검증
make verify-release
# 같은 검증 후 첫 image의 binary를 bin/checknetwork-api로 복사
make release
```

release verifier는 clean current HEAD, tracked single-line SemVer VERSION, optional archive의 embedded commit ID와 canonical `git archive` byte equality를 먼저 검증한다. 동일 source/epoch를 `--no-cache --provenance=false`로 두 번 build해 binary SHA-256, image config ID, RootFS layers를 비교하고 traceroute, `/livez`, `/readyz`, exact health/startup/OCI identity를 smoke한다. Docker CLI 및 daemon은 release에 필수이며 사용할 수 없으면 skip하지 않고 실패한다. 따라서 현재 uncommitted Stage 7 tree에서는 postcommit `make ci-clean-archive`와 release target이 의도적으로 pending이다.

Android build에는 JDK 17, platform 35와 build-tools 35.0.0이 필요하다. `make android-check`가 wrapper checksum, debug/release JVM tests, lint와 assemble을 실행한다. Emulator/physical device, TalkBack·Switch Access와 OEM share-sheet 동작은 이 gate에 포함되지 않는다.

Web 자동 tests/jsdom도 실제 Chrome/Firefox/Safari, 320/375/400 px 물리 viewport, screen reader, HTTP/2 reverse-proxy 동작을 검증하지 않는다. 실제 Android 기기/브라우저 acceptance는 release 전 별도 수동 gate이며 현재 자동화 완료로 간주하지 않는다.
