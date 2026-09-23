import importlib.util
from pathlib import Path
import tempfile
import unittest


MODULE = Path(__file__).with_name("install.py")
SPEC = importlib.util.spec_from_file_location("biscuit_install", MODULE)
install = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(install)


class FactoryInstallerTests(unittest.TestCase):
    def test_boot_kind_rejects_non_android(self):
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / "boot.img"
            image.write_bytes(b"not a boot image")
            self.assertEqual(install.boot_kind(image), "invalid")

    def test_boot_kind_distinguishes_stock_and_emos(self):
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / "boot.img"
            image.write_bytes(b"ANDROID!" + b"\0" * 2040)
            self.assertEqual(install.boot_kind(image), "stock")
            data = bytearray(image.read_bytes())
            data[64:64 + len(b"emos.system=/dev/block/mmcblk0p14")] = \
                b"emos.system=/dev/block/mmcblk0p14"
            image.write_bytes(data)
            self.assertEqual(install.boot_kind(image), "emos")

    def test_partition_number(self):
        self.assertEqual(install.partition_number("/dev/block/mmcblk0p14"), 14)
        with self.assertRaises(install.InstallError):
            install.partition_number("/dev/block/not-a-partition")


if __name__ == "__main__":
    unittest.main()
