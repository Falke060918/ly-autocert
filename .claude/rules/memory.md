---
paths:
  - "docs/**"
---

# Memory rules — so it does not get heavy with long use

> Manual reference: where `paths:` conditional loading is unsupported, read this file directly when starting work that touches the docs/ knowledge files (STATE / MAP / GOTCHAS).

The 3-way routing table (사실→MAP / 경험→STATE / 행동규칙→프롬프트 수정) has its single source in CLAUDE.md '기억'. This file carries only the operating details.

## Files and caps

| File | Role | Cap |
| --- | --- | --- |
| `docs/STATE.md` | Where things stand and what comes next. Volatile — `/handoff` overwrites the whole file every time (append-not-rewrite ban: `.claude/skills/handoff/SKILL.md` §2). | 150 lines |
| `docs/MAP.md` | Cache of exploration results: codebase structure, commands, conventions. `확인일: YYYY-MM-DD` at the top. | 120 lines |
| `docs/GOTCHAS.md` | Things we got burned by once, and how to avoid them. Ships empty from the template — the cap is what a derived project fills. | 20 entries / 100 lines |
| `docs/constitution.md` | The project's invariant principles — every 위임장 and verification verdict is compared against it. | — |

- These 3 layers are isomorphic to Letta (MemGPT)'s memory tiers: always loaded (core) = GOTCHAS·STATE, loaded on demand (archival) = MAP, conversation history (recall) = session records. **Do not invent new memory files** — a separate file such as `docs/DECISIONS.md` is banned. Decisions get absorbed into STATE (in progress) / MAP (hardened into structure) / GOTCHAS (breaking it causes an incident).
- Classification test: is it still true next week? → No means STATE. Yes and it is a location/structure/command means MAP. Yes and not knowing it costs you time means GOTCHAS.
- STATE holds 경험 (episodic), but a **derivable** state value is not 경험 — unpushed-commit counts/hashes, branch ahead/behind, the latest commit subject, whether a file exists are query results that go stale the moment they are hand-written. **Derivable does not stop at the repository**: the outside world's state *right now* — is a local app running, does a CLI connect, does a process exist — is one command away and goes stale faster still, so it is banned on the same grounds. Leave them out and run the command instead; the ban, its examples, the boundary against what does stay, and the replacement commands live in `.claude/skills/handoff/SKILL.md` §2.
- Knowledge files carry **only facts about this repository**. Absolute paths and OS-only commands are "facts about my machine", so they belong in the git-untracked `docs/MACHINE.md`.
- MAP paths are repo-root-relative only; OS-only commands get a `[win]` / `[unix]` tag. Do not defer MAP updates — the implementer updates it in the same commit, every commit.
- **A "line-break cleanup" in these files changes the diff and nothing on screen.** Markdown folds single source newlines into one paragraph, so the edit reads as real work in the diff while the rendered file is identical. Split with blank lines or bullets, and judge a formatting change by rendering it — never by the diff.

## GOTCHAS entry criteria

Enter it only when all 4 conditions hold: time was actually lost / it is reproducible / it is not self-evident from the docs alone / it is still valid next time.
`GOTCHA 후보:` items are collected during the session and applied in bulk at `/handoff` — not committed immediately.
However, **behavior correction fixes the prompt, not GOTCHAS** (the CLAUDE.md routing principle) — GOTCHAS keeps only factual traps.

### In the template repo (`TEMPLATE-REPO`), the file ships empty

`docs/GOTCHAS.md` is not a sync target (`scripts/sync-manifest.sh` never touches `docs/`); it reaches a derived project exactly once, at clone time — `SETUP.md` empties STATE and deletes MAP but keeps this file on purpose. The file exists here **as an empty container the derived project fills with its own traps**, not as a seed: the entries that used to sit here were audited out on 2026-08-29 ([TPL-20260829-001]) because a one-shot clone-time copy is the weakest delivery path we have. The layer itself, its cap, and its path do not change.

So in the template repo, **nothing gets entered into `docs/GOTCHAS.md`.** Where a trap goes instead depends on its kind:

- Platform-general trap a derived project will also hit (OS, shell, git, tooling, the Claude Code runtime) → `.claude/rules/shell-traps.md`. That file *is* a sync target, so it keeps reaching derived projects instead of arriving once and going stale.
- Behavior problem → the prompt, per the CLAUDE.md routing principle: the agent definition, the skill, or the convention that let it happen.
- Problem hit while fixing the template machine itself — its rules, prompts, skills, scripts → **fixed structurally**, so the trap cannot recur. A derived project never edits `scripts/compact-resume.sh`, so a warning about it would be dead weight wherever it landed.

The test before writing a trap down here is unchanged: *would a project that only uses this template — never edits it — still get burned by this?* No means fix the structure instead. This is the template-side counterpart to the derived→template gate in `.claude/skills/harvest/SKILL.md`.

## Validity and invalidation (Graphiti-style)

- Every MAP and GOTCHAS entry maintains a `확인일` (the date it was last confirmed true).
- An entry confirmed wrong is **not deleted** — mark it "무효(날짜, 사유 1줄)" and leave it in place; that prevents the same entry from being re-added.

## Searching past sessions

- Past conversations survive `/clear` in `~/.claude/projects/*.jsonl` — they are merely not searchable. Use the `/recall` skill (FTS5 index) to keyword-search "how did I do this before".
- Even so, never `/clear` without `/handoff` — record first, discard second. `/recall` is a last safety net, not a substitute.
