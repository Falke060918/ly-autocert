---
name: recall
description: "Search earlier Codex conversations using the local rollout JSONL index when the user asks what was discussed or decided in a previous session."
---

# 과거 세션 검색

먼저 `.codex/conventions/codex-runtime.md`를 읽는다.
`python scripts/codex-index-sessions.py index`로 현재 저장소의 기록만 갱신한다.
기록은 CODEX_HOME(기본 ~/.codex)의 sessions/와 archived_sessions/ 아래 JSONL이며,
session_meta.cwd가 현재 프로젝트 경로에 속하는 기록만 포함한다.

1. `python scripts/codex-index-sessions.py search "검색어" --limit 10`
2. 결과의 경로·줄을 `python scripts/codex-index-sessions.py show --file "<경로>" --line <줄>`로 확인한다.
3. 결정과 대화 맥락을 구별해 요약하고 기록의 경로·줄을 근거로 제시한다.

`--project <경로>`는 명시한 프로젝트만, `--all`은 모든 프로젝트다.
다른 프로젝트 검색은 사용자가 요청했을 때만 한다. 추론·도구 결과·비밀을 무차별로 인덱싱하지 않는다.
로컬 DB는 .codex/index/sessions.db이며 커밋하지 않는다. 기록이 없으면 없다고 알린다.
