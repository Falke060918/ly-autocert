---
name: kickoff
description: Project kickoff process that develops an idea into a plan → final goals → HTML mockup, ending at product-scope + mockup approval. Technical design and implementation start afterwards with /architect. Use when starting a new project or when the user brings a new idea. Even without an explicit invocation, use it automatically when a new-product request like "~를 만들어줘" arrives in a repo that has no `docs/02-goals.md`. Not for sessions showing the `TEMPLATE-REPO` marker (the template original itself).
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
The trigger conditions and non-trigger cases are in the "표준 작업 흐름" section of `CLAUDE.md`. This section covers execution only.
Even on auto-trigger, the stage procedure is exactly the same as an explicit invocation. The user's first request sentence substitutes for `$ARGUMENTS`.
Before the stage-1 questions, send a one-line notice first — "새 프로젝트로 보여 기획 단계부터 시작한다. 바로 구현을 원하면 말해라." This is not a confirmation request, so do not wait for an answer; move straight to AskUserQuestion. If the user chooses to skip, stop right there and do not suggest it again in that session.

Note: if the main session is in orchestrator mode (no write tools), delegate file creation/modification to the implementer subagent.
The main session writes the document contents (plan, goals) itself and puts the full text into the delegation prompt. For the mockup HTML, delegate with the requirements written out in detail.

## Per-stage commits & push
Commit each stage's deliverable as soon as it is complete — docs/01-plan.md, docs/02-goals.md, and mockup/ each get their own commit. In orchestrator mode the commit, like the file writes above, is delegated to the implementer subagent. Do not push per stage; push the accumulated commits in one batch at kickoff close (mockup approved).

## Stage 1 — Concretize (branching multi-round questions)
Ask in rounds, one AskUserQuestion call per round (max 4 questions each, 2–4 options each, mark a recommended option). Tell the user that every answer in these rounds is reflected into docs/01-plan.md.

**Stage 1 does not choose a platform or tech stack.** It only confirms platform **requirements and constraints** (꼭 모바일이어야 하는가, 오프라인에서도 돌아야 하는가 등); the stack decision itself is deferred to `/architect`'s alternative comparison.

**Round 1 — nature & problem:**
- Project nature — 개인용 (hobby/personal use) vs 사업용 (monetization intent). This answer decides whether Round 4 and the market-research step run
- Target users
- Core problem to solve (pain point)
- Platform requirements & constraints (requirements only — e.g. 반드시 모바일인가, 오프라인 동작이 필요한가; not a stack choice)

**Round 2 — features & constraints:**
- MVP must-have features (compress to 3 or fewer)
- Core user journeys that must be supported (반드시 지원할 핵심 사용자 여정)
- Post-MVP core features & roadmap priority (what to build first)
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
1. Ask the user which competing services or similar products they already know (AskUserQuestion, include a "시장조사 불필요" option for personal projects).
2. Based on the answers, delegate a **web-search investigation to the explorer subagent**: competing services, current market snapshot, and differentiation points. Receive a summary only.
3. Attach the summary to docs/01-plan.md (it feeds the "타겟 고객/시장조사" section in Stage 2).

## Stage 2 — Plan: docs/01-plan.md
Write with this structure:
- Problem definition & pain points (may be merged into one section)
- Target users / market research (competing services and differentiation, from Stage 1.5 if run)
- Core features (in priority order, marking MVP status) — **give every core feature a requirement ID and acceptance criteria.** IDs use the `REQ-<영역>-<번호>` form (e.g. REQ-AUTH-01) and acceptance criteria must cover both the normal path and the exception path. One line per feature in this format:
  `REQ-AUTH-01 | 요구: 이메일 로그인 | 승인 조건: 올바른 자격증명이면 대시보드로 이동 | 예외: 5회 실패 시 잠금 안내 | 검증: 수동/E2E | 구현 이슈: (미정 — /architect에서 연결)`
  These IDs are the traceability spine: `/architect` issues reference them, and completion is verified against their acceptance criteria
- Feature/implementation roadmap (MVP → later stages)
- Revenue model (사업용 only — omit the section for personal projects)
- Non-functional requirements (performance, security, etc. where applicable)
- Out of scope this time (exclusions — must be explicit)

## Stage 3 — Final goals: docs/02-goals.md
- Final goal (1 sentence)
- Success criteria (3–5 measurable items; where a criterion maps to a core feature, reference its requirement ID in parentheses, e.g. "(REQ-AUTH-01)")
- Milestones: M1 mockup approved → M2 MVP working → M3 complete (state each milestone's completion condition)

After writing, report a summary of the plan and goals to the user and confirm there are no objections.

## Stage 4 — Mockup: mockup/index.html

### 4-0. Mockup approach selection (landing-type projects only)
Run this subsection only when the nature identified in stages 1–2 is a **landing page, marketing site, or one-page product intro**.
For dashboards, CLIs, internal tools, or admin screens, go **straight to 4-1 without asking**.
Also go to 4-1 without asking for — DOM/SVG scroll interactions, real-time 3D, and jobs that only add animation to an existing page.

If it applies, settle the mockup approach with **one** AskUserQuestion (2 options, mark A as recommended):
- **(A) 정적 HTML 목업** — description: "외부 의존성 없는 단일 HTML 파일. 추가 도구·비용 0. 4-1을 그대로 진행한다."
- **(B) scroll-world 몰입형 랜딩** — description: "AI로 씬 이미지와 카메라 이동 영상을 미리 렌더링해 스크롤 위치에 비디오 재생 헤드를 물리는 원페이지. Higgsfield 유료 크레딧 필요. ffmpeg·Python3+Pillow 필요. 산출물은 mp4/webp 정적 자산 + 바닐라 JS라 프레임워크 비종속."

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

### 4-1. Static HTML mockup (default)
- A single static HTML+CSS file with no external dependencies (no CDNs; styles in an inline `<style>`) — **the one exception is the font CDN links** below
- Fonts are a forced default, not a choice: apply the Geist + Pretendard stacks from `.claude/conventions/fonts.md` (CDN `<link>`s at the very top of `<head>`, stacks in the inline `<style>`). No OS-default fallback in the stacks. Deviate only on an explicit user instruction
- 1–3 core screens, filled with dummy data so it looks like a real service
- When complete, tell the user how to check it in a browser:
  - `[win] start mockup\index.html`
  - `[unix] open mockup/index.html`
- Repeat user feedback → mockup fixes until approval comes. If the user says "그냥 진행", treat it as approval

## Kickoff close — after mockup approval
When the user approves the mockup, `/kickoff` ends. Do **not** scaffold, decompose features, create issues, or start implementation here — all of that happens in `/architect`.
1. Push the accumulated per-stage commits in one batch (per the rule above)
2. Update docs/STATE.md in `/handoff` format (done: plan, goals, mockup approved / next: `/architect`)
3. Report to the user, and the report must include both of these:
   - "구현을 시작하려면 `/architect`로 기술설계부터 진행한다."
   - Mockup approval is approval of **screens and UX only** — it is **not** approval of the tech stack, data model, or implementation approach. Those are designed, compared, and approved separately in `/architect`.
