#!/usr/bin/env python3
"""Validate the starter without calling a model or external services."""
import ast
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tomllib
ROOT = Path(__file__).resolve().parent.parent


def identity_errors(root=ROOT):
    errors = []
    identities = []
    for runtime in (".claude", ".codex"):
        path = root / runtime / "identity"
        if not path.is_file():
            errors.append("Missing identity: " + runtime)
            identities.append({})
            continue
        fields = {}
        for line in path.read_text(encoding="utf-8").splitlines():
            if ":" in line and not line.lstrip().startswith("#"):
                key, value = line.split(":", 1)
                fields[key.strip()] = value.strip()
        identities.append(fields)
    for key in ("identity", "origin", "setup", "template", "notion", "work-order-prefix", "purpose"):
        if identities[0].get(key) != identities[1].get(key):
            errors.append("Identity adapters disagree: " + key)
    if identities[0].get("identity") not in ("template", "project"):
        errors.append("Invalid unified identity")
    return errors


def validate():
    errors = identity_errors()
    config = tomllib.loads((ROOT / ".codex/config.toml").read_text(encoding="utf-8"))
    if config.get("approval_policy") not in ("never", "on-request", "untrusted"):
        errors.append("Invalid approval_policy")
    if config.get("sandbox_mode") not in ("workspace-write", "read-only", "danger-full-access"):
        errors.append("Invalid sandbox_mode")
    agents = list((ROOT / ".codex/agents").glob("*.toml"))
    for path in agents:
        data = tomllib.loads(path.read_text(encoding="utf-8"))
        if any(not isinstance(data.get(key), str) or not data[key] for key in
               ("name", "description", "developer_instructions")):
            errors.append(f"Incomplete custom agent: {path.name}")
    hooks = json.loads((ROOT / ".codex/hooks.json").read_text(encoding="utf-8"))["hooks"]
    for event, groups in hooks.items():
        if event not in {"SessionStart", "SessionEnd", "PreCompact", "PostCompact",
                         "PreToolUse", "PostToolUse", "SubagentStart", "SubagentStop", "Stop"}:
            errors.append("Unknown hook: " + event)
        for group in groups:
            for hook in group["hooks"]:
                if hook.get("type") != "command" or not hook.get("commandWindows") or not hook.get("command"):
                    errors.append("Incomplete portable hook: " + event)
    if (ROOT / "AGENTS.md").stat().st_size > 32768:
        errors.append("AGENTS.md exceeds default project instruction budget")
    skills = list((ROOT / ".agents/skills").glob("*/SKILL.md"))
    for path in skills:
        text = path.read_text(encoding="utf-8")
        front = re.match(r"\A---\n(.*?)\n---", text, re.S)
        if not front:
            errors.append(f"Missing skill frontmatter: {path}")
            continue
        name = re.search(r"^name:\s*(\S+)\s*$", front[1], re.M)
        desc = re.search(r"^description:\s*(.+)$", front[1], re.M)
        if not name or name[1] != path.parent.name or not desc or len(desc[1]) > 1024:
            errors.append(f"Invalid skill metadata: {path.parent.name}")
        if "codex-runtime.md" not in text:
            errors.append("Missing runtime contract: " + path.parent.name)
    claude = json.loads((ROOT / ".claude/settings.json").read_text(encoding="utf-8"))
    main_role = claude.get("agent")
    if not main_role or not (ROOT / ".claude/agents" / (main_role + ".md")).is_file():
        errors.append("Claude main agent is missing")
    claude_skills = list((ROOT / ".claude/skills").glob("*/SKILL.md"))
    if {p.parent.name for p in claude_skills} != {p.parent.name for p in skills}:
        errors.append("Claude/Codex skill catalogs differ")
    for event, groups in claude.get("hooks", {}).items():
        for group in groups:
            for hook in group.get("hooks", []):
                if hook.get("type") == "command":
                    for ref in re.findall(r"scripts/[A-Za-z0-9_.-]+", hook.get("command", "")):
                        if not (ROOT / ref).is_file():
                            errors.append(f"Missing Claude hook script: {event}: {ref}")
    for command in ([sys.executable, str(ROOT / "scripts/sync-orchestrator.py"), "--check"],):
        result = subprocess.run(command, capture_output=True, text=True, encoding="utf-8")
        if result.returncode:
            errors.append(result.stderr.strip() or result.stdout.strip())
    active = [ROOT / "AGENTS.md", ROOT / "CLAUDE.md", ROOT / "README.md", ROOT / "SETUP.md"]
    for directory in (".agents", ".codex", ".claude", "scripts", "tests"):
        active.extend(p for p in (ROOT / directory).rglob("*")
                      if p.is_file() and "__pycache__" not in p.parts
                      and p.suffix in (".md", ".py", ".sh", ".toml", ".json"))
    for path in active:
        text = path.read_text(encoding="utf-8")
        if path.suffix == ".py":
            ast.parse(text, filename=str(path))
        # Claude is an active runtime, not a forbidden legacy dependency. Only
        # Codex executable helpers must remain independent of Claude's process/env.
        if path.parent == ROOT / "scripts" and (path.name.startswith("codex-") or path.name == "starter.py"):
            if any(token in text for token in ("CLAUDE_PROJECT_DIR", "CLAUDE_CONFIG_DIR", "claude --agent", "claude -p")):
                errors.append("Cross-runtime dependency: " + str(path.relative_to(ROOT)))
        if path.suffix == ".md":
            pattern = chr(96) + r"((?:\.codex/|\.agents/)[^" + chr(96) + r"\s]+)" + chr(96)
            for ref in re.findall(pattern, text):
                if any(char in ref for char in ("<", "*", "{", "|", "…")):
                    continue
                if not (ROOT / ref).exists() and not any(part in ref for part in
                    ("/perf/", "/review/", "/index/", "/tmp/", "/worktrees/", "/snapshots/",
                     "/automode", "/local-notes.md", "/skills-archive")):
                    errors.append(f"Missing reference in {path.relative_to(ROOT)}: {ref}")
    bash = Path("C:/Program Files/Git/bin/bash.exe") if sys.platform == "win32" else None
    bash = str(bash) if bash and bash.is_file() else shutil.which("bash")
    shell_count = 0
    if bash:
        for path in (ROOT / "scripts").glob("*.sh"):
            result = subprocess.run([bash, "-n", str(path)], capture_output=True, text=True)
            shell_count += 1
            if result.returncode:
                errors.append(f"Shell syntax: {path.name}: {result.stderr}")
    else:
        print("SKIP Bash syntax: Bash is not installed")
    for error in errors:
        print("FAIL", error)
    print(f"Validated Codex {len(skills)} skills/{len(agents)} agents/{len(hooks)} hook events; "
          f"Claude {len(claude_skills)} skills/{len(claude.get('hooks', {}))} hook events; {shell_count} shell scripts")
    return 1 if errors else 0


if __name__ == "__main__":
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    raise SystemExit(validate())
