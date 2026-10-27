#!/usr/bin/env python3
"""Factory-install Tater Linux on an unlocked Echo Spot 2017 (rook) in TWRP.

The installer saves and verifies the original partitions before any write. It
never writes lk or recovery; it replaces only the verified boot-logo bundle in
expdb and restores the complete backup immediately if readback fails.
"""

from __future__ import annotations

import argparse
import getpass
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import struct
import subprocess
import sys
import tempfile
import time
from urllib import request as urlrequest
import zipfile
import zlib


ROOT = Path(__file__).resolve().parent
TOOLS = ROOT / "tools" / "techo5"
if not TOOLS.is_dir():
    TOOLS = ROOT.parents[1] / "factory" / "checkers-linux" / "tools" / "techo5"
sys.path.insert(0, str(TOOLS))
from techo5lib import (Adb, CONSOLE_TECHO5, Console, Fastboot, Fail, check_serial_access,
                       md5, new_api_key, note, valid_api_key, wait_for, write_private)  # noqa: E402


LINEAGE_NAME = "lineage-18.1-20251108-UNOFFICIAL-rook.zip"
LINEAGE_SHA256 = "2755428c124df88ffd3bc6a9c36db16bbe3b7e639883fd1092dc839ac5ae9d13"
LINEAGE_URL = ("https://github.com/amazon-oss/releases/releases/download/"
               "lineage-18.1-rook-v0.3/" + LINEAGE_NAME)
WIFI_MODULE = "vendor/lib/modules/amzn-bcmdhd.ko"
PARTITIONS = {"lk": 3, "expdb": 7, "boot": 9, "recovery": 10, "system": 11}
MAX_LINEAGE_BYTES = 1024 * 1024 * 1024
# Reinstall remains fail-closed: only the current bundle or these exact earlier
# Tater boot images may be replaced. The second entry is the locally built
# v2.5.0 image used for the first multi-Rook installation. Its Android header,
# pinned kernel, rescue init, slot manager, and ramdisk files were compared with
# the release image; only fbprobe's Go toolchain/build metadata differs. The
# third entry is the exact boot image published in the v3.1.2 Rook factory
# archive, retained so that released installations can be upgraded safely.
KNOWN_PREVIOUS_TATER_BOOTS = (
    (13012992, "2496456df296db59349ce8cdc43778fbdb670d92279bb169d76907d8ca7719c7"),
    (13121536, "5c2da30a18b4b3cc6b670bec9d2f08f7ab985453c7179aae4e8d4c9f14f38c6a"),
    (13121536, "91c8a0a84867b4055594c1d150ea31951f0fc49a05da63ef51217ae1769c2e5c"),
)
# A failed reinstall can stop after the installer has written the exact pinned
# Lineage boot image used to recover Rook's vendor files. Accept that one
# intermediate image so the same verified factory flow can resume safely.
KNOWN_REINSTALL_INTERMEDIATE_BOOTS = (
    (7720960, "f09d7f95a8d5d3519925a7ad865c42bffd8a05e53935bea70210da84a0e51638"),
)
ROOK_LOGO_OFFSET = 431416
ROOK_LOGO_SIZE = 12096
ROOK_LOGO_DIMENSIONS = (480, 480)
ROOK_LOGO_SHA256 = "2c8d54f6987cef2e0877360e873191bfc48a35a13e8d7f42e0ecb987f2df860d"
LK_LOGO_BASE = 0x4BD00000
LK_HEADER_SIZE = 512


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def require(condition: bool, message: str) -> None:
    if not condition:
        raise Fail(message)


def manifest() -> dict:
    try:
        data = json.loads((ROOT / "bundle-manifest.json").read_text())
    except (OSError, ValueError) as error:
        raise Fail(f"cannot read the Rook bundle manifest: {error}") from error
    require(data.get("target") == "rook" and data.get("base_os") == "tater-linux",
            "this is not a Rook Tater Linux factory bundle")
    require(isinstance(data.get("files"), dict) and bool(data["files"]),
            "Rook bundle manifest has no files")
    for relative, metadata in data["files"].items():
        path = (ROOT / relative).resolve()
        require(ROOT.resolve() in path.parents, f"bundle path escapes its directory: {relative}")
        require(path.is_file() and path.stat().st_size == metadata.get("size"),
                f"bundle file is missing or has the wrong size: {relative}")
        require(sha256(path) == metadata.get("sha256"), f"bundle file failed SHA-256: {relative}")
    return data


def verify_lineage(path: Path) -> None:
    require(path.is_file() and sha256(path) == LINEAGE_SHA256,
            "LineageOS ZIP is missing or differs from the pinned Rook v0.3 ZIP")
    try:
        with zipfile.ZipFile(path) as archive:
            metadata = archive.read("META-INF/com/android/metadata").decode("utf-8", "replace")
            require(re.search(r"^pre-device=rook$", metadata, re.M) is not None,
                    "LineageOS ZIP does not declare rook")
            require(archive.read("boot.img", pwd=None)[:8] == b"ANDROID!",
                    "LineageOS ZIP lacks a Rook boot image")
    except (OSError, KeyError, zipfile.BadZipFile) as error:
        raise Fail(f"invalid Rook LineageOS ZIP: {error}") from error


def download_lineage(work: Path) -> Path:
    path = work / LINEAGE_NAME
    if path.is_file():
        verify_lineage(path)
        note(f"using verified cached Rook LineageOS ZIP: {path}")
        return path
    work.mkdir(parents=True, exist_ok=True)
    note(f"downloading pinned Rook LineageOS ZIP: {LINEAGE_URL}")
    temporary = None
    try:
        request = urlrequest.Request(LINEAGE_URL, headers={"User-Agent": "Tater-Rook-Installer"})
        with urlrequest.urlopen(request, timeout=45) as response:
            with tempfile.NamedTemporaryFile(prefix="rook-lineage-", suffix=".partial",
                                             dir=work, delete=False) as output:
                temporary = Path(output.name)
                total = 0
                while chunk := response.read(1024 * 1024):
                    total += len(chunk)
                    require(total <= MAX_LINEAGE_BYTES, "LineageOS download exceeds 1 GiB")
                    output.write(chunk)
                output.flush()
                os.fsync(output.fileno())
        verify_lineage(temporary)
        temporary.replace(path)
        temporary = None
        note(f"verified {total} bytes of Rook LineageOS")
        return path
    except (OSError, ValueError) as error:
        raise Fail(f"could not download the pinned Rook LineageOS ZIP: {error}") from error
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def select_serial(requested: str | None, adb_exe: str) -> str:
    try:
        output = subprocess.check_output([adb_exe, "devices"], text=True)
    except (OSError, subprocess.CalledProcessError) as error:
        raise Fail(f"adb devices failed: {error}") from error
    candidates = [line.split()[0] for line in output.splitlines()[1:]
                  if len(line.split()) >= 2 and line.split()[1] == "recovery"]
    if requested:
        require(requested in candidates, f"{requested} is not connected in TWRP")
        return requested
    require(len(candidates) == 1,
            f"expected exactly one device in TWRP, found {len(candidates)}; use --serial")
    return candidates[0]


def device_partition(adb: Adb, name: str, number: int) -> str:
    link = f"/dev/block/platform/bootdevice/by-name/{name}"
    if name == "lk":
        link += "_real"  # amonet deliberately points the ordinary lk link at /dev/null
    actual = adb.sh(f"readlink -f {link}")
    expected = f"/dev/block/mmcblk0p{number}"
    require(actual == expected, f"{name} resolves to {actual!r}, expected {expected}")
    return actual


def has_existing_tater_store(adb: Adb) -> bool:
    """Identify the store without mounting newer ext4 on TWRP's older kernel."""
    system = device_partition(adb, "system", PARTITIONS["system"])
    identity = adb.sh(f"blkid {system}")
    fs_type = re.search(r'(?<!\S)TYPE="([^"]+)"', identity)
    require(fs_type is not None,
            f"could not identify the Rook system filesystem: {identity!r}")
    label = re.search(r'(?<!\S)LABEL="([^"]+)"', identity)
    return fs_type.group(1) == "ext4" and label is not None and label.group(1) == "techo5-store"


def verify_reinstall_boot(adb: Adb, boot: Path) -> None:
    """Only allow a bundled, released, or exact interrupted-install boot."""
    accepted = (
        (boot.stat().st_size, sha256(boot)),
        *KNOWN_PREVIOUS_TATER_BOOTS,
        *KNOWN_REINSTALL_INTERMEDIATE_BOOTS,
    )
    for size, digest in accepted:
        current = adb.sh(f"head -c {size} /dev/block/mmcblk0p9 | sha256sum").split()
        if current and current[0] == digest:
            return
    raise Fail("the current boot image is not a known Rook Tater image; refusing reinstall")


def save_backups(adb: Adb, folder: Path) -> None:
    folder.mkdir(parents=True, exist_ok=True)
    for name, number in PARTITIONS.items():
        partition = device_partition(adb, name, number)
        size_raw = adb.sh(f"blockdev --getsize64 {partition}")
        require(size_raw.isdigit() and int(size_raw) > 0,
                f"cannot determine {name} partition size")
        fields = adb.sh(f"md5sum {partition}").split()
        digest = fields[0] if fields else ""
        require(len(digest) == 32, f"cannot checksum {name} on the device")
        destination = folder / f"{name}.img"
        if destination.is_file():
            require(destination.stat().st_size == int(size_raw) and md5(str(destination)) == digest,
                    f"{destination} differs from the current partition; refusing to overwrite it")
            note(f"{name} backup verified")
            continue
        partial = destination.with_suffix(".img.partial")
        try:
            require(adb.pull(partition, str(partial)), f"reading {name} failed")
            require(partial.stat().st_size == int(size_raw) and md5(str(partial)) == digest,
                    f"{name} backup failed its device checksum")
            partial.replace(destination)
        finally:
            partial.unlink(missing_ok=True)
        note(f"{name} backed up and checksum-verified ({size_raw} bytes)")


def push_checked(adb: Adb, source: Path, remote: str) -> None:
    adb.push(str(source), remote, ready=lambda: " /data " in adb.sh("mount"))
    fields = adb.sh(f"sha256sum {shlex.quote(remote)}").split()
    actual = fields[0] if fields else ""
    require(actual == sha256(source), f"{source.name} changed in transfer to the device")


def valid_rook_logo_slot(image: bytes) -> bool:
    """Recognize Rook's exact in-place LK logo bundle before touching expdb."""
    end = ROOK_LOGO_OFFSET + ROOK_LOGO_SIZE
    if len(image) < end:
        return False
    count, total, offset = struct.unpack_from("<III", image, ROOK_LOGO_OFFSET)
    if count != 1 or offset != 12 or not 16 < total <= ROOK_LOGO_SIZE:
        return False
    try:
        raw = zlib.decompress(image[ROOK_LOGO_OFFSET + 12:ROOK_LOGO_OFFSET + total])
    except zlib.error:
        return False
    if len(raw) != ROOK_LOGO_DIMENSIONS[0] * ROOK_LOGO_DIMENSIONS[1] * 4:
        return False
    pointer = struct.pack("<I", LK_LOGO_BASE + ROOK_LOGO_OFFSET - LK_HEADER_SIZE)
    return sum(
        image[index:index + 4] == pointer
        for index in range(LK_HEADER_SIZE, len(image) - 3, 4)
    ) == 1


def install_tater_boot_logo(adb: Adb, backup: Path, logo: Path) -> str:
    """Replace only Rook's verified expdb logo slot, with full readback and restore."""
    require(logo.is_file() and logo.stat().st_size == ROOK_LOGO_SIZE
            and sha256(logo) == ROOK_LOGO_SHA256,
            "the bundled monochrome Tater boot logo failed verification")
    expdb_backup = backup / "expdb.img"
    original = expdb_backup.read_bytes()
    payload = logo.read_bytes()
    slot = original[ROOK_LOGO_OFFSET:ROOK_LOGO_OFFSET + ROOK_LOGO_SIZE]
    if slot == payload:
        return "the Tater boot logo is already installed"
    if not valid_rook_logo_slot(original):
        return "kept Amazon's boot logo: Rook expdb does not contain the verified logo slot"

    partition = device_partition(adb, "expdb", PARTITIONS["expdb"])
    original_sha = hashlib.sha256(original).hexdigest()
    expected = original[:ROOK_LOGO_OFFSET] + payload + original[ROOK_LOGO_OFFSET + ROOK_LOGO_SIZE:]
    expected_sha = hashlib.sha256(expected).hexdigest()
    fields = adb.sh(f"sha256sum {partition}").split()
    if not fields or fields[0] != original_sha:
        return "kept Amazon's boot logo: expdb changed after its verified backup"

    remote_logo = "/tmp/tater-boot-logo.bin"
    adb.push(str(logo), remote_logo)
    fields = adb.sh(f"sha256sum {remote_logo}").split()
    require(fields and fields[0] == ROOK_LOGO_SHA256,
            "the Tater boot logo changed while transferring to Rook")
    adb.sh(
        f"dd if={remote_logo} of={partition} bs=1 seek={ROOK_LOGO_OFFSET} "
        f"count={ROOK_LOGO_SIZE} 2>/dev/null; sync; rm -f {remote_logo}"
    )
    fields = adb.sh(f"echo 3 > /proc/sys/vm/drop_caches; sha256sum {partition}").split()
    if fields and fields[0] == expected_sha:
        return "the monochrome Tater boot logo was written and read back successfully"

    remote_backup = "/tmp/tater-expdb-backup.img"
    adb.push(str(expdb_backup), remote_backup)
    restore_fields = adb.sh(f"sha256sum {remote_backup}").split()
    require(restore_fields and restore_fields[0] == original_sha,
            "boot-logo readback failed and the expdb backup changed during transfer")
    adb.sh(f"dd if={remote_backup} of={partition} bs=4096 2>/dev/null; "
           f"sync; rm -f {remote_backup}")
    restored = adb.sh(f"echo 3 > /proc/sys/vm/drop_caches; sha256sum {partition}").split()
    require(restored and restored[0] == original_sha,
            f"boot-logo readback and automatic expdb restoration failed; preserve {expdb_backup}")
    raise Fail("Tater boot-logo readback failed; the verified original expdb was restored")


def install_lineage(adb: Adb, zip_path: Path) -> None:
    output = adb.sh("twrp format data")
    require("Done" in output, f"TWRP could not format userdata: {output}")
    adb.reboot("recovery")
    wait_for("TWRP with mounted userdata", 180,
             lambda: adb.state() == "recovery" and " /data " in adb.sh("mount"), 3)
    ready_since = [None]

    def settled() -> bool:
        ready = adb.state() == "recovery" and " /data " in adb.sh("mount")
        now = time.monotonic()
        if not ready:
            ready_since[0] = None
            return False
        if ready_since[0] is None:
            ready_since[0] = now
        return now - ready_since[0] >= 20

    wait_for("TWRP USB to settle", 90, settled, 2)
    push_checked(adb, zip_path, "/data/lineage.zip")
    output = adb.sh("twrp install /data/lineage.zip")
    adb.sh("rm -f /data/lineage.zip")
    require("succeeded" in output.lower(), f"TWRP could not install LineageOS: {output}")
    note("Rook LineageOS installed for its vendor drivers; it was not booted")
    probe = ("mkdir -p /tmp/tater-sys && mount -o ro /dev/block/mmcblk0p11 /tmp/tater-sys && "
             f"grep -a -o 'vermagic=[^ ]*' /tmp/tater-sys/system/{WIFI_MODULE} | head -1; "
             "umount /tmp/tater-sys")
    version = adb.sh(probe)
    require("vermagic=4.9.337" in version,
            f"Rook Wi-Fi module does not match the 4.9.337 boot kernel: {version}")
    note(f"Rook Wi-Fi module matches kernel: {version}")


def confirm(serial: str, version: str, reinstall: bool) -> None:
    print(f"\nAbout to install Tater Linux {version} on Rook {serial}.", flush=True)
    print("This formats userdata, installs pinned LineageOS only for vendor drivers,", flush=True)
    print("flashes boot, and erases system (mmcblk0p11) for Tater's A/B slot store.", flush=True)
    if reinstall:
        print("REINSTALL: the current Tater setup, A/B slots, and local settings will be erased.",
              flush=True)
    print("Recovery and lk are not written. Only expdb's verified boot-logo slot is replaced.", flush=True)
    require(input("Type ERASE to continue: ").strip() == "ERASE", "installation cancelled")


def install(args: argparse.Namespace) -> None:
    info = manifest()
    if args.verify_bundle:
        print(f"Verified Rook factory bundle {info['version']}; no device writes were made.")
        return
    boot = ROOT / "payload" / "boot.img"
    rootfs = ROOT / "payload" / "rootfs.tar.gz"
    require(boot.open("rb").read(8) == b"ANDROID!" and boot.stat().st_size <= 16 * 1024 * 1024,
            "Rook boot image is invalid or exceeds its 16 MiB partition")
    serial = select_serial(args.serial, args.adb)
    adb = Adb(serial, args.adb)
    require(adb.sh("getprop ro.product.device") == "rook", "connected device does not report rook")
    require(adb.sh("id").startswith("uid=0"), "Rook TWRP does not provide root adb")
    for name, number in PARTITIONS.items():
        device_partition(adb, name, number)
    existing_store = has_existing_tater_store(adb)
    require(existing_store == args.reinstall_existing_tater,
            "an existing Tater A/B store requires --reinstall-existing-tater"
            if existing_store else
            "--reinstall-existing-tater was given, but no Tater A/B store was found")
    if existing_store:
        verify_reinstall_boot(adb, boot)
    zip_path = args.lineage_zip or download_lineage(args.work)
    verify_lineage(zip_path)
    # Never replace the original factory backups with images of an installed
    # Tater system. Reinstalls get their own pre-reinstall recovery set.
    backup = args.backups / serial / "pre-reinstall" if existing_store else args.backups / serial
    save_backups(adb, backup)
    note(f"recovery backups: {backup}")
    if args.preflight_only:
        print("Rook preflight complete. No device writes were made.")
        return
    check_serial_access()
    confirm(serial, info["version"], existing_store)
    install_lineage(adb, zip_path)
    lineage_boot = backup / "boot-lineage.img"
    partial = lineage_boot.with_suffix(".img.partial")
    if not lineage_boot.exists():
        try:
            require(adb.pull("/dev/block/mmcblk0p9", str(partial)), "could not save LineageOS boot")
            require(partial.stat().st_size == 16 * 1024 * 1024 and
                    md5(str(partial)) == adb.sh("md5sum /dev/block/mmcblk0p9").split()[0],
                    "LineageOS boot backup failed verification")
            partial.replace(lineage_boot)
        finally:
            partial.unlink(missing_ok=True)
    else:
        fields = adb.sh("md5sum /dev/block/mmcblk0p9").split()
        require(fields and lineage_boot.stat().st_size == 16 * 1024 * 1024 and
                md5(str(lineage_boot)) == fields[0],
                "existing LineageOS boot backup differs from the boot just installed")
    note(f"LineageOS boot backup: {lineage_boot}")
    note(install_tater_boot_logo(adb, backup, ROOT / "boot-logo" / "tater-boot-480.bin"))
    adb.sh("mkdir -p /data/techo5-linux /data/misc/techo5")
    push_checked(adb, rootfs, "/data/techo5-linux/tater-rootfs.tar.gz")
    key_file = backup / "home-assistant.key"
    if key_file.is_file():
        psk = key_file.read_text().strip()
    else:
        psk = new_api_key()
        write_private(str(key_file), psk)
    require(valid_api_key(psk), "platform bootstrap key is invalid")
    fastboot = Fastboot(serial, args.fastboot)
    console = Console(serial, CONSOLE_TECHO5)
    adb.reboot("bootloader")
    wait_for("Rook fastboot", 90, fastboot.present, 3)
    code, output = fastboot.run("flash", "boot", str(boot))
    require(code == 0, f"flashing Rook boot failed: {output}")
    code, output = fastboot.run("reboot")
    require(code == 0, f"restarting Rook from fastboot failed: {output}")
    note("booting the Rook rescue environment; the slot store is not erased yet")
    nudged = [False]

    def rescue_up() -> bool:
        if "RESCUE-UP" in (console.run("test -e /run/techo5/slot || echo RESCUE-UP", 4) or ""):
            return True
        if not nudged[0] and fastboot.present():
            code, _ = fastboot.run("continue")
            nudged[0] = code == 0
        return False

    wait_for("Rook rescue USB console", 300, rescue_up, hint=console.waiting_hint)
    require("4.9.337" in (console.run("uname -r", 8) or ""),
            "Rook rescue booted an unexpected kernel; system was not erased")
    require("RESCUE-READY" in (console.run(
        f"touch /tmp/stay; test -f /android/system/{WIFI_MODULE} && echo RESCUE-READY", 10) or ""),
        "rescue cannot see the Rook vendor tree; system was not erased")
    require("HAVE-STORE" not in (console.run(
        "test -e /store/.techo5-store && echo HAVE-STORE", 10) or ""),
        "a Rook slot store already exists; refusing to erase it")
    note(f"Rook rescue console verified on {console.port}; vendor tree is present")

    output = console.run(
        f"tar -cf /data/techo5-linux/vendor.tar -C /android/system vendor && "
        f"tar -tf /data/techo5-linux/vendor.tar {WIFI_MODULE} >/dev/null && echo VENDOR-SAVED", 180)
    require("VENDOR-SAVED" in (output or ""),
            f"could not preserve the device's own vendor tree; system was not erased: {output}")
    output = console.run(
        "killall techo5 fbprobe 2>/dev/null; umount /android 2>/dev/null; "
        "if mountpoint -q /android; then echo STILL-MOUNTED; else "
        "slotctl mkstore /dev/mmcblk0p11 --i-know-this-erases-it "
        ">/tmp/mkstore.log 2>&1 && echo MKSTORE-OK; fi; tail -5 /tmp/mkstore.log", 300)
    require("MKSTORE-OK" in (output or ""), f"Rook slot-store creation failed: {output}")
    output = console.run(
        f"tar -xf /data/techo5-linux/vendor.tar -C /store && "
        f"test -f /store/{WIFI_MODULE} && echo VENDOR-OK", 180)
    require("VENDOR-OK" in (output or ""), f"restoring Rook vendor tree failed: {output}")
    output = console.run(
        "STORE=/store slotctl install /data/techo5-linux/tater-rootfs.tar.gz "
        ">/tmp/install.log 2>&1 && echo INSTALL-OK; tail -5 /tmp/install.log; "
        f"test -f /store/slots/a/{WIFI_MODULE} || "
        "{ mkdir -p /store/slots/a/vendor && cp -a /store/vendor/. /store/slots/a/vendor/; }; "
        f"test -f /store/slots/a/{WIFI_MODULE} && echo SLOT-VENDOR-OK; "
        "STORE=/store slotctl status", 900)
    require("INSTALL-OK" in (output or "") and "SLOT-VENDOR-OK" in (output or ""),
            f"Rook slot A or its vendor tree is incomplete: {output}")
    # The vendor archive stays on userdata for recovery until a healthy first boot.
    name = args.name.strip() if args.name else "Tater Spot"
    provision = ("mkdir -p /data/misc/techo5; "
                 f"printf '%s\\n' {shlex.quote(name)} > /data/misc/techo5/name; "
                 f"(umask 077; printf '%s\\n' {shlex.quote(psk)} > /data/misc/techo5/psk); "
                 "sync; echo PROVISIONED")
    require("PROVISIONED" in (console.run(provision, 15) or ""),
            "slot A installed, but local provisioning failed")
    console.run("sync; (sleep 2; /bin/busybox.static reboot -f) >/dev/null 2>&1 &", 3)
    wait_for("Rook slot A and all Tater services", 300,
             lambda: "TATER-UP" in (console.run(
                 "test $(cat /run/techo5/slot 2>/dev/null) = a && "
                 "pidof tater-echo >/dev/null && pidof tater-show >/dev/null && "
                 "pidof tater-camera >/dev/null && echo TATER-UP", 5) or ""),
             hint=console.waiting_hint)
    note("Rook booted Tater Linux in slot A; TWRP remains in recovery")
    status = console.run("STORE=/store slotctl status; ip -4 addr show wlan0; tail -8 /data/tater-linux/tater-echo.log", 12)
    if status:
        print(status)
    print("\nFactory boot complete. Pair the Spot through its Tater setup screen/network.")
    print("The root slot stays on trial until it connects to Tater and passes its health window.")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify-bundle", action="store_true")
    parser.add_argument("--preflight-only", action="store_true")
    parser.add_argument("--reinstall-existing-tater", action="store_true")
    parser.add_argument("--serial")
    parser.add_argument("--lineage-zip", type=Path)
    parser.add_argument("--name")
    parser.add_argument("--backups", type=Path, default=ROOT / "backups")
    parser.add_argument("--work", type=Path, default=ROOT / "build")
    parser.add_argument("--adb", default="adb")
    parser.add_argument("--fastboot", default="fastboot")
    return parser.parse_args()


def main() -> int:
    try:
        install(parse_args())
    except (Fail, OSError, subprocess.CalledProcessError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    if "--repair" in sys.argv:
        if not (ROOT / "recovery_install.py").is_file():
            sys.path.insert(0, str(ROOT.parent))
        from recovery_install import main as recovery_main
        raise SystemExit(recovery_main())
    raise SystemExit(main())
