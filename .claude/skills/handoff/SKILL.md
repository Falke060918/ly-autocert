---
name: handoff
description: Records the current work state into docs/STATE.md so the next session can take over without context. Use after completing a large unit of work, before ending a session, or when context has gotten heavy.
---

# /handoff — session handover

This does two things: **(1) knowledge-layer update (MAP.md / GOTCHAS.md)** + **(2) state save (STATE.md)**.
Keep this order — overwriting STATE.md destroys the transfer candidates (특이사항), so finish the knowledge transfer first.

Run handoff and its doc updates (STATE.md / GOTCHAS.md / MAP.md) as a **single implementer, single commit** — never split across parallel or sequential agents; they collide on the shared working tree's git index.

## 1. Knowledge-layer update — docs/MAP.md, docs/GOTCHAS.md (**before** overwriting STATE.md)

Collect transfer candidates from two places (MAP is already updated at exploration/commit time):
- `GOTCHA 후보:` items the orchestrator collected during the session
- Items in the **current** docs/STATE.md 특이사항 section that are "still true next week"

If there are candidates, apply the 4-condition entry gate below **first** to drop non-qualifying ones, then present the survivors all at once via AskUserQuestion (multiSelect) and transfer **the selected items as-is** to GOTCHAS — no post-selection culling. If more than 4 survive, split into multiple questions (AskUserQuestion holds max 4 options each). If there are no candidates (or none pass the gate), skip this confirmation.
**If there are no candidates and no `MAP-STALE`/`KB-OVER-LIMIT` warning either, skip this entire step.** If `KB-OVER-LIMIT` appeared, first identify **which** file is over its cap — the marker does not say: STATE > 150 / MAP > 120 / GOTCHAS > 100 lines. GOTCHAS/MAP overflows are handled in their subsections below; a STATE overflow is handled by the size budget in step 2. If `docs/GOTCHAS.md` does not exist, create it at that point (do not pre-create an empty file). Do not create MAP here — if absent, leave it absent; it appears at the next explorer exploration.

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

Eviction: resolved by an upstream/dependency update / that stack was removed / `last recurrence` is 6+ months ago **and** the cap is exceeded.

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

**Budget STATE.md by size, not by line count.** It is `@`-imported into every session's main context, so what costs is bytes, not lines. Target **6 KB or less (roughly 1,500 tokens)** and 150 lines as a hard ceiling. Dense Korean prose blows the byte budget long before the line cap trips — a 60-line STATE.md measured ~10.7 KB / ~3,200 tokens. Check the actual size (`wc -c docs/STATE.md`) before committing; if it is over, cut. Note the session-start `KB-OVER-LIMIT` marker still counts lines only, so it will not warn you here.

Cut by moving content out rather than by trimming words:
- **Structure / file-role / reference material** — a per-file "what this is for" catalog does not belong in STATE.md. It goes to `docs/MAP.md`, or in this template repo (which keeps no MAP.md) to `docs/TEMPLATE-NOTES.md`. Update that file in the same commit; do **not** re-add a `## 주요 파일` section to STATE.md.
- **Recurrence-prone traps** — `docs/GOTCHAS.md`.
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

These four sections are the whole file — do not append others. Anything that is still true next week is not state; route it to MAP/TEMPLATE-NOTES/GOTCHAS as listed above.

### Kickoff/architect plan docs — docs/01-plan.md, docs/02-goals.md, docs/03-architecture.md (only if they exist)
If this work unit changed the feature direction, scope, goals, or technical design, update `docs/01-plan.md` / `docs/02-goals.md` / `docs/03-architecture.md` **in the same commit** as STATE.md — plan docs must not rot while STATE.md moves on. Only touch the parts the change invalidated; do not rewrite the whole file. If a file does not exist (e.g. this template repo, or a repo that never ran `/kickoff` or `/architect`), do nothing — do not create it here.

If the main session is in orchestrator mode (no write tools): write the full new STATE.md text and delegate to implementer with "overwrite docs/STATE.md with this content, commit, then push all unpushed commits".

## 3. Report

After committing STATE.md, push all unpushed commits (bulk push — handoff closes a work unit; no re-confirmation, no force flags). Then report to the user (in the user's language, Korean):
1. Update done + summary of next tasks
2. Guidance text: "이어서 진행하려면 `/clear`로 컨텍스트를 비우면 됩니다. STATE.md가 자동 로드되므로 바로 작업을 지시하세요. 재시작은 세션 시작 시점에만 로드되는 것(`.claude/settings.json`의 hooks·permissions, CLAUDE.md와 `@` import 파일, 플러그인, 에이전트 정의, SessionStart 훅 마커)을 바꿨을 때만 필요합니다."
