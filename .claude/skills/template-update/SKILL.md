---
name: template-update
description: Syncs a project derived from the claude-starter-v2 template to the latest template-owned machinery (CLAUDE.md, SETUP.md, scripts, .claude rules/agents/skills/conventions, settings) by running scripts/sync-manifest.sh. Use when the user asks to update the project to the latest template version. Refuses to do anything in the template original (identity: template).
---

# /template-update — sync template machinery (v2)

Runs the derived project's sync against the template repo. The whole mechanism lives in
`scripts/sync-manifest.sh` — this skill is its operating procedure, not a second implementation.
Unlike v1's template-update.sh, the script self-updates from `template/main` and re-execs **before**
syncing, so path/guard changes are effective from the first run (no two-run lag), and files the
template deleted are removed via the deletion manifest (`scripts/sync-manifest.deletions`).
User-facing communication is in Korean.

## 1. Preconditions
- **Derived project only.** The script aborts on its own when `.claude/identity` declares
  `identity: template` — if it does, stop and tell the user this skill has no job in the template repo.
- **Clean working tree required.** The script aborts on a dirty tree — commit or stash first.
  That pre-run commit is also the rollback point.

## 2. Ensure the `template` remote
If `git remote get-url template` fails, the script recovers on its own from the `template:` key
in `.claude/identity` (the old template fingerprint, preserved by SETUP step 8) and registers the
remote automatically. Ask the user for the template repository URL **once** (AskUserQuestion)
**only when that key is also absent** — a legacy derivation — then pass it as the script argument;
the script registers the remote, so future runs need no URL.

**[3계층(멈춤선)]** 그 1회 질문은 추정 금지 값이라 무인 모드(`/automode on`)에서도 그대로 묻는다 — URL을 임의로 짓지 않는다. 계층 정의는 `.claude/agents/orchestrator.md`.

## 3. Run the updater
```
bash scripts/sync-manifest.sh [<template-url>]
```
What it does (details in the script header, the single source of truth):
- Checks out the template-owned paths from `template/main` (the `paths=` array at the top of the
  script). Directory entries only replace files that also exist in the template — skills/scripts/rules
  the project added alongside them survive.
- `CLAUDE.md` and `.claude/settings.json` are backed up to `.sync-manifest-backup/<시각>/` with the
  incoming diff printed **before** being overwritten (only when content actually differs).
- Deletes paths listed in the deletion manifest, refusing protected ones (`docs/`, `README.md`,
  `.claude/identity`, `.claude/settings.local.json`, `.mcp.json`, ci.yml).
- Commits nothing — checkout/`git rm` results sit in the index for review.

On a non-zero exit the sync is partial — do **not** create a sync commit. Report the failed paths
and let the user choose: fix and re-run, or roll back with `git reset --hard HEAD` (the tree was
clean before the run; the backup directory is untracked and survives a reset).

## 4. Review
- Inspect with `git status --short` and `git diff --cached` — the checkout **stages** what it
  writes, so a bare `git diff` prints nothing.
- Show the **`CLAUDE.md`** and **`.claude/settings.json`** diffs explicitly and ask whether any
  project customization was lost. Restore from `.sync-manifest-backup/<시각>/` with `cp`; local
  permission customizations belong in `.claude/settings.local.json` from now on (never synced).
- Project-specific rules live in `docs/constitution.md` 하단 "이 프로젝트의 원칙" — `docs/` is never
  synced, so nothing there needs restoring.

## 5. Commit & report
Only when step 3 exited 0. If nothing is staged, skip the commit and say "이미 최신 상태".
Commit as: `chore: sync template machinery to <short-sha>` (`git rev-parse --short template/main`).
Then report the changed files, point at the backup directory (deletable after review), and remind
(Korean): settings/hooks changes take effect only after a **Claude restart**; `docs/`, `README.md`,
`.mcp.json`, CI, and product code were untouched.
