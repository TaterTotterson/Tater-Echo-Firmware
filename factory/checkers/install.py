#!/usr/bin/env python3
"""Install the Tater Show developer preview on an unlocked Checkers.

This first-stage installer intentionally writes no partitions and installs no
native audio service. It verifies Checkers + root on stock Fire OS 6 or the
later LineageOS test environment, installs the screen APK, and optionally
makes it the HOME activity. The hardware profile should be captured on stock
Fire OS before its original ALSA and microphone configuration is replaced.
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


def install_apk(adb: Adb, set_home: bool, demo: bool = False) -> None:
    apk = PAYLOAD / "tater-show.apk"
    try:
        adb.run("install", "--no-streaming", "-r", str(apk))
    except InstallError as exc:
        if "UPDATE_INCOMPATIBLE" in str(exc):
            raise InstallError(
                "a preview signed by a different development key is installed; "
                "run ./install.sh --uninstall, then install again") from exc
        raise
    package = adb.shell(f"pm path {PACKAGE}")
    if not package.startswith("package:"):
        raise InstallError("Android did not report the installed Tater Show package")
    if set_home:
        result = adb.shell(f"cmd package set-home-activity --user 0 {ACTIVITY}", check=False)
        if "error" in result.lower() or "failed" in result.lower():
            print(f"Warning: Android could not select Tater Show as HOME: {result or 'command rejected'}")
            print("The preview will still launch once; use --no-home on later runs to suppress this warning.")
    demo_extra = " --ez demo true" if demo else ""
    result = adb.shell(f"am force-stop {PACKAGE}; am start -W -n {ACTIVITY}{demo_extra}")
    if "error" in result.lower() or "exception" in result.lower():
        raise InstallError(f"Android installed Tater Show but could not launch it: {result}")


def uninstall(adb: Adb) -> None:
    adb.run("uninstall", PACKAGE, check=False)
    print("Tater Show removed. Android will ask for a HOME app the next time Home is opened.")


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
    parser = argparse.ArgumentParser(description="Install the Tater Show preview on Echo Show 5 checkers")
    parser.add_argument("--adb", default="adb", help="path to adb")
    parser.add_argument("--serial", help="ADB serial when more than one device is connected")
    parser.add_argument("--no-home", action="store_true", help="launch once without making Tater Show the HOME app")
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
        print(f"Verified Checkers running {userspace} with root and an amonet 2.x layout.")
        if args.uninstall:
            uninstall(adb)
            return 0
        confirm_unlock(args.yes)
        install_apk(adb, not args.no_home, args.demo)
        print("Tater Show is installed and running.")
        print("The screen will report Native service offline until the measured Checkers audio build is installed.")
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
