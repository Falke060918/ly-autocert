# STATE — 마지막 갱신: 2026-08-10

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**v1.0.0 기능은 그대로, 설치·반입 경험이 제품 수준으로 올라갔다.** 직전 상태는 "고객사 반입 가능"이었고 이번 세션은 그 반입을 **스크립트 하나로 끝나는 절차**로 바꾼 것이다. 기능(Phase 1 인벤토리 + Phase 3a 센서)에는 변화가 없다.

### 이번 세션에 들어간 것
- **대화형 설치 마법사** — `scripts/install.sh`(서버) + `scripts/lib/tui.sh`(방향키 TUI 공용). 모드 메뉴(install/https/reconfigure/status), 입력 3단 폴백(raw → 번호 입력 → 제어 터미널 없으면 전부 기본값). `POSTGRES_PASSWORD`·`SENSOR_CA_ENC_KEY` 자동 생성(화면 미출력, `.env` 600), NIC IP 자동 탐지 후 `SENSOR_ADVERTISED_HOSTS` 다중 선택, `ADMIN_IP_TRUSTED_PROXIES` 자동 주입(기동 후 실제 compose subnet으로 보정), 포트 충돌 검사, `/healthz` 120초 폴링. `--yes`·`--check-only`·`--non-interactive`·`--help`.
- **센서 설치 마법사** — `scripts/install-sensor.sh`(Linux) / `install-sensor.ps1`(Windows, **PS 5.1·UTF-8 BOM 필수**). **콘솔의 enroll 명령을 통째로 붙여넣으면 서버주소·토큰·fingerprint를 자동 파싱**한다. 사전 점검 4종(관리자 권한·NTP·바이너리 무결성·게이트웨이 TCP 도달성), systemd 유닛 / 작업 스케줄러 자동 등록, 재실행 메뉴(재등록/서비스만 재설치/상태만/취소). 토큰은 미에코·마스킹.
- **릴리즈 산출물 3종 분리** — 통합 번들 `autocert-offline-<v>.tar` **폐기**. `main_<v>.tar.gz` / `sensor_linux_<v>.tar.gz` / `sensor_windows_<v>.zip`(+각 `.sha256`, `--target all`일 때만 `dist/SHA256SUMS-<v>`). `bundle-offline.sh`에 `--target` 도입(`--skip-sensor`는 deprecated 별칭). **핵심 구조: 패키지 루트 = 설치 루트** — `install.sh`가 `load-offline.sh --no-next-steps` 호출 후 소스 아카이브를 `--strip-components=1`로 설치 루트에 전개(busybox tar 폴백 포함)해 사전 열람 사본이 동일 바이트로 덮인다.
- **버전 정합 검사 6종**(기존 2종에서 확장) — **CHANGELOG는 실제로는 검사되지 않고 있었다**(die 메시지에서 언급만) → `## v<VERSION> (` 헤딩 검사 신규. compose 원시 매칭 수 ≥ 2 신규(서비스 하나에서 변수가 사라져도 통과하던 결함). 패키지 VERSION/MANIFEST·아카이브 파일명 사후 assert.
- **`COMPOSE_PROJECT_NAME` 고정**(기본 `autocert`) — 패키지 루트=설치 루트라 신버전이 새 디렉터리에 깔리는데 compose 프로젝트명이 디렉터리명에서 파생되어 **업그레이드가 빈 DB로 뜨는 사고**를 막는다. `docker compose config`로 재현·검증. 기존 값은 보존하고, 값이 없는데 디렉터리명 기준 볼륨이 있으면 그 이름을 승계한다.
- **센서 버전 스탬프** — `autocert-sensor --version`. 주입 순서 `AUTOCERT_VERSION` → 루트 `VERSION` → `dev`. 서버 전송 페이로드는 무변경.
- **검증 결함 수정** — `load-offline.sh`가 MANIFEST에서 이미지 태그를 **0건 파싱해도 "적재 성공"으로 통과**하던 결함(검증 무력화 경로). 태그 파싱을 `docker load` 이전으로 앞당겨 부분 적재도 원천 차단. 회귀 테스트로 실제 검출 확인.
- **패키지 무결성 게이트** — 센서 설치 스크립트가 패키지 레벨 `SHA256SUMS`를 **바이너리 배치 전에** 전건 대조. 내용 불일치는 `--force`로도 통과하지 못한다.
- **매뉴얼 재작성** — INSTALL/SENSOR 상단에 **빠른 설치**(서버 3줄 / 센서 Linux 2줄·Windows 1줄) 신설, 기존 수동 절차는 정보 손실 없이 부록으로 이동. 업그레이드 절차 신설(5.1.4), 트러블슈팅 신설. **장 번호는 보존**했다 — OPERATIONS·TLS·compose 주석이 INSTALL 절 번호를 직접 참조한다. CHANGELOG에 `## Unreleased` 신설.
- **복붙 실패 버그 2건** — SENSOR 문서가 `sudo` 없이 안내(스크립트는 root가 아니면 사전 점검 실패) / Windows에 `-ExecutionPolicy Bypass` 누락(서명 없는 ps1 차단). `Unblock-File`(MotW) 안내도 추가.

### 버전
`VERSION`·`docker-compose.yml` 기본 태그·`CHANGELOG.md` 세 곳이 **1.0.0**이고, 이제 세 곳 모두 실제로 검사된다(예전엔 CHANGELOG가 검사 대상이 아니었다). Phase 2(CA 연결·발급)와 오프라인 라이선스는 여전히 미포함 — CHANGELOG "알려진 제약"에 명시.

### 검증
설치 마법사·패키징은 실측 검증했다. 스크래치 픽스처로 3종 패키지 실제 생성(레이아웃·tar-bomb 부재·체크섬·zip docker 폴백), 소스 전개 전후 해시 동일 확인, `COMPOSE_PROJECT_NAME` 사고 재현·차단 확인, 센서 무결성 게이트 3케이스. `bash -n`·shellcheck·PowerShell AST 전부 통과. 백엔드는 `golang:1.25-alpine`에서 `go build`·`go vet` 통과(Go 테스트는 DB가 필요해 미실행). **실제 docker 기동 검증은 이 머신에서 불가**(리스너 바인딩 차단) — 고객사 첫 설치에서 확인해야 한다.

## 다음 할 일 (우선순위 순)
1. **발급 기관(CA) 표시·필터·집계** — 이전 세션 끝에 사용자가 다음으로 미룬 작업.
   - 새 컬럼 `certificates.issuer_organization`(마이그레이션 **0016**) + **기존 데이터 소급 백필 가능** — 인증서 원문은 저장하지 않지만 `issuer_dn` 문자열에 `O=`가 들어 있다.
   - 발급기관 필터 + 대시보드 분포 집계 + 목록 표시 개선.
   - 함께 고칠 버그: `frontend/lib/format.ts`의 DN 파싱이 RFC2253 백슬래시 이스케이프를 몰라 CN에 `\,`가 있으면 잘린다.
   - 주의: 백필로 검증이 통과해도 **앞으로 생기는 행의 INSERT 경로**를 같은 커밋에서 반드시 함께 고친다(0015 `scan_jobs.origin` 전례 — GOTCHAS).

   **실사용 피드백 개선 백로그 (2026-08-10, 랩 첫 설치)** — 사용자가 랩 환경 첫 설치 실사용 중 낸 개선 요청 6건.
   - **스캔 대상 IP 대역 입력 개선** — 수기 CIDR 입력이 번거롭다. 스마트한 선택지 필요(예: 서버 NIC/라우팅 테이블 기반 대역 자동 제안, 최근 사용 대역, 서브넷 선택 UI).
   - **디스커버리 화면 구성** — "스캔 시작"과 "전 포트 스윕 시작" 섹션이 상하로 분리돼 스크롤이 필요하고 분리 이유도 불분명. 통합 또는 탭/모드 전환형으로 재구성 검토.
   - **스캔 중복 실행·취소 부재** — 스캔 버튼 연타/더블클릭 시 큐에 중복으로 쌓여 동시에 돈다. 실행 중 중복 발주 방지(디바운스+서버측 중복 가드)와 스캔 취소 기능 필요.
   - **스마트 스캔** — IP·포트를 더 지능적으로 고르는 스캔 옵션(예: 응답 있는 호스트 우선, 흔한 TLS 포트 프리셋, 이전 결과 기반 타겟팅).
   - **UI 전반 개선 + 발급자 표시** — 화면이 복잡하고 색상이 단조롭다. 인증서 발급자를 리프만이 아니라 체인(중간 CA)·루트까지 표시. (위 1번 "발급 기관 표시·필터·집계"와 연계)
   - **체인/루트 관점 탐색** — Common Name에 체인·루트 인증서도 노출되는 것으로 보이는데, 루트/중간 CA를 선택하면 그 아래 연결된 내 인증서(리프) 목록을 볼 수 있는 계층 뷰 필요.
2. **멀티테넌시 대비**(이번 세션에 사용자와 논의 완료, 결론만 기록) — MSP/하이브리드 시장 대응으로 `tenant_id`를 심는 안. **옵션 B: 스키마 컬럼 + 쿼리 필터 강제 계층까지만**(UI·테넌트 전환기는 나중). 단일 테넌트는 멀티테넌트의 특수 케이스라 **역방향은 불가**하다. 착수 최적 시점은 **Phase 2(CA 연결·발급) 스키마 설계 직전** — Phase 2에서 테이블이 늘어난 뒤에 넣으면 두 번 일한다. 착수 전 planner에 비용 산정 위임(영향 테이블 수·쿼리 지점 수·필터 강제 계층 위치·권한 모델 2층화). 관리 페이지(테넌트 전환기·크로스 테넌트 대시보드)는 후속.
3. **TLS 관련 후속**(사용자 "나중에" 지시) — 목록 화면 TLS 열·필터·정렬 / cipher suite 수집 / "서버 지원 버전 목록" 탐지. 마지막 것은 엔드포인트당 버전 고정 재핸드셰이크 N회가 필요해 **레이트 리미터·스윕 ETA와 충돌 검토가 선행**이다. cipher suite·지원버전은 **소급 불가** — 재스캔으로만 채워진다.
4. **Phase 2(CA 연결과 발급)** + 동반 2건: Prometheus 메트릭 최소셋(`/metrics`), Helm 선행 항목(오프라인 이미지 번들은 완료).
5. **센서 enroll 토큰이 프로세스 인자에 노출된다** — Go CLI가 `--token` 플래그만 받아 `ps`로 보인다. 설치 스크립트가 `--token-file`/환경변수로 완화했으나 근본 해결은 **센서 CLI에 stdin/파일 토큰 입력 추가**다. 1회용·24시간 토큰이라 치명도는 낮지만 인증서 제품이므로 정리 대상.
6. **오프라인 라이선스 구현** — 설계 완료(docs/design/03-license.md), 착수 전 미해결 결정 2건: 에디션 체계와 기본 한도 수치 / 서명키 보관처와 kid 회전 주기. `backend/cmd/licensegen`은 아직 존재하지 않는다(설계 문서상의 계획 경로).
7. **잔여 갭**
   - **실제 docker 기동 검증 미수행** — 이 머신에서 리스너 바인딩이 막혀 라이브 헬스체크를 못 했다. 첫 실환경 설치에서 확인할 것.
   - `.env.example`에 `COMPOSE_PROJECT_NAME` 추가는 **사용자 수작업 필요**(권한 정책상 에이전트가 이 파일을 읽지도 쓰지도 못한다). 설정하지 않아도 `install.sh`가 채우므로 동작에는 지장 없다.
   - `docker-compose.yml` 1행 주석이 구절차(`cp .env.example .env && docker compose up --build`)다 — 개발환경 문구인데 고객사 패키지에도 그대로 실린다. 정리 필요.
   - **Windows 메인 서버는 미지원**(사용자 문의로 확인) — Docker 기반이라 OS별 바이너리가 없고 검증 범위 밖이다. 센서만 Windows 지원.
   - DB 연결이 `sslmode=disable`이다 — docs/manual/TLS.md 9장에 현황·방향만 있고 구현은 없다.
   - rootless Docker 환경 안내 부재. Docker 그룹 소속이 사실상 root 동등이라는 점도 문서에 없다.
   - Windows 센서의 개인키가 NTFS ACL로 보호되지 않는다(`os.Chmod`가 Windows에서 무력).
8. **잔여 low**
   - 재스캔 수동 발주의 actor가 감사에 안 실린다 — 사람 발주와 자동 발주가 origin으로만 구분된다.
   - `handlers_users.go:48,192`·`handlers_notification_policies.go:485`·`handlers_setup.go`가 `err.Error()` 영문 원문을 사용자에게 노출한다.
   - `config.disableChannel`이 채널당 **첫 사유만** 기록한다.
   - `notify.netErrKind`가 `webhook.go`에 있다(원자리는 공용 `channels.go`).
   - API 계층이 `SendTest` nil을 무조건 `sent`로 표시한다("보낼 것이 없어 성공"을 `skipped`로 가르려면 인터페이스 변경 필요).
   - `useTicker`가 두 곳에서 중복 export된다.
   - STARTTLS 포트 인증서 수집 미지원(설계상 확장점만) / enroll 엔드포인트 레이트리밋 없음 / 목록 "초기화" 시 검색 인풋 잔류 / 감사 정본 문자열 0x1F 경계 / Discovery Job 목록 API·감사 로그 조회 UI 부재.

## 이번 세션 특이사항
- **접근 IP 제한은 기본 compose에서 브라우저 IP 기준으로 동작하지 않는다** — 브라우저→frontend→backend 중계라 backend가 보는 출발지는 frontend 컨테이너다. `install.sh`가 기동 후 실제 compose subnet을 읽어 `ADMIN_IP_TRUSTED_PROXIES`에 넣지만, 수동 설치는 여전히 직접 넣어야 한다.
- `.env`·`.env.example`은 권한 정책상 에이전트가 읽지도 쓰지도 못한다 — 신규 변수는 스크립트·문서로만 배선했다.
- 이 머신엔 Go 툴체인이 없어 백엔드 검증은 Docker 컨테이너 경유다. 리스너 바인딩도 막혀 있어 라이브 기동 검증이 불가하다(docs/GOTCHAS.md).
- git author는 자동 추정값(markus@jjuui-MacBookAir.local) 그대로 두기로 **사용자가 결정**했다 — 더 이상 지적하지 않는다.
- 워크트리 병렬 작업 중 같은 트리에 에이전트를 중복 투입해 커밋 경계가 섞인 사례가 있었다(GOTCHAS 등재). 이번 세션 워크트리는 전부 머지·정리 완료(남은 브랜치 없음).

## 주요 파일
- `scripts/` — `install.sh`(서버 마법사) · `install-sensor.sh`/`install-sensor.ps1`(센서) · `lib/tui.sh` · `bundle-offline.sh`(`--target`) · `load-offline.sh` · `build-sensor-dist.sh`
- `CHANGELOG.md`(v1.0.0 + `## Unreleased`) / `VERSION` / `docker-compose.yml`·`docker-compose.https.yml` / `deploy/nginx`·`deploy/tls`
- `docs/00-spec.md`(15장 Phase 배정·16장 제외) / `docs/02-goals.md` / `docs/MAP.md` / `docs/GOTCHAS.md`
- `docs/design/` — 03-license(설계만) / 04-port-autoscan(0013) / 05-notifications(0012) / 06-rescan-and-replacement(0015)
- `docs/manual/` — INSTALL(빠른 설치·5.1 반입·5.1.4 업그레이드·5.2 환경변수·5.5 포트) · SENSOR(빠른 설치) · OPERATIONS(2.8~2.10) · TLS
- `backend/internal/` — api·discovery·store·auth·notify·audit·sensor*·certparse·domain·db / `backend/cmd/` — server·sensor / `backend/migrations/`(최신 **0015**)
- `frontend/app/` — (app)/discovery·certificates·notifications·settings/{access,notifications}·users·sensors + setup / `frontend/lib/`
