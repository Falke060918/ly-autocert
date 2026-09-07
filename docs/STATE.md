# STATE — 마지막 갱신: 2026-09-07

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 로드맵: docs/ROADMAP.md)

## 현재 상태
**ATC-20260907-001(자동발급·갱신·배포) 설계 확정, Wave1 구현은 사용자 중단으로 미완.**
- 설계 정본 `docs/design/07-auto-issuance.md`(04→05→07 재번호, dba784b). 설계 사전 검사 pass — 판정 `.claude/review/ATC-20260907-001-design.verdict.md`(로컬 전용, gitignore).
- 위임장 A·B 원문은 스펙 `docs/specs/ATC-20260907-001-auto-issuance.md`의 '## 위임장' 절에 보관 — 재발사 시 그대로 쓴다.
- **Wave1 A·B를 워크트리 격리로 발사했으나 사용자 중단 지시("중단 후 진행상황/계획 저장 후 커밋/푸시")로 미완 종료.** 핸드오프 시점 실측: 두 워크트리 브랜치(`worktree-agent-a3436f048497ac777`·`a8b297ced2e6a5c51`) 팁이 dba784b — 커밋된 부분 작업 없음.
- 발견 큐 결정 3건: ACME 표준 접수구 **보류** / Prometheus 메트릭 최소셋 **보류** / `.claude/rules/` 부재는 템플릿 동기화로 **복구 완료**(54be393, CLAUDE.md 프로젝트 절·.gitattributes 커스터마이즈는 백업 복원).
- learn 수신함에 1건 들어옴: `docs/learn/2026-09-07-sync-gitattributes.md` — 다음 유인 세션 `/learn` 대상.
- 확정 결정 6건(정본은 스펙): ACME 게이트웨이 먼저 / 배포는 별도 에이전트 신설(센서 확장 아님) / 발급 승인은 정책 스위치(신규 승인 기본 ON, 갱신 자동) / 키는 우리가 생성+CSR 제출 / 크리덴셜(app_id/secret)은 관리 페이지 입력·암호화 저장 / 배포 대상 nginx+Apache 먼저.
- 사전 조사 3건 로컬 보관(untracked): `.claude/tmp/autoissue/{api-docs,codebase,sensor-deploy}/summary.md`.
- 릴리스 1.2.0(VERSION·compose 태그·CHANGELOG·frontend/package.json 4곳 동기), 최신 마이그레이션 0022.
- **P1.5 분해안은 그대로 승인 대기**(planner 산출): 9단위, Wave1 병렬(U8·U7a·U1·U2·U3·U4·U5) → Wave2 U6·U7b. 착수 전 결정 1건: 소유자 후보 디렉토리 API 공개 범위(현 system_administrator 전용). 상세는 git의 d2cb7e5 시점 STATE.

## 다음 할 일 (우선순위 순)
1. **Wave1 A·B 재개** — ① 워크트리·브랜치 잔재 확인(`git worktree list`, `git log --all --grep=ATC-20260907-001-`): 부분 커밋 있으면 회수/폐기 판단, 없으면 스펙 보관 위임장 그대로 재발사. ② 완료 시 3자 검토 배치(설계자+검사관2, 판정 3장 중 2 pass) → push → C(발급 파이프라인+UI) → D(배포 실행) → E(자동 갱신·재배포) 순.
2. **E2E 실검증 준비** — 사용자 제공 데모 계정(app_id/secret) 필요. A 단위 완성 후 관리 페이지에 입력받는 흐름.
3. **CLAUDE.md '범위 통제 — Phase 1만' 절 문구 갱신** — P2+P3 승인과 어긋남(스펙이 이김).
4. **P1.5 트랙** — 사용자 승인 + U1 권한 결정 후 Wave1 7단위 병렬 위임.
5. **보류 2건** — 오프라인 라이선스 선행 결정 논의(설계 docs/design/03-license.md 완료, `backend/cmd/licensegen` 미작성) / dangling docker volume 정리(도커 기동 시).
6. **P2 스키마 설계 직전 일괄 재론 묶음** — 멀티테넌시 컬럼(옵션 B)·SNI 엔드포인트 식별자(현 스캐너 SNI 미전송)·Origin/Edge 스키마. 개별 선행 착수 금지, 착수 전 planner 비용 산정.
7. **잔여 갭 유지분** — docker compose 실기동 검증 미수행 / `.env.example` 키 추가는 사용자 수작업 / Windows 메인 서버 미지원(센서만) / DB `sslmode=disable` / Windows 센서 개인키 NTFS ACL 미보호 / STARTTLS 미지원 / 감사 로그 조회 UI 부재 / 재스캔·취소 actor 미기록 / 웹 콘솔 enroll 명령 예시 `--token-file` 형 전환 검토.

## 이번 세션 특이사항
- `.claude/identity`에 `notion:` 키 없음 — Notion 미러는 키 설정 전까지 전부 건너뜀. 유인 세션에서 페이지 ID 1회 질문 대상.
- 사업 문서(`docs/TLS_인증서_관리_신규사업_AI_context.md`)는 원본 보존 — 수정 금지. 펜타 WAPPLES는 공개 문서만으로 일반화 금지(고객 PoC 항목은 사업 문서 11장). WebtoB는 고객 PoC 확인 6건 선행.
- Phase 번호는 00-spec 15장 새 체계가 정본. 릴리스류 커밋은 author 확인(워크트리 환경에 따라 로컬 git 계정이 갈린 전례).
