---
name: recall
description: "Searches past Claude Code session transcripts (~/.claude/projects/\u003cslug\u003e/*.jsonl) via a local SQLite FTS5 index to answer \"what did we decide/do about X in an earlier session\". Use when the user invokes /recall, or asks things like \"지난 세션에서 ○○ 뭐였지\", \"예전에 이거 어떻게 했더라\", \"그때 왜 그렇게 결정했지\" — anything that needs conversation history from before the current context window."
---

# /recall — 과거 세션에서 찾아 답하기

`/clear`나 세션 종료로 컨텍스트에서 사라진 대화도 `~/.claude/projects/<슬러그>/*.jsonl`에 그대로 남아 있다.
이 스킬은 그 기록을 로컬 SQLite FTS5 인덱스로 검색해(발견 → 펼치기, hermes session_search 방식) 요약해 답한다.
모든 도구는 `scripts/index-sessions.py` 하나다. 인덱스 DB(`.claude/index/sessions.db`)는 로컬 전용이며 절대 커밋하지 않는다.

## 절차

**1. 인덱스 갱신 (매번 먼저 실행 — 증분이라 싸다)**
```bash
python3 scripts/index-sessions.py index
```
- 변경 없는 파일은 (mtime, size) 비교로 건너뛰므로 반복 실행 부담이 없다.
- "세션 기록 없음"이 나오면 이 저장소에서 아직 세션이 기록되지 않은 것이다 —
  `ls ~/.claude/projects/`로 슬러그를 확인해 지정하거나, 관련 저장소가 여럿이면 `--all`.
  슬러그는 `-`로 시작하므로 반드시 **등호 형태** `--project=-Users-...`로 쓴다(띄어쓰면 argparse가 옵션으로 오인).

**2. 검색 (discovery)**
```bash
python3 scripts/index-sessions.py search "키워드1 키워드2" --limit 10
```
- 각 단어는 자동으로 접두 매칭된다(`폰트` → `폰트를`·`폰트는` 매칭). 여러 단어는 AND다.
- 결과가 없으면: 단어 수를 줄이고, 동의어·다른 표기(한/영, 축약)로 2~3회 변형해 재시도한다.
- 사용자 발화만/모델 답변만 좁히려면 `--role user` / `--role assistant`. 특정 세션은 `--session <ID접두>`.
- FTS5 연산자(OR, NEAR, 구문검색)를 직접 쓰려면 `--raw`.

**3. 맥락 펼치기 (scroll)**
검색 결과 줄의 `파일:줄`을 그대로 넘겨 그 지점 앞뒤 대화를 본다.
```bash
python3 scripts/index-sessions.py show --file <경로> --line <줄> --context 3
```
- 스니펫만으로 답을 단정하지 말 것 — 결정·이유를 묻는 질문은 반드시 앞뒤 맥락을 펼쳐 확인한 뒤 답한다.

**4. 답변**
- 찾은 내용을 **요약**해 답한다: 언제(타임스탬프), 누가(user/assistant), 무엇을 결정/수행했는지, 근거 1줄.
- 원문이 필요하면 해당 부분만 인용한다. 전체 세션 덤프를 컨텍스트에 붓지 않는다.
- 못 찾았으면 못 찾았다고 답한다 — 시도한 검색어 변형을 1줄로 같이 적는다. 지어내지 않는다.

## 주의

- 인덱스에는 **사용자 발화와 모델의 텍스트 답변만** 들어간다 — tool 출력(tool_result)·thinking·시스템 레코드는 제외다.
  파일 내용·명령 출력 속에만 있던 단어는 검색에 안 걸리니, 그 대화를 **가리키던 말**(요청·요약 표현)로 검색어를 바꾼다.
- 세션 목록 훑기: `python3 scripts/index-sessions.py sessions` (최근순, 기간·메시지 수).
- 검색 결과의 판단이 현재 코드와 어긋날 수 있다 — 과거 결정은 "그때 그렇게 정했다"이지 현재 상태 보증이 아니다.
  현재 상태가 중요한 질문이면 저장소를 직접 확인해 교차검증한다.
- 출력 파싱이 필요하면 python으로 한다 — `|` 구분 레코드를 bash 내장으로 파싱하지 않는다(v1 실측: 꼬리 필드 유실·긴 줄 O(n²)).
