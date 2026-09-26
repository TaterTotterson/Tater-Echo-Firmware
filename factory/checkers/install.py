#!/usr/bin/env python3
"""Install Tater firmware on an unlocked Echo Show 5 Checkers.

The full native install is currently for rooted stock Fire OS: it verifies
Checkers + root, installs the screen APK and native userspace under /data, and
adds a reversible Magisk boot supervisor. LineageOS 18.1 is accepted only for
the non-persistent screen preview until its native init integration lands. The
installer never writes boot, recovery, system, or vendor.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent
MANIFEST = ROOT / "bundle-manifest.json"
PAYLOAD = ROOT / "payload"
PACKAGE = "com.tatertotterson.show"
ACTIVITY = f"{PACKAGE}/.MainActivity"
REMOTE_STAGE = "/data/local/tmp/tater-checkers-install"
CERTIFIED_FIREOS_NAME = "Fire OS 6574.1 (NS65741/8146)"
CERTIFIED_FIREOS_DISPLAY = "NS65741"
CERTIFIED_FIREOS_INCREMENTAL = "0013222531716"
CERTIFIED_FIREOS_URL = (
    "https://ftvdb.com/echo/firmware/com.amazon.checkers.android.os/"
    "d17ab1fb8fa374cc3f9b1d813d4094dc-13222531716-fire-os-6574-1-"
    "ns65741-8146-2026-09-15/"
)


class InstallError(RuntimeError):
    pass


def sha256(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def load_manifest() -> dict:
    try:
        manifest = json.loads(MANIFEST.read_text())
    except (OSError, ValueError) as exc:
        raise InstallError(f"cannot read {MANIFEST.name}: {exc}") from exc
    if manifest.get("target") != "checkers":
        raise InstallError("this is not a checkers factory bundle")
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
        if resolved.stat().st_size != int(metadata.get("size", -1)):
            raise InstallError(f"bundle file has the wrong size: {relative}")
        if sha256(resolved) != metadata.get("sha256"):
            raise InstallError(f"bundle file failed SHA-256 verification: {relative}")


class Adb:
    def __init__(self, binary: str, serial: str | None):
        self.binary = binary
        self.serial = serial

    @property
    def base(self) -> list[str]:
        result = [self.binary]
        if self.serial:
            result += ["-s", self.serial]
        return result

    def run(self, *args: str, capture: bool = False, check: bool = True) -> str:
        try:
            completed = subprocess.run(
                self.base + list(args), check=check,
                stdout=subprocess.PIPE if capture else None,
                stderr=subprocess.PIPE if capture else None,
            )
        except FileNotFoundError as exc:
            raise InstallError(f"ADB was not found at {self.binary!r}") from exc
        except subprocess.CalledProcessError as exc:
            detail = (exc.stderr or exc.stdout or b"").decode(errors="replace").strip()
            raise InstallError(f"adb {' '.join(args)} failed: {detail or exc.returncode}") from exc
        return completed.stdout.decode(errors="replace").replace("\r", "").strip() if capture else ""

    def shell(self, command: str, check: bool = True) -> str:
        return self.run("shell", command, capture=True, check=check)


class RootShell:
    """Run fixed bring-up checks as root without requiring root adbd."""

    def __init__(self, adb: Adb, prefix: str = "", suffix: str = ""):
        self.adb = adb
        self.prefix = prefix
        self.suffix = suffix

    def run(self, command: str) -> str:
        if "'" in command:
            raise InstallError("internal root command contains an unsupported quote")
        return self.adb.shell(f"{self.prefix}{command}{self.suffix}")


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


def resolve_root(adb: Adb) -> RootShell:
    identity = adb.shell("id", check=False)
    if "uid=0(" in identity:
        return RootShell(adb)
    identity = adb.shell("su -c id", check=False)
    if "uid=0(" in identity:
        return RootShell(adb, "su -c '", "'")
    identity = adb.shell("su 0 id", check=False)
    if "uid=0(" in identity:
        return RootShell(adb, "su 0 sh -c '", "'")
    raise InstallError(
        "root is required (uid-0 ADB, `su -c`, and `su 0` were all unavailable)")


def require_checkers(adb: Adb) -> str:
    # Lineage userdebug can restart adbd as root. Stock Fire OS normally
    # rejects this and is instead accessed through the boot-root `su` path.
    adb.run("root", check=False)
    adb.run("wait-for-device")
    product = adb.shell("getprop ro.product.device").strip().lower()
    if product != "checkers":
        raise InstallError(f"connected device reports {product!r}, not checkers")
    release = adb.shell("getprop ro.build.version.release").strip()
    if release.startswith("7.1"):
        display = adb.shell("getprop ro.build.display.id").strip()
        incremental = adb.shell("getprop ro.build.version.incremental").strip()
        if (display, incremental) != (
                CERTIFIED_FIREOS_DISPLAY, CERTIFIED_FIREOS_INCREMENTAL):
            reported = f"{display or 'unknown'}/{incremental or 'unknown'}"
            raise InstallError(
                f"Checkers reports untested Fire OS build {reported}; this release requires "
                f"{CERTIFIED_FIREOS_NAME}. Restore the verified image from {CERTIFIED_FIREOS_URL} "
                "before installing Tater")
        userspace = "stock Fire OS 6"
    elif release == "11":
        userspace = "LineageOS 18.1"
    else:
        raise InstallError(
            f"Checkers reports Android {release or 'unknown'}; supported bring-up environments are "
            "stock Fire OS 6 (Android 7.1) and LineageOS 18.1 (Android 11)")
    root = resolve_root(adb)
    recovery = root.run(
        "for p in /dev/block/by-name/recovery /dev/block/platform/*/by-name/recovery "
        "/dev/block/platform/*/*/by-name/recovery; do [ -e \"$p\" ] && { echo \"$p\"; break; }; done")
    if not recovery.strip().startswith("/dev/block/"):
        raise InstallError("the recovery partition is not available; keep working TWRP before testing")
    layout = root.run(
        "layout=missing; for p in /dev/block/by-name/recovery /dev/block/platform/*/by-name/recovery "
        "/dev/block/platform/*/*/by-name/recovery; do [ -e \"$p\" ] || continue; "
        "dd if=\"$p\" bs=512 count=2 2>/dev/null | grep -qa microloader "
        "&& layout=amonet-1 || layout=amonet-2; break; done; echo $layout")
    if layout.strip() != "amonet-2":
        raise InstallError("amonet 2.0.1 or newer is required; this device still has the amonet 1.x boot layout")
    return userspace


def confirm_unlock(assume_yes: bool) -> None:
    if assume_yes:
        return
    print("\nThe device reports an amonet 2.x layout, but that layout does not expose its patch version.")
    print("Confirm that you installed amonet 2.0.1 or newer and can still boot TWRP.")
    if input("Type CHECKERS to continue: ").strip() != "CHECKERS":
        raise InstallError("installation cancelled")


def require_supported_install_mode(userspace: str, no_home: bool, uninstalling: bool) -> None:
    if userspace == "LineageOS 18.1" and not no_home and not uninstalling:
        raise InstallError(
            "the persistent Checkers service is still Fire-OS/Magisk-specific; "
            "on LineageOS use --no-home for the screen preview until the native "
            "Lineage init service and rollback path are complete")


def install_apk(adb: Adb, set_home: bool, demo: bool = False) -> None:
    apk = PAYLOAD / "tater-show.apk"
    try:
        adb.run("install", "--no-streaming", "-r", "-g", str(apk))
    except InstallError as exc:
        if "UPDATE_INCOMPATIBLE" in str(exc):
            raise InstallError(
                "a preview signed by a different development key is installed; "
                "run ./install.sh --uninstall, then install again") from exc
        raise
    package = adb.shell(f"pm path {PACKAGE}")
    if not package.startswith("package:"):
        raise InstallError("Android did not report the installed Tater Show package")
    # Android 7 gates BLE scan results on both the runtime location grant and
    # Location being enabled, even for a fixed appliance that never requests
    # coordinates. The Show APK only consumes raw BLE advertisements.
    adb.shell(f"pm grant {PACKAGE} android.permission.ACCESS_FINE_LOCATION", check=False)
    adb.shell(f"pm grant {PACKAGE} android.permission.CAMERA", check=False)
    adb.shell("settings put secure location_mode 3", check=False)
    if set_home:
        result = adb.shell(f"cmd package set-home-activity --user 0 {ACTIVITY}", check=False)
        if "error" in result.lower() or "failed" in result.lower():
            print(f"Warning: Android could not select Tater Show as HOME: {result or 'command rejected'}")
            print("The preview will still launch once; use --no-home on later runs to suppress this warning.")
    demo_extra = " --ez demo true" if demo else ""
    result = adb.shell(f"am force-stop {PACKAGE}; am start -W -n {ACTIVITY}{demo_extra}")
    if "error" in result.lower() or "exception" in result.lower():
        raise InstallError(f"Android installed Tater Show but could not launch it: {result}")


def install_native(adb: Adb, root: RootShell) -> None:
    mapping = {
        "server": "/data/local/bin/server_a",
        "libtater_microwakeword.so": "/data/local/share/tater/microwakeword/libtater_microwakeword.so",
        "hey_tater.tflite": "/data/local/share/tater/microwakeword/hey_tater.tflite",
        "hey_tater.json": "/data/local/share/tater/microwakeword/hey_tater.json",
        "wpa_supplicant-ap": "/data/local/lib/tater/wpa_supplicant-ap",
        "module.prop": "/data/adb/modules/tater_checkers/module.prop",
        "sepolicy.rule": "/data/adb/modules/tater_checkers/sepolicy.rule",
        "post-fs-data.sh": "/data/adb/modules/tater_checkers/post-fs-data.sh",
        "service.sh": "/data/adb/modules/tater_checkers/service.sh",
        "privacy.sh": "/data/adb/modules/tater_checkers/privacy.sh",
        "privacy-packages.txt": "/data/adb/modules/tater_checkers/privacy-packages.txt",
        "privacy-components.txt": "/data/adb/modules/tater_checkers/privacy-components.txt",
        "speech-interaction-manager.replace": (
            "/data/adb/modules/tater_checkers/system/priv-app/"
            "SpeechInteractionManager/.replace"),
        "bishop.replace": (
            "/data/adb/modules/tater_checkers/system/priv-app/"
            "com.amazon.bishop/.replace"),
        "uninstall.sh": "/data/adb/modules/tater_checkers/uninstall.sh",
    }
    # A repair install can run while the old setup AP executable and daemon
    # are active. Stop the existing supervisor first so it cannot race the new
    # generation or immediately restart the old server during replacement.
    root.run(
        "old_supervisor=$(cat /data/local/etc/tater/checkers-service.pid 2>/dev/null); "
        "case $old_supervisor in *[!0-9]*|\"\") ;; *) "
        "[ $old_supervisor -gt 2 ] && kill $old_supervisor 2>/dev/null || true;; esac; "
        "for old_server in $(pidof server 2>/dev/null); do "
        "old_parent=$(sed -n \"s/^PPid:[[:space:]]*//p\" /proc/$old_server/status 2>/dev/null); "
        "case $old_parent in *[!0-9]*|\"\") ;; *) "
        "[ $old_parent -gt 2 ] && kill $old_parent 2>/dev/null || true;; esac; "
        "kill $old_server 2>/dev/null || true; done; sleep 2")
    root.run(
        f"rm -rf {REMOTE_STAGE}; mkdir -p {REMOTE_STAGE} /data/local/bin "
        "/data/local/lib/tater /data/local/share/tater/microwakeword "
        "/data/local/etc/tater /data/local/etc/tater/ota /data/adb/modules/tater_checkers "
        "/data/adb/modules/tater_checkers/system/priv-app/SpeechInteractionManager "
        "/data/adb/modules/tater_checkers/system/priv-app/com.amazon.bishop; "
        f"chown 2000:2000 {REMOTE_STAGE}; chmod 700 {REMOTE_STAGE}; "
        "rm -f /data/local/etc/tater/ota/pending.env /data/local/etc/tater/ota/healthy "
        "/data/local/etc/tater/ota/rollback.apk /data/local/etc/tater/ota/screen.apk")
    for name in mapping:
        source = PAYLOAD / name
        if not source.is_file():
            raise InstallError(f"the bundled native payload is missing: {name}")
        adb.run("push", str(source), f"{REMOTE_STAGE}/{name}")
    for name, destination in mapping.items():
        # Rename a fully copied sibling over the old inode. Android otherwise
        # returns ETXTBSY when repairing a unit whose AP supplicant is active.
        root.run(
            f"rm -f {destination}.new; cp {REMOTE_STAGE}/{name} {destination}.new; "
            f"mv -f {destination}.new {destination}")
    root.run(
        "rm -f /data/local/bin/server_b.new; "
        "cp /data/local/bin/server_a /data/local/bin/server_b.new; "
        "chmod 755 /data/local/bin/server_b.new; "
        "mv -f /data/local/bin/server_b.new /data/local/bin/server_b; "
        "chmod 755 /data/local/bin/server_a /data/local/bin/server_b "
        "/data/local/lib/tater/wpa_supplicant-ap "
        "/data/local/share/tater/microwakeword/libtater_microwakeword.so "
        "/data/adb/modules/tater_checkers/post-fs-data.sh "
        "/data/adb/modules/tater_checkers/service.sh "
        "/data/adb/modules/tater_checkers/privacy.sh "
        "/data/adb/modules/tater_checkers/uninstall.sh; "
        "chmod 644 /data/local/share/tater/microwakeword/hey_tater.tflite "
        "/data/local/share/tater/microwakeword/hey_tater.json "
        "/data/adb/modules/tater_checkers/module.prop "
        "/data/adb/modules/tater_checkers/privacy-packages.txt "
        "/data/adb/modules/tater_checkers/privacy-components.txt "
        "/data/adb/modules/tater_checkers/system/priv-app/SpeechInteractionManager/.replace "
        "/data/adb/modules/tater_checkers/system/priv-app/com.amazon.bishop/.replace "
        "/data/adb/modules/tater_checkers/sepolicy.rule; "
        "ln -sf server_a /data/local/bin/server; "
        "test -e /data/local/etc/tater/setup_enabled || : > /data/local/etc/tater/setup_enabled; "
        f"rm -rf {REMOTE_STAGE}; sync")
    # PackageManager is available during installation. Build and validate the
    # UID cache synchronously so Magisk can restore the firewall in its early
    # post-fs-data phase before Android services make public connections.
    root.run("/system/bin/sh /data/adb/modules/tater_checkers/privacy.sh firewall")
    # Start this boot immediately. Magisk runs the same script automatically
    # on every later boot.
    root.run(
        "nohup /system/bin/sh /data/adb/modules/tater_checkers/service.sh "
        ">/data/local/tmp/tater-checkers-service-launch.log 2>&1 &")


def uninstall(adb: Adb, root: RootShell) -> None:
    root.run(
        "pkill -x server >/dev/null 2>&1 || true; "
        "if [ -x /data/adb/modules/tater_checkers/uninstall.sh ]; then "
        "/system/bin/sh /data/adb/modules/tater_checkers/uninstall.sh; fi; "
        "pm enable --user 0 com.amazon.ds2.oobe.efd >/dev/null 2>&1 || true; "
        "rm -rf /data/adb/modules/tater_checkers; sync")
    adb.run("uninstall", PACKAGE, check=False)
    print("Tater Show and its boot supervisor were removed; Amazon OOBE was re-enabled.")


def collect_profile(adb: Adb) -> None:
    script = ROOT / "tools" / "profile.sh"
    if not script.is_file():
        raise InstallError("the bundled hardware profiler is missing")
    command = [str(script)]
    if adb.serial:
        command += ["-s", adb.serial]
    try:
        subprocess.run(command, check=True)
    except subprocess.CalledProcessError as exc:
        raise InstallError(f"hardware profile failed with exit code {exc.returncode}") from exc


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Install Tater firmware on Echo Show 5 checkers")
    parser.add_argument("--adb", default="adb", help="path to adb")
    parser.add_argument("--serial", help="ADB serial when more than one device is connected")
    parser.add_argument(
        "--no-home", action="store_true",
        help="install only the screen preview without native boot services (required on LineageOS)")
    parser.add_argument("--profile", action="store_true", help="also collect the read-only Checkers hardware profile")
    parser.add_argument("--demo", action="store_true", help="cycle all screen states without the native audio service")
    parser.add_argument("--uninstall", action="store_true", help="remove the preview app")
    parser.add_argument("--yes", action="store_true", help="confirm amonet 2.0.1+ and working TWRP non-interactively")
    parser.add_argument("--verify-bundle", action="store_true", help="verify release files and exit")
    return parser.parse_args()


def main() -> int:
    try:
        args = parse_args()
        manifest = load_manifest()
        verify_bundle(manifest)
        print(f"Tater Echo Firmware {manifest['version']} Checkers bundle verified.")
        if args.verify_bundle:
            return 0
        adb = select_adb(args.adb, args.serial)
        userspace = require_checkers(adb)
        root = resolve_root(adb)
        print(f"Verified Checkers running {userspace} with root and an amonet 2.x layout.")
        require_supported_install_mode(userspace, args.no_home, args.uninstall)
        if args.uninstall:
            uninstall(adb, root)
            return 0
        confirm_unlock(args.yes)
        install_apk(adb, not args.no_home, args.demo)
        if not args.no_home and not args.demo:
            install_native(adb, root)
            print("Tater native firmware and persistent boot supervision are installed.")
            print("Connect to the Tater-Setup hotspot shown on the Echo Show to finish setup.")
        else:
            print("Tater Show screen preview is installed and running without native boot services.")
        if args.profile:
            collect_profile(adb)
        else:
            print("Next: run ./install.sh --profile and send the generated checkers profile archive.")
        return 0
    except (InstallError, KeyboardInterrupt) as exc:
        print(f"\nERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
