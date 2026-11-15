#!/usr/bin/env python3
"""Create target-scoped Tater Echo factory and OTA release artifacts."""

from __future__ import annotations

import argparse
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
import zlib

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO))
from tools import athena_codec_bundle, athena_mesa, hardware_bundle, platform_lock, release_hardware_inputs  # noqa: E402
from linux.dot import build_kernel as dot_kernel  # noqa: E402
from linux.dot import early_boot as dot_early_boot  # noqa: E402

TARGETS = REPO / "targets" / "targets.json"
LINUX_DOT_TARGETS = {"biscuit", "radar"}
LINUX_SHOW_TARGETS = {"checkers", "cronos", "crown"}
LINUX_ATHENA_TARGETS = {"athena"}
ELFCLASS32 = 1
ELFCLASS64 = 2
EM_ARM = 40
OTA_SCHEMA = 2


def ota_platform(target: str) -> str:
    return f"echo-hardware-v1/{target}"


def verify_rootfs_ota_platform(rootfs: Path, target: str) -> None:
    try:
        with tarfile.open(rootfs, 'r:gz') as archive:
            profiles = [entry for entry in archive if entry.name.removeprefix('./') == 'etc/tater-ota-platform']
            if len(profiles) != 1 or not profiles[0].isfile() or profiles[0].size > 128:
                raise ValueError('missing or unsafe immutable OTA profile')
            value = archive.extractfile(profiles[0]).read().decode().strip()
            if value != ota_platform(target):
                raise ValueError('rootfs OTA platform does not match its target')
    except (OSError, ValueError, tarfile.TarError) as error:
        raise SystemExit(f'{target} rootfs platform verification failed: {error}') from error
EM_AARCH64 = 183
SHOW_BOOT_LOGO_SHA256 = "9cfa08b845406dea4e1aa6b3878126afce16aa05a2a9afe5058c8c84d034c7d6"
SHOW_BOOT_LOGO_SIZE = 6105
ROOK_BOOT_LOGO_SHA256 = "2c8d54f6987cef2e0877360e873191bfc48a35a13e8d7f42e0ecb987f2df860d"
ROOK_BOOT_LOGO_SIZE = 12096
PLATFORM_LOCK = platform_lock.load()
DOT_PLATFORM_COMMIT = PLATFORM_LOCK["platforms"]["dot"]["commit"]
ATHENA_PLATFORM_COMMIT = PLATFORM_LOCK["platforms"]["athena"]["commit"]
PLATFORM_REPOSITORY = PLATFORM_LOCK["source"]["repository"]
DOT_AUDIO_LOCK_PATH = REPO / "platforms/dot-audio-lock.json"
# Reviewed build inputs, not an assertion of physical release qualification.
# The separate release_qualified gates must also pass. Checkers restores the
# public baseline's independent microphones and uses a clean initramfs build.
QUALIFIED_CHECKERS_KERNEL_SHA256 = "dee4a1d17315d61d3cc83b8b1d2b1526395a6068020da11eefbc034fc84590f0"
QUALIFIED_CRONOS_KERNEL_SHA256 = "79410d93c4a5a5c2920cd4b4164f2d99afef17661b0cdaea1a45336e6ab734e4"
QUALIFIED_ROOK_KERNEL_SHA256 = "aa33d9b29a5c4d3e5a3805842df385858589de62272e8cca8a5f263adaef5d17"


def require_dot_audio_platform(target: str) -> None:
    profile = json.loads(DOT_AUDIO_LOCK_PATH.read_text())
    status = profile.get("targets", {}).get(target, {})
    if (status.get("release_qualified") is not True
            or not re.fullmatch(r"[0-9a-f]{64}", str(status.get("kernel_image_sha256", "")))):
        raise SystemExit(f"{target.title()} audio release is blocked: {status.get('remaining', 'complete kernel/audio/OTA qualification')}")


def verify_dot_audio_kernel(kernel: Path, target: str) -> None:
    profile = json.loads(DOT_AUDIO_LOCK_PATH.read_text())
    expected = profile["targets"][target].get("kernel_image_sha256")
    try:
        actual = dot_kernel.verify_kernel(kernel, target)
    except (OSError, ValueError, zlib.error) as error:
        raise SystemExit(f"{target} audio kernel verification failed: {error}") from error
    if actual["sha256"] != expected:
        raise SystemExit(f"{target} factory kernel differs from the reviewed audio kernel")


def require_rook_hardware_platform() -> None:
    # Functional owner-device trials do not replace final fleet OTA/factory
    # qualification or authorize publishing the owner's private libraries.
    platform = PLATFORM_LOCK["platforms"]["rook"]
    hardware = platform.get("hardware", {})
    if (hardware.get("release_qualified") is not True
            or hardware.get("source_commit") != platform["commit"]
            or hardware.get("kernel_image_sha256") != QUALIFIED_ROOK_KERNEL_SHA256
            or platform["kernel"]["sha256"] != QUALIFIED_ROOK_KERNEL_SHA256):
        raise SystemExit("Rook hardware release is blocked: finish the final OTA/factory audit before distribution")


def require_crown_hardware_platform() -> None:
    # A successful owner-local application trial is not distribution approval.
    # Keep release packaging closed until final installer/OTA qualification.
    show = PLATFORM_LOCK["platforms"]["show"]
    hardware = show.get("crown_hardware", {})
    if (hardware.get("release_qualified") is not True
            or hardware.get("source_commit") != show["checkers_commit"]
            or not re.fullmatch(r"[0-9a-f]{40}", str(hardware.get("source_commit", "")))
            or not re.fullmatch(r"[0-9a-f]{64}", str(hardware.get("kernel_image_sha256", "")))
            or hardware.get("boot_sha256") != show["targets"]["crown"]["boot"]["sha256"]):
        raise SystemExit("Crown hardware release is blocked: complete final kernel/runtime/installer/OTA qualification and pin its reviewed inputs; see docs/crown-release-readiness.md")


def require_cronos_hardware_platform() -> None:
    show = PLATFORM_LOCK["platforms"]["show"]
    hardware = show.get("cronos_hardware", {})
    if (hardware.get("release_qualified") is not True
            or hardware.get("source_commit") != show["checkers_commit"]
            or not re.fullmatch(r"[0-9a-f]{40}", str(hardware.get("source_commit", "")))
            or hardware.get("kernel_image_sha256") != QUALIFIED_CRONOS_KERNEL_SHA256
            or hardware.get("boot_sha256") != show["targets"]["cronos"]["boot"]["sha256"]):
        raise SystemExit(
            "Cronos hardware release is blocked: pin the reviewed kernel source, "
            "qualified kernel image and matching redistributable boot artifact first; "
            "see docs/cronos.md"
        )


def require_checkers_hardware_platform() -> None:
    show = PLATFORM_LOCK["platforms"]["show"]
    hardware = show.get("checkers_hardware", {})
    if (hardware.get("release_qualified") is not True
            or hardware.get("source_commit") != show["checkers_commit"]
            or not re.fullmatch(r"[0-9a-f]{40}", str(hardware.get("source_commit", "")))
            or hardware.get("kernel_image_sha256") != QUALIFIED_CHECKERS_KERNEL_SHA256
            or hardware.get("boot_sha256") != show["targets"]["checkers"]["boot"]["sha256"]):
        raise SystemExit(
            "Checkers hardware release is blocked: pin the reviewed kernel source, "
            "qualified kernel image and matching redistributable boot artifact first; "
            "see docs/checkers-hardware-build.md"
        )


def require_athena_hardware_platform() -> None:
    platform = PLATFORM_LOCK["platforms"]["athena"]
    hardware = platform.get("hardware", {})
    codec = athena_codec_bundle.load_lock()
    if (hardware.get("release_qualified") is not True
            or hardware.get("source_commit") != platform["commit"]
            or hardware.get("boot_sha256") != codec["kernel_template_sha256"]
            or platform["boot"]["sha256"] != codec["kernel_template_sha256"]
            or hardware.get("mesa_manifest_sha256") != athena_mesa.MANIFEST_SHA256
            or hardware.get("codec_lock_sha256") != digest(athena_codec_bundle.LOCK)):
        raise SystemExit("Athena hardware release is blocked: finish matching kernel/Mesa/codec and private-runtime installer qualification before distribution")


def verify_checkers_hardware_boot(boot: Path) -> None:
    verify_hardware_boot(boot, "Checkers", QUALIFIED_CHECKERS_KERNEL_SHA256)


def verify_cronos_hardware_boot(boot: Path) -> None:
    verify_hardware_boot(boot, "Cronos", QUALIFIED_CRONOS_KERNEL_SHA256)


def verify_hardware_boot(boot: Path, target_name: str, expected: str) -> None:
    """Do not package an old kernel merely because a metadata pin was edited."""
    with boot.open("rb") as source:
        header = source.read(2048)
        if len(header) != 2048 or header[:8] != b"ANDROID!":
            raise SystemExit(f"{target_name} hardware boot is not a legacy Android boot image")
        size = struct.unpack_from("<I", header, 8)[0]
        page = struct.unpack_from("<I", header, 36)[0]
        if page not in (2048, 4096, 8192, 16384) or not 0 < size <= 32 * 1024 * 1024:
            raise SystemExit(f"{target_name} hardware boot has invalid kernel bounds")
        source.seek(page)
        kernel = source.read(size)
    if len(kernel) != size or hashlib.sha256(kernel).hexdigest() != expected:
        raise SystemExit(f"{target_name} factory boot does not contain the qualified hardware kernel")


def hardware_files_from_rootfs(rootfs_source: Path, target: str = "checkers") -> dict[str, bytes]:
    """Return only the reviewed public helpers embedded in this exact rootfs."""
    if target == "athena":
        athena_mesa.verify_rootfs(rootfs_source)
    with tempfile.TemporaryDirectory(prefix="tater-public-hardware-") as temporary:
        directory = Path(temporary)
        with tarfile.open(rootfs_source, "r:gz") as archive:
            seen = set()
            for member in archive:
                name = member.name.removeprefix("./")
                for filename in (*hardware_bundle.helper_names(target), hardware_bundle.MANIFEST):
                    if name != "usr/local/bin/" + filename:
                        continue
                    if filename in seen or not member.isfile() or not 0 < member.size <= 8 * 1024 * 1024:
                        raise SystemExit("rootfs contains an invalid public hardware helper")
                    source = archive.extractfile(member)
                    if source is None:
                        raise SystemExit("rootfs hardware helper is unreadable")
                    (directory / filename).write_bytes(source.read())
                    seen.add(filename)
        hardware_bundle.verify(directory, target=target)
        result = {path.name: path.read_bytes() for path in directory.iterdir()}
    if target == "athena":
        # GPU-only app archives omit the AP pair and silently disable codecs
        # after an app update. Verify the complete system-owned codec set, but
        # include only its executable pair in an application OTA; kernel
        # modules remain in their matching system slot.
        with tempfile.TemporaryDirectory(prefix="tater-athena-public-codecs-") as temporary:
            directory = Path(temporary)
            codec_lock = athena_codec_bundle.load_lock()
            locations = {"usr/local/bin/"+name: name
                         for name in codec_lock['codec_source_lock']['helpers']}
            locations.update({"usr/lib/tater-athena-codecs/"+name: name
                              for name in (*codec_lock['modules'], athena_codec_bundle.MANIFEST)})
            seen = set()
            template_seen = False
            with tarfile.open(rootfs_source, "r:gz") as archive:
                for entry in archive:
                    name = entry.name.removeprefix("./")
                    if name == 'usr/lib/tater-athena-codecs' and not entry.isdir():
                        raise ValueError('Athena codec root may not be a file or link')
                    if name == 'usr/share/techo5/kernel/boot-athena.img':
                        if template_seen or not entry.isfile() or not 0 < entry.size <= 16*1024*1024:
                            raise ValueError('unsafe or duplicate Athena kernel template')
                        data = archive.extractfile(entry).read()
                        if hashlib.sha256(data).hexdigest() != codec_lock['kernel_template_sha256']:
                            raise ValueError('Athena rootfs codec kernel template mismatch')
                        template_seen = True
                    if name not in locations:
                        if name.startswith('usr/lib/tater-athena-codecs/') and not entry.isdir():
                            raise ValueError('unexpected Athena codec sidecar')
                        continue
                    relative = locations[name]
                    if relative in seen or not entry.isfile() or not 0 < entry.size <= 8*1024*1024:
                        raise ValueError('unsafe or duplicate Athena codec entry')
                    (directory/relative).write_bytes(archive.extractfile(entry).read())
                    seen.add(relative)
            if not template_seen:
                raise ValueError('Athena codec kernel template missing from rootfs')
            athena_codec_bundle.verify(directory)
            result.update({name: (directory/name).read_bytes() for name in codec_lock['codec_source_lock']['helpers']})
    return result


def platform_provenance(target: str) -> dict:
    """Describe the exact external platform and toolchain behind an artifact."""
    platform_lock.verify(PLATFORM_LOCK, require_builder_digest=True)
    platforms = PLATFORM_LOCK["platforms"]
    builder = PLATFORM_LOCK["builder"]
    if target in LINUX_DOT_TARGETS:
        audio = json.loads(DOT_AUDIO_LOCK_PATH.read_text())
        source = {
            "repository": PLATFORM_REPOSITORY,
            "commit": platforms["dot"]["commit"],
            "release": audio["profile"],
            "base_kernel_release": platforms["dot"]["kernel_release"],
            "base_release": platforms["dot"]["release"],
            "audio_overlay_lock_sha256": digest(DOT_AUDIO_LOCK_PATH),
            "kernel_image_sha256": audio["targets"][target]["kernel_image_sha256"],
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
            "release": platforms["athena"]["kernel_release"],
            "base_release": platforms["athena"]["release"],
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
    provenance = {
        "lock_sha256": digest(platform_lock.LOCK_PATH),
        "builder": f"{builder['image']}@{builder['digest']}",
        "source": source,
    }
    if target in ("checkers", "cronos", "crown", "athena", "rook"):
        lock = hardware_bundle.load_lock(target)
        provenance["hardware_helpers"] = {"bundle_id": lock["bundle_id"],
                                          "source_lock_sha256": hardware_bundle.lock_digest(lock)}
        inputs = release_hardware_inputs.load()
        provenance["hardware_helpers"].update({
            "source": inputs["source"], "archive": inputs["inputs"][target],
        })
        if target == "athena":
            provenance["athena_codecs"] = inputs["inputs"]["athena-codecs"]
            provenance["athena_mesa_manifest_sha256"] = athena_mesa.MANIFEST_SHA256
    return provenance


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


def require_asset(path: Path, expected_sha256: str, expected_size: int) -> None:
    if (not path.is_file() or path.stat().st_size != expected_size
            or digest(path) != expected_sha256):
        raise SystemExit(f"required release asset failed verification: {path}")


def copy(source: Path, destination: Path, mode: int) -> None:
    if not source.is_file():
        raise SystemExit(f"required release input is missing: {source}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    destination.chmod(mode)


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


def deterministic_mixed_tar(entries: dict[str, bytes | Path], destination: Path) -> None:
    """Write stable flat archives without loading large rootfs images in memory."""
    with destination.open("wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0, compresslevel=9) as gz:
            with tarfile.open(fileobj=gz, mode="w", format=tarfile.PAX_FORMAT) as archive:
                for name, source in sorted(entries.items()):
                    info = tarfile.TarInfo(name)
                    info.mode = 0o644
                    info.uid = info.gid = 0
                    info.uname = info.gname = "root"
                    info.mtime = 0
                    if isinstance(source, Path):
                        info.size = source.stat().st_size
                        with source.open("rb") as stream:
                            archive.addfile(info, stream)
                    else:
                        info.size = len(source)
                        archive.addfile(info, io.BytesIO(source))


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


def minimum_system_version(target: str, definition: dict, release_version: str) -> str:
    """Return the oldest Tater Linux release that satisfies this app release."""
    value = str(definition.get("minimum_system_version") or release_version).strip()
    pattern = r"v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?"
    if not re.fullmatch(pattern, value):
        raise SystemExit(f"target {target!r} has an invalid minimum_system_version")

    def core(version: str) -> tuple[int, int, int]:
        match = re.match(r"v([0-9]+\.[0-9]+\.[0-9]+)", version)
        if match is None:
            raise SystemExit(f"release version {version!r} is invalid")
        major, minor, patch = match.group(1).split(".")
        return int(major), int(minor), int(patch)

    if core(value) > core(release_version):
        raise SystemExit(
            f"target {target!r} minimum_system_version {value} is newer than {release_version}"
        )
    return value


def build(version: str, target: str, output: Path) -> list[Path]:
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?", version):
        raise SystemExit("version must look like v0.1.0")
    targets = json.loads(TARGETS.read_text())["targets"]
    if target not in targets:
        raise SystemExit(f"unknown target {target!r}")
    if target in LINUX_DOT_TARGETS:
        require_dot_audio_platform(target)
    elif target == "checkers":
        require_checkers_hardware_platform()
    elif target == "cronos":
        require_cronos_hardware_platform()
    elif target == "crown":
        require_crown_hardware_platform()
    elif target == "rook":
        require_rook_hardware_platform()
    elif target == "athena":
        require_athena_hardware_platform()
    required_system_version = minimum_system_version(target, targets[target], version)
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
            ffmpeg = build_dir / "tater-ffmpeg"
            ffmpeg_license = build_dir / "FFMPEG-LGPL-2.1.txt"
            required_sources.extend((show, camera, ffmpeg, ffmpeg_license))
        for source in required_sources:
            if not source.is_file():
                raise SystemExit(f"required release input is missing: {source}")
        verify_rootfs_ota_platform(rootfs_source, target)
        if target in LINUX_DOT_TARGETS:
            require_elf(server, ELFCLASS32, EM_ARM, f"{target} Tater Linux daemon")

        def write_app_ota(path: Path, app_files: dict[str, bytes]) -> None:
            app_manifest = {
                "schema": OTA_SCHEMA,
                "platform": ota_platform(target),
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
            executables = {name for name in app_files if name.startswith("tater-") and name != hardware_bundle.MANIFEST}
            deterministic_files_tar(app_entries, path, executables)

        hardware_files = {}
        if target in ("checkers", "cronos", "crown", "athena", "rook"):
            # The rootfs is the source of truth; never package a different
            # ad-hoc /data helper into the matching application release.
            hardware_files = hardware_files_from_rootfs(rootfs_source, target)
        ota = output / f"{stem}-ota.tar.gz"
        if target in LINUX_DOT_TARGETS:
            write_app_ota(ota, {"tater-echo": server.read_bytes()})
        else:
            bridge_files = {
                "tater-echo": server.read_bytes(),
                "tater-show": show.read_bytes(),
            }
            ota_bridge = output / f"{stem}-ota-bridge.tar.gz"
            write_app_ota(ota_bridge, bridge_files)
            artifacts["ota_bridge"] = ota_bridge
            write_app_ota(ota, {
                **bridge_files,
                "FFMPEG-LGPL-2.1.txt": ffmpeg_license.read_bytes(),
                "tater-camera": camera.read_bytes(),
                "tater-ffmpeg": ffmpeg.read_bytes(),
                **hardware_files,
            })
        artifacts["ota"] = ota

        system_ota = output / f"{stem}-system-ota.tar.gz"
        rootfs_meta = metadata(rootfs_source)
        system_manifest = {
            "schema": OTA_SCHEMA,
            "platform": ota_platform(target),
            "kind": "tater-linux-rootfs",
            "target": target,
            "base_os": "tater-linux",
            "version": version,
            "rootfs": rootfs_meta,
        }
        deterministic_mixed_tar(
            {
                "manifest.json": (
                    json.dumps(system_manifest, indent=2, sort_keys=True) + "\n"
                ).encode(),
                "rootfs.tar.gz": rootfs_source,
            },
            system_ota,
        )
        artifacts["system"] = system_ota
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
            verify_dot_audio_kernel(Path(kernel_value), target)
            try:
                dot_early_boot.require_static_arm(REPO / "device/build/dot/tater-volume-up")
            except (OSError, ValueError) as error:
                raise SystemExit(f"Dot early-recovery helper verification failed: {error}") from error
            dot_factory = REPO / "factory/dot-linux"
            inputs = {
                "install.sh": (dot_factory / "install.sh", 0o755),
                "install.py": (dot_factory / "install.py", 0o755),
                "README.md": (dot_factory / "README.md", 0o644),
                "LICENSE": (REPO / "LICENSE", 0o644),
                "NOTICE.md": (REPO / "NOTICE.md", 0o644),
                "payload/rootfs.tar.gz": (rootfs_source, 0o644),
                "payload/kernel.zImage-dtb": (Path(kernel_value), 0o644),
                "payload/tater-volume-up": (REPO / "device/build/dot/tater-volume-up", 0o755),
                "payload/rescue-apks.tar": (Path(rescue_value), 0o644),
                "payload/alpine-minirootfs.tar.gz": (Path(alpine_value), 0o644),
                "tools/techo5lib.py": (platform_source / "tools/techo5lib.py", 0o644),
                "tools/dotimage.py": (platform_source / "tools/dotimage.py", 0o644),
                "tools/early_boot.py": (REPO / "linux/dot/early_boot.py", 0o644),
                "tools/linux/tater-early-recovery": (REPO / "linux/dot/tater-early-recovery", 0o755),
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
                "checkers": REPO / "linux/checkers/inputs/tater-echo-checkers-baseline-boot.img",
                "cronos": REPO / "linux/checkers/inputs/tater-echo-cronos-baseline-boot.img",
                "crown": REPO / "linux/checkers/inputs/tater-echo-crown-baseline-boot.img",
            }[target]
            boot_source = (
                Path(configured_boot) if configured_boot
                else default_boot
            )
            if target == "checkers":
                verify_checkers_hardware_boot(boot_source)
            elif target == "cronos":
                verify_cronos_hardware_boot(boot_source)
            elif target == "crown":
                verify_hardware_boot(boot_source, "Crown", PLATFORM_LOCK["platforms"]["show"]["crown_hardware"]["kernel_image_sha256"])
            inputs = {
                "install.sh": (REPO / "factory/checkers-linux/install.sh", 0o755),
                "install.py": (REPO / "factory/checkers-linux/install.py", 0o755),
                "README.md": (
                    REPO / ({
                        "checkers": "factory/checkers-linux/README.md",
                        "cronos": "factory/cronos-linux/README.md",
                        "crown": "factory/crown-linux/README.md",
                    }[target]), 0o644),
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
                "tools/techo5/boot-logo/tater-wordmark.bin": (
                    REPO / "factory/checkers-linux/tools/techo5/boot-logo/tater-wordmark.bin", 0o644),
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
            if digest(boot_source) != athena_codec_bundle.load_lock()['kernel_template_sha256']:
                raise SystemExit('Athena factory boot does not match the codec kernel template')
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
            verify_hardware_boot(boot_source, 'Rook', QUALIFIED_ROOK_KERNEL_SHA256)
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
                "boot-logo/tater-boot-480.bin": (
                    REPO / "factory/rook-linux/boot-logo/tater-boot-480.bin", 0o644),
            }

        inputs["licenses/ECHOLOCAL-MIT.txt"] = (
            REPO / "LICENSES/ECHOLOCAL-MIT.txt", 0o644)
        inputs["licenses/SENDSPIN-MIT.txt"] = (
            REPO / "LICENSES/SENDSPIN-MIT.txt", 0o644)
        if target in ("checkers", "cronos"):
            inputs["provision_hardware.py"] = (REPO / "factory/provision_hardware.py", 0o755)
        inputs["to_recovery.py"] = (REPO / "factory/to_recovery.py", 0o755)
        inputs["recovery_install.py"] = (REPO / "factory/recovery_install.py", 0o755)
        inputs["tater_settings.py"] = (REPO / "factory/tater_settings.py", 0o644)
        # TWRP remains the lower-level escape hatch.  Tater Recovery is the
        # normal user path, but do not remove the tool needed when the Linux
        # rescue image itself is what needs repair.
        inputs["to_twrp.py"] = (REPO / "factory/to_twrp.py", 0o755)

        for relative, (source, mode) in inputs.items():
            copy(source, factory / relative, mode)
        if target in LINUX_SHOW_TARGETS:
            require_asset(
                factory / "tools/techo5/boot-logo/tater-wordmark.bin",
                SHOW_BOOT_LOGO_SHA256,
                SHOW_BOOT_LOGO_SIZE,
            )
        if target == "rook":
            require_asset(
                factory / "boot-logo/tater-boot-480.bin",
                ROOK_BOOT_LOGO_SHA256,
                ROOK_BOOT_LOGO_SIZE,
            )
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
            # The unit-specific installer inserts the hook later. Fail the
            # release build now if its pinned init contract has changed.
            dot_early_boot.patch_init(init_text.encode())
            slotctl = factory / "tools/linux/rootfs/usr/local/sbin/slotctl"
            slotctl.write_text(slotctl.read_text().replace(
                "usr/local/bin/echod", "usr/local/bin/tater-echo"))
        if target in LINUX_SHOW_TARGETS:
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
            "ota_platform": ota_platform(target),
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
    target_manifest.pop("minimum_system_version", None)
    target_manifest["ota_policy"] = {
        "package_schema": OTA_SCHEMA,
        "platform": ota_platform(target),
        "usb_upgrade_required_if_missing": True,
    }
    if target in LINUX_SHOW_TARGETS | LINUX_ATHENA_TARGETS | {"rook"}:
        target_manifest["ota_migrations"] = [{
            "id": "linux-app-components-v3",
            "required_capability": "ota_bundle_components_version",
            "minimum_capability_version": 3,
            "bridge_artifact": "ota_bridge",
            "final_artifact": "ota",
        }]
    target_manifest["system_ota"] = {
        "required_capability": "system_ota_version",
        "minimum_capability_version": 1,
        "minimum_system_version": required_system_version,
        "boot_policy": "inactive-rootfs-trial",
    }
    if target in LINUX_DOT_TARGETS:
        # System OTA v1 deliberately retains the installed recovery kernel.
        # Never describe the new filter as delivered by that rootfs-only path.
        target_manifest["kernel_update"] = {
            "profile": "tater-adc3101-hpf80-v1",
            "delivery": "target-specific-factory-or-usb-recovery",
            "included_in_application_ota": False,
            "included_in_system_ota": False,
        }
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
