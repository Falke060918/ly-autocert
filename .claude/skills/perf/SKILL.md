---
name: perf
description: Generates a markdown bottleneck report for recent sessions — top tool durations, subagent spawn-to-completion times, user approval-gate waits, and a wall-clock breakdown — from the session transcript JSONL plus optional hook timestamp logs. Use when the user asks where session time went, why a session felt slow, or for a performance report.
---

# /perf — 세션 병목 리포트

Answers "이 세션의 시간이 어디로 갔나" with numbers. User-facing communication is in Korean.

## 1. Run

Delegate to **explorer** (read-only analysis; the main session has no Bash):

```
bash scripts/perf-report.sh [세션 개수] [--gap 초]   # 기본: 최근 1개 세션, gap 30초
```

The script prints the full markdown report to stdout. Have explorer return it verbatim (it is already compact — one section per session); show the user the report and add 1–3 lines of interpretation: the single biggest sink, and whether it is tool time, subagent serialization, or user wait.

Dependency: **jq**. The script aborts with an install hint if missing — relay that hint to the user instead of working around it.

## 2. Data sources & degrade

| Source | Path | Role |
| --- | --- | --- |
| Session transcript JSONL | `<config>/projects/<프로젝트 slug>/<세션UUID>.jsonl` | Required. Tool/agent/user-wait timelines of the **main thread**. |
| Hook timestamp log | `.claude/perf/<세션UUID>.jsonl` | Optional. `scripts/perf-log.sh` appends one line per PreToolUse/PostToolUse/SubagentStop event. Covers **subagent-internal tool calls**, which the transcript cannot see. |

- **No hook log (or empty) → the report still works** from the transcript alone; the script prints a one-line notice instead of the hook section. Do not treat that notice as a failure.
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
