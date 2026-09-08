---
name: automode
description: "Set or report local unattended workflow mode when the user wants fewer progress questions or wants interactive decisions again."
---

# 무인 모드

`.codex/conventions/codex-runtime.md`를 읽는다.
요청에 on/off가 있으면 `python scripts/starter.py automode on` 또는 `off`를 실행한다.
인자가 없으면 `python scripts/starter.py automode`로 현재 값을 보고한다.
파일은 메인 체크아웃의 .codex/automode이며 Git에 넣지 않는다.

on은 승인된 범위의 진행 질문만 줄인다. 범위·권한·외부 쓰기를 승인하지 않는다.
Codex 실행 권한 승인창은 .codex/config.toml의 approval_policy가 제어하며 이 스킬은 그 값을 바꾸지 않는다.
