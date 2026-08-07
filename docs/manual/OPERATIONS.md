# INHERIT AutoCert 운영 매뉴얼

> **문서 상태**: Phase 1(인증서 인벤토리) 구현 완료 기준. 아직 구현되지 않은 기능은 **(예정)** 으로 표기한다. 기능이 완료될 때마다 이 문서를 갱신한다.

모든 셸 명령은 저장소 루트(`docker-compose.yml`이 있는 위치)에서 실행한다. 일상 운영은 웹 콘솔(2장)로 하고, 셸은 상태 확인·백업·업그레이드에만 필요하다.

## 1. 일상 운영 (셸)

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

## 2. 웹 콘솔 운영

`http://<서버 IP>:3000` 접속 → 로그인 (계정·역할은 INSTALL.md 6.1–6.2절). 화면은 좌측 사이드바로 이동한다: **대시보드 / 인증서 / Discovery**. 로그아웃은 우상단 버튼. 세션이 만료되면 로그인 화면으로 돌아간다.

### 2.1 대시보드 — 만료 현황 점검

로그인 직후 화면. 일일 점검은 여기서 시작한다.

- **KPI 카드 8종**: 전체 인증서 / 7·30·60·90일 이내 만료(누적값 — "30일 이내"는 7일 이내를 포함) / 이미 만료 / 자체 서명 / 소유자 미지정. **카드를 클릭하면 해당 조건으로 필터된 인증서 목록으로 이동**한다.
- **만료 구간 바**: 7일 이내 / 8–30일 / 31–60일 / 61–90일 구간별 분포.
- **만료 임박 인증서 표**와 **최근 등록된 인증서 표**: 행 클릭으로 상세 화면 이동.

일일 점검 권장 순서: "이미 만료" → "7일 이내 만료" 카드 확인 → 0이 아니면 클릭해 대상 목록에서 담당자 확인·갱신 조치.

### 2.2 인증서 목록 — 검색·필터·업로드

[인증서] 화면. 전 역할 조회 가능.

- **검색**: CN / SAN / Serial / Fingerprint 통합 검색 (입력 후 자동 적용, Enter로 즉시 적용).
- **필터**: 상태 / 유형 / 만료(기간·이미 만료·자체 서명·소유자 미지정). "초기화"로 전체 해제.
- **정렬·페이지네이션**: 표 헤더 클릭 정렬, 페이지 크기 선택.
- 검색·필터·정렬 상태는 **URL 쿼리에 반영**되므로, 현재 화면 주소를 복사해 다른 운영자와 공유하면 같은 조건이 재현된다.
- **CSV 내보내기**: 우상단 버튼. 현재 검색·필터·정렬 조건에 맞는 인증서 **전체**를 CSV 파일로 내려받는다 (페이지와 무관, 1회 최대 50,000행). 전 역할 사용 가능하며 결과 0건이면 비활성화된다. 파일에는 인벤토리 메타데이터(CN·DN·일련번호·지문·SAN·유효기간·알고리즘·유형·상태 등)만 담긴다 — 인증서 원문·개인키는 저장하지 않으므로 포함될 수 없다. UTF-8(BOM)이라 Excel에서 바로 열린다. 모든 내보내기는 `certificate.export` 감사 이벤트로 기록된다(3.1절).
- **인증서 등록** 패널: 파일(.pem/.crt/.cer/.der) 선택 → 등록. 신규/중복(위치만 추가) 건수가 표시된다. 개인키 포함 파일은 전체 거부된다. 등록 버튼은 System Administrator·PKI Administrator·Operator에게만 보인다. 지원 형식 상세는 INSTALL.md 3.1절.

### 2.3 인증서 상세

목록에서 행 클릭. 기본 정보(지문·일련번호·DN·SAN·유효기간·키/서명 알고리즘·유형·상태)와 **설치 위치** 표(위치별 협상 **TLS 버전** 포함)를 보여준다. 설치 위치가 없는 인증서는 Discovery 스캔으로 위치를 찾을 수 있다.

- **인증서 체인** 카드: 자신→상위→루트 순으로 발급 관계를 보여준다. 인벤토리에 저장된 상위 인증서는 클릭하면 해당 상세로 이동하며, 단계별 만료일·D-day가 표시된다. 자체 서명 CA에는 "루트 CA" 뱃지가 붙는다.
- 상위 인증서가 인벤토리에 없으면 **미보유**로 표시되고 체인이 거기서 끝난다. 해당 CA 인증서를 업로드(2.2절)하거나 스캔으로 수집하면 체인이 이어진다. 상위 매칭은 키 식별자(AKI=SKI) 정확 일치를 우선하고, 키 식별자가 없는 인증서는 발급자 DN으로 대응한다.

### 2.4 Discovery 스캔

[Discovery] 화면. 스캔 시작은 System Administrator·PKI Administrator·Operator만 가능하다 (다른 역할은 조회만).

1. **스캔 시작 폼** 입력:
   - IP / CIDR 대역 — 한 줄에 하나씩 (예: `10.0.0.5`, `10.0.0.0/24`)
   - 포트 목록 — 쉼표 구분, 비우면 443
   - 타임아웃(ms)·동시 연결 수 — 비우면 기본값(5000 ms / 20). 허용 범위·상한은 INSTALL.md 3.2절
2. [스캔 시작] → 우측 **스캔 작업 표**에 Job이 추가되고 진행률이 2초 간격으로 갱신된다.
3. 완료 후 표의 **발견**(수집 인증서 수)·**신규**(인벤토리에 새로 등록된 수) 확인. 신규 숫자를 클릭하면 최근 등록순 인증서 목록으로 이동한다.

주의사항:

- 스캔 작업 표는 **이 브라우저에서 시작한 스캔만** 보여준다 (Job 목록 API가 없어 브라우저 로컬 저장소로 추적 — Phase 1 범위). 다른 PC·브라우저에서 시작한 Job은 보이지 않는다.
- 스캔 진행 중 backend가 재시작되면 그 Job은 진행률이 멈춘 채 남는다 (재개되지 않음). 새 스캔을 다시 실행하면 된다 — 결과 반영은 중복 안전하다.
- 도달 불가·비-TLS 포트·TLS 1.0/1.1 전용 서버는 조용히 건너뛴다. "발견 0"이 반복되면 5장 트러블슈팅 참조.

### 2.5 웹 콘솔 미제공 기능

감사 로그 조회 화면·사용자 관리 화면은 **(예정)** 이다. 감사 로그는 API로 조회한다(3.1절), 사용자 관리는 API로 한다(INSTALL.md 6.2절). 사이드바의 CA 관리·정책·작업·감사 로그 메뉴는 Phase 2+ 자리 표시이며 비활성 상태다.

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
- 인증서 CSV 내보내기 (`certificate.export`) — 조회성 API지만 데이터 반출 경로이므로 예외 없이 기록. 감사 이벤트에는 조회 조건(검색어·필터)과 건수만 남고 인증서 데이터 자체는 담기지 않는다. 감사 기록에 실패하면 내보내기 자체가 실패한다
- Discovery 스캔 생성

조회 화면(대시보드·목록·상세 등 GET API)은 읽기 전용이므로 감사 이벤트를 만들지 않는다 — CSV 내보내기만 예외다(위 항목).

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

## 4. 백업과 복원

### 4.1 백업 (pg_dump)

```sh
docker compose exec db pg_dump -U autocert -d autocert -F c -f /tmp/autocert.dump
docker compose cp db:/tmp/autocert.dump ./backup/autocert-$(date +%Y%m%d).dump
```

- `-F c`(custom 포맷)는 압축되며 `pg_restore`로 선택 복원이 가능하다.
- 백업 파일은 AutoCert 서버 외부(별도 매체·백업 서버)에 보관한다. 인증서 메타데이터와 감사 기록이 담기므로 백업 파일 접근도 통제 대상이다.
- 권장 주기: 일 1회 + 업그레이드 직전 필수.

### 4.2 복원

```sh
docker compose cp ./backup/autocert-YYYYMMDD.dump db:/tmp/restore.dump
docker compose exec db pg_restore -U autocert -d autocert --clean --if-exists /tmp/restore.dump
```

복원은 기존 데이터를 덮어쓴다. 복원 전 backend를 중지(`docker compose stop backend`)하고, 복원 후 재기동한다.

## 5. 업그레이드 절차

1. **백업 먼저** — 4.1절 절차로 DB를 백업한다.
2. 새 버전 소스/이미지를 반입한다.
3. 재빌드·재기동:
   ```sh
   docker compose up --build -d
   ```
4. backend가 기동하면서 **새 SQL 마이그레이션을 자동 적용**한다. 별도 마이그레이션 명령이 없다.
5. 확인: `curl http://localhost:8080/healthz` 200 + 웹 콘솔 로그인·대시보드 표시 + `docker compose logs backend`에서 마이그레이션 오류 없음.

**롤백 주의**: 이전 버전 이미지로 되돌려도 이미 적용된 스키마 변경은 되돌아가지 않는다. 스키마가 바뀐 업그레이드를 되돌려야 하면 **1번에서 받은 백업으로 복원**하는 것이 표준 절차다. 마이그레이션을 수동으로 역적용하지 않는다.

## 6. 트러블슈팅

| 증상 | 원인 후보 | 조치 |
| --- | --- | --- |
| `docker compose up`이 `set POSTGRES_PASSWORD in .env` 오류로 중단 | `.env` 미생성 또는 비밀번호 미설정 | `cp .env.example .env` 후 `POSTGRES_PASSWORD` 설정 |
| backend가 재시작을 반복 | DB 미기동 상태에서 연결 실패 | `docker compose ps`로 db가 `healthy`인지 확인, `docker compose logs db` 점검 |
| 웹 콘솔은 뜨는데 데이터 로드 실패 | frontend → backend 컨테이너 내부 통신 실패 | `docker compose ps`로 backend 기동 확인, `docker compose logs frontend backend` 점검. 브라우저는 3000번만 쓰므로 관리자 PC 방화벽은 무관하다 (INSTALL.md 3.3절). backend를 별도 호스트로 분리했다면 `BACKEND_INTERNAL_URL` 빌드 인자 확인 후 frontend 재빌드 |
| 포트 충돌 (`address already in use`) | 3000/8080/5432를 다른 프로세스가 점유 | 점유 프로세스 정리 또는 compose `ports` 좌측(호스트 포트) 변경 |
| 업그레이드 후 backend 기동 실패, 로그에 마이그레이션 오류 | 스키마 적용 실패 | 로그의 실패 마이그레이션 확인 후 지원 채널에 보고. 임의로 테이블을 수정하지 말 것 |
| 로그인할 계정이 없음 | 초기 관리자 미생성 — `BOOTSTRAP_ADMIN_PASSWORD` 미설정 상태로 최초 기동 | `docker compose logs backend`에서 `BOOTSTRAP_ADMIN_PASSWORD is not set` 경고 확인 → `.env`에 설정 후 backend 재기동. 초기 생성은 사용자가 0명일 때만 동작한다 (INSTALL.md 6.1절) |
| 로그인 화면으로 자꾸 돌아감 / API 401 | 미로그인 또는 세션 만료 (`SESSION_TTL` 기본 8시간) | 재로그인. API 직접 호출이면 `Authorization: Bearer <token>` 헤더(또는 세션 쿠키) 확인 |
| API가 403 반환 / 웹 콘솔에 등록·스캔 버튼이 없음 | 계정 역할에 허용되지 않은 작업 (RBAC 정상 동작) | INSTALL.md 6.2절 역할 표 확인. 반복되면 `audit_events`의 `authz.denied` 이벤트로 누가 무엇을 시도했는지 추적 (3.1절) |
| 인증서 업로드가 400 `private_key_rejected` | 업로드 파일에 개인키 포함 (의도된 거부) | 개인키를 제외하고 인증서(PEM·DER)만 업로드 (INSTALL.md 3.1절) |
| 스캔 생성이 400 거부 | CIDR가 너무 큼(호스트 비트 20 초과) 또는 IP×포트 조합 65,536 초과, 형식 오류 | 대역을 나눠 여러 스캔으로 실행 (INSTALL.md 3.2절 상한 표) |
| 스캔 완료됐는데 발견 0 | 대상이 비-TLS 포트, TLS 1.0/1.1 전용 서버, 방화벽 차단, 타임아웃 과소 | 대상 포트가 TLS인지 확인, 서버→대상 아웃바운드 방화벽 확인, 타임아웃을 늘려 재시도. TLS 1.0/1.1 전용 장비는 수집 불가 (INSTALL.md 3.2절) |
| 스캔 작업 표가 비어 있음 / 이전 스캔이 안 보임 | 스캔 표는 그 브라우저에서 시작한 Job만 추적한다 (2.4절) | 결과 자체는 인벤토리에 반영되어 있다 — 인증서 목록을 최근 등록순으로 확인 |
| 스캔 진행률이 멈춘 채 완료되지 않음 | 스캔 도중 backend 재시작 (Job은 재개되지 않음) | 같은 대상으로 새 스캔 실행 (중복 안전 — 기존 인증서는 위치만 갱신) |
| 웹 콘솔 글꼴이 시스템 폰트로 보임 | 정적 자원 로드 실패 (비정상) | 브라우저 개발자도구 네트워크 탭에서 실패 요청 확인 — 외부 요청이 있어서는 안 된다 |
| 디스크 사용량 증가 | DB 볼륨(`pgdata`)·Docker 이미지 누적 | 오래된 이미지 정리 `docker image prune`. **`docker volume` 정리 명령에 `pgdata`가 포함되지 않도록 주의** |

## 7. 모니터링 (예정)

Prometheus 메트릭 엔드포인트와 OpenTelemetry 연동은 추후 제공한다. 구현 전까지는 1.1절의 healthz 폴링을 외부 감시(기존 NMS 등)에 등록하는 것을 권장한다.

## 8. 운영 기능 로드맵 표기

아래 기능은 스펙에 정의되어 있으나 아직 미구현이다. 완료 시 본 매뉴얼에 운영 절차가 추가된다.

- **(예정)** 감사 로그 웹 콘솔 조회·무결성 검증 화면 — 기록·API는 구현되어 동작 중이다 (3.1절)
- **(예정)** 웹 콘솔 사용자 관리 화면 — API는 제공 중 (INSTALL.md 6.2절)
- **(예정)** Discovery 스캔 Job 전체 목록 API — 현재는 시작한 브라우저에서만 Job 추적 (2.4절)
- **(예정)** 만료 임박 알림 채널
- **(예정, Phase 2 이후)** CA 연동·발급·갱신·배포 자동화·에이전트

구현 완료되어 이 표에서 빠진 항목: 인증서 수동 업로드(2.2절), 로그인·RBAC(INSTALL.md 6.1–6.2절), Discovery 네트워크 TLS 스캔(2.4절), 만료 대시보드·인증서 목록·상세 화면(2.1–2.3절).
