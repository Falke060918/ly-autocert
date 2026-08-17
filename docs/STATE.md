# STATE — 마지막 갱신: 2026-08-17

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md / 로드맵: docs/ROADMAP.md)

## 현재 상태
**P1 잔여 정리 + 릴리스 컷 세션**(2026-08-17)이 끝났다. 실사 결과 구 STATE의 "다음 할 일" 1·2번(디스커버리 화면 재구성·설정 화면·스캔 카드)과 3·6번 대부분(상태색·사이드바 접기·TLS 열·cipher·지원 버전 탐지)은 **이미 2026-08-11 머지로 완료**된 상태였다(목록 TLS 정렬은 집계 파생값이라 의도적 기각). 이번 세션 실제 수행:
- **릴리스 컷 1.2.0** — VERSION·docker-compose 태그·frontend/package.json(+lock)·CHANGELOG 4곳 동기. 이후 변경분은 `## Unreleased`.
- **매뉴얼 3종 헤더** — INSTALL·OPERATIONS·SENSOR의 은퇴 번호 "Phase 3a"를 "센서 구현 완료 기준"으로 교체.
- **사용자 노출 영문 오류 5곳 한글화** — handlers_setup·handlers_users의 `err.Error()` 직노출 제거(adminip는 이미 한글이라 제외).
- **센서 enroll 토큰 stdin·`--token-file` 입력** — `resolveEnrollToken`(`--token` → `--token-file <경로|->` → stdin 순, `--token`은 ps 노출이라 호환용) + 설치 스크립트 2종(`install-sensor.sh`/`.ps1`)을 `--token-file -`+stdin 파이프로 전환. **enroll 레이트리밋** — 비인증 enroll 경로 IP당 1rps·burst 5, 초과 429 (`sensorgw/middleware.go`).
- **P1.5 분해안 수령**(planner 산출 — 아래 절. 구현은 사용자 승인 대기).

검증: docker `golang:1.25-alpine`+일회용 postgres로 `go build ./...`·`go vet ./...`·변경 패키지(api·sensorgw·cmd/sensor) 테스트 전부 통과. frontend는 이번 머지에 코드 변경 없음(package.json 버전만).

**UI 점검 세션 추가분(2026-08-17)** — 목업(가짜 백엔드, `frontend/mock/server.mjs` untracked)으로 콘솔 실물 확인 후 UI 수정 4건 완료·push(df2d1e4·34b3161·0a47361): 보기 탭 라벨 "도메인별/서버별/기관별", 기관별 CA 트리 가로형(좌 루트→우 말단), 대시보드 만료 임박 폭 확대·행 20건, 3섹션 높이 통일 `lg:h-[30rem]`. 목업 기동법은 GOTCHAS에 등재.

### 버전
`VERSION`·compose 태그·CHANGELOG·`frontend/package.json`(+lock) 모두 **1.2.0**. 최신 마이그레이션 **0022**.

## P1.5 분해안 (planner 산출, 구현은 사용자 승인 대기)
9단위. Wave1 7단위 완전 병렬(U8·U7a·U1·U2·U3·U4·U5) → Wave2 U6·U7b.
- **U1** 소유자 지정 (M)
- **U2** 알림 ack·snooze + 에스컬레이션 (L) — 딜리버리 층 확장으로 설계
- **U3** 정책 위반 점수 + 승인 CA 규칙 (M~L) — certrisk 규칙 추가는 **SQL 재표현(certificates_risk.go) 동반 필수**
- **U4** 기간별 리포트 + scan coverage (M) — 스키마 불요
- **U5** 커넥터 공통 프레임 — declared_installations 사이드카 + CSV import (L)
- **U6** K8s 커넥터 (M, U5 후행) — tls.key 즉시 폐기 명시
- **U7a** WebtoB 파서 순수 패키지 (S) / **U7b** WebtoB 대사 배선 (M, U5+U7a 후행) — 스캐너 SNI 미전송 한계로 기본 인증서 대조 + "SNI 확인 불가" 표시
- **U8** Origin/Edge 설계 문서만 (S) — **마이그레이션 금지**(스키마는 P2 재론 묶음)

채택 권고: coverage 리포트(U4)·unauthorized CA(U3). 보류: FQDN 1급 개체화(P2 재론 묶음 편입)·external probe. 제외 권고: GraphQL.
**착수 전 사용자 결정 1건**: 소유자 후보 디렉토리 API(사용자 목록) 공개 범위 — 현재 system_administrator 전용이라 U1이 열어야 할 폭을 정해야 한다.
WebtoB는 고객 PoC 확인 6건 선행(버전별 설정 방언·include 관행·http.m↔wsconfig 불일치 등 — 사업 문서 11장 참조).

## 다음 할 일 (우선순위 순)
1. **P1.5 구현 착수** — 사용자 승인 + U1 권한 결정 후 Wave1 7단위 병렬 위임.
2. **보류 2건** — ① 오프라인 라이선스 선행 결정 논의(에디션 체계·기본 한도 / 서명키 보관처·kid 회전 주기, 설계는 docs/design/03-license.md 완료·`backend/cmd/licensegen` 미작성) ② dangling docker volume 정리(`docker volume prune`, 도커 기동 시).
3. **웹 콘솔 enroll 명령 예시를 `--token-file` 형으로 전환 검토** (신규 발견 — 센서 등록 다이얼로그의 1회 표시 명령이 아직 `--token` 인자형).
4. **P2 스키마 설계 직전 일괄 재론 묶음** — ① 멀티테넌시 컬럼(옵션 B: 스키마 컬럼+쿼리 필터 강제 계층, 역방향 불가) ② SNI 엔드포인트 식별자(현 스캐너 SNI 미전송: `discovery/scanner.go` 주석) ③ Origin/Edge 스키마. 개별 선행 착수 금지, 착수 전 planner에 비용 산정 위임.
5. **잔여 갭 유지분** — 실제 docker compose 기동 검증 미수행(첫 실환경 설치에서) / `.env.example` 신규 키 추가는 사용자 수작업 / Windows 메인 서버 미지원(센서만 지원) / DB `sslmode=disable` / Windows 센서 개인키 NTFS ACL 미보호 / STARTTLS 수집 미지원 / 감사 로그 조회 UI 부재 / 재스캔·취소 수동 발주 actor 미기록 / 위험 집계 API 실패 시 필터 Select 숨김. (enroll 레이트리밋·`err.Error()` 영문 노출·매뉴얼 헤더는 이번 세션에 해결 — 목록에서 제거.)
6. **P2(발급+갱신)** 본체 + 동반: Prometheus 메트릭 최소셋, ACME 게이트웨이 설계(certbot-on-WebtoB 검증 포함), Helm 선행 항목.

## 사용자 확정 결정 (유지)
- **Phase 번호는 00-spec 15장 새 체계가 정본**(P1→P1.5→P2 발급+갱신→P3 배포, 구→신 매핑 표 있음). 범위 통제 "지금은 P1만"은 유지 — P1.5 착수는 사용자 승인이 곧 범위 확장 승인.
- **ACME**: 중앙 게이트웨이 + 표준 클라이언트 골격. WebtoB·WAF도 certbot류 선검증 → 안 되면 하이브리드 → P2 착수 시 확정.
- **SNI·Origin/Edge·멀티테넌시 스키마는 P2 스키마 설계 직전 일괄 재론** — 개별 선행 착수 금지.
- 접속 방식 HTTP/HTTPS 2지선다('둘 다' 제외). 패치 시 DB 백업 기본 ON, 실패 시 중단.
- 정기 스캔: `full` 기본 비활성+대상 비움(30일), `preset` 복수 등록, '지금 실행'은 주기 리셋 안 함. `registered`는 targets·port_spec 금지.
- 서버 뷰 1행=호스트+펼침, 미확인은 '추정' 배지. `GET /servers`의 `sort=risk` UI 라벨은 "만료 임박순".
- 위험 판정은 조회 시 계산·미저장(DER 원문 보존으로 소급 가능). cipher·서버 지원 TLS 버전은 소급 불가 — 재스캔이 유일한 경로.
- 탑바 "Phase 1" 자리는 섹션/테넌트 탭 예약. UI 변경은 목업 선확정 후 구현. `dist/docker-ce-offline-el9*`는 `dist/`에 유지.
- 목록 TLS 열 정렬은 기각(집계 파생값 — 정렬 화이트리스트 밖).

## 이번 세션 특이사항
- 릴리스 커밋 author가 `Markus <…local>`로 잘못 찍혀 머지 전 `--reset-author`로 저장소 컨벤션 계정(Falke060918)으로 정정했다 — 로컬 git 계정 설정이 워크트리 생성 환경에 따라 갈릴 수 있으니 릴리스류 커밋은 author 확인.
- 사업 문서(`docs/TLS_인증서_관리_신규사업_AI_context.md`)는 **원본 보존** — 수정 금지.
- 펜타 WAPPLES는 공개 문서만으로 API/파일 구조 일반화 금지 — 고객 PoC 확인 항목이 사업 문서 11장에 있다.
- 이 머신 리스너 바인딩 차단·DB 테스트 우회·gofmt CRLF·`.env` 접근 불가는 GOTCHAS에 등재돼 있다.

## 주요 파일
- `docs/00-spec.md` 15장 — 확정 로드맵 정본 / `docs/01-plan.md` — P1 범위·제외 / `docs/ROADMAP.md` / `docs/TLS_인증서_관리_신규사업_AI_context.md` — 사업기획 v2.1 원본
- `backend/cmd/sensor/main.go` — `resolveEnrollToken`(토큰 3경로) / `backend/internal/sensorgw/` — `middleware.go`(enroll 레이트리밋)·`sensorgw.go`
- `backend/internal/api/` — `handlers_setup.go`·`handlers_users.go`(한글화) · `schedules.go` · `discovery.go`(`planScan` 승격·preview) · `discovery_activity.go`
- `backend/internal/discovery/` — `scanner.go`(SNI 미전송) · `schedules.go` · `rescan.go` · `ports.go`
- `backend/internal/store/` — `scan_schedules.go` · `discovery_activity.go` · `sweeps.go` · `certificates*.go` · `servers.go`
- `backend/internal/certrisk/` · `backend/internal/certparse/` · `backend/migrations/`(최신 0022) · `backend/api/openapi.yaml`
- `frontend/app/(app)/` — `page.tsx` · `certificates/` · `discovery/` · `settings/scans/` / `frontend/lib/` — `activity.ts` · `scans.ts` · `api.ts` · `servers.ts` · `risks.ts`
- `scripts/` — `install.sh` · `bundle-offline.sh` · `install-sensor.sh`/`.ps1` / `docs/manual/`(INSTALL·SENSOR·OPERATIONS·TLS) / `docs/design/03-license.md` / `docs/MAP.md` · `docs/GOTCHAS.md` · `CHANGELOG.md`
