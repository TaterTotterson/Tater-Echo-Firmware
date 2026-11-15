#!/usr/bin/env python3
"""Build a target-specific Dot kernel with Tater's independently authored HPF.

Uses the existing pinned platform builder and security patches, not a device
backup. Does not connect to or flash a satellite.
"""
from __future__ import annotations

import argparse
import difflib
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import zlib

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from tools import platform_lock

AUDIO = Path(__file__).with_name("audio")
LOCK = ROOT / "platforms/dot-audio-lock.json"
# Built-in kernels may discard MODULE_PARM_DESC's .modinfo section. Validate
# the real retained boot-parameter/status interfaces, then pin the exact digest.
MARKER = b"snd_soc_tlv320aic3101.tater_hpf80"


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def kernel_config(data: bytes) -> tuple[bytes, bytes]:
    # ARM zImage's gzip payload, with an optional appended device tree.
    start = data.find(b"\x1f\x8b\x08")
    if start < 0:
        raise ValueError("kernel has no gzip payload")
    decompressor = zlib.decompressobj(31)
    image = decompressor.decompress(data[start:], 64 * 1024 * 1024)
    if not decompressor.eof:
        raise ValueError("truncated or oversized kernel payload")
    offset = image.find(b"IKCFG_ST")
    if offset < 0:
        raise ValueError("kernel has no embedded config")
    decompressor = zlib.decompressobj(31)
    config = decompressor.decompress(image[offset + 8:], 1024 * 1024)
    if not decompressor.eof:
        raise ValueError("truncated or oversized kernel config")
    return image, config


def verify_kernel(filename: Path, target: str) -> dict:
    if target not in ("biscuit", "radar"):
        raise ValueError("unsupported audio kernel target")
    data = filename.read_bytes()
    if not 0 < len(data) <= 16 * 1024 * 1024:
        raise ValueError("audio kernel size is invalid")
    image, config = kernel_config(data)
    board = re.search(rb'^CONFIG_ARCH_MTK_PROJECT="([a-z0-9_]+)"$', config, re.M)
    if not board or board[1].decode() != target:
        raise ValueError("kernel board does not match target")
    if MARKER not in image or b"Tater ADC HPF Status" not in image:
        raise ValueError("kernel lacks the verified Tater audio profile")
    return {"target": target, "sha256": digest(data), "size": len(data),
            "capture_hpf_schema": 1, "capture_hpf_hz": 80}


def stage_platform(platform: Path, destination: Path) -> None:
    commit = platform_lock.load()["platforms"]["dot"]["commit"]
    actual = subprocess.check_output(["git", "-C", str(platform), "rev-parse", "HEAD"], text=True).strip()
    if actual != commit:
        raise ValueError(f"Dot platform is {actual}, expected {commit}")
    source = subprocess.check_output(["git", "-C", str(platform), "rev-parse", "--show-toplevel"], text=True).strip()
    prefix = platform.resolve().relative_to(Path(source).resolve()).as_posix()
    # Read committed bytes, so unrelated local platform edits cannot leak in.
    paths = [f"{prefix}/tools/linux/build-kernel.sh",
             f"{prefix}/tools/linux/kernel-patches", f"{prefix}/tools/linux/kernel-bt.fragment",
             f"{prefix}/tools/linux/kernel-usbnet.fragment",
             f"{prefix}/docs/dumps/biscuit-fireos6-echolocal-boot-3.18.19.config",
             f"{prefix}/docs/dumps/radar-fireos6-boot-3.18.19.config"]
    archive_bytes = subprocess.check_output(["git", "-C", source, "archive", commit, *paths])
    with tarfile.open(fileobj=io.BytesIO(archive_bytes), mode="r:") as archive:
        for member in archive:
            if member.isdir():
                continue
            relative = Path(member.name).relative_to(prefix)
            if not member.isfile() or relative.is_absolute() or ".." in relative.parts:
                raise ValueError("unexpected platform archive member")
            output = destination / relative
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_bytes(archive.extractfile(member).read())
    header = (AUDIO / "tater_adc3101_hpf.h").read_text()
    addition = "".join(difflib.unified_diff([], header.splitlines(keepends=True),
                        fromfile="/dev/null", tofile="b/sound/soc/codecs/tater_adc3101_hpf.h"))
    patch = (AUDIO / "0051-tater-adc3101-hpf.patch").read_text() + addition
    (destination / "tools/linux/kernel-patches/0051-tater-adc3101-hpf.patch").write_text(patch)


def verify_toolchain(path: Path, expected: str) -> None:
    actual = subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip()
    if actual != expected:
        raise ValueError(f"toolchain commit {actual}, expected {expected}")
    subprocess.run(["git", "-C", str(path), "diff", "--quiet", "HEAD", "--"], check=True)
    if not (path / "bin/arm-eabi-gcc").is_file():
        raise ValueError("pinned ARM GCC compiler is missing")


def build(target: str, platform: Path, output: Path) -> dict:
    lock = json.loads(LOCK.read_text())
    if target not in lock["targets"]:
        raise ValueError("unsupported audio kernel target")
    toolchain = Path(os.environ.get("TOOLCHAIN", str(Path.home() / "toolchain/arm-eabi-4.8"))).resolve()
    if not toolchain.exists():
        toolchain.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "clone", "--depth", "1", "-b", "lollipop-release",
                        lock["toolchain_repository"], str(toolchain)], check=True)
    verify_toolchain(toolchain, lock["toolchain_commit"])
    for name, expected in lock["source_files"].items():
        if digest((AUDIO / name).read_bytes()) != expected:
            raise ValueError(f"audio source lock mismatch: {name}")
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="tater-dot-kernel-platform-") as temporary:
        stage = Path(temporary)
        stage_platform(platform, stage)
        environment = os.environ.copy()
        environment["TOOLCHAIN"] = str(toolchain)
        environment["KBUILD_BUILD_TIMESTAMP"] = lock["build_timestamp_utc"]
        environment["KBUILD_BUILD_VERSION"] = "1"
        subprocess.run(["bash", str(stage / "tools/linux/build-kernel.sh"),
                        "--board", target, "-o", str(output.resolve())],
                       env=environment, check=True)
    metadata = verify_kernel(output / "zImage-dtb", target)
    metadata.update({"schema": 1, "profile": "tater-adc3101-hpf80-v1",
                     "default_enabled": True, "hardware_gain_db": 40,
                     "platform_commit": platform_lock.load()["platforms"]["dot"]["commit"],
                     "toolchain_commit": lock["toolchain_commit"],
                     "build_timestamp_utc": lock["build_timestamp_utc"]})
    (output / "audio-kernel.json").write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n")
    return metadata


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", choices=("biscuit", "radar"), required=True)
    parser.add_argument("--platform", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--verify-kernel", type=Path)
    args = parser.parse_args()
    if args.verify_kernel:
        result = verify_kernel(args.verify_kernel, args.target)
    else:
        if args.platform is None or args.output is None:
            parser.error("build requires --platform and --output")
        result = build(args.target, args.platform, args.output)
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
