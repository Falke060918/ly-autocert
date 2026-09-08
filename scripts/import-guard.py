#!/usr/bin/env python3
"""Check that importing product code did not replace either live agent runtime."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
PROTECTED = ("AGENTS.md", "CLAUDE.md", ".claude", ".codex", ".agents")
EPHEMERAL = tuple(f"{runtime}/{directory}/" for runtime in (".claude", ".codex")
                  for directory in ("perf", "index", "review", "snapshots", "tmp", "worktrees"))


def inventory(root):
    # Include ignored static settings too; hashes never expose their secret values.
    # Exclude only known ephemeral artifacts so incoming ignore rules cannot hide code.
    result = subprocess.run(["git", "-C", str(root), "ls-files", "-z", "--cached", "--others",
                             "--", *PROTECTED], check=True,
                            capture_output=True)
    files = {}
    for raw in set(result.stdout.split(b"\0")) - {b""}:
        name = raw.decode("utf-8")
        if name.startswith(EPHEMERAL) or name.startswith(".claude/compact-snapshot.md") or "__pycache__" in Path(name).parts:
            continue
        path = root / name
        if path.is_symlink():
            files[name] = "symlink:" + os.readlink(path)
        elif path.is_file():
            files[name] = hashlib.sha256(path.read_bytes()).hexdigest()
        else:
            files[name] = "missing"
    return dict(sorted(files.items()))


def compare(snapshot, root):
    if snapshot.get("root") != str(root.resolve()):
        raise ValueError("Snapshot belongs to another checkout")
    before = snapshot["files"]
    after = inventory(root)
    return [name for name in sorted(before.keys() | after.keys()) if before.get(name) != after.get(name)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("snapshot", "check"))
    parser.add_argument("--snapshot", type=Path, required=True)
    parser.add_argument("--root", type=Path, default=ROOT, help=argparse.SUPPRESS)
    args = parser.parse_args()
    try:
        root = args.root.resolve()
        if args.command == "snapshot":
            data = {"root": str(root), "files": inventory(root)}
            if any(data["files"].get(name) in (None, "missing") for name in ("AGENTS.md", "CLAUDE.md")):
                raise ValueError("Both live instruction entrypoints must exist before import")
            args.snapshot.parent.mkdir(parents=True, exist_ok=True)
            # Never replace the before-import evidence with an after-import snapshot.
            with args.snapshot.open("x", encoding="utf-8") as handle:
                json.dump(data, handle, ensure_ascii=False, indent=2)
            print("Saved protected runtime fingerprints; no source text or credentials copied.")
        else:
            changed = compare(json.loads(args.snapshot.read_text(encoding="utf-8")), root)
            if changed:
                print("Import changed protected live runtime paths; restore the pre-import copies before committing:", file=sys.stderr)
                for name in changed:
                    print(name, file=sys.stderr)
                return 1
            print("Both live runtimes are unchanged.")
        return 0
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as exc:
        print(str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
