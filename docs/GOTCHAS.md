# GOTCHAS

Things we got burned by once, and how to avoid them. Cap: 20 entries / 100 lines.
Add an entry only when **all** 4 conditions hold: time was actually lost / reproducible / not obvious from docs alone / still valid next time.
Entries are written in English; user quotes and error messages stay verbatim in their original language.

## Environment & tools

**On Windows, extracting with `python -c "print(...)" > file` corrupts Korean text**
Cause: Windows Python's stdout encoding is `cp949`, so the UTF-8 source gets damaged during redirection. The source file is intact and only the extracted copy is broken, which makes it easy to misdiagnose.
Fix: use a binary write — `open(path,'wb').write(s.encode('utf-8'))`.
(Windows / last recurrence: 2026-07)

**If `rg` is a shell-function shim rather than a real binary, some rtk internal filters do not work**
Cause: the shim is caught by `command -v rg`, but does not exist when rtk executes it as a subprocess.
Fix: check the real path with `command -v rg`; if it is a shim, actually install ripgrep (e.g., `apt install ripgrep`).

**Korean text sent through AskUserQuestion gets corrupted**
Cause: on long structured Korean input the `\uXXXX` escapes break and the Korean the model produced is itself damaged (observed: `비밀번호`→`뱄뱈호`, `형상관리자`→`슬파리관자`). 11% failure rate (11 of 101: 8 user rejections, 3 InputValidationError).
Fix: write Korean in AskUserQuestion arguments as **literal UTF-8 characters**, never as hand-recalled `\uXXXX` escapes. Once a string is corrupted it gets copied forward into later turns.
(last recurrence: 2026-08)

## Permissions & settings

**A wrapper prefix bypasses permission patterns entirely**
Cause: the only wrappers the permission matcher strips are `timeout`/`nice`/`nohup`/`env`. `rtk git push --force` or `rtk proxy git push --force` matches no `git push …` pattern anywhere and passes through the `Bash(rtk:*)` allow.
Fix: write dangerous-command patterns in the generalized form with a leading `*` — `Bash(* git push *--force*)`. Adding per-wrapper mirrors does not scale.
(last recurrence: 2026-07)

**Session-start markers (`RTK-MISSING` etc.) get missed and the user's task is handled first**
Cause: markers are text signals with no enforcement, and the user message right after them grabs the attention.
Fix: check the markers in the session-start output **before** handling the user's request. (last recurrence: 2026-07)

**Fixed a hook but the session-start marker does not appear**
Cause: hooks in `.claude/settings.json` are loaded only at session start. `/clear` empties only the conversation context and does not re-read hooks.
Fix: quit and restart Claude. Restart first, before suspecting the hook logic itself.
(last recurrence: 2026-07)

**Hook edits blocked by the permission-mode classifier**
Cause: edits to settings.json hooks/permissions trip the classifier regardless of allow rules
Fix: state the user-approval basis at the very top of the delegation prompt
(last recurrence: 2026-07)

## Output style & plugins

**Plugin `force-for-plugin` silently overrides user outputStyle**
Cause: a plugin carrying this field replaces the configured output style without notice
Fix: if answer tone changes after installing a plugin, check this field first
(last recurrence: 2026-07)

**brief output style: two footguns**
Cause: removing `keep-coding-instructions: true` silently drops the built-in engineering instructions; editors flag `outputStyle` as "Property not allowed" (key absent from public schema) though it works
Fix: keep the key; ignore the schema warning
(last recurrence: 2026-07)

## Agents & delegation

**Agent spawns with a `name` param idle without doing any work**
Cause: named spawns (background AND sync) route through the teammate mailbox and never receive their instructions — 6 no-ops across 3 sessions; unnamed spawns ran fine every time (sync and background alike).
Fix: spawn subagents unnamed — background is fine, the `name` param alone is the trigger; distrust idle notifications — verify outputs (files/commits) before proceeding.
(last recurrence: 2026-07)

**Transcript JSONL misleads when measuring agents/parallelism**
Cause: tool_result always reports `"status":"async_launched"` whatever `run_in_background` says; JSONL stores one content block per line, so "two tool_use on one line" greps return 0; `isSidechain:true` never appears, so subagent internals (token spend, slow calls) are not derivable at all.
Fix: group tool_use by `message.id` for fan-out width; judge real parallelism only by overlapping async_launched→task-notification intervals (`bash scripts/fanout.sh 99 --all`); metrics from inside an agent must be self-reported via the delegation prompt.
(last recurrence: 2026-08)

## Template machinery

**A path added to OR removed from template-update.sh's paths array takes effect in derived projects only on the SECOND run**
Cause: run 1 executes the project's old script copy — it checks out scripts/ (bringing the new array) but still iterates the old in-memory array. Addition lag was misread as "cheatsheet 누락"; removal lag means a removed path (e.g. ci.yml) still gets synced/clobbered one last time on run 1.
Fix: skills are immune since the array syncs `.claude/skills` wholesale (a4bc58e); when changing the paths array, expect the lag — check `git diff` after run 1 (restore clobbers from it); safe from run 2.
(last recurrence: 2026-07)

**Concurrent sessions on one repo silently author conflicting edits to the same rule file**
Cause: two sessions processed the same user feedback in parallel; both rewrote the same orchestrator.md bullet — push was rejected and the rebase needed a semantic (not textual) merge.
Fix: `git fetch` and check ahead/behind before authoring template-rule commits; on conflict, merge both rule sets' obligations instead of picking a side.
(last recurrence: 2026-08)

## Project stack

**shadcn CLI v3 has no --base-color flag; non-interactive init fails**
Cause: CLI v3 replaced it with explicit base/preset flags.
Fix: `npx shadcn init -b radix -p nova --yes` (-b base, -p preset)
(last recurrence: 2026-08)

**`go mod tidy` fails with a toolchain error after adding pgx/v5**
Cause: pgx/v5 v5.10+ requires Go >=1.25; go.mod pinned lower (1.23).
Fix: set `go 1.25` in go.mod.
(last recurrence: 2026-08)

**Parallel `go test ./...` wipes sibling packages' test schema**
Cause: the db package's migration test runs up/down/up against TEST_DATABASE_URL; default package-level parallelism lets its down step drop tables other packages' integration tests are using mid-run.
Fix: always run `go test -p 1 ./...` (serial packages) against the dedicated autocert_test DB.
(last recurrence: 2026-08)

**List sort tests fail: uppercase sorts before lowercase**
Cause: postgres:16-alpine defaults to C collation, so string ORDER BY is byte-order ('AutoCert' < 'app-01') — locale-aware expectations are wrong.
Fix: write test expectations against C collation (or ORDER BY lower(col) if locale order is a product requirement).
(last recurrence: 2026-08)
