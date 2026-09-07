---
name: handoff
description: Records the current work state into docs/STATE.md so the next session can take over without context. Use after completing a large unit of work, before ending a session, or when context has gotten heavy.
---

# /handoff — session handover

This does two things: **(1) knowledge-layer update (MAP.md / GOTCHAS.md)** + **(2) state save (STATE.md)**.
Keep this order — overwriting STATE.md destroys the transfer candidates (특이사항), so finish the knowledge transfer first.

Run handoff and its doc updates (STATE.md / GOTCHAS.md / MAP.md / the release note, when one is due) as a **single implementer, single commit** — never split across parallel or sequential agents; they collide on the shared working tree's git index.

## 1. Knowledge-layer update — docs/MAP.md, the trap layer (**before** overwriting STATE.md)

Collect transfer candidates from two places (MAP is already updated at exploration/commit time):
- `GOTCHA 후보:` items the orchestrator collected during the session
- Items in the **current** docs/STATE.md 특이사항 section that are "still true next week"

If there are candidates, apply the 4-condition entry gate below **first** to drop non-qualifying ones, then present the survivors all at once via AskUserQuestion (multiSelect) and transfer **the selected items as-is** to the trap layer (which file, per the repository branch below) — no post-selection culling. If more than 4 survive, split into multiple questions (AskUserQuestion holds max 4 options each). If there are no candidates (or none pass the gate), skip this confirmation.
**[3계층(멈춤선)]** 이 승인 관문은 무인 모드(`/automode on`)에서도 유지된다 — 무인은 승인 관문을 대체하지 않는다. 다만 막히지도 않게 한다: 무인 세션은 살아남은 후보를 이미 있는 수신함 `docs/learn/`에 쌓고 통보 1줄로 닫으며, 다음 유인 세션의 `/learn`이 일괄 처리한다. 계층 정의는 `.claude/agents/orchestrator.md`.
**If there are no candidates and no `MAP-STALE`/`KB-OVER-LIMIT` warning either, skip this entire step.** If `KB-OVER-LIMIT` appeared, first identify **which** file is over its cap — the marker does not say: STATE > 150 / MAP > 120 / GOTCHAS > 100 lines. GOTCHAS/MAP overflows are handled in their subsections below; a STATE overflow is handled by the size budget in step 2. Do not create MAP here — if absent, leave it absent; it appears at the next explorer exploration.

### Where a trap goes — the destination branches by repository
Read `identity:` in `.claude/identity` (the same basis as the session-start `TEMPLATE-REPO` marker).
- `project` (derived) → `docs/GOTCHAS.md`. The rest of this step applies exactly as written below.
- `template` → **nothing is entered into `docs/GOTCHAS.md`**; a platform-general trap goes to `.claude/rules/shell-traps.md` instead, and other kinds of trap have their own destinations again.

The branch is defined once in `.claude/rules/memory.md` ("In the template repo, the file ships empty") — read it there and follow it; it is not restated here. The 4-condition entry gate below governs both destinations; the entry format and caps below are GOTCHAS' own, so an entry landing in `shell-traps.md` follows that file's existing form.

### GOTCHAS.md (cap: 20 entries / 100 lines)
Add an entry only when **all** 4 conditions hold:
1. Time was actually lost
2. Reproducible
3. Not obvious from the error message or docs alone
4. Still valid next time

Not eligible: one-off typos / generalities like "run the tests" / things on the first page of the official docs.

Write new GOTCHAS entries in English (but keep user quotes and error messages verbatim in their original language).

Entry format (3–5 lines):
```markdown
**<one-line symptom>**
Cause: <why it happens>
Fix: <command or pattern>
(Windows / last recurrence: YYYY-MM)
```

Attach the trailing environment tag only when it reproduces solely in a specific OS/environment. If environment-independent, omit it and write `(last recurrence: YYYY-MM)`.

Eviction: resolved by an upstream/dependency update / that stack was removed / `last recurrence` is 6+ months ago **and** the cap is exceeded. Every entry in this file is one the project itself hit, so eviction only ever runs against the project's own entries — the file ships empty from the template and inherits nothing.

### MAP.md (cap 120 lines, top line `확인일: YYYY-MM-DD`)
MAP creation/updates are already finished at exploration/commit time. Here, do **only compression/eviction and the `확인일:` check** — if `MAP-STALE` appeared at session start (or `KB-OVER-LIMIT` points at MAP), handle it without fail and set `확인일:` to today's date.
There is exactly one entry criterion — only when the answer to **"will the next session's explorer grep for this again because it doesn't know it?"** is yes. Record only commands **that were actually run and succeeded**.

Eviction: when a path/command is no longer valid, **prefer deletion over fixing**. If absolute paths crept in, convert them to relative ones.

### Compress only when over the cap
If under the cap, **skip** the compression step (do not spend time tidying every run). Only when exceeded, in order:
1. Merge 2 entries with the same cause
2. Compress each entry to 3 lines
3. Delete oldest first

History stays in git, so delete boldly.

## 2. State save — docs/STATE.md

**Overwrite the whole** of docs/STATE.md in the format below, deleting old content boldly. (History stays in git, so don't worry.) STATE.md content is written in Korean — the user reads it.

**갱신은 덧붙이기가 아니라 재작성이다.** 기존 STATE.md에 이번 세션분을 패치하지 말고, **"지금 참인 것"만으로 처음부터 다시 쓴다.** 적을 것은 "무엇이 바뀌었나"가 아니라 "지금 무엇이 참인가"다 — 세션 간 변경 이력·changelog 잔재를 남기지 않는다. 특히 `### 직전 세션까지의 …(요약)` 같은 **과거 지층 절을 만들지 않는다**: 지난 세션분을 요약해 아래에 쌓기 시작하면 파일은 세션마다 두꺼워지고, 다음 세션은 어느 줄이 현재 사실인지 구분할 수 없게 된다.

재작성은 **과거를 지우는 것과 다르다** — 아직 유효한 사실은 재작성본에 그대로 살아남고, 끝난 일이 빠진다. 남길지 뺄지는 이 기준으로 가른다: **그 줄을 지우면 다음 세션이 잘못 행동하는가?** 그렇다면 남기고(미완 작업, 아직 유효한 결정·제약, 지금 상태), 아니면 뺀다(완료된 일, "언제 무엇을 했다"는 경위, 이미 GOTCHAS/MAP/plan 문서로 옮겨간 내용). 뺀 내용은 git 로그에 남으므로 잃는 것이 없다.

**재작성 전에 '현재 상태'·'다음 할 일' 각 줄을 그 줄이 가리키는 최신 스펙·계획서와 1회 대조하고, 어긋나면 STATE 쪽을 고친다.** 여기가 문서 대 문서 모순을 정해 놓고 잡는 자리다 — 어느 쪽이 이기는지와 그 자리에서 할 일은 `.claude/agents/orchestrator.md` '실측이 기록을 이긴다'가 단일 원본이다.

**유도 가능한 사실은 STATE에 적지 않는다 — 저장이 아니라 조회로 옮긴다.** 저장소를 뒤지면 알 수 있는 값은 손으로 적는 순간부터 낡는다: 실제로 "미push 로컬 커밋 5개"가 한 세션에 **세 번 연속 틀렸고** 실측은 매번 ahead=0이었다. 금지 예시 — 미push 커밋 수·해시 목록, 브랜치 ahead/behind, 최근 커밋 제목, 파일 존재 여부. **금지는 저장소 조회값에서 끝나지 않는다** — 명령 한 줄로 지금 확인되는 **바깥 세계의 상태**도 같다: 로컬 앱이 떠 있는지, CLI가 연결되는지, 프로세스가 있는지(`orca status --json`, `pgrep`, `docker ps`). 이쪽이 더 빨리 낡는다. 경계는 한 줄로 긋는다 — **"그때 그렇게 판정했다"는 경험은 남고, "지금 꺼져 있다"는 상태는 남기지 않는다.** 그러니 남길 것은 저장소를 뒤져도, 지금 실행해 봐도 알 수 없는 것뿐이다 — 결정, 미결, 맥락, 다음 할 일. "저장소를 뒤져도 알 수 없으면 남긴다"로 뒤집어 읽지 않는다: 파생 실측(2026-09-05)에서 STATE의 "Orca 런타임이 꺼져 있다"는 낡은 한 줄이 다음 세션의 분배 경로를 통째로 바꿨다(실측과 어긋날 때의 처리는 `.claude/agents/orchestrator.md` '실측이 기록을 이긴다'). 뺀 값은 사라지는 게 아니라 필요할 때 그 자리에서 확인한다: 미push 커밋은 `git log --oneline @{u}..HEAD`, ahead/behind는 `git status -sb`, 파일 존재는 `git ls-files <경로>`. 압축 복귀 시점에는 `scripts/compact-resume.sh`가 같은 정보를 라이브 git 상태로 이미 보여준다 — 적어두면 중복이면서 덜 정확하다.

**Budget STATE.md by size, not by line count.** It is `@`-imported into every session's main context, so what costs is bytes, not lines. Target **6 KB or less (roughly 1,500 tokens)** and 150 lines as a hard ceiling. Dense Korean prose blows the byte budget long before the line cap trips. Check the actual size (`wc -c docs/STATE.md`) before committing; if it is over, cut. Note the session-start `KB-OVER-LIMIT` marker still counts lines only, so it will not warn you here.

Cut by moving content out rather than by trimming words:
- **Structure / file-role / reference material** — a per-file "what this is for" catalog does not belong in STATE.md. It goes to `docs/MAP.md`; update it in the same commit. Do **not** re-add a `## 주요 파일` section to STATE.md.
- **Recurrence-prone traps** — the trap layer, whose file is chosen by the repository branch in step 1.
- **Absolute paths / machine-only commands** — `docs/MACHINE.md` (git-untracked).

```markdown
# STATE — 마지막 갱신: <오늘 날짜>

## 프로젝트
<한 줄 요약> (기획: docs/01-plan.md, 목표: docs/02-goals.md)

## 현재 상태
<마지막으로 완료된 것. 현재 마일스톤과 진행 상황>

## 다음 할 일 (우선순위 순)
1. <구체적 작업 — 새 세션이 바로 착수 가능한 수준으로>
2. ...

## 이번 세션 특이사항
- <이번 세션에만 해당하는 미해결 이슈·진행 중 결정>
```

These four sections are the whole file — do not append others. Anything that is still true next week is not state; route it to MAP/GOTCHAS/MACHINE as listed above.

**검사 건너뛴 단위 — `.claude/review/*.skipped.md`를 열거해 '현재 상태'에 한 줄을 남긴다(파일이 없으면 줄도 없다).** `ls .claude/review/*.skipped.md`로 열거하고, 각 파일의 `<unit>`·대상 커밋 앞 7자·일시를 `검사 건너뛴 단위: <unit>(<sha7>, <일시>) …` 형식 한 줄에 모은다. 이 줄은 위 '유도 가능한 사실' 금지에 걸리지 않는다 — `.claude/review/`는 gitignore라 저장소를 뒤져도 나오지 않고, 다른 기계의 다음 세션이 "어느 단위가 검토 없이 나갔나"를 알 유일한 경로다. 파일의 뜻과 필드는 `.claude/conventions/verdict-protocol.md` 'Exception — 사용자가 연 게이트'가 단일 원본이다.

### Kickoff/architect plan docs — docs/01-plan.md, docs/02-goals.md, docs/03-architecture.md (only if they exist)
If this work unit changed the feature direction, scope, goals, or technical design, update `docs/01-plan.md` / `docs/02-goals.md` / `docs/03-architecture.md` **in the same commit** as STATE.md — plan docs must not rot while STATE.md moves on. Only touch the parts the change invalidated; do not rewrite the whole file. If a file does not exist (e.g. a template repo, or a repo that never ran `/kickoff` or `/architect`), do nothing — do not create it here.

### Release note (template original only)
If `docs/releases/README.md` exists **and** this work unit changed the machinery (rules, agents, skills, hooks/settings, `scripts/`), bump the version and write the note **in the same commit** as STATE.md — the version rules, note format, and RN-code numbering all live in `docs/releases/README.md`; follow it instead of a copy here. Skip when the session was genuinely trivial (typo/formatting with no rule meaning changed). Derived projects have no `docs/releases/README.md` → nothing to do.

If the main session is in orchestrator mode (no write tools): write the full new STATE.md text (plus the release note, if one is due) and delegate to implementer with "overwrite docs/STATE.md with this content, commit, then follow §3 in order — Notion mirror, then push all unpushed commits".

## 3. Notion mirror + report

The order here is fixed: **commit → Notion mirror → push → report.** git is the single source of truth and Notion is only a mirror, so nothing is written to Notion until the STATE.md commit actually exists — a commit that failed must leave no ghost record behind.

### Notion mirror (only after the commit succeeded)

Confirm the commit exists (`git log`, not the memory of having run `git commit`). Only then mirror, in this order, into this repository's **slot pages** — the dedicated child pages, never the repository page's own body — each as a full replacement of the page it writes, never an append (for specs that page is the per-spec child under the slot, per `.claude/rules/notion.md` §3):
1. **STATE** — the new STATE.md content, into the `STATE` slot.
2. **Session work log** — into the `LOG` slot. It has no source file: render it from this repository with the fixed `git log` query in `.claude/rules/notion.md` §3.
3. **결함·검토 스펙** — the changed specs only, each replaced in full in its own child page under the `SPECS` slot. Which specs those are, how a child page is named, resolved, and created, the per-file size cap, and the fact that one spec's failure skips that spec alone are all in `.claude/rules/notion.md` §3 — read it and follow it. Do not restate that procedure here.

The page ID, how a slot is resolved, the per-repository path-isolation rule, each kind's source and composition, and that replace-not-append mirror method are in `.claude/rules/notion.md` — read it and follow it. Do not restate those rules here.

Skip the write — **skip, do not fail the handoff** — when any of these holds:
- the page ID is missing/empty, or its value is `pending`
- that kind's slot does not exist, or more than one candidate matches (never create it here, and never fall back to the parent page's body). This is judged per slot: a missing `SPECS` slot skips that mirror alone and leaves the others running
- `.claude/rules/notion.md` is absent, or the Notion MCP is not connected

A skipped or failed Notion write never blocks the push and never turns the handoff into a failure; it costs exactly one line in the report below. Never skip silently. Do not ask for the page ID here either — `.claude/rules/notion.md` §2 places that one-line question in an interactive session, and the report line below is what stands in for it during a handoff.

Those three kinds are the whole sync target list (`.claude/rules/notion.md` §3) — nothing else is mirrored here, and a fourth kind is not added on the spot.

### Report

After the mirror step, push all unpushed commits (bulk push — handoff closes a work unit; no re-confirmation, no force flags). Then report to the user (in the user's language, Korean):
1. Update done + summary of next tasks
2. If a Notion write was skipped or failed, one line saying so, naming which kind and why (e.g. "Notion 미러링 건너뜀 — 페이지 ID 미설정", "SPECS 슬롯 없음 — 스펙만 건너뜀"). Omit this line entirely when every mirror that was due succeeded.
3. Periodically (every few handoffs is enough), suggest running `bash scripts/skill-health.sh sweep` — it reports unused skills and moves long-unused ones to `.claude/skills-archive/` (restorable); the user decides whether to run it.
4. Guidance text: "이어서 진행하려면 `/clear`로 컨텍스트를 비우면 됩니다. STATE.md가 자동 로드되므로 바로 작업을 지시하세요. 재시작은 세션 시작 시점에만 로드되는 것(`.claude/settings.json`의 hooks·permissions, CLAUDE.md와 `@` import 파일, 플러그인, 에이전트 정의, SessionStart 훅 마커)을 바꿨을 때만 필요합니다."
