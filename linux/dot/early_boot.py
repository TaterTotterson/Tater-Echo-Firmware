"""Add Tater's early held-key hook to a freshly built, unit-specific Dot image.

No device access. Keep the pinned platform's kernel/header and other ramdisk
entries intact. The factory installer is the only delivery path for this hook.
"""
from __future__ import annotations

import gzip
import hashlib
import lzma
from pathlib import Path
import struct


ANCHOR = b'\n[ -n "$UNHEALTHY" ] || boot_slot\n'
HOOK = b'''
# Tater checks the held key before either main root-system slot is loaded.
/usr/local/sbin/tater-early-recovery
early_recovery_result=$?
if [ "$early_recovery_result" -eq 20 ]; then
    UNHEALTHY=1
    crumb "TWRP handoff requires attention: remaining in rescue"
fi
'''


def require_static_arm(path: Path) -> None:
    data = path.read_bytes()
    if (len(data) < 52 or data[:7] != b'\x7fELF\x01\x01\x01'
            or struct.unpack_from('<H', data, 18)[0] != 40):
        raise ValueError('early key helper must be a static 32-bit ARM ELF')
    offset = struct.unpack_from('<I', data, 28)[0]
    entry_size, count = struct.unpack_from('<HH', data, 42)
    if count and (entry_size < 32 or offset + entry_size * count > len(data)):
        raise ValueError('early key helper has invalid ELF program headers')
    for index in range(count):
        if struct.unpack_from('<I', data, offset + index * entry_size)[0] in (2, 3):
            raise ValueError('early key helper must not depend on a dynamic loader')


def patch_init(contents: bytes) -> bytes:
    if contents.count(ANCHOR) != 1 or b'tater-early-recovery' in contents:
        raise ValueError('Dot init no longer has its unique pre-slot recovery hook')
    return contents.replace(ANCHOR, b'\n' + HOOK + ANCHOR, 1)


def patch_boot_image(dotimage, image: Path, helper: Path, script: Path, target: str) -> None:
    if target not in ('biscuit', 'radar'):
        raise ValueError('early recovery is only for Biscuit and Radar')
    require_static_arm(helper)
    header, kernel, compressed, page = dotimage._read_boot_image(str(image))
    if compressed.startswith(b'\x1f\x8b'):
        raw = gzip.decompress(compressed)
    else:
        raw = lzma.decompress(compressed, format=lzma.FORMAT_AUTO)

    patched = dotimage._patch_newc(raw, {'init': patch_init}, {
        'usr/local/bin/tater-volume-up': (0o755, helper.read_bytes()),
        'usr/local/sbin/tater-early-recovery': (0o755, script.read_bytes()),
        'etc/tater-early-target': (0o644, (target + '\n').encode()),
    })
    packed = gzip.compress(patched, mtime=0)
    # Check capacity before replacing even a local candidate.
    aligned = lambda size: (size + page - 1) // page * page
    if page + aligned(len(kernel)) + aligned(len(packed)) > 16 * 1024 * 1024:
        raise ValueError('early-recovery image exceeds the 16 MiB recovery partition')
    dotimage._write_boot_image(header, kernel, packed, page, str(image))


def write_checksums(destination: Path, linux: Path, twrp: Path, handoff: Path) -> None:
    entries = ((linux, 'linux.img'), (twrp, 'twrp.img'), (handoff, 'twrp-handoff.img'))
    destination.write_text(''.join(
        f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {name}\n' for path, name in entries
    ))
    destination.chmod(0o600)
