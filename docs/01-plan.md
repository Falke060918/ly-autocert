# 01 — 기획

## 문제 정의

기업 내부의 TLS·사설 인증서는 Linux/Windows 서버, IIS, Nginx, Apache, Java KeyStore, Kubernetes Secret, 로드밸런서, 네트워크 장비 등에 분산되어 있다. 그 결과 다음이 발생한다.

1. **위치를 알 수 없음** — 어디에 설치됐고, 어떤 서비스가 쓰고, 누가 관리하고, 언제 만료되는지 파악 불가
2. **갱신·배포가 수작업** — 다운로드·변환·전송·설정 수정·재시작·확인을 사람이 하며 누락과 오설치가 발생
3. **만료 장애** — 웹 접속 장애, API 실패, mTLS 연결 실패, 배치 장애
4. **정책 불일치** — 약한 알고리즘, 과도한 유효기간, 미승인 자체서명, 소유자 불명

## 대상 사용자

- **시스템 관리자** — 인증서 위치 확인, 만료 예정 확인, 갱신·배포, 서버 검증
- **보안 관리자** — 정책 설정, 취약 알고리즘 탐지, 승인·폐기, 감사 로그
- **PKI 관리자** — CA 연결, 프로파일·발급 정책 관리, 폐기/CRL
- **감사 담당자** — 변경 이력, 작업 추적, 현황 보고서, 정책 위반 조회

주요 고객: 사내 데이터센터를 운영하는 중견기업, 폐쇄망·망분리 환경, AD CS 사용 기업, Linux/Windows 혼재 환경, Kubernetes/OpenShift 사용 기업.

## 핵심 기능 (우선순위 순)

### MVP — Phase 1: 인증서 인벤토리 (이번 범위)

1. **[MVP] 사용자 로그인 + 기본 RBAC** — 로컬 계정, 비밀번호 정책. 역할: System Administrator / PKI Administrator / Security Administrator / Operator / Auditor / Viewer
2. **[MVP] PostgreSQL 데이터 모델 + 마이그레이션** — Certificate, CertificateLocation, Target, AuditEvent (CertificateAuthority/RenewalJob/DeploymentJob/Policy는 스키마 자리만 확보)
3. **[MVP] 인증서 파서** — PEM, DER 우선. CRT/CER/P7B/PFX/PKCS#12는 확장 가능한 구조로 설계
4. **[MVP] 인증서 수동 업로드 API** — fingerprint(SHA-256) 기준 중복 판정, 중복 시 새 인증서를 만들지 않고 설치 위치/발견 이벤트만 추가
5. **[MVP] 네트워크 TLS 스캔(Discovery)** — IP/CIDR/포트 목록/타임아웃/동시 연결 수 입력, 비동기 Job으로 실행. 수집: IP, 포트, 호스트명, Subject, SAN, Issuer, Serial, 발급일, 만료일, 서명 알고리즘, 공개키 알고리즘·길이, 체인, TLS 프로토콜, 최초 발견/최근 확인 시각
6. **[MVP] 인증서 인벤토리 목록·상세 API + 화면** — 검색·정렬·필터·페이지네이션
7. **[MVP] 만료 대시보드** — 전체 수, 7/30/60/90일 이내 만료, 이미 만료, 자체서명, 소유자 없음
8. **[MVP] 감사 로그** — 로그인/로그아웃/실패, 인증서 등록·발급, 설정 변경, 데이터 내보내기. 애플리케이션 로그와 분리 저장, 무결성 검증값 포함
9. **[MVP] Docker Compose 개발환경**
10. **[MVP] 사용자 관리 API + 화면** — 역할·표시 이름 수정, 비활성화·재활성화, 관리자 비밀번호 재설정(해당 사용자 세션 즉시 폐기), `/users` 화면 1개. 전부 System Administrator 전용이며 변경마다 감사 이벤트를 같은 트랜잭션으로 기록한다. `users.is_active`를 바꿀 API가 없으면 퇴사자 차단이 DB 직접 UPDATE로만 가능해 감사 로그를 우회한다

### Phase 2 이후 (이번 범위 밖, 설계만 고려)

- **Phase 2**: CA 공통 인터페이스, ACME / Microsoft AD CS / HashiCorp Vault PKI 커넥터, 프로파일, CSR 생성, 발급 + Prometheus 메트릭 최소셋(`/metrics`) + 오프라인 이미지 번들·체크섬 반입 절차(Phase 3b 이전 완료, Helm보다 선행)
- **Phase 3**: 센서(원격 수집기) 선행 — **3a**: 격리 세그먼트 원격 스캔용 센서, 아웃바운드 전용 mTLS pull 워커 → **3b**: Linux/Windows 배포 에이전트(단일 Go 바이너리, outbound mTLS), Nginx/IIS/파일 배포, 배포 검증, 롤백
- **Phase 4**: 갱신 정책·스케줄러·승인 워크플로·자동 배포·재시도·알림 + OpenTelemetry 분산 트레이스 + Helm Chart / Kubernetes·비Docker 설치 경로
- **Phase 5**: AI 운영 지원(자연어 검색, 위험 설명, 실패 로그 요약, 소유자 후보 추천)

## 비기능 요구사항

### 보안 (최우선)

- 모든 내부 통신 TLS/mTLS
- 개인키 평문 저장 금지, 저장 민감정보 암호화, 비밀번호는 강력한 단방향 해시
- 인증서·개인키를 로그에 출력 금지, 테스트 코드에도 민감값 하드코딩 금지
- API 요청마다 인증·권한 검사, CSRF/XSS/SQLi 방어, 보안 헤더 기본 적용
- 자체 암호 구현 금지 — 검증된 표준 라이브러리만 사용
- 컨테이너 최소 권한 실행, 의존성 취약점 검사

### 성능 (초기 목표)

- 인증서 10,000개 인벤토리
- 동시 스캔 작업 100개
- 목록 API 2초 이내, 대시보드 요약 API 3초 이내
- 장시간 작업은 비동기 Job

### 아키텍처 원칙

- **결정론적 자동화** — 핵심 인증서 작업은 AI가 아닌 명시적 상태 머신 + 정책 엔진. 재시도 가능·중복 실행 안전·상태 추적·롤백·감사 가능
- **모듈화** — 인증서 도메인 / CA 커넥터 / 대상 커넥터 / 에이전트 / 정책 / 워크플로 / 알림 / AI / 감사를 강하게 분리
- **플러그인 확장점** — CA, 탐색기, 배포 대상, 알림 채널, 인증 제공자, Secret Store, HSM/KMS, AI Provider
- **API 원칙** — 버전 관리, 리소스 중심 URL, 비동기 Job, 변경 API는 감사 로그 생성, Idempotency Key, 통일된 오류 형식, Request ID, OpenAPI 자동 생성
- **폐쇄망 우선** — 외부 SaaS 연결 없이 핵심 기능 동작

### 기술 스택 (원본 명세 11장 고정값, 이유 없이 변경 금지)

- **Backend**: Go, REST API, OpenAPI 3, PostgreSQL, SQL migration, 비동기 작업 큐
- **Frontend**: React + TypeScript + Next.js
- **Agent/Sensor**: Go 단일 바이너리 (Windows/Linux) — 센서(원격 수집기)는 Phase 3a, 배포 에이전트는 Phase 3b
- **배포**: Docker, Docker Compose (Phase 1) / Helm Chart, Kubernetes (Phase 4)
- **관측성**: 구조화 JSON 로그, Request ID 전파 (Phase 1 구현 완료) / Prometheus Metrics (Phase 2) / OpenTelemetry·Trace ID (Phase 4)

## 이번 범위에서 제외 (명시적 제외)

이 목록은 "이번 범위(Phase 1) 밖"이라는 뜻이고, 뒤에 Phase가 적힌 항목은 해당 Phase에서 구현한다. 사용자 관리는 Phase 1 범위로 편입되어 이 목록에서 빠졌다.

- **Phase 2~5 전체** — CA 커넥터·발급, 원격 수집기(센서)·에이전트·배포·롤백, 자동 갱신·승인 워크플로, AI 기능
- 정책 엔진 실행(정책 데이터 모델 자리만 확보, 평가 로직은 Phase 4)
- Prometheus 메트릭·`/metrics` (Phase 2)
- 오프라인 이미지 번들·체크섬 반입 절차 (Phase 2~3b, Helm보다 선행)
- OpenTelemetry 분산 트레이스 (Phase 4)
- Helm Chart / Kubernetes·비Docker 설치 경로 (Phase 4)
- MFA(TOTP), LDAP/AD/OIDC/SAML 연동
- 알림(이메일/Webhook)
- 공개 신뢰 CA 사업, Root CA 자체 구현, 암호 알고리즘 자체 구현, HSM 펌웨어/드라이버
- IoT 프로비저닝, 코드서명 관리, SSH 키 관리, 사용자 인증서 관리
- 멀티테넌트 SaaS, 모바일 앱, 블록체인, 완전한 PQC 발급
- 모든 네트워크 장비 지원, 모든 공개 CA API 지원
- HA·수평 확장 (MVP는 단일 인스턴스)
