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
1. **사용자 조치 필요** — `.env.example`에 센서 변수 3종 수동 추가(도구 권한상 에이전트가 편집 불가):
   - `SENSOR_CA_ENC_KEY=` (생성: `openssl rand -hex 32`)
   - `SENSOR_LISTEN_ADDR=:8443`
   - `SENSOR_ADVERTISED_HOSTS=`
   미설정 시 첫 센서 생성에서 500 `sensor_ca_unconfigured`.
2. **사용자 관리 API + 화면 구현(Phase 1 잔여, 최우선)** — 로드맵 배정 결정 반영 완료(00-spec 14.11·15장, 01-plan). 최소 범위: `PATCH /users/{id}`(role·display_name), `POST /users/{id}/disable`·`/enable`, `POST /users/{id}/password`(관리자 재설정 시 해당 사용자 세션 즉시 폐기), 프론트 `/users` 화면 1개. 전부 system_administrator 게이트 + 감사 이벤트 동일 트랜잭션. 배경: `users.is_active`(0004)를 바꿀 API가 없어 퇴사자 차단이 DB 직접 UPDATE뿐 → 하드 규칙 5 위반.
3. Phase 2(CA 연결과 발급) 착수 — 3a와 독립. Phase 2에 함께 배정된 항목: Prometheus 메트릭 최소셋(`/metrics`), 오프라인 이미지 번들·체크섬 반입 절차(Phase 3b 이전 완료·Helm보다 선행).
4. 잔여 이식성·운영 갭: 내부 통신 평문(`sslmode=disable`·HTTP, 01-plan 비기능 요구와 불일치) / 버전 식별 수단 없음(CHANGELOG 스텁·이미지 태깅 정책 부재) / 서버 재시작 시 running Job 미회수.
5. 잔여 low: enroll 엔드포인트 레이트리밋 없음(폐쇄망 전제라 위험 낮음) / 목록 "초기화" 시 검색 인풋 잔류 / HTTPS 배포 시 세션 쿠키 Secure 플래그 / 감사 정본 문자열 0x1F 경계 / Discovery Job 목록 API·감사 로그 조회 UI 부재.

## 이번 세션 특이사항
- 로드맵 공백 5건 배정 확정·문서 반영: 사용자 관리=Phase 1 / Prometheus 메트릭·오프라인 번들=Phase 2(번들은 3b 이전, Helm 선행) / OpenTelemetry·Helm·K8s=Phase 4. 11장 관측성 4항목 중 구조화 JSON 로그·Request ID는 이미 구현 완료로 정정.
- 오프라인 라이선스(MAC 지문 + 유효기간 방식) 설계 진행 중 — Phase 배정 미정. 제외 선언하지 않는다.
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
