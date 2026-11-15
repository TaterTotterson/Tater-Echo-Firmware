#!/usr/bin/env python3
"""Verify or install the bundled Tater Linux image for Echo Show 8 Gen 2."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent


class Fail(Exception):
    pass


def sha256(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def verify_bundle() -> dict:
    try:
        manifest = json.loads((ROOT / "bundle-manifest.json").read_text())
    except (OSError, ValueError) as error:
        raise Fail(f"cannot read bundle manifest: {error}") from error
    if (manifest.get("target") != "athena" or manifest.get("base_os") != "tater-linux" or
            not isinstance(manifest.get("files"), dict)):
        raise Fail("this is not an Athena Tater Linux factory bundle")
    files = manifest["files"]
    required = {
        "install.sh", "install.py", "recovery_install.py",
        "payload/rootfs.tar.gz", "payload/system.img.gz", "payload/boot.img",
        "tools/install-athena.py", "tools/techo5lib.py",
    }
    missing = sorted(required - files.keys())
    if missing:
        raise Fail(f"bundle manifest is missing required files: {', '.join(missing)}")
    for relative, metadata in sorted(files.items()):
        if not isinstance(relative, str) or not isinstance(metadata, dict):
            raise Fail("bundle manifest has an invalid file entry")
        path = (ROOT / relative).resolve()
        if ROOT.resolve() not in path.parents or not path.is_file():
            raise Fail(f"bundle file is missing or unsafe: {relative}")
        if path.stat().st_size != int(metadata.get("size", -1)):
            raise Fail(f"bundle file has the wrong size: {relative}")
        if sha256(path) != metadata.get("sha256"):
            raise Fail(f"bundle file failed SHA-256 verification: {relative}")
    return manifest


def main(argv: list[str]) -> int:
    try:
        manifest = verify_bundle()
        if argv == ["--verify-bundle"]:
            print(f"Bundle verified: Athena Tater Linux {manifest['version']}")
            return 0
        if "--verify-bundle" in argv:
            raise Fail("--verify-bundle cannot be combined with install options")
        if "--repair" in argv:
            command = [sys.executable, str(ROOT / "recovery_install.py"), *argv]
        else:
            forbidden = {"--image", "--boot", "--release", "--kernel"}
            if any(argument == option or argument.startswith(option + "=")
                   for argument in argv for option in forbidden):
                raise Fail("the Tater bundle pins its image, boot kernel, and mainline install mode")
            if not any(argument == "--name" or argument.startswith("--name=") for argument in argv):
                argv = ["--name", "Tater Athena", *argv]
            command = [
                sys.executable,
                str(ROOT / "tools/install-athena.py"),
                "--image", str(ROOT / "payload/system.img.gz"),
                "--boot", str(ROOT / "payload/boot.img"),
                "--no-watch",
                *argv,
            ]
        if "--repair" in argv:
            return subprocess.call(command, cwd=ROOT)
        def option(name, default=None):
            for index, argument in enumerate(argv):
                if argument.startswith(name + "="):
                    return argument.split("=", 1)[1]
                if argument == name and index + 1 < len(argv):
                    return argv[index + 1]
            return default
        path = ROOT / "hardware_runtime.py"
        if not path.is_file():
            path = ROOT.parent / "hardware_runtime.py"
        spec = importlib.util.spec_from_file_location("tater_athena_runtime", path)
        if spec is None or spec.loader is None:
            raise Fail("factory bundle is missing hardware_runtime.py")
        hardware = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(hardware)
        plan = hardware.prepare("athena", Path(option("--work", str(ROOT / "build"))) / "hardware-inputs", ROOT / "runtime-compat")
        serial = option("--serial")
        if not serial:
            listing = subprocess.check_output([option("--adb", "adb"), "devices"], text=True)
            candidates = [line.split()[0] for line in listing.splitlines()[1:]
                          if len(line.split()) >= 2 and line.split()[1] == "recovery"]
            if len(candidates) != 1:
                raise Fail("expected exactly one Athena in TWRP; use --serial")
            serial = candidates[0]
        status = subprocess.call(command, cwd=ROOT)
        if status == 0 and "--dry-run" not in argv:
            hardware.finish_factory(plan, ROOT / "tools", serial)
        return status
    except (Fail, OSError, KeyError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
