#!/usr/bin/env bash
# ko-wordcheck-hook.sh — PostToolUse 훅: 한국어 문서를 저장하는 순간 굳은 직역 어휘를 지적한다.
#
# 입출력 규약은 scripts/nudge-reminder.sh 와 같다:
#   stdin  = 훅 JSON
#   stdout = {"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"..."}}
#   rc     = 언제나 0 (훅은 매 툴 호출마다 도므로 여기서 깨지면 안 된다)
# 지적할 것이 없으면 아무것도 내지 않는다. 파일은 절대 고치지 않는다 — 판단은 모델·사람 몫이다.
#
# ── .claude/settings.json 에 붙여넣을 조각 (PostToolUse 배열의 첫 객체 "hooks" 안에 한 항목 추가) ──
# 이 저장소의 규칙상 에이전트는 settings.json 을 못 고친다. 아래를 사용자가 직접 넣는다.
#
#           {
#             "type": "command",
#             "command": "d=${CLAUDE_PROJECT_DIR:-$PWD}; b=$d/scripts/ko-wordcheck-hook.sh; [ -f \"$b\" ] && bash \"$b\"; exit 0"
#           }
#
# 넣는 자리: "PostToolUse" → matcher 없는 첫 객체의 "hooks" 배열 (perf-log.sh · nudge-reminder.sh 옆).
# nudge-reminder 는 `exec bash` 로 끝나므로 그 **앞**에 넣어야 한다.
# 훅은 세션 시작에만 읽힌다 — 넣은 뒤 Claude 를 재시작해야 산다.
# ─────────────────────────────────────────────────────────────────────────────
set -u

d=${CLAUDE_PROJECT_DIR:-$PWD}
checker="$d/scripts/ko-wordcheck.sh"
[ -f "$checker" ] || exit 0

# 대형 payload(Write 전체 내용 등)에 대비해 읽기 상한을 둔다
payload=$(head -c 100000 2>/dev/null || true)

tool=$(printf '%s' "$payload" | grep -o '"tool_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')
fpath=$(printf '%s' "$payload" | grep -o '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')

case "${tool:-}" in
  Write|Edit|MultiEdit) ;;
  *) exit 0 ;;
esac

# 윈도우에서 훅 JSON 의 경로는 백슬래시가 이스케이프된 채(`C:\\Users\\...`) 온다.
# 그대로 두면 -f 검사가 실패해 훅이 조용히 아무 일도 안 한다. 슬래시로 바꿔 둔다.
# 겹 역슬래시를 먼저 접고, 남은 홑 역슬래시도 바꾼다 — 이스케이프되지 않은 payload 가
# 들어와도 경로가 뭉개진 채 검사기로 흘러가지 않게 한다.
fpath=$(printf '%s' "$fpath" | sed -e 's|\\\\|/|g' -e 's|\\|/|g')

# 한국어 산문만 본다. 코드·설정에까지 짖으면 소음이 되고, 소음이 되면 꺼진다.
case "${fpath:-}" in
  *.md) ;;
  *) exit 0 ;;
esac
[ -f "$fpath" ] || exit 0

# 지적이 길면 컨텍스트만 먹으므로 앞쪽 몇 건으로 자른다.
hits=$(bash "$checker" --brief "$fpath" 2>/dev/null | head -n 8)
[ -n "$hits" ] || exit 0

# JSON 문자열로 이스케이프한다. gsub 치환문의 백슬래시 해석이 구현마다 달라서
# 한 글자씩 이어붙이는 방식을 쓴다 — 문자열 리터럴만 쓰므로 해석의 여지가 없다.
esc=$(printf '%s' "$hits" | awk '
  BEGIN { BS = "\\" }
  {
    line = ""
    n = length($0)
    for (i = 1; i <= n; i++) {
      ch = substr($0, i, 1)
      if (ch == BS)        line = line BS BS
      else if (ch == "\"") line = line BS "\""
      else                 line = line ch
    }
    printf "%s%sn", line, BS
  }')

msg='[ko-wordcheck] 방금 저장한 한국어 문서에 굳은 직역 후보가 있다. 각 자리가 정상 용법인지 직역인지 직접 판단해라 — 자동 치환은 하지 않는다. 목록: .claude/rules/korean-wordlist.md'
printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"%s%sn%s"}}\n' \
  "$msg" '\' "$esc"
exit 0
