---
name: import
description: Pulls an existing external local folder — its full product source, git history, and old settings/knowledge — INTO the current derived project (a claude-starter-v2 clone). Runs inside the initialized project (identity: project, setup: done), reads the external folder read-only, grafts its git history via merge, resolves overlaps template-first, migrates rules/state/gotchas into docs/, and archives old settings under docs/legacy/. This is a project-scope skill for a one-time absorption of a local folder you want to keep developing on the template — distinct from /template-update (which SYNCS template machinery into a project) and /harvest (which FEEDS lessons back to the template). Use when the user invokes /import <external absolute path>, or wants to continue developing an existing local folder inside a fresh template project. Refuses to run in the template original or an uninitialized clone.
---

# /import — pull an external folder into the current project (v2, 방향 역전형)

Runs **inside** a template-derived project and pulls an external local folder into it. The direction is the reverse of v1: v1 installed the template machinery into an external repo; v2 keeps the machinery here and **absorbs the external folder's source + git history + old settings/knowledge** into the current project. The external folder is read **read-only** — nothing is ever created, modified, or deleted there.

설계 단일 원본은 `docs/specs/import-skill.md`. User-facing communication is in Korean; the documents written for the user (constitution rules / STATE / GOTCHAS entries) are also in Korean.

확정된 설계 결정 (사용자 승인 완료 — 다시 묻지 않는다): (1) 방향 역전 pull, (2) 외부 제품 소스 전체 + 설정 계층 둘 다 편입, (3) git 이력 보존(graft), (4) 충돌은 템플릿 우선(꼭 필요한 외부 문서만 검토 후 반영), (5) 옛 설정은 3버킷 이관 + `docs/legacy/` 무손실 백업.

Orchestrator mode: delegate reading/investigation to **explorer** and every file write/move/commit to **implementer**, exactly as `/kickoff`·`/clarify` do. The main session composes the migrated document contents itself and puts the full text into the delegation prompt.

## 0. 신분·전제·경계 — 무엇보다 먼저
`.claude/identity`와 세션 마커를 읽어 분기한다. **하나라도 걸리면 아무것도 변형하지 않고 중단**한다.

| 상태 | 행동 |
|---|---|
| identity: project **AND** setup: done | 진행(1절로). |
| identity: project **AND** setup: pending | 중단. "SETUP 초기화를 먼저 완료하라(세션 시작의 PROJECT-REPO SETUP-PENDING 흐름)." |
| identity: template **AND** origin 지문 일치 (TEMPLATE-REPO) | 중단. "템플릿 원본에는 import할 대상이 없다." |
| identity: template **AND** origin 지문 불일치 (SETUP-PENDING 클론) | 중단. "SETUP 1~8단계로 초기화(identity→project)부터 하라." |
| identity 파일 없음/이상값 | 중단. "SETUP.md 1단계로 identity를 생성하라." |

**전제조건(진행 전 필수):**
- 현재 저장소가 git 저장소이고 **작업 트리가 clean**하다 — 아래 병합·checkout·백업 이동이 무경고로 덮어쓴다. dirty면 먼저 commit/stash. 이 clean 상태가 곧 롤백점(`git reset --hard HEAD`)이다.
- 인자로 **외부 폴더 절대경로**가 주어졌고, 그 경로가 **존재하는 디렉토리**다.

**경계조건(오류 처리):**
- 인자 없음 → 사용법(`/import <외부폴더 절대경로>`) 1줄과 함께 경로를 1회 묻고, 없으면 중단.
- 경로가 없거나 디렉토리가 아님 → 오류 보고 후 중단.
- 경로가 현재 프로젝트 루트와 동일 → 중단(자기 자신 import 무의미).
- 외부가 git 저장소가 아님 → 4절 **비-git 폴백**(무이력 복사).
- 외부가 그 자체로 템플릿 파생 프로젝트임 → 정상 처리한다. 외부의 `.claude/`·CLAUDE.md·scripts 등 기계는 템플릿 우선으로 해소(ours), docs/의 constitution 하단·STATE·GOTCHAS는 3버킷으로 1:1 이관한다.

## 1. 외부 설정·지식 조사 — explorer 위임 (3버킷)
explorer에 위임해 외부 폴더의 **설정·지식 계층만** 읽는다(승인 대기 중 읽기 전용이라 선행 가능). source-code 스캔 목적이 아님을 프롬프트에 명시:
- 옛 `CLAUDE.md`(및 `CLAUDE.local.md`가 있으면 **존재만** 확인, 내용은 secret 위험으로 별도 취급), 옛 `.claude/` 전체(settings·commands·agents·rules·skills), `AGENTS.md`·`.cursorrules`/`.cursor/rules` 등 타 도구 규칙, `README.md`·`docs/`의 **컨벤션·상태 절만**.

결과를 3버킷으로 분류해 요약 수령:
1. 프로젝트 고유 규칙·컨벤션 → `docs/constitution.md` 하단 "이 프로젝트의 원칙"
2. 진행 상태/todo → `docs/STATE.md`
3. 함정(한 번 시간 잃은 것 + 회피법) → `docs/GOTCHAS.md`

동시에 explorer는 각 외부 파일을 **제품 계층(유지)** vs **설정·지식 계층(→legacy+버킷)**으로 잠정 분류한다 — 이 분류는 3절 관문에서 사용자가 교정한다(가정을 묻지 않고 정하지 않는다).

## 2. 겹침(충돌) 검출 — 읽기 전용
외부 트리의 파일 목록과 현재 트리의 파일 목록을 비교해 **겹치는 경로**를 세 부류로 분류한다:
- **(T) 템플릿 기계 경로** (`CLAUDE.md`·`SETUP.md`·`scripts/`·`.claude/{rules,agents,skills,output-styles,conventions,settings.json}`·`.claude/identity`) → **템플릿 우선(무음)**.
- **(K) 지식·문서 경로** (`README.md`·`docs/`·`AGENTS.md`·`.cursorrules`) → **검토 대상**: 원본은 `docs/legacy/`, 내용은 3버킷/승인 반영.
- **(P) 그 외 제품 소스 겹침** → 스택 중립 템플릿이라 드묾. 기본 템플릿 우선이되 **관문에 개별 나열**해 사용자가 반영을 지정할 수 있게 한다.

외부가 git이면 `git ls-tree -r --name-only import-src/<branch>`, 아니면 폴더 walk로 목록을 얻는다.

## 3. 승인 관문 — AskUserQuestion (쓰기 전 필수)
**어떤 병합·이동·쓰기보다 먼저** 단일 AskUserQuestion으로 계획을 제시한다(다른 스킬과 동일한 승인-우선 관례). 제시 항목:
1. **소스·이력**: 외부 경로, git 저장소 여부(이력 보존 가능/폴백), 대상 브랜치, 가져올 파일 수 개요, 외부 트리 dirty 여부.
2. **분류 확인**: 어떤 외부 경로가 제품 계층(유지)이고 어떤 것이 설정·지식 계층(→legacy+버킷)인지 목록 — 사용자가 오분류를 교정.
3. **충돌 목록**: 겹치는 경로 중 템플릿 우선으로 유지될 것 + 그중 "검토 후 반영" 후보(README 본문·아키텍처 노트·docs 산문).
4. **3버킷 이관 계획**: 규칙→constitution 하단 / 상태·todo→STATE / 함정→GOTCHAS.
5. **legacy 백업 목록**: `docs/legacy/`에 무손실 보관될 옛 설정 원본.
6. **제외 목록**: `.env`·`.env.*`(단 `.env.example` 유지)·`*.local.*`·secret 의심·외부 `.git/` 내부.
7. **한 줄 고지**: 외부 폴더에는 아무것도 쓰지 않는다(읽기만), 이력 결합 시 과거 커밋의 secret이 이력에 남을 수 있어 발견되면 **키 회전 필요**.

선택지는 최소 **승인 / 수정 요청 / 중단**. 승인 전 4~7절은 아무것도 실행하지 않는다.

**[3계층(멈춤선)]** 이 관문은 무인 모드(`/automode on`)에서도 유지된다 — 이력 결합과 legacy 이동은 되돌리기 어렵고, 무인은 승인 관문을 대체하지 않는다. 계층 정의는 `.claude/agents/orchestrator.md`.

## 4. 소스 + 이력 병합
승인 후 implementer에 위임. **외부가 git 저장소일 때(이력 보존 경로):**
```
EXT="<외부폴더 절대경로>"                       # 인자
BR=$(git -C "$EXT" symbolic-ref --quiet --short HEAD || echo DETACHED)
git remote add import-src "$EXT"                # 로컬 경로도 remote 로 동작
git fetch --no-tags import-src                  # 외부 이력·객체 수령 (--no-tags: 태그 오염 방지)
git merge --no-commit --no-ff --allow-unrelated-histories -X ours import-src/"$BR"
# DETACHED 면: git fetch import-src <hash> 후 마지막 인자를 FETCH_HEAD 로.
```
**⚠️ `-X ours`(대문자 X, 병합 전략 옵션)이지 `-s ours`(소문자, 병합 전략)가 아니다.** `-X ours`는 충돌 구간만 템플릿(ours)으로 해소하고 외부 고유 파일은 전부 편입한다. `-s ours`는 외부 트리를 **통째로 버려 아무것도 안 가져오는 무음 실패**가 된다 — 절대 혼동 금지.
- `--allow-unrelated-histories`: 공통 조상 없는 외부 이력을 결합(=graft). 결과 merge commit의 두 부모로 외부 이력이 `git log`에 살아난다.
- `--no-commit`: 커밋 직전 멈춰 secret 제외·승인 문서 반영을 끼워 넣을 창을 만든다.

**병합 후 커밋 전:**
- **제외 적용**: `.env`·`.env.*`(단 `.env.example` 유지)·`*.local.*`·secret 의심 경로가 스테이지됐으면 `git rm --cached -r <경로>` 후 `rm -rf <경로>`.
- **승격된 설정·지식 파일 정리**: 관문에서 legacy+버킷으로 분류된 외부 설정 파일(예: 루트에 편입된 `AGENTS.md`·`.cursorrules`)은 `docs/legacy/`에 보관 후 라이브 트리에서 제거 — 템플릿 기계가 그 역할을 대신한다.
- **승인된 외부 문서 반영**: README 본문·아키텍처 노트 등 관문 승인분을 외부 측에서 가져와 반영.
- 커밋: `chore: /import <외부명> — 소스·이력 병합 (외부→현재, 템플릿 우선)` (`<외부명>`=외부 경로 basename). 이후 `git remote remove import-src`.

**비-git 폴백(외부가 git 저장소가 아님):**
- 이력이 없으므로 보존 불가. 겹치지 않는 파일만 복사(기존 파일 보존=템플릿 우선), 제외 규칙 적용. 예: `rsync -a --ignore-existing --exclude '.git' --exclude '.env' --exclude '.env.*' --exclude '*.local.*' "$EXT"/ ./` (rsync 부재 플랫폼이면 이식 가능한 no-clobber 복사로 대체 — 구현자 재량).
- 단일 커밋 `chore: /import <외부명> — 소스 복사 (외부 비-git, 이력 미보존)`. 보고에 "이력 미보존(외부가 git 아님)" 1줄.

## 5. 설정 원본 legacy 백업 + secret 제외
implementer에 위임. 외부 **폴더에서 직접**(병합 결과가 아니라 — 겹친 설정은 ours로 버려졌으므로) 옛 설정 원본을 `docs/legacy/`로 무손실 복사: 옛 `CLAUDE.md`→`docs/legacy/CLAUDE.md`, 옛 `.claude/`→`docs/legacy/dot-claude/`, `AGENTS.md`·`.cursorrules` 등.

**제외 규칙(병합·복사·legacy 백업 전부에 적용):**
- 절대 가져오지 않음: `.env`·`.env.*`(단 `.env.example`은 유지), `*.local.*`(예: `settings.local.json`·`CLAUDE.local.md`), secret 의심 파일(API 키·토큰·비밀번호·개인키 `id_rsa`/`*.pem`/`credentials` 등), 외부 `.git/` 내부(이력은 fetch로 받지 폴더 복사로 받지 않는다).
- `docs/legacy/`는 **커밋되는 경로**다 — secret 제외는 여기에도 그대로 적용(legacy 경로는 ignore 규칙이 안 덮는다).
- git 이력 graft는 과거 커밋의 secret 블롭까지 결합할 수 있다 — 팁(현재) 트리는 위 패턴으로 제외하되, 이력에 secret이 있었다면 **키 회전 필요**를 보고에 명시. 이력 소급 제거(rewrite)는 이 스킬 범위 밖.

## 6. 3버킷 지식 이관
implementer에 위임(본문은 메인 세션이 작성해 프롬프트에 전문 첨부):
- **`docs/constitution.md` 하단 "이 프로젝트의 원칙"** — 버킷1 규칙. 이 절은 프로젝트 소유라 이후 `/template-update` 동기화가 덮지 않아 살아남는다.
- **`docs/GOTCHAS.md`** — 파일이 이미 있으므로(템플릿 동봉) **절대 무음 덮어쓰기 금지**. 원본을 `docs/legacy/GOTCHAS.md`로 백업(`cp docs/GOTCHAS.md docs/legacy/GOTCHAS.md`) 후, 버킷3 항목을 기존 항목 **뒤에** 추가(원문 형식·20항목/100줄 상한 준수, 중복은 기존 항목에 합침). 원본 내용은 병합 파일 또는 `docs/legacy/`에 반드시 살아남아야 한다.
- **`docs/STATE.md`** — `/handoff` 형식: 프로젝트 한 줄 요약 / 현재 상태 = 외부 진행상태 + "이 저장소는 /import로 외부 <외부명>을 편입했다" / 다음 할 일 = 외부 todo. "이전 설정 원본은 `docs/legacy/`에 보관" 1줄 포함.
- `README.md`는 프로젝트 소유 — 관문에서 반영 지정한 외부 본문만 검토 후 반영.

## 7. 커밋·보고
- **커밋 직전 개인·secret 점검**: `git status --short`로 `.env`·`*.local.*`·secret 의심·`docs/legacy/` 하위 secret이 staged인지 확인. 있으면 `git restore --staged <경로>` + 외부에서 온 것이면 제거 후에만 커밋. secret 커밋은 `[금지]`.
- 커밋은 원자적·검토 가능하게: 최소 (i) 소스+이력 병합(merge commit)과 (ii) 3버킷 지식 이관을 구분한다. 정확한 커밋 개수는 구현자 재량이되 secret은 어느 커밋에도 스테이지되지 않는다. push는 일반 규칙(자동 아님).
- **보고(한국어)**: 변경 파일; 옛 설정 위치(`docs/legacy/`); 외부 폴더 불가침(읽기 전용); `git log`에 외부 이력 결합됨(또는 "이력 미보존 — 외부 비-git"); secret 제외 결과 + 필요 시 키 회전 경고; settings·훅 변경은 **Claude 재시작** 후 적용; 이후 템플릿 동기화는 `/template-update`.
