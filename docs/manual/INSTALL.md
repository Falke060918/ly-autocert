# INHERIT AutoCert 설치 매뉴얼

> **문서 상태**: Phase 1(인증서 인벤토리) + Phase 3a(센서) 구현 완료 기준. 아직 구현되지 않은 기능은 **(예정)** 으로 표기한다. 기능이 완료될 때마다 이 문서를 갱신한다.

## 1. 제품 개요

INHERIT AutoCert는 폐쇄망·레거시 환경을 위한 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼이다. 외부 SaaS 연결을 요구하지 않으며, 모든 인증서 정보와 감사 로그는 고객 내부에 저장된다.

구성 요소는 3개 컨테이너이며, HTTPS로 종단하는 경우에만 프록시 1개가 더 붙는다.

| 구성 요소 | 역할 | 기본 포트 |
| --- | --- | --- |
| frontend | 웹 콘솔 (Next.js) — 브라우저의 API 호출을 backend로 중계하는 동일 출처 프록시 겸용 | 3000 |
| backend | REST API 서버 (Go) | 8080, 센서 게이트웨이 8443 (센서 사용 시에만) |
| db | PostgreSQL 16 데이터베이스 | 5432 — **호스트에 노출하지 않는다** (5.5.3절) |
| proxy | (선택) HTTPS 리버스 프록시 — TLS 종단 (nginx). 적용 시에만 뜬다 (5.4절, TLS.md) | 443, 80 |

**포트는 전부 바꿀 수 있다.** 위 값은 `.env`를 건드리지 않았을 때의 기본값이며, 변경 절차는 **5.5절**에 한 표로 모아 두었다.

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
- 단, 컨테이너 **이미지 반입**은 별도로 필요하다. 인터넷이 되는 개발측에서 반입 번들(이미지 + 소스 + 센서 바이너리 + 체크섬)을 만들어 매체로 옮기고, 폐쇄망에서는 검증·적재만 한다 — 절차는 **5.1절**. 사내 레지스트리를 쓰는 경우는 5.1.2절.
- 번들에는 최종 이미지가 들어 있으므로 **폐쇄망 안에서 빌드할 필요가 없다.** 굳이 현장 빌드를 택하면 베이스 이미지·의존성(Go 모듈·npm 패키지) 접근이 가능한 내부 미러가 따로 필요하다.

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
| 포트 | 1–65535. 단일(`443`)·콤마 나열(`443,8443`)·범위(`8000-9000`)·프리셋(`web`/`common`/`all`)을 섞어 쓸 수 있다. **미지정 시 `common` 프리셋(26개)** (OPERATIONS.md 2.8절). `all`은 백그라운드 스윕 전용으로, 단발 스캔에 쓰면 400으로 거부된다 |
| 수집 불가 포트 | 스캐너는 접속 즉시 TLS를 거는 **implicit TLS 전용**이다. 평문으로 시작해 `STARTTLS`로 승격하는 포트(25·587·110·143·389·5432·3306·1433·21·3389)에서는 인증서를 얻을 수 없어 프리셋에서 제외했다. 해당 인증서는 수동 업로드로 등록한다 (6.3절) |
| 타임아웃 | 엔드포인트당 100 ms – 30 s (범위 밖 값은 자동 보정), 기본 5 s |
| 동시 연결 수 | 1–256 (범위 밖 값은 자동 보정), 기본 20 |
| TLS 버전 | 스캔 클라이언트는 **TLS 1.0·1.1·1.2·1.3을 모두 협상**한다 — 레거시 장비의 인증서야말로 관리 사각지대라 수집 대상에서 뺄 수 없다. 이것은 제품이 제공하는 TLS의 보안 등급이 아니라 **관측 대상의 범위**다(스캐너는 인증서를 읽기만 하고 이 연결로 비밀을 주고받지 않는다). 제품의 다른 TLS 연결(웹훅·SMTP·센서 mTLS)은 TLS 1.2 이상 그대로다 |
| 인증서 수집 기준 | 검증 없이 **수집 전용** — 자기서명·만료·이름 불일치 인증서도 모두 인벤토리에 등록한다. 이 결과를 신뢰 판단에 쓰지 않는다 |
| 호스트 이름 | 역방향 DNS 조회(상한 1초) → 실패 시 리프 인증서 CN 폴백. 둘 다 없으면 비움 |
| 실패 처리 | 도달 불가·비-TLS 포트·핸드셰이크 실패는 조용히 기록하고 전체 작업은 계속된다 |

스캔은 비동기 Job이다 — 생성 즉시 Job ID가 반환(202)되고 백그라운드에서 진행된다. 운영 절차는 OPERATIONS.md 2.4절.

### 3.3 네트워크 구조 (동일 출처 프록시)

브라우저는 **웹 콘솔(3000)과만 통신한다.** 웹 콘솔의 API 호출은 같은 출처의 `/api/*` 경로로 나가고, frontend 컨테이너(Next.js)가 Docker 내부 네트워크로 backend(8080)에 중계한다.

- 브라우저 → backend 직접 연결이 없으므로 **CORS 설정이 필요 없다.**
- 세션은 HttpOnly 쿠키(`autocert_session`)로 유지된다.
- 따라서 방화벽에서 관리자 PC에 열어야 할 포트는 **3000 하나뿐**이다 (4.1절).
- HTTPS 리버스 프록시(선택, TLS.md)를 얹으면 이 구조 앞에 프록시가 하나 더 붙어 관리자가 여는 포트는 **443** 하나가 되고, 3000·8080은 호스트 루프백으로만 남는다. 브라우저 → backend 직결이 없다는 점은 그대로다.

## 4. 네트워크 / 방화벽 요구사항

### 4.0 설치 위치 선정 기준

AutoCert 서버를 어느 네트워크 세그먼트에 둘지는 아래 세 기준으로 정한다.

1. **관리자 도달성** — 관리자 브라우저가 서버의 3000/TCP 포트에 도달할 수 있어야 한다 (4.1절). 웹 콘솔 사용에 필요한 인바운드는 이 포트 하나뿐이다 (3.3절).
2. **스캔 도달성** — 서버가 Discovery 스캔 대상 세그먼트로 아웃바운드 TLS 연결을 낼 수 있어야 한다 (4.2절). 설치 위치는 "관리자가 앉은 곳"이 아니라 **"가장 많은 스캔 대상에 도달하는 곳"** 을 기준으로 고른다 — 기준 ①은 포트 하나만 열면 충족되지만, 기준 ②는 세그먼트 간 방화벽 정책에 좌우되기 때문이다.
3. **도달 불가 세그먼트가 있는 경우** — 어느 위치를 골라도 서버가 도달할 수 없는 세그먼트가 남는다면 **센서(원격 수집기)** 를 쓴다: 격리 세그먼트 안에 설치하는 경량 수집기로, 스캔 대상 도달 + AutoCert 서버로의 아웃바운드 mTLS(8443/TCP)만 있으면 되며 인바운드 포트 개방이 필요 없다. 예: B↔C, C↔D는 통하지만 B↮D인 망에서 서버가 B망에 있다면, 센서를 C망에 두어 D망 인증서를 수집한다. 설치·등록·운영 절차는 **SENSOR.md** 참조. 센서를 쓰지 않는 세그먼트는 **수동 업로드**(6.3절)로 보완한다.

### 4.1 인바운드 (외부 → AutoCert 서버)

아래 포트 값은 **기본값**이다. 바꿨다면 방화벽 정책도 같은 값으로 맞춘다 (5.5절).

| 출발지 | 목적지 포트 (기본) | 프로토콜 | 용도 | 비고 |
| --- | --- | --- | --- | --- |
| 관리자 브라우저 | 3000/TCP (`FRONTEND_PORT`) | HTTP | 웹 콘솔 (API 중계 포함) | **필수** — 웹 콘솔 사용에는 이 포트만 필요하다 (3.3절). HTTPS 구성에서는 이 포트를 열지 않는다 |
| 관리자 브라우저 | 443/TCP (`HTTPS_PORT`) + 80/TCP (`HTTP_REDIRECT_PORT`) | HTTPS | 리버스 프록시 (TLS 종단) | 선택 — HTTPS 구성일 때만. 이때 관리자에게 여는 포트는 이것뿐이다 (5.4절, TLS.md). 80은 443으로 리다이렉트 전용 |
| API 클라이언트 (curl·스크립트 등) | 8080/TCP (`BACKEND_PORT`) | HTTP | REST API 직접 호출 | 선택 — 웹 콘솔만 쓰면 외부 개방 불필요. 자동화·연동 시에만 개방 |
| 센서(원격 수집기) 호스트 | 8443/TCP (`SENSOR_GATEWAY_PORT`) | HTTPS (mTLS) | 센서 게이트웨이 | 선택 — 센서 사용 시에만. `SENSOR_CA_ENC_KEY` 미설정이면 이 포트는 아예 열리지 않는다 (SENSOR.md 3장) |
| (없음) | 5432/TCP | — | PostgreSQL | **호스트에 노출하지 않는다** — 기본 compose 구성에 `ports`가 없다. DB는 backend 컨테이너만 접근하면 된다 (5.5.3절) |

### 4.2 아웃바운드 (AutoCert 서버 → 내부망)

| 출발지 | 목적지 | 포트 예시 | 용도 |
| --- | --- | --- | --- |
| AutoCert 서버 | Discovery 스캔 대상 IP/CIDR | 443, 8443, 636 등 TLS 서비스 포트 | 네트워크 TLS 스캔 (에이전트리스 — 대상 서버에 설치물 없음). 스캔 포트는 스캔 시작 시 관리자가 지정한다 (3.2절) |
| AutoCert 서버 | 내부 DNS 서버 | 53 | 스캔 시 발견 IP의 역방향 DNS 조회 (선택 — 실패해도 스캔은 계속된다) |

인터넷 방향 아웃바운드는 필요 없다.

### 4.3 서버 내부 (컨테이너 간)

frontend → backend(8080), backend → db(5432) 통신은 Docker Compose 내부 네트워크에서 이루어지므로 별도 방화벽 설정이 필요 없다. 호스트 포트를 바꿔도 이 경로는 영향받지 않는다 — 컨테이너 내부 포트까지 바꾸는 경우만 5.5절을 따른다.

## 5. 설치 절차

### 5.1 오프라인 반입 (이미지·소스)

폐쇄망 서버는 이미지를 내려받을 수 없다. **인터넷이 되는 개발측에서 반입 번들을 만들어 매체로 옮기고, 고객사에서는 체크섬 대조 후 적재만 한다.** 폐쇄망 안에서 빌드하지 않는다 — 번들에 최종 이미지가 들어 있으므로 현장에 Go·Node.js·내부 미러가 필요 없다.

두 스크립트가 이 절차의 전부다.

| 스크립트 | 실행 위치 | 실행 환경 |
| --- | --- | --- |
| `scripts/bundle-offline.sh` | **개발측** (인터넷 가능) | Linux·macOS·WSL·Git Bash. docker(+compose v2)·git·tar·gzip·sha256sum 필요 |
| `load-offline.sh` (번들에 동봉) | **고객사 서버** (폐쇄망) | Linux·macOS. docker·gzip·sha256sum 필요 |

#### 5.1.1 개발측 — 매체에 담아 갈 것

저장소 루트에서 한 줄이다.

```sh
sh scripts/bundle-offline.sh
```

이미지 빌드 → `docker save` → 센서 바이너리 크로스 빌드 → 소스·compose·매뉴얼 수집 → SHA-256 체크섬 생성까지 수행하고, `dist/` 아래에 결과를 만든다. **반입 매체에 담을 것은 아래 두 파일뿐이다.**

```
dist/autocert-offline-<버전>.tar           # 번들 전체
dist/autocert-offline-<버전>.tar.sha256    # 매체 손상 판별용
```

주요 옵션: `--version <버전>` / `--output <디렉터리>` / `--skip-build`(이미 빌드된 이미지 사용) / `--skip-sensor` / `--no-archive`. 전체 목록은 `sh scripts/bundle-offline.sh --help`.

번들(`.tar`)을 풀면 다음이 들어 있다.

| 경로 | 내용 |
| --- | --- |
| `MANIFEST.txt` | 제품 버전·생성 일시(UTC)·소스 커밋·**포함 이미지 태그와 이미지 ID** |
| `SHA256SUMS` | 번들 안 모든 파일의 SHA-256 (적재 스크립트가 이걸로 대조한다) |
| `load-offline.sh` | 고객사에서 실행할 검증·적재 스크립트 |
| `images/` | `postgres:16-alpine`, `inherit-autocert/backend:<버전>`, `inherit-autocert/frontend:<버전>` (docker save + gzip) |
| `sensor/` | 센서 바이너리 linux/windows amd64 + 자체 SHA256SUMS (SENSOR.md 2장) |
| `source/` | 제품 소스 아카이브 (`git archive` — `.env` 등 비밀값 파일은 원천적으로 들어가지 않는다) |
| `manual/` | INSTALL·OPERATIONS·SENSOR 매뉴얼 |
| `docker-compose.yml`, `.env.example` | 참고용 사본 (실제 기동은 `source/`를 푼 디렉터리에서 한다) |

- **빌드 단계 전용 베이스 이미지(`golang`·`node`)는 담지 않는다.** 두 Dockerfile 모두 멀티스테이지라 최종 이미지에 실행에 필요한 레이어가 이미 포함되어 있다.
- 번들 크기는 이미지 3종 기준 **약 200 MB**다(센서 바이너리 포함 시 +13 MB).

#### 5.1.2 이미지 이름과 버전 태그

이미지 이름은 compose에 고정되어 있다 — compose 프로젝트명에 따라 이름이 달라지지 않는다.

| 서비스 | 이미지 |
| --- | --- |
| backend | `inherit-autocert/backend:<버전>` |
| frontend | `inherit-autocert/frontend:<버전>` |
| db | `postgres:16-alpine` (공식 이미지 그대로) |

- 버전 기본값은 저장소 루트의 `VERSION` 파일이며 `docker-compose.yml`의 기본 태그와 같아야 한다(다르면 번들 스크립트가 중단한다). 변경 이력은 `CHANGELOG.md`.
- `.env`에 `AUTOCERT_VERSION`·`AUTOCERT_IMAGE_PREFIX`를 넣으면 태그와 접두어를 덮어쓸 수 있다. **사내 레지스트리에 올려 쓰는 경우** `AUTOCERT_IMAGE_PREFIX=registry.internal/autocert` 같은 값을 쓰고, 이때는 반입 번들 대신 레지스트리 push/pull로 대체해도 된다.

#### 5.1.3 고객사 — 현장에서 실행할 것

매체를 서버에 복사한 뒤, 아래가 전부다.

```sh
sha256sum -c autocert-offline-0.1.0.tar.sha256   # 매체 손상 확인
tar xf autocert-offline-0.1.0.tar
cd autocert-offline-0.1.0
sh load-offline.sh                                # 체크섬 대조 + docker load
tar xzf source/autocert-source-0.1.0.tar.gz       # autocert-0.1.0/ 으로 풀린다
cd autocert-0.1.0
```

`load-offline.sh`는 ① `SHA256SUMS` 전건 대조 ② `docker load` ③ 매니페스트의 이미지 태그가 실제로 적재됐는지 확인, 순서로 진행한다.

- **체크섬이 하나라도 어긋나면 적재하지 않고 즉시 중단한다.** 이때는 매체를 다시 복사해 받는다 — 부분 적재나 재시도로 넘기지 않는다.
- 적재 없이 검증만 하려면 `sh load-offline.sh --verify-only`.
- 이후 5.2절(환경 변수) → 5.3절(기동) 순으로 진행한다. **기동 시 `--build`를 붙이지 않는다** — 적재한 이미지를 그대로 쓴다.
- 센서를 쓸 경우 `sensor/` 의 바이너리를 센서 호스트로 옮긴다 (SENSOR.md 2장).

### 5.2 환경 변수 설정

```sh
cp .env.example .env
```

`.env`를 열어 아래 값을 설정한다. **반드시 채워야 하는 값은 `POSTGRES_PASSWORD` 하나뿐이다.** 나머지는 기본값으로 두어도 제품이 정상 동작한다 — 관리자 계정은 설치 후 브라우저 첫 접속 화면에서 만든다 (6.1절).

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `POSTGRES_USER` | `autocert` | DB 사용자 |
| `POSTGRES_PASSWORD` | (없음 — **유일한 필수 입력**) | DB 비밀번호. **반드시 강한 값으로 변경한다.** 예시 값 그대로 운영 금지 |
| `POSTGRES_DB` | `autocert` | DB 이름 |
| `BOOTSTRAP_ADMIN_USERNAME` | `admin` | 선택. 아래 `BOOTSTRAP_ADMIN_PASSWORD`를 쓸 때의 초기 관리자 계정명 |
| `BOOTSTRAP_ADMIN_PASSWORD` | (없음) | 선택 — **무인 설치·자동화용이다.** 값을 넣으면 최초 기동 시 사용자가 하나도 없을 때 이 값으로 초기 관리자를 **1회** 생성한다. 비워 두는 것이 기본이며, 그 경우 첫 접속 시 브라우저에서 계정을 만든다 (6.1절) |
| `SESSION_TTL` | `8h` | 로그인 세션 유효기간. Go duration 형식 (`8h`, `30m`, `12h30m` 등) |
| `SESSION_COOKIE_SECURE` | `false` | 세션 쿠키에 `Secure` 플래그를 붙인다. **HTTPS 리버스 프록시로 TLS를 종단할 때만 `true`** — 평문 HTTP로 접속하면 브라우저가 쿠키를 버려 로그인이 조용히 실패한다. `docker-compose.https.yml`을 쓰면 자동으로 켜지므로 직접 넣을 필요는 없다 (TLS.md) |
| `SENSOR_CA_ENC_KEY` | (없음) | 센서 사용 시에만. 센서 CA 개인키 암호화 키 — hex 64자, `openssl rand -hex 32`로 생성. 미설정이면 센서 게이트웨이(8443)가 열리지 않는다. 설정하면 `SENSOR_ADVERTISED_HOSTS`도 반드시 함께 채운다 (SENSOR.md 3.1절) |
| `SENSOR_ADVERTISED_HOSTS` | (없음) | 센서 사용 시에만. 센서가 서버 접속에 쓸 **실제** DNS/IP 목록(쉼표 구분, 예: `10.20.0.5,autocert.internal`) — 게이트웨이 서버 인증서 SAN. 위 키를 설정했는데 이 값이 비면 센서 게이트웨이(8443)가 뜨지 않는다 — 8080 웹/API는 정상 기동한다 (SENSOR.md 3.1절). **포트를 적지 않는다** (`10.20.0.5:9443` ✗) — 포트가 붙은 항목은 기동 시 경고와 함께 무시된다 |
| `AUTOCERT_VERSION` | 릴리즈 버전 (`docker-compose.yml`에 고정) | 쓸 이미지의 버전 태그. **반입 번들 그대로 쓰면 설정할 필요가 없다** — 버전을 덮어써서 번들을 만든 경우에만 같은 값을 넣는다 (5.1.2절) |
| `AUTOCERT_IMAGE_PREFIX` | `inherit-autocert` | 이미지 이름 접두어. 사내 레지스트리를 쓸 때만 변경 (5.1.2절) |
| 포트 변경용 변수 | (5.5절) | `FRONTEND_PORT`·`BACKEND_PORT`·`SENSOR_GATEWAY_PORT` 등. 기본값을 쓸 것이면 넣지 않아도 된다 — 절차와 함께 봐야 하므로 **5.5절**에 모아 두었다 |

- `POSTGRES_PASSWORD`가 비어 있으면 기동이 거부된다(의도된 안전장치).
- 센서 관련 변수는 `.env.example`에 없다 — 센서를 쓸 때 `.env`에 직접 추가한다.
- **센서 변수는 세트다.** 센서를 쓰지 않으면 `SENSOR_CA_ENC_KEY`·`SENSOR_ADVERTISED_HOSTS`를 **둘 다 넣지 않는다.** `SENSOR_CA_ENC_KEY`만 넣고 `SENSOR_ADVERTISED_HOSTS`를 빈 값으로 두면 **센서 게이트웨이(8443)가 뜨지 않아 센서 기능을 쓸 수 없다.** 이때도 코어 API(8080)와 웹 콘솔은 정상 기동하므로 증상이 조용하다 — backend 로그의 경고 한 줄(`sensor gateway disabled: startup failed — check SENSOR_ADVERTISED_HOSTS …`)로만 확인된다. 센서를 쓸 것이면 두 값을 모두 실제 값으로 채운다 (SENSOR.md 9장).

### 5.2.1 알림 설정 (선택)

만료 임박 알림은 4채널이다 — **알림함(inbox) / 메일(email) / 웹훅(webhook) / Syslog**. 운영 절차는 OPERATIONS.md 2.7절.

아래 변수는 **`.env.example`에 없다.** 필요한 것만 `.env`에 직접 추가한다 (센서 변수와 같은 방식). 값은 `docker-compose.yml`이 `${변수:-기본값}` 형태로 backend에 그대로 넘긴다 — compose 파일을 고칠 필요는 없다.

**모든 값이 비어 있어도 정상이다.** 채널 설정이 비었거나 잘못되면 **그 채널만 비활성화**되고 코어 API(8080)·웹 콘솔·나머지 채널은 정상 기동한다 (센서 게이트웨이와 같은 실패 격리 원칙). 어떤 채널이 왜 꺼졌는지는 backend 기동 로그의 경고와 `GET /api/v1/notification-channels` 응답의 `reason`에서 **같은 문구**로 확인한다 (OPERATIONS.md 2.7절).

**공통**

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `NOTIFY_ENABLED` | `true` | 알림 기능 전체 on/off. `false`면 탐지·발송이 모두 멈춘다(설정 오류가 아니라 의도적 정지) |
| `NOTIFY_SCAN_INTERVAL` | `1h` | 만료 임박 판정 주기. Go duration 형식(`1h`, `30m`, `6h` 등). 임계값이 일 단위이므로 1시간보다 촘촘하게 둘 이유는 없다 |
| `NOTIFY_FIRST_RUN` | `seed` | 최초 판정 처리 방식. `seed` = 이미 지난 임계값은 **발송하지 않고 침묵 처리**(기준선만 잡는다) / `send` = 전부 발송. 아래 주의사항 참조 |
| `NOTIFY_RETENTION_DAYS` | `180` | 종료된 발송 이력(성공·실패·건너뜀) 보존 일수. 진행 중 건은 나이와 무관하게 남는다 |
| `NOTIFY_BASE_URL` | (없음) | 알림 본문에 넣을 웹 콘솔 기준 URL (예: `http://10.20.0.5:3000`). 비우면 본문에 인증서 딥링크가 붙지 않는다 — 알림 자체는 정상 발송된다. **HTTPS 구성이면 `https://autocert.example.internal`처럼 프록시 주소로 바꾼다**(포트 443은 표기 생략) — 안 바꾸면 메일 링크만 평문 주소로 나간다 (TLS.md 5장) |

**메일 (SMTP)** — `SMTP_HOST`가 비면 메일 채널 비활성.

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `SMTP_HOST` | (없음) | 사내 메일 서버 호스트. **비면 메일 채널만 비활성** |
| `SMTP_PORT` | `587` | STARTTLS 587 / implicit TLS 465 / 평문 25가 일반적 |
| `SMTP_FROM` | (없음) | 보내는 사람 주소. 메일 서버가 요구하면 필수 |
| `SMTP_USERNAME` | (없음) | 인증 계정. 비우면 인증 없이 발송(내부 릴레이 구성) |
| `SMTP_PASSWORD` | (없음) | 인증 비밀번호 |
| `SMTP_PASSWORD_FILE` | (없음) | 비밀번호를 담은 **파일 경로**(Docker secret 등). **파일이 있으면 파일 값이 `SMTP_PASSWORD`보다 우선**한다 |
| `SMTP_TLS_MODE` | `starttls` | `starttls`(평문 접속 후 승격) \| `tls`(접속 즉시 TLS, 보통 465) \| `none`(암호화 없음 — 폐쇄망 내부 릴레이에서만) |
| `SMTP_CA_FILE` | (없음) | 메일 서버 인증서를 발급한 **사내 사설 CA 인증서 파일 경로**(PEM). 컨테이너 안 경로이므로 compose에 볼륨 마운트가 필요하다 |
| `SMTP_TLS_INSECURE_SKIP_VERIFY` | `false` | 서버 인증서 검증을 끈다. **권장하지 않는다** — 켜져 있으면 기동 시 경고 로그가 남는다 |

**웹훅** — `WEBHOOK_URL`이 비면 웹훅 채널 비활성.

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `WEBHOOK_URL` | (없음) | 알림 JSON을 POST할 사내 시스템 URL. **비면 웹훅 채널만 비활성** |
| `WEBHOOK_HMAC_SECRET` | (없음) | 설정하면 본문의 HMAC-SHA256 서명을 `X-AutoCert-Signature: sha256=<hex>` 헤더로 붙인다. 비우면 서명 헤더가 생략된다 |
| `WEBHOOK_AUTH_HEADER` | (없음) | 정적 인증 헤더 한 줄 (예: `Authorization: Bearer xxxxx`) |
| `WEBHOOK_CA_FILE` | (없음) | 수신 서버가 사설 CA 인증서를 쓸 때의 CA 파일 경로(PEM) |

**Syslog** — `SYSLOG_ADDR`이 비면 Syslog 채널 비활성.

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `SYSLOG_ADDR` | (없음) | SIEM·중앙 로그 수집기 주소 `host:port` (예: `10.20.0.9:514`). **비면 Syslog 채널만 비활성** |
| `SYSLOG_NETWORK` | `udp` | `udp` \| `tcp`. 아래 주의사항 ② |
| `SYSLOG_FACILITY` | `local0` | RFC5424 facility |
| `SYSLOG_APP_NAME` | `autocert` | RFC5424 APP-NAME. 수집기에서 이 값으로 필터링한다 |

메시지는 RFC5424 형식이며, TCP는 RFC6587 octet-counting 프레이밍을 쓴다.

**주의사항**

1. **사설 CA는 `SMTP_CA_FILE`로 신뢰시킨다.** 폐쇄망에서 메일 서버가 사내 사설 CA 인증서를 쓰는 것은 정상이며, 이때 `SMTP_TLS_INSECURE_SKIP_VERIFY=true`로 우회하지 **않는다.** 검증을 끄면 메일 서버 사칭이 가능해지고, 알림 본문에는 "어느 시스템의 어떤 인증서가 언제 만료되는지"가 담긴다 — 정찰 가치가 있는 정보다. CA 파일을 backend 컨테이너에 마운트하고 그 경로를 지정하는 것이 정상 경로다.
2. **`SYSLOG_NETWORK=udp`는 fire-and-forget이다.** 발송이 "성공"으로 기록되어도 수집기가 실제로 받았다는 보장이 없다(UDP에는 수신 확인이 없다). 누락되면 안 되는 용도라면 `tcp`를 쓴다.
3. **알림함(inbox) 채널은 외부 설정 없이 기본 활성**이다. 위 변수를 하나도 채우지 않아도 웹 콘솔 알림함으로는 알림이 온다 — 설치 직후 아무것도 밖으로 나가지 않으면서 알림이 동작하는 기본값이다.
4. **메일은 at-least-once다.** SMTP 서버가 메시지를 수락한 직후 backend 프로세스가 죽으면 같은 메일이 한 통 더 갈 수 있다. "전송했다"와 "전송했음을 기록했다" 사이에 원자성이 없고, 외부 SMTP 서버를 상대로 exactly-once는 원리적으로 불가능하다. 알림함은 중복이 구조적으로 불가능하고, 웹훅은 페이로드의 `event_id`로 수신 측에서 중복 제거할 수 있다.
5. **방화벽**: 메일·웹훅·Syslog를 쓰면 AutoCert 서버 → 해당 수신 서버 방향 아웃바운드가 필요하다(4.2절 표에는 스캔·DNS만 적혀 있다). 인터넷 방향은 여전히 불필요하다.
6. 자격증명은 **환경변수 전용**이다 — DB에 저장하지 않는다. 따라서 값을 바꾸면 `docker compose up -d backend`로 backend를 재기동해야 반영된다.

### 5.2.2 스캔 스윕 설정 (선택)

전 포트(1–65535) 백그라운드 스윕의 속도·분할 기본값이다. 운영 절차는 OPERATIONS.md 2.8절. 이 변수들도 `.env.example`에 없으므로 조정할 것만 `.env`에 직접 추가한다.

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `SCAN_SWEEP_ENABLED` | `true` | 스윕 기능 on/off. `false`면 스윕 생성이 거부된다(단발 스캔은 영향 없음) |
| `SCAN_SWEEP_RATE_PPS` | `10` | 스윕의 초당 접속 시도 수. **아래 경고 참조** |
| `SCAN_SWEEP_CONCURRENCY` | `8` | 스윕 워커 동시 연결 수 |
| `SCAN_SWEEP_CHUNK_ENDPOINTS` | `4096` | 청크 하나의 최대 엔드포인트 수(IP 수 × 포트 수) |
| `SCAN_SWEEP_CHUNK_GAP` | `5s` | 청크 발주 사이의 공백. Go duration 형식 |
| `SCAN_SWEEP_MAX_DAYS` | `30` | 예상 소요가 이 일수를 넘는 스윕은 생성 시 **422로 거부**된다(응답에 실제 예상 일수가 들어간다) |
| `SCAN_SWEEP_STALL_AFTER` | `10m` | 자식 청크가 이 시간 이상 대기 상태에 머무르면 스윕을 "센서 대기"로 표시한다 |

- **값이 잘못되어도 backend는 정상 기동한다.** 경고 로그에 변수명을 지목하고 기본값으로 폴백한다 (예: `SCAN_SWEEP_RATE_PPS="fast" is not an integer; falling back to 10`). 스캔 튜닝 값 오타로 인벤토리 조회까지 막히지 않는다.
- 값 변경은 backend 재기동 후 **새로 만드는 스윕**부터 적용된다. 이미 도는 스윕의 레이트는 생성 시점 값으로 고정된다.

> **경고 — `SCAN_SWEEP_RATE_PPS`를 올리기 전에 읽을 것**
>
> 이 값을 올리면 스윕이 빨라지지만, **방화벽·IPS·NDR이 포트스캔으로 탐지해 AutoCert 서버 IP를 차단**할 수 있다. 차단되면 스윕뿐 아니라 일반 Discovery 스캔까지 함께 죽는다. 기본값 10 pps는 "며칠 걸려도 눈에 띄지 않게" 도는 것을 노린 값이다.
>
> 실제 소요 예: **/24 한 개(254개 IP)를 전 포트(65,535) 스윕하면 254 × 65,535 = 약 1,665만 엔드포인트이고, 기본 10 pps에서 약 19일**이 걸린다(청크 간 공백 포함 시 조금 더). 100 pps로 올리면 약 2일이지만 탐지 위험은 그만큼 커진다. 상한은 1,000 pps이며 그보다 큰 값은 1,000으로 깎이고 경고 로그가 남는다.
>
> 값을 올려야 한다면 **먼저 보안팀에 스캔 출발지 IP를 예외 등록**하고, 좁은 대역에서 시험한 뒤 확대한다. 반대로 IPS 경보가 뜨면 이 값을 낮추는 것이 1차 대응이다.

### 5.2.3 관리 콘솔 접근 IP 제한 (선택)

웹 콘솔과 API에 접속할 수 있는 **출발지 IP를 제한**하는 기능이다. 허용 목록 자체는 DB에 저장하고 웹 콘솔(관리 → 접근 제한)에서 관리하므로 환경변수로 넣지 않는다. 운영 절차는 OPERATIONS.md 2.9절.

**허용 목록이 비어 있으면 제한이 없다.** 설치 직후·업그레이드 직후에는 아무 규칙도 없으므로 이 기능이 기존 접속을 막는 일은 없다.

아래 두 변수만 환경변수다. 둘 다 `.env.example`에 없으므로 쓸 것만 `.env`에 직접 추가한다.

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `ADMIN_IP_TRUSTED_PROXIES` | (없음) | `X-Forwarded-For`를 신뢰할 **직접 접속** 대역(콤마 구분 IP/CIDR). 비면 XFF를 아예 보지 않고 직접 접속 IP만 쓴다 |
| `ADMIN_IP_ALLOW_BOOTSTRAP` | (없음) | 허용 목록과 무관하게 통과시키는 **비상 우회 대역**(콤마 구분 IP/CIDR). 화면에서 자신을 막았을 때의 복구 경로. 비면 미적용 |

> **중요 — 이 제품의 기본 구성에서는 `ADMIN_IP_TRUSTED_PROXIES`가 사실상 필수다**
>
> 브라우저는 frontend 컨테이너에 접속하고 frontend가 backend로 중계한다(3.3절). 그래서 backend가 보는 출발지는 **관리자 PC가 아니라 frontend 컨테이너**다. 이 변수를 비워두면 모든 접속이 같은 IP 하나로 보여 IP별 제한이 성립하지 않는다.
>
> compose 기본 네트워크는 Docker가 자동 할당하므로, 먼저 실제 대역을 확인한 뒤 넣는다.
>
> ```sh
> docker network inspect inherit_autocert_default --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}'
> # 예: 172.18.0.0/16  → .env에 ADMIN_IP_TRUSTED_PROXIES=172.18.0.0/16
> ```
>
> 앞단에 별도의 리버스 프록시를 두었다면 그 프록시 대역도 함께 넣는다(HTTPS 오버레이의 `proxy` 서비스는 같은 compose 네트워크에 있으므로 위 대역에 포함된다 — 5.4절·TLS.md). **신뢰하지 않는 대역을 넣지 말 것** — 그 대역에서 오는 요청은 헤더 한 줄로 출발지를 위조할 수 있다.

- 값이 IP/CIDR 형식이 아니면 그 항목만 무시하고 경고 로그를 남긴다 — 오타 하나로 기동이 막히면 복구 경로까지 함께 사라지기 때문이다.
- 변경은 backend 재기동(`docker compose up -d backend`) 후 적용된다. **허용 목록(화면에서 하는 추가·삭제·토글)은 재기동 없이 즉시 반영된다.**
- 규칙 변경으로 요청자 자신이 차단되는 경우 서버가 409로 거부한다. 그래도 잠겼다면 `ADMIN_IP_ALLOW_BOOTSTRAP`으로 복구한다 — 설정해 두는 것을 권장한다.

### 5.2.4 주기 재스캔 설정 (선택)

**이미 발견한 엔드포인트**를 주기적으로 다시 확인해 인벤토리 신선도를 유지하고, 인증서가 교체된 것을 감지한다. 새 대역을 찾는 기능이 아니다 — 신규 발견은 단발 스캔과 스윕(5.2.2) 소관이다. 운영 절차는 OPERATIONS.md 2.10절. 이 변수들도 `.env.example`에 없으므로 조정할 것만 `.env`에 직접 추가한다.

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `RESCAN_ENABLED` | `true` | 재스캔 on/off. `false`면 재확인도 교체 감지도 하지 않는다(수동 스캔은 영향 없음) |
| `RESCAN_INTERVAL` | `12h` | 기본 재확인 간격. Go duration 형식, 하한 `1m` |
| `RESCAN_RATE_PPS` | `20` | 재스캔의 초당 접속 시도 수. 스윕(10)보다 빠르고 단발 스캔(50)보다 느리다 |
| `RESCAN_BATCH_IPS` | `1024` | 한 번에 다시 볼 엔드포인트 상한. 초과분은 다음 차례로 넘어간다 |
| `RESCAN_REPLACE_CONFIRM` | `2` | 교체 확정에 필요한 연속 관측 횟수 |
| `RESCAN_RETIRE_MISSES` | `30` | 이만큼 연속으로 안 보이면 그 엔드포인트를 재스캔 대상에서 뺀다(은퇴) |

- **만료 임박 인증서는 자동으로 더 자주 본다.** `RESCAN_INTERVAL`과 무관하게 잔여 **30일 이하는 6시간**, **7일 이하는 1시간** 간격이다. 이 두 값은 설정이 아니라 고정값이다 — 갱신은 만료 직전에 일어나므로 그 구간에서 자주 보는 것이 이 기능의 값어치 대부분이다.
- **값이 잘못되어도 backend는 정상 기동한다.** 경고 로그에 변수명을 지목하고 기본값으로 폴백한다 (예: `RESCAN_INTERVAL="10s" is below the minimum 1m0s; falling back to 12h0m0s`).
- **`RESCAN_REPLACE_CONFIRM`을 1로 낮추지 않는다.** 로드밸런서 뒤에 서로 다른 인증서를 든 서버가 섞여 있으면 관측 결과가 A→B→A→B로 번갈아 나오고, 1회 관측으로 확정하면 두 인증서가 서로를 무한히 "교체됨"으로 만든다. 기본값 2는 그 진동을 멈추게 하는 최솟값이다.
- **은퇴는 삭제가 아니다.** 은퇴한 엔드포인트도 이력은 그대로 남고, 수동 스캔·스윕으로 다시 관측되면 자동으로 재스캔 대상에 복귀한다.
- 센서 위임 세그먼트(`sensor:<id>` 스코프)의 엔드포인트는 그 센서가 **활성일 때만** 발주한다. 센서가 꺼져 있으면 작업이 쌓이지 않고 그냥 건너뛴다.
- 값 변경은 backend 재기동 후 다음 차례부터 적용된다.

### 5.3 기동

```sh
docker compose up -d
```

- **폐쇄망에서는 `--build`를 붙이지 않는다.** 5.1절로 적재한 `inherit-autocert/backend:<버전>`·`inherit-autocert/frontend:<버전>` 이미지를 그대로 쓴다. `--build`는 인터넷(또는 내부 미러)이 되는 환경에서 소스로부터 다시 빌드할 때만 쓴다.
- `docker compose up`이 이미지를 찾지 못하면(`manifest unknown`·`pull access denied`) 적재가 안 됐거나 `.env`의 `AUTOCERT_VERSION`이 적재한 태그와 다른 경우다 — `docker image ls` 결과를 번들의 `MANIFEST.txt`와 대조한다.
- 최초 기동 시 backend가 DB 연결 후 **SQL 마이그레이션을 자동 적용**한다. 별도의 스키마 생성 작업이 필요 없다.
- DB 데이터는 Docker 볼륨 `pgdata`에 영속 저장된다.

동작 확인:

```sh
curl http://localhost:8080/healthz   # HTTP 200이면 정상 (BACKEND_PORT를 바꿨다면 그 포트로)
```

브라우저에서 `http://<서버 IP>:3000` 접속 → **초기 설정 화면**("관리자 계정 만들기")이 표시되면 설치 완료다. 여기서 첫 관리자 계정을 만든다 (6.1절). `BOOTSTRAP_ADMIN_PASSWORD`로 이미 계정을 만들었다면 이 화면 대신 로그인 화면이 나온다. 포트를 바꿨다면 `FRONTEND_PORT` 값으로 접속한다 (5.5절).

### 5.4 운영 환경 보강 (권장)

- **PostgreSQL 포트 비노출**: 기본 compose 구성은 DB를 호스트에 **노출하지 않는다**(`db` 서비스에 `ports` 없음). 백업·점검으로 잠깐 열어야 하면 5.5.3절의 오버레이 파일을 쓰고, 끝나면 되돌린다.
- **API 포트 비노출**: 웹 콘솔만 사용한다면 `backend`의 `ports`(8080)도 호스트 노출을 제한할 수 있다 — 웹 콘솔은 컨테이너 내부 네트워크로 backend에 접근하므로 영향이 없다 (3.3절). `.env`에 `BACKEND_PORT=127.0.0.1:8080` 같은 값은 넣을 수 없으므로, 제한이 필요하면 `docker-compose.yml`의 해당 줄을 `"127.0.0.1:${BACKEND_PORT:-8080}:${BACKEND_INTERNAL_PORT:-8080}"`으로 고친다.
- **API 중계 주소**: frontend가 중계할 backend 주소는 **빌드 시점**에 `BACKEND_INTERNAL_URL` 빌드 인자로 고정된다 (compose 기본 `http://backend:8080` — 컨테이너 내부 주소). 브라우저의 접속 주소와 무관하므로, 관리자가 어떤 IP/도메인으로 접속하든 **바꿀 필요 없다.** backend를 별도 호스트로 분리하거나 컨테이너 내부 포트를 바꾸는 경우에만 값이 달라지며, 이때는 frontend **재빌드**가 필수다 (5.5절).
- **접근 IP 제한**: 관리 콘솔 접속을 특정 대역으로만 열려면 5.2.3절의 신뢰 프록시 대역을 설정한 뒤 웹 콘솔(관리 → 접근 제한)에서 허용 목록을 만든다. 목록이 비어 있으면 제한이 없다.
- **TLS 종단(HTTPS)**: 웹 콘솔 앞단에 nginx 리버스 프록시를 얹어 HTTPS로 종단할 수 있다. 사내 CA 발급 인증서나 자체 서명 인증서를 파일로 반입해 쓰며(폐쇄망이라 외부 ACME는 쓰지 않는다), 오버레이 파일 하나로 켜고 끈다 — 적용하지 않으면 기존 HTTP 구성 그대로다. 절차·인증서 준비·갱신·트러블슈팅은 **TLS.md** 참조.

```sh
# HTTPS 구성으로 기동 (deploy/tls/ 에 server.crt·server.key를 먼저 둔다)
docker compose -f docker-compose.yml -f docker-compose.https.yml up -d --build
```

### 5.5 포트 변경 절차

포트는 전부 바꿀 수 있다. **호스트 포트**(밖에서 접속하는 포트)만 바꾸는 것이 가장 안전하며, 컨테이너 내부 포트는 특별한 사유가 없으면 건드리지 않는다.

#### 5.5.1 무엇을 함께 고쳐야 하는가

| 바꾸려는 것 | `.env` 변수 (기본값) | 함께 고칠 것 | 재빌드 |
| --- | --- | --- | --- |
| **웹 콘솔(화면) 포트** | `FRONTEND_PORT` (3000) | ① 방화벽 인바운드 규칙 (4.1절 첫 행) ② `NOTIFY_BASE_URL`의 포트 (5.2.1절 — 알림 본문 딥링크가 옛 포트를 가리키게 된다) ③ 관리자에게 공지할 접속 주소 | 불필요 |
| **REST API 포트** (호스트) | `BACKEND_PORT` (8080) | 방화벽 인바운드 규칙 (4.1절 둘째 행), API를 직접 호출하는 스크립트·연동 설정 | 불필요 |
| **센서 게이트웨이 포트** (호스트) | `SENSOR_GATEWAY_PORT` (8443) | ① 방화벽: **센서 호스트 → 서버** 아웃바운드/인바운드 규칙 (4.1절 셋째 행, SENSOR.md 3.2절) ② 각 센서의 서비스 유닛 `ExecStart ... --server https://<서버 주소>:<새 포트>` (SENSOR.md 6.1·6.2절) — 센서마다 유닛 파일 수정 후 재시작 | 불필요 |
| **REST API 포트** (컨테이너 내부) | `BACKEND_INTERNAL_PORT` (8080) | frontend의 프록시 대상(`BACKEND_INTERNAL_URL`)이 이 값에서 파생된다 | **필요** — `docker compose up --build -d` |
| **센서 게이트웨이 포트** (컨테이너 내부) | `SENSOR_GATEWAY_INTERNAL_PORT` (8443) | `SENSOR_LISTEN_ADDR`이 이 값에서 파생된다 (compose에서는 `SENSOR_LISTEN_ADDR`을 직접 지정하지 않는다 — 리스너 포트와 매핑 포트가 어긋나는 사고를 막기 위해 단일 변수로 묶었다) | 불필요 |
| **DB 포트** | (기본 미노출) | 5.5.3 참조 | 불필요 |
| **HTTPS 프록시 포트** (HTTPS 구성일 때만) | `HTTPS_PORT` (443) / `HTTP_REDIRECT_PORT` (80) | 방화벽 인바운드 규칙 (4.1절 둘째 행), `NOTIFY_BASE_URL`(5.2.1절), 관리자 공지 주소 | 불필요 |

- 변경 후 반영: `docker compose up -d` (내부 포트를 바꿨으면 `--build` 포함). 컨테이너 재생성이 필요하므로 `restart`만으로는 반영되지 않는다.
- **HTTPS 오버레이(`docker-compose.https.yml`)도 같은 변수를 쓴다.** 오버레이는 `FRONTEND_PORT`·`BACKEND_PORT`를 `127.0.0.1`에 묶어 프록시를 통해서만 들어오게 할 뿐, 변수 이름과 기본값은 base와 같다 (TLS.md).
- **`SENSOR_ADVERTISED_HOSTS`에는 포트를 적지 않는다.** 이 값은 게이트웨이 서버 인증서의 SAN이며 주소만 들어간다. `10.20.0.5:9443`처럼 포트를 붙이면 기동 로그에 경고(`SENSOR_ADVERTISED_HOSTS entry "…" includes a port …`)를 남기고 그 항목은 무시된다.
- 센서 등록 화면의 **등록 명령 예시는 설정된 포트를 그대로 보여준다** — 포트를 바꿨어도 화면의 명령을 그대로 복사해 쓰면 된다.

#### 5.5.2 센서 포트를 바꿔도 재등록은 필요 없다

접속 포트는 센서 인증서의 SAN에도, 센서 상태 디렉토리에도 기록되지 않는다 (SENSOR.md 5.3절). 따라서 게이트웨이 포트 변경은 **주소 변경일 뿐이며 재등록(enroll) 대상이 아니다.** 각 센서에서 `--server`의 포트만 새 값으로 고쳐 서비스를 재시작하면 기존 인증서로 그대로 붙는다. 재등록이 필요한 경우는 인증서 만료(90일 이상 정지)나 상태 디렉토리 삭제뿐이다 (SENSOR.md 8장).

DNS 이름·IP 자체를 바꾸는 것은 이야기가 다르다 — 그 값은 SAN에 들어가므로 `SENSOR_ADVERTISED_HOSTS`에 새 주소를 추가하고 backend를 재기동해야 한다(서버 인증서는 기동 시 발급). 이때도 센서 재등록은 필요 없다.

#### 5.5.3 DB 포트를 임시로 여는 방법

기본 구성은 PostgreSQL을 호스트에 노출하지 않는다. 인증서 인벤토리 전체가 걸린 포트라 상시 개방하지 않는 것이 기본값이며, backend는 compose 내부 네트워크로 접속하므로 노출 없이도 정상 동작한다.

**표준 백업·점검 절차에는 노출이 필요 없다** — `docker compose exec db pg_dump …` / `docker compose exec db psql …`로 컨테이너 안에서 실행한다 (OPERATIONS.md 4.1절). 외부 DB 도구로 붙어야 하는 예외적인 경우에만 아래 오버레이를 겹친다.

```sh
# 루프백(127.0.0.1)에만, 호스트 포트 15432로 노출
DB_PORT=15432 docker compose -f docker-compose.yml -f docker-compose.db-expose.yml up -d
# 작업이 끝나면 원래 구성으로 되돌린다 (노출 해제)
docker compose up -d
```

- `DB_PORT`(기본 5432)로 호스트 포트를, `DB_BIND_ADDR`(기본 `127.0.0.1`)로 바인드 주소를 바꾼다. 외부 호스트에서 붙어야 할 때만 `DB_BIND_ADDR=0.0.0.0`으로 열고, 방화벽으로 출발지를 제한한 뒤 작업이 끝나면 반드시 되돌린다.

## 6. 초기 설정

### 6.1 초기 관리자 계정과 로그인

**기본 경로 — 브라우저에서 만든다.** 설치 직후에는 계정이 하나도 없다. `http://<서버 IP>:3000`에 접속하면 로그인 화면 대신 **초기 설정 화면**이 열린다.

1. 계정명(아이디)과 비밀번호, 비밀번호 확인을 입력하고 [관리자 계정 만들기]를 누른다.
2. 만들어진 계정은 **System Administrator** 역할이다.
3. 로그인 화면으로 이동한다 — 방금 정한 계정으로 로그인하면 설치가 끝난다.

- **이 화면은 사용자가 0명일 때만 열린다.** 계정이 하나라도 생기는 순간 화면과 API(`POST /api/v1/setup/admin`)가 함께 닫히며, 이후 접근하면 로그인 화면으로 되돌아간다. 설정을 다시 열 수 있는 스위치는 없다.
- **비밀번호 정책**: 최소 12자, 최대 72바이트, 계정명과 동일 금지. 위반하면 계정이 만들어지지 않는다.
- 두 번째 이후의 계정은 웹 콘솔 [사용자] 화면에서 만든다 (6.2절).
- 이 화면에 계정명·비밀번호 외의 설정은 없다. 메일 주소 등은 로그인 후 [사용자] 화면에서 채운다.

**대안 경로 — 무인 설치(자동화).** 설치를 스크립트로 돌려 사람이 브라우저를 열지 않는 환경을 위해 환경변수 경로를 유지한다. `.env`에 `BOOTSTRAP_ADMIN_PASSWORD`를 넣어 두면, 최초 기동 시 사용자가 하나도 없을 때 backend가 `BOOTSTRAP_ADMIN_USERNAME`(기본 `admin`) 계정을 System Administrator로 **1회** 생성한다. 이 경우 첫 접속 시 초기 설정 화면 대신 로그인 화면이 나온다.

- 값을 넣지 않으면 계정은 만들어지지 않고 backend 로그에 안내 한 줄만 남는다 — `no users exist; create the first administrator on the web setup screen …`. 오류가 아니다.
- 사용자가 1명이라도 존재하면 이 변수는 무시된다(재기동해도 덮어쓰지 않는다). 초기 생성 후에는 `.env`에서 `BOOTSTRAP_ADMIN_PASSWORD`를 지우는 것을 권장한다 — 파일에 비밀번호가 남지 않게 한다.

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
- 사용자 관리는 웹 콘솔 [사용자] 화면에서 한다 — 생성·수정·비활성화/활성화·비밀번호 재설정 (OPERATIONS.md 2.6절). 메뉴와 화면 모두 **System Administrator 전용**이다.
- 같은 작업의 API도 제공한다 (전부 System Administrator 전용): `POST /api/v1/users`(생성) / `GET /api/v1/users`(목록) / `PATCH /api/v1/users/{id}`(표시이름·역할) / `POST /api/v1/users/{id}/disable`·`/enable` / `POST /api/v1/users/{id}/password`(비밀번호 재설정, 성공 204).

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

### 6.4 알림 채널

만료 임박 알림은 설치 직후 **알림함(inbox) 채널로 바로 동작한다** — `.env`에 아무것도 넣지 않아도 System Administrator 역할에게 웹 콘솔 알림함으로 D-90/60/30/7/1 알림이 간다(기본 정책 1건이 심어져 있다).

메일·웹훅·Syslog로 내보내려면 5.2.1절의 환경변수를 채우고 backend를 재기동한다. 정책(임계값·채널·수신자·대상 필터) 편집과 발송 이력 확인은 웹 콘솔에서 한다 — OPERATIONS.md 2.7절.

## 7. 제거

```sh
docker compose down        # 컨테이너만 제거 (데이터 유지)
docker compose down -v     # 데이터 볼륨까지 완전 삭제 — 복구 불가, 백업 후 실행할 것
```
