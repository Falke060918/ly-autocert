---
name: cheatsheet
description: Korean quick-reference card of commonly forgotten git / gh / template commands (install, auth, daily git, mistake recovery, repo·issue·PR, template skills). Use when the user invokes /cheatsheet or asks how to do a basic git/gh operation (e.g. "gh 로그인 어떻게 하지").
---

# /cheatsheet — command quick-reference card

Section filter requested by the user: $ARGUMENTS

This is a **pure lookup card — do not execute any command on it**. Output text only.
If the user follows up asking to actually run one of these commands, that is a new request — handle it under the normal rules (delegation, permissions).

- **No argument** → print the card below **verbatim**, in a single fenced code block (monospace keeps the alignment). Add nothing before or after except at most one line.
- **With an argument** (Korean or English) → print only the matching section(s), same fenced format. Matching guide:
  | argument examples | section |
  | --- | --- |
  | 설치, install, 확인 | 설치·확인 |
  | 인증, auth, login, 로그인, 계정 | 인증 |
  | git, 일상, daily, commit, push | 일상 git |
  | 복구, 실수, undo, recover, reset, stash | 실수 복구 |
  | 저장소, repo, 이슈, issue, pr | 저장소·이슈·PR |
  | 템플릿, template, skill, rtk | 이 템플릿 명령 |
- If the argument matches nothing, print the whole card.
- If the user's question is **not covered by the card**, answer from general knowledge — the card format is not required there.

## Card

```text
━━ 설치·확인 ━━
winget install --id GitHub.cli --source winget   # Windows
brew install gh                                  # macOS
# Linux: 배포판 패키지 매니저(apt/dnf 등) — 상세: https://cli.github.com
gh --version                                     # 설치 확인

━━ 인증 ━━
gh auth login                # GitHub 로그인
gh auth status               # 로그인 상태 확인
gh auth status --active      # 활성 계정만 표시
gh auth switch               # 계정 전환
gh auth logout               # 로그아웃

━━ 일상 git ━━
git status                   # 지금 상태
git pull                     # 원격 변경 가져오기
git add -A                   # 전체 스테이징
git commit -m "메시지"       # 커밋
git push                     # 원격 반영
git log --oneline -10        # 최근 커밋 10개
git diff                     # 뭐가 바뀌었나
git switch <브랜치>          # 브랜치 이동
git switch -c <새브랜치>     # 브랜치 만들고 이동

━━ 실수 복구 ━━
git restore <파일>           # 파일 수정 되돌리기(커밋 전)
git restore --staged <파일>  # 스테이징만 취소
git reset --soft HEAD~1      # 직전 커밋 취소(변경은 보존)
git commit --amend           # 직전 커밋 고치기 (push 전에만!)
git stash                    # 변경 임시 보관
git stash pop                # 보관한 변경 복원

━━ 저장소·이슈·PR ━━
gh repo clone <owner/repo>                        # 클론
gh repo create <이름> --private --source . --push # 현재 폴더를 새 저장소로
gh repo view --web           # 저장소 브라우저로 열기
gh issue list                # 이슈 목록
gh pr create --fill          # PR 생성(커밋 메시지로 자동 작성)
gh pr view --web             # PR 브라우저로 열기

━━ 이 템플릿 명령 ━━
/kickoff "아이디어 한 줄"    # 기획→목표→목업→이슈 생성
/land                        # 워크트리 머지·정리·이슈 닫기·push 일괄
/handoff                     # 작업 상태를 docs/STATE.md에 기록
/template-update             # 템플릿 최신 기계장치 동기화(파생 프로젝트에서)
bash scripts/fanout.sh [N]   # 스폰 폭·동시 실행·직렬 손실 계측(기본 5, --all=형제 프로젝트별)
rtk gain                     # 토큰 절약 대시보드
```
