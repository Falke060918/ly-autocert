# STATE — 마지막 갱신: 2026-08-07

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md v0.1 / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
**Phase 1(인증서 인벤토리) 이슈 #1~#9 전부 완료** — 각각 reviewer 검증 통과 후 main 머지·push, 이슈 닫음. HEAD 1bae528 = origin/main. (핸드오프 커밋 제외 시)
- 백엔드: 마이그레이션 0002/0004/0006/0007(0003·0005 결번), 업로드·목록·상세·summary·Discovery 스캔·로그인/RBAC(6역할)·감사(해시 체인+append-only 트리거+원자 트랜잭션+verify API). API 명세 backend/api/openapi.yaml.
- 프론트: 로그인·대시보드 KPI 8종·목록(검색/필터/정렬/페이지네이션)·상세(위치+tls_version)·Discovery·업로드. 쿠키 세션+동일 출처 프록시(CORS 불요), 외부 요청 0건(폐쇄망 요건 실증).
- 문서: docs/manual/ INSTALL·OPERATIONS(감사 head_hash 외부 기록 권고 포함 — #8·#9 UI 반영 갱신은 미완).
- Phase 3 설계 레퍼런스(DigiCert ONE·Venafi·Keyfactor·Sectigo 리서치): 아웃바운드 전용 pull 워커, agent/sensor 2계층, sensor 프록시 겸직, 표준 프로토콜(ACME/SCEP/EST) 중계 배치, 공인 CA 커넥터 격리(에어갭=사설 CA+수동 반입).

## 다음 할 일 (우선순위 순)
1. Phase 1 마무리 판단: 02-goals.md 성공 기준 대조 후 사용자와 M2/출시 판정. 필요 시 docs/manual을 #8·#9 UI 기준으로 갱신(현재 API 중심 서술).
2. 잔여 low 항목(원하면): 목록 "초기화" 시 검색 인풋 잔류 / HTTPS 배포 시 세션 쿠키 Secure 플래그 경로 / openapi status·type 다중값 표기 / 감사 정본 문자열 0x1F 경계(길이 접두 개선) / Discovery Job 목록 API(현재 localStorage 한정) / 서버 재시작 시 running Job 회수.
3. Phase 2 착수는 사용자 지시 대기 — docs/00-spec.md 16장 제외 목록 준수(Phase 1 완성 전 구현 금지 규칙은 완성으로 해제됨, 단 범위 확장은 사용자 승인).

## 이번 세션 특이사항
- .env.example에 BOOTSTRAP_ADMIN_USERNAME/BOOTSTRAP_ADMIN_PASSWORD(12자+)/SESSION_TTL 추가 필요 — .env* 권한 차단으로 에이전트 편집 불가, **사용자 수동 조치 대기. 로그인 실사용에 필수.**
- git author 자동 추정값(markus@jjuui-MacBookAir.local) 커밋 지속 — git config 미적용.
- 감사 삽입은 "트랜잭션 마지막" 암묵 규약(전역 advisory lock이 tx 끝까지 유지) — 새 감사 경로 작성 시 준수.
- 마이그레이션 결번: 0003·0005.

## 주요 파일
- docs/00-spec.md: 명세 v0.1 단일 출처 / docs/02-goals.md: 성공 기준·마일스톤
- docs/MAP.md: 구조·라우트·검증 명령 캐시 / docs/GOTCHAS.md: 함정 기록
- docs/manual/: 고객용 INSTALL·OPERATIONS
- backend/internal/: api·auth·audit·certparse·discovery·store·domain·db / backend/migrations/
- frontend/app/·lib/·components/ / docker-compose.yml / mockup/index.html
