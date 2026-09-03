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

production은 explicit `net.Listen`을 default 128/hard 256의 leased listener로 감싼 뒤 `http.Server.Serve`에 전달한다. 따라서 accepted connection token은 handler goroutine보다 먼저 획득되고 실제 connection `Close`에서 exactly once 반환된다. overflow socket은 즉시 close되고 accept loop는 close/shutdown으로 중단 가능한 5 ms backoff를 거친다.

Authenticated body-decode admission, report admission과 response-write admission은 서로 분리된다. body slot은 route/auth/rate 확인 및 declared oversize preflight 뒤 nonblocking으로 획득하고, `MaxBytesReader`에서 JSON object 하나와 EOF를 decode하는 동안만 유지한다. 기본 body capacity는 effective report limit이고 hard max는 64이며 포화는 고정 `body_decode_capacity_unavailable` 503이다. report slot은 checker 실행과 bounded serialization까지만 소유하고, 최대 16개의 독립 write token을 얻은 뒤 report slot을 반환한다. write token이 없으면 큰 payload를 보존/queue하지 않고 작은 `write_capacity_unavailable` 응답을 시도한다. API는 request ID를 auth 이전에, report ID를 실행 이전에 생성하며 submit/admit/reject/start/computed/finish/cancel과 모든 HTTP terminal event에서 같은 opaque ID를 사용한다.

Traceroute command/parser는 Unix `traceroute`와 Windows `tracert.exe` adapter를 분리한다. compact 요청에서는 Runner가 raw results 전체로 `analysis`를 먼저 만든 다음 deterministic compact graph를 생성한다. API transport는 raw report를 mutate하지 않는 copy에서 attempts와 대표 topology만 제거하고 final JSON을 header 이전에 marshal한다. Geo suffix와 route transaction을 단조 이진 탐색으로 축소해 1 MiB 미만을 보장한다.

Web은 strict compact schema, compact-first/legacy-bounded model, owner-safe progressive coordinator로 분리된다. topology/Geo/labels 중 active view만 dynamic DOM을 소유한다. topology item은 실제 element 하나, Geo path는 Canvas semantic data, labels는 bounded page로 materialize하며 commit 직전에 실제 document/chunk element 수를 검사한다. 앱 state와 listener는 `createApp` instance에 귀속되고 `destroy()`가 request, timer, scheduler, Canvas와 listener를 정리한다.

Android는 `MainActivity` presentation과 Android-free `core`, `network`, `state`를 분리한다. production session은 bounded `/checks` discovery로 server capability를 확인한 다음 report POST를 수행한다. 한 `OperationDeadline`이 두 단계의 315초 monotonic budget을 소유하고 discovery local 10초 및 request별 report deadline과 매 지점 교차한다. `RequestCoordinator`의 owner/signature가 교체·입력 무효화·취소·recreation 후 stale callback을 차단하며 transport는 Content-Length/stream 8 MiB와 exactly-once disconnect를 소유한다. valid reduced capability mismatch는 closed `UNSUPPORTED_CAPABILITY` reason으로 전달하고 malformed response는 `INVALID_RESPONSE`로 유지한다. Activity는 fixed reason별 문구만 표시하고 bounded form만 saved state에 기록하며 Bearer와 raw report는 retained memory 밖으로 직렬화하지 않는다. 사람용 redacted text와 확인 후 생성하는 private-cache raw content URI를 별도 공유한다.

`internal/api`는 HTTP 전송만 담당하고, `diagnostic` 패키지는 프로토콜이나 UI를 알지 못한다. 새 프론트엔드는 API만 사용하며, 새 검사 방식은 `Checker` 인터페이스 구현으로 추가한다.

진단 checker가 만든 `Result`는 먼저 typed normalized facts로 변환한 뒤 결정적 analysis 규칙에 입력된다. 분석은 backend의 자유 형식 오류 message를 파싱하지 않고 stable `error_code`와 구조화된 details만 사용한다. `confidence`는 원인 확률이 아니라 finding 문장을 지지하는 evidence의 직접성이고, 관측 불가·malformed·legacy 데이터는 별도 coverage limitation으로 보존한다.

서비스는 checker supervisor 하나를 공유한다. slot은 goroutine 생성 전에 획득하며 checker가 실제 return/panic할 때만 반환된다. non-traceroute checker가 끝나면 return 직후 기록한 timestamp를 parent/supervisor cancellation → report deadline → checker deadline → returned result 순으로 arbitration한다. timestamp가 deadline과 같거나 늦으면 healthy result를 commit하지 않는다. cleanup cancellation은 arbitration 뒤에만 발생한다. request budget이 먼저 끝나면 collector는 deterministic timeout/cancel result를 반환하고 late result가 이미 반환된 report를 mutate하지 못한다. HTTP(S) checker도 header만으로 성공하지 않고 최대 32 KiB body observation을 완료한 뒤 latency/status를 확정하며 read failure를 `response_read_failed`로 보존한다. Go는 context를 무시하는 checker나 injected HTTP transport를 강제 종료할 수 없다. 이런 noncooperative worker는 shutdown 뒤에도 남을 수 있고 실제 반환 때까지 checker/Geo active slot을 점유하므로, bounded shutdown은 `remaining`/`stuck`을 보고할 뿐 강제 종료를 주장하지 않는다.

GeoIP lookup은 프로세스 전체 active 8, queued 64로 제한되고 같은 IP generation의 waiter는 coalesce된다. abandoned generation은 successor를 지우거나 cache를 overwrite할 수 없다. 성공만 2,048-entry/24-hour LRU에 clone해 저장한다. 결과의 privacy-safe source/failure aggregate는 topology details에서 분석의 `coverage.enrichment` schema로 승격된다.

HTTP/1 timeout 모델은 `H=5s`, `B=30s`, `E=301s`, `R=5s`, `D=5s`이다. 따라서 accept 기준 `ReadTimeout=H+B=35s`, post-header `WriteTimeout=B+E+R=336s`이며 HTTP drain과 checker supervisor shutdown은 하나의 end-to-end `ShutdownTimeout=H+B+E+R+D=346s`를 공유한다. 정상 client deadline 315초는 `E+R=306s`에 DNS/connect/TLS/upload/download/scheduler용 9초를 더한 값이며 hostile slow-body `B`를 더하지 않는다. Go HTTP/2에서 `WriteTimeout`의 stream/deadline 의미는 HTTP/1과 같다고 보장할 수 없으므로 TLS gateway/실제 브라우저 조합을 별도 probe해야 하며, 이 산술식은 HTTP/1 server contract이다.

Operational handler는 business middleware의 바깥에서 exact GET/HEAD `/livez`와 `/readyz`만 처리한다. readiness는 startup-cached traceroute executable availability와 irreversible startup/accepting/draining phase의 fixed DTO이며 transient saturation이나 외부 lookup을 관찰하지 않는다. drain transition은 먼저 business admission을 닫고 readiness를 503으로 바꾼 뒤 HTTP와 checker가 공유하는 346초 deadline으로 종료한다. Compose는 `/readyz`를 healthcheck로 사용한다.

Telemetry `outcome`은 delivery/lifecycle 전용이다. 완전한 HTTP 200 report write의 `report_finish`만 검증된 `report_status`, `analysis_verdict`, total/failed/finding counts를 조건부 확장한다. tuple이 없거나 모순이면 확장을 생략하고 base event는 유지한다. typed positive allowlist 외 raw request/diagnostic/provider/error 값은 구조상 입력할 필드가 없다.

Release identity는 tracked `VERSION=0.1.0`, exact revision ldflags, startup log, health DTO와 OCI labels를 한 source of truth로 묶는다. pinned builder/traceroute/runtime images와 checksum-pinned traceroute APK, empty build ID, trimpath, commit-derived epoch를 사용하고 release verifier가 canonical Git archive 및 2회 build digest를 비교한다. 개발 실행/Compose 기본 build args의 `dev`는 release identity가 아니다.

## 일관성 기준

- 모든 공개 JSON 필드는 `snake_case`를 사용한다.
- 시간은 UTC RFC3339, 기간은 밀리초 정수로 표현한다.
- 모든 검사는 `context.Context` 취소와 타임아웃을 지킨다.
- 사용자에게 반환하는 실패는 안정적인 `error_code`와 설명을 함께 가진다.
- 한 검사 실패가 다른 검사를 중단시키지 않는다.
- 테스트는 외부 인터넷, 특정 DNS 설정, 관리자 권한에 의존하지 않는다.
- 로그에 요청 본문, 자격 증명, 전체 URL 쿼리를 남기지 않는다.
- compact response는 nodes 500, links 1,000, body 1 MiB 미만이고 Web commit은 chunk 100, document 1,200 elements를 넘지 않는다.
- Android response도 UTF-8 8 MiB에서 중단하고 strict report/analysis/compact schema를 atomic하게 검증한 뒤 현재 owner에게만 게시한다.

## 보안 기준

기본 API는 `127.0.0.1:8080`의 `trusted-local` 모드이며 사설망 진단을 허용한다. 공개 인터넷에서는 `public` 모드가 API key와 source IP rate limit을 요구하고, DNS 재해석과 HTTP redirect를 포함한 실제 dial 직전에 사설·loopback·link-local·metadata·특수 목적 주소를 차단한다. CORS는 인증으로 취급하지 않는다. 요청 본문은 1 MiB, 대상은 요청당 20개, 동시 report는 기본 4개로 제한한다. 운영에서는 TLS gateway의 인증/rate limit도 중첩한다.

- header parser는 64 KiB, accepted connection은 기본 128/hard max 256, body decode는 effective report default/hard max 64, report 설정 hard max는 16, checker는 기본 80/hard max 1,024, GeoIP는 active 8/queued 64/cache 2,048, response write hard max는 16이다.
- full response는 newline 포함 8 MiB 이하이고 compact response는 1 MiB 미만이다.
