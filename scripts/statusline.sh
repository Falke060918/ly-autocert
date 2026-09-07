#!/usr/bin/env bash
# Claude Code statusLine renderer.
#
# Reads the session JSON that Claude Code writes to stdin and prints one line:
# model | directory | git branch | rtk token savings — followed, only when
# .claude/perf/units/ exists, by one line per parallel work unit.
#
# Design constraints:
#   - never fail, never print an empty line (each section degrades silently)
#   - no machine-specific paths: this file ships in a reusable template, so it
#     locates the project through $CLAUDE_PROJECT_DIR / the session JSON /
#     its own path, in that order.
#   - stay well under 300ms. Process spawns cost ~20-30ms each on Windows, so
#     everything here is a bash builtin: JSON is parsed with =~, the git branch
#     is read straight from .git/HEAD, and `rtk gain` is cached for RTK_TTL
#     seconds. The only fork is `rtk` itself, on a cache miss.
#
# Windows note: Claude Code runs statusLine through Git Bash when it is
# installed, so this stays a bash script rather than PowerShell (bash starts in
# ~30ms, `powershell -NoProfile` needs ~140ms+). settings.json invokes it as
# `bash <path>` so the file never needs the executable bit.

exec 2>/dev/null # nothing this script does should ever surface on stderr

RTK_TTL=60
RTK_CACHE_DIR=${TMPDIR:-/tmp}

# ANSI SGR. Claude Code always renders its UI with VT sequences enabled.
C_MODEL=$'\033[36m'  # cyan
C_DIR=$'\033[1;34m'  # bright blue
C_GIT=$'\033[35m'    # magenta
C_RTK=$'\033[32m'    # green
C_AUTO=$'\033[1;33m' # bright yellow — unattended mode, shown only while it is on
C_SEP=$'\033[90m'    # grey
C_OFF=$'\033[0m'
C_PROBE=$'\033[2m'   # dim
C_WRITE=$'\033[36m'  # cyan
C_VERIFY=$'\033[33m' # yellow
C_DONE=$'\033[32m'   # green
C_LANDED=$'\033[1;32m' # bright green
C_FAIL=$'\033[31m'   # red

# Helpers return through $_R instead of $(...) so they cost no subshell.
_R=''

# _win_path <raw> — JSON-escaped Windows path -> forward slashes, no trailing /,
# no "." segments. The normalisation matters: the path doubles as a cache key,
# and ".../proj" and ".../proj/." must not hash to two different files.
_win_path() {
	local p=$1
	p=${p//\\\\//} # \\ -> /
	p=${p//\\//}   # \  -> /
	while [[ $p == */./* ]]; do p=${p//\/.\//\/}; done
	p=${p%/.}
	while [ ${#p} -gt 1 ] && [ "${p: -1}" = / ]; do p=${p%/}; done
	_R=$p
}

# _json_str <json> <key> — first "key": "value" match.
_json_str() {
	[[ $1 =~ \"$2\"[[:space:]]*:[[:space:]]*\"([^\"]*)\" ]] || return 1
	_R=${BASH_REMATCH[1]}
}

# _json_int <json> <key> — first "key": <number> match, integer part only.
_json_int() {
	[[ $1 =~ \"$2\"[[:space:]]*:[[:space:]]*([0-9]+) ]] || return 1
	_R=${BASH_REMATCH[1]}
}

# _hash32 <string> — djb2, hex. Used to key per-project cache files without
# putting a (possibly very long, possibly non-portable) path in a filename.
_hash32() {
	local s=$1 i h=5381 c n
	n=${#s} # separate statement: `local` expands every RHS before assigning
	for ((i = 0; i < n; i++)); do
		printf -v c '%d' "'${s:i:1}"
		h=$(((h * 33 + c) & 0xFFFFFFFF))
	done
	printf -v _R '%08x' "$h"
}

# _cache_key <dir> — stable hash for a project directory. Under Git Bash the
# same folder reaches us as both "C:/x" (session JSON) and "/c/x" ($PWD), so
# fold the MSYS form into the drive form and lowercase the drive letter first,
# or one project would end up with two cache files that expire independently.
_cache_key() {
	local p=$1 alpha=ABCDEFGHIJKLMNOPQRSTUVWXYZ i
	case $OSTYPE in
	msys* | cygwin* | win*)
		case $p in
		/[A-Za-z]/* | /[A-Za-z]) p=${p:1:1}:${p:2} ;; # /c/x -> c:/x
		esac
		case $p in
		[A-Z]:*)
			i=${alpha%%"${p:0:1}"*} # letters before this one == its index
			alpha=abcdefghijklmnopqrstuvwxyz
			p=${alpha:${#i}:1}${p:1}
			;;
		esac
		;;
	esac
	_hash32 "$p"
}

# --- stdin -------------------------------------------------------------------
input=''
if [ ! -t 0 ]; then
	# -d '' slurps everything; returns non-zero at EOF but still fills input.
	# -t guards against a caller that never closes the pipe, which would
	# otherwise wedge the status line forever.
	IFS= read -r -d '' -t 2 input
fi

parts=()

# --- model -------------------------------------------------------------------
if _json_str "$input" display_name; then
	parts+=("${C_MODEL}${_R}${C_OFF}")
fi

# --- directory ---------------------------------------------------------------
_json_str "$input" current_dir || _json_str "$input" cwd || _R=$PWD
[ -n "$_R" ] || _R=$PWD
_win_path "$_R"
dir=$_R
base=${dir##*/}
[ -n "$base" ] && parts+=("${C_DIR}${base}${C_OFF}")

# --- project root ------------------------------------------------------------
# Scope for the rtk numbers below. Never a hardcoded path: prefer what the
# session tells us, then $CLAUDE_PROJECT_DIR, then this script's own location
# (<root>/scripts/statusline.sh), then the session directory.
proj=''
if _json_str "$input" project_dir; then
	_win_path "$_R"
	proj=$_R
elif [ -n "$CLAUDE_PROJECT_DIR" ]; then
	_win_path "$CLAUDE_PROJECT_DIR"
	proj=$_R
else
	self=${BASH_SOURCE[0]:-$0}
	_win_path "$self"
	self=$_R
	case $self in
	/* | ?:/*) ;;
	*)
		_win_path "$PWD/$self"
		self=$_R
		;;
	esac
	self=${self%/*} # .../scripts
	self=${self%/*} # .../  (project root)
	[ -n "$self" ] && [ -d "$self" ] && proj=$self
fi
[ -n "$proj" ] || proj=$dir

# --- git branch --------------------------------------------------------------
# Walk up to the nearest .git and read HEAD directly: no `git` process spawn.
_git_branch() {
	local d=$1 prev='' gitdir='' head line
	[ -d "$d" ] || return 1
	while [ -n "$d" ] && [ "$d" != "$prev" ]; do
		if [ -d "$d/.git" ]; then
			gitdir=$d/.git
			break
		elif [ -f "$d/.git" ]; then # linked worktree / submodule
			IFS= read -r line <"$d/.git" || return 1
			line=${line#gitdir:}
			_win_path "${line# }"
			case $_R in
			/* | ?:/*) gitdir=$_R ;;
			*) gitdir=$d/$_R ;;
			esac
			break
		fi
		prev=$d
		d=${d%/*}
	done
	[ -n "$gitdir" ] || return 1
	IFS= read -r head <"$gitdir/HEAD" || return 1
	case $head in
	ref:*)
		head=${head#ref:}
		head=${head# }
		_R=${head#refs/heads/}
		;;
	'') return 1 ;;
	*) _R=${head:0:7} ;; # detached HEAD
	esac
	[ -n "$_R" ]
}

if _git_branch "$dir"; then
	parts+=("${C_GIT}${_R}${C_OFF}")
fi

# _repo_root <dir> — main repository root into $_R, i.e. the directory the
# status writers anchor to. Same upward walk as _git_branch, one fork lighter
# than the writers' `git rev-parse --git-common-dir`: a linked worktree's .git
# file points at <main>/.git/worktrees/<name>, so dropping the /worktrees/<name>
# tail and the .git tail lands on the main repo from inside a worktree too —
# exactly what `--git-common-dir` minus its own `.git` tail yields.
_repo_root() {
	local d=$1 prev='' line g
	[ -d "$d" ] || return 1
	while [ -n "$d" ] && [ "$d" != "$prev" ]; do
		if [ -d "$d/.git" ]; then
			_R=$d
			return 0
		elif [ -f "$d/.git" ]; then # linked worktree / submodule
			IFS= read -r line <"$d/.git" || return 1
			line=${line#gitdir:}
			_win_path "${line# }"
			case $_R in
			/* | ?:/*) g=$_R ;;
			*) g=$d/$_R ;;
			esac
			case $g in */worktrees/*) g=${g%/worktrees/*} ;; esac
			# Anything that is not <root>/.git by now is not a shape we can map
			# back (a submodule, say); let the caller keep its own guess.
			[ "${g%/.git}" != "$g" ] || return 1
			_R=${g%/.git}
			return 0
		fi
		prev=$d
		d=${d%/*}
	done
	return 1
}

# --- rtk savings -------------------------------------------------------------
# 41460 -> 41.5K, 1234567 -> 1.2M
_human() {
	local n=$1 t
	if [ "$n" -ge 999950 ]; then
		t=$(((n + 50000) / 100000)) # tenths of a million, rounded
		printf -v _R '%d.%dM' $((t / 10)) $((t % 10))
	elif [ "$n" -ge 1000 ]; then
		t=$(((n + 50) / 100)) # tenths of a thousand, rounded
		printf -v _R '%d.%dK' $((t / 10)) $((t % 10))
	else
		_R=$n
	fi
}

# Numbers are scoped to $proj: `rtk gain -p` filters on the *current working
# directory*, so we cd first (builtin, free). Older rtk builds have no -p; they
# fall back to the global total rather than dropping the section.
_rtk_gain() {
	local bin=$HOME/.local/bin/rtk.exe raw saved pct
	if [ ! -x "$bin" ]; then
		bin=$(command -v rtk) || return 1
	fi
	# `|| return 1` and not `|| exit`: a failed cd must only drop the rtk
	# section, never the whole status line. A missing $proj is not an error —
	# we then just read the numbers from wherever we already are.
	if [ -d "$proj" ]; then
		cd "$proj" || return 1
	fi
	raw=$("$bin" gain -p -f json) || raw=$("$bin" gain -f json) || return 1
	_json_int "$raw" total_saved || return 1
	saved=$_R
	# A project with no recorded savings prints nothing at all: "rtk ↓0 (0%)"
	# is noise, and every freshly cloned project would start out wearing it.
	[ "$saved" -gt 0 ] || return 1
	pct=''
	_json_int "$raw" avg_savings_pct && pct=$_R
	_human "$saved"
	if [ -n "$pct" ]; then
		printf -v _R 'rtk ↓%s (%s%%)' "$_R" "$pct"
	else
		printf -v _R 'rtk ↓%s' "$_R"
	fi
}

# One cache file per project, so switching projects never shows the previous
# one's numbers for up to RTK_TTL seconds. An empty value is cached too — that
# is what suppresses the rtk fork when the project has no stats (or rtk is not
# installed at all).
_cache_key "$proj"
RTK_CACHE=$RTK_CACHE_DIR/cc-statusline-rtk-$_R.cache

rtk=''
fresh=0
printf -v now '%(%s)T' -1 || now=''
case $now in # bash 3.2 (macOS /bin/bash) has no %(...)T
'' | *[!0-9]*) now=$(date +%s) ;;
esac
case $now in
'' | *[!0-9]*) now=0 ;;
esac
if [ -r "$RTK_CACHE" ]; then
	ts='' cached=''
	{
		IFS= read -r ts && IFS= read -r cached
	} <"$RTK_CACHE"
	case $ts in
	'' | *[!0-9]*) ;;
	*) [ $((now - ts)) -lt "$RTK_TTL" ] && {
		fresh=1
		rtk=$cached
	} ;;
	esac
fi
if [ "$fresh" = 0 ]; then
	_rtk_gain || _R=''
	rtk=$_R
	printf '%s\n%s\n' "$now" "$rtk" >"$RTK_CACHE"
fi
[ -n "$rtk" ] && parts+=("${C_RTK}${rtk}${C_OFF}")

# --- work units --------------------------------------------------------------
# Optional dashboard: one line per parallel work unit, read from
# <main repo root>/.claude/perf/units/<unit>.status. Each file holds exactly one
# line, "stage|unit|epoch|note|source", where stage is probe/write/verify/done/
# landed/fail and source is main / sub / child, optionally suffixed with
# "@<worktree-name>". Fields are counted from the front and a short tail is
# tolerated, so a 4-field line written before source existed still renders.
#
# Closing stages (done/landed/fail) drop off the board after UNIT_CLOSED_STALE
# seconds so only work still in flight stays on screen; an open stage lasts
# UNIT_OPEN_STALE seconds past its last update, because a unit that stopped
# refreshing that long ago is an abandoned run, not work in flight.
#
# SYNC NOTE: the writer side of this is `.claude/conventions/status-protocol.md`
# (the single source — agents reference it, no per-agent copies in v2). Path,
# field split, stage tokens and the empty-note rule are one agreement across
# both sides — change either and fix the other in the same commit.
#
# The whole section hangs off `[ -d "$units_dir" ]`, so a project that never
# writes status files renders byte-for-byte what it rendered before. Files are
# read with the `read` builtin (no fork per unit) and their age comes from the
# epoch field, never from a stat call.
UNIT_OPEN_STALE=3600 # 1h — an open stage (probe/write/verify) that has not been
                   # refreshed for this long is an abandoned or past-session run
UNIT_CLOSED_STALE=60 # 1m — a closed unit (done/landed/fail) has nothing left to
                   # watch, so it clears fast and the board shows live work only
UNIT_MAX=8         # hard cap on the lines added below the status line
UNIT_COLS=120      # fallback width when the caller exports no COLUMNS
UNIT_READ_MAX=260  # characters read per file; wider than any terminal line

# _cut <text> <max-columns> — display width into $_W, text into $_T, truncated
# with an ellipsis when it does not fit. max <= 0 measures without cutting.
# Width counts East Asian wide characters as two columns; a byte-oriented
# locale overestimates instead of underestimating, which truncates a little
# early but never wraps the line.
#
# bash 3.2 note (macOS /bin/bash): `printf '%d' "'$c"` there returns the first
# *byte* of a multibyte character as a signed char (가 -> -22), never the
# codepoint — which zeroed out every CJK width in v1. So this switches to
# LC_ALL=C (bash applies the locale change immediately, verified on 3.2.57)
# and decodes UTF-8 byte-wise; that also makes the width independent of the
# ambient locale. All indices below (i, safe) are byte offsets, so the final
# slice must happen before the locale is restored.
_T='' _W=0
_cut() {
	local s=$1 max=$2 i j n b cp cw len w=0 safe=0 safew=0 over=0
	local lc_set=${LC_ALL+x} lc=${LC_ALL-}
	LC_ALL=C # byte semantics for ${#s} / ${s:i:1} from here on
	# Bound the loop: every character is at least one column wide and at most
	# four bytes long, so nothing past byte max*4 can still fit in $max
	# columns. Without this an over-long note — the one field a work unit
	# writes freely — scales the hot path linearly.
	if [ "$max" -gt 0 ] && [ ${#s} -gt $((max * 4)) ]; then
		s=${s:0:max*4}
		over=1 # the dropped tail alone guarantees an overflow
	fi
	n=${#s}
	for ((i = 0; i < n; i += len)); do
		printf -v b '%d' "'${s:i:1}" || b=0
		[ "$b" -ge 0 ] || b=$((b + 256)) # signed char -> 0..255
		if [ "$b" -lt 128 ]; then
			len=1 cp=$b
		elif [ "$b" -lt 224 ]; then
			len=2 cp=$((b & 31))
		elif [ "$b" -lt 240 ]; then
			len=3 cp=$((b & 15))
		else
			len=4 cp=$((b & 7))
		fi
		for ((j = i + 1; j < i + len && j < n; j++)); do
			printf -v b '%d' "'${s:j:1}" || b=0
			[ "$b" -ge 0 ] || b=$((b + 256))
			cp=$(((cp << 6) | (b & 63)))
		done
		cw=1
		if [ "$cp" -ge 4352 ] && [ "$cp" -le 4447 ]; then
			cw=2 # Hangul Jamo
		elif [ "$cp" -ge 11904 ] && [ "$cp" -le 42191 ]; then
			cw=2 # CJK radicals .. Yi
		elif [ "$cp" -ge 44032 ] && [ "$cp" -le 55203 ]; then
			cw=2 # Hangul syllables
		elif [ "$cp" -ge 63744 ] && [ "$cp" -le 64255 ]; then
			cw=2 # CJK compatibility ideographs
		elif [ "$cp" -ge 65281 ] && [ "$cp" -le 65376 ]; then
			cw=2 # fullwidth forms
		elif [ "$cp" -ge 127744 ] && [ "$cp" -le 129791 ]; then
			cw=2 # emoji
		fi
		if [ $((w + cw)) -le $((max - 1)) ]; then
			safe=$((i + len))
			safew=$((w + cw))
		fi
		w=$((w + cw))
	done
	if [ "$max" -gt 0 ] && { [ "$over" = 1 ] || [ "$w" -gt "$max" ]; }; then
		_T=${s:0:safe} # byte slice — must run under LC_ALL=C
	else
		_T=$s safe=-1
	fi
	if [ -n "$lc_set" ]; then LC_ALL=$lc; else unset LC_ALL; fi
	if [ "$safe" -ge 0 ]; then
		_T+=…
		_W=$((safew + 1))
	else
		_W=$w
	fi
}

# _elapsed <seconds> — 12s / 4m / 1h04m
_elapsed() {
	local s=$1
	if [ "$s" -lt 60 ]; then
		_R=${s}s
	elif [ "$s" -lt 3600 ]; then
		_R=$((s / 60))m
	else
		printf -v _R '%dh%02dm' $((s / 3600)) $(((s % 3600) / 60))
	fi
}

# _wt_no <name> — 1-based number of a worktree name in the sorted wt_names
# array. The *reader* hands out these numbers, never the writer: writers only
# leave the worktree name, so a number cannot drift between two agents, and
# sorting by name keeps it from moving when a unit appears or finishes.
wt_names=()
_wt_no() {
	local w=$1 x n=${#wt_names[@]}
	for ((x = 0; x < n; x++)); do
		if [ "${wt_names[x]}" = "$w" ]; then
			_R=$((x + 1))
			return 0
		fi
	done
	return 1
}

# _label <kind> <worktree> — ASCII source tag into $_R, at most 8 columns.
# [M] main session · [S] internal subagent · [S/W1] subagent driving worktree 1
# (the Orca proxy) · [W1] the child agent inside worktree 1. ASCII only: the
# glyphs elsewhere on the line are East Asian Ambiguous already, and the tag
# doubles as an alignment column that must not shift per terminal.
_label() {
	local k=$1 w=$2 n=''
	_R=''
	[ -n "$k" ] || return 0
	if [ -n "$w" ] && _wt_no "$w"; then
		n=$_R
	fi
	case $k in
	main) _R='[M]' ;;
	sub)
		if [ -n "$n" ]; then _R="[S/W$n]"; else _R='[S]'; fi
		;;
	child)
		if [ -n "$n" ]; then _R="[W$n]"; else _R='[W]'; fi
		;;
	*) _R='' ;;
	esac
	[ ${#_R} -le 8 ] || _R="${_R:0:7}]"
}

unit_lines=()
# Never a bare relative path: the status line is invoked from whatever cwd the
# session happens to sit in, while the writers anchor to the main repo root.
# $proj (session JSON's workspace.project_dir, then $CLAUDE_PROJECT_DIR, then
# this script's own location, then the session dir) is the cheap starting
# point; _repo_root then folds a linked worktree back onto the main repo the
# way the writers' git-common-dir does. Neither step forks.
units_root=$proj
_repo_root "$units_root" && units_root=$_R
[ -n "$units_root" ] || units_root=$PWD

# --- automode ----------------------------------------------------------------
# <main repo root>/.claude/automode holds one line: `on` (무인 — the session
# closes 진행 질문 on defaults) or `off`. Forgetting that it is on is this
# feature's biggest risk, so the mode gets a permanent seat on the status line —
# but only while it is on: any other reading (an unknown value, an unreadable or
# absent file) is 유인 and adds nothing, which keeps the rendered output
# byte-for-byte what it was before this section existed. `read` is a builtin, so
# this forks nothing; the root is the one $units_root already resolved, so the
# marker hook and the status line can never point at two different files.
# Contract single source: docs/specs/2026-08-31-automode-toggle.md §3.
automode=''
if [ -r "$units_root/.claude/automode" ]; then
	# First line only; a file with no trailing newline still fills the variable.
	IFS= read -r automode <"$units_root/.claude/automode" || :
	automode=${automode//[[:space:]]/} # CR of a CRLF file, stray padding
fi
case $automode in
on) parts+=("${C_AUTO}무인모드${C_OFF}") ;;
esac

units_dir=$units_root/.claude/perf/units
if [ -d "$units_dir" ]; then
	u_key=() u_stage=() u_name=() u_age=() u_note=() u_kind=() u_wtn=()
	n_probe=0 n_write=0 n_verify=0 n_done=0 n_landed=0 n_fail=0
	for f in "$units_dir"/*.status; do
		[ -f "$f" ] || continue # unmatched glob, or a directory
		fld=()
		# First line only, no subshell. -n bounds the read: `read` consumes a
		# long line one character at a time, so a runaway note (the one field
		# a unit writes freely) costs ~1s per 8 files at 40k characters — the
		# single biggest risk to the budget here. UNIT_READ_MAX is far past any
		# terminal width, so a note clipped here could never have rendered.
		IFS='|' read -r -a fld -n "$UNIT_READ_MAX" <"$f"
		# Count fields from the front and tolerate a short tail: `read -a` drops a
		# *trailing* empty field, so an empty note ("write|unit|123|") arrives as
		# three fields and a source-less line as four. Requiring an exact count
		# dropped those units from the board entirely. Six or more (a pipe inside
		# the note) is still skipped: past the fourth separator we cannot tell a
		# note from a field, so guessing would show a truncated note as if it
		# were the whole one.
		[ ${#fld[@]} -ge 3 ] && [ ${#fld[@]} -le 5 ] || continue
		u_st=${fld[0]} u_un=${fld[1]} u_ts=${fld[2]} u_nt=${fld[3]:-} u_sr=${fld[4]:-}
		u_nt=${u_nt%$'\r'} # CRLF-written file
		u_sr=${u_sr%$'\r'}
		# Anything unrecognised is skipped in silence: a half-written or
		# stale file must never cost us a line, let alone the status line.
		case $u_st in
		probe | write | verify | done | landed | fail) ;;
		*) continue ;;
		esac
		[ -n "$u_un" ] || continue
		case $u_ts in
		'' | *[!0-9]*) continue ;;
		esac
		u_el=$((now - u_ts))
		[ "$u_el" -ge 0 ] || u_el=0 # clock skew / file written "later"
		# A closed unit expires in a minute, an open one in an hour: leftovers
		# of a finished run were sitting on the board all session and crowding
		# out the units still running, and an open stage that stopped being
		# refreshed an hour ago is an abandoned run, not live work.
		case $u_st in
		done | landed | fail) u_lim=$UNIT_CLOSED_STALE ;;
		*) u_lim=$UNIT_OPEN_STALE ;;
		esac
		[ "$u_el" -le "$u_lim" ] || continue
		u_grp=0
		case $u_st in
		done)
			u_grp=1
			n_done=$((n_done + 1))
			;;
		landed)
			u_grp=1
			n_landed=$((n_landed + 1))
			;;
		fail) n_fail=$((n_fail + 1)) ;;
		verify) n_verify=$((n_verify + 1)) ;;
		write) n_write=$((n_write + 1)) ;;
		probe) n_probe=$((n_probe + 1)) ;;
		esac
		# Source: "<kind>" or "<kind>@<worktree-name>", kind in main/sub/child.
		# Absent (an older 4-field line) or unrecognised leaves the tag column
		# blank for that unit — never a dropped line.
		u_kd='' u_wt=''
		case $u_sr in
		*@*)
			u_kd=${u_sr%%@*}
			u_wt=${u_sr#*@}
			;;
		*) u_kd=$u_sr ;;
		esac
		case $u_kd in
		main | sub | child) ;;
		*) u_kd='' u_wt='' ;;
		esac
		case $u_wt in # a name we cannot map to a column is no name at all
		*[[:space:]]* | *@*) u_wt='' ;;
		esac
		if [ -n "$u_wt" ] && ! _wt_no "$u_wt"; then
			wt_names+=("$u_wt")
		fi
		# Sort key: live units first, newest first inside each group.
		printf -v u_k '%d%010d' "$u_grp" $((9999999999 - u_ts))
		u_key+=("$u_k")
		u_stage+=("$u_st")
		u_name+=("$u_un")
		u_age+=("$u_el")
		u_note+=("$u_nt")
		u_kind+=("$u_kd")
		u_wtn+=("$u_wt")
	done

	u_n=${#u_key[@]}
	if [ "$u_n" -gt 0 ]; then
		# Insertion sort over indices: a handful of units, no `sort` fork.
		order=()
		for ((i = 0; i < u_n; i++)); do
			order+=("$i")
			for ((j = i; j > 0; j--)); do
				a=${order[j - 1]} b=${order[j]}
				[[ ${u_key[a]} > ${u_key[b]} ]] || break
				order[j - 1]=$b
				order[j]=$a
			done
		done

		# Worktree numbers: alphabetical, 1-based. Assigning them here (and not
		# in the writers) is what keeps a proxy line "sub@a" and its child line
		# "child@a" on the same number, and what keeps that number from moving
		# between two units of the same run.
		wt_n=${#wt_names[@]}
		for ((i = 1; i < wt_n; i++)); do
			for ((j = i; j > 0; j--)); do
				[[ ${wt_names[j - 1]} > ${wt_names[j]} ]] || break
				swap=${wt_names[j - 1]}
				wt_names[j - 1]=${wt_names[j]}
				wt_names[j]=$swap
			done
		done

		cols=${COLUMNS:-0}
		case $cols in
		'' | *[!0-9]*) cols=0 ;;
		esac
		[ "$cols" -ge 40 ] || cols=$UNIT_COLS
		cols=$((cols - 1)) # a line that ends exactly on the last column
		# wraps in some terminals, and a wrap costs us a blank line

		# Leave room for the roll-up line, plus the overflow line when the
		# unit lines do not all fit.
		shown=$u_n
		[ "$shown" -le $((UNIT_MAX - 1)) ] || shown=$((UNIT_MAX - 2))

		# Pass 1 measures the three aligned columns (tag / name / elapsed) so
		# pass 2 can pad them. Widths are per render, so a board whose units
		# carry no source at all keeps its old, tag-less shape.
		l_lab=() l_nam=() l_naw=() l_ela=()
		lab_w=0 nam_w=0 ela_w=0
		for ((k = 0; k < shown; k++)); do
			i=${order[k]}
			_label "${u_kind[i]}" "${u_wtn[i]}"
			l_lab+=("$_R")
			[ ${#_R} -gt "$lab_w" ] && lab_w=${#_R}
			_elapsed "${u_age[i]}"
			l_ela+=("$_R")
			[ ${#_R} -gt "$ela_w" ] && ela_w=${#_R}
			_cut "${u_name[i]}" 0
			l_nam+=("$_T")
			l_naw+=("$_W")
			[ "$_W" -gt "$nam_w" ] && nam_w=$_W
		done

		# "  <glyph> [<tag> ]<name> <말문>  <elapsed>"
		lab_c=0
		[ "$lab_w" -gt 0 ] && lab_c=$((lab_w + 1))
		fixw=$((4 + lab_c + 1 + 4 + 2 + ela_w))
		nam_max=$((cols - fixw))
		[ "$nam_max" -ge 8 ] || nam_max=8
		[ "$nam_w" -le "$nam_max" ] || nam_w=$nam_max

		for ((k = 0; k < shown; k++)); do
			i=${order[k]}
			case ${u_stage[i]} in
			probe) col=$C_PROBE glyph='○' word='파악' ;;
			write) col=$C_WRITE glyph='◑' word='작성' ;;
			verify) col=$C_VERIFY glyph='◕' word='검증' ;;
			done) col=$C_DONE glyph='✓' word='완료' ;;
			landed) col=$C_LANDED glyph='◆' word='반영' ;;
			*) col=$C_FAIL glyph='✗' word='실패' ;;
			esac
			name=${l_nam[k]} naw=${l_naw[k]}
			if [ "$naw" -gt "$nam_w" ]; then
				_cut "$name" "$nam_w"
				name=$_T naw=$_W
			fi
			printf -v napad '%*s' $((nam_w - naw)) ''
			el=${l_ela[k]}
			printf -v elpad '%*s' $((ela_w - ${#el})) ''
			line="  ${col}${glyph}${C_OFF} "
			if [ "$lab_w" -gt 0 ]; then
				printf -v labpad '%*s' $((lab_w - ${#l_lab[k]})) ''
				line+="${C_SEP}${l_lab[k]}${C_OFF}${labpad} "
			fi
			line+="${name}${napad} ${col}${word}${C_OFF}${C_SEP}  ${elpad}${el}${C_OFF}"
			if [ -n "${u_note[i]}" ]; then
				avail=$((cols - fixw - nam_w - 2))
				if [ "$avail" -ge 4 ]; then
					_cut "${u_note[i]}" "$avail"
					line+="${C_SEP}  ${_T}${C_OFF}"
				fi
			fi
			unit_lines+=("$line")
		done

		if [ "$shown" -lt "$u_n" ]; then
			hidden_done=0 hidden_landed=0
			for ((k = shown; k < u_n; k++)); do
				i=${order[k]}
				case ${u_stage[i]} in
				done) hidden_done=$((hidden_done + 1)) ;;
				landed) hidden_landed=$((hidden_landed + 1)) ;;
				esac
			done
			line="  ${C_SEP}+$((u_n - shown))건 더${C_OFF}"
			[ "$hidden_done" -gt 0 ] &&
				line+="${C_SEP} · ${C_OFF}${C_DONE}완료 ${hidden_done}${C_OFF}"
			[ "$hidden_landed" -gt 0 ] &&
				line+="${C_SEP} · ${C_OFF}${C_LANDED}반영 ${hidden_landed}${C_OFF}"
			unit_lines+=("$line")
		fi

		# Roll-up: survives the cap, so the shape of the run stays visible
		# even when most unit lines were folded away.
		roll=''
		_roll() { # <colour> <label> <count>
			[ "$3" -gt 0 ] || return 0
			[ -n "$roll" ] && roll+="${C_SEP} · ${C_OFF}"
			roll+="$1$2 $3${C_OFF}"
		}
		_roll "$C_PROBE" 파악 "$n_probe"
		_roll "$C_WRITE" 작성 "$n_write"
		_roll "$C_VERIFY" 검증 "$n_verify"
		_roll "$C_DONE" 완료 "$n_done"
		_roll "$C_LANDED" 반영 "$n_landed"
		_roll "$C_FAIL" 실패 "$n_fail"
		[ -n "$roll" ] && unit_lines+=("  $roll")
	fi
fi

# --- render ------------------------------------------------------------------
[ ${#parts[@]} -gt 0 ] || parts=("${C_DIR}claude${C_OFF}")

out=''
for p in "${parts[@]}"; do
	[ -n "$out" ] && out+="${C_SEP} | ${C_OFF}"
	out+=$p
done
printf '%s\n' "$out"

for p in "${unit_lines[@]}"; do
	[ -n "$p" ] && printf '%s\n' "$p"
done
