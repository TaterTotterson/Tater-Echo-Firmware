from __future__ import annotations

import hashlib
import importlib.util
from pathlib import Path
import re
import tarfile
import tempfile
import unittest


HELPER_PATH = Path(__file__).with_name("recovery_install.py")
SPEC = importlib.util.spec_from_file_location("tater_echo_recovery_install", HELPER_PATH)
assert SPEC is not None and SPEC.loader is not None
helper = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(helper)


class FakeConsole:
    def __init__(self, answers: list[str]) -> None:
        self.answers = list(answers)
        self.commands: list[str] = []

    def console_exchange(self, _port: str, command: str, _wait: int):
        self.commands.append(command)
        return self.answers.pop(0)


class FakeSerialPort:
    def __init__(self) -> None:
        self.output = bytearray()
        self.payload = bytearray()
        self.size = 0
        self.digest = ""
        self.command_seen = False
        self.closed = False
        self.max_payload_write = 0

    def write(self, data: bytes) -> None:
        if not self.command_seen and b"head -c " in data:
            command = data.decode()
            self.size = int(re.search(r"head -c (\d+)", command).group(1))
            digests = re.findall(r"[0-9a-f]{64}", command)
            self.digest = digests[0]
            self.command_seen = True
            self.output.extend(b"\nTATER_UPLOAD_READY_42\n")
            return
        if self.command_seen and len(self.payload) < self.size:
            self.max_payload_write = max(self.max_payload_write, len(data))
            self.payload.extend(data)
            if len(self.payload) == self.size:
                got = hashlib.sha256(self.payload).hexdigest()
                self.output.extend(f"\nTATER_UPLOAD_DONE_42 {got}\n".encode())

    def read(self) -> bytes:
        data = bytes(self.output)
        self.output.clear()
        return data

    def close(self) -> None:
        self.closed = True


class FakeUploadConsole(FakeConsole):
    CONSOLE_DOT = ("18d1", "4ee7")
    CONSOLE_TECHO5 = ("1d6b", "0104")
    IS_WINDOWS = False

    def __init__(self) -> None:
        super().__init__(["TATER_UPLOAD_SPACE_OK"])
        self.port = FakeSerialPort()

    def SerialPort(self, _port: str):
        return self.port


class RecoveryInstallTests(unittest.TestCase):
    def test_rootfs_target_and_version_are_verified(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            release = root / "etc/tater-release"
            release.parent.mkdir(parents=True)
            release.write_text("Tater Linux Radar v9.8.7\n")
            archive = root / "rootfs.tar.gz"
            with tarfile.open(archive, "w:gz") as output:
                output.add(release, arcname="etc/tater-release")
            self.assertEqual(
                "Tater Linux Radar v9.8.7",
                helper.verify_rootfs(archive, "radar", "v9.8.7"),
            )
            with self.assertRaisesRegex(helper.Fail, "not biscuit"):
                helper.verify_rootfs(archive, "biscuit", "v9.8.7")
            with self.assertRaisesRegex(helper.Fail, "not radar v9.8.70"):
                helper.verify_rootfs(archive, "radar", "v9.8.70")

    def test_probe_requires_recovery_and_matching_target(self) -> None:
        console = FakeConsole([
            "TATER_RECOVERY_READY\nTATER_RELEASE=Tater Linux Crown v3.0.0\nactive: a"
        ])
        release = helper.probe_recovery(console, "/dev/ttyACM0", "crown")
        self.assertEqual("Tater Linux Crown v3.0.0", release)
        self.assertIn("! -s /run/techo5/slot", console.commands[0])
        self.assertIn("[ -e /.techo5-store ]", console.commands[0])
        self.assertIn('STORE="$store" slotctl status', console.commands[0])

    def test_usb_payload_is_verified_on_device(self) -> None:
        console = FakeUploadConsole()
        with tempfile.TemporaryDirectory() as temporary:
            payload = Path(temporary) / "rootfs.tar.gz"
            payload.write_bytes((b"Tater recovery payload\n" * 8192) + b"done")
            helper.upload_rootfs(console, "/dev/ttyACM0", payload)
        self.assertEqual(hashlib.sha256(console.port.payload).hexdigest(), console.port.digest)
        self.assertTrue(console.port.closed)

    def test_windows_upload_stays_below_serial_write_timeout(self) -> None:
        console = FakeUploadConsole()
        console.IS_WINDOWS = True
        with tempfile.TemporaryDirectory() as temporary:
            payload = Path(temporary) / "rootfs.tar.gz"
            payload.write_bytes(b"x" * 20000)
            helper.upload_rootfs(console, "COM3", payload)
        self.assertLessEqual(console.port.max_payload_write, 4096)

    def test_repair_preserves_settings_unless_factory_reset_requested(self) -> None:
        preserved = FakeConsole([
            "TATER_RECOVERY_INSTALL_OK\nactive: b\nslot b: trial 3 — Tater Linux Checkers v3.1.0"
        ])
        helper.install_rootfs(preserved, "/dev/ttyACM0", "checkers", False)
        self.assertNotIn("native.json", preserved.commands[0])
        self.assertIn("[ -e /.techo5-store ]", preserved.commands[0])
        self.assertIn('STORE="$store" slotctl install', preserved.commands[0])
        reset = FakeConsole([
            "TATER_RECOVERY_INSTALL_OK\nactive: b\nslot b: trial 3 — Tater Linux Checkers v3.1.0"
        ])
        helper.install_rootfs(reset, "/dev/ttyACM0", "checkers", True)
        self.assertIn("native.json", reset.commands[0])
        self.assertIn("setup_enabled", reset.commands[0])


if __name__ == "__main__":
    unittest.main()
