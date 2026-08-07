# INHERIT AutoCert 설치 매뉴얼

> **문서 상태**: 초안. 제품은 Phase 1(인증서 인벤토리) 개발 중이며, 아직 구현되지 않은 기능은 **(예정)** 으로 표기한다. 기능이 완료될 때마다 이 문서를 갱신한다.

## 1. 제품 개요

INHERIT AutoCert는 폐쇄망·레거시 환경을 위한 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼이다. 외부 SaaS 연결을 요구하지 않으며, 모든 인증서 정보와 감사 로그는 고객 내부에 저장된다.

구성 요소는 3개 컨테이너다.

| 구성 요소 | 역할 | 기본 포트 |
| --- | --- | --- |
| frontend | 웹 콘솔 (Next.js) | 3000 |
| backend | REST API 서버 (Go) | 8080 |
| db | PostgreSQL 16 데이터베이스 | 5432 |

## 2. 시스템 요구사항

### 2.1 소프트웨어

- **Docker Engine** 및 **Docker Compose v2** (`docker compose` 명령 사용 가능해야 함)
- Linux x86_64 서버 권장. macOS에서도 동작한다.
- 그 외 런타임(Go, Node.js 등)은 서버에 설치할 필요 없다 — 모두 컨테이너 이미지에 포함된다.

### 2.2 폐쇄망 전제

**운영에 외부 인터넷 연결이 필요 없다.**

- 웹 콘솔의 폰트를 포함한 모든 정적 자원은 컨테이너에 셀프호스팅되어 있다. CDN 등 외부 요청이 발생하지 않는다.
- 단, 컨테이너 **이미지 반입**은 별도로 필요하다. 인터넷이 되는 환경에서 이미지를 빌드/저장(`docker save`)한 뒤 반입 매체로 옮겨 폐쇄망에서 적재(`docker load`)하거나, 내부 레지스트리를 사용한다. 소스 반입 후 폐쇄망 내에서 빌드하는 경우 빌드 시점에는 베이스 이미지·의존성 접근이 가능한 내부 미러가 필요하다.

### 2.3 하드웨어 (권장 최소값)

- CPU 2코어 / 메모리 4 GB / 디스크 20 GB 이상 (인증서 10,000개 규모 기준. 감사 로그 축적에 따라 디스크 여유를 확보할 것)

## 3. 네트워크 / 방화벽 요구사항

### 3.1 인바운드 (외부 → AutoCert 서버)

| 출발지 | 목적지 포트 | 프로토콜 | 용도 | 비고 |
| --- | --- | --- | --- | --- |
| 관리자 브라우저 | 3000/TCP | HTTP | 웹 콘솔 | 필수 |
| 관리자 브라우저 | 8080/TCP | HTTP | REST API | 필수 — 웹 콘솔이 브라우저에서 직접 API를 호출한다 |
| (없음) | 5432/TCP | — | PostgreSQL | **호스트 외부로 노출하지 않을 것을 권장.** DB는 backend 컨테이너만 접근하면 된다 (4.4절 참고) |

### 3.2 아웃바운드 (AutoCert 서버 → 내부망)

| 출발지 | 목적지 | 포트 예시 | 용도 | 상태 |
| --- | --- | --- | --- | --- |
| AutoCert 서버 | Discovery 스캔 대상 IP/CIDR | 443, 8443, 636 등 TLS 서비스 포트 | 네트워크 TLS 스캔 (에이전트리스 — 대상 서버에 설치물 없음) | **(예정)** — 스캔 포트 목록은 스캔 작업 등록 시 관리자가 지정한다 |

인터넷 방향 아웃바운드는 필요 없다.

### 3.3 서버 내부 (컨테이너 간)

frontend → backend(8080), backend → db(5432) 통신은 Docker Compose 내부 네트워크에서 이루어지므로 별도 방화벽 설정이 필요 없다.

## 4. 설치 절차

### 4.1 소스/이미지 반입

배포 아카이브(또는 저장소)를 서버의 작업 디렉터리에 푼다. 이하 명령은 저장소 루트에서 실행한다.

### 4.2 환경 변수 설정

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
| `BOOTSTRAP_ADMIN_PASSWORD` | (없음) | 초기 관리자 비밀번호. 최초 기동 시 사용자가 하나도 없으면 이 값으로 초기 관리자를 **1회** 생성한다. 미설정 시 계정이 생성되지 않고 경고 로그만 남는다 (5.1절) |
| `SESSION_TTL` | `8h` | 로그인 세션 유효기간. Go duration 형식 (`8h`, `30m`, `12h30m` 등) |

- `POSTGRES_PASSWORD`가 비어 있으면 기동이 거부된다(의도된 안전장치).
- **주의**: 현재 `.env.example`에는 인증 관련 3개 변수(`BOOTSTRAP_ADMIN_USERNAME`/`BOOTSTRAP_ADMIN_PASSWORD`/`SESSION_TTL`)가 아직 들어 있지 않다. `.env`에 위 이름 그대로 **직접 추가**한다 — `docker-compose.yml`이 backend 컨테이너로 전달하도록 이미 배선되어 있다.

### 4.3 기동

```sh
docker compose up --build -d
```

- 최초 기동 시 backend가 DB 연결 후 **SQL 마이그레이션을 자동 적용**한다. 별도의 스키마 생성 작업이 필요 없다.
- DB 데이터는 Docker 볼륨 `pgdata`에 영속 저장된다.

동작 확인:

```sh
curl http://localhost:8080/healthz   # HTTP 200이면 정상
```

브라우저에서 `http://<서버 IP>:3000` 접속 → 대시보드가 표시되면 설치 완료.

### 4.4 운영 환경 보강 (권장)

- **PostgreSQL 포트 비노출**: 기본 compose 구성은 개발 편의상 5432를 호스트에 노출한다. 운영 시 `docker-compose.yml`의 `db` 서비스에서 `ports` 항목을 제거하거나 `127.0.0.1:5432:5432`로 제한한다.
- **API 주소**: 웹 콘솔이 호출할 API 주소는 frontend **빌드 시점**에 `NEXT_PUBLIC_API_URL` 빌드 인자로 고정된다(기본 `http://localhost:8080`). 관리자가 서버 외부에서 접속하는 환경이면 `docker-compose.yml`의 해당 값을 `http://<서버 IP 또는 도메인>:8080`으로 바꾸고 **재빌드**해야 한다.
- **TLS 종단** (예정): 웹 콘솔·API 앞단의 HTTPS 리버스 프록시 구성 가이드는 추후 제공한다.

## 5. 초기 설정

### 5.1 초기 관리자 계정과 로그인

최초 기동 시 사용자가 하나도 없고 `BOOTSTRAP_ADMIN_PASSWORD`가 설정되어 있으면, backend가 `BOOTSTRAP_ADMIN_USERNAME`(기본 `admin`) 계정을 **System Administrator** 역할로 1회 생성한다.

- `BOOTSTRAP_ADMIN_PASSWORD`를 설정하지 않으면 계정은 생성되지 않고 backend 로그에 경고만 남는다 — `no users exist and BOOTSTRAP_ADMIN_PASSWORD is not set; login is impossible until it is provided`. 이 경우 `.env`에 값을 넣고 `docker compose up -d backend`로 재기동하면 된다.
- 사용자가 1명이라도 존재하면 이 변수는 무시된다(재기동해도 덮어쓰지 않는다). 초기 생성 후에는 `.env`에서 `BOOTSTRAP_ADMIN_PASSWORD`를 지우는 것을 권장한다 — 파일에 비밀번호가 남지 않게 한다.
- **비밀번호 정책**: 최소 12자, 최대 72바이트, 계정명과 동일 금지. 정책 위반 비밀번호로는 계정이 생성되지 않는다.

로그인은 REST API로 한다 (웹 콘솔 로그인 화면은 **(예정)**):

```sh
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<비밀번호>"}'
```

- 응답의 `token`을 이후 요청에 `Authorization: Bearer <token>` 헤더로 넣는다. 브라우저에는 HttpOnly 세션 쿠키(`autocert_session`)도 함께 설정된다.
- 세션 유효기간은 `SESSION_TTL`(기본 8시간)이다. 만료 시 재로그인한다.
- 로그아웃: `POST /api/v1/auth/logout` (세션 즉시 폐기). 현재 사용자 확인: `GET /api/v1/auth/me`.

### 5.2 사용자와 역할 (RBAC)

역할은 6개다 (역할 변경·부여 권한은 System Administrator에게만 있다).

| 역할 | `role` 값 | 권한 |
| --- | --- | --- |
| System Administrator | `system_administrator` | 시스템 설정, 사용자 관리, 모든 인증서 관리, CA 관리 |
| PKI Administrator | `pki_administrator` | CA 연결, 인증서 프로파일 관리, 인증서 발급·폐기 |
| Security Administrator | `security_administrator` | 정책 관리, 승인, 감사 로그 조회 |
| Operator | `operator` | 인증서 조회, 갱신 실행, 배포 실행 |
| Auditor | `auditor` | 읽기 전용, 감사 로그·보고서 조회 |
| Viewer | `viewer` | 제한된 인증서 조회 |

- 모든 API는 요청마다 인증 **과** 역할 검사를 거친다. 허용되지 않은 역할의 호출은 **HTTP 403**으로 거부되고, 거부 자체가 감사 이벤트(`authz.denied`)로 기록된다.
- 사용자 생성/조회: `POST /api/v1/users` / `GET /api/v1/users` — **System Administrator 전용**.

```sh
curl -X POST http://localhost:8080/api/v1/users \
  -H "Authorization: Bearer <token>" -H 'Content-Type: application/json' \
  -d '{"username":"auditor1","password":"<12자 이상>","display_name":"감사 담당","role":"auditor"}'
```

### 5.3 초기 인증서 등록 (수동 업로드)

`POST /api/v1/certificates/import` — 허용 역할: **System Administrator, PKI Administrator, Operator**.

```sh
curl -X POST http://localhost:8080/api/v1/certificates/import \
  -H "Authorization: Bearer <token>" \
  -F "file=@server-cert.pem"
```

- 지원 형식: **PEM**(체인 포함 가능)·**DER**. `multipart/form-data`의 `file` 필드 또는 원시 본문(raw body) 둘 다 가능. 본문 상한 1 MiB.
- 중복 판정은 SHA-256 fingerprint 기준이다 — 이미 등록된 인증서를 다시 올리면 새 항목을 만들지 않고 **설치 위치만 추가**된다 (응답의 `duplicate: true`).
- **개인키가 포함된 파일은 전체가 400으로 거부된다** (`private_key_rejected`). 개인키는 저장·로그되지 않는다. 개인키를 제외하고 인증서만 업로드할 것.
- 네트워크 TLS 스캔(Discovery)으로 인벤토리를 자동 확보하는 기능은 **(예정)** 이다.

### 5.4 알림 채널 (예정)

만료 임박 알림 설정은 추후 제공한다.

## 6. 제거

```sh
docker compose down        # 컨테이너만 제거 (데이터 유지)
docker compose down -v     # 데이터 볼륨까지 완전 삭제 — 복구 불가, 백업 후 실행할 것
```
