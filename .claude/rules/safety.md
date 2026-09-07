---
paths:
  - ".claude/settings.json"
  - ".claude/settings.local.json"
  - "scripts/**"
  - "**/.env*"
---

# Safety rules — blocking is done by machinery, not by prose

> Manual reference: where `paths:` conditional loading is unsupported, read this file directly when starting work that touches settings, hooks, scripts, or .env.

The list of the four `[금지]` items (secret / self-granted permissions / force push / production data destruction) has its single source in CLAUDE.md '하드 규칙'. This file carries only the implementation details.

## deny patterns — generalized against wrapper bypass

- Block dangerous commands with **deny**, not ask. Measured in v1: a force push actually got through 22 ask patterns plus the rtk hook's allow — the wrapper prefix (`rtk git push --force`) bypassed permission-pattern matching.
- The reason it bypasses: the matcher strips a fixed set of wrappers only — `timeout`, `nice`, `nohup`, `env`. Any other prefix is part of the command as far as matching is concerned, so `rtk git push --force` matches no `git push` pattern at all.
- Always write patterns in the generalized form with a leading `*`: `Bash(* git push *--force*)`. Adding one mirror pattern per wrapper does not scale.
- **A deny covers the tool path it names, not the file.** Listing `Edit(./.claude/settings.json)` alone leaves the Write tool and shell redirects wide open — measured: another session edited that file for real, and the permission-mode classifier that did fire tripped on a `python -c` verification command, not on the write. Assume one wall, not two: before calling a file protected, check which tool paths its deny entries actually name. When it truly matters, have the user edit the file by hand.
- Known limitation (not fixed): the `Bash(* push -*f)` family also blocks harmless arguments ending in `f`, such as `git push -u origin wtf` — a false positive in the safe direction, so we accept it. When it blocks you, rename the argument or ask the user to run the command by hand.

## Interpreter bypass

- Once a command goes through an interpreter (`python -c`, `node -e` — commands that take code as a string and execute it), no shell permission pattern can see the final action — a structural blind spot of the permission system.
- **Covered, as of 2026-08-24.** `.claude/settings.json` now carries 12 deny patterns in the wrapper-bypass-proof form (leading `*`) blocking `python -c`, node string evaluation (`-e` / `--eval` / `-p` / `--print`), and `npx`. Deny applies even under bypassPermissions, so this holds despite `"defaultMode": "bypassPermissions"` with `"ask": []`.
- The trade-off: inline code execution through these interpreters is blocked for everyone, including legitimate uses — a subagent's throwaway one-line check is refused by our own deny exactly like a dangerous command, and no permission mode lets it through. Write the code to a file and run the file (`python <file>`, `node <file>`) instead of passing it as a string; for a verification that is thrown away right after, the agent's own scratchpad subdirectory is where the file goes.
- The sandbox remains the second defense line for this path, and its actual effectiveness on Windows is still **unverified** — treat it as unmeasured, not as proven. Interpreter code that reaches execution through a script file runs outside the deny patterns' sight, so the isolation still matters: never route an action through an interpreter to get past a deny pattern, and tell the user when a dangerous action would need one.
- (Superseded 2026-08-24: the earlier v2 plan bound interpreter string execution to ask. It never landed — ask is empty. Kept here so the claim does not get re-added as if it were live.)

## Division of labor: patterns are the first filter, isolation is the real defense

- Turn the sandbox (isolated execution environment) on by default — variants the patterns fail to catch still run only inside the isolation.
- Caveat: apply the sandbox only after checking platform availability (Windows especially). On platforms where it is unavailable, go with deny patterns alone.

## secret

- Creating `.env` is `[기본]` — do it when instructed. What is `[금지]` is **committing** it. On creation, check whether `.gitignore` covers it.
- If a secret is already committed, removing it is not the end — report "키 회전 필요" along with it.

## Settings and hook checks

- For checking settings/hooks for dangerous patterns, do not build your own — use AgentShield (an `npx`-run scanner) as is.
- The permission-mode classifier can block merges and permission expansion regardless of the allow settings. When blocked, guide the user to Shift+Tab (mode switch); never work around it by self-editing settings.
- **`.claude/settings.json` is read at session start only, and `/clear` never re-reads it.** So a hook you just fixed does not run in this session no matter how right the fix is: restart Claude before suspecting the hook itself. The same load timing is why the session-start markers are plain unenforced text — nothing re-fires them either, so read the markers before handling the user request.
- **A hook that parses its own JSON payload with shell text tools reads the wrong thing, and says nothing** — a hook always exits 0, so neither shape below ever surfaces as an error. On Windows the payload's `file_path` arrives backslash-escaped (`C:\\Users\\...`), so feeding it straight into `[ -f "$p" ]` fails and the hook silently does nothing at all. And grepping the *whole* payload for a key matches a same-named key from another object first — measured on a `Skill` payload, `"command"` inside `tool_response` beat the real `tool_input` value.
- Fix for both: unescape `\\`→`\` before any path test, and narrow to the target object before extracting a key rather than grepping the entire payload. When a hook appears to do nothing, dump raw stdin to a file and look at it before suspecting the logic.
