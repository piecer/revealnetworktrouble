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

Android release는 빈 API base로 시작해 운영 HTTPS origin을 명시적으로 요구한다. debug에서만 emulator/localhost HTTP를 허용한다. Android Bearer는 Activity/session 메모리에만 존재하고 saved state나 일반 preferences에 쓰지 않는다. 원본 report 공유는 확인 대화상자 뒤 private cache `FileProvider` URI로만 수행한다. process singleton은 한 번에 한 prepare만 받고 queue하지 않으며 모든 UTF-8 sizing/file I/O를 off-main worker에서 수행한다. file당 8 MiB, recognized live set 2 files/16 MiB, reconciliation step당 32 entries/32 deletes다. startup, 새 요청/replacement, final Activity owner retirement와 explicit cleanup retry가 artifacts를 정리한다. cleanup 실패는 새 share를 닫고 owner/raw secret reference를 먼저 제거한다.

`FileProvider`는 exported=false이고 `cache/shared-reports/`만 노출하며 chooser에는 read grant만 준다. raw URI lease 15분은 wall clock expiry를 앱 프로세스가 `observe()`한 경우 revoke/delete하는 **process-observed** policy다. process death 뒤 stock provider grant expiry, recipient의 already-open descriptor 또는 복사본은 제어하지 못한다. 이 동작은 emulator/physical-device process-death acceptance 전까지 보장으로 확대하지 않는다.

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

checker/GeoIP worker가 context cancellation을 지키지 않으면 Go process가 그 goroutine 또는 injected transport를 강제로 종료할 수 없다. request는 budget에서 synthetic timeout/cancel로 반환되지만 worker는 실제 종료 때까지 active slot을 점유한다. HTTP drain과 checker supervisor shutdown은 하나의 346초 end-to-end deadline을 공유한다. deadline 직후 supervisor는 같은 lock에서 `0..capacity`(configured hard max 1,024)의 bounded `active`, `stuck`, `remaining`(`remaining=active`)을 atomic하게 capture한다. 어느 값이든 nonzero이면 fixed reason의 ERROR를 한 번 기록하고 runMain/프로세스는 exit 1을 반환한다. HTTP shutdown error와 checker drain error는 같은 deadline 안에서 join하며 incomplete drain에 “completed” INFO를 출력하지 않는다. barrier를 나중에 release하면 active/stuck accounting은 0으로 회복하지만 이미 반환한 nonzero status는 변경하지 않는다.

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
- startup에서 traceroute 실행 파일이 없거나 호환 가능한 command grammar를 선택하지 못함: `503 {"status":"not_ready","reason":"traceroute_unavailable"}`

Startup은 version 문자열을 판정하지 않는다. Trusted deployment input인 `PATH`에서 exact platform 실행 파일(Unix `traceroute`, Windows `tracert.exe`)을 한 번 resolve하고 `127.0.0.1`만 대상으로 fixed candidate grammar를 기능 probe한다. 전체 probe deadline은 2초, stdout+stderr 합계는 32 KiB이며 timeout/overflow/nonzero/해석 불가/loopback 미도달은 그 candidate를 거부한다. Unix는 먼저 Linux iputils/traceroute, Alpine BusyBox와 Darwin에서 공통인 numeric grammar `-n -q 1 -w 2 -m 30`을 시도하고, 그 옵션을 거부하는 GNU inetutils-compatible grammar `-q 1 -w 2 -m 30`을 시도한다. 성공한 exact 실행 경로와 grammar는 immutable capability로 startup에서 한 번 캐시되어 readiness와 모든 실제 traceroute 실행에 함께 사용된다. 실행 파일 부재나 모든 grammar 실패는 nil capability로 fail closed한다. Runtime PATH lookup이나 다른 grammar fallback은 없으며 service가 다른 route를 제공하더라도 readiness는 계속 `traceroute_unavailable`이다. Web/Android screen/export의 exact fixed copy는 `Traceroute capability was unavailable, so no route observation was established.`이고 raw server message/target을 반사하지 않는다. Application probe가 전달하는 destination은 loopback뿐이며 GeoIP나 임의 target을 조회하지 않는다.

보안 경계: 이 기능 probe는 `PATH`에서 찾은 실행 파일을 실제로 시작한다. timeout, output cap, process-group/descendant cleanup은 정지하지 않거나 noisy한 프로세스를 제한하지만 악성 실행 파일을 sandbox하지 않으며 종료 전에 수행한 side effect를 되돌릴 수 없다. 운영 image와 `PATH`의 traceroute binary는 trusted deployment input이어야 한다. 이름이 같은 임의 사용자 제공 파일이 먼저 오는 `PATH`로 service를 시작하지 않는다.

Readiness는 외부 네트워크/GeoIP/임의 target을 호출하지 않고 일시적인 connection/report/body/checker capacity 포화에도 변하지 않는다. shutdown은 먼저 irreversible atomic drain phase로 전환한다. 이후 `/livez`만 200을 유지하고 `/readyz`는 operational 503을 반환한다. Business route는 outer wrapper가 raw response를 쓰지 않고 API로 전달하며 closed **route → public auth → rate limit → server_draining → body decode → report/checker admission** 순서를 지킨다. 따라서 unauthorized/rate-limited request는 그 canonical error를 먼저 받고, drain rejection은 body를 읽거나 report/checker/SSRF work를 시작하지 않은 채 fixed `503 server_draining`과 canonical `Retry-After`로 닫힌다. Compose API healthcheck는 `/readyz`다.

## Telemetry 및 privacy

JSON log `msg=api_telemetry`의 event allowlist는 `http_terminal`, `report_submit`, `report_admit`, `report_reject`, `report_start`, `report_computed`, `report_finish`, `report_cancel`이다. base payload fields는 `event`, `outcome`, `request_id`, `report_id`, `method`, `route`, `status`, `active`, `capacity`, `request_bytes`, `response_attempted_bytes`, `response_bytes`, `duration_ms`, `runner_duration_ms`, `marshal_duration_ms`, `write_duration_ms`이며 `msg`는 항상 `api_telemetry`다. outcome allowlist는 `ok`, `unauthorized`, `rate_limited`, `invalid_json`, `invalid_request`, `request_too_large`, `server_capacity_unavailable`, `server_draining`, `body_decode_capacity_unavailable`, `checker_capacity_unavailable`, `write_capacity_unavailable`, `policy_blocked`, `timeout`, `cancelled`, `panic_safe_failure`, `serialization_failed`, `full_response_too_large`, `compact_response_too_large`, `write_failed_zero`, `write_failed_partial`, `unmatched`이다. outcome은 진단 성공 여부가 아니라 delivery/lifecycle만 뜻한다. route는 실제 path가 아니라 `GET /api/v1/health`, `GET /api/v1/checks`, `POST /api/v1/reports`, `OPTIONS /api/v1/`, `unmatched` 중 하나다.

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

Compose API는 host의 `127.0.0.1:9090`, 웹은 `localhost:3000`에서 접근한다. 컨테이너 API는 `trusted-local`로 실행되며 host publish도 loopback으로 제한한다. 외부 공개 시에는 `public` 모드와 TLS reverse proxy를 사용하고 proxy 계층에도 인증/rate limit을 둔다. 기본 Compose build args의 exact 개발 sentinel은 `CHECKNETWORK_VERSION=0.1.0-dev`, `CHECKNETWORK_REVISION=0000000000000000000000000000000000000000`, `SOURCE_DATE_EPOCH=0`이며 API/Web에 동일하게 전달된다. zero revision은 개발 전용 sentinel이고 실제 commit 또는 release provenance가 아니므로 release artifact로 배포하지 않는다. 명시적인 `CHECKNETWORK_VERSION`, `CHECKNETWORK_REVISION`, `SOURCE_DATE_EPOCH` override는 Compose가 그대로 두 service에 전달한다.

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
# clean exact current HEAD에서 production verifier를 umask 077/027/000으로 세 번 실행하는 exact release gate
make verify-release-real REVISION=<exact-40-character-HEAD-SHA>
# opt-in actual Buildx archive component regressions (shared daemon inventory readback 포함;
# verify-release-real의 exact production workflow를 대신하지 않음)
make verify-api-archive-real
make verify-web-archive-real
make verify-archives-real
```

release verifier는 clean current HEAD, tracked single-line SemVer VERSION, optional archive의 embedded commit ID와 canonical `git archive` byte equality를 먼저 검증한다. Caller와 무관하게 private temp/captured stderr/archive는 `0700`/`0600`으로 유지하고 canonical extraction만 build-facing umask `022`를 사용한다. 추출 root는 다시 `0700`이며 `nginx.conf`와 six Web assets는 build 전에 exact `0644`여야 한다. 동일 private canonical source/commit epoch에서 API를 concrete `docker buildx build --no-cache --provenance=false --output=type=docker,dest=<archive>,rewrite-timestamp=true .`로 두 번 build한다. 두 tagless archive의 bytes와 validator schema `archive/config/manifest/rootfs/layers/binary/traceroute/go_version/go_path` 전체가 같아야 하고 두 추출 binary/traceroute bytes도 각각 같아야 한다.

`verify_api_archive.py`의 hard bounds는 archive 512 MiB, outer/layer members 4,096, member 256 MiB, outer member total 512 MiB, layers 64, compressed layer 256 MiB, expanded layer 512 MiB, expanded selected-file total 128 MiB다. Exact pinned Alpine layer digest, diff-ID와 history prefix를 **layer tar member 해석 전에 trusted base**로 확정한다. Tagless single manifest/config/OCI graph만 허용하고 traversal/noncanonical/duplicate/unreferenced ambiguity, post-base link/device/FIFO/socket, malformed whiteout와 wrong runtime identity를 거부한다. OCI whiteout은 layer-local marker set이다: root opaque는 lower entries 전체, directory opaque는 lower descendants, ordinary whiteout은 lower target/descendants만 제거하고 marker와 같은 layer에서 생성된 entry는 tar 순서와 무관하게 보존한다. 제거된 canonical selected file은 같은 layer 또는 이후 layer에서 재생성되어야 한다. Final selected filesystem은 executable `0755` regular `/checknetwork-api`와 `/traceroute` 두 파일만 허용하며 traceroute pinned APK payload SHA와 Go build version/path/revision을 검사한다.

각 pass의 selected files는 미리 만든 caller-owned empty real directory를 dir-fd로 열고 no-follow + create-exclusive로 생성한 뒤 exact mode, complete write, fsync, no-follow readback size/hash를 확인한다. 실패 시 그 invocation이 생성한 파일만 제거한다. `CHECKNETWORK_RELEASE_OUTPUT` publication은 destination-local temp/backup, immutable extracted source copy, mode `0755`, original-tree revalidation, atomic rename과 stateful rollback을 사용하며 symlink/directory destination을 거부한다.

API verifier가 shared daemon에서 소유하는 role은 API network와 `smoke` container **정확히 두 개**다. High-entropy name의 nonce label과 별도 independent owner label을 쓰고 daemon call 전에 `create_pending`으로 전환한다. 정상 create response 또는 daemon-success/CLI-response-loss recovery로 얻은 immutable ID를 exact name+labels와 대조한다. Cleanup은 name absence를 명시적으로 확인해야만 성공하고, exact-owned resource가 있으면 immutable ID로 제거한 뒤 name absence를 다시 확인한다. Inspect/remove 결과가 모호하거나 identity mismatch/replacement이면 caller resource를 보존하면서 cleanup 실패로 닫는다. Pending response-loss 상태도 exact name+labels+ID를 모두 재확인하거나 name absence를 확인할 때만 정리된다. Borrowed pinned Alpine base에 검증·추출된 binary와 traceroute를 read-only mount해 traceroute, `/livez`, `/readyz`, health/startup identity를 확인하지만 이는 mount compatibility만 증명하고 derived API archive 실행을 증명하지 않는다.

Web도 같은 revision/version/epoch와 exact release platform `linux/amd64`를 사용한다. Derived command contract는 `docker buildx build --platform linux/amd64 --no-cache --provenance=false --output=type=docker,dest=<archive>,rewrite-timestamp=true frontend`이고 두 output은 tagless(`RepoTags` absent/null)여야 한다. Pinned base archive도 `docker image save`가 아니라 mode `0700` private generated context의 유일한 Dockerfile `FROM nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10`에서 같은 platform/Buildx exporter로 두 번 만든다. 이 Dockerfile에는 `COPY`가 없고 project/ambient bytes가 base context에 들어가지 않는다. Base는 revision-independent이므로 canonical `SOURCE_DATE_EPOCH=0`을 사용한다. 이는 2025 pinned-layer/config timestamps를 바꾸지 않으면서 outer descriptor/tar timestamps를 고정하며, 두 base archive bytes가 다르면 실패한다. Web hard bounds는 archive 512 MiB, outer/layer members 4,096, outer/layer member 256 MiB, outer regular-member declared-byte total 512 MiB, layers 64, compressed/expanded layer 256 MiB, final selected-file total 32 MiB다. Outer member declared size를 stream 순서로 retain/read 전에 합산하여 compressed archive의 aggregate expansion도 512 MiB에서 닫는다. Exact outer regular graph는 `manifest.json` + 그 manifest가 참조한 config/layers + optional coupled `index.json`/`oci-layout` + index가 참조한 single OCI manifest blob이다. Actual tagless Buildx shape에 불필요한 `repositories`와 모든 unreferenced regular은 금지한다. Explicit directory는 referenced blobs의 ancestor `blobs`/`blobs/sha256`만 허용하고 implicit tar parents는 요구하지 않으며, unknown directory와 모든 outer symlink/hardlink/device/FIFO/socket을 거부한다. `verify_web_archive.py`는 base archive가 tagless single-platform `linux/amd64`인지와 exact pinned nginx config digest, ordered layer digest/diff-ID, canonical history digest를 먼저 인증하고 derived config의 pinned nginx config/layer/rootfs/history prefix까지 **어떤 layer tar member도 순회하기 전에** 확인한다. 그 뒤에만 authenticated inherited base의 canonical contained-target symlink/hardlink를 허용한다. Base에서 불필요한 device/FIFO/socket과 selected special은 거부하고, 모든 post-base symlink/hardlink/device/FIFO/socket은 selection 전에 거부한다. 즉 폐쇄 정책 토큰은 pinned nginx config/layer/rootfs/history prefix 및 post-base symlink/hardlink/device/FIFO/socket 이다. Archive size/member/layer/expanded-file budgets, path traversal·duplicate, manifest/config ambiguity, tags, invalid whiteout도 offline 거부한다. OCI layer order와 `.wh.<name>`/`.wh..wh..opq`를 적용해 canonical `/etc/nginx/nginx.conf`와 Web root의 정확한 six files(`app.js`, `index.html`, `state.js`, `styles.css`, `topology-model.js`, `topology-renderer.js`) bytes/modes만 남는지 확인한다. 두 build의 archive/config/manifest/rootfs/asset-manifest digests가 같아야 한다.

**Derived API archive와 derived Web archive는 모두 shared daemon에 load, daemon-tag, import, delete 또는 execute하지 않는다.** 따라서 둘 다 derived daemon image/tag/cleanup role이 없다. Web verifier가 소유하는 daemon role은 internal network와 smoke container 정확히 두 개이고 runtime smoke의 pinned nginx base image/cache는 borrowed resource다. Canonical nginx config와 six assets를 read-only bind mount하므로 pinned base가 같은 bytes를 serve함만 증명하고 derived Web archive 실행을 증명하지 않는다. 두 owned role은 random 128-bit nonce name과 independent owner token labels를 사용한다. create 호출 **전에** pending state를 기록하고, normal response의 ID 또는 daemon-success/CLI-response-loss 뒤 exact name+nonce+owner lookup으로 immutable ID를 확보한다. API/Web cleanup은 confirmed name absence만 성공으로 취급하며 exact-owned present 상태는 immutable ID 제거 뒤 absence를 재검사한다. Daemon/inspect/remove ambiguity와 identity mismatch/replacement는 caller를 보존한 채 cleanup 실패다. 성공 line과 publication commit 전에 네 owned resource 모두 이 barrier를 통과해야 하며 실패하면 publication을 exact rollback하고 nonzero로 끝난다. EXIT cleanup은 idempotent하고 ordinary error는 nonzero를 유지하며 HUP/INT/TERM은 cleanup 실패에도 129/130/143과 고정된 비식별 cleanup diagnostic을 유지한다.

`verify-release-real` production gate는 umask 077/027/000 세 pass의 output을 비교한다. Shared-daemon closure는 canonical Docker inspect JSON projection(image ID/tags/digests, container immutable ID/name/image/sorted config labels/sorted attachments, network ID/name/driver/scope/sorted labels/sorted membership endpoints)을 사용한다. Exact fake gate는 12 cases이며 uptime/status와 map formatting은 제외하지만 caller replacement, label, attachment/membership 변화와 release-label residue는 실패한다. Production child 실패는 captured stderr를 private temp에 두고 fixed `production release verification failed`만 출력하며, 모든 pass 성공 시 정상 build stderr를 전달한다.

성공 output은 API `api_archive`, `api_config`, `api_manifest`, `api_rootfs`, `api_binary`, `api_traceroute` 여섯 field와 Web `archive`, `config`, `manifest`, `rootfs`, `asset_manifest` 다섯 field다. Docker CLI/daemon/buildx 또는 required pinned base cache 부재는 skip이 아니라 실패다. Stage 8 historical source inventory는 Web 260 tests(456 semantic mutations including 13 generic cancelled-detail rejections), producer findings 23/result shapes 31/expanded semantic result matrix 136, presentation `6/10/21/23/33`, Android debug/release 각각 direct-child XML 297 tests + variant canaries 2, archive validators API 28 + Web 25 = 53 tests, exact real release-gate fake cases 12다. Compose SemVer fix 뒤 current source inventory는 Web 298 tests다. 최신 parent focused gates와 Task 1~15 task-level reviews는 blocker 0 / major 0이지만 documentation edits 뒤 final clean-environment `make ci`는 아직 pending이다. Repository root의 ignored `checknetwork-api` binary는 candidate manifest/검증에서 제외되어 있으나 비파괴 요청 때문에 제거하지 않고 retained 상태다. New manifest/temp release/five-way review/commit 및 postcommit exact-SHA `make ci-clean-archive`, `make verify-release`, `make release`도 pending이며 final claim이 아니다. 이전 임시 manifest version/record count/hash는 current evidence가 아니다.

## Stage 8 manifest/commit 운영 순서

Stage 8 candidate의 required parent는 `36dc1dca9838337a5b5d1cf6ebc76bb35f5a4243`이다. 최종 documentation edit와 fresh `make ci`, generated-artifact cleanup 뒤 **어떤 temporary commit보다 먼저** `${TMPDIR}/stage8-manifest.jsonl`을 repository 밖에 생성한다. canonical UTF-8 JSONL header는 exact parent를 포함하고 add/modify/delete records는 raw path bytes의 unpadded base64 순, operation rank, normalized `0644|0755`, byte size/SHA-256로 닫힌다. saved manifest bytes의 SHA-256과 candidate exact path/content/mode set을 확인한 다음에만 isolated clone에서 base의 direct-child temporary commit을 materialize한다. temporary tree fingerprint/parent, supplied canonical archive release, independent reviews가 모두 같은 manifest를 가리켜야 한다. 이 순서 전에는 Stage 8 final commit 또는 SHA를 주장하지 않는다.

Android build에는 system JDK 17, platform 35와 build-tools 35.0.0이 필요하다. `JAVA_HOME`이 undefined이면 canonical `make`가 `PATH`의 executable `javac`에서 portable JAVA_HOME을 자동 유도한다. Plain `readlink`로 absolute/relative symlink chain과 공백·shell metacharacter path를 lexical하게 최대 40 hops까지 해석하고 `readlink -f`/`realpath`에는 의존하지 않는다. Cycle, cap 초과, dangling/non-executable terminal은 빈 derived value로 닫혀 JDK 검사에서 실패한다. Caller가 명시한 빈 값/잘못된 값을 포함한 `JAVA_HOME`은 자동 fallback 없이 그대로 보존하고 fail closed한다. SDK precedence는 명시 `ANDROID_HOME` > 명시 `ANDROID_SDK_ROOT` > valid `~/Android/Sdk` > valid project-managed `~/.local/share/checknetwork-android/sdk`다. 첫 explicit override가 빈 값/잘못된 값이어도 다음 후보로 fallback하지 않고 두 SDK 변수를 그 exact 값으로 export한 뒤 strict platform/build-tools 검사에서 실패한다. `make android-check`가 wrapper checksum, debug/release JVM tests, lint와 assemble을 실행한다. Emulator/physical device, TalkBack·Switch Access와 OEM share-sheet 동작은 이 gate에 포함되지 않는다.

Web 자동 tests/jsdom도 실제 Chrome/Firefox/Safari, 320/375/400 px 물리 viewport, screen reader, HTTP/2 reverse-proxy 동작을 검증하지 않는다. 실제 Android 기기/브라우저 acceptance는 release 전 별도 수동 gate이며 현재 자동화 완료로 간주하지 않는다.
