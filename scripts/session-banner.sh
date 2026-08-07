#!/usr/bin/env bash
# SessionStart welcome banner (UI-only, zero model tokens).
# Prints exactly one single-line JSON object to stdout:
#   {"systemMessage":"..."}
# Claude Code shows systemMessage to the USER only; it never enters model
# context. The inline SessionStart marker hook in .claude/settings.json is a
# separate entry (plain stdout -> model context) and must stay untouched.
# systemMessage is PLAIN TEXT: no ANSI color/escape sequences (they render as
# literal \033[... garbage), no markdown — emoji/Unicode symbols only.
#
# SYNC NOTE: the repo-mode detection below lives in THREE places and they
# MUST stay in sync — the inline SessionStart marker hook in
# .claude/settings.json, this script, and the template-original guard in
# scripts/template-update.sh — same signals:
#   1) sentinel file .claude/template-origin (template_origin: / mode: lines)
#   2) `git remote get-url origin` repo-name comparison (owner ignored,
#      trailing slashes and .git stripped)
# If one changes, change the other two.
#
# Modes:
#   TEMPLATE      — repo name matches template_origin, or mode: template
#   SETUP_PENDING — sentinel exists but repo name differs (derived project,
#                   SETUP.md step 1 not done), or mode: project with no remote
#   ASK           — sentinel exists, no remote, no mode: line
#   PROJECT       — no sentinel file (initialized / normal project)
#
# Always exit 0; never block session start.

d=${CLAUDE_PROJECT_DIR:-$PWD}
so="$d/.claude/template-origin"

# Origin repo name — hoisted OUT of the sentinel block only because the
# banner TITLE below needs it in every mode, not just when the sentinel
# exists. This is banner-only plumbing, NOT a change to the synced detection
# signals in the SYNC NOTE above: the parsing is the exact signal-2 logic
# (URL last segment, owner ignored, trailing slashes and .git stripped) and the sentinel
# block below consumes the same value unchanged.
ru=$(git -C "$d" remote get-url origin 2>/dev/null)
while [ "${ru%/}" != "$ru" ]; do ru=${ru%/}; done
rn=${ru##*/}
rn=${rn##*:}
rn=${rn%.git}
# Banner-only extra (NOT part of the synced detection signals above): owner
# segment of the origin URL, for the TEMPLATE-mode `gh repo create` line.
# Handles https://host/owner/repo(.git) and git@host:owner/repo(.git).
ow=${ru%/*}
case "$ru" in
  */*/*) ow=${ow##*/} ;;
  *:*/*) ow=${ow##*:} ;;
  *) ow= ;;
esac

mode=PROJECT
if [ -f "$so" ]; then
  to=$(grep -E '^[[:space:]]*template_origin:' "$so" 2>/dev/null | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '[:space:]')
  md=$(grep -E '^[[:space:]]*mode:' "$so" 2>/dev/null | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '[:space:]')
  if [ -n "$rn" ]; then
    if [ -n "$to" ] && [ "$rn" = "$to" ]; then mode=TEMPLATE; else mode=SETUP_PENDING; fi
  else
    case "$md" in
      template) mode=TEMPLATE ;;
      project) mode=SETUP_PENDING ;;
      *) mode=ASK ;;
    esac
  fi
fi

# Mode block. Single-quoted so \n stays a literal two-char JSON escape.
case "$mode" in
  TEMPLATE)
    # New-project one-liner: owner/repo derived from origin at RUNTIME (never
    # hardcoded). Placeholder fallback when the owner is unparsable (e.g.
    # mode: template with no remote) or either part has characters outside
    # [A-Za-z0-9._-] — the value is embedded into the single-line JSON below,
    # so it must never carry quotes/backslashes/control chars.
    tpl='<소유자>/claude-starter'
    if [ -n "$ow" ] && [ -n "$rn" ]; then
      case "$ow$rn" in
        *[!A-Za-z0-9._-]*) : ;;
        *) tpl=$ow/$rn ;;
      esac
    fi
    mb='🧩 모드: 템플릿 원본 (TEMPLATE-REPO)\n → 템플릿 자체를 수정하는 저장소. /kickoff 대상 아님\n ▸ 새 프로젝트: gh repo create <이름> --private --template '"$tpl"' --clone' ;;
  SETUP_PENDING)
    mb='🚧 모드: 파생 프로젝트 — 초기화 전\n → claude-starter 템플릿에서 생성된 저장소. SETUP.md 1번 초기화부터 진행' ;;
  ASK)
    mb='❓ 모드: 미확정\n → 템플릿/프로젝트 여부를 이번 세션에서 확인함' ;;
  *)
    mb='🚀 모드: 프로젝트 (초기화 완료)\n → 제품을 개발하는 저장소. 새 아이디어는 /kickoff로 시작' ;;
esac

# Banner title — banner-only derivation (NOT a detection signal; see SYNC
# NOTE): origin repo name, else the project directory name (backslashes
# normalized first so basename also works on Windows-form paths). Same
# single-line-JSON safety rule as tpl above: anything outside [A-Za-z0-9._-]
# falls back to a fixed title, so quotes/backslashes/control chars can never
# reach the JSON below.
title=$rn
[ -n "$title" ] || title=$(basename "${d//\\//}")
case "$title" in
  ''|*[!A-Za-z0-9._-]*) title='Claude Code' ;;
esac

# Next actions — the first 2 non-empty lines under the "## 다음 할 일" heading
# of docs/STATE.md (the heading /handoff writes). This took over the slot of
# the old /release advert line: the answer to "다음 작업 뭐야?" already sat in
# STATE.md, but nothing surfaced it at session start. A missing file or a
# missing section leaves this empty and the whole block is dropped silently —
# the banner must never fail a session start.
# JSON safety, same rule as tpl/title above: backslashes, double quotes and
# control chars are stripped before the text can reach the single-line JSON.
# Truncation is byte-based under LC_ALL=C (the hook's locale is not guaranteed
# to be UTF-8); a cut line then loses its whole trailing multibyte sequence, so
# a half-written character can never land in the output.
st="$d/docs/STATE.md"
nx=
if [ -f "$st" ]; then
  while IFS= read -r ln; do
    [ -n "$ln" ] || continue
    cl=$(printf '%s' "$ln" | LC_ALL=C cut -b1-90)
    if [ "$cl" != "$ln" ]; then
      cl=$(printf '%s' "$cl" | LC_ALL=C sed -E 's/[\xC0-\xFF][\x80-\xBF]*$//')'…'
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
msg=$msg'📖 사용법\n'
msg=$msg' ▸ 메인 세션은 오케스트레이터 — 직접 구현하지 않고 서브에이전트에 위임\n'
msg=$msg' ▸ /kickoff  새 프로젝트 기획 (아이디어→목표→목업→이슈)\n'
msg=$msg' ▸ /land  작업 단위 마무리 (머지→워크트리 정리→이슈 닫기→push 일괄)\n'
msg=$msg' ▸ /handoff  작업 상태를 docs/STATE.md에 저장 (세션 인수인계)\n'
msg=$msg' ▸ /cheatsheet  자주 잊는 git·gh·템플릿 명령 조회 카드\n'
msg=$msg' ▸ /template-update  파생 프로젝트를 템플릿 최신 장치에 동기화\n\n'
msg=$msg$nx
msg=$msg'⚡ 효과\n'
msg=$msg' ▸ rtk가 셸 출력을 최대 90% 압축해 토큰 절약\n'
msg=$msg' ▸ 탐색·구현을 서브에이전트로 격리해 메인 컨텍스트 보호\n'
msg=$msg' ▸ STATE/MAP/GOTCHAS 지식 파일로 세션 간 문맥 유지\n'
msg=$msg'───────────────────────────────────────────'

# %s does not reinterpret backslashes or % in the argument, so the \n escapes
# and "90%" pass through into the JSON literally. No jq dependency.
printf '{"systemMessage":"%s"}\n' "$msg"
exit 0
