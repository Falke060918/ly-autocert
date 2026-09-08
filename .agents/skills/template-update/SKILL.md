---
name: template-update
description: "Update a derived project from its unified Claude/Codex template using the guarded synchronization script when the user requests a template update."
---

# 템플릿 갱신

먼저 .codex/conventions/codex-runtime.md를 읽는다.
양쪽 `.claude/identity`와 `.codex/identity`가 project인 파생 저장소에서만 수행한다. template 원본에서는 중단한다.
git status로 사용자 변경을 확인한다. 더티 트리를 자동 stash하거나 지우지 않는다.

`bash scripts/codex-sync-manifest.sh`를 실행한다.
template remote가 없다면 identity의 template 주소를 사용한다.
둘 다 없으면 임의 주소를 만들지 않고 필요한 주소를 요청한다.
템플릿 소유 AGENTS.md/CLAUDE.md/SETUP.md/.gitattributes/scripts/tests/.agents/skills,
.codex의 rules·roles·agents·conventions와 .claude의 rules·agents·skills·output-styles·conventions를 동기화한다.
Claude의 `scripts/sync-manifest.sh` 진입점도 같은 양쪽 운영 파일을 갱신한다.

스크립트는 깨끗한 트리 검사, 변경 전 백업, 자기 갱신, 삭제 manifest 검사를 수행한다.
docs 전체, README, 양쪽 identity·automode·세션 산출물, .claude/settings*.json,
.codex/config.toml·hooks.json·local-notes.md, .mcp.json과 프로젝트 CI는 갱신하지 않는다.
설정·훅 변경도 요청받았다면 diff를 먼저 확인하고 프로젝트 설정과 병합한다.
자동 업데이트로 sandbox나 approval 정책을 바꾸지 않는다.

역할 문서가 바뀌면 `python scripts/sync-orchestrator.py --check`가 config의 생성 블록과 비교한다.
불일치 시 역할 변경을 검토하고 `python scripts/sync-orchestrator.py`로 관리 블록만 갱신한다.
관리 블록 밖의 사용자 developer_instructions가 있으면 덮어쓰지 않고 중단하므로 직접 병합한다.
검사가 실패한 동기화는 완료로 보고하지 않는다.

갱신 결과 diff와 백업 위치를 확인하고 `python scripts/validate.py` 및 관련 검사를 실행한다.
변경 파일만 커밋한다. push는 승인 범위를 따른다.
hooks/agents/skills 변경은 새 세션에서 로딩을 확인하도록 알린다.
