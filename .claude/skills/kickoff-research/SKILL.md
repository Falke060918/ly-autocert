---
name: kickoff-research
description: Research kickoff process that develops an investigation topic into a research plan → goals → report skeleton, then runs per-question parallel investigation, ending at report approval. Use when starting research/investigation work — when `.claude/identity` carries `purpose: research`, or the request itself is investigation in nature (조사·리서치·비교 분석·시장/기술 조사 등), or the user has just answered SETUP's closing purpose+idea question (SETUP.md §9's 2단 질문) with 조사·리서치. Not for repos whose `.claude/identity` declares `identity: template` (the template original itself — kickoff-style planning is disabled there). For product-building requests route to `kickoff`, for document-writing requests to `kickoff-docs`.
---

# /kickoff-research — from question to report

Research topic provided by the user: $ARGUMENTS
(If empty, first ask for the topic in one line)

All user-facing questions, notifications, and reports in this flow are written in the user's language (Korean). Documents the user reads (docs/01-plan.md, docs/02-goals.md, the report) are also written in Korean.

Proceed through the stages in order (1 → 2 → 3 → 4 → 5). Each stage's output must be left as a file.
`/kickoff-research` ends at report approval. There is no `/architect` phase and no market-research stage — kickoff's Stage 1.5 exists to inform a product plan, and here investigation *is* the deliverable, so a separate market-research step would duplicate the research body. Throughout this flow, do not write project code: the deliverables are documents only (`docs/` and the report file).
If the user instructs skipping a stage, skip it.

## When entered via auto-trigger
Auto-trigger follows the identity 분기 in `CLAUDE.md`: it fires only when `.claude/identity` declares `identity: project` and the request is investigation in nature — `purpose: research` in `.claude/identity` makes this the default route, and a research-natured request routes here even without the key (the key is the default route, not a cage; a request whose nature contradicts the stored purpose follows the request). One-off lookups a single explorer can answer, informational questions, and an explicit "그냥 바로 해" do not trigger it — this skill is for research that deserves a plan, not for every question.
Even on auto-trigger, the stage procedure is exactly the same as an explicit invocation. The user's first request sentence substitutes for `$ARGUMENTS`.
**A second auto-trigger source: the answer to SETUP's closing purpose+idea question.** When `SETUP.md` §9's 2단 질문 comes back with 조사·리서치, the idea answer substitutes for `$ARGUMENTS` and this skill fires in the same turn — the user never types a slash command. On this path skip the one-line notice below — the SETUP question already announced that planning starts here.
Before the stage-1 questions, send a one-line notice first — "조사 작업으로 보여 조사 기획 단계부터 시작한다. 바로 조사만 원하면 말해라." This is not a confirmation request, so do not wait for an answer; move straight to AskUserQuestion. If the user chooses to skip, stop right there and do not suggest it again in that session.

Note: if the main session is in orchestrator mode (no write tools), delegate file creation/modification to the implementer subagent.
The main session writes the document contents (plan, goals, report skeleton) itself and puts the full text into the delegation prompt. Investigation goes to **explorer** subagents (read-only research); writing findings into the report goes to the **implementer**.

## Per-stage commits & push
Protocol: `.claude/conventions/kickoff-common.md` §4. This skill's bindings: the deliverables are docs/01-plan.md, docs/02-goals.md, the report skeleton, and the filled report — each gets its own commit — and the batch-push point is this skill's close (report approved), under §4's `origin`-absent guard, not blind.

## Stage 1 — Concretize (multi-round questions)
Ask in rounds, one AskUserQuestion call per round (max 4 questions each, 2–4 options each, mark a recommended option). Tell the user that every answer in these rounds is reflected into docs/01-plan.md.
**[2계층(범위·목적)]** 이 라운드는 무인 모드(`/automode on`)에서도 그대로 묻는다 — 무엇을 조사하는가는 모드와 무관하게 사람과 같이 정한다. 바로 아래 unattended draft mode는 물을 수단이 아예 없을 때의 경로라 별개다(능력이 정책을 이긴다). 계층 정의는 `.claude/agents/orchestrator.md`.

**Unattended draft mode — when the answers do not come.** The mode, its two-condition test, and the marker/잠정본 forms have their single source in `.claude/conventions/kickoff-common.md` §1–§2. This skill's bindings for §2's two abstract terms:
- **"The spec already fixed"** is, here: the topic statement (`$ARGUMENTS`, or the first request sentence on auto-trigger) plus every answer already given in the earlier rounds — and, once Stage 2 has written it, docs/01-plan.md including its "이번 범위 밖" section.
- **Expensive — hence a marker** — is, here: anything the later stages branch on (the core-question list, the decision the research serves, the report format), anything that spends money or leaves a trace outside the repo (유료 자료·유료 API 조사), and anything that would force a completed stage to be redone.

Stage 1 is this skill's ambiguity-elimination pass — the `[확인 필요: <질문 한 줄>]` marker, the 잠정본 first-line format, and the "exception buys stages, not delegation" rule are all `.claude/conventions/kickoff-common.md` §3. Here, "the next stage" the marker gate blocks is Stage 2; the unattended exception covers moving between stages 1–4 and delegating the stage 2–4 document writes that carry the markers through verbatim; before the Stage 5 per-question investigation is delegated, the marker count must be 0 — Stage 5 is this flow's implementation analog, and the gate is unchanged where it actually bites.

**Round 1 — core questions & decision purpose:**
- The core questions to answer (compress to 5 or fewer; each becomes a report section and an investigation unit)
- Decision purpose — 이 조사로 무슨 결정을 하나 (도입 여부, 방향 선택, 예산 판단 등). This is the yardstick every finding is judged against
- Who consumes the answer, and by when (마감이 있으면 조사 깊이가 달라진다)

**Round 2 — source strategy:**
- Source types to use — 웹 검색 / 공식 문서·스펙 / 저장소·코드 / 사내 자료(연결된 소스) — and any sources to avoid
- Credibility bar — 1차 출처 필수인가, 2차 요약으로 충분한가; how fresh must the data be (최신성 기준)
- Constraints — 유료 자료·API 사용 여부, 마감, 법적·기밀 제약

**Round 3 — report format:**
- Report shape — 질문별 섹션 / 비교표 중심 / 요약+부록 (mark one as recommended based on the questions)
- Depth per question — 한 단락 요약인가, 근거·출처를 단 상세 분석인가
- Where the report lives (default: `docs/research/<주제>.md`) and its language

## Stage 2 — Research plan: docs/01-plan.md
Write with this structure:
- Decision purpose & core questions (each question gets an ID in the `RQ-<번호>` form, e.g. RQ-01). One line per question in this format:
  `RQ-01 | 질문: 경쟁 도구 3종의 가격 구조는 | 판단 연결: 도입 예산 결정 | 출처: 공식 가격 페이지 1차 | 완료 조건: 3종 모두 최신 가격표 확보`
  These IDs are the traceability spine: Stage 5's investigation units and the report's sections reference them, and completion is verified against their 완료 조건
- Source strategy (types, credibility bar, freshness) — per question where they differ
- Report format decision (shape, depth, location) from Round 3
- Out of scope this time (exclusions — must be explicit; questions the user deferred or excluded go here)

## Stage 3 — Goals: docs/02-goals.md
- Decision purpose (1 sentence — what will be decided with this research)
- Success criteria (3–5 measurable items; where a criterion maps to a core question, reference its ID in parentheses, e.g. "(RQ-01)")
- Milestones: M1 report skeleton approved → M2 all questions investigated → M3 report approved (state each milestone's completion condition)

After writing, report a summary of the plan and goals to the user and confirm there are no objections. **[2계층(범위·목적)]** 이 이견 확인은 무인 모드에서도 그대로 묻는다 — 목표 문서의 승인이다.

## Stage 4 — Report skeleton (deliverable sample)
This is the flow's sample-approval gate — the counterpart of kickoff's mockup. Write the report file at the Stage-2 location as a **skeleton**: the full heading tree with every section **empty**, plus one example table or example finding block filled with dummy data so the user sees the shape of the final report before any investigation is spent.
- A `전제:` section first, ahead of every RQ section — the premises the report rests on, each line with its source tag. The rule (tags, `전제: 없음`, conditional conclusions) lives in `.claude/agents/explorer.md` and is not repeated here; in the skeleton the section stands empty like the others
- One section per core question, titled with its ID and question (`## RQ-01 — <질문>`), plus 요약 and 결론·판단 근거 sections
- The example table/block must not pass for a real finding — label it "예시" visibly, and anything still carrying a `[확인 필요: ...]` marker in the plan stays visibly unresolved in the skeleton too (carry the marker through verbatim, never fill it with a plausible-looking value)
- Repeat user feedback → skeleton fixes until approval comes. If the user says "그냥 진행", treat it as approval. **[2계층(범위·목적)]** 뼈대 승인은 무인 모드에서도 그대로 묻는다

## Stage 5 — Per-question parallel investigation
After skeleton approval (M1), decompose by core question and investigate — no `/architect` in between: the RQ list *is* the decomposition, so a separate design phase would add a layer with nothing to decide.
1. **One explorer per core question, delegated in parallel** (read-only research; group trivially small questions, but never split one question across two explorers). Each delegation carries: the question's RQ line from docs/01-plan.md verbatim, the source strategy for that question, and the 완료 조건. Receive summaries with sources — not raw page dumps.
2. Delegate the write-back to the implementer: fill each report section from its explorer summary, findings cited to their sources, each section closed against its RQ's 완료 조건. A question whose 완료 조건 could not be met is reported as such in its section — never padded to look answered. The report's `전제:` section is filled by carrying each explorer's `전제:` lines over with their source tags intact — not rewritten.
3. Write the 요약 and 결론·판단 근거 sections last, answering the decision purpose from Stage 3 — findings first, then what they mean for the decision.
4. Report to the user and iterate fixes until report approval. **[2계층(범위·목적)]** 보고서 승인은 무인 모드에서도 그대로 묻는다 — M3 게이트다.

## Close — after report approval
When the user approves the report, `/kickoff-research` ends.
1. Push the accumulated per-stage commits in one batch, under the `origin`-absent guard — protocol: `.claude/conventions/kickoff-common.md` §4
2. Update docs/STATE.md in `/handoff` format (done: plan, goals, report approved / next: the user's decision, or `/kickoff` if chained below)
3. Report to the user, and the report must include this line:
   - Report approval is approval of **the findings and their presentation only** — it does not decide the question the research was for; that decision stays the user's.
4. **Then, in the same turn, ask the sequential-chain question**: "이 결과로 개발(`/kickoff`)을 시작할까?" — this handles the 순차형 복합 목적 (조사해서 개발) without a combined-purpose skill. Same-turn chaining per `.claude/conventions/kickoff-common.md` §5, including its "여기까지만" exception: here the next phase's first user-approval question *is* this chain question — the turn stops there, not before it and not past it, and it still asks even in unattended mode. If the answer is yes, enter `/kickoff` in the same turn with the research topic and report as context; if no, end the turn.
