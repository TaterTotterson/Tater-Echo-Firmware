#!/usr/bin/env python3
"""Publish/consume allowlisted open helpers, never owner-local runtime trees.

The input catalog is separate from physical release qualification. A valid
download does not authorize a firmware release or bypass a hardware-test gate.
"""
from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import re
import sys
import tarfile
import tempfile
from urllib import request

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from tools import athena_codec_bundle, hardware_bundle  # noqa: E402

LOCK = ROOT / "platforms/hardware-release-inputs.json"
MAX_ARCHIVE = 25 * 1024 * 1024
MAX_FILE = 8 * 1024 * 1024
MAX_UNPACKED = 64 * 1024 * 1024
TARGETS = ("checkers", "cronos", "crown", "rook", "athena")


def names(name: str) -> set[str]:
    if name == "athena-codecs":
        return {*athena_codec_bundle.identities(athena_codec_bundle.load_lock()), athena_codec_bundle.MANIFEST}
    if name not in TARGETS:
        raise ValueError("unknown public hardware input")
    return {*hardware_bundle.helper_names(name), hardware_bundle.MANIFEST}


def verify(directory: Path, name: str) -> dict:
    if name == "athena-codecs":
        return athena_codec_bundle.verify(directory)
    names(name)
    return hardware_bundle.verify(directory, target=name)


def load() -> dict:
    lock = json.loads(LOCK.read_text())
    if lock.get("schema") != 1 or set(lock.get("inputs", {})) != {*TARGETS, "athena-codecs"}:
        raise ValueError("incomplete public hardware input catalog")
    if lock.get("source", {}).get("repository") != "https://github.com/TaterTotterson/Tater-Echo-Hardware":
        raise ValueError("unexpected hardware source repository")
    if not re.fullmatch(r"[0-9a-f]{40}", str(lock["source"].get("commit", ""))):
        raise ValueError("hardware source must be commit-pinned")
    for name, asset in lock["inputs"].items():
        names(name)
        if (not re.fullmatch(r"[0-9a-f]{64}", str(asset.get("sha256", "")))
                or not isinstance(asset.get("size"), int) or not 0 < asset["size"] <= MAX_ARCHIVE
                or not str(asset.get("url", "")).startswith(
                    "https://github.com/TaterTotterson/Tater-Echo-Hardware/releases/download/")):
            raise ValueError("public input is not an immutable, bounded release asset: " + name)
    return lock


def pack(directory: Path, output: Path, name: str) -> dict:
    verify(directory, name)
    with output.open("xb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                for filename in sorted(names(name)):
                    data = (directory / filename).read_bytes()
                    entry = tarfile.TarInfo(filename)
                    entry.size = len(data)
                    entry.mode = 0o755 if filename.startswith("tater-hw-") else 0o644
                    archive.addfile(entry, io.BytesIO(data))
    return {"sha256": hashlib.sha256(output.read_bytes()).hexdigest(), "size": output.stat().st_size}


def unpack(data: bytes, output: Path, name: str) -> None:
    if output.exists() or output.is_symlink():
        raise ValueError("refusing to merge or overwrite public input output")
    if len(data) > MAX_ARCHIVE:
        raise ValueError("public input exceeds its archive boundary")
    # Bound the tar stream itself, including PAX metadata, before tarfile can
    # allocate memory for any untrusted extended header.
    with gzip.GzipFile(fileobj=io.BytesIO(data), mode="rb") as compressed:
        contents = compressed.read(MAX_UNPACKED + 1)
    if len(contents) > MAX_UNPACKED:
        raise ValueError("public input exceeds its decompressed boundary")
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".public-hardware-", dir=output.parent) as temporary:
        stage = Path(temporary) / "verified"
        stage.mkdir()
        seen = set()
        with tarfile.open(fileobj=io.BytesIO(contents), mode="r|") as archive:
            for entry in archive:
                if (entry.name not in names(name) or entry.name in seen or not entry.isfile()
                        or not 0 < entry.size <= MAX_FILE):
                    raise ValueError("unexpected/unsafe public input archive member")
                stream = archive.extractfile(entry)
                if stream is None:
                    raise ValueError("unreadable public input member")
                path = stage / entry.name
                path.write_bytes(stream.read())
                path.chmod(0o755 if entry.name.startswith("tater-hw-") else 0o644)
                seen.add(entry.name)
        verify(stage, name)
        stage.rename(output)


def fetch(name: str, output: Path) -> None:
    asset = load()["inputs"][name]
    with request.urlopen(asset["url"], timeout=60) as response:
        if not response.geturl().startswith("https://"):
            raise ValueError("public input redirect must remain HTTPS")
        data = response.read(asset["size"] + 1)
    if len(data) != asset["size"] or hashlib.sha256(data).hexdigest() != asset["sha256"]:
        raise ValueError("public input archive size/SHA-256 differs from committed catalog")
    unpack(data, output, name)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("verify-lock")
    for command in ("pack", "fetch"):
        child = commands.add_parser(command)
        child.add_argument("--name", choices=(*TARGETS, "athena-codecs"), required=True)
        child.add_argument("--output", type=Path, required=True)
        if command == "pack":
            child.add_argument("directory", type=Path)
    args = parser.parse_args()
    try:
        if args.command == "verify-lock":
            load()
        elif args.command == "pack":
            print(json.dumps(pack(args.directory, args.output, args.name), sort_keys=True))
        else:
            fetch(args.name, args.output)
    except (OSError, ValueError, KeyError, tarfile.TarError) as error:
        parser.exit(1, f"refusing: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
