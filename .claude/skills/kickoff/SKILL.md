---
name: kickoff
description: "Project kickoff process that develops an idea into a plan → final goals → HTML mockup, ending at product-scope + mockup approval. Technical design and implementation start afterwards with /architect. Use when starting a new project or when the user brings a new idea. Even without an explicit invocation, use it automatically when a new-product request like \"~를 만들어줘\" arrives in a repo that has no `docs/02-goals.md`, or when the user has just answered SETUP\u0027s closing purpose+idea question (SETUP.md §9\u0027s 2단 질문) with a purpose that routes here. Not for repos whose `.claude/identity` declares `identity: template` (the template original itself — kickoff-style product planning is disabled there). Also not for document-writing or research purposes — when `.claude/identity` carries `purpose: docs` or `purpose: research`, or the request itself is document-writing or investigation in nature, route to `kickoff-docs` / `kickoff-research` instead."
---

# /kickoff — from idea to mockup

Idea provided by the user: $ARGUMENTS
(If empty, first ask for the idea in one line)

All user-facing questions, notifications, and reports in this flow are written in the user's language (Korean). Documents the user reads (docs/01-plan.md, docs/02-goals.md, STATE.md entries) are also written in Korean.

Proceed through the stages in order (1 → 1.5 → 2 → 3 → 4). Each stage's output must be left as a file.
`/kickoff` ends at mockup approval + product-scope approval. Scaffolding, feature decomposition, issue creation, and implementation all belong to `/architect`, not here.
Throughout kickoff, do not write actual project code. Mockups go only inside `mockup/`.
If the user instructs skipping a stage, skip it.

## When entered via auto-trigger
Auto-trigger follows the identity 분기 in `CLAUDE.md`: it fires only when `.claude/identity` declares `identity: project`, `docs/02-goals.md` does not exist, and the request is a new-product request. One-off fixes/bugs/refactors, informational questions, and an explicit "그냥 바로 해" do not trigger it. This section covers execution only.
**Read `purpose:` from `.claude/identity` before firing.** `purpose: docs` routes to `kickoff-docs` and `purpose: research` to `kickoff-research` — this skill does not fire there. `dev`, `test`, `mixed`, and an absent key all stay here (test·복합 have no variant yet — for those two, say so in one line and run this default flow). A request whose nature contradicts the stored purpose follows the request, not the key — the key is the default route, not a cage.
Even on auto-trigger, the stage procedure is exactly the same as an explicit invocation. The user's first request sentence substitutes for `$ARGUMENTS`.
**A second auto-trigger source: the answer to SETUP's closing purpose+idea question.** `SETUP.md` §9 ends the setup turn with a 2단 질문 — a purpose choice plus "무엇을 만들까(다룰까)? 한 줄로" — and records the purpose into `.claude/identity`. When the purpose routes here, the idea answer substitutes for `$ARGUMENTS` and kickoff fires in the same turn. On this default path the user never types a slash command; explicit `/kickoff` remains available as before. On this path skip the one-line notice below — the SETUP question already announced that planning starts here.
Before the stage-1 questions, send a one-line notice first — "새 프로젝트로 보여 기획 단계부터 시작한다. 바로 구현을 원하면 말해라." This is not a confirmation request, so do not wait for an answer; move straight to AskUserQuestion. If the user chooses to skip, stop right there and do not suggest it again in that session.

Note: if the main session is in orchestrator mode (no write tools), delegate file creation/modification to the implementer subagent.
The main session writes the document contents (plan, goals) itself and puts the full text into the delegation prompt. For the mockup HTML, delegate with the requirements written out in detail.
**The mockup goes to the implementer, never the planner** — it lands in `mockup/`, outside `docs/`, and needs its own commit, while the planner writes only inside the `docs/` tree and has no Bash to commit with.

## Per-stage commits & push
Protocol: `.claude/conventions/kickoff-common.md` §4. Kickoff's bindings: the deliverables are docs/01-plan.md, docs/02-goals.md, and mockup/ — each gets its own commit — and the batch-push point is kickoff close (mockup approved), under §4's `origin`-absent guard, not blind.

## Stage 1 — Concretize (branching multi-round questions)
Ask in rounds, one AskUserQuestion call per round (max 4 questions each, 2–4 options each, mark a recommended option). Tell the user that every answer in these rounds is reflected into docs/01-plan.md.
**[2계층(범위·목적)]** 이 라운드는 무인 모드(`/automode on`)에서도 그대로 묻는다 — 무엇을 만드는가는 모드와 무관하게 사람과 같이 정한다. 바로 아래 unattended draft mode는 물을 수단이 아예 없을 때의 경로라 별개다(능력이 정책을 이긴다). 계층 정의는 `.claude/agents/orchestrator.md`.

**Unattended draft mode — when the answers do not come.** The mode, its two-condition test, and the marker/잠정본 forms have their single source in `.claude/conventions/kickoff-common.md` §1–§2. One kickoff-specific rider: the platform/stack rule below still holds in that mode — an unanswered platform requirement becomes a marker, never a stack pick. Kickoff's bindings for §2's two abstract terms:
- **"The spec already fixed"** is, in kickoff: the idea statement (`$ARGUMENTS`, or the first request sentence on auto-trigger) plus every answer already given in the earlier rounds — and, once Stage 2 has written it, docs/01-plan.md including its "이번 범위 밖" section.
- **Expensive — hence a marker** — is, in kickoff: anything the later rounds and stages branch on (project nature 개인용/사업용, which gates Round 4 and Stage 1.5; the MVP feature set; platform requirements; the revenue model), anything that spends money or leaves a trace outside the repo (the 4-0 (B) asset generation), and anything that would force a completed stage to be redone.

Stage 1 is kickoff's ambiguity-elimination pass — it uses the `/clarify` skill's mechanism: the `[확인 필요: <질문 한 줄>]` marker, the 잠정본 first-line format, and the "exception buys stages, not delegation" rule are all `.claude/conventions/kickoff-common.md` §3. In kickoff, "the next stage" the marker gate blocks is Stage 2; the unattended exception covers moving between stages 1–4 and delegating the stage 2–4 document/mockup writes that carry the markers through verbatim; before any implementation is delegated (from `/architect` onward), the marker count must be 0, exactly as CLAUDE.md requires — the gate is unchanged where it actually bites.

**Stage 1 does not choose a platform or tech stack.** It only confirms platform **requirements and constraints** (꼭 모바일이어야 하는가, 오프라인에서도 돌아야 하는가 등); the stack decision itself is deferred to `/architect`'s alternative comparison.

**One exception — the feasibility gate.** *Choosing* a stack and *screening out* a requirement that no stack can deliver are two different acts, and only the second one belongs in Stage 1. The gate asks a single question — "is this buildable under any stack at all?" — and never names, compares, or ranks one; a requirement with at least one workable answer passes through untouched and still undecided. The rule above stays whole: comparison and selection remain `/architect`'s.
- **When it runs** — right after Round 2, before Stage 1.5. It reads exactly two things: the platform requirements from Round 1 and the MVP feature set + constraints from Round 2. Running it here is the whole point of the exception; `/architect` would reach it only after the plan and the goals have already promised the thing.
- **What it catches** — a requirement impossible under *every* stack, or impossible under every stack within the constraints the user just fixed in Round 2 (예산, 마감, 법적 제약). Typical shapes: it needs a platform API that does not exist or is closed to third parties; it needs data nobody may lawfully hand over; it needs an accuracy or latency level no shipping product has reached. "React Native보다 네이티브가 낫다", "서버리스면 비용이 더 든다" is a stack comparison, not this gate — the gate catches nothing there. **Uncertainty passes**: unless you can state why it is impossible, it goes through.
- **How a catch is closed** — never silently into Stage 2, and never by picking a stack to rescue it. Tell the user in one line what is impossible and why, then close it inside that same round as **축소** (cut the requirement down to a buildable core), **대안** (a different requirement that reaches the same goal), or — when no answer comes, i.e. unattended draft mode above — the literal `[확인 필요: <질문 한 줄>]` marker, counted in the 잠정본 line like any other marker. A 축소·대안 answer then flows into Stage 2 as an ordinary Round 2 answer, and whatever was cut belongs in the plan's "이번 범위 밖" section. **[2계층(범위·목적)]** 이 축소·대안 질문은 무인 모드에서도 그대로 묻는다 — 요구를 깎는 것은 범위를 바꾸는 일이다.

**Round 1 — nature & problem:**
- Project nature — 개인용 (hobby/personal use) vs 사업용 (monetization intent). This answer decides whether Round 4 and the market-research step run
- Target users
- Core problem to solve (pain point)
- Platform requirements & constraints (requirements only — e.g. 반드시 모바일인가, 오프라인 동작이 필요한가; not a stack choice)

**Round 2 — features & constraints:**
- MVP must-have features (compress to 3 or fewer)
- Core user journeys that must be supported (반드시 지원할 핵심 사용자 여정)
- Post-MVP candidate features — put each candidate to the user **one by one** as 진행 / 보류 / 제외, never as one "무엇부터 만들까" ranking question. The 진행 set is the roadmap, and its internal order comes from dependencies, not from a ranking answer (`CLAUDE.md` '작업 전 정리본')
- Constraints — deadline, 유료 API·인프라 예산, 기술·법적 제약

**Round 3 — implementation-shaping facts:**
These answers steer `/architect`'s design, so collect them here even if the user is unsure (an "아직 모름 — /architect에서 결정" option is allowed per question).
- Existing systems/data to connect to (기존 시스템·데이터 연결 여부)
- Accounts & data — 로그인 필요 여부와 권한 수준, 저장할 데이터와 민감정보 범위
- Deployment & operations — 배포 환경, 운영 주체, 예상 규모
- Decision scope — 사용자가 직접 결정할 사항 vs Claude에 위임할 사항

**Round 4 — revenue model (사업용 only):**
Run only when Round 1 answered 사업용; for a personal project skip this round entirely.
- Billing model (구독 / 일회성 / 광고 / 프리미엄 etc.)
- Who converts to paid (유료 전환 대상)
- Rough price range (가격대 감)

## Stage 1.5 — Market research (question + web research)
Runs after Stage 1, before Stage 2. Skippable: if it is a personal hobby project and the user chooses "시장조사 불필요", skip this whole section.
**A marker on the prerequisite answer means this section does not run.** If Round 1's 개인용/사업용 answer is still a `[확인 필요: ...]` marker, do not research and do not guess the nature — this is the same line Round 4 draws above ("Run only when Round 1 answered 사업용"): a step gated on that answer does not fire while the answer is a marker. Leave the plan's market-research section as its own `[확인 필요: <질문 한 줄>]` marker, counted in the 잠정본 line like any other, and go on to Stage 2.
1. Ask the user which competing services or similar products they already know (AskUserQuestion, include a "시장조사 불필요" option for personal projects). **[2계층(범위·목적)]** 무인 모드에서도 그대로 묻는다.
2. Based on the answers, delegate a **web-search investigation to the explorer subagent**: competing services, current market snapshot, and differentiation points. Receive a summary only.
3. Attach the summary to docs/01-plan.md (it feeds the "타겟 고객/시장조사" section in Stage 2).

## Stage 2 — Plan: docs/01-plan.md
Write with this structure:
- Problem definition & pain points (may be merged into one section)
- Target users / market research (competing services and differentiation, from Stage 1.5 if run)
- Core features (marking MVP status; order them by dependency, never by a ranking answer — Round 2 collects 진행/보류/제외 per candidate, not a priority ranking) — **give every core feature a requirement ID and acceptance criteria.** IDs use the `REQ-<영역>-<번호>` form (e.g. REQ-AUTH-01) and acceptance criteria must cover both the normal path and the exception path. One line per feature in this format:
  `REQ-AUTH-01 | 요구: 이메일 로그인 | 승인 조건: 올바른 자격증명이면 대시보드로 이동 | 예외: 5회 실패 시 잠금 안내 | 검증: 수동/E2E | 구현 이슈: (미정 — /architect에서 연결)`
  These IDs are the traceability spine: `/architect` issues reference them, and completion is verified against their acceptance criteria
- Feature/implementation roadmap (MVP → later stages) — the 진행 set from Round 2, sequenced by dependency; 보류 items get their own list, 제외 items are not carried into this document
- Revenue model (사업용 only — omit the section for personal projects)
- Non-functional requirements (performance, security, etc. where applicable)
- Out of scope this time (exclusions — must be explicit)

## Stage 3 — Final goals: docs/02-goals.md
- Final goal (1 sentence)
- Success criteria (3–5 measurable items; where a criterion maps to a core feature, reference its requirement ID in parentheses, e.g. "(REQ-AUTH-01)")
- Milestones: M1 mockup approved → M2 MVP working → M3 complete (state each milestone's completion condition)

After writing, report a summary of the plan and goals to the user and confirm there are no objections. **[2계층(범위·목적)]** 이 이견 확인은 무인 모드에서도 그대로 묻는다 — 목표 문서의 승인이다.

## Stage 4 — Mockup: mockup/index.html

### 4-0. Mockup approach selection (landing-type projects only)
Run this subsection only when the nature identified in stages 1–2 is a **landing page, marketing site, solution/product intro page, or one-page product intro**.
For dashboards, CLIs, internal tools, or admin screens, go **straight to 4-1 without asking**.
Also go to 4-1 without asking for — DOM/SVG scroll interactions, real-time 3D, and jobs that only add animation to an existing page.

If it applies, settle the mockup approach with **one** AskUserQuestion (3 options, mark A as recommended):
**[2계층(범위·목적) + 3계층(멈춤선)]** 무인 모드에서도 그대로 묻는다 — (B)는 유료 크레딧을 쓰므로 비용 관문까지 겹친다.
- **(A) 정적 HTML 목업** — description: "외부 의존성 없는 단일 HTML 파일. 추가 도구·비용 0. 4-1을 그대로 진행한다."
- **(B) scroll-world 몰입형 랜딩** — description: "AI로 씬 이미지와 카메라 이동 영상을 미리 렌더링해 스크롤 위치에 비디오 재생 헤드를 물리는 원페이지. Higgsfield 유료 크레딧 필요. ffmpeg·Python3+Pillow 필요. 산출물은 mp4/webp 정적 자산 + 바닐라 JS라 프레임워크 비종속."
- **(C) React+shadcn 목업** — description: "Vite+React+shadcn에 랜딩 블록(Magic UI·Tailark — `.claude/rules/frontend.md` §9)을 조립한 목업. 목업은 모양·흐름의 참고물이다 — 구현은 `/architect`에서 새로 설계해 처음부터 만든다. 로컬 dev 서버로 확인해야 하고(더블클릭 열기 없음) Node가 필요하다."

If B is chosen:
1. If the plugin is not installed, the orchestrator cannot install it directly, so show the user these two lines and wait.
   ```
   /plugin marketplace add oso95/scroll-world
   /plugin install scroll-world@scroll-world
   ```
2. If any prerequisite (Higgsfield CLI auth + credit balance, ffmpeg/ffprobe, Python3 + Pillow) is missing, **fall back to A** and tell the user in one line what was missing that caused the fallback.
3. Asset generation is expensive — per N scenes: N image calls + 2N-1 video calls, and adding the mobile 9:16 chain doubles the video calls. Therefore run it **only after the stage-3 goals/concept are confirmed**.
4. Outputs (videos, images, engine JS) go only inside `mockup/`. Still no actual project code during kickoff.
5. The M1 (mockup approval) gate applies exactly as with A — iterate on fixes until approval.

If C is chosen:
1. During kickoff the code still goes only inside `mockup/` — the "no actual project code" rule holds. The approved mockup code is **never promoted** into the implementation tree: it stays in `mockup/` as a look-and-flow reference, and `/architect` designs and builds the implementation from scratch (2026-09-07 사용자 재결정 — see `.claude/skills/architect/SKILL.md`).
2. The forced defaults bind exactly as in 4-1 — fonts (`.claude/rules/fonts.md`), on-screen text, tables, and flow (`.claude/rules/frontend.md` §6–§8) — and the approval-gate flow check below applies unchanged.
3. If Node/npm is missing, **fall back to A** and tell the user in one line what was missing that caused the fallback.
4. The M1 (mockup approval) gate applies exactly as with A.

### 4-1. Static HTML mockup (default)
- A single static HTML+CSS file with no external dependencies (no CDNs; styles in an inline `<style>`) — **exactly three exceptions: the font CDN links, the Basecoat CDN, and the lucide SVG icons** below. This list is the single source of the CDN exception count
- Fonts are a forced default, not a choice: apply the Geist + Pretendard stacks from `.claude/rules/fonts.md` (CDN `<link>`s at the very top of `<head>`, stacks in the inline `<style>`). No OS-default fallback in the stacks. Deviate only on an explicit user instruction
- **Component look default is Basecoat** ([basecoatui.com](https://basecoatui.com/) — the shadcn look without React): load the CDN CSS (required — it is standalone, no Tailwind build) and the CDN JS **only when the mockup uses an interactive component** (dropdown, tabs, dialog 등 — most components are CSS-only):
  ```html
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/basecoat-css@latest/dist/basecoat.cdn.min.css" />
  <!-- 대화형 컴포넌트를 쓸 때만 -->
  <script src="https://cdn.jsdelivr.net/npm/basecoat-css@latest/dist/js/all.min.js" defer></script>
  ```
  Style with Basecoat's class vocabulary so the implementation maps each class onto the same-named shadcn/ui component (`.claude/rules/frontend.md` §0). An offline mockup drops these links like the font links and keeps the layout self-contained
- **Icon default is lucide SVG** (`https://unpkg.com/lucide-static@latest/icons/<name>.svg`) — the third CDN exception, and only for mockups that actually use icons. Insert via `<img>` or inline SVG; no JS. The implementation maps the same icon names onto `lucide-react` (`.claude/rules/frontend.md` §10). An offline mockup drops these links exactly like the font and Basecoat links
- On-screen text, table shape, and flow follow the forced defaults in `.claude/rules/frontend.md` §6–§8 (titles without standing subtitles, guidance text only where needed; table baseline; flow rules) — they bind the mockup, not just the real implementation
- 1–3 core screens, filled with dummy data so it looks like a real service
- Dummy data must not pass for a settled decision: whatever still carries a `[확인 필요: ...]` marker in the plan stays visibly unresolved on the screen too — mark those values with a "확인 필요" badge (dashed outline, no fill) instead of inventing a plausible-looking one. Filling an open item in silently makes the mockup claim a decision nobody made
- Do not draw a device frame — phone bezel, tablet shell, browser chrome — while the platform requirement is still open, because the frame asserts a platform the plan has not fixed. Draw one only after the platform is settled, and even then only if it carries information the screen itself does not
- When complete, tell the user how to check it in a browser:
  - `[win] start mockup\index.html`
  - `[unix] open mockup/index.html`
- Before asking for approval, run the flow gate of `.claude/rules/frontend.md` §8: present the core-journey step table (진입 → 핵심 가치 도달, 화면 순서 + 단계 수 — §8.2) alongside the mockup, and answer the 3-second question (§8.6) for every screen
- Repeat user feedback → mockup fixes until approval comes. If the user says "그냥 진행", treat it as approval. **[2계층(범위·목적)]** 목업 승인은 무인 모드에서도 그대로 묻는다

## Kickoff close — after mockup approval
When the user approves the mockup, `/kickoff` ends. Do **not** scaffold, decompose features, create issues, or start implementation here — all of that happens in `/architect`.
1. **Create `docs/design-system.md`** — extract it from the approved mockup, so the look survives across sessions and feeds `/architect` (it is an `/architect` input — the single source of branding tokens there). Values are **extracted, never invented**: every token comes from what the user just approved. Writing follows the kickoff convention — the orchestrator composes the body and delegates the write to an implementer. This step is the single source of the file's schema:
   - **제품 컨텍스트** — 무엇을 만드나 · 무드 키워드, 1~3줄
   - **브랜딩 토큰** — color(이름 붙인 hex 4~6개), font(확정 스택 — `.claude/rules/fonts.md` 강제값+디스플레이 예외 반영), spacing, shadow, layout(그리드·컨테이너)
   - **모션 패턴**
   - **특이 요구**

   `docs/design-system.md`가 이미 있으면 조용히 덮지 않고 사용자에게 묻는다.
2. Push the accumulated per-stage commits in one batch, under the `origin`-absent guard — protocol: `.claude/conventions/kickoff-common.md` §4. The batch includes the `docs/design-system.md` created in step 1
3. Update docs/STATE.md in `/handoff` format (done: plan, goals, mockup approved / next: `/architect`)
4. Report to the user, and the report must include this line:
   - Mockup approval is approval of **screens and UX only** — it is **not** approval of the tech stack, data model, or implementation approach. Those are designed, compared, and approved separately in `/architect`.
5. **Then, in the same turn, enter `/architect` immediately** — same-turn chaining per `.claude/conventions/kickoff-common.md` §5, including its "여기까지만" exception. In kickoff the next phase's first user-approval question is `/architect`'s **technical-plan approval** — the turn stops there, not before it and not past it; the mockup gate above and that tech-plan gate both still ask, even in unattended mode.
