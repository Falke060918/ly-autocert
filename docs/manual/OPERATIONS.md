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

- **구조화 JSON 로그 / Trace·Request ID** (예정): 로그 포맷이 구조화되면 이 절에 필드 설명과 조회 예시를 추가한다.
- **감사 로그** (예정): 생성·수정·삭제·내보내기 등 모든 변경 작업의 감사 이벤트(AuditEvent)는 애플리케이션 로그와 분리되어 DB에 저장되며, 웹 콘솔 감사 로그 화면에서 조회한다. 구현 완료 후 갱신.
- 보안 원칙: 인증서 본문·개인키는 로그에 출력되지 않는다. 로그에서 발견되면 제품 결함이므로 즉시 보고할 것.

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
| 웹 콘솔 글꼴이 시스템 폰트로 보임 | 정적 자원 로드 실패 (비정상) | 브라우저 개발자도구 네트워크 탭에서 실패 요청 확인 — 외부 요청이 있어서는 안 된다 |
| 디스크 사용량 증가 | DB 볼륨(`pgdata`)·Docker 이미지 누적 | 오래된 이미지 정리 `docker image prune`. **`docker volume` 정리 명령에 `pgdata`가 포함되지 않도록 주의** |

## 6. 모니터링 (예정)

Prometheus 메트릭 엔드포인트와 OpenTelemetry 연동은 추후 제공한다. 구현 전까지는 1.1절의 healthz 폴링을 외부 감시(기존 NMS 등)에 등록하는 것을 권장한다.

## 7. 운영 기능 로드맵 표기

아래 기능은 스펙에 정의되어 있으나 아직 미구현이다. 완료 시 본 매뉴얼에 운영 절차가 추가된다.

- **(예정)** Discovery 네트워크 TLS 스캔 — 대상 CIDR·포트·주기·동시 연결 수 등록, 비동기 Job 조회
- **(예정)** 인증서 수동 업로드 (PEM·DER 우선)
- **(예정)** 로그인·RBAC 계정/역할 관리 (6개 역할)
- **(예정)** 감사 로그 조회·무결성 검증
- **(예정)** 만료 임박 알림 채널
- **(예정, Phase 2 이후)** CA 연동·발급·갱신·배포 자동화·에이전트
