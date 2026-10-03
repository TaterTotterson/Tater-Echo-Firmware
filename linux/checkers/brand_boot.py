#!/usr/bin/env python3
"""Brand the pinned Checkers rescue screen and start USB before painting it.

The first factory boot runs from TECHO5's initramfs while the installer creates
the slot store. Keep the kernel, boot parameters, partition handling, and slot
selection unchanged. Bring up the rescue console before the framebuffer
program so a slow first paint cannot delay the installer's only control path.
"""

from __future__ import annotations

import argparse
import hashlib
import lzma
from pathlib import Path
import struct


BASE_SHA256 = "46c19fd23c210714e29eb2bf88c77b540f3290c0cf50b680b22032e7a4771da7"
MAGIC = b"070701"
OLD_SCREEN = b'''# Panel: the test screen with the clock ticking, as proof of life.
if [ -x /usr/local/bin/fbprobe ]; then
	/usr/local/bin/fbprobe -hold 1000h > /tmp/fbprobe.log 2>&1 &
fi
'''
OLD_CONSOLE = b'''# Root shell on USB serial.
t5_usb_acm
(
	while true; do
		[ -e /dev/ttyGS0 ] || mdev -s
		if [ -e /dev/ttyGS0 ]; then
			setsid sh -c 'exec sh -l < /dev/ttyGS0 > /dev/ttyGS0 2>&1'
		fi
		sleep 2
	done
) &
'''
NEW_SCREEN = b'''# Bring up the factory control path before starting framebuffer work.
t5_usb_acm
(
	echo "rescue USB worker started at uptime $(cut -d' ' -f1 /proc/uptime)" >> "$LOGDIR/console-start.log"
	while true; do
		[ -e /dev/ttyGS0 ] || mdev -s
		if [ -e /dev/ttyGS0 ]; then
			echo "ttyGS0 ready at uptime $(cut -d' ' -f1 /proc/uptime)" >> "$LOGDIR/console-start.log"
			setsid sh -c 'exec sh -l < /dev/ttyGS0 > /dev/ttyGS0 2>&1'
			echo "USB shell exited at uptime $(cut -d' ' -f1 /proc/uptime)" >> "$LOGDIR/console-start.log"
		fi
		sleep 2
	done
) &

# The LineageOS system with no slot store is the expected USB factory stage.
# Other rescue states are faults, so do not mislabel those as an install.
# This fbprobe build uses Tater's dark setup palette and orange accent.
if [ -x /usr/local/bin/fbprobe ]; then
	if [ "$MODE" = android ]; then
		/usr/local/bin/fbprobe -hold 1000h -title "INSTALLING TATER" -lines \\
"Preparing your Checkers for Tater.|Keep power and USB connected.|This can take several minutes.|If this stays, check the USB installer." \\
			> /tmp/fbprobe.log 2>&1 &
	else
		/usr/local/bin/fbprobe -hold 1000h -title "TATER RECOVERY" -lines \\
"The system could not start.|Your data may still be intact.|Keep power and USB connected.|Check the USB installer or slot status." \\
			> /tmp/fbprobe.log 2>&1 &
	fi
fi
'''


def align(value: int, multiple: int) -> int:
    return (value + multiple - 1) // multiple * multiple


def replace_rescue_in_cpio(archive: bytes, fbprobe: bytes) -> bytes:
    """Rewrite /init and fbprobe, preserving every other cpio entry and its metadata."""
    output = bytearray()
    cursor = 0
    found_init = found_fbprobe = False
    while cursor < len(archive):
        if archive[cursor:cursor + 6] != MAGIC:
            raise ValueError("unexpected initramfs cpio header")
        header = bytearray(archive[cursor:cursor + 110])
        if len(header) != 110:
            raise ValueError("truncated initramfs cpio header")
        fields = [int(header[6 + n * 8:14 + n * 8], 16) for n in range(13)]
        size, name_size = fields[6], fields[11]
        if not 1 <= name_size <= 4096:
            raise ValueError("invalid initramfs cpio name length")
        name_end = cursor + 110 + name_size
        name_bytes = archive[cursor + 110:name_end]
        if len(name_bytes) != name_size or not name_bytes.endswith(b"\0"):
            raise ValueError("truncated initramfs cpio name")
        name = name_bytes[:-1]
        body_at = align(name_end, 4)
        body = archive[body_at:body_at + size]
        if len(body) != size:
            raise ValueError("truncated initramfs cpio body")
        cursor = align(body_at + size, 4)
        if name in (b"init", b"/init"):
            if found_init or body.count(OLD_SCREEN) != 1 or body.count(OLD_CONSOLE) != 1:
                raise ValueError("pinned rescue init did not match the expected install screen")
            body = body.replace(OLD_SCREEN + b"\n" + OLD_CONSOLE, NEW_SCREEN, 1)
            if OLD_SCREEN in body or OLD_CONSOLE in body:
                raise ValueError("pinned rescue block was not contiguous")
            header[6 + 6 * 8:14 + 6 * 8] = f"{len(body):08X}".encode()
            found_init = True
        elif name in (b"usr/local/bin/fbprobe", b"/usr/local/bin/fbprobe"):
            if found_fbprobe:
                raise ValueError("duplicate rescue framebuffer renderer")
            body = fbprobe
            header[6 + 6 * 8:14 + 6 * 8] = f"{len(body):08X}".encode()
            found_fbprobe = True
        output.extend(header)
        output.extend(name_bytes)
        output.extend(b"\0" * (align(len(output), 4) - len(output)))
        output.extend(body)
        output.extend(b"\0" * (align(len(output), 4) - len(output)))
        if name == b"TRAILER!!!":
            if cursor != len(archive) or not found_init or not found_fbprobe:
                raise ValueError("unexpected initramfs trailer or missing rescue files")
            return bytes(output)
    raise ValueError("initramfs cpio has no trailer")


def brand_boot(image: bytes, fbprobe: bytes) -> bytes:
    if fbprobe[:4] != b"\x7fELF" or fbprobe[4:6] != b"\x01\x01" or fbprobe[18:20] != b"\x28\x00":
        raise ValueError("Tater rescue fbprobe must be a 32-bit little-endian ARM ELF")
    if image[:8] != b"ANDROID!" or len(image) < 2048:
        raise ValueError("not a Checkers Android boot image")
    kernel_size, _, ramdisk_size, _, second_size, _, _, page, header_version = struct.unpack_from("<9I", image, 8)
    if header_version != 0 or second_size or page != 2048:
        raise ValueError("unexpected pinned Checkers boot header")
    ramdisk_at = page + align(kernel_size, page)
    if len(image) != ramdisk_at + align(ramdisk_size, page):
        raise ValueError("unexpected Checkers boot image length")
    kernel = image[page:page + kernel_size]
    compressed = image[ramdisk_at:ramdisk_at + ramdisk_size]
    if not compressed.startswith(b"\xfd7zXZ\0"):
        raise ValueError("unexpected pinned initramfs compression")
    # The pinned boot image uses XZ CRC32 (stream check 1). Python's default
    # CRC64 (check 4) is not supported by this device's early initramfs
    # decompressor and makes it reset before the rescue console can start.
    if compressed[7] != lzma.CHECK_CRC32:
        raise ValueError("unexpected pinned initramfs XZ check")
    ramdisk = lzma.compress(
        replace_rescue_in_cpio(lzma.decompress(compressed), fbprobe),
        format=lzma.FORMAT_XZ,
        check=lzma.CHECK_CRC32,
    )
    header = bytearray(image[:page])
    struct.pack_into("<I", header, 16, len(ramdisk))
    boot_id = hashlib.sha1()
    for blob in (kernel, ramdisk, b"", b""):
        boot_id.update(blob)
        boot_id.update(struct.pack("<I", len(blob)))
    header[576:608] = boot_id.digest() + b"\0" * 12
    result = bytes(header) + kernel + b"\0" * (align(kernel_size, page) - kernel_size)
    result += ramdisk + b"\0" * (align(len(ramdisk), page) - len(ramdisk))
    if len(result) > 16 * 1024 * 1024:
        raise ValueError("branded boot image exceeds the 16 MiB partition limit")
    return result


def build(source: Path, fbprobe: Path, destination: Path) -> None:
    image = source.read_bytes()
    if hashlib.sha256(image).hexdigest() != BASE_SHA256:
        raise ValueError("boot image is not the pinned TECHO5 Checkers v0.7.16 input")
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(brand_boot(image, fbprobe.read_bytes()))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("fbprobe", type=Path)
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    build(args.source, args.fbprobe, args.destination)
    print(f"Tater-branded Checkers boot image: {args.destination}")
