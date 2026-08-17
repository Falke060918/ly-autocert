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

**`.env.*` files are blocked from Read/Edit, so agents cannot add new env var placeholders**
Cause: the permission deny rule for secret-leak prevention covers `.env.example` too, not just live `.env`.
Fix: document new variables in docs/manual/INSTALL.md·SENSOR.md, wire them through scripts, and have the user add them to `.env.example` by hand. (last recurrence: 2026-08)

## Agents & delegation

**A named agent spawn idles, the transcript misreports parallelism, and two agents in one tree smear the commits**
Cause: named spawns (background AND sync) route through the teammate mailbox and never receive their instructions; tool_result always reports `"status":"async_launched"` and `isSidechain:true` never appears; and an unfinished edit from agent A is still in the tree when agent B stages, so B's commit carries A's half-done change — neither commit is reviewable on its own.
Fix: spawn subagents unnamed and verify outputs (files/commits) instead of trusting idle notifications; judge parallelism only by overlapping async_launched→notification intervals; isolate parallel work that can touch the same files in its own git worktree and keep same-tree delegation sequential. (last recurrence: 2026-08)

## Backend, DB & tests

**Backend tests: no local Go toolchain, parallel packages wipe the schema, and cross-package state leaks in**
Cause: `go`/`gofmt` are not guaranteed to exist on a dev machine (this one has none); the db package runs up/down/up against TEST_DATABASE_URL so package parallelism drops tables mid-run; and migration 0012's seeded `기본 만료 알림` policy plus a bootstrap admin left by an earlier package join notification assertions — running a single package hides all three.
Fix: run in `golang:1.25-alpine` mounted at `backend/` with a disposable per-run postgres, always `go test -p 1 -count=1 ./...` as a whole; in store tests disable every enabled policy at setup and restore it afterwards. (last recurrence: 2026-08)

**`gofmt -w ./internal` in a container rewrites the whole tree to LF, and `gofmt -l` lists every file**
Cause: the working tree is CRLF while the container writes LF, so a wide `-w` run reformats files outside the change set and `git status` shows 100+ modified; `gofmt -l` flags everything for the same reason, hiding the real differences. main also carries 7 pre-existing unformatted files, so "it was already dirty" is not a safe assumption either way.
Fix: never run `gofmt -w` beyond the files you touched; to compare, `tr -d '\r' < f > a.go && gofmt a.go | cmp -s - a.go`. (last recurrence: 2026-08)

**Chain completeness judged "up to the root" marks every correct server as danger**
Cause: well-behaved TLS servers deliberately omit the root CA from the presented chain, so "does it reach a self-signed root" turns the entire estate red; the `incomplete_chain` signal actually worth raising is a *missing intermediate*.
Fix: judge whether the presented chain links unbroken from the leaf up to a CA (same rule as SSL Labs), not whether it terminates at a root. (last recurrence: 2026-08)

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

## Release & packaging

**A release has 4 version sync points, a fixed CHANGELOG heading, and dies if you `--amend` after packaging**
Cause: `scripts/bundle-offline.sh` asserts `VERSION` against the compose tag defaults, `frontend/package.json`+`package-lock.json` (3 spots: lock top level and `packages[""]`) and a `## v<version> (` grep — a heading like `## 1.1.0 - date` fails checks 4/6; and MANIFEST freezes the source commit, so amending the release commit leaves shipped packages pointing at a dangling hash. v1.0.0 shipped with the frontend still at 0.1.0 because check 7 did not exist yet.
Fix: bump all 4 places together, keep the heading exactly `## v<version> (<date>)`, and if you amend the release commit re-run the bundler with `--skip-build` to realign MANIFEST with HEAD. (last recurrence: 2026-08)

## Frontend & deployment

**Compose derives the project name from the directory, so an upgrade comes up with an empty database**
Cause: volumes are named `<project>_pgdata` and the project defaults to the install directory name — installing a new version into a new directory creates a fresh volume while the old data sits there invisible, indistinguishable from data loss.
Fix: pin `COMPOSE_PROJECT_NAME` (default `autocert`) in `.env`, inherit the directory-derived name when adopting an existing install, verify with `docker compose config` — and when the value is empty drop the `-p` flag entirely, because `docker compose -p ""` dies with "project name must not be empty" instead of falling back. (last recurrence: 2026-08)

**Making `main` the scroll container freezes route-change scroll restore, and every check stays green**
Cause: pinning the app shell by giving `<main>` `overflow-y-auto` moves scrolling off the document, so Next's scroll-to-top on navigation, `scrollIntoView` and anchor jumps all address a window that never scrolls — tsc, lint and `next build` cannot see any of it.
Fix: keep the document as the scroll container, pin the sidebar/topbar with `position: sticky`, and offset anchors with `html { scroll-padding-top }`. (last recurrence: 2026-08)

**TLS flags lock you out: `SESSION_COOKIE_SECURE` over plain HTTP, HSTS before a trusted cert**
Cause: with Secure set on HTTP the API returns 200 with Set-Cookie and only the browser drops it, so the screen bounces back to /login while every server log says success; HSTS removes the "proceed anyway" escape hatch for a self-signed cert and the pin outlives the config change (max-age).
Fix: flip the reverse proxy and `SESSION_COOKIE_SECURE` together; enable HSTS only after the customer's own trusted certificate is installed. (last recurrence: 2026-08)

**`page_size` over 200 is a 400, and every local check stays green**
Cause: every list endpoint caps `page_size` at 200; the dashboard asked for 500 inside a `Promise.all`, so one rejected fetch took the whole screen down to an error alert — tsc, lint, unit tests and `next build` all pass because the limit lives on the server.
Fix: keep bulk reads at ≤200 or loop pages with a hard page ceiling (dashboard expiry horizon: 200 × 3 pages). (last recurrence: 2026-08)

**Two views sharing one set of URL query params (sort/filter/page) turn a view switch into a 400**
Cause: the list view and the server view have different sort-key whitelists, so a `sort` value that is only valid in one of them leaks into the other when the URL survives the switch, and the server rejects the request.
Fix: namespace the second view's params with a prefix (`srv_`) so nothing carries over; share a param only when both views actually accept the same value set. (last recurrence: 2026-08)

**A fresh worktree typechecks against a fake `tsc`, and `frontend/AGENTS.md` is not in the repo**
Cause: with no `node_modules`, `npx tsc` fetches an unrelated package instead of the project's TypeScript; even after `npm install`, Next's generated global types (`LayoutProps` and friends) exist only once `npx next typegen` has run; and `frontend/AGENTS.md` is gitignored with generation off (`agentRules: false`), so a delegation prompt that points at it sends the agent to a file a fresh checkout does not have.
Fix: `npm install` → `npx next typegen` → `npx tsc --noEmit`, and treat `next build` as the authoritative typecheck. (last recurrence: 2026-08)

**Running the frontend without the Go backend needs zero product-code changes**
Cause: it looks like the console cannot be exercised without the backend + DB, but the Next rewrites proxy reads the `BACKEND_INTERNAL_URL` env var and passes Set-Cookie through, so a fake backend can stand in for the whole API — login included — with no edits to product code.
Fix: `node frontend/mock/server.mjs` (:8090, untracked reusable dev tool — do not commit) + `BACKEND_INTERNAL_URL=http://localhost:8090 npx next dev`. (last recurrence: 2026-08)
