#!/usr/bin/env python3
"""Package a local Rook factory bundle. This command never touches a device."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import shutil
import tarfile
import tempfile


SOURCE = Path(__file__).resolve().parent
REPO = SOURCE.parents[1]


def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def inspect_rootfs(path: Path, version: str) -> None:
    with tarfile.open(path, "r:gz") as archive:
        members = archive.getnames()
        if any(member.name.lstrip("./").startswith("vendor/") and not member.isdir()
               for member in archive.getmembers()):
            raise SystemExit("Rook rootfs must not bundle the device's vendor files")
        release = next((name for name in members if name.lstrip("./") == "etc/tater-release"), None)
        board = next((name for name in members if name.lstrip("./") == "etc/techo5/device.conf"), None)
        if release is None or board is None:
            raise SystemExit("Rook rootfs has no release or board map")
        release_file = archive.extractfile(release)
        board_file = archive.extractfile(board)
        if release_file is None or board_file is None:
            raise SystemExit("Rook rootfs metadata is unreadable")
        if f"Rook {version}" not in release_file.read().decode():
            raise SystemExit("rootfs version or target differs from the requested Rook release")
        if "STORE_DEV=/dev/mmcblk0p11" not in board_file.read().decode():
            raise SystemExit("Rook rootfs does not use system partition 11")


def build(args: argparse.Namespace) -> None:
    if args.boot.open("rb").read(8) != b"ANDROID!" or args.boot.stat().st_size > 16 * 1024 * 1024:
        raise SystemExit("Rook boot image is invalid or exceeds the boot partition")
    inspect_rootfs(args.rootfs, args.version)
    tools = REPO / "factory/checkers-linux/tools/techo5"
    with tempfile.TemporaryDirectory(prefix="tater-rook-factory-") as temporary:
        directory = Path(temporary) / f"tater-echo-rook-{args.version}-factory"
        (directory / "payload").mkdir(parents=True)
        (directory / "tools/techo5").mkdir(parents=True)
        for source, relative in (
            (SOURCE / "install.py", "install.py"),
            (SOURCE / "install.sh", "install.sh"),
            (SOURCE / "README.md", "README.md"),
            (tools / "techo5lib.py", "tools/techo5/techo5lib.py"),
            (tools / "LICENSE", "tools/techo5/LICENSE"),
            (args.boot, "payload/boot.img"),
            (args.rootfs, "payload/rootfs.tar.gz"),
        ):
            shutil.copy2(source, directory / relative)
        (directory / "install.sh").chmod(0o755)
        files = {}
        for path in sorted(directory.rglob("*")):
            if path.is_file():
                files[str(path.relative_to(directory))] = {
                    "size": path.stat().st_size,
                    "sha256": digest(path),
                }
        (directory / "bundle-manifest.json").write_text(json.dumps({
            "target": "rook", "base_os": "tater-linux", "version": args.version,
            "files": files,
        }, indent=2) + "\n")
        args.output.parent.mkdir(parents=True, exist_ok=True)
        with tarfile.open(args.output, "w:gz") as archive:
            archive.add(directory, arcname=directory.name)
    print(f"Rook factory bundle: {args.output} ({args.output.stat().st_size} bytes)")
    print("This is a local factory bundle; tagged releases use tools/package_echo_release.py.")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--boot", type=Path, required=True)
    parser.add_argument("--rootfs", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, required=True)
    build(parser.parse_args())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
