#!/usr/bin/env python3
"""Read-only Rook/TWRP and backup preflight for a factory install."""

from __future__ import annotations

import argparse
from pathlib import Path
import subprocess


PARTITIONS = {"boot": 9, "recovery": 10, "system": 11}
BACKUPS = ("boot", "recovery", "system", "lk", "expdb")


def adb(serial: str, *arguments: str) -> str:
    command = ["adb", "-s", serial, *arguments]
    result = subprocess.run(command, capture_output=True, text=True, check=True)
    return result.stdout.strip()


def check(serial: str, backup_root: Path) -> None:
    states = {line.split()[0]: line.split()[1] for line in adb(serial, "devices").splitlines()[1:]
              if len(line.split()) >= 2}
    if states.get(serial) != "recovery":
        raise RuntimeError(f"{serial} is not in TWRP recovery (state={states.get(serial)!r})")
    if adb(serial, "shell", "getprop", "ro.product.device") != "rook":
        raise RuntimeError("connected USB device is not rook")

    directory = backup_root / serial
    for name, number in PARTITIONS.items():
        expected = f"/dev/block/mmcblk0p{number}"
        actual = adb(serial, "shell", "readlink", "-f", f"/dev/block/by-name/{name}")
        if actual != expected:
            raise RuntimeError(f"{name} resolves to {actual!r}, not {expected}")
        raw_size = adb(serial, "shell", "blockdev", "--getsize64", expected)
        if not raw_size.isdigit() or int(raw_size) <= 0:
            raise RuntimeError(f"cannot determine {name} partition size: {raw_size!r}")
        saved = directory / f"{name}.img"
        if not saved.is_file() or saved.stat().st_size != int(raw_size):
            raise RuntimeError(f"{saved} is missing or differs from the {raw_size}-byte partition")
        print(f"{name}: {expected}, {raw_size} bytes, matching local backup")

    for name in BACKUPS:
        saved = directory / f"{name}.img"
        if not saved.is_file() or saved.stat().st_size == 0:
            raise RuntimeError(f"required boot/recovery backup is missing: {saved}")
    print(f"Rook preflight passed for {serial}. No device writes were made.")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True, help="exact ADB serial to inspect")
    parser.add_argument("--backups", required=True, type=Path,
                        help="private directory containing <serial>/boot.img and other backups")
    arguments = parser.parse_args()
    try:
        check(arguments.serial, arguments.backups)
    except (OSError, subprocess.CalledProcessError, RuntimeError) as error:
        parser.exit(1, f"Rook preflight failed: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
