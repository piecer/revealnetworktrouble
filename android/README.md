# CheckNetwork Android

Go 진단 API를 사용하는 네이티브 Android 클라이언트입니다. Android 8.0(API 26) 이상을 지원합니다.

## 실행

1. Android Studio에서 `android/` 디렉터리를 엽니다.
2. JDK 17과 Android SDK 35가 선택되어 있는지 확인합니다.
3. 백엔드를 저장소 루트에서 `go run ./cmd/checknetwork-api`로 실행합니다.
4. `app`을 디버그 빌드하여 에뮬레이터에서 실행합니다.

에뮬레이터의 기본 API 주소 `http://10.0.2.2:8080`은 호스트 PC의 `localhost:8080`을 가리킵니다. 실기기에서는 같은 네트워크에 있는 개발 PC의 IP 또는 배포된 HTTPS API 주소를 입력해야 합니다.

## 보안 및 배포

- 디버그 빌드만 로컬 HTTP 통신을 허용합니다.
- 릴리스 빌드는 HTTPS만 허용합니다. 운영 API는 유효한 TLS 인증서를 사용해야 합니다.
- 릴리스 APK/AAB 생성 전 `versionCode`, `versionName`, 서명 구성을 배포 환경에 맞게 설정합니다.

## 기능

- DNS, TCP, HTTP 검사 대상 추가 및 삭제
- API 서버 주소와 100~30000ms 타임아웃 설정
- 전체 상태, 성공/실패 수, 지연 시간 및 개별 결과 표시
- 원본 JSON 리포트 Android 공유 시트로 전송
- 마지막으로 사용한 API 서버 주소 저장
