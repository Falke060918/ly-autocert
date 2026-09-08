#!/usr/bin/env sh
# stylelint-fonts.sh — machine check of the font rules in .claude/rules/fonts.md §1.
# Rule bodies live there (single source); this runner only compares CSS against them.
# Usage: bash scripts/stylelint-fonts.sh [css-path-or-glob ...]   (default: **/*.css)
# rc 0 = clean, skipped (no node), or no target files; rc != 0 = violations or stylelint error.

if ! command -v node >/dev/null 2>&1 || ! command -v npx >/dev/null 2>&1; then
  echo "SKIP: node 없음 — stylelint 검사 건너뜀"
  exit 0
fi

cfg="$(dirname "$0")/stylelint-fonts.config.json"

if [ $# -eq 0 ]; then
  set -- "**/*.css"
fi

out=$(npx -y stylelint --config "$cfg" "$@" 2>&1)
rc=$?

if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "No files matching"; then
  echo "대상 CSS 파일 0개 — 검사할 것 없음 (패턴: $*)"
  exit 0
fi

if [ $rc -eq 0 ]; then
  echo "OK: 폰트 규칙 위반 0 (대상: $*)"
else
  printf '%s\n' "$out"
fi
exit $rc
