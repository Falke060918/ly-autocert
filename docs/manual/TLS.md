# HTTPS(TLS 종단) 구성 매뉴얼

> **문서 상태**: 리버스 프록시 오버레이(`docker-compose.https.yml`) + 세션 쿠키 Secure 설정 기준.
> 이 구성은 **선택**이다. 적용하지 않으면 기존 HTTP 구성 그대로 동작한다 (INSTALL.md 5.3절).

## 1. 개요

기본 설치의 웹 콘솔(3000)과 REST API(8080)는 **평문 HTTP**다. 폐쇄망이라도 관리 콘솔에는
세션 쿠키·인증서 인벤토리·감사 로그가 오가므로, 운영 환경에서는 앞단에 **HTTPS 리버스 프록시**를
두어 TLS로 종단하는 것을 권장한다.

```
관리자 브라우저 ──HTTPS(443)──▶ proxy(nginx) ──HTTP──▶ frontend(3000) ──HTTP──▶ backend(8080) ──▶ db
                                  [TLS 종단]              (Docker 내부 네트워크)
```

- **프록시는 nginx(`nginx:1.27-alpine`)를 쓴다.** 이미지 1개(약 75 MB)만 반입하면 되고 설정이
  파일 하나로 끝나며, 폐쇄망에서 검증·인수인계가 가장 쉬운 표준 소프트웨어이기 때문이다.
  Caddy·Traefik은 자동 발급(ACME)이 강점인데 폐쇄망에서는 그 강점을 쓸 수 없다.
- **외부 ACME(Let's Encrypt 등)는 쓰지 않는다.** 인증서는 **사내 CA에서 발급받은 파일**이나
  **자체 서명 인증서**를 반입해 마운트한다 (3장).
- 이 구성은 base `docker-compose.yml`을 수정하지 않는다. 오버레이 파일을 얹을 때만 프록시가 뜬다.

## 2. 준비물

| 항목 | 요구사항 |
| --- | --- |
| 서버 인증서 | 관리자가 브라우저에 입력할 **접속 주소가 SAN에 포함**되어야 한다. DNS 이름으로 접속하면 `DNS:`, IP로 접속하면 `IP:` 항목이 필요하다. CN만 있고 SAN이 없으면 최신 브라우저가 거부한다 |
| 개인키 | **암호(passphrase) 없이** 저장한다. 암호가 걸려 있으면 nginx가 기동 중 입력을 기다리다 멈춘다 |
| 체인 파일 | `server.crt`는 **리프 인증서 → 중간 CA** 순서로 이어붙인 PEM이다. 루트는 넣지 않아도 된다 |
| 키 알고리즘 | RSA 3072 또는 ECDSA P-256. 사내 CA 정책이 있으면 그쪽을 따른다 |
| 프록시 이미지 | `nginx:1.27-alpine` 1개를 함께 반입한다(약 75 MB). 폐쇄망 반입 방식은 INSTALL.md 2.2절과 같다 — `docker save`/`docker load` 또는 내부 레지스트리 |

## 3. 인증서 준비

### 3.1 사내 CA에서 발급받기 (권장)

서버에서 키와 CSR을 만들고 **CSR만** 사내 CA에 제출한다. 개인키는 서버를 떠나지 않는다.

```sh
mkdir -p deploy/tls
openssl req -new -newkey rsa:3072 -nodes \
  -keyout deploy/tls/server.key \
  -out    deploy/tls/server.csr \
  -subj   "/CN=autocert.example.internal" \
  -addext "subjectAltName=DNS:autocert.example.internal,IP:10.20.0.5"
chmod 600 deploy/tls/server.key
```

- `CN`·SAN의 이름과 IP는 **실제 접속 주소로 바꾼다** (위 값은 예시다).
- 발급받은 인증서를 `deploy/tls/server.crt`로 저장한다. 중간 CA가 있으면 리프 아래에 이어붙인다:

```sh
cat leaf.crt intermediate-ca.crt > deploy/tls/server.crt
```

### 3.2 자체 서명 인증서 (사내 CA가 없을 때)

```sh
mkdir -p deploy/tls
openssl req -x509 -newkey rsa:3072 -nodes -days 397 \
  -keyout deploy/tls/server.key \
  -out    deploy/tls/server.crt \
  -subj   "/CN=autocert.example.internal" \
  -addext "subjectAltName=DNS:autocert.example.internal,IP:10.20.0.5" \
  -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=serverAuth"
chmod 600 deploy/tls/server.key
```

자체 서명은 브라우저 경고가 뜬다. 경고를 없애려면 이 인증서를 관리자 PC의 신뢰 저장소에
설치해야 한다 (6장). 관리자가 여럿이면 사내 CA 발급이 결국 더 싸다.

### 3.3 파일 배치

```
deploy/tls/server.crt   # 리프(+중간 CA) PEM   권한 644
deploy/tls/server.key   # 개인키 PEM           권한 600
```

- 파일 이름은 고정이다 (`deploy/nginx/autocert.conf`가 이 이름으로 읽는다).
- `deploy/tls/`는 **git 미추적**이다(`.gitignore`). 실제 키·인증서를 저장소에 커밋하지 않는다.
- 컨테이너에는 **읽기 전용(`:ro`)** 으로 마운트된다.

## 4. 기동

```sh
docker compose -f docker-compose.yml -f docker-compose.https.yml up -d --build
```

이후 모든 compose 명령에 두 파일을 함께 넘긴다(안 넘기면 프록시 없는 기본 구성으로 되돌아간다).
매번 쓰기 번거로우면 셸 프로필에 넣어둔다:

```sh
export COMPOSE_FILE=docker-compose.yml:docker-compose.https.yml
```

오버레이가 바꾸는 것은 셋뿐이다.

| 대상 | 기본 구성 | HTTPS 오버레이 |
| --- | --- | --- |
| 관리자 접속 | `http://<서버>:3000` | `https://<서버>/` (80은 443으로 308 리다이렉트) |
| 호스트 공개 포트 | 3000·8080·8443 | 80·443·8443. 3000·8080은 `127.0.0.1`로만 바인딩 |
| 세션 쿠키 | Secure 없음 | `SESSION_COOKIE_SECURE=true` → Secure 플래그 부착 |

- **센서 게이트웨이(8443)는 그대로 열린다.** 그 자체가 mTLS라 TLS 종단 대상이 아니며,
  프록시를 거치면 클라이언트 인증서 검증이 깨진다 (SENSOR.md 3장).
- `ports: !override` 문법은 **Docker Compose v2.24.4 이상**이 필요하다. 더 낮은 버전이면
  오버레이의 `ports` 블록 2개를 지우고 base의 `ports`를 직접 손본다(지우지 않으면 포트가
  병합되어 3000·8080이 계속 외부에 열린다).

### 4.1 확인

```sh
# 인증서와 종단 확인 (자체 서명이면 -k)
curl -kI https://<서버 주소>/

# 80 → 443 리다이렉트 확인
curl -I http://<서버 주소>/

# 세션 쿠키에 Secure가 붙는지 (로그인 응답 헤더)
curl -k -i -X POST https://<서버 주소>/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<비밀번호>"}' | grep -i set-cookie
# → set-cookie: autocert_session=...; HttpOnly; Secure; SameSite=Lax
```

브라우저에서 `https://<서버 주소>/`로 로그인되면 완료다.

## 5. 애플리케이션 측 설정

| 설정 | 값 | 비고 |
| --- | --- | --- |
| `SESSION_COOKIE_SECURE` | `true` | 오버레이가 자동으로 켠다. `.env`에 명시하면 그 값이 우선한다 |
| `NOTIFY_BASE_URL` | `https://autocert.example.internal` | 알림 본문 딥링크 기준 URL. **http로 두면 알림 메일의 링크가 평문 주소로 나간다** — HTTPS 전환 시 함께 바꾼다. 포트가 443이면 포트 표기는 생략한다 (INSTALL.md 5.2.1) |
| `BACKEND_INTERNAL_URL` | `http://backend:8080` (그대로) | 컨테이너 내부 중계 주소다. 브라우저 접속 주소와 무관하므로 **바꾸지 않는다** (INSTALL.md 5.4) |
| HSTS | 프록시에서 설정 | `deploy/nginx/autocert.conf`에 주석으로 준비되어 있다. 웹 콘솔(Next.js) 쪽 헤더 설정은 건드리지 않아도 된다 |

- `SESSION_COOKIE_SECURE`는 **backend 재기동 시 반영**된다(자격증명·설정 변수 공통 규칙).
- backend는 프록시 뒤에서 평문 HTTP를 받으므로 요청만으로 HTTPS 여부를 알 수 없다. 그래서
  Secure 플래그는 자동 감지가 아니라 **설정값**이다. 값이 잘못돼도 기동은 막히지 않고 경고 로그
  후 `false`로 폴백한다.
- 감사 로그의 출발지 IP는 `X-Forwarded-For`의 첫 값을 우선 사용한다. 프록시를 두면 이 헤더가
  없을 때 모든 요청이 프록시 컨테이너 IP로 기록되므로, `deploy/nginx/autocert.conf`의
  `X-Forwarded-For` 헤더 설정을 **지우지 않는다.** 관리자 IP 제한 기능을 함께 쓰는 경우
  신뢰 프록시 설정은 해당 기능 문서를 따른다.

## 6. 클라이언트 신뢰 설정

### 6.1 CA 인증서 배포

사내 CA 발급이든 자체 서명이든, 관리자 PC의 신뢰 저장소에 **발급 CA 인증서(자체 서명이면 그
인증서 자체)** 를 설치해야 브라우저 경고가 사라진다.

- Windows: `certlm.msc` → 신뢰할 수 있는 루트 인증 기관 → 인증서 가져오기 (그룹 정책으로 일괄 배포 가능)
- Linux: `/usr/local/share/ca-certificates/`에 복사 후 `update-ca-certificates`
- macOS: 키체인 접근 → 시스템 → 인증서 추가 후 "항상 신뢰"

API를 스크립트로 호출하는 서버에도 같은 CA를 신뢰시킨다. 그렇지 않으면 `curl -k`(검증 생략)로
운영하게 되는데, 이는 중간자 공격을 막지 못하므로 상시 운영 방식으로 삼지 않는다.

### 6.2 HSTS는 CA 신뢰 배포 후에 켠다

`autocert.conf`의 `Strict-Transport-Security` 줄은 기본 주석 처리되어 있다.
**인증서가 신뢰되지 않은 상태에서 HSTS를 켜면 브라우저의 "위험을 감수하고 계속" 우회가 막혀
콘솔에 아예 접속할 수 없다.** CA 배포가 끝난 뒤 주석을 풀고 reload한다 (7장).

## 7. 인증서 갱신·교체

인증서 만료 전에 파일만 바꾸고 프록시를 reload하면 된다. **다운타임이 없다.**

```sh
# 1) 새 인증서·키를 같은 이름으로 교체 (기존 파일은 백업)
cp new-leaf-and-chain.pem deploy/tls/server.crt
cp new.key               deploy/tls/server.key
chmod 600 deploy/tls/server.key

# 2) 설정 검사 후 무중단 reload
docker compose -f docker-compose.yml -f docker-compose.https.yml exec proxy nginx -t
docker compose -f docker-compose.yml -f docker-compose.https.yml exec proxy nginx -s reload
```

- `nginx -t`가 실패하면 **reload하지 않는다** — 기존 프로세스는 이전 인증서로 계속 서비스한다.
- 갱신을 잊지 않는 가장 확실한 방법은 **이 인증서를 AutoCert 자신에 등록**하는 것이다. 웹 콘솔
  [인증서] → 등록으로 `server.crt`를 업로드하거나(개인키는 올리지 않는다), Discovery 스캔 대상에
  AutoCert 서버의 443 포트를 포함시키면 만료 알림 대상이 된다 (OPERATIONS.md 2.7절).

## 8. 트러블슈팅

| 증상 | 원인 | 대응 |
| --- | --- | --- |
| 로그인 버튼을 눌러도 로그인 화면으로 되돌아온다 | 평문 `http://<서버>:3000`으로 접속했는데 `SESSION_COOKIE_SECURE=true`다. 브라우저가 Secure 쿠키를 **조용히 버린다**(API는 200을 반환하므로 로그가 정상으로 보인다) | `https://`로 접속한다. HTTPS를 쓰지 않을 거면 `SESSION_COOKIE_SECURE=false`(또는 오버레이 미사용)로 되돌리고 backend 재기동 |
| 브라우저가 "안전하지 않음" 경고 | CA가 신뢰 저장소에 없다 / SAN에 접속 주소가 없다 | 6.1절 CA 배포. SAN이 문제면 인증서를 재발급(3장) |
| `502 Bad Gateway` | frontend 컨테이너가 죽었거나 아직 기동 중 | `docker compose ps`·`logs frontend` 확인. 업스트림은 요청 시점에 DNS 해석하므로 frontend가 살아나면 자동 복구된다 |
| nginx가 뜨지 않고 `cannot load certificate` | `deploy/tls/server.crt`·`server.key`가 없거나 형식이 잘못됨 | 파일 존재·PEM 형식 확인. 키에 암호가 걸려 있으면 제거한다 |
| 인증서 업로드가 `413`으로 거부됨 | 프록시의 본문 크기 상한 | `autocert.conf`의 `client_max_body_size`(기본 4m)를 확인한다. 제품 상한은 1 MiB다 |
| 센서가 연결되지 않는다 | 센서 게이트웨이(8443)를 프록시 뒤로 넣었다 | 8443은 mTLS라 종단하면 안 된다. 오버레이 기본값처럼 직접 노출한다 |

프록시 로그: `docker compose -f docker-compose.yml -f docker-compose.https.yml logs -f proxy`

## 9. 남아 있는 평문 구간

TLS 종단은 **브라우저 ↔ 서버** 구간만 덮는다. 아래 구간은 이번 구성의 범위 밖이며 현황과
대응 방향을 함께 적는다.

- **backend ↔ PostgreSQL (`sslmode=disable`)** — 현재 DB 접속 DSN은 `sslmode=disable`이다.
  db는 compose 내부 네트워크의 같은 호스트에서만 접근하고 외부 포트 노출도 권장하지 않으므로
  (INSTALL.md 5.4) 실질 노출면은 호스트 내부로 한정된다. 다만 **DB를 별도 호스트나 관리형
  인스턴스로 분리하는 순간 평문 구간이 네트워크를 타게 된다.** 대응 방향은 ① 서버 인증서를 갖춘
  PostgreSQL을 두고 DSN을 `sslmode=verify-full` + `sslrootcert`로 바꾸는 것 ②
  `docker-compose.yml`의 `DATABASE_URL`을 환경변수로 완전히 외부화해 고객이 DSN을 통제하게 하는
  것이다. 두 가지 모두 별도 작업 항목이며 **이 구성으로는 바뀌지 않는다.**
- **proxy ↔ frontend ↔ backend (컨테이너 내부 HTTP)** — 같은 Docker 네트워크 안이며 호스트
  외부로 나가지 않는다. 세 컨테이너를 서로 다른 호스트로 분리하는 배포에서는 각 구간에 TLS가
  필요하다(현재 지원 구성이 아니다).
- **센서 ↔ backend (8443)** — 이미 mTLS로 암호화·상호 인증된다 (SENSOR.md).
- **backend → 외부 수신 시스템(SMTP·웹훅·Syslog)** — 각 채널 설정으로 TLS를 켠다
  (INSTALL.md 5.2.1: `SMTP_TLS_MODE`·`SMTP_CA_FILE`·`WEBHOOK_CA_FILE`).
