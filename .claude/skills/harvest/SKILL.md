---
name: harvest
description: "Feeds project-agnostic lessons (GOTCHAS entries, skills, rules) from a derived project back into the claude-starter-v2 template repo as a draft PR. Use when the user asks to harvest, 환류, or send lessons back to the template — or when /handoff spots a lesson that is not repo-specific."
---

# /harvest — 파생의 교훈을 템플릿 본체로 환류

design/02 "환류" 절의 구현. 판정 기준은 하나다: **다른 프로젝트에서도 참인가?** 참이면 그것은
프로젝트 지식이 아니라 템플릿 지식이고, 이 스킬이 템플릿 본체로 보내는 PR(반영 요청) 초안을 만든다.
User-facing communication is in Korean.

## 0. 신분 확인 — 발신 측인지 수신 측인지

`.claude/identity` 의 `identity:` 값을 읽는다 (`#` 줄 무시).

- `project` → **발신 측**. 아래 1~4를 진행한다.
- `template` → **수신 측**. 아래 5(수신 검토)만 해당한다 — 템플릿 자신에게서 회수할 것은 없다.
- 파일 없음 → 중단하고 SETUP.md 1단계(identity 생성)를 안내한다. 추정으로 진행하지 않는다.

## 1. 후보 수집 (발신 측, explorer 위임 가능)

세 곳을 훑어 "이 저장소 고유가 아닌 것"을 고른다.

| 출처 | 후보 판정 |
| --- | --- |
| `docs/GOTCHAS.md` 항목 | 제품 도메인·이 저장소의 파일·스택 이름이 원인에 등장하면 **고유** — 제외. 플랫폼·도구·Claude 동작에 대한 것이면 후보. |
| `.claude/skills/` 중 프로젝트가 직접 만든 것 | 절차가 제품 개념 없이 성립하면 후보. (템플릿에서 동기화로 받은 스킬은 애초에 템플릿 것 — 제외.) |
| `docs/constitution.md` 하단 "이 프로젝트의 원칙" | 스택 선택·도메인 규칙은 고유 — 제외. 위임·검증·안전에 대한 일반 규칙이면 후보. |

**절대 후보가 아닌 것**: 절대 경로·머신 사실(그 자리는 `docs/MACHINE.md`), secret, 제품 코드,
`docs/STATE.md` 류 휘발성 기록.

## 2. 사용자 승인

후보를 AskUserQuestion 으로 항목화해(4개씩 묶음) 보낸다/보류/제외를 고르게 한다.
승인 없는 환류는 없다 — design/02 의 "승격 관문은 전부 사람 승인" 원칙과 같다.

**[3계층(멈춤선)]** 이 관문은 무인 모드(`/automode on`)에서도 유지된다 — 무인은 승인 관문을 대체하지 않는다. 계층 정의는 `.claude/agents/orchestrator.md`.

## 3. PR 초안 작성

템플릿 저장소 위치는 sync 와 같은 git remote `template` 에서 얻는다 (`git remote get-url template`).
없으면 URL 을 1회 묻는다.

1. 스크래치 폴더에 템플릿 저장소를 얕게 클론한다 (`git clone --depth 1 <url>`).
2. 브랜치 `harvest/<프로젝트명>-<YYYYMMDD>` 를 만든다.
3. 승인된 항목을 반영한다 — 스킬은 디렉토리째 `.claude/skills/` 로 복사, 규칙은 해당
   rules/에이전트 문서에 추가. **GOTCHAS 항목은 템플릿의 `docs/GOTCHAS.md` 로 가지 않는다**:
   받는 쪽이 템플릿 저장소라 그 파일에는 아무것도 넣지 않으므로, 플랫폼 일반 함정은
   `.claude/rules/shell-traps.md` 에 그 파일의 형식으로 넣는다. 분기의 단일 원본은
   `.claude/rules/memory.md` 이고, 어느 목적지도 아닌 항목(행동 문제·템플릿 기계 자체의 함정)은
   그 파일이 가리키는 자리로 보내거나 PR 본문에 사유를 적고 뺀다.
4. 항목당 커밋 1개, 메시지에 출처를 남긴다: `harvest(<프로젝트명>): <요지>`.
5. `gh` 가 되면 push 후 `gh pr create --draft` — 본문에 각 항목의 "다른 프로젝트에서도 참인 이유"
   1줄씩. push 권한이 없으면 `git format-patch` 로 패치 파일을 만들어 사용자에게 전달 경로를 안내한다.

## 4. 발신 측 마무리

- 보낸 항목은 파생 쪽에서 지우지 않는다 — 템플릿에 머지되면 다음 sync-manifest 동기화가
  (동기화 대상인 skills·rules 는) 정본을 도로 내려보낸다. GOTCHAS 항목도 템플릿에서는
  `.claude/rules/shell-traps.md`(동기화 대상)로 받으므로 같은 경로로 내려온다 —
  파생의 `docs/GOTCHAS.md` 사본은 그대로 둔다.
- `docs/STATE.md` 에 "환류 PR <링크> 대기" 를 남겨 다음 세션이 추적하게 한다.

## 5. 수신 검토 (identity: template 에서)

harvest PR/패치가 들어오면:

- 항목마다 일반성 재판정 — 발신 프로젝트 고유 명사가 남아 있으면 일반형으로 고쳐 받는다.
- 받는 자리는 템플릿의 `docs/GOTCHAS.md` 가 아니다 — 3단계 3번의 분기대로 받는다
  (플랫폼 일반 함정이면 `.claude/rules/shell-traps.md`, 단일 원본 `.claude/rules/memory.md`).
- 기존 함정 계층·스킬과 중복 대조 — 같은 함정의 변형이면 새 항목 대신 기존 항목에 합친다.
- 머지 후: 스킬·rules 로 받은 것은 각 파생의 다음 동기화 때 자동 전파된다. 함정도 동기화 대상인
  `shell-traps.md` 로 받으니 같은 경로를 탄다 — 신규 파생에만 실리던 GOTCHAS 경로는 쓰지 않는다.
