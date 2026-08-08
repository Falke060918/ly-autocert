# STATE — 마지막 갱신: 2026-08-08

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**Phase 1(인증서 인벤토리) 완료에 이어 Phase 3a(센서=원격 수집기) 구현 완료·push 완료(ae80df8..c914613, 27커밋, main=origin/main).**
- 마이그레이션: 0009(sensors·sensor_ca) / 0010(scan_jobs 리스 확장·targets.executor_scope·scan_job_endpoints 멱등 아비터).
- 센서 mTLS 게이트웨이 8443(`internal/sensorgw`): enroll·renew·lease·start·heartbeat·observations·complete + 리스 만료 reaper 30s.
- 관리자 센서 API 4종(`internal/api/sensors.go`) — 생성은 system_administrator 전용·등록 토큰 1회만 노출.
- 센서 바이너리(`cmd/sensor` + `internal/sensor`): enroll 시 CA 지문 핀 검증, run은 pull 루프, 90일 인증서를 잔여 30일에 자동 갱신. linux/windows 정적 빌드 `make sensor-dist`.
- 프론트 `/sensors` 화면 및 스캔 실행기(서버/센서) 선택, `docs/manual/SENSOR.md` 신설.

### 검증 결과
- reviewer 종합 검증 pass — critical/high 0건.
- 백엔드 e2e 실행 확인: 센서 생성 → CA 핀 enroll → 토큰 재사용 401 → 센서 지정 스캔 completed·인벤토리 반영(스코프 `sensor:<uuid>`) → disable 후 즉시 401 → 감사 4종(sensor.token.issue/enroll/renew/disable).
- 보안 하드 규칙 6종·00-spec 7.8 정합 확인. 전 패키지 `go test -p 1 ./...` 및 frontend build 통과.
- 지적된 medium 2건·low 4건 전부 수정 반영(c958660·d9bff11·c914613).
- 서버 zero-config 유지: `SENSOR_CA_ENC_KEY` 미설정이면 8443 게이트웨이를 아예 띄우지 않음.

## 다음 할 일 (우선순위 순)
1. **사용자 조치 필요** — 센서 변수는 `.env`에 직접 추가한다(도구 권한상 에이전트가 `.env*` 편집 불가). **`SENSOR_CA_ENC_KEY`와 `SENSOR_ADVERTISED_HOSTS`는 세트다 — 하나만 넣으면 안 된다.**
   - **센서를 안 쓸 거면 둘 다 넣지 않는다**(zero-config 정상 동작). `SENSOR_CA_ENC_KEY`만 넣고 `SENSOR_ADVERTISED_HOSTS`를 빈 값으로 두면 게이트웨이만 안 뜨는 게 아니라 **백엔드 프로세스 전체가 exit(1)** 한다(`sensorca: at least one SAN (dns name or ip) is required`) — 8080 API도 안 뜨고 컨테이너가 crash-loop. 에러 문구에 변수명이 없어 DB·포트 문제로 오진하기 쉽다.
   - 센서를 쓸 거면 3종을 함께 넣는다: `SENSOR_CA_ENC_KEY=<openssl rand -hex 32 출력>` / `SENSOR_ADVERTISED_HOSTS=<센서가 실제로 접속할 주소, 예: 10.0.0.5,autocert.internal — 로컬 테스트면 127.0.0.1,localhost>` / `SENSOR_LISTEN_ADDR=:8443`.
   - 값 없이 센서를 등록하려 하면 첫 센서 생성에서 500 `sensor_ca_unconfigured`.
2. **센서 e2e 결함 5건 수정 진행 중**(별도 워크트리, 코드) — 머지·재검증 후 land 필요: ① 위 설정 조합에서의 프로세스 crash ② 하트비트 진행률이 서버에 반영되지 않음 ③ 프론트가 pending/leased를 구분 표시하지 않음 ④ 서버 재시작 시 서버 실행 job 미회수 ⑤ 스캔 생성 응답의 `total_endpoints` 불일치.
3. **사용자 관리 API + 화면 구현(Phase 1 잔여, 최우선)** — 로드맵 배정 결정 반영 완료(00-spec 14.11·15장, 01-plan). 최소 범위: `PATCH /users/{id}`(role·display_name), `POST /users/{id}/disable`·`/enable`, `POST /users/{id}/password`(관리자 재설정 시 해당 사용자 세션 즉시 폐기), 프론트 `/users` 화면 1개. 전부 system_administrator 게이트 + 감사 이벤트 동일 트랜잭션. 배경: `users.is_active`(0004)를 바꿀 API가 없어 퇴사자 차단이 DB 직접 UPDATE뿐 → 하드 규칙 5 위반.
4. Phase 2(CA 연결과 발급) 착수 — 3a와 독립. Phase 2에 함께 배정된 항목: Prometheus 메트릭 최소셋(`/metrics`), 오프라인 이미지 번들·체크섬 반입 절차(Phase 3b 이전 완료·Helm보다 선행).
5. 잔여 이식성·운영 갭: 내부 통신 평문(`sslmode=disable`·HTTP, 01-plan 비기능 요구와 불일치) / 버전 식별 수단 없음(CHANGELOG 스텁·이미지 태깅 정책 부재).
6. 잔여 low: enroll 엔드포인트 레이트리밋 없음(폐쇄망 전제라 위험 낮음) / 목록 "초기화" 시 검색 인풋 잔류 / HTTPS 배포 시 세션 쿠키 Secure 플래그 / 감사 정본 문자열 0x1F 경계 / Discovery Job 목록 API·감사 로그 조회 UI 부재.

## 이번 세션 특이사항
- 센서 e2e 검증(8단계: 서버 기동 → 센서 생성·토큰 → enroll → run 폴링 → 센서 스캔 → 인벤토리 반영 → 인증서 갱신 → 비활성화) **전 항목 pass**. 다만 부수 결함 5건이 함께 드러났고(위 "다음 할 일" 2번) 현재 코드 수정이 진행 중이다.
- 설정 함정 확인: `SENSOR_CA_ENC_KEY`만 설정하고 `SENSOR_ADVERTISED_HOSTS`를 비우면 백엔드 전체가 죽는다. 이전 STATE의 `.env` 안내(빈 값 추가)가 이 사고를 유발했고 이번에 정정했다 — 문서 3종(STATE·SENSOR·INSTALL)과 GOTCHAS 반영 완료.
- 로드맵 공백 5건 배정 확정·문서 반영: 사용자 관리=Phase 1 / Prometheus 메트릭·오프라인 번들=Phase 2(번들은 3b 이전, Helm 선행) / OpenTelemetry·Helm·K8s=Phase 4. 11장 관측성 4항목 중 구조화 JSON 로그·Request ID는 이미 구현 완료로 정정.
- 오프라인 라이선스 설계 완료(docs/design/03-license.md) — 상용화 준비 트랙(Phase 2 병렬, 마감=첫 유상 납품 직전) 배정, 구현 착수 보류. 평가판 없음·만료 시 관리자 전용 라이선스 등록 경로만 개방으로 확정.
- git author 자동 추정값(markus@jjuui-MacBookAir.local) 커밋 지속 — git config 미적용.
- 이전 STATE의 "INSTALL.md `.env.example` 경고 문단 이미 삭제됨" 기록은 오류였고 이번 세션에 실제로 삭제(25cc399).
- OPERATIONS.md 문서 갭(CSV 내보내기·상세 체인 카드)은 이번 세션에 해소(4603819).
- 반복 함정은 docs/GOTCHAS.md로 이전됨.

## 주요 파일
- docs/00-spec.md(7.8 센서·15장 3a/3b·16장 제외) / docs/02-goals.md / docs/MAP.md / docs/GOTCHAS.md
- docs/manual/ — INSTALL·OPERATIONS·SENSOR
- backend/internal/ — api·sensorgw·sensor·sensorca·sensorwire·store·discovery·audit·certparse·domain·db
- backend/cmd/ — server·sensor / backend/migrations/(최신 0010)
- frontend/app/·lib/ / docker-compose.yml
