---
paths:
  - "**/TASK-PROMPT.md"
---

# Orca dispatch rules — running child worktree agents inside the ADE

> Manual reference: the `paths:` condition cannot express this rule set's trigger (an environment marker) — when `ORCA-ENV` appears in the session-start output, read this file directly before starting. If `ORCA-CHILD` appears alongside it, this session is a child: it comes up as an orchestrator (the repository's `settings.json` wires `"agent": "orchestrator"`), so finish through one internal implementer and do not create further worktrees or re-dispatch through Orca.

Under `ORCA-ENV`, a delegation unit defaults to an **Orca child agent** (one worktree + branch + terminal card per unit) instead of an internal subagent. The orchestrator has no shell, so the orca CLI is owned by one **unified proxy** per child (model: haiku implementer) that owns creation → waiting → collection → status setting. Only lightweight delegations (1 line, a few seconds) stay on the internal Agent tool. If an orca call fails, that unit falls back to an internal subagent and the user gets a one-line notice — but "fails" is decided by §0 below, not by the first `disconnected` the sandbox hands back.

**The proxy counts toward the concurrent-agent cap of 5–8** (the cap's single source: `.claude/agents/orchestrator.md`). It is an internal subagent holding a slot for its whole creation → wait → collection cycle — each 5-minute `terminal wait` occupies the slot even while the real work runs outside the session — so N Orca children consume N slots, and the cap bounds proxies and other internal subagents *together*, not as separate pools. The 5–8 figure itself is an initial value with no recorded measurement behind it — adjust it from observation (`/perf` reports, fanout instrumentation), not by argument.

When the syntax is uncertain, do not guess — look at `orca skills get orca-cli` (the official guide shipped with the CLI) or `orca <cmd> --help` first. There is no `orca --version` — the version is `result.runtime.appVersion` from `orca status --json`. Put `--json` on every call and parse fields instead of scraping text.

## 0. App connection check before dispatch — outside the sandbox

The orca CLI talks to the running Orca app over the local loopback/IPC, and the Bash tool's sandbox blocks exactly that (the measured behaviour lives in `.claude/rules/shell-traps.md`, 'The sandbox denies a bash write…' — this section is the behaviour rule, that file is the fact). So a pre-dispatch check run *inside* the sandbox cannot tell an app that is off from an app it simply cannot reach.

- **Run every orca CLI call outside the sandbox** — the pre-dispatch `orca status --json`, `orca worktree list --json`, and the create/send/wait/read calls that follow. Use the Bash tool's sandbox-disabled path for them, the same route the `git merge` entry in shell-traps.md calls "with the sandbox disabled". Inside the sandbox the call exits 0 with well-formed JSON, so nothing tells you the answer is wrong.
- **Read `connectionState: "disconnected"`, `runtime.state: "stale_bootstrap"`, `reachable: false`, `running: false, pid: null`, and `error.code: "runtime_unavailable"` as "connection failed", never as "app is off".** They are the output of the CLI failing to reach the app; a sandboxed call produces all of them against an app that is up and serving another shell. (What `stale_bootstrap` means precisely is not known — do not build a branch on a guessed meaning.)
- **Fall back only after a process check.** Before treating a unit as "orca unavailable → internal subagent", confirm the app is really absent with a process-level look outside the sandbox — e.g. `pgrep -x Orca` (check the actual process name on the host first) — and re-run `orca status --json` there. Fallback is justified when *both* the CLI cannot connect and no app process exists. When the process exists but the CLI still cannot connect, report that as a broken connection, not as an absent app.
- **A past record is not evidence — decide this by measuring, every session.** Whether the app is running and reachable is settled by a check run *now*, outside the sandbox. `docs/STATE.md`, an earlier session's report, and a previous fallback are none of them grounds — a line saying "the Orca runtime was off" satisfies neither of the two conditions above, so it can never justify a fallback on its own. **When a measurement and a record disagree, the measurement wins** and the record is stale, including the record this session started from; the orchestrator-side handling of that disagreement (and of delegations already dispatched on the stale premise) is `.claude/agents/orchestrator.md` '실측이 기록을 이긴다'.
- **The session-start `ORCA-APP-*` marker is a notice, not this check.** It is measured outside the sandbox, but at session start only, so it goes stale the moment the app is started or quit — whatever it said, the check above runs again before every dispatch (marker semantics: `CLAUDE.md` session-start marker table).
- Recorded misjudgement (2026-09-05, derived-project measurement): a session ran `orca status --json` / `orca worktree list --json` inside the sandbox, got `stale_bootstrap` / `runtime_unavailable` (each with a `SecCodeCheckValidity` line on stderr), read it as "the app is off", and skipped Orca dispatch entirely — while the same app (pid 54902, v1.4.197) was connected and listing worktrees normally outside the sandbox.

## 1. Creation — deliver the prompt as a file (the standard)

```
orca worktree create --name <unit> --agent claude --json
```

- **Never send a long delegation prompt directly via `--prompt` or `--text`.** Three measured failure modes: Windows cmd splits argv on the embedded `"` / a missing `--enter` types into the input box without submitting / a long text arrives truncated at the front — and all three report creation and delivery as successful.
- Standard procedure: write the delegation prompt into a `TASK-PROMPT.md` file inside the worktree, and send only one short line to the terminal:

```
orca terminal send --terminal <handle> --text "TASK-PROMPT.md를 읽고 그대로 수행" --enter --json
```

- **`--enter` is mandatory.** Omit it and the characters just sit in the input box with nothing submitted. The only exception is a deliberately unsubmitted key (a single arrow press to redraw the TUI).
- From the JSON result, collect the worktree id (`result.worktree.id`, of the form `<repo-id>::<path>`) and the terminal handle (`result.agentTerminalHandle`; on older versions `result.startupTerminal.handle`). If they are missing, recover with `orca terminal list --json`.
- A child branches from the repository's default base ref. If it needs unpushed work from the session, commit first and pass `--base-branch <세션 브랜치>`.
- TASK-PROMPT.md must contain: "부모 세션에서 이미 승인된 작업 — 확인 질문(AskUserQuestion) 없이 끝까지 수행. 너는 이 저장소의 `settings.json`(`agent: orchestrator`) 때문에 오케스트레이터로 뜬다 — 내부 implementer 1명에 위임해 커밋으로 끝내라. 금지는 추가 워크트리 생성·Orca 재분배뿐이다" + one line of approved scope + the exact output spec ("이 표기를 바꾸지 말 것"). The child loads the same CLAUDE.md, so without these lines it raises its own pre-work brief question and stops. Do not write "직접 구현·재위임 금지": the child has no Edit/Write/Bash, so that line cannot be obeyed — the wiring (`settings.json`) stays as it is; the rule follows the wiring.
- Recorded misjudgement (2026-09-06, first run under `ORCA-ENV` in this repository): the children read this repository's `.claude/settings.json` and came up as orchestrators without Edit/Write/Bash, so the then-standard line "직접 구현하고 커밋으로 끝내라 — 재위임 금지" was an instruction they could not follow; each child finished normally by spinning up one internal implementer.

## 2. Watching and collecting

```
orca worktree ps --json
orca terminal wait --terminal <handle> --for tui-idle --timeout-ms 300000 --json
orca terminal read --terminal <handle> --cursor <prev> --limit 1000 --json
```

- Liveness check first: about 1 minute after delivery, use `terminal read` to confirm actual traces of work (tool calls, a token counter). If you only see the version banner, or the prompt text is sitting in the input box, delivery failed — do not move on to a long wait; diagnose and report immediately.
- A `wait` timeout (`ok: false`, `error.code: "timeout"`) means "still running", not a failure. Wait once more; on the second timeout, report "실행 중" and finish.
- **Idle is not done.** When a question dialog scrolls out of the buffer, it is reconstructed as an empty idle — force a redraw with a harmless key (a single arrow, without `--enter`) and read again. **Judge completion by commit existence** (`git log --oneline` in the worktree) — staged-only is a failure mode.
- **Completion criterion (standard wording — put it in the proxy prompt verbatim):** "완료 = `git log <베이스 커밋>..HEAD`에 하위 ID(`<ID>-X`)가 든 커밋이 존재한다. 베이스 커밋 자체, 상위 ID만 든 커밋, staged-only는 증거가 아니다." Record the base commit (`git rev-parse HEAD` in the worktree right after creation) before delivering the prompt, and grep only the range after it — the base commit often carries the parent ID because it is the spec commit.
- **Never close a turn as "대기 중" (standard wording — put it in the proxy prompt verbatim):** "매 턴 완료 / 실행 중 / 막힘 셋 중 하나로 판정을 낸다 — '대기 중'으로 턴을 닫지 않는다." This continues the wait rule above: after the second `wait` timeout the verdict is "실행 중", not silence.
- **Return format is enforced, not just requested (standard wording — put it in the proxy prompt verbatim):** "프록시의 반환 첫 줄은 반드시 `판정: 완료` / `판정: 실행 중` / `판정: 막힘` 중 하나로 시작한다." A return that does not start with one of these three is not a return — the orchestrator immediately launches a new watch proxy, again with the standard wording verbatim in its prompt.
- Recorded misjudgement (2026-09-06, first run under `ORCA-ENV` in this repository): one proxy stopped twice with nothing but "대기 중" and no verdict; another read the base commit `d26b8cd` (the spec commit, whose message carried the parent ID `TPL-20260906-001`) as the completion of sub-unit C and set the card to in-review — the real C commit `1592cc6` came later.
- Recorded misjudgement (2026-09-07, this repository): even with the standard wording in the prompt, two haiku proxies closed their turns with "완료를 기다리는 중"-style lines instead of a verdict; each was recovered by relaunching a watch proxy.
- `terminal read` is cursor-based — pass the previous read's `nextCursor` to get only new output. Handles are runtime-scoped, so after an Orca restart or `terminal_handle_stale`, re-acquire them with `terminal list --json`.
- Board card status: `orca worktree set --worktree id:<id> --workspace-status in-review --json`.
- Never remove or replace a worktree that has a live proxy attached — wait for the proxy's completion notice, or create a new one under a different name.

## 3. Merging and cleanup (single track)

The single-track rule — the Orca worktree is the only track, with no template `isolation: worktree` stacked on top — is stated at the dispatch point: `.claude/agents/orchestrator.md` 'Orca mode' (single source). The `/land` order stays the same; only the listing and removal commands change:

```
orca worktree list --json          # git worktree list 대신
git merge --no-ff <자식 브랜치>     # 그대로: 세션 브랜치로, 의존 순서대로
orca worktree rm --worktree id:<id> --json   # git worktree remove + prune 대신
```

- Merge only verified results, no force, check for uncommitted files before `rm --force`, and leave unmerged children open and reported.
- **The delivery file is the expected leftover (single source; `/land` §2 refers here).** Before `rm`, run `git status --short` in the child worktree: if the only line is `?? TASK-PROMPT.md`, delete that file and `rm` without `--force` — the file is the §1 delivery vehicle, never work. Any other uncommitted line means leave the worktree in place and report, as before.
- Recorded misjudgement (2026-09-06, first run under `ORCA-ENV` in this repository): `TASK-PROMPT.md` placed per §1 stayed untracked in all three worktrees, so `orca worktree rm` (no force) refused all three; deleting the file let each `rm` through, and Orca removed the branch along with it.
