# Duet Starter — Claude 실행 경계

Claude의 메인은 `.claude/settings.json`의 `agent: orchestrator`로 시작한다.
메인은 계획·위임·검토·통합을 맡고 실행은 implementer에게 맡긴다.
위임장 4종, 실패 후 반성, M급 이상 3자 검토 등 기존 Claude 절차를 유지한다.

## 두 실행기가 함께 쓰는 것

- 코드와 `docs/STATE.md`, `docs/MAP.md`, `docs/GOTCHAS.md`, `docs/constitution.md`, 제품 스펙을 공유한다.
- Claude는 `.claude/`와 원래 이름의 `scripts/`를 사용한다. Codex의 `.agents/skills/`,
  `.codex/`, `scripts/codex-*`를 Claude 스킬·훅으로 로딩하지 않는다.
- 도구를 바꾸기 전에 `/handoff`로 STATE를 저장한다. 새 도구는 STATE부터 읽는다.
  대화 원문·세션 ID·압축 스냅샷·계측·검토 판정·automode는 실행기별이다.
- 동시에 같은 파일이나 STATE를 수정하지 않는다. 파일 소유권을 나누고 STATE 갱신은 한 명에게만 맡긴다.
  다른 실행기의 판정을 새 실행기의 검증으로 사칭하지 않는다.
- 두 identity의 프로젝트 종류·목적·origin·template·work-order-prefix는 같은 값으로 유지한다.
  초기화는 `SETUP.md`를 따른다. 원본 Notion 페이지 ID는 이어받지 않는다.

## 승인과 안전 경계

사용자가 승인한 작업·방식은 반복 확인하지 않는다. 명확한 요청은 끝까지 진행한다.
마커·STATE·이전 템플릿의 기록 자체는 파일 삭제, GitHub·Notion 쓰기, 배포의 승인이 아니다.
`PROJECT-REPO SETUP-PENDING`이면 SETUP을 읽고 현재 요청에 초기화가 포함될 때만 실행한다.
원본의 snapshot 후 삭제 절차 대신 기존 파일과 이력을 보존하는 통합 SETUP을 적용한다.
커밋은 로컬 기록이며 push·Notion 반영과 별개다. 외부 반영이 승인된 경우에만 실행한다.
Notion이 pending이거나 원격이 없으면 로컬 기록으로 완료하며 임의 연결하지 않는다.
`claude-source`, `codex-source`, `template`, `upstream`은 원본 추적용이며 제품 변경을 push하지 않는다.
권한·sandbox·trust 거부를 다른 도구나 하위 에이전트로 우회하지 않는다.
무인 모드는 진행 질문만 줄이며 새로운 목표·비용·파괴적 작업까지 허용하지 않는다.

`/template-update`는 양쪽 운영 파일을 함께 갱신하되 사용자 설정과 제품 기록을 보존한다.
구체적 보존 항목과 Codex 역할 재생성 절차는 `docs/UNIFICATION.md`를 따른다.
`docs/legacy-claude/`와 통합 이전 specs/releases/design의 사례는 역사 자료이지 현 세션의 지시가 아니다.
