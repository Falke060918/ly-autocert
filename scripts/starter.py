#!/usr/bin/env python3
"""Portable Codex starter lifecycle, state, and measurement helpers (stdlib only)."""
import argparse
from collections import defaultdict
from datetime import datetime, timezone, date
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent
LIMITS = {"STATE.md": 150, "MAP.md": 120, "GOTCHAS.md": 100}


def read_text(path):
    try:
        return Path(path).read_text(encoding="utf-8-sig")
    except (OSError, UnicodeError):
        return ""


def git(*args, root=ROOT):
    result = subprocess.run(
        ["git", "-C", str(root), *args], capture_output=True,
        text=True, encoding="utf-8", errors="replace", timeout=5,
    )
    return result.stdout.strip() if result.returncode == 0 else ""


def identity(root=ROOT):
    return {
        k.strip(): v.strip() for line in read_text(root / ".codex/identity").splitlines()
        if line.strip() and not line.lstrip().startswith("#") and ":" in line
        for k, v in [line.split(":", 1)]
    }


def normalize(value):
    value = value.strip().replace("\\", "/")
    value = re.sub(r"^[a-z]+://", "", value, flags=re.I)
    value = re.sub(r"^git@", "", value, flags=re.I)
    if not re.match(r"^[A-Za-z]:/", value):
        value = value.replace(":", "/", 1)
    return re.sub(r"\.git$", "", value.rstrip("/"), flags=re.I).lower() or "none"


def main_root(root=ROOT):
    common = git("rev-parse", "--git-common-dir", root=root)
    if common:
        directory = Path(common)
        if not directory.is_absolute():
            directory = root / directory
        if directory.resolve().name == ".git":
            return directory.resolve().parent
    return root


def markers(root=ROOT):
    data = identity(root)
    result = []
    if data.get("identity") == "template":
        actual = normalize(git("remote", "get-url", "origin", root=root))
        if actual == normalize(data.get("origin", "none")):
            result.append("TEMPLATE-REPO")
            reflog = git("reflog", "--format=%gs", root=root).splitlines()
            if len(reflog) == 1 and reflog[0].startswith("clone:"):
                result.append("CLONE-SUSPECT")
        else:
            result.extend(["PROJECT-REPO", "SETUP-PENDING"])
    elif data.get("identity") == "project":
        if data.get("setup") != "done":
            result.extend(["PROJECT-REPO", "SETUP-PENDING"])
    else:
        result.append("IDENTITY-MISSING")
    if any(len(read_text(root / "docs" / n).splitlines()) > cap for n, cap in LIMITS.items()):
        result.append("KB-OVER-LIMIT")
    map_lines = read_text(root / "docs/MAP.md").splitlines()
    if map_lines:
        found = re.search(r"확인일:\s*(\d{4}-\d{2}-\d{2})", map_lines[0])
        if found:
            try:
                if (date.today() - date.fromisoformat(found[1])).days > 30:
                    result.append("MAP-STALE")
            except ValueError:
                pass
    if read_text(main_root(root) / ".codex/automode").splitlines()[:1] == ["on"]:
        result.append("AUTOMODE-ON")
    return " ".join(result)


def session_key(session):
    # Untrusted hook identifiers never become paths.
    return hashlib.sha256(session.encode()).hexdigest()[:24]


def atomic_write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent,
                                     delete=False, newline="\n") as handle:
        handle.write(text)
        temporary = Path(handle.name)
    try:
        temporary.replace(path)
    finally:
        temporary.unlink(missing_ok=True)


def snapshot(session, root=ROOT):
    # No diffs, secret files, or transcript text in an automatic snapshot.
    body = ["# Codex context snapshot", f"Session: {session}",
            f"UTC: {datetime.now(timezone.utc).isoformat()}",
            f"HEAD: {git('rev-parse', 'HEAD', root=root)}",
            f"Branch: {git('branch', '--show-current', root=root)}", "",
            "## Working tree", git("status", "--short", root=root), "",
            "## STATE", read_text(root / "docs/STATE.md")[:16000]]
    path = root / ".codex/snapshots" / (session_key(session) + ".md")
    atomic_write(path, "\n".join(body) + "\n")
    return path


def log_event(event, root=ROOT):
    session = str(event.get("session_id") or "unknown")
    directory = root / ".codex/perf"
    directory.mkdir(parents=True, exist_ok=True)
    record = {
        "time": time.time(), "event": event.get("hook_event_name", ""),
        "session": session, "tool": event.get("tool_name", ""),
        "call": event.get("tool_use_id", ""), "agent": event.get("agent_id", ""),
    }
    # Deliberately exclude tool inputs, outputs, environment, and credentials.
    data = (json.dumps(record, ensure_ascii=False) + "\n").encode("utf-8")
    path = directory / (session_key(session) + ".jsonl")
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    try:
        os.write(descriptor, data)
    finally:
        os.close(descriptor)
    return path


def additional(event, message):
    return {"hookSpecificOutput": {"hookEventName": event, "additionalContext": message}}


def handle_hook(event, root=ROOT):
    name = event.get("hook_event_name", "")
    session = str(event.get("session_id") or "unknown")
    path = log_event(event, root)
    if name == "SessionStart":
        message = markers(root) + "\nRead AGENTS.md, docs/STATE.md and docs/GOTCHAS.md."
        if event.get("source") == "compact":
            snap = read_text(root / ".codex/snapshots" / (session_key(session) + ".md"))
            if snap:
                message += "\n" + snap
        return additional(name, message)
    if name == "PreCompact":
        snapshot(session, root)
    elif name == "PostToolUse":
        count = sum(1 for line in read_text(path).splitlines() if '"event": "PostToolUse"' in line)
        if count and count % 25 == 0:
            return additional(name, "승인된 작업 범위를 유지하고, 새 학습 후보가 있으면 docs/learn/에 기록한다.")
    elif name == "SessionEnd":
        # A cheap review pointer, not a new model call or an invented learning.
        candidate = root / "docs/learn" / ("session-" + session_key(session) + ".md")
        candidate.parent.mkdir(parents=True, exist_ok=True)
        try:
            with candidate.open("x", encoding="utf-8") as handle:
                handle.write("# 세션 검토 후보\n\n")
                handle.write(f"세션 ID: {session}\n\n")
                handle.write("내용 추출·규칙 승격은 아직 수행하지 않음. $recall로 해당 세션을 확인한 뒤 ")
                handle.write("사용자 교정이나 재현 가능한 교훈이 있을 때만 $learn으로 처리한다.\n")
        except FileExistsError:
            pass
    return {}


def perf_report(session=None, limit=1, root=ROOT):
    files = sorted((root / ".codex/perf").glob("*.jsonl"), key=lambda p: p.stat().st_mtime, reverse=True)
    reports = []
    for path in files:
        records = []
        for line in read_text(path).splitlines():
            try:
                record = json.loads(line)
                if isinstance(record, dict) and isinstance(record.get("time"), (float, int)):
                    records.append(record)
            except ValueError:
                pass
        if not records:
            continue
        sid = records[0].get("session", "")
        if session and not sid.startswith(session):
            continue
        records.sort(key=lambda e: e["time"])
        starts, durations = {}, defaultdict(list)
        for rec in records:
            call = rec.get("call")
            if rec.get("event") == "PreToolUse" and call:
                starts[call] = rec
            elif rec.get("event") == "PostToolUse" and call in starts:
                before = starts.pop(call)
                durations[before.get("tool") or "unknown"].append(max(0, rec["time"] - before["time"]))
        lines = [f"## 세션 {sid}", f"관측 구간: {records[-1]['time'] - records[0]['time']:.2f}초",
                 f"이벤트: {len(records)}개", "",
                 "| 도구 | 완료 호출 | 합계(초) | 최대(초) |", "| --- | ---: | ---: | ---: |"]
        for tool, values in sorted(durations.items(), key=lambda item: sum(item[1]), reverse=True):
            lines.append(f"| {tool} | {len(values)} | {sum(values):.2f} | {max(values):.2f} |")
        lines.extend(["", f"종료 이벤트가 없는 호출: {len(starts)}개",
                      "시간은 훅 관측값이며 실행·승인·스케줄링 대기를 포함할 수 있다.",
                      "병렬 호출 합계는 관측 구간보다 클 수 있다. 토큰·요금·모델 사고 시간은 추정하지 않는다."])
        reports.append("\n".join(lines))
        if len(reports) >= limit:
            break
    return "\n\n".join(reports) if reports else "계측 기록 없음. 프로젝트 훅 신뢰 후 실행한 세션부터 수집된다."


def main():
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8")
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("status")
    for name in ("snapshot", "resume"):
        cmd = commands.add_parser(name)
        cmd.add_argument("--session", default="manual")
    cmd = commands.add_parser("automode")
    cmd.add_argument("value", choices=["on", "off"], nargs="?")
    commands.add_parser("hook")
    cmd = commands.add_parser("perf")
    cmd.add_argument("limit", nargs="?", type=int, default=1)
    cmd.add_argument("--session")
    args = parser.parse_args()
    if args.command == "status":
        print(markers() or "PROJECT-REPO READY")
    elif args.command == "snapshot":
        print(snapshot(args.session))
    elif args.command == "resume":
        print(read_text(ROOT / ".codex/snapshots" / (session_key(args.session) + ".md")) or "스냅샷 없음")
    elif args.command == "automode":
        path = main_root() / ".codex/automode"
        if args.value:
            atomic_write(path, args.value + "\n")
        print("on" if read_text(path).splitlines()[:1] == ["on"] else "off")
    elif args.command == "perf":
        print(perf_report(args.session, max(1, args.limit)))
    elif args.command == "hook":
        try:
            event = json.loads(sys.stdin.read())
            if not isinstance(event, dict):
                raise ValueError("hook input must be an object")
            print(json.dumps(handle_hook(event), ensure_ascii=False))
        except (OSError, ValueError, subprocess.SubprocessError) as exc:
            print("starter hook skipped: " + type(exc).__name__, file=sys.stderr)
            print("{}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
