---
paths:
  - "**/*.html"
  - "**/*.css"
  - "**/*.tsx"
  - "**/*.jsx"
  - "**/*.vue"
  - "**/*.svelte"
  - "**/components/**"
---

# Frontend design rules — a UI nobody mistakes for someone else's

> Manual reference: where `paths:` conditional loading is unsupported, read this file together with `.claude/rules/fonts.md` when starting mockup or UI work. Pass both paths in the delegation prompt as well.

Source: the `frontend-design` skill from anthropics/skills (Apache-2.0), adapted for this repository — the original's "pick a new typeface for every project" was replaced by the forced font default (`.claude/rules/fonts.md`).

Approach it like the design lead of a small studio: a template-looking proposal is a client already lost. Make deliberate choices in palette, layout, and detail that belong to this brief alone, and take one aesthetic risk you can justify.

## 0. Stack premise — do not rewrite primitives without checking

In a React project, shadcn/ui is the component default. Before building a new component, always check: (1) does the project's existing `components/ui/` already have it, (2) does shadcn/ui already provide it (see <https://ui.shadcn.com/llms.txt>). Buttons, dialogs, inputs, and tables already exist — extend what is there, and spend differentiation on composition, tokens, and signature elements.

- This does not mean shipping the default shadcn look as is — restyle with Tailwind theme tokens. Do not let the stack decide your palette or type scale.
- Nor is it a stack mandate — the template is stack-neutral, so on a non-React project (or when the user specifies another stack), ignore this section only and apply the rest as written.
- When the stack is not decided yet — kickoff Stage 1 deliberately does not choose one — apply from this section only the name-collision guard below, and leave the shadcn-first check until a stack exists to check against.
- **Mockup ↔ implementation continuity**: the static HTML mockup uses Basecoat classes (`.claude/skills/kickoff/SKILL.md` 4-1 — shadcn look without React), and the implementation maps each of them onto the same-named shadcn/ui component — the look, tokens, and component vocabulary carry over even though the markup is rewritten as JSX.
- **Never give a palette variable a name shadcn already owns.** The preset claims, among others, `--background`, `--foreground`, `--primary`, `--secondary`, `--accent`, `--muted`, `--card`, `--popover`, `--destructive` (most of them with a `-foreground` pair), `--border`, `--input`, `--ring`, and `--radius` inside `:root` — treat that as an open set and read the `:root` block of the project's own shadcn stylesheet for the names that actually apply. Those names are mapping slots, not colors of yours: give the palette its own names (`--brand-teal`, `--brand-ink`) and let the shadcn variable reference them through `var()`.
  - Declaring the same name twice in the same `:root` raises no error — within one cascade layer the later declaration silently wins, and derived variables (`--ring: var(--accent)`) inherit the value you did not mean. Unlayered **normal** declarations beat layered ones regardless of order, so a collision is harder to reason about than "the last one wins" suggests. A focus ring mapped to a faint selection surface is then present but invisible, which is the expensive kind to debug. (Observed in another repository — not measured in this one.)
  - `@theme`'s `--color-accent` and `:root`'s `--accent` are different names in different namespaces, so they never collide. That is the way out of an existing overlap: keep the palette meaning in `:root`, and expose only the value the utilities (`bg-accent`) consume as `--color-accent` in `@theme inline`.
  - **Check after defining the palette: `--primary`, `--ring`, and `--accent` must not converge on one value.** Fill = border = selection border leaves no color left to draw "selected" with — the selected element renders identically to the unselected one, so clicks look like they did nothing. This is a separate failure from the name collision and survives renaming, so check the resolved values, not just the names.

## 1. Root it in a subject

If the brief does not pin down a product or subject, pin it down yourself before designing: one concrete subject, its audience, and the page's single job — and state your choice. The subject's world — materials, tools, outputs, vocabulary — is the source of differentiating choices.

## 2. Design principles

- **The hero is the argument.** Open with the most distinctive thing in the subject's world. A big number + small label + gradient accent is the template answer — use it only when it is genuinely the best one.
- **Typography follows the font rules** — typeface choice is not an axis of discretion. The design axes left to you are type scale, hierarchy, weight, letter-spacing, and line-height. Build a deliberate scale and rhythm inside the fixed families.
- **Structure is information.** Numbers, eyebrows, rules, and labels are for encoding properties that are actually true of the content — do not stamp 01/02/03 onto something that has no real order.
- **Motion with intent.** One orchestrated moment beats scattered effects. Over-animation adds to the AI-generated smell.
- **Match complexity to the vision.** Maximalism needs refined execution; minimalism needs precision in spacing, type, and detail.

## 3. Process: plan first, then code

`docs/design-system.md`가 있으면 토큰 단일 원본은 그 파일이다: 새로 계획하지 말고 그 토큰에서 파생한다.

AI-generated design today converges on three defaults: (1) cream background + high-contrast serif + terracotta, (2) black background + fluorescent single-color accent, (3) newspaper layout with hairline rules. If the brief specifies a visual direction, follow it, and do not spend your free axes on these defaults.

Work in two stages. First plan a compressed token system — **color** (4–6 named hex values), **type** (size, weight, and spacing per display/body/utility role), **layout** (a one-sentence description + an ASCII wireframe), **signature** (the single element this page will be remembered for). Compare the plan against the brief and fix the parts that are "defaults any similar brief would also produce" — only then write code, deriving every color and type decision from the plan.

- Watch CSS selector specificity: `.section`-style and element selectors easily cancel each other out (frequent in inter-section padding/margin).
- Do planning and iteration inside your thinking; show the user only what you are confident in.

## 4. Restraint and self-critique

Spend boldness in one place only — one signature element that stays in memory, with everything around it quiet and disciplined. Hold the quality floor without announcing it: responsive down to mobile, visible keyboard focus, reduced motion respected. Self-critique with screenshots as you build.

## 5. Copy

Words are design material, not decoration — they exist for one purpose: to aid understanding and make writing easy.

- Write from the user's side: name things by what people perceive, not by system structure ("웹훅 설정"이 아니라 "알림 관리").
- Active voice by default; a control says exactly what will happen ("Submit"이 아니라 "변경사항 저장"). The same action keeps the same name throughout the flow — the toast for a "게시" button is "게시됨".
- Failures and empty screens are moments for giving direction, not for atmosphere: what went wrong and how to fix it, without apology, without ambiguity. An empty screen is an invitation to act.
- Tune the tone conversational: plain verbs, sentence case, no filler. Each element does exactly one thing.

## 6. On-screen text — titles stand alone (forced default)

A forced default like the font rules, not a preference — it applies to **mockups and real screen implementations alike**.

- **No standing descriptive subtitle under a title or feature name — the title only.** A screen where every heading carries its own explanation sentence reads as documentation, not a UI; if a name needs a sentence to be understood, fix the name instead. 사용자 원문 예시: "'계정 · 수집 대상 / 맨 위 표에서 계정을 하나 고르면 그 계정의 설정이 가운데 한자리에 모인다. …' 이런식으로 제목이나 기능 이름마다 부제붙는거 없애기."
- **Guidance text — empty-list notices, input help, and the like — only where it is genuinely needed, and short.** One short line where the user would otherwise be stuck; nowhere else. This bounds §5's empty-screen direction, it does not repeal it: give direction where action is blocked, not a caption on every field.

## 7. Tables — the baseline shape (forced default)

Same force and same scope as §6 — mockups and implementations both. The complaint this rule answers, verbatim: "열 폭·정렬이 제멋대로, 선이 어디는 있고 어디는 없음, 표 같지 않게 생김, 테이블 제목이 1행 위치에 있고 제목행이 상부 경계없이 제목행과 붙어있어서 어색함, 표에 대한 검수가 부족함."

- **Borders are consistent.** Pick one border scheme per table (full grid, horizontal rules only, …) and hold it — never lines in some places and none in others.
- **The table title (caption) is visually separated from the header row.** The caption is not row 1 of the table: it sits outside the body with its own spacing, and the header row keeps its own top boundary instead of butting against the caption.
- **Column width and alignment follow the data.** Numbers right-aligned, text left-aligned, each header aligned with its column; widths set deliberately, not left to whatever wraps.
- **A table looks like a table.** Header row visually distinct from body rows; row height and cell padding consistent throughout.

## 8. Flow design — fewer steps to the value (forced default)

Same force and same scope as §6 — mockups and implementations both. The complaint this rule answers, verbatim: "기본적으로 지금은 너무 프론트엔드 설계가 유저친화적이지않고 , 기능도 너무 스텝이 많아.하나의 기능을 쓰기위해서 건너가는단계도 많고, 좀 창의적이고 각 솔루션 목적에 맞는 세부 설계가 되었으면 좋겠어."

1. **One purpose per screen.** Limit each screen to the one question it answers and push the rest to the next screen. Forms especially: one thing per page ([GOV.UK design notes](https://designnotes.blog.gov.uk/2015/07/03/one-thing-per-page/)).
2. **The core-journey step table is mandatory.** Before mockup approval, lay out "진입 → 핵심 가치 도달" as an ordered list of screens and state the step count. There is deliberately **no click-count cap** — the 3-click rule has no data behind it ([NN/g](https://www.nngroup.com/articles/3-click-rule/)); instead check that each step convinces the user the next one is worth taking.
3. **One primary CTA on the first screen.** More choices slow the decision ([Hick's Law](https://lawsofux.com/hicks-law/)). Landing and solution pages: one primary CTA plus at most one secondary.
4. **Defaults do the work.** Do not open with settings screens or wizards — start usable on smart defaults, and reveal advanced options progressively ([NN/g progressive disclosure](https://www.nngroup.com/articles/progressive-disclosure/)).
5. **Empty and failure screens direct action** — already forced by §5; the review connects to it here instead of restating it.
6. **The 3-second review question.** For every screen at the mockup approval gate: "사용자가 지금 할 일이 3초 안에 보이는가."
7. **Default flow per purpose.** 랜딩 = 단일 CTA 흐름, 대시보드 = 현황 확인 → 즉시 행동, 폼 = 한 가지씩. These are the floor under the flow only — creativity (the signature element) stays with §2–§4.

## 9. shadcn ecosystem reference table

Where to reach first before hand-building a block or a theme — React+shadcn projects only (on a non-React stack this table does not apply; daisyUI is the fallback there, see the note below). Verdicts summarize `docs/research/2026-09-07-frontend-kit-survey.md` §1 (measured 2026-09-07); go there for stars, licenses, and the full candidate list. kibo·cult-ui·svgl 3행의 근거는 `docs/specs/2026-09-07-frontend-absorption.md`다.

| Source | What it is | Verdict (survey §1) |
| --- | --- | --- |
| [공식 Blocks](https://ui.shadcn.com/blocks) | Dashboard/login/sidebar blocks, free open code, `npx shadcn add <이름>` | First stop for app screens — installs straight into the project |
| [Magic UI](https://github.com/magicuidesign/magicui) | Landing/marketing blocks on top of shadcn (MIT, 22k+) | First candidate for landing blocks — design and code carry into implementation |
| [Tailark](https://github.com/tailark/blocks) | Marketing blocks installed via the shadcn CLI (MIT) | Same registry path as 공식 Blocks — use alongside Magic UI |
| [tweakcn](https://github.com/jnsahaj/tweakcn) | Theme editor that exports shadcn CSS variables (Apache-2.0, 10k+) | Theme-settling tool — the exported variables feed mockup and implementation both |
| [HyperUI](https://github.com/markmead/hyperui) | Copy-paste Tailwind markup sections (MIT, 12k+) | Landing-section source — same Tailwind markup, easy to port |
| [awesome-shadcn-ui](https://github.com/birobirobiro/awesome-shadcn-ui) | Curated index of the shadcn ecosystem (20k+) | Exploration entry point when the rows above do not cover the need |
| [Kibo UI](https://github.com/shadcnblocks/kibo) | Advanced-component registry on top of shadcn — editor, Gantt, kanban and the like (MIT, 3.9k, measured 2026-09-07) | 비고: 최근 push 4개월 정체(2026-05-04) — 채택 전 유지보수 상태 재확인 |
| [cult-ui](https://github.com/nolly-studio/cult-ui) | Animation/marketing components in the shadcn family (MIT, 6.1k, measured 2026-09-07, push 2026-07-22) | 비고: Magic UI와 일부 겹침 — 겹치는 자리는 Magic UI 먼저 |
| [svgl](https://github.com/pheralb/svgl) | Brand-logo SVG collection (MIT, 6.2k, measured 2026-09-07, push 2026-09-04) | 랜딩의 로고 월·기술 스택 표기용 |

- Replacing shadcn itself was surveyed and **rejected** (survey §3): the copy-ownership model, 공식 Blocks, the theme tooling, and the marketing-block ecosystem exist only around shadcn, and §0's "do not rewrite primitives" leans on exactly that. [daisyUI](https://github.com/saadeghi/daisyui) stays only as the fallback for non-React stacks — its class system does not carry into a shadcn implementation.

## 10. 표면별 기본값 — 아이콘·차트·이메일

React+shadcn 프로젝트 기준의 기본값이다 (§9와 같은 전제).

- **아이콘 = lucide** (shadcn 기본 아이콘 세트). 구현은 `lucide-react`, 목업은 unpkg의 lucide-static SVG(`https://unpkg.com/lucide-static@latest/icons/<이름>.svg`)로 **같은 아이콘 이름**을 쓴다 — 목업↔구현 연결. 목업 CDN 예외 목록의 단일 원본은 `.claude/skills/kickoff/SKILL.md` 4-1이다.
- **차트 = recharts** (shadcn Charts가 딛는 라이브러리). 차트는 shadcn Charts 경유가 1순위, 그 밖은 recharts 직접.
- **이메일 = react-email**. 단, 이메일 표면이 실제로 요청된 프로젝트에서만 — 미리 깔지 않는다(투기 금지).
- **비React 스택**: 아이콘(lucide-static SVG)만 그대로 적용 가능하고, 차트·이메일 기본값은 미적용이다.

## 11. 참고 링크 (전재 금지)

본문 전재 금지 — 링크로만 참조한다.

- [awesome-claude-design](https://github.com/VoltAgent/awesome-claude-design) — 디자인 시스템 프롬프트 큐레이션 인덱스 (MIT).
- [Claude-Code-Frontend-Design-Toolkit](https://github.com/wilwaldon/Claude-Code-Frontend-Design-Toolkit) — 프론트엔드 산출물 개선 도구 모음. **라이선스 없음 — 링크만 가능, 본문·코드 복사 불가.**
