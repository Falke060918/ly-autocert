# Codex 단위별 진행 기록

Claude의 단위별 단계·소유권 규약을 유지한다. **Codex 호스트 상태줄에 자동 표시되는 기능은 아니다.**
원본 Claude statusline.sh는 호출하지 않는다. 이 파일은 보조 신호이며 판정/커밋 증거를 대신하지 않는다.

## 위치·이름

`<메인 저장소 루트>/.codex/perf/units/<unit>.status`에 단위마다 한 파일을 둔다.
메인 루트는 `git rev-parse --path-format=absolute --git-common-dir`의 절대 경로를 확인하고
마지막 `.git` 디렉터리의 부모로 잡는다. common-dir가 `.git`이 아닌 별도 Git 디렉터리이거나
bare repo이면 문자열을 잘라 추정하지 말고 위임장에 명시된 메인 루트를 사용한다.
위임장은 linked worktree에도 메인 루트를 전달한다. 위치를 확인할 수 없으면 상태 쓰기는 건너뛴다.

unit은 위임장의 Work Order 하위 ID 그대로, 없으면 명시된 lowercase kebab-case 이름이다.
ASCII 영문·숫자·하이픈만 허용하고 경로 구분자/점/공백을 거부한다. ID 이외 대문자는 쓰지 않는다.
위임 이름이 없으면 경량/planner는 조용히 건너뛴다. 임의로 이름을 바꾸지 않는다.
동시에 같은 unit을 검토하는 세 reviewer는 상태 파일만 `<unit>-planner|order|user.status`로 분리한다.
판정 파일의 unit과 접미사는 verdict-protocol을 따른다.

## 형식 — 원본과 같은 5필드

```text
<stage>|<unit>|<epoch-seconds>|<작업 내용 한 줄>|<source>
```

- stage: `probe` → `write` → `verify` → `done` → `landed`, 실행 실패는 `fail`.
- source: `main`, `sub`, `sub@<worktree>`, `child@<worktree>`만 쓴다.
- 작업 내용은 "검증 중" 같은 단계명이 아니라 실제 작업(예: 로그인 폼 검증 추가)이다. fail이면 막힌 이유.
- note 안 `|`와 줄바꿈은 공백으로 정리한다. source 안 공백/파이프/줄바꿈을 허용하지 않는다.
- 각 전환 때 자기 파일 전체를 한 줄로 덮어쓴다. 도구 편집 규칙에 맞게 apply_patch를 쓴다.
- local-only인 `.codex/perf/`는 commit하거나 force-add하지 않는다.

## 소유권·관측

implementer는 probe/write/verify/done/fail, explorer는 probe/done, reviewer는 verify/done/fail을 쓴다.
검토가 끝나 fail 판정이 나온 것은 실행 실패가 아니므로 상태는 done, 판정은 fail이다.
planner는 상태를 쓰지 않는다. land 실행 implementer만 실제 merge 뒤 landed를 기록한다.
Orca proxy는 child-first: 자식이 done/fail을 남겼으면 덮어쓰지 않고, 자식 종료 실패 때만 보조 기록한다.

상태 쓰기는 best-effort다. 실패하면 실제 작업을 계속하며 반복 시도/검증 실패로 만들지 않는다.
상태 파일이 없다는 이유만으로 실패·미착수를 단정하지 않는다.
원본의 화면 표시 만료(진행 3600초, 종료 60초)는 별도 reader를 만들 때의 렌더 규약이다.
만료는 파일 삭제가 아니며, 현재 이 저장소는 Codex 상태줄 reader를 제공하지 않는다.
