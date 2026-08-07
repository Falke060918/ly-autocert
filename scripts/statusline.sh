#!/usr/bin/env bash
# Claude Code statusLine renderer.
#
# Reads the session JSON that Claude Code writes to stdin and prints exactly one
# line: model | directory | git branch | rtk token savings.
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
C_SEP=$'\033[90m'    # grey
C_OFF=$'\033[0m'

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

# --- render ------------------------------------------------------------------
[ ${#parts[@]} -gt 0 ] || parts=("${C_DIR}claude${C_OFF}")

out=''
for p in "${parts[@]}"; do
	[ -n "$out" ] && out+="${C_SEP} | ${C_OFF}"
	out+=$p
done
printf '%s\n' "$out"
