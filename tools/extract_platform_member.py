#!/usr/bin/env python3
"""Extract one hash-pinned regular file from a platform tar archive."""

from __future__ import annotations

import argparse
import hashlib
from pathlib import Path, PurePosixPath
import shutil
import tarfile


def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def canonical_member(name: str, *, archive_entry: bool = False) -> PurePosixPath | None:
    candidate = name[2:] if archive_entry and name.startswith("./") else name
    clean = PurePosixPath(candidate)
    if (not candidate or clean.is_absolute() or ".." in clean.parts or
            str(clean) != candidate):
        return None
    return clean


def extract(archive_path: Path, member_name: str, output: Path, expected_sha256: str, expected_size: int) -> None:
    clean = canonical_member(member_name)
    if clean is None:
        raise SystemExit("archive member must be a safe relative path")
    with tarfile.open(archive_path, "r:*") as archive:
        matches = [
            member
            for member in archive.getmembers()
            if canonical_member(member.name, archive_entry=True) == clean
        ]
        if len(matches) != 1 or not matches[0].isfile():
            raise SystemExit(f"platform archive does not contain one regular {clean}")
        source = archive.extractfile(matches[0])
        if source is None:
            raise SystemExit(f"platform archive member is unreadable: {clean}")
        output.parent.mkdir(parents=True, exist_ok=True)
        temporary = output.with_name(output.name + ".partial")
        try:
            with source, temporary.open("wb") as destination:
                shutil.copyfileobj(source, destination, length=1024 * 1024)
            if temporary.stat().st_size != expected_size or digest(temporary) != expected_sha256:
                raise SystemExit(f"platform archive member failed verification: {clean}")
            temporary.chmod(0o755)
            temporary.replace(output)
        finally:
            temporary.unlink(missing_ok=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("member")
    parser.add_argument("output", type=Path)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--size", type=int, required=True)
    args = parser.parse_args()
    extract(args.archive, args.member, args.output, args.sha256, args.size)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
