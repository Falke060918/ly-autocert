# SETUP — 첫 30분에 할 일

템플릿을 클론한 직후 한 번만 하는 작업이다. 위에서부터 순서대로 처리하면 된다.
템플릿이 **무엇을·왜** 하는지는 [README.md](README.md)를 본다.

---

## 1. 초기화 — 템플릿의 기록 지우기

템플릿에서 복제하면 claude-starter 자신의 기록이 딸려온다. 먼저 지운다.

- `.claude/template-origin` — **가장 먼저 삭제**한다. 이 센티넬이 남아 있는 한 세션 시작 때마다 `PROJECT-REPO SETUP-PENDING` 경고가 뜬다.
  (remote가 없고 파일에 `mode:` 줄도 없으면 대신 `ASK-MODE`가 뜬다 — 어느 쪽이든 이 파일을 지우면 멈춘다.)
- `docs/STATE.md` — 템플릿 원본의 개발 기록이 항상 실려 온다. 내용을 비우고 새 프로젝트 한 줄로 교체한다. 이후는 `/kickoff`·`/handoff`가 덮어쓴다.
- `docs/MAP.md` — 있으면 **삭제**한다. 100% 프로젝트 고유라 남길 게 없다. 첫 탐색 때 다시 생긴다.
- `docs/GOTCHAS.md` — **삭제하지 않는다.** 템플릿 기계장치(rtk 래퍼)·환경에 대한 항목은 파생 프로젝트에도 그대로 유효하다.
  내 프로젝트·환경과 무관한 항목만 골라 지운다 — 예: 리눅스/맥에서만 작업한다면 `(Windows / …)` 꼬리표가 붙은 항목.
- `CHANGELOG.md` — 릴리즈 항목이 있으면 비운다. `LICENSE`는 아래 5번에서 처리한다.

## 2. rtk 설치 (머신당 1회)

rtk는 프로젝트가 아니라 **머신에 설치되는 도구**라서 템플릿 클론만으로는 설치되지 않는다.
SessionStart 훅이 세션 시작 시 자동 감지하고 없으면 Claude가 설치해주지만, 수동으로 하려면:

```powershell
# Windows
powershell -ExecutionPolicy Bypass -File scripts\setup.ps1
```

```bash
# Linux / macOS / WSL
bash scripts/setup.sh
```

bash 명령 출력을 60~90% 압축하는 rtk 훅을 전역 등록한다.
확인: `rtk init --show` · 절약량 대시보드: `rtk gain` · 제거: `rtk init -g --uninstall`

## 3. 플레이스홀더 치환

템플릿에는 원본 저장소 기준 값이 남아 있다. 3곳을 내 프로젝트 값으로 바꾼다.

| 파일 | 플레이스홀더 | 바꿀 값 |
|---|---|---|
| `README.md` | 템플릿 경로 `Falke060918/claude-starter` | 내 저장소 경로 (또는 "새 프로젝트 시작" 섹션 자체를 삭제) |
| `LICENSE` | 연도 `2026`, 저작권자 `Falke060918` | 내 연도·이름 (아래 5번 참고) |
| `CHANGELOG.md` | 플랫폼 접두어 안내 (`web`·`android`·`ios`) | 실제 플랫폼만 남긴다. 단일 플랫폼이면 접두어 없이 `## v1.0.0` |

## 4. GitHub 배선

```bash
gh repo create <내프로젝트> --private --source . --push   # 이미 클론했다면 생략
gh label list                                            # feat / bug / chore 존재 확인
```

(명령이 기억 안 나면 `/cheatsheet` — git·gh 기본 명령 조회 카드)

- `.github/ISSUE_TEMPLATE/`의 이슈 폼은 `feat` / `bug` / `chore` 라벨을 붙인다. 없는 라벨만 `gh label create <이름>`으로 만든다.
- 빈 이슈는 `config.yml`에서 막아뒀다 — 이슈는 항상 폼을 거친다.
- `.github/workflows/ci.yml`은 main push / PR에서 자동 실행된다. **별도 설정이 필요 없다.**
- PR을 쓸 거라면 `.github/PULL_REQUEST_TEMPLATE.md`가 자동으로 적용된다.

**GitHub을 안 쓰면** 이 단계를 통째로 건너뛴다. `.github/`(이슈 폼·PR 템플릿·CI)는 지워도 되고, 둬도 그냥 동작하지 않을 뿐 해롭지 않다.
`/kickoff`는 GitHub 연결을 자동 감지해 로컬 모드로 동작한다 — 이슈 대신 작업 목록이 `docs/STATE.md`에 쌓인다. 나중에 GitHub을 붙이면 자동으로 전환된다.

## 5. LICENSE 교체 또는 삭제

⚠️ 저장소에 들어 있는 LICENSE는 **템플릿 자체**에 대한 것이다(MIT, 2026 Falke060918).
**실제 프로젝트 라이선스로 교체하거나, 비공개 프로젝트면 삭제하라.** 그대로 두면 내 코드가
남의 이름으로 MIT 배포되는 것으로 보인다.

## 6. 프로젝트 고유 규칙 추가

`CLAUDE.md` **하단**에 이 프로젝트에만 해당하는 규칙을 덧붙인다. 예: 기술 스택, 디렉토리 컨벤션,
빌드·테스트 명령, 건드리면 안 되는 영역. 상단의 운영 원칙(지시 우선 원칙 / 하드 규칙 / 역할)은 건드리지 않는다.

템플릿은 **스택 중립**이다 — 쓰는 스택은 여기에 적는다. 단 React 웹 UI면 컴포넌트 기본값이 이미 shadcn/ui이고,
새로 만들기 전에 기존 `components/ui/`와 shadcn/ui를 먼저 확인하라는 규칙이 걸려 있다(CLAUDE.md "프론트엔드 기본 스택"). 다른 스택이면 적용되지 않는다.

MCP 서버는 파생 프로젝트가 직접 관리한다 — 템플릿의 `.mcp.json`(playwright · chrome-devtools)은 최초 1회 참고용이고,
`/template-update`가 덮지 않으므로 사내 API·DB 등 프로젝트 서버를 여기에 마음대로 추가해도 된다.

자주 쓰는 명령이 권한 프롬프트에 걸리면 `.claude/settings.json`의 `permissions.allow`에 추가한다.
MCP 도구 권한(`.mcp.json`의 playwright·chrome-devtools)은 템플릿 `settings.json`에 이미 설정돼 있다 — 새로 클론한 폴더는 최초 1회 워크스페이스 신뢰 확인만 거치면 프롬프트 없이 붙는다.
답변 스타일(기본 `brief` — 짧고 명료한 답변)이 마음에 안 들면 `.claude/output-styles/brief.md`를 고치거나 `/config` → Output style에서 바꾼다.

⚠️ 위 세 파일(`CLAUDE.md`·`.claude/settings.json`·`.claude/output-styles/`)과 `.claude/conventions/`(폰트 등 기본 강제값)는 `/template-update`가 통째로 덮어쓴다 —
실행 후 `git diff --cached`(checkout 결과는 이미 index에 올라가 있어 그냥 `git diff`는 빈 출력이다)에서 커스터마이징 소실 여부를
반드시 확인하고 복원하라. `CLAUDE.md`·`.claude/settings.json`은 덮어쓰기 직전 사본이 `.template-update-backup/<시각>/`에 남는다. 개인·머신별 권한은 처음부터
`.claude/settings.local.json`에 두면 동기화가 건드리지 않는다.

## 7. `/kickoff` 실행

```bash
claude
```

첫 실행에서 폴더 신뢰 확인(trust dialog)을 수락해야 `settings.json`의 권한 allowlist가 적용된다.
이어서 `/kickoff "아이디어 한 줄"` 을 입력하면 질문→기획→목표→목업 승인까지 진행된다. 구현을 시작하려면 그다음 `/architect`로 기술계획을 승인받는다 — 스캐폴딩·이슈 생성은 거기서 이뤄진다.

랜딩페이지·마케팅 사이트 성격이면 목업 단계에서 scroll-world(스크롤 동기화 몰입형 랜딩) 사용 여부를 물어본다.
쓸 생각이면 미리 `/plugin install scroll-world@scroll-world` 와 ffmpeg·Python3+Pillow, Higgsfield CLI 인증·크레딧을 준비해 둔다. 없으면 정적 HTML 목업으로 자동 폴백한다.

## 8. (선택) CI에 앱 잡 추가

`.github/workflows/ci.yml`의 기본 `validate` 잡은 템플릿 설정·스크립트만 검사한다.
앱 코드가 생기면 스택에 맞는 lint/test 잡을 같은 파일에 추가한다.

```yaml
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      # 스택에 맞게: setup-node / setup-python / setup-java 등
      # - run: npm ci && npm run lint && npm test
```

`ci.yml`은 파생 이후 **프로젝트 소유**다 — `/template-update`가 건드리지 않으므로 추가한 앱 잡은 안전하다.
기본 `validate` 잡은 파생 시점의 사본이라 이후 템플릿 개선을 자동으로 받지 않는다 — 필요 없으면 지워도 된다.

---

여기까지 끝내면 이후 운영(커스터마이징, 워크플로우)은 [README.md](README.md)를 참고한다.

---

## 기존 파생 프로젝트 최신화

`/template-update` 스킬이 생기기 전에 만든 파생 프로젝트는 최초 1회만 아래로 부트스트랩한다.

```bash
git remote add template <템플릿 저장소 URL>
git fetch template
git checkout template/main -- scripts/template-update.sh .claude/skills/template-update
# checkout이 파일을 스테이징까지 하므로 먼저 커밋한다 — 안 하면 스크립트가 더티 트리 중단에 걸린다
git commit -m "chore: template-update 부트스트랩"
bash scripts/template-update.sh
```

이후 최신화는 `/template-update` 가 처리한다 — 템플릿 소유 파일(CLAUDE.md, SETUP.md 자신, scripts, `.claude/agents`·`.claude/skills`·`.claude/output-styles`·`.claude/conventions`, settings.json)만 갱신하고 `docs/`·`README.md`·`.mcp.json`·`.github/workflows/ci.yml`·제품 코드는 건드리지 않는다.
