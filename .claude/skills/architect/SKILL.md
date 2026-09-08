---
name: architect
description: "Technical design gate between /kickoff (product scope + mockup approved) and implementation. Investigates the existing code/environment, has the planner compare technical alternatives, gets explicit user approval of the tech plan, records it, and only then scaffolds and decomposes work into issues. Use when the user invokes /architect, or when implementation is about to start on a project whose mockup is approved but whose technical plan is not."
---

# /architect — from approved product scope to implementation-ready

Precondition: `/kickoff` is done (docs/01-plan.md, docs/02-goals.md exist, mockup approved). If not, guide the user to `/kickoff` first.
All user-facing questions and reports are in the user's language (Korean); documents the user reads (docs/03-architecture.md, STATE.md entries) are also in Korean.

Note: if the main session is in orchestrator mode (no write tools), delegate file creation/modification and commits to the implementer subagent, exactly as `/kickoff` does. The main session writes the document contents itself and puts the full text into the delegation prompt.

**Hard gate: until the user approves the technical plan in Stage 4, no package installs, no scaffolding, no implementation code.** Stages 1–5 produce only analysis and documents.

## Mockup code is a reference, never inherited

This applies to every mockup path, including kickoff 4-0's (C) React+shadcn mockup: **do not carry the mockup's code into the implementation tree.** Read the mockup as a reference — extract the screen structure, flows, and UX requirements from it — and do the technical design and scaffolding from scratch (Stages 2 and 6). Reason: mockup code is built for speed of approval, and inheriting it lets that improvised code ride into the product. (2026-09-07 사용자 재결정: "목업을 그대로 이어받지말고 참고해서 아예 새로운처음부터 설계하고 만들기.")

## Stage 1 — Investigate the ground
Delegate to the **explorer** subagent: existing code and structure (if any), current stack and versions, runtime/deployment environment, external services and credentials in use, plus the constraints stated in docs/01-plan.md and docs/02-goals.md. Receive a summary only.

## Stage 2 — Technical design (planner)
Delegate the design to the **planner** subagent, passing the plan/goals, the mockup's screen structure, and the Stage 1 summary. `docs/design-system.md`가 있으면 함께 넘긴다(브랜딩 토큰의 단일 원본). Require the return to include (this matches the planner's return format):
- **At least 2 technical alternatives** compared — pros/cons and cost (money, ops burden, learning curve) for each — with a recommendation and its rationale
- Data model, API contracts, auth/security boundaries, deployment approach
- Risks, irreversible decisions, and the choices that need user approval

## Stage 3 — Resolve open questions
Anything the planner marked undecided or user-facing (irreversible choices, paid services, vendor lock-in) goes to the user via AskUserQuestion — options with a recommended one marked. Never silently decide an irreversible point.
**[3계층(멈춤선)]** 비가역 선택과 유료 서비스는 무인 모드(`/automode on`)에서도 그대로 묻는다 — 무인은 권한을 넓히지 않는다. 계층 정의는 `.claude/agents/orchestrator.md`.

## Stage 4 — User approval of the tech plan
Present the plan in plain language via AskUserQuestion: the recommended alternative and why, what it costs, the rejected alternative(s) in one line each, and the irreversible points. Offer at minimum "승인 / 수정 요청 / 다른 대안 선택".
Iterate on modification requests until approval. Approval here is the technical counterpart of the mockup approval — the mockup approved screens/UX; this approves stack, data model, and implementation approach.
**[2계층(범위·목적)]** 기술 계획 승인은 무인 모드에서도 그대로 묻는다 — 어디까지 만드는가를 정하는 자리다.

## Stage 5 — Record the approved plan
Write **docs/03-architecture.md** (delegate to implementer) and commit it:
- Chosen alternative + rejected alternatives with reasons
- Data model, API contracts, auth/security boundaries, deployment approach
- Irreversible decisions and accepted risks
Later direction changes update this file in the same commit as the change (same rule as 01-plan/02-goals in `/handoff`).

**Fill in what SETUP left undecided — in this same commit.** SETUP's step 7 ("프로젝트 고유 규칙") deliberately leaves the stack, the directory conventions, and the build/test commands as `"스택·디렉토리 컨벤션·빌드 명령 미정 — kickoff 이후에 채운다"`, because before kickoff none of them exist yet. This stage is where they do exist, so **this stage is the one that fills them in**: write the settled ones into the **"이 프로젝트의 원칙"** section at the bottom of `docs/constitution.md` (the derived project's own half — template sync never overwrites `docs/`, so it survives). Only what the approved plan actually settles, phrased as invariants by that section's own test ("다음 주에도, 어느 작업에서도 참인가?"); anything still moving belongs in STATE/MAP instead, and machine- or person-specific settings go to `.claude/settings.local.json`. Leave the "미정" line behind only for items the plan genuinely did not settle.

## Stage 6 — Scaffold & decompose (after approval only)
1. Delegate stack scaffolding to the **implementer** subagent (instruct it to follow docs/03-architecture.md and reference the mockup's screen structure)
   - **CI comes with the scaffold, in this same step**: have the implementer also create a CI pipeline matching the chosen stack (build · test · lint · type check — drop only the axes the stack genuinely lacks, e.g. no type check for plain JS). GitHub mode: `.github/workflows/ci.yml`; local mode: an equivalent local script (e.g. `scripts/ci.sh`) wired as the standard check command. **CI passing is part of every feature's completion condition** — include a "CI 통과" line in each issue's 완료 조건 (or the local-mode item's 완료 field), and reviewer verification treats a red CI as not done
   - **제품용 README 재작성도 이 같은 단계·같은 커밋에서**: 루트 `README.md`를 지금까지 남아 있던 템플릿 소개에서 제품 소개로 새로 쓴다. 근거 문서는 `docs/02-goals.md`(목표·범위)와 `docs/03-architecture.md`(스택·구조·실행법)다. 최소 항목 — 제품 한 줄 소개 · 목표/범위 요약 · 기술 스택 · 로컬 실행법(설치·실행 명령) · 프로젝트 구조 개요. 템플릿 자체를 설명하는 내용(오케스트레이터 강제 모드, 템플릿 파일 트리 등)과 폰트·스택 관련 템플릿 규칙 문구는 남기지 않는다 — README는 제품 문서다. (이 재작성으로 `/template-update`가 전제하는 "README는 제품용으로 재작성됨"과 실제가 비로소 부합한다)
2. Decompose the M2 implementation into **feature units**. First detect the mode (if the command fails, just treat it as local mode):
   `gh auth status >/dev/null 2>&1 && git remote get-url origin >/dev/null 2>&1 && echo GITHUB || echo LOCAL`
   **Judge by `origin`, not by "some remote exists"** — a derived project often has only `template`, pointing at the template repository (SETUP's agent default skips `gh repo create`), and `gh` resolves that remote into the template's own repo, so issues created here would be filed on the template original instead of this project (measured 2026-08-31: the name-blind check answered `GITHUB` in a derived testbed and `gh repo view` there returned the template's `owner/repo`).
   **An `origin` that still points at the template original is not GitHub mode either — treat it as local mode.** Compare `git remote get-url origin` against the template address this repository already records (`.claude/identity`'s `template:` key, or `git remote get-url template`); a copy cloned straight from the public template carries the template URL as its own `origin` until SETUP repoints it (`CLAUDE.md`'s `CLONE-SUSPECT`), so the check above passes and the issues still land on the template. Say one line ("origin이 템플릿 원본을 가리킨다 — 로컬 모드로 진행한다") and do not create a remote just to reach GitHub mode. This is the same comparison `/land` makes before pushing (`.claude/skills/land/SKILL.md`).
   Every work unit — GitHub issue or local-mode STATE item — must carry these **6 required fields**: ① 관련 요구사항 ID (docs/01-plan.md의 REQ-* — 없으면 "해당 없음"과 사유) ② 변경 범위 (손대는 파일·모듈) ③ 범위 밖 항목 (이번에 하지 않는 것) ④ 검증 가능한 완료 조건 ⑤ 테스트 방법 ⑥ 의존 이슈 (없으면 "없음").
   - **Local mode**: create no issues. Write each feature as one line in docs/STATE.md "다음 할 일", and append the same 6 required fields compactly under each item — e.g. `(REQ-AUTH-01 / 범위: src/auth / 범위 밖: 소셜 로그인 / 완료: 로그인 후 /dashboard 이동 / 테스트: E2E 수동 / 의존: 없음)` (this substitutes for the issue form's required fields). These items are the work boundaries. Tell the user in one line: "GitHub 미연결 — 로컬 모드로 진행한다".
   - **GitHub mode**: create an issue per feature (delegate to implementer):
     - Label prep: if `.github/ISSUE_TEMPLATE/` exists, follow its label conventions; **if it does not** (this template ships no `.github/`), label each issue with a type (`feat` / `fix` / `docs` / `chore`), a milestone (`M1`/`M2`/`M3`), and a platform (`web` / `app` …). Milestone labels are named after the milestones in docs/02-goals.md in the `M1`/`M2`/`M3` format. Create only the missing labels with `gh label create <name>` (platform labels only when multi-platform)
     - **Issue body convention**: the body itself carries the 6 required fields, whether or not `.github/ISSUE_TEMPLATE/` exists — where the templates are present `gh issue create --body` **bypasses** their required-field validation, and where they are absent there is nothing to validate against. So the body must include the 3 headings `### 배경` / `### 제안` / `### 완료 조건`, and the 6 required fields above are distributed into them as shown below. Write completion conditions as verifiable sentences (e.g., "로그인 후 /dashboard로 리다이렉트된다"). Issue titles and bodies are written in Korean.
     - Issue creation:
       ```
       gh issue create --title "<기능>" --label feat --label M2 --body "### 배경
       <왜 필요한가>
       - 관련 요구사항: REQ-XXX-NN

       ### 제안
       <무엇을 어떻게. 관련 파일 포함>
       - 변경 범위: <손대는 파일·모듈>
       - 범위 밖: <이번에 하지 않는 것>
       - 의존 이슈: #<번호> (없으면 '없음')

       ### 완료 조건
       - [ ] <검증 가능한 문장>
       - 테스트 방법: <어떻게 검증하는가>"
       ```
       (For multi-platform, add `--label web` etc.)
   - Do not create issues for micro-tasks (5-minute jobs) — feature units only
3. Update docs/STATE.md in `/handoff` format (done: tech plan approved & scaffolded & README 제품용 재작성 완료 / next: issue number list — or the task list above in local mode)
4. Report the issue list (task list in local mode) to the user

Implementation rules afterwards:
- State the issue number in implementation delegation prompts ("#3 구현: ...") — in local mode, paste the STATE.md item verbatim including its completion condition
- If using branches, put the number in the name (`feat/3-login`)
- When implementation + verification are done, close with `gh issue close <number> --comment "<완료 요약>"` (delegate to implementer). "Verification done" means the unit's reviewer verdict file `.claude/review/<unit>.verdict.md` (local-only, gitignored; schema: `.claude/conventions/verdict-protocol.md`) exists, its 판정 is pass, and its 대상 commit matches the unit's HEAD — session memory of a reviewer run does not count. In local mode, remove the item from STATE.md "다음 할 일" and record it as completed in "현재 상태" (the STATE template has no "완료" section)
