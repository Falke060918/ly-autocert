---
name: perf
description: "Generates a markdown bottleneck report for recent sessions — top tool durations, subagent spawn-to-completion times, user approval-gate waits, and a wall-clock breakdown — from the session transcript JSONL plus optional hook timestamp logs. Use when the user asks where session time went, why a session felt slow, or for a performance report."
---

# /perf — 세션 병목 리포트

Answers "이 세션의 시간이 어디로 갔나" with numbers. User-facing communication is in Korean.

## 1. Run

Delegate to **explorer** (read-only analysis; the main session has no Bash):

```
bash scripts/perf-report.sh [세션 개수] [--gap 초] [--session <세션 id>]   # 기본: 최근 1개 세션, gap 30초
```

The script prints the full markdown report to stdout. Have explorer return it verbatim (it is already compact — one section per session); show the user the report and add 1–3 lines of interpretation: the single biggest sink, and whether it is tool time, subagent serialization, or user wait.

`--session` pins the report to one session id (exact filename match first, then prefix); without it "recent" means **most recently modified transcript**, which in parallel sessions need not be the one you mean.

Dependency: **jq**. The script aborts with an install hint if missing — relay that hint to the user instead of working around it.

## 2. Data sources & degrade

| Source | Path | Role |
| --- | --- | --- |
| Session transcript JSONL | `<config>/projects/<프로젝트 slug>/<세션UUID>.jsonl` | Required. Tool/agent/user-wait timelines of the **main thread**. |
| Hook timestamp log | `.claude/perf/<세션UUID>.jsonl` | Optional. `scripts/perf-log.sh` appends one line per PreToolUse/PostToolUse/SubagentStart/SubagentStop event. A `SubagentStop` whose agent type is empty is written as `SubagentStopNoAgent` instead — the line is kept as evidence, but the report counts no delegation for it. Covers **subagent-internal tool calls**, which the transcript cannot see. |

- **No hook log (or empty) → the report still works** from the transcript alone; the script prints a one-line notice instead of the hook section. Do not treat that notice as a failure.
- **The report is also produced automatically**, not only on request: `scripts/learn-harvest.sh` (SessionEnd) calls this same script in the background with `--session <that session's id>` and stores its output as `.claude/perf/reports/<날짜>-<세션8자>.md`. The session id in the filename is taken from the report body's `## 세션 <id>` heading, not from the hook input — so **the name always matches the contents**, including on the rare fallback path where the id-pinned lookup finds no transcript. Read that file when the user asks about an *earlier* session; a missing file just means there was no perf data (or no jq), never a failure.
- **A `## 수집 경고` section at the end of an archived report is not damage** — it is where `scripts/learn-harvest.sh` parks the stderr warnings `scripts/perf-report.sh` emitted, so the automatic path does not swallow them the way it used to. Its presence means some log lines could not be read, so **every count in that report is a lower bound**: the skipped lines are missing from it. A report without the section had nothing to skip.
- **When the automatic path collects nothing, that failure is filed too** — `.claude/perf/reports/<스탬프>-<세션8자>-collect-failed.md`, in the same directory as the reports, so reading that directory shows both what was collected and what was not. It carries the rc and, under the same `## 수집 경고` heading, whatever stderr came out; when both the id-pinned call and the fallback failed, both rcs and both sets of warnings are in there. The session id in this name comes from the hook input, since there is no report body to read it from.
- **A `-2`/`-3`… before the `.md` means an earlier archive was kept, not overwritten.** The stamp is second-resolution, so two runs finishing in the same second collide; the second one steps aside to the next free suffix. This applies to ordinary reports and to the failure records alike. Past `-99` the archive is **abandoned rather than overwritten**, and the abandonment is appended as one line to `.claude/perf/reports/collect-overflow.log`.
- Mid-session, `scripts/nudge-reminder.sh` appends one short line to its nudge — this session's delegation count and summed subagent output tokens, read from the same hook log. It is a reminder, not a report; for numbers, run the script.
- Hook logging starts only after the `.claude/settings.json` hooks are loaded — i.e. **after a Claude restart** following the settings change. Sessions before that simply have no log.
- `.claude/perf/` is git-ignored twice over: the template `.gitignore` lists it, and `perf-log.sh` drops a self-ignoring `.claude/perf/.gitignore` (`*`) so derived projects need no `.gitignore` edit. The logs are per-machine data — never commit them.

## 3. Relation to scripts/fanout.sh (complementary, do not merge)

- `fanout.sh` — **parallelism metrics** across many sessions (fan-out width, cc1/cc2/cc3 concurrency ratios, serial-loss estimate). Use it for A/B judgment after changing delegation rules.
- `perf-report.sh` — **time decomposition of individual sessions** (which tool, which spawn, which wait ate the wall clock). Use it to find bottlenecks.

Both read the same transcript location and share its parsing pitfalls — message.id grouping, `async_launched` → `<task-notification>` lifetimes, `isSidechain` absence. Those pitfalls are documented once, in the `fanout.sh` header; `perf-report.sh` refers there instead of duplicating. Keep it that way: a parsing fix discovered in one script must be checked against the other.

## 4. Reading the report — known approximations

- Intervals overlap (a user gap can run while an agent runs), so the breakdown rows need not sum exactly to the wall clock.
- "사용자 대기" = AskUserQuestion open time (approval gates) + response gaps ≥ `--gap` seconds before a plain user message.
- Hook-log Pre/Post pairing is FIFO per tool name — with parallel agents running the same tool, individual durations can mix (totals stay valid); the report says so itself.
- Spawns without a completion notification (still running, or session cut) are excluded and counted separately.
