#!/usr/bin/env bash
# 스킬 건강관리 — hermes curator 축소판 (design/02 "스킬 건강관리").
# .agents/skills/ 각 스킬의 사용 빈도를 카운터 파일로 추적하고,
# 미사용 스킬을 active → stale → archived 로 전이한다.
#   archived = .agents/skills-archive/<스킬>/ 로 이동 (삭제 아님 — restore 로 복귀).
#   tar 스냅샷은 두지 않는다 — git 이 스냅샷 역할이다.
#
# Codex 스킬에는 공통 Skill 도구 이벤트가 없다. 이 원장은 수동 기록만 집계한다.
# `bash scripts/codex-skill-health.sh record <스킬>`로 실제 사용을 기록한다.
# 기록 누락을 미사용 증거로 삼지 않는다. sweep은 사용자가 요청한 경우에만 실행한다.
#
# 사용법 (수동/주기 호출용 CLI — $handoff 가 주기적으로 sweep 호출을 제안한다):
#   bash scripts/codex-skill-health.sh record <스킬>    # 사용 1회 적재
#   bash scripts/codex-skill-health.sh status           # 상태 보고 (읽기 전용, 기본)
#   bash scripts/codex-skill-health.sh sweep            # 전이 실행 — stale 보고 + archive 이동
#   bash scripts/codex-skill-health.sh restore <스킬>   # 보관에서 복귀 (+사용 1회로 적재)
#
# 기준일 (환경변수로 조정 — 테스트는 0 으로 낮춰 즉시 전이시킨다):
#   SKILL_STALE_DAYS   (기본 30) — 이만큼 미사용이면 active → stale
#   SKILL_ARCHIVE_DAYS (기본 60) — 이만큼 미사용이면 stale → archived
#
# 최근 사용일의 우선순위: 카운터 파일의 최근 기록 → (기록이 없으면) 관측이 시작된
# 때와 스킬이 생긴 때 중 더 나중 → 원장 자체가 없으면 지금 = active 관용.
# 폴더의 "마지막 커밋일"은 쓰지 않는다 — 그건 안 쓴 일수가 아니라 안 고친 일수라,
# 스킬을 계속 손보는 템플릿 원본에서는 sweep 이 영영 발화하지 않는다.

set -u

STALE_DAYS=${SKILL_STALE_DAYS:-30}
ARCHIVE_DAYS=${SKILL_ARCHIVE_DAYS:-60}

# 스킬 트리는 지금 체크아웃(워크트리 포함)을 본다.
repo=$(git rev-parse --show-toplevel 2>/dev/null)
repo=${repo:-${CODEX_STARTER_ROOT:-$PWD}}
skills_dir=$repo/.agents/skills
archive_dir=$repo/.agents/skills-archive

# 카운터 파일은 메인 저장소에 쌓는다 — 워크트리에서 불려도 한곳에 모이도록
# git common dir 로 앵커한다 (%.git 다음 %/ 순서 유지 — status-protocol 과 동일).
root=$(git rev-parse --git-common-dir 2>/dev/null)
root=${root%.git}; root=${root%/}
perf_dir=${root:-$repo}/.codex/perf
ledger=$perf_dir/skill-usage.jsonl

now=$(date +%s)

usage() {
  cat >&2 <<'EOF'
Usage: bash scripts/codex-skill-health.sh <record <스킬> | status | sweep | restore <스킬>>
  record  스킬 사용 1회 적재 (.codex/perf/skill-usage.jsonl)
  status  스킬별 상태 보고 — active / stale / archived (읽기 전용)
  sweep   전이 실행 — SKILL_ARCHIVE_DAYS 이상 미사용은 .agents/skills-archive/ 로 이동
  restore 보관된 스킬을 .agents/skills/ 로 복귀
EOF
  exit 1
}

sanitize() { printf '%s' "$1" | tr -cd 'A-Za-z0-9._-'; }

# 관측 시작 epoch — 원장의 가장 이른 기록. 원장이 없으면 관측이 시작되지 않았다.
ledger_start() {
  s=''
  [ -f "$ledger" ] && s=$(sed -n 's/.*"epoch":\([0-9][0-9]*\).*/\1/p' "$ledger" | sort -n | head -n 1)
  case $s in *[!0-9]*) s='' ;; esac
  printf '%s' "$s"
}

# 최근 사용 epoch. 인자: $1=스킬명 $2=스킬 폴더 경로
last_used() {
  last=''
  if [ -f "$ledger" ]; then
    last=$(grep -F "\"skill\":\"$1\"" "$ledger" 2>/dev/null \
      | sed -n 's/.*"epoch":\([0-9][0-9]*\).*/\1/p' | sort -n | tail -n 1)
  fi
  case $last in *[!0-9]*) last='' ;; esac
  if [ -z "$last" ]; then
    obs=$(ledger_start)
    # 관측 기록이 하나도 없으면 판정 근거가 없다 — 미사용으로 몰지 않는다.
    if [ -z "$obs" ]; then printf '%s' "$now"; return; fi
    # 스킬이 생긴 때 = 폴더를 건드린 첫 커밋 (마지막이 아니라 첫).
    born=$(git -C "$repo" log --reverse --format=%ct -- "$2" 2>/dev/null | head -n 1)
    case $born in *[!0-9]*|'') born=$(stat -f %m "$2/SKILL.md" 2>/dev/null || stat -c %Y "$2/SKILL.md" 2>/dev/null) ;; esac
    case $born in *[!0-9]*|'') born=0 ;; esac
    last=$obs
    [ "$born" -gt "$last" ] && last=$born
  fi
  case $last in *[!0-9]*|'') last=$now ;; esac
  printf '%s' "$last"
}

# grep -c 는 0건이어도 "0"을 찍고 exit 1 이므로 || 폴백을 붙이면 이중 출력된다 —
# 캡처 후 숫자 검증으로 정규화한다.
use_count() {
  c=''
  [ -f "$ledger" ] && c=$(grep -cF "\"skill\":\"$1\"" "$ledger" 2>/dev/null)
  case $c in ''|*[!0-9]*) c=0 ;; esac
  printf '%s' "$c"
}

# 미사용 일수 → 상태. $1=age_days  (archive 판정을 먼저 — 둘 다 0이면 archive 우선)
state_of() {
  if [ "$1" -ge "$ARCHIVE_DAYS" ]; then printf 'archive-due'
  elif [ "$1" -ge "$STALE_DAYS" ]; then printf 'stale'
  else printf 'active'; fi
}

append_ledger() { # $1=스킬명
  mkdir -p "$perf_dir" || return 1
  [ -f "$perf_dir/.gitignore" ] || printf '*\n' > "$perf_dir/.gitignore"
  printf '{"ts":"%s","epoch":%s,"skill":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$now" "$1" >> "$ledger"
}

cmd=${1:-status}
case $cmd in

record)
  s=$(sanitize "${2:-}")
  [ -n "$s" ] || usage
  append_ledger "$s" || exit 1
  ;;

status)
  printf '%-24s %-12s %10s %6s\n' 'SKILL' 'STATE' 'UNUSED(d)' 'USES'
  for d in "$skills_dir"/*/; do
    [ -f "${d}SKILL.md" ] || continue
    name=$(basename "$d")
    lu=$(last_used "$name" "$d")
    age=$(( (now - lu) / 86400 )); [ "$age" -ge 0 ] || age=0
    printf '%-24s %-12s %10s %6s\n' "$name" "$(state_of "$age")" "$age" "$(use_count "$name")"
  done
  for d in "$archive_dir"/*/; do
    [ -f "${d}SKILL.md" ] || continue
    printf '%-24s %-12s %10s %6s\n' "$(basename "$d")" 'archived' '-' "$(use_count "$(basename "$d")")"
  done
  ;;

sweep)
  moved=0; staled=0
  for d in "$skills_dir"/*/; do
    [ -f "${d}SKILL.md" ] || continue
    name=$(basename "$d")
    lu=$(last_used "$name" "$d")
    age=$(( (now - lu) / 86400 )); [ "$age" -ge 0 ] || age=0
    st=$(state_of "$age")
    case $st in
      archive-due)
        if [ -e "$archive_dir/$name" ]; then
          echo "skip: $name — 보관함에 같은 이름이 이미 있다 (수동 정리 필요)" >&2
          continue
        fi
        mkdir -p "$archive_dir" || exit 1
        mv "${d%/}" "$archive_dir/$name" || exit 1
        echo "archived: $name (${age}일 미사용 → .agents/skills-archive/$name — restore 로 복귀 가능)"
        moved=$((moved + 1)) ;;
      stale)
        echo "stale: $name (${age}일 미사용 — ${ARCHIVE_DAYS}일부터 보관 대상)"
        staled=$((staled + 1)) ;;
    esac
  done
  echo "sweep 완료: stale ${staled}건, archived ${moved}건 (기준: stale ${STALE_DAYS}일 / archive ${ARCHIVE_DAYS}일)"
  ;;

restore)
  s=$(sanitize "${2:-}")
  [ -n "$s" ] || usage
  if [ ! -d "$archive_dir/$s" ]; then
    echo "restore 실패: $archive_dir/$s 가 없다" >&2; exit 1
  fi
  if [ -e "$skills_dir/$s" ]; then
    echo "restore 실패: .agents/skills/$s 가 이미 있다" >&2; exit 1
  fi
  mkdir -p "$skills_dir" || exit 1
  mv "$archive_dir/$s" "$skills_dir/$s" || exit 1
  # 복귀 직후 재보관되지 않도록 사용 1회로 적재해 시계를 리셋한다.
  append_ledger "$s"
  echo "restored: $s → .agents/skills/$s"
  ;;

*) usage ;;
esac
exit 0
