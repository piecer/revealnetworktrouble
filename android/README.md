# CheckNetwork Android

Go 진단 API를 사용하는 네이티브 Android 클라이언트입니다. Android 8.0(API 26) 이상을 지원합니다.

## 필수 환경

- JDK 17 (`JAVA_HOME` 미설정 시 canonical `make`가 `PATH`의 `javac`에서 탐색)
- Android SDK Platform 35
- Android SDK Build-Tools 35.0.0
- SDK는 `ANDROID_HOME`, `ANDROID_SDK_ROOT`, `~/Android/Sdk`, `~/.local/share/checknetwork-android/sdk` 순으로 선택
- 첫 Gradle 실행에서 플러그인 및 테스트 의존성을 받을 수 있는 네트워크 연결

저장소 루트에서 환경과 체크인된 Gradle 8.11.1 wrapper의 출처/해시를 각각 확인할 수 있습니다.

```sh
make android-wrapper-verify
make android-env
```

wrapper JAR은 재현 가능한 빌드를 위해 의도적으로 버전 관리됩니다. 고정된 SHA-256 값과 업그레이드 절차는 [`gradle/wrapper/README.md`](gradle/wrapper/README.md)에 있습니다.

## 실행

1. Android Studio에서 `android/` 디렉터리를 엽니다.
2. JDK 17과 Android SDK 35가 선택되어 있는지 확인합니다.
3. 백엔드를 저장소 루트에서 `go run ./cmd/checknetwork-api`로 실행합니다.
4. `app`을 디버그 빌드하여 에뮬레이터에서 실행합니다.

에뮬레이터의 기본 API 주소 `http://10.0.2.2:8080`은 호스트 PC의 `localhost:8080`을 가리킵니다. 실기기에서는 같은 네트워크에 있는 개발 PC의 IP 또는 배포된 HTTPS API 주소를 입력해야 합니다.

## 검증 게이트

저장소 루트에서 다음 게이트를 실행합니다.

```sh
make android-test      # debug/release JVM + Robolectric 테스트; XML 결과가 0건이면 실패
make android-lint      # debug/release Android lint
make android-assemble  # debug/release APK 조립
make android-check     # 위 Android 게이트 전체
make ci                # 기존 Go/웹 테스트와 Android 게이트 전체
```

`android-test`는 에뮬레이터 없이 실행되며 request/report schema, `/checks` discovery, body/deadline/cancel 경계, owner lifecycle, manifest/resources와 Activity recreation/share 계약을 검사합니다. `android-lint`는 baseline이나 issue 비활성화 없이 실제 lint 오류에서 실패합니다. 이 로컬 게이트는 계측 테스트, 실기기 동작 또는 스크린 리더 접근성 검증을 대신하지 않습니다.

## 보안 및 배포

- 디버그 빌드만 로컬 HTTP 통신을 허용합니다.
- 릴리스 빌드는 HTTPS만 허용하며 기본 API 주소가 비어 있습니다. 운영 API origin과 유효한 TLS 인증서를 명시해야 합니다.
- Bearer는 HTTPS 요청 메모리에만 유지되며 URL, saved state, 일반 preferences, report 및 공유 데이터에 저장하지 않습니다.
- 기본 공유는 식별자와 네트워크 관측값을 제외한 사람용 요약입니다. 원본 JSON은 경고 확인 후에만 private cache의 bounded 파일과 일회성 read grant로 공유합니다.
- 릴리스 APK/AAB 생성 전 `versionCode`, `versionName`, 서명 구성을 배포 환경에 맞게 설정합니다.

## 기능

- `/api/v1/checks` capability를 확인한 뒤 DNS, TCP, HTTP, HTTPS, traceroute, SSH와 SMTP/IMAP/POP3 계열 13종 검사를 실행합니다.
- HTTP/HTTPS expected status와 traceroute 1~10 attempts, 최대 20 targets, 100~30000ms timeout을 검증합니다.
- status/verdict, coverage, findings, evidence, actions, limitations/provider failures, raw result와 compact topology 요약을 순서대로 표시합니다.
- 요청 교체·입력 변경·취소·Activity recreation에 owner/signature를 적용하고 stale completion을 게시하지 않습니다.
- redacted human summary와 명시적으로 확인한 bounded raw JSON을 별도 공유합니다.
- 320dp, landscape, large-font reflow와 accessibility labels/live regions를 자동 계약으로 검사합니다.

Interactive Android topology/Geo graph, 실제 TalkBack·Switch Access, OEM share sheet 및 물리 기기 네트워크 취소는 후속 device acceptance 범위입니다.
