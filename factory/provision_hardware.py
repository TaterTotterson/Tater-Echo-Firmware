#!/usr/bin/env python3
"""Verify/install owner-local Checkers/Cronos/Rook libraries; --install opts in.

This script and its hashes may be published; the private runtime directory
and transfer archive must never be included in firmware/release artifacts.
"""
from __future__ import annotations

import argparse
import hashlib
import io
import json
from pathlib import Path
import re
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT))
import recovery_install as usb  # noqa: E402

REMOTE_DIR = "/data/tater-hardware-provision"
REMOTE_FILE = REMOTE_DIR + "/private-runtime.tar"
RUNTIMES = {
    "tater-gpu": ("bc75d30a7be167b827e89b1d7629ec5dc4b528c99106d0b1e9beed02b4815561", 34, 37_506_624),
    "tater-vdec": ("490a3c771d1c6f979828e60b93c363ec42403ca9bd3c98b12f8c5cd0761f2cc4", 76, 12_839_369),
}
COMPONENT = "46ec0c93f5fc5b6e6512125397d35236c5be5759dbdb285bd29c4c4714e181fe"
CRONOS_RUNTIMES = {
    "tater-gpu": RUNTIMES["tater-gpu"],
    "tater-vdec": ("8564d383faba23c32545037f62a7ab9ddddf227a61790470dbc883e6eb871f0a", 66, 41_926_684),
}
CRONOS_COMPONENT = "161020e59faad3b1d250dcc3db168ce9109c08c8b2f02bc736109356f7ed5db4"
SAFE_NAME = re.compile(r"[A-Za-z0-9_./+@-]+\Z")


def profile(target: str):
    if target == "checkers":
        return RUNTIMES, COMPONENT
    if target in ("cronos", "rook"):
        return CRONOS_RUNTIMES, CRONOS_COMPONENT
    raise ValueError("unqualified private runtime target")


def files(directory: Path) -> list[Path]:
    result = []
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError("private runtime must be an ordinary directory")
    for path in sorted(directory.rglob("*")):
        relative = path.relative_to(directory).as_posix()
        if path.is_symlink() or not SAFE_NAME.fullmatch(relative) or ".." in Path(relative).parts:
            raise ValueError("private runtime contains an unsafe path")
        if path.is_file():
            if not 0 < path.stat().st_size <= 32 * 1024 * 1024:
                raise ValueError("private runtime file exceeds its boundary")
            result.append(path)
        elif not path.is_dir():
            raise ValueError("private runtime contains a special file")
    return result


def verify(directory: Path, *, target: str = "checkers") -> dict[str, str]:
    runtimes, component = profile(target)
    candidates = files(directory)
    manifest_path = directory / "private-runtime.json"
    if manifest_path.stat().st_size > 32 * 1024:
        raise ValueError("private runtime inventory exceeds its boundary")
    manifest = json.loads(manifest_path.read_text())
    inventory = {path.relative_to(directory).as_posix(): usb.sha256(path) for path in candidates if path != manifest_path}
    if manifest != {"schema": 1, "target": target, "private": True, "files": inventory}:
        raise ValueError("private runtime inventory does not match its contents")
    allowed = set()
    for runtime, identity in runtimes.items():
        paths = sorted(name for name in inventory if name == runtime + "/linker" or name.startswith(runtime + "/lib/"))
        # Tree hashing uses linker first, then sorted libraries, as in the
        # research preparer (lexical sorting would put lib/ before linker).
        paths.sort(key=lambda name: (name != runtime + "/linker", name))
        digest = hashlib.sha256()
        size = 0
        for name in paths:
            relative = name.removeprefix(runtime + "/")
            digest.update(relative.encode() + b"\0" + inventory[name].encode() + b"\n")
            size += (directory / name).stat().st_size
        if (digest.hexdigest(), len(paths), size) != identity:
            raise ValueError(f"private library tree is not qualified: {runtime}")
        expected = {"schema": 2, "runtime_id": target+"-mt8163-mali-pbuffer-1" if runtime == "tater-gpu" else target+"-mt8163-vdec-stock-1",
                    "tree_sha256": identity[0], "file_count": identity[1], "runtime_bytes": identity[2]}
        if json.loads((directory / runtime / "runtime.json").read_text()) != expected:
            raise ValueError("private library manifest is not qualified")
        allowed.update(paths)
        allowed.add(runtime + "/runtime.json")
    if inventory.get("tater-venc/libMtkOmxVenc.so") != component:
        raise ValueError("private encoder component is not qualified")
    if json.loads((directory / "tater-venc/runtime.json").read_text()) != {
        "schema": 2, "runtime_id": target+"-mt8163-venc-stock-1", "component_sha256": component,
        "base_tree_sha256": runtimes["tater-vdec"][0],
    }:
        raise ValueError("private encoder manifest is not qualified")
    allowed.update(("tater-venc/libMtkOmxVenc.so", "tater-venc/runtime.json"))
    if set(inventory) != allowed:
        raise ValueError("private runtime includes unexpected files or shared helpers")
    return inventory


def archive_runtime(directory: Path, inventory: dict[str, str], output: Path, *, target: str = "checkers") -> None:
    with tarfile.open(output, "x", format=tarfile.USTAR_FORMAT) as archive:
        for name in sorted(inventory):
            data = (directory / name).read_bytes()
            if hashlib.sha256(data).hexdigest() != inventory[name]:
                raise ValueError("private input changed while preparing the USB transfer")
            info = tarfile.TarInfo(name)
            info.mode = 0o555 if name.endswith("/linker") else 0o444
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
        # Transfer the bounded script as a file rather than flooding the USB
        # shell's canonical input buffer with hundreds of verification lines.
        script = install_command(inventory, target=target).encode()
        info = tarfile.TarInfo("provision.sh")
        info.size, info.mode = len(script), 0o700
        archive.addfile(info, io.BytesIO(script))
        transfer_inventory = {**inventory, "provision.sh": hashlib.sha256(script).hexdigest()}
        sums = "".join(f"{digest}  {name}\n" for name, digest in sorted(transfer_inventory.items())).encode()
        info = tarfile.TarInfo("SHA256SUMS")
        info.size, info.mode = len(sums), 0o600
        archive.addfile(info, io.BytesIO(sums))
    output.chmod(0o600)


PROBE_BODY = r'''set -eu
umask 077
test -s /run/techo5/slot
grep -Eiq '^Tater Linux checkers([[:space:]]|$)' /etc/tater-release
test "$(uname -r)" = 4.9.337-g8d928c5176cc
case "$(uname -v)" in '#4 '*) ;; *) exit 1;; esac
test -s /usr/local/bin/tater-hardware.json
grep -q ' /data ' /proc/mounts
test ! -L /data/tater-hardware-provision
test ! -L /data/tater-hardware-provision/private-runtime.tar
printf '%s%s\n' TATER_CHECKERS_ HARDWARE_READY'''
PROBE = "(\n" + PROBE_BODY + "\n)"


def target_probe_body(target: str) -> str:
    profile(target)
    if target == "checkers":
        return PROBE_BODY
    if target == "rook":
        return PROBE_BODY.replace("checkers", "rook").replace("4.9.337-g8d928c5176cc", "4.9.337-g4174e0b4d0e2").replace("'#4 '", "'#2 '").replace("TATER_CHECKERS_", "TATER_ROOK_").replace(
            "test -s /usr/local/bin/tater-hardware.json",
            "test -s /usr/local/bin/tater-hardware.json\n"
            "test \"$(cat /sys/class/graphics/fb0/virtual_size)\" = 480,1440\n"
            "test \"$(cat /sys/class/graphics/fb0/bits_per_pixel)\" = 32\n"
            "grep -Eq '(^|[[:space:]])lcm=1-hx8379c_wvga_dsi_vdo([[:space:]]|$)' /proc/cmdline")
    body = PROBE_BODY.replace("checkers", "cronos").replace("'#4 '", "'#2 '").replace("TATER_CHECKERS_", "TATER_CRONOS_")
    return body.replace("printf '%s%s\\n' TATER_CRONOS_ HARDWARE_READY", "grep -Eq '(^|[[:space:]])androidboot.product=cronos([[:space:]]|$)' /proc/cmdline\nprintf '%s%s\\n' TATER_CRONOS_ HARDWARE_READY")


def install_command(inventory: dict[str, str], *, target: str = "checkers") -> str:
    runtimes, _ = profile(target)
    # Only /data runtime directories may be added. Existing runtime libraries,
    # system slots, recovery, private pairing and shared helpers are untouched.
    body = target_probe_body(target)
    body = "\n".join(line for line in body.splitlines() if not line.startswith("printf "))
    lines = ["(", body,
             f"test ! -L {REMOTE_DIR}", f"stage=$(mktemp -d {REMOTE_DIR}/stage.XXXXXX)",
             'case "$stage" in /data/tater-hardware-provision/stage.??????) ;; *) exit 1;; esac',
             f'tar -xf {REMOTE_FILE} -C "$stage"', '(cd "$stage" && sha256sum -c SHA256SUMS >/dev/null)']
    # Validate *all* existing runtime destinations before adding any missing
    # one. Legacy helper/manifest pairs may remain: binaries select sidecars.
    for runtime in (*runtimes, "tater-venc"):
        destination = "/data/" + runtime
        lines.extend([f"test ! -L {destination}", f"if [ -e {destination} ]; then",
                      f"  test -d {destination}",
                      f'  test -z "$(find {destination} -type l -print)"'])
        for name, digest in sorted(inventory.items()):
            if name.startswith(runtime + "/") and not name.endswith("/runtime.json"):
                lines.append(f'  test "$(sha256sum /data/{name} | cut -d\' \' -f1)" = {digest}')
        if runtime in runtimes:
            lines.append(f'  test "$(find {destination}/lib -type f | wc -l | tr -d \' \')" = {runtimes[runtime][1] - 1}')
        lines.append("fi")
    for runtime in (*runtimes, "tater-venc"):
        lines.append(f'if [ ! -e /data/{runtime} ]; then')
        lines.append(f'  chmod 555 "$stage/{runtime}"')
        if runtime in runtimes:
            lines.append(f'  chmod 555 "$stage/{runtime}/lib"')
        lines.append(f'  mv -Tn "$stage/{runtime}" /data/{runtime}')
        lines.append('fi')
    lines.append("sync")
    # Delete only named files we uploaded; never recursively remove a runtime,
    # partition, or an unresolved directory. On error, retain staging for repair.
    for name in sorted(inventory):
        lines.append(f'rm -f "$stage/{name}"')
    directories = {str(parent) for name in inventory for parent in Path(name).parents if str(parent) != "."}
    for name in sorted(directories, key=lambda name: (-len(Path(name).parts), name)):
        lines.append(f'rmdir "$stage/{name}" 2>/dev/null || :')
    lines.extend(['rm -f "$stage/SHA256SUMS" "$stage/provision.sh"', 'rmdir "$stage"', f'rm -f {REMOTE_FILE}',
                  "printf '%s%s\\n' TATER_HARDWARE_ PROVISIONED", ")"])
    return "\n".join(lines)


def install(directory: Path, console_library, port: str, *, target: str = "checkers") -> None:
    inventory = verify(directory, target=target)
    body = target_probe_body(target)
    answer = usb.exchange(console_library, port, "(\n"+body+"\n)", 10)
    if "TATER_"+target.upper()+"_HARDWARE_READY" not in answer.splitlines():
        raise ValueError(f"refusing: this is not the qualified {target.title()} kernel/system USB console")
    with tempfile.TemporaryDirectory(prefix="tater-private-hardware-") as temporary:
        archive = Path(temporary) / "private-runtime.tar"
        archive_runtime(directory, inventory, archive, target=target)
        # Staging plus permanent files need up to two copies; the generic
        # uploader reserves its own transfer margin as well.
        available = usb.exchange(console_library, port, "df -Pk /data | awk 'END {print $4}'", 10)
        numbers = [int(line) for line in available.splitlines() if line.strip().isdigit()]
        if not numbers or numbers[-1] < (archive.stat().st_size * 2 + 1023) // 1024 + 8192:
            raise ValueError("insufficient userdata space for safe private runtime provisioning")
        usb.upload_rootfs(console_library, port, archive, remote_dir=REMOTE_DIR, remote_file=REMOTE_FILE)
        command = ("(\n" + body + "\n" +
                   f"test ! -L {REMOTE_DIR}/provision.sh\n"
                   f"tar -xOf {REMOTE_FILE} provision.sh > {REMOTE_DIR}/provision.sh\n"
                   f"chmod 700 {REMOTE_DIR}/provision.sh\n"
                   f"sh {REMOTE_DIR}/provision.sh\n"
                   f"rm -f {REMOTE_DIR}/provision.sh\n)")
        answer = usb.exchange(console_library, port, command, 180)
        if "TATER_HARDWARE_PROVISIONED" not in answer.splitlines():
            raise ValueError("private runtime install was refused; existing destinations were not overwritten")


def select_normal_port(library, requested: str | None) -> str:
    if requested:
        return requested
    ports = usb.available_ports(library)
    if not ports:
        raise ValueError("no normal Tater Linux USB console was found; boot normally, keep USB connected, and close serial terminals")
    if len(ports) != 1:
        raise ValueError("more than one USB console is connected; select the Checkers with --port")
    return ports[0]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime", type=Path, required=True)
    parser.add_argument("--install", action="store_true", help="explicitly install through the qualified Checkers USB console")
    parser.add_argument("--port")
    parser.add_argument("--target", choices=("checkers", "cronos", "rook"), default="checkers")
    arguments = parser.parse_args()
    try:
        inventory = verify(arguments.runtime, target=arguments.target)
        if arguments.install:
            library = usb.load_console_library()
            port = select_normal_port(library, arguments.port)
            install(arguments.runtime, library, port, target=arguments.target)
            print("Private runtime libraries provisioned. Reboot normally to enable them; no firmware was flashed.")
        else:
            print(f"Verified {arguments.target} private runtime: {len(inventory)} files. No device was accessed.")
    except (OSError, ValueError, usb.Fail) as error:
        parser.exit(1, f"refusing: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
