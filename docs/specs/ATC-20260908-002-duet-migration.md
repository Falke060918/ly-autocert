# ATC-20260908-002 — Duet 템플릿 전환

구현 단위: `ATC-20260908-002-A`

## 사용자 원문

> 템플릿도 duet 으로 변경해서 업데이트해.

앞선 역할 요구: 메인 세션은 오케스트레이터로 동작하고, 구현·검토는 서브에이전트와
워크트리로 분리한다. 이 전환은 이미 승인된 작업이다.

## 성공 기준

1. 공식 Duet `c7e1557dd900ecead868085fc40dc435e15d3875`의 양쪽 운영 파일과
   업데이트 경로를 이식한다. identity는 project/done, origin은 원래 프로젝트,
   work-order-prefix는 ATC이며 template은 Duet이다.
2. Codex의 전체 메인 역할 주입·worker 등록·Windows/POSIX 훅을 초기화하고,
   기존 Claude 설정·권한을 그대로 보존한다. 정적 파싱, 관리 역할 동기화 검사,
   marker, 공식 validator와 관련 테스트를 통과한다.
3. 제품·설치파일·미커밋 작업·프로젝트 추가 스킬과 스크립트·비밀·CI를 보존한다.
   원본 규칙 백업, 파일 해시와 복구 절차를 남긴다.

## 범위 외

제품 코드·기능·D/E 실장비·인증서 변경, 전역 설정·trust 변경, 권한 확대,
템플릿 원본 변경·push, 기존 사용자 작업 stash/reset/묶음 commit,
기존 Orca 작업트리 삭제는 하지 않는다. 설치파일 재생성도 이 단위에 포함하지 않는다.

## 가정과 병합 판단

- 처음 생성하는 Codex config는 원본의 never/workspace-write/network=false를 쓴다.
  이는 새 세션의 보수적 프로젝트 기본값이다. 현재 하네스의 실제 권한은 상위 설정이
  결정하며 프로젝트 config가 현재 세션의 권한이나 역할을 소급 변경하지 않는다.
- 신뢰 저장소를 수정하지 않는다. hooks와 전체 역할의 실제 자동 로딩은 새 세션에서
  확인해야 하며 정적 검사만으로 실제 로딩 완료를 주장하지 않는다.
- Notion 연결은 확인된 것이 없어 pending으로 기록하고 외부 서비스를 만들지 않는다.
- 원본 프로젝트 규칙 36줄은 `docs/constitution.md`에 보존한다. 과거 Phase 1 제한이
  현재 사용자 승인·제품 명세와 상충한다는 판정을 함께 기록하고 제품 범위는 재정의하지 않는다.
- 프로젝트 전용 cheatsheet를 보존하며 Codex에는 출력 전용 어댑터를 추가한다.
- 기존 PS1 CRLF·SH LF·제품 archive 제외 규칙과 Next/TLS gitignore 규칙을 보존한다.
- Git remote 설정은 작업트리 간 공유된다. 검토 전 updater는 실행 프로세스의
  url.insteadOf 설정으로 Duet을 읽고, 실제 template URL 변경은 검토 후 반영 단계에서 한다.

## 구현과 검증 증거

시작 커밋: `a6063dab5f62927afab091731e774cf592b92dfb`.
Orca 작업트리: `duet-migration-20260908`, 브랜치 `Falke060918/duet-migration-20260908`.
공식 updater는 깨끗한 작업트리에서 실행하며 dirty 검사 우회 변수를 설정하지 않는다.
원본 보호 설정을 먼저 별도 bootstrap commit으로 만든 후 updater를 실행한다.

로컬 증거: `.codex/tmp/duet-migration/implementation/`의 `before/`,
`root-before-sha256.json`, `backup-manifest.json`, `updater.log`, `result.md`.
updater 자체 백업은 자식의 `.sync-manifest-backup/`에 있다.
최종 검증 명령·종료 코드·대상 커밋은 result.md에 기록한다.

## 갱신과 복구

향후 양쪽 identity가 project이고 작업트리가 깨끗할 때
`bash scripts/codex-sync-manifest.sh` 또는 `bash scripts/sync-manifest.sh`를 실행한다.
config/hooks/identity/Claude settings/docs는 updater 보호 대상이므로 자동 덮어쓰지 않는다.
갱신 diff에서 AGENTS/CLAUDE의 프로젝트 헌법 연결과 `.gitattributes`의 프로젝트
예외를 보존 병합하고 `python scripts/sync-orchestrator.py --check`를 다시 확인한다.

부모 반영 전에는 자식 브랜치를 그대로 두면 부모 파일은 변하지 않는다.
부모 반영 후 복구가 필요하면 반영 목록과 백업을 대조하여 그 파일만 원본으로 되돌린다.
부재였던 새 운영 파일은 반영 후 사용자 변경이 없는지 해시를 확인한 뒤에만 제거한다.
제품 파일 전체 reset, stash, 디렉터리 재귀 삭제를 복구 수단으로 쓰지 않는다.
template remote만 이전 claude-starter-v2 주소로 되돌리되 origin은 보존한다.
