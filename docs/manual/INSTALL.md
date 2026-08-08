# INHERIT AutoCert 설치 매뉴얼

> **문서 상태**: Phase 1(인증서 인벤토리) + Phase 3a(센서) 구현 완료 기준. 아직 구현되지 않은 기능은 **(예정)** 으로 표기한다. 기능이 완료될 때마다 이 문서를 갱신한다.

## 1. 제품 개요

INHERIT AutoCert는 폐쇄망·레거시 환경을 위한 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼이다. 외부 SaaS 연결을 요구하지 않으며, 모든 인증서 정보와 감사 로그는 고객 내부에 저장된다.

구성 요소는 3개 컨테이너다.

| 구성 요소 | 역할 | 기본 포트 |
| --- | --- | --- |
| frontend | 웹 콘솔 (Next.js) — 브라우저의 API 호출을 backend로 중계하는 동일 출처 프록시 겸용 | 3000 |
| backend | REST API 서버 (Go) | 8080 |
| db | PostgreSQL 16 데이터베이스 | 5432 |

## 2. 시스템 요구사항

### 2.1 소프트웨어

- **Docker Engine** 및 **Docker Compose v2** (`docker compose` 명령 사용 가능해야 함)
- Linux x86_64 서버 권장. macOS에서도 동작한다.
- 그 외 런타임(Go, Node.js 등)은 서버에 설치할 필요 없다 — 모두 컨테이너 이미지에 포함된다.
- DB는 compose에 포함된 **PostgreSQL 16**(`postgres:16-alpine`)을 사용한다. 별도 설치가 필요 없다.
- **관리자 브라우저**: 최신 버전의 모던 브라우저(Chrome·Edge·Firefox 등). 웹 콘솔은 Next.js 16 / React 19 기반으로 구형 브라우저(IE 등)는 지원하지 않는다.

### 2.2 폐쇄망 전제

**운영에 외부 인터넷 연결이 필요 없다.**

- 웹 콘솔의 폰트를 포함한 모든 정적 자원은 컨테이너에 셀프호스팅되어 있다. CDN 등 외부 요청이 발생하지 않는다 (브라우저 개발자도구 네트워크 탭에서 외부 요청 0건으로 확인 가능).
- 단, 컨테이너 **이미지 반입**은 별도로 필요하다. 인터넷이 되는 환경에서 이미지를 빌드/저장(`docker save`)한 뒤 반입 매체로 옮겨 폐쇄망에서 적재(`docker load`)하거나, 내부 레지스트리를 사용한다. 소스 반입 후 폐쇄망 내에서 빌드하는 경우 빌드 시점에는 베이스 이미지·의존성 접근이 가능한 내부 미러가 필요하다.

### 2.3 하드웨어 (권장 최소값)

- CPU 2코어 / 메모리 4 GB / 디스크 20 GB 이상 (인증서 10,000개 규모 기준. 감사 로그 축적에 따라 디스크 여유를 확보할 것)

## 3. 지원 사양

구현 코드 기준의 실제 지원 범위다. 여기 없는 형식·기능은 지원하지 않는다.

### 3.1 인증서 형식과 파싱 범위

**업로드 지원 형식** (파서는 Go 표준 라이브러리 `crypto/x509`만 사용):

| 형식 | 지원 | 비고 |
| --- | --- | --- |
| PEM | O | 체인 포함 가능 — 파일 안 모든 `CERTIFICATE` 블록을 순서대로 등록. CSR 등 다른 블록 타입은 무시 |
| DER | O | 단일 또는 연접(concatenated) 인증서 |
| CRT / CER 확장자 | O | 내용이 PEM 또는 DER이므로 위 둘로 처리된다 |
| PKCS#7 (.p7b) | X | 미지원 — 파서에 확장점만 마련되어 있다 |
| PKCS#12 (.pfx/.p12) | X | 미지원. 개인키가 포함되는 형식이므로 지원하더라도 개인키는 받지 않는다 |

- **개인키 거부**: PEM의 `PRIVATE KEY` 계열 블록, DER 인코딩 개인키(PKCS#8·PKCS#1·SEC1)가 감지되면 **파일 전체가 400으로 거부**된다(`private_key_rejected`). 개인키는 저장·로그되지 않는다.
- **업로드 상한**: 1 MiB. `multipart/form-data`의 `file` 필드 또는 원시 본문(raw body) 둘 다 가능.
- **저장 정보**: 인증서 **원문(본문)은 저장하지 않는다.** 메타데이터만 저장한다 — SHA-256 지문(중복 판정 키), 일련번호, Subject/Issuer DN, CN, SAN(DNS·IP·이메일·URI), 유효기간, 공개키 알고리즘·키 길이, 서명 알고리즘, 유형, 상태.
- **공개키 알고리즘**: RSA·ECDSA·Ed25519는 키 길이(bit)까지 기록한다. 그 외 알고리즘(DSA 등)은 알고리즘 이름만 기록되고 키 길이는 0으로 남는다.
- **서명 알고리즘**: `crypto/x509`가 인식하는 알고리즘 이름을 그대로 기록한다.
- **유형 분류**: CA 여부(IsCA) 최우선, 이후 EKU 기준 — `ca` / `server` / `client` / `code_signing` / `email` / `other`.
- **상태**: 저장 시점에 `active` / `expired`만 판정한다. "만료 임박" 등 파생 상태는 조회 시 유효기간에서 계산한다.

### 3.2 Discovery 네트워크 스캔 지원 범위

에이전트리스 방식이다 — 대상 서버에 아무것도 설치하지 않고, TLS 핸드셰이크로 서버가 제시하는 인증서 체인을 수집한다.

| 항목 | 값 |
| --- | --- |
| 대상 입력 | 단일 IP 또는 CIDR (IPv4·IPv6). 웹 콘솔에서는 한 줄에 하나씩 입력 |
| CIDR 크기 상한 | 호스트 비트 20비트 이하 (IPv4 기준 /12 이상). 초과 시 400 거부 |
| 엔드포인트 상한 | IP × 포트 조합 65,536개까지. 초과 시 400 거부. 중복 IP는 자동 제거 |
| 포트 | 1–65535, 복수 지정 가능. 미지정 시 `443` |
| 타임아웃 | 엔드포인트당 100 ms – 30 s (범위 밖 값은 자동 보정), 기본 5 s |
| 동시 연결 수 | 1–256 (범위 밖 값은 자동 보정), 기본 20 |
| TLS 버전 | 스캔 클라이언트는 Go 표준 `crypto/tls` 기본 설정을 사용한다 — **TLS 1.2·1.3 협상**. TLS 1.0/1.1 전용 서버는 핸드셰이크가 실패해 "도달 불가"로 기록되며 인증서는 수집되지 않는다 |
| 인증서 수집 기준 | 검증 없이 **수집 전용** — 자기서명·만료·이름 불일치 인증서도 모두 인벤토리에 등록한다. 이 결과를 신뢰 판단에 쓰지 않는다 |
| 호스트 이름 | 역방향 DNS 조회(상한 1초) → 실패 시 리프 인증서 CN 폴백. 둘 다 없으면 비움 |
| 실패 처리 | 도달 불가·비-TLS 포트·핸드셰이크 실패는 조용히 기록하고 전체 작업은 계속된다 |

스캔은 비동기 Job이다 — 생성 즉시 Job ID가 반환(202)되고 백그라운드에서 진행된다. 운영 절차는 OPERATIONS.md 2.4절.

### 3.3 네트워크 구조 (동일 출처 프록시)

브라우저는 **웹 콘솔(3000)과만 통신한다.** 웹 콘솔의 API 호출은 같은 출처의 `/api/*` 경로로 나가고, frontend 컨테이너(Next.js)가 Docker 내부 네트워크로 backend(8080)에 중계한다.

- 브라우저 → backend 직접 연결이 없으므로 **CORS 설정이 필요 없다.**
- 세션은 HttpOnly 쿠키(`autocert_session`)로 유지된다.
- 따라서 방화벽에서 관리자 PC에 열어야 할 포트는 **3000 하나뿐**이다 (4.1절).

## 4. 네트워크 / 방화벽 요구사항

### 4.0 설치 위치 선정 기준

AutoCert 서버를 어느 네트워크 세그먼트에 둘지는 아래 세 기준으로 정한다.

1. **관리자 도달성** — 관리자 브라우저가 서버의 3000/TCP 포트에 도달할 수 있어야 한다 (4.1절). 웹 콘솔 사용에 필요한 인바운드는 이 포트 하나뿐이다 (3.3절).
2. **스캔 도달성** — 서버가 Discovery 스캔 대상 세그먼트로 아웃바운드 TLS 연결을 낼 수 있어야 한다 (4.2절). 설치 위치는 "관리자가 앉은 곳"이 아니라 **"가장 많은 스캔 대상에 도달하는 곳"** 을 기준으로 고른다 — 기준 ①은 포트 하나만 열면 충족되지만, 기준 ②는 세그먼트 간 방화벽 정책에 좌우되기 때문이다.
3. **도달 불가 세그먼트가 있는 경우** — 어느 위치를 골라도 서버가 도달할 수 없는 세그먼트가 남는다면 **센서(원격 수집기)** 를 쓴다: 격리 세그먼트 안에 설치하는 경량 수집기로, 스캔 대상 도달 + AutoCert 서버로의 아웃바운드 mTLS(8443/TCP)만 있으면 되며 인바운드 포트 개방이 필요 없다. 예: B↔C, C↔D는 통하지만 B↮D인 망에서 서버가 B망에 있다면, 센서를 C망에 두어 D망 인증서를 수집한다. 설치·등록·운영 절차는 **SENSOR.md** 참조. 센서를 쓰지 않는 세그먼트는 **수동 업로드**(6.3절)로 보완한다.

### 4.1 인바운드 (외부 → AutoCert 서버)

| 출발지 | 목적지 포트 | 프로토콜 | 용도 | 비고 |
| --- | --- | --- | --- | --- |
| 관리자 브라우저 | 3000/TCP | HTTP | 웹 콘솔 (API 중계 포함) | **필수** — 웹 콘솔 사용에는 이 포트만 필요하다 (3.3절) |
| API 클라이언트 (curl·스크립트 등) | 8080/TCP | HTTP | REST API 직접 호출 | 선택 — 웹 콘솔만 쓰면 외부 개방 불필요. 자동화·연동 시에만 개방 |
| 센서(원격 수집기) 호스트 | 8443/TCP | HTTPS (mTLS) | 센서 게이트웨이 | 선택 — 센서 사용 시에만. `SENSOR_CA_ENC_KEY` 미설정이면 이 포트는 아예 열리지 않는다 (SENSOR.md 3장) |
| (없음) | 5432/TCP | — | PostgreSQL | **호스트 외부로 노출하지 않을 것을 권장.** DB는 backend 컨테이너만 접근하면 된다 (5.4절 참고) |

### 4.2 아웃바운드 (AutoCert 서버 → 내부망)

| 출발지 | 목적지 | 포트 예시 | 용도 |
| --- | --- | --- | --- |
| AutoCert 서버 | Discovery 스캔 대상 IP/CIDR | 443, 8443, 636 등 TLS 서비스 포트 | 네트워크 TLS 스캔 (에이전트리스 — 대상 서버에 설치물 없음). 스캔 포트는 스캔 시작 시 관리자가 지정한다 (3.2절) |
| AutoCert 서버 | 내부 DNS 서버 | 53 | 스캔 시 발견 IP의 역방향 DNS 조회 (선택 — 실패해도 스캔은 계속된다) |

인터넷 방향 아웃바운드는 필요 없다.

### 4.3 서버 내부 (컨테이너 간)

frontend → backend(8080), backend → db(5432) 통신은 Docker Compose 내부 네트워크에서 이루어지므로 별도 방화벽 설정이 필요 없다.

## 5. 설치 절차

### 5.1 소스/이미지 반입

배포 아카이브(또는 저장소)를 서버의 작업 디렉터리에 푼다. 이하 명령은 저장소 루트에서 실행한다.

### 5.2 환경 변수 설정

```sh
cp .env.example .env
```

`.env`를 열어 아래 값을 설정한다.

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `POSTGRES_USER` | `autocert` | DB 사용자 |
| `POSTGRES_PASSWORD` | (없음 — 필수 입력) | DB 비밀번호. **반드시 강한 값으로 변경한다.** 예시 값 그대로 운영 금지 |
| `POSTGRES_DB` | `autocert` | DB 이름 |
| `BOOTSTRAP_ADMIN_USERNAME` | `admin` | 초기 관리자(System Administrator) 계정명 |
| `BOOTSTRAP_ADMIN_PASSWORD` | (없음) | 초기 관리자 비밀번호. 최초 기동 시 사용자가 하나도 없으면 이 값으로 초기 관리자를 **1회** 생성한다. 미설정 시 계정이 생성되지 않고 경고 로그만 남는다 (6.1절) |
| `SESSION_TTL` | `8h` | 로그인 세션 유효기간. Go duration 형식 (`8h`, `30m`, `12h30m` 등) |
| `SENSOR_CA_ENC_KEY` | (없음) | 센서 사용 시에만. 센서 CA 개인키 암호화 키 — hex 64자, `openssl rand -hex 32`로 생성. 미설정이면 센서 게이트웨이(8443)가 열리지 않는다 (SENSOR.md 3.1절) |
| `SENSOR_ADVERTISED_HOSTS` | (없음) | 센서 사용 시에만. 센서가 서버 접속에 쓸 DNS/IP 목록(쉼표 구분) — 게이트웨이 서버 인증서 SAN (SENSOR.md 3.1절) |
| `SENSOR_LISTEN_ADDR` | `:8443` | 센서 게이트웨이 리슨 주소. 보통 기본값 사용 |

- `POSTGRES_PASSWORD`가 비어 있으면 기동이 거부된다(의도된 안전장치).
- 센서 관련 3개 변수는 `.env.example`에 없다 — 센서를 쓸 때 `.env`에 직접 추가한다.

### 5.3 기동

```sh
docker compose up --build -d
```

- 최초 기동 시 backend가 DB 연결 후 **SQL 마이그레이션을 자동 적용**한다. 별도의 스키마 생성 작업이 필요 없다.
- DB 데이터는 Docker 볼륨 `pgdata`에 영속 저장된다.

동작 확인:

```sh
curl http://localhost:8080/healthz   # HTTP 200이면 정상
```

브라우저에서 `http://<서버 IP>:3000` 접속 → 로그인 화면이 표시되면 설치 완료. 초기 계정은 6.1절.

### 5.4 운영 환경 보강 (권장)

- **PostgreSQL 포트 비노출**: 기본 compose 구성은 개발 편의상 5432를 호스트에 노출한다. 운영 시 `docker-compose.yml`의 `db` 서비스에서 `ports` 항목을 제거하거나 `127.0.0.1:5432:5432`로 제한한다.
- **API 포트 비노출**: 웹 콘솔만 사용한다면 `backend`의 `ports`(8080)도 호스트 노출을 제한할 수 있다 — 웹 콘솔은 컨테이너 내부 네트워크로 backend에 접근하므로 영향이 없다 (3.3절).
- **API 중계 주소**: frontend가 중계할 backend 주소는 **빌드 시점**에 `BACKEND_INTERNAL_URL` 빌드 인자로 고정된다 (compose 기본 `http://backend:8080` — 컨테이너 내부 주소). 브라우저의 접속 주소와 무관하므로, 관리자가 어떤 IP/도메인으로 접속하든 **바꿀 필요 없다.** backend를 별도 호스트로 분리하는 경우에만 이 값을 바꾸고 frontend를 재빌드한다.
- **TLS 종단** (예정): 웹 콘솔 앞단의 HTTPS 리버스 프록시 구성 가이드는 추후 제공한다.

## 6. 초기 설정

### 6.1 초기 관리자 계정과 로그인

최초 기동 시 사용자가 하나도 없고 `BOOTSTRAP_ADMIN_PASSWORD`가 설정되어 있으면, backend가 `BOOTSTRAP_ADMIN_USERNAME`(기본 `admin`) 계정을 **System Administrator** 역할로 1회 생성한다.

- `BOOTSTRAP_ADMIN_PASSWORD`를 설정하지 않으면 계정은 생성되지 않고 backend 로그에 경고만 남는다 — `no users exist and BOOTSTRAP_ADMIN_PASSWORD is not set; login is impossible until it is provided`. 이 경우 `.env`에 값을 넣고 `docker compose up -d backend`로 재기동하면 된다.
- 사용자가 1명이라도 존재하면 이 변수는 무시된다(재기동해도 덮어쓰지 않는다). 초기 생성 후에는 `.env`에서 `BOOTSTRAP_ADMIN_PASSWORD`를 지우는 것을 권장한다 — 파일에 비밀번호가 남지 않게 한다.
- **비밀번호 정책**: 최소 12자, 최대 72바이트, 계정명과 동일 금지. 정책 위반 비밀번호로는 계정이 생성되지 않는다.

**로그인은 웹 콘솔에서 한다**: `http://<서버 IP>:3000` 접속 → 로그인 화면에서 계정명·비밀번호 입력. 미로그인 상태로 다른 화면에 접근하면 로그인 화면으로 이동된다.

- 세션은 HttpOnly 쿠키(`autocert_session`)로 유지되고, 유효기간은 `SESSION_TTL`(기본 8시간)이다. 만료 시 재로그인한다.
- 로그아웃은 웹 콘솔 우상단의 로그아웃 버튼(세션 즉시 폐기).
- 자동화·스크립트용 API 로그인도 가능하다:

```sh
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<비밀번호>"}'
# 응답의 token을 이후 요청에 Authorization: Bearer <token> 헤더로 사용
# 로그아웃: POST /api/v1/auth/logout / 현재 사용자: GET /api/v1/auth/me
```

### 6.2 사용자와 역할 (RBAC)

역할은 6개다 (역할 변경·부여 권한은 System Administrator에게만 있다).

| 역할 | `role` 값 | 권한 |
| --- | --- | --- |
| System Administrator | `system_administrator` | 시스템 설정, 사용자 관리, 모든 인증서 관리, CA 관리 |
| PKI Administrator | `pki_administrator` | CA 연결, 인증서 프로파일 관리, 인증서 발급·폐기 |
| Security Administrator | `security_administrator` | 정책 관리, 승인, 감사 로그 조회 |
| Operator | `operator` | 인증서 조회, 갱신 실행, 배포 실행 |
| Auditor | `auditor` | 읽기 전용, 감사 로그·보고서 조회 |
| Viewer | `viewer` | 제한된 인증서 조회 |

- 모든 API는 요청마다 인증 **과** 역할 검사를 거친다. 허용되지 않은 역할의 호출은 **HTTP 403**으로 거부되고, 거부 자체가 감사 이벤트(`authz.denied`)로 기록된다. 웹 콘솔도 권한 없는 역할에는 업로드·스캔 시작 기능을 숨긴다.
- 사용자 생성/조회는 현재 API 전용이다 (웹 콘솔 사용자 관리 화면은 **(예정)**): `POST /api/v1/users` / `GET /api/v1/users` — **System Administrator 전용**.

```sh
curl -X POST http://localhost:8080/api/v1/users \
  -H "Authorization: Bearer <token>" -H 'Content-Type: application/json' \
  -d '{"username":"auditor1","password":"<12자 이상>","display_name":"감사 담당","role":"auditor"}'
```

### 6.3 초기 인증서 등록

인벤토리를 채우는 방법은 두 가지다. 허용 역할은 둘 다 **System Administrator, PKI Administrator, Operator**.

1. **수동 업로드** — 웹 콘솔 [인증서] 화면의 "인증서 등록" 패널에서 파일(.pem/.crt/.cer/.der)을 선택해 등록한다 (OPERATIONS.md 2.2절). API도 가능:

```sh
curl -X POST http://localhost:8080/api/v1/certificates/import \
  -H "Authorization: Bearer <token>" \
  -F "file=@server-cert.pem"
```

2. **Discovery 스캔** — 웹 콘솔 [Discovery] 화면에서 대상 IP/CIDR과 포트를 지정해 네트워크 TLS 스캔으로 자동 수집한다 (OPERATIONS.md 2.4절).

- 지원 형식·파싱 범위·스캔 범위는 3장 참조.
- 중복 판정은 SHA-256 fingerprint 기준이다 — 이미 등록된 인증서를 다시 올리거나 스캔에서 재발견되면 새 항목을 만들지 않고 **설치 위치만 추가**된다 (업로드 응답의 `duplicate: true`).
- **개인키가 포함된 파일은 전체가 400으로 거부된다** (`private_key_rejected`). 개인키를 제외하고 인증서만 업로드할 것.

### 6.4 알림 채널 (예정)

만료 임박 알림 설정은 추후 제공한다.

## 7. 제거

```sh
docker compose down        # 컨테이너만 제거 (데이터 유지)
docker compose down -v     # 데이터 볼륨까지 완전 삭제 — 복구 불가, 백업 후 실행할 것
```
