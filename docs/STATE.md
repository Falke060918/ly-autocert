# STATE — 마지막 갱신: 2026-08-07

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md v0.1 / 기획: docs/01-plan.md / 목표: docs/02-goals.md)

## 현재 상태
M1(목업 승인) 통과, 스캐폴딩(이슈 #1) 완료 — 검증 통과, push 완료, 이슈 닫음. main = origin/main 동기.
- 목업을 shadcn zinc 라이트 테마로 전면 재설계 후 사용자 승인 (커밋 6825774).
- 스캔 전략 확정: Phase 1은 에이전트리스 네트워크 TLS 스캔(대상 서버 설치 0, Go crypto/tls). 에이전트(로컬 파일 스캔·배포)는 스펙 7.8 원칙대로 Phase 3에서.
- GitHub 이슈 기반 워크플로우 전환: 라벨 6종(phase-1/backend/frontend/db/security/infra), 이슈 #1~#9 생성(각 본문에 완료 조건 포함). #1 닫힘.
- 스캐폴딩 완료: backend/(Go, GET /healthz, go:embed 마이그레이션 서버 기동 시 자동 적용) + frontend/(Next.js 16 + Tailwind v4 + shadcn/ui, 폰트 npm 패키지 셀프호스팅 — 외부 요청 0건으로 폐쇄망 요건 검증됨) + docker-compose.yml(db/backend/frontend). reviewer가 compose 전체 기동·healthz 200·4라우트 렌더·보안 훑기 통과 확인. 구조·포트·실행법은 docs/MAP.md 참조.

## 다음 할 일 (우선순위 순)
완료 조건은 각 GitHub 이슈 본문에 있다. gh 호출 방법은 docs/MACHINE.md 참조.
1. 이슈 #2 — DB 데이터 모델 + 마이그레이션. Certificate/CertificateLocation/Target/AuditEvent 구현, CA·RenewalJob·DeploymentJob·Policy는 스키마 자리만. 필드는 docs/00-spec.md 12장 기준.
2. 이슈 #3 — 인증서 파서 + 업로드 API (PEM·DER 우선, SHA-256 fingerprint 중복 판정)
3. 이슈 #4 — 인증서 목록·상세 API (검색·정렬·필터·페이지네이션)
4. 이슈 #5 — 네트워크 TLS 스캔(Discovery), 비동기 Job
5. 이슈 #6 — 로그인 + 기본 RBAC (6개 역할)
6. 이슈 #7 — 감사 로그 (분리 저장 + 무결성 검증값)
7. 이슈 #8 — 만료 대시보드 프론트 / 이슈 #9 — 목록·상세·Discovery 프론트

## 이번 세션 특이사항
- `.claude/template-origin` 삭제 완료(사용자가 터미널에서 직접) — SETUP-PENDING 마커 해소.
- gh 활성 계정이 cliphi(저장소 소유자는 Falke060918) — 모든 gh 호출에 `GH_TOKEN=$(gh auth token --user Falke060918)` 접두 필요. 상세는 docs/MACHINE.md.
- 이 머신에 Go 툴체인 없음 — 백엔드 빌드/검증은 Docker 경유(모듈 캐시 볼륨 autocert-gomodcache 존재).
- SETUP.md 잔여: 3번(CHANGELOG 플랫폼 접두어 문구 정리 — 단일 플랫폼이라 제거 대상), 7번.
- git author가 자동 추정값(markus@jjuui-MacBookAir.local)으로 커밋된 이력 있음 — 사용자에게 git config 설정 안내했으나 적용 여부 미확인.

## 주요 파일
- docs/00-spec.md: 명세 v0.1 — 데이터 모델(12장)·API·Phase 구분의 단일 출처
- docs/01-plan.md / docs/02-goals.md: 기획 / 목표·성공 기준·마일스톤
- docs/MAP.md: 코드 구조·포트·실행/검증 명령 캐시 (확인일 2026-08-07)
- backend/ · frontend/ · docker-compose.yml: 스캐폴딩 골격
- mockup/index.html: 승인된 4화면 목업 (shadcn zinc 라이트, JS 0줄)
