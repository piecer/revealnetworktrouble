# 테스트 및 회귀 기준

## 로컬 검증

```bash
npm --prefix frontend ci
make test                 # Go 전체 + executable Web state/DOM/topology tests
make web-test-syntax      # 모든 Web JavaScript 구문 검사
make test-race
make vet
GOFLAGS=-buildvcs=false make build
make android-wrapper-verify # Gradle 8.11.1 wrapper/JAR/distribution checksum
make android-env            # JDK 17, Android platform 35/build-tools 35.0.0
make android-test           # debug/release JVM tests + nonzero collection
make android-lint
make android-assemble       # debug + minified release
# 전체 로컬 CI gate: make ci
go test ./cmd/checknetwork-api -run 'Test.*(ConnectionAdmission|Ready|Release|Operations|Identity)'
go test ./backend/api ./backend/diagnostic -run 'Test.*(Body|Deadline|HTTP|Telemetry|LatencyDelta)'
git diff --check
```

`make ci-inner`는 fresh gate로 `npm ci`, uncached Go tests, Web tests/syntax, Go race/vet/build, Android wrapper/env/debug+release tests/lint/assemble를 한 번씩 수행한다. `make ci`는 이를 직접 호출하며 재귀 archive를 만들지 않는다. `make ci-clean-archive`는 **commit 후 clean exact HEAD SHA에서만** 실행하는 외부 acceptance다. 현재 uncommitted tree에서 실행하면 의도적으로 실패하므로 commit 전에 실행하지 않는다. `make verify-release`와 `make release`도 clean exact current HEAD 및 Docker CLI+daemon을 필수로 요구하며 Docker 부재를 skip으로 처리하지 않는다.

프런트엔드는 ESM과 Node 내장 test runner를 사용하고 `jsdom`은 `30.0.1`로 lockfile에 고정한다. state/model/renderer/DOM tests가 schema, planning, progressive ownership과 실제 DOM event를 실행한다.

## 테스트 계층

- 단위: 입력 정규화, 상태 집계, checker 오류 분류
- 통합: `httptest` 기반 API 요청·응답과 CORS
- 회귀: 성공과 일부 실패가 섞인 리포트, 잘못된 JSON, 과도한 대상 수
- 분석 계약: typed/legacy details 정규화, deterministic finding/evidence/action, confidence/coverage 분리, malformed 입력의 inconclusive 처리
- 분석 cardinality: Go가 생성·직렬화한 20 HTTPS 최대 fixture(40 findings/40 evidence/40 actions/80 available)를 Web `normalizeReport`와 Android `ReportParser`에 직접 통과시키고, 64/64/64 및 coverage 각 128의 cap+1은 양쪽에서 거부
- 생산자 정확성: traceroute 부분 출력과 command error 동시 발생, attempt별 timeout, 전체 latency
- topology link schema: optional `latency_delta_ms`의 finite/nonnegative/30000 cap, RTT 감소 시 zero clamp+omit, Android의 `latency_ms` rejection
- 런타임 경계: traceroute 30-hop/256 KiB parser 경계, 실제 subprocess overflow 취소·prefix 상한·process-group pipe 정리, Unix/Windows compile
- Windows traceroute: `tracert.exe` command spec과 세 probe parser fixture, 386/amd64/arm64 compile을 검증한다. 명령은 `CREATE_SUSPENDED`로 시작하고 `KILL_ON_JOB_CLOSE` Job Object에 할당한 뒤 primary thread를 재개한다.
- API 직렬화: non-finite 값과 실패 Marshaler가 부분 200을 만들지 않고 안정적인 `response_serialization_failed` 500을 반환
- Compact API: full 호환, transport pruning, 500/1,000, 1 MiB exclusive boundary, Geo/transaction rollback, deterministic bytes와 통계·참조 무결성
- Compact 성능: 최대 Geo fixture의 logarithmic marshal/build 횟수, linear reference와 byte-identical 최대 보존, raw/cache 불변
- 프런트엔드 model/renderer: strict schema, malformed no-fallback, bounded legacy canonicalization, fair prefix, owner-safe chunk≤100/document≤1,200, active-view unmount와 Canvas cleanup
- Web state: 독립 lane, owner/signature, stale success/finalize 차단, invalidation/cancel, error/Retry-After, ready-only report와 credential-free signature
- Web DOM/jsdom: A/B stale ownership, 입력 변경·navigation·`beforeunload`·timeout abort, 초기 deep-link focus, same-lane stale alert cleanup, ready-only export와 session credential 격리
- Web response/schema: 8 MiB `Content-Length`/stream/multibyte/cancel-failure, 누적 topology/string/container budget, 정상 최대 traceroute 수용, hostile multiplicative report 거부, non-plain/accessor 거부
- Web 보안/계층: reflected Bearer 비저장, 대소문자·겹침·report ID human redaction, XSS inert DOM, legacy analysis, compact header→analysis→folded raw 순서
- Web label/lifecycle: import 1 MiB/500 records/256 label/1024 note, 100-row pagination, A/B 격리, destroy/HMR와 timer/scheduler/import/fullscreen late-completion faults
- Web 접근성/반응형: concise live/alert/busy, native import, roving topology focus, Canvas accessible list, caption/scope 표, fullscreen focus, 320/375/400 reflow, reduced motion
- Android JVM/Robolectric: request/report/error/state contracts, debug/release network policy, lifecycle/resource/accessibility contracts. `assert-android-test-results.sh`가 debug/release 각각 0 tests를 실패로 처리한다.
- Android Stage 7: checker panic/capacity finding의 exhaustive export labels, discovery+report 단일 315초 `OperationDeadline`, discovery `min(10s, remaining)` 및 report local intersection, clock regression/cancel/zero-remaining, typed `UNSUPPORTED_CAPABILITY` 5 reasons와 fixed UI, malformed `INVALID_RESPONSE` 분리를 검증한다.
- Android build: Gradle Wrapper checksum, JDK/SDK 환경, debug/release lint와 assemble을 독립 gate로 실행한다.
- pre-handler/body admission: connection default 128/hard 256/env validation, 256 incomplete-header bounded/recovery probe, immediate-close overflow와 interruptible backoff, body effective-report default/hard 64/env, auth/rate 뒤 acquisition, declared oversize preflight, decode-only lifetime, fixed 503/recovery를 검증한다.
- checker/HTTP truth: exact completion timestamp의 parent/supervisor/report/check deadline arbitration과 no-late-healthy, 32 KiB bounded body observation, timeout/cancel 및 `response_read_failed`를 검증한다.
- operational probes: exact GET/HEAD path/method, public unauth/no-rate repeated `/livez`/`/readyz`, fixed startup/ready/drain/traceroute-unavailable bodies, startup-cached executable, temporary saturation no-flap과 business drain bypass를 검증한다.
- telemetry: delivery-only outcome, valid complete-write `report_finish`의 5 diagnostic fields, enum/count/status 관계 및 20/40 bounds, malformed tuple extension omission+base retention, privacy canary와 write/serialization/capacity paths를 검증한다.
- 운영 config: connection 1/256 accepted 및 257 rejected, body 1/64 accepted 및 65 rejected, report 16 accepted/17 rejected, checker 1/1,024 accepted 및 1,025 rejected를 확인한다. `H=5s`, `B=30s`, `E=301s`, `R=5s`, `D=5s`와 HTTP/1 식(35/336/346초), 64 KiB header를 exact field로 검증한다.
- 배포 contract: Compose 6분 stop, CPU/memory/PID, `/readyz` API health dependency, nginx worker 2, API non-root image를 source contract와 `docker compose config`로 검증한다. Docker daemon이 있으면 개발 API/Web image build와 health smoke도 수행한다.
- release contract: `VERSION=0.1.0`, exact health/startup/OCI identity, digest-pinned images, Go 1.22.12 확인, checksum-pinned traceroute APK, clean exact SHA/commit epoch/canonical `git archive`, two-build binary/image/rootfs determinism과 functional probes를 검증한다. Docker는 mandatory이며 unavailable이면 실패한다.
- CI script contract: POSIX `sh -n`, no recursive archive, no `eval`, exact 40-char lowercase SHA/current clean HEAD, trap cleanup, canonical archive equality, archive preexisting generated directory rejection, Go JSON/Node TAP/Android XML machine count를 검증한다.
- 수동: Windows/macOS/Linux에서 실행, 브라우저 UI, 실제 DNS/TCP/HTTPS 대상

외부 인터넷 대상은 수동·스테이징 시험에서만 사용한다. 자동 테스트는 로컬 리스너와 테스트 서버로 결정적이어야 한다.

## 아직 자동 gate가 대신하지 않는 acceptance

- physical Android device/emulator instrumentation, TalkBack/Switch Access, OEM share sheet/cache lifecycle
- 실제 Chrome/Firefox/Safari viewport·screen-reader·HTTP/2 reverse-proxy deadline probe
- hosted full CI와 postcommit exact-SHA `make ci-clean-archive`
- clean committed tree에서만 가능한 deterministic `make verify-release`/`make release`

따라서 JVM/jsdom/contract test 통과를 물리 기기/브라우저 검증으로 보고하지 않는다.
