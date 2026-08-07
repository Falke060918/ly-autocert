---
name: kickoff
description: Project kickoff process that develops an idea into a plan → final goals → HTML mockup. Use when starting a new project or when the user brings a new idea. Even without an explicit invocation, use it automatically when a new-product request like "~를 만들어줘" arrives in a repo that has no `docs/02-goals.md`. Not for sessions showing the `TEMPLATE-REPO` marker (the template original itself).
---

# /kickoff — from idea to mockup

Idea provided by the user: $ARGUMENTS
(If empty, first ask for the idea in one line)

All user-facing questions, notifications, and reports in this flow are written in the user's language (Korean). Documents the user reads (docs/01-plan.md, docs/02-goals.md, STATE.md entries) are also written in Korean.

Proceed through the 5 stages in order. Each stage's output must be left as a file.
In stages 1–4, do not write actual project code. Mockups go only inside `mockup/`.
If the user instructs skipping a stage, skip it.

## When entered via auto-trigger
The trigger conditions and non-trigger cases are in the "표준 작업 흐름" section of `CLAUDE.md`. This section covers execution only.
Even on auto-trigger, the 5-stage procedure is exactly the same as an explicit invocation. The user's first request sentence substitutes for `$ARGUMENTS`.
Before the stage-1 questions, send a one-line notice first — "새 프로젝트로 보여 기획 단계부터 시작한다. 바로 구현을 원하면 말해라." This is not a confirmation request, so do not wait for an answer; move straight to AskUserQuestion. If the user chooses to skip, stop right there and do not suggest it again in that session.

Note: if the main session is in orchestrator mode (no write tools), delegate file creation/modification to the implementer subagent.
The main session writes the document contents (plan, goals) itself and puts the full text into the delegation prompt. For the mockup HTML, delegate with the requirements written out in detail.

## Stage 1 — Concretize (questions)
Confirm the following with the AskUserQuestion tool (max 4 questions, 2–4 options each, mark a recommended option):
- Target users and the core problem to solve
- MVP must-have features (compress to 3 or fewer)
- Platform/stack (default recommendation: web)
- Constraints (deadline, external APIs, data, budget)

## Stage 2 — Plan: docs/01-plan.md
Write with this structure:
- Problem definition
- Target users
- Core features (in priority order, marking MVP status)
- Non-functional requirements (performance, security, etc. where applicable)
- Out of scope this time (exclusions — must be explicit)

## Stage 3 — Final goals: docs/02-goals.md
- Final goal (1 sentence)
- Success criteria (3–5 measurable items)
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
4. Outputs (videos, images, engine JS) go only inside `mockup/`. Still no actual project code before stage 5.
5. The M1 (mockup approval) gate applies exactly as with A — iterate on fixes until approval.

### 4-1. Static HTML mockup (default)
- A single static HTML+CSS file with no external dependencies (no CDNs; styles in an inline `<style>`) — **the one exception is the font CDN links** below
- Fonts are a forced default, not a choice: apply the Geist + Pretendard stacks from `.claude/conventions/fonts.md` (CDN `<link>`s at the very top of `<head>`, stacks in the inline `<style>`). No OS-default fallback in the stacks. Deviate only on an explicit user instruction
- 1–3 core screens, filled with dummy data so it looks like a real service
- When complete, tell the user how to check it in a browser:
  - `[win] start mockup\index.html`
  - `[unix] open mockup/index.html`
- Repeat user feedback → mockup fixes until approval comes. If the user says "그냥 진행", treat it as approval

## Stage 5 — Start after approval
When the user approves the mockup:
1. Delegate stack scaffolding to the **implementer** subagent (instruct it to reference the mockup's screen structure)
2. Decompose the M2 implementation into **feature units**. First detect the mode (if the command fails, just treat it as local mode):
   `gh auth status >/dev/null 2>&1 && git remote | grep -q . && echo GITHUB || echo LOCAL`
   - **Local mode**: create no issues. Write each feature as one line in docs/STATE.md "다음 할 일", and append one **verifiable completion condition** line per item (this substitutes for the issue form's required fields). These items are the work boundaries. Tell the user in one line: "GitHub 미연결 — 로컬 모드로 진행한다".
   - **GitHub mode**: create an issue per feature (delegate to implementer):
     - Label prep: follow the label conventions in `.github/ISSUE_TEMPLATE/`. Milestone labels are named after the stage-3 milestones in the `M1`/`M2`/`M3` format. Create only the missing labels with `gh label create <name>` (platform labels only when multi-platform)
     - **Issue body convention**: `gh issue create --body` **bypasses** the required-field validation of `.github/ISSUE_TEMPLATE/*.yml`. So the body must include the 3 headings `### 배경` / `### 제안` / `### 완료 조건`. Write completion conditions as verifiable sentences (e.g., "로그인 후 /dashboard로 리다이렉트된다"). Issue titles and bodies are written in Korean.
     - Issue creation:
       ```
       gh issue create --title "<기능>" --label feat --label M2 --body "### 배경
       <왜 필요한가>

       ### 제안
       <무엇을 어떻게. 관련 파일 포함>

       ### 완료 조건
       - [ ] <검증 가능한 문장>"
       ```
       (For multi-platform, add `--label web` etc.)
   - Do not create issues for micro-tasks (5-minute jobs) — feature units only
3. Update docs/STATE.md in `/handoff` format (done: plan & mockup / next: issue number list — or the task list above in local mode)
4. Report the issue list (task list in local mode) to the user

Implementation rules afterwards:
- State the issue number in implementation delegation prompts ("#3 구현: ...") — in local mode, paste the STATE.md item verbatim including its completion condition
- If using branches, put the number in the name (`feat/3-login`)
- When implementation + verification (reviewer pass) are done, close with `gh issue close <number> --comment "<완료 요약>"` (delegate to implementer). In local mode, remove the item from STATE.md "다음 할 일" and record it as completed in "현재 상태" (the STATE template has no "완료" section)
