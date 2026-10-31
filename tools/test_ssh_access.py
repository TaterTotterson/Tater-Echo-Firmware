from __future__ import annotations

import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
AUTHORIZE = ROOT / "linux/common/tater-ssh-authorize"
CONTROL = ROOT / "linux/common/tater-ssh-control"
SUPERVISOR = ROOT / "linux/common/tater-sshd"
PUBLIC_KEY = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKeyMaterialOnly laptop"
STORED_KEY = "no-agent-forwarding,no-X11-forwarding,no-port-forwarding " + PUBLIC_KEY


class SSHAuthorizeTests(unittest.TestCase):
    def environment(self, root: Path) -> dict[str, str]:
        environment = os.environ.copy()
        environment.update({
            "TATER_SSH_DIR": str(root / "ssh"),
            "TATER_SSH_STATE": str(root / "run/tater-sshd.state"),
            "TATER_SSH_PID_FILE": str(root / "run/tater-sshd.pid"),
        })
        return environment

    def run_authorize(
        self, root: Path, *arguments: str, input_text=None
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["sh", str(AUTHORIZE), *arguments],
            env=self.environment(root),
            input=input_text,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_add_is_unique_and_disable_enable_is_recoverable(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first = self.run_authorize(root, "add", PUBLIC_KEY)
            duplicate = self.run_authorize(root, "add", PUBLIC_KEY)
            status_result = self.run_authorize(root, "status")
            keys = root / "ssh/authorized_keys"

            self.assertEqual(0, first.returncode, first.stderr)
            self.assertEqual(0, duplicate.returncode, duplicate.stderr)
            self.assertIn("already authorized", duplicate.stdout)
            self.assertEqual(STORED_KEY + "\n", keys.read_text())
            self.assertEqual(0o600, stat.S_IMODE(keys.stat().st_mode))
            self.assertEqual(0o700, stat.S_IMODE(keys.parent.stat().st_mode))
            self.assertIn("enabled keys=1", status_result.stdout)

            disabled = self.run_authorize(root, "disable")
            self.assertEqual(0, disabled.returncode, disabled.stderr)
            self.assertFalse(keys.exists())
            self.assertEqual(STORED_KEY + "\n", (root / "ssh/authorized_keys.disabled").read_text())
            self.assertNotEqual(0, self.run_authorize(root, "add", PUBLIC_KEY).returncode)

            enabled = self.run_authorize(root, "enable")
            self.assertEqual(0, enabled.returncode, enabled.stderr)
            self.assertEqual(STORED_KEY + "\n", keys.read_text())

    def test_add_accepts_stdin_and_rejects_non_keys(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            added = self.run_authorize(root, "add", input_text=PUBLIC_KEY + "\n")
            self.assertEqual(0, added.returncode, added.stderr)
            invalid = self.run_authorize(root, "add", "password hunter2")
            self.assertNotEqual(0, invalid.returncode)
            self.assertIn("unsupported public-key type", invalid.stderr)


class SSHSupervisorTests(unittest.TestCase):
    def write_executable(self, path: Path, contents: str) -> None:
        path.write_text(contents)
        path.chmod(0o755)

    def environment(self, root: Path) -> dict[str, str]:
        environment = os.environ.copy()
        environment.update({
            "TATER_SSH_DIR": str(root / "ssh"),
            "TATER_SSH_STATE": str(root / "run/tater-sshd.state"),
            "TATER_SSH_LOG": str(root / "data/sshd.log"),
            "TATER_SSH_PID_FILE": str(root / "run/tater-sshd.pid"),
            "TATER_DROPBEAR": str(root / "bin/dropbear"),
            "TATER_DROPBEARKEY": str(root / "bin/dropbearkey"),
            "TATER_BUSYBOX": str(root / "bin/busybox"),
            "TATER_SSH_RUN_ONCE": "1",
            "TATER_TEST_ARGS": str(root / "dropbear.args"),
            "TATER_SSH_SHADOW_SOURCE": str(root / "run/tater-sshd.shadow"),
            "TATER_SSH_SHADOW_TARGET": str(root / "etc/shadow"),
            "TATER_SSH_SHADOW_MARKER": str(root / "run/tater-sshd.shadow-mounted"),
        })
        return environment

    def make_stubs(self, root: Path) -> None:
        (root / "bin").mkdir(parents=True)
        (root / "etc").mkdir()
        (root / "etc/shadow").write_text("root:*:0:0:99999:7:::\n")
        self.write_executable(
            root / "bin/dropbearkey",
            """#!/bin/sh
mode=$1
shift
key=
while [ "$#" -gt 0 ]; do
    if [ "$1" = -f ]; then key=$2; shift 2; else shift; fi
done
case "$mode" in
-t) printf 'test-ed25519-host-key\n' > "$key" ;;
-y) grep -q '^test-ed25519-host-key$' "$key" && printf 'Public key portion is:\nssh-ed25519 TEST\n' ;;
*) exit 2 ;;
esac
""",
        )
        self.write_executable(
            root / "bin/dropbear",
            """#!/bin/sh
printf '%s\n' "$@" > "$TATER_TEST_ARGS"
""",
        )
        self.write_executable(
            root / "bin/busybox",
            """#!/bin/sh
case "$1" in
awk) shift; exec /usr/bin/awk "$@" ;;
mount) shift; cp "$3" "$4" ;;
umount) exit 0 ;;
*) exit 2 ;;
esac
""",
        )

    def test_no_authorized_key_keeps_listener_disabled(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_stubs(root)
            result = subprocess.run(
                ["sh", str(SUPERVISOR)],
                env=self.environment(root),
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertIn("disabled waiting-for-tater", (root / "run/tater-sshd.state").read_text())
            self.assertFalse((root / "dropbear.args").exists())
            self.assertFalse((root / "ssh/dropbear_ed25519_host_key").exists())

    def test_key_alone_does_not_enable_dropbear(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_stubs(root)
            ssh_dir = root / "ssh"
            ssh_dir.mkdir()
            (ssh_dir / "authorized_keys").write_text(STORED_KEY + "\n")
            result = subprocess.run(
                ["sh", str(SUPERVISOR)],
                env=self.environment(root),
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertIn("disabled waiting-for-tater", (root / "run/tater-sshd.state").read_text())
            self.assertFalse((root / "dropbear.args").exists())
            self.assertFalse((root / "ssh/dropbear_ed25519_host_key").exists())

    def test_invalid_existing_host_key_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_stubs(root)
            ssh_dir = root / "ssh"
            ssh_dir.mkdir()
            (ssh_dir / "mode").write_text("password\n")
            (ssh_dir / "password_hash").write_text("$6$testsalt$testhash\n")
            (ssh_dir / "dropbear_ed25519_host_key").write_text("not-a-key\n")
            environment = self.environment(root)
            result = subprocess.run(
                ["sh", str(SUPERVISOR)],
                env=environment,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(0, result.returncode)
            self.assertIn("error existing Ed25519 host key is invalid", (root / "run/tater-sshd.state").read_text())
            self.assertFalse((root / "dropbear.args").exists())

    def test_managed_password_enables_login_without_weakening_forwarding_guards(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_stubs(root)
            ssh_dir = root / "ssh"
            ssh_dir.mkdir()
            (ssh_dir / "mode").write_text("password\n")
            (ssh_dir / "password_hash").write_text("$6$testsalt$testhash\n")
            environment = self.environment(root)

            result = subprocess.run(
                ["sh", str(SUPERVISOR)], env=environment, capture_output=True, text=True, check=False
            )

            self.assertEqual(0, result.returncode, result.stderr)
            arguments = (root / "dropbear.args").read_text().splitlines()
            self.assertNotIn("-s", arguments)
            self.assertNotIn("-g", arguments)
            for flag in ("-F", "-E", "-j", "-k"):
                self.assertIn(flag, arguments)
            self.assertEqual(
                "root:$6$testsalt$testhash:0:0:99999:7:::\n",
                (root / "etc/shadow").read_text(),
            )
            self.assertIn("authentication=password", (root / "run/tater-sshd.state").read_text())
            self.assertEqual("test-ed25519-host-key\n", (ssh_dir / "dropbear_ed25519_host_key").read_text())
            self.assertEqual(0o600, stat.S_IMODE((ssh_dir / "dropbear_ed25519_host_key").stat().st_mode))

    def test_managed_disabled_mode_overrides_an_authorized_key(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_stubs(root)
            ssh_dir = root / "ssh"
            ssh_dir.mkdir()
            (ssh_dir / "mode").write_text("disabled\n")
            (ssh_dir / "authorized_keys").write_text(STORED_KEY + "\n")

            result = subprocess.run(
                ["sh", str(SUPERVISOR)],
                env=self.environment(root),
                capture_output=True,
                text=True,
                check=False,
            )

            self.assertEqual(0, result.returncode, result.stderr)
            self.assertIn("disabled managed-by-tater", (root / "run/tater-sshd.state").read_text())
            self.assertFalse((root / "dropbear.args").exists())

    def test_status_defaults_to_disabled_before_tater_configures_ssh(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            result = subprocess.run(
                ["sh", str(CONTROL), "status"],
                env=self.environment(root),
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertIn("mode=disabled", result.stdout)


class SSHControlTests(unittest.TestCase):
    def write_executable(self, path: Path, contents: str) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)
        path.chmod(0o755)

    def environment(self, root: Path) -> dict[str, str]:
        busybox = root / "bin/busybox"
        self.write_executable(
            busybox,
            """#!/bin/sh
command=$1
shift
case "$command" in
mkpasswd) cat >/dev/null; printf '$6$testsalt$fixed-hash\\n' ;;
chpasswd) cat > "$TATER_TEST_CHPASSWD" ;;
*) exit 2 ;;
esac
""",
        )
        environment = os.environ.copy()
        environment.update({
            "TATER_SSH_DIR": str(root / "ssh"),
            "TATER_SSH_STATE": str(root / "run/tater-sshd.state"),
            "TATER_SSH_PID_FILE": str(root / "run/tater-sshd.pid"),
            "TATER_BUSYBOX": str(busybox),
            "TATER_TEST_CHPASSWD": str(root / "chpasswd.input"),
        })
        return environment

    def run_control(self, root: Path, input_text: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["sh", str(CONTROL), "configure"],
            env=self.environment(root),
            input=input_text,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_configure_persists_only_hash_and_skips_restart_once_active(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            password = "tater-k7pm-4xq2"

            first = self.run_control(root, f"true\n{password}\n")
            second = self.run_control(root, f"true\n{password}\n")
            (root / "run").mkdir()
            (root / "run/tater-sshd.state").write_text(
                "active port=22 authentication=password\n"
            )
            third = self.run_control(root, f"true\n{password}\n")

            self.assertEqual(0, first.returncode, first.stderr)
            self.assertEqual(0, second.returncode, second.stderr)
            self.assertIn("restarted enabled", second.stdout)
            self.assertEqual(0, third.returncode, third.stderr)
            self.assertIn("unchanged enabled", third.stdout)
            self.assertEqual("password\n", (root / "ssh/mode").read_text())
            self.assertEqual("$6$testsalt$fixed-hash\n", (root / "ssh/password_hash").read_text())
            self.assertNotIn(password, "".join(path.read_text() for path in (root / "ssh").iterdir()))
            self.assertFalse((root / "chpasswd.input").exists())

            disabled = self.run_control(root, "false\n\n")
            self.assertEqual(0, disabled.returncode, disabled.stderr)
            self.assertEqual("disabled\n", (root / "ssh/mode").read_text())

    def test_configure_rejects_short_or_shell_unsafe_passwords(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for password in ("short", "tater password with spaces", "tater:password:bad"):
                result = self.run_control(root, f"true\n{password}\n")
                self.assertNotEqual(0, result.returncode)
                self.assertIn("password", result.stderr)


class SSHRootfsWiringTests(unittest.TestCase):
    def test_every_rootfs_installs_managed_ssh_control(self) -> None:
        for relative in (
            "linux/dot/build_rootfs.py",
            "linux/checkers/build_rootfs.py",
            "linux/rook/build_rootfs.py",
        ):
            with self.subTest(builder=relative):
                source = (ROOT / relative).read_text()
                self.assertIn('"common/tater-ssh-control"', source)
                self.assertIn('"usr/local/sbin/tater-ssh-control"', source)


if __name__ == "__main__":
    unittest.main()
