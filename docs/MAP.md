# MAP — 코드베이스 구조·명령 캐시
확인일: 2026-08-07

## 저장소 구조
- `backend/` — Go 백엔드
  - `cmd/server/main.go` — 엔트리포인트. config 로드 → DB 연결 → 마이그레이션 → HTTP 서버 기동
  - `internal/config/` — 환경변수 기반 설정
  - `internal/db/` — PostgreSQL 연결 + 마이그레이션 적용. `migrate_test.go`는 `TEST_DATABASE_URL` 설정 시에만 실행(미설정 시 스킵)
  - `internal/domain/` — 핵심 데이터 모델 Go 타입 (00-spec 12장 기준: Certificate/CertificateLocation/Target/AuditEvent + 상태 enum)
  - `internal/certparse/` — 인증서 파서 (PEM 체인·DER, crypto/x509만). 개인키 검출 시 `ErrPrivateKeyFound`로 전체 거부. 새 포맷(P7B/PFX)은 `decoders` 목록에 추가
  - `internal/store/` — PostgreSQL 영속화. fingerprint 중복 판정 upsert + 감사 이벤트. text[]는 SQL 쪽 `string_to_array(chr(31))`, inet은 `::inet`/`host()` 캐스트로 변환 (database/sql 제약)
  - `internal/api/` — HTTP 라우팅·핸들러. `/healthz`, `POST /api/v1/certificates/import`(업로드). `middleware.go`의 `withRequestID`(X-Request-ID) + `requirePermission`(RBAC 확장점, 이슈 #6 전까지 통과)
  - `migrations/` — SQL 마이그레이션. `go:embed`로 바이너리에 포함되어 **서버 기동 시 자동 적용**
    - `0002_core_schema` — 완전 구현 4테이블 + placeholder 4테이블(certificate_authorities/policies/renewal_jobs/deployment_jobs). fingerprint_sha256 UNIQUE(소문자 hex 64자 CHECK), status·type CHECK, 만료·상태·이름 인덱스
  - `api/openapi.yaml` — OpenAPI 3 명세
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
- backend: `go build ./...`, `go vet ./...` (backend/ 에서. 로컬 Go 없으면 Docker 경유)
- DB 통합 테스트(db·store): compose db 기동 후 전용 테스트 DB 생성(`docker compose exec db createdb -U autocert autocert_test`) 뒤 `TEST_DATABASE_URL=postgres://autocert:<pw>@db:5432/autocert_test?sslmode=disable go test -p 1 ./...` — db 패키지가 up/down/up 전체를 돌리므로 **메인 DB(autocert)에는 돌리지 않고**, 패키지 병렬 실행이 down과 겹치지 않게 **`-p 1` 필수**. 로컬 Go 없으면 compose 네트워크(`inherit_autocert_default`)에 붙인 golang 컨테이너 경유
- frontend: `npm run build`, `npm run lint` (frontend/ 에서)

## GitHub
- origin: `github.com/Falke060918/INHERIT_AutoCert` (private)
- 이슈 #1~#9 = Phase 1 작업 단위, 라벨 6종 배선됨
