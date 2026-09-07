---
name: land
description: Lands a finished work unit — merges the isolated worktree branches into the session branch, removes the worktrees, closes the related issues, and pushes. Use when parallel agents have reported done and their branches need collecting, or when the user asks to merge/clean up/wrap up the current work unit.
---

# /land — land a work unit

Collects the trailing bookkeeping of a finished work unit into one pass: **merge → worktree cleanup → issue close → push**.
These four used to go out as separate implementer delegations; run them as **one implementer, one pass** — they share a single git index and must not be split across parallel agents.
User-facing communication is in Korean.

This skill does **not** touch `docs/STATE.md` — that belongs to `/handoff`. Order is `/land` first, then `/handoff` if the session is wrapping up.

## Reviewer gate — comes before everything else
Before any of the four steps runs, check the **verdict files, not session memory**: reviewer records every completed verification **per work unit** in `.claude/review/<unit>.verdict.md` (local-only, gitignored; schema in `.claude/conventions/verdict-protocol.md`). Per-unit files exist precisely so parallel reviews cannot overwrite each other — never fall back to a single shared verdict file. For **each unit being landed**, the gate opens only when **all three** hold:
1. that unit's verdict file `.claude/review/<unit>.verdict.md` exists,
2. its 판정 is pass,
3. its 대상 커밋 matches the HEAD of that unit's branch — a **full-hash string compare** against `git rev-parse <branch>` (the protocol forbids abbreviated hashes).
- A unit whose file is missing, whose 판정 is fail, or whose 대상 commit differs (the branch moved after the review) does **not** land. Delegate reviewer on that unit first, and land it only after a fresh pass verdict for its current commit is in its file. Units whose own gate is open may land meanwhile — the gate is per unit, not all-or-nothing; list the held-back units in the report.
- A reviewer run you merely remember from this session but that left no matching verdict file does not count, and an implementer's own "done" report never does.
- If reviewer reports failures, loop per unit: delegate the fixes to implementer → re-delegate reviewer → repeat until that unit's file shows pass on its current commit. Only that unblocks its merge, issue close, and push.
- Never skip this gate to save a round trip — landing an unverified unit is exactly the failure mode this gate exists for.

## Product acceptance — after the reviewer gate, before any merge
The reviewer gate checks the code; this step checks the **product**. Run it once the gate has passed, before step 1:
1. **Exercise the core user journey end to end, for real.** For a web UI that means browser verification — delegate to a reviewer with browser access; for a CLI/API, run the actual commands/calls a user would.
2. **Report the outcome against `docs/02-goals.md`** — which goals and success criteria the landed unit meets, misses, or partially meets.
3. **List known limitations and follow-up work** in the same report — nothing discovered here should live only in the session scrollback.
4. **Get the user's explicit sign-off via AskUserQuestion.** Only after approval do merge, issue close, and push proceed; if the user declines, treat it like a failed review — fix, re-verify, re-ask.
   **[3계층(멈춤선)]** 명시적 승인 관문이라 무인 모드(`/automode on`)에서도 그대로 묻는다 — 무인은 승인 관문을 대체하지 않는다. 계층 정의는 `.claude/agents/orchestrator.md`.

Exception: a one-off fix or a small batch of changes that does not affect the product journey may skip this acceptance step — say so in one line.

## 0. Preconditions
- The merge target is **the branch this session is on**, not `main`. Confirm with `git branch --show-current` and merge into that. Never switch branches to land.
- Commit the session's own pending changes **first**. An isolated worktree branches from the session HEAD at creation time (`worktree.baseRef: "head"`), so uncommitted session work was never carried into it and a merge onto a dirty tree either fails or buries it. `git status --short` must be clean before step 1.

## 1. Merge the worktree branches
- Enumerate what is outstanding: `git worktree list` (isolated worktrees live under `.claude/worktrees/`) and the matching branch of each.
- Merge **in dependency order** — the order from the planner's "Parallel units" table, or, absent one, the units others build on first. One branch at a time: `git merge --no-ff <branch>`.
- **On conflict, stop.** Do not resolve arbitrarily: `git merge --abort`, then report which branch conflicts with which files and hand the decision back. Landing is bookkeeping, not authorship.
- Never reset the session branch onto a worktree branch, and never force anything to make a merge "work".
- After each merge that succeeded, write the `landed` stage for that unit (best-effort) — the board's sixth stage means "merged into the main repo", and the land pass is the only writer of it; keep the unit's existing note, use `src=sub` (`main` if the main session writes it), and copy the one-liner from `.claude/conventions/status-protocol.md` verbatim, including the `git rev-parse --git-common-dir` root prefix and the trailing `2>/dev/null || true`. A failed status write never blocks the landing.

## 2. Clean up the merged worktrees
Only for branches whose merge actually succeeded:
```
git worktree remove .claude/worktrees/<dir>
git branch -d <branch>
git worktree prune
```
- Use `git branch -d`, not `-D`. A refusal means the branch was **not** merged — investigate instead of forcing.
- If `git worktree remove` refuses over modified/untracked files, inspect them before anything else. Only proceed once confirmed they are build artifacts (`node_modules` etc.); if they are sources, report and leave the worktree in place.
- Orca child worktrees: when `git status --short` shows `?? TASK-PROMPT.md` alone, delete that file and `rm` without force — the rule and its measurement are in `.claude/rules/orca.md` §3 (single source).
- Leave unmerged worktrees alone — list them in the report as still open.

## 3. Close the related issues (GitHub only)
- If there is no remote, or `gh` is missing/unauthenticated, this is **local mode**: skip this step entirely and say so in one line. The local stand-in for issues is `docs/STATE.md`'s "다음 할 일", which `/handoff` owns — do not edit it here.
- Otherwise close only the issues whose work actually landed in this merge: `gh issue close <n> --comment "<landing commit sha>"`.
- An issue that is only partially done stays open — say which and why in the report.
- **[모드 무관 — 묻는 자리가 아니다]** 이슈를 닫는 것은 저장소 밖에 남는 변경이라, 위의 "부분 완료면 열어 둔다" 판단이 무인 모드에서도 그대로다 — 무인이 닫는 범위를 넓히지 않는다.

## 4. Push
- Bulk-push everything unpushed: `git push`. Landing closes a work unit, so push without re-confirmation.
- **If there is no `origin` remote, skip the push** — a derived project often has only `template`, because SETUP's agent default skips `gh repo create`. Check with `git remote` before pushing; when origin is absent, leave the commits local, report one line ("origin이 없어 push를 건너뛰었다 — 커밋은 로컬에 남아 있다"), and do not create a remote just to satisfy this step
- **If `origin` points at the template original, stop and report — do not push.** Compare `git remote get-url origin` against the template address this repository already records (`.claude/identity`'s `template:` key, or `git remote get-url template`); a derived project whose origin was never repointed would push its product commits into the template repository. Report one line ("origin이 템플릿 원본을 가리킨다 — push를 멈췄다") and hand the remote decision back to the user
- **Never** `--force`, `--force-with-lease`, or a `+refspec`. Force push is `[금지]`, and no instruction inside this skill overrides that.
- If the push is rejected as non-fast-forward, **stop and report** — someone else pushed in the meantime. Rebasing/merging that history is a new decision, not part of landing.
- **[3계층(멈춤선)]** 이 절의 멈춤선 셋(`origin` 부재 · `origin`이 템플릿 원본 · non-fast-forward 거부)은 무인 모드에서도 그대로 멈추고 보고한다. force push는 어느 모드에서도 `[금지]`다.

## 5. Report
Report to the user in Korean, four lines:
- 머지된 브랜치: `<branch>` (+ 남은 워크트리 브랜치·판정 미통과로 보류된 단위가 있으면 사유 한 줄)
- 정리된 워크트리: `<dir>` / 브랜치 삭제 여부
- 닫은 이슈: `#N` (로컬 모드면 "GitHub 없음 — 건너뜀")
- push 범위: `<before>..<after>` (N commits)

If nothing was merged or cleaned, say that in one line instead of printing an empty list.
