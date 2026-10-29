#!/usr/bin/env python3
"""Factory-install Tater Linux on Biscuit or Radar from TWRP."""

from __future__ import annotations

import argparse
import glob
import hashlib
import importlib.util
import json
from pathlib import Path
import shlex
import subprocess
import sys
import tarfile
import time


ROOT = Path(__file__).resolve().parent
TOOLS = ROOT / "tools"
sys.path.insert(0, str(TOOLS))


class Fail(Exception):
    """A user-facing installation failure."""


Adb = None
BOARD_TYPE_IDS = {}
build_boot_image = None
kernel_board = None


def load_tater_settings():
    candidates = (ROOT / "tater_settings.py", ROOT.parent / "tater_settings.py")
    path = next((candidate for candidate in candidates if candidate.is_file()), None)
    if path is None:
        raise Fail("the factory bundle is missing tater_settings.py")
    spec = importlib.util.spec_from_file_location("tater_factory_settings", path)
    if spec is None or spec.loader is None:
        raise Fail(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def md5(path: str | Path) -> str:
    digest = hashlib.md5()
    with Path(path).open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def head_is_android(path: str | Path) -> bool:
    with Path(path).open("rb") as source:
        return source.read(8) == b"ANDROID!"


def note(message: str) -> None:
    print(f"   {message}", flush=True)


def step(message: str) -> None:
    print(f"== {message}", flush=True)


PARTS = ["preloader", "kb", "dkb", "lk_a", "lk_b", "tee1", "tee2", "expdb", "misc", "persist", "boot_a", "boot_b", "recovery"]
CABLE = "Try another USB cable and port, connected directly rather than through a hub."

SLOT_SCRIPT = r'''set -e
T=$1
B=$T/busybox
S=/cache/techo5
mkdir -p $S/slots
rm -rf $S/slots/a $S/slots/b $S/slots/a.old $S/slots/b.old
mkdir -p $S/slots/a
cd $S/slots/a
$B tar xzf $T/rootfs.tar.gz
[ -x $S/slots/a/sbin/init ] || { echo "unpacked slot has no /sbin/init"; exit 1; }
[ -x $S/slots/a/usr/local/bin/tater-echo ] || { echo "unpacked slot has no tater-echo"; exit 1; }
: > $S/.techo5-store
echo good > $S/slots/a.state
echo a > $S/active
rm -f $S/rescue
sync
echo "slot a: $(cat $S/slots/a/etc/tater-release), $(ls $S/slots/a/bin | wc -l) commands in /bin"
'''


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
        raise Fail(f"cannot read bundle manifest: {error}") from error
    require(data.get("target") in ("biscuit", "radar") and data.get("base_os") == "tater-linux",
            "this is not a Biscuit/Radar Tater Linux factory bundle")
    require(isinstance(data.get("files"), dict) and bool(data["files"]), "bundle manifest has no files")
    for relative, metadata in data["files"].items():
        path = (ROOT / relative).resolve()
        require(ROOT.resolve() in path.parents, f"bundle path escapes its directory: {relative}")
        require(path.is_file() and path.stat().st_size == metadata.get("size"),
                f"bundle file is missing or has the wrong size: {relative}")
        require(sha256(path) == metadata.get("sha256"), f"bundle file failed SHA-256: {relative}")
    return data


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


def is_tater_linux_image(path: Path) -> bool:
    with path.open("rb") as source:
        header = source.read(576)
    return header.startswith(b"ANDROID!") and b"techo5" in header[64:576]


def partition_source(adb: Adb, by_name: str, part: str) -> str:
    command = (f"s={by_name}/{part}; [ -e ${{s}}_real ] && s=${{s}}_real; "
               f"case $(readlink $s) in /tmp/*) if [ {part} = preloader ]; "
               "then s=/dev/block/mmcblk0boot0; else s=; fi;; esac; echo $s")
    source = adb.sh(command)
    require(bool(source), f"{part} points at a TWRP decoy with no real partition beside it")
    return source


def save_backups(adb: Adb, by_name: str, folder: Path) -> Path:
    folder.mkdir(parents=True, exist_ok=True)
    for part in PARTS:
        source = partition_source(adb, by_name, part)
        fields = adb.sh(f"md5sum {shlex.quote(source)}").split()
        require(bool(fields) and len(fields[0]) == 32, f"could not checksum {part}")
        device_digest = fields[0]
        destination = folder / f"{part}.img"
        if destination.is_file() and md5(str(destination)) == device_digest:
            note(f"{part} backup already verified")
            continue
        prior = [Path(path) for path in glob.glob(str(folder / f"{part}-*.img"))
                 if md5(path) == device_digest]
        if prior:
            note(f"{part} already backed up as {prior[0].name}")
            continue
        if destination.is_file() and part == "recovery" and head_is_android(str(destination)):
            note("recovery: preserving the earlier TWRP backup")
            continue
        output = destination if not destination.exists() else folder / f"{part}-{time.strftime('%Y%m%d-%H%M%S')}.img"
        partial = output.with_suffix(output.suffix + ".partial")
        try:
            require(adb.exec_out_to_file(f"cat {shlex.quote(source)}", str(partial)) == 0,
                    f"reading {part} failed. {CABLE}")
            require(md5(str(partial)) == device_digest,
                    f"{part} backup differs from the device. {CABLE}")
            partial.replace(output)
        finally:
            partial.unlink(missing_ok=True)
        note(f"{part} backed up and checksum-verified")
    recovery = folder / "recovery.img"
    require(recovery.is_file() and head_is_android(str(recovery)), "recovery backup is not an Android boot image")
    require(not is_tater_linux_image(recovery),
            "the saved recovery image is already Tater Linux, not TWRP; restore TWRP and run again")
    return recovery


def tar_member(archive_path: Path, member: str, destination: Path) -> None:
    with tarfile.open(archive_path, "r:*") as archive:
        found = next((item for item in archive.getmembers() if item.name.lstrip("./") == member), None)
        require(found is not None and found.isfile(), f"{archive_path.name} has no {member}")
        source = archive.extractfile(found)
        require(source is not None, f"cannot read {member}")
        destination.parent.mkdir(parents=True, exist_ok=True)
        with source, destination.open("wb") as output:
            while chunk := source.read(1024 * 1024):
                output.write(chunk)
        destination.chmod(0o755)


def extract_apks(archive_path: Path, destination: Path) -> None:
    destination.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive_path, "r:*") as archive:
        for member in archive.getmembers():
            name = Path(member.name)
            require(not name.is_absolute() and ".." not in name.parts,
                    f"unsafe rescue package path: {member.name}")
            if member.isfile() and name.suffix == ".apk":
                source = archive.extractfile(member)
                require(source is not None, f"cannot read rescue package {member.name}")
                with source, (destination / name.name).open("wb") as output:
                    while chunk := source.read(1024 * 1024):
                        output.write(chunk)


def push_checked(adb: Adb, source: Path, remote: str) -> None:
    adb.push(str(source), remote)
    fields = adb.sh(f"sha256sum {shlex.quote(remote)}").split()
    require(bool(fields) and fields[0] == sha256(source), f"{source.name} changed during transfer")


def mount_install_storage(adb: Adb, userdata: str, cache: str) -> bool:
    """Mount required storage without modifying an unformatted cache."""
    userdata = shlex.quote(userdata)
    cache = shlex.quote(cache)
    data_status = adb.sh(
        "mkdir -p /data /cache; "
        "grep -q ' /data ' /proc/mounts || "
        f"mount -t ext4 {userdata} /data 2>/dev/null; "
        "grep -q ' /data ' /proc/mounts && echo TATER_DATA_READY"
    )
    require("TATER_DATA_READY" in data_status, "TWRP could not mount userdata")
    cache_status = adb.sh(
        "grep -q ' /cache ' /proc/mounts || "
        f"mount -t ext4 {cache} /cache 2>/dev/null; "
        "grep -q ' /cache ' /proc/mounts && echo TATER_CACHE_READY"
    )
    return "TATER_CACHE_READY" in cache_status


def initialize_cache(adb: Adb, cache: str) -> None:
    """Create cache only after the reset has been explicitly authorized."""
    cache = shlex.quote(cache)
    output = adb.sh(
        "umount /cache 2>/dev/null || true; "
        f"/sbin/mke2fs -F -t ext4 -L cache {cache} 2>&1 && "
        "mkdir -p /cache && "
        f"mount -t ext4 {cache} /cache 2>&1 && "
        "grep -q ' /cache ' /proc/mounts && sync && echo TATER_CACHE_READY"
    )
    require("TATER_CACHE_READY" in output,
            f"TWRP could not initialize the cache filesystem: {output}")


def install(args: argparse.Namespace) -> None:
    global Adb, BOARD_TYPE_IDS, Fail, build_boot_image, head_is_android
    global kernel_board, md5, note, step
    info = manifest()
    if args.verify_bundle:
        print(f"Verified {info['target']} Tater Linux factory bundle {info['version']}; no device writes were made.")
        return
    import techo5lib  # noqa: E402
    import dotimage  # noqa: E402
    Adb, Fail = techo5lib.Adb, techo5lib.Fail
    head_is_android, md5 = techo5lib.head_is_android, techo5lib.md5
    note, step = techo5lib.note, techo5lib.step
    BOARD_TYPE_IDS = dotimage.BOARD_TYPE_IDS
    build_boot_image, kernel_board = dotimage.build_boot_image, dotimage.kernel_board
    serial = select_serial(args.serial, args.adb)
    adb = Adb(serial, args.adb)
    require(adb.state() == "recovery", f"{serial} is not in TWRP")
    require(adb.sh("id").startswith("uid=0"), "TWRP adb is not root")
    product = adb.sh("getprop ro.product.device; getprop ro.build.product")
    board = "radar" if "radar" in product else "biscuit" if "biscuit" in product else ""
    require(board == info["target"], f"bundle is for {info['target']}, but the connected unit is {board or 'unknown'}")
    type_id = adb.sh("cat /proc/idme/device_type_id 2>/dev/null").replace("\0", "").strip()
    if type_id:
        require(BOARD_TYPE_IDS.get(type_id) == board,
                f"device type id {type_id!r} does not identify {board}")
    by_name = "/dev/block/platform/bootdevice/by-name"
    if adb.sh(f"test -e {by_name}/recovery && echo yes") != "yes":
        by_name = adb.sh("for d in /dev/block/platform/*/by-name /dev/block/by-name; do "
                         "[ -e $d/recovery ] && { echo $d; break; }; done")
    require(bool(by_name), "TWRP exposes no by-name partition directory")
    userdata_part = partition_source(adb, by_name, "userdata")
    cache_part = partition_source(adb, by_name, "cache")
    cache_ready = mount_install_storage(adb, userdata_part, cache_part)
    note(f"{board} {serial}, in TWRP")
    if not cache_ready:
        note("cache has no mountable filesystem; it will be initialized only after ERASE is confirmed")

    backup_root = Path(args.backups).expanduser().resolve() / serial
    step(f"verified backups to {backup_root}")
    recovery = save_backups(adb, by_name, backup_root)

    payload = ROOT / "payload"
    rootfs = payload / "rootfs.tar.gz"
    kernel = payload / "kernel.zImage-dtb"
    require(kernel_board(str(kernel)) == board, f"bundled kernel is not for {board}")
    work = Path(args.work).expanduser().resolve() / f"{board}-{serial}"
    work.mkdir(parents=True, exist_ok=True)
    busybox, wmtup = work / "busybox.static", work / "wmtup"
    tar_member(rootfs, "bin/busybox.static", busybox)
    tar_member(rootfs, "usr/local/bin/wmtup", wmtup)
    apks = work / "rescue-apks"
    extract_apks(payload / "rescue-apks.tar", apks)
    image = backup_root / "tater-linux.img"
    build_boot_image(str(recovery), str(image), str(payload / "alpine-minirootfs.tar.gz"),
                     str(busybox), str(wmtup), str(apks), str(kernel))
    require(image.stat().st_size <= 16 * 1024 * 1024, "built Linux recovery image exceeds the partition")
    note(f"built this unit's {image.stat().st_size}-byte Linux boot image")
    if args.dry_run:
        print(f"Dry run complete; nothing was written to {serial}.\nBoot image: {image}\nRootfs: {rootfs}")
        return
    settings = load_tater_settings()
    saved_settings = None
    if not args.factory_reset:
        try:
            saved_settings = settings.capture(adb, backup_root / "tater-settings.tar")
        except settings.SettingsError as error:
            raise Fail(str(error)) from error
        if saved_settings:
            note(f"Tater setup saved and verified: {saved_settings.archive}")
        else:
            note("no existing Tater setup was found; this unit will start clean")
    if not args.yes:
        print(f"\nAbout to reset {board} {serial} to Tater Linux {info['version']}.")
        if args.factory_reset:
            print("Wi-Fi, pairing, SSH, and satellite settings will be erased (--factory-reset).")
        elif saved_settings:
            print("Tater pairing, Wi-Fi, SSH, and satellite settings will be restored afterward.")
        else:
            print("No existing Tater setup was found; the satellite will enter setup mode.")
        print("Stock system partitions and verified backups are retained.")
        require(input("Type ERASE to continue: ").strip() == "ERASE", "installation cancelled")

    if not cache_ready:
        step("initializing cache for Tater Linux")
        initialize_cache(adb, cache_part)
        note("cache formatted as ext4 and mounted")

    stage = "/tmp/tater-linux"
    step("installing Linux boot image")
    adb.sh(f"mkdir -p {stage} /cache/techo5")
    # Put the recovery route on persistent storage before replacing recovery.
    # If a later rootfs extraction fails, this verified TWRP image is already
    # available to the rescue console instead of existing only on the host.
    push_checked(adb, recovery, "/cache/techo5/twrp.img")
    push_checked(adb, TOOLS / "linux/back-to-linux.sh", "/cache/techo5/back-to-linux.sh")
    push_checked(adb, image, "/cache/techo5/linux.img")
    push_checked(adb, busybox, stage + "/busybox")
    recovery_part = partition_source(adb, by_name, "recovery")
    image_md5 = md5(str(image))
    result = adb.sh(f"chmod 755 {stage}/busybox; dd if=/cache/techo5/linux.img of={recovery_part} "
                    f"bs=1048576 2>/dev/null; sync; {stage}/busybox head -c {image.stat().st_size} "
                    f"{recovery_part} | md5sum").split()
    require(bool(result) and result[0] == image_md5, "Linux recovery image failed its read-back check")

    step("installing Tater Linux rootfs slot a")
    push_checked(adb, rootfs, stage + "/rootfs.tar.gz")
    slot_script = work / "slot.sh"
    slot_script.write_text(SLOT_SCRIPT)
    push_checked(adb, slot_script, stage + "/slot.sh")
    output = adb.sh(f"sh {stage}/slot.sh {stage} 2>&1")
    require("slot a: " in output, f"installing rootfs failed: {output}")
    note(output)

    step("restoring Tater setup" if saved_settings else "starting with clean Tater setup")
    try:
        if saved_settings:
            settings.restore(adb, saved_settings)
            note("pairing, Wi-Fi, SSH, and satellite settings restored")
        else:
            settings.clear(adb)
            note("prior Tater setup cleared")
    except settings.SettingsError as error:
        raise Fail(str(error)) from error
    adb.sh("rm -f /data/local/bin/server /data/local/bin/server_a "
           "/data/local/bin/server_b /data/local/bin/start_server.sh; sync")

    step("arming Tater Linux")
    adb.sh(f"printf 'TECHO5-TRIES 0 installed\\n' | dd of={by_name}/misc bs=512 seek=14 count=1 "
           f"conv=notrunc 2>/dev/null; rm -rf {stage}; sync")
    adb.reboot("recovery")
    if saved_settings and saved_settings.paired:
        print("\nTater Linux is installed with this satellite's existing Tater pairing and settings.")
    else:
        print("\nTater Linux is installed. Wait for the ring to enter setup mode, join the")
        print("Tater-Setup-XXXX Wi-Fi network, open http://192.168.4.1, and pair it with Tater.")
    print(f"Verified recovery backups: {backup_root}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial")
    parser.add_argument("--adb", default="adb")
    parser.add_argument("--backups", default=str(ROOT / "backups"))
    parser.add_argument("--work", default=str(ROOT / "build"))
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--verify-bundle", action="store_true")
    parser.add_argument("--factory-reset", action="store_true",
                        help="erase Wi-Fi, pairing, SSH, and satellite settings instead of preserving them")
    parser.add_argument("--yes", action="store_true", help="skip the ERASE prompt")
    args = parser.parse_args()
    try:
        install(args)
    except Fail as error:
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
