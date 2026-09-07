#!/usr/bin/env bash
# PreCompact hook — writes a mechanical snapshot of the repository state to
# <repo root>/.claude/compact-snapshot.md (overwrite, local only / gitignored).
#
# Why: compaction drops the tail of the conversation, and what gets lost there is
# usually mechanical (unpushed commits, orphan worktree commits, a unit whose
# verdict never landed). A hook is a shell command — it cannot run a slash
# command or a tool, so it can record FACTS ONLY. No judgement, no summary:
# an invented "conclusion" here would be injected into the model context as if
# it were true.
#
# Companion: scripts/compact-resume.sh (SessionStart, matcher `compact`) points
# the next session at this file.
#
# Never blocks compaction and never fails — always exit 0 (PreCompact exit 2
# would ABORT the compaction; delaying it is not possible). No `set -e`.
# Body is ASCII; only git's own output (commit subjects) may carry UTF-8, and it
# is passed through byte-for-byte — nothing is re-encoded through a print path.

d=${CLAUDE_PROJECT_DIR:-$PWD}
root=$(git -C "$d" rev-parse --show-toplevel 2>/dev/null)
[ -n "$root" ] || exit 0            # not a git repo: nothing to snapshot

out="$root/.claude/compact-snapshot.md"
tmp="$out.tmp.$$"
mkdir -p "$root/.claude" 2>/dev/null || exit 0

g() { git -C "$root" "$@" 2>/dev/null; }

{
  echo "# compact snapshot"
  echo
  echo "Mechanical facts only, written by scripts/compact-snapshot.sh just before compaction."
  echo "Nothing here is interpreted - compare it against what you were doing."
  echo
  echo "- taken: $(date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null)"
  echo "- branch: $(g rev-parse --abbrev-ref HEAD)"
  echo "- HEAD: $(g rev-parse HEAD)"
  echo
  echo '## recent commits (git log --oneline -15)'
  echo
  g log --oneline -15
  echo
  echo '## unpushed commits (git log --oneline @{u}..HEAD)'
  echo
  if g rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1; then
    up=$(g log --oneline '@{u}..HEAD')
    if [ -n "$up" ]; then echo "$up"; else echo "(none - branch is not ahead of upstream)"; fi
  else
    echo "(no upstream configured for this branch - push state unknown)"
  fi
  echo
  echo '## working tree (git status --porcelain)'
  echo
  # rc, never emptiness: outside a git repo status exits 128 with empty stdout,
  # and reading that as "clean" turns a failure into a false all-clear
  # (.claude/rules/shell-traps.md).
  st=$(g status --porcelain); rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "(git status failed, rc=$rc - working tree state unknown)"
  elif [ -n "$st" ]; then
    echo "$st"
  else
    echo "(clean)"
  fi
  echo
  wt=$(g worktree list)
  if [ "$(printf '%s\n' "$wt" | grep -c .)" -gt 1 ]; then
    echo '## worktrees (git worktree list)'
    echo
    echo "$wt"
    echo
  fi
  echo '## verdicts (.claude/review/*.verdict.md)'
  echo
  found=0
  for f in "$root"/.claude/review/*.verdict.md; do
    [ -f "$f" ] || continue
    found=1
    v=$(grep -m1 -E '^-[[:space:]]*판정:' "$f" 2>/dev/null)
    [ -n "$v" ] || v="(no verdict line)"
    echo "- ${f##*/}: ${v#*: }"
  done
  [ "$found" -eq 1 ] || echo "(no verdict files)"
} > "$tmp" 2>/dev/null

mv -f "$tmp" "$out" 2>/dev/null || rm -f "$tmp" 2>/dev/null
exit 0
