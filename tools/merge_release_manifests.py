#!/usr/bin/env python3
"""Merge independently built target artifacts into one release manifest."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import shutil


def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def load(path: Path) -> dict:
    try:
        value = json.loads(path.read_text())
    except (OSError, ValueError) as exc:
        raise SystemExit(f"cannot read target manifest {path}: {exc}") from exc
    if value.get("schema") != 1 or value.get("product") != "Tater Echo Firmware":
        raise SystemExit(f"unsupported target manifest: {path}")
    if not isinstance(value.get("targets"), dict) or not value["targets"]:
        raise SystemExit(f"target manifest has no targets: {path}")
    return value


def merge(version: str, inputs: list[Path], output: Path) -> list[Path]:
    output.mkdir(parents=True, exist_ok=True)
    targets: dict[str, dict] = {}
    copied: dict[str, Path] = {}
    for source_manifest in inputs:
        manifest = load(source_manifest)
        if manifest.get("version") != version:
            raise SystemExit(
                f"version mismatch in {source_manifest}: {manifest.get('version')!r}, expected {version!r}")
        for target, definition in manifest["targets"].items():
            if target in targets:
                raise SystemExit(f"duplicate target {target!r}")
            artifacts = definition.get("artifacts")
            if not isinstance(artifacts, dict) or not artifacts:
                raise SystemExit(f"target {target!r} has no artifacts")
            for kind, metadata in artifacts.items():
                name = metadata.get("name", "")
                if not name or Path(name).name != name:
                    raise SystemExit(f"target {target!r} has an unsafe {kind} artifact name")
                source = source_manifest.parent / name
                if not source.is_file():
                    raise SystemExit(f"target {target!r} artifact is missing: {source}")
                if source.stat().st_size != int(metadata.get("size", -1)):
                    raise SystemExit(f"target {target!r} artifact has the wrong size: {name}")
                if digest(source) != metadata.get("sha256"):
                    raise SystemExit(f"target {target!r} artifact failed SHA-256: {name}")
                if name in copied and digest(copied[name]) != digest(source):
                    raise SystemExit(f"two targets produced different artifacts named {name!r}")
                destination = output / name
                shutil.copyfile(source, destination)
                copied[name] = destination
            targets[target] = definition

    release_manifest = output / "firmware-manifest.json"
    release_manifest.write_text(json.dumps({
        "schema": 1,
        "product": "Tater Echo Firmware",
        "version": version,
        "targets": targets,
    }, indent=2, sort_keys=True) + "\n")
    sums = output / "SHA256SUMS"
    files = [*sorted(copied.values(), key=lambda path: path.name), release_manifest]
    sums.write_text("".join(f"{digest(path)}  {path.name}\n" for path in files))
    return [*files, sums]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("inputs", nargs="+", type=Path)
    args = parser.parse_args()
    for path in merge(args.version, args.inputs, args.output):
        print(path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
