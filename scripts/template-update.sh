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
#   any product code             obviously project-owned.

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
  if [ "$settings_synced" = 1 ]; then
    echo "!! 주의: .claude/settings.json 이 템플릿 버전으로 덮어써졌다."
    echo "   로컬 커스터마이징은 다시 적용해야 한다 — 앞으로는 .claude/settings.local.json 에"
    echo "   두면 템플릿 최신화가 절대 건드리지 않는다. settings/hooks 변경은 Claude 재시작"
    echo "   후에만 적용된다."
    echo ""
  fi
  if [ "$claude_md_synced" = 1 ]; then
    echo "!! 주의: CLAUDE.md(하단 커스터마이징) 도 템플릿 버전으로 덮어써졌다."
    echo "   프로젝트에서 직접 수정했던 내용은 커밋 전에 git diff 로 확인해 복원하라."
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
