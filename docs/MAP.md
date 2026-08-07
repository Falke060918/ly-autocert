# MAP — 코드베이스 구조·명령 캐시
확인일: 2026-08-07

## 저장소 구조
- `backend/` — Go 백엔드
  - `cmd/server/main.go` — 엔트리포인트. config 로드 → DB 연결 → 마이그레이션 → HTTP 서버 기동
  - `internal/config/` — 환경변수 기반 설정
  - `internal/db/` — PostgreSQL 연결 + 마이그레이션 적용
  - `internal/api/` — HTTP 라우팅·핸들러 (`/healthz` 등)
  - `migrations/` — SQL 마이그레이션. `go:embed`로 바이너리에 포함되어 **서버 기동 시 자동 적용**
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

## 포트
- backend: 8080 / PostgreSQL: 5432 / frontend: 3000

## 실행
- `cp .env.example .env && docker compose up --build` [unix]

## 검증 명령
- backend: `go build ./...`, `go vet ./...` (backend/ 에서. 로컬 Go 없으면 Docker 경유)
- frontend: `npm run build`, `npm run lint` (frontend/ 에서)

## GitHub
- origin: `github.com/Falke060918/INHERIT_AutoCert` (private)
- 이슈 #1~#9 = Phase 1 작업 단위, 라벨 6종 배선됨
