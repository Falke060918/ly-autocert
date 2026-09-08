"""Offline integration checks for a checkout used by both CLI runtimes."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import tomllib
import unittest
from unittest.mock import patch

import test_starter
from test_starter import BASH, ROOT, commit, git, init, module, starter, write

renderer = module("sync_orchestrator", ROOT / "scripts/sync-orchestrator.py")
validator = module("validate_dual", ROOT / "scripts/validate.py")
import_guard = module("import_guard", ROOT / "scripts/import-guard.py")
claude_index = module("claude_index", ROOT / "scripts/index-sessions.py")
codex_index = module("codex_index", ROOT / "scripts/codex-index-sessions.py")


class OrchestratorConfigTests(unittest.TestCase):
    def test_identity_work_order_prefix_mismatch_is_rejected(self):
        with tempfile.TemporaryDirectory(prefix="identity-mirror-") as folder:
            root = Path(folder)
            for runtime in (".claude", ".codex"):
                write(root, runtime + "/identity", "identity: project\nwork-order-prefix: AI\n")
            self.assertEqual(validator.identity_errors(root), [])
            write(root, ".codex/identity", "identity: project\nwork-order-prefix: DIFFERENT\n")
            self.assertEqual(validator.identity_errors(root), ["Identity adapters disagree: work-order-prefix"])

    def test_full_role_roundtrip_preserves_unrelated_settings(self):
        config = '# custom comment\napproval_policy = "never"\n[agents.worker]\nmodel = "user-model"\n'
        role = '한글 전체 역할\n"quoted" and \\ paths\n' * 200
        generated = renderer.render(config, role)
        self.assertEqual(tomllib.loads(generated)["developer_instructions"], role)
        self.assertIn(config, generated)
        updated = renderer.render(generated, role + "new rule\n")
        data = tomllib.loads(updated)
        self.assertEqual(data["agents"]["worker"]["model"], "user-model")
        self.assertEqual(data["approval_policy"], "never")
        self.assertEqual(data["developer_instructions"], role + "new rule\n")
        self.assertEqual(renderer.render(updated, role + "new rule\n"), updated)

    def test_unmanaged_instruction_is_not_overwritten(self):
        with self.assertRaises(ValueError):
            renderer.render('developer_instructions = "user custom"\n', "template")
        with self.assertRaises(ValueError):
            renderer.render(renderer.BEGIN + "\n", "template")

    def test_check_detects_drift_without_mutation(self):
        with tempfile.TemporaryDirectory(prefix="orchestrator-check-") as folder:
            root = Path(folder)
            path = write(root, ".codex/config.toml", renderer.render('approval_policy = "never"\n', "old role"))
            write(root, ".codex/roles/orchestrator.md", "new role")
            before = path.read_bytes()
            result = subprocess.run([os.sys.executable, str(ROOT / "scripts/sync-orchestrator.py"),
                                     "--root", str(root), "--check"], capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(path.read_bytes(), before)


class ImportProtectionTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="dual-import-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name) / "target"
        init(self.root)
        self.live = {"AGENTS.md": "Codex live", "CLAUDE.md": "Claude live",
                     ".claude/settings.json": "{}", ".codex/config.toml": 'approval_policy="never"',
                     ".agents/skills/keep/SKILL.md": "live skill"}
        for name, body in self.live.items():
            write(self.root, name, body)
        write(self.root, ".gitignore", ".claude/settings.local.json\n.codex/tmp/\n")
        commit(self.root)
        self.snapshot = {"root": str(self.root.resolve()), "files": import_guard.inventory(self.root)}

    def test_removed_entrypoint_and_changed_each_live_runtime_are_detected(self):
        (self.root / "AGENTS.md").unlink()
        self.assertEqual(import_guard.compare(self.snapshot, self.root), ["AGENTS.md"])
        write(self.root, "AGENTS.md", self.live["AGENTS.md"])
        for name, original in self.live.items():
            with self.subTest(path=name):
                write(self.root, name, "foreign runtime")
                self.assertEqual(import_guard.compare(self.snapshot, self.root), [name])
                write(self.root, name, original)

    def test_real_merge_ours_does_not_hide_incoming_runtime_paths(self):
        external = Path(self.tmp.name) / "external"
        init(external)
        write(external, "AGENTS.md", "external instructions")
        write(external, ".codex/agents/foreign.toml", 'name="foreign"')
        write(external, "app.py", "print('product')\n")
        commit(external)
        git(self.root, "remote", "add", "import-src", str(external))
        git(self.root, "fetch", "--no-tags", "import-src")
        git(self.root, "merge", "--no-commit", "--no-ff", "--allow-unrelated-histories", "-X", "ours", "import-src/main")
        self.assertEqual((self.root / "AGENTS.md").read_text(), self.live["AGENTS.md"])
        self.assertTrue((self.root / "app.py").is_file())
        self.assertEqual(import_guard.compare(self.snapshot, self.root), [".codex/agents/foreign.toml"])
        self.assertEqual(git(external, "status", "--short"), "")

    def test_ignored_static_settings_are_protected_but_session_artifacts_are_not(self):
        write(self.root, ".claude/settings.local.json", '{"local":true}')
        baseline = {"root": str(self.root.resolve()), "files": import_guard.inventory(self.root)}
        write(self.root, ".claude/perf/session.jsonl", "session data")
        write(self.root, ".codex/tmp/import.json", "snapshot data")
        write(self.root, "app.py", "product change")
        self.assertEqual(import_guard.compare(baseline, self.root), [])
        write(self.root, ".claude/settings.local.json", '{"foreign":true}')
        self.assertEqual(import_guard.compare(baseline, self.root), [".claude/settings.local.json"])


@unittest.skipUnless(BASH and Path(BASH).is_file(), "Git Bash/Bash required")
class DualLifecycleTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="dual-lifecycle-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        init(self.root)
        for runtime in (".claude", ".codex"):
            write(self.root, runtime + "/identity", "identity: template\norigin: none\n")
        write(self.root, "docs/STATE.md", "Shared task state\n")
        for name in ("session-markers.sh", "compact-snapshot.sh", "compact-resume.sh", "perf-log.sh"):
            write(self.root, "scripts/" + name, (ROOT / "scripts" / name).read_text(encoding="utf-8"))
        commit(self.root)

    def claude(self, name, payload=""):
        env = {**os.environ, "CLAUDE_PROJECT_DIR": self.root.as_posix()}
        result = subprocess.run([BASH, "scripts/" + name], cwd=self.root, env=env,
                                input=payload, capture_output=True, text=True, encoding="utf-8", timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout

    def test_switching_runtimes_shares_state_but_not_automode(self):
        self.assertIn("TEMPLATE-REPO", self.claude("session-markers.sh"))
        self.assertIn("TEMPLATE-REPO", starter.markers(self.root))
        write(self.root, ".claude/automode", "on\n")
        self.assertIn("AUTOMODE-ON", self.claude("session-markers.sh"))
        self.assertNotIn("AUTOMODE-ON", starter.markers(self.root))
        write(self.root, "docs/STATE.md", "Shared task state\n" * 151)
        self.assertIn("KB-OVER-LIMIT", self.claude("session-markers.sh"))
        self.assertIn("KB-OVER-LIMIT", starter.markers(self.root))

    def test_snapshots_and_perf_logs_do_not_overwrite_other_runtime(self):
        self.claude("compact-snapshot.sh")
        claude_snapshot = self.root / ".claude/compact-snapshot.md"
        before = claude_snapshot.read_bytes()
        codex_snapshot = starter.snapshot("shared-session-id", self.root)
        self.assertEqual(claude_snapshot.read_bytes(), before)
        self.assertIn("Shared task state", codex_snapshot.read_text(encoding="utf-8"))
        self.assertIn("compact-snapshot.md", self.claude("compact-resume.sh"))
        event = {"hook_event_name": "PostToolUse", "session_id": "shared-session-id", "tool_name": "fixture"}
        self.claude("perf-log.sh", json.dumps(event, separators=(",", ":")))
        claude_log = self.root / ".claude/perf/shared-session-id.jsonl"
        before = claude_log.read_bytes()
        codex_log = starter.log_event(event, self.root)
        self.assertEqual(claude_log.read_bytes(), before)
        self.assertEqual(json.loads(codex_log.read_text())["session"], "shared-session-id")
        self.assertNotEqual(codex_log.parent, claude_log.parent)

    def test_transcript_parsers_keep_distinct_databases(self):
        with patch.object(claude_index, "DB_PATH", self.root / ".claude/index/sessions.db"), \
             patch.object(codex_index, "DB_PATH", self.root / ".codex/index/sessions.db"):
            a = write(self.root, "fixtures/claude.jsonl", json.dumps({"type": "user", "message": {"content": "Claude decision"}}) + "\n")
            con = claude_index.open_db()
            self.assertEqual(claude_index.index_file(con, a), 1)
            con.commit()
            con.close()
            con = codex_index.open_db()
            self.assertEqual(con.execute("SELECT count(*) FROM messages").fetchone()[0], 0)
            con.close()
            con = claude_index.open_db()
            self.assertEqual(con.execute("SELECT text FROM messages").fetchall(), [("Claude decision",)])
            con.close()


class ClaudeSyncTests(test_starter.SyncTests):
    """Run the same behavioral update suite through Claude's preserved entrypoint."""
    def sync(self, root=None):
        return subprocess.run([BASH, "scripts/sync-manifest.sh"], cwd=root or self.project,
                              capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=30)

    def test_both_manifests_protect_project_data_and_delete_only_template_files(self):
        write(self.project, ".claude/settings.local.json", '{"keep":true}\n')
        write(self.project, "app/keep.txt", "product data\n")
        write(self.project, "scripts/obsolete.py", "old template helper\n")
        commit(self.project)
        write(self.template, "scripts/codex-sync-manifest.deletions", "\n".join([
            "docs", "docs/STATE.md", ".claude", ".claude/identity", ".claude/settings.local.json",
            ".codex/config.toml", ".codex/hooks.json", "app/keep.txt", "scripts", "scripts/obsolete.py", "",
        ]))
        commit(self.template)
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.project / "scripts/obsolete.py").exists())
        self.assertEqual((self.project / "app/keep.txt").read_text(), "product data\n")
        self.assertEqual((self.project / "docs/STATE.md").read_text(), "user project state\n")
        self.assertIn('"keep":true', (self.project / ".claude/settings.local.json").read_text())
        self.assertIn("project", (self.project / ".claude/identity").read_text())
        self.assertIn("on-request", (self.project / ".codex/config.toml").read_text())

    def test_disagreeing_identity_adapters_block_before_any_fetch(self):
        write(self.project, ".codex/identity", "identity: template\norigin: none\n")
        commit(self.project)
        before = git(self.project, "status", "--short")
        result = self.sync()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(before, git(self.project, "status", "--short"))

    def test_updated_role_reports_config_drift_without_overwriting_custom_config(self):
        write(self.template, "scripts/sync-orchestrator.py", (ROOT / "scripts/sync-orchestrator.py").read_text(encoding="utf-8"))
        write(self.template, ".codex/roles/orchestrator.md", "new full main role\n")
        commit(self.template)
        config_path = self.project / ".codex/config.toml"
        before = config_path.read_bytes()
        result = self.sync()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("ACTION REQUIRED", result.stderr)
        self.assertEqual(config_path.read_bytes(), before)
        self.assertEqual((self.project / ".codex/roles/orchestrator.md").read_text(), "new full main role\n")


if __name__ == "__main__":
    unittest.main()
