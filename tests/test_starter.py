import argparse
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent.parent


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


starter = module("starter", ROOT / "scripts/starter.py")
index = module("index_sessions", ROOT / "scripts/codex-index-sessions.py")
BASH = "C:/Program Files/Git/bin/bash.exe" if os.name == "nt" else shutil.which("bash")


def write(root, name, text):
    path = root / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return path


def git(root, *args):
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True,
                            text=True, encoding="utf-8", errors="replace")
    if result.returncode:
        raise AssertionError(result.stderr)
    return result.stdout.strip()


def init(root):
    root.mkdir(parents=True, exist_ok=True)
    git(root, "init", "-b", "main")
    git(root, "config", "user.name", "Fixture")
    git(root, "config", "user.email", "fixture@example.invalid")


def commit(root):
    git(root, "add", ".")
    git(root, "commit", "-m", "fixture")


class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="codex-starter-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        init(self.root)
        write(self.root, ".codex/identity", "identity: template\norigin: none\n")
        write(self.root, "docs/STATE.md", "Current work\n")

    def test_identity_markers_and_no_mutation(self):
        before = git(self.root, "status", "--short")
        self.assertEqual(starter.markers(self.root), "TEMPLATE-REPO")
        git(self.root, "remote", "add", "origin", "https://example.invalid/new/project")
        self.assertIn("SETUP-PENDING", starter.markers(self.root))
        self.assertEqual(before, git(self.root, "status", "--short"))
        write(self.root, ".codex/identity", "identity: project\nsetup: done\n")
        self.assertEqual(starter.markers(self.root), "")

    def test_limits_stale_and_mode(self):
        write(self.root, "docs/STATE.md", "x\n" * 151)
        write(self.root, "docs/MAP.md", "확인일: 2000-01-01\n")
        write(self.root, ".codex/automode", "on\n")
        value = starter.markers(self.root)
        for token in ("KB-OVER-LIMIT", "MAP-STALE", "AUTOMODE-ON"):
            self.assertIn(token, value)

    def test_snapshot_isolated_and_no_secrets(self):
        commit(self.root)
        write(self.root, ".env", "TOKEN=do-not-copy\n")
        a = starter.snapshot("../../session-a", self.root)
        b = starter.snapshot("session-b", self.root)
        self.assertNotEqual(a, b)
        a.resolve().relative_to(self.root.resolve())
        self.assertNotIn("do-not-copy", a.read_text(encoding="utf-8"))
        event = {"hook_event_name": "SessionStart", "session_id": "../../session-a", "source": "compact"}
        value = starter.handle_hook(event, self.root)
        self.assertIn("Current work", value["hookSpecificOutput"]["additionalContext"])

    def test_hook_logs_only_metadata_and_end_is_idempotent(self):
        event = {"hook_event_name": "PostToolUse", "session_id": "../x",
                 "tool_input": {"secret": "do-not-log"}, "tool_response": "do-not-log"}
        path = starter.log_event(event, self.root)
        self.assertNotIn("do-not-log", path.read_text(encoding="utf-8"))
        event["hook_event_name"] = "SessionEnd"
        starter.handle_hook(event, self.root)
        starter.handle_hook(event, self.root)
        self.assertEqual(len(list((self.root / "docs/learn").glob("*.md"))), 1)

    def test_perf_pairs_by_call_id(self):
        event = {"session_id": "one", "tool_name": "shell", "tool_use_id": "call"}
        with patch.object(starter.time, "time", return_value=1):
            starter.log_event({**event, "hook_event_name": "PreToolUse"}, self.root)
        with patch.object(starter.time, "time", return_value=4):
            starter.log_event({**event, "hook_event_name": "PostToolUse"}, self.root)
        report = starter.perf_report("one", root=self.root)
        self.assertIn("| shell | 1 | 3.00 | 3.00 |", report)

    def test_hook_command_from_nested_directory(self):
        commit(self.root)
        write(self.root, "scripts/starter.py", (ROOT / "scripts/starter.py").read_text(encoding="utf-8"))
        nested = self.root / "nested folder"
        nested.mkdir()
        hook = json.loads((ROOT / ".codex/hooks.json").read_text(encoding="utf-8"))["hooks"]["SessionStart"][0]["hooks"][0]
        command = hook["commandWindows"] if os.name == "nt" else hook["command"]
        result = subprocess.run(command if os.name == "nt" else [BASH, "-c", command],
                                cwd=nested, input=json.dumps({"hook_event_name": "SessionStart",
                                                            "session_id": "fixture"}),
                                capture_output=True, text=True, encoding="utf-8", timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("TEMPLATE-REPO", json.loads(result.stdout)["hookSpecificOutput"]["additionalContext"])


class IndexTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="codex-index-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.project = self.root / "project"
        self.project.mkdir()
        self.home = self.root / "codex-home"
        self.patches = patch.multiple(index, CODEX_DIR=self.home, PROJECTS_DIR=self.home / "sessions",
                                      REPO_ROOT=self.project, DB_PATH=self.project / ".codex/index/sessions.db")
        self.patches.start()
        self.addCleanup(self.patches.stop)

    def rollout(self, name, cwd, message):
        records = [
            {"type": "session_meta", "payload": {"id": name, "cwd": str(cwd)}},
            {"type": "response_item", "timestamp": "2026-09-07T00:00:00Z",
             "payload": {"type": "message", "role": "user",
                         "content": [{"type": "input_text", "text": message}]}},
            {"type": "event_msg", "payload": {"type": "user_message", "message": message}},
            {"type": "response_item", "payload": {"type": "message", "role": "developer",
                                                 "content": [{"type": "input_text", "text": "private system"}]}},
        ]
        return write(self.home, f"sessions/2026/09/07/{name}.jsonl",
                     "\n".join(json.dumps(x) for x in records) + "\nmalformed\n")

    def run_index(self, all_projects=False):
        with contextlib.redirect_stdout(io.StringIO()):
            index.cmd_index(argparse.Namespace(all=all_projects, project=None))

    def test_project_scope_dedup_incremental_and_deleted_files(self):
        path = self.rollout("local", self.project / "nested", "한국어 결정")
        self.rollout("other", self.root / "project-other", "unrelated")
        self.run_index()
        self.run_index()
        con = index.open_db()
        self.assertEqual(con.execute("SELECT text FROM messages").fetchall(), [("한국어 결정",)])
        con.close()
        path.unlink()
        self.run_index()
        con = index.open_db()
        self.assertEqual(con.execute("SELECT count(*) FROM messages").fetchone()[0], 0)
        con.close()

    def test_explicit_all_then_local_removes_foreign_scope(self):
        self.rollout("local", self.project, "local")
        self.rollout("other", self.root / "other", "foreign")
        self.run_index(True)
        con = index.open_db()
        self.assertEqual(con.execute("SELECT count(*) FROM messages").fetchone()[0], 2)
        con.close()
        self.run_index()
        con = index.open_db()
        self.assertEqual(con.execute("SELECT text FROM messages").fetchall(), [("local",)])
        con.close()

    def test_search_and_show_observe_rollout(self):
        path = self.rollout("local", self.project, "폰트를 선택했다")
        self.run_index()
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = index.cmd_search(argparse.Namespace(query="폰트", raw=False, role=None, session=None, limit=10))
            shown = index.cmd_show(argparse.Namespace(file=str(path), line=2, context=2, max_chars=1000))
        self.assertEqual((code, shown), (0, 0))
        self.assertIn("폰트를 선택했다", out.getvalue())
        self.assertEqual(index.build_query('say "hello"', False), '"say"* """hello"""*')


@unittest.skipUnless(BASH and Path(BASH).is_file(), "Git Bash/Bash required")
class SyncTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="codex-sync-test-")
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.template = self.base / "template space"
        init(self.template)
        for name in ("scripts/codex-sync-manifest.sh", "scripts/codex-sync-manifest.deletions", "scripts/starter.py",
                     "scripts/sync-manifest.sh", "scripts/sync-manifest.deletions"):
            write(self.template, name, (ROOT / name).read_text(encoding="utf-8"))
        write(self.template, "AGENTS.md", "template rules\n")
        write(self.template, "CLAUDE.md", "template Claude rules\n")
        write(self.template, ".claude/identity", "identity: template\norigin: none\n")
        write(self.template, ".claude/settings.json", '{"agent":"orchestrator"}\n')
        write(self.template, ".claude/agents/orchestrator.md", "Claude first version\n")
        write(self.template, ".codex/identity", "identity: template\norigin: none\n")
        write(self.template, ".codex/config.toml", 'approval_policy = "never"\n')
        write(self.template, ".codex/hooks.json", '{"hooks":{}}\n')
        write(self.template, ".codex/roles/explorer.md", "first version\n")
        write(self.template, "docs/STATE.md", "template state\n")
        commit(self.template)
        self.project = self.base / "project space"
        subprocess.run(["git", "clone", "--no-hardlinks", str(self.template), str(self.project)],
                       check=True, capture_output=True)
        git(self.project, "config", "user.name", "Fixture")
        git(self.project, "config", "user.email", "fixture@example.invalid")
        git(self.project, "remote", "rename", "origin", "template")
        write(self.project, ".codex/identity", "identity: project\nsetup: done\ntemplate: " +
              self.template.as_posix() + "\n")
        write(self.project, ".claude/identity", "identity: project\nsetup: done\ntemplate: " +
              self.template.as_posix() + "\n")
        write(self.project, ".claude/settings.json", '{"agent":"orchestrator","project":true}\n')
        write(self.project, "docs/STATE.md", "user project state\n")
        write(self.project, ".codex/config.toml", 'approval_policy = "on-request"\n')
        write(self.project, ".codex/hooks.json", '{"hooks":{}, "description":"project"}\n')
        write(self.project, ".agents/skills/custom/SKILL.md", "user skill\n")
        commit(self.project)

    def sync(self, root=None):
        return subprocess.run([BASH, "scripts/codex-sync-manifest.sh"], cwd=root or self.project,
                              capture_output=True, text=True, encoding="utf-8", errors="replace",
                              timeout=30)

    def test_sync_preserves_project_files_and_recovers_space_path(self):
        git(self.project, "remote", "remove", "template")
        write(self.template, ".codex/roles/explorer.md", "second version\n")
        write(self.template, ".claude/agents/orchestrator.md", "Claude second version\n")
        write(self.template, "tests/new.py", "pass\n")
        commit(self.template)
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((self.project / ".codex/roles/explorer.md").read_text(), "second version\n")
        self.assertEqual((self.project / ".claude/agents/orchestrator.md").read_text(), "Claude second version\n")
        self.assertIn('"project":true', (self.project / ".claude/settings.json").read_text())
        self.assertEqual((self.project / "docs/STATE.md").read_text(), "user project state\n")
        self.assertIn("on-request", (self.project / ".codex/config.toml").read_text())
        self.assertIn("project", (self.project / ".codex/hooks.json").read_text())
        self.assertTrue((self.project / ".agents/skills/custom/SKILL.md").is_file())
        self.assertTrue((self.project / "tests/new.py").is_file())

    def test_dirty_and_template_guards(self):
        write(self.project, "untracked.txt", "do not overwrite")
        self.assertNotEqual(self.sync().returncode, 0)
        self.assertNotEqual(self.sync(self.template).returncode, 0)
        self.assertEqual((self.project / "untracked.txt").read_text(), "do not overwrite")

    def test_non_codex_template_cannot_replace_runtime(self):
        git(self.template, "rm", "AGENTS.md")
        commit(self.template)
        self.assertNotEqual(self.sync().returncode, 0)
        self.assertTrue((self.project / "AGENTS.md").is_file())


if __name__ == "__main__":
    unittest.main()
