#!/usr/bin/env bash
# sync-manifest.sh — 템플릿→파생 동기화 + 삭제 manifest (v1 template-update.sh 의 후계)
#
# duet-starter 에서 파생된 프로젝트 안에서 실행한다. 템플릿 원본에서는 실행을 거부한다
# (.claude/identity 의 `identity: template` 선언 기준 — v1 의 remote 추정 판별을 대체).
# 템플릿 소유 기계(CLAUDE.md, SETUP.md, scripts, .claude/rules·agents·skills·output-styles·
# conventions 및 Codex 대응 운영 파일)만 갱신한다. settings/config/hooks는 프로젝트 소유다.
#
# 사용법: bash scripts/sync-manifest.sh [템플릿 저장소 URL]
#   - git remote `template` 이 있으면 그것을 쓴다
#   - 없으면 URL 인자로 1회 등록한다 (이후 인자 불필요)
#   - 인자도 없으면 .claude/identity 의 `template:` 지문(SETUP 8단계가 보존)으로 자동 복구한다
#     — URL 을 묻는 경로는 이 지문마저 없는 레거시 파생뿐이다
#
# v1 실측 교훈 2건이 이 스크립트의 존재 이유다:
#
# 1) TWO-RUN LAG 해소 — v1 은 1회차 실행이 프로젝트의 "구버전 스크립트"로 끝까지 돌아,
#    경로 배열·가드 변경이 2회차에야 유효했다(v1 실측). v2 는 fetch 직후 자기 자신
#    (이 스크립트 + 삭제 manifest)을 먼저 template/main 버전으로 바꾸고 새 사본을 exec 로
#    재실행한다 — 모든 변경이 1회차부터 유효하다. SYNC_MANIFEST_REEXEC=1 이 재귀 방지 가드다.
#
# 2) 삭제 manifest — checkout 은 template/main 에 "존재하는" 파일만 덮어쓰므로 템플릿에서
#    지운 파일이 파생에 잔존물로 영원히 남는다(v1 실측: SETUP 잔존물). 템플릿이 지운 경로를
#    scripts/sync-manifest.deletions 에 등재하면 이 스크립트가 파생에서도 제거한다.
#
# checkout 이 기존 파일을 무경고로 덮어쓰는 문제(v1 실측)에 대한 방어도 유지한다:
# 더티 트리 중단 + 동기화 대상 10경로 **전부**를 덮어쓰기 전에 .sync-manifest-backup/<시각>/ 에
# 백업하고 들어올 diff 를 먼저 보여준다. 다만 파생이 손대지 않은 파일(merge-base 기준)은
# 덮어써도 잃을 것이 없으므로 요약 1줄로만 알린다 — 매번 전 경로의 diff 를 쏟으면 아무도
# 안 읽는다. 파생이 흔히 고치는 CLAUDE.md·AGENTS.md는 그 억제에서 빠져 언제나
# 백업+diff 한다(기존 동작 그대로).
#
# 이 스크립트는 git add/commit 을 하지 않는다 — checkout·git rm 결과는 index 에 올라가고,
# 검토와 커밋은 호출자 몫이다. 스스로 쓰는 것은 로컬 exclude 파일(.git/info/exclude) 한 줄뿐.

self=scripts/sync-manifest.sh
manifest=scripts/sync-manifest.deletions

# 동기화(checkout) 대상 — 템플릿 소유 경로만. 디렉토리 항목은 checkout 의미론에 기댄다:
# template/main 에 존재하는 파일만 덮어쓰므로, 프로젝트가 같은 폴더에 추가한 자기 스킬·
# 스크립트·규칙은 건드리지 않는다. `.claude` 통째 동기화는 금지 — identity 를 되살린다
# (아래 HARD EXCLUSIONS).
paths=(
  AGENTS.md
  CLAUDE.md
  SETUP.md
  .gitattributes
  scripts
  tests
  .claude/rules
  .claude/agents
  .claude/skills
  .claude/output-styles
  .claude/conventions
  .codex/rules
  .codex/roles
  .codex/agents
  .agents/skills
  .codex/conventions
)

# 백업+diff 는 위 paths 전부에 적용된다. 아래 목록은 그중 "언제나 시끄러운" 등급 —
# 파생이 흔히 직접 수정하는 경로라 로컬 수정 판정과 무관하게 항상 백업+diff 한다.
# 나머지 경로는 파생이 실제로 손댄 파일만 백업+diff 하고, 손대지 않은 파일은 요약 1줄이다.
# 위 paths 와 동기화 유지: 여기 있는데 paths 에 없는 항목은 아무 일도 하지 않는다.
customizable=(
  CLAUDE.md
  AGENTS.md
)
backup_root=.sync-manifest-backup
diff_cap=200
backup_dir=""     # 첫 백업 때 만들어진다. exec 재실행 사이에는 환경변수로 이어받는다.
backed_up=()

# !! HARD EXCLUSIONS — paths 에도, 삭제 manifest 에도 절대 넣지 않는다 !!
#   .claude/identity             파생의 신분 선언(project). 동기화하면 template 로 되돌아가
#                                모든 세션 분기가 뒤집힌다 — v1 template-origin 센티넬 사고의 동형.
#   docs/                        STATE/MAP/GOTCHAS/constitution — 프로젝트 소유.
#   README.md                    프로젝트 소유 (제품용으로 재작성됨).
#   .claude/settings.local.json  로컬 커스터마이징 — 동기화가 절대 건드리지 않는 자리.
#   .github/workflows/ci.yml     파생 후 프로젝트 소유 (v1 실측: 동기화가 앱 잡을 지웠다).
#   .mcp.json                    파생 후 프로젝트 소유 (프로젝트 MCP 서버가 여기 쌓인다).
#   .claude/perf/ .claude/review/ docs/learn/      로컬 런타임 산출물 — gitignore 대상.
#   .claude/automode             무인/유인 모드 상태. 동기화하면 남의 모드가 파생으로 새어
#                                파생이 켜진 채로 시작한다 — 상태는 체크아웃마다 독립이다.
#   제품 코드                    당연히 프로젝트 소유.
# 삭제 manifest 파서는 아래 protected_path 로 이 목록을 기계적으로도 거부한다.

protected_path() {
  case "$1" in
    docs|docs/*|README.md|.claude/identity|.claude/settings*.json|.claude/automode|.claude/perf|.claude/perf/*|.claude/review|.claude/review/*|.claude/index|.claude/index/*|.claude/snapshots|.claude/snapshots/*|.codex/identity|.codex/config.toml|.codex/hooks.json|.codex/local-notes.md|.codex/automode|.codex/perf|.codex/perf/*|.codex/review|.codex/review/*|.codex/index|.codex/index/*|.codex/snapshots|.codex/snapshots/*|.mcp.json|.github/workflows/ci.yml)
      return 0 ;;
    scripts/sync-manifest.sh|scripts/codex-sync-manifest.sh|scripts/sync-manifest.deletions|scripts/codex-sync-manifest.deletions)
      return 0 ;;
  esac
  return 1
}

# A deletion manifest may remove only named files in template-owned areas.
# This rejects ancestor directories, application paths, symlinks and pathspec magic.
deletable_path() {
  case "$1" in
    scripts/*|tests/*|.claude/rules/*|.claude/agents/*|.claude/skills/*|.claude/output-styles/*|.claude/conventions/*|.codex/rules/*|.codex/roles/*|.codex/agents/*|.codex/conventions/*|.agents/skills/*) ;;
    *) return 1 ;;
  esac
  case "$1" in *'\\'*|*':'*|*'*'*|*'?'*|*'['*|*'..'*|/*) return 1 ;; esac
  [ ! -d "$root/$1" ] && [ ! -L "$root/$1" ] || return 1
  # realpath resolves symlink parents too; the target must stay in this checkout.
  resolved=$(realpath -m -- "$root/$1") || return 1
  case "$resolved" in "$root"/*) return 0 ;; *) return 1 ;; esac
}

# 백업 폴더를 git status 에서 숨긴다 — .gitignore 는 프로젝트 소유라 로컬 exclude 에 넣는다.
# --git-common-dir: 워크트리 안(.git 이 파일)에서도 동작한다.
exclude_backup_dir() {
  gitdir=$(git rev-parse --git-common-dir 2>/dev/null) || return 0
  [ -n "$gitdir" ] || return 0
  ex="$gitdir/info/exclude"
  if [ -f "$ex" ] && grep -qF "/$backup_root/" "$ex"; then return 0; fi
  mkdir -p "$gitdir/info" 2>/dev/null || return 0
  {
    echo ""
    echo "# sync-manifest.sh: 덮어쓰기 직전 백업 (지워도 무방)"
    echo "/$backup_root/"
  } >>"$ex" 2>/dev/null || {
    echo "알림: $backup_root/ 을 git 무시 목록에 넣지 못했다 — 커밋에 섞이지 않게 직접 확인하라." >&2
    return 0
  }
}

# rev:path 형태(git show template/main:scripts/…)는 어디에도 쓰지 않는다 — Windows git-bash 의 MSYS
# 경로 변환이 슬래시 포함 rev:path 를 망가뜨린다(.claude/rules/shell-traps.md 'MSYS path conversion').
# 존재 확인은 전부 `git ls-tree --name-only template/main -- <path>` (pathspec 은 변환 대상이 아니다).
in_template() {
  [ -n "$(git ls-tree --name-only template/main -- "$1")" ]
}

# "언제나 백업+diff" 등급인가 (customizable 목록 소속인가).
always_verbose() {
  for c in "${customizable[@]}"; do
    [ "$1" = "$c" ] && return 0
  done
  return 1
}

# 파생이 이 파일을 직접 손댔는가 — 갈라진 지점(merge-base) 이후 파생 쪽 변경 유무로 본다.
# mbase 가 없으면(공통 조상 없는 파생) 판정 불가이므로 "손댔다"로 본다 — 안전한 쪽.
# 알려진 한계: 이전 동기화 커밋도 파생 쪽 변경으로 잡히므로, 한 번 동기화한 뒤로는
# 판정이 시끄러운 쪽으로 기운다. 조용한 쪽으로 틀리지는 않으므로 그대로 둔다.
locally_modified() {
  [ -n "$mbase" ] || return 0
  # HEAD 에 없는데 디스크에 있다 = 동기화가 준 사본이 아니다 (gitignore 된 로컬 파일 등).
  git ls-files --error-unmatch -- "$1" >/dev/null 2>&1 || return 0
  [ -n "$(git diff --name-only "$mbase" HEAD -- "$1")" ]
}

# 백업 폴더는 첫 백업 때 한 번만 만든다 — 백업할 것이 없으면 폴더도 안 생긴다.
ensure_backup_dir() {
  [ -z "$backup_dir" ] || return 0
  backup_dir="${SYNC_MANIFEST_BACKUP_DIR:-$root/$backup_root/$(date -u +%Y%m%d-%H%M%S)}"
  mkdir -p "$backup_dir" || {
    echo "중단: 백업 폴더를 만들지 못했다 — $backup_dir" >&2
    exit 1
  }
  exclude_backup_dir
}

# 파일 하나를 백업한다. 실패는 중단: 되돌릴 길 없는 덮어쓰기를 막는 것이 존재 이유다.
# 이미 백업돼 있으면(자체 갱신 때 부모가 떠 놓은 사본) 다시 복사하지 않고 1 을 돌려준다
# — 그 자리의 파일은 이미 새 내용이라, 덮어 뜨면 원본 백업이 사라진다.
backup_file() {
  bf=$1
  ensure_backup_dir
  if [ -e "$backup_dir/$bf" ]; then
    backed_up+=("$bf")
    return 1
  fi
  mkdir -p "$backup_dir/$(dirname "$bf")" || {
    echo "중단: 백업 폴더를 만들지 못했다 — $backup_dir/$(dirname "$bf")" >&2
    exit 1
  }
  cp "$root/$bf" "$backup_dir/$bf" || {
    echo "중단: 백업 실패 — $bf (아무것도 덮어쓰지 않고 멈춘다)" >&2
    exit 1
  }
  backed_up+=("$bf")
}

# 들어올 변경을 미리 보여준다 — 길면 앞부분만.
show_incoming_diff() {
  sd=$1
  echo ""
  echo "── 덮어쓰기 예정: $sd — 백업 완료, 아래가 들어올 변경이다 ──"
  git diff --stat HEAD template/main -- "$sd"
  git diff HEAD template/main -- "$sd" | head -n "$diff_cap"
  dl=$(git diff HEAD template/main -- "$sd" | wc -l | tr -d '[:space:]')
  if [ "${dl:-0}" -gt "$diff_cap" ]; then
    echo "   … diff $dl 줄 중 앞 $diff_cap 줄만 표시 — 전체: git diff HEAD template/main -- $sd"
  fi
}

main() {
  root=$(git rev-parse --show-toplevel 2>/dev/null) || {
    echo "중단: git 저장소가 아니다. 파생 프로젝트 안에서 실행하라." >&2
    exit 1
  }
  cd "$root" || exit 1

  # A unified project has two identity adapters. Never update only half a template.
  for runtime in .claude .codex; do
    identity_value=$(sed -n 's/^[[:space:]]*identity:[[:space:]]*//p' "$root/$runtime/identity" 2>/dev/null | head -1 | tr -d '[:space:]')
    if [ "$identity_value" != project ]; then
      echo "중단: $runtime/identity must be project; complete the unified SETUP.md first." >&2
      exit 1
    fi
  done

  # -- 신분 확인: 템플릿 원본에서는 절대 돌지 않는다. -------------------------
  # 다른 무엇보다 먼저 검사해 원본에서는 아무것도 변형하지 않는다.
  idfile="$root/.claude/identity"
  if [ -f "$idfile" ]; then
    idv=$(grep -E '^[[:space:]]*identity:' "$idfile" 2>/dev/null | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '[:space:]')
    if [ "$idv" = "template" ]; then
      echo "중단: .claude/identity 가 template 이다 — 여기는 템플릿 원본. 이 스크립트는 파생 프로젝트 전용이다." >&2
      echo "  파생 전환 절차는 SETUP.md 1단계(identity: project)." >&2
      exit 1
    fi
    if [ -n "$idv" ] && [ "$idv" != "project" ]; then
      echo "중단: .claude/identity 의 identity 값이 이상하다 — '$idv' (허용: template | project)." >&2
      exit 1
    fi
  else
    echo "알림: .claude/identity 가 없다. 세션 시작 분기가 판별 불가 상태다 — SETUP.md 1단계로 생성을 권한다. 동기화는 계속한다."
  fi

  # -- 더티 트리 중단: checkout 이 조용히 덮어쓴다 (v1 실측 방어). -------------
  # 재실행(self-update 후 exec)일 때는 건너뛴다: 부모가 깨끗함을 이미 확인했고,
  # 지금의 유일한 변경은 방금 checkout 된 self/manifest 뿐이다.
  if [ -z "${SYNC_MANIFEST_REEXEC:-}" ] && [ -n "$(git status --porcelain)" ]; then
    echo "중단: 커밋 안 된 변경이 있다. checkout 이 조용히 덮어쓰므로 먼저 commit 또는 stash 하고 다시 실행하라." >&2
    exit 1
  fi

  # -- template remote 확보. ---------------------------------------------------
  if turl=$(git remote get-url template 2>/dev/null); then
    if [ -n "$1" ]; then
      echo "알림: git remote 'template' 이 이미 등록되어 있어 URL 인자는 무시한다 — 사용 중: $turl"
      echo "  바꾸려면: git remote set-url template <새 URL>"
    fi
  else
    if [ -n "$1" ]; then
      git remote add template "$1" || exit 1
    else
      # 폴백: SETUP 8단계가 identity 에 보존한 `template:` 지문으로 remote 를 자동 복구한다
      # (재clone 으로 remote 가 사라져도 첫 /template-update 가 URL 을 묻지 않게).
      # URL 화 규칙은 identity 주석과 동일: host/... 지문은 https:// 접두, 로컬 경로는 그대로.
      tfp=""
      if [ -f "$idfile" ]; then
        tfp=$(grep -E '^[[:space:]]*template:' "$idfile" 2>/dev/null | head -1 | sed 's/^[^:]*:[[:space:]]*//;s/[[:space:]]*$//' | tr -d '\r')
      fi
      if [ -n "$tfp" ] && [ "$tfp" != "none" ]; then
        case "$tfp" in
          *://*|git@*|/*|./*|../*|~*|?:*) trecover=$tfp ;;   # URL과 로컬 경로는 그대로 쓴다
          *) trecover="https://$tfp" ;;          # host/owner/repo 지문은 URL 화한다
        esac
        echo "알림: git remote 'template' 이 없어 .claude/identity 의 template: 지문으로 자동 복구한다 — $trecover"
        git remote add template "$trecover" || exit 1
      else
        echo "사용법: bash scripts/sync-manifest.sh <템플릿 저장소 URL>" >&2
        echo "git remote 'template' 이 없고 .claude/identity 에 template: 지문도 없다(레거시 파생). URL 을 한 번 넘기면 remote 로 등록되고 다음부터는 인자 없이 실행한다." >&2
        exit 1
      fi
    fi
  fi

  # --no-tags: 템플릿의 릴리즈 태그가 파생의 태그 공간으로 새지 않게.
  git fetch --no-tags template || exit 1

  sha=$(git rev-parse -q --verify --short template/main) || {
    echo "중단: template remote 에서 main 브랜치를 찾지 못했다 (template/main 확인 실패)." >&2
    echo "  URL 이 템플릿 저장소가 아니거나 기본 브랜치가 main 이 아니다. 현재 URL: $(git remote get-url template)" >&2
    exit 1
  }

  # 파생이 손댄 파일을 가려내는 기준점 — 없으면(공통 조상 없는 파생) 전부 손댄 것으로 본다.
  mbase=$(git merge-base HEAD template/main 2>/dev/null) || mbase=""

  if ! in_template CLAUDE.md || ! in_template AGENTS.md || ! in_template .claude/identity || ! in_template .codex/identity || ! in_template scripts/starter.py; then
    echo "중단: template/main is not a unified Claude/Codex starter." >&2
    exit 1
  fi

  # -- SELF-UPDATE FIRST — two-run lag 해소. -----------------------------------
  # 이 스크립트와 삭제 manifest 가 template/main 과 다르면, 그 둘만 먼저 checkout 하고
  # "새 사본"을 exec 로 재실행한다. 이후의 경로 배열·가드·manifest 처리 전부가 최신
  # 버전으로 돈다 — v1 의 "1회차는 구버전이 끝까지 돈다" 문제의 직접 수리다.
  if [ -z "${SYNC_MANIFEST_REEXEC:-}" ]; then
    needs_reexec=0
    for p in "$self" "$manifest"; do
      in_template "$p" || continue
      if [ -n "$(git diff --name-only HEAD template/main -- "$p")" ]; then
        # 자체 갱신도 덮어쓰기다 — 아래 백업 블록이 돌기 전에 일어나므로 여기서 먼저 뜬다.
        # 백업 폴더는 환경변수로 새 사본에 넘겨 한 실행에 폴더가 둘 생기지 않게 한다.
        if [ -f "$root/$p" ] && locally_modified "$p"; then
          backup_file "$p" && show_incoming_diff "$p"
        fi
        git checkout template/main -- "$p" || {
          echo "중단: 스크립트 자체 갱신(checkout) 실패 — $p (위 git 메시지 참고)" >&2
          exit 1
        }
        needs_reexec=1
      fi
    done
    if [ "$needs_reexec" = 1 ]; then
      echo "── 스크립트/manifest 자체가 갱신됨 — 새 버전으로 재실행한다 (two-run lag 방지) ──"
      SYNC_MANIFEST_REEXEC=1 SYNC_MANIFEST_BACKUP_DIR="$backup_dir" exec bash "$root/$self" "$@"
    fi
  fi

  # -- 백업 + diff — 무엇이든 덮어쓰기 전에. -----------------------------------
  # 트리는 깨끗하므로(위에서 확인; 재실행이면 self/manifest 만 변경) HEAD 가 프로젝트의
  # 현재 사본이고, commit-to-commit diff 가 checkout 이 하려는 일 그대로다.
  #
  # 10개 경로 전부를 같은 방식으로 훑는다. 디렉토리 항목은 diff 가 돌려주는 파일 목록으로
  # 풀어 파일 단위로 처리한다 — 파일 하나짜리 항목은 그 목록이 자기 자신이라 같은 코드로 돈다.
  for p in "${paths[@]}"; do
    in_template "$p" || continue
    # --diff-filter=d: 템플릿에서 사라진 파일(파생에만 남은 것)은 제외 — checkout 이 안 건드린다.
    # core.quotepath=false: 비ASCII 파일명이 \nnn 로 인용돼 경로가 깨지는 것을 막는다.
    changed=$(git -c core.quotepath=false diff --name-only --diff-filter=d HEAD template/main -- "$p")
    if [ -z "$changed" ]; then
      echo "동일 (덮어써도 내용 변화 없음): $p"
      continue
    fi
    quiet=0
    while IFS= read -r f; do
      [ -n "$f" ] || continue
      [ -f "$root/$f" ] || continue   # 로컬에 없다 = 새로 들어오는 파일, 잃을 것이 없다
      if always_verbose "$p" || locally_modified "$f"; then
        backup_file "$f" && show_incoming_diff "$f"
      else
        quiet=$((quiet + 1))
      fi
    done <<<"$changed"
    if [ "$quiet" -gt 0 ]; then
      echo "갱신 (로컬 수정 없음 — 백업·diff 생략): $p — 파일 $quiet 개"
    fi
  done
  if [ -n "$backup_dir" ]; then echo ""; fi

  # -- 경로별 checkout. --------------------------------------------------------
  # 템플릿에 없는 경로는 skip, 진짜 checkout 실패(권한·index.lock …)는 fail 로 구분한다.
  fail=0
  for p in "${paths[@]}"; do
    if ! in_template "$p"; then
      echo "skip (템플릿에 없음): $p"
    elif git checkout template/main -- "$p"; then
      :
    else
      echo "실패 (checkout 에러 — 권한·lock 등, 위 git 메시지 참고): $p" >&2
      fail=1
    fi
  done

  # -- 삭제 manifest 처리. -----------------------------------------------------
  # 방금 scripts/ checkout(그리고 self-update)이 최신 manifest 를 이미 가져왔으므로
  # 등재는 1회차 실행부터 유효하다.
  deleted=()
  del_skipped=()
  for deletion_manifest in scripts/sync-manifest.deletions scripts/codex-sync-manifest.deletions; do
  if [ -f "$root/$deletion_manifest" ]; then
    while IFS= read -r line || [ -n "$line" ]; do
      entry=${line%%#*}
      entry=$(printf '%s' "$entry" | tr -d '\r')
      entry="${entry#"${entry%%[![:space:]]*}"}"
      entry="${entry%"${entry##*[![:space:]]}"}"
      [ -n "$entry" ] || continue
      # 경로 탈출·절대 경로 거부 — manifest 는 저장소 루트 상대 경로만 받는다.
      case "$entry" in
        /*|*..*)
          del_skipped+=("$entry (루트 상대 경로가 아님)")
          continue ;;
      esac
      if protected_path "$entry" || ! deletable_path "$entry"; then
        del_skipped+=("$entry (프로젝트 소유 — HARD EXCLUSIONS)")
        continue
      fi
      # 아직 템플릿에 존재하면 잘못된 등재 — 지우지 않는다.
      if in_template "$entry"; then
        del_skipped+=("$entry (template/main 에 아직 존재 — 등재 오류)")
        continue
      fi
      if git ls-files --error-unmatch -- "$entry" >/dev/null 2>&1; then
        if git --literal-pathspecs rm -q -- "$entry"; then
          deleted+=("$entry")
        else
          echo "실패 (git rm 에러): $entry" >&2
          fail=1
        fi
      elif [ -e "$root/$entry" ]; then
        # 추적되지 않은 잔존물 — rm 으로 제거 (index 에는 아무 흔적 없음).
        rm -f -- "$root/$entry" && deleted+=("$entry (untracked)")
      fi
      # 파생에 이미 없으면 조용히 지나간다 — manifest 는 영구 누적이다.
    done <"$root/$deletion_manifest"
  fi
  done

  # Config and hooks stay project-owned. Detect stale generated main instructions;
  # never replace user config as a side effect of a template update.
  if [ -f scripts/sync-orchestrator.py ]; then
    python scripts/sync-orchestrator.py --check || {
      echo "ACTION REQUIRED: review .codex/roles/orchestrator.md, then run python scripts/sync-orchestrator.py." >&2
      fail=1
    }
  fi

  # -- 보고. -------------------------------------------------------------------
  echo ""
  if [ "$fail" = 1 ]; then
    echo "── 템플릿 동기화 일부 실패: template/main @ $sha — 위 '실패' 경로는 반영 안 됨 ──"
  else
    echo "── 템플릿 동기화 완료: template/main @ $sha ──"
  fi
  if [ "${#deleted[@]}" -gt 0 ]; then
    echo "── 삭제 manifest 로 제거된 경로 ──"
    for d in "${deleted[@]}"; do echo "   삭제: $d"; done
  fi
  if [ "${#del_skipped[@]}" -gt 0 ]; then
    echo "── 삭제 manifest 에서 건너뛴 항목 (등재를 점검하라) ──"
    for d in "${del_skipped[@]}"; do echo "   skip: $d"; done
  fi
  git status --short
  echo ""
  # checkout·git rm 결과는 이미 index 에 있다 — 검토는 --cached (그냥 git diff 는 빈 출력).
  echo "검토: git status --short / git diff --cached"
  echo "   (checkout 결과는 이미 index 에 올라가 있어 그냥 git diff 는 빈 출력이다.)"
  if [ -n "$backup_dir" ]; then
    rel=${backup_dir#"$root"/}
    echo ""
    echo "── 덮어쓰기 직전 백업 ──"
    for p in "${backed_up[@]}"; do
      echo "   $rel/$p   →   $p"
    done
    echo "   복원: cp \"$rel/${backed_up[0]}\" \"${backed_up[0]}\" / 전체 되돌리기: git reset --hard HEAD"
    echo "   이 폴더는 git 이 무시하도록 등록해 뒀다. 검토가 끝나면 rm -rf \"$rel\" 로 지워라."
  fi
  echo ""
  echo "커밋하지 않았다. 위 변경을 검토한 뒤 직접 커밋하라."
  [ "$fail" = 0 ]
}

# main() 경유 호출: bash 가 파일 전체를 파싱한 뒤 실행하게 한다 — `scripts` 동기화가 실행 중인
# 이 파일 자신을 덮어쓴다(self-update 재실행 뒤에는 동일 내용이지만 가드는 유지한다).
# `; exit` 는 그 가드의 나머지 절반: 없으면 main 반환 후 새로 checkout 된 파일의 꼬리 바이트를
# 다음 명령으로 읽는다.
main "$@"; exit
