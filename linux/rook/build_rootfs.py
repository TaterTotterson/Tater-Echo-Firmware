#!/usr/bin/env python3
"""Build the Rook Tater rootfs from the signed, hash-pinned TECHO5 Spot base.

The device's own LineageOS vendor files are installed privately by the USB
installer. They are never copied into this redistributable rootfs.
"""

from __future__ import annotations

import argparse
from pathlib import Path
import sys
import tarfile
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "checkers"))
import build_rootfs as common  # noqa: E402 - shared, hardware-neutral Tater scripts


BASE_VERSION = "spot-v0.5.25"
BASE_SHA256 = "c2960f9a2792868a2707fdc514594a034b404c6a472b049c4fb3397165fe0eb2"


def build(args: argparse.Namespace) -> None:
    if common.sha256(args.base_rootfs) != BASE_SHA256:
        raise SystemExit(f"base rootfs is not signed TECHO5 Spot {BASE_VERSION}")
    with tempfile.TemporaryDirectory(prefix="tater-rook-rootfs-") as temporary:
        root = Path(temporary) / "root"
        root.mkdir()
        with tarfile.open(args.base_rootfs, "r:gz") as archive:
            common.safe_extract(archive, root)

        # Fail closed if the pinned archive's own board map is not Rook's.
        board = (root / "etc/techo5/device.conf").read_text()
        for assignment in (
            "DATA_DEV=/dev/mmcblk0p13",
            "STORE_DEV=/dev/mmcblk0p11",
            "WIFI_MODULE=/vendor/lib/modules/amzn-bcmdhd.ko",
            "BT_UART=/dev/ttyMT1",
        ):
            if assignment not in board:
                raise SystemExit(f"Rook base has an unexpected device map: {assignment}")

        common.copy(args.server, root / "usr/local/bin/tater-echo", 0o755)
        common.copy(args.show, root / "usr/local/bin/tater-show", 0o755)
        common.copy(args.reboot, root / "usr/local/bin/tater-reboot-now", 0o755)
        common.copy(args.camera, root / "usr/local/bin/tater-camera", 0o755)
        common.copy(args.mww_runtime, root / "usr/share/tater/microwakeword/libtater_microwakeword.so", 0o755)
        common.copy(args.onnx_runtime, root / "usr/share/tater/microwakeword/libonnxruntime.so", 0o755)
        common.copy(args.hey_tater_model, root / "usr/share/tater/microwakeword/hey_tater.tflite", 0o644)
        common.copy(args.hey_tater_manifest, root / "usr/share/tater/microwakeword/hey_tater.json", 0o644)
        common.copy(args.hey_tater_oww_onnx, root / "usr/share/tater/microwakeword/hey_tater.oww.onnx", 0o644)
        common.copy(args.hey_tater_oww_metadata, root / "usr/share/tater/microwakeword/hey_tater.oww.json", 0o644)
        common.copy(args.hey_tater_bundle, root / "usr/share/tater/microwakeword/hey_tater.wake-bundle.json", 0o644)
        common.copy(args.stop_model, root / "usr/share/tater/microwakeword/stop.tflite", 0o644)
        common.copy(args.stop_manifest, root / "usr/share/tater/microwakeword/stop.json", 0o644)
        common.copy(args.oww_melspectrogram_onnx, root / "usr/share/tater/microwakeword/melspectrogram.onnx", 0o644)
        common.copy(args.oww_embedding_onnx, root / "usr/share/tater/microwakeword/embedding_model.onnx", 0o644)
        common.copy(args.tinyalsa, root / "usr/lib/libtinyalsa.so.2.0.0", 0o755)
        for name, digest, member in common.SETUP_PACKAGES:
            common.setup_binary(args.apk_cache, name, digest, member, root / member)
        common.copy(Path(__file__).resolve().parents[1] / "checkers/setup-ap.sh",
                    root / "usr/local/sbin/tater-setup-ap-base", 0o755)
        common.copy(Path(__file__).with_name("setup-ap.sh"),
                    root / "usr/local/sbin/tater-setup-ap", 0o755)
        for name in ("libtinyalsa.so", "libtinyalsa.so.2"):
            link = root / "usr/lib" / name
            link.unlink(missing_ok=True)
            link.symlink_to("libtinyalsa.so.2.0.0")

        # Keep the platform tools (slotctl, btbridge, Wi-Fi) but replace the
        # TECHO5 application. Slotctl's rootfs validation expects this path.
        old_daemon = root / "usr/local/bin/techo5"
        old_daemon.unlink(missing_ok=True)
        old_daemon.symlink_to("tater-echo")
        for unused in ("techo5-aec", "techo5-librespot"):
            (root / "usr/local/bin" / unused).unlink(missing_ok=True)

        inittab = common.INITTAB.replace("Checkers", "Rook")
        common.write(root / "etc/inittab", inittab, 0o755)
        common.write(root / "usr/local/sbin/tater-run", common.TATER_RUN, 0o755)
        common.write(root / "usr/local/sbin/tater-show-run", common.TATER_SHOW_RUN, 0o755)
        common.write(root / "usr/local/sbin/tater-camera-run", common.TATER_CAMERA_RUN, 0o755)
        common.write(root / "usr/local/sbin/tater-app", common.TATER_APP, 0o755)
        common.write(root / "etc/hostname", "tater-rook\n")
        common.write(root / "etc/motd", "Tater Linux for Echo Spot (Rook).\n"
                     "Root filesystems use A/B slots; state is on /data and recovery is TWRP.\n")
        common.write(root / "usr/lib/os-release", common.OS_RELEASE.format(version=args.version)
                     .replace("Checkers", "Rook"))
        release = f"Tater Linux Rook {args.version} (TECHO5 Spot platform {BASE_VERSION})\n"
        common.write(root / "etc/tater-release", release)
        common.write(root / "etc/techo5-release", release)
        common.write(root / "usr/share/licenses/tater-linux/TECHO5-SPOT-LICENSE",
                     args.techo5_spot_license.read_text())
        common.write(root / "usr/share/licenses/tater-linux/TECHO5-LICENSE",
                     args.techo5_license.read_text())
        common.write(root / "usr/share/licenses/tater-linux/NOTICE",
                     "Rook's hardware enablement and rescue boot contract derive from TECHO5 Spot "
                     "by HuskerMinion under the MIT License.\n"
                     "https://github.com/HuskerMinion/techo5-spot\n"
                     "The Rook camera driver derives from TECHO5 by HuskerMinion under the MIT License.\n"
                     "https://github.com/HuskerMinion/techo5\n\n"
                     + common.ATTRIBUTION)
        common.patch_boot(root, target="rook")
        common.patch_slotctl(root)
        common.deterministic_tar(root, args.output)
    print(f"built {args.output} ({args.output.stat().st_size} bytes, sha256 {common.sha256(args.output)})")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("base-rootfs", "server", "show", "reboot", "camera", "mww-runtime", "onnx-runtime", "hey-tater-model",
                 "hey-tater-manifest", "hey-tater-oww-onnx", "hey-tater-oww-metadata",
                 "hey-tater-bundle", "stop-model", "stop-manifest", "tinyalsa",
                 "oww-melspectrogram-onnx", "oww-embedding-onnx",
                 "techo5-spot-license", "techo5-license", "apk-cache", "output"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--version", required=True)
    build(parser.parse_args())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
