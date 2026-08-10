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

**`gofmt -l .` lists all 147 files on a Windows checkout**
Cause: the working tree is CRLF, and gofmt reports every CRLF file as unformatted — the output carries no signal about actual formatting drift.
Fix: do not use `gofmt -l` as a gate here; if you must check, run it on a copy stripped with `sed 's/\r$//'`. (Windows / last recurrence: 2026-08)

## Permissions & settings

**Hook/permission edits: `/clear` does not reload them, and the classifier blocks the edit**
Cause: `.claude/settings.json` hooks load only at session start and `/clear` does not re-read them; edits to hooks/permissions trip the permission-mode classifier regardless of allow rules; session-start markers are unenforced text easily eclipsed by the user's message.
Fix: check markers **before** the user's request; state the user-approval basis at the top of the delegation prompt; after editing hooks restart Claude (not `/clear`). (last recurrence: 2026-07)

**`.env.*` files are blocked from Read/Edit, so agents cannot add new env var placeholders**
Cause: the permission deny rule for secret-leak prevention covers `.env.example` too, not just live `.env`.
Fix: document new variables in docs/manual/INSTALL.md·SENSOR.md and have the user add them to `.env.example` by hand. (last recurrence: 2026-08)

## Agents & delegation

**Agent spawns idle when given a `name`, and the transcript misreports parallelism**
Cause: named spawns (background AND sync) route through the teammate mailbox and never receive their instructions; separately tool_result always reports `"status":"async_launched"` and `isSidechain:true` never appears.
Fix: spawn subagents unnamed and verify outputs (files/commits) instead of trusting idle notifications; judge parallelism only by overlapping async_launched→notification intervals. (last recurrence: 2026-08)

## Backend, DB & tests

**Backend verification may have no local Go toolchain, and parallel `go test ./...` wipes the test schema**
Cause: `go`/`gofmt` are not guaranteed to exist on a dev machine (this one has none); the db package runs up/down/up against TEST_DATABASE_URL, so package-level parallelism lets its down step drop tables mid-run.
Fix: run in `golang:1.25-alpine` mounted at `backend/` with a reachable postgres in `TEST_DATABASE_URL`; always `-p 1` against a disposable per-run DB, never a shared one. (last recurrence: 2026-08)

**Notification tests fail on state they did not create — run the suite whole, never per package**
Cause: migration 0012 seeds `기본 만료 알림` (inbox, all certs) so detection assertions pick up its events, and a bootstrap admin left behind by an earlier package joins the fan-out recipient set — `-p 1` alone does not isolate either.
Fix: run `go test -p 1 -count=1 ./...` as a whole; in store tests disable every enabled policy at setup and restore it afterwards. (last recurrence: 2026-08)

**Audit insert must be the last statement in its transaction**
Cause: implicit convention — the global advisory lock taken by the audit hash chain is held until tx end; inserting earlier widens lock contention and breaks chain-ordering assumptions.
Fix: in any new mutation path, make the AuditEvent insert the final operation inside the same transaction. (last recurrence: 2026-08)

**Migrations: numbering gaps, partial-index ON CONFLICT (42P10), and a new column's INSERT path**
Cause: 0003/0005 were dropped; `ON CONFLICT (col) WHERE …` binds one specific partial index and 0010 swapped it; a DEFAULT+backfill column silently leaves *future* inserts on the default — 0015's `scan_jobs.origin` recorded every new sweep chunk as `'manual'`, and the successful backfill made the check look green.
Fix: number new migrations max+1 (never fill gaps); ship an index swap and every arbiter in the SAME merge; when adding a column, grep every INSERT into that table in the same commit. (last recurrence: 2026-08)

**Postgres params inside `COALESCE`/`CASE` are inferred as text — 42804 / 42P08 at runtime only**
Cause: an uncast `$1` defaults to text in those contexts, and using the same `$1` as both text and `::inet` in one INSERT yields "inconsistent types deduced"; Go compiles and vets clean either way.
Fix: cast every parameter at its use site (`$1::int`, `$1::inet`) and exercise the query in a DB test — nothing before runtime catches it. (last recurrence: 2026-08)

**A "N concurrent requests" race test passes even with the guard removed**
Cause: the window on an unauthenticated create path (initial admin setup) is microseconds, so plain fan-out never collides and the test proves nothing about the lock.
Fix: hold the same advisory lock/row in a parallel transaction and assert the request blocks until it commits (`TestInitialSetupWaitsForConcurrentSetupTransaction`). (last recurrence: 2026-08)

**Legacy TLS 1.0/1.1 hosts are silently missing from scan results**
Cause: Go's client default `MinVersion` is TLS 1.2, so those endpoints fail the handshake and are recorded as unreachable — indistinguishable from a firewall drop, and no error names TLS.
Fix: set `MinVersion: tls.VersionTLS10` on the collection-path `tls.Config` only (`ScanEndpoint`); every other TLS path stays at 1.2 (`TestScanEndpointCollectsLegacyTLSVersions`). (last recurrence: 2026-08)

**Contract drift: openapi.yaml corrupts silently, and a new status enum freezes UI polling**
Cause: paths appended below the top-level `security:` block fall outside the `paths:` map; a status value missing from the frontend union type is absent from the poll-target filter and falls through to the else branch (rendered as "실패").
Fix: keep `security:` outside/after `paths:`; change the domain enum, backend/api/openapi.yaml and frontend/lib/api.ts in the SAME commit — `TestOpenAPISpecMatchesRoutes` fails at parse time. (last recurrence: 2026-08)

**A request DTO without `DisallowUnknownFields` turns a new column into a silently unfillable one**
Cause: encoding/json drops unknown keys, so the API answers 201 while discarding the field — `users.email` had a column, a CHECK and recipient logic, yet no way to set it.
Fix: when adding a field, start at the decoder (`decodeUserRequest` is the single entry point) and keep `DisallowUnknownFields` on it; verify a round-trip read, not just the 2xx. (last recurrence: 2026-08)

**A `*json.RawMessage` field cannot tell "key absent" from an explicit JSON `null`**
Cause: encoding/json leaves the pointer nil in both cases, so a PATCH handler silently ignores a caller clearing a field.
Fix: for partial-update DTOs use a non-pointer `json.RawMessage` — it stays nil only when the key is absent. (last recurrence: 2026-08)

## Frontend & deployment

**`SESSION_COOKIE_SECURE=true` over plain HTTP looks like a login failure**
Cause: the API returns 200 with Set-Cookie and only the browser silently drops the cookie, so the screen bounces back to /login while every server log says success.
Fix: keep it `false` unless a TLS terminator actually fronts the console — flip the proxy and this flag together. (last recurrence: 2026-08)

**Enabling HSTS while the certificate is still untrusted locks the console out**
Cause: HSTS removes the browser's "proceed anyway" escape hatch for a self-signed/internal cert, and the pin outlives the config change (max-age).
Fix: turn HSTS on only after the customer's own trusted certificate is installed; otherwise recovery needs a per-browser HSTS reset. (last recurrence: 2026-08)

**eslint `react-hooks/purity` + `set-state-in-effect` block the usual "elapsed time" tick**
Cause: the config rejects `Date.now()` during render AND a bare `setState` inside an effect body, so both obvious ways to keep a ticking clock fail `npm run lint`.
Fix: `useState(() => Date.now())` lazy initializer + a `setInterval` inside the effect that updates it. (last recurrence: 2026-08)

**Frontend e2e must run the production server, and its `/api/*` target is frozen at build time**
Cause: with `output: "standalone"`, `next start` bakes the rewrite target from the build-time `BACKEND_INTERNAL_URL`; and `next dev` in a tree holding a production build serves `_next/static/chunks/*` as 403, killing hydration.
Fix: rebuild after changing `BACKEND_INTERNAL_URL`, and never mix `next dev` into a tree used for a production build. (last recurrence: 2026-08)
