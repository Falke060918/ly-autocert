#!/usr/bin/env bash
# nudge-reminder.sh — PostToolUse 훅: 툴 호출 카운터 + 학습 저장 넛지 (design/01-학습.md의 (b))
# 툴 호출을 셀 때마다 카운터를 올리고, 임계값(기본 10회) 도달 시
# "배운 것 저장할까" 상기 문구를 additionalContext로 주입한 뒤 카운터를 리셋한다.
# 학습 저장(learn inbox·GOTCHAS·메모리 파일에 대한 Write/Edit)이 감지되면 즉시 카운터를 리셋한다.
# 넛지에는 이번 세션의 토큰 요약 1줄을 덧붙인다 — 관측치(.claude/perf/<세션>.jsonl)를
# 아무도 안 보는 문제를 세션 중에 보이게 만드는 용도다. 집계 실패는 조용히 생략한다.
set -u

d=${CLAUDE_PROJECT_DIR:-$PWD}
dir="$d/.claude/perf"
counter="$dir/learn-counter"
threshold=${NUDGE_THRESHOLD:-10}

mkdir -p "$dir" 2>/dev/null || exit 0

# stdin의 훅 JSON — 대형 payload(Write 전체 내용 등)에 대비해 읽기 상한을 둔다
payload=$(head -c 100000 2>/dev/null || true)

tool=$(printf '%s' "$payload" | grep -o '"tool_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')
fpath=$(printf '%s' "$payload" | grep -o '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')

# 세션 id — perf-log.sh 와 같은 방식(첫 매치가 곧 진짜 필드)으로 훅 JSON에서 뽑는다.
# 아래 세션 경계 리셋과 token_summary 가 함께 쓴다.
sid=$(printf '%s' "$payload" \
  | grep -o '"session_id"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 \
  | sed 's/.*"\([^"]*\)"$/\1/' | tr -cd 'A-Za-z0-9-')

# 세션 경계 리셋: 직전 호출과 세션 id가 다르면 카운터를 0부터 다시 센다.
# 카운터는 세션 밖에서는 의미가 없는데 파일로 남아, 안 그러면 새 세션 초반에
# 이전 세션 누적분으로 난데없이 넛지가 뜬다.
# 세션 id를 못 읽으면 아무것도 하지 않고 기존 동작으로 흘러간다 — 훅은 매 툴
# 호출마다 도므로 여기서 깨지면 안 된다.
sidfile="$dir/learn-counter.sid"
if [ -n "${sid:-}" ]; then
  prev=$(head -c 200 "$sidfile" 2>/dev/null | tr -cd 'A-Za-z0-9-')
  if [ "${prev:-}" != "$sid" ]; then
    printf '0\n' > "$counter" 2>/dev/null
    printf '%s\n' "$sid" > "$sidfile" 2>/dev/null
  fi
fi

# 저장 감지: 학습 관련 파일 Write/Edit → 카운터 리셋 후 종료
case "${tool:-}" in
  Write|Edit)
    case "${fpath:-}" in
      *docs/learn/*|*docs/GOTCHAS.md|*MEMORY.md|*/memory/*)
        printf '0\n' > "$counter" 2>/dev/null
        exit 0
        ;;
    esac
    ;;
esac

# 토큰 요약 1줄 — 이번 세션의 위임 건수와 서브에이전트 출력 토큰 합.
# 데이터원은 perf-log.sh 가 적재하는 .claude/perf/<session_id>.jsonl 의 SubagentStop 줄.
# 세션 id·로그·집계 중 무엇이 없거나 실패하면 빈 문자열을 돌려주고 넘어간다.
token_summary() {
  [ -n "${sid:-}" ] || return 0
  [ -s "$dir/$sid.jsonl" ] || return 0
  awk '
    /"event":"SubagentStop"/ {
      n++
      if (match($0, /"tok_out":[0-9]+/)) out += substr($0, RSTART + 10, RLENGTH - 10) + 0
    }
    END {
      if (n == 0) exit 0
      if (out >= 1000)   printf " 이번 세션 위임 %d건 · 출력 토큰 약 %dk.", n, int(out / 1000)
      else if (out > 0)  printf " 이번 세션 위임 %d건 · 출력 토큰 약 %d.", n, out
      else               printf " 이번 세션 위임 %d건.", n
    }' "$dir/$sid.jsonl" 2>/dev/null
}

count=0
[ -f "$counter" ] && count=$(tr -dc 0-9 < "$counter" 2>/dev/null | head -c 9)
count=${count:-0}
count=$((count + 1))

if [ "$count" -ge "$threshold" ]; then
  printf '0\n' > "$counter" 2>/dev/null
  summary=$(token_summary 2>/dev/null) || summary=
  # 문구는 포맷 문자열이 아니라 인자로 넘긴다 — 문구에 '%'가 섞이면 printf가 깨진다.
  msg='[learn-nudge] 툴 호출이 임계값에 도달했다. 이번 작업에서 배운 것(규칙감·스킬감·GOTCHA 후보)이 있으면 지금 저장을 고려하라 — docs/learn/ 수신함 또는 GOTCHA 후보로. 저장할 것이 없으면 그대로 진행해도 된다. 카운터는 리셋됐다.'
  printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"%s%s"}}\n' \
    "$msg" "${summary:-}"
  exit 0
fi

printf '%s\n' "$count" > "$counter" 2>/dev/null
exit 0
