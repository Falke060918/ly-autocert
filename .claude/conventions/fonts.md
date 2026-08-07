# Font convention — Geist + Pretendard (forced default)

Applies to every visual artifact: `/kickoff` mockups, HTML prototypes, artifacts, design
samples, and the real product UI. This is **not a recommendation — it is the default that
gets applied without asking**. The only exception is an explicit user instruction naming a
different font; in that case follow the user and note the deviation in one line.

## 1. Stacks (copy verbatim, keep the order)

```
body / UI : Geist, Pretendard, sans-serif
code / mono: "Geist Mono", D2Coding, monospace
```

Order matters: **Geist first so Latin renders in Geist, Pretendard second so Hangul falls
back to Pretendard.** Reversing the two makes Pretendard swallow the Latin glyphs and the
whole point is lost.

**No OS-default fallback.** Never add `-apple-system`, `system-ui`, `ui-sans-serif`,
`ui-monospace`, `Segoe UI` or any other system font to these stacks. The only case where a
font other than Geist/Pretendard shows up on screen is a web font that failed to load. The
trailing `sans-serif`/`monospace` are CSS generic keywords, not specific families — keep
them as the last-resort guard and add nothing before them.

## 2. CDN loading (paste at the very top of `<head>`)

Put these links **first inside `<head>`**, before any stylesheet or script, so the fonts
start downloading as early as possible. Always load with `font-display: swap` (the Google
Fonts URL below carries `&display=swap`; for self-hosted `@font-face`, set it explicitly) —
it shortens the window in which a load failure or delay silently paints another font.

```html
<!-- Pretendard (Hangul, variable + dynamic subset) -->
<link rel="stylesheet" href="https://cdn.jsdelivr.net/gh/orioncactus/pretendard@latest/dist/web/variable/pretendardvariable-dynamic-subset.min.css" />

<!-- Geist / Geist Mono (Latin, numerals, code) -->
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Geist:wght@100..900&family=Geist+Mono:wght@100..900&display=swap" />
```

## 3. Pure CSS

```css
:root {
  --font-sans: Geist, Pretendard, sans-serif;
  --font-mono: "Geist Mono", D2Coding, monospace;
}

body {
  font-family: var(--font-sans);
}

code, pre, kbd, samp {
  font-family: var(--font-mono);
}
```

## 4. Tailwind

```js
// tailwind.config.js
module.exports = {
  theme: {
    extend: {
      fontFamily: {
        sans: ["Geist", "Pretendard", "sans-serif"],
        mono: ['"Geist Mono"', "D2Coding", "monospace"],
      },
    },
  },
};
```

Tailwind v4 (CSS-first config) uses the same values as theme variables:

```css
@import "tailwindcss";

@theme {
  --font-sans: Geist, Pretendard, sans-serif;
  --font-mono: "Geist Mono", D2Coding, monospace;
}
```

`extend.fontFamily` **overrides** Tailwind's default `font-sans`/`font-mono`, so `font-sans`
and `font-mono` utilities pick these up with no extra class.

## 5. Notes

- The mockup rule "no external dependencies" has exactly one exception: these font CDN
  links. Everything else in a mockup stays inline/self-contained.
- Offline or air-gapped work: keep the same stacks unchanged. Do not swap in a different
  family and do not re-add a system font "just for offline" — the browser's generic
  `sans-serif`/`monospace` is the intended last resort.
