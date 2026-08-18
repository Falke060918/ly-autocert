# Orca dispatch convention — child worktree agents from inside the ADE

Applies only when the session-start output carries the `ORCA-ENV` marker. This file is the
command reference for the **proxy implementer** that the orchestrator delegates orca CLI
execution to (the orchestrator itself has no shell). Pass this path in every Orca-dispatch
delegation prompt. Commands below were verified against orca CLI 1.4.184 on Windows.
A longer, version-matched guide ships with the CLI itself: `orca skills get orca-cli`.

## 0. Preconditions

- Detection is two-part and conservative: the `ORCA-ENV` marker fires only when an
  Orca-injected env var (`ORCA_TERMINAL_HANDLE`, or `TERM_PROGRAM=Orca`) is present **and**
  `command -v orca` resolves. A machine with only the CLI installed (no ADE around the
  session) gets no marker and stays on internal subagents.
- Prefer `--json` on every call; parse fields instead of scraping text.
- If any orca call fails, do not retry blindly — report the failure so the orchestrator
  can fall back to an internal subagent.

## 1. Spawn one child agent per delegation unit

```
orca worktree create --name <unit-name> --agent claude --prompt "<full delegation prompt>" --json
```

- One worktree per unit; Orca gives it its own branch and a separate terminal card.
- From the JSON result capture two values and return them to the orchestrator:
  - worktree id — the full `result.worktree.id`, shaped `<repo-id>::<path>` (a bare repo
    id is not a worktree id)
  - agent terminal handle — `result.agentTerminalHandle`; older runtimes return only
    `result.startupTerminal.handle`; if neither is present, recover it with
    `orca terminal list --json`
- The child branches from the repo default base ref. If the unit must include the
  session's unpushed work, commit first and pass `--base-branch <session-branch>`.
- Do not bare-create and then `terminal create` the agent — `--agent` in one step is the
  supported path (the two-step variant leaves a stray fallback shell).

## 2. Monitor and collect results

```
orca worktree ps --json                                              # board sweep: all worktrees + status
orca terminal wait --terminal <handle> --for tui-idle --timeout-ms 300000 --json
orca terminal read --terminal <handle> --cursor <prev> --limit 1000 --json
```

- `terminal wait --for tui-idle` is the completion signal for TUI agents (Claude Code
  included); always pass `--timeout-ms`. A timeout is "still running", not an error.
- A `terminal wait` timeout returns `ok: false` with `error.code: "timeout"` — interpret
  this as "still running", not as a failure.
- `terminal read` is cursor-based: read once before/at spawn, then read with
  `--cursor <nextCursor from the previous read>` to get only the new output (the agent's
  final report). `--limit` raises the retained-line window.
- Card status on the Orca board (`workspaceStatus`) is worktree metadata:
  `orca worktree set --worktree id:<repo-id>::<path> --workspace-status in-review --json`
  (default ids: todo, in-progress, in-review, completed).
- Handles are runtime-scoped: after an Orca restart or a `terminal_handle_stale` error,
  reacquire with `orca terminal list --json` and continue with the replacement.
- To send a follow-up instruction to a live child: `orca terminal send`.

## 3. Merge and clean up (single-track worktrees)

In Orca mode the template's own isolation (`isolation: worktree` under
`.claude/worktrees/`) is **not used** — Orca worktrees are the only worktrees. The
landing flow keeps the same order as `/land` but swaps the enumeration and removal
commands:

```
orca worktree list --json          # instead of: git worktree list
git merge --no-ff <child-branch>   # unchanged: merge into the session branch, dependency order
orca worktree rm --worktree id:<repo-id>::<path> --json   # instead of: git worktree remove + prune
```

- Merge only after the child's result is collected and reviewed; never reset or force.
- `orca worktree rm` refuses when uncommitted changes remain unless `--force` — inspect
  before forcing, exactly like the `/land` rule for `git worktree remove`.
- Unmerged children stay open and are listed in the report as still open.
