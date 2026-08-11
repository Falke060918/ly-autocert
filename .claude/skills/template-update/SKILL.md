---
name: template-update
description: Syncs a project derived from the claude-starter template to the latest template-owned machinery (CLAUDE.md, scripts, agents, skills, settings). Use when the user asks to update the project to the latest template version.
---

# /template-update — sync template machinery

Pulls the latest template-owned files from the template repo into this derived project.
User-facing communication is in Korean.

## What this actually overwrites

Eight paths are checked out from `template/main`, replacing the project's copies. For the directory entries, only files that also exist in the template are replaced — skills/scripts/agents the project added alongside them survive.

`CLAUDE.md` · `SETUP.md` · `scripts/` · `.claude/agents/` · `.claude/skills/` · `.claude/output-styles/` · `.claude/conventions/` · `.claude/settings.json`

⚠️ **`CLAUDE.md` is overwritten wholesale, including the project-specific rules at its bottom.** Together with `.claude/settings.json` (extra `permissions.allow` entries) it is the pair derived projects actually edit. The script copies both into `.template-update-backup/<timestamp>/` and prints the incoming diff *before* overwriting, but deciding what to restore is a human call — step 4 is not optional.

Never touched (hard exclusions listed in the script): `.claude/template-origin`, `docs/` (STATE/MAP/GOTCHAS/plans), `README.md`, `.claude/settings.local.json`, `.github/workflows/ci.yml`, `.mcp.json`, product code.

Procedure (delegate all execution work to implementer):

## 1. Preconditions
- This must be a **derived project**, not the template original. The script aborts on its own if run in the original (`.claude/template-origin` sentinel + origin repo-name match) — if it does, stop and tell the user this skill has no job in the template repo.
- Require a **clean working tree**. Uncommitted changes must be committed or stashed first — the checkout overwrites silently, and the script aborts on a dirty tree.

## 2. Ensure the `template` remote
Derived projects do **not** record the template URL anywhere. If `git remote get-url template` fails, ask the user for the template repository URL **once** (AskUserQuestion), then pass it as the script argument — the script registers the remote, so future runs need no URL.

## 3. Run the updater
```
bash scripts/template-update.sh [<template-url>]
```
The script fetches `template/main`, backs up `CLAUDE.md` and `.claude/settings.json` with their incoming diffs printed, then checks out the template-owned paths (the list lives in one array at the top of the script). Paths missing from the template are skipped with a notice. The script commits nothing.

**On a non-zero exit the sync is partial** — one or more paths failed to check out (permissions, `index.lock`, …) and the script printed `실패 (checkout 에러 …)` for each. Do **not** create a "sync 완료" commit in that case. Report the failed paths and their cause, and let the user choose: fix and re-run, or roll back with `git reset --hard HEAD` (the tree was clean before the run, and the untracked backup directory survives a reset).

## 4. Review
- Inspect the resulting changes: `git status --short`, `git diff --cached`. The checkout **stages** what it writes, so a bare `git diff` prints nothing — always `--cached`.
- Show the user the **`CLAUDE.md`** diff explicitly and ask whether any project-specific rule at the bottom was lost. Restore the lost parts from the backup.
- Show the **`.claude/settings.json`** diff explicitly. Local customizations must be re-applied, and should live in `.claude/settings.local.json` from now on (that file is never synced).
- Backups sit at `.template-update-backup/<timestamp>/<original path>`; restore with `cp`, compare with `diff`. The directory is registered in the repo's local `.git/info/exclude`, so it never shows up in `git status` or in a commit.

## 5. Commit
Only when step 3 exited 0. If nothing is staged (no upstream changes — already in sync), skip the commit and tell the user in one line: "이미 최신 상태".
Commit as: `chore: sync template machinery to <short-sha>` — `<short-sha>` is the synced template SHA the script printed (`git rev-parse --short template/main`).

## 6. Report
- List the changed files to the user.
- Point at the backup directory and say it can be deleted once the review is done.
- Remind (Korean): settings/hooks changes take effect only after a **Claude restart**; `docs/`, `README.md`, `.mcp.json`, CI, and product code were untouched.
