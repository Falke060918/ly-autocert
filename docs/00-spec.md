# 온프레미스 인증서 라이프사이클 관리 플랫폼  
## AI 개발 킥오프 계획 명세서 v0.1

---

## 1. 프로젝트 개요

### 1.1 프로젝트 목적

본 프로젝트는 기업 내부에 분산된 TLS 인증서와 사설 인증서를 중앙에서 발견하고, 관리하고, 갱신하고, 배포할 수 있는 **경량 온프레미스 인증서 라이프사이클 관리 플랫폼**을 구축하는 것을 목적으로 한다.

기업 환경에서는 인증서가 다음과 같은 여러 위치에 분산되어 있다.

- Linux 서버
- Windows Server
- IIS
- Nginx
- Apache
- Java KeyStore
- Kubernetes Secret
- Load Balancer
- 사내 네트워크 장비
- Microsoft AD CS
- Vault PKI
- Private CA
- Public CA

현재 많은 조직이 인증서 위치와 소유자를 정확하게 파악하지 못하고 있으며, 인증서 갱신과 교체를 수작업으로 수행한다.

본 제품은 이러한 문제를 해결하기 위해 다음 과정을 중앙화하고 자동화한다.

> 인증서 발견 → 인벤토리 구성 → 위험 분석 → 발급 요청 → 승인 → 갱신 → 배포 → 검증 → 감사

---

## 2. 제품 비전

### 2.1 제품 한 문장 정의

> 폐쇄망과 레거시 시스템을 운영하는 기업을 위한 설치가 쉽고 가벼운 온프레미스 인증서 자동화 플랫폼

### 2.2 핵심 가치

제품은 다음 세 가지 가치를 제공해야 한다.

1. **가시성**
   - 조직 내부에 존재하는 인증서를 자동으로 찾아낸다.
   - 인증서의 위치, 용도, 만료일, 소유 시스템을 확인할 수 있다.

2. **자동화**
   - 인증서 발급, 갱신, 배포, 검증 과정을 자동화한다.
   - 인증서 만료로 인한 서비스 장애를 방지한다.

3. **통제**
   - 인증서 정책, 승인 절차, 권한, 감사 로그를 중앙에서 관리한다.
   - 누가 어떤 인증서를 발급하거나 변경했는지 추적할 수 있다.

---

## 3. 제품 포지셔닝

### 3.1 목표 포지션

본 제품은 대규모 글로벌 PKI 플랫폼 전체를 대체하는 제품이 아니다.

다음 영역에 집중한다.

> 기업 내부의 TLS 인증서와 Private PKI 인증서를 빠르게 파악하고 자동으로 운영할 수 있는 경량 CLM 플랫폼

CLM은 Certificate Lifecycle Management를 의미한다.

### 3.2 주요 차별점

#### 경량 설치

- 단일 VM 설치 지원
- Docker Compose 설치 지원
- Kubernetes 설치 지원
- 외부 인터넷 연결 없이 운영 가능
- 오프라인 라이선스 지원
- 오프라인 업데이트 지원

#### 폐쇄망 우선

- 외부 SaaS 연결을 요구하지 않는다.
- 모든 인증서 정보와 감사 로그는 고객 내부에 저장한다.
- 인터넷이 차단된 환경에서도 핵심 기능이 작동해야 한다.

#### 레거시 환경 자동화

- Windows 인증서 저장소
- IIS
- Nginx
- Apache
- Java KeyStore
- PKCS#12
- Kubernetes Secret
- Microsoft AD CS

위 환경을 우선 지원한다.

#### 단순한 운영 경험

- 보안 전문가가 아니어도 인증서 상태를 이해할 수 있어야 한다.
- 설치 후 짧은 시간 안에 인증서 인벤토리를 확보할 수 있어야 한다.
- 복잡한 PKI 용어보다 운영 관점의 정보를 우선 제공한다.

---

## 4. 주요 고객

### 4.1 목표 고객

초기 목표 고객은 다음과 같다.

- 사내 데이터센터를 운영하는 중견기업
- 폐쇄망 또는 망분리 환경을 운영하는 기업
- Microsoft AD CS를 사용하는 기업
- 다수의 Linux 및 Windows 서버를 운영하는 기업
- Kubernetes 또는 OpenShift를 사용하는 기업
- 인증서 갱신을 수작업으로 수행하는 기업
- 사설 인증서와 공개 인증서를 함께 사용하는 기업
- 인증서 만료 장애를 경험한 기업

### 4.2 주요 사용자

#### 시스템 관리자

- 인증서 위치 확인
- 만료 예정 인증서 확인
- 인증서 갱신 및 배포
- 서버 상태 검증

#### 보안 관리자

- 인증서 정책 설정
- 취약 알고리즘 탐지
- 승인 및 폐기 관리
- 감사 로그 확인

#### PKI 관리자

- CA 연결
- 인증서 프로파일 관리
- 발급 정책 관리
- 인증서 폐기 및 CRL 관리

#### 감사 담당자

- 변경 이력 확인
- 사용자 작업 추적
- 인증서 현황 보고서 생성
- 정책 위반 내역 확인

---

## 5. 해결하려는 핵심 문제

### 5.1 인증서 위치를 알 수 없음

기업 내부의 인증서가 여러 서버와 장비에 분산되어 있다.

운영자는 다음 정보를 정확히 알기 어렵다.

- 인증서가 어디에 설치되어 있는가
- 어떤 서비스에서 사용되는가
- 누가 관리하는가
- 언제 만료되는가
- 어떤 CA에서 발급되었는가

### 5.2 갱신과 배포가 수작업임

인증서 발급 이후 운영자가 직접 다음 작업을 수행한다.

- 인증서 다운로드
- 파일 변환
- 서버 전송
- 설정 파일 수정
- 서비스 재시작
- 정상 적용 여부 확인

이 과정에서 누락, 설정 오류, 잘못된 인증서 설치가 발생할 수 있다.

### 5.3 인증서 만료 장애

인증서가 만료되면 다음 문제가 발생한다.

- 웹 서비스 접속 장애
- API 통신 실패
- 서비스 간 mTLS 연결 실패
- 배치 및 내부 시스템 장애
- 고객 신뢰 저하

### 5.4 정책이 일관되지 않음

조직 내부에서 서로 다른 키 길이, 알고리즘, 유효기간, 발급 규칙이 사용된다.

중앙 정책이 없으면 다음 문제가 발생한다.

- 약한 암호 알고리즘 사용
- 과도하게 긴 인증서 유효기간
- 승인되지 않은 자체 서명 인증서
- 인증서 소유자 불명
- 폐기된 서비스의 인증서 잔존

---

## 6. 제품 목표

### 6.1 1차 목표

초기 버전은 다음 질문에 답할 수 있어야 한다.

1. 우리 조직에 어떤 인증서가 존재하는가?
2. 인증서는 어느 서버와 서비스에서 사용되는가?
3. 어떤 인증서가 곧 만료되는가?
4. 어떤 인증서가 보안 정책을 위반하는가?
5. 인증서를 자동으로 갱신할 수 있는가?
6. 갱신된 인증서를 안전하게 배포할 수 있는가?
7. 배포 실패 시 이전 상태로 복구할 수 있는가?

### 6.2 핵심 성공 기준

- 네트워크 대역을 입력하면 TLS 인증서를 탐색할 수 있다.
- 발견된 인증서가 중앙 인벤토리에 저장된다.
- 만료 예정 인증서를 대시보드에서 확인할 수 있다.
- 인증서별 설치 위치와 서비스 정보를 확인할 수 있다.
- 연결된 CA를 통해 인증서를 발급할 수 있다.
- 발급된 인증서를 대상 서버에 배포할 수 있다.
- 배포 후 실제 서비스 인증서를 검증할 수 있다.
- 모든 변경 작업이 감사 로그에 기록된다.

---

## 7. MVP 범위

## 7.1 인증서 탐색

### 네트워크 TLS 스캔

사용자가 다음 정보를 입력할 수 있어야 한다.

- IP 주소
- CIDR 대역
- 포트 목록
- 스캔 주기
- 타임아웃
- 동시 연결 수

스캐너는 다음 정보를 수집한다.

- IP
- 포트
- 호스트 이름
- 인증서 Subject
- SAN
- Issuer
- Serial Number
- 발급일
- 만료일
- 서명 알고리즘
- 공개키 알고리즘
- 키 길이
- 인증서 체인
- TLS 프로토콜
- 최초 발견 시간
- 최근 확인 시간

### 수동 인증서 등록

다음 형식의 인증서를 업로드할 수 있어야 한다.

- PEM
- DER
- CRT
- CER
- P7B
- PFX
- PKCS#12

개인키가 포함된 파일은 별도의 보안 정책을 적용한다.

---

## 7.2 인증서 인벤토리

인증서별로 다음 정보를 관리한다.

- 인증서 고유 ID
- Fingerprint
- Subject
- SAN
- Issuer
- Serial Number
- 유효기간
- 공개키 알고리즘
- 키 길이
- 서명 알고리즘
- 인증서 유형
- 인증서 상태
- 설치 위치
- 사용 서비스
- 환경 구분
- 소유 부서
- 담당자
- 연결된 CA
- 자동 갱신 여부
- 최근 스캔 시간
- 최근 배포 시간
- 태그
- 메모

### 인증서 상태

다음 상태를 기본 제공한다.

- 정상
- 만료 임박
- 만료
- 폐기
- 정책 위반
- 연결 실패
- 소유자 미지정
- 자동화 가능
- 자동화 불가
- 갱신 진행 중
- 배포 실패

---

## 7.3 대시보드

대시보드에서는 다음 정보를 제공한다.

- 전체 인증서 수
- 7일 이내 만료
- 30일 이내 만료
- 60일 이내 만료
- 90일 이내 만료
- 이미 만료된 인증서
- 정책 위반 인증서
- 자체 서명 인증서
- 소유자가 없는 인증서
- 자동 갱신 대상
- 최근 갱신 실패
- 최근 배포 실패

다음 기준으로 필터링할 수 있어야 한다.

- 만료일
- CA
- 소유 부서
- 담당자
- 설치 위치
- 환경
- 인증서 유형
- 태그
- 상태

---

## 7.4 CA 커넥터

모든 CA는 공통 인터페이스를 통해 접근한다.

### 공통 CA 인터페이스

```text
CreateCertificateRequest
ApproveCertificateRequest
IssueCertificate
RenewCertificate
RevokeCertificate
GetCertificate
ListCertificates
GetCertificateChain
GetCAStatus
```

### MVP 지원 대상

1. Microsoft AD CS
2. ACME 호환 CA
3. HashiCorp Vault PKI

### 추후 지원 대상

- EJBCA
- step-ca
- AWS Private CA
- Azure Key Vault Certificates
- Google Cloud CAS
- 공개 인증기관 API
- SCEP
- EST
- CMP

CA별 구현은 플러그인 또는 어댑터 구조로 분리한다.

---

## 7.5 인증서 발급

운영자는 다음 절차로 인증서를 발급할 수 있어야 한다.

1. 인증서 프로파일 선택
2. Subject 입력
3. SAN 입력
4. 키 알고리즘 선택
5. 유효기간 요청
6. 대상 CA 선택
7. 배포 대상 선택
8. 승인 요청
9. 승인 후 발급
10. 대상 서버 배포
11. 정상 적용 검증

### 지원 키 알고리즘

초기 지원 대상:

- RSA 2048
- RSA 3072
- RSA 4096
- ECDSA P-256
- ECDSA P-384

알고리즘 값은 코드에 고정하지 않고 확장 가능한 데이터 모델로 관리한다.

---

## 7.6 인증서 갱신

자동 갱신 정책을 인증서별 또는 프로파일별로 설정할 수 있어야 한다.

예시:

- 만료 30일 전 갱신 시작
- 실패 시 1시간 간격으로 3회 재시도
- 이후 관리자 알림
- 운영환경은 승인 후 배포
- 개발환경은 자동 배포
- 배포 후 TLS 연결 검사
- 검증 실패 시 이전 인증서로 롤백

### 갱신 상태 흐름

```text
SCHEDULED
→ REQUESTED
→ APPROVAL_PENDING
→ ISSUING
→ ISSUED
→ DEPLOYING
→ VERIFYING
→ COMPLETED
```

실패 상태:

```text
ISSUE_FAILED
DEPLOY_FAILED
VERIFY_FAILED
ROLLBACK_REQUIRED
ROLLED_BACK
MANUAL_INTERVENTION_REQUIRED
```

---

## 7.7 배포 자동화

### MVP 배포 대상

- Linux 파일 시스템
- Nginx
- Apache
- Windows Certificate Store
- IIS
- Java KeyStore
- Kubernetes Secret

### 공통 배포 절차

1. 대상 상태 확인
2. 기존 인증서 백업
3. 신규 인증서 전달
4. 파일 또는 저장소 반영
5. 설정 유효성 검사
6. 서비스 reload 또는 restart
7. TLS 연결 검증
8. 실패 시 롤백
9. 결과 기록

### 원자적 배포

가능한 경우 다음 방식으로 배포한다.

- 임시 파일 생성
- 권한 검증
- 파일 교체
- 설정 검증
- 서비스 reload
- 외부 연결 테스트
- 성공 시 백업 정리

---

## 7.8 에이전트

외부에서 직접 접근할 수 없는 서버를 관리하기 위해 경량 에이전트를 제공한다.

### 에이전트 역할

- 로컬 인증서 탐색
- 인증서 설치 위치 확인
- 인증서 및 체인 배포
- 서비스 설정 검증
- 서비스 reload
- 배포 결과 보고
- 로컬 인증서 상태 수집

### 에이전트 원칙

- 서버에서 외부로 연결하는 outbound 방식
- 중앙 서버가 직접 SSH 접속하는 방식을 기본값으로 사용하지 않음
- 중앙 서버와 mTLS 통신
- 에이전트별 고유 아이덴티티 사용
- 명령 허용 목록 적용
- 임의 셸 명령 실행 금지
- 모든 작업 서명 및 검증
- 실행 결과 감사 로그 기록

### 센서(원격 수집기)

원격 수집 구성요소는 두 계층으로 구분한다.

- 센서(원격 수집기): 네트워크 세그먼트당 배치하는 네트워크 수집기. 서버 대신 에이전트리스 TLS 스캔을 실행한다.
- 에이전트: 호스트당 설치하는 로컬 구성요소. 로컬 인증서 탐색과 배포를 담당한다.

#### 배치 원칙

- 센서는 스캔 대상 세그먼트에 도달할 수 있는 위치에 배치한다.
- 센서에서 중앙 서버로 향하는 아웃바운드 HTTPS(mTLS) 경로 1개만 필요하다.
- 센서에 인바운드 포트를 열지 않는다.
- 예: B↔C, C↔D는 통신 가능하지만 B↮D인 토폴로지에서 서버가 B에 있으면, 센서를 C에 배치해 D 세그먼트를 수집한다.

#### 등록 흐름

- 관리자가 1회용 등록 토큰을 발급한다. 서버는 토큰의 SHA-256 해시만 저장한다.
- 센서는 로컬에서 키쌍을 생성하고 CSR을 제출해 클라이언트 인증서를 수령한다.
- 센서 개인키는 센서 호스트를 떠나지 않는다.
- 등록 이후 모든 통신은 mTLS로 수행한다.
- 등록, 비활성화, 토큰 발급은 전건 AuditEvent를 남긴다.

#### 작업 분배

- 센서가 서버에서 작업을 가져가는 pull 리스 방식이다.
- 작업 상태: pending → leased(만료 시각 포함) → running(하트비트) → completed/failed
- 리스가 만료되면 작업을 실패로 처리한다.
- 작업 유형은 폐쇄 enum으로 관리하며 `scan` 하나로 시작한다.
- 임의 명령 실행 경로와 AI 실행 경로를 두지 않는다.

#### 결과 수집

- 센서는 스캔 결과를 Observation 배치로 POST한다.
- (job_id, ip, port) 기준으로 멱등하다. 같은 배치를 다시 보내도 결과가 중복되지 않는다.
- 서버는 기존 upsert 인벤토리 경로를 재사용해 결과를 반영한다.

ACME/SCEP/EST 발급 프로토콜 중계는 Phase 4 이후 후보로만 남긴다.

---

## 7.9 정책 엔진

관리자는 인증서 정책을 정의할 수 있어야 한다.

### 정책 항목

- 허용 키 알고리즘
- 최소 키 길이
- 허용 서명 알고리즘
- 최대 유효기간
- 필수 SAN 규칙
- 허용 도메인
- Wildcard 허용 여부
- 자체 서명 인증서 허용 여부
- 환경별 승인 필요 여부
- 자동 갱신 허용 여부
- 자동 배포 허용 여부
- 개인키 export 허용 여부

### 기본 정책 예시

```yaml
name: production-tls-policy

scope:
  environment:
    - production

rules:
  minimum_rsa_key_size: 2048
  allowed_key_algorithms:
    - RSA
    - ECDSA

  denied_signature_algorithms:
    - SHA1-RSA
    - MD5-RSA

  maximum_validity_days: 200
  require_owner: true
  require_approval: true
  allow_self_signed: false
  allow_private_key_export: false
```

---

## 7.10 승인 워크플로

다음 작업에 승인 정책을 적용할 수 있어야 한다.

- 신규 인증서 발급
- 운영 인증서 갱신
- 인증서 폐기
- CA 설정 변경
- 인증서 정책 변경
- 운영 서버 배포

### 기본 승인 유형

- 승인 없음
- 1인 승인
- 2인 승인
- 지정 그룹 승인
- 소유자 승인
- 보안 관리자 승인

승인 요청에는 다음 정보가 포함되어야 한다.

- 요청자
- 대상 인증서
- 대상 서버
- 요청 사유
- 정책 위반 여부
- 변경 예정 시간
- 영향받는 서비스
- 롤백 가능 여부

---

## 7.11 사용자 및 권한

### 기본 역할

#### System Administrator

- 시스템 설정
- 사용자 관리
- 모든 인증서 관리
- CA 관리

#### PKI Administrator

- CA 연결
- 인증서 프로파일 관리
- 인증서 발급 및 폐기

#### Security Administrator

- 정책 관리
- 승인
- 감사 로그 조회

#### Operator

- 인증서 조회
- 갱신 실행
- 배포 실행

#### Auditor

- 읽기 전용
- 감사 로그 및 보고서 조회

#### Viewer

- 제한된 인증서 조회

### 인증 방식

MVP:

- 로컬 계정
- 비밀번호 정책
- TOTP 기반 MFA

추후:

- LDAP
- Active Directory
- OIDC
- SAML

---

## 7.12 감사 로그

다음 작업은 반드시 감사 로그에 기록한다.

- 로그인 및 로그아웃
- 로그인 실패
- 인증서 등록
- 인증서 발급
- 인증서 갱신
- 인증서 배포
- 인증서 폐기
- 정책 생성 및 변경
- CA 연결 변경
- 사용자 및 권한 변경
- 승인 및 거절
- 에이전트 명령 실행
- 설정 변경
- 데이터 내보내기

### 감사 로그 필드

- 이벤트 ID
- 이벤트 유형
- 사용자
- 사용자 IP
- 대상 리소스
- 변경 전 값
- 변경 후 값
- 요청 ID
- 실행 결과
- 실패 원인
- 발생 시간
- 무결성 검증값

감사 로그는 일반 애플리케이션 로그와 분리한다.

---

## 7.13 알림

다음 이벤트에 대한 알림을 지원한다.

- 인증서 만료 예정
- 인증서 만료
- 갱신 성공
- 갱신 실패
- 배포 성공
- 배포 실패
- 검증 실패
- 정책 위반 발견
- 신규 인증서 발견
- 승인 요청
- 에이전트 연결 끊김

### MVP 알림 채널

- 이메일
- Webhook
- 애플리케이션 내부 알림

추후:

- Slack
- Microsoft Teams
- SMS
- SIEM
- Syslog

---

## 8. AI 기능의 목적

AI는 인증서 발급과 키 관리의 핵심 실행 엔진이 아니다.

AI는 운영자가 인증서 상태를 이해하고 문제를 빠르게 해결하도록 돕는 보조 계층으로 사용한다.

### 8.1 초기 AI 기능

#### 인증서 위험 설명

예시 질문:

- 이 인증서가 왜 위험한가?
- 이 인증서가 만료되면 어떤 영향이 있는가?
- 어떤 정책을 위반하고 있는가?
- 우선 처리해야 할 인증서는 무엇인가?

AI는 인벤토리와 정책 데이터를 기반으로 설명한다.

#### 갱신 실패 분석

AI는 다음 정보를 분석한다.

- 작업 로그
- CA 응답
- 에이전트 로그
- 서비스 설정 검사 결과
- TLS 연결 검사 결과

AI는 다음 내용을 출력한다.

- 가장 가능성 높은 원인
- 확인해야 할 항목
- 권장 복구 순서
- 재시도 가능 여부

#### 자연어 검색

예시:

- 30일 안에 만료되는 운영 인증서를 보여줘.
- AD CS에서 발급된 인증서 중 소유자가 없는 것을 찾아줘.
- Nginx에 설치된 RSA 2048 인증서를 보여줘.
- 최근 한 달 동안 배포에 실패한 인증서를 보여줘.

자연어 요청을 안전한 내부 조회 조건으로 변환한다.

#### 인증서 소유자 추정

다음 정보를 이용해 소유자를 추정한다.

- 호스트 이름
- 도메인 이름
- 서버 태그
- CMDB 정보
- Kubernetes Namespace
- 최근 작업자
- 조직 정보

AI의 결과는 제안일 뿐이며 자동 확정하지 않는다.

### 8.2 AI 안전 원칙

AI는 다음 작업을 단독으로 수행할 수 없다.

- 인증서 폐기
- CA 설정 변경
- 개인키 export
- 운영 서버 재시작
- 인증서 정책 변경
- 사용자 권한 변경
- Root CA 관련 작업

AI가 생성한 실행 제안은 반드시 정책 엔진과 권한 검사를 통과해야 한다.

AI가 생성한 셸 명령을 직접 실행하지 않는다.

---

## 9. 비기능 요구사항

## 9.1 보안

- 모든 내부 통신은 TLS 또는 mTLS 사용
- 저장되는 민감정보 암호화
- 비밀번호는 강력한 단방향 해시 사용
- 개인키 평문 저장 금지
- 비밀정보를 애플리케이션 로그에 기록하지 않음
- API 요청마다 인증과 권한 검사
- CSRF, XSS, SQL Injection 방어
- 민감한 설정값은 Secret Store에서 관리
- 세션 만료 및 강제 로그아웃 지원
- 보안 헤더 기본 적용
- 의존성 취약점 검사
- 컨테이너 최소 권한 실행

## 9.2 가용성

MVP는 단일 인스턴스를 지원한다.

장기적으로 다음 구조를 지원할 수 있어야 한다.

- 애플리케이션 수평 확장
- 데이터베이스 HA
- 작업 큐 재처리
- 장애 복구
- 백업 및 복원
- 멀티 노드 에이전트 연결

## 9.3 성능 목표

초기 목표:

- 인증서 10,000개 인벤토리 관리
- 에이전트 1,000개 연결
- 동시 스캔 작업 100개
- 일반 목록 API 응답 2초 이내
- 대시보드 요약 API 응답 3초 이내
- 장시간 작업은 비동기 처리

## 9.4 확장성

다음 구성요소는 플러그인 또는 어댑터로 확장 가능해야 한다.

- CA
- 인증서 탐색기
- 배포 대상
- 알림 채널
- 인증 제공자
- Secret Store
- HSM 및 KMS
- AI Provider

---

## 10. 권장 시스템 아키텍처

```text
┌──────────────────────────────────────────────┐
│                  Web Console                 │
├──────────────────────────────────────────────┤
│            API Gateway / Backend API         │
├──────────────────────────────────────────────┤
│ IAM │ RBAC │ Approval │ Policy │ Audit       │
├──────────────────────────────────────────────┤
│ Certificate Inventory │ Lifecycle Engine     │
├──────────────────────────────────────────────┤
│ Discovery │ Renewal │ Deployment │ Verify    │
├──────────────────────────────────────────────┤
│          Connector Abstraction Layer         │
├──────────────────────────────────────────────┤
│ AD CS │ ACME │ Vault │ K8s │ IIS │ Nginx     │
├──────────────────────────────────────────────┤
│       PostgreSQL │ Queue │ Secret Store      │
└──────────────────────────────────────────────┘
             ▲                ▲                ▲
             │ mTLS           │ mTLS           │
      ┌──────┴──────┐  ┌──────┴──────┐  ┌──────┴──────┐
      │ Host Agents │  │   Sensors   │  │ AI Assistant │
      └─────────────┘  └─────────────┘  └─────────────┘
```

---

## 11. 기술 스택 기본안

AI는 아래 기술 스택을 기본값으로 사용한다.

### Backend

- Go
- REST API
- OpenAPI 3
- PostgreSQL
- SQL migration
- 비동기 작업 큐

### Frontend

- React
- TypeScript
- Next.js
- 컴포넌트 기반 UI
- 서버 상태와 작업 상태 실시간 갱신

### Agent

- Go
- 단일 바이너리
- Windows 및 Linux 지원
- 자동 업데이트 기능은 MVP 이후 구현

### 배포

- Docker (Phase 1 구현 완료)
- Docker Compose (Phase 1 구현 완료)
- Helm Chart (Phase 4)
- Kubernetes (Phase 4)

### 관측성

- OpenTelemetry (Phase 4)
- Prometheus Metrics (Phase 2)
- 구조화된 JSON 로그 (Phase 1 구현 완료)
- Trace ID 및 Request ID (Phase 1 구현 완료 — 전 응답 X-Request-ID 전파. Trace ID는 OpenTelemetry 도입 시 연결)

기술 스택은 특별한 이유 없이 변경하지 않는다.

변경이 필요한 경우 다음을 먼저 문서화한다.

- 변경 이유
- 장점
- 단점
- 마이그레이션 영향
- 운영 복잡도

---

## 12. 핵심 데이터 모델

### Certificate

```text
id
fingerprint_sha256
serial_number
subject_dn
issuer_dn
common_name
sans
not_before
not_after
public_key_algorithm
public_key_size
signature_algorithm
certificate_type
status
ca_id
owner_id
department_id
environment
auto_renew
renewal_policy_id
created_at
updated_at
```

### CertificateLocation

```text
id
certificate_id
target_id
location_type
file_path
store_name
service_name
port
last_verified_at
deployment_status
```

### Target

```text
id
name
target_type
hostname
ip_address
operating_system
environment
agent_id
connection_status
tags
```

### Sensor (Phase 3)

```text
id
name
status
enrollment_token_hash
client_cert_fingerprint_sha256
registered_at
last_seen_at
disabled_at
created_at
updated_at
```

유의점: 다중 세그먼트 환경에서는 사설 IP 대역이 겹칠 수 있다. 위치 유일성 기준을
(ip, port)에서 (실행기 스코프, ip, port)로 확장해야 하며, 마이그레이션 0006의
부분 유니크 인덱스(uq_targets_network_ip, uq_certificate_locations_network_endpoint)도 함께 확장 대상이다.

### ScanJob

```text
id
status
targets
ports
timeout_ms
concurrency
total_endpoints
scanned_endpoints
reachable_endpoints
certificates_found
certificates_new
error
created_at
started_at
finished_at
```

Phase 3 예정 필드: executor_type(server | sensor), sensor_id — 서버 직접 실행과 센서 위임 실행을 구분한다.

### CertificateAuthority

```text
id
name
provider_type
endpoint
status
configuration_reference
created_at
updated_at
```

### RenewalJob

```text
id
certificate_id
status
scheduled_at
started_at
completed_at
attempt_count
failure_reason
workflow_state
```

### DeploymentJob

```text
id
certificate_id
target_id
status
backup_reference
started_at
completed_at
verification_result
rollback_status
failure_reason
```

### Policy

```text
id
name
scope
rules
priority
enabled
created_at
updated_at
```

### AuditEvent

```text
id
event_type
actor_id
target_type
target_id
request_id
before_value
after_value
result
source_ip
created_at
integrity_hash
```

---

## 13. API 설계 원칙

- 모든 API는 버전 관리한다.
- 리소스 중심 URL을 사용한다.
- 장시간 작업은 비동기 Job으로 처리한다.
- 모든 변경 API는 감사 로그를 생성한다.
- 모든 API는 권한 검사를 수행한다.
- Idempotency Key를 지원한다.
- 오류 응답 형식을 통일한다.
- Request ID를 모든 응답에 포함한다.
- 페이지네이션과 필터링을 기본 제공한다.
- OpenAPI 문서를 자동 생성한다.

### 예시 API

```text
GET    /api/v1/certificates
POST   /api/v1/certificates/import
GET    /api/v1/certificates/{id}
POST   /api/v1/certificates/{id}/renew
POST   /api/v1/certificates/{id}/revoke
GET    /api/v1/certificates/{id}/locations

GET    /api/v1/targets
POST   /api/v1/targets
GET    /api/v1/agents
POST   /api/v1/agents/register

GET    /api/v1/certificate-authorities
POST   /api/v1/certificate-authorities
POST   /api/v1/certificate-authorities/{id}/test

GET    /api/v1/discovery-jobs
POST   /api/v1/discovery-jobs

GET    /api/v1/renewal-jobs
GET    /api/v1/deployment-jobs

GET    /api/v1/policies
POST   /api/v1/policies

GET    /api/v1/approvals
POST   /api/v1/approvals/{id}/approve
POST   /api/v1/approvals/{id}/reject

GET    /api/v1/audit-events
```

---

## 14. 사용자 화면

MVP는 다음 화면을 제공한다.

### 14.1 로그인

- 아이디
- 비밀번호
- MFA
- 로그인 실패 메시지

### 14.2 대시보드

- 전체 인증서 현황
- 만료 예정
- 정책 위반
- 갱신 실패
- 배포 실패
- 최근 발견 인증서

### 14.3 인증서 목록

- 검색
- 정렬
- 필터
- 태그
- 상태
- 일괄 작업

### 14.4 인증서 상세

- 인증서 기본 정보
- 인증서 체인
- SAN
- 설치 위치
- 연결 서비스
- 소유자
- 정책 평가
- 갱신 이력
- 배포 이력
- 감사 이력

### 14.5 Discovery

- 네트워크 범위 등록
- 스캔 작업 생성
- 스캔 결과 확인
- 반복 스캔 일정 설정

### 14.6 CA 관리

- CA 등록
- 연결 테스트
- 프로파일 관리
- CA 상태 확인

### 14.7 정책 관리

- 정책 생성
- 정책 수정
- 적용 범위 설정
- 정책 테스트
- 위반 인증서 조회

### 14.8 작업 관리

- 갱신 작업
- 배포 작업
- 실패 작업
- 재시도
- 롤백

### 14.9 승인함

- 승인 대기 목록
- 변경 내용
- 영향 대상
- 승인
- 거절
- 의견 기록

### 14.10 감사 로그

- 사용자별 검색
- 이벤트 유형별 검색
- 기간 검색
- 대상 리소스 검색
- CSV 내보내기

### 14.11 사용자 관리

System Administrator 전용 화면이다.

- 사용자 목록(사용자명, 표시 이름, 역할, 활성 여부, 생성일)
- 사용자 생성
- 역할 및 표시 이름 수정
- 비활성화 및 재활성화(퇴사자 계정 차단)
- 관리자에 의한 비밀번호 재설정(해당 사용자의 세션 즉시 폐기)

---

## 15. 구현 단계

## Phase 1: 인증서 인벤토리

구현 범위:

- 사용자 로그인
- 기본 RBAC
- PostgreSQL 데이터 모델
- 인증서 파싱
- 인증서 수동 업로드
- 네트워크 TLS 스캔
- 인증서 인벤토리
- 인증서 상세 화면
- 만료일 대시보드
- 기본 감사 로그
- 사용자 관리 API 및 화면(14.11) — 역할·표시 이름 수정, 비활성화·재활성화, 관리자 비밀번호 재설정. 전부 System Administrator 전용이며 변경마다 감사 이벤트를 같은 트랜잭션으로 기록한다.

완료 조건:

- CIDR을 입력해 TLS 인증서를 수집할 수 있다.
- 발견된 인증서를 목록에서 조회할 수 있다.
- 인증서 만료일과 설치 위치를 확인할 수 있다.
- 동일 인증서가 여러 위치에서 발견되는 경우 하나의 인증서와 여러 위치로 연결된다.
- 퇴사자 계정을 화면에서 비활성화할 수 있고, 그 즉시 로그인이 차단되며 감사 로그에 남는다.

## Phase 2: CA 연결과 발급

구현 범위:

- CA 공통 인터페이스
- ACME 커넥터
- Microsoft AD CS 커넥터
- Vault PKI 커넥터
- 인증서 프로파일
- CSR 생성
- 인증서 발급
- 발급 이력
- 관측성 — Prometheus 메트릭 최소셋: `/metrics` 엔드포인트(promhttp) + HTTP 요청 수·지연, 스캔 Job 상태별 카운트, 리스 만료 수, 센서 last_seen 지연, 인증서 만료 구간 게이지
- 오프라인 이미지 번들·체크섬 반입 절차 — Phase 2에서 시작해 Phase 3b 이전에 완료한다. Helm(Phase 4)보다 선행한다. Compose 고객에게도 폐쇄망 반입 수단이 필수이기 때문이다.

완료 조건:

- 등록된 CA의 연결 상태를 확인할 수 있다.
- UI 또는 API에서 인증서를 발급할 수 있다.
- 발급된 인증서가 인벤토리에 자동 등록된다.
- `/metrics`에서 위 지표를 수집할 수 있다.
- 인터넷이 없는 고객사에 이미지 번들을 체크섬 검증과 함께 반입해 설치할 수 있다.

## Phase 3: 원격 수집기(센서)·에이전트와 배포

Phase 3a는 Phase 2와 독립적이며 Phase 2보다 먼저 착수할 수 있다.

### Phase 3a: 센서(원격 수집기)

구현 범위:

- 센서 등록(1회용 등록 토큰, 서버는 SHA-256 해시만 저장)
- 센서 클라이언트 인증서 발급(센서 로컬 키쌍 생성 → CSR)
- mTLS 통신
- pull 리스 작업 분배(pending → leased → running → completed/failed)
- 원격 세그먼트 TLS 스캔 실행(작업 유형 `scan`)
- Observation 배치 멱등 수집(기존 upsert 인벤토리 경로 재사용)
- 센서 등록·비활성화·토큰 발급 감사 로그

완료 조건:

- 서버가 직접 도달할 수 없는 세그먼트의 인증서가 센서를 통해 인벤토리에 수집된다.
- 센서는 인바운드 포트 없이 동작한다.

### Phase 3b: 에이전트와 배포

구현 범위:

- Linux 에이전트
- Windows 에이전트
- 에이전트 등록
- mTLS 통신
- Nginx 배포
- IIS 배포
- 파일 기반 배포
- 배포 검증
- 롤백

완료 조건:

- 중앙 서버에서 승인된 배포 작업을 에이전트가 실행한다.
- 기존 인증서를 백업한다.
- 신규 인증서 배포 후 서비스 인증서를 검사한다.
- 검증 실패 시 이전 인증서로 복구할 수 있다.

## Phase 4: 자동 갱신

구현 범위:

- 갱신 정책
- 스케줄러
- 승인 워크플로
- 자동 발급
- 자동 배포
- 자동 검증
- 실패 재시도
- 알림
- 관측성 — OpenTelemetry 분산 트레이스(Trace ID 전파, 스캔·발급·배포 워크플로 스팬)
- Helm Chart / Kubernetes 매니페스트 및 비Docker 설치 경로

완료 조건:

- 만료 기준에 따라 자동 갱신 작업이 생성된다.
- 정책에 따라 승인 또는 자동 진행된다.
- 갱신, 배포, 검증 과정이 하나의 워크플로로 추적된다.
- 하나의 갱신 워크플로를 트레이스 하나로 추적할 수 있다.
- Kubernetes 환경에 Helm Chart로 설치할 수 있다.

## Phase 5: AI 운영 지원

구현 범위:

- 자연어 인증서 검색
- 인증서 위험 설명
- 갱신 실패 로그 요약
- 대응 절차 제안
- 소유자 후보 추천

완료 조건:

- AI 응답은 실제 인벤토리와 로그 데이터에 근거한다.
- AI가 존재하지 않는 인증서나 작업 결과를 생성하지 않는다.
- AI가 실행 작업을 직접 수행하지 않는다.
- 모든 AI 제안은 사용자 검토를 거친다.

---

## 16. MVP에서 제외하는 기능

초기 버전에서는 다음 기능을 구현하지 않는다.

- 공개 신뢰 CA 사업
- Root CA 자체 구현
- 암호 알고리즘 자체 구현
- HSM 펌웨어 또는 드라이버 개발
- IoT 디바이스 프로비저닝
- 코드서명 관리
- SSH 키 관리
- 사용자 인증서 관리
- 멀티테넌트 SaaS
- 모바일 애플리케이션
- 블록체인 기반 인증서 관리
- 완전한 PQC 인증서 발급
- 모든 네트워크 장비 지원
- 모든 공개 CA API 지원
- AI를 이용한 무승인 자동 변경

이 기능을 요청받더라도 MVP 핵심 기능이 완성되기 전에는 구현하지 않는다.

---

## 17. 개발 원칙

### 17.1 보안 우선

- 개인키를 다루는 코드는 최소화한다.
- 검증된 암호화 라이브러리를 사용한다.
- 자체 암호 구현을 금지한다.
- 인증서와 개인키를 로그에 출력하지 않는다.
- 민감한 값은 테스트 코드에도 하드코딩하지 않는다.

### 17.2 결정론적 자동화

핵심 인증서 작업은 AI가 아닌 명확한 상태 머신과 정책 엔진으로 실행한다.

모든 작업은 다음 특성을 가져야 한다.

- 재시도 가능
- 중복 실행 안전
- 상태 추적 가능
- 실패 원인 확인 가능
- 롤백 가능
- 감사 가능

### 17.3 모듈화

다음 구성요소를 강하게 분리한다.

- 인증서 도메인
- CA 커넥터
- 대상 시스템 커넥터
- 에이전트
- 정책 엔진
- 워크플로 엔진
- 알림
- AI 기능
- 감사 로그

### 17.4 테스트 우선

각 기능에는 다음 테스트를 포함한다.

- Unit Test
- Integration Test
- API Test
- Migration Test
- Connector Mock Test
- Failure Scenario Test
- Rollback Test
- Security Test

### 17.5 운영 가능성

기능 구현 시 반드시 다음을 함께 구현한다.

- 로그
- 메트릭
- 오류 코드
- 상태 확인 API
- 재시도 방식
- 타임아웃
- 관리자 문서
- 장애 복구 절차

---

## 18. AI 개발 지시사항

이 문서를 받은 개발 AI는 다음 순서로 작업한다.

1. 전체 요구사항을 분석한다.
2. MVP 범위와 제외 범위를 구분한다.
3. 모듈 단위 시스템 구조를 제안한다.
4. 데이터베이스 스키마를 설계한다.
5. API 명세를 작성한다.
6. 인증서 라이프사이클 상태 머신을 정의한다.
7. 프로젝트 디렉터리 구조를 생성한다.
8. Phase 1부터 순서대로 구현한다.
9. 각 단계마다 테스트를 작성한다.
10. 변경사항과 설계 결정을 문서화한다.

### 개발 AI가 임의로 하면 안 되는 것

- 요구사항에 없는 대규모 기능 추가
- 핵심 기술 스택 변경
- 인증서 개인키 평문 저장
- 자체 암호화 함수 구현
- 인증과 권한 검사 생략
- 감사 로그 생략
- 운영 명령을 임의의 셸 문자열로 실행
- 테스트 없이 커넥터 구현
- 실패 처리를 단순 로그 출력으로 끝내기
- AI 응답을 검증 없이 실행 작업으로 연결

---

## 19. 첫 번째 개발 작업

첫 번째 작업의 목표는 인증서 인벤토리의 기반을 만드는 것이다.

### 구현 항목

1. Go Backend 프로젝트 생성
2. PostgreSQL 연결
3. Database Migration 구성
4. Certificate 모델 구현
5. CertificateLocation 모델 구현
6. Target 모델 구현
7. PEM 및 DER 인증서 파서 구현
8. 인증서 업로드 API 구현
9. 인증서 목록 API 구현
10. 인증서 상세 API 구현
11. 기본 React 관리 화면 구현
12. 감사 이벤트 기록 구현
13. Unit Test 및 Integration Test 작성
14. Docker Compose 개발환경 작성

### 첫 번째 작업 완료 조건

다음 흐름이 작동해야 한다.

```text
인증서 파일 업로드
→ 인증서 파싱
→ Fingerprint 생성
→ 중복 여부 확인
→ 데이터베이스 저장
→ 인증서 목록 표시
→ 인증서 상세 정보 표시
→ 감사 로그 기록
```

동일한 인증서를 다시 업로드할 경우 중복 인증서를 새로 생성하지 않는다.

대신 기존 인증서에 새로운 설치 위치 또는 발견 이벤트를 연결할 수 있도록 설계한다.

---

## 20. 프로젝트의 최종 방향

본 프로젝트의 핵심은 CA 자체를 만드는 것이 아니다.

핵심은 다음과 같다.

> 서로 다른 CA와 서버 환경 사이에서 인증서의 전체 생명주기를 통합 관리하는 온프레미스 Control Plane 구축

제품은 인증서 운영자가 다음 상태에 도달하도록 해야 한다.

- 조직의 모든 인증서를 파악할 수 있다.
- 만료 위험을 사전에 발견할 수 있다.
- 인증서 발급과 갱신을 표준화할 수 있다.
- 인증서를 대상 시스템에 자동 배포할 수 있다.
- 실패한 배포를 안전하게 복구할 수 있다.
- 모든 변경과 승인 내역을 추적할 수 있다.
- 폐쇄망에서도 독립적으로 운영할 수 있다.

모든 설계와 구현 결정은 이 목적을 기준으로 판단한다.