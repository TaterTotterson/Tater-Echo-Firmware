from __future__ import annotations

import hashlib
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
    def _copy(root: Path, relative: str, destination: str | None = None) -> None:
        source = REAL_REPO / relative
        target = root / (destination or relative)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)

    @staticmethod
    def _write(path: Path, contents: bytes = b"fixture") -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(contents)

    @staticmethod
    def _elf(path: Path, elf_class: int, machine: int) -> None:
        header = bytearray(64)
        header[:4] = b"\x7fELF"
        header[4:7] = bytes((elf_class, 1, 1))
        struct.pack_into("<H", header, 16, 2)
        struct.pack_into("<H", header, 18, machine)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(header)

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

    def dot_fixture(self, root: Path, target: str, version: str) -> tuple[Path, dict[str, str]]:
        targets = root / "targets/targets.json"
        targets.parent.mkdir(parents=True)
        targets.write_text(json.dumps({"targets": {target: {
            "display_name": target.title(), "native_base_os": "tater-linux",
            "factory_install": True, "ota": True, "status": "hardware-tested",
        }}}))
        for relative in (
            "factory/dot-linux/install.sh",
            "factory/dot-linux/install.py",
            "factory/recovery_install.py",
            "factory/to_recovery.py",
            "factory/to_twrp.py",
            "factory/dot-linux/README.md",
            "factory/checkers-linux/tools/techo5/techo5lib.py",
            "linux/dot/tater-net",
            "linux/dot/wifi-set",
            "LICENSE",
            "LICENSES/ECHOLOCAL-MIT.txt",
            "LICENSES/SENDSPIN-MIT.txt",
            "NOTICE.md",
        ):
            destination = "platform/tools/techo5lib.py" if relative.endswith("techo5lib.py") else None
            self._copy(root, relative, destination)
        platform = root / "platform"
        for relative in (
            "tools/dotimage.py", "tools/linux/mkimage.py", "tools/linux/init",
            "tools/linux/back-to-linux.sh",
            "tools/linux/rootfs/usr/local/sbin/slotctl",
            "tools/linux/rootfs/usr/local/sbin/techo5-net",
            "tools/linux/rootfs/usr/local/sbin/wifi-set",
            "tools/linux/rootfs/usr/local/sbin/to-twrp",
            "tools/linux/rootfs/usr/local/sbin/techo5-firewall",
            "LICENSE", "NOTICE",
        ):
            self._write(platform / relative, b"fixture\n")
        self._write(
            platform / "tools/linux/init",
            b"/data/techo5-linux /data/misc/echolocal stale platform wording\n",
        )
        self._write(
            platform / "tools/linux/rootfs/usr/local/sbin/slotctl",
            b"usr/local/bin/echod\n",
        )
        build = root / "device/build"
        self._elf(build / "dot/tater-echo", package_echo_release.ELFCLASS32,
                  package_echo_release.EM_ARM)
        self._elf(build / "microwakeword-linux/libtater_microwakeword.so",
                  package_echo_release.ELFCLASS32, package_echo_release.EM_ARM)
        self._elf(build / "onnxruntime-linux-armv7/libonnxruntime.so",
                  package_echo_release.ELFCLASS32, package_echo_release.EM_ARM)
        rootfs = build / f"tater-{target}-rootfs-{version}.tar.gz"
        self._write(rootfs, b"rootfs")
        kernel = root / f"{target}.kernel"
        rescue = root / "rescue.tar"
        alpine = root / "alpine.tar.gz"
        for path in (kernel, rescue, alpine):
            self._write(path)
        env = {
            "TATER_DOT_PLATFORM_SOURCE": str(platform),
            f"TATER_{target.upper()}_KERNEL": str(kernel),
            "TATER_DOT_RESCUE_APKS": str(rescue),
            "TATER_DOT_ALPINE_ROOTFS": str(alpine),
            f"TATER_{target.upper()}_ROOTFS": str(rootfs),
            "TATER_LINUX_ROOTFS": "",
        }
        return targets, env

    def test_dot_targets_package_linux_rootfs_and_single_daemon_ota(self) -> None:
        version = "v9.8.7"
        for target in ("biscuit", "radar"):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary) / "repo"
                output = Path(temporary) / "release"
                targets, env = self.dot_fixture(root, target, version)
                with (
                    mock.patch.object(package_echo_release, "REPO", root),
                    mock.patch.object(package_echo_release, "TARGETS", targets),
                    mock.patch.object(package_echo_release.subprocess, "check_output",
                                      return_value=package_echo_release.DOT_PLATFORM_COMMIT + "\n"),
                    mock.patch.dict(os.environ, env, clear=False),
                ):
                    artifacts = package_echo_release.build(version, target, output)

                names = {path.name for path in artifacts}
                self.assertIn(f"tater-echo-{target}-{version}-factory.tar.gz", names)
                self.assertIn(f"tater-echo-{target}-{version}-ota.tar.gz", names)
                self.assertNotIn(f"tater-echo-{target}-{version}-ota-bridge.tar.gz", names)
                with tarfile.open(output / f"tater-echo-{target}-{version}-ota.tar.gz") as archive:
                    self.assertEqual(set(archive.getnames()), {"manifest.json", "tater-echo"})
                    ota_manifest = json.load(archive.extractfile("manifest.json"))
                    self.assertEqual("tater-linux", ota_manifest["base_os"])
                factory_archive = output / f"tater-echo-{target}-{version}-factory.tar.gz"
                extract = Path(temporary) / "extract"
                with tarfile.open(factory_archive) as archive:
                    prefix = f"tater-echo-{target}-{version}-factory"
                    members = set(archive.getnames())
                    for required in (
                        "to_recovery.py", "recovery_install.py",
                        "to_twrp.py",
                        "payload/rootfs.tar.gz", "payload/kernel.zImage-dtb",
                        "payload/rescue-apks.tar", "payload/alpine-minirootfs.tar.gz",
                        "tools/dotimage.py", "tools/linux/mkimage.py",
                        "tools/linux/rootfs/usr/local/sbin/tater-net",
                        "tools/linux/rootfs/usr/local/sbin/techo5-net",
                        "tools/linux/rootfs/usr/local/sbin/wifi-set",
                        "licenses/ECHOLOCAL-MIT.txt",
                        "licenses/SENDSPIN-MIT.txt",
                    ):
                        self.assertIn(f"{prefix}/{required}", members)
                    bundle = json.load(archive.extractfile(f"{prefix}/bundle-manifest.json"))
                    self.assertEqual("tater-linux", bundle["base_os"])
                    self.assertIn("to_twrp.py", bundle["files"])
                    self.assertIn("to_recovery.py", bundle["files"])
                    self.assertIn("recovery_install.py", bundle["files"])
                    init = archive.extractfile(f"{prefix}/tools/linux/init").read().decode()
                    self.assertNotIn("/data/techo5-linux", init)
                    self.assertNotIn("/data/misc/echolocal", init)
                    self.assertNotIn("stale platform wording", init)
                    slotctl = archive.extractfile(
                        f"{prefix}/tools/linux/rootfs/usr/local/sbin/slotctl"
                    ).read().decode()
                    self.assertIn("usr/local/bin/tater-echo", slotctl)
                    installer = archive.extractfile(f"{prefix}/install.py").read().decode()
                    self.assertIn("rm -rf $S/slots/a $S/slots/b", installer)
                    network = archive.extractfile(
                        f"{prefix}/tools/linux/rootfs/usr/local/sbin/tater-net"
                    ).read().decode()
                    self.assertIn("DATA=${TATER_DATA_DIR:-/data/tater-linux}", network)
                    self.assertIn("CONF=$DATA/wpa_supplicant.conf", network)
                    self.assertNotIn("/data/misc/wifi", network)
                    archive.extractall(extract)
                verified = subprocess.run(
                    [str(extract / prefix / "install.sh"), "--verify-bundle"],
                    capture_output=True, text=True, check=False,
                )
                self.assertEqual(0, verified.returncode, verified.stderr)
                self.assertIn(f"Verified {target} Tater Linux factory bundle {version}", verified.stdout)
                repair_help = subprocess.run(
                    [str(extract / prefix / "install.sh"), "--repair", "--help"],
                    capture_output=True, text=True, check=False,
                )
                self.assertEqual(0, repair_help.returncode, repair_help.stderr)
                self.assertIn("Repair an installed Tater Echo", repair_help.stdout)
                release_manifest = json.loads((output / "firmware-manifest.json").read_text())
                self.assertNotIn("ota_migrations", release_manifest["targets"][target])

    def test_rejects_a_host_binary_when_arm_is_required(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            binary = Path(temporary) / "binary"
            binary.write_bytes(b"\xca\xfe\xba\xbe" + bytes(60))
            with self.assertRaisesRegex(SystemExit, "must be a little-endian 32-bit ARM ELF"):
                package_echo_release.require_elf(
                    binary, package_echo_release.ELFCLASS32,
                    package_echo_release.EM_ARM, "firmware",
                )

    def test_little_endian_arm_elf_is_accepted(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            binary = Path(temporary) / "binary"
            header = bytearray(64)
            header[:4] = b"\x7fELF"
            header[4:7] = bytes((package_echo_release.ELFCLASS32, 1, 1))
            struct.pack_into("<H", header, 18, package_echo_release.EM_ARM)
            binary.write_bytes(header)
            package_echo_release.require_elf(
                binary, package_echo_release.ELFCLASS32,
                package_echo_release.EM_ARM, "firmware",
            )

    def test_crown_uses_shared_show_factory_with_target_specific_boot(self) -> None:
        version = "v2.3.0"
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "repo"
            output = Path(temporary) / "release"
            targets = root / "targets/targets.json"
            targets.parent.mkdir(parents=True)
            targets.write_text(json.dumps({"targets": {"crown": {
                "display_name": "Echo Show 8 1st Gen",
                "factory_install": True,
                "ota": True,
                "status": "experimental",
            }}}))

            for relative in (
                "factory/checkers-linux/install.sh",
                "factory/checkers-linux/install.py",
                "factory/recovery_install.py",
                "factory/to_recovery.py",
                "factory/to_twrp.py",
                "factory/checkers-linux/tools/techo5/install-show.py",
                "factory/checkers-linux/tools/techo5/techo5lib.py",
                "factory/checkers-linux/tools/techo5/LICENSE",
                "factory/checkers-linux/tools/techo5/boot-logo/cronos-wordmark.bin.b64",
                "factory/crown-linux/README.md",
                "linux/checkers/provision_console.py",
                "linux/checkers/brand_boot.py",
                "LICENSE",
                "LICENSES/ECHOLOCAL-MIT.txt",
                "LICENSES/SENDSPIN-MIT.txt",
                "NOTICE.md",
            ):
                self._copy(root, relative)

            build = root / "device/build"
            build.mkdir(parents=True)
            (build / "tater-echo").write_bytes(b"crown-daemon")
            (build / "tater-show-linux").write_bytes(b"crown-screen")
            (build / "tater-camera").write_bytes(b"crown-camera")
            (build / "tater-rescue-fbprobe").write_bytes(b"arm-renderer")
            runtime = build / "microwakeword-linux/libtater_microwakeword.so"
            runtime.parent.mkdir(parents=True)
            runtime.write_bytes(b"crown-wake-runtime")
            ort = build / "onnxruntime-linux-armv7/libonnxruntime.so"
            ort.parent.mkdir(parents=True)
            ort.write_bytes(b"crown-onnx-runtime")
            rootfs = build / f"tater-crown-rootfs-{version}.tar.gz"
            rootfs.write_bytes(b"crown-rootfs")
            boot = root / "linux/checkers/inputs/tater-echo-crown-baseline-boot.img"
            boot.parent.mkdir(parents=True)
            boot.write_bytes(b"pinned-crown-boot")

            def brand(command, **_kwargs):
                self.assertEqual(["--target", "crown"], command[2:4])
                Path(command[-1]).write_bytes(b"branded-crown-boot")
                return subprocess.CompletedProcess(command, 0)

            with (
                mock.patch.object(package_echo_release, "REPO", root),
                mock.patch.object(package_echo_release, "TARGETS", targets),
                mock.patch.object(package_echo_release.subprocess, "run", side_effect=brand),
                mock.patch.dict(os.environ, {
                    "TATER_CROWN_ROOTFS": str(rootfs),
                    "TATER_CROWN_BOOT_IMAGE": str(boot),
                    "TATER_LINUX_ROOTFS": "",
                    "TATER_LINUX_BOOT_IMAGE": "",
                }),
            ):
                artifacts = package_echo_release.build(version, "crown", output)

            names = {path.name for path in artifacts}
            self.assertIn(f"tater-echo-crown-{version}-factory.tar.gz", names)
            self.assertIn(f"tater-echo-crown-{version}-ota.tar.gz", names)
            self.assertIn(f"tater-echo-crown-{version}-ota-bridge.tar.gz", names)
            with tarfile.open(
                output / f"tater-echo-crown-{version}-ota.tar.gz", "r:gz"
            ) as archive:
                self.assertEqual(
                    set(archive.getnames()),
                    {"manifest.json", "tater-echo", "tater-show", "tater-camera"},
                )
            with tarfile.open(
                output / f"tater-echo-crown-{version}-ota-bridge.tar.gz", "r:gz"
            ) as archive:
                self.assertEqual(
                    set(archive.getnames()), {"manifest.json", "tater-echo", "tater-show"}
                )
            release_manifest = json.loads((output / "firmware-manifest.json").read_text())
            self.assertEqual(release_manifest["targets"]["crown"]["ota_migrations"], [{
                "id": "linux-app-components-v1",
                "required_capability": "ota_bundle_components_version",
                "minimum_capability_version": 1,
                "bridge_artifact": "ota_bridge",
                "final_artifact": "ota",
            }])
            extract = Path(temporary) / "extract"
            with tarfile.open(
                output / f"tater-echo-crown-{version}-factory.tar.gz", "r:gz"
            ) as archive:
                prefix = f"tater-echo-crown-{version}-factory"
                self.assertEqual(
                    b"branded-crown-boot",
                    archive.extractfile(f"{prefix}/payload/boot.img").read(),
                )
                self.assertIn("Echo Show 8", archive.extractfile(
                    f"{prefix}/README.md").read().decode())
                self.assertIn(f"{prefix}/to_twrp.py", archive.getnames())
                self.assertIn(f"{prefix}/to_recovery.py", archive.getnames())
                self.assertIn(f"{prefix}/recovery_install.py", archive.getnames())
                logo = archive.extractfile(
                    f"{prefix}/tools/techo5/boot-logo/cronos-wordmark.bin"
                ).read()
                self.assertEqual(package_echo_release.SHOW_BOOT_LOGO_SIZE, len(logo))
                self.assertEqual(
                    package_echo_release.SHOW_BOOT_LOGO_SHA256,
                    hashlib.sha256(logo).hexdigest(),
                )
                manifest = json.load(archive.extractfile(f"{prefix}/bundle-manifest.json"))
                archive.extractall(extract)
            self.assertEqual("crown", manifest["target"])
            self.assertEqual("tater-linux", manifest["base_os"])
            verified = subprocess.run(
                [str(extract / prefix / "install.sh"), "--verify-bundle"],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(0, verified.returncode, verified.stderr)
            self.assertIn(f"Bundle verified: Crown Tater Linux {version}", verified.stdout)
            repair_help = subprocess.run(
                [str(extract / prefix / "install.sh"), "--repair", "--help"],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(0, repair_help.returncode, repair_help.stderr)
            self.assertIn("Repair an installed Tater Echo", repair_help.stdout)

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
                "factory/recovery_install.py",
                "factory/to_recovery.py",
                "factory/to_twrp.py",
                "factory/rook-linux/README.md",
                "factory/checkers-linux/tools/techo5/techo5lib.py",
                "factory/checkers-linux/tools/techo5/LICENSE",
                "LICENSE",
                "LICENSES/ECHOLOCAL-MIT.txt",
                "LICENSES/SENDSPIN-MIT.txt",
                "NOTICE.md",
            ):
                self._copy(root, relative)

            build = root / "device/build/rook"
            build.mkdir(parents=True)
            (build / "tater-echo").write_bytes(b"rook-daemon-v2.1.0")
            (build / "tater-show").write_bytes(b"rook-screen-v2.1.0")
            (build / "tater-camera").write_bytes(b"rook-camera-v2.1.0")
            runtime = build / "microwakeword/libtater_microwakeword.so"
            runtime.parent.mkdir(parents=True)
            runtime.write_bytes(b"rook-wake-runtime-v2.1.0")
            ort = root / "device/build/onnxruntime-linux-armv7/libonnxruntime.so"
            ort.parent.mkdir(parents=True)
            ort.write_bytes(b"rook-onnx-runtime-v2.1.0")
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
            self.assertIn(f"tater-echo-rook-{version}-ota-bridge.tar.gz", names)
            self.assertIn(f"tater-echo-rook-{version}-wake-runtime.so", names)
            self.assertIn(f"tater-echo-rook-{version}-onnxruntime.so", names)

            manifest = json.loads((output / "firmware-manifest.json").read_text())
            self.assertEqual(manifest["version"], version)
            self.assertEqual(set(manifest["targets"]), {"rook"})
            self.assertEqual(manifest["targets"]["rook"]["status"], "hardware-tested")

            factory_archive = output / f"tater-echo-rook-{version}-factory.tar.gz"
            extract = Path(temporary) / "extract"
            with tarfile.open(factory_archive, "r:gz") as archive:
                installer = archive.getmember(f"tater-echo-rook-{version}-factory/install.sh")
                self.assertEqual(installer.mode & 0o777, 0o755)
                helper = archive.getmember(f"tater-echo-rook-{version}-factory/to_twrp.py")
                self.assertEqual(helper.mode & 0o777, 0o755)
                recovery = archive.getmember(f"tater-echo-rook-{version}-factory/to_recovery.py")
                self.assertEqual(recovery.mode & 0o777, 0o755)
                repair = archive.getmember(f"tater-echo-rook-{version}-factory/recovery_install.py")
                self.assertEqual(repair.mode & 0o777, 0o755)
                archive.extractall(extract)
            factory = extract / f"tater-echo-rook-{version}-factory"
            result = subprocess.run(
                [str(factory / "install.sh"), "--verify-bundle"],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn(f"Verified Rook factory bundle {version}", result.stdout)
            repair_help = subprocess.run(
                [str(factory / "install.sh"), "--repair", "--help"],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(repair_help.returncode, 0, repair_help.stderr)
            self.assertIn("Repair an installed Tater Echo", repair_help.stdout)

            with tarfile.open(output / f"tater-echo-rook-{version}-ota.tar.gz", "r:gz") as archive:
                self.assertEqual(
                    set(archive.getnames()),
                    {"manifest.json", "tater-echo", "tater-show", "tater-camera"},
                )
            with tarfile.open(
                output / f"tater-echo-rook-{version}-ota-bridge.tar.gz", "r:gz"
            ) as archive:
                self.assertEqual(
                    set(archive.getnames()), {"manifest.json", "tater-echo", "tater-show"}
                )
            self.assertEqual(manifest["targets"]["rook"]["ota_migrations"], [{
                "id": "linux-app-components-v1",
                "required_capability": "ota_bundle_components_version",
                "minimum_capability_version": 1,
                "bridge_artifact": "ota_bridge",
                "final_artifact": "ota",
            }])

    def test_athena_factory_pins_platform_images_and_shared_show_ota(self) -> None:
        version = "v3.1.0"
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "repo"
            output = Path(temporary) / "release"
            targets = root / "targets/targets.json"
            targets.parent.mkdir(parents=True)
            targets.write_text(json.dumps({"targets": {"athena": {
                "display_name": "Echo Show 8 2nd Gen",
                "factory_install": True,
                "ota": True,
                "status": "experimental-hardware-validation-required",
            }}}))
            for relative in (
                "factory/athena-linux/install.sh",
                "factory/athena-linux/install.py",
                "factory/athena-linux/README.md",
                "factory/recovery_install.py",
                "factory/to_recovery.py",
                "factory/to_twrp.py",
                "LICENSE",
                "LICENSES/ECHOLOCAL-MIT.txt",
                "LICENSES/SENDSPIN-MIT.txt",
                "NOTICE.md",
            ):
                self._copy(root, relative)

            platform = root / "platform"
            for relative in ("tools/install-athena.py", "tools/techo5lib.py", "LICENSE", "NOTICE"):
                self._write(platform / relative, (relative + "\n").encode())
            build = root / "device/build"
            for name, value in (
                ("tater-echo", b"athena-daemon"),
                ("tater-show-linux", b"athena-screen"),
                ("tater-camera", b"athena-camera"),
            ):
                self._write(build / name, value)
            self._write(build / "microwakeword-linux/libtater_microwakeword.so", b"runtime")
            self._write(build / "onnxruntime-linux-armv7/libonnxruntime.so", b"ort")
            rootfs = build / f"tater-athena-rootfs-{version}.tar.gz"
            system = root / "system.img.gz"
            boot = root / "boot.img"
            self._write(rootfs, b"athena-rootfs")
            self._write(system, b"athena-store")
            self._write(boot, b"athena-mainline-boot")

            with (
                mock.patch.object(package_echo_release, "REPO", root),
                mock.patch.object(package_echo_release, "TARGETS", targets),
                mock.patch.object(package_echo_release.subprocess, "check_output",
                                  return_value=package_echo_release.ATHENA_PLATFORM_COMMIT + "\n"),
                mock.patch.dict(os.environ, {
                    "TATER_ATHENA_PLATFORM_SOURCE": str(platform),
                    "TATER_ATHENA_ROOTFS": str(rootfs),
                    "TATER_ATHENA_SYSTEM_IMAGE": str(system),
                    "TATER_ATHENA_BOOT_IMAGE": str(boot),
                    "TATER_LINUX_ROOTFS": "",
                }),
            ):
                artifacts = package_echo_release.build(version, "athena", output)

            names = {path.name for path in artifacts}
            self.assertIn(f"tater-echo-athena-{version}-factory.tar.gz", names)
            self.assertIn(f"tater-echo-athena-{version}-ota.tar.gz", names)
            self.assertIn(f"tater-echo-athena-{version}-ota-bridge.tar.gz", names)
            extract = Path(temporary) / "extract"
            with tarfile.open(output / f"tater-echo-athena-{version}-factory.tar.gz") as archive:
                prefix = f"tater-echo-athena-{version}-factory"
                members = set(archive.getnames())
                for relative in (
                    "payload/rootfs.tar.gz", "payload/system.img.gz", "payload/boot.img",
                    "tools/install-athena.py", "tools/techo5lib.py", "recovery_install.py",
                ):
                    self.assertIn(f"{prefix}/{relative}", members)
                archive.extractall(extract)
            verified = subprocess.run(
                [str(extract / prefix / "install.sh"), "--verify-bundle"],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(0, verified.returncode, verified.stderr)
            self.assertIn(f"Bundle verified: Athena Tater Linux {version}", verified.stdout)

            repair_help = subprocess.run(
                [str(extract / prefix / "install.sh"), "--repair", "--help"],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(0, repair_help.returncode, repair_help.stderr)
            self.assertIn("Repair an installed Tater Echo", repair_help.stdout)

            for override in ("--image=other.img", "--boot=other.img", "--kernel=amazon"):
                refused = subprocess.run(
                    [str(extract / prefix / "install.sh"), override],
                    capture_output=True, text=True, check=False,
                )
                self.assertNotEqual(0, refused.returncode, override)
                self.assertIn("pins its image", refused.stderr)

            manifest_path = extract / prefix / "bundle-manifest.json"
            incomplete = json.loads(manifest_path.read_text())
            del incomplete["files"]["payload/boot.img"]
            manifest_path.write_text(json.dumps(incomplete))
            refused = subprocess.run(
                [str(extract / prefix / "install.sh"), "--verify-bundle"],
                capture_output=True, text=True, check=False,
            )
            self.assertNotEqual(0, refused.returncode)
            self.assertIn("missing required files", refused.stderr)


if __name__ == "__main__":
    unittest.main()
