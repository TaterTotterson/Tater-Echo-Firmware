import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


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
    def __init__(self, identity: str, boot_digest: str = "", previous_digest: str = ""):
        self.identity = identity
        self.boot_digest = boot_digest
        self.previous_digest = previous_digest

    def sh(self, command: str) -> str:
        if command.startswith("readlink -f "):
            return "/dev/block/mmcblk0p11"
        if command.startswith("blkid "):
            return self.identity
        if "head -c" in command:
            if str(installer.PRE_BRANDING_BOOT[0]) in command and self.previous_digest:
                return f"{self.previous_digest}  -"
            return f"{self.boot_digest}  -"
        raise AssertionError(command)


class RookInstallerTests(unittest.TestCase):
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
            installer.verify_reinstall_boot(FakeReinstallAdb(
                'TYPE="ext4"', previous_digest=installer.PRE_BRANDING_BOOT[1]), boot)
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
