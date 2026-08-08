# GOTCHAS

Things we got burned by once, and how to avoid them. Cap: 20 entries / 100 lines.
Add an entry only when **all** 4 conditions hold: time was actually lost / reproducible / not obvious from docs alone / still valid next time.
Entries are written in English; user quotes and error messages stay verbatim in their original language.

## Environment & tools

**On Windows, extracting with `python -c "print(...)" > file` corrupts Korean text**
Cause: Windows Python stdout is `cp949`, so UTF-8 gets damaged during redirection; only the extracted copy breaks, misleading diagnosis.
Fix: use a binary write — `open(path,'wb').write(s.encode('utf-8'))`. (Windows / last recurrence: 2026-07)

**If `rg` is a shell-function shim rather than a real binary, some rtk internal filters do not work**
Cause/Fix: the shim passes `command -v rg` but does not exist when rtk executes it as a subprocess; install real ripgrep (e.g., `apt install ripgrep`).

**Korean text sent through AskUserQuestion gets corrupted**
Cause: on long structured Korean input, hand-recalled `\uXXXX` escapes break (observed: `비밀번호`→`뱄뱈호`); 11% failure rate; corrupted strings get copied forward into later turns.
Fix: write Korean in AskUserQuestion arguments as **literal UTF-8 characters**, never `\uXXXX` escapes. (last recurrence: 2026-08)

## Permissions & settings

**A wrapper prefix bypasses permission patterns entirely**
Cause: the matcher strips only `timeout`/`nice`/`nohup`/`env`; `rtk git push --force` matches no `git push …` pattern and passes through the `Bash(rtk:*)` allow.
Fix: write dangerous-command patterns generalized with a leading `*` — `Bash(* git push *--force*)`; per-wrapper mirrors do not scale. (last recurrence: 2026-07)

**Session-start markers: check them first; `/clear` does not reload hooks**
Cause: markers are unenforced text signals easily eclipsed by the user's message; `.claude/settings.json` hooks load only at session start, and `/clear` does not re-read them.
Fix: check markers in the session-start output **before** the user's request; after editing hooks, restart Claude (not `/clear`) before suspecting the hook logic. (last recurrence: 2026-07)

**Hook edits blocked by the permission-mode classifier**
Cause: edits to settings.json hooks/permissions trip the classifier regardless of allow rules
Fix: state the user-approval basis at the very top of the delegation prompt (last recurrence: 2026-07)

**`.env.*` files are blocked from Read/Edit, so agents cannot add new env var placeholders**
Cause: the permission deny rule for secret-leak prevention covers `.env.example` too, not just live `.env`.
Fix: document new variables in docs/manual/INSTALL.md·SENSOR.md and have the user add them to `.env.example` by hand. (last recurrence: 2026-08)

## Output style & plugins

**Output-style footguns: plugin override and brief style**
Cause: a plugin carrying `force-for-plugin` replaces the configured output style without notice; in brief style, removing `keep-coding-instructions: true` silently drops the built-in engineering instructions (editors flag `outputStyle` as "Property not allowed" though it works).
Fix: if answer tone changes after installing a plugin, check that field first; keep the key and ignore the schema warning. (last recurrence: 2026-07)

## Agents & delegation

**Agent spawns with a `name` param idle without doing any work**
Cause: named spawns (background AND sync) route through the teammate mailbox and never receive their instructions — 6 no-ops across 3 sessions; unnamed spawns ran fine every time.
Fix: spawn subagents unnamed (the `name` param alone is the trigger); distrust idle notifications — verify outputs (files/commits) before proceeding. (last recurrence: 2026-07)

**Transcript JSONL misleads when measuring agents/parallelism**
Cause: tool_result always reports `"status":"async_launched"` whatever `run_in_background` says; JSONL stores one content block per line, so "two tool_use on one line" greps return 0; `isSidechain:true` never appears.
Fix: group tool_use by `message.id` for fan-out width; judge parallelism only by overlapping async_launched→task-notification intervals (`bash scripts/fanout.sh 99 --all`); agent-internal metrics must be self-reported. (last recurrence: 2026-08)

## Template machinery

**A path added to OR removed from template-update.sh's paths array takes effect in derived projects only on the SECOND run**
Cause: run 1 executes the project's old script copy — it checks out scripts/ (bringing the new array) but still iterates the old in-memory array; removal lag means a removed path (e.g. ci.yml) still gets synced/clobbered one last time on run 1.
Fix: skills are immune (`.claude/skills` syncs wholesale, a4bc58e); expect the lag when changing the array — check `git diff` after run 1 (restore clobbers from it); safe from run 2. (last recurrence: 2026-07)

**Concurrent sessions on one repo silently author conflicting edits to the same rule file**
Cause: two sessions processed the same user feedback in parallel; both rewrote the same orchestrator.md bullet — push was rejected and the rebase needed a semantic (not textual) merge.
Fix: `git fetch` and check ahead/behind before authoring template-rule commits; on conflict, merge both rule sets' obligations instead of picking a side. (last recurrence: 2026-08)

## Project stack

**Stack setup version traps: shadcn CLI v3 flags, and pgx/v5 vs the go.mod toolchain**
Cause: shadcn CLI v3 dropped `--base-color` (non-interactive init fails); pgx/v5 v5.10+ requires Go >=1.25 while go.mod was pinned at 1.23, so `go mod tidy` errors.
Fix: `npx shadcn init -b radix -p nova --yes` (-b base, -p preset); set `go 1.25` in go.mod. (last recurrence: 2026-08)

**`go test ./...` in parallel wipes the test schema out from under sibling packages / other worktrees**
Cause: the db package runs up/down/up against TEST_DATABASE_URL; package-level parallelism lets its down step drop tables mid-run, and a worktree on a newer migration leaves the shared DB at a version other branches lack ("no migration found for version N").
Fix: always `go test -p 1 ./...`, against a disposable per-worktree DB (createdb/dropdb) — never the shared autocert_test. (last recurrence: 2026-08)

**List sort tests fail: uppercase sorts before lowercase**
Cause: postgres:16-alpine defaults to C collation, so string ORDER BY is byte-order ('AutoCert' < 'app-01') — locale-aware expectations are wrong.
Fix: write test expectations against C collation (or ORDER BY lower(col) if locale order is a product requirement). (last recurrence: 2026-08)

**Appending paths after the top-level `security:` block silently corrupts backend/api/openapi.yaml**
Cause: YAML top-level key ordering — paths inserted below `security:` fall outside the `paths:` map; the file stayed broken from the #7 merge unnoticed.
Fix: keep `security:` outside/after `paths:`; TestOpenAPISpecMatchesRoutes now fails at parse time — update the spec in the same commit as any route change. (last recurrence: 2026-08)

**Audit insert must be the last statement in its transaction**
Cause: implicit convention — the global advisory lock taken by the audit hash chain is held until tx end; inserting earlier widens lock contention and breaks chain-ordering assumptions.
Fix: in any new mutation path, make the AuditEvent insert the final operation inside the same transaction. (last recurrence: 2026-08)

**Migrations: version numbers have gaps, and a partial-index column swap breaks existing ON CONFLICT arbiters (42P10)**
Cause: 0003/0005 were dropped during development; `ON CONFLICT (col) WHERE ...` infers one specific partial unique index, and 0010 swapped `uq_targets_network_ip` for `(executor_scope, ip_address)`.
Fix: number new migrations max(existing)+1, never fill gaps; ship an index swap and every ON CONFLICT arbiter update in the SAME merge/land unit — the server auto-applies migrations at boot, so a half-landed swap breaks scanning immediately. (last recurrence: 2026-08)

**Sensor enroll CA-fingerprint pin fails when the gateway presents a leaf-only TLS chain**
Cause: `verifyPinnedChain` looks for the pinned cert inside the *presented* chain, not the trust store; sensorgw initially set `Certificates: [][]byte{leaf}`.
Fix: present leaf+CA DER; unit tests using a mock chain of {leaf, CA} pass either way, so add a regression test against the real listener. (last recurrence: 2026-08)

**Adding a server-side status enum value without updating the frontend union type silently freezes UI polling**
Cause: the new value is missing from the poll-target filter and falls through to the else branch (rendered as "실패").
Fix: update the domain enum, backend/api/openapi.yaml, and frontend/lib/api.ts in the same commit. (last recurrence: 2026-08)
