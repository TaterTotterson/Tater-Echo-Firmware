#!/usr/bin/env python3
"""Safe post-amonet factory installer for the Echo Dot 2 (biscuit).

The release does not contain Amazon's kernel or device trees.  While the Dot
is in TWRP this installer reads the device's own stock boot partition, keeps a
recovery copy, builds emOS around that kernel, verifies every write, and then
installs Tater's A/B userspace firmware under /data.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parent
MANIFEST = ROOT / "bundle-manifest.json"
PAYLOAD = ROOT / "payload"
REMOTE_STAGE = "/tmp/tater-echo-factory"
ANDROID_MAGIC = b"ANDROID!"


class InstallError(RuntimeError):
    pass


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as src:
        for chunk in iter(lambda: src.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_manifest() -> dict:
    try:
        manifest = json.loads(MANIFEST.read_text())
    except (OSError, ValueError) as exc:
        raise InstallError(f"cannot read {MANIFEST.name}: {exc}") from exc
    if manifest.get("target") != "biscuit":
        raise InstallError("this is not a biscuit factory bundle")
    files = manifest.get("files")
    if not isinstance(files, dict) or not files:
        raise InstallError("bundle manifest has no files")
    return manifest


def verify_bundle(manifest: dict) -> None:
    for relative, metadata in sorted(manifest["files"].items()):
        path = ROOT / relative
        try:
            resolved = path.resolve(strict=True)
        except OSError as exc:
            raise InstallError(f"bundle file is missing: {relative}") from exc
        if ROOT not in resolved.parents:
            raise InstallError(f"bundle manifest path escapes its directory: {relative}")
        expected_size = int(metadata.get("size", -1))
        if resolved.stat().st_size != expected_size:
            raise InstallError(f"bundle file has the wrong size: {relative}")
        actual = sha256(resolved)
        if actual != metadata.get("sha256"):
            raise InstallError(f"bundle file failed SHA-256 verification: {relative}")


class Adb:
    def __init__(self, binary: str, serial: str | None):
        self.binary = binary
        self.serial = serial

    @property
    def base(self) -> list[str]:
        args = [self.binary]
        if self.serial:
            args += ["-s", self.serial]
        return args

    def run(self, *args: str, capture: bool = False) -> str:
        try:
            completed = subprocess.run(
                self.base + list(args), check=True,
                stdout=subprocess.PIPE if capture else None,
                stderr=subprocess.PIPE if capture else None,
            )
        except FileNotFoundError as exc:
            raise InstallError(f"ADB was not found at {self.binary!r}") from exc
        except subprocess.CalledProcessError as exc:
            detail = (exc.stderr or exc.stdout or b"").decode(errors="replace").strip()
            raise InstallError(f"adb {' '.join(args)} failed: {detail or exc.returncode}") from exc
        return completed.stdout.decode(errors="replace").replace("\r", "").strip() if capture else ""

    def shell(self, command: str) -> str:
        return self.run("shell", command, capture=True)

    def push(self, local: Path, remote: str) -> None:
        self.run("push", str(local), remote)

    def pull(self, remote: str, local: Path) -> None:
        self.run("pull", remote, str(local))


def select_adb(binary: str, serial: str | None) -> Adb:
    probe = Adb(binary, None)
    output = probe.run("devices", capture=True)
    devices = [line.split()[0] for line in output.splitlines()[1:]
               if len(line.split()) >= 2 and line.split()[1] == "device"]
    if serial:
        if serial not in devices:
            raise InstallError(f"ADB device {serial!r} is not connected")
        return Adb(binary, serial)
    if len(devices) != 1:
        raise InstallError(
            f"expected exactly one connected ADB device, found {len(devices)}; "
            "use --serial when more than one is attached")
    return Adb(binary, devices[0])


def require_twrp(adb: Adb) -> None:
    twrp = adb.shell("getprop ro.twrp.version 2>/dev/null || true")
    marker = adb.shell("test -x /sbin/twrp && echo yes || true")
    if not twrp and marker != "yes":
        raise InstallError(
            "the Echo is not in TWRP; boot it with Volume Up before running this installer")
    product = adb.shell("getprop ro.product.device 2>/dev/null || true").lower()
    if product and "biscuit" not in product:
        raise InstallError(f"connected device reports {product!r}, not biscuit")


def resolve_partition(adb: Adb, name: str) -> str:
    value = adb.shell(
        f"for p in /dev/block/by-name/{name} /dev/block/platform/*/by-name/{name}; "
        "do [ -e \"$p\" ] && readlink -f \"$p\" && break; done")
    value = value.splitlines()[0] if value else ""
    if not re.fullmatch(r"/dev/block/mmcblk[0-9]+p[0-9]+", value):
        raise InstallError(f"could not resolve the {name} partition in TWRP")
    return value


def partition_number(path: str) -> int:
    match = re.search(r"p([0-9]+)$", path)
    if not match:
        raise InstallError(f"partition path has no number: {path}")
    return int(match.group(1))


def pull_partition(adb: Adb, partition: str, destination: Path, label: str) -> None:
    remote = f"{REMOTE_STAGE}-{label}.img"
    adb.shell(f"rm -f {remote}; dd if={partition} of={remote} bs=1048576; sync")
    adb.pull(remote, destination)
    adb.shell(f"rm -f {remote}")


def boot_kind(path: Path) -> str:
    data = path.read_bytes()
    if not data.startswith(ANDROID_MAGIC):
        return "invalid"
    cmdline = data[64:576]
    if b"emos.system=" in cmdline or b"ramoops.mem_address=0x44400000" in cmdline:
        return "emos"
    return "stock"


def verified_partition_write(adb: Adb, image: Path, partition: str, label: str) -> None:
    remote = f"{REMOTE_STAGE}-{label}.img"
    verify_remote = f"{REMOTE_STAGE}-{label}-verify.img"
    verify_local = image.parent / f"{label}-verify.img"
    size = image.stat().st_size
    if size <= 0 or size % 2048:
        raise InstallError(f"{label} image size is not a non-zero 2048-byte multiple")
    adb.push(image, remote)
    adb.shell(f"dd if={remote} of={partition} bs=2048; sync")
    adb.shell(f"rm -f {verify_remote}; dd if={partition} of={verify_remote} bs=2048 count={size // 2048}")
    adb.pull(verify_remote, verify_local)
    if sha256(verify_local) != sha256(image):
        raise InstallError(
            f"read-back verification failed for {partition}; DO NOT REBOOT the Echo")
    adb.shell(f"rm -f {remote} {verify_remote}")
    verify_local.unlink(missing_ok=True)


def load_packer():
    path = ROOT / "tools" / "em_emos_build.py"
    spec = importlib.util.spec_from_file_location("tater_em_emos_build", path)
    if not spec or not spec.loader:
        raise InstallError("cannot load the bundled emOS image builder")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def build_emos(reference: Path, system_part: int, version: str, destination: Path) -> None:
    packer = load_packer()
    sbin = {
        name: (PAYLOAD / name).read_bytes()
        for name in ("wpa_supplicant", "wpa_cli", "em-wifi", "busybox")
    }
    try:
        built = packer.build_emos_image(
            reference.read_bytes(), (PAYLOAD / "init32").read_bytes(),
            version, sbin=sbin, system_part=system_part)
    except Exception as exc:
        raise InstallError(f"could not build emOS from this Echo's stock boot image: {exc}") from exc
    destination.write_bytes(built["image"])
    print(f"Built device-specific emOS image ({built['size']:,} bytes, sha256 {built['sha256'][:16]}…)")


def install_userspace(adb: Adb) -> None:
    adb.shell(
        "mount /data >/dev/null 2>&1 || true; "
        "test -d /data && test -w /data || exit 31; "
        "mkdir -p /data/local/bin /data/local/etc/tater "
        "/data/local/share/tater/microwakeword /data/emos")
    remote_payload = f"{REMOTE_STAGE}-payload"
    adb.shell(f"rm -rf {remote_payload}; mkdir -p {remote_payload}")
    mapping = {
        "server": "/data/local/bin/server_a",
        "start_server.sh": "/data/local/bin/start_server.sh",
        "libtater_microwakeword.so": "/data/local/share/tater/microwakeword/libtater_microwakeword.so",
        "hey_tater.tflite": "/data/local/share/tater/microwakeword/hey_tater.tflite",
        "hey_tater.json": "/data/local/share/tater/microwakeword/hey_tater.json",
    }
    for name, destination in mapping.items():
        staged = f"{remote_payload}/{name}"
        adb.push(PAYLOAD / name, staged)
        adb.shell(f"cp {staged} {destination}")
    adb.shell(
        "chmod 755 /data/local/bin/server_a /data/local/bin/start_server.sh "
        "/data/local/share/tater/microwakeword/libtater_microwakeword.so; "
        "chmod 644 /data/local/share/tater/microwakeword/hey_tater.tflite "
        "/data/local/share/tater/microwakeword/hey_tater.json; "
        "cp /data/local/bin/server_a /data/local/bin/server_b; "
        "chmod 755 /data/local/bin/server_b; "
        "ln -sf server_a /data/local/bin/server; "
        "stamp=$(date +%Y%m%d%H%M%S); "
        "for f in /data/local/etc/tater/native.json /data/local/etc/tater/device_token /data/emos/wpa.conf; do "
        "  [ ! -e \"$f\" ] || mv \"$f\" \"$f.before-factory-$stamp\"; "
        "done; "
        ": > /data/local/etc/tater/setup_enabled; "
        f"rm -rf {remote_payload}; sync")


def restore(adb: Adb, image: Path) -> None:
    if boot_kind(image) != "stock":
        raise InstallError("restore image is not a stock Android boot image")
    boot_a = resolve_partition(adb, "boot_a")
    verified_partition_write(adb, image, boot_a, "restore-stock-a")
    adb.shell("bcbtool set_active a; sync")
    print("Stock boot restored to boot_a and slot A selected. You may now reboot.")


def install(adb: Adb, manifest: dict, assume_yes: bool, no_reboot: bool) -> None:
    boot_a = resolve_partition(adb, "boot_a")
    boot_b = resolve_partition(adb, "boot_b")
    system_a = resolve_partition(adb, "system_a")
    system_b = resolve_partition(adb, "system_b")

    with tempfile.TemporaryDirectory(prefix="tater-echo-") as temporary:
        temp = Path(temporary)
        image_a, image_b = temp / "boot_a.img", temp / "boot_b.img"
        print("Reading both boot slots before changing anything…")
        pull_partition(adb, boot_a, image_a, "read-a")
        pull_partition(adb, boot_b, image_b, "read-b")
        kind_a, kind_b = boot_kind(image_a), boot_kind(image_b)
        print(f"boot_a: {kind_a}; boot_b: {kind_b}")
        if kind_b == "stock":
            donor, donor_slot, system = image_b, "b", system_b
        elif kind_a == "stock":
            donor, donor_slot, system = image_a, "a", system_a
        else:
            raise InstallError(
                "neither boot slot contains a usable stock image; restore FireOS in TWRP first")

        backup_dir = ROOT / "factory-backups"
        backup_dir.mkdir(mode=0o700, exist_ok=True)
        recovery = backup_dir / f"biscuit-stock-boot-{sha256(donor)[:12]}.img"
        if not recovery.exists():
            shutil.copy2(donor, recovery)
            os.chmod(recovery, 0o600)
        print(f"Recovery boot saved to {recovery}")

        built = temp / "tater-emos-boot.img"
        build_emos(donor, partition_number(system), manifest["version"], built)

        if not assume_yes:
            print("\nThis will preserve stock in boot_b, write Tater emOS to boot_a, and reset Wi-Fi/Tater pairing.")
            if input("Type INSTALL to continue: ").strip() != "INSTALL":
                raise InstallError("installation cancelled")

        if donor_slot == "a" and kind_b != "stock":
            print("Preserving the stock boot image in boot_b…")
            verified_partition_write(adb, donor, boot_b, "stock-b")

        print("Installing Tater userspace and first-boot setup portal…")
        install_userspace(adb)
        print("Writing and verifying device-specific emOS in boot_a…")
        verified_partition_write(adb, built, boot_a, "tater-emos-a")
        adb.shell("bcbtool set_active a; sync")
        active = adb.shell("bcbtool get_active")
        if active.strip() != "a":
            raise InstallError(f"could not select slot A (bcbtool reports {active!r}); DO NOT REBOOT")

    print("\nInstall verified. Stock recovery remains in boot_b and in the local backup above.")
    if no_reboot:
        print("The Echo is still in TWRP. Run: adb reboot")
    else:
        print("Rebooting. Join the Tater-Setup-XXXX Wi-Fi network when it appears.")
        adb.run("reboot")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Install Tater firmware after amonet-biscuit v2.0.0")
    parser.add_argument("--adb", default="adb", help="path to adb")
    parser.add_argument("--serial", help="ADB serial when more than one device is connected")
    parser.add_argument("--yes", action="store_true", help="skip the INSTALL confirmation")
    parser.add_argument("--no-reboot", action="store_true", help="leave the Echo in TWRP after installation")
    parser.add_argument("--verify-bundle", action="store_true", help="verify the release bundle and exit")
    parser.add_argument("--restore", type=Path, help="restore a saved stock boot image to boot_a")
    return parser.parse_args()


def main() -> int:
    try:
        args = parse_args()
        manifest = load_manifest()
        verify_bundle(manifest)
        print(f"Tater Echo Firmware {manifest['version']} bundle verified.")
        if args.verify_bundle:
            return 0
        adb = select_adb(args.adb, args.serial)
        require_twrp(adb)
        if args.restore:
            restore(adb, args.restore.resolve())
        else:
            install(adb, manifest, args.yes, args.no_reboot)
        return 0
    except (InstallError, KeyboardInterrupt) as exc:
        print(f"\nERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
