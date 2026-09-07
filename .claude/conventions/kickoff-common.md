# Kickoff-family shared conventions — single source

The kickoff family of skills — `kickoff` and its purpose variants (`kickoff-docs`, `kickoff-research`, and any later ones) — share five protocol pieces. **This file is their single source**: a variant references the section it needs instead of restating it, so an edit here reaches every variant at once. What stays in each skill is only the **binding** — which files, stages, and gate names the generic rule attaches to in that flow. A variant that copies a section back into its own body instead of referencing it recreates the drift this file exists to remove.

## §1 Unattended draft mode — when the answers do not come

If AskUserQuestion cannot be used, a round comes back with no answer, or the user says "자동으로 진행" partway through, do not stall. Put every open item through the two-condition test (§2): what passes it you settle yourself on the default, and what fails it stays open as a `[확인 필요: ...]` marker (§3), placed where that decision belongs. Then continue to the next round and on through the remaining stages. Report in one line how many markers the draft carries. The markers are eliminated later via `/clarify`.

## §2 The two-condition test — what you may settle on your own

An open item is yours to settle only when **(1) it is cheap to undo** *and* **(2) it stays inside the spec already fixed**. This is a logical AND: cheap to undo but outside the spec is a marker, inside the spec but expensive to undo is a marker. The user's own words for the rule — "되돌리기 쉬운거 + 처음에 정한 스펙 이내이면 정하기". It is the same line CLAUDE.md '복창과 검증' draws for a 갈림길 (되돌리기 쉬우면 선언 후 진행, 파괴적·비가역이면 질문); unattended mode only changes what "질문" turns into when nobody is there to answer — the §3 marker.

- **"The spec already fixed"** means, cumulatively, whatever the user has fixed *in this run*: the opening statement (the idea/topic sentence the skill started from) plus every answer already given in the earlier rounds — and, once the skill's plan document exists, that document including its "이번 범위 밖" section. Nothing else counts as spec: not what a similar product usually ships with, not what would be nice to have. Each skill names its own opening statement and plan document.
- **"Cheap to undo"** means a later correction rewrites a passage, not the plan. Treat as expensive — hence a marker — anything the later rounds and stages branch on, anything that spends money or leaves a trace outside the repo, and anything that would force a completed stage to be redone. Each skill lists its own branch-deciding answers.
- **A passing item is closed on its default and recorded, not marked.** Do not manufacture a marker for it — a marker means an open question, and an item you settled is not open. Write the chosen default where it applies in the draft, keep it out of the marker count, and add it to the same one-line report as an assumption count ("가정으로 닫은 항목 M건").

## §3 The `[확인 필요]` marker and the 잠정본 line

Any material decision still open after the rounds is marked in the draft with the literal `[확인 필요: <질문 한 줄>]` tag (the colon is part of the machine contract — `/clarify`'s exact form), one marker per open item, and eliminated via `/clarify` before the next stage. A draft with a remaining tag does not move on — **except in unattended draft mode (§1)**, where the draft moves on carrying a 잠정본 line as the **first line of the file**, above the title, in exactly this form — `> 잠정본 — 확인 필요 <N>건` (`N` is the marker count in that file as a plain decimal; the line is a blockquote so it survives rendering). One such line per file, never two. Rewrite `N` whenever the count changes, and delete the line when `/clarify` brings the count to 0 — a file with the line and no marker is a bug, and so is a marker with no line.

**That exception buys stages, not delegation.** It covers moving between the skill's stages, and delegating the document/mockup/deliverable writes that carry the markers through verbatim. It never covers delegating implementation off the draft: before any implementation is delegated, the marker count must be 0, exactly as CLAUDE.md requires. Settling items under §2 does not lower that bar either — it only keeps the draft moving; whatever markers remain must still reach 0 before any implementation is delegated.

## §4 Per-stage commits & batch push

Commit each stage's deliverable as soon as it is complete — each deliverable gets its own commit (each skill lists its own deliverables). In orchestrator mode the commit, like the file writes, is delegated to the implementer subagent. Do not push per stage; push the accumulated commits in one batch at the skill's close. **If there is no `origin` remote, skip the push** — a derived project often has only `template`, because SETUP's agent default skips `gh repo create`. Check with `git remote` before pushing; when origin is absent, leave the commits local, report one line ("origin이 없어 push를 건너뛰었다 — 커밋은 로컬에 남아 있다"), and do not create a remote just to satisfy this step.

## §5 Same-turn chaining into the next phase

When the skill's closing approval arrives, enter the next phase **in the same turn**. There is no path where the close report says "다음은 X" and the turn ends — announcing it and closing the turn is exactly the failure this rule removes. Rationale, in one line: work that can start now is not closed with an announcement (the same line `.claude/agents/orchestrator.md` draws). The turn stops at the next phase's **first user-approval question** — not before it and not past it. No approval gate disappears in this chaining: the closing gate and the next phase's first gate both still ask, even in unattended mode; the only thing removed is the idle wait between them.

- The one exception: if the user has explicitly asked to stop here (e.g. "여기까지만"), do not enter the next phase — report and end the turn.
