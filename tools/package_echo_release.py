#!/usr/bin/env python3
"""Create target-scoped Tater Echo factory and OTA release artifacts."""

from __future__ import annotations

import argparse
import base64
import gzip
import hashlib
import io
import json
import lzma
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO))
from tools import platform_lock  # noqa: E402

TARGETS = REPO / "targets" / "targets.json"
LINUX_DOT_TARGETS = {"biscuit", "radar"}
LINUX_SHOW_TARGETS = {"checkers", "crown"}
LINUX_ATHENA_TARGETS = {"athena"}
ELFCLASS32 = 1
ELFCLASS64 = 2
EM_ARM = 40
EM_AARCH64 = 183
SHOW_BOOT_LOGO_SHA256 = "1354914c8a2498a7f69a8ebc4a23782ff3369f9e4f980de04f7b9b77734c3403"
SHOW_BOOT_LOGO_SIZE = 6105
PLATFORM_LOCK = platform_lock.load()
DOT_PLATFORM_COMMIT = PLATFORM_LOCK["platforms"]["dot"]["commit"]
ATHENA_PLATFORM_COMMIT = PLATFORM_LOCK["platforms"]["athena"]["commit"]
PLATFORM_REPOSITORY = PLATFORM_LOCK["source"]["repository"]


def platform_provenance(target: str) -> dict:
    """Describe the exact external platform and toolchain behind an artifact."""
    platform_lock.verify(PLATFORM_LOCK, require_builder_digest=True)
    platforms = PLATFORM_LOCK["platforms"]
    builder = PLATFORM_LOCK["builder"]
    if target in LINUX_DOT_TARGETS:
        source = {
            "repository": PLATFORM_REPOSITORY,
            "commit": platforms["dot"]["commit"],
            "release": platforms["dot"]["release"],
        }
    elif target in LINUX_SHOW_TARGETS:
        source = {
            "repository": PLATFORM_REPOSITORY,
            "commit": platforms["show"]["checkers_commit"],
            "release": platforms["show"]["release"],
        }
    elif target in LINUX_ATHENA_TARGETS:
        source = {
            "repository": PLATFORM_REPOSITORY,
            "commit": platforms["athena"]["commit"],
            "release": platforms["athena"]["release"],
        }
    elif target == "rook":
        source = {
            "repository": PLATFORM_REPOSITORY,
            "commit": platforms["rook"]["commit"],
            "release": platforms["rook"]["release"],
            "show_helper_commit": platforms["show"]["checkers_commit"],
        }
    else:
        return {}
    return {
        "lock_sha256": digest(platform_lock.LOCK_PATH),
        "builder": f"{builder['image']}@{builder['digest']}",
        "source": source,
    }


def require_elf(path: Path, elf_class: int, machine: int, description: str) -> None:
    """Reject stale host executables before they enter a release bundle."""
    try:
        header = path.read_bytes()[:20]
    except OSError as exc:
        raise SystemExit(f"required release input is missing: {path}") from exc
    class_names = {ELFCLASS32: "32-bit", ELFCLASS64: "64-bit"}
    machine_names = {EM_ARM: "ARM", EM_AARCH64: "AArch64"}
    expected = f"{class_names[elf_class]} {machine_names[machine]} ELF"
    if (len(header) < 20 or header[:4] != b"\x7fELF"
            or header[4] != elf_class or header[5] != 1
            or struct.unpack_from("<H", header, 18)[0] != machine):
        raise SystemExit(f"{description} must be a little-endian {expected}: {path}")


def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as src:
        for chunk in iter(lambda: src.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def metadata(path: Path) -> dict:
    return {"sha256": digest(path), "size": path.stat().st_size}


def copy(source: Path, destination: Path, mode: int) -> None:
    if not source.is_file():
        raise SystemExit(f"required release input is missing: {source}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    destination.chmod(mode)


def decode_verified_asset(source: Path, destination: Path, expected_sha256: str,
                          expected_size: int) -> None:
    try:
        encoded = "".join(source.read_text(encoding="ascii").split())
        payload = base64.b64decode(encoded, validate=True)
    except (OSError, UnicodeError, ValueError) as exc:
        raise SystemExit(f"required encoded release asset is invalid: {source}") from exc
    if len(payload) != expected_size or hashlib.sha256(payload).hexdigest() != expected_sha256:
        raise SystemExit(f"required encoded release asset failed verification: {source}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(payload)
    destination.chmod(0o644)


def write_json(path: Path, value: dict) -> None:
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def deterministic_tar(source: Path, destination: Path) -> None:
    with destination.open("wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0, compresslevel=9) as gz:
            with tarfile.open(fileobj=gz, mode="w", format=tarfile.PAX_FORMAT) as archive:
                for path in [source] + sorted(source.rglob("*")):
                    arcname = path.relative_to(source.parent)
                    info = archive.gettarinfo(str(path), str(arcname))
                    info.uid = info.gid = 0
                    info.uname = info.gname = "root"
                    info.mtime = 0
                    if path.is_file():
                        with path.open("rb") as src:
                            archive.addfile(info, src)
                    else:
                        archive.addfile(info)


def deterministic_files_tar(entries: dict[str, bytes], destination: Path,
                            executable: set[str]) -> None:
    with destination.open("wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0, compresslevel=9) as gz:
            with tarfile.open(fileobj=gz, mode="w", format=tarfile.PAX_FORMAT) as archive:
                for name, contents in sorted(entries.items()):
                    info = tarfile.TarInfo(name)
                    info.size = len(contents)
                    info.mode = 0o755 if name in executable else 0o644
                    info.uid = info.gid = 0
                    info.uname = info.gname = "root"
                    info.mtime = 0
                    archive.addfile(info, io.BytesIO(contents))


def verify_rook_factory_inputs(rootfs: Path, boot: Path, version: str) -> None:
    image = boot.read_bytes()
    if len(image) > 16 * 1024 * 1024 or image[:8] != b"ANDROID!":
        raise SystemExit("Rook boot image is invalid or exceeds the boot partition")
    try:
        kernel_size, _, ramdisk_size, _, _, _, _, page, header_version = struct.unpack_from(
            "<9I", image, 8)
        if header_version != 0 or page != 2048:
            raise ValueError("unexpected Rook boot header")
        ramdisk_at = page + ((kernel_size + page - 1) // page) * page
        ramdisk = lzma.decompress(image[ramdisk_at:ramdisk_at + ramdisk_size])
    except (struct.error, lzma.LZMAError, ValueError) as error:
        raise SystemExit(f"Rook boot image has no valid rescue ramdisk: {error}") from error
    if b"INSTALLING TATER" not in ramdisk or b"TATER RECOVERY" not in ramdisk or \
            b"TATER LINUX" not in ramdisk:
        raise SystemExit("Rook boot image lacks the branded rescue screen")
    with tarfile.open(rootfs, "r:gz") as archive:
        members = {member.name.lstrip("./"): member for member in archive.getmembers()}
        if any(name.startswith("vendor/") and member.isfile() for name, member in members.items()):
            raise SystemExit("Rook rootfs must not include device-specific vendor files")
        release = members.get("etc/tater-release")
        board = members.get("etc/techo5/device.conf")
        if release is None or board is None:
            raise SystemExit("Rook rootfs is missing release or board metadata")
        release_file = archive.extractfile(release)
        board_file = archive.extractfile(board)
        if release_file is None or board_file is None:
            raise SystemExit("Rook rootfs release or board metadata is unreadable")
        if f"Rook {version}" not in release_file.read().decode() or \
                "STORE_DEV=/dev/mmcblk0p11" not in board_file.read().decode():
            raise SystemExit("Rook rootfs target, version, or slot partition is wrong")

def build(version: str, target: str, output: Path) -> list[Path]:
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?", version):
        raise SystemExit("version must look like v0.1.0")
    targets = json.loads(TARGETS.read_text())["targets"]
    if target not in targets:
        raise SystemExit(f"unknown target {target!r}")
    output.mkdir(parents=True, exist_ok=True)
    stem = f"tater-echo-{target}-{version}"
    artifacts: dict[str, Path] = {}
    if target in LINUX_DOT_TARGETS | LINUX_SHOW_TARGETS | LINUX_ATHENA_TARGETS | {"rook"}:
        prefix = target.upper()
        configured_rootfs = (
            os.getenv(f"TATER_{prefix}_ROOTFS", "").strip()
            or os.getenv("TATER_LINUX_ROOTFS", "").strip()
        )
        if target == "rook":
            default_rootfs = REPO / "device/build" / f"rook/tater-rook-rootfs-{version}.tar.gz"
        elif target == "athena":
            default_rootfs = REPO / "device/build" / f"tater-athena-rootfs-{version}.tar.gz"
        else:
            default_rootfs = REPO / "device/build" / f"tater-{target}-rootfs-{version}.tar.gz"
        rootfs_source = Path(configured_rootfs) if configured_rootfs else default_rootfs
        build_dir = REPO / "device/build" / (
            "rook" if target == "rook" else
            "dot" if target in LINUX_DOT_TARGETS else ""
        )
        server = build_dir / "tater-echo"
        required_sources = [rootfs_source, server]
        if target not in LINUX_DOT_TARGETS:
            show = build_dir / ("tater-show" if target == "rook" else "tater-show-linux")
            camera = build_dir / "tater-camera"
            required_sources.extend((show, camera))
        for source in required_sources:
            if not source.is_file():
                raise SystemExit(f"required release input is missing: {source}")
        if target in LINUX_DOT_TARGETS:
            require_elf(server, ELFCLASS32, EM_ARM, f"{target} Tater Linux daemon")

        def write_app_ota(path: Path, app_files: dict[str, bytes]) -> None:
            app_manifest = {
                "schema": 1,
                "target": target,
                "base_os": "tater-linux",
                "version": version,
                "files": {
                    name: {"sha256": hashlib.sha256(contents).hexdigest(), "size": len(contents)}
                    for name, contents in sorted(app_files.items())
                },
            }
            app_entries = {
                **app_files,
                "manifest.json": (json.dumps(app_manifest, indent=2, sort_keys=True) + "\n").encode(),
            }
            deterministic_files_tar(app_entries, path, set(app_files))

        ota = output / f"{stem}-ota.tar.gz"
        if target in LINUX_DOT_TARGETS:
            write_app_ota(ota, {"tater-echo": server.read_bytes()})
        else:
            bridge_files = {"tater-echo": server.read_bytes(), "tater-show": show.read_bytes()}
            ota_bridge = output / f"{stem}-ota-bridge.tar.gz"
            write_app_ota(ota_bridge, bridge_files)
            artifacts["ota_bridge"] = ota_bridge
            write_app_ota(ota, {**bridge_files, "tater-camera": camera.read_bytes()})
        artifacts["ota"] = ota
    else:
        raise SystemExit(f"factory packaging is not implemented for {target!r}")

    runtime_source = REPO / "device/build" / (
        "rook/microwakeword/libtater_microwakeword.so" if target == "rook"
        else "microwakeword-linux/libtater_microwakeword.so"
    )
    ort_source = REPO / "device/build/onnxruntime-linux-armv7/libonnxruntime.so"
    if target in LINUX_DOT_TARGETS:
        require_elf(runtime_source, ELFCLASS32, EM_ARM, f"{target} microWakeWord runtime")
        require_elf(ort_source, ELFCLASS32, EM_ARM, f"{target} ONNX Runtime")
    runtime_asset = output / f"{stem}-wake-runtime.so"
    copy(runtime_source, runtime_asset, 0o755)
    artifacts["wake_runtime"] = runtime_asset

    ort_asset = output / f"{stem}-onnxruntime.so"
    copy(ort_source, ort_asset, 0o755)
    artifacts["onnx_runtime"] = ort_asset

    with tempfile.TemporaryDirectory(prefix="tater-echo-package-") as temporary:
        factory = Path(temporary) / f"{stem}-factory"
        factory.mkdir()
        if target in LINUX_DOT_TARGETS:
            platform_source_value = os.getenv("TATER_DOT_PLATFORM_SOURCE", "").strip()
            if not platform_source_value:
                raise SystemExit("TATER_DOT_PLATFORM_SOURCE must name the pinned TECHO5 Dot checkout")
            platform_source = Path(platform_source_value)
            try:
                platform_commit = subprocess.check_output(
                    ["git", "-C", str(platform_source), "rev-parse", "HEAD"], text=True
                ).strip()
            except (OSError, subprocess.CalledProcessError) as error:
                raise SystemExit(f"cannot verify TECHO5 Dot source: {platform_source}") from error
            if platform_commit != DOT_PLATFORM_COMMIT:
                raise SystemExit(
                    f"TECHO5 Dot checkout is {platform_commit}, expected {DOT_PLATFORM_COMMIT}"
                )
            kernel_value = os.getenv(f"TATER_{target.upper()}_KERNEL", "").strip()
            rescue_value = os.getenv("TATER_DOT_RESCUE_APKS", "").strip()
            alpine_value = os.getenv("TATER_DOT_ALPINE_ROOTFS", "").strip()
            if not kernel_value or not rescue_value or not alpine_value:
                raise SystemExit(
                    f"TATER_{target.upper()}_KERNEL, TATER_DOT_RESCUE_APKS, and "
                    "TATER_DOT_ALPINE_ROOTFS are required for a Dot Linux factory bundle"
                )
            dot_factory = REPO / "factory/dot-linux"
            inputs = {
                "install.sh": (dot_factory / "install.sh", 0o755),
                "install.py": (dot_factory / "install.py", 0o755),
                "README.md": (dot_factory / "README.md", 0o644),
                "LICENSE": (REPO / "LICENSE", 0o644),
                "NOTICE.md": (REPO / "NOTICE.md", 0o644),
                "payload/rootfs.tar.gz": (rootfs_source, 0o644),
                "payload/kernel.zImage-dtb": (Path(kernel_value), 0o644),
                "payload/rescue-apks.tar": (Path(rescue_value), 0o644),
                "payload/alpine-minirootfs.tar.gz": (Path(alpine_value), 0o644),
                "tools/techo5lib.py": (platform_source / "tools/techo5lib.py", 0o644),
                "tools/dotimage.py": (platform_source / "tools/dotimage.py", 0o644),
                "tools/linux/mkimage.py": (platform_source / "tools/linux/mkimage.py", 0o755),
                "tools/linux/init": (platform_source / "tools/linux/init", 0o755),
                "tools/linux/back-to-linux.sh": (
                    platform_source / "tools/linux/back-to-linux.sh", 0o755),
                "tools/linux/rootfs/usr/local/sbin/slotctl": (
                    platform_source / "tools/linux/rootfs/usr/local/sbin/slotctl", 0o755),
                "tools/linux/rootfs/usr/local/sbin/tater-net": (
                    REPO / "linux/dot/tater-net", 0o755),
                # The pinned platform rescue init still invokes the old name.
                "tools/linux/rootfs/usr/local/sbin/techo5-net": (
                    REPO / "linux/dot/tater-net", 0o755),
                "tools/linux/rootfs/usr/local/sbin/wifi-set": (
                    REPO / "linux/dot/wifi-set", 0o755),
                "tools/linux/rootfs/usr/local/sbin/to-twrp": (
                    platform_source / "tools/linux/rootfs/usr/local/sbin/to-twrp", 0o755),
                "tools/linux/rootfs/usr/local/sbin/techo5-firewall": (
                    platform_source / "tools/linux/rootfs/usr/local/sbin/techo5-firewall", 0o755),
                "tools/TECHO5-DOT-LICENSE": (platform_source / "LICENSE", 0o644),
                "tools/TECHO5-DOT-NOTICE": (platform_source / "NOTICE", 0o644),
            }
        elif target in LINUX_SHOW_TARGETS:
            configured_boot = (
                os.getenv(f"TATER_{target.upper()}_BOOT_IMAGE", "").strip()
                or os.getenv("TATER_LINUX_BOOT_IMAGE", "").strip()
            )
            default_boot = {
                "checkers": REPO / "linux/checkers/inputs/techo5-boot-checkers-v0.7.16.img",
                "crown": REPO / "linux/checkers/inputs/techo5-boot-crown-v1.0.1.img",
            }[target]
            boot_source = (
                Path(configured_boot) if configured_boot
                else default_boot
            )
            inputs = {
                "install.sh": (REPO / "factory/checkers-linux/install.sh", 0o755),
                "install.py": (REPO / "factory/checkers-linux/install.py", 0o755),
                "README.md": (
                    REPO / ("factory/checkers-linux/README.md" if target == "checkers"
                            else "factory/crown-linux/README.md"), 0o644),
                "LICENSE": (REPO / "LICENSE", 0o644),
                "NOTICE.md": (REPO / "NOTICE.md", 0o644),
                "payload/rootfs.tar.gz": (rootfs_source, 0o644),
                "tools/provision_console.py": (
                    REPO / "linux/checkers/provision_console.py", 0o755),
                "tools/techo5/install-show.py": (
                    REPO / "factory/checkers-linux/tools/techo5/install-show.py", 0o755),
                "tools/techo5/techo5lib.py": (
                    REPO / "factory/checkers-linux/tools/techo5/techo5lib.py", 0o644),
                "tools/techo5/LICENSE": (
                    REPO / "factory/checkers-linux/tools/techo5/LICENSE", 0o644),
            }
        elif target == "athena":
            platform_source_value = os.getenv("TATER_ATHENA_PLATFORM_SOURCE", "").strip()
            if not platform_source_value:
                raise SystemExit("TATER_ATHENA_PLATFORM_SOURCE must name the pinned TECHO5 checkout")
            platform_source = Path(platform_source_value)
            try:
                platform_commit = subprocess.check_output(
                    ["git", "-C", str(platform_source), "rev-parse", "HEAD"], text=True
                ).strip()
            except (OSError, subprocess.CalledProcessError) as error:
                raise SystemExit(f"cannot verify TECHO5 Athena source: {platform_source}") from error
            if platform_commit != ATHENA_PLATFORM_COMMIT:
                raise SystemExit(
                    f"Tater Echo Linux Athena checkout is {platform_commit}, expected {ATHENA_PLATFORM_COMMIT}"
                )
            system_value = os.getenv("TATER_ATHENA_SYSTEM_IMAGE", "").strip()
            boot_value = os.getenv("TATER_ATHENA_BOOT_IMAGE", "").strip()
            if not system_value or not boot_value:
                raise SystemExit("TATER_ATHENA_SYSTEM_IMAGE and TATER_ATHENA_BOOT_IMAGE are required")
            system_source, boot_source = Path(system_value), Path(boot_value)
            inputs = {
                "install.sh": (REPO / "factory/athena-linux/install.sh", 0o755),
                "install.py": (REPO / "factory/athena-linux/install.py", 0o755),
                "README.md": (REPO / "factory/athena-linux/README.md", 0o644),
                "LICENSE": (REPO / "LICENSE", 0o644),
                "NOTICE.md": (REPO / "NOTICE.md", 0o644),
                "payload/rootfs.tar.gz": (rootfs_source, 0o644),
                "payload/system.img.gz": (system_source, 0o644),
                "payload/boot.img": (boot_source, 0o644),
                "tools/install-athena.py": (platform_source / "tools/install-athena.py", 0o755),
                "tools/techo5lib.py": (platform_source / "tools/techo5lib.py", 0o644),
                "tools/TECHO5-LICENSE": (platform_source / "LICENSE", 0o644),
                "tools/TECHO5-NOTICE": (platform_source / "NOTICE", 0o644),
            }
        else:
            configured_boot = os.getenv("TATER_ROOK_BOOT_IMAGE", "").strip()
            boot_source = (Path(configured_boot) if configured_boot else
                           REPO / "device/build/rook/tater-rook-boot.img")
            if not boot_source.is_file():
                raise SystemExit(f"required Rook boot image is missing: {boot_source}")
            verify_rook_factory_inputs(rootfs_source, boot_source, version)
            inputs = {
                "install.sh": (REPO / "factory/rook-linux/install.sh", 0o755),
                "install.py": (REPO / "factory/rook-linux/install.py", 0o755),
                "README.md": (REPO / "factory/rook-linux/README.md", 0o644),
                "LICENSE": (REPO / "LICENSE", 0o644),
                "NOTICE.md": (REPO / "NOTICE.md", 0o644),
                "payload/rootfs.tar.gz": (rootfs_source, 0o644),
                "payload/boot.img": (boot_source, 0o644),
                "tools/techo5/techo5lib.py": (
                    REPO / "factory/checkers-linux/tools/techo5/techo5lib.py", 0o644),
                "tools/techo5/LICENSE": (
                    REPO / "factory/checkers-linux/tools/techo5/LICENSE", 0o644),
            }

        inputs["licenses/ECHOLOCAL-MIT.txt"] = (
            REPO / "LICENSES/ECHOLOCAL-MIT.txt", 0o644)
        inputs["licenses/SENDSPIN-MIT.txt"] = (
            REPO / "LICENSES/SENDSPIN-MIT.txt", 0o644)
        inputs["to_recovery.py"] = (REPO / "factory/to_recovery.py", 0o755)
        inputs["recovery_install.py"] = (REPO / "factory/recovery_install.py", 0o755)
        # TWRP remains the lower-level escape hatch.  Tater Recovery is the
        # normal user path, but do not remove the tool needed when the Linux
        # rescue image itself is what needs repair.
        inputs["to_twrp.py"] = (REPO / "factory/to_twrp.py", 0o755)

        for relative, (source, mode) in inputs.items():
            copy(source, factory / relative, mode)
        if target in LINUX_DOT_TARGETS:
            init = factory / "tools/linux/init"
            init_text = init.read_text()
            init_text = init_text.replace("/data/techo5-linux", "/data/tater-linux")
            init_text = init_text.replace("/data/misc/echolocal", "/data/local/etc/tater")
            init_text = "".join(
                "Tater Linux hardware bring-up\n"
                if "/data/tater-linux" in line and "/data/local/etc/tater" in line
                else line
                for line in init_text.splitlines(keepends=True)
            )
            init.write_text(init_text)
            slotctl = factory / "tools/linux/rootfs/usr/local/sbin/slotctl"
            slotctl.write_text(slotctl.read_text().replace(
                "usr/local/bin/echod", "usr/local/bin/tater-echo"))
        if target in LINUX_SHOW_TARGETS:
            decode_verified_asset(
                REPO / "factory/checkers-linux/tools/techo5/boot-logo/cronos-wordmark.bin.b64",
                factory / "tools/techo5/boot-logo/cronos-wordmark.bin",
                SHOW_BOOT_LOGO_SHA256,
                SHOW_BOOT_LOGO_SIZE,
            )
            branded_boot = factory / "payload/boot.img"
            subprocess.run([
                sys.executable,
                str(REPO / "linux/checkers/brand_boot.py"),
                "--target", target,
                str(boot_source),
                str(REPO / "device/build/tater-rescue-fbprobe"),
                str(branded_boot),
            ], check=True)
        bundle_files = {
            str(path.relative_to(factory)): metadata(path)
            for path in sorted(factory.rglob("*")) if path.is_file()
        }
        bundle_manifest = {
            "schema": 1,
            "product": "Tater Echo Firmware",
            "target": target,
            "version": version,
            "files": bundle_files,
        }
        if target in LINUX_DOT_TARGETS | LINUX_SHOW_TARGETS | LINUX_ATHENA_TARGETS | {"rook"}:
            bundle_manifest["base_os"] = "tater-linux"
            bundle_manifest["platform"] = platform_provenance(target)
        write_json(factory / "bundle-manifest.json", bundle_manifest)
        archive = output / f"{stem}-factory.tar.gz"
        deterministic_tar(factory, archive)
        artifacts["factory"] = archive

    release_manifest = output / "firmware-manifest.json"
    target_manifest = {
        **targets[target],
        "artifacts": {
            kind: {"name": path.name, **metadata(path)}
            for kind, path in artifacts.items()
        },
    }
    if target in LINUX_SHOW_TARGETS | LINUX_ATHENA_TARGETS | {"rook"}:
        target_manifest["ota_migrations"] = [{
            "id": "linux-app-components-v1",
            "required_capability": "ota_bundle_components_version",
            "minimum_capability_version": 1,
            "bridge_artifact": "ota_bridge",
            "final_artifact": "ota",
        }]
    target_manifest["platform"] = platform_provenance(target)
    write_json(release_manifest, {
        "schema": 1,
        "product": "Tater Echo Firmware",
        "version": version,
        "targets": {
            target: target_manifest
        },
    })
    sums = output / "SHA256SUMS"
    release_files = [*artifacts.values(), release_manifest]
    sums.write_text("".join(f"{digest(path)}  {path.name}\n" for path in release_files))
    return [*artifacts.values(), release_manifest, sums]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--target", default="biscuit")
    parser.add_argument("--output", type=Path, default=REPO / "release")
    args = parser.parse_args()
    for artifact in build(args.version, args.target, args.output.resolve()):
        print(artifact)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
