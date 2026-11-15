#!/usr/bin/env python3
"""Prepare public inputs and assemble device-local libraries over USB.

No proprietary library is downloaded from Tater or packaged in a release.
All vendor inputs are read from this unit's retained, hash-pinned /vendor/lib.
Preparation is host-only; install() is an explicit, guarded device operation.
"""
from __future__ import annotations

import base64
from concurrent.futures import ThreadPoolExecutor
import hashlib
import io
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import struct
import tarfile
import tempfile
from urllib import request
import zipfile

ROOT = Path(__file__).resolve().parent
RECIPE = ROOT / "hardware-runtime-sources.json"
REMOTE = "/data/tater-runtime-install"
SAFE = re.compile(r"[A-Za-z0-9_./+@-]+\Z")
SHA = re.compile(r"[0-9a-f]{64}\Z")
MAX_FILE = 32 * 1024 * 1024


def digest(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError("expected an ordinary runtime input")
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def safe_path(name):
    if not isinstance(name, str) or not SAFE.fullmatch(name) or name.startswith("/") or any(p in ("", ".", "..") for p in name.split("/")):
        raise ValueError("unsafe runtime path")
    return name


def encoded(value):
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()


def recipe(target):
    value = json.loads(RECIPE.read_text())
    if value.get("schema") != 1 or target not in value.get("targets", {}):
        raise ValueError("unsupported hardware runtime target")
    profile = value["targets"][target]
    for root in profile["roots"]:
        if "/" in safe_path(root):
            raise ValueError("runtime root must be one directory")
    for name, entry in profile["files"].items():
        if safe_path(name).split("/")[0] not in profile["roots"] or not SHA.fullmatch(entry["sha256"]):
            raise ValueError("invalid runtime file identity")
        if set(entry) not in ({"sha256", "public"}, {"sha256", "vendor"}):
            raise ValueError("runtime file needs exactly one source")
        if "vendor" in entry:
            safe_path(entry["vendor"])
        elif entry["public"] != entry["sha256"] or entry["public"] not in value["public_files"]:
            raise ValueError("unknown public runtime input")
    for name in profile["manifests"]:
        if safe_path(name).split("/")[0] not in profile["roots"] or name in profile["files"]:
            raise ValueError("invalid runtime manifest path")
    return value, profile


def fetch(url, destination, expected, limit, *, encoding=None, exact_size=None):
    if destination.is_file() and not destination.is_symlink() and digest(destination) == expected:
        return destination
    if destination.is_symlink() or (destination.exists() and not destination.is_file()):
        raise ValueError("unsafe download cache destination")
    if not url.startswith("https://"):
        raise ValueError("runtime inputs require HTTPS")
    destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temporary = None
    try:
        with request.urlopen(request.Request(url, headers={"User-Agent": "Tater-Echo-USB-Installer"}), timeout=60) as response:
            if not response.geturl().startswith("https://"):
                raise ValueError("runtime redirect left HTTPS")
            with tempfile.NamedTemporaryFile(dir=destination.parent, prefix="download-", suffix=".partial", delete=False) as stream:
                temporary = Path(stream.name)
                if encoding == "base64":
                    payload = response.read(limit * 2 + 1)
                    if len(payload) > limit * 2:
                        raise ValueError("encoded runtime input exceeds boundary")
                    payload = base64.b64decode(payload.strip(), validate=True)
                    if len(payload) > limit:
                        raise ValueError("runtime input exceeds boundary")
                    stream.write(payload)
                else:
                    size = 0
                    while chunk := response.read(1024 * 1024):
                        size += len(chunk)
                        if size > limit:
                            raise ValueError("runtime download exceeds boundary")
                        stream.write(chunk)
                stream.flush()
                os.fsync(stream.fileno())
        if digest(temporary) != expected or (exact_size is not None and temporary.stat().st_size != exact_size):
            raise ValueError("runtime download failed size/SHA-256 verification; no device changes made")
        temporary.replace(destination)
        temporary = None
        return destination
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def read_ext4_file(image, path):
    """Read a bounded ordinary file from the two pinned extent-based images.

    No mounts, writes, symlink traversal, external tools or Python dependencies.
    Not a general ext4 reader: other geometries fail closed. The enclosing
    archive and each resulting file are independently hash-verified.
    """
    if not path.startswith("/"):
        raise ValueError("runtime image path must be absolute")
    parts = safe_path(path[1:]).split("/")
    with image.open("rb") as source:
        image_size = image.stat().st_size
        def read(offset, count):
            if offset < 0 or count < 0 or count > MAX_FILE or offset + count > image_size:
                raise ValueError("runtime filesystem read exceeds image boundary")
            source.seek(offset)
            data = source.read(count)
            if len(data) != count:
                raise ValueError("truncated runtime filesystem")
            return data
        sb = read(1024, 1024)
        u32 = lambda data, at: struct.unpack_from("<I", data, at)[0]
        u16 = lambda data, at: struct.unpack_from("<H", data, at)[0]
        block_size = 1024 << u32(sb, 24) if u32(sb, 24) <= 2 else 0
        inode_count, blocks = u32(sb, 0), u32(sb, 4)
        per_group, inode_size = u32(sb, 40), u16(sb, 88)
        if (u16(sb, 56) != 0xef53 or block_size not in (1024, 2048, 4096)
                or u32(sb, 96) != 0x42 or not per_group or not inode_count
                or blocks * block_size > image_size or inode_size not in (128, 256)
                or u16(sb, 254) not in (0, 32)):
            raise ValueError("unsupported pinned runtime filesystem geometry")
        descriptors = (u32(sb, 20) + 1) * block_size
        def inode(number):
            if not 1 <= number <= inode_count:
                raise ValueError("invalid runtime filesystem inode")
            group, index = divmod(number - 1, per_group)
            descriptor = read(descriptors + group * 32, 32)
            return read(u32(descriptor, 8) * block_size + index * inode_size, inode_size)
        def contents(item):
            size = u32(item, 4) | u32(item, 108) << 32
            if not 0 < size <= MAX_FILE or not u32(item, 32) & 0x80000:
                raise ValueError("runtime file is not bounded extent-based data")
            output, seen, ranges = bytearray(size), set(), []
            def extents(node, expected_depth=None):
                magic, entries, maximum, depth = struct.unpack_from("<HHHH", node)
                if (magic != 0xf30a or entries > maximum or maximum > (len(node) - 12) // 12
                        or depth > 5 or (expected_depth is not None and depth != expected_depth)):
                    raise ValueError("invalid runtime extent tree")
                for index in range(entries):
                    at = 12 + index * 12
                    logical = u32(node, at)
                    if depth:
                        block = u32(node, at + 4) | u16(node, at + 8) << 32
                        if block in seen or len(seen) >= 4096:
                            raise ValueError("cyclic or oversized runtime extent tree")
                        seen.add(block)
                        extents(read(block * block_size, block_size), depth - 1)
                    else:
                        length = u16(node, at + 4)
                        physical = u32(node, at + 8) | u16(node, at + 6) << 32
                        start, end = logical * block_size, (logical + length) * block_size
                        if not 0 < length <= 32768 or not physical or start >= size or any(start < b and a < end for a, b in ranges):
                            raise ValueError("invalid, unwritten or overlapping runtime extent")
                        ranges.append((start, end))
                        output[start:min(end, size)] = read(physical * block_size, min(end, size) - start)
            extents(item[40:100])
            return bytes(output)
        current = inode(2)
        for name in parts:
            if u16(current, 0) & 0xf000 != 0x4000:
                raise ValueError("runtime path crosses a non-directory or symlink")
            directory, offset, matches = contents(current), 0, []
            while offset < len(directory):
                if offset + 8 > len(directory):
                    raise ValueError("truncated runtime directory")
                number, record, length = u32(directory, offset), u16(directory, offset + 4), directory[offset + 6]
                if record < 8 or record % 4 or offset + record > len(directory) or length > record - 8:
                    raise ValueError("invalid runtime directory entry")
                if number and directory[offset + 8:offset + 8 + length] == name.encode("ascii"):
                    matches.append(number)
                offset += record
            if len(matches) != 1:
                raise ValueError("runtime file is missing or ambiguous in public image")
            current = inode(matches[0])
        if u16(current, 0) & 0xf000 != 0x8000:
            raise ValueError("runtime image member is not an ordinary file")
        return contents(current)


def neutralize(data):
    """Original MIT adapter: relax only four reviewed libc-O import versions."""
    data = bytearray(data)
    if data[:6] != b"\x7fELF\x01\x01":
        raise ValueError("expected pinned ELF32 VNDK input")
    offset = struct.unpack_from("<I", data, 0x20)[0]
    width, count, names_index = struct.unpack_from("<HHH", data, 0x2e)
    sections = [struct.unpack_from("<10I", data, offset + i * width) for i in range(count)]
    def string(start):
        return bytes(data[start:data.index(0, start)]).decode("ascii")
    names = sections[names_index][4]
    sections_by_name = {string(names + s[0]): s for s in sections}
    symbols = sections_by_name[".dynsym"]
    strings = sections[symbols[6]][4]
    versions = sections_by_name[".gnu.version"][4]
    wanted = {"__system_property_read_callback", "__system_property_wait", "strtod_l", "strtof_l"}
    for index in range(symbols[5] // symbols[9]):
        name = string(strings + struct.unpack_from("<I", data, symbols[4] + index * symbols[9])[0])
        if name in wanted:
            at = versions + index * 2
            if struct.unpack_from("<H", data, at)[0] != 4:
                raise ValueError("unexpected libc-O import version")
            struct.pack_into("<H", data, at, 1)
    return bytes(data)


def public_inputs(value, profile):
    return {entry["public"] for entry in profile["files"].values() if "public" in entry}


def prepare(target, work, compat):
    """Verify every public byte before the factory installer can write a device."""
    value, profile = recipe(target)
    work = work.absolute()
    if work.is_symlink() or any(p.is_symlink() for p in work.parents):
        raise ValueError("runtime work directory must not traverse symlinks")
    work.mkdir(parents=True, exist_ok=True, mode=0o700)
    objects = work / "objects"
    if objects.is_symlink():
        raise ValueError("unsafe runtime object-cache directory")
    objects.mkdir(exist_ok=True, mode=0o700)
    needed = public_inputs(value, profile)
    if any((objects / sha).is_symlink() for sha in needed):
        raise ValueError("runtime object cache contains a symlink")
    pending = {sha: value["public_files"][sha] for sha in needed
               if not (objects / sha).is_file() or (objects / sha).is_symlink() or digest(objects / sha) != sha}
    print("Preparing verified hardware libraries for %s (device-local vendor files are not distributed)." % target, flush=True)
    # Rootless image reads only; there are no loop mounts, sudo or image writes.
    image_kinds = {e["kind"] for e in pending.values()} & {"sdk24", "apex"}
    for kind in sorted(image_kinds):
        item = value["archives"][kind]
        archive_path = fetch(item["url"], work / (kind + ".zip"), item["sha256"], item["size"],
                             encoding=item.get("encoding"), exact_size=item["size"])
        with tempfile.TemporaryDirectory(prefix="image-", dir=work) as temporary:
            image = Path(temporary) / "input.img"
            with zipfile.ZipFile(archive_path) as archive:
                member = archive.getinfo(item["member"])
                if not 0 < member.file_size <= item["max_image_bytes"]:
                    raise ValueError("runtime image exceeds boundary")
                with archive.open(member) as source, image.open("xb") as output:
                    shutil.copyfileobj(source, output, 1024 * 1024)
            for sha, entry in pending.items():
                if entry["kind"] != kind:
                    continue
                destination = Path(temporary) / sha
                destination.write_bytes(read_ext4_file(image, entry["path"]))
                if digest(destination) != sha:
                    raise ValueError("public Android image library failed SHA-256 verification")
                destination.replace(objects / sha)
    def prepare_one(pair):
        sha, entry = pair
        if entry["kind"] in image_kinds:
            return
        destination = objects / sha
        if entry["kind"] == "compat":
            source = compat / safe_path(entry["path"])
            if digest(source) != sha:
                raise ValueError("bundled public compatibility library differs from lock")
            shutil.copyfile(source, destination)
        elif entry["kind"] == "gitiles":
            source = fetch(entry["url"], work / (entry.get("input_sha256", sha) + ".source"),
                           entry.get("input_sha256", sha), MAX_FILE, encoding="base64")
            data = source.read_bytes()
            if entry.get("transform"):
                if entry["transform"] != "neutralize-libc-o":
                    raise ValueError("unknown runtime transformation")
                data = neutralize(data)
            if hashlib.sha256(data).hexdigest() != sha:
                raise ValueError("transformed public runtime library failed verification")
            destination.write_bytes(data)
        else:
            raise ValueError("unknown public runtime input kind")
    with ThreadPoolExecutor(max_workers=6) as pool:
        list(pool.map(prepare_one, pending.items()))
    for sha in needed:
        if digest(objects / sha) != sha:
            raise ValueError("prepared public runtime input changed")
    return {"target": target, "profile": profile, "objects": objects}


def probe(target, profile):
    lines = ["set -eu", "test \"$(id -u)\" = 0", "test -s /run/techo5/slot",
             "grep -q ' /data ' /proc/mounts",
             "test \"$(cat /etc/tater-ota-platform)\" = echo-hardware-v1/" + target,
             "test \"$(uname -r)\" = " + profile["kernel"]]
    if target == "athena":
        lines.append("tr '\\000' '\\n' < /proc/device-tree/compatible | grep -qx amzn,athena")
    elif target == "rook":
        # Rook's qualified bootloader does not supply androidboot.product.
        # Match the hardware helpers' MT8163 + exact Rook LCD + Linux-boot
        # contract instead; contradictory/duplicate identity tokens fail closed.
        lines.extend(["rook_args=$(tr ' ' '\\n' < /proc/cmdline)",
                      "test \"$(printf '%s\\n' \"$rook_args\" | grep -c '^androidboot.hardware=')\" = 1",
                      "printf '%s\\n' \"$rook_args\" | grep -qx androidboot.hardware=mt8163",
                      "test \"$(printf '%s\\n' \"$rook_args\" | grep -c '^lcm=')\" = 1",
                      "printf '%s\\n' \"$rook_args\" | grep -qx lcm=1-hx8379c_wvga_dsi_vdo",
                      "test \"$(printf '%s\\n' \"$rook_args\" | grep -c '^techo5=')\" = 1",
                      "printf '%s\\n' \"$rook_args\" | grep -qx techo5=linux",
                      "rook_product=$(printf '%s\\n' \"$rook_args\" | sed -n '/^androidboot.product=/p')",
                      "test -z \"$rook_product\" || test \"$rook_product\" = androidboot.product=rook"])
    else:
        lines.extend(["test \"$(tr ' ' '\\n' < /proc/cmdline | grep -c '^androidboot.product=')\" = 1",
                      "tr ' ' '\\n' < /proc/cmdline | grep -qx androidboot.product=" + target])
    if target != "athena":
        lines.extend(["test \"$(cat /sys/class/graphics/fb0/virtual_size)\" = " + profile["panel"],
                      "test \"$(cat /sys/class/graphics/fb0/bits_per_pixel)\" = 32"])
    return "\n".join(lines)


def installation_script(target, profile):
    """Validate all staged/existing destinations before any no-clobber commit."""
    files = dict(profile["files"])
    for name, manifest in profile["manifests"].items():
        files[name] = {"sha256": hashlib.sha256(encoded(manifest)).hexdigest(), "manifest": name}
    lines = ["#!/bin/sh", probe(target, profile), "umask 077", "test ! -L " + REMOTE,
             'stage=$(mktemp -d ' + REMOTE + '/stage.XXXXXX)',
             'case "$stage" in ' + REMOTE + '/stage.??????) ;; *) exit 1;; esac',
             'tar -xf ' + REMOTE + '/payload.tar -C "$stage"',
             '(cd "$stage" && sha256sum -c SHA256SUMS >/dev/null)']
    for name, entry in sorted(files.items()):
        parent = str(PurePosixPath(name).parent)
        lines.append('mkdir -p "$stage/runtime/' + parent + '"')
        if "vendor" in entry:
            source = "/vendor/lib/" + entry["vendor"]
            lines.extend(['test -f ' + source, 'test ! -L ' + source,
                          'test "$(sha256sum ' + source + " | cut -d' ' -f1)\" = " + entry["sha256"],
                          'cp ' + source + ' "$stage/runtime/' + name + '"'])
        else:
            source = "manifests/" + name if "manifest" in entry else "inputs/" + entry["public"]
            lines.append('cp "$stage/' + source + '" "$stage/runtime/' + name + '"')
        lines.append('test "$(sha256sum "$stage/runtime/' + name + '" | cut -d\' \' -f1)" = ' + entry["sha256"])
    for root in profile["roots"]:
        destination = "/data/" + root
        members = {n: e for n, e in files.items() if n.startswith(root + "/")}
        lines.extend(["test ! -L " + destination, "if [ -e " + destination + " ]; then",
                      "  test -d " + destination,
                      '  test -z "$(find ' + destination + ' -type l -print)"',
                      '  test "$(find ' + destination + ' -type f | wc -l | tr -d \' \')" = ' + str(len(members))])
        for name, entry in sorted(members.items()):
            lines.append('  test "$(sha256sum /data/' + name + ' | cut -d\' \' -f1)" = ' + entry["sha256"])
        lines.append("fi")
    for root in profile["roots"]:
        lines.extend(['if [ ! -e /data/' + root + ' ]; then',
                      '  mv -Tn "$stage/runtime/' + root + '" /data/' + root,
                      '  test ! -e "$stage/runtime/' + root + '"',
                      '  find /data/' + root + ' -type f -exec chmod 444 {} +',
                      '  find /data/' + root + ' -type d -exec chmod 555 {} +'])
        if root + "/linker" in files:
            lines.append("  chmod 555 /data/" + root + "/linker")
        lines.append("fi")
    # On errors retain staging for diagnosis/retry; never erase old runtimes.
    # Success removes only explicit files belonging to this newly created stage.
    cleanup = {*["runtime/" + n for n in files], *["inputs/" + e["public"] for e in files.values() if "public" in e],
               *["manifests/" + n for n in profile["manifests"]], "SHA256SUMS", "install.sh"}
    for name in sorted(cleanup):
        lines.append('rm -f "$stage/' + name + '"')
    directories = {str(p) for n in cleanup for p in PurePosixPath(n).parents if str(p) != "."}
    for name in sorted(directories, key=lambda n: (-len(PurePosixPath(n).parts), n)):
        lines.append('rmdir "$stage/' + name + '" 2>/dev/null || :')
    lines.extend(['rmdir "$stage"', 'rm -f ' + REMOTE + '/payload.tar', 'sync', 'echo TATER_RUNTIME_INSTALLED'])
    return "\n".join(lines) + "\n"


def archive(plan, output):
    target, profile, objects = plan["target"], plan["profile"], plan["objects"]
    contents = {"manifests/" + name: encoded(value) for name, value in profile["manifests"].items()}
    for sha in {e["public"] for e in profile["files"].values() if "public" in e}:
        if digest(objects / sha) != sha:
            raise ValueError("public runtime input changed before transfer")
        contents["inputs/" + sha] = (objects / sha).read_bytes()
    contents["install.sh"] = installation_script(target, profile).encode()
    contents["SHA256SUMS"] = "".join(hashlib.sha256(data).hexdigest() + "  " + name + "\n" for name, data in sorted(contents.items())).encode()
    with tarfile.open(output, "x", format=tarfile.USTAR_FORMAT) as tar:
        for name, data in sorted(contents.items()):
            entry = tarfile.TarInfo(name)
            entry.size, entry.mode = len(data), 0o600
            tar.addfile(entry, io.BytesIO(data))
    output.chmod(0o600)


def install(plan, library, console):
    """Called only after the target's verified factory boot. No boot writes."""
    try:
        from . import recovery_install as usb
    except ImportError:
        import recovery_install as usb
    target, profile = plan["target"], plan["profile"]
    answer = console.run("(\n" + probe(target, profile) + "\necho TATER_RUNTIME_READY\n)", 15) or ""
    if "TATER_RUNTIME_READY" not in answer.splitlines():
        raise ValueError("refusing hardware runtime install: target/kernel/system USB preflight failed")
    port = console.port or console.find()
    if not port:
        raise ValueError("installed satellite USB console disappeared")
    with tempfile.TemporaryDirectory(prefix="runtime-transfer-", dir=plan["objects"].parent) as temporary:
        payload = Path(temporary) / "payload.tar"
        archive(plan, payload)
        need = (payload.stat().st_size * 2 + sum(e.get("runtime_bytes", 0) for e in profile["manifests"].values()) + 1023) // 1024 + 8192
        answer = console.run("df -Pk /data | awk 'END {print $4}'", 10) or ""
        available = [int(line) for line in answer.splitlines() if line.strip().isdigit()]
        if not available or available[-1] < need:
            raise ValueError("insufficient userdata space for safe runtime staging; installed libraries left unchanged")
        usb.upload_rootfs(library, port, payload, remote_dir=REMOTE, remote_file=REMOTE + "/payload.tar")
        command = "(\n" + probe(target, profile) + "\ntest ! -L " + REMOTE + "/install.sh\ntar -xOf " + REMOTE + "/payload.tar install.sh > " + REMOTE + "/install.sh\nsh " + REMOTE + "/install.sh\n)"
        answer = usb.exchange(library, port, command, 300)
        if "TATER_RUNTIME_INSTALLED" not in answer.splitlines():
            raise ValueError("runtime installation refused; existing libraries were not overwritten. Keep the USB connection and staging for repair/retry")
    print("Device-local hardware libraries installed and verified; restarting the satellite to load them.", flush=True)
    # The assembly script already synced its verified files. Use the reboot
    # helper shipped in every screen rootfs, not rescue-only busybox.static.
    # Invoke directly: an interactive USB shell can terminate background jobs
    # when the host closes the port. A lost reply here is expected; completion
    # still requires a different, target-verified boot identity below.
    console.run("/usr/local/bin/tater-reboot-now", 3)


def finish_factory(plan, tools, serial):
    """Wait for the selected unit, install, then verify its post-install boot."""
    path = tools / "techo5lib.py"
    spec = importlib.util.spec_from_file_location("tater_runtime_console", path)
    if spec is None or spec.loader is None:
        raise ValueError("factory bundle is missing its USB console library")
    library = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(library)
    console = library.Console(serial, library.CONSOLE_TECHO5)
    command = "(\n" + probe(plan["target"], plan["profile"]) + "\necho TATER_RUNTIME_READY\n)"
    try:
        library.wait_for("the installed satellite USB console", 300,
                         lambda: "TATER_RUNTIME_READY" in (console.run(command, 10) or "").splitlines(),
                         hint=console.waiting_hint)
        previous_boot = console_boot_id(console)
        if previous_boot is None:
            raise ValueError("cannot verify the installed satellite's boot identity before library setup")
        install(plan, library, console)
        # A responding pre-reboot shell must not count as a successful reboot.
        library.wait_for("the satellite after hardware-library setup", 300,
                         lambda: runtime_reboot_ready(console, previous_boot, command),
                         hint=console.waiting_hint)
    except library.Fail as error:
        raise ValueError("hardware-library setup did not finish: " + str(error)) from error


def console_boot_id(console):
    answer = console.run("cat /proc/sys/kernel/random/boot_id", 10) or ""
    values = [line.strip() for line in answer.splitlines()
              if re.fullmatch(r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}", line.strip())]
    return values[0] if len(values) == 1 else None


def runtime_reboot_ready(console, previous_boot, command):
    current_boot = console_boot_id(console)
    return (current_boot is not None and current_boot != previous_boot
            and "TATER_RUNTIME_READY" in (console.run(command, 10) or "").splitlines())
