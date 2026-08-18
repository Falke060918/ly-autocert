---
name: architect
description: Technical design gate between /kickoff (product scope + mockup approved) and implementation. Investigates the existing code/environment, has the planner compare technical alternatives, gets explicit user approval of the tech plan, records it, and only then scaffolds and decomposes work into issues. Use when the user invokes /architect, or when implementation is about to start on a project whose mockup is approved but whose technical plan is not.
---

# /architect — from approved product scope to implementation-ready

Precondition: `/kickoff` is done (docs/01-plan.md, docs/02-goals.md exist, mockup approved). If not, guide the user to `/kickoff` first.
All user-facing questions and reports are in the user's language (Korean); documents the user reads (docs/03-architecture.md, STATE.md entries) are also in Korean.

Note: if the main session is in orchestrator mode (no write tools), delegate file creation/modification and commits to the implementer subagent, exactly as `/kickoff` does. The main session writes the document contents itself and puts the full text into the delegation prompt.

**Hard gate: until the user approves the technical plan in Stage 4, no package installs, no scaffolding, no implementation code.** Stages 1–5 produce only analysis and documents.

## Stage 1 — Investigate the ground
Delegate to the **explorer** subagent: existing code and structure (if any), current stack and versions, runtime/deployment environment, external services and credentials in use, plus the constraints stated in docs/01-plan.md and docs/02-goals.md. Receive a summary only.

## Stage 2 — Technical design (planner)
Delegate the design to the **planner** subagent, passing the plan/goals, the mockup's screen structure, and the Stage 1 summary. Require the return to include (this matches the planner's return format):
- **At least 2 technical alternatives** compared — pros/cons and cost (money, ops burden, learning curve) for each — with a recommendation and its rationale
- Data model, API contracts, auth/security boundaries, deployment approach
- Risks, irreversible decisions, and the choices that need user approval

## Stage 3 — Resolve open questions
Anything the planner marked undecided or user-facing (irreversible choices, paid services, vendor lock-in) goes to the user via AskUserQuestion — options with a recommended one marked. Never silently decide an irreversible point.

## Stage 4 — User approval of the tech plan
Present the plan in plain language via AskUserQuestion: the recommended alternative and why, what it costs, the rejected alternative(s) in one line each, and the irreversible points. Offer at minimum "승인 / 수정 요청 / 다른 대안 선택".
Iterate on modification requests until approval. Approval here is the technical counterpart of the mockup approval — the mockup approved screens/UX; this approves stack, data model, and implementation approach.

## Stage 5 — Record the approved plan
Write **docs/03-architecture.md** (delegate to implementer) and commit it:
- Chosen alternative + rejected alternatives with reasons
- Data model, API contracts, auth/security boundaries, deployment approach
- Irreversible decisions and accepted risks
Later direction changes update this file in the same commit as the change (same rule as 01-plan/02-goals in `/handoff`).

## Stage 6 — Scaffold & decompose (after approval only)
1. Delegate stack scaffolding to the **implementer** subagent (instruct it to follow docs/03-architecture.md and reference the mockup's screen structure)
   - **CI comes with the scaffold, in this same step**: have the implementer also create a CI pipeline matching the chosen stack (build · test · lint · type check — drop only the axes the stack genuinely lacks, e.g. no type check for plain JS). GitHub mode: `.github/workflows/ci.yml`; local mode: an equivalent local script (e.g. `scripts/ci.sh`) wired as the standard check command. **CI passing is part of every feature's completion condition** — include a "CI 통과" line in each issue's 완료 조건 (or the local-mode item's 완료 field), and reviewer verification treats a red CI as not done
   - **제품용 README 재작성도 이 같은 단계·같은 커밋에서**: 루트 `README.md`를 지금까지 남아 있던 템플릿 소개에서 제품 소개로 새로 쓴다. 근거 문서는 `docs/02-goals.md`(목표·범위)와 `docs/03-architecture.md`(스택·구조·실행법)다. 최소 항목 — 제품 한 줄 소개 · 목표/범위 요약 · 기술 스택 · 로컬 실행법(설치·실행 명령) · 프로젝트 구조 개요. 템플릿 자체를 설명하는 내용(오케스트레이터 강제 모드, 템플릿 파일 트리 등)과 폰트·스택 관련 템플릿 규칙 문구는 남기지 않는다 — README는 제품 문서다. (이 재작성으로 `/template-update`가 전제하는 "README는 제품용으로 재작성됨"과 실제가 비로소 부합한다)
2. Decompose the M2 implementation into **feature units**. First detect the mode (if the command fails, just treat it as local mode):
   `gh auth status >/dev/null 2>&1 && git remote | grep -q . && echo GITHUB || echo LOCAL`
   Every work unit — GitHub issue or local-mode STATE item — must carry these **6 required fields**: ① 관련 요구사항 ID (docs/01-plan.md의 REQ-* — 없으면 "해당 없음"과 사유) ② 변경 범위 (손대는 파일·모듈) ③ 범위 밖 항목 (이번에 하지 않는 것) ④ 검증 가능한 완료 조건 ⑤ 테스트 방법 ⑥ 의존 이슈 (없으면 "없음").
   - **Local mode**: create no issues. Write each feature as one line in docs/STATE.md "다음 할 일", and append the same 6 required fields compactly under each item — e.g. `(REQ-AUTH-01 / 범위: src/auth / 범위 밖: 소셜 로그인 / 완료: 로그인 후 /dashboard 이동 / 테스트: E2E 수동 / 의존: 없음)` (this substitutes for the issue form's required fields). These items are the work boundaries. Tell the user in one line: "GitHub 미연결 — 로컬 모드로 진행한다".
   - **GitHub mode**: create an issue per feature (delegate to implementer):
     - Label prep: follow the label conventions in `.github/ISSUE_TEMPLATE/`. Milestone labels are named after the milestones in docs/02-goals.md in the `M1`/`M2`/`M3` format. Create only the missing labels with `gh label create <name>` (platform labels only when multi-platform)
     - **Issue body convention**: `gh issue create --body` **bypasses** the required-field validation of `.github/ISSUE_TEMPLATE/*.yml`. So the body must include the 3 headings `### 배경` / `### 제안` / `### 완료 조건`, and the 6 required fields above are distributed into them as shown below. Write completion conditions as verifiable sentences (e.g., "로그인 후 /dashboard로 리다이렉트된다"). Issue titles and bodies are written in Korean.
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
- When implementation + verification are done, close with `gh issue close <number> --comment "<완료 요약>"` (delegate to implementer). "Verification done" means the reviewer verdict file `.claude/review/last-verdict.md` (local-only, gitignored) exists, its 판정 is pass, and its 대상 commit matches the unit's HEAD — session memory of a reviewer run does not count. In local mode, remove the item from STATE.md "다음 할 일" and record it as completed in "현재 상태" (the STATE template has no "완료" section)
