# STATE — 마지막 갱신: 2026-08-10

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**v1.0.0 확정 — 고객사 반입이 가능한 상태.** 기능(Phase 1 인벤토리 + Phase 3a 센서)은 이전 세션까지 이미 있었고, 이번 세션의 서사는 그것을 **실제로 남의 서버에 설치해서 운영할 수 있는 제품으로 끌어올린 것**이다. 설치 문턱(필수 환경변수 1개)·접근 통제·HTTPS·오프라인 반입·자동 재확인이 이번에 채워졌다.

### 이번 세션에 들어간 것
- **관리 콘솔 접근 IP 제한**(0014) — 허용 IP/CIDR을 `/settings/access` 화면에서 관리. 미들웨어가 인증보다 앞이라 차단 IP는 로그인도 못 받는다. 잠금 방지 4단(규칙 0건=전체 허용 / 자기 차단은 409 / `ADMIN_IP_ALLOW_BOOTSTRAP` 비상 대역 / 변경 즉시 캐시 무효화). XFF는 `ADMIN_IP_TRUSTED_PROXIES` 대역에서 온 것만 신뢰.
- **초기 관리자 계정을 첫 접속 시 브라우저에서 생성**(`/setup`, 미인증 API 2종) — 필수 환경변수가 `POSTGRES_PASSWORD` 하나로 줄었다. 계정이 1명이라도 생기면 두 라우트가 영구히 닫힌다. 동시 요청은 advisory lock + `INSERT … WHERE NOT EXISTS` 이중 방어.
- **오프라인 반입 번들** — `scripts/bundle-offline.sh`(개발측) / `load-offline.sh`(고객사). 이미지 save·센서 바이너리·git archive·SHA256SUMS·MANIFEST. 버전 3곳(VERSION·compose 기본 태그·CHANGELOG) 정합 검사 내장.
- **HTTPS 리버스 프록시 오버레이**(`docker-compose.https.yml` + `deploy/nginx`·`deploy/tls`) + `SESSION_COOKIE_SECURE`. 근거·절차는 docs/manual/TLS.md.
- **호스트 포트 전면 변수화**, DB 포트 기본 미노출, 포트 변경 절차 INSTALL 5.5.
- **자동 주기 재스캔**(0015) — 기본 12h, 만료 30일 이내 6h·7일 이내 1h. 현황 카드 + 수동 발주. **인증서 교체 감지** — 2회 연속 확인 후 옛 인증서 `replaced` 전이(수동 업로드 인증서는 보호), 목록에 신선도(마지막 확인) 표시.
- **CSV 내보내기를 System·PKI Administrator 2역할로 축소**, 인증서 소유자를 UUID 대신 계정 표시명으로 표시.
- **버그 수정 8건** — 스캐너 TLS 하한(1.0/1.1 장비 인증서 누락) / 만료일 오프바이원 4건(화면 D-day가 메일과 1일 차이·만료 후 24h "임박" 오표시·유효 인증서에 "만료됨" 메일·경과일수 부풀림) / 센서 등록 명령 예시 포트 하드코딩 / `SENSOR_ADVERTISED_HOSTS` 검증 부재 / 스윕 자식 작업 origin 오기록 / XFF 무조건 신뢰.

### 버전
`VERSION`·`docker-compose.yml` 기본 태그·`CHANGELOG.md` 세 곳이 **1.0.0**이다. 어긋나면 `scripts/bundle-offline.sh`가 중단한다(이번에 `--skip-build`로 정합성 검사 통과 확인). **개발 버전 1.0.0**이며 Phase 2(CA 연결·발급)와 오프라인 라이선스는 들어 있지 않다 — CHANGELOG "알려진 제약"에 명시.

### 검증
backend `go build ./...`·`go vet ./...`·`go test -p 1 -count=1 ./...` 전체 pass(Docker `golang:1.25-alpine` + 일회용 postgres). frontend `npm run lint`·`npm run build`·`npm test` pass. 워크트리 9개 전부 머지·정리 완료(남은 브랜치 없음).

## 다음 할 일 (우선순위 순)
1. **발급 기관(CA) 표시·필터·집계** — 사용자가 이번 세션 끝에 다음으로 미룬 작업.
   - 새 컬럼 `certificates.issuer_organization`(마이그레이션 **0016**) + **기존 데이터 소급 백필 가능** — 인증서 원문은 저장하지 않지만 `issuer_dn` 문자열에 `O=`가 들어 있다.
   - 발급기관 필터 + 대시보드 분포 집계 + 목록 표시 개선.
   - 함께 고칠 버그: `frontend/lib/format.ts`의 DN 파싱이 RFC2253 백슬래시 이스케이프를 몰라 CN에 `\,`가 있으면 잘린다.
   - 주의: 백필로 검증이 통과해도 **앞으로 생기는 행의 INSERT 경로**를 같은 커밋에서 반드시 함께 고친다(0015 `scan_jobs.origin` 전례 — GOTCHAS).
2. **TLS 관련 후속**(사용자 "나중에" 지시) — 목록 화면 TLS 열·필터·정렬 / cipher suite 수집 / "서버 지원 버전 목록" 탐지. 마지막 것은 엔드포인트당 버전 고정 재핸드셰이크 N회가 필요해 **레이트 리미터·스윕 ETA와 충돌 검토가 선행**이다. cipher suite·지원버전은 **소급 불가** — 재스캔으로만 채워진다.
3. **Phase 2(CA 연결과 발급)** + 동반 2건: Prometheus 메트릭 최소셋(`/metrics`), Helm 선행 항목(오프라인 이미지 번들은 이번에 완료).
4. **오프라인 라이선스 구현** — 설계 완료(docs/design/03-license.md), 착수 전 미해결 결정 2건: 에디션 체계와 기본 한도 수치 / 서명키 보관처와 kid 회전 주기. **`backend/cmd/licensegen`은 아직 존재하지 않는다**(설계 문서상의 계획 경로 — 이전 STATE의 "발급 도구 존재" 기재는 오기였다).
5. **잔여 갭**
   - DB 연결이 `sslmode=disable`이다 — docs/manual/TLS.md 9장에 현황·방향만 기록돼 있고 구현은 없다.
   - rootless Docker 환경 안내 부재. Docker 그룹 소속이 사실상 root 동등이라는 점도 문서에 없다.
   - Windows 센서의 개인키가 NTFS ACL로 보호되지 않는다(`os.Chmod`가 Windows에서 무력).
6. **잔여 low**
   - 재스캔 수동 발주의 actor가 감사에 안 실린다 — 사람 발주와 자동 발주가 origin으로만 구분된다.
   - `handlers_users.go:48,192`·`handlers_notification_policies.go:485`·`handlers_setup.go`가 `err.Error()` 영문 원문을 사용자에게 노출한다.
   - `config.disableChannel`이 채널당 **첫 사유만** 기록한다.
   - `notify.netErrKind`가 `webhook.go`에 있다(원자리는 공용 `channels.go`).
   - API 계층이 `SendTest` nil을 무조건 `sent`로 표시한다("보낼 것이 없어 성공"을 `skipped`로 가르려면 인터페이스 변경 필요).
   - `useTicker`가 두 곳에서 중복 export된다.
   - STARTTLS 포트 인증서 수집 미지원(설계상 확장점만) / enroll 엔드포인트 레이트리밋 없음 / 목록 "초기화" 시 검색 인풋 잔류 / 감사 정본 문자열 0x1F 경계 / Discovery Job 목록 API·감사 로그 조회 UI 부재.

## 이번 세션 특이사항
- **접근 IP 제한은 기본 compose에서 그대로 쓰면 브라우저 IP 기준으로 동작하지 않는다** — 브라우저→frontend→backend 중계라 backend가 보는 출발지는 frontend 컨테이너다. `ADMIN_IP_TRUSTED_PROXIES`에 compose 네트워크 대역을 넣어야 한다. **고객사 적용 시 반드시 짚을 것.**
- `.env`·`.env.example`은 권한 정책상 에이전트가 읽지도 쓰지도 못한다. 이번 신규 변수(`RESCAN_*`·`ADMIN_IP_*`·`HTTPS_PORT`·`HTTP_REDIRECT_PORT`·호스트 포트 변수군)는 compose 패스스루만 배선했고 문서는 INSTALL 5.2.3·5.2.4·5.5와 TLS.md에 있다 — 전부 비어도 정상 동작한다.
- 이 머신엔 Go 툴체인이 없어 백엔드 검증은 Docker 컨테이너 경유다(docs/GOTCHAS.md).
- git author는 자동 추정값(markus@jjuui-MacBookAir.local) 그대로 두기로 **사용자가 결정**했다 — 더 이상 지적하지 않는다.
- 반복 함정은 docs/GOTCHAS.md 참조(이번 세션 9건 반영, 캡 유지 위해 오래된 9건 정리).

## 주요 파일
- `CHANGELOG.md`(v1.0.0·버전 체계) / `VERSION` / `docker-compose.yml`·`docker-compose.https.yml` / `scripts/bundle-offline.sh`·`load-offline.sh` / `deploy/nginx`·`deploy/tls`
- `docs/00-spec.md`(15장 Phase 배정·16장 제외) / `docs/02-goals.md` / `docs/MAP.md` / `docs/GOTCHAS.md`
- `docs/design/` — 03-license(설계만) / 04-port-autoscan(0013) / 05-notifications(0012) / 06-rescan-and-replacement(0015)
- `docs/manual/` — INSTALL(5.1 반입·5.2 환경변수·5.5 포트) · OPERATIONS(2.8 포트/스윕·2.9 접근제한·2.10 주기 재스캔) · SENSOR · TLS
- `backend/internal/` — api(adminip·rescan·handlers_setup)·discovery(scanner·rescan·sweep·ports·limiter)·store(rescan·replacement·adminip·sweeps·notifications)·auth(setup)·notify·audit·sensor*·certparse·domain·db
- `backend/cmd/` — server(main·wiring)·sensor / `backend/migrations/`(최신 **0015**)
- `frontend/app/` — (app)/discovery·(app)/certificates·(app)/notifications·(app)/settings/{access,notifications}·(app)/users·(app)/sensors·setup / `frontend/lib/`(api·format·rescan·sweeps)
