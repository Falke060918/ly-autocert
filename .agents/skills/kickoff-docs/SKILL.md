---
name: kickoff-docs
description: "Plan a document project, establish scope and goals, obtain feedback on an outline and sample, then write and review sections. Use when the user wants to start a substantial document or a derived project has purpose docs."
---

먼저 .codex/conventions/codex-runtime.md를 읽는다. 사용자 승인 범위와 현재 도구 구성이 우선한다.

# $kickoff-docs — from document idea to approved sample

Document idea provided by the user: 사용자가 스킬 이름 뒤에 적은 요청
(If empty, first ask in one line what document to write)

All user-facing questions, notifications, and reports in this flow are written in the user's language (Korean). Documents the user reads (docs/01-plan.md, docs/02-goals.md, STATE.md entries) are also written in Korean.

Proceed through the stages in order (1 → 2 → 3 → 4). Each stage's output must be left as a file.
`$kickoff-docs` ends at sample approval + plan approval. The final 목차 design and the actual section writing belong to the decomposition entry after close, not here.
Throughout this flow, do not write the actual deliverable document. The sample section goes only inside `mockup/` — the same isolation line kickoff draws for its HTML mockup: the sample exists to be judged, not to be the deliverable. On approval the implementer writing that section may reuse its text verbatim.
If the user instructs skipping a stage, skip it.

## When entered via auto-trigger
Auto-trigger fires only when `.codex/identity` declares `identity: project` and the routing in the frontmatter description points here — `purpose: docs`, a document-writing request, or SETUP §9's 문서 작성 answer. One-off text fixes ("이 문단 고쳐줘"), informational questions, and an explicit "그냥 바로 해" do not trigger it. The stage procedure is exactly the same as an explicit invocation; the user's first request sentence (or the SETUP idea answer) substitutes for `사용자가 스킬 이름 뒤에 적은 요청`.
On the SETUP path, kickoff-docs fires in the same turn as the answer and skips the one-line notice below — the SETUP question already announced that planning starts here.
Otherwise, before the stage-1 questions, send a one-line notice first — "문서 작업으로 보여 기획 단계부터 시작한다. 바로 집필을 원하면 말해라." This is not a confirmation request, so do not wait for an answer; move straight to AskUserQuestion. If the user chooses to skip, stop right there and do not suggest it again in that session.

Codex에서는 독립 작업은 위임하고, 작은 작업은 제공된 편집 도구로 직접 수행할 수 있다.

## Per-stage commits & push
Protocol: `.codex/conventions/kickoff-common.md` §4. This skill's bindings: the deliverables are docs/01-plan.md, docs/02-goals.md, and mockup/sample-section.md — each gets its own commit — and the batch-push point is close (sample approved), under §4's `origin`-absent guard.

## Stage 1 — Concretize (rounds)
Ask in rounds, one AskUserQuestion call per round (max 4 questions each, 2–4 options each, mark a recommended option). Tell the user that every answer is reflected into docs/01-plan.md.
**[2계층(범위·목적)]** 이 라운드는 무인 모드(`$automode on`)에서도 그대로 묻는다 — 무엇을 쓰는가는 모드와 무관하게 사람과 같이 정한다.

**Unattended draft mode — when the answers do not come.** The mode, its two-condition test, and the marker/잠정본 forms have their single source in `.codex/conventions/kickoff-common.md` §1–§3. This skill's bindings for §2's two abstract terms:
- **"The spec already fixed"** is: the document idea (`사용자가 스킬 이름 뒤에 적은 요청`, or the first request sentence on auto-trigger) plus every answer already given in the earlier rounds — and, once Stage 2 has written it, docs/01-plan.md including its "이번 범위 밖" section.
- **Expensive — hence a marker** — is: the branch-deciding answers below (독자·목적·형식 — the whole 목차 and tone hang on them), anything that spends money or leaves a trace outside the repo (외부 공개·발송이 걸린 문서), and anything that would force a completed stage to be redone.

The `[확인 필요: <질문 한 줄>]` marker and the 잠정본 first-line format are §3. In this skill, "the next stage" the marker gate blocks is Stage 2; the unattended exception covers moving between stages 1–4 and delegating the stage 2–4 document writes that carry the markers through verbatim; before any section writing is delegated (the decomposition entry after close), the marker count must be 0, exactly as AGENTS.md requires.

**Round 1 — reader & purpose:**
- Who reads it (독자) — 의사결정자 / 실무자 / 일반 대중 / 내부 기록용, and how much they already know
- Document purpose — 설득 (행동·결정을 끌어낸다) / 설명 (이해시킨다) / 기록 (남긴다). This answer decides tone, structure, and what "success" means in Stage 3
- What single change or takeaway the reader should leave with (한 문장)

**Round 2 — scope & format:**
- Scope — what the document covers, and just as explicitly what it does not (이 답이 "이번 범위 밖" 절이 된다)
- Format & length — 보고서 / 제안서 / 매뉴얼 / 백서 / 블로그 글 등, and a rough length band
- Source material — what exists to write from (자료·데이터·기존 문서), and what is missing
- Constraints — 마감, 문체·용어 규정, 공개 범위

## Stage 2 — Plan: docs/01-plan.md
Write with this structure:
- Reader & purpose (from Round 1 — 독자, 설득/설명/기록, the one-sentence takeaway)
- **Draft 목차** — top-level sections with one line each on what that section must accomplish for the reader (not what it contains — what it does)
- Source material per section — what feeds it, and a `[확인 필요: ...]` marker where the material is missing
- Out of scope this time (이번 범위 밖 — must be explicit, from Round 2's scope answer)

## Stage 3 — Final goals: docs/02-goals.md
- Final goal (1 sentence — what the document makes the reader do or understand)
- Success criteria (3–5 verifiable items — e.g. "의사결정자가 §3만 읽고 예산 규모를 말할 수 있다", not "잘 쓰여 있다")
- Milestones: M1 sample approved → M2 all sections drafted → M3 document assembled and reviewed (state each milestone's completion condition)

After writing, report a summary of the plan and goals to the user and confirm there are no objections. **[2계층(범위·목적)]** 이 이견 확인은 무인 모드에서도 그대로 묻는다 — 목표 문서의 승인이다.

## Stage 4 — Sample: 목차 + 샘플 절 1개
This is the HTML-mockup replacement: tone and depth are approved on a real artifact, not on adjectives.
- Write `mockup/sample-section.md`: the full 목차 as it now stands, followed by **one representative section written out in full** — pick the section that best exposes the document's tone, depth, and evidence style (usually the hardest or most central one, not the introduction)
- The sample is written at deliverable quality — it is what the user is approving, and its text may be reused verbatim after approval
- Whatever still carries a `[확인 필요: ...]` marker in the plan stays visibly unresolved in the sample too — carry the marker through verbatim instead of inventing a plausible value
- Repeat user feedback → sample fixes until approval comes. If the user says "그냥 진행", treat it as approval. **[2계층(범위·목적)]** 표본 승인은 무인 모드에서도 그대로 묻는다

## Close — after sample approval
When the user approves the sample, `$kickoff-docs` ends. Do **not** write the remaining sections here — that is the decomposition entry's job.
1. Push the accumulated per-stage commits in one batch, under the `origin`-absent guard — protocol: `.codex/conventions/kickoff-common.md` §4
2. Update docs/STATE.md in `$handoff` format (done: plan, goals, sample approved / next: 목차 설계 → 절 단위 집필)
3. Report to the user, and the report must include this line:
   - Sample approval is approval of **tone, depth, and the draft 목차 direction only** — the final 목차 and each section's content are designed and written in the next phase, section by section.
4. **Then, in the same turn, enter the decomposition** — same-turn chaining per `.codex/conventions/kickoff-common.md` §5, including its "여기까지만" exception. There is no `$architect` here; the next phase is: **planner designs the final 목차** (section list, per-section goal and source, writing order by dependency) → the orchestrator decomposes it into **per-section implementer writing units**. The turn stops at the next phase's first user-approval question — the planner's **집필 계획(최종 목차) 승인** — not before it and not past it; that gate still asks even in unattended mode.

### 순차형 복합 연쇄 — when the document itself is done
When the deliverable document is complete (all sections written and assembled — after the decomposition phase, not at sample approval), ask the user once: **"이 결과로 개발(kickoff)을 시작할까?"** — one AskUserQuestion, yes/no. This is how a sequential compound purpose ("문서로 정리한 뒤 개발") is handled: on yes, enter `$kickoff` in the same turn with the finished document as the idea base (its summary substitutes for `사용자가 스킬 이름 뒤에 적은 요청`); on no, close normally. There is no combination-specific skill — this one chain question is the whole mechanism.
