#!/usr/bin/env python3
"""Render the full main role into Codex's managed developer-instructions block."""
import argparse
import json
from pathlib import Path
import re
import sys
import tomllib

ROOT = Path(__file__).resolve().parent.parent
BEGIN = "# BEGIN GENERATED ORCHESTRATOR"
END = "# END GENERATED ORCHESTRATOR"


def render(config, role):
    data = tomllib.loads(config)
    block = BEGIN + "\n" + "developer_instructions = " + json.dumps(role, ensure_ascii=False) + "\n" + END
    pattern = re.compile(r"(?m)^" + re.escape(BEGIN) + r"\n.*?^" + re.escape(END) + r"$", re.S)
    matches = list(pattern.finditer(config))
    if matches:
        if len(matches) != 1 or config.count(BEGIN) != 1 or config.count(END) != 1:
            raise ValueError("Ambiguous generated block; preserve the config and repair its markers manually")
        updated = pattern.sub(lambda _: block, config, count=1)
    else:
        if BEGIN in config or END in config or "developer_instructions" in data:
            raise ValueError("Unmanaged developer_instructions/markers found; refusing to overwrite user instructions")
        updated = block + "\n\n" + config
    parsed = tomllib.loads(updated)
    if parsed.get("developer_instructions") != role:
        raise ValueError("Generated block must be at the TOML top level")
    before = {k: v for k, v in data.items() if k != "developer_instructions"}
    after = {k: v for k, v in parsed.items() if k != "developer_instructions"}
    if before != after:
        raise ValueError("Refusing an update that changes unrelated configuration")
    return updated


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="Fail on drift without changing any file")
    parser.add_argument("--root", type=Path, default=ROOT, help=argparse.SUPPRESS)
    args = parser.parse_args()
    try:
        path = args.root / ".codex/config.toml"
        config = path.read_text(encoding="utf-8")
        role = (args.root / ".codex/roles/orchestrator.md").read_text(encoding="utf-8")
        updated = render(config, role)
        if args.check and updated != config:
            print("Orchestrator drift: review the role, then run python scripts/sync-orchestrator.py", file=sys.stderr)
            return 1
        if not args.check and updated != config:
            path.write_text(updated, encoding="utf-8", newline="\n")
        print("Orchestrator developer_instructions are current.")
        return 0
    except (OSError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
