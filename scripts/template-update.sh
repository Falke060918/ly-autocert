#!/usr/bin/env bash
# Template updater — run inside a project DERIVED from claude-starter, never
# in the template original. Pulls the latest template-owned machinery
# (CLAUDE.md, scripts, agents, skills, settings) from the template repo
# without touching project-owned files (docs/, README.md, product code).
#
# Usage: bash scripts/template-update.sh [템플릿 저장소 URL]
#   - uses the git remote named `template` if it exists
#   - else registers it from the URL argument (one-time; omit afterwards)
#   - else exits with a usage message
#
# The script never runs `git add`/`git commit` — it only checks paths out of
# template/main (which places them in worktree+index); the caller reviews the
# result and commits.
#
# Paths a derived project commonly customizes (CLAUDE.md, .claude/settings.json)
# are copied into .template-update-backup/<timestamp>/ and their incoming diff
# is printed BEFORE anything is overwritten — the sync still takes the newest
# template rules, the backup only guarantees a way back.

# Template-owned paths to sync. Directory entries rely on checkout
# semantics: `git checkout template/main -- <dir>` overwrites only files that
# EXIST in template/main, so files the project added under the same directory
# (its own skills/scripts/agents) are left alone — the same reason `scripts`
# and `.claude/agents` are listed whole. New template skills therefore
# propagate automatically through `.claude/skills`; only a NEW TOP-LEVEL path
# needs adding here. Never widen this to `.claude` wholesale — that would
# resurrect the template-origin sentinel (see HARD EXCLUSIONS below).
paths=(
  CLAUDE.md
  SETUP.md
  scripts
  .claude/agents
  .claude/skills
  .claude/output-styles
  .claude/conventions
  .claude/settings.json
)

# Synced paths a derived project routinely edits — project rules appended to
# the bottom of CLAUDE.md, extra permissions in settings.json. Checkout
# replaces them wholesale, so each one is backed up and diffed before the
# sync touches it.
# KEEP IN SYNC WITH `paths` ABOVE: an entry here that is no longer synced
# would be backed up and diffed although nothing overwrites it.
customizable=(
  CLAUDE.md
  .claude/settings.json
)
backup_root=.template-update-backup

# !! HARD EXCLUSIONS — NEVER add these to the sync list above !!
#   .claude/template-origin      initialized derived projects deleted this
#                                sentinel in SETUP step 1; resurrecting it
#                                flips them back to SETUP_PENDING mode at
#                                every session start.
#   docs/                        STATE/MAP/GOTCHAS/plans are project-owned.
#   README.md                    project-owned (rewritten for the product).
#   .claude/settings.local.json  local customizations — never touched; this
#                                is exactly where users keep settings that
#                                must survive template updates.
#   .github/workflows/ci.yml     project CI — SETUP step 8 has products add
#                                app build/test jobs to this very file, and
#                                syncing it silently deleted them in a real
#                                derived project. Derivations still get the
#                                initial copy at template-clone time; after
#                                that it is theirs.
#   .mcp.json                    same failure mode as ci.yml — projects add
#                                their own MCP servers (internal API, DB, …)
#                                to this file, and a wholesale sync would
#                                silently drop them. The template copy is a
#                                one-time starting point at clone time; after
#                                that MCP servers are project-owned.
#   any product code             obviously project-owned.

# Keeps the backup directory out of `git status`. It goes into the repo's
# LOCAL exclude file, not .gitignore: .gitignore is project-owned and the
# added line would ride along in the user's next commit. Untracked-and-not-
# ignored would be worse than noise — the NEXT run's dirty-tree check would
# abort on leftover backups.
# --git-common-dir (not --git-dir) so this also works from inside a git
# worktree, where .git is a file and info/ lives in the main repo's git dir.
exclude_backup_dir() {
  gitdir=$(git rev-parse --git-common-dir 2>/dev/null) || gitdir=$(git rev-parse --git-dir 2>/dev/null) || return 0
  [ -n "$gitdir" ] || return 0
  ex="$gitdir/info/exclude"
  if [ -f "$ex" ] && grep -qF "/$backup_root/" "$ex"; then return 0; fi
  mkdir -p "$gitdir/info" 2>/dev/null || return 0
  {
    echo ""
    echo "# template-update.sh: 덮어쓰기 직전 백업 (지워도 무방)"
    echo "/$backup_root/"
  } >> "$ex" 2>/dev/null || {
    echo "알림: $backup_root/ 을 git 무시 목록에 넣지 못했다 — 커밋에 섞이지 않게 직접 확인하라." >&2
    return 0
  }
}

main() {
  root=$(git rev-parse --show-toplevel 2>/dev/null) || {
    echo "중단: git 저장소가 아니다. 파생 프로젝트 안에서 실행하라." >&2
    exit 1
  }
  cd "$root" || exit 1

  # -- Abort: never run inside the template ORIGINAL itself. -----------------
  # Checked before everything else (including the dirty check) so the
  # original always gets the accurate message and nothing is mutated there.
  # SYNC NOTE: the sentinel parsing below reuses the repo-mode detection from
  # scripts/session-banner.sh (and the inline SessionStart marker hook in
  # .claude/settings.json) — same signals: template_origin:/mode: lines +
  # origin repo-name comparison (owner ignored, trailing slashes and .git
  # stripped).
  # If one side changes, change the other.
  so="$root/.claude/template-origin"
  if [ -f "$so" ]; then
    to=$(grep -E '^[[:space:]]*template_origin:' "$so" 2>/dev/null | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '[:space:]')
    md=$(grep -E '^[[:space:]]*mode:' "$so" 2>/dev/null | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '[:space:]')
    ru=$(git remote get-url origin 2>/dev/null)
    while [ "${ru%/}" != "$ru" ]; do ru=${ru%/}; done
    rn=${ru##*/}
    rn=${rn##*:}
    rn=${rn%.git}
    tmpl=0
    if [ -n "$rn" ]; then
      if [ -n "$to" ] && [ "$rn" = "$to" ]; then tmpl=1; fi
    elif [ "$md" = "template" ]; then
      tmpl=1
    fi
    if [ "$tmpl" = 1 ]; then
      echo "중단: 여기는 템플릿 원본이다. 이 스크립트는 템플릿으로 만든 파생 프로젝트 안에서 실행한다." >&2
      exit 1
    fi
  fi

  # -- Abort: dirty working tree — checkout would silently clobber. ----------
  if [ -n "$(git status --porcelain)" ]; then
    echo "중단: 커밋 안 된 변경이 있다. checkout이 조용히 덮어쓰므로 먼저 commit 또는 stash 하고 다시 실행하라." >&2
    exit 1
  fi

  # -- Template remote discovery. --------------------------------------------
  if turl=$(git remote get-url template 2>/dev/null); then
    if [ -n "$1" ]; then
      echo "알림: git remote 'template'이 이미 등록되어 있어 URL 인자는 무시한다 — 사용 중: $turl"
      echo "  바꾸려면: git remote set-url template <새 URL>"
    fi
  else
    if [ -n "$1" ]; then
      git remote add template "$1" || exit 1
    else
      echo "사용법: bash scripts/template-update.sh <템플릿 저장소 URL>" >&2
      echo "git remote 'template'이 없어 템플릿 위치를 모른다. URL을 한 번 넘기면 remote로 등록되고 다음부터는 인자 없이 실행한다." >&2
      exit 1
    fi
  fi

  # --no-tags: the template's release tags must not leak into the derived
  # project's tag namespace (tags there belong to the project's /release).
  git fetch --no-tags template || exit 1

  # Abort: template/main must resolve. With a wrong URL or a template whose
  # default branch is not `main`, every checkout below would "skip" and the
  # script would print a false success with an empty SHA.
  sha=$(git rev-parse -q --verify --short template/main) || {
    echo "중단: template remote에서 main 브랜치를 찾지 못했다 (template/main 확인 실패)." >&2
    echo "  URL이 템플릿 저장소가 아니거나 기본 브랜치가 main이 아니다. 현재 URL: $(git remote get-url template)" >&2
    exit 1
  }

  # -- Backup + diff BEFORE any overwrite. -----------------------------------
  # The working tree is clean at this point (checked above), so HEAD is the
  # project's current copy and a commit-to-commit diff shows exactly what the
  # checkout is about to do. NOT the `HEAD:$p template/main:$p` rev:path form
  # — MSYS mangles rev:path arguments whose path contains a slash (see the
  # note on the checkout loop below); a pathspec after `--` is left alone.
  backup_dir=""
  backed_up=()
  diff_cap=200
  # "was overwritten with different content", as opposed to settings_synced /
  # claude_md_synced below which only mean "checkout ran". The warnings key
  # off these so an identical file does not produce a "복원하라" notice.
  settings_changed=0
  claude_md_changed=0
  for p in "${customizable[@]}"; do
    [ -f "$root/$p" ] || continue
    [ -n "$(git ls-tree --name-only template/main -- "$p")" ] || continue
    if [ -z "$(git diff --name-only HEAD template/main -- "$p")" ]; then
      echo "동일 (덮어써도 내용 변화 없음): $p"
      continue
    fi
    if [ -z "$backup_dir" ]; then
      backup_dir="$root/$backup_root/$(date -u +%Y%m%d-%H%M%S)"
      mkdir -p "$backup_dir" || {
        echo "중단: 백업 폴더를 만들지 못했다 — $backup_dir" >&2
        exit 1
      }
      exclude_backup_dir
    fi
    mkdir -p "$backup_dir/$(dirname "$p")" || {
      echo "중단: 백업 폴더를 만들지 못했다 — $backup_dir/$(dirname "$p")" >&2
      exit 1
    }
    # Backup failure aborts: overwriting without a way back is the one thing
    # this block exists to prevent.
    cp "$root/$p" "$backup_dir/$p" || {
      echo "중단: 백업 실패 — $p (아무것도 덮어쓰지 않고 멈춘다)" >&2
      exit 1
    }
    backed_up+=("$p")
    if [ "$p" = ".claude/settings.json" ]; then settings_changed=1; fi
    if [ "$p" = "CLAUDE.md" ]; then claude_md_changed=1; fi
    echo ""
    echo "── 덮어쓰기 예정: $p — 백업 완료, 아래가 들어올 변경이다 ──"
    git diff --stat HEAD template/main -- "$p"
    git diff HEAD template/main -- "$p" | head -n "$diff_cap"
    dl=$(git diff HEAD template/main -- "$p" | wc -l | tr -d '[:space:]')
    if [ "${dl:-0}" -gt "$diff_cap" ]; then
      echo "   … diff $dl 줄 중 앞 $diff_cap 줄만 표시 — 전체: git diff HEAD template/main -- $p"
    fi
  done
  if [ -n "$backup_dir" ]; then echo ""; fi

  # Per-path checkout: a path missing from the template must not kill the
  # run, but a REAL checkout failure (permissions, index.lock, …) must not be
  # lumped in with it either — the ls-tree existence probe separates "not in
  # template" from actual errors first, so failures are never disguised as
  # skips. NOT `git cat-file -e template/main:"$p"`: on Windows git-bash,
  # MSYS path conversion mangles rev:path arguments whose path part contains
  # a slash (template/main:.claude/agents -> template\main;.claude\agents),
  # falsely skipping every .claude/.github path. ls-tree takes the path as a
  # pathspec after `--` — the same form checkout itself uses — which MSYS
  # leaves alone.
  # Checkout overwrites files that exist in template/main; files the project
  # added under the same directories are left alone.
  settings_synced=0
  claude_md_synced=0
  fail=0
  for p in "${paths[@]}"; do
    if [ -z "$(git ls-tree --name-only template/main -- "$p")" ]; then
      echo "skip (템플릿에 없음): $p"
    elif git checkout template/main -- "$p"; then
      if [ "$p" = ".claude/settings.json" ]; then settings_synced=1; fi
      if [ "$p" = "CLAUDE.md" ]; then claude_md_synced=1; fi
    else
      echo "실패 (checkout 에러 — 권한·lock 등, 위 git 메시지 참고): $p" >&2
      fail=1
    fi
  done

  echo ""
  if [ "$fail" = 1 ]; then
    echo "── 템플릿 동기화 일부 실패: template/main @ $sha — 위 '실패' 경로는 반영 안 됨 ──"
  else
    echo "── 템플릿 동기화 완료: template/main @ $sha ──"
  fi
  git status --short
  echo ""
  if [ "$settings_synced" = 1 ] && [ "$settings_changed" = 1 ]; then
    echo "!! 주의: .claude/settings.json 이 템플릿 버전으로 덮어써졌다."
    echo "   로컬 커스터마이징은 다시 적용해야 한다 — 앞으로는 .claude/settings.local.json 에"
    echo "   두면 템플릿 최신화가 절대 건드리지 않는다. settings/hooks 변경은 Claude 재시작"
    echo "   후에만 적용된다."
    echo ""
  fi
  if [ "$claude_md_synced" = 1 ] && [ "$claude_md_changed" = 1 ]; then
    echo "!! 주의: CLAUDE.md(하단 커스터마이징) 도 템플릿 버전으로 덮어써졌다."
    echo "   프로젝트에서 직접 수정했던 내용은 커밋 전에 git diff --cached 로 확인해 복원하라."
    echo ""
  fi
  # checkout puts the result in worktree AND index, so a bare `git diff`
  # (worktree vs index) prints nothing — the review command must be --cached.
  echo "검토: git status --short / git diff --cached"
  echo "   (checkout 결과는 이미 index에 올라가 있어 그냥 git diff 는 빈 출력이다.)"
  echo ""
  if [ -n "$backup_dir" ]; then
    rel=${backup_dir#"$root"/}
    echo "── 덮어쓰기 직전 백업 ──"
    for p in "${backed_up[@]}"; do
      echo "   $rel/$p   →   $p"
    done
    echo "   복원: cp \"$rel/${backed_up[0]}\" \"${backed_up[0]}\""
    echo "   비교: diff \"$rel/${backed_up[0]}\" \"${backed_up[0]}\""
    echo "   전체 되돌리기: git reset --hard HEAD (실행 전 트리는 깨끗했다. 백업은 추적 대상이"
    echo "   아니라 남는다.)"
    echo "   이 폴더는 git이 무시하도록 등록해 뒀다. 검토가 끝나면 rm -rf \"$rel\" 로 지워라."
    echo ""
  fi
  echo "커밋하지 않았다. 위 변경을 검토한 뒤 직접 커밋하라."
  # Real checkout failures must surface in the exit status too, not only in
  # the text above — main()'s return value becomes the script's exit code.
  [ "$fail" = 0 ]
}

# Invoked through main() so bash parses the whole file before executing any of
# it — the `scripts` sync path overwrites this very script while it runs.
# `; exit` is the other half of that guard: without it bash would read the next
# command from the tail bytes of the freshly checked-out file after main returns.
main "$@"; exit
