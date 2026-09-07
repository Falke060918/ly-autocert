# Verdict protocol — per-unit verification verdict file

The convention for recording the verification result of one work unit in **one file per unit**.
v1's single file (`.claude/review/last-verdict.md`) was dropped once its flaw was confirmed —
parallel verifications overwrite each other there (design/05·07). "I ran a review" held in session
memory does not count — whether it is the push gate or a re-delegation, the reader looks at this file only.

## Location and name

- Path: `<메인 저장소 루트>/.claude/review/<unit>.verdict.md` — one file per unit.
  The filename *is* the unit name.
- `<unit>` uses the **same name and the same rules** as the status file — the Work Order's sub-unit ID when the work
  has one (`.claude/review/ORCA-20260827-001-B.verdict.md`), otherwise the existing kebab-case name from the
  delegation prompt. Naming details in `.claude/conventions/status-protocol.md`, ID issuance in
  `.claude/conventions/work-order.md`. The file stays **one per unit** either way — the ID only makes the name unique,
  it does not merge units into one file.
  A name that diverges from status splits one unit's progress from its verdict, and the gate never opens.
  판정을 요구하는 단위는 검토자 접미사가 붙은 파일 셋을 쓴다 — 아래 '3자 판정' 절이 단일 원본이다.
- Even when verifying inside an isolated worktree, write the file at the **main repository root** — find the
  root with the same git common dir anchor as the status protocol (`git rev-parse --git-common-dir`
  → strip `%.git` → then `%/`, in that order). Written to a relative path inside the worktree, main never sees it.
- **Local-only. Not committed.** `.claude/review/` is gitignored — a verdict is a session artifact about
  that commit at that moment, not a fact of the repository.

## Format

Every time reviewer finishes a verification, **overwrite the whole file** (not an append — only the latest verdict remains).

```markdown
# verdict: <unit>

- 일시: <검증을 마친 시각, UTC ISO8601 — 예: 2026-08-23T09:15:00Z>
- 대상 커밋: <검증한 시점 그 단위 HEAD의 전체 해시 40자>
- 판정: pass 또는 fail (둘뿐이다 — "조건부 pass" 같은 토큰을 만들지 않는다)

## 요구 대비

위임장의 성공 기준을 **각 줄 그대로 옮겨 적고** 하나씩 판정한다 (design/03).

- [충족] <성공 기준 원문 1>
- [미충족] <성공 기준 원문 2> — <무엇이 모자랐는지 1줄>
- [충족] 사용자 도달 — <사용자가 보는 화면·명령·수동 실행 경로, path:line 1줄> (검사관 점검표의 '사용자 도달' 단계 — `.claude/agents/reviewer.md`. 자동 실행만 있고 볼 화면도 수동 경로도 없으면 [미충족]; 위임장이 도달 경로를 명시적으로 범위 밖에 뒀으면 이 줄 대신 아래 '단위 밖 결함'에 적는다)

## 근거

판정의 이유 — 무엇을 열어 봤고, 무엇을 실행했고, 결과가 무엇이었는지.
"괜찮아 보인다"가 아니라 확인한 사실만 적는다.

## 단위 밖 결함

이 단위 귀책이 아니지만 검토 중 발견한 결함 — 한 줄에 하나, 줄마다 그것을 담을 단위가 있는지 적는다. 절이 비면 `없음` 1줄.

- <결함 1줄> — 담을 단위: 있음(`<ID>`)
- <결함 1줄> — 담을 단위: 없음

## PROOF (실행되는 것을 추가·수정했을 때만 — 3줄)

1. 실행한 명령: <실제로 돌린 명령 1줄>
2. 관측된 것: <출력·종료코드·생긴 파일 등 눈으로 본 것 1줄>
3. 그래서 확인된 것: <그 관측이 무엇을 입증하는지 1줄>

<!-- 자기 적용이 불가능하면 대신 이렇게 적는다 -->
- PROOF 불가: <왜 지금 돌릴 수 없는지 1줄> — 관측 대기 항목으로 `docs/STATE.md`에 등재함

## 콜드리드

문서 산출물일 때만 적는다 — 한 줄.
`통과` 또는 `실패 — <신호와 path:line 1줄>` 또는 `해당 없음 (문서 산출물 아님)`.

## 반성 (fail일 때만 — 정확히 3줄, 생략 불가)

1. 무엇이 요구와 달랐나: <...>
2. 왜 그렇게 됐다고 보나: <...>
3. 다음 시도는 무엇을 다르게 해야 하나: <...>
```

## Verdict rules — fail을 만드는 조건 (단일 원본)

**한 문장**: 판정을 가르는 것은 **요구 대비 미충족 · PROOF 부재(적용 범위일 때) · 4대 실패 모드의 fail 표시 항목** 셋뿐이고,
**콜드리드 실패를 포함한 나머지 지적은 findings**다 — findings는 감점이지 탈락이 아니다.

fail 사유 — 하나라도 걸리면 판정은 `fail`이다:

1. **요구 대비에 `[미충족]`이 하나라도 있다.** 코드가 아무리 좋아도 fail이다
   (design/03 — the inspector compares against the success criteria and the user's own words before quality).
2. **PROOF 적용 범위인데 `## PROOF` 절이 없다** (또는 파일이 존재한다는 말만 되풀이한다).
   적용 범위·3줄 형식·자기 적용이 불가능할 때의 예외는 아래 'PROOF — the dogfooding evidence rule' 절이 단일 원본이다.
   적용 범위 밖이면 PROOF 절이 **없는 것이 정상**이고 그 부재는 fail이 아니다.
3. **`.claude/agents/reviewer.md`의 'Karpathy 4대 실패 모드 점검'에서 fail로 표시된 항목에 걸린다**
   (드러내지도 묻지도 않고 닫은 갈림길, `[확인 필요]`를 남긴 채 묻지 않고 진행, 요청으로 되짚을 수 없는 변경 등).
   항목 목록과 각 항목의 fail 표시 여부는 그 파일이 단일 원본이다 — 4대 실패 모드는 **전부 표시가 있고**, 표시 없는 항목은 없다.

findings — 판정을 가르지 않는다. severity를 달아 findings에 적는다:

- **콜드리드 실패**(문서 산출물에만 적용) — medium으로 올리고 `콜드리드` 필드에 적는다.
- 반박 부재(이견 필드 없음), 그 밖의 버그·엣지 케이스·품질 지적.

그 밖의 기록 규칙:

- **`## 단위 밖 결함`은 판정을 가르지 않지만, `담을 단위: 없음` 줄은 pass라도 오케스트레이터에게 올라가야 하는 신호다.** "귀책 아님"으로 닫고 끝내는 것이 이 절이 막는 실패다 — 검사관은 그 줄을 반환에 그대로 옮기고, 오케스트레이터는 그것을 발견 큐 항목으로 읽는다(`.claude/agents/orchestrator.md` '발견 즉시 투입'). 담을 단위가 `있음(<ID>)`이면 그 단위의 검토에서 잡히므로 올리지 않는다. 실측 계기(2026-09-06 파생 회고): 판정 1장 pass였는데 수집만 main에 있고 볼 화면이 없었으며, 검사관이 "그 화면을 만들 단위가 어디에도 없다"를 보고도 판정 파일 밖에 둬 아무 게이트에 안 걸렸다.
- **Do not abbreviate 대상 커밋.** The push gate does a string comparison, so write the full hash.
  If even one more commit lands after the verification, that verdict is automatically void — verify again
  (재검증의 규모는 아래 'Stale verdict' 절이 가른다 — 전면이 기본값이 아니다).
- A unit with no commit to verify (an uncommitted artifact such as a document draft) is not subject to the gate,
  so no verdict file is created for it — commit first if you want one.

## PROOF — the dogfooding evidence rule

**Scope: only things that run** — a skill, a hook, or a script that was added or changed. Plain documents,
rule text, prose, and config wording are **out of scope**; for those the PROOF section is simply absent
and its absence is not a fail. The trigger is "does this artifact execute", not "is this file new".

- When in scope, **"등록했다"(registered / wired it up) is not evidence.** A verdict is pass only when the
  `## PROOF` section holds **evidence of running the thing once against itself**: the command that was run
  and what was actually observed. No PROOF section (or one that only restates that the file exists) → fail.
- **Three lines are enough**: 무엇을 실행했나(명령) / 무엇이 관측됐나(출력·부작용) / 그래서 무엇이 확인됐나.
  Longer is allowed but never required — the point is that a human can re-run the same line.
- **Exception — when self-application is physically impossible.** Some things only fire on an event that
  cannot be staged (a `PreCompact` hook needs a real auto-compaction; a `SessionStart` hook needs a new session).
  Then pass requires, instead of evidence: one line saying why it cannot be run now, **plus** an entry added to
  `docs/STATE.md` as a pending-observation item. Evidence deferred and recorded — not a gate that stays shut forever.
- The exception is for *impossibility*, not for inconvenience. "It was hard to set up" is a fail, not an exception.

## Push gate

A unit's push passes only when **all three** of the following hold:

1. `.claude/review/<unit>.verdict.md` **exists**.
2. `판정:` is **pass**.
3. `대상 커밋:` **matches that unit's current HEAD**.

If any one of the three is off, run reviewer again. Session memory is not evidence.
(The rule document that enforces the gate belongs to a separate unit — this document is the source of truth for the file schema.)

### Stale verdict — HEAD 불일치의 처방 두 갈래 (단일 원본)

조건 3이 어긋났을 때(판정 파일의 `대상 커밋` ≠ 그 단위의 현재 HEAD) 처방은 **낡음의 원인 커밋이 무엇을 건드렸는가**로 갈린다. 이 절은 위 3조건 게이트를 바꾸지 않는다 — 존재·pass·HEAD 일치는 그대로 서 있고, 바뀌는 것은 **HEAD를 다시 일치시키는 비용**뿐이다.

- **표적 재확인** — `대상 커밋`과 HEAD 사이의 커밋 전부가 다음 중 하나면 전면 재검증 대신 이것으로 갱신한다:
  (a) 그 판정 자신이 findings로 남긴 medium/low 지적의 보강 커밋이거나,
  (b) 그 단위가 맡지 않은 파일만 건드린 커밋이다.
  할 일 세 가지가 전부다: 그 diff가 실제로 지적 범위 안(또는 단위 밖)인지 확인 + 실행 표면이 있으면 핵심 실행 확인 1회 + 판정 파일의 `대상 커밋`을 새 HEAD로 갱신하고 근거 1줄을 남긴다. **판정 자체(pass)는 새로 내리지 않는다** — 이미 내린 pass가 유효한 채로 대상만 옮기는 것이다.
- **전면 재검증** — 원인 커밋이 그 단위의 파일을 **지적 범위 밖에서** 건드렸으면 기존대로 전부 다시 본다. 판단이 애매하면 이쪽이 기본값이다.

왜 이 절이 필요한가 — 실측 두 건:
① makeit 파생 실측(2026-08-31): 검증이 pass를 내면서 medium을 남기고, 그 보강 커밋이 같은 단위에 붙어 판정이 낡는 루프가 하루에 3번 났다(회귀 가드 10.5분 + A·B 갱신 16분 + 최종 20분 = 46분). 사용자 원문: "절차 결함이지 리뷰가 느린 게 아닙니다."
② 본체 2026-08-31 세션: -B의 재작업 커밋 `0c75024` 하나 때문에, pass였던 -A·-C·-D 판정의 HEAD 재확인 위임을 별도로 돌렸다 — 셋 다 (b)의 경우라 표적 재확인이면 각 1분 안에 닫혔을 자리다.

### Exception — doc-only units (판정 면제, 단일 원본)

A unit with **no execution surface** pushes without a verdict file. The test is **mechanical, not the
orchestrator's discretion** — run it over the unit's commits:

```bash
git diff --name-only <base>..HEAD
```

The unit is doc-only when **every** path in that output ends with `.md`, **and** none of them sits under
`scripts/` (a `.md` there ships next to executable code), **and** `.claude/settings.json` is not among them.
`<base>` is the commit the unit started from — `origin/<branch>` when a whole unit's backlog is going out.
All conditions true → push in that same turn: no verdict file, no reviewer run, no question.
**Any one off → the three-condition gate above applies unchanged.** Empty output is not doc-only either —
there is nothing to push.

- **판정 면제는 push 면제이지 검토 면제가 아니다.** The diff's scope is still read against the request before
  pushing; what is waived is the verdict *file*, never the look at what is about to leave the machine.
- Why the exception is not a hole in the gate: the gate exists to keep **unverified running code** off the
  remote. Prose, rules, and prompts have nothing to run — PROOF is already out of scope for them (see
  'PROOF — the dogfooding evidence rule'), so a verdict file adds no evidence and only stalls documentation
  sync (measured: a template-sync unit sat unpushed because it was neither an implementation unit nor a
  `/handoff` moment).

### Exception — S급 경량 단위 (판정 면제, 단일 원본)

An **S-sized** unit pushes without a verdict file as well. This test is mechanical too — it reads the unit's
paperwork, not the orchestrator's judgement of how small the change felt — and **both** parts must hold:

1. **The unit carries no Work Order sub-unit ID.** `.claude/conventions/work-order.md` issues an ID from M size up
   and never for S, so the *absence* of an ID is the S-size test; there is nothing further to weigh. A unit named
   after a sub-unit ID (`TPL-20260830-001-A`) is therefore out of this exception by construction.
2. **The work went out as exactly one lightweight delegation** — the 1-line prompt of CLAUDE.md '위임장 4종 세트'.
   Two delegations, or one full 4종 세트 prompt, means the work was not S-sized whatever its diff looks like.

**Any one of the two off → the three-condition gate above applies unchanged** (file exists · pass · matches HEAD).
The two exceptions do not compound into a wider hole: doc-only is decided from the diff, this one from the unit's
size, and a unit that satisfies neither is gated.

- **우선순위는 고위험 > S급 면제.** 3계층 멈춤선(파괴적·비가역 등 — 계층 표: `.claude/agents/orchestrator.md`
  '무인 모드' 절)에 해당하는 작업은 S급이어도 이 면제를 잃는다 — 위 두 조건이 다 참이어도 판정을 요구하고,
  그 판정은 아래 '3자 판정' 절을 따른다.
- **판정 면제는 push 면제이지 검토 면제가 아니다** — the same line as doc-only. The diff's scope is still read
  against the request before pushing.
- **With an execution surface, minimum evidence replaces the file.** When the unit does touch something that runs,
  the implementer's completion report carries **one line for the command it ran and one line for what it observed** —
  the PROOF pair, in the report instead of in a verdict file. Missing that pair, the exception does not apply and the
  gate above does.
- Why the line sits here and not lower: pushing code units with no verdict at all has already cost us — a derived
  project sent a batch out that way and the user found two defects the reviewer would have caught. Running the
  reviewer round trip over a one-line fix costs more than it returns. M급 이상 is where the two curves cross.

### Exception — 사용자가 연 게이트 (판정 면제, 단일 원본)

사용자가 **명시적으로** 검사를 건너뛰고 push하라고 했을 때("바로 넣어라", "검토 없이 올려")의 면제다. 이 단위는
판정 파일 대신 **`.claude/review/<unit>.skipped.md`**를 남기고, **push 게이트는 이 파일의 존재로 열린다** — 위 3조건도
아래 2/3 산식도 이 단위에는 걸리지 않는다. **이것은 검토 실패가 아니라 사용자가 속도를 고른 기록이다** — 그래서 남긴다:
"어느 단위가 검토 없이 나갔나"를 세션 기억이 아니라 파일로 되찾기 위해서다(실측: 파생 프로젝트에서 사용자가 "바로 넣어라"로
게이트를 연 단위들이 그 사실을 어디에도 남기지 않아, 나중에 검토된 단위와 구분할 수 없었다).

파일 내용 — 네 필드, 단위마다 한 파일, 덮어쓰기:

```markdown
# skipped: <unit>

- 일시: <push한 시각, UTC ISO8601>
- 대상 커밋: <push된 그 단위 HEAD의 전체 해시 40자>
- 사용자 원문: "<건너뛰라고 한 문장 그대로>"
- 건너뛴 검토: 3자 또는 단독
```

- 위치·이름 규칙은 판정 파일과 같다 — 메인 저장소 루트의 `.claude/review/`, `<unit>`은 하위 ID 또는 위임장의
  kebab-case 이름, local-only·gitignored. 파일을 만드는 것은 push를 실행하는 쪽(implementer 또는 메인 세션)이고,
  push와 같은 턴에 만든다.
- **판별은 기계적이다 — 사용자 원문에 건너뛰라는 문장이 있어야 이 면제다.** 그래서 원문 인용이 필수 필드다. 오케스트레이터가
  "급하니까"로 스스로 여는 것은 면제가 아니라 게이트 위반이고, 원문 없는 `skipped.md`는 게이트를 열지 않는다.
- **이 면제는 사용자가 묻지 않았는데 먼저 건너뛰라고 한 경우에만 성립한다.** 오케스트레이터가 "지금 넣을까? 검사관은 안 띄웠다 →
  바로 넣어라 (추천)"로 그 말을 받아내면 면제가 아니라 `.claude/agents/orchestrator.md` push 항목의 금지형 위반이다 — 추천안을
  눌러 나온 답은 이 절의 "사용자 원문"이 아니다(파생 실측 2026-09-06: 두 단위 연속으로 그 질문이 나왔고 매번 추천안이
  선택됐다). 검사관을 안 띄운 단위의 다음 행동은 사용자에게 묻는 것이 아니라 그 턴에서 검사를 띄우는 것이다(행동 규칙은
  `orchestrator.md`가 단일 원본).
- **세션 이월 — 한 번 받은 말은 그 세션의 같은 종류 단위에 그대로 적용된다.** 사용자가 세션 중 한 번 명시하면(CLAUDE.md 0절
  "확인은 작업당 최대 1회") 같은 세션의 **같은 종류 단위 — 같은 스펙·같은 위험 계층 —** 는 다시 묻지 않고 `skipped.md`만 남기고
  push한다. 그 파일의 `사용자 원문` 필드는 첫 원문을 인용해 채운다: `세션 기본값(첫 원문: "<그 문장 그대로>", 첫 단위: <unit>)`.
  스펙이 다르거나 위험 계층이 올라간 단위(3계층 멈춤선)에는 이월되지 않는다 — 그 단위는 이 면제 밖이고, 묻는 것이 아니라
  검사를 띄운다.
- **판정 면제는 push 면제이지 diff를 안 보는 면제가 아니다** — 위 두 Exception과 같은 선이다. 사용자가 건너뛴 것은 검사관의
  판정이고, 무엇이 나가는지는 push하는 쪽이 여전히 읽는다.
- `/handoff`는 이 파일들을 열거해 `docs/STATE.md`에 '검사 건너뛴 단위' 줄을 남긴다 — 절차는 `.claude/skills/handoff/SKILL.md` §2.

### Exception — UI-only units → 단독 검토 (강도 완화, 단일 원본)

화면만 바뀐 단위는 **검사관 3명이 아니라 1명**이다. 이것은 위 세 Exception과 달리 **면제가 아니라 강도 완화**다 — 판정 파일은
필요하고, 검사도 그대로 한다. 판별은 오케스트레이터의 재량이 아니라 doc-only와 같은 diff 경로의 기계 판정이다:

```bash
git diff --name-only <base>..HEAD
```

그 출력의 **모든** 경로가 프론트엔드 UI 경로 — 컴포넌트·스타일·마크업, 곧 `.claude/rules/frontend.md`의 `paths:`가 가리키는
범위 — 안에 있고, **그리고** 서버·API·마이그레이션 경로(`server/`, `api/`, `migrations/` 류)·`scripts/`·설정 파일
(`.claude/settings.json`, 패키지·빌드 설정)이 그 출력에 하나도 없으면 UI 전용이다. `<base>`는 doc-only와 같다.
**하나라도 어긋나면 3자 판정(아래 절) 그대로다.**

- **판정 파일은 1장이다** — `.claude/review/<unit>.verdict.md`, 접미사 없음. 게이트는 위 Push gate의 3조건(존재 · pass · HEAD 일치)
  그대로이고 2/3 산식은 걸리지 않는다 — pass 1장이면 열린다.
- **검사관은 `.claude/agents/reviewer.md`의 절차를 줄이지 않는다** — 사용자 도달 확인과 실브라우저 확인(step 7)을 그대로 한다.
  줄어드는 것은 검토자 수이지 검토의 깊이가 아니다.
- **우선순위는 고위험 > UI 단독** — S급 면제와 같은 선이다. 3계층 멈춤선에 해당하는 작업은 화면만 바뀌었어도 3자로 돌아간다.
- 왜 1명인가: 되돌리기 쉽고 서버 상태를 바꾸지 않는 변경에 3자 왕복을 그대로 물리면, 오케스트레이터가 검사 자체를 건너뛰고
  사용자에게 "넣을까?"를 묻는 쪽으로 새는 것이 실측됐다(2026-09-06, `server/**` 0줄·마이그레이션 없음 단위 2건). 검사관 1명은
  그 새는 자리를 막는 값이지 검사를 없애는 값이 아니다.

## 3자 판정 (three-verdict variant, 단일 원본)

**판정을 요구하는 단위는 전부 이 절을 따른다** — 이 절 밖은 면제 단위(위 세 Exception: doc-only·S급 경량·사용자가 연 게이트)와
단독 검토 단위('Exception — UI-only units' — 판정 파일 1장으로 3조건 게이트를 그대로 쓴다)뿐이다.
처음(2026-08-31)에는 고위험(3계층 멈춤선) 단위에만 발동했으나 — makeit 파생 실측: 검사관 1명이 잡을 것을
전부 잡았고, 3자는 리뷰 시간과 판정 낡음 루프를 3배로 만든다 — **2026-09-01 사용자 재결정으로 M급 전반으로
확대됐다**(원문: "큰 작업의 경우 컨펌이 필요할 때는 여러 검사관이 판단하여 다수결로 진행한다"). 발동 판별은
재량이 아니라 분해 시점의 Work Order ID 부여이고, 검토자를 어떻게 띄우고 각자에게 무엇을 주는가도 행동
절차라 `.claude/agents/orchestrator.md` '3자 검토' 절이 단일 원본이다 — 여기는 파일 스키마와 게이트 산식만 정한다.

이 단위는 판정 파일이 하나가 아니라 **검토자별 셋**이다. 서로 다른 입력으로 검토한 결과를 한 파일에
합치면 어느 입력이 무엇을 잡았는지 사라지므로, `<unit>` 뒤에 검토자 접미사를 붙여 가른다:

| 파일 | 검토자 | 검토 입력 |
| --- | --- | --- |
| `<unit>.planner.verdict.md` | 설계자(planner) — planner 설계가 없는 단위는 스펙(범위 헌장)을 입력으로 받는 맥락 없는 제3 검사관이 이 자리를 대체한다(파일 이름은 그대로) | 기존 설계문서 (대체 시: 그 작업의 스펙) |
| `<unit>.order.verdict.md` | 검사관 A (새로 띄움, 맥락 없음) | 오케스트레이터가 설계자에게 전달한 위임장만 |
| `<unit>.user.verdict.md` | 검사관 B (새로 띄움, 맥락 없음) | 사용자에게 컨펌받은 확인본만 — 확인본 없는 경로(무인·"그냥 바로 해")에서는 사용자 원문 인용 |

- **스키마는 셋 다 위 Format 그대로다** — 요구 대비 필드(성공 기준 각 줄에 충족/미충족), fail이면 반성
  3줄, 전부 파일별로 그대로 의무다. 접미사는 이름만 가르지 형식을 바꾸지 않는다.
- 각 파일의 갱신도 기존 규칙 그대로다 — 그 검토자의 재검증마다 자기 파일 전체를 덮어쓴다.
- **배치 검토여도 파일은 단위마다 셋이다** — 세 검토자가 단위 N개를 한 배치로 봤으면 파일은 3N장이고
  (`<unit>`이 단위마다 다르다), 여러 단위를 한 파일에 합치지 않는다. 배치 여부는 파일 이름에도 스키마에도
  드러나지 않는다 — 배치는 검토자를 띄우는 방식일 뿐이라 이 절의 스키마를 한 줄도 바꾸지 않는다. 언제 묶고
  무엇을 각 검토자에게 주는가는 행동 절차라 `.claude/agents/orchestrator.md` '3자 검토' 절이 단일 원본이다.

### 게이트와의 관계 — 대체가 아니라 확장

기존 3조건(존재 · pass · HEAD 일치)은 그대로 서 있다. 이 변형에서 바뀌는 것은 **pass 조건의
산식 하나**다: 단일 파일의 `판정: pass` 대신 **3장 중 2장 이상이 pass**여야 게이트가 열린다.
나머지 두 조건(존재 · `대상 커밋` = 그 단위의 현재 HEAD)은 **판정 파일 각각에 그대로 적용**된다 —
없거나 HEAD가 어긋난 파일은 그 표에서 pass로 세지 않는다. Stale verdict의 처방 두 갈래도 파일별로
그대로 쓴다.

**산식은 단위마다 따로 센다 — 배치 검토도 예외가 아니다.** 세 검토자가 단위 N개를 한 배치로 검토해
파일이 3N장 있어도 게이트는 배치 전체가 아니라 **그 단위의 3장**으로만 열린다. 한 단위가 2/3에 못 미쳐도
채운 단위는 그대로 push되고, 못 채운 단위만 막힌다 — `/land`의 per-unit 게이트와 같은 선이다.

**판정 보류는 pass로 세지 않는다.** 판정 토큰은 `pass`/`fail` 둘뿐이므로(위 Format) 보류는 애초에 판정이 아니다 —
요구 대비에 `[판정 보류 — 입력 밖]`처럼 **자기 입력만으로는 판정할 수 없다고 적힌 줄**이 남아 있으면, 그 파일은
`판정: pass`라고 적혀 있어도 2/3 산식에서 pass 한 장으로 세지 않는다(실측: 파생 프로젝트의
`MLB-20260903-001-A.order.verdict.md:12`가 보류 줄을 남긴 채 pass로 집계됐다). 보류가 나왔다는 것은 그 검토자에게
간 입력이 **사용자 지시를 흘렸다**는 신호이므로, 처방은 검토자를 바꾸는 것이 아니라 **위임장을 고쳐 그 검토자를 다시
띄우는 것**이다 — 세 검토자의 입력을 가르는 설계는 그대로 둔다(성공 기준을 오케스트레이터가 잘못 줄 수 있다는 것이
그 설계의 존재 이유라, 성공 기준을 셋 모두에게 주는 것은 장치를 없애는 것이다). 되돌리는 절차는 행동 규칙이라
`.claude/agents/orchestrator.md` '3자 검토' 절이 단일 원본이다.

**pass가 1장 이하(3분의 1 이하)면 게이트는 닫힌 채 재설계 루프로 넘어간다.** 게이트 관점에서 이 루프는
2/3에 이르는 투표가 나올 때까지 push를 계속 막는 장치이지, 몇 번 돌면 강제로 열어 주는 장치가 아니다 —
반복 상한 없음. 루프의 절차(누가 누구에게 무엇을 넘기나)는 `.claude/agents/orchestrator.md`가 단일 원본이다.

## The failure loop — 반성 3줄 and the repeat-fail record

- A fail verdict makes the **반성 3줄 mandatory** (design/02, the Reflexion approach). The three questions are themselves the format:
  무엇이 요구와 달랐나 / 왜 그렇게 됐다고 보나 / 다음 시도는 무엇을 다르게 해야 하나.
- On re-delegation the orchestrator (or the re-delegation skill) reads the 반성 3줄 from this file and
  **attaches them verbatim to the re-delegation prompt** — built into the procedure rather than left to memory.
- **Repeat-fail record**: when a re-delegated artifact **fails again on the same point**, reviewer leaves one more line
  right after overwriting the verdict file:

  ```bash
  bash scripts/perf-log.sh refail <unit> "<같은 지적 사유 1줄>"
  ```

  It accumulates in `.claude/perf/refails.jsonl` and `scripts/perf-report.sh` aggregates it —
  the data source for the "동일 원인 재탈락률" (the design/02 verification metric). Failing on a **different**
  point is not a repeat fail — do not record it.

## Reader tolerance rules

- A missing file means "not verified" — not an error, just a gate that does not open.
- A stale verdict file left over from a past unit is harmless — its 대상 커밋 diverges from HEAD, so
  the gate filters it out by itself. There is no need to delete it.
- A malformed file is treated the same as fail (gate does not open) — the reader itself does not die.
