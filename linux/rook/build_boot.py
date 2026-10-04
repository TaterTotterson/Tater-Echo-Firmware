#!/usr/bin/env python3
"""Build Rook's Tater-branded rescue boot image offline; never flash it here.

Keep TECHO5 Spot's kernel, slot handling and rescue services intact. Only the
rescue display and its startup order change for Rook's upright round panel.
"""

from __future__ import annotations

import argparse
import hashlib
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import zipfile

import build_rescue_fbprobe


INPUT_HASHES = {
    "lineage_zip": "2755428c124df88ffd3bc6a9c36db16bbe3b7e639883fd1092dc839ac5ae9d13",
    "kernel": "368738ee20b86dc3dc0268d3b4b145e67cca9666379ac26b2379abd6c9136298",
    "rescue": "a6f3860d0e3bd5b298ff310d8fe0d6a9a7885a2f6aa792e4134783df4cbf15bf",
    "alpine_rootfs": "50942d567e6ee422c16cb46d5c282ed9d8adc9007c2a483faf4148a18c64ce32",
}
SPOT_SOURCE_COMMIT = "8253a4a1d52dd37dd152dd1d435b97e541e0a191"
SPOT_INIT_SHA256 = "7007c362ff4bad4c6dc2fea3100b7fde72d27f832c47961923edcdd1d919d32d"
BOOT_PARTITION_BYTES = 16 * 1024 * 1024
OLD_SCREEN = b'''# Panel: the test screen with the clock ticking, as proof of life.
if [ -x /usr/local/bin/fbprobe ]; then
	/usr/local/bin/fbprobe -hold 1000h > /tmp/fbprobe.log 2>&1 &
fi
'''
OLD_CONSOLE = b'''# Root shell on USB serial.
t5_usb_acm
(
	while true; do
		[ -e /dev/ttyGS0 ] || mdev -s
		if [ -e /dev/ttyGS0 ]; then
			setsid sh -c 'exec sh -l < /dev/ttyGS0 > /dev/ttyGS0 2>&1'
		fi
		sleep 2
	done
) &
'''
NEW_RESCUE_SCREEN = b'''# Start the USB installer control path before painting the screen.
t5_usb_acm
(
	while true; do
		[ -e /dev/ttyGS0 ] || mdev -s
		if [ -e /dev/ttyGS0 ]; then
			setsid sh -c 'exec sh -l < /dev/ttyGS0 > /dev/ttyGS0 2>&1'
		fi
		sleep 2
	done
) &

# Only the first factory stage is an install. Other rescue boots are faults.
if [ -x /usr/local/bin/fbprobe ]; then
	if [ "$MODE" = android ]; then
		/usr/local/bin/fbprobe -hold 1000h -title "INSTALLING TATER" -lines \\
"Preparing your Spot.|Keep power and USB connected.|This can take a few minutes.|If stuck, check installer." \\
			> /tmp/fbprobe.log 2>&1 &
	else
		/usr/local/bin/fbprobe -hold 1000h -title "TATER RECOVERY" -lines \\
"The system could not start.|Your data may still be safe.|Keep power and USB connected.|Check the USB installer." \\
			> /tmp/fbprobe.log 2>&1 &
	fi
fi
'''


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def require_hash(path: Path, expected: str) -> None:
    if not path.is_file() or sha256(path) != expected:
        raise SystemExit(f"{path} is missing or differs from its pinned SHA-256")


def branded_init(source: bytes) -> bytes:
    if hashlib.sha256(source).hexdigest() != SPOT_INIT_SHA256:
        raise SystemExit("Rook rescue init is not the pinned TECHO5 Spot revision")
    original = OLD_SCREEN + b"\n" + OLD_CONSOLE
    if source.count(original) != 1:
        raise SystemExit("pinned Rook rescue display and console are not contiguous")
    return source.replace(original, NEW_RESCUE_SCREEN, 1)


def build(args: argparse.Namespace) -> None:
    for name, digest in INPUT_HASHES.items():
        require_hash(getattr(args, name), digest)
    source_commit = subprocess.check_output(
        ["git", "-C", str(args.spot_source), "rev-parse", "HEAD"], text=True
    ).strip()
    if source_commit != SPOT_SOURCE_COMMIT:
        raise SystemExit(f"TECHO5 Spot source is {source_commit}, expected {SPOT_SOURCE_COMMIT}")
    with zipfile.ZipFile(args.lineage_zip) as archive:
        metadata = archive.read("META-INF/com/android/metadata").decode("utf-8", "replace")
        if "\npre-device=rook\n" not in "\n" + metadata:
            raise SystemExit("the LineageOS ZIP does not declare rook")
        with tempfile.TemporaryDirectory(prefix="tater-rook-boot-") as temporary:
            work = Path(temporary)
            init = work / "init"
            init.write_bytes(branded_init((args.spot_source / "tools/linux/init").read_bytes()))
            fbprobe = work / "tater-rook-rescue-fbprobe"
            build_rescue_fbprobe.build(args.techo5, fbprobe)
            boot = work / "lineage-boot.img"
            with archive.open("boot.img") as source, boot.open("wb") as target:
                while chunk := source.read(1024 * 1024):
                    target.write(chunk)
            if boot.open("rb").read(8) != b"ANDROID!":
                raise SystemExit("the LineageOS ZIP has no Android boot image")
            rescue = work / "rescue"
            rescue.mkdir()
            with tarfile.open(args.rescue, "r") as bundle:
                for member in bundle.getmembers():
                    target = (rescue / member.name).resolve()
                    if target != rescue.resolve() and rescue.resolve() not in target.parents:
                        raise SystemExit(f"unsafe rescue-bundle path: {member.name}")
                bundle.extractall(rescue)

            command = [
                sys.executable, str(rescue / "host/mkimage.py"),
                "--kernel-image", str(boot),
                "--rootfs", str(args.alpine_rootfs),
                "--init", str(init),
                "--add", str(rescue / "busybox.static") + "=/bin/busybox.static",
                "--kernel", str(args.kernel),
            ]
            command += ["--add", str(fbprobe) + "=/usr/local/bin/fbprobe"]
            for tool in ("audioprobe", "rebootto"):
                command += ["--add", str(rescue / "bin" / tool) + "=/usr/local/bin/" + tool]
            for script, destination in (
                ("slotctl", "/usr/local/sbin/slotctl"),
                ("techo5-lib.sh", "/lib/techo5-lib.sh"),
            ):
                command += ["--script", str(rescue / "scripts" / script) + "=" + destination]
            for package in sorted((rescue / "apks").glob("*.apk")):
                command += ["--apk", str(package)]
            args.output.parent.mkdir(parents=True, exist_ok=True)
            command += ["--compress", "xz", "--cmdline-append", "techo5=linux",
                        "-o", str(args.output)]
            subprocess.run(command, check=True)

    size = args.output.stat().st_size
    if size > BOOT_PARTITION_BYTES or args.output.open("rb").read(8) != b"ANDROID!":
        raise SystemExit("built boot image is invalid or exceeds Rook's 16 MiB boot partition")
    print(f"Tater-branded Rook boot image: {args.output} ({size} bytes, sha256 {sha256(args.output)})")
    print("No device writes were made. This image is not a factory installer or public release.")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("lineage-zip", "kernel", "rescue", "alpine-rootfs", "spot-source", "techo5", "output"):
        parser.add_argument("--" + name, required=True, type=Path)
    build(parser.parse_args())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
