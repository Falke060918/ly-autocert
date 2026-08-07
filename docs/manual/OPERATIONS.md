# INHERIT AutoCert 운영 매뉴얼

> **문서 상태**: 초안. 제품은 Phase 1(인증서 인벤토리) 개발 중이며, 아직 구현되지 않은 기능은 **(예정)** 으로 표기한다. 기능이 완료될 때마다 이 문서를 갱신한다.

모든 명령은 저장소 루트(`docker-compose.yml`이 있는 위치)에서 실행한다.

## 1. 일상 운영

### 1.1 상태 확인

```sh
docker compose ps                        # 컨테이너 상태 (db는 healthcheck 결과 표시)
curl http://localhost:8080/healthz       # API 생존 확인 — HTTP 200 기대
```

### 1.2 기동 / 중지 / 재시작

```sh
docker compose up -d          # 기동
docker compose stop           # 중지 (데이터 유지)
docker compose restart backend   # 특정 서비스만 재시작
```

## 2. 백업과 복원

### 2.1 백업 (pg_dump)

```sh
docker compose exec db pg_dump -U autocert -d autocert -F c -f /tmp/autocert.dump
docker compose cp db:/tmp/autocert.dump ./backup/autocert-$(date +%Y%m%d).dump
```

- `-F c`(custom 포맷)는 압축되며 `pg_restore`로 선택 복원이 가능하다.
- 백업 파일은 AutoCert 서버 외부(별도 매체·백업 서버)에 보관한다. 인증서 메타데이터와 감사 기록이 담기므로 백업 파일 접근도 통제 대상이다.
- 권장 주기: 일 1회 + 업그레이드 직전 필수.

### 2.2 복원

```sh
docker compose cp ./backup/autocert-YYYYMMDD.dump db:/tmp/restore.dump
docker compose exec db pg_restore -U autocert -d autocert --clean --if-exists /tmp/restore.dump
```

복원은 기존 데이터를 덮어쓴다. 복원 전 backend를 중지(`docker compose stop backend`)하고, 복원 후 재기동한다.

## 3. 로그 확인

```sh
docker compose logs -f backend           # API 서버 로그 실시간
docker compose logs -f frontend
docker compose logs -f db
docker compose logs --since 1h backend   # 최근 1시간
```

- **구조화 JSON 로그 / Request ID**: backend 로그는 구조화 JSON이다. 모든 API 응답에 `X-Request-ID` 헤더가 붙고, 오류 로그·감사 이벤트에 같은 `request_id`가 기록되므로 이 값으로 요청 단위 추적이 가능하다. OpenTelemetry Trace 연동은 **(예정)**.
- 보안 원칙: 인증서 본문·개인키는 로그에 출력되지 않는다. 로그에서 발견되면 제품 결함이므로 즉시 보고할 것.

### 3.1 감사 로그

다음 이벤트는 애플리케이션 로그와 분리되어 DB의 `audit_events` 테이블에 기록된다.

- 로그인 성공·실패 (`user.login`), 로그아웃 (`user.logout`)
- 사용자 생성 (`user.created`)
- 권한 거부 — 허용되지 않은 역할의 API 호출 (`authz.denied`, HTTP 403과 함께)
- 인증서 업로드 — 성공은 물론 거부(개인키 포함·파싱 실패 등)도 사유 코드와 함께 기록

`audit_events`는 **추가 전용(append-only)** 이다. UPDATE/DELETE/TRUNCATE는 DB 트리거가 차단하며, 각 이벤트에는 직전 이벤트와 연결된 SHA-256 해시 체인 무결성 검증값(`prev_hash`/`integrity_hash`)이 기록된다.

**조회 API** — 역할이 `security_administrator` 또는 `auditor`인 계정으로:

```sh
# 사용자·이벤트 유형·기간 필터 (RFC3339)
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8080/api/v1/audit/events?event_type=user.login&from=2026-08-01T00:00:00Z"
```

**무결성 검증 API** — 체인 전체를 재계산해 변조 지점을 `id` 단위로 특정한다:

```sh
curl -H "Authorization: Bearer <token>" http://localhost:8080/api/v1/audit/verify
# valid=false면 invalid[]의 hash_mismatch(행 내용 변조)·chain_broken(중간 삭제) 확인
```

- 응답의 `head_hash`(체인 꼬리 해시)는 **주기적으로 외부 매체에 기록**할 것을 권장한다. 가장 최근 이벤트들을 통째로 지우는 꼬리 삭제는 체인만으로는 탐지되지 않으며, 외부에 기록해 둔 head_hash와의 대조로만 발견된다.
- 웹 콘솔 감사 로그 화면은 **(예정)** 이다. DB 직접 조회가 필요하면 읽기만 한다:

```sh
docker compose exec db psql -U autocert -d autocert -c \
  "SELECT created_at, event_type, actor_id, target_type, target_id, result, source_ip
     FROM audit_events ORDER BY created_at DESC LIMIT 20;"
```

## 4. 업그레이드 절차

1. **백업 먼저** — 2.1절 절차로 DB를 백업한다.
2. 새 버전 소스/이미지를 반입한다.
3. 재빌드·재기동:
   ```sh
   docker compose up --build -d
   ```
4. backend가 기동하면서 **새 SQL 마이그레이션을 자동 적용**한다. 별도 마이그레이션 명령이 없다.
5. 확인: `curl http://localhost:8080/healthz` 200 + 웹 콘솔 접속 + `docker compose logs backend`에서 마이그레이션 오류 없음.

**롤백 주의**: 이전 버전 이미지로 되돌려도 이미 적용된 스키마 변경은 되돌아가지 않는다. 스키마가 바뀐 업그레이드를 되돌려야 하면 **1번에서 받은 백업으로 복원**하는 것이 표준 절차다. 마이그레이션을 수동으로 역적용하지 않는다.

## 5. 트러블슈팅

| 증상 | 원인 후보 | 조치 |
| --- | --- | --- |
| `docker compose up`이 `set POSTGRES_PASSWORD in .env` 오류로 중단 | `.env` 미생성 또는 비밀번호 미설정 | `cp .env.example .env` 후 `POSTGRES_PASSWORD` 설정 |
| backend가 재시작을 반복 | DB 미기동 상태에서 연결 실패 | `docker compose ps`로 db가 `healthy`인지 확인, `docker compose logs db` 점검 |
| 웹 콘솔은 뜨는데 데이터 로드 실패 | 브라우저→8080 방화벽 차단, 또는 `NEXT_PUBLIC_API_URL`이 접속 환경과 불일치 | 관리자 PC에서 `http://<서버 IP>:8080/healthz` 접근 확인. 주소가 다르면 compose의 빌드 인자 수정 후 **frontend 재빌드** (INSTALL.md 4.4절) |
| 포트 충돌 (`address already in use`) | 3000/8080/5432를 다른 프로세스가 점유 | 점유 프로세스 정리 또는 compose `ports` 좌측(호스트 포트) 변경 |
| 업그레이드 후 backend 기동 실패, 로그에 마이그레이션 오류 | 스키마 적용 실패 | 로그의 실패 마이그레이션 확인 후 지원 채널에 보고. 임의로 테이블을 수정하지 말 것 |
| 로그인할 계정이 없음 | 초기 관리자 미생성 — `BOOTSTRAP_ADMIN_PASSWORD` 미설정 상태로 최초 기동 | `docker compose logs backend`에서 `BOOTSTRAP_ADMIN_PASSWORD is not set` 경고 확인 → `.env`에 설정 후 backend 재기동. 초기 생성은 사용자가 0명일 때만 동작한다 (INSTALL.md 5.1절) |
| API가 401 반환 | 미로그인 또는 세션 만료 (`SESSION_TTL` 기본 8시간) | 재로그인 후 `Authorization: Bearer <token>` 헤더(또는 세션 쿠키) 확인 |
| API가 403 반환 | 계정 역할에 허용되지 않은 작업 (RBAC 정상 동작) | INSTALL.md 5.2절 역할 표 확인. 반복되면 `audit_events`의 `authz.denied` 이벤트로 누가 무엇을 시도했는지 추적 (3.1절) |
| 인증서 업로드가 400 `private_key_rejected` | 업로드 파일에 개인키 포함 (의도된 거부) | 개인키를 제외하고 인증서(PEM·DER)만 업로드 |
| 웹 콘솔 글꼴이 시스템 폰트로 보임 | 정적 자원 로드 실패 (비정상) | 브라우저 개발자도구 네트워크 탭에서 실패 요청 확인 — 외부 요청이 있어서는 안 된다 |
| 디스크 사용량 증가 | DB 볼륨(`pgdata`)·Docker 이미지 누적 | 오래된 이미지 정리 `docker image prune`. **`docker volume` 정리 명령에 `pgdata`가 포함되지 않도록 주의** |

## 6. 모니터링 (예정)

Prometheus 메트릭 엔드포인트와 OpenTelemetry 연동은 추후 제공한다. 구현 전까지는 1.1절의 healthz 폴링을 외부 감시(기존 NMS 등)에 등록하는 것을 권장한다.

## 7. 운영 기능 로드맵 표기

아래 기능은 스펙에 정의되어 있으나 아직 미구현이다. 완료 시 본 매뉴얼에 운영 절차가 추가된다.

- **(예정)** Discovery 네트워크 TLS 스캔 — 대상 CIDR·포트·주기·동시 연결 수 등록, 비동기 Job 조회
- **(예정)** 인증서 목록·상세 조회 API와 만료 대시보드·웹 콘솔 로그인 화면
- **(예정)** 감사 로그 웹 콘솔 조회·무결성 검증 — 기록 자체는 구현되어 동작 중이다 (3.1절)
- **(예정)** 만료 임박 알림 채널
- **(예정, Phase 2 이후)** CA 연동·발급·갱신·배포 자동화·에이전트

구현 완료되어 이 표에서 빠진 항목: 인증서 수동 업로드 (INSTALL.md 5.3절), 로그인·RBAC 계정/역할 관리 (INSTALL.md 5.1–5.2절).
