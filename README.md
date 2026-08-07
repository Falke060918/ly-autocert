# claude-starter

Claude Code 개발 착수 템플릿. 아이디어 → 기획 → 목업 → 구현까지 정형화된 프로세스와
토큰·컨텍스트 절약 장치가 미리 세팅되어 있다.
**한국어 전제 템플릿이다** — 운영 문서(CLAUDE.md·docs/STATE.md)와 `/kickoff` 산출 문서(기획·목표)가 한국어로 작성된다.

## 새 프로젝트 시작

```powershell
gh repo create 내프로젝트 --private --template Falke060918/claude-starter --clone
cd 내프로젝트
claude
```

첫 실행에서 폴더 신뢰 확인(trust dialog)을 수락해야 `settings.json`의 권한 allowlist가 적용된다.
이후 `/kickoff "아이디어 한 줄"` 을 입력하면 착수 프로세스가 시작된다.

클론 직후 해야 할 일(rtk 설치, 플레이스홀더 치환, GitHub 배선, LICENSE 처리)은 **[SETUP.md](SETUP.md)** 에 체크리스트로 정리되어 있다.

## rtk (셸 출력 압축)

rtk는 프로젝트가 아니라 머신에 설치되는 도구라 **머신당 1회** 필요하며, SessionStart 훅이 세션 시작 시 자동 감지해 없으면 설치를 진행한다.
수동 설치 절차는 [SETUP.md](SETUP.md#2-rtk-설치-머신당-1회).

## 이 템플릿이 주는 3가지 효과

| 효과 | 구현 장치 |
|---|---|
| 1. 토큰 절약 | rtk 훅(셸 출력 60~90% 압축, 미설치 시 자동 감지·설치), "요약만 반환" 규칙, 파일 덤프 금지 |
| 2. 작업 속도 + 컨텍스트 효율 | 오케스트레이터 **강제** 모드(메인 세션은 Edit/Write/Bash가 없어 조율만 가능), 병렬 서브에이전트(끝나면 컨텍스트와 함께 소멸, worktree 격리), `/handoff` + STATE.md로 주기적 세션 리셋 |
| 3. 빠른 개발 착수 | `/kickoff`(질문→기획→목표→목업 5단계), acceptEdits 모드, 권한 allowlist, TDD·체계적 디버깅이 내장된 역할 정의 |

**쓸수록 프로젝트에 특화된다** — 탐색 결과(`docs/MAP.md`)와 실패 경험(`docs/GOTCHAS.md`)이 누적되는 지식 레이어가 있어
같은 코드베이스를 다시 뒤지는 일과 이미 한 번 데인 실수를 반복하는 일이 줄어든다. MAP은 필요할 때 자동 생성된다.
GOTCHAS는 플랫폼 일반 항목이 템플릿에 동봉돼 클론에 딸려오고 파생 프로젝트에서도 그대로 유효하다(그래서 SETUP.md 1번이 "삭제하지 않는다"). 두 파일 다 자동 로드하지 않아 상시 토큰 비용이 없다.

**모든 규칙은 지시가 없을 때의 기본값이다.** 사용자가 명시하면 규칙보다 지시가 우선한다(CLAUDE.md의 지시 우선 원칙).

## 오케스트레이터 강제 모드

`settings.json`의 `"agent": "orchestrator"` 때문에 메인 세션은 `.claude/agents/orchestrator.md` 정의로 실행된다.
이 정의에는 **Edit/Write/Bash 도구가 없어서** 메인 세션은 파일 수정·명령 실행이 물리적으로 불가능하고,
서브에이전트(implementer 등)에 위임하는 것만 가능하다. 서브에이전트는 자기 정의의 도구를 그대로 가지므로 정상 작업한다.

- 일회성 해제(직접 수정이 필요할 때): `claude --agent general-purpose`
- 영구 해제: settings.json에서 `"agent"` 줄 삭제

## 구성

```
CLAUDE.md                     오케스트레이터 운영 규칙 (매 세션 로드, STATE.md 자동 임포트)
SETUP.md                      클론 직후 첫 30분 체크리스트
.claude/settings.json         agent 강제, effort high, thinking on, acceptEdits, allowlist, 플러그인
.claude/agents/orchestrator.md  메인 세션 정의 (Edit/Write/Bash 없음 — 위임 전용)
.claude/agents/               planner / implementer / reviewer / explorer 서브에이전트
.claude/output-styles/brief.md  답변 스타일 — 짧고 명료하게 (메인 세션 답변 형식에만 적용)
.claude/conventions/fonts.md  폰트 기본 강제값 (Geist + Pretendard) — CDN·Tailwind·CSS 스니펫
.claude/skills/kickoff/       /kickoff — 아이디어→기획→목표→목업 5단계 착수 프로세스
.claude/skills/handoff/       /handoff — STATE.md 갱신 후 세션 교체
.claude/skills/land/          /land — 머지·워크트리 정리·이슈 닫기·push 일괄 착지
.claude/skills/cheatsheet/    /cheatsheet — 자주 잊는 git·gh·템플릿 명령 조회 카드
.claude/skills/template-update/  /template-update — 파생 프로젝트를 템플릿 최신 장치에 동기화
.claude/template-origin       저장소 판정 센티널(템플릿 원본/파생 구분) — 파생 프로젝트는 SETUP 1번에서 삭제한다
.github/ISSUE_TEMPLATE/       feat / bug / chore 이슈 폼 (빈 이슈 비활성화)
.github/PULL_REQUEST_TEMPLATE.md  PR 본문 템플릿
.github/workflows/ci.yml      CI — 스크립트·settings.json·에이전트 프론트매터 검증 (push/PR 자동, 파생 후에는 프로젝트 소유 — 동기화가 덮지 않음)
CHANGELOG.md                  릴리즈 기록
LICENSE                       MIT — 템플릿 자체용. 프로젝트 것으로 교체하거나 삭제 (SETUP.md 5번)
docs/STATE.md                 세션 간 인수인계 파일 (01-plan.md, 02-goals.md는 kickoff가 생성)
docs/MAP.md                   (자동 생성됨) 구조·명령·컨벤션 캐시 — 첫 explorer 탐색 직후
docs/GOTCHAS.md               데인 것과 회피법 — 플랫폼 일반 항목 동봉, 파생에서도 유효 (삭제하지 않는다)
mockup/                       목업 산출물 폴더
scripts/setup.ps1 · setup.sh  rtk 설치 스크립트 — Windows / Linux·macOS (머신당 1회, 미설치 시 자동 실행됨)
scripts/statusline.sh         상태줄 — 모델 · 디렉토리 · git 브랜치 · rtk 절약 토큰 (프로젝트 기준)
scripts/session-banner.sh     세션 시작 배너 — 저장소 모드·다음 할 일·주요 명령 안내 (SessionStart 훅이 실행)
scripts/template-update.sh    /template-update 본체 — 템플릿 소유 파일만 template remote에서 checkout
```

## 포함된 플러그인 (첫 실행 시 설치 제안됨)

| 플러그인 | 용도 | 상시 토큰 비용 |
|---|---|---|
| frontend-design | 고품질 UI/목업 생성 | 소량 |
| context7 | 최신 라이브러리 문서 실시간 주입 (MCP) | MCP 오버헤드 있음 |
| typescript-lsp / pyright-lsp | TS/Python 코드 인텔리전스 | 하네스 레벨 |
| document-skills | PDF/DOCX/XLSX/PPTX 문서 생성 (공식) | 사용 시에만 |
| scroll-world *(선택 · 기본 비활성)* | 스크롤 동기화 몰입형 원페이지 랜딩 생성. **Higgsfield 유료 크레딧 필요**, ffmpeg·Python3+Pillow 필요 | 없음 (미설치) |

TDD·디버깅 방법론은 플러그인이 아니라 `.claude/agents/` 역할 정의(implementer·reviewer)에 내장되어 있다.

scroll-world는 마켓플레이스만 등록돼 있고 설치되지 않는다. 랜딩페이지 프로젝트에서 `/kickoff`가 사용 여부를 물어보며, 쓰겠다고 하면 그때 설치를 안내한다.

플러그인 on/off: `claude plugin disable <이름>` 또는 세션 안에서 `/plugin`. 토큰 비용 확인: `claude plugin details <이름>`

## 표준 워크플로우

1. `/kickoff "아이디어"` → 질문에 답하면 `docs/01-plan.md`, `docs/02-goals.md`, `mockup/index.html` 생성
2. 목업을 브라우저로 확인하고 피드백 → 승인 → **기능 단위 GitHub 이슈 자동 생성**
3. 구현은 서브에이전트에 위임된다 (이슈 번호 연동, 완료·검증 시 자동으로 이슈 닫힘)
4. 큰 단위 완료 시 `/handoff` → 세션 종료 → `claude` 재시작 → 이어서 진행

## 커스터마이징

- **답변 스타일**: `brief`(짧고 명료한 답변) 가 기본 적용된다 — 정의는 `.claude/output-styles/brief.md`.
  바꾸려면 `/config` → Output style, 완전히 끄려면 `.claude/settings.json`의 `outputStyle` 키를 삭제한다.
  답변 형식에만 적용되고 코드·문서 산출물의 분량은 줄이지 않는다.
- **권한 추가**: `.claude/settings.json`의 `permissions.allow` (프로젝트에서 자주 쓰는 명령 추가).
  단 이 파일의 직접 편집분은 `/template-update`가 통째로 덮어쓴다 — 살아남아야 하는 개인·머신별 권한은
  사용자가 직접 `.claude/settings.local.json`에 작성한다(동기화가 건드리지 않는 파일).
  Claude 자신이 settings.local.json을 생성·수정하는 것은 별개로 하드 규칙 `[금지]`(자가 권한 확장) 대상이다.
- **프로젝트 규칙**: CLAUDE.md 하단에 추가
- **상태줄**: `scripts/statusline.sh` — 표시 항목·색은 파일 상단 `C_*` 변수, rtk 캐시 주기는 `RTK_TTL`.
  끄려면 `.claude/settings.json`의 `statusLine` 키 삭제
- **컨텍스트 잔량 상태줄**: `~/.claude/settings.json`에 ccstatusline 등록 (머신당 1회).
  위젯 조정(Context % 추가 등): 터미널에서 `npx ccstatusline@latest` 실행 후 TUI에서 선택
- **모델·effort 개인 취향**: `~/.claude/settings.json` (이 템플릿에는 프로젝트 공통값만 둔다)
