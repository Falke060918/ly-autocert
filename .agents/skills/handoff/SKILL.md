---
name: handoff
description: "Preserve current project state, evidence, and next steps when finishing a work unit, preparing a handoff, or reducing a heavy conversation context."
---

# 인수인계

`.codex/conventions/codex-runtime.md`와 `.codex/rules/memory.md`를 읽는다.
사용자 원문과 실제 파일·커밋·검사 결과를 대조한다. 설명만으로 완료 처리하지 않는다.

## 1. 지식층 점검

- 사실/경로/명령은 MAP, 진행은 STATE, 재현 가능한 함정은 GOTCHAS로 분류한다.
- MAP 120줄, STATE 150줄, GOTCHAS 100줄 상한을 확인한다.
- STATE는 원본처럼 6KB 안팎을 목표로 간결하게 정리한다. 이는 요약 목표이며 6KB를 넘었다고 작업을 막는 하드 제한은 아니다.
- 새 사실과 모순되는 항목은 갱신·무효화한다. 최신 스펙보다 낡은 STATE를 정정한다.
- MAP-STALE 또는 KB-OVER-LIMIT이면 새 후보가 없어도 해당 파일을 대조·정리한다.
- 템플릿 원본은 제품 MAP이 없어도 정상이다. 제품 전용 함정을 원본 GOTCHAS에 쌓지 않는다.
- 행동 교정은 docs/learn/ 후보로 남기거나 승인된 경우 역할·스킬 지침을 직접 고친다.
- 사용 빈도는 수동 기록 기반이다. 기록이 없다는 이유만으로 스킬을 미사용으로 단정하거나 보관하지 않는다.

## 2. STATE 재작성

기존 파일에 무한 누적하지 않고 현재 상태·다음 할 일·이번 세션 특이사항으로 압축한다.
현재 상태에는 완료/진행/미검증/막힘을 구별하고 증거 경로와 관련 커밋을 남긴다.
메인 저장소의 `.codex/review/*.skipped.md`를 열거해 각 파일의 단위 ID·대상 커밋 앞 7자·일시를
`검사 건너뛴 단위: <unit>(<sha7>, <일시>) …` 형식으로 '현재 상태'에 한 줄로 남긴다.
해당 파일이 없으면 줄을 새로 만들지 않는다. 판정 면제의 뜻과 필드는
`.codex/conventions/verdict-protocol.md`의 'Exception — 사용자가 연 게이트'를 따른다.
검토 기록은 local-only이므로 STATE의 이 인계 줄은 유지한다. 도구를 바꿔도 아직 유효한
Claude 측 검사 생략 인계 기록을 지우지 말고, 양쪽 기록이 있으면 runtime 이름으로 구별한다.
다음 할 일은 승인된 범위의 구체적인 다음 단계와 필요한 결정을 적는다.
최신 스펙·계획서와 줄마다 대조한다. 실패한 검증과 미실행 검사를 감추지 않는다.
제품 목표·설계가 바뀌었다면 관련 계획 문서도 같은 단위에서 갱신한다.
릴리즈 노트는 실제 버전 변경/발행이 요청된 경우 프로젝트 규칙에 맞춰 작성한다.
사용자 변경을 포함한 파일은 자신의 변경 범위를 확인하고 필요한 파일만 커밋한다.

## 3. 외부 반영

push·Notion·PR 변경은 사용자 요청에 포함되거나 같은 세션에서 승인됐을 때만 한다.
origin이 없거나 템플릿을 가리키면 제품 커밋을 push하지 않는다.
Notion 작업이 승인됐으면 .codex/rules/notion.md에 따라 STATE/LOG/SPECS 슬롯을 반영한다.
연결·페이지가 없으면 건너뛰고 보고한다. handoff 때문에 새 연결·페이지를 만들지 않는다.
검증 판정이 필요한 단위는 .codex/conventions/verdict-protocol.md를 확인한다.

## 4. 인계 보고

현재 결과, 검증, 남은 제약과 다음 시작점을 짧게 알린다.
필요하면 `python scripts/starter.py snapshot --session <현재ID>`로 로컬 복구 기록을 남긴다.
ID를 모르면 manual을 사용하며 다른 세션 기록을 자기 것으로 덮지 않는다.
대화 초기화는 사용자가 선택한다. 새 세션에서 AGENTS.md가 STATE를 읽도록 안내한다.
