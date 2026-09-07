---
name: automode
description: Turns unattended mode on or off for this checkout. `/automode on`, `/automode off`, or `/automode` with no argument to report the current mode. Unattended mode silences only mid-work "how should I do this" questions — scope/purpose rounds and stop-lines keep asking in either mode, while mid-work learn/harvest candidates go to the `docs/learn/` inbox with a one-line notice instead of a question. Use when the user wants to step away and let the session run, or wants to be asked again.
---

# /automode — 무인·유인 모드 토글

인자: $ARGUMENTS

이 스킬이 하는 일은 하나다 — 이 체크아웃의 모드 값을 읽거나 바꾸고 한 줄로 알린다. **무인 모드가 무엇을 끄고 무엇을 끄지 않는지의 단일 원본은 `.claude/agents/orchestrator.md`의 3계층 표와 판별 순서**이고, 여기서 그 규칙을 다시 정의하지 않는다. 사용자에게 나가는 문장은 한국어다.

## 상태 자리 — `.claude/automode`

- 경로는 **메인 저장소 루트**의 `.claude/automode` 하나다. 워크트리 안에서 실행해도 자기 워크트리에 새 파일을 만들지 않는다. 오케스트레이터는 셸이 없어 `git rev-parse`를 직접 못 돌리므로, **루트 절대경로를 오케스트레이터가 위임장에 적어 넘긴다** — 일꾼이 루트를 다시 셈하지 않게 하는 장치다(루트 고정의 취지는 `.claude/conventions/status-protocol.md`의 root anchoring과 같다).
- 내용은 한 줄, `on` 또는 `off`.
- **읽는 규칙: 첫 줄이 정확히 `on`일 때만 무인이다. 그 밖의 값도, 파일이 아예 없는 것도 전부 `off`(유인)로 읽는다.** 모르면 묻는 쪽이 안전한 방향이라 기본값이 유인이다.
- 이 파일은 gitignore 대상이라 **커밋하지 않는다.** 토글은 워킹 트리를 더럽히지 않고, 파생 clone은 언제나 유인으로 시작한다.

## 세 갈래

오케스트레이터에게는 Write가 없다. **파일 쓰기는 implementer 경량 위임 1건으로 넘기고, 오케스트레이터가 직접 쓰지 않는다.** 위임 프롬프트는 "이 절대경로에 이 한 줄을 쓰고 커밋하지 마라" 한 줄이면 된다 — 경로는 위에서 말한 대로 메인 저장소 루트 절대경로로 적어 넘긴다.

### `/automode on` — 무인으로 전환
1. 현재 값을 읽는다. 이미 `on`이면 쓰기를 위임하지 않고 "이미 무인"이라고 한 줄로 닫는다.
2. `.claude/automode`에 `on` 한 줄을 쓰도록 implementer에 경량 위임한다.
3. 위임이 돌아오면 **오케스트레이터가 `.claude/automode` 첫 줄을 Read로 직접 재확인한다** — 완료 보고는 증거가 아니다. 첫 줄이 `on`이 아니면 재위임한다.
4. 통보 1줄: 무인으로 켰다는 사실과, 범위·목적 질문과 멈춤선은 그대로 뜬다는 경계 한 마디.

### `/automode off` — 유인으로 되돌림
1. 현재 값을 읽는다. 이미 `off`면(값이 다르거나 파일이 없는 경우를 포함한다) 쓰기를 위임하지 않고 한 줄로 닫는다.
2. `.claude/automode`에 `off` 한 줄을 쓰도록 implementer에 경량 위임한다. 파일을 지워도 결과는 같지만, 값을 남기는 쪽이 "꺼 둔 것"과 "설정한 적 없는 것"을 눈으로 가른다.
3. 위임이 돌아오면 **오케스트레이터가 `.claude/automode` 첫 줄을 Read로 직접 재확인한다** — 첫 줄이 `off`가 아니면 재위임한다.
4. 통보 1줄.

### `/automode` — 인자 없이 조회
파일을 읽어 현재 모드를 한 줄로 보고한다. 아무것도 쓰지 않고 위임하지도 않는다. 그 밖의 인자(`/automode yes` 같은)를 받으면 **켜지도 끄지도 않고** 조회와 똑같이 현재 모드를 보고하면서 인식하지 못한 인자라고 덧붙인다.

전환 직후에는 재확인 Read의 결과가 컨텍스트에 남아 그 세션은 지금 모드를 안다. 나중에라도 확신이 안 서면 오케스트레이터가 Read로 직접 읽으면 된다.

## 무인 모드가 끄는 것과 끄지 않는 것

**무인이 끄는 것은 1계층(진행 질문) 하나뿐이고, 2계층(범위·목적)과 3계층(멈춤선)은 무인이어도 그대로 묻는다.**
작업 중 발생한 learn·harvest 후보는 무인에서 묻지 않고 `docs/learn/` 수신함에 쌓고 통보 1줄로 닫는다 — 그 스킬을 실제로 실행하는 승인 관문 자체는 무인이어도 묻는다.
계층 표·판별 순서·기록 의무·재위임 상한·헤드리스와의 우선순위는 `.claude/agents/orchestrator.md` '무인 모드' 절이 단일 원본이다 — 여기서 다시 적지 않는다.

## 보고

한 줄이면 끝난다. 예: "무인 모드로 켰다 — 범위·목적 질문과 멈춤선은 그대로 뜬다." / "지금은 유인 모드다."
