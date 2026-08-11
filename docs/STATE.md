# STATE — 마지막 갱신: 2026-08-11

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**정기 스캔 백엔드 + 위험/서버 프론트 + 설치 매뉴얼** 작업 단위가 끝났다. 직전 단위가 설계·스키마·store까지였다면, 이번에 **API 배선과 화면 절반**이 붙었다. 워크트리 4개를 순차 머지(`881e8ab`→`c5cbb3c`→`7fbe227`→`4242b8c`)했고 프론트는 메인 트리 직접 커밋(`cd05d59`)이다. 범위는 여전히 Phase 1 인벤토리 + Phase 3a 센서이고 Phase 2(CA 연결·발급)는 미착수.

### 이번 세션에 들어간 것
- **정기 스캔 스케줄러** — `discovery/schedules.go` 신설. tick은 **① 마감 → ② 발주** 순서다(실행이 running인 동안 활성 유니크 인덱스가 그 스케줄의 주기를 막으므로 끝난 스윕의 실행을 먼저 닫아야 다음 주기가 열린다). 스윕이 안 붙은 채 15분 넘긴 실행은 failed로 회수 — 안 하면 인덱스가 영구 잠금. 취소는 **스윕 취소 → 실행 닫기** 순서(반대면 두 스윕이 겹친다). '지금 실행'은 주기 실행과 같은 경로를 타고 `next_run_at`을 건드리지 않는다. `rescan.go`에 설정 행 오버라이드(enabled/interval/rate) 추가 — 꺼지면 **발주만** 건너뛰고 은퇴·교체 전이는 유지, 만료 임박 가속 티어는 상수 그대로.
- **스캔 API 4종** — `GET/PATCH /discovery/schedules`, `POST .../{id}/run`, `POST .../runs/{id}/cancel`. PATCH가 `discovery.ParsePortSpec`을 `PortSpecValidator`로 주입한다(통합 테스트가 `443,,8443`→400으로 고정). `WithScheduleController` 미배선이면 `wired:false` + 실행·취소만 503.
- **자동 승격 + 미리보기** — `resolveScanPorts`의 `all` 거부 삭제. `planScan`(미리보기·발주 공용)이 총 엔드포인트 수가 `SCAN_SWEEP_CHUNK_ENDPOINTS`(기본 4096)를 **초과할 때만** 스윕으로 승격. 응답에 `mode`/`promoted`/`sweep_id`/`total_chunks`, `job_id`는 단발일 때만. `POST /discovery/scans/preview`는 아무것도 만들지 않아 감사 없음, 상한 초과는 거부 대신 `too_long`+사유. 포트 배열은 안 싣는다(all이면 6.5만 개 — `port-presets`가 정본).
- **통합 작업 목록** — `GET /discovery/activity`. 세 축(scan_jobs·scan_sweeps·scan_schedule_runs)을 UNION ALL CTE로 묶어 count와 페이지가 공유. 제외 규칙 = `origin='rescan'`·`origin='sweep'`·`schedule_id IS NOT NULL`인 스윕. **`cancel`은 서버가 판정**(scan=pending/running, sweep=queued·running·paused, schedule_run=running).
- **기본값 노출** — `GET /discovery/defaults`(env 반영 유효값 + 기본 포트 사양 + `max_rate_pps`/`max_endpoints`). 프론트 `DEFAULT_SWEEP_*` 상수 삭제 → `getDiscoveryDefaults()`. 못 받으면 미리보기를 비운다. 단발 50pps는 미노출.
- **프론트 3종** — 세그먼트 `[목록|서버|발급 기관]`, 서버 뷰는 1행=호스트 + 펼침 시 `GET /servers/{id}` 지연 로드, 미확인은 '추정' 배지. 인증서 상세에 `배포 N · 서비스 중 M · 교체됨 K`(+판정 불가분 `확인 안 됨`)와 설치 위치 "현재" 열. 위험 배지·필터(`risk=`·`risk_code=`)와 `GET /certificates/risks` 집계 배선. 위험 배지는 토큰만 사용.
- **설치 매뉴얼** — INSTALL 5.1.4를 `--mode patch` 인자 중심 → **"`./install.sh` 실행 후 메뉴에서 [패치] 선택"** 중심으로 재작성(P1~P12 기준 9단계 표 + 패치 전용 옵션 4종). 접속 방식 HTTP/HTTPS 2지선다를 INSTALL 0.2·5.4·TLS.md에 반영. `.env` 키 3종(`AUTOCERT_SCHEME`·`AUTOCERT_PUBLIC_HOST`·`SCAN_SUGGESTED_NETWORKS`) 문서화. OPERATIONS 4.1/5장을 "패치가 백업을 직접 받는다" 기준으로 교체.

### 버전
`VERSION`·compose 기본 태그·CHANGELOG·`frontend/package.json`(+lock) 모두 **1.1.0**(패키징 스크립트가 네 곳 다 검사). 이후 변경분은 `## Unreleased`. 최신 마이그레이션 **0019**(신규 없음).

### 검증
머지 후 `golang:1.25-alpine` + 호스트 포트 미노출 postgres(사용자 정의 네트워크)로 `go build ./...`·`go vet ./...`·`go test -p 1 -count=1 ./...` **전 패키지 통과**. `TestOpenAPISpecMatchesRoutes`·`TestMigrateUpDownUp` 개별 확인. 논리 충돌(activity vs `schedule_id`) 실측: 스케줄이 만든 스윕이 activity에 **sweep 행으로 0회, schedule_run 행으로 정확히 1회** 나온다. 프론트 `typegen`→`tsc`→`lint`→`build` 전부 green(14 라우트). gofmt는 머지로 만진 21개 파일만 대조 — 차이 0. **실제 docker compose 기동 검증은 여전히 불가**(이 머신 리스너 바인딩 차단) — 첫 실환경 설치에서 확인.

## 다음 할 일 (우선순위 순)
1. **디스커버리 화면 재구성** — 2카드 탭 해체(`sweep-panel.tsx` 생성 폼 제거 · `rescan-card.tsx` 삭제→설정 화면으로 이전), 단일 폼 + `GET /discovery/activity` 기반 통합 목록. 클라이언트 추정기(`countSpecPorts`·`estimateSweep`·`MAX_CIDR_HOST_BITS=20`) 삭제 — 파서는 서버 한 곳. **함께 처리해야 하는 것 3건**: ① 승격된 요청은 `job_id`가 없는데 `lib/sweeps.ts:125`·`lib/api.ts:760`이 필수로 보고 폴링해 추적이 끊긴다 ② activity의 `cancel=true`인 schedule_run 취소 버튼 배선 ③ 승격 시 `sweep_id`로 폴링 전환.
2. **설정 화면 `/settings/scans`** + **대시보드 스캔 카드 재구성**(3종 정기 스캔의 다음 주기·마지막 체크·[지금 실행]). 새 카드를 만들지 말고 기존 `scan-status-card.tsx` 내부를 바꾼다. **선행 필요**: preset 스케줄 **생성·삭제 라우트가 없다** — store엔 `CreateScanSchedule`/`DeleteScanSchedule`이 있으나 API 미노출이라 `POST`/`DELETE /discovery/schedules[/{id}]`를 함께 만들어야 한다(preset은 N행 등록 가능).
3. **잔여 정리** — 상태색 하드코딩(`components/cert-badges.tsx`·`components/freshness.tsx`, 그중 6곳은 필수입력 `*` 마커라 규약 선행) / main의 gofmt 미준수 파일 7개 / 좁은 화면 사이드바 접기(탑바 좌측은 테넌트 탭 예약분이라 사용자 결정 선행) / `docker-compose.yml` 1행 주석의 구절차 / 오늘 병렬 검증이 남긴 dangling docker volume 5개(`docker volume prune`).
4. **멀티테넌시 대비**(논의 완료, 결론만) — **옵션 B: 스키마 컬럼 + 쿼리 필터 강제 계층까지만**(UI·전환기는 나중). 단일 테넌트는 멀티의 특수 케이스라 **역방향 불가**. 착수 최적 시점은 **Phase 2 스키마 설계 직전**. 착수 전 planner에 비용 산정 위임(영향 테이블 수·쿼리 지점 수·필터 강제 계층 위치·권한 모델 2층화). 탑바에 비워둔 자리가 이 작업의 UI 자리다.
5. **TLS 후속**(사용자 "나중에") — 목록 TLS 열·필터·정렬 / cipher suite 수집 / 서버 지원 버전 목록 탐지. 마지막 것은 엔드포인트당 재핸드셰이크 N회라 **레이트 리미터·스윕 ETA 충돌 검토 선행**. cipher suite·지원버전은 **소급 불가**(재스캔으로만).
6. **Phase 2(CA 연결과 발급)** + 동반 2건: Prometheus 메트릭 최소셋(`/metrics`), Helm 선행 항목(오프라인 이미지 번들은 완료).
7. **센서 enroll 토큰이 프로세스 인자에 노출** — Go CLI가 `--token` 플래그만 받아 `ps`로 보인다. 설치 스크립트가 `--token-file`/환경변수로 완화했으나 근본 해결은 **센서 CLI의 stdin/파일 토큰 입력**. 1회용·24시간이라 치명도는 낮다.
8. **오프라인 라이선스** — 설계 완료(docs/design/03-license.md), 미해결 결정 2건: 에디션 체계·기본 한도 수치 / 서명키 보관처·kid 회전 주기. `backend/cmd/licensegen`은 아직 없다(설계상 경로).
9. **잔여 갭**
    - **실제 docker 기동 검증 미수행** — 첫 실환경 설치에서 확인.
    - `.env.example`에 `COMPOSE_PROJECT_NAME`·신규 3키 추가는 **사용자 수작업**(권한 정책상 에이전트가 읽지도 쓰지도 못한다). 미설정이어도 `install.sh`가 채운다.
    - **Windows 메인 서버 미지원**(Docker 기반). 센서만 Windows 지원.
    - DB 연결이 `sslmode=disable` — TLS.md 9장에 현황·방향만 있고 구현 없음.
    - rootless Docker 안내 부재 / Docker 그룹이 사실상 root 동등이라는 점 미문서화.
    - Windows 센서 개인키가 NTFS ACL로 보호되지 않는다(`os.Chmod` 무력).
    - 매뉴얼에 UI 변경 반영 없음(캡처 없는 텍스트 매뉴얼이라 치명적이진 않다).
    - 위험 집계 API 실패 시 필터 Select 2종이 숨어, URL에 `risk=`가 있으면 해제 수단이 "초기화" 버튼뿐이다(발급기관 필터 기존 동작과 동일).
10. **잔여 low** — 재스캔·취소 수동 발주의 actor가 감사에 안 실린다(origin으로만 구분) / `handlers_users.go:48,192`·`handlers_notification_policies.go:485`·`handlers_setup.go`가 `err.Error()` 영문 원문 노출 / `config.disableChannel`이 채널당 첫 사유만 기록 / `notify.netErrKind`가 `webhook.go`에 있다(원자리는 `channels.go`) / API가 `SendTest` nil을 무조건 `sent` 표시 / `useTicker` 중복 export / STARTTLS 포트 인증서 수집 미지원 / enroll 레이트리밋 없음 / 목록 "초기화" 시 검색 인풋 잔류 / 감사 정본 문자열 0x1F 경계 / 감사 로그 조회 UI 부재.

## 사용자 확정 결정 (유지)
- 접속 방식은 **HTTP/HTTPS 2지선다**, '둘 다'는 세션 쿠키 Secure 강등이라 제외. 패치 시 **DB 백업 기본 ON, 실패하면 중단**.
- 정기 스캔: `full`은 **기본 비활성 + 대상 비움**(30일), `preset`은 **여러 개 등록 가능**, **'지금 실행'은 주기를 리셋하지 않는다**. `registered`는 targets·port_spec을 CHECK로 금지.
- 서버 뷰는 인증서 화면 세그먼트, 1행=호스트+펼침, 미확인 서버는 최근 발견 인증서로 **추정 표기**. **`GET /servers`의 `sort=risk`는 만료 임박도지 위험 판정이 아니다 — UI 라벨은 "만료 임박순"**.
- 인증서 위험 판정은 **조회 시 계산하고 저장하지 않는다**(마이그레이션 없이 소급 100%). DER 원문을 저장하므로 앞으로의 신규 판정도 소급 재계산 가능.
- 탑바에서 "Phase 1" 자리는 **추후 섹션/테넌트 분리 탭 용도로 비워둔다**. UI 변경은 **목업(단일 HTML)으로 먼저 확정 후** 구현. git author는 자동 추정값 그대로. `dist/docker-ce-offline-el9*`는 버전 무관 재사용물이라 `dist/`에 둔다.

## 이번 세션 특이사항
- 워크트리 4개 순차 머지의 충돌은 전부 **"양쪽 다 살리기"**로 해소했다 — `SweepInput`에 `ScheduleID`(0019)와 `Promoted`(승격)가 **둘 다** 남아야 한다. `openapi.yaml` 충돌이 공유 `400/401/403` 꼬리를 갈라놓아 경로마다 복제했다.
- **정기 스캔 tick의 활성화는 `SCAN_SWEEP_ENABLED`를 그대로 따른다**(전용 env 없음). 스윕 발주자가 꺼져 있으면 청크를 돌 주체가 없기 때문 — 분리하려면 새 변수가 필요하다.
- `go test`에서 `-p 1`을 빼면 api 패키지의 `cleanInventory`가 store 픽스처를 실행 중 삭제해 무관한 테스트가 깨진다(GOTCHAS 기존 항목의 실제 재현 사례).
- `gofmt -w ./internal`을 넓게 돌리면 작업트리 CRLF 때문에 전 `.go`가 LF로 재작성돼 100+ 파일이 바뀐다 — 만진 파일만 `tr -d '\r' | gofmt | cmp`로 대조할 것.
- 리스너 바인딩이 막힌 이 머신에서도 DB 테스트는 돈다 — `-p` 없는 postgres를 사용자 정의 네트워크에 띄우고 golang 컨테이너를 같은 네트워크에 붙인다(검증용 컨테이너·네트워크·볼륨은 매번 정리).
- `.env`·`.env.example`은 권한 정책상 에이전트가 읽지도 쓰지도 못한다 — 신규 변수는 스크립트·문서로만 배선한다.
- `CHANGELOG.md`에 `install.sh` 재설계 항목이 애초에 누락돼 있어 이번에 메꿨다.

## 주요 파일
- `backend/internal/discovery/` — `schedules.go`(tick·마감→발주) · `rescan.go`(설정 행 오버라이드) · `ports.go`
- `backend/internal/api/` — `schedules.go` · `discovery.go`(`planScan` 승격·preview) · `discovery_activity.go` · `discovery_defaults.go` · `server.go`(옵션 7종)
- `backend/internal/store/` — `scan_schedules.go` · `discovery_activity.go` · `sweeps.go`(`SweepInput`: `ScheduleID`+`Promoted`) · `certificates*.go` · `servers.go`
- `backend/internal/certrisk/` · `backend/internal/certparse/` · `backend/migrations/`(최신 0019) · `backend/api/openapi.yaml`
- `frontend/app/(app)/` — `page.tsx` · `certificates/`(page + `servers-view.tsx` + `issuers-view.tsx` + `[id]`) · `discovery/`
- `frontend/lib/` — `servers.ts` · `risks.ts` · `sweeps.ts`(`getDiscoveryDefaults`) · `scans.ts` · `issuers.ts` · `api.ts` / `frontend/components/risk-badges.tsx`
- `scripts/` — `install.sh`(메뉴·패치 P1~P12) · `lib/tui.sh` · `bundle-offline.sh` · `load-offline.sh` · `install-sensor.sh`/`.ps1`
- `docs/manual/`(INSTALL·SENSOR·OPERATIONS·TLS) · `docs/design/` · `docs/mockups/ui-redesign.html` · `docs/MAP.md` · `docs/GOTCHAS.md` · `CHANGELOG.md`
