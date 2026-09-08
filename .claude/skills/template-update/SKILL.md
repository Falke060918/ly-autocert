---
name: template-update
description: "Update a derived project from its unified Claude/Codex template when the user requests a template update; preserve project settings and documents."
---

# /template-update — 통합 템플릿 갱신

먼저 .claude/conventions/unified-runtime.md를 읽는다.
양쪽 identity가 project인 파생 저장소에서만 수행한다. template 원본에서는 실행하지 않는다.
메인은 아래 실행을 implementer에게 위임한다. 더티 트리를 자동 stash하거나 삭제하지 않는다.

1. git status와 template 원격을 확인한다. 원격이 없으면 identity의 template 주소를 사용한다.
   둘 다 없으면 주소를 한 번 요청한다. 임의 주소나 단독 starter 주소를 대신 쓰지 않는다.
2. `bash scripts/sync-manifest.sh [<template-url>]`를 실행한다.
   양쪽 AGENTS/CLAUDE/SETUP, scripts/tests, Claude와 Codex 역할·규칙·스킬·규약을 함께 갱신한다.
   Codex의 `scripts/codex-sync-manifest.sh`도 같은 통합 운영 경로를 갱신한다.
3. docs 전체, README, 양쪽 identity·automode·세션 산출물, Claude settings*.json,
   Codex config.toml·hooks.json·local-notes.md, .mcp.json, 프로젝트 CI는 보존한다.
   프로젝트가 추가한 스킬도 보존한다. 변경 전 백업과 삭제 manifest의 보호 검사를 확인한다.
4. 역할 변경으로 생성된 Codex 설정이 낡으면 sync가 실패를 알린다.
   역할 변경을 검토한 뒤 `python scripts/sync-orchestrator.py`로 관리 블록만 갱신한다.
   관리되지 않은 사용자 developer_instructions는 덮어쓰지 않는다.
   `python scripts/sync-orchestrator.py --check`와 관련 검사가 통과하기 전에는 완료로 보고하지 않는다.
5. `git diff --cached`와 `git diff`를 모두 검토하고 `python scripts/validate.py`를 실행한다.
   부분 실패는 완료 커밋을 만들지 않고 실패한 경로와 복구 방법을 알린다.
   되돌리기는 정확한 변경 파일과 백업을 대조해 수행하며 reset --hard를 자동 실행하지 않는다.
6. 성공한 변경만 커밋하고 승인된 경우에만 push한다. 백업 위치와 보존 항목을 보고한다.
   설정·역할·훅 변경은 각 실행기의 새 세션에서 로딩을 확인한다.

템플릿 갱신 요청만으로 사용자 권한·sandbox·trust 정책을 넓히지 않는다.
