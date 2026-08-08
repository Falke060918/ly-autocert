# MAP — 코드베이스 구조·명령 캐시
확인일: 2026-08-08

## 저장소 구조
- `backend/` — Go 백엔드
  - `cmd/server/main.go` — 엔트리포인트. config 로드 → DB 연결 → 마이그레이션 → HTTP 서버 기동
  - `cmd/sensor/main.go` — 센서(원격 수집기) 바이너리 엔트리포인트. `enroll`/`run` 서브커맨드, slog JSON 로그. 정적 크로스 빌드: `CGO_ENABLED=0 GOOS=linux|windows GOARCH=amd64 go build ./cmd/sensor`
  - `internal/config/` — 환경변수 기반 설정 (`SESSION_TTL` 기본 8h, `BOOTSTRAP_ADMIN_USERNAME`/`_PASSWORD`)
  - `internal/db/` — PostgreSQL 연결 + 마이그레이션 적용. DB 테스트는 `TEST_DATABASE_URL` 설정 시에만 실행(미설정 시 스킵)
  - `internal/domain/` — 핵심 데이터 모델 Go 타입 (00-spec 12장 기준: Certificate/CertificateLocation/Target/AuditEvent + User/Role 6종 + 상태 enum. `sensor.go` — Sensor/SensorStatus·JobType('scan' 폐쇄)·ExecutorType·ExecutorScopeServer)
  - `internal/certparse/` — 인증서 파서 (PEM 체인·DER, crypto/x509만). 개인키 검출 시 `ErrPrivateKeyFound`로 전체 거부. 새 포맷(P7B/PFX)은 `decoders` 목록에 추가. SKI/AKI를 소문자 hex로 추출(체인 매칭 키)
  - `internal/store/` — PostgreSQL 영속화. fingerprint 중복 판정 upsert + 감사 이벤트 + 목록·상세 조회(검색·필터·정렬·페이지네이션 — 정렬 컬럼은 화이트리스트, 만료·self_signed·unowned 필터는 파생 계산) + `certificates_summary.go`(대시보드 집계 단일 쿼리 — expiring_* 누적값) + `certificates_chain.go`(상세 응답 chain 배열 — 자신→루트, AKI=SKI 우선·DN 폴백 매칭, 미보유 상위는 stored=false, upsert가 중복 재업로드 시 키 식별자 백필) + `certificates_export.go`(목록 조건 기반 전체 조회 + certificate.export 감사를 같은 트랜잭션으로 — 감사 실패 시 내보내기도 실패, 상한 50000행). text[]는 SQL 쪽 `string_to_array(chr(31))`, int[]는 `string_to_array(',')::int[]`, inet은 `::inet`/`host()` 캐스트로 변환 (database/sql 제약). `discovery.go` — scan_jobs 상태 머신 + 발견 인증서 인벤토리 반영(targets 아비터는 0010의 (executor_scope, ip) 부분 유니크 인덱스 — 서버 경로 스코프 'server', ON CONFLICT upsert로 동시 실행 안전). `sensorjobs.go` — 센서 pull 리스 상태 머신(FOR UPDATE SKIP LOCKED 리스 90s TTL·전이 가드 leased→running→completed/failed·하트비트 연장·만료 reaper) + Observation 멱등 배치(scan_job_endpoints PK 아비터 ON CONFLICT DO NOTHING, 스코프 'sensor:<uuid>'). `sensors.go` — Phase 3a 센서 영속화(0009): 생성=1회용 토큰 발급(평문은 반환만, DB엔 SHA-256 hex·24h) / enroll은 단일 UPDATE 원자 소진(pending+해시+미만료, 실패도 감사) / fingerprint로 active 조회(mTLS 대조)·원자 교체(renew)·disable(pending 토큰 회수 겸용) / sensor_ca 단일 행 Save(중복은 ErrSensorCAExists)·Load — 감사 sensor.token.issue/enroll/renew/disable 전건 동일-tx
  - `internal/discovery/` — 에이전트리스 네트워크 TLS 스캔. `scanner.go`(crypto/tls 핸드셰이크로 서버 인증서 체인 수집, IP/CIDR·포트 Expand, InsecureSkipVerify는 **수집 전용**, 호스트명은 역방향 DNS 1s → 리프 CN 폴백. **store/db 비의존 유지** — Phase 3a 센서(원격 수집기) 바이너리 재사용 전제) + `manager.go`(비동기 Job: 워커풀·상태 머신, 백그라운드는 `context.Background()`. SensorID 지정 시 executor_type='sensor' pending 생성만 — 서버 goroutine 미기동, 센서가 pull 리스). 도달불가·핸드셰이크 실패는 조용히 실패 기록 후 계속
  - `internal/auth/` — 비밀번호 해시(bcrypt cost 12)·정책(12자↑)·세션 토큰(DB엔 SHA-256 해시만)·로그인/사용자 서비스. 초기 관리자는 users 비어 있을 때 `BOOTSTRAP_ADMIN_PASSWORD`로 1회 생성
  - `internal/audit/` — AuditEvent 기록(`Insert`는 pool/tx 겸용)·조회(`List`)·무결성 검증(`Verify`). 변경+감사는 단일 트랜잭션(감사 실패 시 변경도 롤백 — auth 서비스·store 모두). 해시 체인(prev_hash+SHA-256)은 DB BEFORE INSERT 트리거가 계산 — 검증도 같은 SQL 함수(audit_event_hash)로 재계산해 표현 불일치 오탐 없음. 꼬리 삭제는 체인만으로 탐지 불가 → verify 응답 head_hash 외부 기록으로 보완
  - `internal/sensorwire/` — 서버-센서 mTLS API wire DTO + Validate (00-spec 7.8): Enroll/Renew(CSR PEM)·Lease·JobStart·Heartbeat·ObservationBatch(base64 DER 체인, 배치 상한 1000)·Complete. **내부 패키지 의존 0**(stdlib만) — 센서 바이너리와 공유 전제
  - `internal/sensor/` — 센서 바이너리 클라이언트 측 구현(서버 게이트웨이 `/sensor/v1/*`와 sensorwire로 통신, 스캔은 discovery.Expand/ScanEndpoint 재사용 — store/db/서버 패키지 비의존): `enroll.go`(로컬 ECDSA P-256 키 생성·CSR 제출·`--ca-fingerprint` SHA-256 핀 검증), `state.go`(상태 디렉토리 0700/0600, 기본 /var/lib/autocert-sensor·%ProgramData%\AutocertSensor, 원자 교체 저장), `identity.go`(mTLS 신원, renew 무중단 스왑), `runner.go`(pull 리스 폴링 루프: lease 204 백오프→start→세마포어 스캔→관측 배치 ≤100건 멱등 재전송→complete, 30s 하트비트, 기동 시+일 1회 잔여 30일 미만 renew). 개인키·인증서·토큰 로그 출력 금지. 테스트는 httptest 모의 게이트웨이 — DB 불필요
  - `internal/sensorca/` — 센서 mTLS 내부 CA 순수 crypto(저장 비의존): ECDSA P-256 CA(10년, MaxPathLenZero)·클라이언트 CSR 서명(90일)·서버 인증서 발급(SAN 인자)·AES-256-GCM Seal/Open(키 32B 고정). CA 개인키는 Seal 암호문으로만 저장(sensor_ca) — 평문 저장·로그 출력 금지
  - `internal/api/` — 라우팅·핸들러·미들웨어. 라우트 등록은 `public()`/`protected(roles...)` 둘뿐 — 보호 라우트는 역할 명시 강제(빈 역할은 panic), 등록 패턴은 `Server.routes`에 기록되어 OpenAPI 정합 테스트(`openapi_conformance_test.go`)가 명세와 양방향 대조(누락/초과 검출, DB 불필요). 모든 응답에 X-Request-ID + 보안 헤더(nosniff·X-Frame-Options DENY·no-referrer·CSP default-src 'none', `/api/*`는 Cache-Control no-store — `withSecurityHeaders`)
    - 라우트: `/healthz`·`POST /api/v1/auth/login`(public) / logout·me(전 역할) / `GET|POST /api/v1/users`(system_administrator만) / `POST /api/v1/certificates/import`·`POST /api/v1/discovery/scans`(system_administrator·pki_administrator·operator) / `GET /api/v1/certificates`·`GET /api/v1/certificates/summary`·`GET /api/v1/certificates/{id}`·`GET /api/v1/discovery/scans/{id}`(전 역할, 읽기 전용 — 감사 이벤트 없음. summary는 리터럴 세그먼트라 {id}보다 우선 매칭) / `GET /api/v1/certificates/export`(전 역할, 목록과 같은 필터로 CSV — 읽기지만 데이터 유출 경로라 certificate.export 감사 전건 기록, 7.12) / `GET /api/v1/audit/events`·`GET /api/v1/audit/verify`(security_administrator·auditor만 — 7.11) / `GET /api/v1/openapi.yaml`(전 역할 — 임베드 명세 원문, 폐쇄망이라 인증 뒤)
    - users API는 생성(POST)/조회(GET)만 존재 — 수정·비활성 핸들러 없음(`handlers_users.go`)
    - `discovery.go` — 스캔 생성(202 즉시 반환)·상태 조회 핸들러. UUID 경로값은 `isUUID`로 검증(잘못된 값은 404, DB `::uuid` 캐스트 500 방지)
  - `migrations/` — SQL 마이그레이션. `go:embed`로 바이너리에 포함되어 **서버 기동 시 자동 적용**
    - `0002_core_schema` — 완전 구현 4테이블 + placeholder 4테이블(certificate_authorities/policies/renewal_jobs/deployment_jobs). fingerprint_sha256 UNIQUE(소문자 hex 64자 CHECK), status·type CHECK, 만료·상태·이름 인덱스
    - `0004_users_rbac` — users(역할 CHECK 6종)/sessions/departments(placeholder) + certificates.owner_id·department_id FK. audit_events.actor_id는 의도적으로 FK 없음(append-only 보호). 0003은 결번(이슈 #3은 마이그레이션 불필요) — 버전 공백 허용
    - `0006_scan_jobs` — 스캔 Job 상태 머신 테이블(status pending/running/completed/failed, targets text[]·ports int[], 진행률·결과 카운터) + 동시 스캔 경합 방지 부분 유니크 인덱스(targets IP당 1행·network_endpoint 위치 1행, store가 ON CONFLICT 아비터로 사용) + certificate_locations.tls_version. 0005는 결번(이슈 #4는 마이그레이션 불필요 — 0003과 동일 처리)
    - `0007_audit_chain` — audit_events 해시 체인(prev_hash 컬럼·pgcrypto digest·BEFORE INSERT 트리거가 advisory lock으로 직렬화, **id도 락 안에서 재발급** — identity 기본값은 락 대기 전에 뽑혀 체인 순서와 어긋남) + UPDATE/DELETE/TRUNCATE 차단 트리거 + prev_hash UNIQUE(분기 차단). 기존 행은 up에서 체인 백필. down은 트리거·함수 제거만(함수는 행 타입 의존이라 남기면 0002 down 실패). 테스트에서 audit_events 정리는 `ALTER TABLE … DISABLE TRIGGER USER` 후 DELETE
    - `0008_chain_keys` — certificates.subject_key_id/authority_key_id(소문자 hex CHECK) + SKI·subject_dn 인덱스. 기존 행은 NULL(원문 미보관 — 백필 불가), 체인 조회는 DN 폴백·재업로드 시 upsert 백필
    - `0009_sensors` — sensors(상태 pending/active/disabled, 1회용 등록 토큰은 SHA-256 해시만·클라이언트 인증서 fingerprint UNIQUE) + sensor_ca(단일 행 강제 boolean PK, CA 키는 AES-256-GCM 암호문+nonce만)
    - `0010_sensor_scan_jobs` — scan_jobs 리스 확장(job_type 'scan' 폐쇄·executor_type·sensor_id·leased_at/lease_expires_at·status에 'leased' 추가) + targets.executor_scope + 인덱스 스왑 uq_targets_network_ip→uq_targets_network_scope_ip(스코프,ip) + scan_job_endpoints(Observation 멱등 아비터, PK(job_id,ip,port)). store/discovery.go의 targets ON CONFLICT 아비터도 (executor_scope, ip)로 갱신 완료 — 서버 스캔 정상
  - `api/openapi.yaml` — OpenAPI 3 명세 (certificates 목록/summary/상세/import/export·auth·users·discovery/scans·audit/events·audit/verify·openapi.yaml 경로, bearer+cookie 보안 스킴 포함). `api/embed.go`(package apispec)가 go:embed로 바이너리에 포함 — 서버가 그대로 제공하고 정합 테스트도 같은 바이트를 파싱. 라우트 추가/제거 시 명세를 같은 커밋에서 고쳐야 테스트가 통과
- `frontend/` — Next.js (App Router) + TypeScript. 전 화면 클라이언트 fetch (이슈 #8·#9 구현)
  - `app/(app)/` — 인증 셸(사이드바+탑바+AuthProvider): `/`(대시보드 — KPI 8종·만료 구간·임박 표), `/certificates`(목록 — URL 쿼리 구동 검색·필터·정렬·페이지네이션 + 업로드 패널 + CSV 내보내기 버튼), `/certificates/[id]`(상세 — 위치·tls_version·체인 카드(상위 클릭 이동, 미보유 표시)), `/discovery`(스캔 폼 + 2s 폴링 + 실행기 선택(서버 직접/active 센서). Job 목록 API가 없어 localStorage로 이 브라우저 Job만 추적), `/sensors`(센서 목록·등록 다이얼로그(토큰·CA fingerprint·enroll 명령 1회 표시)·비활성화(system_administrator 전용) — **서버 sensors API는 Phase 3a U5 예정, api.ts 타입·함수는 계약 선정의**)
  - `app/login/` — 로그인 (셸 밖). `proxy.ts` — autocert_session 쿠키 없으면 /login 리다이렉트(1차 관문, 실검증은 /auth/me 401)
  - API 경로: 브라우저는 같은 출처 `/api/*`만 호출 → next.config.ts rewrites가 백엔드로 중계(쿠키 CORS 불요). 프록시 대상은 build-arg `BACKEND_INTERNAL_URL`(compose는 http://backend:8080)로 빌드 시 고정. headers()가 전 페이지에 CSP('self' 기반, script/style만 'unsafe-inline' — Next 인라인 요구, dev만 'unsafe-eval')·nosniff·DENY·no-referrer 부착. HSTS는 HTTP 배포라 주석 처리(HTTPS 전환 시 활성화)
  - `components/ui/` — shadcn/ui (badge/button/card/input/separator/table/label/textarea/select/progress/skeleton/alert/dialog)
  - `components/` — app-sidebar, app-topbar(사용자·로그아웃), cert-badges(파생 상태 뱃지·D-day)
  - `lib/` — api.ts(타입+엔드포인트 함수+ApiError), auth.tsx(useAuth·역할 게이트 — 업로드·스캔은 3역할), format.ts
  - `app/fonts.css` — 폰트는 npm 패키지 셀프호스팅 `@font-face` (폐쇄망 대응, CDN 없음)
- `docker-compose.yml` / `.env.example` — 루트. backend + postgres + frontend
  - 베이스 이미지 3종: `golang:1.25-alpine`·`gcr.io/distroless/static-debian12`(backend/Dockerfile), `node:24-alpine`(frontend/Dockerfile) — 폐쇄망 빌드 시 내부 레지스트리 미러 필요
- `mockup/index.html` — 승인된 4화면 정적 목업 (제품 코드 아님)
- `docs/` — 00-spec(명세) / 01-plan(기획) / 02-goals(목표) / STATE / GOTCHAS
  - 00-spec 15장=Phase 1~5 로드맵, 16장=MVP 제외 목록, 02-goals=성공 기준 7개+M1~M3(Phase 판정 시 대조 출처)
  - `docs/manual/` — 고객용 매뉴얼 (INSTALL/OPERATIONS — #8·#9 UI 기준 운영 절차 + 지원 사양(3장: 형식·스캔 범위·프록시 구조) 반영. 미구현 기능은 "(예정)" 표기 — 이슈 완료 시마다 갱신)

## 포트
- backend: 8080 / PostgreSQL: 5432 / frontend: 3000

## 실행
- `cp .env.example .env && docker compose up --build` [unix]

## 검증 명령
- backend: `go build ./...`, `go vet ./...`, 단위 테스트 `go test ./...` (backend/ 에서. 로컬 Go 없으면 Docker 경유)
- 라우트-OpenAPI 명세 정합: `make openapi-check` (backend/ 에서, DB 불필요 — backend/Makefile)
- DB 통합 테스트(db·store·api): compose db 기동 후 전용 테스트 DB 생성(`docker compose exec db createdb -U autocert autocert_test`) 뒤 `TEST_DATABASE_URL=postgres://autocert:<pw>@db:5432/autocert_test?sslmode=disable go test -p 1 ./...` — db 패키지가 up/down/up 전체를 돌리므로 **메인 DB(autocert)에는 돌리지 않는다**. **`-p 1` 필수**: 패키지 병렬 실행 시 db 패키지의 down이 다른 패키지가 쓰는 스키마를 지운다. 로컬 Go 없으면 compose 네트워크(`inherit_autocert_default`)에 붙인 golang 컨테이너 경유
- frontend: `npm run build`, `npm run lint` (frontend/ 에서)

## GitHub
- origin: `github.com/Falke060918/INHERIT_AutoCert` (private)
- 이슈 #1~#9 = Phase 1 작업 단위, 라벨 6종 배선됨
