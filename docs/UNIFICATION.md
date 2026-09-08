# 통합 기록 — Duet Starter

이 문서의 기본 본문은 Duet 원본 `c7e1557dd900ecead868085fc40dc435e15d3875`의 통합 기록이다.
원본 저장소의 이력·원격·실행 결과를 INHERIT_AutoCert에서 재현했다는 뜻은 아니다.
이 프로젝트의 이행 범위와 검증은 `docs/specs/ATC-20260908-002-duet-migration.md`를,
프로젝트별 보존·복구 절차는 맨 아래 “INHERIT_AutoCert 갱신과 복구”를 따른다.

## 사용자 선택과 구조

2026-09-07 사용자 요청: "claude starter 와 같은 방식으로 진행하고 통합 starter 만들어."
토큰 최소화를 위한 축약판 대신 Claude의 오케스트레이터·실행 위임·검토 흐름을 유지한다.
Codex 변환본의 Git 이력에서 새 저장소를 만들고 Claude의 추적된 설정·역할·스킬·스크립트를 병합했다.
원본 두 저장소는 수정하지 않는다. claude-source/codex-source 원격은 읽기용이며 push URL은 DISABLED다.
최초 통합 작업에는 원격 게시와 기존 원본의 교체를 포함하지 않았다.
2026-09-07 후속 이름 변경·게시 요청으로 ai-starter-v2를 Duet Starter(duet-starter)로 바꾸고,
GitHub 게시 대상은 비공개 `Falke060918/duet-starter`로 정했다. 기존 원본은 그대로 보존한다.

## 실행기별 연결

| 구분 | Claude | Codex |
| --- | --- | --- |
| 프로젝트 지침 | CLAUDE.md | AGENTS.md |
| 메인 역할 로딩 | settings.json의 agent: orchestrator | config.toml의 developer_instructions 전문 |
| 관리 역할 원본 | .claude/agents/orchestrator.md | .codex/roles/orchestrator.md |
| worker | .claude/agents/ | .codex/agents/*.toml + .codex/roles/ |
| 스킬 | .claude/skills/ 17개 | .agents/skills/ 17개 |
| 훅 | .claude/settings.json | .codex/hooks.json |
| 충돌 스크립트 | 원래 파일 이름 | codex- 접두사 |

Codex 역할 생성은 `python scripts/sync-orchestrator.py`, 일치 검사는 `--check`다.
생성기는 관리 블록만 갱신하고 그 밖의 모델·권한·사용자 설정을 보존한다.
원래 시스템 프롬프트를 교체하는 model_instructions_file은 사용하지 않는다.
신뢰되지 않은 프로젝트에서는 설정이 로딩되지 않을 수 있으므로 AGENTS가 필수 읽기 절차를 제공한다.
worker는 메인 역할을 되풀이하지 않고 자신의 역할을 수행한다. 필요한 도구가 없으면 한계를 보고한다.

## 유지하는 Claude 방식

- 메인은 계획·분해·위임·검토·통합을 맡는다. 작은 실행도 경량 위임한다.
- 위임장에는 사용자 기준 텍스트·성공 기준·금지 범위·산출물/파일 소유권을 전달한다.
- M급 이상은 입력을 나눈 3자 검토와 판정 기록, UI 전용은 1명 검토 등 원본의 구분을 따른다.
- 이미 승인한 범위를 반복 질문하지 않는다. 새로운 목표·비용·권한까지 자동 승인하지 않는다.
- handoff로 STATE를 저장하고 다음 도구가 같은 기록에서 이어간다.

## 공유와 격리

코드, STATE, MAP, GOTCHAS, constitution, 제품 스펙은 공유한다.
identity는 각 실행기가 기존 경로에서 읽도록 두되 공통 프로젝트 필드는 동일하게 유지한다.
SETUP은 두 identity를 함께 초기화하며 원본 Notion ID를 이어받지 않는다.
로그·대화 인덱스·압축 스냅샷·리뷰 산출물·automode는 실행기별로 분리한다.
다른 실행기의 대화를 자동 import하거나 두 모델을 동시에 호출하지 않는다.

동명 스크립트 중 내용이 다른 10개 Codex 파일에 codex- 접두사를 붙였다:
compact-resume.sh, compact-snapshot.sh, index-sessions.py, ko-wordcheck.sh,
perf-report.sh, session-markers.sh, skill-health.sh, stylelint-fonts.sh,
sync-manifest.sh, sync-manifest.deletions.
starter.py는 Codex 전용, odiff-compare.sh와 stylelint-fonts.config.json은 동일 내용이라 공유한다.

## 초기화와 업데이트

초기화는 기존 이력·제품 파일을 보존하는 SETUP을 따른다. 마커만으로 삭제하거나 외부 서비스를 만들지 않는다.
두 sync 진입점은 통합 템플릿의 양쪽 운영 파일을 함께 갱신한다. 단독 Claude/Codex 템플릿은 거부한다.
docs, README, 양쪽 identity·설정·훅·로컬 산출물, MCP/프로젝트 CI는 보존한다.
역할 문서가 바뀌어 config 생성 블록이 낡으면 갱신기는 완료 대신 필요한 조치를 알린다.
역할 변경을 검토하고 sync-orchestrator를 실행한 뒤 검증한다. 사용자 custom developer_instructions는 자동 덮어쓰지 않는다.

## 동일하지 않은 플랫폼 기능

- Codex의 메인 비구현 규칙은 행동 지침이다. Claude 역할의 tools 목록처럼 물리적인 도구 제거를 보장하지 않는다.
- 권한과 trust는 각 실행기가 관리한다. Claude의 permissions JSON을 Codex TOML로 그대로 복사하지 않는다.
- Codex 모델/effort는 사용자 설정을 상속한다. Claude 공급자 모델명을 강제하지 않는다.
- Claude 배너의 ORCA-ENV/ORCA-CHILD/ORCA-APP-* 마커는 Codex에 이식하지 않았다.
  Codex는 실제 제공된 환경·사용자 요청과 설치된 Orca 스킬로 연결·역할을 확인한다.
- Claude의 종료 학습 훅은 원본처럼 조건부 claude -p 호출을 할 수 있다.
  Codex 종료 훅은 검토할 세션 포인터를 남기며 자동으로 Claude나 별도 모델을 호출하지 않는다.
- 전체 오케스트레이터 기본 주입은 축약 Codex starter보다 시작 컨텍스트가 커진다. 이번 사용자 선택에 따른 차이다.

## 검증 범위

`python scripts/validate.py`, `python -m unittest discover -s tests -v`로 설정·경로·상태·복구·동기화를 확인한다.
`python scripts/sync-orchestrator.py --check`는 실제 config와 역할 원문의 일치를 확인한다.
실제 모델 호출·계정 로그인·사용자의 프로젝트/훅 신뢰·외부 서비스는 자동 시험하지 않는다.
모델 입력 렌더 확인과 실제 에이전트 행동 검증은 구분해서 보고한다.

### 2026-09-07 실행 결과

- Windows / Codex CLI 0.153.2에서 `sync-orchestrator.py --check` 통과: config의 메인 지침과 역할 전문이 일치한다.
- `validate.py` 통과: Codex 스킬 17개·worker 4개·훅 이벤트 7개, Claude 스킬 17개·훅 이벤트 8개,
  Bash 스크립트 24개 문법과 양쪽 identity 공통 필드를 확인했다.
- `python -m unittest discover -s tests -v`: 28개 전부 통과(37.871초).
  임시 Git fixture에서 양쪽 업데이트 진입점, 사용자 설정 보존, 삭제 보호, 경로 공백,
  역할 설정 드리프트, 공유 STATE와 분리된 automode·스냅샷·로그·인덱스를 확인했다.
  import 전후 지문 검사로 양쪽 라이브 지침 삭제·변조와 실제 Git merge의 외부 runtime 유입도 검출했다.
- 격리된 임시 CODEX_HOME에서 `codex debug prompt-input`으로 입력을 렌더했다.
  최종 orchestrator 전문이 정확히 한 번 포함되고 저장소 스킬 이름 17개가 모두 발견됐다.
  설정값은 approval_policy=never, sandbox_mode=workspace-write였다.
  실제 모델 추론·사용자 trust 저장소 수정 없이 확인한 결과이며 유료 세션의 행동 보장은 아니다.
- 원본 두 저장소의 Git 작업 트리는 깨끗하다. 통합본은 비밀 파일·실행기별 런타임 산출물의
  ignore 규칙과 diff 공백 검사를 확인했다. 외부 게시와 실제 Claude/Codex 모델 세션은 실행하지 않았다.

## 공식 근거

- [Codex 설정](https://learn.chatgpt.com/docs/config-file/config-sample): developer_instructions와 기본 시스템 교체의 차이.
- [Codex 하위 에이전트](https://learn.chatgpt.com/docs/agent-configuration/subagents): 역할별 지침과 설정 계층.
- [Claude 기능 로딩](https://code.claude.com/docs/en/features-overview): 기본 지침과 필요 시 읽는 스킬의 구분.

## INHERIT_AutoCert 갱신과 복구

이 저장소는 기존 제품과 Git 이력을 유지한 파생 프로젝트다. Claude와 Codex identity는
project/done, origin은 INHERIT_AutoCert, work-order-prefix는 ATC이며 template은 Duet이다.
프로젝트 고유 명령 카드 어댑터를 포함하므로 양쪽 스킬은 각각 18개다.
원본 운영 파일은 공식 updater로 갱신하고, 프로젝트 고유 규칙은 `docs/constitution.md`에서
양쪽 실행기가 함께 읽는다. 새 역할·설정·훅은 새 세션에서 로딩을 확인한다.

갱신 전 기존 미커밋 작업을 보존하고, 변경할 파일의 목록·기존 존재 여부·SHA256과
원본 백업을 먼저 남긴다. 부모가 더티하면 자동 stash/reset하지 않고 격리된 깨끗한
작업트리에서 갱신·검증한 뒤, 검토된 이번 변경만 반영한다.
부모의 기존 `.codex/tmp/`, `.codex/recordings/`, `.claude/tmp/`와 리뷰 산출물은
gitignore가 추가돼 목록에서 사라져도 삭제하지 않는다.

두 updater는 docs·README·identity·설정·훅·프로젝트 CI를 보호한다. 실행 후 diff에서
AGENTS/CLAUDE의 프로젝트 헌법 연결, `.gitattributes`의 PS1 CRLF·제품 아카이브 제외,
`.gitignore`의 Next 자동 지침 차단·TLS 비밀 보호와 프로젝트 추가 자산이 유지됐는지 확인한다.
역할 변경은 검토 후 `python scripts/sync-orchestrator.py`로 관리 블록만 갱신하고
`--check`와 `python scripts/validate.py`를 실행한다. 실제 권한과 trust는 바꾸지 않는다.

공식 updater 출력의 `git reset --hard HEAD`나 백업 폴더 일괄 삭제 예시는 이 프로젝트의
복구 지시가 아니다. 기존 사용자 작업이 있으므로 그대로 실행하지 않는다.
실제 복구 명령을 실행하기 전에 백업 목록과 현재 파일 해시를 대조하고, 이번에 바꾼
파일만 백업에서 선택해 복원한다. 새 파일은 반영 이후 사용자 변경이 없음을 확인한
대상만 제거한다. 목록 밖 제품·미커밋 파일과 기존 임시자료는 유지한다.
복구 후 허용한 변경 외의 파일 해시를 다시 대조한다. 전체 reset·clean·재귀 삭제와
사용자 변경을 포함하는 묶음 커밋을 복구 수단으로 쓰지 않는다.

이번 이행의 로컬 백업·보호 해시·반영 목록은
`.codex/tmp/duet-migration/implementation/`에 있다. 공식 updater 백업은 실행한
작업트리의 `.sync-manifest-backup/`에 남긴다. 백업이 없거나 소유권을 확인할 수 없는
파일은 임의로 덮거나 삭제하지 않고, 그 파일을 보존한 상태로 통합 담당자에게 보고한다.
