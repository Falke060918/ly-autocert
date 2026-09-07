# 압축 대비 훅 — 설정 결정의 근거 기록

이 파일은 붙여넣을 설정 조각이 아니다. `.claude/settings.json`에 **이미 등록된** 압축 관련 훅을 왜 지금 형태로 등록했는지 남기는 기록이다.
설정 값 자체의 단일 원본은 `.claude/settings.json`이므로 여기에 JSON을 다시 적지 않는다.

## 대상 훅

- `scripts/compact-snapshot.sh` — **PreCompact**에서 돌며 압축 직전의 저장소 상태(미푸시 커밋·워크트리·미착지 판정 등 기계적 사실만)를 `.claude/compact-snapshot.md`에 덮어쓴다.
- `scripts/compact-resume.sh` — **SessionStart(matcher `compact`)**에서 돌며 압축 직후 세션에 그 스냅샷 파일의 존재를 몇 줄로 알린다(파일 내용 자체는 출력하지 않는다).

두 훅은 `.claude/settings.json`의 `hooks`에 등록되기 전까지 무동작이고, 등록은 끝났다. 형태를 바꿔야 하면 사용자가 직접 고친다 — 에이전트의 `settings.json` `hooks` 편집은 하드 규칙상 `[금지]`다.

## PreCompact matcher를 `auto`만 둔 이유

`PreCompact` matcher는 `auto` 하나뿐이다 — 빠뜨린 것이 아니라 의식적 선택이다. 수동 `/compact`는 사용자가 시점을 알고 직접 치는 것이므로 스냅샷 대상이 아니다.
필요해지면 `manual` matcher 원소를 `PreCompact` 배열에 추가하면 된다. (2026-08-25 판단)

## 등록 시점에 확인한 것 (2026-08-25)

- 조각을 합성한 `settings.json` 사본이 JSON으로 정상 파싱되고 기존 키·훅이 모두 보존됐다.
- 등록 후 `hooks.SessionStart`는 원소 2개(matcher `compact` / matcher 없음), `PreCompact`는 원소 1개(`auto`)다. 기존 `SessionStart` 첫 원소(`session-markers.sh` + `session-banner.sh`)는 덮어써지지 않았다.
- **설정을 바꾸면 Claude 재시작이 필요하다** — `settings.json`의 `hooks`는 세션 시작에만 로드되고 `/clear`로는 다시 읽히지 않는다.
