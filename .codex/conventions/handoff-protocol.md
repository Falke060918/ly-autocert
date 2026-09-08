> Codex 적용: 먼저 `.codex/conventions/codex-runtime.md`를 따른다. 이식 원문의 자동 push·외부 쓰기·다음 단계는 승인된 범위에만 적용한다. 도구/하네스 관측은 Claude의 배경이며 Codex 보장으로 읽지 않는다.

# Handoff protocol — the plumbing of a delegation

위임장이 "무엇을 하라"라면 이 문서는 **"결과물을 어디에 두고 무엇을 돌려주는가"**다.
The template specifies the *content* of a delegation in detail; without this file its *plumbing* stays unspecified, and
unspecified plumbing fails silently — an artifact left only in the return value comes back truncated or empty.

**This document is the single source of truth for the four plumbing items.** Agent definitions reference this path
in one line; they do not restate the rules. The fixed-type artifacts (verdict / status / spec) keep their own
schema documents — this file only places them on the map.

## 1. 산출물 저장 경로 — where the artifact goes

- **The artifact is a file at a named path. The return value is a receipt, never the delivery.**
  Do not deliver through the runtime's own capture file (`tasks/<agentId>.output`) either — that path is created by
  the harness, not named by the delegation, and nothing in the delegation guarantees it was written. 규칙의 근거는
  수치가 아니라 원리다: **harness가 만드는 경로에 기대지 말고 지정 경로에 저장한다.** (다른 저장소에서는 이 파일이
  빈 채로 남는 사례가 관측된 적이 있다 — 이 저장소에서 측정된 값은 아니다.)
- **A delegation prompt that expects an artifact names its path.** If the prompt names none and the result will not
  fit inside the return cap of §2, the worker picks a path itself, writes the file, and returns that path — it does
  not dump the content into the answer.
- **Where the harness itself refuses the write, the return text becomes the delivery — do not retry the save.** This
  runtime rejects a subagent writing its report to a file at all ("Subagents should return findings as text" — 2026-08-29,
  three workers hit the identical refusal). 이것은 위 규칙의 **예외 문단이지 대체 규칙이 아니다**: 저장이 가능한 자리에서는
  기본값이 그대로 서 있고, 원리("harness가 만드는 경로에 기대지 말고 지정 경로에 저장한다")도 바뀌지 않는다. 바뀌는 것은
  저장 자체가 막혔을 때뿐이다. The worker returns the content as text, states in one line that the save was refused, and
  **the duty to arrange its save moves to the main orchestrator**, who delegates the save to an implementer at the named path; the main does not write it personally. Retrying the write,
  or silently dropping the artifact because the write failed, are both wrong.
- **Over the §2 cap, an unsaveable artifact is split, never truncated.** The return is cut from the front, so a worker
  that dumps past the cap loses the beginning without any error. Return the part that carries the decision, name in one
  line what was left out, and let the delegator ask for the rest — a second delegation returning the remainder costs one
  round trip, a silently halved artifact costs the whole thing.
- **인계 문서는 `확인한 사실`과 `확인하지 못한 전제`를 갈라 적는다.** 직접 읽거나 실행해서 확인한 것만 앞 칸에 넣고,
  확인할 도구가 없어 가정한 것은 뒤 칸에 넣는다 — 셸이 없는 에이전트가 서술한 저장소 상태는 언제나 뒤 칸이다.
  **구분이 없으면 받는 쪽이 전제를 사실로 읽는다**: 이 저장소의 결함 문서가 그렇게 물려준 전제 여럿이 그 사이 낡아,
  착수 전에 19건을 통째로 실물과 대조해야 했다(2026-08-29). 보고서 쪽의 `가정 목록` 칸과 같은 갈래이고,
  그 칸의 규정은 각 에이전트 정의에 있다 — 여기서 되풀이하지 않는다.
- Fixed-type artifacts already have their place. Use these as is; do not invent a parallel location.

  | Kind | Path | Schema |
  | --- | --- | --- |
  | 검증 판정 | `<메인 저장소 루트>/.codex/review/<unit>.verdict.md` | `.codex/conventions/verdict-protocol.md` |
  | 진행 단계 | `<메인 저장소 루트>/.codex/perf/units/<unit>.status` | `.codex/conventions/status-protocol.md` |
  | 범위 헌장(스펙) | `docs/specs/<ID>-<slug>.md` | `.agents/skills/clarify/SKILL.md` |

- **`<ID>` and `<unit>` are not named on the spot.** Where the work has a Work Order, its ID names the spec file and its
  sub-unit IDs (`<ID>-A`, `<ID>-B`) are used as the `<unit>` above, verbatim. Form, issuance, and which work gets one:
  `.codex/conventions/work-order.md` — do not restate those rules here.
- **Anchor the root to the main repository**, by the same `git rev-parse --git-common-dir` anchor the status protocol
  specifies (it is written out there once — do not re-derive it here). A path written relative inside an isolated
  worktree is invisible to the reader in main.
- `.codex/review/` and `.codex/perf/` are gitignored session artifacts. Anything under `docs/` is committed —
  which one applies follows from the path, not from the worker's judgement.
- **A directory can declare "local only" through its own `.gitignore` — read that intent before committing into it.**
  `docs/learn/` ignores everything but its own `.gitignore` (`*` plus `!.gitignore`) precisely so that nothing enters the
  repository until a human approves it, and `git add -f` overrides that contract without raising a single error. So a
  delegation that merely says "commit it" is not authority to force-add: check the receiving directory's `.gitignore`
  first, and where it says local-only, leave the file on disk and report it instead. To undo a force-add,
  `git rm --cached` — the files stay on disk.

## 2. 반환값 크기 한도 — what comes back

- **최종 답변은 20줄 이내: 요약 + 파일 경로.** When the final answer exceeds the output-token limit it is cut with
  `stop_reason: max_tokens` and the continuation is stored as a separate block — the caller receives only the last
  block, so **the front of a long answer is lost**. A short answer is not politeness here; it is the only answer
  that arrives intact.
- **Never return full file contents.** Quote only text that is load-bearing (an exact signature, the one wrong line).
- **Paths in the return are absolute.** A relative path resolves differently for the caller.
- Where an agent definition sets a tighter limit for one part (explorer's 10-line quote cap, implementer's 5-line
  change summary), **the tighter one wins** — 20줄 is the ceiling for the whole answer, not a per-section budget.
- **이 숫자의 단일 원본은 이 문서다.** 에이전트 정의(`explorer` / `implementer` / `planner` / `reviewer`)는 같은 숫자를
  일부러 되풀이한다 — 프롬프트는 항상 주입되지만 문서는 안 읽힐 수 있기 때문이다(.codex/conventions/operating-principles.md '기억' 절). 값을 바꿀 때는
  여기를 먼저 고치고 그 네 파일을 함께 훑는다. 되풀이해도 되는 것은 **숫자와 한 줄 사유**까지이고, 규약 문단 자체는 아니다.

## 3. 임시 작업공간 소유권 — who owns the scratch space

- Temporary files go to a **per-agent subdirectory of the scratchpad**: `<스크래치패드 루트>/<unit>/`, where `<unit>`
  is the unit name from the delegation prompt. The scratchpad root itself is supplied by the environment.
- **Where no unit name was given (a name-exempt delegation — lightweight, planner), the fallback is the agent kind
  *plus* a unique identifier the orchestrator writes into the delegation prompt** (`explorer-a`, `explorer-b`, …).
  The agent kind alone is not a fallback: two same-kind delegations running in parallel would share `<루트>/explorer/`,
  recreating exactly the overwrite this section exists to prevent. **Running two or more name-exempt delegations in
  parallel without such identifiers is forbidden** — the orchestrator either assigns them or serializes the delegations.
- **That subdirectory is a bare path, not a prepared workspace — create it yourself as the very first step.** Nothing
  creates it for the delegation, so a redirect into it dies before the command it belongs to ever runs. `mkdir -p
  "<스크래치패드 루트>/<unit>"` first, then work.
- **One agent writes to its own subdirectory only.** Not to a sibling unit's, not to the root. Two units that pick the
  same temp filename in a shared directory overwrite each other's data, and the overwrite raises no error — it surfaces
  only as a wrong result. Per-unit subdirectories remove the possibility instead of relying on unique naming.
- **Absolute path under that subdirectory — never `/…`, never bare-relative.** In Git Bash `/` is the MSYS root
  (`C:\Program Files\Git\`), so `> /after.txt` writes to a system root; `> after.txt` dirties whatever cwd it lands in.
- **Nothing under the scratchpad is a deliverable.** It is a workbench: intermediate data, generated fixtures, throwaway
  scripts. Anything meant to survive the delegation goes to a path from §1.
- **Never run `git commit` from the scratchpad.** The path falls outside the `includeIf` condition that supplies the git
  identity, so the commit aborts with no user configured (rc=128) — it looks like the work failed when only the working
  directory was wrong. Commits happen inside the repository, always.
- **Cleanup stays inside the same fence: delete only under your own subdirectory.** Do not clean up another unit's
  subdirectory, and do not treat a leftover one as garbage — a parallel unit may still be running in it. A delete aimed
  outside it is caught by the permission guard as a critical-path removal and stops to ask the user.

## 4. 코드 작업공간 — where code changes happen

**격리 방식 선택 기준의 단일 원본은 이 절이다.** 오케스트레이터는 아래 표만 보고 정한다 — 매번 즉흥 판단하지 않는다.

| 조건 | 선택 | 근거 |
| --- | --- | --- |
| 위임 단위끼리 **고칠 파일이 겹친다**, 또는 일꾼이 **저장소 전역 git 쓰기 명령**을 쓴다 | `isolation: worktree` | 두 축 중 **하나라도** 걸리면 격리한다. 동시 편집 충돌은 브랜치로 갈라지고, 워크트리는 작업트리·인덱스가 분리돼 있어 전역 명령의 사정거리도 실제로 끊긴다. 실패한 갈래는 브랜치를 버리면 끝난다. |
| **두 축 모두 아니다** — 파일 단위로 깨끗이 갈린다 **그리고** git 쓰기 명령을 쓸 일이 없다 | 공유 작업트리 + **일꾼 커밋 금지** | 겹침도 전역 명령도 없으면 격리가 막을 충돌도 없고 남는 건 오버헤드와 머지뿐이다. 커밋은 오케스트레이터가 검증 후 implementer 하나에게 순차 위임한다. **"커밋을 남기는 병렬 위임은 무조건 `isolation: worktree`"(`.codex/roles/orchestrator.md`)와 모순이 아니다 — 일꾼이 커밋하지 않으므로 그 규칙의 조건 자체가 성립하지 않는다.** |
| **git 미추적 대용량 자산이 있어야 검증**되는 작업 | 워크트리 **불가** → 공유 작업트리 | 미추적 파일은 워크트리에 따라오지 않는다(추적되는 것만 체크아웃된다). 그 자산 없이는 성공 기준을 검증할 수 없으므로, 격리를 고르면 일꾼이 "검증 불가"로 끝난다. |
| **워크트리가 필요한데 미추적 자산 검증도 필요**(1행과 3행 동시 성립) | **병렬 포기 → 순차 위임**(한 번에 하나씩) | 공유 트리는 1행의 사고를 막지 못하고 워크트리는 자산이 따라오지 않는다 — 두 공간 격리가 모두 막히면 남는 격리 축은 **시간**뿐이다. |

**무엇이 전역 명령인가 — 작업트리나 인덱스를 통째로 건드리는 쓰기 명령이다.** `git stash` · `reset` · `checkout` ·
`restore` · `clean`은 일꾼이 만진 파일만이 아니라 **저장소 전체**에 작용하므로, 파일이 안 겹쳐도 옆 일꾼의 미커밋
작업을 함께 쓸어간다. 읽기 명령(`status` · `log` · `show` · `diff` · `grep`)은 공유 작업트리에서도 안전하다 —
여기까지 위축될 필요는 없다.

**근거가 된 사고(파생 프로젝트, 2026-08)**: 파일이 겹치지 않아 공유 작업트리로 갔고 커밋도 금지했는데, `git stash`
한 번에 세 일꾼의 미커밋 작업이 한꺼번에 날아갔다. 파일 교집합만 보는 판단은 이 사고를 막지 못한다.

**대가 — 공유 작업트리는 롤백이 파일 단위가 된다.** 워크트리였다면 브랜치를 버리는 것으로 끝날 일을, 여기서는 되돌릴
파일을 손으로 골라내야 한다. 그래서 **시작 시점의 `git status`가 clean이어야 안전하다** — 그때만 "지금 생긴 변경 =
이번 위임의 변경"이 되어 되돌릴 대상이 특정된다. clean이 아니면 먼저 정리하거나 워크트리로 간다.

**그리고 공유 쪽 조건에는 물리 강제력이 없다.** "git 쓰기 명령을 쓰지 않는다"는 위임장에 적힌 문장일 뿐이고, 일꾼이
마음먹으면 그대로 실행된다. 워크트리만이 구조로 막는다. 즉 공유 작업트리를 고르는 것은 완전한 방어가 아니라
**오버헤드를 아끼는 비용 대비 타협**이고, 조금이라도 의심스러우면 워크트리가 맞다.

In Orca mode the Orca worktree is the only track regardless (`.codex/rules/orca.md`).
