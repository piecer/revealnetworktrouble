# 테스트 및 회귀 기준

## 로컬 검증

```bash
npm --prefix frontend ci
make test                 # Go 전체 + executable Web state/DOM/topology tests
make web-test-syntax      # 모든 Web JavaScript 구문 검사
make test-race
make vet
GOFLAGS=-buildvcs=false make build
git diff --check
```

프런트엔드는 ESM과 Node 내장 test runner를 사용하고 `jsdom`은 `30.0.1`로 lockfile에 고정한다. `app.test.js`의 기존 topology/map 회귀를 유지하면서 `state.test.js`와 `dom.test.js`는 실제 module과 DOM event를 실행한다.

## 테스트 계층

- 단위: 입력 정규화, 상태 집계, checker 오류 분류
- 통합: `httptest` 기반 API 요청·응답과 CORS
- 회귀: 성공과 일부 실패가 섞인 리포트, 잘못된 JSON, 과도한 대상 수
- 분석 계약: typed/legacy details 정규화, deterministic finding/evidence/action, confidence/coverage 분리, malformed 입력의 inconclusive 처리
- 생산자 정확성: traceroute 부분 출력과 command error 동시 발생, attempt별 timeout, 전체 latency
- 프런트엔드 회귀: 동일 노드 재합류, 네트워크 계층 집계, 연속 무응답 홉 folding
- Web state: 독립 lane, owner/signature, stale success/finalize 차단, invalidation/cancel, error/Retry-After, ready-only report와 credential-free signature
- Web DOM/jsdom: A/B stale ownership, 입력 변경·navigation·`beforeunload`·timeout abort, 초기 deep-link focus, same-lane stale alert cleanup, ready-only export와 session credential 격리
- Web response/schema: 8 MiB `Content-Length`/stream/multibyte/cancel-failure, 누적 topology/string/container budget, 정상 최대 traceroute 수용, hostile multiplicative report 거부, non-plain/accessor 거부
- Web 보안/계층: reflected Bearer 비저장, 대소문자·겹침·report ID human redaction, XSS inert DOM, legacy analysis, compact header→analysis→folded raw 순서
- Web 접근성/반응형: hash focus, live/alert/busy, accordion keyboard, native import, named topology SVG, caption/scope IP 표, 320/375/400 reflow, component scroll, double focus ring, reduced motion
- Web 기존 기능: topology filter/labels, IP import/edit, Geo/CARTO wiring과 환경 설정 우선순위
- 수동: Windows/macOS/Linux에서 실행, 브라우저 UI, 실제 DNS/TCP/HTTPS 대상

외부 인터넷 대상은 수동·스테이징 시험에서만 사용한다. 자동 테스트는 로컬 리스너와 테스트 서버로 결정적이어야 한다.
