# STATE — 마지막 갱신: 2026-08-07

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md v0.1 / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
이슈 #1(스캐폴딩)·#2(DB 모델)·#3(파서+업로드 API)·#6(로그인+RBAC) 완료 — 각각 reviewer 검증 통과, main 머지·push, 이슈 닫음. HEAD f7f19e7 = origin/main.
- #2: 0002 마이그레이션 — 완전 4테이블(certificates/certificate_locations/targets/audit_events) + placeholder 4테이블(ca/policies/renewal_jobs/deployment_jobs).
- #3: PEM·DER 파서(crypto/x509만), POST /api/v1/certificates/import — fingerprint 중복 시 위치만 추가, 개인키 검출 시 400 거부+감사 기록.
- #6: 0004 마이그레이션(users/sessions/departments), bcrypt cost 12, 세션 토큰 SHA-256 해시만 저장, public()/protected(roles...) 라우트 체계(빈 역할 panic), 6역할 RBAC, 감사 5종. import 라우트는 3역할(system_administrator/pki_administrator/operator) protected.
- 고객 매뉴얼 초안: docs/manual/INSTALL.md·OPERATIONS.md — 이슈 완료마다 갱신하는 살아있는 문서(#3·#6 반영 갱신 필요).
- Phase 3 설계 레퍼런스 리서치 완료(DigiCert ONE·Venafi·Keyfactor·Sectigo 공개 문서): 업계 수렴 패턴 = 아웃바운드 전용 pull 워커(인바운드 개방 0), agent/sensor 2계층, sensor 프록시 겸직(구멍 최소화), 표준 프로토콜(ACME/SCEP/EST) 엔드포인트를 중계 지점에 배치, 공인 CA는 커넥터 계층으로 격리(완전 에어갭은 사설 CA+수동 반입).

## 다음 할 일 (우선순위 순)
1. **진행 중(병렬 워크트리)**: 이슈 #4(목록·상세 API — 마이그레이션 0005 예약) / 이슈 #5(네트워크 TLS 스캔 Discovery 비동기 Job — 0006 예약). 완료 시 reviewer 검증 → /land.
2. 이슈 #7 — 감사 로그(분리 저장+무결성 검증값). 함께 처리: user+audit 비원자 트랜잭션(reviewer medium — handlers_users.go·handlers_auth.go, 감사 실패 시 500이지만 INSERT는 잔존), audit_events append-only DB 레벨 차단 여부 결정.
3. 이슈 #8 — 만료 대시보드 프론트 / #9 — 목록·상세·Discovery 프론트.
4. 매뉴얼 갱신: INSTALL/OPERATIONS에 로그인·업로드 실구현 반영.

## 이번 세션 특이사항
- .env.example에 BOOTSTRAP_ADMIN_USERNAME/BOOTSTRAP_ADMIN_PASSWORD/SESSION_TTL 항목 추가 필요 — .env* 권한 차단으로 에이전트 편집 불가, 사용자 수동 조치 대기. 미설정 시 초기 관리자 미생성(경고 로그만).
- #6 low 잔여(기록용): 더미 bcrypt cost 10 vs 실제 12 타이밍 차이 / openapi 일부 400 사유 미기재 / 감사 source_ip가 X-Forwarded-For 무조건 신뢰(폐쇄망 전제 주석 있음).
- #3 low 잔여: internal/api 패키지 단위 테스트 없음(E2E만) / manual-upload Target 동시 생성 레이스(MVP 단일 인스턴스 전제 주석).
- 마이그레이션 번호: 0003 결번(주석 명시), 0005=#4·0006=#5 예약.
- git author 자동 추정값 커밋 이력 — git config 설정 안내했으나 적용 여부 미확인.

## 주요 파일
- docs/00-spec.md: 명세 v0.1 — 데이터 모델(12장)·API·역할(7.11)·Phase 구분 단일 출처
- docs/MAP.md: 구조·포트·실행/검증 명령 캐시 / docs/GOTCHAS.md: 함정 기록
- docs/manual/: 고객용 INSTALL·OPERATIONS
- backend/internal/: api·auth·audit·certparse·store·domain·db / backend/migrations/
- frontend/ · docker-compose.yml · mockup/index.html
