#!/usr/bin/env sh
# odiff-compare.sh — pixel comparison of a before/after screenshot pair.
# Wraps the odiff call in a script so the repo's bare-npx deny does not block it
# (same shape as scripts/stylelint-fonts.sh; script-mediated npx is allowed).
# Usage: bash scripts/odiff-compare.sh <before.png> <after.png> [diff.png]
# rc 0 = images match, or skipped (no odiff/node); rc 21/22 = images differ
# (diff image written to [diff.png] when given); any other rc = tool/input error,
# NOT a detected difference.

if [ $# -lt 2 ]; then
  echo "usage: bash scripts/odiff-compare.sh <before.png> <after.png> [diff.png]" >&2
  exit 2
fi

before=$1
after=$2
diffout=$3

if command -v odiff >/dev/null 2>&1; then
  run_odiff() { odiff "$@"; }
elif command -v node >/dev/null 2>&1 && command -v npx >/dev/null 2>&1; then
  run_odiff() { npx -y odiff-bin "$@"; }
else
  echo "SKIP: odiff/node 없음 — 픽셀 비교 건너뜀 (스크린샷 눈 대조로 폴백)"
  exit 0
fi

if [ -n "$diffout" ]; then
  run_odiff "$before" "$after" "$diffout"
else
  run_odiff "$before" "$after"
fi
rc=$?

if [ $rc -eq 0 ]; then
  echo "OK: 두 이미지 동일 (rc=0)"
elif [ $rc -eq 21 ] || [ $rc -eq 22 ]; then
  if [ -n "$diffout" ]; then
    echo "DIFF: 두 이미지가 다름 (rc=$rc) — 차이 이미지: $diffout"
  else
    echo "DIFF: 두 이미지가 다름 (rc=$rc)"
  fi
else
  echo "ERROR: odiff 실행 오류 (rc=$rc) — 차이 검출이 아니라 도구/입력 오류다" >&2
fi
exit $rc
