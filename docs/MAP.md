# MAP — 코드베이스 구조·명령 캐시
확인일: 2026-08-07

## 저장소 구조
- `backend/` — Go 백엔드
  - `cmd/server/main.go` — 엔트리포인트. config 로드 → DB 연결 → 마이그레이션 → HTTP 서버 기동
  - `internal/config/` — 환경변수 기반 설정 (`SESSION_TTL` 기본 8h, `BOOTSTRAP_ADMIN_USERNAME`/`_PASSWORD`)
  - `internal/db/` — PostgreSQL 연결 + 마이그레이션 적용. DB 테스트는 `TEST_DATABASE_URL` 설정 시에만 실행(미설정 시 스킵)
  - `internal/domain/` — 핵심 데이터 모델 Go 타입 (00-spec 12장 기준: Certificate/CertificateLocation/Target/AuditEvent + User/Role 6종 + 상태 enum)
  - `internal/certparse/` — 인증서 파서 (PEM 체인·DER, crypto/x509만). 개인키 검출 시 `ErrPrivateKeyFound`로 전체 거부. 새 포맷(P7B/PFX)은 `decoders` 목록에 추가
  - `internal/store/` — PostgreSQL 영속화. fingerprint 중복 판정 upsert + 감사 이벤트 + 목록·상세 조회(검색·필터·정렬·페이지네이션 — 정렬 컬럼은 화이트리스트, 만료 필터는 not_after 파생 계산). text[]는 SQL 쪽 `string_to_array(chr(31))`, int[]는 `string_to_array(',')::int[]`, inet은 `::inet`/`host()` 캐스트로 변환 (database/sql 제약). `discovery.go` — scan_jobs 상태 머신 + 발견 인증서 인벤토리 반영(target·network_endpoint 위치는 부분 유니크 인덱스 아비터의 ON CONFLICT upsert — 동시 실행 안전)
  - `internal/discovery/` — 에이전트리스 네트워크 TLS 스캔. `scanner.go`(crypto/tls 핸드셰이크로 서버 인증서 체인 수집, IP/CIDR·포트 Expand, InsecureSkipVerify는 **수집 전용**, 호스트명은 역방향 DNS 1s → 리프 CN 폴백) + `manager.go`(비동기 Job: 워커풀·상태 머신, 백그라운드는 `context.Background()`). 도달불가·핸드셰이크 실패는 조용히 실패 기록 후 계속
  - `internal/auth/` — 비밀번호 해시(bcrypt cost 12)·정책(12자↑)·세션 토큰(DB엔 SHA-256 해시만)·로그인/사용자 서비스. 초기 관리자는 users 비어 있을 때 `BOOTSTRAP_ADMIN_PASSWORD`로 1회 생성
  - `internal/audit/` — AuditEvent 기록(`Insert`는 pool/tx 겸용)·조회(`List`)·무결성 검증(`Verify`). 변경+감사는 단일 트랜잭션(감사 실패 시 변경도 롤백 — auth 서비스·store 모두). 해시 체인(prev_hash+SHA-256)은 DB BEFORE INSERT 트리거가 계산 — 검증도 같은 SQL 함수(audit_event_hash)로 재계산해 표현 불일치 오탐 없음. 꼬리 삭제는 체인만으로 탐지 불가 → verify 응답 head_hash 외부 기록으로 보완
  - `internal/api/` — 라우팅·핸들러·미들웨어. 라우트 등록은 `public()`/`protected(roles...)` 둘뿐 — 보호 라우트는 역할 명시 강제(빈 역할은 panic). 모든 응답에 X-Request-ID
    - 라우트: `/healthz`·`POST /api/v1/auth/login`(public) / logout·me(전 역할) / `GET|POST /api/v1/users`(system_administrator만) / `POST /api/v1/certificates/import`·`POST /api/v1/discovery/scans`(system_administrator·pki_administrator·operator) / `GET /api/v1/certificates`·`GET /api/v1/certificates/{id}`·`GET /api/v1/discovery/scans/{id}`(전 역할, 읽기 전용 — 감사 이벤트 없음) / `GET /api/v1/audit/events`·`GET /api/v1/audit/verify`(security_administrator·auditor만 — 7.11)
    - `discovery.go` — 스캔 생성(202 즉시 반환)·상태 조회 핸들러. UUID 경로값은 `isUUID`로 검증(잘못된 값은 404, DB `::uuid` 캐스트 500 방지)
  - `migrations/` — SQL 마이그레이션. `go:embed`로 바이너리에 포함되어 **서버 기동 시 자동 적용**
    - `0002_core_schema` — 완전 구현 4테이블 + placeholder 4테이블(certificate_authorities/policies/renewal_jobs/deployment_jobs). fingerprint_sha256 UNIQUE(소문자 hex 64자 CHECK), status·type CHECK, 만료·상태·이름 인덱스
    - `0004_users_rbac` — users(역할 CHECK 6종)/sessions/departments(placeholder) + certificates.owner_id·department_id FK. audit_events.actor_id는 의도적으로 FK 없음(append-only 보호). 0003은 결번(이슈 #3은 마이그레이션 불필요) — 버전 공백 허용
    - `0006_scan_jobs` — 스캔 Job 상태 머신 테이블(status pending/running/completed/failed, targets text[]·ports int[], 진행률·결과 카운터) + 동시 스캔 경합 방지 부분 유니크 인덱스(targets IP당 1행·network_endpoint 위치 1행, store가 ON CONFLICT 아비터로 사용) + certificate_locations.tls_version. 0005는 결번(이슈 #4는 마이그레이션 불필요 — 0003과 동일 처리)
    - `0007_audit_chain` — audit_events 해시 체인(prev_hash 컬럼·pgcrypto digest·BEFORE INSERT 트리거가 advisory lock으로 직렬화, **id도 락 안에서 재발급** — identity 기본값은 락 대기 전에 뽑혀 체인 순서와 어긋남) + UPDATE/DELETE/TRUNCATE 차단 트리거 + prev_hash UNIQUE(분기 차단). 기존 행은 up에서 체인 백필. down은 트리거·함수 제거만(함수는 행 타입 의존이라 남기면 0002 down 실패). 테스트에서 audit_events 정리는 `ALTER TABLE … DISABLE TRIGGER USER` 후 DELETE
  - `api/openapi.yaml` — OpenAPI 3 명세 (certificates 목록/상세/import·auth·users·discovery/scans·audit/events·audit/verify 경로, bearer+cookie 보안 스킴 포함)
- `frontend/` — Next.js (App Router) + TypeScript
  - `app/` — 4화면: `/`(대시보드), `/certificates`(목록), `/certificates/[id]`(상세), `/discovery`
  - `components/ui/` — shadcn/ui 컴포넌트 (badge/button/card/input/separator/table)
  - `components/` — app-sidebar, app-topbar
  - `lib/api.ts` — API 클라이언트. `NEXT_PUBLIC_API_URL` 기본값 `http://localhost:8080`
  - `app/fonts.css` — 폰트는 npm 패키지 셀프호스팅 `@font-face` (폐쇄망 대응, CDN 없음)
- `docker-compose.yml` / `.env.example` — 루트. backend + postgres + frontend
- `mockup/index.html` — 승인된 4화면 정적 목업 (제품 코드 아님)
- `docs/` — 00-spec(명세) / 01-plan(기획) / 02-goals(목표) / STATE / GOTCHAS
  - `docs/manual/` — 고객용 매뉴얼 (INSTALL/OPERATIONS, 미구현 기능은 "(예정)" 표기 — 이슈 완료 시마다 갱신)

## 포트
- backend: 8080 / PostgreSQL: 5432 / frontend: 3000

## 실행
- `cp .env.example .env && docker compose up --build` [unix]

## 검증 명령
- backend: `go build ./...`, `go vet ./...`, 단위 테스트 `go test ./...` (backend/ 에서. 로컬 Go 없으면 Docker 경유)
- DB 통합 테스트(db·store·api): compose db 기동 후 전용 테스트 DB 생성(`docker compose exec db createdb -U autocert autocert_test`) 뒤 `TEST_DATABASE_URL=postgres://autocert:<pw>@db:5432/autocert_test?sslmode=disable go test -p 1 ./...` — db 패키지가 up/down/up 전체를 돌리므로 **메인 DB(autocert)에는 돌리지 않는다**. **`-p 1` 필수**: 패키지 병렬 실행 시 db 패키지의 down이 다른 패키지가 쓰는 스키마를 지운다. 로컬 Go 없으면 compose 네트워크(`inherit_autocert_default`)에 붙인 golang 컨테이너 경유
- frontend: `npm run build`, `npm run lint` (frontend/ 에서)

## GitHub
- origin: `github.com/Falke060918/INHERIT_AutoCert` (private)
- 이슈 #1~#9 = Phase 1 작업 단위, 라벨 6종 배선됨
