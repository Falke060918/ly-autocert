# Status protocol — per-unit stage file

The convention for recording which stage a delegated unit is at, as **one line in one file**.
This file — not session memory, not the terminal screen — is the truth, and readers such as the statusline look at nothing else.

**This document is the single source of truth.** Unlike v1, no copy of the one-liner or of the token list lives in an
agent definition file — agents and rules only reference this path. The one real coupling is the reader implementation,
`scripts/statusline.sh`: it matches this document on root anchoring, field splitting, stage tokens, per-stage expiry
(1 hour while in progress, 60 seconds once closed), source labels, worktree number assignment, and empty-note handling.
Change one side and you change the other in the same commit.

## Location and name

- Path: `<메인 저장소 루트>/.claude/perf/units/<unit>.status` — one file per unit. The filename *is* the unit name.
- **The root must be anchored to the main repository.** Written as the plain relative path `.claude/perf/units/`, an agent
  running inside an isolated worktree creates the file inside its own worktree, and the reader running in the main
  repository never sees it.
  Take the root from git's common dir — it points at the same place wherever you run it (main checkout top level, a
  subdirectory of it, or a linked worktree).

  ```bash
  root="$(git rev-parse --git-common-dir 2>/dev/null)"; root="${root%.git}"; root="${root%/}"; dir="${root:-.}/.claude/perf/units"
  ```

  `--git-common-dir` gives `.git` at the main top level, `../.git` in a subdirectory of main, and the main repository's absolute path `<repo>/.git` in a linked worktree.
  Strip the trailing `.git` and any leftover slash and they become the empty string (→ `.`), `..`, and `<repo>` respectively — all pointing at the same main repository root.
  Outside a git repository the value is empty and falls through to `.` — in that case write into the current directory and move on silently.
- **`<unit>` is the Work Order's sub-unit ID when the work has one** — `ORCA-20260827-001-B`, used as is
  (issuance rules: `.claude/conventions/work-order.md`). There is no mapping table: the sub-unit ID *is* the filename,
  and because the ID is unique by construction, the hand-appended `-2` (`final-polish-2`) is never needed again.
  With no Work Order (an S-sized job, or work with no spec yet) use the kebab-case name from the delegation prompt —
  an ID is not forced on small work — and with neither, fall back to the branch name (see "Writing rules" below).
- Characters: ASCII letters, digits, and hyphens only — **uppercase only inside a Work Order ID**, everything else
  lowercase kebab-case (`status-protocol`, `readme-rewrite`).
- **A name containing a slash is never used as is.** Derived from a branch name, an owner prefix such as
  `Falke060918/status-protocol` produces a subdirectory that does not exist in the path, and the write fails with `No such file or directory`.
  Drop the owner prefix and every slash, and use **only the last path segment** in lowercase kebab-case (`${unit##*/}` → `status-protocol`).
- Use the name **exactly as given in the delegation prompt**. The receiving side does not shorten or alter it on its own —
  a mismatched name splits one unit across two files and the reader misses both.
- **Do not touch `.gitignore`.** `.claude/perf/` is already ignored. There is no reason to add a new ignore rule.

## Format

Exactly **one line**: plain text in **five fields** separated by pipes (`|`). It is not JSON.

```
<stage>|<unit>|<epoch-seconds>|<한 줄 노트>|<source>
```

| Field | Contents |
| --- | --- |
| `<stage>` | One of the six tokens below. |
| `<unit>` | The unit name. Must equal the filename (`<unit>.status`). |
| `<epoch-seconds>` | Unix seconds at write time. `date +%s`. |
| `<한 줄 노트>` | **What the work is.** Does not repeat the stage — see "The note field" below. |
| `<source>` | Who wrote it. `<kind>` or `<kind>@<워크트리 이름>`. kind is one of exactly three: `main`/`sub`/`child`. |

- **Put no pipe character (`|`) and no newline in the note** — the reader splits the fields wrongly.
  A single stray pipe pushes the tail of the note into the source field (the label disappears),
  and with source attached as well the line has six fields, so the reader discards that unit entirely (without an error).
  That is why the one-liner below does not write the note directly but substitutes pipes with spaces via `${note//|/ }` — it does not rely on the rule alone.
- **A line with an empty note (`write|unit|1755583380||sub`) is not discarded** — the reader renders it with the memo field left out.
  An empty note breaks a recommendation, not the protocol, so writing it empty beats not writing the line at all because nothing came to mind.
  Lines missing trailing fields entirely (`write|unit|1755583380|노트`, `write|unit|1755583380`) are treated the same way.
- **The reader counts fields from the front and tolerates a short tail.** Checking for an exact field count is forbidden —
  **old four-field lines with no source field still render normally** (backward compatibility is mandatory). Only the origin-label slot is left blank.
- The file is always one line. If it ever becomes several, the reader treats only the first line as valid.
- The reader parses with bash builtins only (`IFS='|' read -r -a`, with length bounded by `read -n`). It presumes no external
  tool such as jq — do not convert this to a structured format.

## The source field and origin labels

| kind | Meaning | Screen label |
| --- | --- | --- |
| `main` | A line written by the main session itself | `[M]` |
| `sub` | An internal subagent. `sub` when it belongs to no worktree | `[S]` |
| `sub@<워크트리 이름>` | The internal subagent driving that worktree (the Orca integration proxy) | `[S/W1]` |
| `child@<워크트리 이름>` | The child agent itself, running **inside** that worktree | `[W1]` |

- **Do not invent a new kind.** These three are all of them, and an unknown kind renders without a label (same as an old line).
- Put no `|`, whitespace, or newline in the source field. Labels are ASCII only — it is a fixed-width alignment column, so
  East-Asian Ambiguous characters mixed in throw the columns off differently on every terminal. A label is at most 8 columns.
- **The writer does not decide the worktree number.** The writer records only the worktree **name**, and the reader
  sorts those names **alphabetically and assigns numbers from 1**. That is how the proxy (`sub@a`) and the child (`child@a`)
  attached to the same worktree **always get the same number**, and why the numbers do not shift as units come and go.
- How a child obtains its own worktree name: the last path segment of `git rev-parse --show-toplevel`
  (or the last path segment of the branch name). Lowercase kebab-case, by the same rule as unit names.

## The note field

- **The note says "what the work is". It does not repeat the stage.** The stage field on the left already shows the stage, so
  writing the stage there again makes what is actually going on disappear from the screen.
- Good: `import 스킬 개선`, `상태줄 대시보드 개선`, `template-update 주석 정리`
- Bad: `검증 중`, `작업완료`, `자식 생성`, `probe`, `커밋함`
- Free-form, but keep it short — around 40 characters. **As a rule, do not leave it empty.**
- **Within one unit the note stays largely unchanged** — rewriting the same wording as the stage changes is normal.
  Swapping in a detail is fine as long as it is still 'what the work is'.
- The one exception is `fail` — there, write in one line **what you got stuck on**.

## stage tokens

| stage | Meaning |
| --- | --- |
| `probe` | 파악중 — working out the scope and the target files |
| `write` | 작성중 — actually changing files |
| `verify` | 검증중 — build/test/review pending |
| `done` | 완료 |
| `landed` | 반영 — the result has been merged into the main repository. The stage after `done` |
| `fail` | 실패 — one line in the note on what you got stuck on |

The normal flow is `probe → write → verify → done → landed`, and a run that ends in failure has `fail` as its last line.
`landed` is written not by the unit's owner but by **whoever performs the land (merge)** — the merge happens in the main repository after the unit is finished, so src is `sub` or `main`.
Those six tokens are all of them. Do not invent a new one.

## Writing rules

- On every transition, **overwrite the whole file.** It is not an append — the file always holds only the current line.
- The one-liner (anchor the root → clean the name → create the directory → redirect). Swap only `write`, the note, and `src` to fit the situation:

  ```bash
  root="$(git rev-parse --git-common-dir 2>/dev/null)"; root="${root%.git}"; root="${root%/}"; dir="${root:-.}/.claude/perf/units"; unit=my-unit; unit="${unit##*/}"; note='상태줄 대시보드 개선'; src=sub; { mkdir -p "$dir" && printf '%s|%s|%s|%s|%s\n' write "$unit" "$(date +%s)" "${note//|/ }" "${src//[|[:space:]]/}" > "$dir/$unit.status"; } 2>/dev/null || true
  ```

  The trailing `2>/dev/null || true` is not decoration — it guarantees best-effort **by structure rather than by wording**.
  Without it a failed write leaks out as exit code 1 and stderr, making the real work look like it failed.
  `${src//[|[:space:]]/}` is there for the same reason — a pipe or whitespace in source throws the fields off.
- Ownership divides as follows. **The principle is that two parties never write over each other on one unit, and there are exactly two exceptions — the Orca proxy's backstop, and the land performer stamping `landed` after the unit is finished.**
  - implementer: `probe` → `write` → `verify` → `done` or `fail`. src is `child@<워크트리 이름>` inside a worktree, `sub` in the main repository
  - reviewer: `verify` → `done` or `fail`. Same src rule as implementer
  - explorer: `probe` at the start, `done` at the end. Same src rule as implementer
  - When the main session writes directly: src is `main`
  - The land performer (the implementer running `/land`, or the main session): `landed` **after that unit's branch has actually been merged**.
    It is not written by the unit's own child — the merge happens in the main repository after the worktree is cleaned up, so src is `sub` (or `main` if the main session does it).
    Reuse the wording the unit had been using as the note
  - The Orca integration proxy: **it is a backstop.** Right after worktree create it writes `probe` (src `sub@<워크트리 이름>`),
    and at the end it **reads the status file first** and leaves it alone if the child already wrote `done`/`fail`.
    Only when the child could not get that far (it died or stalled) does the proxy close it out with `done` (commit succeeded) or `fail`.
  - **Child-first principle**: the child inside the worktree writes its own stages (`child@<워크트리 이름>`).
    If the proxy writes only up to `verify` and stops, finished work stays on screen as '검증' forever — the child does the closing, and the proxy stamps it only when the child has failed.
  - planner: **does not write.** It has no shell tool, so the one-liner cannot run there, and its `Write` reaches `docs/` only (the status file lives under `.claude/perf/`, outside that scope) — it is not a workaround for this file. A planner unit producing no status file — and therefore no line on the dashboard — is normal.
- If neither a Work Order sub-unit ID nor a unit name came with the delegation prompt (the third priority in "Location and name"),
  derive it from the **last path segment** of the branch name, and if there is none of that either, **skip silently.**
- **This is best-effort.** If a write fails, ignore it and carry on with the real work. status never stops the work, fails it, or
  drags it into a retry loop. Nor is the failure reported as an error.

## The reader

- **If the file does not exist, that unit does not appear on screen at all.** Absence is not failure — the unit has simply not been delegated yet, or it is a lightweight delegation that does not write status. The reader does not stamp a separate label such as "not started".
- **A stale file from a past session is harmless where it lies** — the reader ignores expired lines. There is no need to delete it.
- **An in-progress line (`probe`/`write`/`verify`) drops off the screen 1 hour (3600 seconds) after its last update.** A unit that has gone
  that long without an update is not work in progress but an abandoned run.
- **A closed line (`done`/`landed`/`fail`) drops off the screen after 60 seconds.** A finished unit has nothing left to show, yet it kept
  sitting on screen and pushing in-progress lines out.
- Both expiries **remove the line from the render only, never the file**, and the writer is unaffected by this rule — there is no separate cleanup command.
- On a malformed line the reader does not die; it skips that unit only. The status display is supplementary information, not a blocking condition.
- The reader must find **the same main repository root as the writer**. If only one side is off, the file is written fine but nothing shows on screen.
  `scripts/statusline.sh` starts from `workspace.project_dir` in the session JSON and walks up looking for `.git` (for a linked worktree's
  `gitdir: <메인>/.git/worktrees/<이름>` it strips that tail to get back to main), and uses the spot as is if it finds nothing —
  the result is the same place as the `--git-common-dir` one-liner above, and it spawns no processes at all, to stay within the statusline budget.
- Width calculation **must be correct on macOS's stock bash 3.2 as well.** In bash 3.2, `printf '%d' "'<문자>"` returns not the code point but
  the first multibyte byte as a signed (negative) value — which wipes out CJK width calculation (measured in v1).
  That is why `scripts/statusline.sh` switches to byte mode with `LC_ALL=C` and decodes UTF-8 itself. Do not revert this approach.

## Examples

`<메인 저장소 루트>/.claude/perf/units/status-protocol.status` — even when the branch is `Falke060918/status-protocol`
and the run happens inside an isolated worktree, the file appears in that one place.

```
verify|status-protocol|1755583380|상태 규약 문서 정리|child@status-protocol
```

The line left by the proxy driving that worktree gets the same number:

```
probe|status-protocol|1755583200|상태 규약 문서 정리|sub@status-protocol
```

On screen (the label slot is fixed-width, and old lines without source are left blank):

```
◑ [S/W1] status-board-fix   작성  12s  상태줄 대시보드 개선
✓ [S/W2] orca-recheck       완료  41s  orca 규약 버전 갱신
◑ [S]    readme-fix         작성   8s  README 문구 정리
◆ [S]    status-protocol    반영   6s  상태 규약 문서 정리
```
