---
name: template-update
description: Syncs a project derived from the claude-starter template to the latest template-owned machinery (CLAUDE.md, scripts, agents, skills, settings). Use when the user asks to update the project to the latest template version.
---

# /template-update — sync template machinery

Pulls the latest template-owned files from the template repo into this derived project.
Project-owned files — `docs/` (STATE/MAP/GOTCHAS/plans), `README.md`, product code, `.claude/settings.local.json` — are never touched.
User-facing communication is in Korean.

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
The script fetches `template/main` and checks out only the template-owned paths (the list lives in one array at the top of the script). Missing paths are skipped with a notice. The script commits nothing.

## 4. Review
- Inspect the resulting changes: `git status --short`, `git diff --cached`.
- Show the user the `.claude/settings.json` diff **specifically** — it was overwritten from the template. Local customizations must be re-applied, and should live in `.claude/settings.local.json` from now on (that file is never synced).

## 5. Commit
If nothing is staged (no upstream changes — already in sync), skip the commit and tell the user in one line: "이미 최신 상태".
Commit as: `chore: sync template machinery to <short-sha>` — `<short-sha>` is the synced template SHA the script printed (`git rev-parse --short template/main`).

## 6. Report
- List the changed files to the user.
- Remind (Korean): settings/hooks changes take effect only after a **Claude restart**; `docs/`, `README.md`, and product code were untouched.
