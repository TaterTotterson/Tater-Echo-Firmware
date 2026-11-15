#!/usr/bin/env python3
"""Verify/package only source-built public runtime compatibility libraries."""
from __future__ import annotations
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import tarfile
from urllib import request

ROOT = Path(__file__).resolve().parents[1]
CATALOG = ROOT / "platforms/runtime-compat-input.json"
EXPECTED = json.loads((ROOT / "factory/hardware-runtime-sources.json").read_text())["compat_files"]
METADATA = {"runtime-compat.json", "runtime-compat-source.tar.gz", "AOSP-SYNC-NOTICE"}
MAX_ARCHIVE = 2 * 1024 * 1024


def verify(directory):
    if directory.is_symlink() or not directory.is_dir() or {p.name for p in directory.iterdir()} != {*EXPECTED, *METADATA}:
        raise ValueError("public compatibility archive has an unexpected file inventory")
    for name in (*EXPECTED, *METADATA):
        path = directory / name
        if path.is_symlink() or not path.is_file() or not 0 < path.stat().st_size <= MAX_ARCHIVE:
            raise ValueError("unsafe compatibility archive member")
    for name, expected in EXPECTED.items():
        data = (directory / name).read_bytes()
        if hashlib.sha256(data).hexdigest() != expected or data[:6] != b"\x7fELF\x01\x01" or data[18:20] != b"\x28\x00":
            raise ValueError("compatibility library is not the pinned ARM32 output: " + name)
    manifest = json.loads((directory / "runtime-compat.json").read_text())
    if set(manifest) != {"schema", "files", "source_lock_sha256"} or manifest["schema"] != 1 or manifest["files"] != EXPECTED or not re.fullmatch(r"[0-9a-f]{64}", manifest["source_lock_sha256"]):
        raise ValueError("compatibility build manifest differs from the pinned inventory")
    # Publish corresponding original/upstream source alongside every binary.
    with gzip.GzipFile(fileobj=io.BytesIO((directory / "runtime-compat-source.tar.gz").read_bytes())) as compressed:
        raw = compressed.read(MAX_ARCHIVE + 1)
    if len(raw) > MAX_ARCHIVE:
        raise ValueError("compatibility source exceeds boundary")
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
        members = archive.getmembers()
        if len(members) > 100 or any(not e.isfile() or e.size > MAX_ARCHIVE for e in members) or len({e.name for e in members}) != len(members):
            raise ValueError("unsafe compatibility source archive")
        lock = archive.extractfile("platforms/runtime-compat-lock.json").read()
        if hashlib.sha256(lock).hexdigest() != manifest["source_lock_sha256"]:
            raise ValueError("compatibility source lock differs from build receipt")
        source = json.loads(lock)
        expected_sources = {**source["sources"], **{"aosp-sync/" + n: h for n, h in source["sync_sources"].items()}}
        allowed = {*expected_sources, "LICENSE", "LICENSES/MIT.txt", "NOTICE.md", "platforms/runtime-compat-lock.json"}
        if source["files"] != EXPECTED or {e.name for e in members} != allowed:
            raise ValueError("compatibility source has missing or unexpected inputs")
        for name, expected in expected_sources.items():
            if hashlib.sha256(archive.extractfile(name).read()).hexdigest() != expected:
                raise ValueError("compatibility corresponding source differs from lock")
    return manifest


def pack(directory, output):
    verify(directory)
    with output.open("xb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                for name in sorted({*EXPECTED, *METADATA}):
                    data = (directory / name).read_bytes()
                    entry = tarfile.TarInfo(name)
                    entry.size, entry.mode = len(data), 0o644
                    archive.addfile(entry, io.BytesIO(data))
    return {"sha256": hashlib.sha256(output.read_bytes()).hexdigest(), "size": output.stat().st_size}


def unpack(data, output):
    if output.exists() or output.is_symlink() or len(data) > MAX_ARCHIVE:
        raise ValueError("unsafe or existing compatibility destination")
    with gzip.GzipFile(fileobj=io.BytesIO(data)) as compressed:
        raw = compressed.read(MAX_ARCHIVE + 1)
    if len(raw) > MAX_ARCHIVE:
        raise ValueError("compatibility archive exceeds decompressed boundary")
    # No tar extraction API: copy only bounded, exact, ordinary leaf files.
    import tempfile
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="compat-", dir=output.parent) as temporary:
        stage = Path(temporary) / "verified"
        stage.mkdir()
        seen = set()
        with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
            for entry in archive:
                if entry.name not in {*EXPECTED, *METADATA} or entry.name in seen or not entry.isfile() or not 0 < entry.size <= MAX_ARCHIVE:
                    raise ValueError("unexpected public compatibility archive member")
                (stage / entry.name).write_bytes(archive.extractfile(entry).read())
                seen.add(entry.name)
        verify(stage)
        stage.rename(output)


def fetch(output):
    item = json.loads(CATALOG.read_text())
    if item.get("schema") != 1 or not re.fullmatch(r"[0-9a-f]{40}", item.get("source_commit", "")):
        raise ValueError("compatibility source is not commit-pinned")
    url = item["url"]
    if not url.startswith("https://github.com/TaterTotterson/Tater-Echo-Hardware/releases/download/") or not 0 < item["size"] <= MAX_ARCHIVE:
        raise ValueError("compatibility input is not a bounded release asset")
    with request.urlopen(url, timeout=60) as response:
        if not response.geturl().startswith("https://"):
            raise ValueError("compatibility redirect left HTTPS")
        data = response.read(item["size"] + 1)
    if len(data) != item["size"] or hashlib.sha256(data).hexdigest() != item["sha256"]:
        raise ValueError("compatibility archive failed size/SHA-256 verification")
    unpack(data, output)


def factory_inputs(temporary):
    directory = os.getenv("TATER_RUNTIME_COMPAT_DIR", "").strip()
    if directory:
        path = Path(directory)
    else:
        path = temporary / "runtime-compat"
        fetch(path)
    verify(path)
    return {"runtime-compat/" + name: (path / name, 0o644) for name in (*EXPECTED, *METADATA)}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["verify", "pack", "fetch"])
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        if args.command == "verify":
            print(json.dumps(verify(args.directory), sort_keys=True))
        elif args.command == "pack":
            print(json.dumps(pack(args.directory, args.output), sort_keys=True))
        else:
            fetch(args.output)
    except (OSError, ValueError, KeyError, tarfile.TarError) as error:
        parser.exit(1, "refusing: " + str(error) + "\n")
