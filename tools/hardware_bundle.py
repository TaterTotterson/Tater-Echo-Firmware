#!/usr/bin/env python3
"""Verify/build the public Checkers hardware helper set, never vendor binaries."""
from __future__ import annotations

import argparse
import hashlib
import gzip
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
from urllib import request as urlrequest

ROOT = Path(__file__).resolve().parents[1]
LOCK = ROOT / "platforms/checkers-hardware-lock.json"
HELPERS = ("tater-hw-gpu", "tater-hw-vdec", "tater-hw-venc")
MANIFEST = "tater-hardware.json"

def helper_names(target: str) -> tuple[str, ...]:
    if target == "athena":
        return ("tater-hw-gpu",)
    if target in ("checkers", "cronos", "crown", "rook"):
        return HELPERS
    raise ValueError("unqualified hardware target")


def digest(path: Path) -> str:
    result = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(chunk)
    return result.hexdigest()


def lock_digest(lock: dict) -> str:
    return hashlib.sha256(json.dumps(lock, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def load_lock(target: str = "checkers") -> dict:
    names = helper_names(target)
    path = LOCK if target == "checkers" else ROOT / f"platforms/{target}-hardware-lock.json"
    lock = json.loads(path.read_text())
    if lock.get("schema") != 1 or lock.get("target") != target or set(lock.get("helpers", {})) != set(names):
        raise ValueError("invalid hardware source lock")
    return lock


def verify(directory: Path, *, lock: dict | None = None, target: str = "checkers") -> dict:
    lock = load_lock(target) if lock is None else lock
    names = helper_names(lock["target"])
    # An exact file allowlist prevents shipping link libraries, device dumps,
    # or a private runtime accidentally placed beside the public helpers.
    if directory.is_symlink() or not directory.is_dir() or {path.name for path in directory.iterdir()} != {*names, MANIFEST}:
        raise ValueError("hardware bundle must contain exactly its target's public helpers and manifest")
    for name in (*names, MANIFEST):
        path = directory / name
        if path.is_symlink() or not path.is_file() or path.stat().st_size > 8 * 1024 * 1024:
            raise ValueError("hardware bundle contains an unsafe or oversized file")
    manifest_path = directory / MANIFEST
    if manifest_path.stat().st_size > 16 * 1024:
        raise ValueError("hardware manifest exceeds its boundary")
    expected_manifest = {"schema": 1, "target": lock["target"], "bundle_id": lock["bundle_id"],
                         "files": lock["helpers"], "source_lock_sha256": lock_digest(lock)}
    manifest = json.loads(manifest_path.read_text())
    if manifest != expected_manifest:
        raise ValueError("hardware manifest does not match the firmware source lock")
    for name in names:
        path = directory / name
        if digest(path) != lock["helpers"][name]:
            raise ValueError(f"hardware helper hash mismatch: {name}")
        with path.open("rb") as source:
            header = source.read(20)
        if len(header) != 20 or header[:6] != b"\x7fELF\x01\x01" or header[18:20] != b"\x28\x00":
            raise ValueError(f"hardware helper is not little-endian ARM32 ELF: {name}")
    return manifest


def build(source: Path, ndk: Path, headers: Path, libraries: Path, output: Path, *, target: str = "checkers") -> None:
    lock = load_lock(target)
    for relative, expected in lock["sources"].items():
        path = source / relative
        if path.is_symlink() or not path.is_file() or digest(path) != expected:
            raise ValueError(f"research source does not match firmware lock: {relative}")
    lock_path = LOCK if target == "checkers" else ROOT / f"platforms/{target}-hardware-lock.json"
    if target == "athena":
        subprocess.run([sys.executable, str(source / "scripts/build_athena_gpu.py"),
                        "--lock", str(lock_path), "--image", lock["builder_image_id"], "--output", str(output)], check=True)
        verify(output, target=target)
        return
    if ndk is None or headers is None or libraries is None:
        raise ValueError("Android helper build requires NDK, headers and link-libraries")
    subprocess.run([sys.executable, str(source / "scripts/build_tater_hardware.py"),
                    "--lock", str(lock_path), "--ndk", str(ndk), "--headers", str(headers),
                    "--link-libraries", str(libraries), "--output", str(output)], check=True)
    verify(output, lock=lock, target=target)


def pack(directory: Path, output: Path, *, target: str = "checkers") -> None:
    verify(directory, target=target)
    with output.open("xb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as zipped:
            with tarfile.open(fileobj=zipped, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                for name in sorted((*helper_names(target), MANIFEST)):
                    data = (directory / name).read_bytes()
                    info = tarfile.TarInfo(name)
                    info.size, info.mode = len(data), 0o644 if name == MANIFEST else 0o755
                    archive.addfile(info, io.BytesIO(data))


def unpack(source, output: Path, *, target: str = "checkers") -> None:
    if output.exists() or output.is_symlink():
        raise ValueError("hardware output exists; refusing to merge or overwrite it")
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".hardware-", dir=output.parent) as temporary:
        stage = Path(temporary) / "public"
        stage.mkdir()
        with tarfile.open(fileobj=source, mode="r|gz") as archive:
            seen = set()
            for member in archive:
                if member.name not in (*helper_names(target), MANIFEST) or member.name in seen or not member.isfile() or not 0 < member.size <= 8 * 1024 * 1024:
                    raise ValueError("public hardware archive contains an unexpected/unsafe entry")
                stream = archive.extractfile(member)
                if stream is None:
                    raise ValueError("public hardware archive entry is unreadable")
                destination = stage / member.name
                destination.write_bytes(stream.read())
                destination.chmod(0o644 if member.name == MANIFEST else 0o755)
                seen.add(member.name)
        verify(stage, target=target)
        stage.rename(output)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    check = commands.add_parser("verify")
    check.add_argument("directory", type=Path)
    compile_parser = commands.add_parser("build")
    for name in ("source", "ndk", "headers", "link-libraries", "output"):
        compile_parser.add_argument("--" + name, required=name in ("source", "output"), type=Path)
    pack_parser = commands.add_parser("pack")
    pack_parser.add_argument("directory", type=Path)
    pack_parser.add_argument("--output", required=True, type=Path)
    fetch_parser = commands.add_parser("fetch")
    fetch_parser.add_argument("--url", required=True)
    fetch_parser.add_argument("--output", required=True, type=Path)
    for command_parser in (check, compile_parser, pack_parser, fetch_parser):
        command_parser.add_argument("--target", choices=("checkers", "cronos", "crown", "athena", "rook"), default="checkers")
    arguments = parser.parse_args()
    try:
        if arguments.command == "verify":
            print(json.dumps(verify(arguments.directory, target=arguments.target), sort_keys=True))
        elif arguments.command == "build":
            build(arguments.source.resolve(), arguments.ndk.resolve() if arguments.ndk else None, arguments.headers.resolve() if arguments.headers else None, arguments.link_libraries.resolve() if arguments.link_libraries else None, arguments.output, target=arguments.target)
        elif arguments.command == "pack":
            pack(arguments.directory, arguments.output, target=arguments.target)
        else:
            if not arguments.url.startswith("https://"):
                raise ValueError("public helper input must use HTTPS")
            with urlrequest.urlopen(arguments.url, timeout=30) as response:
                if not response.geturl().startswith("https://"):
                    raise ValueError("public helper redirect must remain HTTPS")
                data = response.read(25 * 1024 * 1024 + 1)
                if len(data) > 25 * 1024 * 1024:
                    raise ValueError("public helper download exceeds its boundary")
            unpack(io.BytesIO(data), arguments.output, target=arguments.target)
    except (OSError, ValueError, subprocess.CalledProcessError, tarfile.TarError) as error:
        parser.exit(1, f"refusing: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
