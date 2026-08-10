# STATE — 마지막 갱신: 2026-08-10

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**랩 첫 설치 실사용 피드백 6건을 전부 반영한 UI 전면 개선 작업 단위가 끝났다.** 직전 상태는 "설치·반입 경험이 제품 수준"이었고, 이번 세션은 **화면**을 같은 수준으로 끌어올린 것이다. Phase 1 인벤토리 + Phase 3a 센서라는 기능 범위 자체는 그대로이며 Phase 2(CA 연결·발급)는 여전히 미착수다.

### 이번 세션에 들어간 것
- **'관제 콘솔' 테마 + 다크모드** — 심청 슬레이트 배경 + cert-blue 강조, 상태 3색(danger/warn/ok)을 badge tone 변형으로 고정(장식용 금지), 버튼에 위험 확정용 솔리드 `danger` 추가, 타이포 유틸리티. 다크모드는 의존성 없이 자작 배선(`ThemeProvider`·`ThemeToggle` + FOUC 방지 인라인 스크립트).
- **대시보드 재구성** — 만료 지평선 히어로(자작 SVG: 이미 만료 블록 + 90일 주 단위 밀도 바, 구간 클릭 = 만료 필터 목록으로 이동)·KPI 밴드 8종(숫자마다 필터 목록 링크 — 02-goals 성공 기준 3)·발급기관 분포 도넛·스캔 현황·최근 등록. 집계 API 실패는 카드 단위로 격리한다(대시보드 전체가 죽지 않게).
- **디스커버리 2카드 탭 재구성** — 카드 1 "검색 실행"([스캔|전 포트 스윕] 탭, 대상 입력 + 제안 칩 3종(최근 사용·알려진 호스트 /24·설치 감지), 실행기 선택은 탭 공용·전환 시 입력 보존), 카드 2 "작업 현황"([스캔|스윕|주기 재스캔] 탭, 서버 Job 목록이 정본이고 localStorage 추적은 폴백). **중복 발주 가드**(409 `duplicate_scan` → 기존 Job 강조·스크롤 이동)와 **실행 중 취소**(확인 다이얼로그, `canceled`는 실패가 아닌 중립 배지)가 여기 붙었다.
- **인증서 발급자 축** — 목록에 발급자 열(`issuer_organization` 우선, 폴백 dnOrganization→dnCommonName) + 발급기관 필터 Select + 뷰 세그먼트 [목록|발급 기관](CA 루트→중간 2계층 트리, 보유 CA 클릭 시 `issued_by=` 필터로 목록 전환). 상세는 체인 카드를 **수직 레일**로 재구성(단계 점·커넥터·중간/루트 CA 뱃지·"이 CA가 발급한 N건 →" 링크).
- **백엔드 마이그레이션 2건 + API** — `0016_issuer_organization`(컬럼·인덱스 + issuer_dn RFC2253 정규식 소급 백필, **같은 커밋에 신규 INSERT 경로도 수정** — 0015 origin 전례), `0017_scan_cancel_dedup`(status CHECK에 `canceled` + `dedup_hash` 부분 유니크). API는 발급기관 집계(`GET /certificates/issuers`)·스캔 Job 목록(`GET /discovery/scans`)·스캔 취소(`POST /discovery/scans/{id}/cancel`)·대상 제안(`GET /discovery/scan-suggestions`)·목록 필터 확장(`issuer_org`·`issued_by`) 5종.
- **DN 파서 교체** — `frontend/lib/format.ts`의 정규식 분해를 RFC2253 이스케이프 인지 토크나이저 `parseDN`으로 교체(`\,`가 들어간 CN이 잘리던 버그). RFC1779 따옴표·`\2c` 16진 이스케이프·멀티밸류 RDN·openssl 슬래시 표기까지 테스트로 고정.
- **리뷰 지적 6건 수정**(작업 단위 마지막) — ① 대시보드 만료 지평선이 `page_size=500`을 보내 실서버에서 400(=화면 전체 사망)이던 것을 200×최대 3페이지 루프로 교체 ② 루트 CA 리프 수 거짓 표기 → `subtree_leaf_count`를 응답에 추가하고 라벨을 "직접 발급 N건 · 하위 포함 M건"으로 분리 ③ 스캔 현황 목록을 `origin=manual`로 한정(스윕 청크·재스캔 Job에는 취소 버튼 미표시) ④ "조직 미상" URL 규약을 `__none__` 센티널로 통일(빈 값도 동일 취급) ⑤ 취소 409 문구를 상태별 분기(종료된 Job / 센서 실행 중) ⑥ MAP 갱신.

### 목업 선행 검증
`docs/mockups/ui-redesign.html`(4화면 단일 HTML)로 **사용자가 먼저 확정한 뒤** 구현에 들어갔다. 탑바의 "Phase 1 — 인증서 인벤토리" 문구는 사용자 지시로 삭제했고, **그 자리는 추후 섹션/테넌트 분리 탭 용도로 비워둔다**(다른 것으로 채우지 않는다).

### 버전
`VERSION`·`docker-compose.yml` 기본 태그·`CHANGELOG.md` 세 곳 모두 **1.0.0**이고 세 곳 다 패키징 스크립트가 실제로 검사한다. 이번 UI 개선분은 `## Unreleased`에 쌓인다. 최신 마이그레이션은 **0017**.

### 검증
프론트엔드 `npm install` → `npx next typegen` → `tsc --noEmit`·`npm run lint`·`npm test`(13건)·`next build` 전부 통과. 백엔드는 `golang:1.25-alpine` 컨테이너 + **호스트 포트를 열지 않은 postgres 컨테이너를 같은 docker 네트워크에** 띄우는 방식으로 `go build`·`go vet`·`internal/store`·`internal/api` 테스트 통과(subtree 합산 테스트 포함) — 예전의 "이 머신에선 Go 테스트 불가" 전제는 폐기됐다(GOTCHAS). **실제 docker compose 기동 검증은 여전히 불가**(리스너 바인딩 차단) — 고객사/랩 설치에서 확인한다.

## 다음 할 일 (우선순위 순)
1. **멀티테넌시 대비**(이전 세션에 사용자와 논의 완료, 결론만 기록) — MSP/하이브리드 시장 대응으로 `tenant_id`를 심는 안. **옵션 B: 스키마 컬럼 + 쿼리 필터 강제 계층까지만**(UI·테넌트 전환기는 나중). 단일 테넌트는 멀티테넌트의 특수 케이스라 **역방향은 불가**하다. 착수 최적 시점은 **Phase 2(CA 연결·발급) 스키마 설계 직전** — Phase 2에서 테이블이 늘어난 뒤에 넣으면 두 번 일한다. 착수 전 planner에 비용 산정 위임(영향 테이블 수·쿼리 지점 수·필터 강제 계층 위치·권한 모델 2층화). 탑바에 비워둔 공간이 이 작업의 UI 자리다.
2. **TLS 관련 후속**(사용자 "나중에" 지시) — 목록 화면 TLS 열·필터·정렬 / cipher suite 수집 / "서버 지원 버전 목록" 탐지. 마지막 것은 엔드포인트당 버전 고정 재핸드셰이크 N회가 필요해 **레이트 리미터·스윕 ETA와 충돌 검토가 선행**이다. cipher suite·지원버전은 **소급 불가** — 재스캔으로만 채워진다.
3. **Phase 2(CA 연결과 발급)** + 동반 2건: Prometheus 메트릭 최소셋(`/metrics`), Helm 선행 항목(오프라인 이미지 번들은 완료).
4. **센서 enroll 토큰이 프로세스 인자에 노출된다** — Go CLI가 `--token` 플래그만 받아 `ps`로 보인다. 설치 스크립트가 `--token-file`/환경변수로 완화했으나 근본 해결은 **센서 CLI에 stdin/파일 토큰 입력 추가**다. 1회용·24시간 토큰이라 치명도는 낮지만 인증서 제품이므로 정리 대상.
5. **오프라인 라이선스 구현** — 설계 완료(docs/design/03-license.md), 착수 전 미해결 결정 2건: 에디션 체계와 기본 한도 수치 / 서명키 보관처와 kid 회전 주기. `backend/cmd/licensegen`은 아직 존재하지 않는다(설계 문서상의 계획 경로).
6. **실사용 피드백 잔여 관찰** — 6건은 이번 세션에 전부 반영됐다(대역 제안 칩 / 2카드 탭 / 중복 가드·취소 / 스마트 스캔 프리셋·최근 사용 타겟팅 / UI 개선·발급자 표시 / CA 계층 뷰). 랩에서 다시 돌려보고 **체감이 실제로 나아졌는지**를 확인한 뒤 다음 개선 항목을 받는다 — 특히 스캔 취소가 센서 스코프에서 어떻게 보이는지(센서 실행 중은 취소 불가이고 409 문구로만 안내된다).
7. **잔여 갭**
   - **실제 docker 기동 검증 미수행** — 이 머신에서 리스너 바인딩이 막혀 라이브 헬스체크를 못 했다. 첫 실환경 설치에서 확인할 것.
   - `.env.example`에 `COMPOSE_PROJECT_NAME` 추가는 **사용자 수작업 필요**(권한 정책상 에이전트가 이 파일을 읽지도 쓰지도 못한다). 설정하지 않아도 `install.sh`가 채우므로 동작에는 지장 없다.
   - `docker-compose.yml` 1행 주석이 구절차(`cp .env.example .env && docker compose up --build`)다 — 개발환경 문구인데 고객사 패키지에도 그대로 실린다. 정리 필요.
   - **Windows 메인 서버는 미지원**(사용자 문의로 확인) — Docker 기반이라 OS별 바이너리가 없고 검증 범위 밖이다. 센서만 Windows 지원.
   - DB 연결이 `sslmode=disable`이다 — docs/manual/TLS.md 9장에 현황·방향만 있고 구현은 없다.
   - rootless Docker 환경 안내 부재. Docker 그룹 소속이 사실상 root 동등이라는 점도 문서에 없다.
   - Windows 센서의 개인키가 NTFS ACL로 보호되지 않는다(`os.Chmod`가 Windows에서 무력).
   - 매뉴얼(INSTALL/OPERATIONS)에 이번 UI 변경 반영이 없다 — 화면 캡처 없는 텍스트 매뉴얼이라 치명적이진 않다.
8. **잔여 low**
   - 재스캔 수동 발주의 actor가 감사에 안 실린다 — 사람 발주와 자동 발주가 origin으로만 구분된다. 스캔 취소도 같은 형태다.
   - `handlers_users.go:48,192`·`handlers_notification_policies.go:485`·`handlers_setup.go`가 `err.Error()` 영문 원문을 사용자에게 노출한다.
   - `config.disableChannel`이 채널당 **첫 사유만** 기록한다.
   - `notify.netErrKind`가 `webhook.go`에 있다(원자리는 공용 `channels.go`).
   - API 계층이 `SendTest` nil을 무조건 `sent`로 표시한다("보낼 것이 없어 성공"을 `skipped`로 가르려면 인터페이스 변경 필요).
   - `useTicker`가 두 곳에서 중복 export된다.
   - STARTTLS 포트 인증서 수집 미지원(설계상 확장점만) / enroll 엔드포인트 레이트리밋 없음 / 목록 "초기화" 시 검색 인풋 잔류 / 감사 정본 문자열 0x1F 경계 / 감사 로그 조회 UI 부재.

## 이번 세션 특이사항
- **사용자 결정 2건** — ① 탑바에서 "Phase 1 — 인증서 인벤토리"를 지운 자리는 **추후 섹션/테넌트 분리 탭 용도로 비워둔다** ② UI 변경은 **목업(단일 HTML)으로 사용자가 먼저 확정한 뒤** 구현에 들어간다(이번 `docs/mockups/ui-redesign.html`이 그 절차의 첫 사례다).
- 리스너 바인딩이 막힌 이 머신에서도 **DB 테스트는 돈다** — `-p` 없는 postgres 컨테이너를 사용자 정의 docker 네트워크에 띄우고 golang 컨테이너를 같은 네트워크에 붙이면 된다(GOTCHAS 등재, 검증용 컨테이너·네트워크는 매번 정리).
- `page_size` 상한 200은 **로컬 검증 전부가 green이어도 실서버에서만 터진다** — 대시보드가 500을 보내 화면 전체가 죽을 뻔한 것을 리뷰가 잡았다(GOTCHAS 등재).
- `.env`·`.env.example`은 권한 정책상 에이전트가 읽지도 쓰지도 못한다 — 신규 변수는 스크립트·문서로만 배선한다.
- 세션 후반 API 한도로 에이전트가 한 번 끊겼다 — 중단 지점이 "코드 수정 완료, MAP 갱신 직전"이라 진단부터 하고 이어받았다. 작업 단위를 커밋으로 잘게 끊어둔 것이 복구를 쉽게 했다.
- git author는 자동 추정값(markus@jjuui-MacBookAir.local) 그대로 두기로 **사용자가 결정**했다 — 더 이상 지적하지 않는다.

## 주요 파일
- `frontend/app/(app)/` — `page.tsx`(대시보드 히어로·KPI 밴드) · `certificates/`(page + `issuers-view.tsx` + `[id]` 체인 레일) · `discovery/`(2카드 탭 + `target-field.tsx`·`sweep-panel.tsx`·`rescan-card.tsx`·`scan-status-card.tsx`)
- `frontend/lib/` — `issuers.ts`(집계·필터 계약 + `ISSUER_ORG_UNKNOWN`) · `scans.ts`(Job 목록·취소·대역 제안) · `format.ts`(RFC2253 `parseDN` + `format.test.mts`) · `api.ts`
- `frontend/components/` — `charts/`(expiry-horizon·issuer-distribution·scan-status-card) · `ui/`(shadcn + badge tone·button danger) · theme provider/toggle
- `backend/internal/store/` — `certificates_issuers.go`(분포·CA 트리·subtree 합산) · `discovery.go`(취소·중복 가드·`ScanJobStateError`) · `scan_suggestions.go`
- `backend/internal/api/discovery.go`(취소 409 분기) · `backend/api/openapi.yaml` · `backend/migrations/`(최신 **0017**)
- `docs/mockups/ui-redesign.html`(사용자 확정 목업) / `docs/MAP.md` / `docs/GOTCHAS.md` / `CHANGELOG.md`(`## Unreleased`)
- `scripts/` — `install.sh` · `install-sensor.sh`/`.ps1` · `lib/tui.sh` · `bundle-offline.sh`(`--target`) · `load-offline.sh` · `build-sensor-dist.sh`
- `docs/manual/` — INSTALL · SENSOR · OPERATIONS · TLS / `docs/design/` — 03-license(설계만) · 04-port-autoscan · 05-notifications · 06-rescan-and-replacement
