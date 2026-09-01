# CheckNetwork

CheckNetwork는 기본 통신, DNS, 네트워크 경로, 해외망, 특정 서비스 상태를 한 번에 검사하고 구조화된 리포트를 만드는 멀티플랫폼 애플리케이션입니다.

백엔드는 Go로 작성된 독립 REST API이며, 진단 엔진은 다른 CLI·데스크톱·모바일 프론트엔드에서도 재사용할 수 있습니다. 웹 앱과 네이티브 Android 앱을 제공합니다.

## 빠른 시작

```bash
go run ./cmd/checknetwork-api
```

기본 `8080` 대신 다른 포트(예: `9090`)를 사용하려면 listen 주소를 지정합니다.

```bash
CHECKNETWORK_ADDR=:9090 go run ./cmd/checknetwork-api
```

Windows PowerShell에서는 다음과 같습니다.

```powershell
$env:CHECKNETWORK_ADDR=":9090"
go run ./cmd/checknetwork-api
```

다른 터미널에서:

```bash
curl http://localhost:8080/api/v1/health
curl -X POST http://localhost:8080/api/v1/reports \
  -H 'Content-Type: application/json' \
  -d '{"targets":[{"kind":"dns","address":"example.com"},{"kind":"tcp","address":"1.1.1.1:443"},{"kind":"http","address":"https://example.com"}]}'
```

웹 UI는 정적 파일 서버로 별도 실행합니다.

```bash
cd frontend
python3 -m http.server 3000
```

브라우저에서 `http://localhost:3000`을 열고 API 주소에 백엔드 주소(기본값은 `http://localhost:8080`, 위 예시는 `http://localhost:9090`)를 입력합니다.

## 검증

```bash
go test ./...
go vet ./...
```

자세한 내용은 [구현 계획](docs/PLAN.md), [아키텍처](docs/ARCHITECTURE.md), [API 명세](docs/API.md), [테스트 기준](docs/TESTING.md)을 참고하세요.

## Android 앱

Android Studio에서 `android/` 디렉터리를 열어 실행합니다. 에뮬레이터의 기본 API 주소는 `http://10.0.2.2:8080`이며, 디버그 빌드에서만 로컬 HTTP를 허용합니다. 빌드 및 배포 설정은 [Android 안내](android/README.md)를 참고하세요.
