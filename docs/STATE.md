# STATE — 마지막 갱신: 2026-08-08

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md v0.1 / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**Phase 1(인증서 인벤토리) 공식 완료 — M1·M2·M3 전부 달성 선언(2026-08-08).** 성공 기준 7개 + M3 항목(OpenAPI·보안 점검·문서) reviewer 실측 통과, main=origin/main.
- 이슈 #1~#9에 더해 M3 보강 4건 머지: 인증서 체인 표시(마이그레이션 0008 SKI/AKI, AKI=SKI 매칭+DN 폴백, 상세 chain 배열+체인 카드) / CSV 내보내기(GET /api/v1/certificates/export, 같은 트랜잭션 감사)+import 감사 actor_id 수정 / 보안 헤더(백엔드 미들웨어+프론트 CSP, HSTS는 HTTPS 전환 시 주석 해제) / OpenAPI 정합(TestOpenAPISpecMatchesRoutes, make openapi-check, GET /api/v1/openapi.yaml 인증 뒤 제공, 라우트 15=명세 15).
- .env.example에 BOOTSTRAP_ADMIN_USERNAME/PASSWORD(자리표시)/SESSION_TTL(기본 8h, Go duration) 추가 완료 — quickstart만으로 로그인까지 동작 실증.
- docs/manual INSTALL·OPERATIONS: 지원 사양(형식·파싱·Discovery 한계·폐쇄망 구조) 장 신설 + 웹 콘솔 운영 절차로 갱신.
- Phase 3 설계 레퍼런스(리서치): 아웃바운드 전용 pull 워커, agent/sensor 2계층, 표준 프로토콜(ACME/SCEP/EST) 중계, 에어갭=사설 CA+수동 반입.

## 다음 할 일 (우선순위 순)
1. Phase 2(CA 연결과 발급) 착수 여부 사용자 결정 대기 — 착수 시 /kickoff 급 기획이 아니라 00-spec 15장 Phase 2 항목(CA 공통 인터페이스, ACME/AD CS/Vault PKI 커넥터, 프로파일, CSR, 발급 이력) 기반 planner 설계부터.
2. 잔여 low 항목(원하면): 목록 "초기화" 시 검색 인풋 잔류 / HTTPS 배포 시 세션 쿠키 Secure 플래그 / 감사 정본 문자열 0x1F 경계(길이 접두) / Discovery Job 목록 API(현재 localStorage 한정)·서버 재시작 시 running Job 회수 / 감사 로그 조회 UI(현재 API만) / 상세 위치 카드가 hostname 대신 IP 표기.
3. INSTALL.md의 ".env.example에 인증 변수 미포함 — 수동 추가" 경고 문단 삭제(이제 포함됨 — 확인 후 정리).

## 이번 세션 특이사항
- .env.example에 사용자가 실비밀번호를 넣었다가 자리표시로 교체한 이력 있음 — 해당 값이 실사용 중이면 회전 권고 전달됨.
- git author 자동 추정값(markus@jjuui-MacBookAir.local) 커밋 지속 — git config 미적용.
- 감사 삽입은 "트랜잭션 마지막" 암묵 규약(전역 advisory lock이 tx 끝까지 유지) — 새 감사 경로 작성 시 준수.
- 마이그레이션 결번: 0003·0005. 최신 0008.

## 주요 파일
- docs/00-spec.md: 명세 v0.1 단일 출처(15장 Phase 로드맵, 16장 제외) / docs/02-goals.md: 성공 기준·마일스톤
- docs/MAP.md: 구조·라우트·검증 명령 캐시 / docs/GOTCHAS.md: 함정 기록
- docs/manual/: 고객용 INSTALL·OPERATIONS
- backend/internal/: api·auth·audit·certparse·discovery·store·domain·db / backend/migrations/(최신 0008) / backend/Makefile(openapi-check)
- frontend/app/·lib/·components/ / docker-compose.yml / mockup/index.html
