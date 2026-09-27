#!/usr/bin/env python3
"""Build the deterministic, versioned Checkers Magisk support module."""

from __future__ import annotations

import argparse
import io
from pathlib import Path
import re
import zipfile


REPO = Path(__file__).resolve().parents[1]
SOURCE = REPO / "factory" / "checkers" / "magisk"

MODULE_FILES = (
    ("module.prop", "module.prop", 0o644),
    ("sepolicy.rule", "sepolicy.rule", 0o644),
    ("post-fs-data.sh", "post-fs-data.sh", 0o755),
    ("service.sh", "service.sh", 0o755),
    ("privacy.sh", "privacy.sh", 0o755),
    ("privacy-packages.txt", "privacy-packages.txt", 0o644),
    ("privacy-components.txt", "privacy-components.txt", 0o644),
    (
        "speech-interaction-manager.replace",
        "system/priv-app/SpeechInteractionManager/.replace",
        0o644,
    ),
    (
        "bishop.replace",
        "system/priv-app/com.amazon.bishop/.replace",
        0o644,
    ),
    ("uninstall.sh", "uninstall.sh", 0o755),
)


def validate_version(version: str) -> re.Match[str]:
    match = re.fullmatch(
        r"v([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+]([A-Za-z0-9.-]+))?",
        version,
    )
    if not match:
        raise SystemExit("version must look like v0.1.0")
    return match


def render_module_prop(version: str) -> bytes:
    match = validate_version(version)
    major, minor, patch = (int(match.group(index)) for index in range(1, 4))
    # Keep the historical release encoding used by v0.2.0-v0.2.2. The patch
    # occupies the hundreds so prerelease/build variants can remain monotonic
    # without ever sorting above the next patch release.
    version_code = major * 100_000_000 + minor * 10_000 + patch * 100
    rendered = (SOURCE / "module.prop").read_text()
    rendered = re.sub(r"(?m)^version=.*$", f"version={version.removeprefix('v')}", rendered)
    rendered = re.sub(r"(?m)^versionCode=.*$", f"versionCode={version_code}", rendered)
    return rendered.encode()


def module_archive_bytes(version: str) -> bytes:
    validate_version(version)
    output = io.BytesIO()
    with zipfile.ZipFile(
        output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9
    ) as archive:
        for source_name, archive_name, mode in sorted(MODULE_FILES, key=lambda item: item[1]):
            source = SOURCE / source_name
            if not source.is_file():
                raise SystemExit(f"required Checkers module input is missing: {source}")
            data = render_module_prop(version) if source_name == "module.prop" else source.read_bytes()
            info = zipfile.ZipInfo(archive_name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = mode << 16
            archive.writestr(info, data)
    return output.getvalue()


def build_module_archive(version: str, destination: Path) -> Path:
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(module_archive_bytes(version))
    destination.chmod(0o644)
    return destination


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    print(build_module_archive(args.version, args.output.resolve()))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
