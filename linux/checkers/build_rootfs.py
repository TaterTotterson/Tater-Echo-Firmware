#!/usr/bin/env python3
"""Build a Tater Linux Echo Show root filesystem from a pinned TECHO5 base.

TECHO5 supplies the hardware-proven Alpine userspace, boot contract, vendor
module setup, and A/B slot manager. Tater replaces its application layer while
retaining that MIT-licensed low-level platform work.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import os
from pathlib import Path, PurePosixPath
import shutil
import sys
import tarfile
import tempfile
from urllib import request as urlrequest

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO))
from tools import platform_lock  # noqa: E402

PLATFORMS = platform_lock.load()["platforms"]
BASE_VERSION = PLATFORMS["show"]["release"]
BASE_SHA256 = PLATFORMS["show"]["rootfs"]["sha256"]
ATHENA_BASE_VERSION = PLATFORMS["athena"]["release"]
ATHENA_BASE_SHA256 = PLATFORMS["athena"]["rootfs"]["sha256"]
ALPINE_APK_BASE = "https://dl-cdn.alpinelinux.org/alpine/v3.24/main/armv7/"
SETUP_PACKAGES = (
    ("hostapd-2.11-r4.apk", "f932ff857358b3b800c8523217a5aba196c2988bf09e20bae2da5ddd66979efe", "usr/sbin/hostapd"),
    ("dnsmasq-2.92_p2-r0.apk", "bd796346d062004f2d5645bfb0919fae1eebb709793e7dcae011bd804537dfe8", "usr/sbin/dnsmasq"),
)


ROOK_WIFI_MAC_FUNCTION = r'''# tater_rook_wifi_mac <interface>: Rook's Broadcom driver initially exposes the
# same placeholder address on every Spot. Replace only that known placeholder
# with the unit's factory IDME address before association and DHCP. A serial-
# derived locally administered address is a stable last resort for damaged or
# incomplete IDME data. Other targets and already-correct addresses are no-ops.
tater_rook_wifi_mac() {
	iface=$1
	case "$(cat "/sys/class/net/$iface/address" 2>/dev/null)" in
	00:90:4c:*) ;;
	*) return 0 ;;
	esac

	mac=$(tr -dc '0-9A-Fa-f' 2>/dev/null < /proc/idme/mac_addr | tr 'A-F' 'a-f')
	kind=factory
	# Twelve hex digits, unicast, non-zero, and not the driver's placeholder.
	case "$mac" in
	000000000000|00904c1a0900|?[13579bdf]*) mac= ;;
	esac
	if [ ${#mac} -ne 12 ]; then
		serial=$(tr -dc '0-9A-Za-z' 2>/dev/null < /proc/idme/serial)
		digest=$(printf %s "$serial" | sha256sum 2>/dev/null | cut -c1-10 | tr -dc '0-9a-f')
		if [ -n "$serial" ] && [ ${#digest} -eq 10 ]; then
			mac=02$digest
			kind="serial-derived fallback"
		else
			log "wifi: no valid factory identity; keeping the Broadcom placeholder"
			return 0
		fi
	fi

	# The firmware must be loaded but the link and supplicant must still be down.
	pidof wpa_supplicant >/dev/null && return 0
	ip link set "$iface" down 2>/dev/null
	formatted=$(echo "$mac" | sed 's/../&:/g; s/:$//')
	if ! ip link set "$iface" address "$formatted" 2>/tmp/ifmac.err; then
		grep -qi busy /tmp/ifmac.err && return 1
		log "wifi: $kind address refused: $(cat /tmp/ifmac.err)"
		return 0
	fi
	log "wifi: $kind address set to $formatted"
}
'''


def sha256(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


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


def ensure_tater_ssh_group(root: Path) -> None:
    """Give Dropbear one fail-closed login allowlist containing only root."""
    group = root / "etc/group"
    contents = group.read_text() if group.is_file() else ""
    expected = "tater-ssh:x:900:root"
    for line in contents.splitlines():
        fields = line.split(":")
        if fields[0] == "tater-ssh":
            if line != expected:
                raise SystemExit("base rootfs has an unexpected tater-ssh group")
            return
        if len(fields) >= 3 and fields[2] == "900":
            raise SystemExit("base rootfs already uses reserved tater-ssh gid 900")
    separator = "" if not contents or contents.endswith("\n") else "\n"
    write(group, contents + separator + expected + "\n")


def ensure_video_decoder_user(root: Path) -> None:
    """Give the network decoder a dedicated account that owns no files."""
    passwd = root / "etc/passwd"
    passwd_contents = passwd.read_text() if passwd.is_file() else ""
    expected_user = "techo5-video:x:89:89:techo5-video:/var/empty:/sbin/nologin"
    users = [line.split(":") for line in passwd_contents.splitlines() if line]
    user_found = False
    for fields in users:
        if fields[0] == "techo5-video":
            if ":".join(fields) != expected_user:
                raise SystemExit("base rootfs has an unexpected techo5-video user")
            user_found = True
        elif len(fields) >= 4 and (fields[2] == "89" or fields[3] == "89"):
            raise SystemExit("base rootfs already uses reserved video uid/gid 89")
    if not user_found:
        separator = "" if not passwd_contents or passwd_contents.endswith("\n") else "\n"
        write(passwd, passwd_contents + separator + expected_user + "\n")

    group = root / "etc/group"
    group_contents = group.read_text() if group.is_file() else ""
    groups = [line.split(":") for line in group_contents.splitlines() if line]
    video_found = False
    inet_found = False
    rewritten: list[str] = []
    for fields in groups:
        if fields[0] == "techo5-video":
            if len(fields) < 4 or fields[2] != "89":
                raise SystemExit("base rootfs has an unexpected techo5-video group")
            video_found = True
        elif len(fields) >= 3 and fields[2] == "89":
            raise SystemExit("base rootfs already uses reserved video gid 89")
        if fields[0] == "inet":
            if len(fields) < 4 or fields[2] != "3003":
                raise SystemExit("base rootfs has an unexpected inet group")
            members = [member for member in fields[3].split(",") if member]
            if "techo5-video" not in members:
                members.append("techo5-video")
            fields[3] = ",".join(members)
            inet_found = True
        elif len(fields) >= 3 and fields[2] == "3003":
            raise SystemExit("base rootfs already uses reserved inet gid 3003")
        rewritten.append(":".join(fields))
    if not video_found:
        rewritten.append("techo5-video:x:89:")
    if not inet_found:
        rewritten.append("inet:x:3003:techo5-video")
    write(group, "\n".join(rewritten) + "\n")


def setup_binary(cache: Path, name: str, digest: str, member: str, destination: Path) -> None:
    """Install only the pinned APK's executable, not its post-install scripts."""
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
        destination.chmod(0o755)


def patch_rook_wifi_mac(root: Path) -> None:
    """Set Rook's per-unit Wi-Fi address after firmware load, before link-up."""
    path = root / "lib/techo5-lib.sh"
    contents = path.read_text()
    function_marker = "\n# t5_wifi_up <module.ko> <wpa.conf>:"
    if contents.count(function_marker) != 1 or "\ntater_rook_wifi_mac() {\n" in contents:
        raise SystemExit("pinned Rook Wi-Fi library no longer has the expected setup function")
    contents = contents.replace(
        function_marker,
        "\n" + ROOK_WIFI_MAC_FUNCTION + function_marker,
        1,
    )
    # Current pinned Tater Echo Linux already calls its shared factory-MAC
    # helper after the driver firmware loads and before the link comes up.
    # Keep that ordering, but use Rook's stricter helper so damaged IDME data
    # still receives the stable serial-derived fallback instead of retaining
    # Broadcom's shared placeholder address.
    shared_mac_call = "\tn=0; until t5_wifi_mac wlan0; do"
    link_up_call = "\tn=0; until ip link set wlan0 up 2>/tmp/ifup.err; do"
    if contents.count(shared_mac_call) == 1 and contents.count(link_up_call) == 1:
        if contents.index(shared_mac_call) > contents.index(link_up_call):
            raise SystemExit("pinned Rook Wi-Fi library sets its MAC after link-up")
        contents = contents.replace(
            shared_mac_call,
            "\tn=0; until tater_rook_wifi_mac wlan0; do",
            1,
        )
        path.write_text(contents)
        return

    # Older pinned libraries had no shared factory-MAC helper. Insert the same
    # guarded call immediately before their exact link-up sequence.
    link_up = "\tt5_ipv6_private wlan0\n\tn=0; until ip link set wlan0 up 2>/tmp/ifup.err; do"
    mac_before_link = (
        "\tt5_ipv6_private wlan0\n"
        "\t# bcmdhd needs its firmware loaded before accepting a MAC change,\n"
        "\t# but the address must be unique before association and DHCP.\n"
        "\tn=0; until tater_rook_wifi_mac wlan0; do\n"
        "\t\tn=$((n+1)); [ $n -ge 15 ] && { "
        "log \"wifi: factory address not set: $(cat /tmp/ifmac.err)\"; break; }\n"
        "\t\tsleep 1\n"
        "\tdone\n"
        "\tn=0; until ip link set wlan0 up 2>/tmp/ifup.err; do"
    )
    if contents.count(link_up) != 1:
        raise SystemExit("pinned Rook Wi-Fi library no longer has the expected link-up sequence")
    contents = contents.replace(link_up, mac_before_link, 1)
    path.write_text(contents)


def patch_network_keeper(contents: str) -> str:
    """Keep the vendor Wi-Fi watchdog from fighting an active wired route."""
    marker = (
        "\twhile true; do\n"
        "\t\tsleep 60\n"
        "\t\t# Someone is choosing a network on the screen: not a fault, no reboot.\n"
    )
    if marker not in contents:
        return contents
    replacement = (
        "\twhile true; do\n"
        "\t\tsleep 60\n"
        "\t\t# USB Ethernet owns the default route only after DHCP succeeds. Do\n"
        "\t\t# not restart Wi-Fi or reboot while that wired link is active.\n"
        "\t\tif grep -q '^active ' /run/tater-usb-ethernet.state 2>/dev/null; then\n"
        "\t\t\tdown=0; lost=0; continue\n"
        "\t\tfi\n"
        "\t\t# Someone is choosing a network on the screen: not a fault, no reboot.\n"
    )
    return contents.replace(marker, replacement, 1)


def patch_boot(root: Path, *, target: str = "checkers") -> None:
    if target == "athena":
        patch_boot_athena(root)
        return
    path = root / "etc/techo5/boot.sh"
    contents = path.read_text()
    contents = contents.replace("/data/techo5-linux", "/data/tater-linux")
    contents = contents.replace("pidof techo5", "pidof tater-echo")
    health_probe = (
        "\t\tpid=$(pidof tater-echo | cut -d' ' -f1)\n"
        "\t\tif [ -n \"$pid\" ]; then"
    )
    camera_health = " && pidof tater-camera >/dev/null" if target in ("checkers", "crown", "rook") else ""
    health_replacement = (
        "\t\tpid=$(pidof tater-echo | cut -d' ' -f1)\n"
        "\t\t# Commit only while the daemon is actually connected to Tater and\n"
        "\t\t# the native display service is alive. A daemon that merely stays\n"
        "\t\t# resident while BLE has wedged Wi-Fi is not a healthy slot.\n"
        "\t\tif [ -n \"$pid\" ] && [ -s /run/tater-connected ] && \\\n"
        "\t\t   pidof tater-show >/dev/null" + camera_health + "; then"
    )
    if health_probe not in contents:
        raise SystemExit("pinned base boot script no longer has the expected trial health probe")
    contents = contents.replace(health_probe, health_replacement, 1)
    device_scan = "mdev -s"
    console_note = (
        "# The Echo Show kernel can hold console_lock while verbose vendor Wi-Fi\n"
        "# messages flood its consoles. FBIOPAN_DISPLAY waits on that lock and\n"
        "# the panel freezes. Keep messages in dmesg, but only print emergencies\n"
        "# to the consoles. Apply before the Wi-Fi module is loaded below.\n"
        if target in ("checkers", "crown") else
        "# Keep verbose vendor messages out of the screen's console while\n"
        "# retaining them in dmesg.\n"
    )
    loopback = (
        device_scan
        + "\n" + console_note
        + "echo 1 4 1 7 > /proc/sys/kernel/printk\n"
        + "\n# Tater's screen service is loopback-only. The pinned base\n"
        + "# leaves lo without 127.0.0.1, so listeners otherwise fail.\n"
        + "ip link set lo up\n"
        + "ip -4 addr show lo | grep -q '127\\.0\\.0\\.1/' || ip addr add 127.0.0.1/8 dev lo"
    )
    if device_scan not in contents:
        raise SystemExit("pinned base boot script no longer has its mdev scan line")
    contents = contents.replace(device_scan, loopback, 1)
    wifi_line = "t5_wifi_conf $LOGDIR/wpa_supplicant.conf"
    wifi_replacement = (
        "# The USB factory installer may write credentials before Tater Linux's\n"
        "# persistent directory exists. Import that one-time handoff without\n"
        "# overwriting a network subsequently changed by Tater.\n"
        "if [ ! -s $LOGDIR/wpa_supplicant.conf ] && "
        "[ -s /data/techo5-linux/wpa_supplicant.conf ]; then\n"
        "    cp /data/techo5-linux/wpa_supplicant.conf $LOGDIR/wpa_supplicant.conf\n"
        "    chmod 600 $LOGDIR/wpa_supplicant.conf\n"
        "fi\n"
        + wifi_line
    )
    if wifi_line not in contents:
        raise SystemExit("pinned base boot script no longer has its Wi-Fi setup line")
    contents = contents.replace(wifi_line, wifi_replacement, 1)
    bluetooth_line = 't5_bt_up "$BT_MODULE" /var/log'
    bluetooth_replacement = (
        '# Tater owns /dev/stpbt directly for passive BLE presence. Load the\n'
        '# vendor transport, but do not start btbridge/BlueZ on the same node.\n'
        'if [ ! -e /dev/stpbt ] && [ -e "$BT_MODULE" ]; then\n'
        '    insmod "$BT_MODULE" 2>/tmp/insmod-bt.err || '
        'log "bt: driver load failed: $(cat /tmp/insmod-bt.err)"\n'
        'fi'
    )
    if target in ("checkers", "crown"):
        if bluetooth_line not in contents:
            raise SystemExit("pinned base boot script no longer has its Bluetooth setup line")
        contents = contents.replace(bluetooth_line, bluetooth_replacement, 1)

    # BusyBox's reboot applet can stop init-managed services without reaching
    # the kernel restart on Checkers. Tater's helper issues the reboot syscall
    # directly without a second global sync after slotctl's durable transaction.
    reboot_line = "\n\t\t\treboot\n"
    if contents.count(reboot_line) != 2:
        raise SystemExit("pinned base boot script no longer has the expected reboot lines")
    contents = contents.replace(reboot_line, "\n\t\t\t/usr/local/bin/tater-reboot-now\n")
    root_commit = (
        '\t\t\t\tlog "slot $s: daemon up for $TRIAL_SETTLE s, committing"\n'
        "\t\t\t\tslotctl commit >> /run/boot.log 2>&1\n"
        "\t\t\t\texit 0\n"
    )
    root_commit_replacement = (
        '\t\t\t\tlog "slot $s: daemon up for $TRIAL_SETTLE s, committing"\n'
        "\t\t\t\t# These Shows expose the same ext4 filesystem at / and /store.\n"
        "\t\t\t\t# Its vendor kernel can leave the remount-ro syscall stuck after\n"
        "\t\t\t\t# applying the flag. Commit durably, then use one direct restart;\n"
        "\t\t\t\t# the initramfs brings both mounts back read-only.\n"
        "\t\t\t\tif SLOTCTL_LEAVE_RW=1 slotctl commit >> /run/boot.log 2>&1; then\n"
        '\t\t\t\t\tlog "slot $s: committed; restarting with read-only mounts"\n'
        "\t\t\t\t\t/usr/local/bin/tater-reboot-now\n"
        "\t\t\t\tfi\n"
        "\t\t\t\texit 0\n"
    )
    if root_commit not in contents:
        raise SystemExit("pinned base boot script no longer has the expected root commit block")
    contents = contents.replace(root_commit, root_commit_replacement, 1)
    trial_timeout = "\t\tif [ $now -ge $TRIAL_TIMEOUT ]; then\n"
    setup_grace = (
        "\t\t# An unpaired unit may need several attempts at Wi-Fi or its\n"
        "\t\t# one-time code. The bootstrap merely means the form was saved;\n"
        "\t\t# the durable device token proves pairing really succeeded.\n"
        "\t\tif [ ! -s /data/local/etc/tater/device_token ]; then\n"
        "\t\t\tTRIAL_TIMEOUT=$((now+900))\n"
        "\t\t\tcontinue\n"
        "\t\tfi\n"
        + trial_timeout
    )
    if contents.count(trial_timeout) != 1:
        raise SystemExit("pinned base boot script no longer has the expected trial timeout")
    contents = contents.replace(trial_timeout, setup_grace, 1)
    final_log = 'cp /run/boot.log $LOGDIR/boot.log 2>/dev/null\nlog "boot script done"'
    app_health = r'''# --- Tater application A/B health. Routine OTA lives on /data, like Biscuit,
# and never remounts this live rootfs. Both the daemon and renderer must remain
# up and the daemon must connect before the coordinated application slot commits.
(
	APP=/data/tater-linux/app
	[ -s "$APP/pending.env" ] || exit 0
	settle=${TATER_APP_SETTLE:-300}
	timeout=${TATER_APP_TIMEOUT:-900}
	boot_start=$(cut -d. -f1 /proc/uptime)
	while [ -s "$APP/pending.env" ]; do
		sleep 15
		now=$(cut -d. -f1 /proc/uptime)
		echo_pid=$(pidof tater-echo | cut -d' ' -f1)
		show_pid=$(pidof tater-show | cut -d' ' -f1)
		echo_started=$(awk '{print int($22/100)}' /proc/$echo_pid/stat 2>/dev/null)
		show_started=$(awk '{print int($22/100)}' /proc/$show_pid/stat 2>/dev/null)
		if [ -s /run/tater-connected ] && [ -n "$echo_started" ] && [ -n "$show_started" ] && \
		   [ $((now-echo_started)) -ge "$settle" ] && [ $((now-show_started)) -ge "$settle" ]; then
			log "Tater application: daemon and renderer healthy for $settle s, committing"
			tater-app commit >> /run/boot.log 2>&1
			exit 0
		fi
		if [ $((now-boot_start)) -ge "$timeout" ]; then
			log "Tater application: health timeout, rolling back"
			if tater-app rollback >> /run/boot.log 2>&1; then
				/usr/local/bin/tater-reboot-now --directory "$APP"
			fi
			exit 1
		fi
	done
) &

'''
    if final_log not in contents:
        raise SystemExit("pinned base boot script no longer has its final log marker")
    contents = contents.replace(final_log, app_health + final_log, 1)
    path.write_text(patch_network_keeper(contents))


def patch_boot_athena(root: Path) -> None:
    """Retain TECHO5's Athena hardware bring-up and replace its app contract."""
    path = root / "etc/techo5/boot.sh"
    contents = path.read_text()
    contents = contents.replace("/data/techo5-linux", "/data/tater-linux")
    contents = contents.replace("pidof techo5", "pidof tater-echo")
    contents = contents.replace(")\" != techo5 ]", ")\" != tater-echo ]")
    health = '\t\tif [ -n "$pid" ]; then'
    replacement = (
        '\t\tif [ -n "$pid" ] && [ -s /run/tater-connected ] && \\\n'
        '\t\t   pidof tater-show >/dev/null && pidof tater-camera >/dev/null; then'
    )
    if contents.count(health) < 1:
        raise SystemExit("pinned Athena boot script no longer has its slot health probe")
    contents = contents.replace(health, replacement, 1)
    trial_timeout = "\t\tif [ $now -ge $TRIAL_TIMEOUT ]; then\n"
    setup_grace = (
        "\t\t# First-time Tater pairing can legitimately outlast a rootfs trial.\n"
        "\t\t# Do not spend boot attempts until the durable device token exists.\n"
        "\t\tif [ ! -s /data/local/etc/tater/device_token ]; then\n"
        "\t\t\tTRIAL_TIMEOUT=$((now+900))\n"
        "\t\t\tcontinue\n"
        "\t\tfi\n"
        + trial_timeout
    )
    if contents.count(trial_timeout) != 1:
        raise SystemExit("pinned Athena boot script no longer has its trial timeout")
    contents = contents.replace(trial_timeout, setup_grace, 1)
    scan = "mdev -s"
    if contents.count(scan) != 1:
        raise SystemExit("pinned Athena boot script no longer has its mdev scan")
    contents = contents.replace(
        scan,
        scan + "\n# Tater's renderer and camera helper are loopback-only.\n"
        "ip link set lo up\n"
        "ip -4 addr show lo | grep -q '127\\.0\\.0\\.1/' || ip addr add 127.0.0.1/8 dev lo",
        1,
    )
    wifi = "t5_wifi_conf $LOGDIR/wpa_supplicant.conf"
    if contents.count(wifi) != 1:
        raise SystemExit("pinned Athena boot script no longer has its Wi-Fi setup line")
    contents = contents.replace(
        wifi,
        "if [ ! -s $LOGDIR/wpa_supplicant.conf ] && "
        "[ -s /data/techo5-linux/wpa_supplicant.conf ]; then\n"
        "    cp /data/techo5-linux/wpa_supplicant.conf $LOGDIR/wpa_supplicant.conf\n"
        "    chmod 600 $LOGDIR/wpa_supplicant.conf\n"
        "fi\n" + wifi,
        1,
    )
    final = 'cp /run/boot.log $LOGDIR/boot.log 2>/dev/null\nlog "boot script done"'
    if final not in contents:
        raise SystemExit("pinned Athena boot script no longer has its final marker")
    contents = contents.replace(final, APP_HEALTH + final, 1)
    path.write_text(patch_network_keeper(contents))


def patch_slotctl(root: Path) -> None:
    path = root / "usr/local/sbin/slotctl"
    contents = path.read_text()
    original = """done_writing() {
\tsync
\tif [ -n "$WAS_RO" ]; then
\t\tmount -o remount,ro "$STORE" 2>/dev/null || echo "slotctl: note: $STORE left writable (busy)" >&2
\t\tWAS_RO=
\tfi
}
"""
    replacement = """done_writing() {
\tsync
\tif [ -n "$WAS_RO" ]; then
\t\tif [ "$SLOTCTL_LEAVE_RW" = 1 ]; then
\t\t\techo "slotctl: $STORE left writable for immediate restart" >&2
\t\telse
\t\t\tmount -o remount,ro "$STORE" 2>/dev/null || echo "slotctl: note: $STORE left writable (busy)" >&2
\t\tfi
\t\tWAS_RO=
\tfi
}
"""
    if original not in contents:
        raise SystemExit("pinned base slotctl no longer has the expected write transaction")
    contents = contents.replace(original, replacement, 1)
    switch = "cmd_switch() {\n"
    rearm = """# Re-arm a running trial only at a deliberate setup reboot. Otherwise the
# three boot attempts protect against a broken rootfs as TECHO5 intended.
cmd_rearm() {
	s=$(booted)
	[ -n "$s" ] || die "not booted from a slot"
	case "$(state $s)" in
	good) echo "slotctl: slot $s is already good"; return 0;;
	trial*) ;;
	*) die "slot $s is $(state $s); cannot re-arm it";;
	esac
	writable
	set_state $s "trial $TRIES"
	done_writing
	echo "slotctl: slot $s re-armed for setup (trial $TRIES)"
}

cmd_switch() {
"""
    dispatch = "commit) cmd_commit;;\n"
    if contents.count(switch) != 1 or contents.count(dispatch) != 1:
        raise SystemExit("pinned base slotctl no longer has the expected command dispatch")
    contents = contents.replace(switch, rearm, 1)
    contents = contents.replace(dispatch, dispatch + "rearm) cmd_rearm;;\n", 1)
    usage = "  switch <a|b>            boot this slot next; a bad slot goes back on trial\n"
    if contents.count(usage) != 1:
        raise SystemExit("pinned base slotctl no longer has the expected usage text")
    contents = contents.replace(
        usage, usage + "  rearm                   refresh this booted trial for a deliberate setup restart\n", 1
    )
    path.write_text(contents)


def safe_extract(archive: tarfile.TarFile, destination: Path) -> None:
    def stays_inside(path: PurePosixPath) -> bool:
        depth = 0
        for part in path.parts:
            if part in ("", "."):
                continue
            if part == "..":
                depth -= 1
                if depth < 0:
                    return False
            else:
                depth += 1
        return True

    destination_real = destination.resolve()
    members = archive.getmembers()
    link_paths: set[PurePosixPath] = set()
    for member in members:
        target = (destination / member.name).resolve()
        if target != destination_real and destination_real not in target.parents:
            raise SystemExit(f"unsafe path in base rootfs: {member.name}")
        member_path = PurePosixPath(member.name)
        if member.issym():
            link = PurePosixPath(member.linkname)
            if not link.is_absolute() and not stays_inside(member_path.parent / link):
                raise SystemExit(f"unsafe link target in base rootfs: {member.name} -> {member.linkname}")
            link_paths.add(member_path)
        elif member.islnk():
            link = PurePosixPath(member.linkname)
            if link.is_absolute() or not stays_inside(link):
                raise SystemExit(f"unsafe link target in base rootfs: {member.name} -> {member.linkname}")
            link_paths.add(PurePosixPath(member.name))

    # A later archive member must never be extracted through a link created by
    # an earlier member. Absolute symlink targets themselves are normal in a
    # Linux rootfs (for example /bin/cp -> /bin/busybox).
    for member in members:
        path = PurePosixPath(member.name)
        if any(parent in link_paths for parent in path.parents):
            raise SystemExit(f"unsafe linked path in base rootfs: {member.name}")

    # The archive is content-addressed above and every member was bounded to
    # destination. Python 3.14 changed extractall's default to reject the
    # absolute symlinks used by Alpine; request the historical behavior there
    # while retaining compatibility with Python 3.9 on older Macs.
    if sys.version_info >= (3, 12):
        archive.extractall(destination, filter="fully_trusted")
    else:
        archive.extractall(destination)


def deterministic_tar(root: Path, output: Path) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0, compresslevel=9) as zipped:
            with tarfile.open(fileobj=zipped, mode="w", format=tarfile.PAX_FORMAT) as archive:
                for path in [root] + sorted(root.rglob("*")):
                    name = "." if path == root else "./" + path.relative_to(root).as_posix()
                    info = archive.gettarinfo(str(path), name)
                    info.uid = info.gid = 0
                    info.uname = info.gname = "root"
                    info.mtime = 0
                    if path.is_file() and not path.is_symlink():
                        with path.open("rb") as source:
                            archive.addfile(info, source)
                    else:
                        archive.addfile(info)


def build(args: argparse.Namespace) -> None:
    base_version = ATHENA_BASE_VERSION if args.target == "athena" else BASE_VERSION
    base_sha256 = ATHENA_BASE_SHA256 if args.target == "athena" else BASE_SHA256
    if sha256(args.base_rootfs) != base_sha256:
        raise SystemExit(
            f"base rootfs is not TECHO5 {base_version} ({args.base_rootfs})"
        )
    with tempfile.TemporaryDirectory(prefix=f"tater-{args.target}-rootfs-") as temporary:
        root = Path(temporary) / "root"
        root.mkdir()
        with tarfile.open(args.base_rootfs, "r:gz") as archive:
            safe_extract(archive, root)

        # The signed baseline supplies the filesystem, while the exact pinned
        # platform checkout supplies the current shared hardware policy.
        copy(args.techo5_lib, root / "lib/techo5-lib.sh", 0o644)
        copy(args.server, root / "usr/local/bin/tater-echo", 0o755)
        copy(args.show, root / "usr/local/bin/tater-show", 0o755)
        copy(args.reboot, root / "usr/local/bin/tater-reboot-now", 0o755)
        copy(args.camera, root / "usr/local/bin/tater-camera", 0o755)
        copy(args.ffmpeg, root / "usr/local/bin/tater-ffmpeg", 0o755)
        if args.target == "athena":
            if args.kernelctl is None or args.kernel_image is None:
                raise SystemExit("Athena rootfs requires its kernel updater and pinned boot image")
            copy(args.kernelctl, root / "usr/local/bin/tater-kernelctl", 0o755)
            kernel_dir = root / "usr/share/techo5/kernel"
            copy(args.kernel_image, kernel_dir / "boot-athena.img", 0o644)
            write(
                kernel_dir / "boot-athena.img.sha256",
                f"{sha256(args.kernel_image)}  boot-athena.img\n",
            )
        copy(
            args.mww_runtime,
            root / "usr/share/tater/microwakeword/libtater_microwakeword.so",
            0o755,
        )
        copy(args.onnx_runtime, root / "usr/share/tater/microwakeword/libonnxruntime.so", 0o755)
        copy(args.hey_tater_model, root / "usr/share/tater/microwakeword/hey_tater.tflite", 0o644)
        copy(args.hey_tater_manifest, root / "usr/share/tater/microwakeword/hey_tater.json", 0o644)
        copy(args.hey_tater_oww_onnx, root / "usr/share/tater/microwakeword/hey_tater.oww.onnx", 0o644)
        copy(args.hey_tater_oww_metadata, root / "usr/share/tater/microwakeword/hey_tater.oww.json", 0o644)
        copy(args.hey_tater_nww_cnn, root / "usr/share/tater/microwakeword/hey_tater.nww.cnn.onnx", 0o644)
        copy(args.hey_tater_nww_gru, root / "usr/share/tater/microwakeword/hey_tater.nww.gru.onnx", 0o644)
        copy(args.hey_tater_bundle, root / "usr/share/tater/microwakeword/hey_tater.wake-bundle.json", 0o644)
        copy(args.stop_model, root / "usr/share/tater/microwakeword/stop.tflite", 0o644)
        copy(args.stop_manifest, root / "usr/share/tater/microwakeword/stop.json", 0o644)
        copy(args.oww_melspectrogram_onnx, root / "usr/share/tater/microwakeword/melspectrogram.onnx", 0o644)
        copy(args.oww_embedding_onnx, root / "usr/share/tater/microwakeword/embedding_model.onnx", 0o644)
        copy(args.tinyalsa, root / "usr/lib/libtinyalsa.so.2.0.0", 0o755)
        for name, digest, member in SETUP_PACKAGES:
            setup_binary(args.apk_cache, name, digest, member, root / member)
        if args.target == "athena":
            copy(Path(__file__).parents[1] / "athena/setup-ap.sh",
                 root / "usr/local/sbin/tater-setup-ap", 0o755)
            copy(Path(__file__).with_name("setup-ap.sh"),
                 root / "usr/local/libexec/tater-setup-ap-common", 0o755)
        else:
            copy(Path(__file__).with_name("setup-ap.sh"), root / "usr/local/sbin/tater-setup-ap", 0o755)
        copy(Path(__file__).parents[1] / "common/tater-usb-inventory",
             root / "usr/local/sbin/tater-usb-inventory", 0o755)
        copy(Path(__file__).parents[1] / "common/tater-usb-ethernet",
             root / "usr/local/sbin/tater-usb-ethernet", 0o755)
        copy(Path(__file__).parents[1] / "common/tater-sshd",
             root / "usr/local/sbin/tater-sshd", 0o755)
        copy(Path(__file__).parents[1] / "common/tater-ssh-authorize",
             root / "usr/local/sbin/tater-ssh-authorize", 0o755)
        copy(Path(__file__).parents[1] / "common/tater-ssh-control",
             root / "usr/local/sbin/tater-ssh-control", 0o755)
        ensure_tater_ssh_group(root)
        ensure_video_decoder_user(root)
        for name in ("libtinyalsa.so", "libtinyalsa.so.2"):
            link = root / "usr/lib" / name
            link.unlink(missing_ok=True)
            link.symlink_to("libtinyalsa.so.2.0.0")

        old_daemon = root / "usr/local/bin/techo5"
        old_daemon.unlink(missing_ok=True)
        old_daemon.symlink_to("tater-echo")
        for unused in ("techo5-aec", "techo5-librespot"):
            (root / "usr/local/bin" / unused).unlink(missing_ok=True)

        device_name = {
            "checkers": "Echo Show 5 (Checkers)",
            "crown": "Echo Show 8 (Crown)",
            "athena": "Echo Show 8 Gen 2 (Athena)",
        }[args.target]
        pretty_target = args.target.title()
        write(root / "etc/inittab", INITTAB.format(target=pretty_target), 0o755)
        write(root / "usr/local/sbin/tater-run", TATER_RUN, 0o755)
        write(root / "usr/local/sbin/tater-show-run", TATER_SHOW_RUN, 0o755)
        write(root / "usr/local/sbin/tater-camera-run", TATER_CAMERA_RUN, 0o755)
        write(root / "usr/local/sbin/tater-app", TATER_APP, 0o755)
        write(root / "etc/hostname", f"tater-{args.target}\n")
        write(root / "etc/motd", MOTD.format(device_name=device_name))
        write(root / "usr/lib/os-release", OS_RELEASE.format(version=args.version, target=pretty_target))
        release = f"Tater Linux {pretty_target} {args.version} (TECHO5 platform {base_version})\n"
        write(root / "etc/tater-release", release)
        write(root / "etc/techo5-release", release)
        write(root / "usr/share/licenses/tater-linux/TECHO5-LICENSE", args.techo5_license.read_text())
        write(root / "usr/share/licenses/tater-linux/FFMPEG-LGPL-2.1.txt",
              args.ffmpeg_license.read_text())
        write(root / "usr/share/licenses/tater-linux/NOTICE", ATTRIBUTION)
        patch_boot(root, target=args.target)
        if args.target != "athena":
            patch_slotctl(root)
        else:
            # Keep the application daemon and the low-level kernel updater as
            # separate binaries. A system OTA installs this rootfs into the
            # inactive slot; only after that slot settles does boot.sh stage
            # its pinned kernel with the unit's existing speaker tuning and
            # the platform updater's three-boot rollback state.
            boot_script = root / "etc/techo5/boot.sh"
            boot_contents = boot_script.read_text()
            old_kernel_command = "/usr/local/bin/techo5 tools kernel"
            if old_kernel_command not in boot_contents:
                raise SystemExit("Athena base rootfs has no kernel updater hooks")
            boot_script.write_text(boot_contents.replace(
                old_kernel_command,
                "/usr/local/bin/tater-kernelctl tools kernel",
            ))
            device_conf = root / "etc/techo5/device.conf"
            if "\tKERNEL_UPDATE=1" not in device_conf.read_text():
                raise SystemExit("Athena base rootfs does not enable kernel updates")
        deterministic_tar(root, args.output)

    print(f"built {args.output} ({args.output.stat().st_size} bytes, sha256 {sha256(args.output)})")


INITTAB = """# Tater Linux {target} — BusyBox init.
::sysinit:/etc/techo5/boot.sh
::respawn:/usr/local/sbin/tater-usb-ethernet
::respawn:/usr/local/sbin/tater-sshd
::respawn:/usr/local/sbin/tater-run
::respawn:/usr/local/sbin/tater-show-run
::respawn:/usr/local/sbin/tater-camera-run
::respawn:/usr/local/sbin/techo5-console
::ctrlaltdel:/usr/local/bin/tater-reboot-now
::shutdown:/etc/techo5/shutdown.sh
"""

TATER_RUN = """#!/bin/sh
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
if [ ! -s /data/local/etc/tater/native.json ]; then
    echo "tater-run: no native configuration; starting setup hotspot" >> "$LOG"
    /usr/local/sbin/tater-setup-ap >> "$LOG" 2>&1 || echo "tater-run: hotspot unavailable; USB provisioning remains available" >> "$LOG"
    exec $(tater-app binary tater-echo) setup-mode >> "$LOG" 2>&1
fi
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
"""

TATER_SHOW_RUN = """#!/bin/sh
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
LOG=/data/tater-linux/tater-show.log
mkdir -p /data/tater-linux
n=0
while [ ! -e /dev/fb0 ] && [ ! -e /dev/graphics/fb0 ]; do
    sleep 1
    n=$((n+1))
    [ "$n" -ge 30 ] && exit 1
done
echo "tater-show-run: starting at $(date)" >> "$LOG"
attempt=0
while true; do
    binary=$(tater-app binary tater-show)
    started=$(cut -d. -f1 /proc/uptime)
    $binary >> "$LOG" 2>&1
    status=$?
    stopped=$(cut -d. -f1 /proc/uptime)
    runtime=$((stopped-started))
    if [ "$runtime" -ge 15 ]; then attempt=0; else attempt=$((attempt+1)); fi
    echo "tater-show-run: exit=$status runtime=${runtime}s fast-attempt=$attempt" >> "$LOG"
    if [ "$attempt" -ge 3 ] && tater-app rollback >> "$LOG" 2>&1; then
        /usr/local/bin/tater-reboot-now --directory /data/tater-linux/app
    fi
    sleep 2
done
"""

TATER_CAMERA_RUN = """#!/bin/sh
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
LOG=/data/tater-linux/tater-camera.log
mkdir -p /data/tater-linux
echo "tater-camera-run: starting at $(date)" >> "$LOG"
exec /usr/local/bin/tater-camera >> "$LOG" 2>&1
"""

TATER_APP = """#!/bin/sh
APP=/data/tater-linux/app
case "$1" in
binary)
    case "$2" in tater-echo|tater-show) ;; *) exit 2;; esac
    current=$(readlink -f "$APP/current" 2>/dev/null)
    if [ -n "$current" ] && [ -x "$current/$2" ]; then
        echo "$current/$2"
    else
        echo "/usr/local/bin/$2"
    fi
    ;;
rollback)
    [ -s "$APP/pending.env" ] || exit 1
    previous=$(sed -n 's/^previous=//p' "$APP/pending.env" | head -1)
    case "$previous" in
    system) rm -f "$APP/current" ;;
    a|b)
        [ -x "$APP/slots/$previous/tater-echo" ] && [ -x "$APP/slots/$previous/tater-show" ] || exit 1
        ln -s "slots/$previous" "$APP/current.rollback" || exit 1
        # Plain mv follows a symlink to a directory and moves the rollback
        # link inside that slot. Remove the old link first on all mv variants.
        rm -f "$APP/current" || exit 1
        mv -f "$APP/current.rollback" "$APP/current" || exit 1
        ;;
    *) exit 1;;
    esac
    rm -f "$APP/pending.env"
    echo "tater-app: rolled back to $previous"
    ;;
commit)
    [ -e "$APP/pending.env" ] || exit 0
    rm -f "$APP/pending.env"
    echo "tater-app: application slot committed"
    ;;
*) echo "usage: tater-app binary tater-echo|tater-show | rollback | commit" >&2; exit 2;;
esac
"""

MOTD = """Tater Linux for {device_name}.
Root filesystems use A/B slots; persistent state is on /data and recovery is TWRP.
Logs: /data/tater-linux/   Slot status: slotctl status
"""

OS_RELEASE = """NAME="Tater Linux"
ID=tater-linux
VERSION="{version}"
VERSION_ID="{version}"
PRETTY_NAME="Tater Linux {target} {version}"
HOME_URL="https://github.com/TaterTotterson/Tater-Echo-Firmware"
"""

ATTRIBUTION = """This root filesystem uses hardware enablement, the rescue boot contract, and
A/B slot tooling derived from TECHO5 by HuskerMinion under the MIT License.
https://github.com/HuskerMinion/techo5

The setup hotspot includes hostapd 2.11 (BSD) and dnsmasq 2.92 (GPL-2.0)
executables extracted from verified Alpine Linux v3.24 armv7 packages.
Package sources: https://gitlab.alpinelinux.org/alpine/aports/-/tree/master/main/hostapd
and https://gitlab.alpinelinux.org/alpine/aports/-/tree/master/main/dnsmasq

Live camera video uses the reduced FFmpeg 8.1.2 decoder from the pinned Tater
Echo Linux Athena baseline under LGPL-2.1-or-later. Its license is installed at
/usr/share/licenses/tater-linux/FFMPEG-LGPL-2.1.txt.
"""

APP_HEALTH = r'''# --- Tater application A/B health.
(
	APP=/data/tater-linux/app
	[ -s "$APP/pending.env" ] || exit 0
	settle=${TATER_APP_SETTLE:-300}
	timeout=${TATER_APP_TIMEOUT:-900}
	boot_start=$(cut -d. -f1 /proc/uptime)
	while [ -s "$APP/pending.env" ]; do
		sleep 15
		now=$(cut -d. -f1 /proc/uptime)
		echo_pid=$(pidof tater-echo | cut -d' ' -f1)
		show_pid=$(pidof tater-show | cut -d' ' -f1)
		echo_started=$(awk '{print int($22/100)}' /proc/$echo_pid/stat 2>/dev/null)
		show_started=$(awk '{print int($22/100)}' /proc/$show_pid/stat 2>/dev/null)
		if [ -s /run/tater-connected ] && [ -n "$echo_started" ] && [ -n "$show_started" ] && \
		   [ $((now-echo_started)) -ge "$settle" ] && [ $((now-show_started)) -ge "$settle" ]; then
			tater-app commit >> /run/boot.log 2>&1
			exit 0
		fi
		if [ $((now-boot_start)) -ge "$timeout" ]; then
			if tater-app rollback >> /run/boot.log 2>&1; then
				/usr/local/bin/tater-reboot-now --directory "$APP"
			fi
			exit 1
		fi
	done
) &

'''


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", choices=("checkers", "crown", "athena"), default="checkers")
    parser.add_argument("--base-rootfs", type=Path, required=True)
    parser.add_argument("--server", type=Path, required=True)
    parser.add_argument("--show", type=Path, required=True)
    parser.add_argument("--reboot", type=Path, required=True)
    parser.add_argument("--camera", type=Path, required=True)
    parser.add_argument("--ffmpeg", type=Path, required=True)
    parser.add_argument("--kernelctl", type=Path)
    parser.add_argument("--kernel-image", type=Path)
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
    parser.add_argument("--techo5-lib", type=Path, required=True)
    parser.add_argument("--techo5-license", type=Path, required=True)
    parser.add_argument("--ffmpeg-license", type=Path, required=True)
    parser.add_argument("--apk-cache", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    build(args)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
