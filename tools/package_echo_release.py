#!/usr/bin/env python3
"""Create target-scoped Tater Echo factory and OTA release artifacts."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile


REPO = Path(__file__).resolve().parents[1]
TARGETS = REPO / "targets" / "targets.json"


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

def build(version: str, target: str, output: Path) -> list[Path]:
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?", version):
        raise SystemExit("version must look like v0.1.0")
    targets = json.loads(TARGETS.read_text())["targets"]
    if target not in targets:
        raise SystemExit(f"unknown target {target!r}")

    output.mkdir(parents=True, exist_ok=True)
    stem = f"tater-echo-{target}-{version}"
    artifacts: dict[str, Path] = {}
    if target == "biscuit":
        ota = output / f"{stem}-ota.bin"
        copy(REPO / "device/build/server", ota, 0o755)
        artifacts["ota"] = ota
    elif target == "checkers":
        configured_rootfs = os.getenv("TATER_CHECKERS_ROOTFS", "").strip()
        rootfs_source = (
            Path(configured_rootfs) if configured_rootfs
            else REPO / "device/build" / f"tater-checkers-rootfs-{version}.tar.gz"
        )
        server = REPO / "device/build/tater-echo"
        show = REPO / "device/build/tater-show-linux"
        for source in (rootfs_source, server, show):
            if not source.is_file():
                raise SystemExit(f"required release input is missing: {source}")
        ota = output / f"{stem}-ota.tar.gz"
        app_files = {"tater-echo": server.read_bytes(), "tater-show": show.read_bytes()}
        app_manifest = {
            "schema": 1,
            "target": "checkers",
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
        deterministic_files_tar(app_entries, ota, set(app_files))
        artifacts["ota"] = ota
    else:
        raise SystemExit(f"factory packaging is not implemented for {target!r}")

    with tempfile.TemporaryDirectory(prefix="tater-echo-package-") as temporary:
        factory = Path(temporary) / f"{stem}-factory"
        factory.mkdir()
        if target == "biscuit":
            inputs = {
                "install.sh": (REPO / "factory/biscuit/install.sh", 0o755),
                "install.py": (REPO / "factory/biscuit/install.py", 0o755),
                "README.md": (REPO / "factory/biscuit/README.md", 0o644),
                "LICENSE": (REPO / "LICENSE", 0o644),
                "NOTICE.md": (REPO / "NOTICE.md", 0o644),
                "tools/tater_emos_build.py": (
                    REPO / "factory/biscuit/tools/tater_emos_build.py", 0o644),
                "payload/server": (REPO / "device/build/server", 0o755),
                "payload/start_server.sh": (
                    REPO / "factory/biscuit/payload/start_server.sh", 0o755),
                "payload/libtater_microwakeword.so": (
                    REPO / "device/build/microwakeword-android/libtater_microwakeword.so", 0o755),
                "payload/hey_tater.tflite": (
                    REPO / "device/build/microwakeword-testdata/hey_tater.tflite", 0o644),
                "payload/hey_tater.json": (
                    REPO / "device/internal/wakeword/microwakeword/models/hey_tater.json", 0o644),
                "payload/stop.tflite": (
                    REPO / "device/build/microwakeword-testdata/stop.tflite", 0o644),
                "payload/stop.json": (
                    REPO / "device/internal/wakeword/microwakeword/models/stop.json", 0o644),
                "payload/init32": (REPO / "emos/build/init32", 0o755),
                "payload/wpa_supplicant": (REPO / "emos/build/wpa/wpa_supplicant", 0o755),
                "payload/wpa_cli": (REPO / "emos/build/wpa/wpa_cli", 0o755),
                "payload/em-wifi": (REPO / "emos/build/wpa/em-wifi", 0o755),
                "payload/busybox": (REPO / "emos/build/bb/busybox", 0o755),
                "sources/build-busybox.sh": (REPO / "emos/tools/build-busybox.sh", 0o755),
            }
            busybox_sources = list((REPO / "emos/build/bb").glob("busybox-*.tar.bz2"))
            if len(busybox_sources) != 1:
                raise SystemExit("expected exactly one BusyBox source tarball")
            inputs[f"sources/{busybox_sources[0].name}"] = (busybox_sources[0], 0o644)
            inputs["sources/busybox-LICENSE"] = (REPO / "emos/build/bb/busybox-LICENSE", 0o644)
        else:
            configured_boot = os.getenv("TATER_CHECKERS_BOOT_IMAGE", "").strip()
            boot_source = (
                Path(configured_boot) if configured_boot
                else REPO / "linux/checkers/inputs/techo5-boot-checkers-v0.7.16.img"
            )
            inputs = {
                "install.sh": (REPO / "factory/checkers-linux/install.sh", 0o755),
                "install.py": (REPO / "factory/checkers-linux/install.py", 0o755),
                "README.md": (REPO / "factory/checkers-linux/README.md", 0o644),
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

        for relative, (source, mode) in inputs.items():
            copy(source, factory / relative, mode)
        if target == "checkers":
            branded_boot = factory / "payload/boot.img"
            subprocess.run([
                sys.executable,
                str(REPO / "linux/checkers/brand_boot.py"),
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
        if target == "checkers":
            bundle_manifest["base_os"] = "tater-linux"
        write_json(factory / "bundle-manifest.json", bundle_manifest)
        archive = output / f"{stem}-factory.tar.gz"
        deterministic_tar(factory, archive)
        artifacts["factory"] = archive

    release_manifest = output / "firmware-manifest.json"
    write_json(release_manifest, {
        "schema": 1,
        "product": "Tater Echo Firmware",
        "version": version,
        "targets": {
            target: {
                **targets[target],
                "artifacts": {
                    kind: {"name": path.name, **metadata(path)}
                    for kind, path in artifacts.items()
                },
            }
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
