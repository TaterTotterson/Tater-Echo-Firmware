#!/usr/bin/env python3
"""Install Tater Linux on an unlocked Checkers through USB and TWRP."""

from __future__ import annotations

import argparse
import getpass
import hashlib
import importlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from urllib import error as urlerror
from urllib import request as urlrequest


ROOT = Path(__file__).resolve().parent
MANIFEST = ROOT / "bundle-manifest.json"
PAYLOAD = ROOT / "payload"
TECHO_TOOLS = ROOT / "tools" / "techo5"
PROVISION = ROOT / "tools" / "provision_console.py"
CHECKERS_LINEAGE_SHA256 = "785fa643fd68b2e6f6f02d96a2da58373c6a577b92a27cf6cec69603bb94068e"
CHECKERS_LINEAGE_NAME = "lineage-18.1-20260904-UNOFFICIAL-checkers.zip"
CHECKERS_LINEAGE_URL = (
    "https://github.com/amazon-oss/releases/releases/download/"
    "lineage-18.1-checkers-v0.7/" + CHECKERS_LINEAGE_NAME
)
CHECKERS_LINEAGE_MAX_BYTES = 1024 * 1024 * 1024


class InstallError(RuntimeError):
    pass


def sha256(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def verify_lineage_zip(path: Path | None) -> None:
    if path is None:
        return
    if not path.is_file():
        raise InstallError("LineageOS zip not found: %s" % path)
    if sha256(path) != CHECKERS_LINEAGE_SHA256:
        raise InstallError("LineageOS zip SHA-256 does not match the tested Checkers v0.7 image")


def download_lineage_zip(work: Path) -> Path:
    """Fetch the pinned vendor-driver image once, without trusting partial files."""
    destination = work / CHECKERS_LINEAGE_NAME
    if destination.is_file():
        try:
            verify_lineage_zip(destination)
        except InstallError:
            print("Cached LineageOS ZIP failed verification; downloading a fresh copy.", flush=True)
        else:
            print("Using verified cached LineageOS ZIP: %s" % destination, flush=True)
            return destination
    elif destination.exists():
        raise InstallError("LineageOS cache path is not a file: %s" % destination)

    print("Downloading the pinned Checkers LineageOS ZIP from %s" % CHECKERS_LINEAGE_URL, flush=True)
    temporary = None
    try:
        work.mkdir(parents=True, exist_ok=True)
        request = urlrequest.Request(CHECKERS_LINEAGE_URL, headers={"User-Agent": "Tater-Checkers-Installer"})
        with urlrequest.urlopen(request, timeout=30) as response:
            length = response.headers.get("Content-Length")
            if length and int(length) > CHECKERS_LINEAGE_MAX_BYTES:
                raise InstallError("LineageOS download exceeds the 1 GiB safety limit")
            with tempfile.NamedTemporaryFile(prefix="lineage-", suffix=".partial", dir=work, delete=False) as output:
                temporary = Path(output.name)
                digest = hashlib.sha256()
                total = 0
                next_report = 64 * 1024 * 1024
                while chunk := response.read(1024 * 1024):
                    total += len(chunk)
                    if total > CHECKERS_LINEAGE_MAX_BYTES:
                        raise InstallError("LineageOS download exceeds the 1 GiB safety limit")
                    output.write(chunk)
                    digest.update(chunk)
                    if total >= next_report:
                        print("Downloaded %d MiB..." % (total // (1024 * 1024)), flush=True)
                        next_report += 64 * 1024 * 1024
                output.flush()
                os.fsync(output.fileno())
        if digest.hexdigest() != CHECKERS_LINEAGE_SHA256:
            raise InstallError("downloaded LineageOS ZIP failed SHA-256 verification; no device changes were made")
        os.replace(temporary, destination)
        temporary = None
        verify_lineage_zip(destination)
        print("Verified LineageOS ZIP (%d bytes): %s" % (total, destination), flush=True)
        return destination
    except (OSError, ValueError, urlerror.URLError) as error:
        raise InstallError(
            "could not download the pinned LineageOS ZIP: %s. "
            "Download it manually from %s and pass --lineage-zip PATH"
            % (error, CHECKERS_LINEAGE_URL)
        ) from error
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def load_manifest() -> dict:
    try:
        manifest = json.loads(MANIFEST.read_text())
    except (OSError, ValueError) as error:
        raise InstallError("cannot read bundle-manifest.json: %s" % error) from error
    if manifest.get("target") != "checkers" or manifest.get("base_os") != "tater-linux":
        raise InstallError("this is not a Checkers Tater Linux factory bundle")
    if not isinstance(manifest.get("files"), dict) or not manifest["files"]:
        raise InstallError("bundle manifest has no files")
    return manifest


def verify_bundle(manifest: dict) -> None:
    for relative, metadata in sorted(manifest["files"].items()):
        path = ROOT / relative
        try:
            resolved = path.resolve(strict=True)
        except OSError as error:
            raise InstallError("bundle file is missing: %s" % relative) from error
        if ROOT not in resolved.parents:
            raise InstallError("bundle manifest path escapes its directory: %s" % relative)
        if resolved.stat().st_size != int(metadata.get("size", -1)):
            raise InstallError("bundle file has the wrong size: %s" % relative)
        if sha256(resolved) != metadata.get("sha256"):
            raise InstallError("bundle file failed SHA-256 verification: %s" % relative)


def adb_base(binary: str, serial: str | None = None) -> list[str]:
    command = [binary]
    if serial:
        command += ["-s", serial]
    return command


def run(command: list[str], *, capture=False, output=None, check=True):
    try:
        return subprocess.run(
            command,
            check=check,
            stdout=subprocess.PIPE if capture else output,
            stderr=subprocess.PIPE if capture else None,
        )
    except FileNotFoundError as error:
        raise InstallError("required command was not found: %s" % command[0]) from error
    except subprocess.CalledProcessError as error:
        detail = (error.stderr or error.stdout or b"").decode(errors="replace").strip()
        raise InstallError("%s failed: %s" % (" ".join(command), detail or error.returncode)) from error


def adb_text(binary: str, serial: str, *args: str, check=True) -> str:
    completed = run(adb_base(binary, serial) + list(args), capture=True, check=check)
    return completed.stdout.decode(errors="replace").replace("\r", "").strip()


def select_device(binary: str, requested: str | None) -> tuple[str, str]:
    output = run([binary, "devices"], capture=True).stdout.decode(errors="replace")
    devices = []
    for line in output.splitlines()[1:]:
        fields = line.split()
        if len(fields) >= 2 and fields[1] == "recovery":
            devices.append((fields[0], fields[1]))
    if requested:
        matches = [item for item in devices if item[0] == requested]
        if len(matches) != 1:
            raise InstallError("ADB device %r is not connected" % requested)
        return matches[0]
    if len(devices) != 1:
        raise InstallError("expected exactly one device in TWRP, found %d; use --serial" % len(devices))
    return devices[0]


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise InstallError("cannot load bundled tool %s" % path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_platform_installer(args, manifest: dict, serial: str) -> None:
    sys.path.insert(0, str(TECHO_TOOLS))
    techo5lib = importlib.import_module("techo5lib")
    installer = load_module("tater_techo5_install_show", TECHO_TOOLS / "install-show.py")

    class BundledRelease:
        def __init__(self, _repo, _release, _work):
            self.version = manifest["version"]

    installer.Release = BundledRelease
    argv = [
        str(TECHO_TOOLS / "install-show.py"),
        "--serial", serial,
        "--boot", str(PAYLOAD / "boot.img"),
        "--rootfs", str(PAYLOAD / "rootfs.tar.gz"),
        "--backups", str(args.backups),
        "--work", str(args.work),
        "--adb", args.adb,
        "--fastboot", args.fastboot,
    ]
    if args.lineage_zip:
        argv += ["--lineage-zip", str(args.lineage_zip)]
    if args.name:
        argv += ["--name", args.name]
    if args.wifi:
        argv += ["--wifi", args.wifi]
    if args.wifi_passphrase_file:
        argv += ["--wifi-passphrase-file", str(args.wifi_passphrase_file)]
    if args.force:
        argv.append("--force")
    before = sys.argv
    try:
        sys.argv = argv
        try:
            installer.main()
        except techo5lib.Fail as error:
            raise InstallError(str(error)) from error
    finally:
        sys.argv = before


def finish_tater_provisioning(args, serial: str) -> None:
    # A fresh unit is intentionally left unpaired. Its local setup network
    # takes the Wi-Fi and one-time code from the user after USB flashing ends.
    if not args.usb_pair and not args.tater_url and not args.pairing_code:
        print("\nFactory flash complete. Join the open Tater-Setup network shown on Checkers,")
        print("then open http://192.168.4.1.")
        print("Create an Add Satellite pairing code in Tater and enter it there.")
        return
    if not args.usb_pair and (not args.tater_url or not args.pairing_code):
        raise InstallError("pass both --tater-url and --pairing-code for USB pairing, or neither for hotspot setup")

    print("\nCreate an Add Satellite pairing code in Tater now.")
    server = (args.tater_url or input("Tater server URL (for example http://10.0.0.5:8501): ")).strip()
    code = (args.pairing_code or getpass.getpass("One-time pairing code: ")).replace(" ", "").strip()
    if not server or not code:
        raise InstallError("Tater server and pairing code are required")
    name = (args.name or "Tater Show").strip()
    native = json.dumps({
        "url": server,
        "token": code,
        "device_name": name,
        "room": (args.room or "").strip(),
    }, indent=2).encode() + b"\n"
    provision = load_module("tater_checkers_provision", PROVISION)
    provision.provision_console(serial, str(TECHO_TOOLS), {
        "native": native,
        "token": code.encode() + b"\n",
    })
    print("Saved the Tater pairing bootstrap; Checkers is rebooting to redeem it.")


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify-bundle", action="store_true")
    parser.add_argument("--serial")
    parser.add_argument("--lineage-zip", type=Path,
                        help="use a local pinned Checkers LineageOS 18.1 v0.7 ZIP instead of downloading it")
    parser.add_argument("--name")
    parser.add_argument("--room")
    parser.add_argument("--wifi")
    parser.add_argument("--wifi-passphrase-file", type=Path)
    parser.add_argument("--tater-url")
    parser.add_argument("--pairing-code")
    parser.add_argument("--usb-pair", action="store_true",
                        help="enter the Tater server and pairing code over USB instead of using the setup hotspot")
    parser.add_argument("--force", action="store_true")
    parser.add_argument("--backups", type=Path, default=ROOT / "backups")
    parser.add_argument("--work", type=Path, default=ROOT / "build")
    parser.add_argument("--adb", default="adb")
    parser.add_argument("--fastboot", default="fastboot")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    manifest = load_manifest()
    verify_bundle(manifest)
    if args.verify_bundle:
        print("Bundle verified: Checkers Tater Linux %s" % manifest["version"])
        return 0
    verify_lineage_zip(args.lineage_zip)

    serial, _state = select_device(args.adb, args.serial)
    if not args.name:
        args.name = "Tater Checkers"
    product = adb_text(args.adb, serial, "shell", "getprop", "ro.product.device").lower()
    if product != "checkers":
        raise InstallError("connected device reports %r, not checkers" % product)
    if args.lineage_zip is None:
        args.lineage_zip = download_lineage_zip(args.work)
    run_platform_installer(args, manifest, serial)
    finish_tater_provisioning(args, serial)
    print("\nCheckers now runs Tater Linux %s; TWRP remains in recovery." % manifest["version"])
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except InstallError as error:
        print("error: %s" % error, file=sys.stderr)
        raise SystemExit(1)
