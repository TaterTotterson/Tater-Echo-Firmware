#!/usr/bin/env python3
"""Build the Biscuit/Radar Tater Linux rootfs from a pinned TECHO5 Dot base."""

from __future__ import annotations

import argparse
import gzip
import hashlib
from pathlib import Path
import shutil
import sys
import tarfile
import tempfile
from urllib import request as urlrequest

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO))
from tools import platform_lock  # noqa: E402

PLATFORM = platform_lock.load()["platforms"]["dot"]
PLATFORM_VERSION = PLATFORM["release"]
PLATFORM_SHA256 = PLATFORM["rootfs"]["sha256"]
ALPINE_APK_BASE = "https://dl-cdn.alpinelinux.org/alpine/v3.24/main/armv7/"
SETUP_PACKAGES = (
    ("hostapd-2.11-r4.apk", "f932ff857358b3b800c8523217a5aba196c2988bf09e20bae2da5ddd66979efe", "usr/sbin/hostapd"),
    ("dnsmasq-2.92_p2-r0.apk", "bd796346d062004f2d5645bfb0919fae1eebb709793e7dcae011bd804537dfe8", "usr/sbin/dnsmasq"),
    ("iw-6.17-r0.apk", "7b045ae3a00dfd22859cc8560ac4a614a7b104a3dd0aee6f5e82574b9eb500e6", "usr/sbin/iw"),
)
CXX_RUNTIME_PACKAGE = (
    "libstdc++-15.2.0-r5.apk",
    "c57f103f92812e2c6c8fb81ffa61df3dcb08a49917a2284aff64bddeade6f310",
    "usr/lib/libstdc++.so.6.0.34",
)
ALSA_CAPTURE_PACKAGE = (
    "alsa-utils-1.2.15.2-r1.apk",
    "d297c4da007497f8a10f20f3eb82811ee705039a860198202d3ae5d77a94a596",
    "usr/bin/aplay",
)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def copy(source: Path, destination: Path, mode: int) -> None:
    if not source.is_file():
        raise SystemExit(f"required input is missing: {source}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    destination.chmod(mode)


def write(path: Path, contents: str, mode: int = 0o644) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(contents)
    path.chmod(mode)


def alpine_file(
    cache: Path,
    name: str,
    digest: str,
    member: str,
    destination: Path,
    mode: int = 0o755,
) -> None:
    """Install one file from a pinned Alpine package."""
    cache.mkdir(parents=True, exist_ok=True)
    package = cache / name
    if not package.is_file() or sha256(package) != digest:
        with tempfile.NamedTemporaryFile(prefix=name + ".", suffix=".partial", dir=cache, delete=False) as temporary:
            partial = Path(temporary.name)
        try:
            with urlrequest.urlopen(ALPINE_APK_BASE + name, timeout=30) as response, partial.open("wb") as output:
                shutil.copyfileobj(response, output, length=1024 * 1024)
            if sha256(partial) != digest:
                raise SystemExit(f"pinned Alpine package hash mismatch: {name}")
            partial.replace(package)
        finally:
            partial.unlink(missing_ok=True)
    with tarfile.open(package, "r:gz") as archive:
        source = archive.extractfile(member)
        if source is None:
            raise SystemExit(f"{name} does not contain {member}")
        destination.parent.mkdir(parents=True, exist_ok=True)
        with source, destination.open("wb") as output:
            shutil.copyfileobj(source, output)
        destination.chmod(mode)


def safe_extract(archive: tarfile.TarFile, destination: Path) -> None:
    # The archive is accepted only after its pinned SHA-256 is verified. Keep
    # Alpine's normal absolute BusyBox links while independently refusing any
    # member name that would be written outside the extraction directory.
    root = destination.resolve()
    for member in archive.getmembers():
        target = (destination / member.name).resolve()
        if target != root and root not in target.parents:
            raise SystemExit(f"unsafe path in TECHO5 Dot rootfs: {member.name}")
    archive.extractall(destination, filter="fully_trusted")


def deterministic_tar(root: Path, output: Path) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0, compresslevel=9) as zipped:
            with tarfile.open(fileobj=zipped, mode="w", format=tarfile.PAX_FORMAT) as archive:
                for item in [root] + sorted(root.rglob("*")):
                    name = "." if item == root else "./" + item.relative_to(root).as_posix()
                    info = archive.gettarinfo(str(item), name)
                    info.uid = info.gid = 0
                    info.uname = info.gname = "root"
                    info.mtime = 0
                    if item.is_file() and not item.is_symlink():
                        with item.open("rb") as source:
                            archive.addfile(info, source)
                    else:
                        archive.addfile(info)


def replace_once(path: Path, old: str, new: str, description: str) -> None:
    contents = path.read_text()
    if contents.count(old) != 1:
        raise SystemExit(f"pinned TECHO5 Dot {description} changed unexpectedly")
    path.write_text(contents.replace(old, new, 1))


def patch_platform(root: Path) -> None:
    boot = root / "etc/techo5/boot.sh"
    contents = boot.read_text()
    # Tater owns one persistent namespace. The low-level TECHO5 slot store
    # remains /store/techo5 because it is part of the recovery boot contract.
    contents = contents.replace("/data/techo5-linux", "/data/tater-linux")
    contents = contents.replace("/data/misc/echolocal", "/data/local/etc/tater")
    contents = contents.replace("echod", "tater-echo")
    contents = contents.replace("the daemon", "the Tater daemon")

    # Radar's current Fire OS system image is system_a (p13), while Biscuit's
    # known-good donor is system_b (p14).  The two generations also store the
    # same vendor files under different directory layouts.  Probe both slots,
    # preferring the board's normal one, so a factory reset can always adopt
    # the unit's own Wi-Fi firmware and audio calibration.
    old_system = "SYSTEM=/dev/mmcblk0p14   # Android's system: the Wi-Fi firmware is adopted from it, once"
    new_system = """SYSTEM_A=/dev/mmcblk0p13 # Fire OS system_a: Radar's current firmware donor
SYSTEM_B=/dev/mmcblk0p14 # Fire OS system_b: Biscuit's normal firmware donor
SYSTEM_PRIMARY=$SYSTEM_B
SYSTEM_SECONDARY=$SYSTEM_A"""
    if contents.count(old_system) != 1:
        raise SystemExit("pinned TECHO5 Dot boot script lost its Android system partition")
    contents = contents.replace(old_system, new_system, 1)

    old_node = "$BB mknod -m 600 $SYSTEM b 179 14 2>/dev/null"
    new_node = """$BB mknod -m 600 $SYSTEM_A b 179 13 2>/dev/null
$BB mknod -m 600 $SYSTEM_B b 179 14 2>/dev/null"""
    if contents.count(old_node) != 1:
        raise SystemExit("pinned TECHO5 Dot boot script lost its Android system device node")
    contents = contents.replace(old_node, new_node, 1)

    board_marker = 'say "board $BOARD"'
    system_probe = r'''say "board $BOARD"

if [ "$BOARD" = radar ]; then
	SYSTEM_PRIMARY=$SYSTEM_A
	SYSTEM_SECONDARY=$SYSTEM_B
fi

# Mount the first retained Fire OS system slot that contains the MediaTek
# firmware. Fire OS 6 uses system/vendor/* beneath a filesystem root, while
# the older system image exposes etc/* and vendor/* directly.
mount_vendor_system() {
	$BB umount /android 2>/dev/null
	for candidate in $SYSTEM_PRIMARY $SYSTEM_SECONDARY; do
		if $BB mount -t ext4 -o ro,noatime "$candidate" /android 2>/dev/null; then
			if [ -e /android/system/vendor/firmware/WIFI_RAM_CODE_8163 ]; then
				ANDROID_FIRMWARE=/android/system/vendor/firmware
				ANDROID_AUDIO_ALGORITHMS=/android/system/vendor/etc/audio-algorithms
				ANDROID_AUDIO_DEVICE=/android/system/etc/audio_device.xml
				ANDROID_SYSTEM_PARTITION=$candidate
				return 0
			fi
			if [ -e /android/etc/firmware/WIFI_RAM_CODE_8163 ]; then
				ANDROID_FIRMWARE=/android/etc/firmware
				ANDROID_AUDIO_ALGORITHMS=/android/vendor/etc/audio-algorithms
				ANDROID_AUDIO_DEVICE=/android/etc/audio_device.xml
				ANDROID_SYSTEM_PARTITION=$candidate
				return 0
			fi
			$BB umount /android
		fi
	done
	return 1
}'''
    if contents.count(board_marker) != 1:
        raise SystemExit("pinned TECHO5 Dot boot script lost its board marker")
    contents = contents.replace(board_marker, system_probe, 1)

    replacements = (
        (
            "elif $BB mount -t ext4 -o ro,noatime $SYSTEM /android 2>/dev/null; then",
            "elif mount_vendor_system; then",
            2,
        ),
        (
            "if $BB mount -t ext4 -o ro,noatime $SYSTEM /android 2>/dev/null; then",
            "if mount_vendor_system; then",
            1,
        ),
        (
            "$BB cp /android/system/vendor/etc/audio-algorithms/* $AA/ 2>/dev/null",
            '$BB cp "$ANDROID_AUDIO_ALGORITHMS"/* $AA/ 2>/dev/null',
            1,
        ),
        (
            "$BB cp /android/system/etc/audio_device.xml $S/audio_device.xml.new 2>/dev/null",
            '$BB cp "$ANDROID_AUDIO_DEVICE" $S/audio_device.xml.new 2>/dev/null',
            1,
        ),
        (
            "$BB cp /android/system/vendor/firmware/* $FW/ 2>/dev/null",
            '$BB cp "$ANDROID_FIRMWARE"/* $FW/ 2>/dev/null',
            1,
        ),
    )
    for old, new, expected in replacements:
        if contents.count(old) != expected:
            raise SystemExit(f"pinned TECHO5 Dot vendor adoption changed unexpectedly: {old}")
        contents = contents.replace(old, new)

    mdev_marker = "$BB mdev -s\necho 7 4 1 7 > /proc/sys/kernel/printk"
    mdev_replacement = "$BB mdev -s\n/usr/local/sbin/tater-boot-ring &\necho 7 4 1 7 > /proc/sys/kernel/printk"
    if contents.count(mdev_marker) != 1:
        raise SystemExit("pinned TECHO5 Dot boot script lost its mdev marker")
    contents = contents.replace(mdev_marker, mdev_replacement, 1)

    ready = "$BB touch /run/techo5/ready\nsay \"ready at $($BB date)\""
    health = r'''$BB touch /run/techo5/ready
say "ready at $($BB date)"

# Routine firmware OTA uses application slots on userdata. Commit only after
# the new daemon has remained connected to Tater; a fast-crashing or offline
# build rolls back without touching the Linux rootfs slot.
(
	APP=/data/tater-linux/app
	[ -s "$APP/pending.env" ] || exit 0
	start=$($BB cut -d. -f1 /proc/uptime)
	while [ -s "$APP/pending.env" ]; do
		$BB sleep 15
		now=$($BB cut -d. -f1 /proc/uptime)
		pid=$($BB pidof tater-echo | $BB cut -d' ' -f1)
		started=$($BB awk '{print int($22/100)}' /proc/$pid/stat 2>/dev/null)
		if [ -s /run/tater-connected ] && [ -n "$started" ] && [ $((now-started)) -ge 300 ]; then
			echo "tater-dot boot: application healthy; committing" > /dev/kmsg
			tater-app commit >> /data/tater-linux/boot.log 2>&1
			exit 0
		fi
		if [ $((now-start)) -ge 900 ]; then
			echo "tater-dot boot: application health timeout; rolling back" > /dev/kmsg
			if tater-app rollback >> /data/tater-linux/boot.log 2>&1; then
				/usr/local/bin/tater-reboot-now --directory "$APP"
			fi
			exit 1
		fi
	done
) &'''
    if contents.count(ready) != 1:
        raise SystemExit("pinned TECHO5 Dot boot script lost its ready marker")
    contents = contents.replace(ready, health, 1)
    boot.write_text(contents)

    bluetooth = root / "usr/local/sbin/techo5-bt"
    bluetooth.write_text(bluetooth.read_text().replace("/data/techo5-linux", "/data/tater-linux"))
    console = root / "usr/local/sbin/techo5-console"
    console.write_text(console.read_text().replace("/data/techo5-linux", "/data/tater-linux"))
    logs = root / "usr/local/sbin/techo5-logs"
    logs.write_text(logs.read_text().replace("/data/techo5-linux", "/data/tater-linux"))

    slotctl = root / "usr/local/sbin/slotctl"
    slot_contents = slotctl.read_text()
    slot_contents = slot_contents.replace("usr/local/bin/echod", "usr/local/bin/tater-echo")
    slotctl.write_text(slot_contents)

    shutdown = root / "etc/techo5/shutdown.sh"
    shutdown.write_text(shutdown.read_text().replace("echod", "tater-echo"))
    write(root / "etc/motd", "Tater Linux for Echo Dot 2 and Echo 2. State is under /data/tater-linux; rootfs slots are managed with slotctl.\n")


INITTAB = """# Tater Linux for Biscuit and Radar — BusyBox init.
::sysinit:/etc/techo5/boot.sh
::respawn:/usr/local/sbin/tater-run
::respawn:/usr/local/sbin/tater-usb-ethernet
::respawn:/usr/local/sbin/techo5-console
::respawn:/usr/local/sbin/techo5-logs
::ctrlaltdel:/usr/local/bin/tater-reboot-now
::shutdown:/etc/techo5/shutdown.sh
"""


TATER_RUN = r'''#!/bin/sh
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
export TATER_NATIVE_CONFIG=/data/local/etc/tater/native.json
export TATER_MWW_DIR=/data/local/share/tater/microwakeword
LOG=/data/tater-linux/tater-echo.log
mkdir -p /data/tater-linux /data/local/etc/tater /data/local/share/tater/microwakeword
for source in /usr/share/tater/microwakeword/*; do
	target=/data/local/share/tater/microwakeword/${source##*/}
	[ -e "$target" ] || cp "$source" "$target"
done

# Apply the board's thermal and CPU policy once per boot. The operation is
# idempotent and deliberately finishes before audio is opened.
if [ ! -e /run/tater-platform-initialized ]; then
	/usr/local/bin/tater-echo platform-init >> "$LOG" 2>&1 || true
	: > /run/tater-platform-initialized
fi

if [ ! -s /data/local/etc/tater/native.json ]; then
	echo "tater-run: clean install; starting pairing hotspot" >> "$LOG"
	/usr/local/sbin/tater-setup-ap >> "$LOG" 2>&1 || \
		echo "tater-run: hotspot unavailable; USB recovery remains available" >> "$LOG"
	exec /usr/local/bin/tater-echo setup-mode >> "$LOG" 2>&1
fi

# Tater attaches its active GATT bridge once at daemon start. Give the
# background BlueZ bring-up a bounded chance to settle so Radar/Biscuit do not
# permanently lose BLE GATT after a slow cold boot. A kernel without Bluetooth
# also writes the marker immediately through techo5-bt's EXIT trap.
n=0
while [ ! -e /run/techo5/bt-settled ] && [ "$n" -lt 45 ]; do
	sleep 1
	n=$((n+1))
done

if [ -f "$LOG" ] && [ "$(stat -c %s "$LOG" 2>/dev/null || echo 0)" -gt 5000000 ]; then
	mv -f "$LOG" "$LOG.1"
fi
attempt=0
while true; do
	binary=$(tater-app binary tater-echo)
	started=$(cut -d. -f1 /proc/uptime)
	echo "tater-run: starting $($binary version 2>/dev/null | head -1) at $(date)" >> "$LOG"
	$binary >> "$LOG" 2>&1
	status=$?
	stopped=$(cut -d. -f1 /proc/uptime)
	runtime=$((stopped-started))
	if [ "$runtime" -ge 15 ]; then attempt=0; else attempt=$((attempt+1)); fi
	echo "tater-run: exit=$status runtime=${runtime}s fast-attempt=$attempt" >> "$LOG"
	if [ "$attempt" -ge 3 ] && tater-app rollback >> "$LOG" 2>&1; then
		/usr/local/bin/tater-reboot-now --directory /data/tater-linux/app
	fi
	sleep 2
done
'''


TATER_APP = r'''#!/bin/sh
APP=/data/tater-linux/app
case "$1" in
binary)
	[ "$2" = tater-echo ] || exit 2
	current=$(readlink -f "$APP/current" 2>/dev/null)
	if [ -n "$current" ] && [ -x "$current/tater-echo" ]; then
		echo "$current/tater-echo"
	else
		echo /usr/local/bin/tater-echo
	fi
	;;
rollback)
	[ -s "$APP/pending.env" ] || exit 1
	previous=$(sed -n 's/^previous=//p' "$APP/pending.env" | head -1)
	case "$previous" in
	system) rm -f "$APP/current" ;;
	a|b)
		[ -x "$APP/slots/$previous/tater-echo" ] || exit 1
		ln -s "slots/$previous" "$APP/current.rollback" || exit 1
		rm -f "$APP/current" || exit 1
		mv -f "$APP/current.rollback" "$APP/current" || exit 1
		;;
	*) exit 1 ;;
	esac
	rm -f "$APP/pending.env"
	echo "tater-app: rolled back to $previous"
	;;
commit)
	[ -e "$APP/pending.env" ] || exit 0
	rm -f "$APP/pending.env"
	echo "tater-app: application slot committed"
	;;
*) echo "usage: tater-app binary tater-echo | rollback | commit" >&2; exit 2 ;;
esac
'''


BOOT_RING = r'''#!/bin/sh
# Replace the inherited blue kernel spinner as soon as the LED sysfs nodes
# exist. The application controller creates /run/tater-led-ready when it takes
# ownership; until then this remains a visible, Tater-coloured boot heartbeat.
BB=/bin/busybox.static
LED=/sys/devices/soc/11007000.i2c/i2c-0/0-003f
FRAME=$LED/frame
[ -e "$FRAME" ] || exit 0
echo 0 > "$LED/boot_animation" 2>/dev/null || true
echo 3 > "$LED/led_current" 2>/dev/null || true
orange=601800
white=FFFFFF
position=0
while [ ! -e /run/tater-led-ready ]; do
	frame=
	i=0
	while [ "$i" -lt 12 ]; do
		if [ "$i" -eq "$position" ]; then frame=${frame}${white}; else frame=${frame}${orange}; fi
		i=$((i + 1))
	done
	printf '%s' "$frame" > "$FRAME" 2>/dev/null || exit 0
	position=$(((position + 1) % 12))
	$BB usleep 88000
done
'''


OS_RELEASE = '''NAME="Tater Linux"
ID=tater-linux
VERSION="{version}"
VERSION_ID="{version}"
PRETTY_NAME="Tater Linux {target} {version}"
HOME_URL="https://github.com/TaterTotterson/Tater-Echo-Firmware"
'''


NOTICE = """This root filesystem uses the Linux boot, MediaTek Wi-Fi/Bluetooth
enablement, and A/B rootfs tooling from TECHO5 Dot by HuskerMinion under the
MIT License. Source: https://github.com/HuskerMinion/techo5-dot

TECHO5 Dot includes work derived from EchoLocal. The complete upstream MIT
notices are included beside this file.
"""


def build(args: argparse.Namespace) -> None:
    if sha256(args.base_rootfs) != PLATFORM_SHA256:
        raise SystemExit(f"base rootfs is not pinned TECHO5 Dot {PLATFORM_VERSION}")
    with tempfile.TemporaryDirectory(prefix=f"tater-{args.target}-linux-") as temporary:
        root = Path(temporary) / "root"
        root.mkdir()
        with tarfile.open(args.base_rootfs, "r:gz") as archive:
            safe_extract(archive, root)

        # Replace the upstream application completely; only its proven Linux
        # hardware platform and operating-system services remain.
        (root / "usr/local/bin/echod").unlink(missing_ok=True)
        (root / "usr/local/bin/techo5-librespot").unlink(missing_ok=True)
        (root / "usr/local/sbin/techo5-run").unlink(missing_ok=True)
        shutil.rmtree(root / "usr/share/techo5/models", ignore_errors=True)
        localtime = root / "etc/localtime"
        localtime.unlink(missing_ok=True)
        localtime.symlink_to("/usr/share/zoneinfo/UTC")
        timezone = root / "etc/timezone"
        timezone.unlink(missing_ok=True)
        write(timezone, "UTC\n")
        copy(args.server, root / "usr/local/bin/tater-echo", 0o755)
        copy(args.reboot, root / "usr/local/bin/tater-reboot-now", 0o755)
        copy(args.mww_runtime, root / "usr/share/tater/microwakeword/libtater_microwakeword.so", 0o755)
        copy(args.onnx_runtime, root / "usr/share/tater/microwakeword/libonnxruntime.so", 0o755)
        for source, name in (
            (args.hey_tater_model, "hey_tater.tflite"),
            (args.hey_tater_manifest, "hey_tater.json"),
            (args.hey_tater_oww_onnx, "hey_tater.oww.onnx"),
            (args.hey_tater_oww_metadata, "hey_tater.oww.json"),
            (args.hey_tater_nww_cnn, "hey_tater.nww.cnn.onnx"),
            (args.hey_tater_nww_gru, "hey_tater.nww.gru.onnx"),
            (args.hey_tater_bundle, "hey_tater.wake-bundle.json"),
            (args.stop_model, "stop.tflite"),
            (args.stop_manifest, "stop.json"),
            (args.oww_melspectrogram_onnx, "melspectrogram.onnx"),
            (args.oww_embedding_onnx, "embedding_model.onnx"),
        ):
            copy(source, root / "usr/share/tater/microwakeword" / name, 0o644)
        copy(args.tinyalsa, root / "usr/lib/libtinyalsa.so.2.0.0", 0o755)
        for name in ("libtinyalsa.so", "libtinyalsa.so.2"):
            link = root / "usr/lib" / name
            link.unlink(missing_ok=True)
            link.symlink_to("libtinyalsa.so.2.0.0")

        copy(args.setup_ap, root / "usr/local/sbin/tater-setup-ap", 0o755)
        copy(args.setup_ap_base, root / "usr/local/sbin/tater-setup-ap-base", 0o755)
        copy(args.network, root / "usr/local/sbin/tater-net", 0o755)
        copy(args.eth, root / "usr/local/sbin/tater-usb-ethernet", 0o755)
        copy(Path(__file__).parents[1] / "common/tater-usb-inventory",
             root / "usr/local/sbin/tater-usb-inventory", 0o755)
        for old_name, tater_name in (
            ("techo5-net", "tater-net"),
            ("techo5-eth", "tater-usb-ethernet"),
        ):
            compatibility = root / "usr/local/sbin" / old_name
            compatibility.unlink(missing_ok=True)
            compatibility.symlink_to(tater_name)
        copy(args.wifi_set, root / "usr/local/sbin/wifi-set", 0o755)
        for name, digest, member in SETUP_PACKAGES:
            alpine_file(args.apk_cache, name, digest, member, root / member)
        name, digest, member = CXX_RUNTIME_PACKAGE
        alpine_file(args.apk_cache, name, digest, member, root / member)
        cxx_link = root / "usr/lib/libstdc++.so.6"
        cxx_link.unlink(missing_ok=True)
        cxx_link.symlink_to(Path(member).name)
        name, digest, member = ALSA_CAPTURE_PACKAGE
        alpine_file(args.apk_cache, name, digest, member, root / member)
        arecord = root / "usr/bin/arecord"
        arecord.unlink(missing_ok=True)
        arecord.symlink_to("aplay")
        write(root / "etc/inittab", INITTAB, 0o644)
        write(root / "usr/local/sbin/tater-run", TATER_RUN, 0o755)
        write(root / "usr/local/sbin/tater-app", TATER_APP, 0o755)
        write(root / "usr/local/sbin/tater-boot-ring", BOOT_RING, 0o755)
        write(root / "etc/hostname", f"tater-{args.target}\n")
        write(root / "usr/lib/os-release", OS_RELEASE.format(version=args.version, target=args.target.title()))
        release = f"Tater Linux {args.target.title()} {args.version} (TECHO5 Dot {PLATFORM_VERSION})\n"
        write(root / "etc/tater-release", release)
        write(root / "etc/techo5-release", release)
        copy(args.platform_license, root / "usr/share/licenses/tater-linux/TECHO5-DOT-LICENSE", 0o644)
        copy(args.platform_notice, root / "usr/share/licenses/tater-linux/TECHO5-DOT-NOTICE", 0o644)
        write(root / "usr/share/licenses/tater-linux/NOTICE", NOTICE)
        patch_platform(root)
        deterministic_tar(root, args.output)

    print(f"built {args.output} ({args.output.stat().st_size} bytes, sha256 {sha256(args.output)})")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", choices=("biscuit", "radar"), required=True)
    parser.add_argument("--base-rootfs", type=Path, required=True)
    parser.add_argument("--server", type=Path, required=True)
    parser.add_argument("--reboot", type=Path, required=True)
    parser.add_argument("--mww-runtime", type=Path, required=True)
    parser.add_argument("--onnx-runtime", type=Path, required=True)
    parser.add_argument("--hey-tater-model", type=Path, required=True)
    parser.add_argument("--hey-tater-manifest", type=Path, required=True)
    parser.add_argument("--hey-tater-oww-onnx", type=Path, required=True)
    parser.add_argument("--hey-tater-oww-metadata", type=Path, required=True)
    parser.add_argument("--hey-tater-nww-cnn", type=Path, required=True)
    parser.add_argument("--hey-tater-nww-gru", type=Path, required=True)
    parser.add_argument("--hey-tater-bundle", type=Path, required=True)
    parser.add_argument("--stop-model", type=Path, required=True)
    parser.add_argument("--stop-manifest", type=Path, required=True)
    parser.add_argument("--oww-melspectrogram-onnx", type=Path, required=True)
    parser.add_argument("--oww-embedding-onnx", type=Path, required=True)
    parser.add_argument("--tinyalsa", type=Path, required=True)
    parser.add_argument("--setup-ap", type=Path, required=True)
    parser.add_argument("--setup-ap-base", type=Path, required=True)
    parser.add_argument("--network", type=Path, required=True)
    parser.add_argument("--eth", type=Path, required=True)
    parser.add_argument("--wifi-set", type=Path, required=True)
    parser.add_argument("--apk-cache", type=Path, required=True)
    parser.add_argument("--platform-license", type=Path, required=True)
    parser.add_argument("--platform-notice", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, required=True)
    build(parser.parse_args())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
