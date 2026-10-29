#!/usr/bin/env python3
"""Preserve only Tater-owned setup state across destructive factory installs.

The root-system and application slots deliberately are not included.  A
factory bundle must boot the versions it carries, while the satellite keeps
its Tater identity, Wi-Fi, local controls, SSH credentials, and platform key.
"""

from __future__ import annotations

import hashlib
from pathlib import Path
import shlex
import tarfile


MAX_ARCHIVE_BYTES = 16 * 1024 * 1024
REMOTE_ARCHIVE = "/tmp/tater-settings-preserve.tar"

# Relative to /. Directories are kept deliberately narrow: update slots,
# binaries, logs, caches, and captured media must never survive a factory
# reinstall merely because the user chose to keep setup.
PRESERVED_PATHS = (
    "data/local/etc/tater",
    "data/tater-linux/wpa_supplicant.conf",
    "data/tater-linux/ssh",
    "data/tater-linux/ble_bonds.json",
    # Earlier Tater/TECHO5 platform locations are retained for migration and
    # for the low-level Home Assistant/SSH bootstrap used by the Shows.
    "data/misc/techo5",
    "data/techo5-linux/wpa_supplicant.conf",
    "data/techo5-linux/ssh",
    "data/techo5-linux/ble_bonds.json",
    "data/techo5/misc/techo5",
    "data/techo5/techo5-linux/wpa_supplicant.conf",
    "data/techo5/techo5-linux/ssh",
    "data/techo5/techo5-linux/ble_bonds.json",
)

CLEAR_PATHS = (
    "/data/local/etc/tater",
    "/data/local/share/tater",
    "/data/tater-linux",
    "/data/misc/techo5",
    "/data/techo5-linux",
    "/data/techo5/misc/techo5",
    "/data/techo5/techo5-linux",
)

class SettingsError(RuntimeError):
    pass


class SettingsBackup:
    def __init__(self, archive: Path, paired: bool) -> None:
        self.archive = archive
        self.paired = paired


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _allowed(name: str) -> bool:
    return any(name == root or name.startswith(root + "/") for root in PRESERVED_PATHS)


def inspect_archive(path: Path) -> bool:
    """Validate a settings archive and return whether it has complete pairing state."""
    if not path.is_file() or not 0 < path.stat().st_size <= MAX_ARCHIVE_BYTES:
        raise SettingsError("the saved Tater settings archive is empty or unexpectedly large")
    seen: set[str] = set()
    paired_files: set[str] = set()
    total = 0
    try:
        with tarfile.open(path, "r:") as archive:
            members = archive.getmembers()
            if not members:
                raise SettingsError("the saved Tater settings archive is empty")
            for member in members:
                name = member.name.removeprefix("./").rstrip("/")
                parts = Path(name).parts
                if (not name or member.name.startswith("/") or ".." in parts or
                        not _allowed(name) or name in seen):
                    raise SettingsError(f"unsafe or unexpected Tater settings path: {member.name}")
                if not (member.isdir() or member.isfile()):
                    raise SettingsError(f"unsupported Tater settings entry: {member.name}")
                seen.add(name)
                if member.isfile():
                    total += member.size
                    if total > MAX_ARCHIVE_BYTES:
                        raise SettingsError("the saved Tater settings are unexpectedly large")
                    if member.size > 0 and name in {
                            "data/local/etc/tater/native.json",
                            "data/local/etc/tater/device_token"}:
                        paired_files.add(name)
    except (OSError, tarfile.TarError) as error:
        raise SettingsError(f"cannot verify the saved Tater settings: {error}") from error
    return len(paired_files) == 2


def capture(adb, destination: Path) -> SettingsBackup | None:
    """Pull a checked settings archive from mounted userdata, or None when none exists."""
    destination = Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    paths = " ".join(PRESERVED_PATHS)
    command = (
        f"rm -f {REMOTE_ARCHIVE}; "
        "grep -q ' /data ' /proc/mounts || twrp mount data >/dev/null 2>&1 || true; "
        "saved=''; "
        f"for path in {paths}; do [ ! -e /$path ] || saved=\"$saved $path\"; done; "
        "if [ -z \"$saved\" ]; then echo TATER_SETTINGS_NONE; "
        f"elif (cd / && tar -cf {REMOTE_ARCHIVE} $saved) && "
        f"chmod 600 {REMOTE_ARCHIVE}; then echo TATER_SETTINGS_ARCHIVED; "
        "else echo TATER_SETTINGS_FAILED; fi"
    )
    answer = adb.sh(command)
    if "TATER_SETTINGS_NONE" in answer:
        return None
    if "TATER_SETTINGS_ARCHIVED" not in answer:
        raise SettingsError(f"could not archive the existing Tater settings: {answer}")
    fields = adb.sh(f"sha256sum {REMOTE_ARCHIVE}").split()
    if not fields or len(fields[0]) != 64:
        raise SettingsError("could not checksum the existing Tater settings")
    partial = destination.with_suffix(destination.suffix + ".partial")
    partial.unlink(missing_ok=True)
    try:
        if not adb.pull(REMOTE_ARCHIVE, str(partial)):
            raise SettingsError("could not copy the existing Tater settings to the computer")
        paired = inspect_archive(partial)
        if _sha256(partial) != fields[0]:
            raise SettingsError("the existing Tater settings changed during backup")
        partial.chmod(0o600)
        partial.replace(destination)
    finally:
        partial.unlink(missing_ok=True)
        adb.sh(f"rm -f {REMOTE_ARCHIVE}")
    return SettingsBackup(destination, paired)


def clear(adb) -> None:
    """Remove Tater-owned state and old app overrides, leaving unrelated userdata alone."""
    paths = " ".join(shlex.quote(path) for path in CLEAR_PATHS)
    answer = adb.sh(f"rm -rf {paths}; sync; echo TATER_SETTINGS_CLEARED")
    if "TATER_SETTINGS_CLEARED" not in answer:
        raise SettingsError(f"could not clear prior Tater settings: {answer}")


def restore(adb, saved: SettingsBackup) -> None:
    """Clear old Tater state and restore the checked, settings-only archive."""
    paired = inspect_archive(saved.archive)
    if paired != saved.paired:
        raise SettingsError("the saved Tater settings archive changed after it was captured")
    adb.push(str(saved.archive), REMOTE_ARCHIVE)
    fields = adb.sh(f"sha256sum {REMOTE_ARCHIVE}").split()
    if not fields or fields[0] != _sha256(saved.archive):
        raise SettingsError("the saved Tater settings changed while returning to the satellite")
    clear(adb)
    answer = adb.sh(
        f"cd / && tar -xf {REMOTE_ARCHIVE} && rm -f {REMOTE_ARCHIVE} && sync && "
        "echo TATER_SETTINGS_RESTORED"
    )
    if "TATER_SETTINGS_RESTORED" not in answer:
        raise SettingsError(f"could not restore the saved Tater settings: {answer}")
