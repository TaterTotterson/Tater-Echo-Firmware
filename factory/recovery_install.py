#!/usr/bin/env python3
"""Repair an installed Tater Echo from its Tater Recovery USB console."""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import shlex
import sys
import tarfile
import time


ROOT = Path(__file__).resolve().parent
TOOL_PATHS = (ROOT / "tools", ROOT / "tools/techo5")
REMOTE_DIR = "/data/tater-recovery"
REMOTE_ROOTFS = REMOTE_DIR + "/rootfs.tar.gz"


class Fail(Exception):
    """A user-facing recovery installation failure."""


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_manifest() -> dict:
    try:
        data = json.loads((ROOT / "bundle-manifest.json").read_text())
    except (OSError, ValueError) as error:
        raise Fail(f"cannot read bundle manifest: {error}") from error
    if data.get("target") not in {"biscuit", "radar", "checkers", "crown", "athena", "rook"}:
        raise Fail("this is not a supported Tater Echo factory bundle")
    if data.get("base_os") != "tater-linux" or not isinstance(data.get("files"), dict):
        raise Fail("this bundle does not contain a Tater Linux recovery payload")
    return data


def verify_bundle(data: dict) -> None:
    for relative, metadata in sorted(data["files"].items()):
        path = (ROOT / relative).resolve()
        if ROOT.resolve() not in path.parents or not path.is_file():
            raise Fail(f"bundle file is missing or unsafe: {relative}")
        if path.stat().st_size != int(metadata.get("size", -1)):
            raise Fail(f"bundle file has the wrong size: {relative}")
        if sha256(path) != metadata.get("sha256"):
            raise Fail(f"bundle file failed SHA-256 verification: {relative}")


def verify_rootfs(rootfs: Path, target: str, version: str) -> str:
    try:
        with tarfile.open(rootfs, "r:*") as archive:
            member = next(
                item for item in archive.getmembers()
                if item.name.lstrip("./") == "etc/tater-release" and item.isfile()
            )
            source = archive.extractfile(member)
            if source is None:
                raise KeyError("etc/tater-release")
            release = source.read(4096).decode("utf-8", "replace").strip()
    except (OSError, KeyError, StopIteration, tarfile.TarError) as error:
        raise Fail(f"the bundled root filesystem is unreadable: {error}") from error
    target_matches = re.search(rf"\b{re.escape(target)}\b", release, re.IGNORECASE)
    version_matches = re.search(
        rf"(?<![A-Za-z0-9.+-]){re.escape(version)}(?![A-Za-z0-9.+-])", release
    )
    if not target_matches or not version_matches:
        raise Fail(
            f"the bundled root filesystem identifies itself as {release!r}, "
            f"not {target} {version}"
        )
    return release


def load_console_library():
    for path in TOOL_PATHS:
        library = path / "techo5lib.py"
        if not library.is_file():
            continue
        spec = importlib.util.spec_from_file_location("tater_repair_techo5lib", library)
        if spec is None or spec.loader is None:
            break
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    raise Fail("the factory archive is incomplete: bundled techo5lib.py is missing")


def available_ports(console_library) -> list[str]:
    ports: list[str] = []
    for ids in (console_library.CONSOLE_DOT, console_library.CONSOLE_TECHO5):
        for port, _serial in console_library.list_consoles(ids):
            if port not in ports:
                ports.append(port)
    return ports


def select_port(console_library, requested: str | None) -> str:
    if requested:
        return requested
    ports = available_ports(console_library)
    if not ports:
        raise Fail(
            "no Tater Recovery USB console was found; enter recovery with Volume Up "
            "or to_recovery.py, keep USB connected, and close any serial terminal"
        )
    if len(ports) != 1:
        raise Fail("more than one recovery console is connected; pass --port followed by one of: " +
                   ", ".join(ports))
    return ports[0]


def exchange(console_library, port: str, command: str, wait: int) -> str:
    try:
        answer = console_library.console_exchange(port, command, wait)
    except OSError as error:
        raise Fail(f"could not open recovery console {port}: {error}") from error
    if answer is None:
        raise Fail(f"Tater Recovery on {port} did not answer")
    return answer


def probe_recovery(console_library, port: str, expected_target: str) -> str:
    command = r'''
if [ ! -s /run/techo5/slot ] && [ -x /usr/local/sbin/slotctl ] &&
   grep -q ' /data ' /proc/mounts; then
    touch /tmp/stay
    store=/store
    [ -e /.techo5-store ] && store=/
    release=
    for f in "$store"/techo5/slots/*/etc/tater-release "$store"/slots/*/etc/tater-release; do
        [ -s "$f" ] || continue
        release=$(cat "$f")
        break
    done
    if [ -n "$release" ]; then
        echo TATER_RECOVERY_READY
        echo "TATER_RELEASE=$release"
        STORE="$store" slotctl status
    fi
fi
'''.strip()
    answer = exchange(console_library, port, command, 10)
    lines = answer.splitlines()
    if "TATER_RECOVERY_READY" not in lines:
        raise Fail(
            "the connected console is not an installed satellite in Tater Recovery; "
            "normal Tater Linux and TWRP are intentionally refused"
        )
    release = next((line.removeprefix("TATER_RELEASE=") for line in lines
                    if line.startswith("TATER_RELEASE=")), "")
    if not re.search(rf"\b{re.escape(expected_target)}\b", release, re.IGNORECASE):
        raise Fail(
            f"this bundle is for {expected_target}, but recovery reports {release or 'an unknown target'}"
        )
    return release


def prepare_upload(console_library, port: str, size: int) -> None:
    need_kib = (size + 1023) // 1024 + 8192
    command = (
        f"mkdir -p {REMOTE_DIR}; rm -f {REMOTE_ROOTFS}.partial; "
        "available=$(df -Pk /data | awk 'END {print $4}'); "
        f"if [ -n \"$available\" ] && [ \"$available\" -ge {need_kib} ]; then "
        "echo TATER_UPLOAD_SPACE_OK; else echo TATER_UPLOAD_NO_SPACE:$available; fi"
    )
    answer = exchange(console_library, port, command, 10)
    if "TATER_UPLOAD_SPACE_OK" not in answer.splitlines():
        raise Fail(f"recovery does not have enough free userdata space for the repair payload: {answer}")


def _wait_for_pattern(serial_port, pattern: re.Pattern[str], timeout: int) -> re.Match[str]:
    deadline = time.monotonic() + timeout
    buffered = b""
    while time.monotonic() < deadline:
        chunk = serial_port.read()
        if chunk:
            buffered = (buffered + chunk)[-65536:]
            text = buffered.decode("utf-8", "replace").replace("\r", "")
            for line in text.split("\n"):
                match = pattern.fullmatch(line.strip())
                if match:
                    return match
        else:
            time.sleep(0.02)
    raise Fail("the recovery console timed out during payload transfer")


def upload_rootfs(console_library, port: str, source: Path) -> None:
    size = source.stat().st_size
    digest = sha256(source)
    prepare_upload(console_library, port, size)
    remote_partial = REMOTE_ROOTFS + ".partial"
    command = (
        "u=$$; stty raw -echo; printf '\\nTATER_UPLOAD_READY_%s\\n' \"$u\"; "
        f"head -c {size} > {shlex.quote(remote_partial)}; sync; "
        f"got=$(sha256sum {shlex.quote(remote_partial)} | cut -d' ' -f1); "
        f"if [ \"$got\" = {shlex.quote(digest)} ]; then "
        f"mv -f {shlex.quote(remote_partial)} {shlex.quote(REMOTE_ROOTFS)}; fi; "
        "printf '\\nTATER_UPLOAD_DONE_%s %s\\n' \"$u\" \"$got\"; stty sane"
    )
    try:
        serial_port = console_library.SerialPort(port)
    except OSError as error:
        raise Fail(f"could not open {port} for recovery upload: {error}") from error
    try:
        serial_port.write(b"\n")
        time.sleep(0.3)
        serial_port.read()
        serial_port.write((command + "\n").encode("utf-8"))
        ready = _wait_for_pattern(serial_port, re.compile(r"TATER_UPLOAD_READY_(\d+)"), 15)
        upload_id = ready.group(1)
        sent = 0
        next_report = 16 * 1024 * 1024
        # Windows' bundled serial wrapper uses a two-second synchronous write
        # timeout and does not expose partial-write counts.  Small writes stay
        # comfortably below that limit even on a conservative CDC driver;
        # POSIX can use larger writes because techo5lib retries EAGAIN and
        # drains every memoryview completely.
        chunk_size = 4096 if getattr(console_library, "IS_WINDOWS", False) else 64 * 1024
        with source.open("rb") as payload:
            while chunk := payload.read(chunk_size):
                serial_port.write(chunk)
                sent += len(chunk)
                if sent >= next_report:
                    print(f"   transferred {sent // (1024 * 1024)} MiB", flush=True)
                    next_report += 16 * 1024 * 1024
        done = _wait_for_pattern(
            serial_port,
            re.compile(rf"TATER_UPLOAD_DONE_{re.escape(upload_id)} ([0-9a-f]{{64}})"),
            180,
        )
    except (OSError, TimeoutError) as error:
        raise Fail(f"USB recovery transfer failed after {sent if 'sent' in locals() else 0} bytes: {error}") from error
    finally:
        serial_port.close()
    if done.group(1) != digest:
        raise Fail(
            f"the recovery payload failed verification on the satellite "
            f"({done.group(1)} != {digest}); it was not installed"
        )
    print(f"   transferred and verified {size} bytes", flush=True)


def install_rootfs(console_library, port: str, target: str, factory_reset: bool) -> str:
    reset = ""
    if factory_reset:
        reset = (
            "mkdir -p /data/local/etc/tater; "
            "printf '1\\n' > /data/local/etc/tater/setup_enabled; "
            "rm -f /data/local/etc/tater/native.json /data/local/etc/tater/device_token "
            "/data/tater-linux/wpa_supplicant.conf; "
        )
    command = (
        "store=/store; [ -e /.techo5-store ] && store=/; "
        f"if STORE=\"$store\" slotctl install {shlex.quote(REMOTE_ROOTFS)} "
        ">/tmp/tater-recovery-install.log 2>&1; then "
        "rm -rf /data/tater-linux/app /data/tater-linux/app.new; "
        f"{reset}"
        f"rm -f {shlex.quote(REMOTE_ROOTFS)}; sync; "
        "echo TATER_RECOVERY_INSTALL_OK; STORE=\"$store\" slotctl status; "
        "else echo TATER_RECOVERY_INSTALL_FAILED; tail -20 /tmp/tater-recovery-install.log; fi"
    )
    answer = exchange(console_library, port, command, 900)
    if "TATER_RECOVERY_INSTALL_OK" not in answer.splitlines():
        raise Fail(f"Tater Recovery could not install the repair slot:\n{answer}")
    if not re.search(rf"Tater Linux {re.escape(target)}\b", answer, re.IGNORECASE):
        raise Fail(f"the new slot did not identify itself as {target}:\n{answer}")
    return answer


def reboot_repaired(console_library, port: str) -> None:
    command = (
        "sync; (sleep 2; if [ -x /sbin/techo5-retry ]; then /sbin/techo5-retry; "
        "else /bin/busybox.static reboot -f; fi) >/tmp/tater-recovery-reboot.log 2>&1 & "
        "echo TATER_RECOVERY_REBOOTING"
    )
    answer = exchange(console_library, port, command, 5)
    if "TATER_RECOVERY_REBOOTING" not in answer.splitlines():
        raise Fail("the repaired slot is installed, but recovery did not acknowledge the reboot")


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repair", action="store_true", required=True,
                        help="install this bundle into the inactive root filesystem slot")
    parser.add_argument("--factory-reset", action="store_true",
                        help="also clear Wi-Fi and Tater pairing after the verified repair")
    parser.add_argument("--port", help="specific recovery USB serial port")
    parser.add_argument("--yes", action="store_true", help="skip the REPAIR or ERASE prompt")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    try:
        args = parse_args(argv)
        info = load_manifest()
        verify_bundle(info)
        rootfs = ROOT / "payload/rootfs.tar.gz"
        release = verify_rootfs(rootfs, info["target"], info["version"])
        console_library = load_console_library()
        port = select_port(console_library, args.port)
        current = probe_recovery(console_library, port, info["target"])
        print(f"== Tater Recovery on {port}")
        print(f"   current: {current}")
        print(f"   repair:  {release}")
        if args.factory_reset:
            print("   Wi-Fi and Tater pairing will be cleared after the repair.")
        else:
            print("   Wi-Fi, pairing, room, and satellite settings will be preserved.")
        if not args.yes:
            word = "ERASE" if args.factory_reset else "REPAIR"
            if input(f"Type {word} to continue: ").strip() != word:
                raise Fail("recovery installation cancelled")
        print("== transferring the verified root filesystem", flush=True)
        upload_rootfs(console_library, port, rootfs)
        print("== installing the inactive A/B root filesystem slot", flush=True)
        status = install_rootfs(console_library, port, info["target"], args.factory_reset)
        for line in status.splitlines():
            if line.startswith(("active:", "slot ")):
                print("   " + line)
        reboot_repaired(console_library, port)
        if args.factory_reset:
            print("Repair complete. The satellite is rebooting into Tater setup mode.")
        else:
            print("Repair complete. The satellite is rebooting; the new slot remains on trial until healthy.")
        return 0
    except (Fail, OSError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
