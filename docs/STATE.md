# STATE — 마지막 갱신: 2026-08-09

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**Phase 3a(센서) e2e 실동작 검증 완료 + 거기서 드러난 결함 전건 수정 완료. 사용자 관리 API·화면 구현으로 Phase 1 잔여 결함도 해소.** (main HEAD 88961ee, 워킹트리 clean)

### Phase 3a 센서 — e2e 검증 pass
- 8단계 전 항목 pass: 센서 등록 → CA 지문 핀 enroll → 등록 토큰 재사용 401 → 센서 지정 스캔 completed → 인벤토리 반영(스코프 `sensor:<uuid>`) → 리스 만료 회수 → disable 즉시 401 → 감사 4종(sensor.token.issue/enroll/renew/disable) 해시체인 유효 → `/sensors` 화면.
- 검증 중 드러난 결함 5건 전부 수정(마이그레이션 **0011** 추가):
  1. 센서 게이트웨이 설정 실패가 코어 API(8080)까지 죽이던 문제 — 이제 게이트웨이만 비활성화(5~6개 SENSOR_* 조합 전부 healthz 200 실증).
  2. 하트비트 진행률이 서버에 반영되지 않고 버려지던 문제.
  3. 러너 관측 배치의 시간 기반 플러시(30s) 추가.
  4. 서버 재시작 시 고아 서버 Job을 `failed`(error=`server restarted`)로 회수.
  5. 스캔 생성 응답의 `total_endpoints` 불일치.
- 백엔드 low 3건 추가 수정: 게이트웨이 실패 경고를 원인별 변수로 분기(SAN 없음→`SENSOR_ADVERTISED_HOSTS` / 키 복호 실패→`SENSOR_CA_ENC_KEY`), ErrLastAdmin 도달 불가 근거 주석 + 서비스 계층 테스트, 하트비트 진행률 total 상한 클램프.
- 스캔 대기상태 UI 구분: pending을 "대기 중" 배지로 분리하고 대기 대상(서버/센서명)·경과 시간 표시, 90초 초과 또는 센서 비활성이면 앰버 경고.

### 사용자 관리 (Phase 1 결함 해소)
- `PATCH /users/{id}`, `POST /users/{id}/disable|enable|password` — 전부 system_administrator 전용, 변경+감사 4종 동일 트랜잭션.
- 비활성화·비밀번호 재설정 시 대상 세션 즉시 폐기. 락아웃 방지 3종(자기 비활성화·자기 역할 변경·마지막 활성 관리자 → 409).
- 프론트 `/users` 화면(목록·생성·수정·활성 전환·비밀번호 재설정), 사이드바 메뉴도 관리자 전용.
- 배경: 이전엔 `users.is_active`를 바꿀 API가 없어 퇴사자 차단이 DB 직접 UPDATE뿐이었고 감사를 우회했다(하드 규칙 5 위반).

### 세션 폐기 후 로그인 복귀 결함(high) 수정
- 서버가 세션을 폐기해도 HttpOnly 쿠키가 남아 로그인 화면에 영영 도달하지 못하던 트랩 해소. `/session/clear` 로컬 라우트 + 401 처리를 apiFetch 계층으로 일원화(소프트 내비게이션에서 리다이렉트가 아예 안 일어나던 더 깊은 절반도 함께 수정) + 로그인 안내 문구.

### 로드맵 공백 5건 배정 확정
- 사용자 관리 = Phase 1(완료) / Prometheus 메트릭 최소셋·오프라인 이미지 번들 = Phase 2(번들은 Helm 선행) / OpenTelemetry·Helm·K8s = Phase 4.
- 11장 관측성 4항목 중 구조화 JSON 로그·Request ID는 **이미 구현 완료**로 정정됨.

### 오프라인 라이선스 — 설계 완료, 구현 착수 보류
- `docs/design/03-license.md`. Ed25519 서명 + 지문(install_id 필수 + 호스트 성분 최소 1개) + 상태머신(unlicensed→active→grace→expired).
- 사용자 결정: 평가판 없음(개발용 라이선스로 대체) / 만료 시 관리자만 로그인 가능하고 라이선스 등록 경로만 개방 / 발급 도구는 `backend/cmd/licensegen`(제품 이미지 미포함).
- 상용화 준비 트랙(Phase 2 병렬), 실질 마감은 첫 유상 납품 직전.

### 매뉴얼 정정
- INSTALL·SENSOR의 "값이 비면 백엔드가 죽는다" 서술 제거(이제 게이트웨이만 비활성). OPERATIONS에 사용자 관리 절 신설, `/discovery` 대기 배지 등재.

## 다음 할 일 (우선순위 순)
1. **Phase 2(CA 연결과 발급) 착수.** 동반 배정 항목 2개를 같은 Phase에서 끝낸다 — Prometheus 메트릭 최소셋(`/metrics`), 오프라인 이미지 번들·체크섬 반입 절차(Phase 3b 이전, Helm 선행).
2. **오프라인 라이선스 구현** — 설계는 끝났고 착수만 보류 중. 착수 전 미해결 결정 2건: 에디션 체계와 기본 한도 수치 / 서명키 보관처와 kid 회전 주기.
3. 잔여 갭: 내부 통신 평문(`sslmode=disable`·HTTP — 01-plan 비기능 요구와 불일치) / 버전 식별 수단 없음(CHANGELOG 스텁·이미지 태깅 정책 부재).
4. 잔여 low: enroll 엔드포인트 레이트리밋 없음(폐쇄망 전제라 위험 낮음) / 목록 "초기화" 시 검색 인풋 잔류 / HTTPS 배포 시 세션 쿠키 Secure 플래그 / 감사 정본 문자열 0x1F 경계 / Discovery Job 목록 API·감사 로그 조회 UI 부재.

## 이번 세션 특이사항
- `.env`·`.env.example`은 권한 정책상 에이전트가 읽지도 쓰지도 못한다. 새 환경변수는 항상 docs/manual/INSTALL.md·SENSOR.md에 적고 사용자가 손으로 넣는다. 센서 변수 3종은 사용자가 이미 `.env`에 반영 완료했고, 이제 값이 비어도 코어 API는 정상 기동하므로 긴급 항목이 아니다.
- git author가 자동 추정값(markus@jjuui-MacBookAir.local)으로 계속 커밋되고 있다 — git config 미적용.
- 이번 세션 미푸시 18커밋을 handoff와 함께 일괄 push했다.
- 반복 함정은 docs/GOTCHAS.md로 이전됨(센서 게이트웨이 항목은 이번 동작 변경에 맞춰 정정).

## 주요 파일
- docs/00-spec.md(7.8 센서·14.11 사용자 관리·15장 Phase 배정·16장 제외) / docs/02-goals.md / docs/MAP.md / docs/GOTCHAS.md
- docs/design/03-license.md — 오프라인 라이선스 설계 확정본(구현 코드 없음)
- docs/manual/ — INSTALL·OPERATIONS·SENSOR
- backend/internal/ — api(handlers_users)·auth(users)·sensorgw·sensor·sensorca·sensorwire·store·discovery·audit·certparse·domain·db
- backend/cmd/ — server·sensor / backend/migrations/(최신 0011)
- frontend/app/ — (app)/users·(app)/sensors·(app)/discovery·session/clear / frontend/lib/ / docker-compose.yml
