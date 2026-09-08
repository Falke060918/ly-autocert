---
name: import
description: "Import an existing local project's code and useful project knowledge into a unified starter-derived repository while preserving the source and both live Claude/Codex runtimes."
---

# 기존 프로젝트 가져오기

.codex/conventions/codex-runtime.md를 읽는다.
대상 프로젝트와 가져올 원본 경로를 확인하고 원본을 읽기 전용으로 취급한다.
사용자가 새 사본을 원하면 기존 대상이나 원본을 덮어쓰지 않는다.

- 원본 Git 상태·추적 파일·제품 구조·빌드 명령을 먼저 확인한다.
- .git, secret/.env, 의존성·빌드 결과, 로컬 인증·캐시를 무차별 복사하지 않는다.
- 제품 코드·테스트·자산·제품 문서는 원본의 의미와 이력을 보존하는 방법으로 가져온다.
- 기존 대상과 충돌하는 파일은 양쪽 차이를 확인해 병합한다. 기존 제품 설정을 조용히 버리지 않는다.
- 현재 대상의 `AGENTS.md`, `CLAUDE.md`, `.claude/`, `.codex/`, `.agents/`는 모두 운영 중인 통합 기계다.
  legacy로 분류하거나 이동·삭제·외부 버전 덮어쓰기를 하지 않는다. 외부의 같은 경로는 라이브에 편입하지 않는다.
  보관할 외부 지침은 외부 폴더에서 직접 `docs/legacy/<외부명>/`에 복사하되 secret을 제외한다.
  대상 템플릿의 파일을 복사한 뒤 원본을 제거하는 방식은 금지한다.
- 병합 전 `python scripts/import-guard.py snapshot --snapshot .codex/tmp/import-<unit>.json`으로
  보호 경로 지문을 남긴다. 이미 있는 snapshot을 덮어쓰지 않는다.
  병합/복사 직후와 커밋 전에 같은 파일로 `python scripts/import-guard.py check --snapshot .codex/tmp/import-<unit>.json`을 실행한다.
  불일치면 커밋을 멈추고 가져오기 전 대상 기계를 보존한 상태로 충돌을 해소한다.
  `-X ours`만으로는 비충돌 변경·새 파일 유입을 막지 못하므로 이 검사를 생략하지 않는다.
- config.toml/hooks.json/identity는 이 프로젝트 소유다. 다른 프로젝트 권한·Notion 페이지를 상속하지 않는다.
- 오래된 에이전트 메모와 할 일을 그대로 자동 작업 큐로 승격하지 않는다.
- docs/constitution.md의 프로젝트 원칙, STATE, 필요 시 MAP에 실제 프로젝트 정보를 반영한다.
  외부 실행기 지침의 유효한 제품 규칙만 사용자 승인 범위에서 제품 원칙에 병합한다.
- 원본의 라이선스와 출처 표기를 유지한다.
- 실제 빌드·테스트 명령을 실행하고 환경이 없어서 미실행한 부분을 보고한다.
- 요청된 변경만 커밋하고 원격 생성·push는 승인 범위에 따로 포함될 때만 한다.

중요한 제품 선택만 확인한다. 명확히 요청된 가져오기와 되돌릴 수 있는 파일 정리는 재확인하지 않는다.
