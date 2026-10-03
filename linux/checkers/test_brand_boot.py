import hashlib
import importlib.util
import lzma
from pathlib import Path
import struct
import unittest
from unittest import mock


def load(name):
    source = Path(__file__).with_name(name + ".py")
    spec = importlib.util.spec_from_file_location(name, source)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


brand = load("brand_boot")
renderer = load("build_rescue_fbprobe")


def entry(name, body=b"", mode=0o100755):
    fields = (1, mode, 0, 0, 1, 0, len(body), 0, 0, 0, 0, len(name) + 1, 0)
    header = b"070701" + b"".join(f"{value:08X}".encode() for value in fields)
    result = header + name.encode() + b"\0"
    result += b"\0" * (-len(result) % 4)
    result += body
    return result + b"\0" * (-len(result) % 4)


def files(archive):
    result = {}
    cursor = 0
    while cursor < len(archive):
        header = archive[cursor:cursor + 110]
        assert header[:6] == b"070701"
        size = int(header[54:62], 16)
        name_size = int(header[94:102], 16)
        name = archive[cursor + 110:cursor + 110 + name_size - 1].decode()
        body_at = brand.align(cursor + 110 + name_size, 4)
        result[name] = archive[body_at:body_at + size]
        cursor = brand.align(body_at + size, 4)
    return result


def boot_image(archive):
    kernel = b"pinned-kernel"
    ramdisk = lzma.compress(archive, check=lzma.CHECK_CRC32)
    header = bytearray(2048)
    header[:8] = b"ANDROID!"
    struct.pack_into("<9I", header, 8, len(kernel), 0x40080000, len(ramdisk),
                     0x69244e00, 0, 0, 0, 2048, 0)
    return bytes(header) + kernel + b"\0" * (brand.align(len(kernel), 2048) - len(kernel)) + \
        ramdisk + b"\0" * (brand.align(len(ramdisk), 2048) - len(ramdisk))


class BrandBootTests(unittest.TestCase):
    def test_brands_only_rescue_files_and_keeps_kernel(self):
        original = entry("init", b"before\n" + brand.OLD_SCREEN + b"\n" + brand.OLD_CONSOLE + b"after\n")
        original += entry("usr/local/bin/fbprobe", b"old renderer")
        original += entry("etc/untouched", b"important data")
        original += entry("TRAILER!!!", b"", 0)
        arm_elf = b"\x7fELF\x01\x01" + b"\0" * 12 + b"\x28\x00" + b"new renderer"
        image = boot_image(original)

        result = brand.brand_boot(image, arm_elf)
        kernel_size, _, ramdisk_size, _, _, _, _, page, _ = struct.unpack_from("<9I", result, 8)
        self.assertEqual(image[page:page + kernel_size], result[page:page + kernel_size])
        ramdisk_at = page + brand.align(kernel_size, page)
        self.assertEqual(result[ramdisk_at + 7], lzma.CHECK_CRC32)
        output = files(lzma.decompress(result[ramdisk_at:ramdisk_at + ramdisk_size]))
        self.assertIn(b"INSTALLING TATER", output["init"])
        self.assertIn(b"TATER RECOVERY", output["init"])
        self.assertNotIn(brand.OLD_SCREEN, output["init"])
        self.assertNotIn(brand.OLD_CONSOLE, output["init"])
        self.assertLess(output["init"].index(b"t5_usb_acm"), output["init"].index(b"fbprobe -hold"))
        self.assertEqual(arm_elf, output["usr/local/bin/fbprobe"])
        self.assertEqual(b"important data", output["etc/untouched"])
        self.assertEqual(b"pinned-kernel", result[page:page + kernel_size])
        self.assertNotEqual(image[576:608], result[576:608])

    def test_rejects_unexpected_rescue_script(self):
        archive = entry("init", b"different screen") + entry("usr/local/bin/fbprobe", b"old")
        archive += entry("TRAILER!!!", b"", 0)
        with self.assertRaisesRegex(ValueError, "expected install screen"):
            brand.replace_rescue_in_cpio(archive, b"new")

    def test_rejects_wrong_renderer_architecture(self):
        with self.assertRaisesRegex(ValueError, "32-bit little-endian ARM"):
            brand.brand_boot(b"ANDROID!", b"not an ELF")

    def test_theme_uses_tater_setup_colors(self):
        source = "\n".join(renderer.PALETTE) + "\n"
        with mock.patch.object(renderer, "SOURCE_SHA256", hashlib.sha256(source.encode()).hexdigest()):
            themed = renderer.themed_source(source.encode()).decode()
        for old, new in renderer.PALETTE.items():
            self.assertNotIn(old, themed)
            self.assertIn(new, themed)


if __name__ == "__main__":
    unittest.main()
