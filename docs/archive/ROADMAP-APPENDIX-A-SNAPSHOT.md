# ROADMAP 부록 A 스냅샷 — 직접 확인한 실행 결과

ROADMAP live 문서에서 이동했다. 이 block은 작성 시점 관측 기록이고 현재 reading이 아니며, 현재 cardinality는 gate를 실행해 관측한다(정책: `backend/diagnostic/documentation_contract_test.go`).

## 부록 A. 직접 확인한 실행 결과 요약

```text
go build ./...                      OK
go vet ./...                        OK
go test ./...
  backend/api                       ok   (12.1s)
  backend/diagnostic                FAIL TestRunTraceCommandNormalParentExitKillsChildAndGrandchildHoldingPipes
                                         "descendant PID ... survived runTraceCommand"
                                         (ps: sh/sh/sleep 이 PPID 1 의 Z 상태 — 종료되었으나 미수거)
  cmd/checknetwork-api              FAIL TestComposeResolvedExposureBoundary
                                         exec: "docker": executable file not found in $PATH
                                    FAIL TestComposeResolvedBuildIdentityIsValidSharedAndOverrideable
                                         docker compose config: exec: "docker": not found
                                    FAIL TestCleanArchiveDisablesAmbientVCSStampingWithoutDroppingCallerGOFLAGS
                                         go: downloading go1.25 ... lookup proxy.golang.org: no such host
npm ci && npm test (Node 22.23.4)   tests 892 / pass 891 / skipped 1 / fail 0 / 64.4s
API smoke (127.0.0.1)               /readyz 200, /api/v1/health 200, /api/v1/checks 13 kinds
                                    POST /api/v1/reports (tcp ok, http ok, tcp closed, dns localhost)
                                      -> 200, 4ms, 2,389B, status=degraded, analysis.verdict=attention
                                    CORS: localhost:3000 / 127.0.0.1:3000 허용, 임의 origin 거부
                                    Headers: Cache-Control: no-store, X-Content-Type-Options: nosniff
```
