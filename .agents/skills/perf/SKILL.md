---
name: perf
description: "Report observed Codex tool durations and incomplete calls from local lifecycle hook logs when the user asks where session time went."
---

# 세션 계측

`.codex/conventions/codex-runtime.md`를 읽고
`python scripts/starter.py perf 1` 또는
`python scripts/starter.py perf --session <세션ID>`를 실행한다.

데이터는 .codex/perf/의 세션별 훅 이벤트다. PreToolUse/PostToolUse를 tool_use_id로 연결한다.
훅 미승인·미지원 환경이나 과거 세션에는 기록이 없을 수 있다. 없는 시간을 추정하지 않는다.
합계는 병렬 실행 때문에 실제 경과 시간보다 길 수 있다.
모델 사고 시간·승인 대기·도구 실행을 이 자료만으로 정확히 나누지 않는다.
Claude JSONL용 계측과 달리 토큰 비용·fanout 점수는 제공하지 않는다.
사용자에게 가장 오래 걸린 관측 호출, 미완료 호출, 자료 한계를 짧게 설명한다.
