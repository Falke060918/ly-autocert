# STATE — 마지막 갱신: 2026-08-11

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**1.1.0 릴리즈 패키징 + 업그레이드 경로 정비가 끝났다.** 직전 세션이 UI 전면 개선 구현을 마쳤고, 이번 작업 단위는 그것을 **버전 올려 오프라인 배포 패키지로 뽑고**, 이어서 **"기존 설치를 새 버전으로 올리는 경로"의 구멍 두 개를 막은 뒤 패키지를 다시 뽑은 것**이다. 기능 범위는 1.0.0과 같다 — Phase 1 인벤토리 + Phase 3a 센서이며 Phase 2(CA 연결·발급)는 여전히 미착수다.

### 버전 1.0.0 → 1.1.0
동기화 지점은 이제 **4곳**이고 `scripts/bundle-offline.sh`가 전부 검사한다.
- 루트 `VERSION`(단일 진실원천)
- `docker-compose.yml` 기본 태그 — `${AUTOCERT_VERSION:-1.1.0}` 참조 **2개**(backend·frontend)
- `CHANGELOG.md` — 헤딩 포맷 `## v<버전> (<날짜>)` **고정**. 이번 릴리즈는 `## v1.1.0 (2026-08-11)`이고 그 위에 빈 `## Unreleased`를 새로 얹었다
- `frontend/package.json` + `package-lock.json` — lock은 최상위와 `packages[""]` 둘 다(=총 3곳)

### 이번 세션에 들어간 것
- **CHANGELOG 확정** — `## Unreleased`에 쌓여 있던 UI 전면 개선분을 `## v1.1.0 (2026-08-11)`로 확정하고 **사용자 관점 문구**로 정리했다(화면 / 설치·반입 절). 내부 구현 용어가 아니라 "무엇이 달라졌는지"로 쓴다.
- **번들 스크립트에 검사 7 추가** — 프론트엔드 버전 3곳(package.json·lock 최상위·lock `packages[""]`)이 루트 `VERSION`과 일치하는지 대조한다. 검사가 6종 → **7종**이 됐고 1~4·7은 사전 검사, 5·6은 패키지 생성 직후 사후 assert다. 계기는 **지난 1.0.0 릴리즈가 프론트 버전 0.1.0인 채 조용히 나간 것**이다.
- **오프라인 배포 패키지 3종 생성 완료** — `dist/main_1.1.0.tar.gz`(193 MiB) · `dist/sensor_linux_1.1.0.tar.gz`(2.7 MiB) · `dist/sensor_windows_1.1.0.zip`(2.7 MiB) + 각 `.sha256` + 집계 `dist/SHA256SUMS-1.1.0`. 검사 1~7 전부 통과, 패키지 내부 SHA256SUMS 대조 OK, 센서 바이너리 `--version` = 1.1.0.
- **업그레이드 경로 정비 2건** (같은 작업 단위의 후반)
  - **문서** — OPERATIONS 5장이 개발환경 절차(`docker compose up --build`)를 지시하고 있었다. 폐쇄망에서는 소스 빌드가 성립하지 않으므로 "오프라인 패키지 반입 + `install.sh`"로 바꾸고 **절차 정본을 INSTALL 5.1.4 한 곳으로 모았다**(OPERATIONS는 백업·확인·롤백만 적고 링크한다). INSTALL 5.1.4에는 `COMPOSE_PROJECT_NAME` 기본값 정정(`inherit_autocert`는 존재하지 않는 값이었다)·승계 `.env`의 `AUTOCERT_VERSION` 함정·HTTPS 재기동 함정을 넣었고, 패키지 `MANIFEST.txt`에도 업그레이드 3단계를 추가했다.
  - **스크립트** — `install.sh`에 `detect_https_deployment` 추가. `dc()`가 `CFG_HTTPS=1`일 때만 TLS 오버레이를 얹는데 그 값이 `mode_https`에서만 서서, HTTPS 운영 중인 설치를 인자 없이 재실행하면 프록시가 빠진 채 뜨고 `SESSION_COOKIE_SECURE=true`만 남아 **로그인만 조용히 실패**했다. 감지는 오탐이 곧 기동 실패이므로 **AND 게이트**다 — 흔적 1개 이상(`HTTPS_PORT`/`SESSION_COOKIE_SECURE=true`/proxy 컨테이너) + `deploy/tls/server.crt`·`server.key` 실존 + 오버레이 파일 + compose ≥ 2.24.4. 하나라도 없으면 켜지 않고 `--mode https` 안내 경고만 낸다. `--mode https` 명시 실행은 감지를 거치지 않는다.
  - **매뉴얼 버전 예시 정정** — INSTALL·SENSOR의 예시 파일명이 `1.0.0`에 고정돼 있던 것을 1.1.0으로 맞췄다(실제 과거 버전을 가리키는 2곳은 그대로 뒀다).
- **재패키징 완료** — 위 3건을 커밋(`eb30920`)한 뒤 `sh scripts/bundle-offline.sh --skip-build`로 dist/ 산출물 3종을 다시 뽑았다. 검사 1~7 통과·sha256 사이드카/집계 재생성·패키지 내부 `manual/INSTALL.md`에 정정된 절차 반영 확인. **MANIFEST의 소스 커밋 = `eb30920`**(이전 패키지는 `9ddbda3`이었다). `dist/docker-ce-offline-el9*`는 버전 무관 재사용물이라 손대지 않았다.

### 검증
- 릴리즈 경로: 번들 스크립트 검사 7종 통과 + 산출물 체크섬·센서 `--version` 대조까지가 이번 세션에 실제로 돌린 범위다. 재패키징분도 같은 범위로 재확인했다.
- 스크립트 변경분은 `sh -n scripts/install.sh`·`sh -n scripts/bundle-offline.sh`(+`bash -n`) 문법 검사까지가 전부다 — **`detect_https_deployment`의 실동작(기존 HTTPS 설치 재실행)은 검증하지 못했다.** 이 머신에서 compose 기동 자체가 안 되기 때문이며, 첫 실환경 업그레이드에서 확인할 항목이다.
- 코드 검증은 직전 세션 결과가 유효하다 — 프론트엔드 `npm install` → `npx next typegen` → `tsc --noEmit`·`npm run lint`·`npm test`(13건)·`next build` 통과, 백엔드는 `golang:1.25-alpine` + 호스트 포트를 열지 않은 postgres를 같은 docker 네트워크에 붙여 `go build`·`go vet`·`internal/store`·`internal/api` 통과.
- **이 머신에서 docker 이미지 빌드는 된다**(막힌 것은 리스너 바인딩뿐이다). 다만 **실제 `docker compose` 기동 검증은 여전히 미수행**이며 첫 실환경 설치에서 확인한다.

### 사용자 결정
- `dist/docker-ce-offline-el9*`(RHEL9용 Docker 설치 오프라인 패키지)는 **버전 무관 재사용물**이므로 dist/에 그대로 둔다.
- (직전 세션 유지) 탑바에서 "Phase 1 — 인증서 인벤토리"를 지운 자리는 **추후 섹션/테넌트 분리 탭 용도로 비워둔다**.
- (직전 세션 유지) UI 변경은 **목업(단일 HTML)으로 사용자가 먼저 확정한 뒤** 구현에 들어간다 — `docs/mockups/ui-redesign.html`이 첫 사례다.
- git author는 자동 추정값(markus@jjuui-MacBookAir.local) 그대로 둔다 — 더 이상 지적하지 않는다.

## 다음 할 일 (우선순위 순)
1. **멀티테넌시 대비**(사용자와 논의 완료, 결론만 기록) — MSP/하이브리드 시장 대응으로 `tenant_id`를 심는 안. **옵션 B: 스키마 컬럼 + 쿼리 필터 강제 계층까지만**(UI·테넌트 전환기는 나중). 단일 테넌트는 멀티테넌트의 특수 케이스라 **역방향은 불가**하다. 착수 최적 시점은 **Phase 2(CA 연결·발급) 스키마 설계 직전** — Phase 2에서 테이블이 늘어난 뒤에 넣으면 두 번 일한다. 착수 전 planner에 비용 산정 위임(영향 테이블 수·쿼리 지점 수·필터 강제 계층 위치·권한 모델 2층화). 탑바에 비워둔 공간이 이 작업의 UI 자리다.
2. **TLS 관련 후속**(사용자 "나중에" 지시) — 목록 화면 TLS 열·필터·정렬 / cipher suite 수집 / "서버 지원 버전 목록" 탐지. 마지막 것은 엔드포인트당 버전 고정 재핸드셰이크 N회가 필요해 **레이트 리미터·스윕 ETA와 충돌 검토가 선행**이다. cipher suite·지원버전은 **소급 불가** — 재스캔으로만 채워진다.
3. **Phase 2(CA 연결과 발급)** + 동반 2건: Prometheus 메트릭 최소셋(`/metrics`), Helm 선행 항목(오프라인 이미지 번들은 완료).
4. **센서 enroll 토큰이 프로세스 인자에 노출된다** — Go CLI가 `--token` 플래그만 받아 `ps`로 보인다. 설치 스크립트가 `--token-file`/환경변수로 완화했으나 근본 해결은 **센서 CLI에 stdin/파일 토큰 입력 추가**다. 1회용·24시간 토큰이라 치명도는 낮지만 인증서 제품이므로 정리 대상.
5. **오프라인 라이선스 구현** — 설계 완료(docs/design/03-license.md), 착수 전 미해결 결정 2건: 에디션 체계와 기본 한도 수치 / 서명키 보관처와 kid 회전 주기. `backend/cmd/licensegen`은 아직 존재하지 않는다(설계 문서상의 계획 경로).
6. **랩 재검증** — 실사용 피드백 6건은 직전 세션에 전부 반영됐고 이제 **1.1.0 패키지로 실제 설치해 체감이 나아졌는지**를 확인한다. 특히 ① 첫 실환경 compose 기동(위 잔여 갭 1번의 해소 경로이기도 하다) ② 스캔 취소가 센서 스코프에서 어떻게 보이는지(센서 실행 중은 취소 불가이고 409 문구로만 안내된다) ③ **HTTPS 설치를 인자 없는 `install.sh`로 업그레이드했을 때 `detect_https_deployment`가 실제로 오버레이를 유지하는지**(`deploy/tls/`를 신버전 디렉터리로 복사한 경우/안 한 경우 둘 다).
7. **잔여 갭**
   - **실제 docker compose 기동 검증 미수행** — 이 머신에서 리스너 바인딩이 막혀 라이브 헬스체크를 못 했다. 첫 실환경 설치(1.1.0 패키지)에서 확인할 것.
   - `.env.example`에 `COMPOSE_PROJECT_NAME` 추가는 **사용자 수작업 필요**(권한 정책상 에이전트가 이 파일을 읽지도 쓰지도 못한다). 설정하지 않아도 `install.sh`가 채우므로 동작에는 지장 없다.
   - `docker-compose.yml` 1행 주석이 구절차(`cp .env.example .env && docker compose up --build`)다 — 개발환경 문구인데 고객사 패키지에도 그대로 실린다. 정리 필요.
   - **Windows 메인 서버는 미지원**(사용자 문의로 확인) — Docker 기반이라 OS별 바이너리가 없고 검증 범위 밖이다. 센서만 Windows 지원.
   - DB 연결이 `sslmode=disable`이다 — docs/manual/TLS.md 9장에 현황·방향만 있고 구현은 없다.
   - rootless Docker 환경 안내 부재. Docker 그룹 소속이 사실상 root 동등이라는 점도 문서에 없다.
   - Windows 센서의 개인키가 NTFS ACL로 보호되지 않는다(`os.Chmod`가 Windows에서 무력).
   - 매뉴얼(INSTALL/OPERATIONS)에 1.1.0 UI 변경 반영이 없다 — 화면 캡처 없는 텍스트 매뉴얼이라 치명적이진 않다.
8. **잔여 low**
   - 재스캔 수동 발주의 actor가 감사에 안 실린다 — 사람 발주와 자동 발주가 origin으로만 구분된다. 스캔 취소도 같은 형태다.
   - `handlers_users.go:48,192`·`handlers_notification_policies.go:485`·`handlers_setup.go`가 `err.Error()` 영문 원문을 사용자에게 노출한다.
   - `config.disableChannel`이 채널당 **첫 사유만** 기록한다.
   - `notify.netErrKind`가 `webhook.go`에 있다(원자리는 공용 `channels.go`).
   - API 계층이 `SendTest` nil을 무조건 `sent`로 표시한다("보낼 것이 없어 성공"을 `skipped`로 가르려면 인터페이스 변경 필요).
   - `useTicker`가 두 곳에서 중복 export된다.
   - STARTTLS 포트 인증서 수집 미지원(설계상 확장점만) / enroll 엔드포인트 레이트리밋 없음 / 목록 "초기화" 시 검색 인풋 잔류 / 감사 정본 문자열 0x1F 경계 / 감사 로그 조회 UI 부재.

## 이번 세션 특이사항
- **릴리즈 커밋을 `--amend`하면 이미 만든 패키지가 dangling 해시를 가리킨다** — MANIFEST의 `소스 커밋`이 amend 전 해시로 굳기 때문이다. amend 했으면 `--skip-build`로 재패키징해 HEAD와 맞춘다(GOTCHAS 등재).
- **CHANGELOG 릴리즈 헤딩 포맷은 릴리즈를 중단시킬 수 있다** — `## v<버전> (<날짜>)`가 아니면 `bundle-offline.sh` 검사 4/6이 die한다(GOTCHAS 등재).
- 버전 동기화 지점이 4곳이라는 사실 자체가 GOTCHAS·MAP에 모두 들어갔다 — 릴리즈 때 한 곳이라도 빼먹으면 스크립트가 막는다(1.0.0은 검사 7이 없어서 프론트 0.1.0인 채 나갔다).
- `.env`·`.env.example`은 권한 정책상 에이전트가 읽지도 쓰지도 못한다 — 신규 변수는 스크립트·문서로만 배선한다.
- **`deploy/tls/server.crt`·`server.key`는 어떤 패키지에도 들어 있지 않다**(고객사가 직접 두는 파일). 그래서 신버전 디렉터리에는 기본적으로 없고, 업그레이드 시 복사해 오지 않으면 HTTPS 자동 감지가 걸리지 않는다 — 감지 폴백이 있어도 `--mode https` 명시가 여전히 정답인 이유다(GOTCHAS 등재).
- 워크트리 2개(문서/스크립트)를 병렬로 돌렸는데 둘 다 `docs/MAP.md`를 건드렸다 — **다른 절을 고쳐서 자동 병합됐다**(충돌 없음). MAP처럼 모두가 만지는 파일은 위임 시 "어느 절을 고칠지"를 갈라 주면 충돌이 나지 않는다.

## 주요 파일
- 릴리즈 축 — `VERSION` · `docker-compose.yml`(태그 참조 2곳) · `CHANGELOG.md` · `frontend/package.json`+`package-lock.json` · `scripts/bundle-offline.sh`(검사 7종) · `dist/`(1.1.0 산출물 3종 + `SHA256SUMS-1.1.0` + 버전 무관 `docker-ce-offline-el9*`)
- `frontend/app/(app)/` — `page.tsx`(대시보드 히어로·KPI 밴드) · `certificates/`(page + `issuers-view.tsx` + `[id]` 체인 레일) · `discovery/`(2카드 탭 + `target-field.tsx`·`sweep-panel.tsx`·`rescan-card.tsx`·`scan-status-card.tsx`)
- `frontend/lib/` — `issuers.ts`(집계·필터 계약 + `ISSUER_ORG_UNKNOWN`) · `scans.ts`(Job 목록·취소·대역 제안) · `format.ts`(RFC2253 `parseDN` + `format.test.mts`) · `api.ts`
- `frontend/components/` — `charts/`(expiry-horizon·issuer-distribution·scan-status-card) · `ui/`(shadcn + badge tone·button danger) · theme provider/toggle
- `backend/internal/store/` — `certificates_issuers.go`(분포·CA 트리·subtree 합산) · `discovery.go`(취소·중복 가드·`ScanJobStateError`) · `scan_suggestions.go`
- `backend/internal/api/discovery.go`(취소 409 분기) · `backend/api/openapi.yaml` · `backend/migrations/`(최신 **0017**)
- `docs/mockups/ui-redesign.html`(사용자 확정 목업) / `docs/MAP.md` / `docs/GOTCHAS.md`
- `scripts/` — `install.sh` · `install-sensor.sh`/`.ps1` · `lib/tui.sh` · `bundle-offline.sh`(`--target`) · `load-offline.sh` · `build-sensor-dist.sh`
- `docs/manual/` — INSTALL · SENSOR · OPERATIONS · TLS / `docs/design/` — 03-license(설계만) · 04-port-autoscan · 05-notifications · 06-rescan-and-replacement
