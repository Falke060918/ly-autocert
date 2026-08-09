# STATE — 마지막 갱신: 2026-08-09

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**포트 자동 탐지 스캔 + 만료 알림 4채널 구현·e2e 검증 완료.** 사용자 결정으로 Phase 2(CA 연결·자동발급)를 뒤로 미루고 이 둘을 먼저 끝냈다(만료 알림은 원래 Phase 4 항목이나 지시로 앞당김). Phase 2는 취소가 아니라 순서 변경이다.

### 포트 자동 탐지 스캔 (설계 docs/design/04-port-autoscan.md, 마이그레이션 0013)
- 포트 문법 `ParsePortSpec` — 단일(`443`)·범위(`8000-9000`)·프리셋명 혼용. 프리셋 3종: `web` 6개 / `common` 26개(**기본값**, 기존 `[443]`에서 변경) / `all` 65535개(스윕 전용). `GET /discovery/port-presets`가 단일 진실원천이며 프론트는 목록을 하드코딩하지 않는다.
- **STARTTLS 승격 포트(25·587·110·143·389·5432·3306·1433·21·3389)는 프리셋에서 의도적 제외** — 스캐너가 접속 즉시 ClientHello를 보내는 implicit TLS 전용이라 원리상 인증서를 못 얻는다. 회귀 테스트로 고정.
- 전 포트 백그라운드 스윕: 부모 `scan_sweeps` + 자식은 기존 `scan_jobs` 청크 재사용(기본 4096 엔드포인트, **job 상태 enum 무변경**). 웨이브 순서 common→1-1023→1024-49151→49152-65535. 진행률 컬럼 없이 자식 집계로 파생. 부분 유니크 `WHERE status<>'failed'`로 중복 발주 차단 + 실패 seq만 재시도 허용.
- ETA 초과(기본 30일)는 422 + 실제 소요 일수 포함. 레이트 제한 기본 10pps + `InterleaveByHost`(연속 프로브가 같은 IP를 안 때린다 — IPS 오탐 억제, 결정론 필수).
- 프론트: 클라이언트 포트 파싱 삭제(`8443-9443`이 조용히 8443으로 잘리던 버그 해소), 프리셋 칩, 스윕 패널(진행률·ETA·pause/resume/cancel·"센서 대기").

### 만료 알림 4채널 (설계 docs/design/05-notifications.md, 마이그레이션 0012)
- 구조: 스케줄러 탐지 → `notification_events`(dedup_key 멱등) → `notification_deliveries` 아웃박스 → 채널 4종(SMTP·웹훅·알림함·Syslog). 스케줄러 중복은 `pg_try_advisory_xact_lock`, 디스패처는 `FOR UPDATE SKIP LOCKED`.
- 재시도 1m·2m·4m·8m, 5회째 `failed`. 디스패처 15s×20건 = 분당 ~80건이 곧 자연 throttle(별도 rate 변수 없음).
- Syslog는 RFC5424 직접 포맷(`log/syslog`는 동결·RFC3164·Windows 미지원이라 미사용), TCP는 octet-counting.
- 자격증명은 DB 미저장·환경변수 전용. `GET /notification-channels`는 `{channel,enabled,reason}` 3필드뿐이라 설정값이 실릴 자리가 타입에 없다.
- 감사 생략 2건은 **승인된 결정**: ① 알림함 읽음·보관(본인 개인 상태) ② 발송 성공/실패(감사 체인이 advisory lock으로 직렬화돼 대량 발송이 로그인·업로드를 지연시킨다 — 이력은 `notification_deliveries`가 보유).
- 프론트: `/notifications` 알림함 + 상단바 벨 배지 + `/settings/notifications`(정책·채널상태·테스트발송·발송이력).

### e2e 검증 결과 (커밋 4edba0f 기준)
- 스윕 10항목·회귀 안전 전항목·알림 9항목 pass. 비표준 포트(5671/9443/12345) 인증서 실제 수집 확인, 레이트 실측 2.00pps, 라운드로빈 같은 IP 연속 0회, 재시작 고아 회수 후 동일 seq 재발주, 감사 해시체인 valid, PEM·개인키 유출 0건.
- **high 2건 발견 후 전건 수정**
  1. 소급 발송 — D-3 인증서를 새로 발견하면 90/60/30/7이 동시에 터져 인증서 1건에 알림 4건(메일 8통). 스윕이 근접 만료 200건을 발견하면 메일 800통이 될 뻔했다. `CROSS JOIN LATERAL min(도달 임계값)`으로 **1건만 발화**하도록 수정. D-7 알림 제목이 D-6이던 어긋남도 함께 해소.
  2. `users.email`을 넣을 방법이 제품에 없었다 — 컬럼·해석 로직은 있는데 API·화면에 입력이 없어 `POST /users`가 값을 조용히 버렸고 owner/role/user 수신자는 메일이 항상 0명이었다. API·화면·openapi에 email 추가 + `DisallowUnknownFields`.
- low 수정: 성공 건 `attempt_count` 0 표시, 보존 삭제가 만료 알림을 되살리던 문제, 빈 포트 토큰(에러로 통일), 400 메시지의 내부 접두사 노출, 채널 에러의 설정값 유출(웹훅 URL엔 토큰이 박히는 경우가 흔하다), 알림함 테스트 발송이 항상 실패로 보이던 문제.

### 앞선 세션에서 완료된 것(요약)
- Phase 1 인벤토리 + Phase 3a 센서 e2e 검증 완료, 사용자 관리 API·화면(퇴사자 차단이 DB 직접 UPDATE뿐이던 하드 규칙 5 위반 해소), 세션 폐기 후 로그인 복귀 결함 수정.
- 오프라인 라이선스는 설계만 확정(docs/design/03-license.md) — 평가판 없음 / 만료 시 관리자만 로그인 + 라이선스 등록 경로만 개방 / 발급 도구 `backend/cmd/licensegen`(제품 이미지 미포함).

## 다음 할 일 (우선순위 순)
1. **Phase 2(CA 연결과 발급) 착수** — 미룬 것이지 취소가 아니다. 동반 배정 2건을 같은 Phase에서 끝낸다: Prometheus 메트릭 최소셋(`/metrics`), 오프라인 이미지 번들·체크섬 반입 절차(Phase 3b 이전, Helm 선행).
2. **오프라인 라이선스 구현** — 설계 완료(docs/design/03-license.md), 착수 전 미해결 결정 2건: 에디션 체계와 기본 한도 수치 / 서명키 보관처와 kid 회전 주기.
3. 잔여 갭: 내부 통신 평문(`sslmode=disable`·HTTP — 01-plan 비기능 요구와 불일치) / 버전 식별 수단 없음(CHANGELOG 스텁·이미지 태깅 정책 부재).
4. 잔여 low
   - `handlers_users.go:48,192`·`handlers_notification_policies.go:485`가 `err.Error()` 원문(영문 내부 문구)을 사용자에게 노출한다 — discovery 쪽은 이번에 정리했으나 이 둘은 남았다.
   - `config.disableChannel`이 채널당 **첫 사유만** 기록 → `SMTP_PORT`와 `SMTP_PASSWORD_FILE`이 둘 다 잘못되면 후자가 안 보인다.
   - `notify.netErrKind` 헬퍼가 `webhook.go`에 있다 — 원래 자리는 공용 `channels.go`.
   - API 계층이 `SendTest` nil을 무조건 `sent`로 표시 → 알림함처럼 "보낼 것이 없어 성공"을 `skipped`로 구분하려면 인터페이스 변경 필요.
   - `store.loadNotifyUsers`가 `is_active` 계정만 읽는다(의도된 동작이나 매뉴얼에 미기재).
   - 서로 다른 thresholds의 두 정책이 같은 인증서를 커버하면 이벤트가 2건이 될 수 있다(기존 동작, 설계 7.3.1에 근거 명시).
   - STARTTLS 포트 인증서 수집 미지원(설계상 확장점만).
   - 기존 잔여: enroll 엔드포인트 레이트리밋 없음(폐쇄망 전제라 위험 낮음) / 목록 "초기화" 시 검색 인풋 잔류 / HTTPS 배포 시 세션 쿠키 Secure 플래그 / 감사 정본 문자열 0x1F 경계 / Discovery Job 목록 API·감사 로그 조회 UI 부재.

## 이번 세션 특이사항
- `.env`·`.env.example`은 권한 정책상 에이전트가 읽지도 쓰지도 못한다. 신규 변수군(`NOTIFY_*`/`SMTP_*`/`WEBHOOK_*`/`SYSLOG_*`/`SCAN_SWEEP_*`)은 compose 패스스루만 배선했고 문서는 INSTALL.md 5.2.1·5.2.2에 있다 — 전부 비어도 정상 동작한다(알림은 알림함만 활성, 스윕은 기본값).
- 이 머신엔 Go 툴체인이 없어 백엔드 검증은 Docker 컨테이너 경유다(docs/GOTCHAS.md·docs/MACHINE.md).
- git author가 자동 추정값(markus@jjuui-MacBookAir.local)으로 계속 커밋되고 있다 — git config 미적용.
- 반복 함정은 docs/GOTCHAS.md 참조(이번 세션 5건 추가).

## 주요 파일
- docs/00-spec.md(15장 Phase 배정·16장 제외) / docs/02-goals.md / docs/MAP.md / docs/GOTCHAS.md
- docs/design/ — 03-license(설계만) / 04-port-autoscan(0013) / 05-notifications(0012)
- docs/manual/ — INSTALL(5.2.1·5.2.2 신규 변수) · OPERATIONS(2.7 만료 알림·2.8 포트/스윕) · SENSOR
- backend/internal/ — api·notify·discovery(ports·limiter·sweep)·store(sweeps·notifications)·auth·audit·sensor*·certparse·domain·db
- backend/cmd/ — server(main·wiring)·sensor / backend/migrations/(최신 0013)
- frontend/app/ — (app)/discovery(sweep-panel)·(app)/notifications·(app)/settings/notifications·(app)/users·(app)/sensors / frontend/lib/(api·sweeps) / docker-compose.yml
