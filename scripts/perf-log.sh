#!/usr/bin/env bash
# 훅 수집기 — PreToolUse/PostToolUse/SubagentStop 훅의 stdin(JSON)에서
# timestamp + tool_name + agent를 뽑아 세션별 JSONL로 한 줄 append 한다.
#   기록 위치: <저장소>/.claude/perf/<session_id>.jsonl
# 이 로그는 scripts/perf-report.sh 가 읽는다. 로그가 없어도 리포트는
# transcript만으로 동작한다(degrade) — 이 스크립트는 부가 데이터원일 뿐이다.
#
# !! 절대 도구 실행을 막지 않는다 — 어떤 실패든 항상 exit 0 !!
# 매 도구 호출마다 실행되므로 외부 의존성(jq 등) 없이 순수 sh로만 뽑는다.
#
# 필드 추출이 grep 부분 문자열로 충분한 이유(fanout.sh와 같은 전제):
# 훅 JSON에서 session_id/hook_event_name/tool_name 은 사용자 데이터가 담기는
# tool_input 보다 앞에 오고, 문자열 값 내부의 따옴표는 \" 로 이스케이프되므로
# "필드명":" 리터럴의 첫 매치는 항상 진짜 필드다.

in=$(head -c 65536 2>/dev/null) || in=

grab() { printf '%s' "$in" | grep -o "\"$1\":\"[^\"]*\"" | head -n 1 | cut -d'"' -f4; }

sid=$(grab session_id | tr -cd 'A-Za-z0-9-')
[ -n "$sid" ] || sid=unknown
ev=$(grab hook_event_name)
tool=$(grab tool_name)
ag=$(grab agent_type)
[ -n "$ag" ] || ag=$(grab subagent_type)

d=${CLAUDE_PROJECT_DIR:-$PWD}/.claude/perf
mkdir -p "$d" 2>/dev/null || exit 0
# 파생 프로젝트의 .gitignore 를 건드리지 않고 디렉터리를 자체 gitignore 한다.
[ -f "$d/.gitignore" ] || printf '*\n' > "$d/.gitignore" 2>/dev/null

printf '{"ts":"%s","event":"%s","tool":"%s","agent":"%s"}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$ev" "$tool" "$ag" >> "$d/$sid.jsonl" 2>/dev/null
exit 0
