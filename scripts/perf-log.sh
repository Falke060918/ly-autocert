#!/usr/bin/env bash
# 관측 수집기 — 위임 1건당 시간·토큰 적재 + 재탈락 기록 (design/05 "관측").
# v1(claude-starter) perf-log.sh 이식 + 개선: SubagentStop 토큰 적재, refail 모드 신설.
# 입구가 둘이다:
#
# (1) 훅 모드(기본, stdin=JSON) — PreToolUse/PostToolUse/SubagentStart/SubagentStop
#     훅의 stdin에서 timestamp + tool_name + agent 를 뽑아 세션별 JSONL로 한 줄
#     append 한다. SubagentStop 이면 서브에이전트 토큰(tok_in/tok_out)도 함께 적는다 —
#     메인 transcript 에는 서브에이전트별 토큰이 아예 없으므로(isSidechain 부재,
#     v1 실측) 여기가 유일한 데이터원이다.
#       기록 위치: <저장소>/.claude/perf/<session_id>.jsonl
#     이 로그는 scripts/perf-report.sh 가 읽는다. 로그가 없어도 리포트는
#     transcript만으로 동작한다(degrade) — 이 스크립트는 부가 데이터원일 뿐이다.
#
#     !! 절대 도구 실행을 막지 않는다 — 훅 모드는 어떤 실패든 항상 exit 0 !!
#     매 도구 호출마다 실행되므로 외부 의존성(jq 등) 없이 sh + awk 로만 뽑는다.
#
# (2) refail 모드 — bash scripts/perf-log.sh refail <unit> "<같은 지적 사유 1줄>"
#     같은 지적으로 다시 탈락한 재위임 1건을 <메인 저장소>/.claude/perf/refails.jsonl 에
#     append 한다. "동일 원인 재탈락률"(design/02 검증 지표)의 데이터원이고, 언제
#     부르는지는 .claude/conventions/verdict-protocol.md 가 정한다.
#
# 필드 추출이 grep 부분 문자열로 충분한 이유(fanout.sh와 같은 전제):
# 훅 JSON에서 session_id/hook_event_name/tool_name 은 사용자 데이터가 담기는
# tool_input 보다 앞에 오고, 문자열 값 내부의 따옴표는 \" 로 이스케이프되므로
# "필드명":" 리터럴의 첫 매치는 항상 진짜 필드다.

# ---- (2) refail 모드 — CLI라서 usage 오류는 exit 1 로 알린다. ----------------
if [ "${1:-}" = "refail" ]; then
  unit=${2:-}; cause=${3:-}
  if [ -z "$unit" ] || [ -z "$cause" ]; then
    echo 'Usage: bash scripts/perf-log.sh refail <unit> "<같은 지적 사유 1줄>"' >&2
    exit 1
  fi
  unit=$(printf '%s' "$unit" | tr -cd 'A-Za-z0-9._-')
  # JSONL 한 줄 유지: 줄바꿈·탭은 공백으로, "와 \ 는 '로 눕힌다(이스케이프 대신 치환).
  cause=$(printf '%s' "$cause" | tr '\n\t' '  ' | tr '"\\' "''")
  # 워크트리 안에서 불려도 메인 저장소에 쌓이도록 git common dir로 루트를 고정한다
  # (.claude/conventions/status-protocol 과 같은 앵커 — %.git 다음 %/ 순서 유지).
  root=$(git rev-parse --git-common-dir 2>/dev/null)
  root=${root%.git}; root=${root%/}
  d=${root:-${CLAUDE_PROJECT_DIR:-$PWD}}/.claude/perf
  mkdir -p "$d" || exit 1
  [ -f "$d/.gitignore" ] || printf '*\n' > "$d/.gitignore"
  printf '{"ts":"%s","event":"refail","unit":"%s","cause":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$unit" "$cause" >> "$d/refails.jsonl"
  exit 0
fi

# ---- (1) 훅 모드 -------------------------------------------------------------
in=$(head -c 65536 2>/dev/null) || in=

grab()  { printf '%s' "$in" | grep -o "\"$1\":\"[^\"]*\"" | head -n 1 | cut -d'"' -f4; }
grabn() { printf '%s' "$in" | grep -o "\"$1\":[0-9][0-9]*" | head -n 1 | cut -d: -f2; }

sid=$(grab session_id | tr -cd 'A-Za-z0-9-')
[ -n "$sid" ] || sid=unknown
ev=$(grab hook_event_name)
tool=$(grab tool_name)
ag=$(grab agent_type)
[ -n "$ag" ] || ag=$(grab subagent_type)
# 서브에이전트 개체 고유값(훅 문서: "Present only when the hook fires inside a
# subagent call"). 유형만으로는 같은 유형 둘이 겹쳐 돌 때 시작↔종료를 선입선출로
# 추정할 수밖에 없어서, 리포트가 완전 일치로 짝지으라고 이 칸을 적는다.
# 안 실려 오면 빈 문자열로 남고 리포트는 기존 유형별 선입선출로 물러난다.
aid=$(grab agent_id)

# SubagentStop 완료 통지에서 토큰을 캔다 — 훅 JSON에 숫자 필드가 직접 있으면
# 그걸 쓰고, 없으면 agent_transcript_path 의 서브에이전트 transcript에서 센다:
#   tok_out = 전체 output_tokens 합(생성량)
#   tok_in  = 마지막 usage 줄의 input+cache_creation+cache_read (최종 컨텍스트 크기)
# 둘 다 못 구하면 토큰 필드 없이 적는다(리포트가 // 0 으로 관용한다).
tok=''
if [ "$ev" = "SubagentStop" ]; then
  ti=$(grabn total_input_tokens); to=$(grabn total_output_tokens)
  if [ -z "$ti$to" ]; then
    at=$(grab agent_transcript_path)
    [ -n "$at" ] || at=$(grab agentTranscriptPath)
    if [ -n "$at" ] && [ -r "$at" ]; then
      set -- $(awk '
        /"output_tokens":/ {
          line = $0
          while (match(line, /"output_tokens":[0-9]+/)) {
            out += substr(line, RSTART + 16, RLENGTH - 16) + 0
            line = substr(line, RSTART + RLENGTH)
          }
        }
        /"input_tokens":/ {
          t = 0
          if (match($0, /"input_tokens":[0-9]+/))                t += substr($0, RSTART + 15, RLENGTH - 15) + 0
          if (match($0, /"cache_creation_input_tokens":[0-9]+/)) t += substr($0, RSTART + 30, RLENGTH - 30) + 0
          if (match($0, /"cache_read_input_tokens":[0-9]+/))     t += substr($0, RSTART + 26, RLENGTH - 26) + 0
          lastin = t
        }
        END { printf "%d %d\n", lastin + 0, out + 0 }
      ' "$at" 2>/dev/null)
      ti=${1:-}; to=${2:-}
    fi
  fi
  case "$ti$to" in
    *[!0-9]*|'') ;;                       # 숫자가 아니면 버린다
    *) [ "$((${ti:-0} + ${to:-0}))" -gt 0 ] && tok=",\"tok_in\":${ti:-0},\"tok_out\":${to:-0}" ;;
  esac
fi

# 유형이 빈 SubagentStop 은 위임 종료가 아니다 — 서브에이전트가 살아 있는 동안
# 주기적으로 뜬다(동시 1개면 약 32.5초, 3개면 약 11초 간격). 리포트가 이걸 위임
# 1건으로 세는 바람에 한 세션 311행 중 272행이 유령이었다. 원인 미확인이라 버리지
# 않고 event 를 갈라 적는다 — 증거는 남고 위임 집계에서만 빠진다.
if [ "$ev" = "SubagentStop" ] && [ -z "$ag" ]; then
  ev=SubagentStopNoAgent
fi

d=${CLAUDE_PROJECT_DIR:-$PWD}/.claude/perf
mkdir -p "$d" 2>/dev/null || exit 0
# 파생 프로젝트의 .gitignore 를 건드리지 않고 디렉터리를 자체 gitignore 한다.
[ -f "$d/.gitignore" ] || printf '*\n' > "$d/.gitignore" 2>/dev/null

# aid 는 덧붙인 칸이다 — 앞 네 칸(ts/event/tool/agent)과 꼬리의 토큰 칸은 그대로 둔다.
# 옛 로그에는 이 칸이 없고, 리포트가 없으면 빈 값으로 관용한다(하위 호환).
printf '{"ts":"%s","event":"%s","tool":"%s","agent":"%s","aid":"%s"%s}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$ev" "$tool" "$ag" "$aid" "$tok" >> "$d/$sid.jsonl" 2>/dev/null
exit 0
