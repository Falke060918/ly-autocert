#!/usr/bin/env bash
# SessionStart hook (matcher: compact) — right after a compaction, point the
# session at the snapshot scripts/compact-snapshot.sh left behind, AND read the
# repository state again, live, right now.
#
# Why both: the snapshot is a photo taken just before compaction, so it goes
# stale the moment anything moves; the live read is current but remembers
# nothing about what was in flight. Printed side by side, a disagreement
# between the two is itself the signal (a commit landed, a file changed, a
# branch moved since the photo).
#
# SessionStart stdout enters the model context, so this prints a few lines and
# NOT the snapshot itself: dumping the whole file would spend context to save
# context. The file is read only if it is actually needed. For the same reason
# the whole output is kept under MAX bytes — the pointer block alone is ~400
# bytes, and the live block gets roughly that much again. The budget is
# enforced mechanically (render at decreasing detail until it fits), because a
# byte budget cannot be eyeballed: one Korean character is 3 UTF-8 bytes.
#
# Degrading must never be silent: whatever the budget costs us is announced in
# the output itself (the "예산 초과로 축약" line), counted, and counted inside
# the budget — otherwise the reader hunts for information that was dropped
# without a trace. Lines the body already announces (the "… 외 N건" tail) are
# not re-counted there.
#
# No snapshot file: print nothing. Always exit 0 - never block session start.

d=${CLAUDE_PROJECT_DIR:-$PWD}
root=$(git -C "$d" rev-parse --show-toplevel 2>/dev/null)
if [ -n "$root" ]; then isrepo=1; else isrepo=0; root=$d; fi

snap="$root/.claude/compact-snapshot.md"
[ -f "$snap" ] || exit 0

MAX=1000                            # total stdout budget in bytes

g() { git -C "$root" "$@" 2>/dev/null; }

# --- live state (read now, not from the snapshot) ---------------------------
branch=; headsha=; log3=; st=; strc=0; nfiles=0; ncommits=0; up=
if [ "$isrepo" -eq 1 ]; then
  branch=$(g rev-parse --abbrev-ref HEAD)
  headsha=$(g rev-parse --short HEAD)
  log3=$(g log --oneline -3)
  [ -n "$log3" ] && ncommits=$(printf '%s\n' "$log3" | grep -c .)
  # rc, never emptiness: outside a git repo status exits 128 with empty stdout,
  # and reading that as "clean" turns a failure into a false all-clear
  # (.claude/rules/shell-traps.md).
  st=$(g status --porcelain); strc=$?
  [ "$strc" -eq 0 ] && nfiles=$(printf '%s\n' "$st" | grep -c .)
  if g rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1; then
    up="미push $(g log --oneline '@{u}..HEAD' | grep -c .)건"
  else
    up="upstream 없음"
  fi
fi

# byte length under C semantics; a subshell so the locale switch never leaks
blen() ( LC_ALL=C; s=$1; printf '%s' "${#s}" )

# $1 = how many recent commits to list, $2 = how many changed files to list
render() {
  nc=$1; nfs=$2
  echo "🗜 압축 직후 — 압축 직전 기계적 스냅샷: $snap"
  echo " ▸ 이어서 작업하기 전에 이 파일을 읽고 끊긴 지점을 복구한다 (전문은 주입하지 않았다)"
  echo " ▸ 대조할 것: 미push 커밋 · 워크트리 고아 커밋 · 판정이 fail이거나 없는 단위 · TaskList의 미착수 예고"
  echo " ▸ 아래는 스냅샷이 아니라 지금 다시 읽은 라이브 상태다 — 둘이 어긋나면 그 차이가 정보다"
  if [ "$isrepo" -ne 1 ]; then
    echo "   (git 저장소가 아니다 — 라이브 상태 없음)"
    return 0
  fi
  echo "   현재: ${branch:-?} @ ${headsha:-?} · $up"
  # dc / dfs: what this level drops WITHOUT the body saying so. The file list's
  # own "… 외 N건" tail already says it, so it is not counted again here.
  dc=0; dfs=0
  if [ "$nc" -gt 0 ] && [ -n "$log3" ]; then
    printf '%s\n' "$log3" | head -n "$nc" | sed 's/^/   · /'
  fi
  [ "$nc" -lt "$ncommits" ] && dc=$((ncommits - nc))
  if [ "$strc" -ne 0 ]; then
    echo "   변경 파일: git status 실패 rc=$strc — 워킹트리 상태 불명"
  elif [ "$nfiles" -eq 0 ]; then
    echo "   변경 파일: 없음 (클린)"
  else
    echo "   변경 파일 ${nfiles}건:"
    if [ "$nfs" -gt 0 ]; then
      printf '%s\n' "$st" | head -n "$nfs" | sed 's/^/   · /'
      rest=$((nfiles - nfs))
      [ "$rest" -gt 0 ] && echo "   · … 외 ${rest}건"
    else
      dfs=$nfiles
    fi
  fi
  # Announce the cut, in the output, inside the budget.
  omit=
  [ "$dc" -gt 0 ] && omit="커밋 ${dc}건"
  if [ "$dfs" -gt 0 ]; then
    [ -n "$omit" ] && omit="$omit · "
    omit="${omit}변경 파일 ${dfs}건"
  fi
  if [ -n "$omit" ]; then
    echo "   (예산 초과로 축약 — ${omit} 생략)"
  fi
}

# Degrade until it fits the budget; the last level is the floor and prints
# even if it is still over (a truncated pointer would be worse than none).
out=
for lv in "3 6" "3 3" "2 2" "1 0" "0 0"; do
  # shellcheck disable=SC2086
  set -- $lv
  out=$(render "$1" "$2")
  [ "$(blen "$out")" -le "$MAX" ] && break
done
printf '%s\n' "$out"
exit 0
