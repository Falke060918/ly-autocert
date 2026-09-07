# STATE — 마지막 갱신: 2026-09-07

## 프로젝트
INHERIT_AutoCert — 폐쇄망·레거시 환경용 온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼. (명세: docs/00-spec.md / 기획: docs/01-plan.md / 로드맵: docs/ROADMAP.md)

## 현재 상태
**P2+P3 범위 확장을 사용자가 승인**(2026-09-07) — 자동발급·갱신·배포 착수. 스펙 `docs/specs/ATC-20260907-001-auto-issuance.md` 확정·push(ae45dcf), Work Order 접두어 ATC 등록, `.claude/identity` 신설(project/done).
- 확정 결정 6건(정본은 스펙): ACME 게이트웨이 먼저 / 배포는 별도 에이전트 신설(센서 확장 아님, 자동 업데이트는 보류) / 발급 승인은 정책 스위치(신규 승인 기본 ON, 갱신 자동) / 키는 우리가 생성+CSR 제출 / 크리덴셜(app_id/secret)은 관리 페이지 입력·암호화 저장 / 배포 대상 nginx+Apache 먼저.
- 사전 조사 3건 로컬 보관(untracked): `.claude/tmp/autoissue/{api-docs,codebase,sensor-deploy}/summary.md`.
- 진행 중: planner가 `docs/design/04-auto-issuance.md` 설계 작성 중 — 아직 미완.
- 릴리스 1.2.0(VERSION·compose 태그·CHANGELOG·frontend/package.json 4곳 동기), 최신 마이그레이션 0022.
- **P1.5 분해안은 그대로 승인 대기**(planner 산출): 9단위, Wave1 병렬(U8 Origin/Edge 설계문서만·U7a WebtoB 파서·U1 소유자 지정·U2 알림 ack/snooze+에스컬레이션·U3 정책 위반 점수·U4 리포트+coverage·U5 커넥터 공통 프레임) → Wave2 U6 K8s·U7b WebtoB 대사 배선. 착수 전 결정 1건: 소유자 후보 디렉토리 API(사용자 목록) 공개 범위 — 현재 system_administrator 전용. 상세는 git의 d2cb7e5 시점 STATE.

## 다음 할 일 (우선순위 순)
1. **설계 사전 검사 → ATC-20260907-001 하위 단위 분배** — planner의 04-auto-issuance.md 완성분을 검토한 뒤 Wave 분해·병렬 위임.
2. **P1.5 트랙** — 사용자 승인 + U1 권한 결정 후 Wave1 7단위 병렬 위임.
3. **정리 필요 2건** — ① `.claude/rules/` 디렉터리가 저장소에 없음(CLAUDE.md 조건부 로드 표·notion 규칙 참조와 불일치) ② CLAUDE.md '범위 통제 — Phase 1만' 절이 P2+P3 승인과 어긋남(스펙이 이김, 문구 갱신 필요).
4. **보류 2건** — 오프라인 라이선스 선행 결정 논의(설계 docs/design/03-license.md 완료, `backend/cmd/licensegen` 미작성) / dangling docker volume 정리(도커 기동 시).
5. **P2 스키마 설계 직전 일괄 재론 묶음** — 멀티테넌시 컬럼(옵션 B)·SNI 엔드포인트 식별자(현 스캐너 SNI 미전송)·Origin/Edge 스키마. 개별 선행 착수 금지, 착수 전 planner 비용 산정.
6. **잔여 갭 유지분** — docker compose 실기동 검증 미수행 / `.env.example` 키 추가는 사용자 수작업 / Windows 메인 서버 미지원(센서만) / DB `sslmode=disable` / Windows 센서 개인키 NTFS ACL 미보호 / STARTTLS 미지원 / 감사 로그 조회 UI 부재 / 재스캔·취소 actor 미기록 / 웹 콘솔 enroll 명령 예시 `--token-file` 형 전환 검토.

## 이번 세션 특이사항
- 사업 문서(`docs/TLS_인증서_관리_신규사업_AI_context.md`)는 원본 보존 — 수정 금지. 펜타 WAPPLES는 공개 문서만으로 일반화 금지(고객 PoC 확인 항목은 사업 문서 11장). WebtoB는 고객 PoC 확인 6건 선행.
- Phase 번호는 00-spec 15장 새 체계가 정본. 목록 TLS 열 정렬은 기각(집계 파생값 — 화이트리스트 밖).
- 릴리스류 커밋은 author 확인(워크트리 생성 환경에 따라 로컬 git 계정이 갈린 전례 있음).
