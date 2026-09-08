"""Project validation must inspect operating files, not accumulated session artifacts."""
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
ARTIFACT_DIRS = ("tmp", "recordings", "review", "perf", "snapshots", "index", "worktrees")


class ValidationOwnershipTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="duet-validation-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for filename in ("AGENTS.md", "CLAUDE.md", "README.md", "SETUP.md"):
            shutil.copy2(ROOT / filename, self.root / filename)
        for directory in (".agents", ".codex", ".claude", "scripts", "tests"):
            shutil.copytree(
                ROOT / directory, self.root / directory,
                ignore=shutil.ignore_patterns(*ARTIFACT_DIRS, "__pycache__"),
            )

    def validate(self):
        return subprocess.run(
            [sys.executable, str(self.root / "scripts/validate.py")],
            cwd=self.root, capture_output=True, text=True, encoding="utf-8",
        )

    def test_session_artifacts_are_not_operating_source(self):
        for runtime in (".codex", ".claude"):
            for directory in ARTIFACT_DIRS:
                target = self.root / runtime / directory
                target.mkdir(parents=True)
                (target / "bom.py").write_bytes(b"\xef\xbb\xbfimport os\n")
                (target / "unfinished.py").write_text("def unfinished(\n", encoding="utf-8")
                (target / "historic.md").write_text("`.codex/no-longer-present.md`\n", encoding="utf-8")
        result = self.validate()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Validated Codex", result.stdout)

    def test_malformed_operating_config_still_fails(self):
        (self.root / ".codex/config.toml").write_text('broken = "unterminated\n', encoding="utf-8")
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("TOMLDecodeError", result.stderr)

    def test_malformed_operating_agent_still_fails(self):
        (self.root / ".codex/agents/implementer.toml").write_text('name = "unterminated\n', encoding="utf-8")
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("TOMLDecodeError", result.stderr)

    def test_custom_operating_source_is_not_excluded_by_directory_name(self):
        target = self.root / ".codex/rules/tmp"
        target.mkdir()
        (target / "custom.py").write_text("def unfinished(\n", encoding="utf-8")
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("SyntaxError", result.stderr)
        self.assertIn("custom.py", result.stderr)


if __name__ == "__main__":
    unittest.main()
