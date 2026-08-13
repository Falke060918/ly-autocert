# STATE — 마지막 갱신: 2026-08-14

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**로드맵 확정 세션**이 끝났다(커밋 `7d63b41`, push 완료). 사용자가 `docs/TLS_인증서_관리_신규사업_AI_context.md`(사업기획 v2.1)를 추가했고, 이를 기존 spec·plan과 대조 분석한 뒤 **Phase 체계를 사업기획 체계로 확정**해 정본 문서에 반영했다. 코드 변경은 없음 — 문서만.

### 확정된 새 Phase 체계 (00-spec 15장이 정본, 구→신 매핑 표 있음)
- **P1** Discovery/Inventory/Alert — 센서는 후행이 아니라 P1의 전제 아키텍처(구현 완료). 현 범위는 여전히 P1만.
- **P1.5** (신설, 전체 채택) — 소유자 지정·ack/snooze·에스컬레이션·정책 위반 점수·리포트·CMDB/ITSM/K8s 커넥터·WebtoB 설정 파일 read-only 파서·Origin/Edge(내외부) 인증서 이원 관리 설계.
- **P2** 발급+갱신 오케스트레이션 — 중앙 ACME-compatible RA/게이트웨이 골격(구 P4 자동갱신 흡수). **사용자 지시 순서**: WebtoB·WAF에도 certbot류 표준 클라이언트 사용을 먼저 개발·검증 시도 → 어려우면 하이브리드(불가 장비는 배포 adapter) → 최종 확정은 P2 착수 시 재결정.
- **P3** 배포 자동화/Multi-CA — capability matrix 원칙, "WebtoB 자동 교체는 MVP 약속 아님". OTel·Helm 여기로.
- 구 P5(AI)·상용화(라이선스) 트랙은 별도 트랙으로 유지.

### 보류 결정 (재론 시점 명기됨)
**SNI 기반 스캔 + 엔드포인트 식별자(IP+Port+Protocol+SNI) 스키마 변경은 보류** — 재론 시점은 **P2 스키마 설계 직전, 멀티테넌시 컬럼·Origin/Edge 구현과 일괄**. 역방향 불가급 결정(현 스캐너는 SNI 미전송: `discovery/scanner.go:217` 주석). Origin/Edge도 로드맵 배치만 확정, 스키마는 같은 시점에 묶음.

### 버전
`VERSION`·compose 태그·CHANGELOG·`frontend/package.json`(+lock) 모두 **1.1.0**. 이후 변경분은 `## Unreleased`. 최신 마이그레이션 **0019**.

## 다음 할 일 (우선순위 순)
1. **디스커버리 화면 재구성** — 2카드 탭 해체(`sweep-panel.tsx` 생성 폼 제거 · `rescan-card.tsx` 삭제→설정 화면으로 이전), 단일 폼 + `GET /discovery/activity` 기반 통합 목록. 클라이언트 추정기(`countSpecPorts`·`estimateSweep`·`MAX_CIDR_HOST_BITS=20`) 삭제 — 파서는 서버 한 곳. **함께 처리 3건**: ① 승격된 요청은 `job_id`가 없는데 `lib/sweeps.ts:125`·`lib/api.ts:760`이 필수로 보고 폴링해 추적이 끊긴다 ② activity의 `cancel=true`인 schedule_run 취소 버튼 배선 ③ 승격 시 `sweep_id`로 폴링 전환.
2. **설정 화면 `/settings/scans`** + **대시보드 스캔 카드 재구성**(3종 정기 스캔의 다음 주기·마지막 체크·[지금 실행]). 새 카드 대신 기존 `scan-status-card.tsx` 내부를 바꾼다. **선행**: preset 스케줄 생성·삭제 라우트 부재 — `POST`/`DELETE /discovery/schedules[/{id}]` 신설 필요(store 함수는 있음).
3. **잔여 정리** — 상태색 하드코딩(`cert-badges.tsx`·`freshness.tsx`, 6곳은 필수입력 `*` 규약 선행) / main gofmt 미준수 7파일 / 좁은 화면 사이드바 접기(탑바 좌측은 테넌트 탭 예약) / dangling docker volume 5개(`docker volume prune`).
4. **P1.5 착수 준비** — P1 잔여(1~3번)가 끝나면 P1.5 항목(소유자·ack·에스컬레이션·커넥터·WebtoB 파서·Origin/Edge 설계) 분해를 planner에 위임. WebtoB 파서는 `http.m` 등 설정 포맷 조사 선행.
5. **P2 스키마 설계 직전 일괄 재론 묶음** — ① 멀티테넌시 컬럼(옵션 B: 스키마 컬럼+쿼리 필터 강제 계층, 역방향 불가) ② SNI 엔드포인트 식별자 ③ Origin/Edge 스키마. 착수 전 planner에 비용 산정 위임.
6. **TLS 후속**(사용자 "나중에") — 목록 TLS 열·필터·정렬 / cipher suite 수집 / 서버 지원 버전 탐지(레이트 리미터·스윕 ETA 충돌 검토 선행). cipher·지원버전은 소급 불가.
7. **P2(발급+갱신)** 본체 + 동반: Prometheus 메트릭 최소셋, ACME 게이트웨이 설계(certbot-on-WebtoB 검증 포함), Helm 선행 항목.
8. **센서 enroll 토큰이 프로세스 인자에 노출** — 근본 해결은 센서 CLI의 stdin/파일 토큰 입력. 1회용·24시간이라 치명도 낮음.
9. **오프라인 라이선스** — 설계 완료(docs/design/03-license.md), 미해결 2건: 에디션 체계·기본 한도 / 서명키 보관처·kid 회전 주기. `backend/cmd/licensegen` 미작성.
10. **잔여 갭** — 실제 docker compose 기동 검증 미수행(첫 실환경 설치에서) / `.env.example` 신규 키 추가는 사용자 수작업 / Windows 메인 서버 미지원(센서만 지원) / DB `sslmode=disable` / Windows 센서 개인키 NTFS ACL 미보호 / 매뉴얼 3종 헤더의 "Phase 3a" 은퇴 번호("센서 구현 완료 기준"으로 고칠 것) / 위험 집계 API 실패 시 필터 Select 숨김 / 감사 로그 조회 UI 부재 / 재스캔·취소 수동 발주 actor 미기록 / `err.Error()` 영문 노출 3곳 / enroll 레이트리밋 없음 / STARTTLS 수집 미지원.

## 사용자 확정 결정 (유지)
- **Phase 번호는 00-spec 15장 새 체계가 정본**(P1→P1.5→P2 발급+갱신→P3 배포). 구 번호 인용 시 매핑 표 참조. 범위 통제 "지금은 P1만"은 유지.
- **ACME**: 중앙 게이트웨이 + 표준 클라이언트 골격. WebtoB·WAF도 certbot류 선검증 → 안 되면 하이브리드 → P2 착수 시 확정.
- **SNI·Origin/Edge·멀티테넌시 스키마는 P2 스키마 설계 직전 일괄 재론** — 개별 선행 착수 금지.
- 접속 방식 HTTP/HTTPS 2지선다('둘 다' 제외). 패치 시 DB 백업 기본 ON, 실패 시 중단.
- 정기 스캔: `full` 기본 비활성+대상 비움(30일), `preset` 복수 등록, '지금 실행'은 주기 리셋 안 함. `registered`는 targets·port_spec CHECK 금지.
- 서버 뷰 1행=호스트+펼침, 미확인은 '추정' 배지. `GET /servers`의 `sort=risk` UI 라벨은 "만료 임박순".
- 위험 판정은 조회 시 계산·미저장(DER 원문 보존으로 소급 가능).
- 탑바 "Phase 1" 자리는 섹션/테넌트 탭 예약. UI 변경은 목업 선확정 후 구현. `dist/docker-ce-offline-el9*`는 `dist/`에 유지.

## 이번 세션 특이사항
- 사업 문서(`docs/TLS_인증서_관리_신규사업_AI_context.md`)는 **원본 보존** — 수정하지 않는다. 로드맵 반영 커밋에서 git 추적만 추가했다.
- 새 문서 분석에서 나온 미채택 아이디어(추후 후보): FQDN 인벤토리 1급 개체화, 정책 위반 점수·scan coverage 리포트, unauthorized CA 정책, external probe, GraphQL. P1.5/P2 분해 때 재검토.
- 펜타 WAPPLES는 공개 문서만으로 API/파일 구조 일반화 금지 — 고객 PoC 확인 항목 7가지가 사업 문서 11장에 있다.
- 이 머신 리스너 바인딩 차단·DB 테스트 우회·gofmt CRLF·`.env` 접근 불가는 GOTCHAS에 등재돼 있다.

## 주요 파일
- `docs/00-spec.md` 15장 — **확정 로드맵 정본**(구→신 매핑 표) / `docs/01-plan.md` — P1 범위·제외 목록 / `docs/TLS_인증서_관리_신규사업_AI_context.md` — 사업기획 v2.1 원본(1257줄)
- `backend/internal/discovery/` — `scanner.go`(SNI 미전송 :217) · `schedules.go` · `rescan.go` · `ports.go`
- `backend/internal/api/` — `schedules.go` · `discovery.go`(`planScan` 승격·preview) · `discovery_activity.go` · `discovery_defaults.go`
- `backend/internal/store/` — `scan_schedules.go` · `discovery_activity.go` · `sweeps.go` · `certificates*.go` · `servers.go`
- `backend/internal/certrisk/` · `backend/internal/certparse/` · `backend/migrations/`(최신 0019) · `backend/api/openapi.yaml`
- `frontend/app/(app)/` — `page.tsx` · `certificates/` · `discovery/` / `frontend/lib/` — `sweeps.ts` · `scans.ts` · `api.ts` · `servers.ts` · `risks.ts`
- `scripts/` — `install.sh` · `bundle-offline.sh` · `install-sensor.sh`/`.ps1` / `docs/manual/`(INSTALL·SENSOR·OPERATIONS·TLS) / `docs/MAP.md` · `docs/GOTCHAS.md` · `CHANGELOG.md`
