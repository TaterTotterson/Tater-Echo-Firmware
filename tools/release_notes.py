#!/usr/bin/env python3
from __future__ import annotations

import re
import sys
from pathlib import Path


REPO = Path(__file__).resolve().parents[1]
CHANGELOG = REPO / "CHANGELOG.md"
VERSION_HEADING = re.compile(r"^##\s+(v[0-9]+\.[0-9]+\.[0-9]+(?:[+-][A-Za-z0-9.-]+)?)\b")


def extract(version: str, changelog: str) -> str:
    wanted = version.strip()
    if not wanted.startswith("v"):
        wanted = f"v{wanted}"

    selected: list[str] = []
    collecting = False
    for line in changelog.splitlines():
        match = VERSION_HEADING.match(line)
        if match:
            if collecting:
                break
            collecting = match.group(1) == wanted
        if collecting:
            selected.append(line)

    if not selected:
        raise ValueError(f"CHANGELOG.md has no release notes for {wanted}")
    return "\n".join(selected).strip() + "\n"


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        raise SystemExit("usage: release_notes.py vX.Y.Z")
    try:
        notes = extract(argv[1], CHANGELOG.read_text(encoding="utf-8"))
    except ValueError as exc:
        raise SystemExit(str(exc)) from exc
    sys.stdout.write(notes)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
