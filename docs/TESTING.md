# 테스트 및 회귀 기준

## 로컬 검증

```bash
go test ./...
go test -race ./...
node frontend/app.test.js
go vet ./...
```

## 테스트 계층

- 단위: 입력 정규화, 상태 집계, checker 오류 분류
- 통합: `httptest` 기반 API 요청·응답과 CORS
- 회귀: 성공과 일부 실패가 섞인 리포트, 잘못된 JSON, 과도한 대상 수
- 분석 계약: typed/legacy details 정규화, deterministic finding/evidence/action, confidence/coverage 분리, malformed 입력의 inconclusive 처리
- 생산자 정확성: traceroute 부분 출력과 command error 동시 발생, attempt별 timeout, 전체 latency
- 프런트엔드 회귀: 동일 노드 재합류, 네트워크 계층 집계, 연속 무응답 홉 folding
- 수동: Windows/macOS/Linux에서 실행, 브라우저 UI, 실제 DNS/TCP/HTTPS 대상

외부 인터넷 대상은 수동·스테이징 시험에서만 사용한다. 자동 테스트는 로컬 리스너와 테스트 서버로 결정적이어야 한다.
