#!/usr/bin/env bash
# SessionStart welcome banner (UI-only, zero model tokens — one exception:
# the Orca markers (ORCA-ENV / ORCA-CHILD) at the bottom ride along as
# additionalContext).
# Prints exactly one single-line JSON object to stdout:
#   {"systemMessage":"..."}
# Claude Code shows systemMessage to the USER only; it never enters model
# context; hookSpecificOutput.additionalContext DOES enter model context. The
# session-markers.sh SessionStart hook is a separate entry (plain stdout ->
# model context) and must stay untouched.
# systemMessage is PLAIN TEXT: no ANSI color/escape sequences (they render as
# literal \033[... garbage), no markdown — emoji/Unicode symbols only.
#
# SYNC NOTE: the mode detection below is a REPLICA of the marker logic in
# scripts/session-markers.sh (single source of the decision: .claude/identity
# declaration + origin fingerprint). The val()/norm() helpers and the
# case-by-case decision MUST stay in sync with that file — if one changes,
# change the other. Mapping:
#   TEMPLATE         <-> marker "TEMPLATE-REPO"
#   SETUP_PENDING    <-> marker "PROJECT-REPO SETUP-PENDING"
#   PROJECT          <-> no marker (identity: project + setup: done)
#   IDENTITY_MISSING <-> marker "IDENTITY-MISSING"
#   CLONE-SUSPECT warning line <-> marker suffix "CLONE-SUSPECT" (TEMPLATE only)
#
# Always exit 0; never block session start.

d=${CLAUDE_PROJECT_DIR:-$PWD}
idf="$d/.claude/identity"

# Origin repo URL/name — hoisted up here only because the banner TITLE and the
# TEMPLATE-mode `gh repo create` line need it in every mode. This is banner
# plumbing; the fingerprint comparison below feeds the SAME raw URL through
# norm() exactly as session-markers.sh does.
ru=$(git -C "$d" remote get-url origin 2>/dev/null)
rw=$ru
while [ "${rw%/}" != "$rw" ]; do rw=${rw%/}; done
rn=${rw##*/}
rn=${rn##*:}
rn=${rn%.git}
# Banner-only extra: owner segment of the origin URL, for the TEMPLATE-mode
# `gh repo create` line. Handles https://host/owner/repo(.git) and
# git@host:owner/repo(.git).
ow=${rw%/*}
case "$rw" in
  */*/*) ow=${ow##*/} ;;
  *:*/*) ow=${ow##*:} ;;
  *) ow= ;;
esac

# --- replicated from session-markers.sh (see SYNC NOTE above) ---------------
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

# Fresh-clone heuristic → CLONE-SUSPECT (empty otherwise): exactly one reflog
# entry and that entry is `clone: from <url>`. Notice only — it never changes
# the mode, and never feeds the SETUP_PENDING decision.
clone_suspect() {
  rl=$(git -C "$d" reflog --format='%gs' -2 2>/dev/null)
  [ "$(printf '%s' "$rl" | awk 'END { print NR + 0 }')" = 1 ] || return 0
  case "$rl" in "clone: from "*) printf 'CLONE-SUSPECT' ;; esac
}

mode=IDENTITY_MISSING
if [ -f "$idf" ]; then
  idv=$(val identity)
  case "$idv" in
    template)
      rec=$(val origin)
      [ -n "$rec" ] || rec=none          # legacy identity without origin: key
      [ "$rec" = none ] || rec=$(norm "$rec")
      act=$ru
      if [ -n "$act" ]; then act=$(norm "$act"); else act=none; fi
      if [ "$act" = "$rec" ]; then mode=TEMPLATE; else mode=SETUP_PENDING; fi
      ;;
    project)
      case "$(val setup)" in
        done) mode=PROJECT ;;
        *) mode=SETUP_PENDING ;;
      esac
      ;;
    *) mode=IDENTITY_MISSING ;;
  esac
fi
# --- end replicated block ---------------------------------------------------

# Mode block. Single-quoted so \n stays a literal two-char JSON escape.
case "$mode" in
  TEMPLATE)
    # New-project one-liner: owner/repo derived from origin at RUNTIME (never
    # hardcoded). Placeholder fallback when the owner is unparsable or either
    # part has characters outside [A-Za-z0-9._-] — the value is embedded into
    # the single-line JSON below, so it must never carry quotes/backslashes/
    # control chars.
    tpl='<소유자>/claude-starter-v2'
    if [ -n "$ow" ] && [ -n "$rn" ]; then
      case "$ow$rn" in
        *[!A-Za-z0-9._-]*) : ;;
        *) tpl=$ow/$rn ;;
      esac
    fi
    mb='🧩 모드: 템플릿 원본 (TEMPLATE-REPO)\n → 템플릿 기계 자체를 수정하는 저장소. kickoff류 제품 기획 비활성 · /harvest 수신 측\n ▸ 새 프로젝트: gh repo create <이름> --private --template '"$tpl"' --clone' ;;
  SETUP_PENDING)
    mb='🚧 모드: 파생 프로젝트 — 초기화 전 (SETUP-PENDING)\n → 묻지 않고 자동 실행 — 에이전트가 SETUP.md를 런북 삼아 8단계를 직접 실행한다\n ▸ 아무 메시지나 보내면 초기화가 시작됩니다 — 별도 명령은 필요 없다\n ▸ 되돌리려면: 완료 보고의 스냅샷 해시로 reset + identity·remote 복원 (SETUP.md 2-0)' ;;
  IDENTITY_MISSING)
    mb='❓ 모드: 신분 미확정 (IDENTITY-MISSING)\n → .claude/identity 부재·무효 — 세션 시작에 1회 질문해 생성한다 (추정으로 채우지 않음)' ;;
  *)
    mb='🚀 모드: 프로젝트 (초기화 완료)\n → 제품을 개발하는 저장소. 새 아이디어는 /kickoff → /architect → 구현 → /land' ;;
esac

# CLONE-SUSPECT notice — the TEMPLATE-mode counterpart of the marker suffix. A
# plain `git clone` of the PUBLISHED template keeps the template URL as origin,
# so the fingerprint still matches and this reads as the template original: the
# derived project never gets its automatic setup, and nothing says so. This line
# is the whole remedy — it informs, and changes no behaviour anywhere.
cw=
if [ "$mode" = TEMPLATE ] && [ -n "$(clone_suspect)" ]; then
  cw='⚠ CLONE-SUSPECT: git clone 직후로 보인다. 파생 프로젝트로 쓸 거라면 README.md의 초기화 절차(.git 버리고 git init)를 먼저 밟는다 — 안 밟으면 지문이 템플릿과 같아 계속 템플릿 원본으로 뜬다. 템플릿 자체를 고치러 왔다면 무시해도 된다.\n\n'
fi

# ARCHITECT-PENDING safety net — kickoff finished (docs/02-goals.md exists) but
# the tech-design gate has not run (/architect writes docs/03-architecture.md).
# Cross-session backstop: /architect otherwise only surfaces in the
# kickoff-closing turn, so once that turn scrolls off or /clear runs there is
# nothing left to catch "mockup approved but tech design skipped". Fires only in
# PROJECT mode (a real, initialized product repo) — never in the template
# original (TEMPLATE) or a not-yet-initialized derived project (SETUP_PENDING).
ap=
if [ "$mode" = PROJECT ] && [ -f "$d/docs/02-goals.md" ] && [ ! -f "$d/docs/03-architecture.md" ]; then
  ap='⚠ ARCHITECT-PENDING: 목업까지 승인됐지만 기술설계(/architect)가 아직이다. 구현 전에 /architect를 먼저 진행한다.\n\n'
fi

# STATE-STALE nudge — docs/STATE.md only changes when /handoff runs, so commits
# pile up behind it silently and it starts describing a tree that no longer
# exists (measured 2026-08-27: three consecutive wrong commit records in one
# session). Unlike the rest of the banner this needs no conversation context:
# "how many commits landed since STATE.md was last committed" is purely
# mechanical, which is exactly what a hook can decide.
# Threshold 5: a normal work unit commits fewer than that before /handoff, so a
# banner that fires every session — and therefore gets ignored — is avoided.
# Everything unexpected stays silent by construction: outside a git repo, with
# no docs/STATE.md, or with STATE.md never committed, `git log` prints nothing
# and the block is skipped; if the range itself is unusable, `wc -l` counts 0.
# The rc of the pipeline is never consulted (it would be `wc`'s, not git's) —
# the numeric guard on the count is the only test.
sn=
sl=$(git -C "$d" log -1 --format=%H -- docs/STATE.md 2>/dev/null)
if [ -n "$sl" ]; then
  sb=$(git -C "$d" log --format=%H "$sl..HEAD" 2>/dev/null | wc -l | tr -d '[:space:]')
  case "$sb" in
    ''|*[!0-9]*) : ;;
    *)
      if [ "$sb" -ge 5 ]; then
        sn='⚠ STATE 낡음: docs/STATE.md가 커밋 '"$sb"'개 뒤처짐 — /handoff 권장\n\n'
      fi
      ;;
  esac
fi

# Banner title — banner-only derivation (NOT a detection signal): origin repo
# name, else the project directory name (backslashes normalized first so
# basename also works on Windows-form paths). Same single-line-JSON safety rule
# as tpl above: anything outside [A-Za-z0-9._-] falls back to a fixed title, so
# quotes/backslashes/control chars can never reach the JSON below.
title=$rn
[ -n "$title" ] || title=$(basename "${d//\\//}")
case "$title" in
  ''|*[!A-Za-z0-9._-]*) title='Claude Code' ;;
esac

# Next actions — the first 2 non-empty lines under the "## 다음 할 일" heading
# of docs/STATE.md (prefix match: the v2 heading is "## 다음 할 일 (우선순위
# 순)" and /handoff may vary the suffix). A missing file or a missing section
# leaves this empty and the whole block is dropped silently — the banner must
# never fail a session start.
# JSON safety, same rule as tpl/title above: backslashes, double quotes and
# control chars are stripped before the text can reach the single-line JSON.
# Truncation is byte-based under LC_ALL=C (the hook's locale is not guaranteed
# to be UTF-8); a cut line then loses its whole trailing multibyte sequence, so
# a half-written character can never land in the output. The byte ranges are
# built with printf octal escapes because BSD sed (macOS) does not understand
# \xNN inside bracket expressions (GNU sed does — v1 relied on that).
mb_lead=$(printf '\300')  # 0xC0 — first UTF-8 lead byte
mb_hi=$(printf '\377')    # 0xFF
mb_c1=$(printf '\200')    # 0x80 — first continuation byte
mb_c2=$(printf '\277')    # 0xBF
st="$d/docs/STATE.md"
nx=
if [ -f "$st" ]; then
  while IFS= read -r ln; do
    [ -n "$ln" ] || continue
    cl=$(printf '%s' "$ln" | LC_ALL=C cut -b1-90)
    if [ "$cl" != "$ln" ]; then
      cl=$(printf '%s' "$cl" | LC_ALL=C sed -E "s/[$mb_lead-$mb_hi][$mb_c1-$mb_c2]*\$//")'…'
    fi
    nx=$nx' ▸ '"$cl"'\n'
  done <<EOF
$(awk '/^##[[:space:]]*다음 할 일/{f=1;next} f&&/^##/{exit} f&&NF{print; if(++n==2) exit}' "$st" 2>/dev/null | sed -e 's/[\\"]//g' -e 's/\*\*//g' -e 's/`//g' -e 's/[[:cntrl:]]//g')
EOF
fi
[ -z "$nx" ] || nx='📌 다음 할 일 (docs/STATE.md)\n'"$nx"'\n'

# Layout: top/bottom rule lines only, no right-edge borders (CJK double-width
# characters break box alignment). Plain text, no markdown.
msg='── ✦ '"$title"' ✦ ─────────────────────\n'
msg=$msg$mb'\n\n'
msg=$msg$cw
msg=$msg$ap
msg=$msg$sn
msg=$msg'📖 사용법 — 아래 스킬은 상황에 맞으면 에이전트가 자동 실행한다. 직접 입력은 선택\n'
msg=$msg' ▸ 메인 세션은 오케스트레이터 — 직접 구현하지 않고 서브에이전트에 위임\n'
msg=$msg' ▸ /clarify  요청을 스펙으로 — [확인 필요] 0이 될 때까지 소거 후 위임\n'
msg=$msg' ▸ /kickoff  새 프로젝트 기획 (아이디어→목표→목업 승인)\n'
msg=$msg' ▸ /architect  기술설계 게이트 (대안 비교→기술계획 승인→스캐폴딩)\n'
msg=$msg' ▸ /land  작업 단위 마무리 (머지→워크트리 정리→이슈 닫기→push 일괄)\n'
msg=$msg' ▸ /handoff  작업 상태를 docs/STATE.md에 저장 (세션 인수인계)\n'
msg=$msg' ▸ /recall  과거 세션 대화 검색 (지난 세션에서 뭐였지)\n'
msg=$msg' ▸ /learn  docs/learn/ 수신함의 학습 후보를 승인 반영\n'
msg=$msg' ▸ /perf  세션 병목 리포트 (구간별 소요 시간 분석)\n'
msg=$msg' ▸ /harvest  파생 프로젝트의 교훈을 템플릿에 환류 (draft PR)\n'
msg=$msg' ▸ /template-update  파생 프로젝트를 템플릿 최신 장치에 동기화\n\n'
msg=$msg$nx
msg=$msg'⚡ 적용된 장치와 효과\n'
msg=$msg' ▸ rtk — 셸 출력 최대 90% 압축, 토큰 절약\n'
msg=$msg' ▸ 서브에이전트 — 탐색은 따로, 대화 가볍게\n'
msg=$msg' ▸ 기억 3파일 — 세션이 바뀌어도 문맥 유지\n'
msg=$msg' ▸ 세션 검색 — 지난 대화를 키워드로 찾기\n'
msg=$msg' ▸ 넛지 훅 — 툴 10회마다 기록 상기\n'
msg=$msg' ▸ 학습 회수 — 종료 후 놓친 교훈 자동 적재\n'
msg=$msg' ▸ 검증 판정 — 탈락 사유가 재작업에 첨부\n'
msg=$msg' ▸ 모호점 게이트 — 안 정해지면 구현 차단\n'
msg=$msg' ▸ 스킬 정리 — 사용 기록 자동 적재, 보관은 수동 sweep\n'
msg=$msg' ▸ 위임 계측 — 위임별 걸린 시간 기록\n'
msg=$msg'───────────────────────────────────────────'

# Orca markers — model context, not banner UI. additionalContext is the
# script-side channel into model context. Shared Orca signals: env
# (ORCA_TERMINAL_HANDLE or TERM_PROGRAM=Orca) AND the orca CLI on PATH. The
# git-dir vs git-common-dir comparison then splits child from parent (they are
# equal only in the main checkout).
#   ORCA-CHILD (linked worktree): an Orca child worktree is a full repo copy,
#   so the child Claude also sees the orchestrator rules (CLAUDE.md,
#   .claude/rules/orca.md), believes it must delegate too, and can spawn a
#   grandchild worktree chain (v1 measured: 4-deep). This marker overrides any
#   ORCA-ENV context if that also appears — the branches below are exclusive,
#   so a child session emits ORCA-CHILD only.
#   ORCA-ENV (main checkout): the parent session inside the Orca ADE. The
#   marker switches delegation defaults to Orca child agents (one worktree +
#   branch + terminal card per unit — .claude/rules/orca.md). Emitted here
#   because nothing else detects the environment.
#   ORCA-APP-* (ORCA-ENV only): the measured app-connection state at session
#   start. It is a NOTICE, not a dispatch decision — a snapshot goes stale the
#   moment the app is started or quit, so the pre-dispatch check stays
#   mandatory. That check and the fallback conditions live in
#   .claude/rules/orca.md section 0 (single source).
# Text is ASCII with no quotes/backslashes/control chars — safe in the
# single-line JSON below.

# App-connection probe. This hook runs OUTSIDE the Bash tool sandbox, so unlike
# a sandboxed `orca status` it reaches the app's loopback for real (measured
# 2026-09-05: sandboxed curl to the Orca port dies rc=7 while this hook's POST
# to the same port lands). `orca status --json` exits 0 even when it cannot
# reach the app, so the JSON body decides, never the rc — and a doubtful read
# prints UNKNOWN, never UNREACHABLE: reporting a running app as off is the one
# failure this probe exists to prevent.
orca_app_state() {
  f=$(mktemp "${TMPDIR:-/tmp}/orca-status.XXXXXX" 2>/dev/null) || return 0
  # Watchdog instead of `timeout` (macOS ships no coreutils timeout): cap the
  # session-start cost at ~2s. Measured normal path: 0.15-0.19s.
  orca status --json >"$f" 2>/dev/null </dev/null &
  p=$!
  n=0
  while kill -0 "$p" 2>/dev/null && [ "$n" -lt 20 ]; do
    sleep 0.1
    n=$((n + 1))
  done
  kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null
  wait "$p" 2>/dev/null
  cs=$(sed -n 's/.*"connectionState"[[:space:]]*:[[:space:]]*"\([A-Za-z_]*\)".*/\1/p' "$f" 2>/dev/null | head -1)
  rm -f "$f" 2>/dev/null
  case "$cs" in
    connected) printf 'CONNECTED' ;;
    '') : ;;
    *) printf 'UNREACHABLE' ;;
  esac
}

oc=
if { [ -n "$ORCA_TERMINAL_HANDLE" ] || [ "$TERM_PROGRAM" = "Orca" ]; } && command -v orca >/dev/null 2>&1; then
  gd=$(git -C "$d" rev-parse --git-dir 2>/dev/null)
  gc=$(git -C "$d" rev-parse --git-common-dir 2>/dev/null)
  if [ -n "$gd" ] && [ "$gd" != "$gc" ]; then
    oc='ORCA-CHILD: this session runs inside an Orca child worktree. You are the implementer for this unit — implement directly, never create further Orca worktrees or spawn delegation chains. This overrides ORCA-ENV if it also appears.'
  elif [ -n "$gd" ] && [ "$gd" = "$gc" ]; then
    # Probed in the parent branch only: a child does not dispatch, so the check
    # would buy it nothing and only slow its start.
    case "$(orca_app_state)" in
      CONNECTED) oa=' ORCA-APP-CONNECTED: at session start the orca CLI reached the app.' ;;
      UNREACHABLE) oa=' ORCA-APP-UNREACHABLE: at session start the orca CLI could not reach the app.' ;;
      *) oa=' ORCA-APP-UNKNOWN: the app connection check returned nothing usable at session start.' ;;
    esac
    oa=$oa' A start-time snapshot, not a dispatch decision - section 0 of that file still decides.'
    oc='ORCA-ENV: this session runs in the Orca ADE main checkout. Delegation units default to Orca child agents (one worktree + branch + terminal card per unit) — read .claude/rules/orca.md before dispatching. ORCA-CHILD overrides this if it also appears.'"$oa"
  fi
fi

# %s does not reinterpret backslashes or % in the argument, so the \n escapes
# and "90%" pass through into the JSON literally. No jq dependency.
if [ -n "$oc" ]; then
  printf '{"systemMessage":"%s","hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"%s"}}\n' "$msg" "$oc"
else
  printf '{"systemMessage":"%s"}\n' "$msg"
fi
exit 0
