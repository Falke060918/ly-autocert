# GOTCHAS

Things we got burned by once, and how to avoid them. Cap: 20 entries / 100 lines.
Add an entry only when **all** 4 conditions hold: time was actually lost / reproducible / not obvious from docs alone / still valid next time.
Entries are written in English; user quotes and error messages stay verbatim in their original language.

## Environment & tools

**Korean text gets silently corrupted — Windows redirection, and AskUserQuestion escapes**
Cause: Windows Python stdout is `cp949`, so `python -c "print(...)" > file` damages UTF-8 in the extracted copy only; separately, hand-recalled `\uXXXX` escapes in AskUserQuestion break (observed `비밀번호`→`뱄뱈호`) and get copied forward.
Fix: write files binary — `open(path,'wb').write(s.encode('utf-8'))`; write Korean in tool arguments as literal UTF-8 characters, never `\uXXXX`. (Windows / last recurrence: 2026-08)

**`docker run … -w /src` from Git Bash lands in `C:/Program Files/Git/src`**
Cause: MSYS rewrites absolute POSIX-looking arguments into Windows paths before docker sees them, so `-w`, `-v` targets and in-container paths are mangled and the run fails with a confusing "no such file".
Fix: prefix the whole command with `MSYS_NO_PATHCONV=1`, and pass the host side of `-v` as `$(pwd -W)`. (Windows / last recurrence: 2026-08)

## Permissions & settings

**Hook/permission edits: `/clear` does not reload them, and the classifier blocks the edit**
Cause: `.claude/settings.json` hooks load only at session start and `/clear` does not re-read them; edits to hooks/permissions trip the permission-mode classifier regardless of allow rules; session-start markers are unenforced text easily eclipsed by the user's message.
Fix: check markers **before** the user's request; state the user-approval basis at the top of the delegation prompt; after editing hooks restart Claude (not `/clear`). (last recurrence: 2026-07)

**`.env.*` files are blocked from Read/Edit, so agents cannot add new env var placeholders**
Cause: the permission deny rule for secret-leak prevention covers `.env.example` too, not just live `.env`.
Fix: document new variables in docs/manual/INSTALL.md·SENSOR.md, wire them through scripts, and have the user add them to `.env.example` by hand. (last recurrence: 2026-08)

## Agents & delegation

**Agent spawns idle when given a `name`, and the transcript misreports parallelism**
Cause: named spawns (background AND sync) route through the teammate mailbox and never receive their instructions; separately tool_result always reports `"status":"async_launched"` and `isSidechain:true` never appears.
Fix: spawn subagents unnamed and verify outputs (files/commits) instead of trusting idle notifications; judge parallelism only by overlapping async_launched→notification intervals. (last recurrence: 2026-08)

**Two agents in one working tree smear the commit boundaries**
Cause: an unfinished edit from agent A is sitting in the tree when agent B stages and commits, so B's commit carries A's half-done change — neither commit is reviewable or revertable on its own.
Fix: isolate any parallel work that could touch the same files in its own git worktree; keep same-tree delegation strictly sequential. (last recurrence: 2026-08)

## Backend, DB & tests

**Backend tests: no local Go toolchain, parallel packages wipe the schema, and cross-package state leaks in**
Cause: `go`/`gofmt` are not guaranteed to exist on a dev machine (this one has none); the db package runs up/down/up against TEST_DATABASE_URL so package parallelism drops tables mid-run; and migration 0012's seeded `기본 만료 알림` policy plus a bootstrap admin left by an earlier package join notification assertions — running a single package hides all three.
Fix: run in `golang:1.25-alpine` mounted at `backend/` with a disposable per-run postgres, always `go test -p 1 -count=1 ./...` as a whole; in store tests disable every enabled policy at setup and restore it afterwards. (last recurrence: 2026-08)

**A blocked host listener does not block DB tests — put both containers on one docker network**
Cause: "cannot bind a host port" was read as "cannot run postgres", so DB-backed Go tests were skipped for whole sessions; a container published with no `-p` never touches a host listener and is still reachable by service name from another container on the same user-defined network.
Fix: `docker network create` → `docker run -d --network … postgres:16-alpine` (no `-p`) → run the golang container on the same network with `TEST_DATABASE_URL=postgres://…@<container-name>:5432/…`; remove the container and network afterwards. (last recurrence: 2026-08)

**Audit insert must be the last statement in its transaction**
Cause: implicit convention — the global advisory lock taken by the audit hash chain is held until tx end; inserting earlier widens lock contention and breaks chain-ordering assumptions.
Fix: in any new mutation path, make the AuditEvent insert the final operation inside the same transaction. (last recurrence: 2026-08)

**Migrations: numbering gaps, partial-index ON CONFLICT (42P10), and a new column's INSERT path**
Cause: 0003/0005 were dropped; `ON CONFLICT (col) WHERE …` binds one specific partial index and 0010 swapped it; a DEFAULT+backfill column silently leaves *future* inserts on the default — 0015's `scan_jobs.origin` recorded every new sweep chunk as `'manual'`, and the successful backfill made the check look green.
Fix: number new migrations max+1 (never fill gaps); ship an index swap and every arbiter in the SAME merge; when adding a column, grep every INSERT into that table in the same commit. (last recurrence: 2026-08)

**Postgres params inside `COALESCE`/`CASE` are inferred as text — 42804 / 42P08 at runtime only**
Cause: an uncast `$1` defaults to text in those contexts, and using the same `$1` as both text and `::inet` in one INSERT yields "inconsistent types deduced"; Go compiles and vets clean either way.
Fix: cast every parameter at its use site (`$1::int`, `$1::inet`) and exercise the query in a DB test — nothing before runtime catches it. (last recurrence: 2026-08)

**Contract drift: openapi.yaml corrupts silently, and a new status enum freezes UI polling**
Cause: paths appended below the top-level `security:` block fall outside the `paths:` map; a status value missing from the frontend union type is absent from the poll-target filter and falls through to the else branch (rendered as "실패").
Fix: keep `security:` outside/after `paths:`; change the domain enum, backend/api/openapi.yaml and frontend/lib/api.ts in the SAME commit — `TestOpenAPISpecMatchesRoutes` fails at parse time. (last recurrence: 2026-08)

**JSON decoding drops new fields silently, and `*json.RawMessage` cannot see an explicit `null`**
Cause: encoding/json discards unknown keys, so the API answers 201 while throwing the value away (`users.email` had a column, a CHECK and recipient logic yet no way to set it); and a pointer field stays nil both for "key absent" and for JSON `null`, so a PATCH quietly ignores a caller clearing a field.
Fix: keep `DisallowUnknownFields` on the single decoder (`decodeUserRequest`), use a non-pointer `json.RawMessage` in partial-update DTOs, and verify a round-trip read rather than the 2xx. (last recurrence: 2026-08)

## Frontend & deployment

**Compose derives the project name from the directory, so an upgrade comes up with an empty database**
Cause: volumes are named `<project>_pgdata` and the project defaults to the install directory name — installing a new version into a new directory creates a fresh volume while the old data sits there invisible, indistinguishable from data loss.
Fix: pin `COMPOSE_PROJECT_NAME` (default `autocert`) in `.env`, inherit the directory-derived name when adopting an existing install, and verify with `docker compose config`. (last recurrence: 2026-08)

**Install scripts run from two layouts, so `$SCRIPT_DIR/..` is not the install root**
Cause: `scripts/install.sh` executes both from the git checkout (in `scripts/`) and from the extracted package (at the root); a copy of `load-offline.sh` lives in the repo's `scripts/` too, so its presence cannot tell the two layouts apart.
Fix: locate the root by walking up for `docker-compose.yml` and branch on that marker only. (last recurrence: 2026-08)

**TLS flags lock you out: `SESSION_COOKIE_SECURE` over plain HTTP, HSTS before a trusted cert**
Cause: with Secure set on HTTP the API returns 200 with Set-Cookie and only the browser drops it, so the screen bounces back to /login while every server log says success; HSTS removes the "proceed anyway" escape hatch for a self-signed cert and the pin outlives the config change (max-age).
Fix: flip the reverse proxy and `SESSION_COOKIE_SECURE` together; enable HSTS only after the customer's own trusted certificate is installed. (last recurrence: 2026-08)

**eslint `react-hooks/purity` + `set-state-in-effect` block the usual "elapsed time" tick**
Cause: the config rejects `Date.now()` during render AND a bare `setState` inside an effect body, so both obvious ways to keep a ticking clock fail `npm run lint`.
Fix: `useState(() => Date.now())` lazy initializer + a `setInterval` inside the effect that updates it. (last recurrence: 2026-08)

**`page_size` over 200 is a 400, and every local check stays green**
Cause: every list endpoint caps `page_size` at 200; the dashboard asked for 500 inside a `Promise.all`, so one rejected fetch took the whole screen down to an error alert — tsc, lint, unit tests and `next build` all pass because the limit lives on the server.
Fix: keep bulk reads at ≤200 or loop pages with a hard page ceiling (dashboard expiry horizon: 200 × 3 pages). (last recurrence: 2026-08)

**A fresh worktree typechecks against a fake `tsc`, and `frontend/AGENTS.md` is not in the repo**
Cause: with no `node_modules`, `npx tsc` fetches an unrelated package instead of the project's TypeScript; even after `npm install`, Next's generated global types (`LayoutProps` and friends) exist only once `npx next typegen` has run; and `frontend/AGENTS.md` is gitignored with generation off (`agentRules: false`), so a delegation prompt that points at it sends the agent to a file a fresh checkout does not have.
Fix: `npm install` → `npx next typegen` → `npx tsc --noEmit`, and treat `next build` as the authoritative typecheck. (last recurrence: 2026-08)

**Next.js bakes `/api/*` rewrites at build time, and `next dev` poisons a production build tree**
Cause: with `output: "standalone"`, `next start` freezes the rewrite target from the build-time `BACKEND_INTERNAL_URL`; and `next dev` in a tree holding a production build serves `_next/static/chunks/*` as 403, killing hydration.
Fix: rebuild after changing `BACKEND_INTERNAL_URL`, and never mix `next dev` into a tree used for a production build. (last recurrence: 2026-08)
