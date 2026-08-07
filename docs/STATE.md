# STATE — 마지막 갱신: 2026-08-05

## 프로젝트
claude-starter — Claude Code 오케스트레이터 강제 모드 스타터 템플릿.
템플릿 원본이므로 `/kickoff` 대상이 아니고 `docs/01-plan.md`·`02-goals.md`가 없다. **MAP.md는 이 저장소에 두지 않는다**(파생 프로젝트로 샘).
세션 시작에 `TEMPLATE-REPO` 마커가 보이면 여기가 그 원본이다.

## 현재 상태
"서브에이전트 1개만 돈다" 문제의 **진단이 뒤집혔고**, 문구 강화 대신 삭제·측정·자동화로 방향을 틀었다.

**진단 반전**: Agent 툴은 `run_in_background` 값과 무관하게 **전부 비동기**(`async_launched` 314 / `completed` 56)다. 기다리느라 1개만 돈 게 아니라 **그냥 한 번에 1개씩만 띄웠다.** 세 번의 문구 강화가 효과 없던 이유다.

**전수 계측** (Downloads/Development 밑 8프로젝트 39세션, 8,822 레코드): 스폰 398 / 스폰 메시지 310 / 폭 1이 84% / 동시 실행 1개인 시간 77%, 2개 13%, 3개+ 8% / 최대 동시 8 / **직렬 손실 누계 504분(8.4h)** — into-Ione 393분, claude-starter 73분. 도구 지연은 병목이 아니다(Bash·Read·Glob·Web 전부 합쳐 0.21h). 반면 AskUserQuestion 대기 6.33h(96건). implementer 300 : reviewer 26. claude-starter 계열 메인 세션의 직접 Edit/Write는 **0건**(직접 구현 금지 규칙은 작동 중).

**이번 세션 반영분**:
- 개입 B — orchestrator.md/CLAUDE.md에서 모순·死문 삭제: 동기 실행 예외절("결과가 필요하면 블로킹 허용" — 모든 위임을 정당화), 더티트리 격리 불릿(HEAD 분기 사실과 모순), SendMessage 처방(도구 부재), 격리 불릿 3개→템플릿 1개. 불릿 34→31.
- 개입 A — `scripts/fanout.sh` 신설 후 교정. `fg%`→`asy%`, `isSidechain` 필터 삭제(전 코퍼스 0건), 동시 실행·직렬 손실 지표 추가, `--all` 다프로젝트 집계.
- 잡무 — `/land` 스킬 신설(머지·워크트리 정리·이슈 닫기·push 일괄, 뒤처리 위임 28건 흡수), 배너에 STATE "다음 할 일" 2줄 노출, `/release` 삭제(39세션 0회 사용)+전 문서 참조 정리, `docs/MACHINE.md.example` 도입(절대경로 재입력 9회 대응), `.claude/worktrees/` gitignore.
- 표준 흐름 변경: 검증(reviewer) → **`/land`** → `/handoff`.

## 다음 할 일 (우선순위 순)
1. **효과 측정** — 재시작 후 세션을 하나 돌리고 `bash scripts/fanout.sh 99 --all`로 cc1% 하락 여부 확인. 이게 개입 B의 유일한 판정 수단이다. 안 줄면 문구가 아니라 다른 층위 문제다.
2. **reviewer 미실행 대응** — implementer 300 : reviewer 26, 심층 세션 18개 중 8개가 reviewer 0회. 그 자리를 사용자가 QA로 메웠다(15건: "흰화면이야", "콘솔에 404", "반응 없음"). 미착수 개선안 중 품질 영향 최대.
3. **GOTCHAS 전달 경로** — 등재해도 메인 세션이 안 읽는다. named-spawn 함정은 07-28 등재 후 **6회 재발**. `@`로 자동 로드하지 않는 규칙이 원인. `## Agents & delegation` 섹션만 SessionStart 훅으로 출력하거나 `@docs/GOTCHAS.md` 추가(현재 92줄, 부담 작음).
4. **push 자동화** — push 전용 implementer 위임 44건 + 사용자가 "푸시해" 타이핑 11회. `Bash(git push:*)`는 이미 allow인데 오케스트레이터에 Bash가 없어 생긴 우회. `hooks.Stop`에 미푸시 커밋 자동 push. **settings.json hooks 편집이라 사용자 확인 1회 필요.**
5. **오케스트레이터 탐색 도구 제거** — `.claude/agents/orchestrator.md`의 `tools:`에서 `Glob, Grep, WebSearch, WebFetch` 삭제(`Read`는 유지). 메인 컨텍스트 약 330KB 절감, explorer 위임이 물리적으로 강제됨(explorer는 48회뿐이었고, 안 쓴 이유가 메인이 직접 할 수 있어서다). 부작용: 정보성 질문 직답이 어려워진다.
6. **AskUserQuestion 손상 차단** — `hooks.PreToolUse`로 `header`/`label` 비-ASCII 거부(`question` 본문은 한국어 유지). 실패율 11%. **settings.json 편집이라 확인 1회 필요.**
7. **정보성 질문 위임 억제** — 질문형 프롬프트 76건 중 37건이 서브에이전트를 띄웠다. 사용자 명시 항의 1회. CLAUDE.md에 예외 조항이 이미 있으나 미준수 — 5번(도구 제거)과 상충하므로 함께 설계할 것.
8. **settings.json 자가 편집 물리 차단** — `permissions.deny`에 `Edit/Write(./.claude/settings.json)`. `[Self-Modification]` classifier 차단 2회는 오케스트레이터가 implementer에게 권한 완화를 지시해서 났다.
9. (이전부터) 파생 실검증 잔여 — 최초 릴리즈 경로, SETUP-PENDING/ASK-MODE 흐름, worktree.baseRef 격리 확인.

## 이번 세션 특이사항
- CLAUDE.md·orchestrator.md·session-banner.sh·스킬 구성 변경 — **재시작해야 적용**(파생은 `/template-update` 선행)
- `/release` 스킬이 사라졌다. 파생 프로젝트가 `/template-update`를 돌리면 `.claude/skills` 통째 동기화라 자동 전파된다.
- `docs/MACHINE.md`는 git 미추적이다. 각 머신에서 `.example`을 복사해 쓴다.
- 계측 한계: 서브에이전트 내부는 트랜스크립트에 안 남아 에이전트별 토큰·내부 도구 시간은 산출 불가. 직렬 손실 504분은 **상한**이다(임계값 감도 6.1~10.4h, 일부는 진짜 데이터 의존).
- 이 머신 rtk: 설치돼 있으나 `~/.local/bin`이 PATH에 없어 RTK-MISSING 오탐 지속(머신 설정이라 사용자 몫)
- AskUserQuestion 인터럽트 시 유령 답변이 잡히는 UI 문제 지속 — 인터럽트와 동시에 온 답변은 재확인할 것

## 주요 파일
- `CLAUDE.md`: 운영 원칙(한국어). 마커 6종/자동 kickoff/handoff→clear/push 일괄/분해 의무화/폰트 기본값. 표준 흐름에 `/land` 포함
- `.claude/settings.json`: agent 강제, `effortLevel: "medium"`, ask에 push force 패턴군, deny `Read(./.env.*)`, `worktree.baseRef: "head"`, SessionStart 훅 2개. **훅 문자열 ASCII 유지**
- `.claude/agents/*.md`: 5역할(영어). orchestrator는 모순·死문 삭제 후 불릿 31개. effort: planner/reviewer `max`, implementer/explorer `high`. **model 미지정 = 메인 세션 모델 상속**
- `.claude/skills/{kickoff,land,handoff,template-update,cheatsheet}/SKILL.md`: 지시부 영어. handoff·land는 단일 에이전트·단일 커밋
- `scripts/fanout.sh`: fan-out 폭 + 동시 실행 비율 + 직렬 손실 계측. `bash scripts/fanout.sh [N] [--all] [--gap 초]`
- `scripts/session-banner.sh` / `scripts/template-update.sh`: 모드 감지 파싱은 settings 훅 포함 3곳 문자 단위 동기화 필수. 배너가 STATE "다음 할 일" 2줄 출력. 동기화 8경로 — ci.yml은 HARD EXCLUSIONS
- `.claude/conventions/fonts.md`: 폰트 강제값. `Geist, Pretendard, sans-serif` / `"Geist Mono", D2Coding, monospace`. **OS 기본 폰트 폴백 금지**
- `docs/MACHINE.md.example`: 머신 전용 절대경로·명령 메모 틀(사본은 git 미추적)
- `docs/GOTCHAS.md`: 영어 항목 15개
