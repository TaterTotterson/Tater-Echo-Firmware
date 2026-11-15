from __future__ import annotations

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
LINUX_ROUTER = ROOT / "linux/common/tater-recovery-router"
RETIRED_COMMAND = "cmd sh /cache/tater-recovery/router.sh\n"


class RecoveryRouterTests(unittest.TestCase):
    def test_cleanup_script_is_valid_posix_shell(self) -> None:
        subprocess.run(["sh", "-n", str(LINUX_ROUTER)], check=True)

    def test_migration_cannot_write_boot_recovery_or_arm_slot_rescue(self) -> None:
        contents = LINUX_ROUTER.read_text()
        self.assertIn("PARTNAME=$want", contents)
        self.assertIn("exec /usr/local/bin/rebootto recovery", contents)
        self.assertNotIn("dd if=", contents)
        self.assertNotIn("partition boot", contents)
        self.assertNotIn("partition system", contents)
        self.assertNotIn("rescue.new", contents)
        self.assertNotIn("install_router()", contents)

    def run_cleanup(self, root: Path, argument: str = "disable"):
        # Execute the real shell cleanup/dispatch, replacing only cache mount
        # plumbing. Tests must never mount or inspect the host's real disks.
        contents = LINUX_ROUTER.read_text()
        prefix, dispatch = contents.split('\ncase "${1:-disable}" in', 1)
        script = prefix + """
open_cache() { CACHE_ROOT=$TATER_TEST_CACHE; }
close_cache() { :; }
""" + '\ncase "${1:-disable}" in' + dispatch
        return subprocess.run(
            ["sh", "-c", script, "tater-recovery-router", argument],
            env={**os.environ, "TATER_TEST_CACHE": str(root)},
            capture_output=True, text=True, timeout=5,
        )

    def test_removes_only_our_redirect_and_is_idempotent(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            command = root / "recovery/openrecoveryscript"
            command.parent.mkdir()
            command.write_text(RETIRED_COMMAND)
            retained_log = root / "tater-recovery/router.log"
            retained_log.parent.mkdir()
            retained_log.write_text("qualification evidence\n")
            result = self.run_cleanup(root)
            self.assertEqual(0, result.returncode, result.stdout + result.stderr)
            self.assertFalse(command.exists())
            self.assertEqual("qualification evidence\n", retained_log.read_text())
            self.assertEqual(0, self.run_cleanup(root).returncode)

    def test_legacy_install_alias_disables_rather_than_rearms(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            command = root / "recovery/openrecoveryscript"
            command.parent.mkdir()
            command.write_text(RETIRED_COMMAND)
            result = self.run_cleanup(root, "install")
            self.assertEqual(0, result.returncode, result.stdout + result.stderr)
            self.assertFalse(command.exists())
            self.assertEqual(0, self.run_cleanup(root, "install").returncode)
            self.assertFalse(command.exists())

    def test_unrelated_recovery_commands_are_not_changed(self) -> None:
        for foreign in ("print user recovery task\n", "# user script\n" * 2000):
            with self.subTest(length=len(foreign)), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                command = root / "recovery/openrecoveryscript"
                command.parent.mkdir()
                command.write_text(foreign)
                result = self.run_cleanup(root)
                self.assertEqual(0, result.returncode, result.stdout + result.stderr)
                self.assertEqual(foreign, command.read_text())

    def test_mixed_script_is_refused_without_erasing_user_commands(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            command = root / "recovery/openrecoveryscript"
            command.parent.mkdir()
            mixed = RETIRED_COMMAND + "print user recovery task\n"
            command.write_text(mixed)
            self.assertNotEqual(0, self.run_cleanup(root).returncode)
            self.assertEqual(mixed, command.read_text())

    def test_symlink_script_and_symlink_directory_are_refused(self) -> None:
        for directory_link in (False, True):
            with self.subTest(directory_link=directory_link), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                other = root / "user-owned"
                other.mkdir()
                original = other / "openrecoveryscript"
                original.write_text(RETIRED_COMMAND)
                if directory_link:
                    (root / "recovery").symlink_to(other, target_is_directory=True)
                else:
                    (root / "recovery").mkdir()
                    (root / "recovery/openrecoveryscript").symlink_to(original)
                self.assertNotEqual(0, self.run_cleanup(root).returncode)
                self.assertEqual(RETIRED_COMMAND, original.read_text())

    def test_missing_command_does_not_create_recovery_state(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertEqual(0, self.run_cleanup(root).returncode)
            self.assertEqual([], list(root.iterdir()))


if __name__ == "__main__":
    unittest.main()
