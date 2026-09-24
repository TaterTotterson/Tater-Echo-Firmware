#!/usr/bin/env python3
"""Create target-scoped Tater Echo factory and OTA release artifacts."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
from pathlib import Path
import re
import shutil
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
        apk = output / f"{stem}-screen-preview.apk"
        copy(REPO / "screen/app/build/outputs/apk/debug/app-debug.apk", apk, 0o644)
        artifacts["screen_apk"] = apk
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
                "tools/em_emos_build.py": (REPO / "controller/em_emos_build.py", 0o644),
                "payload/server": (REPO / "device/build/server", 0o755),
                "payload/start_server.sh": (REPO / "controller/device_payloads/start_server.sh", 0o755),
                "payload/libtater_microwakeword.so": (
                    REPO / "device/build/microwakeword-android/libtater_microwakeword.so", 0o755),
                "payload/hey_tater.tflite": (
                    REPO / "device/build/microwakeword-testdata/hey_tater.tflite", 0o644),
                "payload/hey_tater.json": (
                    REPO / "device/internal/wakeword/microwakeword/models/hey_tater.json", 0o644),
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
            inputs = {
                "install.sh": (REPO / "factory/checkers/install.sh", 0o755),
                "install.py": (REPO / "factory/checkers/install.py", 0o755),
                "README.md": (REPO / "factory/checkers/README.md", 0o644),
                "LICENSE": (REPO / "LICENSE", 0o644),
                "NOTICE.md": (REPO / "NOTICE.md", 0o644),
                "tools/profile.sh": (REPO / "porting/profile.sh", 0o755),
                "payload/tater-show.apk": (artifacts["screen_apk"], 0o644),
            }

        for relative, (source, mode) in inputs.items():
            copy(source, factory / relative, mode)

        bundle_files = {
            str(path.relative_to(factory)): metadata(path)
            for path in sorted(factory.rglob("*")) if path.is_file()
        }
        write_json(factory / "bundle-manifest.json", {
            "schema": 1,
            "product": "Tater Echo Firmware",
            "target": target,
            "version": version,
            "files": bundle_files,
        })
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
