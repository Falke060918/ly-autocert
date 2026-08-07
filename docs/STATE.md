# STATE — 마지막 갱신: 2026-08-07

## 프로젝트
INHERIT_AutoCert — 폐쇄망과 레거시 시스템을 운영하는 기업을 위한, 설치가 쉽고 가벼운 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼.
원본 명세: `docs/00-spec.md` (v0.1) / 기획: `docs/01-plan.md` / 목표: `docs/02-goals.md`. 문서는 한국어.

## 현재 상태
`/kickoff` 1~4단계 완료. **M1(목업 승인) 대기 중**이며 아직 제품 코드는 한 줄도 없다.

- 템플릿(claude-starter) 초기화 수행 — 원본 명세를 `docs/00-spec.md`로 이관, README를 프로젝트용으로 교체, `CLAUDE.md`에 프로젝트 고유 규칙 섹션 추가, LICENSE는 저작권자가 저장소 소유자와 동일해 MIT 유지.
- 기획·목표 문서 작성 완료.
- 목업 `mockup/index.html` 작성 완료 — 4화면(대시보드 / 인증서 목록 / 인증서 상세 / Discovery), 단일 정적 HTML, JS 0줄(라디오+CSS `:has()` 전환), 외부 의존성은 폰트 CDN뿐.

**확정 사항**
- 1차 구현 범위 = **Phase 1(인증서 인벤토리)만**. Phase 2~5(CA 커넥터·발급 / 에이전트·배포·롤백 / 자동 갱신·승인 / AI 지원)는 이번 범위 밖.
- 기술 스택 = `docs/00-spec.md` 11장 고정값: Go + REST/OpenAPI 3 + PostgreSQL + 비동기 작업 큐 / Next.js + React + TypeScript / Docker Compose / OpenTelemetry·Prometheus.

## 다음 할 일 (우선순위 순)
1. **M1 게이트 — 목업 승인.** 사용자가 `mockup/index.html`을 확인하고 승인해야 다음으로 넘어간다. 수정 요청이 오면 목업만 고친다.
   - 완료 조건: 사용자가 4화면을 확인하고 승인 의사를 밝힌다.
2. **스캐폴딩** — Go 백엔드 + PostgreSQL + Next.js 프론트 + Docker Compose 개발환경 골격 생성. 목업의 화면 구조를 참조한다.
   - 완료 조건: `docker compose up`으로 백엔드·DB·프론트가 함께 기동되고 헬스체크 엔드포인트가 200을 반환한다.
3. **DB 데이터 모델 + 마이그레이션** — Certificate, CertificateLocation, Target, AuditEvent 구현. CertificateAuthority / RenewalJob / DeploymentJob / Policy는 스키마 자리만 확보. 필드는 `docs/00-spec.md` 12장 기준.
   - 완료 조건: 마이그레이션이 up/down 모두 성공하고 Migration Test가 통과한다.
4. **인증서 파서 + 업로드 API** — PEM·DER 우선(CRT/CER/P7B/PFX는 확장 가능한 구조로 설계). SHA-256 fingerprint로 중복 판정.
   - 완료 조건: 동일 인증서를 두 번 업로드해도 Certificate 레코드는 1개이고 CertificateLocation만 늘어난다.
5. **인증서 목록·상세 API** — 검색·정렬·필터·페이지네이션.
   - 완료 조건: 인증서 10,000건에서 목록 API가 2초 이내 응답한다.
6. **네트워크 TLS 스캔(Discovery)** — IP/CIDR/포트/타임아웃/동시 연결 수 입력, 비동기 Job 실행.
   - 완료 조건: CIDR을 입력하면 스캔 Job이 생성되고 도달 가능한 TLS 엔드포인트의 인증서가 인벤토리에 저장된다.
7. **로그인 + 기본 RBAC** — 로컬 계정, 비밀번호 정책. 6개 역할(System Administrator / PKI Administrator / Security Administrator / Operator / Auditor / Viewer). MFA는 범위 밖.
   - 완료 조건: 권한 없는 역할이 보호된 API를 호출하면 403이 반환된다.
8. **감사 로그** — 로그인/로그아웃/실패, 인증서 등록, 설정 변경, 데이터 내보내기 기록. 애플리케이션 로그와 분리 저장, 무결성 검증값 포함.
   - 완료 조건: 위 이벤트가 모두 기록되고 사용자·이벤트 유형·기간으로 조회된다.
9. **만료 대시보드 프론트** — 7/30/60/90일 이내 만료, 이미 만료, 자체 서명, 소유자 없음.
   - 완료 조건: 실제 DB 데이터로 카운트가 채워지고 각 숫자에서 해당 목록으로 이동한다.
10. **인증서 목록·상세·Discovery 프론트** — 목업 구조를 실제 API에 연결.
    - 완료 조건: 업로드부터 상세 조회까지 UI로 전 흐름이 동작한다.

## 이번 세션 특이사항
- **미해결**: `.claude/template-origin` 삭제가 auto mode 권한 분류기에 막혔다(에이전트 실행 2회 + 위임 스폰 1회 모두 차단). 남아 있는 동안 세션 시작마다 `PROJECT-REPO SETUP-PENDING` 마커가 뜬다. 사용자가 터미널에서 `git rm -f .claude/template-origin`을 직접 실행해야 한다. 기능 영향은 없다.
- `SETUP.md` 미처리 잔여: 3번 중 `CHANGELOG.md` 플랫폼 접두어 안내 정리, 4번(GitHub 라벨 배선), 7번. 단일 플랫폼(웹) 프로젝트이므로 CHANGELOG 접두어 문구는 제거 대상이다.
- `docs/MAP.md` 없음 — 소스 코드가 아직 없어 기록할 구조가 없다. 스캐폴딩 후 첫 explorer 탐색 시 생성한다.
- GitHub 연동 상태(`gh auth status`)를 아직 확인하지 않았다. 다음 세션 스캐폴딩 착수 전에 확인해 이슈 기반으로 갈지 로컬 모드(STATE "다음 할 일")로 갈지 정한다. origin은 `github.com/Falke060918/INHERIT_AutoCert`.
- 폰트는 목업뿐 아니라 제품 UI에도 강제값이다. 폐쇄망 제품이므로 실제 구현 시엔 CDN이 아니라 **폰트를 로컬에 번들**해야 한다.

## 주요 파일
- `docs/00-spec.md`: 원본 기획 명세서 v0.1 — 제품 목적·범위·데이터 모델·API·Phase 구분의 단일 출처
- `docs/01-plan.md`: 기획 — 문제 정의, 대상 사용자, Phase 1 기능 9종, 비기능 요구사항, 이번 범위 제외 목록
- `docs/02-goals.md`: 최종 목표, 측정 가능한 성공 기준 7개, 마일스톤 M1~M3
- `mockup/index.html`: 4화면 목업(1,483줄). 단일 정적 HTML, JS 0줄, 라디오+CSS `:has()` 화면 전환. `--font-sans: Geist, Pretendard, sans-serif` / `--font-mono: "Geist Mono", D2Coding, monospace`
- `CLAUDE.md`: 오케스트레이터 운영 원칙 + 하단 `# 프로젝트 고유 규칙` (스택 고정 / 보안 하드 규칙 6항 / 결정론적 자동화 / Phase 1 범위 통제)
- `README.md`: 프로젝트 소개·문서 링크·스택. 설치/실행 절차는 Phase 1 착수 후 작성 예정
- `docs/GOTCHAS.md`: 템플릿 동봉 항목 15개 유지
