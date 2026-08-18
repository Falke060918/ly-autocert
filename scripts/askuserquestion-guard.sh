#!/usr/bin/env bash
# PreToolUse guard for AskUserQuestion: block inputs that carry encoding-corruption
# signatures (see docs/GOTCHAS.md "Korean text sent through AskUserQuestion gets corrupted").
#
# Design is deliberately conservative — normal Korean written as literal UTF-8 must always
# pass; only definite corruption signatures block. Wrong-but-valid Hangul (비밀번호→뱄뱈호)
# is not detectable and is intentionally out of scope.
#
# Blocks (exit 2, stderr goes back to the model):
#   1. invalid UTF-8 byte sequences in the payload
#   2. U+FFFD replacement character — literal bytes or as a � escape
#   3. literal \uXXXX text remnants in Hangul code-point ranges (double-escaped in raw
#      JSON, i.e. the displayed string would show the literal text "\uc548" instead of "안")
# Passes: properly escaped JSON \uXXXX (single backslash, decodes to real Korean),
#   non-Hangul literal escapes (e.g. a question legitimately discussing A).
#
# Pure bash + grep (+ iconv when available). No jq, no python. Git Bash compatible.
set -u

payload=$(cat 2>/dev/null || true)
[ -n "$payload" ] || exit 0

fail() {
  echo "AskUserQuestion 입력에 인코딩 손상 흔적이 있다($1). 한글을 \\uXXXX 이스케이프가 아니라 UTF-8 리터럴 문자로 다시 써서 재시도하라. 손상된 문자열을 복사해 쓰지 말고 처음부터 다시 작성하라." >&2
  exit 2
}

# 1) invalid UTF-8 bytes
if command -v iconv >/dev/null 2>&1; then
  printf '%s' "$payload" | iconv -f UTF-8 -t UTF-8 >/dev/null 2>&1 || fail "invalid UTF-8"
fi

# 2) U+FFFD replacement character — literal bytes (EF BF BD) or � escape
printf '%s' "$payload" | grep -q "$(printf '\xef\xbf\xbd')" && fail "U+FFFD"
printf '%s' "$payload" | grep -Eqi '\\+ufffd' && fail "escaped U+FFFD"

# 3) literal \uXXXX remnants in Hangul ranges: double backslash in raw JSON means the
#    string itself contains the text "\uXXXX" — an escape that never got decoded.
#    Ranges: AC00-D7FF (syllables), 1100-11FF (jamo), 3130-318F (compat jamo).
printf '%s' "$payload" | grep -Eqi '\\\\u(a[c-f][0-9a-f]{2}|[bc][0-9a-f]{3}|d[0-7][0-9a-f]{2}|11[0-9a-f]{2}|31[3-8][0-9a-f])' \
  && fail "literal \\uXXXX remnant in Hangul range"

exit 0
