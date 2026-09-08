---
name: webapp-testing
description: "Drives a local web app in a real headless browser with Python + Playwright — loads pages, clicks through flows, captures screenshots and console/network errors. Use when verifying a UI/frontend change actually works, when reproducing a report like \"white screen\" / \"404 in the console\" / \"the button does nothing\", or when a review needs evidence beyond passing tests."
---

먼저 .codex/conventions/codex-runtime.md를 읽는다. 사용자 승인 범위와 현재 도구 구성이 우선한다.

<!--
Ported from anthropics/skills — https://github.com/anthropics/skills/tree/main/skills/webapp-testing
Licensed under the Apache License 2.0; full terms in LICENSE.txt next to this file.
Local deviations: prerequisites section, artifact paths moved to the OS temp dir
(the upstream `/mnt/user-data/outputs` path only exists in the claude.ai sandbox),
and a locally added "Visual regression (odiff)" section (not in upstream).
-->

# Web Application Testing

To test local web applications, write native Python Playwright scripts and run them with `Bash`.
This is a **local script toolkit, not an MCP server** — no extra tool grant and no restart is needed to use it.

**Helper Scripts Available**:
- `scripts/with_server.py` - Manages server lifecycle (supports multiple servers)

**Always run scripts with `--help` first** to see usage. DO NOT read the source until you try running the script first and find that a customized solution is absolutely necessary. These scripts can be very large and thus pollute your context window. They exist to be called directly as black-box scripts rather than ingested into your context window.

## Prerequisites

Playwright and its browser binaries must be installed on the machine (`pip install playwright && playwright install`).
If the import or the browser launch fails, say so in one line and fall back to non-browser verification — do not silently skip the check and do not report a pass you did not observe. The exact install command for this machine belongs in `docs/MACHINE.md`, not in a tracked knowledge file.

## Decision Tree: Choosing Your Approach

```
User task → Is it static HTML?
    ├─ Yes → Read HTML file directly to identify selectors
    │         ├─ Success → Write Playwright script using selectors
    │         └─ Fails/Incomplete → Treat as dynamic (below)
    │
    └─ No (dynamic webapp) → Is the server already running?
        ├─ No → Run: python scripts/with_server.py --help
        │        Then use the helper + write simplified Playwright script
        │
        └─ Yes → Reconnaissance-then-action:
            1. Navigate and wait for networkidle
            2. Take screenshot or inspect DOM
            3. Identify selectors from rendered state
            4. Execute actions with discovered selectors
```

## Example: Using with_server.py

To start a server, run `--help` first, then use the helper:

**Single server:**
```bash
python scripts/with_server.py --server "npm run dev" --port 5173 -- python your_automation.py
```

**Multiple servers (e.g., backend + frontend):**
```bash
python scripts/with_server.py \
  --server "cd backend && python server.py" --port 3000 \
  --server "cd frontend && npm run dev" --port 5173 \
  -- python your_automation.py
```

To create an automation script, include only Playwright logic (servers are managed automatically):
```python
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True) # Always launch chromium in headless mode
    page = browser.new_page()
    page.goto('http://localhost:5173') # Server already running and ready
    page.wait_for_load_state('networkidle') # CRITICAL: Wait for JS to execute
    # ... your automation logic
    browser.close()
```

## Reconnaissance-Then-Action Pattern

1. **Inspect rendered DOM**:
   ```python
   page.screenshot(path=screenshot_path, full_page=True)
   content = page.content()
   page.locator('button').all()
   ```

2. **Identify selectors** from inspection results

3. **Execute actions** using discovered selectors

## Common Pitfall

❌ **Don't** inspect the DOM before waiting for `networkidle` on dynamic apps
✅ **Do** wait for `page.wait_for_load_state('networkidle')` before inspection

## Best Practices

- **Use bundled scripts as black boxes** - To accomplish a task, consider whether one of the scripts available in `scripts/` can help. These scripts handle common, complex workflows reliably without cluttering the context window. Use `--help` to see usage, then invoke directly.
- Use `sync_playwright()` for synchronous scripts
- Always close the browser when done
- Use descriptive selectors: `text=`, `role=`, CSS selectors, or IDs
- Add appropriate waits: `page.wait_for_selector()` or `page.wait_for_timeout()`
- **Write automation scripts and screenshots outside the repo** — use the OS temp dir (`tempfile.gettempdir()`), never a path inside the working tree, so verification leaves no untracked artifacts behind.
- Report what was actually observed (page title, visible text, console errors, screenshot path). A screenshot that was never taken is not evidence.

## Visual regression (odiff)

When a change touches an **existing** screen and a before/after screenshot pair exists, compare the two pixel-by-pixel with odiff (a single binary, fetched on demand) — always through the repo's runner script, never a bare `npx` call (the repo's own deny list blocks bare `npx` on the command line; the script route is the one that runs):

```bash
bash scripts/odiff-compare.sh <before.png> <after.png> [diff.png]
```

- **rc 0 = the images match** (or the tool is absent — the script prints a `SKIP:` line and exits 0). **rc 21/22 = the images differ**, and a diff image highlighting the changed pixels is written to `[diff.png]` when that argument was given. **Any other rc is a tool or input error, not a detected difference** — do not report it as "the screens changed".
- All three paths go in the **OS temp dir** (`tempfile.gettempdir()`) — same artifact rule as screenshots above, never inside the working tree.
- **If Node/npx is missing**, say so in one line and fall back to eyeball comparison of the two screenshots — do not silently skip the check, and do not report a pixel-level match you did not measure (same shape as the Prerequisites fallback).
- **Baseline prerequisite**: `<before.png>` must have been captured from the pre-change state. If no baseline can be obtained (a brand-new screen, or the pre-change state is already gone), this check does not fire — there is nothing to compare against, and that is a stated limitation, not a failure.

## Reference Files

- **examples/** - Examples showing common patterns:
  - `element_discovery.py` - Discovering buttons, links, and inputs on a page
  - `static_html_automation.py` - Using file:// URLs for local HTML (mockups included)
  - `console_logging.py` - Capturing console logs during automation
