#!/usr/bin/env bash
# SessionStart marker hook — reads .claude/identity and prints AT MOST ONE
# marker line to stdout (SessionStart plain stdout enters model context;
# one line keeps session-start noise minimal — v1 measured lesson).
#
# Markers:
#   TEMPLATE-REPO               identity: template AND origin fingerprint matches
#   PROJECT-REPO SETUP-PENDING  fingerprint mismatch (= a clone of the template,
#                               auto-recognized) OR identity: project + setup: pending
#                               (the session then runs SETUP.md with NO user
#                               confirmation; SETUP.md step 2-0 takes a snapshot
#                               commit before deleting anything, so a false
#                               positive here stays revertible — tracked files
#                               via the commit, identity and the renamed remote
#                               by hand per 2-0; where no snapshot is possible
#                               at all, 2-0 stops instead of deleting)
#   (no output)                 identity: project + setup: done — normal project
#   IDENTITY-MISSING            identity file absent or identity value invalid
#                               (session asks once and creates/fixes it)
#   KB-OVER-LIMIT               a knowledge file is over its cap — STATE > 150 /
#                               MAP > 120 / GOTCHAS > 100 lines. Appended as a
#                               suffix to the marker above (or printed alone when
#                               there is no identity marker), so the output stays
#                               one line. It deliberately does NOT name the file —
#                               .claude/skills/handoff/SKILL.md, the consumer,
#                               identifies which one and states that contract.
#                               A missing file never triggers it.
#   MAP-STALE                   docs/MAP.md's top-line `확인일:` date is older than
#                               MAP_STALE_DAYS (below). Appended the same way as
#                               KB-OVER-LIMIT — suffix, or alone. Silent when the
#                               file is absent (normal in the template repo, which
#                               has no MAP), when the top line carries no readable
#                               `확인일:` calendar date, or when that date is in
#                               the future (a negative age, which the numeric
#                               guard below rejects).
#                               Consumer: .claude/skills/handoff/SKILL.md.
#   AUTOMODE-ON                 unattended mode is on — the main repository root
#                               holds a `.claude/automode` whose first line is
#                               `on`. Appended the same way as KB-OVER-LIMIT —
#                               suffix, or alone. Silent on every other reading:
#                               `off`, an unknown value, an unreadable file and
#                               an absent file all mean 유인 (attended), because
#                               a failed read must never hand the session the
#                               unattended mode nobody asked for.
#                               Consumer: .claude/agents/orchestrator.md.
#   CLONE-SUSPECT               this checkout looks like a fresh `git clone` (see
#                               the transient limitation below). Appended as a
#                               suffix to TEMPLATE-REPO only. NOTICE ONLY — it
#                               changes no branch and triggers no automatic run;
#                               the session tells the user once and points at
#                               README.md's init procedure. Deliberately never
#                               feeds the SETUP-PENDING decision: a false positive
#                               must never reach the deleting path.
#
# Fingerprint = normalized `git remote get-url origin`; absent remote → "none".
# Normalization (same rule as documented in .claude/identity): strip scheme and
# `git@`, scp-form first `:` → `/`, strip trailing `/` and `.git`, lowercase.
# Chosen over the root-commit hash because clones/forks share commit history
# (hash identical in original and copy) while the remote differs the moment a
# copy exists — a local clone gains the source path as origin, a GitHub
# template generation or fork gets a different URL. Known transient limitation:
# a fresh clone of a PUBLISHED template keeps the template URL as origin until
# repointed, and reads as TEMPLATE-REPO for that window — the CLONE-SUSPECT
# suffix makes that window visible instead of silent, without changing it.
#
# Always exit 0 — never block session start.
#
# SYNC NOTE: scripts/session-banner.sh replicates this detection (val()/norm()
# and the case-by-case decision) to pick its banner mode. If the logic here
# changes, change it there too.

d=${CLAUDE_PROJECT_DIR:-$PWD}
idf="$d/.claude/identity"

# Line count of a file; 0 when it is absent (a missing MAP.md is normal).
# awk, not `grep -c`: the rtk wrapper strips a backslash layer from space-free
# pattern arguments, which silently turns a count into a wrong count.
lines() {
  [ -f "$1" ] || { printf '0'; return; }
  awk 'END { print NR + 0 }' "$1" 2>/dev/null || printf '0'
}

# Knowledge-file cap check → KB-OVER-LIMIT suffix (empty when all are under cap).
kb=
if [ "$(lines "$d/docs/STATE.md")" -gt 150 ] ||
   [ "$(lines "$d/docs/MAP.md")" -gt 120 ] ||
   [ "$(lines "$d/docs/GOTCHAS.md")" -gt 100 ]; then
  kb=KB-OVER-LIMIT
fi

# How old docs/MAP.md's `확인일:` may get before it counts as stale. The only
# place this number appears — change it here and nowhere else.
MAP_STALE_DAYS=30

# MAP freshness check → MAP-STALE suffix. Stays empty (silent) on every
# uncertainty: no file, no readable date on the top line, or a date that is not
# a real day on the calendar. A false marker costs more than a missed one.
stale=
mapf="$d/docs/MAP.md"
if [ -f "$mapf" ]; then
  # Top line only, and only when it really is the `확인일:` key — matched as a
  # literal byte sequence via index(), so the locale cannot affect it.
  mapdate=$(awk 'NR==1 { if (index($0, "확인일:") && match($0, /[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]/)) print substr($0, RSTART, RLENGTH) } NR>1 { exit }' "$mapf" 2>/dev/null)
  if [ -n "$mapdate" ]; then
    # Age in days, computed from two Y-M-D strings in awk. It used to be
    # `date -d "$mapdate" +%s`, which is GNU-only: BSD/macOS `date` answers
    # `illegal option -- d`, the `2>/dev/null` capture turned that into an empty
    # string, and the silent branch read the empty string as "not stale" — so
    # this gate was dead on macOS from the day it shipped, without an error
    # (measured 2026-08-29). awk is already this script's calculator (see
    # lines() above) and behaves the same on every platform, so the fix removes
    # the `date` arithmetic instead of adding a second platform branch.
    # Both values go through the environment: `awk -v` interprets escapes in the
    # value, ENVIRON does not (.claude/rules/shell-traps.md).
    # Nothing is printed unless both are real calendar dates, which keeps the
    # silent branch for a broken date such as 2026-02-30; a future date yields a
    # negative number, which the numeric guard below rejects — also silent.
    age=$(MAPDATE="$mapdate" TODAY="$(date +%Y-%m-%d)" awk '
      # Days since 1970-01-01 for a Y-M-D string (Howard Hinnant days_from_civil,
      # integer-only). Sets `bad` on anything that is not a real date.
      function days(s,   p, y, m, e, dm, era, yoe, doy, doe) {
        split(s, p, "-"); y = p[1] + 0; m = p[2] + 0; e = p[3] + 0
        if (y < 1 || m < 1 || m > 12 || e < 1) { bad = 1; return 0 }
        dm = 31
        if (m == 4 || m == 6 || m == 9 || m == 11) dm = 30
        else if (m == 2) dm = (y % 4 == 0 && (y % 100 != 0 || y % 400 == 0)) ? 29 : 28
        if (e > dm) { bad = 1; return 0 }
        y -= (m <= 2)
        era = int(y / 400); yoe = y - era * 400
        doy = int((153 * (m + (m > 2 ? -3 : 9)) + 2) / 5) + e - 1
        doe = yoe * 365 + int(yoe / 4) - int(yoe / 100) + doy
        return era * 146097 + doe - 719468
      }
      BEGIN {
        a = days(ENVIRON["MAPDATE"]); b = days(ENVIRON["TODAY"])
        if (!bad) print b - a
      }' 2>/dev/null)
    case "$age" in
      ''|*[!0-9]*) : ;;                # unreadable, or a future date — silent
      *) [ "$age" -gt "$MAP_STALE_DAYS" ] && stale=MAP-STALE ;;
    esac
  fi
fi

# Unattended-mode check → AUTOMODE-ON suffix. The state file sits at the MAIN
# repository root even when this runs inside a linked worktree, so the root comes
# from git's common dir — the same place `scripts/statusline.sh` reads it from,
# and the two must never disagree about which file is the mode. `git rev-parse`
# prints nothing and fails outside a repository, so the result is judged by rc
# and not by emptiness (.claude/rules/shell-traps.md), falling back to $d.
# Anything but a literal `on` — including every failure above — stays silent.
amroot=$d
if gcd=$(git -C "$d" rev-parse --git-common-dir 2>/dev/null) && [ -n "$gcd" ]; then
  case "$gcd" in
    /* | ?:*) : ;;                   # already absolute
    *) gcd="$d/$gcd" ;;              # `.git` / `../.git`, relative to $d
  esac
  gcd=${gcd%/}
  gcd=${gcd%/.git}
  [ -d "$gcd" ] && amroot=$gcd
fi
auto=
if [ -f "$amroot/.claude/automode" ]; then
  amv=$(head -1 "$amroot/.claude/automode" 2>/dev/null | tr -d '[:space:]')
  [ "$amv" = on ] && auto=AUTOMODE-ON
fi

# Combined suffix — KB-OVER-LIMIT first, then MAP-STALE, then AUTOMODE-ON; any of
# them may be absent, and the output stays one line either way. All three ride in
# $kb so that every `echo` below keeps its single `${kb:+ $kb}` tail.
kb="${kb}${kb:+${stale:+ }}${stale}"
kb="${kb}${kb:+${auto:+ }}${auto}"

if [ ! -f "$idf" ]; then
  echo "IDENTITY-MISSING${kb:+ $kb}"
  exit 0
fi

# Read `key: value` — first matching non-comment line, whitespace stripped.
val() {
  grep -E "^[[:space:]]*$1:" "$idf" 2>/dev/null | head -1 \
    | sed 's/^[^:]*:[[:space:]]*//' | tr -d '[:space:]'
}

# Canonical remote form: host/owner/repo (lowercased) or a bare local path.
norm() {
  u=$1
  while [ "${u%/}" != "$u" ]; do u=${u%/}; done
  u=${u#*://}
  u=${u#git@}
  case "$u" in *:*) u=${u%%:*}/${u#*:} ;; esac
  u=${u%.git}
  printf '%s' "$u" | tr '[:upper:]' '[:lower:]'
}

# Fresh-clone heuristic → CLONE-SUSPECT (empty otherwise). A clone writes
# exactly one reflog entry, `clone: from <url>`, and any real work in the
# template original pushes that entry down (measured 2026-08-29: a new clone
# has 1 entry, this repository has 73 with `commit (initial)` at the bottom).
# Every uncertainty stays silent — outside a git repo, or with reflogs
# disabled, `git reflog` prints nothing and no suffix is emitted.
clone_suspect() {
  rl=$(git -C "$d" reflog --format='%gs' -2 2>/dev/null)
  [ "$(printf '%s' "$rl" | awk 'END { print NR + 0 }')" = 1 ] || return 0
  case "$rl" in "clone: from "*) printf 'CLONE-SUSPECT' ;; esac
}

idv=$(val identity)
case "$idv" in
  template)
    rec=$(val origin)
    [ -n "$rec" ] || rec=none          # legacy identity without origin: key
    [ "$rec" = none ] || rec=$(norm "$rec")
    act=$(git -C "$d" remote get-url origin 2>/dev/null)
    if [ -n "$act" ]; then act=$(norm "$act"); else act=none; fi
    if [ "$act" = "$rec" ]; then
      cs=$(clone_suspect)
      echo "TEMPLATE-REPO${cs:+ $cs}${kb:+ $kb}"
    else
      echo "PROJECT-REPO SETUP-PENDING${kb:+ $kb}"
    fi
    ;;
  project)
    case "$(val setup)" in
      done)                            # initialized — no identity marker
        [ -n "$kb" ] && echo "$kb"
        ;;
      *) echo "PROJECT-REPO SETUP-PENDING${kb:+ $kb}" ;;
    esac
    ;;
  *)
    echo "IDENTITY-MISSING${kb:+ $kb}"
    ;;
esac
exit 0
