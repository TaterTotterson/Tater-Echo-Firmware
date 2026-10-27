import hashlib
import importlib.util
import json
from pathlib import Path
import re
import struct
import tempfile
import unittest
from unittest.mock import patch
import zlib


HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("rook_factory_install", HERE / "install.py")
installer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(installer)


class FakeAdb:
    def __init__(self, reply):
        self.reply = reply

    def sh(self, _command):
        return self.reply


class FakeReinstallAdb:
    def __init__(self, identity: str, boot_digest: str = "", digests: dict[int, str] | None = None):
        self.identity = identity
        self.boot_digest = boot_digest
        self.digests = digests or {}

    def sh(self, command: str) -> str:
        if command.startswith("readlink -f "):
            return "/dev/block/mmcblk0p11"
        if command.startswith("blkid "):
            return self.identity
        if "head -c" in command:
            match = re.search(r"head -c (\d+)", command)
            if not match:
                raise AssertionError(command)
            digest = self.digests.get(int(match.group(1)), self.boot_digest)
            return f"{digest}  -"
        raise AssertionError(command)


class FakeLogoAdb:
    def __init__(self, original_digest: str, write_digest: str):
        self.original_digest = original_digest
        self.current_digest = original_digest
        self.write_digest = write_digest
        self.actions: list[str] = []

    def sh(self, command: str) -> str:
        self.actions.append(command)
        if command.startswith("readlink -f "):
            return "/dev/block/mmcblk0p7"
        if command == "sha256sum /tmp/tater-boot-logo.bin":
            return f"{installer.ROOK_LOGO_SHA256}  /tmp/tater-boot-logo.bin"
        if command == "sha256sum /tmp/tater-expdb-backup.img":
            return f"{self.original_digest}  /tmp/tater-expdb-backup.img"
        if command.startswith("dd if=/tmp/tater-boot-logo.bin"):
            self.current_digest = self.write_digest
            return ""
        if command.startswith("dd if=/tmp/tater-expdb-backup.img"):
            self.current_digest = self.original_digest
            return ""
        if command.startswith("echo 3 > /proc/sys/vm/drop_caches; sha256sum "):
            return f"{self.current_digest}  /dev/block/mmcblk0p7"
        if command == "sha256sum /dev/block/mmcblk0p7":
            return f"{self.current_digest}  /dev/block/mmcblk0p7"
        raise AssertionError(command)

    def push(self, source: str, destination: str) -> None:
        self.actions.append(f"push {source} {destination}")


class RookInstallerTests(unittest.TestCase):
    @staticmethod
    def rook_expdb_with_verified_logo_slot() -> bytes:
        raw = b"\x00\x00\x00\xff" * (
            installer.ROOK_LOGO_DIMENSIONS[0] * installer.ROOK_LOGO_DIMENSIONS[1]
        )
        compressed = zlib.compress(raw, 9)
        bundle = struct.pack("<III", 1, 12 + len(compressed), 12) + compressed
        slot = bundle + bytes(installer.ROOK_LOGO_SIZE - len(bundle))
        image = bytearray(installer.ROOK_LOGO_OFFSET + installer.ROOK_LOGO_SIZE + 4096)
        image[installer.ROOK_LOGO_OFFSET:
              installer.ROOK_LOGO_OFFSET + installer.ROOK_LOGO_SIZE] = slot
        pointer = struct.pack(
            "<I", installer.LK_LOGO_BASE + installer.ROOK_LOGO_OFFSET - installer.LK_HEADER_SIZE
        )
        image[installer.LK_HEADER_SIZE:installer.LK_HEADER_SIZE + 4] = pointer
        return bytes(image)

    def test_monochrome_boot_logo_matches_rook_slot(self):
        logo = (HERE / "boot-logo/tater-boot-480.bin").read_bytes()
        self.assertEqual(installer.ROOK_LOGO_SIZE, len(logo))
        self.assertEqual(installer.ROOK_LOGO_SHA256, installer.sha256(
            HERE / "boot-logo/tater-boot-480.bin"))
        count, total, offset = struct.unpack_from("<III", logo)
        self.assertEqual((1, 12), (count, offset))
        raw = zlib.decompress(logo[12:total])
        self.assertEqual(480 * 480 * 4, len(raw))
        self.assertEqual({b"\x00\x00\x00\xff", b"\xff\xff\xff\xff"},
                         {raw[index:index + 4] for index in range(0, len(raw), 4)})
        self.assertEqual(bytes(installer.ROOK_LOGO_SIZE - total), logo[total:])

    def test_rook_logo_slot_requires_bundle_and_one_pointer(self):
        image = bytearray(self.rook_expdb_with_verified_logo_slot())
        pointer = struct.pack(
            "<I", installer.LK_LOGO_BASE + installer.ROOK_LOGO_OFFSET - installer.LK_HEADER_SIZE
        )
        self.assertTrue(installer.valid_rook_logo_slot(bytes(image)))
        image[installer.LK_HEADER_SIZE + 4:installer.LK_HEADER_SIZE + 8] = pointer
        self.assertFalse(installer.valid_rook_logo_slot(bytes(image)))

    def test_rook_boot_logo_write_is_read_back(self):
        original = self.rook_expdb_with_verified_logo_slot()
        logo_path = HERE / "boot-logo/tater-boot-480.bin"
        expected = (original[:installer.ROOK_LOGO_OFFSET] + logo_path.read_bytes()
                    + original[installer.ROOK_LOGO_OFFSET + installer.ROOK_LOGO_SIZE:])
        original_digest = hashlib.sha256(original).hexdigest()
        expected_digest = hashlib.sha256(expected).hexdigest()
        adb = FakeLogoAdb(original_digest, expected_digest)
        with tempfile.TemporaryDirectory() as temporary:
            backup = Path(temporary)
            (backup / "expdb.img").write_bytes(original)
            result = installer.install_tater_boot_logo(adb, backup, logo_path)
        self.assertIn("written and read back successfully", result)
        self.assertTrue(any(action.startswith("dd if=/tmp/tater-boot-logo.bin")
                            for action in adb.actions))
        self.assertFalse(any("conv=notrunc" in action for action in adb.actions))
        self.assertFalse(any("tater-expdb-backup.img" in action for action in adb.actions))

    def test_rook_boot_logo_failure_restores_full_expdb(self):
        original = self.rook_expdb_with_verified_logo_slot()
        original_digest = hashlib.sha256(original).hexdigest()
        adb = FakeLogoAdb(original_digest, "f" * 64)
        with tempfile.TemporaryDirectory() as temporary:
            backup = Path(temporary)
            (backup / "expdb.img").write_bytes(original)
            with self.assertRaises(installer.Fail):
                installer.install_tater_boot_logo(
                    adb, backup, HERE / "boot-logo/tater-boot-480.bin"
                )
        self.assertTrue(any(action.startswith("dd if=/tmp/tater-expdb-backup.img")
                            for action in adb.actions))
        self.assertFalse(any("conv=notrunc" in action for action in adb.actions))

    def test_unknown_rook_logo_layout_is_untouched(self):
        adb = FakeLogoAdb("0" * 64, "0" * 64)
        with tempfile.TemporaryDirectory() as temporary:
            backup = Path(temporary)
            (backup / "expdb.img").write_bytes(bytes(
                installer.ROOK_LOGO_OFFSET + installer.ROOK_LOGO_SIZE
            ))
            result = installer.install_tater_boot_logo(
                adb, backup, HERE / "boot-logo/tater-boot-480.bin"
            )
        self.assertIn("kept Amazon's boot logo", result)
        self.assertEqual([], adb.actions)

    def test_wrong_partition_map_is_rejected(self):
        with self.assertRaises(installer.Fail):
            installer.device_partition(FakeAdb("/dev/block/mmcblk0p12"), "system", 11)

    def test_existing_store_uses_twrp_readable_filesystem_identity(self):
        self.assertTrue(installer.has_existing_tater_store(FakeReinstallAdb(
            '/dev/block/mmcblk0p11: LABEL="techo5-store" UUID="abc" TYPE="ext4"')))
        self.assertFalse(installer.has_existing_tater_store(FakeReinstallAdb(
            '/dev/block/mmcblk0p11: LABEL="system" UUID="abc" TYPE="ext4"')))
        self.assertFalse(installer.has_existing_tater_store(FakeReinstallAdb(
            '/dev/block/mmcblk0p11: LABEL="techo5-store" TYPE="f2fs"')))
        with self.assertRaises(installer.Fail):
            installer.has_existing_tater_store(FakeReinstallAdb(""))

    def test_reinstall_requires_matching_current_boot(self):
        with tempfile.TemporaryDirectory() as temporary:
            boot = Path(temporary) / "boot.img"
            boot.write_bytes(b"ANDROID!rook-boot")
            installer.verify_reinstall_boot(
                FakeReinstallAdb('TYPE="ext4"', installer.sha256(boot)), boot)
            for size, digest in installer.KNOWN_PREVIOUS_TATER_BOOTS:
                installer.verify_reinstall_boot(
                    FakeReinstallAdb('TYPE="ext4"', digests={size: digest}), boot)
            for size, digest in installer.KNOWN_REINSTALL_INTERMEDIATE_BOOTS:
                installer.verify_reinstall_boot(
                    FakeReinstallAdb('TYPE="ext4"', digests={size: digest}), boot)
            with self.assertRaises(installer.Fail):
                installer.verify_reinstall_boot(FakeReinstallAdb('TYPE="ext4"', "0" * 64), boot)

    def test_bundle_is_hash_checked_and_target_locked(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "payload").mkdir()
            image = root / "payload/boot.img"
            image.write_bytes(b"ANDROID!sample")
            details = {"size": image.stat().st_size, "sha256": installer.sha256(image)}
            manifest = {"target": "rook", "base_os": "tater-linux", "version": "v0.0.1",
                        "files": {"payload/boot.img": details}}
            (root / "bundle-manifest.json").write_text(json.dumps(manifest))
            with patch.object(installer, "ROOT", root):
                self.assertEqual(installer.manifest()["target"], "rook")
                image.write_bytes(b"ANDROID!changed")
                with self.assertRaises(installer.Fail):
                    installer.manifest()
                image.write_bytes(b"ANDROID!sample")
                manifest["target"] = "checkers"
                (root / "bundle-manifest.json").write_text(json.dumps(manifest))
                with self.assertRaises(installer.Fail):
                    installer.manifest()


if __name__ == "__main__":
    unittest.main()
