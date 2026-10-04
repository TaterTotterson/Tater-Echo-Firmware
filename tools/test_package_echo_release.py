from __future__ import annotations

import json
import lzma
import os
from pathlib import Path
import shutil
import struct
import subprocess
import tarfile
import tempfile
import unittest
from unittest import mock

from tools import package_echo_release


REAL_REPO = Path(__file__).resolve().parents[1]


class PackageEchoReleaseTests(unittest.TestCase):
    @staticmethod
    def _copy(root: Path, relative: str) -> None:
        source = REAL_REPO / relative
        destination = root / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)

    @staticmethod
    def _rook_boot(path: Path) -> None:
        ramdisk = lzma.compress(b"INSTALLING TATER\nTATER RECOVERY\nTATER LINUX\n")
        page = 2048
        header = b"ANDROID!" + struct.pack(
            "<9I",
            0, 0, len(ramdisk), 0, 0, 0, 0, page, 0,
        )
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(header.ljust(page, b"\0") + ramdisk)

    @staticmethod
    def _rook_rootfs(path: Path, version: str) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "etc/techo5").mkdir(parents=True)
            (root / "etc/tater-release").write_text(f"Tater Linux Rook {version}\n")
            (root / "etc/techo5/device.conf").write_text("STORE_DEV=/dev/mmcblk0p11\n")
            path.parent.mkdir(parents=True, exist_ok=True)
            with tarfile.open(path, "w:gz") as archive:
                archive.add(root / "etc", arcname="etc")

    def test_rook_factory_and_ota_bundle_verify_end_to_end(self) -> None:
        version = "v2.1.0"
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "repo"
            output = Path(temporary) / "release"
            targets = root / "targets/targets.json"
            targets.parent.mkdir(parents=True)
            targets.write_text(json.dumps({"targets": {"rook": {
                "display_name": "Echo Spot",
                "factory_install": True,
                "ota": True,
                "status": "hardware-tested",
            }}}))

            for relative in (
                "factory/rook-linux/install.sh",
                "factory/rook-linux/install.py",
                "factory/rook-linux/README.md",
                "factory/checkers-linux/tools/techo5/techo5lib.py",
                "factory/checkers-linux/tools/techo5/LICENSE",
                "LICENSE",
                "NOTICE.md",
            ):
                self._copy(root, relative)

            build = root / "device/build/rook"
            build.mkdir(parents=True)
            (build / "tater-echo").write_bytes(b"rook-daemon-v2.1.0")
            (build / "tater-show").write_bytes(b"rook-screen-v2.1.0")
            self._rook_boot(build / "tater-rook-boot.img")
            rootfs = build / f"tater-rook-rootfs-{version}.tar.gz"
            self._rook_rootfs(rootfs, version)

            with (
                mock.patch.object(package_echo_release, "REPO", root),
                mock.patch.object(package_echo_release, "TARGETS", targets),
                mock.patch.dict(os.environ, {
                    "TATER_ROOK_ROOTFS": str(rootfs),
                    "TATER_ROOK_BOOT_IMAGE": "",
                }),
            ):
                artifacts = package_echo_release.build(version, "rook", output)

            names = {path.name for path in artifacts}
            self.assertIn(f"tater-echo-rook-{version}-factory.tar.gz", names)
            self.assertIn(f"tater-echo-rook-{version}-ota.tar.gz", names)

            manifest = json.loads((output / "firmware-manifest.json").read_text())
            self.assertEqual(manifest["version"], version)
            self.assertEqual(set(manifest["targets"]), {"rook"})
            self.assertEqual(manifest["targets"]["rook"]["status"], "hardware-tested")

            factory_archive = output / f"tater-echo-rook-{version}-factory.tar.gz"
            extract = Path(temporary) / "extract"
            with tarfile.open(factory_archive, "r:gz") as archive:
                installer = archive.getmember(f"tater-echo-rook-{version}-factory/install.sh")
                self.assertEqual(installer.mode & 0o777, 0o755)
                archive.extractall(extract)
            factory = extract / f"tater-echo-rook-{version}-factory"
            result = subprocess.run(
                [str(factory / "install.sh"), "--verify-bundle"],
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn(f"Verified Rook factory bundle {version}", result.stdout)

            with tarfile.open(output / f"tater-echo-rook-{version}-ota.tar.gz", "r:gz") as archive:
                self.assertEqual(set(archive.getnames()), {"manifest.json", "tater-echo", "tater-show"})


if __name__ == "__main__":
    unittest.main()
