# STATE — 마지막 갱신: 2026-08-08

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md v0.1 / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**Phase 1(인증서 인벤토리) 공식 완료(2026-08-08, main=origin/main). 이어서 센서(원격 수집기) 아키텍처를 계획 문서에 반영 완료(2887930..ddc4ae2, 코드 변경 0건).**
- 배경: 고객사 다중 분리망(예: B↔C·C↔D 연결, B↮D)에서 서버 설치 지점이 도달 못 하는 세그먼트 대응 요구 — 관리망(A/B) 설치 요청에도 센서로 대응 가능해야 함(DigiCert ONE 센서 유사).
- 00-spec.md: 7.8장 "센서(원격 수집기)" 하위절(배치 원칙: 아웃바운드 mTLS 1경로·인바운드 불필요 / 1회용 등록 토큰 SHA-256 해시만 저장+CSR+클라이언트 인증서 / pull 리스 상태 머신 pending→leased→running→completed·failed, 폐쇄 enum scan / Observation 멱등 배치 POST). 10장 다이어그램 Sensors 박스. 12장 Sensor 모델 자리+ScanJob 등재+유의점(사설 IP 겹침 → 위치 유일성 (실행기 스코프,ip,port) 확장, 0006 부분 유니크 인덱스 포함). 15장 Phase 3을 3a(센서)/3b(배포 에이전트)로 재구성, "3a는 Phase 2와 독립·선행 착수 가능".
- 01-plan.md: Phase 3 항목 3a/3b 갱신, 제외 목록에 "원격 수집기(센서)" 명시, 스택 표기 Agent/Sensor.
- docs/manual/INSTALL.md: 4.0 "설치 위치 선정 기준" 신설 — ①관리자 브라우저→3000 도달 ②스캔 대상 아웃바운드("가장 많은 대상에 도달하는 곳" 기준) ③도달 불가 세그먼트는 수동 업로드 보완+센서 (예정) 안내.
- docs/MAP.md: scanner.go store/db 비의존 규약(Phase 3a 센서 재사용 전제) 등재.

## 다음 할 일 (우선순위 순)
1. Phase 2(CA 연결과 발급) 또는 Phase 3a(센서) 착수 여부 사용자 결정 대기 — 3a는 Phase 2와 독립·선행 가능(00-spec 15장). 착수 시 /kickoff급 기획이 아니라 해당 장 기반 planner 설계부터.
2. OPERATIONS.md 문서 갭: CSV 내보내기(export 버튼·certificate.export 감사)와 상세 체인 카드가 2.2·2.3·3.1절에 미반영(#8·#9 시점 기준) — 문서만 갱신.
3. 잔여 low 항목(원하면): 목록 "초기화" 시 검색 인풋 잔류 / HTTPS 배포 시 세션 쿠키 Secure 플래그 / 감사 정본 문자열 0x1F 경계(길이 접두) / Discovery Job 목록 API(현재 localStorage 한정)·서버 재시작 시 running Job 회수 / 감사 로그 조회 UI(현재 API만) / 상세 위치 카드가 hostname 대신 IP 표기.

## 이번 세션 특이사항
- git author 자동 추정값(markus@jjuui-MacBookAir.local) 커밋 지속 — git config 미적용.
- INSTALL.md의 ".env.example 인증 변수 미포함" 경고 문단은 이미 삭제된 상태로 확인 — 관련 구항목 정리 완료.
- .env.example 실비밀번호 이력: 자리표시로 교체됨, 실사용 값이면 회전 권고 기전달.
- 감사 삽입 규약·마이그레이션 결번은 GOTCHAS.md로 이전됨.

## 주요 파일
- docs/00-spec.md: 명세 단일 출처(7.8 센서, 15장 3a/3b 로드맵, 16장 제외) / docs/02-goals.md: 성공 기준·마일스톤
- docs/MAP.md: 구조·라우트·검증 명령 캐시 / docs/GOTCHAS.md: 함정 기록
- docs/manual/: 고객용 INSTALL(4.0 설치 위치 기준)·OPERATIONS
- backend/internal/: api·auth·audit·certparse·discovery(scanner는 store 비의존)·store·domain·db / backend/migrations/(최신 0008)
- frontend/app/·lib/·components/ / docker-compose.yml / mockup/index.html
