from __future__ import annotations

import io
from pathlib import Path
import tarfile
import tempfile
import unittest

from factory import tater_settings as settings


def archive_bytes(files: dict[str, bytes]) -> bytes:
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w") as archive:
        for name, contents in files.items():
            member = tarfile.TarInfo(name)
            member.size = len(contents)
            archive.addfile(member, io.BytesIO(contents))
    return output.getvalue()


def archive_with_localtime(
        target: str, nested: bool = False,
        path: str = settings.LOCALTIME_PATH) -> bytes:
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w") as archive:
        timezone = tarfile.TarInfo("data/local/etc/tater/timezone")
        timezone.size = 4
        archive.addfile(timezone, io.BytesIO(b"UTC\n"))
        localtime = tarfile.TarInfo(path)
        localtime.type = tarfile.SYMTYPE
        localtime.linkname = target
        archive.addfile(localtime)
        if nested:
            child = tarfile.TarInfo(path + "/escape")
            child.size = 3
            archive.addfile(child, io.BytesIO(b"bad"))
    return output.getvalue()


class FakeAdb:
    def __init__(self, archive: bytes | None = None) -> None:
        self.archive = archive
        self.remote: dict[str, bytes] = {}
        self.commands: list[str] = []

    def sh(self, command: str) -> str:
        self.commands.append(command)
        if "for path in" in command:
            if self.archive is None:
                return "TATER_SETTINGS_NONE"
            self.remote[settings.REMOTE_ARCHIVE] = self.archive
            return "TATER_SETTINGS_ARCHIVED"
        if command == f"sha256sum {settings.REMOTE_ARCHIVE}":
            import hashlib
            return hashlib.sha256(self.remote[settings.REMOTE_ARCHIVE]).hexdigest() + "  archive"
        if command == f"rm -f {settings.REMOTE_ARCHIVE}":
            self.remote.pop(settings.REMOTE_ARCHIVE, None)
            return ""
        if command.startswith("rm -rf ") and "TATER_SETTINGS_CLEARED" in command:
            return "TATER_SETTINGS_CLEARED"
        if "tar -xf" in command:
            return "TATER_SETTINGS_RESTORED"
        raise AssertionError(command)

    def pull(self, remote: str, local: str) -> bool:
        Path(local).write_bytes(self.remote[remote])
        return True

    def push(self, local: str, remote: str) -> None:
        self.remote[remote] = Path(local).read_bytes()


class TaterSettingsTests(unittest.TestCase):
    def test_capture_and_restore_complete_pairing(self) -> None:
        data = archive_bytes({
            "data/local/etc/tater/native.json": b'{"url":"http://tater:8501"}\n',
            "data/local/etc/tater/device_token": b"token\n",
            "data/local/etc/tater/state.json": b'{"muted":false}\n',
            "data/tater-linux/wpa_supplicant.conf": b"network={}\n",
            "data/tater-linux/ssh/mode": b"password\n",
        })
        adb = FakeAdb(data)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "settings.tar"
            saved = settings.capture(adb, path)
            self.assertIsNotNone(saved)
            assert saved is not None
            self.assertTrue(saved.paired)
            self.assertEqual(0o600, path.stat().st_mode & 0o777)
            settings.restore(adb, saved)
        self.assertTrue(any("TATER_SETTINGS_CLEARED" in command for command in adb.commands))
        self.assertTrue(any("TATER_SETTINGS_RESTORED" in command for command in adb.commands))

    def test_no_existing_state_is_not_an_error(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            self.assertIsNone(settings.capture(FakeAdb(), Path(directory) / "settings.tar"))

    def test_archive_allows_zoneinfo_localtime_symlink(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "settings.tar"
            for localtime_path in settings.LOCALTIME_PATHS:
                path.write_bytes(archive_with_localtime(
                    "/usr/share/zoneinfo/America/Chicago", path=localtime_path))
                self.assertFalse(settings.inspect_archive(path))

    def test_archive_rejects_escape_symlink_and_app_slot(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "bad.tar"
            for member in ("../escape", "data/tater-linux/app/slots/a/tater-echo"):
                path.write_bytes(archive_bytes({member: b"bad"}))
                with self.assertRaises(settings.SettingsError):
                    settings.inspect_archive(path)
            output = io.BytesIO()
            with tarfile.open(fileobj=output, mode="w") as archive:
                link = tarfile.TarInfo("data/local/etc/tater/native.json")
                link.type = tarfile.SYMTYPE
                link.linkname = "/etc/shadow"
                archive.addfile(link)
            path.write_bytes(output.getvalue())
            with self.assertRaises(settings.SettingsError):
                settings.inspect_archive(path)
            for localtime_path in settings.LOCALTIME_PATHS:
                for contents in (
                        archive_with_localtime("/etc/shadow", path=localtime_path),
                        archive_with_localtime(
                            "/usr/share/zoneinfo/../../etc/shadow", path=localtime_path),
                        archive_with_localtime(
                            "/usr/share/zoneinfo/UTC", nested=True, path=localtime_path)):
                    path.write_bytes(contents)
                    with self.assertRaises(settings.SettingsError):
                        settings.inspect_archive(path)

    def test_restore_rejects_archive_changed_after_capture(self) -> None:
        data = archive_bytes({"data/local/etc/tater/native.json": b"native"})
        adb = FakeAdb(data)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "settings.tar"
            saved = settings.capture(adb, path)
            assert saved is not None
            path.write_bytes(archive_bytes({
                "data/local/etc/tater/native.json": b"native",
                "data/local/etc/tater/device_token": b"token",
            }))
            with self.assertRaisesRegex(settings.SettingsError, "changed"):
                settings.restore(adb, saved)


if __name__ == "__main__":
    unittest.main()
