#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
UI/UX Pro Max Search - BM25 search engine for UI/UX style guides

Trimmed port from nextlevelbuilder/ui-ux-pro-max-skill (MIT; see LICENSE.txt).
Local deviations: the per-stack search (--stack) and the design-system
generator (--design-system / --persist) are removed with their data; only
the 6 bundled domains remain.

Usage: python search.py "<query>" [--domain <domain>] [--max-results 3]

Domains: style, color, chart, ux, typography, reasoning
(omit --domain to auto-detect from the query)
"""

import argparse
import json as json_module
import sys
import io
from core import CSV_CONFIG, MAX_RESULTS, UNTRUNCATED_COLS, search

# Force UTF-8 for stdout/stderr to handle emojis on Windows (cp1252 default)
if sys.stdout.encoding and sys.stdout.encoding.lower() != 'utf-8':
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')
if sys.stderr.encoding and sys.stderr.encoding.lower() != 'utf-8':
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8')

TRUNCATE_AT = 300


def format_output(result, full=False):
    """Format results for Claude consumption (token-optimized)"""
    if "error" in result:
        return f"Error: {result['error']}"

    output = []
    output.append("## UI Pro Max Search Results")
    domain_note = result['domain']
    if result.get("auto_detected"):
        domain_note += " (auto-detected"
        if result.get("runner_up_domain"):
            domain_note += f", runner-up: {result['runner_up_domain']}"
        domain_note += ")"
    output.append(f"**Domain:** {domain_note} | **Query:** {result['query']}")
    output.append(f"**Source:** {result['file']} | **Found:** {result['count']} results\n")

    if result['count'] == 0:
        output.append(
            "No matches. This is not a match with an empty value -- the query "
            "did not hit the database. Retry with broader/different keywords "
            "before falling back to general defaults, and say explicitly that "
            "no database match was found if you do fall back."
        )
        suggestions = result.get("suggestions") or []
        if suggestions:
            output.append(f"**Closest known terms:** {', '.join(suggestions)}")
        return "\n".join(output)

    for i, row in enumerate(result['results'], 1):
        output.append(f"### Result {i}")
        for key, value in row.items():
            value_str = str(value)
            if not full and key not in UNTRUNCATED_COLS and len(value_str) > TRUNCATE_AT:
                value_str = value_str[:TRUNCATE_AT] + "..."
            output.append(f"- **{key}:** {value_str}")
        output.append("")

    return "\n".join(output)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="UI Pro Max Search")
    parser.add_argument("query", help="Search query")
    parser.add_argument("--domain", "-d", choices=list(CSV_CONFIG.keys()), help="Search domain")
    parser.add_argument("--max-results", "-n", type=int, choices=range(1, 21), default=MAX_RESULTS,
                        metavar="1-20", help="Max results (default: 3)")
    parser.add_argument("--json", action="store_true", help="Output as JSON")
    parser.add_argument("--full", action="store_true", help="Do not truncate long field values in text output")

    args = parser.parse_args()

    result = search(args.query, args.domain, args.max_results)
    if args.json:
        print(json_module.dumps(result, indent=2, ensure_ascii=False))
    else:
        print(format_output(result, full=args.full))
