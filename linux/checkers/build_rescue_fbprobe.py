#!/usr/bin/env python3
"""Build TECHO5's proven rescue framebuffer renderer with Tater's setup colors.

Go's source overlay changes only the four palette literals in the pinned
renderer. The upstream checkout is never edited, and the build fails if its
source has changed unexpectedly.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile


SOURCE_SHA256 = "bf915b9f295e6c4fb5a402dc22709c464615b944f6ceea8f0825fdee8942ce72"
PALETTE = {
    # Match tater-show-linux's setup background, orange accent and cool white.
    "bg := color.RGBA{0x1c, 0x15, 0x11, 0xff}":
        "bg := color.RGBA{0x11, 0x09, 0x05, 0xff}",
    "amber := color.RGBA{0xe9, 0xa2, 0x3b, 0xff}":
        "amber := color.RGBA{0xff, 0x84, 0x30, 0xff}",
    "paper := color.RGBA{0xe8, 0xdc, 0xc8, 0xff}":
        "paper := color.RGBA{0xed, 0xf3, 0xfa, 0xff}",
    'clockScale, color.RGBA{0xe8, 0xdc, 0xc8, 0xff})':
        'clockScale, color.RGBA{0xed, 0xf3, 0xfa, 0xff})',
}


def themed_source(source: bytes) -> bytes:
    if hashlib.sha256(source).hexdigest() != SOURCE_SHA256:
        raise ValueError("fbprobe source is not the pinned TECHO5 revision")
    result = source.decode()
    for before, after in PALETTE.items():
        if result.count(before) != 1:
            raise ValueError(f"fbprobe palette marker changed: {before}")
        result = result.replace(before, after, 1)
    return result.encode()


def build(checkout: Path, output: Path) -> None:
    source = (checkout / "cmd/fbprobe/main.go").resolve(strict=True)
    themed = themed_source(source.read_bytes())
    output.parent.mkdir(parents=True, exist_ok=True)
    output = output.resolve()
    with tempfile.TemporaryDirectory(prefix="tater-rescue-fbprobe-") as directory:
        substitute = Path(directory) / "main.go"
        substitute.write_bytes(themed)
        overlay = Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(substitute)}}))
        env = os.environ.copy()
        env.update({"GOOS": "linux", "GOARCH": "arm", "GOARM": "7", "CGO_ENABLED": "0"})
        subprocess.run(
            ["go", "build", "-trimpath", "-ldflags", "-s -w", "-overlay", str(overlay),
             "-o", str(output), "./cmd/fbprobe"],
            cwd=checkout, env=env, check=True,
        )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("checkout", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    build(args.checkout, args.output)
    print(f"Tater-themed rescue renderer: {args.output}")
