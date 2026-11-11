# 보안 정책

## 지원과 신고

현재 정식 공개 릴리스의 지원 기간이나 보안 지원 버전 표는 확정하지 않았다. `VERSION=0.1.0`은 소스 identity이며 release 발행·보안 인증을 뜻하지 않는다. 프로젝트 라이선스는 [Apache-2.0](LICENSE)으로 결정했다. 공개 배포 전 비공개 신고 창구 및 지원 범위는 추가로 확정해야 한다.

비공개 신고 채널의 활성화·담당자는 아직 확정되지 않았다. 저장소 Security 탭에 private vulnerability reporting이 활성화돼 있는지 먼저 확인한다. 없으면 기존에 확인한 maintainer의 비공개 연락 경로를 사용한다. 그러한 경로도 없다면 공개 이슈에는 비공개 연락 방법 요청만 남기고 exploit·재현 대상·IP·토큰·원본 report를 게시하지 않는다. 아직 존재가 확인되지 않은 이메일 주소나 신고 기능을 안내하지 않는다.

비공개 보고에는 영향 범위, 해당 source revision, 최소 재현과 예상/실제 결과, 필요한 권한, 익명화한 로그 및 제안된 완화책을 포함한다. 응답 SLA나 패치 일정을 보장하지 않는다. 실제 key/token이 노출되면 먼저 폐기·교체하고, 이력 재작성과 공개 disclosure는 maintainer와 별도 협의한다.

## 배포 경계

- 기본 `trusted-local`은 무인증이며 사설망 진단을 허용한다. 같은 호스트 사용자와 Docker 네트워크 구성원도 신뢰 경계에 포함된다.
- 직접 실행에서 loopback 밖 bind는 정확한 `CHECKNETWORK_EXPOSE_TRUSTED_LOCAL=yes`가 필요하다. 이 승인은 위험 인지일 뿐 인증·방화벽이 아니다.
- 노출된 접근에는 `public`, API key, 양수 rate limit과 TLS proxy를 사용한다. Public SSRF 정책은 DNS 재해석·redirect를 포함한 실제 연결 주소의 사설/metadata/특수 목적 대역을 차단한다. 승인 변수로 Public 요구를 끌 수 없다.
- CORS는 인증이 아니다. forwarded headers를 rate-limit identity로 신뢰하지 않으며 proxy 뒤 사용자별 제한은 proxy에서도 적용해야 한다.
- startup traceroute PATH와 실행 파일, Docker daemon 및 공급자 endpoint 설정은 trusted deployment input이다. 임의 실행 파일을 sandbox하는 기능이 아니다.

## 데이터와 해석

Bearer·API key·쿠키·실제 내부 target/report를 소스·이슈·스크린샷에 포함하지 않는다. 사람용 redaction에도 모든 민감 정보가 제거됐다고 가정하지 않는다. raw JSON 공유와 외부 Geo/IP 조회는 별도의 개인정보 경계다. Android FileProvider의 process-observed lease는 이미 열린 descriptor나 수신 앱 복사본의 강제 만료를 보장하지 않는다.

검사는 API 서버 위치에서 실행된다. `confidence`는 원인 확률이 아니고 ASN/Geo/CIDR 일치는 소유권·물리 경로·장애 원인의 증명이 아니다. 측정되지 않은 항목을 관측 성공으로 표시하지 않는다.

## 검증과 한계

CI는 Public/SSRF/노출 회귀, Go source govulncheck, dev dependency 포함 npm high/critical audit, 전체 Git 이력 Gitleaks를 수행한다. 설치·DB/API 오류는 실패다. 공급자 advisory와 audit DB가 다르면 공식 영향 범위와 수정 버전을 직접 확인하며, 0건만으로 안전을 선언하지 않는다.

Git 이력 검사는 미커밋 credential 파일 전체 스캔이 아니다. Go source scan은 별도 Docker compiler·OS·Android 의존성이나 실제 배포를 검증하지 않는다. Docker builder와 offline validator를 Go 1.26.11로 맞췄어도 추출 release binary의 별도 gate와 clean exact-SHA 릴리스 검증을 생략할 수 없다. [CI](docs/CI.md), [운영](docs/OPERATIONS.md), [검증 기록](docs/PHASE2-VERIFICATION.md)을 참조한다.
