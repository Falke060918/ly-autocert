---
paths:
  - "docs/STATE.md"
  - "docs/specs/**"
  - ".claude/identity"
---

# Notion rules — one repository, one page

> Manual reference: `paths:` cannot express this rule set's real trigger (a Notion MCP call) — read this file directly **before** any Notion tool call, whatever file you are working on.

The Notion MCP is authenticated against a whole workspace, so it will happily read and write any page in it. **There is no physical isolation per repository — this file is the only wall.** The user's own words: "각 프로젝트는 각자의 notion 경로에 작성해야돼, 예를들어 지금은 템플릿 원본 세션이기때문에 해당 문서에만 접근".

## 1. The one allowed page

- The single source of "which page is mine" is the `notion:` key in `.claude/identity` (32-hex page ID, no dashes). Read it; never hardcode an ID in a prompt or a script.
- **Write target = that page and its descendants only.** Creating a child page under it is allowed; writing to, moving, or deleting anything outside that subtree is not.
- Never widen the target by inference — a page found by search, a page named after this project, a page the user mentioned in passing are all *not* the target unless the ID matches.
- If a write would land outside the subtree, stop and report it instead of doing it.

## 2. When the key is missing or `pending`

If `notion:` is absent, empty, or `pending`, **do not write to Notion at all.** That invariant has no exception: do not pick a page yourself, do not create a new top-level page, and do not fall back to another repository's ID — a derived project that inherits the template's ID would write into the template's own document.

**A page ID is only half of "configured" — the MCP server itself may not be connected.** A project-scoped MCP server does not live in this repository's `.mcp.json`; it lives in `~/.claude.json` under `projects["<absolute path>"].mcpServers`. Judge connection by that entry — looking at `.mcp.json` first reports a working connection as "not configured". The consequence that matters: **a derived clone does not inherit the connection**, because `~/.claude.json` is per-machine and keyed by absolute path while the clone only carries files. So setting a fresh page ID is not enough; with no connection **the mirror is skipped silently**, without an error to notice. Treat a missing connection exactly like a missing key — do not write, and surface it the same way below.

Only *how you surface it* differs by where you are. The three phrasings in this repository are one rule, not three:
- **Interactive session** — ask the user for the page ID once, in one line, and continue the rest of the work meanwhile.
  **[3계층(멈춤선)]** 이 1회 질문은 추정 금지 값이라 무인 모드(`/automode on`)에서도 그대로 묻는다 — 무인이 페이지 ID를 고르지 않는다. 계층 정의는 `.claude/agents/orchestrator.md`.
- **`/handoff`** (`.claude/skills/handoff/SKILL.md` §3) — skip the mirror and spend one line of the report on it, never a question: a handoff closes a work unit and must not stall, and a missing page never turns it into a failure.
- **Unattended runbook** (`SETUP.md` §8) — never ask, leave one line in the completion report: there is no user in the loop to answer.

**How `pending` gets resolved: SETUP creates the page, once.** A derived project is not meant to stay `pending` — its unattended setup run *tries* to create its own page, and that creation is the single write §2 allows before a key exists, because it is what produces the key. Everything else in §2 stands unchanged.

- **Guard first — never create twice.** If `notion:` already holds a 32-hex ID, the page exists: do nothing. Re-running SETUP or re-initializing a clone must not scatter duplicate pages across the workspace.
- **Parent**: create it as a child of the `Dev` page whose ID lives in the **global** settings (`~/.claude/CLAUDE.md`). Read it from there at run time — **never hardcode that ID in this repository**: it is per-user, not per-repo, and a committed copy would ride along into every clone.
- **Name**, in the user's own format: `저장소이름_(생성일)_(owner/repo)` — e.g. `my-app_(2026-08-27)_(falke060918/my-app)`. The repository name is the clone's own directory name and the date is the day SETUP runs. `owner/repo` is the `origin` remote URL normalized (drop the scheme and `git@`, scp-form first `:`→`/`, drop a trailing `/` and `.git`, lowercase) with the host removed. **With no `origin` remote, put `local/<repository name>` in that slot.** That is not an edge case but the *main* path: SETUP's default skips `gh repo create`, so a freshly initialized clone usually has no origin yet. The fallback keeps the name's shape while recording that no remote exists.
- **Record it in the same breath as creating it**: the moment the create call returns an ID, write that 32-hex ID (no dashes) into *that* repository's own `.claude/identity` under `notion:` — before any other step runs. A creation that is not yet recorded is an orphan: an interruption in between leaves a page nobody owns, and the next run's guard sees an empty key and creates a second one. From that moment §1 governs it — the write target is the new page and its descendants only, never the `Dev` parent and never a sibling project's page.
- **Fail loud, not silent**: if the parent ID is not in the global settings, or the MCP is not connected (judged as above), **skip the creation, leave `notion: pending`, and say so** — one line in the SETUP completion report, exactly as the unattended bullet above requires. A skipped creation is a reported outcome, never an invisible one, and §2's "do not write" invariant simply keeps holding until a human sets the key.

## 3. What gets synced (3 kinds)

| Kind | Source artifact in this repo | Slot | Wired today? |
| --- | --- | --- | --- |
| STATE | `docs/STATE.md` — the whole file | `STATE` | **yes** — `/handoff` §3, after the commit |
| Session work log | the commit history itself — no file, rendered at write time (below) | `LOG` | **yes** — `/handoff` §3, after the commit |
| Defect / review specs | `docs/specs/*.md` — one child page per file (below) | `SPECS` | **yes** — `/handoff` §3, after the commit, for the changed specs only (below) |

**All three are wired, and they share one shape**: commit first, then a full replacement of that kind's slot page, then push (`.claude/skills/handoff/SKILL.md` §3). Only the source and the per-kind skip differ. Nothing else becomes a sync target on the spot — a fourth kind is a design decision for the user, not something to improvise mid-task.

**Release notes are left out on purpose, not by oversight.** `docs/releases/` is the template machinery's own version history, written for whoever edits the template, and the commits it summarizes already reach a human reader through the `LOG` slot — so mirroring it would duplicate `LOG` for an audience that is not reading Notion. Say that when someone asks why the table has three rows and not four; do not treat the gap as a missing wire.

**Session work log — the source is the commit history, and no file is created for it.** `.claude/rules/memory.md` bans inventing new memory files, and that ban is **not** narrowed here; it does not have to be. What happened in a session is already recorded in the commits, and the same file forbids hand-copying a derivable value into a knowledge file because the copy goes stale the moment it is written — a log file transcribed out of git would be exactly that copy. So the mirror renders the log from the repository at write time and stores nothing, with a fixed query and a fixed limit:

```bash
git log -50 --date=short --pretty='%ad %h %s'
```

Fixed both ways is what makes full replacement deterministic — the slot holds the newest 50 subjects and nothing older. Dropping older lines loses nothing: git keeps them, and Notion is the mirror, not the record. Work Order IDs ride along in the subjects, which is how progress is meant to be read back (`.claude/conventions/work-order.md`).

**Defect / review specs — one child page per spec file, inside the `SPECS` slot.** The source is a directory, so the mapping has to be chosen, and this one was reversed on 2026-09-04: composing every file into a single page had reached 213 KB, more than one MCP write can carry, which left the slot structurally un-updatable. Per-file pages bind each write to one file (largest measured: 29 KB) and keep that size flat as the directory grows. The two rules that once argued against them are kept rather than waived — slot creation stays a deliberate once-per-repository act (a child page *inside* an existing slot is the mirror's own work, see "No slot, no mirror" below), and an orphan page is reported, never deleted (§4).

- **Title = the filename with `.md` removed** — `docs/specs/2026-08-31-automode-toggle.md` → `2026-08-31-automode-toggle`, and the reverse is `<title>.md`. The extension comes off because a bare `X.md` is rewritten into an `http://X.md` link that cannot be undone (§4), and a page title is one place backticks cannot protect. Ordering is not maintained: Notion places children in creation order, and the mirror never reorders them.
- **Resolve a child exactly the way the slot itself is resolved** — from the parent's children by exact title, never by workspace search (§1). One `notion-fetch` of the slot hands a run everything it needs: the child titles, their page IDs, and the orphan candidates.
- **Create a missing child in two calls**: create it with no body (or a one-line placeholder), then fill it by full replacement. The split is not decoration — `notion-create-pages` silently drops the H1 at the top of `content` (§4) — and it also makes creation and update share one write path. **When the second call fails, what is left is a titled page with an empty body, and "the page exists" would pass on it forever after.** So an existing child whose body is empty counts as not yet written: rewrite it on the next run exactly as if it were missing, and report it. If more than one child carries the same title, skip that one spec and report it — the slot's own duplicate rule, one level down.
- **The body is that file verbatim.** The composed page's heading demotion is gone: one file per page means the file's own heading tree is the page's tree.
- **No per-child pre-write backup fetch.** The mirror owns the body and the original is in git, so there is nothing to preserve, and the single slot fetch above stands in for it. The **post-write fetch-back comparison (§4) still runs**, and it gets cheaper — the comparison target is one page instead of the whole slot.

**Which specs a run writes: the ones that changed, plus the ones with no page yet.** The write set is the union of (a) and (b).

```bash
# (a) specs touched by the last 50 commits — drops the blank first line, keeps only files that exist today
git log -50 --name-only --pretty=format: -- 'docs/specs/*.md' | sort -u \
  | while read -r f; do [ -n "$f" ] && [ -f "$f" ] && printf '%s\n' "$f"; done

# (b) specs with no child page yet — `ls docs/specs/*.md` against the slot's child titles
```

The window is a fixed 50 commits rather than `@{u}..HEAD` because that range resets on every push. It holds only the *unpushed* commits, so a spec that was committed and pushed outside a handoff — which `CLAUDE.md` allows, and which the orchestrator does at the end of every work unit — drops out of every later handoff's window and never comes back: the slot drifts permanently instead of catching up, and no run reports anything, because each individual window was legitimately empty. Measured 2026-08-31: the slot was 21 commits and 2 days behind with three specs missing, `638ede9` having been pushed at 09:11 while the handoff commit that would have mirrored it was not written until 09:43. The number 50 is the same constant the `LOG` query above already uses, not a new one. Test (b) is the floor under the window — a new spec is caught by having no page, whatever the commit range says — and when it is unclear, write: a redundant write costs one call, a silent drift costs the mirror. A spec on neither list is skipped, and when both lists come out empty the specs mirror does nothing and reports nothing.

**An orphan — a child title with no matching file — is named in the report and never deleted** (§4: deletion is proposed to the user, never done on our own judgment).

**One spec's failure never stops the others.** Skip, report, carry on — the same shape as §2's missing key:

| Condition | What the skip covers |
| --- | --- |
| Page ID missing or `pending`, MCP not connected | the whole mirror (unchanged) |
| `SPECS` slot missing, or more than one candidate | the specs mirror only (unchanged) |
| Duplicate child title | that one spec |
| File over the size cap (below) | that one spec |
| A create or write call fails | that one spec; the rest continue |

Report them on one line — e.g. `SPECS 일부 건너뜀 — 2026-08-25-template-dryrun-findings(크기 초과), 고아 후보 1건`. When everything due succeeded, that line is absent entirely.

**A spec file over 40 KB is skipped and reported — read that as a signal to split the spec, not to raise the cap.** Judge it with `wc -c` right before the write. 40 KB sits above the largest file measured on 2026-09-04 (29 KB) and far below the 213 KB that made the composed page unwritable; if a 30 KB-class write does fail in practice, lower the cap to the measured value rather than raising it.

**The composed body already sitting in the slot is replaced once, and the slot's body is never written again.** Replace it with a two- or three-line pointer — e.g. "이 페이지는 컨테이너다. 스펙 문서는 아래 하위 페이지 하나씩에 있고, 원본은 저장소의 `docs/specs/`다." — and after that the mirror only ever writes child pages.

- **Destructive, and gated on one user approval.** The whole 213 KB body goes away. It is recoverable in principle (the originals are in git, and Notion keeps page history), but a full replacement of a slot body is a 3계층 멈춤선, so get that approval once before running it.
- **Run it only while the slot still has no child pages**, because replacing a parent's body can take its children with it. Judge it **without reading the slot's body**, since the next line bans that read: search scoped to §1's page (`notion-ai-search` with `page_url`) and look for a hit whose `path` runs through the slot — no such hit means zero children. That search is a read about the subtree, not slot resolution, so §3's "never resolve the slot by workspace search" is untouched. Treat any other basis (memory of a past run, a report line) as no basis at all. The order cannot be reversed: body replacement first, child creation after.
- **Do not fetch the old body before replacing it.** The mirror owns it, so there is nothing to back up, and a 65k-token read is an incident of its own. The post-write fetch-back (§4) still runs; the new body is three lines, so it stays cheap.
- **A failure here does not break the new structure.** It costs the steady-state price of a large slot fetch and nothing else: leave one line in the report and try again at the next handoff.

### The slot — the mirror never touches the repository page's own body

§1's page is the repository's own page, and **the user arranges its body by hand**: columns, tables, notes. A mirrored document has no place there, and replacing that body in full would erase the arrangement. So every mirror targets a **slot**: the child page of §1's page whose title is exactly that kind's slot name from the table above (`STATE`, `LOG`, `SPECS`). Those child pages — and only they — are what "replace the body in full" below applies to. The parent's body is outside the mirror's reach entirely: its tables, its column layout, its headings are never rewritten and never edited by a mirror write. (Notion appends a link block to the parent when the child page is created; that is Notion's own doing, not a mirror write.)

**Resolve the slot deterministically, from the parent's children.** `notion-fetch` §1's page and take the child whose title is exactly that kind's slot name. **Never resolve it by workspace search** — a same-named page in another project would match, and the mirror would land outside this repository's subtree in violation of §1.

**No slot, no mirror — judged per kind.** If that child is absent, or if more than one child carries the title, **do not mirror that kind**: do not create the slot mid-mirror, do not guess between candidates, and above all do not fall back to writing the parent's body. A missing `SPECS` slot skips the specs mirror only; it never stops the others and never diverts them elsewhere. Skip and report it exactly the way §2 reports a missing key (in `/handoff`, the one report line of its §3). Creating a slot is a deliberate once-per-repository act — the same shape as §2's "SETUP creates the page, once" — never something a mirror does on the fly. A derived repository needs its own slots under its own page, for the same reason it needs its own `notion:` value. **That ban covers the slot, not what lives inside it**: creating a child page *under* an existing slot is ordinary mirror work (the specs mirror above), because it lands inside §1's subtree and needs no new slot.

**A mirror write replaces the slot page's body in full — it never appends.** Each sync rewrites the slot from its source as that source stands now — the file, the directory, or the `git log` query of §3 — exactly the way `/handoff` rewrites STATE.md from scratch (`.claude/skills/handoff/SKILL.md` §2: 갱신은 덧붙이기가 아니라 재작성이다). Appending would stack past layers in Notion that no longer exist in git, and the mirror would stop matching its source. For the specs mirror this applies one level down: the unit replaced in full is a single child page, not the slot, and the slot's own body is written exactly once (the one-time replacement above) and never again. The pre-write backup fetch is likewise one fetch of the slot per run, not one per child. (A 2026-09-01 `/learn` review weighed swapping this for diff-driven targeted `update_content` edits on the large `SPECS` slot and **excluded** the proposal — full replacement stays the method. Recorded here so the same candidate does not come back around, and the 2026-09-04 split kept it: the write method did not change, only the size of what one write covers.)

**When a write does target a hand-arranged page, full replacement is not permission to rebuild — edit the fetched page, never a blank one.** This is not the mirror's path (the mirror only ever writes the slot); it governs a write the user directs into §1's page itself. Those tables are plain table blocks, not databases, so no block IDs are exposed and there is no partial-update API: swapping the entire page markup through `notion-update-page` is the only path there is. That is precisely why preserving the layout is the **writer's** duty and not the tool's — nothing else will do it. So `notion-fetch` the page first and edit *that* markup in place, changing only the cell contents; do not assemble a fresh page from the source file and paste it over. Whatever the user arranged by hand stays as found — column structure including its ratios, blocks sitting outside the tables, whether a table carries a header row. The user's own words: "표를 좌우로 배치하고싶어서 수정했거든." This is not a table standard to apply; it is the user's arrangement to follow, whatever it happens to be.

Nothing else is synced. The repository stays the single source of truth: Notion is a mirror for humans, so a conflict is resolved in favor of the file in git.

## 4. Read vs write

- **Read (search, fetch, view)**: allowed anywhere in the workspace when the user asks for it — reading does not damage another project's document. Still, do not dump fetched page bodies into the main context; summarize.
- **Write (create, update, append, move, delete)**: confined to §1's subtree, and only for the §3 targets.
- **Korean goes into the tool arguments as literal UTF-8, never a hand-recalled `\uXXXX` escape.** The rule's single source is CLAUDE.md '한국어 말투'; what belongs here is why this is the path where it keeps breaking. It was violated 4 times on 2026-08-31: once an escape broke an `AskUserQuestion` call outright on JSON parsing, once it corrupted 5 Hangul characters in a Notion write that then cost a repair call, once the MCP happened to decode it and nothing showed, and once it went in although the 위임장 carried an explicit ban on it. So neither the rule nor a line in the 위임장 holds on its own, and three of those four were silent or only half-visible — the fetch-back comparison below is what actually catches this one.
- **Notion rewrites some text as it stores it — wrap anything that looks like a filename in backticks.** A bare `CLAUDE.md` or `STATE.md` in a table cell becomes an `http://CLAUDE.md` link (4 cells in one write, all needing a second call to undo); inside backticks it survives verbatim. **When the source legitimately has no backticks — a mirror copying a file verbatim — the link is not undoable and must simply be reported.** Rewriting the cell as plain text re-links it on every write (32 places, measured), and editing the link markup itself duplicated the adjacent Hangul twice (`)이` → `)이이))`). Leave the href, keep the plain text matching the source, and say so in the report. Some ` + ` separators in cells also come back as ` • ` — harmless, leave them. Neither conversion raises an error, so **keep the pre-write fetch as a backup** and fetch the page back after every write: compare the text against the source, and the layout against that backup (§3). The comparison is never skipped — what the difference *means* is the judgment call: **a difference is not automatically a loss, so first ask whether your own call explains it.** If it does, restore it from the backup instead of leaving it — a silent loss is the failure mode here, not an error message. If it does not — this session updated only the `title` property, yet table cell text and a column width came back changed — the change is outside what you wrote, so read it as another editor's concurrent work: **preserve it and report it, never restore over it.** When the answer is unclear the default is preservation, because the two mistakes are not symmetric: restoring erases someone else's edit irreversibly, while an unnecessary report costs a line and can be acted on later. The user confirmed this reading of the case above: "내가 의도해서 수정한게 맞아. 앞으로도 그렇게 판단하면 좋겠어."
- **`notion-create-pages` deletes the H1 sitting at the very top of `content`** — even when `title` is passed separately, so a full-text mirror loses its first line silently; the same fetch-back above catches it, and the fix is to re-insert that heading at the top.
- **Delete of an existing Notion page is never done on our own judgment** — propose it and let the user do it.
