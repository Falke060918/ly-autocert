---
name: clarify
description: "Turns a request into a spec draft, marks every ambiguity in-place with the literal colon-bearing 확인 필요 tag (the colon is part of the machine contract — exact form in step 2), eliminates the tags round by round via AskUserQuestion, and saves the completed spec under docs/specs/. A spec with a remaining tag is not delegatable — the delegation path counts the tag and stops. Use when the user invokes $clarify, or before delegating a request that leaves material decisions open. (spec-kit $clarify port — design/03 (a))"
---

먼저 .codex/conventions/codex-runtime.md를 읽는다. 사용자 승인 범위와 현재 도구 구성이 우선한다.

# $clarify — 모호점 소거로 스펙 완성

Request to clarify: 사용자가 스킬 이름 뒤에 적은 요청

Purpose: in v1, ambiguity was digested by orchestrator discretion and the intent leaked (design/03 실측). Here ambiguity becomes a **visible marker inside the spec**, and only the user's answer removes a marker. The rule "a spec with markers cannot be delegated" is not prose — a machine count enforces it (device, not text: design/00).
All user-facing questions are in the user's language (Korean); the spec file the user reads is also Korean.

## Trigger conditions

- The user invokes `$clarify` (with or without an inline request).
- **Auto**: before delegating any request that leaves **2+ material open decisions** — scope boundary, data shape, UX choice, irreversible/paid pick. A single trivially inferable point is not a trigger: state it in the spec's 가정 section instead (00 문서 원칙 1 — 가정은 명시).
- **Not triggered**: 경량 위임(1줄 사소한 수정), 순수 정보성 질문, or the user says "그냥 바로 해" — then skip the whole flow.
- **[2계층(범위·목적)]** 무인 모드(`$automode on`)는 이 트리거를 끄지 않는다 — 스펙이 곧 범위·목적이라 무인에서도 그대로 발동하고 그대로 묻는다. 계층 정의는 `.codex/roles/orchestrator.md`.
- **M급 이상만 Work Order다**: 스펙과 ID는 **여러 단위로 갈리거나 검증이 필요한 작업(M급 이상)**에만 발급한다. 오타 하나, 문구 한 줄 같은 **S급**은 스펙도 ID도 만들지 않는다 — 판단선은 위 Not triggered와 같다(경량 위임이면 S급이다). 근거: 오타 수정에 ID를 발급하는 순간 프로세스가 제품보다 복잡해진다.

## Procedure

### 1. Spec draft
Write a draft with these sections (each maps to the delegation 4종 세트, design/03 (b)). 완성된 스펙은 곧 **Work Order**다 — 새 계층을 만들지 않고 이 스펙에 식별자만 얹는다:
- **Work Order ID** — 서두 첫 줄. 초안 단계에서는 `(발급 전)`으로 두고 4단계 저장 직전에 확정한다(발급 절차는 4단계).
- **사용자 원문** — the user's actual sentences, quoted verbatim. Never paraphrased.
- **무엇을 만드는가** — 1–3 lines.
- **성공 기준** — 2–3 verifiable "끝났을 때 무엇이 참인가" statements. Not procedure.
- **하지 말 것** — explicit out-of-scope lines for this work.
- **범위 외** — **mandatory**: the work's outer boundary as a scope charter — which adjacent/derived work this spec does NOT cover. Distinct from 하지 말 것 (per-delegation prohibitions): 범위 외 is what the "발견 즉시 투입" gate judges against.
- **가정** — assumptions the spec makes without asking.
- **작업 단위** — `<ID>-A`, `<ID>-B` … 목록. 이 문자열이 이후 위임 이름이자 판정 파일 `.codex/review/<ID>-A.verdict.md` 이름의 원본이 된다. 단위를 나누는 것은 **오케스트레이터 몫**이므로 clarify는 **자리만 만들고 억지로 채우지 않는다** — 아직 안 갈렸으면 `(위임 시 오케스트레이터가 확정)` 한 줄만 남긴다. 여기서 지어낸 단위는 분해를 흉내낸 것이지 분해가 아니다.

스펙에 **판정·상태·커밋 목록을 적지 않는다** — 스펙은 요구의 원본이고, 진행 상황과 판정은 각각 다른 곳(`docs/STATE.md`·`.codex/review/`)에 산다. ID 형식·필드·수명의 상세 단일 원본: `.codex/conventions/work-order.md`.

Cross-check the draft against `docs/constitution.md`: a conflict with an invariant principle is itself an ambiguity — mark it, do not silently bend either side.

### 2. Marking
Ambiguity marker, exact form: `[확인 필요: <질문 한 줄>]`, placed **inline where the ambiguity lives**.
The literal prefix **including the colon** is the machine contract — delegation-side greps match it, so never vary the wording (no missing colon, no `[확인필요]`, no `[TBD]`).
Never write the marker form as an example inside the spec body (no legend line, no cross-reference spelling it out) — the gate cannot tell a real marker from a mention of one, so a written-out example closes the gate forever.
Never resolve a fork silently: 갈림길은 답이 아니라 표식이 된다. A fork means the two readings yield **different artifacts** — the same discriminator as AGENTS.md '복창과 검증'. A point whose answer changes no artifact is not a fork: close it on the default and give it one line in `가정`, exactly as the trivially inferable case under Trigger conditions. Mark only real forks — that is what keeps the question count down.

### 3. Elimination rounds — AskUserQuestion
**[2계층(범위·목적)]** 이 라운드는 무인 모드에서도 그대로 묻는다 — 마커 하나하나가 범위·목적을 정하는 물음이다.
- Take up to **4 markers per AskUserQuestion call**; one marker = one question. Put candidate resolutions in the **options** (recommended one marked), and always include `나중에 (보류)` / `뺀다 (제외)`.
- Write Korean option/label text as **literal UTF-8 characters**, never `\uXXXX` escapes (v1 GOTCHA: escapes corrupt Korean).
- Apply each answer immediately: replace the marker with the decision, in place.
  - 보류 → remove the marker and **move that part out of the spec body into a `## 보류` list** (deferred, outside this delegation's scope). 제외 → record one line in `하지 말 것`.
- **If the file carries a 잠정본 line, keep it in sync.** A draft written in `$kickoff`'s unattended draft mode carries `> 잠정본 — 확인 필요 <N>건` as the **first line of the file**, above the title (a blockquote, one such line per file, `N` the plain-decimal marker count in that file). Maintaining it is this skill's job: after each round rewrite `N` to the current count, and **delete the line entirely the moment the count reaches 0**. A file with the line and no marker is a bug, and so is a marker with no line. A file that has no such line never gains one here — this bullet only runs when the line is already there.
- Repeat rounds until the spec body has **0 markers**.

### 4. Completion check and output
- Machine check: `awk '{n+=gsub(/\[확인 필요:/,"")} END{print n+0}' <spec-file>` must print `0` (the colon is part of the pattern — it is what separates a real marker from prose that merely mentions one). If not, return to step 3.
  - **Why not `grep -c`** (do not "simplify" it back): a wrapper rewrite can strip one backslash layer from the pattern and silently change what it matches, and `grep -c` counts matching *lines* — two markers on one line count as 1, so the gate under-counts. `awk` is not hook-rewritten, needs no pipe, and prints `0` with rc=0 when there is no match.
- **Work Order ID 발급** — 마커가 0이 된 시점에 발급한다(초안에는 번호를 예약하지 않는다. 저장되지 못한 초안이 번호를 갉아먹지 않게 하려는 것이다).
  1. **PREFIX**: `.codex/identity`의 Work Order PREFIX 키에서 읽는다 — **하드코딩하지 않는다**. 키 이름과 형식의 단일 원본은 `.codex/conventions/work-order.md`. 키가 **없으면 발급을 보류하고** 사용자에게 1회 물어 값을 정한 뒤 진행한다 — 임의로 짓지 않는다. **[3계층(멈춤선)]** 추정 금지 값이라 무인 모드에서도 묻는다.
  2. **날짜**: 저장 시점의 `YYYYMMDD`.
  3. **일련번호**: 카운터 파일이 아니라 **증거에서 구한다** — 절차의 단일 원본은 `.codex/conventions/work-order.md` §1이고, 여기서 규칙을 다시 정의하지 않는다. 실행은 증거 **두 곳을 모두** 본다: `docs/specs/`의 `<PREFIX>-<YYYYMMDD>-`로 시작하는 파일명, 그리고 `git log --all --grep=<PREFIX>-<YYYYMMDD>`. 둘을 합친 최대값 +1, 3자리 zero-pad. 그날 첫 건이면 `001`. **파일명만 보면 안 된다** — 스펙이 지워졌거나 커밋에만 남은 ID의 번호를 재사용하게 된다.
  - 결과 형태: `<PREFIX>-<YYYYMMDD>-<NNN>` (예: `ORCA-20260827-001`). 이 값을 스펙 서두의 `(발급 전)` 자리에 넣고, 작업 단위 목록의 `<ID>` 자리도 같이 채운다.
- Save the completed spec to **`docs/specs/<ID>-<slug>.md`** (slug: lowercase kebab, from the request topic). Commit under normal commit rules.
- Downstream delegation prompts cite this file path **and quote the 사용자 원문 section verbatim** — the spec is the source the 4종 세트 is assembled from.
- This spec is also the **판정 기준 for discovered work**: mid-work derived tasks are judged against it in 1 line — in-spec tasks dispatch immediately without a user question, out-of-spec tasks queue up (발견 즉시 투입; procedure: `.codex/roles/orchestrator.md` '발견 즉시 투입' 절). A spec missing the 범위 외 section cannot arbitrate that judgment — treat its absence as a marker-level defect.

## Gate contract (for delegation-side skills)
Before delegating from a spec, run the marker count above on it. **Any nonzero → stop the delegation and resume $clarify.** This is the "표식이 남은 스펙은 위임 불가" device from design/03 (a).
이 관문은 모드와 무관하다 — 무인 모드는 마커 계산을 끄지 않고, 마커가 남은 스펙은 무인에서도 위임되지 않는다.

스펙 작성은 제공된 편집 도구 또는 위임으로 수행한다.
