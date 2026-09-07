---
paths:
  - "**/*.html"
  - "**/*.css"
  - "**/*.tsx"
  - "**/*.jsx"
  - "**/*.vue"
  - "**/*.svelte"
  - "**/tailwind.config.*"
---

# Font rules — Geist + Pretendard (forced default)

> Manual reference: where `paths:` conditional loading is unsupported, read this file directly when starting any work that has a screen — mockups, HTML prototypes, product UI.

Applies to every visual artifact (mockups, artifacts, prototypes, product UI). **This is not a recommendation but a default you apply without asking.** Two exceptions, and only these two — in both, record the deviation in one line at the top of the artifact:

- An explicit user instruction naming different fonts — follow it.
- An artifact that cannot reach the network (an offline single file): drop the §2 CDN links only, and keep the §1 stack exactly as written.

## 1. Stacks (copy as is, keep the order)

```
본문/UI : Geist, Pretendard, sans-serif
코드/모노: "Geist Mono", D2Coding, monospace
```

- The order is the point: **Geist for Latin, Pretendard as the Hangul fallback** — reverse it and Pretendard swallows the Latin glyphs.
- **Never fall back to OS default fonts.** Do not put `-apple-system`, `system-ui`, `ui-sans-serif`, `ui-monospace`, or `Segoe UI` in the stack. The trailing `sans-serif`/`monospace` are CSS generic keywords (not specific fonts), so always keep them as the last line of defense, and add nothing in front of them.

## 2. CDN loading (very top of `<head>`, before stylesheets and scripts)

```html
<!-- Pretendard (한글, 가변 + 동적 서브셋) -->
<link rel="stylesheet" href="https://cdn.jsdelivr.net/gh/orioncactus/pretendard@latest/dist/web/variable/pretendardvariable-dynamic-subset.min.css" />
<!-- Geist / Geist Mono (라틴·숫자·코드) -->
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Geist:wght@100..900&family=Geist+Mono:wght@100..900&display=swap" />
```

Always load with `font-display: swap` (the Google Fonts URL above includes `&display=swap`; self-hosted `@font-face` states it explicitly). It shrinks the window in which a different font is silently painted on load failure or delay.

## 3. Plain CSS / Tailwind

```css
:root { --font-sans: Geist, Pretendard, sans-serif; --font-mono: "Geist Mono", D2Coding, monospace; }
body { font-family: var(--font-sans); }
code, pre, kbd, samp { font-family: var(--font-mono); }
```

```css
/* Tailwind v4 (CSS-first) */
@theme { --font-sans: Geist, Pretendard, sans-serif; --font-mono: "Geist Mono", D2Coding, monospace; }
```

```js
// Tailwind v3 — extend.fontFamily가 기본 font-sans/font-mono를 덮어쓴다
fontFamily: { sans: ["Geist", "Pretendard", "sans-serif"], mono: ['"Geist Mono"', "D2Coding", "monospace"] }
```

## 4. Notes

- These font CDN links are one of exactly two CDN exceptions to the "no external dependencies" rule for mockups — the other is the Basecoat CDN (single source: `.claude/skills/kickoff/SKILL.md` 4-1). Everything else stays inline / self-contained.
- Offline and air-gapped work does not change the stack either — the generic keywords are the intended last-resort fallback.
