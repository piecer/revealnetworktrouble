# README 화면 출처와 재현

2026-11-10에 별도 소스 snapshot과 소유한 loopback HTTP 서버/API 컨테이너에서 기존 `frontend/documentation.browser.cjs`를 실제 실행했다. 이미지 SHA-256, fixture·스크립트 해시, 브라우저 버전은 [provenance.json](provenance.json)에 보존한다.

## 화면별 의미

| 이미지 | 데이터와 의미 |
|---|---|
| `diagnostics.png` | Docker로 실제 빌드·추출한 Go 1.26.10 binary를 pinned Alpine base에 read-only mount하고, 실제 API에 example.com DNS/HTTPS 요청을 보낸 화면. API 서버 관측이며 사용자 기기 경로나 전체 서비스 정상의 증거가 아니다. |
| `topology.png` | `testdata/geo-details-rich-compact-report.json`을 명시적으로 interception한 2D fixture replay. 실제 외부 경로 관측이 아니다. |
| `geo-map.png` | 같은 producer fixture의 좌표로 그린 offline 지도. 실제 장비 위치나 실시간 Geo 조회 성공의 증거가 아니다. |
| `common-prefix.png` | 문서용 IPv4/IPv6 주소를 브라우저에서 로컬 CIDR 계산한 화면. 실제 공통 경로의 증거가 아니다. |

1440/320/375/400px × 6개 view/mode의 24개 사례가 실행됐다. DOM 상한·가로 overflow·script error·기본 Tab 이동을 확인했으며 브라우저의 third-party 요청은 0개였다. DNS/HTTPS 외부 요청은 API 서버가 수행하므로 이 수치를 서버 외부 통신 0건으로 해석하지 않는다.

## 재현 조건

- checkout 소스에 맞는 Go/API, Python 3 또는 동등한 정적 서버, 별도로 설치한 Playwright/Chromium이 필요하다. 사용한 조합은 Playwright 1.63.0과 Chromium 153.0.8010.12다. 다른 revision의 캐시를 임의로 연결하지 않는다.
- Web/API는 소유한 loopback 포트에만 공개하고 해당 Web origin을 API의 `CHECKNETWORK_ALLOWED_ORIGINS`로 지정한다. API `/readyz`의 실제 200을 먼저 확인한다.
- 저장소 루트에서 `PLAYWRIGHT_PATH=/absolute/path/to/playwright node frontend/documentation.browser.cjs WEB_ORIGIN API_ORIGIN OUTPUT_DIR`를 실행한다. origin은 exact loopback HTTP origin이어야 하며 OUTPUT_DIR는 새 증거 디렉터리를 사용한다.
- 기본 출력은 OUTPUT_DIR 아래 `responsive-ui.json`과 `screenshots/`다. 원본 JSON에서 passed/expectedCases/results 수를 함께 검사한다. 진단 실패도 API가 완료한 관측일 수 있으므로 스크린샷만으로 외부 접속 성공을 일반화하지 않는다.
- 이번 검증의 원본 로그·source manifest·컨테이너 소유권/정리 영수증은 `provenance.json`에 적힌 scratch 증거 경로와 상위 디렉터리에 있다. scratch는 만료될 수 있으므로 최종 릴리스에서는 CI artifact로 다시 보존한다.

이 기록은 working-tree snapshot의 실행 증거다. Docker로 빌드한 derived archive 자체를 실행한 검증, 운영 스택 재배포, clean exact-SHA 릴리스, 실제 Safari/Firefox·모바일 단말·화면 판독기 수용 시험을 대신하지 않는다. API mount compatibility와 최종 이미지 배포 acceptance를 구분한다.
