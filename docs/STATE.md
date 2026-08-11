# STATE — 마지막 갱신: 2026-08-11

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**실사용 피드백 2차 — 설치 UX 재설계 + 인증서 위험 판정 + 서버 축** 작업 단위가 끝났다. 직전 단위가 "화면을 제품 수준으로"였다면 이번은 **설치·업그레이드 경험**과 **인증서를 판정하는 축**을 세운 것이다. 기능 범위는 여전히 Phase 1 인벤토리 + Phase 3a 센서이고 Phase 2(CA 연결·발급)는 미착수다. 이번 단위에서 **설계·스키마·store 계층까지는 끝났고 API 배선과 화면이 남은 것**이 여럿이다(아래 "다음 할 일" 1~7).

### 이번 세션에 들어간 것
- **1.1.0 릴리즈 패키징 완료** — 버전 동기화 4곳(`VERSION`·compose 태그 기본값·CHANGELOG `## v<버전> (<날짜>)` 헤딩·`frontend/package.json`+lock 3spot), `bundle-offline.sh` **검사 7종**(1~4·7 사전 / 5·6 사후). `dist/`에 `main_1.1.0.tar.gz`·`sensor_linux_1.1.0.tar.gz`·`sensor_windows_1.1.0.zip` + `SHA256SUMS-1.1.0`. **재패키징으로 sha256이 바뀌었다 — 최종본만 반입할 것.**
- **`install.sh` 재설계** — `./install.sh`만 실행하면 뜨는 메뉴에 **`패치 — 기존 설치 업그레이드`** 신설(구버전 도커 라벨 역추적 → DB 백업(기본 ON, 실패 시 중단) → `.env`·`deploy/tls` 승계 → 구버전 정지(`-v` 없이) → 빈 DB 검증 → 롤백 안내). 접속 방식 **HTTP/HTTPS 2지선다**('둘 다'는 세션 쿠키 Secure 강등이라 제외), 인증서 짝 검증·SAN에서 FQDN 기본값·기동 후 443 확인. `--mode https`는 `reconfigure --scheme https` 별칭으로 유지. 설치 시 NIC 대역을 `SCAN_SUGGESTED_NETWORKS`에 자동 기록. `lib/tui.sh`에 방향키 디렉터리 탐색기 `tui_browse` 추가.
- **인증서 위험 판정 엔진** — `internal/certrisk` 14+1개 항목(weak_key/key_deprecated_2030/weak_signature/no_san/wildcard 3종/long_validity/expired_ca/self_signed/chain_anomaly/legacy_tls/incomplete_chain 등). **조회 시 계산하고 저장하지 않는다** — 마이그레이션 없이 소급 100%. 필터 `?risk=`·`?risk_code=`, 집계 `GET /certificates/risks`. SQL 필터와 Go 판정을 픽스처로 교차 검증한다.
- **서버 축 API** — `GET /servers`·`GET /servers/{id}`, 인증서 상세 `locations[]`에 `current_fingerprint`·`current_certificate_id`. 마이그레이션 0건.
- **마이그레이션 0018 (`certificate_raw_and_chain`)** — `raw_der`·`spki_sha256`·KU/EKU·`is_ca`/`path_len_constraint`·OCSP/CRL/AIA URL + `certificate_locations.presented_chain_len`/`chain_complete`. **소급 백필 불가(재스캔으로만)**. 이제 원문이 있으므로 **앞으로의 신규 판정은 소급 재계산이 가능하다**.
- **마이그레이션 0019 (`scan_schedules`)** — `scan_schedules`(full·registered 각 1행 부분 유니크 / **preset은 N행**) + `scan_schedule_runs`(활성 run 유니크로 중복 실행을 구조적으로 차단) + `scan_sweeps.schedule_id`. `registered`는 targets·port_spec을 CHECK로 금지한다. store 계층(`store/scan_schedules.go`)과 클레임 규약까지 구현됐다.
- **프론트엔드** — 전 9화면 제목 블록 제거(건수·액션은 기존 탭 줄에 흡수), 만료 지평선 244→180px, 사이드바·상단바 sticky 고정(문서 스크롤 유지 + `scroll-padding-top`), 상태색 하드코딩 일부를 토큰으로 교체.

### 사용자 확정 결정
- 접속 방식은 **HTTP/HTTPS 2지선다**, '둘 다'는 넣지 않는다.
- 패치 시 **DB 백업 기본 ON, 실패하면 중단**.
- **인증서 DER 원문을 저장한다.**
- 정기 스캔: `full`은 **기본 비활성 + 대상 비움**(30일), `preset`은 **여러 개 등록 가능**, **'지금 실행'은 주기를 리셋하지 않는다**(원래 일정 유지).
- 서버 뷰는 인증서 화면 세그먼트 `[목록|서버|발급 기관]`, 1행=호스트+펼침, 미확인 서버는 최근 발견 인증서로 **추정 표기**.
- `dist/docker-ce-offline-el9*`는 버전 무관 재사용물이라 `dist/`에 그대로 둔다.
- (이전 결정 유지) 탑바에서 "Phase 1 — 인증서 인벤토리"를 지운 자리는 **추후 섹션/테넌트 분리 탭 용도로 비워둔다**. UI 변경은 **목업(단일 HTML)으로 사용자가 먼저 확정한 뒤** 구현한다. git author는 자동 추정값 그대로 둔다.

### 버전
`VERSION`·compose 기본 태그·CHANGELOG·`frontend/package.json`(+lock) 모두 **1.1.0**이고 네 곳 다 패키징 스크립트가 실제로 검사한다. 이후 변경분은 `## Unreleased`에 쌓인다. 최신 마이그레이션은 **0019**.

### 검증
머지 후 `golang:1.25-alpine` + 호스트 포트를 열지 않은 postgres 컨테이너(같은 docker 네트워크)로 `go build ./...`·`go vet ./...`·`go test -p 1 -count=1 ./...` **전 패키지 통과**. `TestMigrateUpDownUp`이 0018·0019 포함 up/down/up을 돌았고 `TestOpenAPISpecMatchesRoutes`도 통과. 머지로 만진 파일의 gofmt 차이는 **머지 이전부터 있던 것뿐**(main의 미준수 7개 중 `domain.go`·`store/discovery.go`)이라 손대지 않았다. **실제 docker compose 기동 검증은 여전히 불가**(리스너 바인딩 차단) — 고객사/랩 설치에서 확인한다.

## 다음 할 일 (우선순위 순)
1. **정기 스캔 스케줄러 tick + API 4종** — `discovery/schedules.go` 신설, `rescan.go`에 설정 행 오버라이드(만료 임박 가속 티어는 유지), `GET/PATCH /discovery/schedules`·`POST .../run`·`POST .../runs/{id}/cancel`(변경은 감사 로그). **store의 `PortSpecValidator` 주입이 API 배선에서 반드시 채워져야** 포트 문법 검증이 실제로 걸린다(미주입이면 "비었는지"까지만 본다).
2. **자동 승격 + 미리보기** — `api/discovery.go`의 `all` 거부(`resolveScanPorts`) 제거, 총 엔드포인트 수로 단발↔스윕 자동 승격(임계 = `SCAN_SWEEP_CHUNK_ENDPOINTS` 기본 4096), `POST /discovery/scans/preview` 신설. 레이트·청크·ETA 가드는 스윕 경로 그대로 유지.
3. **통합 작업 목록** — `GET /discovery/activity`(scan_jobs + scan_sweeps 부모 + scan_schedule_runs UNION). **취소 가능 여부를 서버가 `cancel` 필드로 판정해 내려준다.** 자동 재스캔 개별 Job은 싣지 않는다(도배 방지).
4. **기본값 노출** — `GET /discovery/defaults`(env 반영 유효값). `frontend/lib/sweeps.ts`의 `DEFAULT_SWEEP_*` 하드코딩 상수 삭제(환경변수를 바꾼 현장에서 placeholder가 거짓말한다). 단발 스캔 50pps는 UI에 아예 없다.
5. **디스커버리 화면 재구성** — 2카드 탭 해체(`sweep-panel.tsx` 생성 폼 제거·`rescan-card.tsx` 삭제→설정 화면으로 이전), 단일 폼 + 통합 목록. 클라이언트 포트/IP 추정기(`countSpecPorts`·`estimateSweep` 등) 삭제 — 파서는 서버 한 곳.
6. **설정 화면 `/settings/scans`** + **대시보드 스캔 카드 재구성**(3종 정기 스캔의 다음 주기·마지막 체크·[지금 실행]). 새 카드를 만들지 말고 기존 `scan-status-card.tsx` 내부를 바꾼다.
7. **프론트 3종** — 서버 뷰(`certificates/servers-view.tsx`·`lib/servers.ts`) / 인증서 상세 "배포 N · 서비스 중 M · 교체됨 K" / 위험 배지·필터. **`GET /servers`의 `sort=risk`는 만료 임박도지 위험 판정이 아니다 — UI 라벨은 "만료 임박순"으로 쓸 것.**
8. **설치 매뉴얼 갱신** — INSTALL 5.1.4 / TLS.md / OPERATIONS 5장 / 패키지 MANIFEST를 `--mode patch`가 아니라 **"`./install.sh` 실행 후 메뉴에서 [패치] 선택"** 중심으로 교체. 새 `.env` 키 `AUTOCERT_SCHEME`·`AUTOCERT_PUBLIC_HOST`·`SCAN_SUGGESTED_NETWORKS` 문서화(**`.env.example` 추가는 사용자 수작업** — 에이전트 권한 밖).
9. **잔여 정리** — 상태색 하드코딩 7곳(그중 6곳은 필수입력 `*` 마커라 규약을 먼저 정해야 한다) / main의 gofmt 미준수 파일 7개 / 좁은 화면 사이드바 접기(탑바 좌측 자리는 테넌트 탭 예약분이라 사용자 결정 선행) / `docker-compose.yml` 1행 주석의 구절차.
10. **멀티테넌시 대비**(사용자와 논의 완료, 결론만 기록) — MSP/하이브리드 대응으로 `tenant_id`를 심는 안. **옵션 B: 스키마 컬럼 + 쿼리 필터 강제 계층까지만**(UI·테넌트 전환기는 나중). 단일 테넌트는 멀티테넌트의 특수 케이스라 **역방향은 불가**하다. 착수 최적 시점은 **Phase 2 스키마 설계 직전** — 테이블이 늘어난 뒤에 넣으면 두 번 일한다. 착수 전 planner에 비용 산정 위임(영향 테이블 수·쿼리 지점 수·필터 강제 계층 위치·권한 모델 2층화). 탑바에 비워둔 공간이 이 작업의 UI 자리다.
11. **TLS 관련 후속**(사용자 "나중에" 지시) — 목록 화면 TLS 열·필터·정렬 / cipher suite 수집 / "서버 지원 버전 목록" 탐지. 마지막 것은 엔드포인트당 버전 고정 재핸드셰이크 N회가 필요해 **레이트 리미터·스윕 ETA와 충돌 검토가 선행**이다. cipher suite·지원버전은 **소급 불가** — 재스캔으로만 채워진다.
12. **Phase 2(CA 연결과 발급)** + 동반 2건: Prometheus 메트릭 최소셋(`/metrics`), Helm 선행 항목(오프라인 이미지 번들은 완료).
13. **센서 enroll 토큰이 프로세스 인자에 노출된다** — Go CLI가 `--token` 플래그만 받아 `ps`로 보인다. 설치 스크립트가 `--token-file`/환경변수로 완화했으나 근본 해결은 **센서 CLI에 stdin/파일 토큰 입력 추가**다. 1회용·24시간 토큰이라 치명도는 낮지만 인증서 제품이므로 정리 대상.
14. **오프라인 라이선스 구현** — 설계 완료(docs/design/03-license.md), 착수 전 미해결 결정 2건: 에디션 체계와 기본 한도 수치 / 서명키 보관처와 kid 회전 주기. `backend/cmd/licensegen`은 아직 존재하지 않는다(설계 문서상의 계획 경로).
15. **잔여 갭**
    - **실제 docker 기동 검증 미수행** — 이 머신에서 리스너 바인딩이 막혀 라이브 헬스체크를 못 했다. 첫 실환경 설치에서 확인할 것.
    - `.env.example`에 `COMPOSE_PROJECT_NAME` 추가는 **사용자 수작업 필요**(권한 정책상 에이전트가 이 파일을 읽지도 쓰지도 못한다). 설정하지 않아도 `install.sh`가 채우므로 동작에는 지장 없다.
    - **Windows 메인 서버는 미지원** — Docker 기반이라 OS별 바이너리가 없고 검증 범위 밖이다. 센서만 Windows 지원.
    - DB 연결이 `sslmode=disable`이다 — docs/manual/TLS.md 9장에 현황·방향만 있고 구현은 없다.
    - rootless Docker 환경 안내 부재. Docker 그룹 소속이 사실상 root 동등이라는 점도 문서에 없다.
    - Windows 센서의 개인키가 NTFS ACL로 보호되지 않는다(`os.Chmod`가 Windows에서 무력).
    - 매뉴얼(INSTALL/OPERATIONS)에 UI 변경 반영이 없다 — 화면 캡처 없는 텍스트 매뉴얼이라 치명적이진 않다.
16. **잔여 low**
    - 재스캔 수동 발주의 actor가 감사에 안 실린다 — 사람 발주와 자동 발주가 origin으로만 구분된다. 스캔 취소도 같은 형태다.
    - `handlers_users.go:48,192`·`handlers_notification_policies.go:485`·`handlers_setup.go`가 `err.Error()` 영문 원문을 사용자에게 노출한다.
    - `config.disableChannel`이 채널당 **첫 사유만** 기록한다.
    - `notify.netErrKind`가 `webhook.go`에 있다(원자리는 공용 `channels.go`).
    - API 계층이 `SendTest` nil을 무조건 `sent`로 표시한다.
    - `useTicker`가 두 곳에서 중복 export된다.
    - STARTTLS 포트 인증서 수집 미지원(설계상 확장점만) / enroll 엔드포인트 레이트리밋 없음 / 목록 "초기화" 시 검색 인풋 잔류 / 감사 정본 문자열 0x1F 경계 / 감사 로그 조회 UI 부재.

## 이번 세션 특이사항
- **워크트리 2개를 순차 머지**했다(0019 스케줄 스키마·store → 0018 DER 원문·체인). 마이그레이션 번호가 갈려 SQL 충돌은 없었고 실제 충돌은 `docs/MAP.md` 1블록뿐이라 **양쪽 항목을 0018→0019 순으로 나란히 살렸다**. `CHANGELOG.md`의 `## Unreleased`는 ort가 자동 병합했다.
- 앱 셸 고정에서 `main`을 스크롤 컨테이너로 만들면 **tsc/lint/build가 전부 green인 채로** 라우팅 상단 복귀·`scrollIntoView`·앵커 이동이 동시에 죽는다(GOTCHAS 등재).
- 컨테이너에서 `gofmt -w ./internal`을 넓게 돌리면 전 `.go`가 LF로 재작성돼 `git status`가 100+ 파일이 된다 — 작업트리 CRLF 때문에 `gofmt -l`이 전부를 나열하는 것도 정상이다(GOTCHAS 등재).
- 체인 완전성을 "루트까지"로 정의하면 루트를 보내지 않는 정상 서버가 전건 danger가 된다 — 리프→CA 연결 여부로 판정한다(SSL Labs 동일, GOTCHAS 등재).
- 리스너 바인딩이 막힌 이 머신에서도 **DB 테스트는 돈다** — `-p` 없는 postgres를 사용자 정의 네트워크에 띄우고 golang 컨테이너를 같은 네트워크에 붙인다(검증용 컨테이너·네트워크는 매번 정리).
- `.env`·`.env.example`은 권한 정책상 에이전트가 읽지도 쓰지도 못한다 — 신규 변수는 스크립트·문서로만 배선한다.

## 주요 파일
- `backend/internal/certrisk/` — `certrisk.go`(카탈로그·Evaluate) · `rules.go`(규칙 구현) / `backend/internal/certparse/` — `certparse.go` · `presented_chain.go`
- `backend/internal/store/` — `scan_schedules.go`(스케줄·run 클레임) · `certificates.go`/`certificates_query.go`/`certificates_risk.go` · `certificates_issuers.go` · `servers.go` · `discovery.go` · `scan_suggestions.go`
- `backend/migrations/` — 최신 **0019**(0018 DER·체인 / 0019 정기 스캔 스케줄) / `backend/api/openapi.yaml`
- `frontend/app/(app)/` — `page.tsx`(대시보드) · `certificates/`(page + `issuers-view.tsx` + `[id]`) · `discovery/`(`target-field.tsx`·`sweep-panel.tsx`·`rescan-card.tsx`·`scan-status-card.tsx`)
- `frontend/lib/` — `issuers.ts` · `scans.ts` · `sweeps.ts` · `format.ts`(RFC2253 `parseDN`) · `api.ts`
- `scripts/` — `install.sh`(메뉴·패치 모드) · `lib/tui.sh`(`tui_browse`) · `bundle-offline.sh` · `load-offline.sh` · `install-sensor.sh`/`.ps1` · `build-sensor-dist.sh`
- `docs/manual/` — INSTALL · SENSOR · OPERATIONS · TLS / `docs/design/` — 03-license · 04-port-autoscan · 05-notifications · 06-rescan-and-replacement
- `docs/mockups/ui-redesign.html`(사용자 확정 목업) / `docs/MAP.md` / `docs/GOTCHAS.md` / `CHANGELOG.md`(`## Unreleased`)
