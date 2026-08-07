# Ported from anthropics/skills (skills/webapp-testing/examples/static_html_automation.py).
# https://github.com/anthropics/skills - Apache License 2.0, see ../LICENSE.txt
# Deviations: screenshots go to the OS temp dir instead of the sandbox-only
# /mnt/user-data/outputs path, and the file:// URL is built with pathlib so it
# also works on Windows drive-letter paths.

import os
import tempfile
from pathlib import Path

from playwright.sync_api import sync_playwright

# Example: Automating interaction with static HTML files using file:// URLs
# (this is also how a mockup produced by /kickoff gets checked)

html_file_path = Path('path/to/your/file.html').resolve()
file_url = html_file_path.as_uri()

out_dir = tempfile.gettempdir()

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page(viewport={'width': 1920, 'height': 1080})

    # Navigate to local HTML file
    page.goto(file_url)

    # Take screenshot
    page.screenshot(path=os.path.join(out_dir, 'static_page.png'), full_page=True)

    # Interact with elements
    page.click('text=Click Me')
    page.fill('#name', 'John Doe')
    page.fill('#email', 'john@example.com')

    # Submit form
    page.click('button[type="submit"]')
    page.wait_for_timeout(500)

    # Take final screenshot
    page.screenshot(path=os.path.join(out_dir, 'after_submit.png'), full_page=True)

    browser.close()

print(f"Static HTML automation completed! Screenshots in {out_dir}")
