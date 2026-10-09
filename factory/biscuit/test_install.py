import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock


MODULE = Path(__file__).with_name("install.py")
SPEC = importlib.util.spec_from_file_location("biscuit_install", MODULE)
install = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(install)


class FactoryInstallerTests(unittest.TestCase):
    def test_source_checkout_explains_how_to_get_release_bundle(self):
        with tempfile.TemporaryDirectory() as directory:
            original = install.MANIFEST
            install.MANIFEST = Path(directory) / "bundle-manifest.json"
            try:
                with self.assertRaisesRegex(
                        install.InstallError,
                        r"outside a published factory bundle.*factory install\.sh.*releases/latest"):
                    install.load_manifest()
            finally:
                install.MANIFEST = original

    def test_boot_kind_rejects_non_android(self):
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / "boot.img"
            image.write_bytes(b"not a boot image")
            self.assertEqual(install.boot_kind(image), "invalid")

    def test_adb_selection_accepts_twrp_recovery_transport(self):
        output = """List of devices attached
G090LF1072830RGM recovery product:omni_biscuit device:biscuit
offline-one offline
unauthorized-one unauthorized
normal-one device product:biscuit
"""
        self.assertEqual(
            install.connected_adb_devices(output),
            ["G090LF1072830RGM", "normal-one"],
        )

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
        self.assertEqual(install.partition_number("/dev/block/mmcblk0p17"), 17)
        with self.assertRaises(install.InstallError):
            install.partition_number("/dev/block/not-a-partition")

    def test_em_os_init_matches_the_reference_kernel(self):
        self.assertEqual(install.init_name_for_arch("arm"), "init32")
        self.assertEqual(install.init_name_for_arch("arm64"), "init")
        with self.assertRaisesRegex(install.InstallError, "kernel architecture"):
            install.init_name_for_arch("")

    def test_radar_selects_the_fire_os_6_arm_donor(self):
        self.assertEqual(install.select_donor_slot(
            "radar", "stock", "arm", "stock", "arm64"), "a")
        self.assertEqual(install.select_donor_slot(
            "radar", "stock", "arm64", "stock", "arm"), "b")
        with self.assertRaisesRegex(install.InstallError, "Fire OS 6 ARM"):
            install.select_donor_slot(
                "radar", "stock", "arm64", "stock", "arm64")

    def test_biscuit_keeps_preferring_stock_in_slot_b(self):
        self.assertEqual(install.select_donor_slot(
            "biscuit", "stock", "arm", "stock", "arm64"), "b")

    def test_build_emos_selects_the_matching_bundled_init(self):
        class Packer:
            selected_init = None

            @staticmethod
            def reference_kernel_arch(_reference):
                return "arm64"

            @classmethod
            def build_emos_image(cls, _reference, init_binary, _version, **kwargs):
                cls.selected_init = init_binary
                self.assertEqual(kwargs["system_part"], 14)
                self.assertEqual(kwargs["data_part"], 16)
                self.assertEqual(kwargs["boot_part"], 10)
                self.assertEqual(kwargs["cache_part"], 15)
                return {"image": b"ANDROID!image", "size": 13, "sha256": "a" * 64}

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            payload = root / "payload"
            payload.mkdir()
            for name in ("wpa_supplicant", "wpa_cli", "em-wifi", "busybox"):
                (payload / name).write_bytes(name.encode())
            (payload / "init").write_bytes(b"aarch64-init")
            (payload / "init32").write_bytes(b"arm-init")
            reference = root / "boot.img"
            output = root / "emos.img"
            reference.write_bytes(b"reference")
            with (
                mock.patch.object(install, "PAYLOAD", payload),
                mock.patch.object(install, "load_packer", return_value=Packer),
            ):
                install.build_emos(reference, 14, 16, 10, 15, "v1.2.3", output)
            self.assertEqual(Packer.selected_init, b"aarch64-init")
            self.assertEqual(output.read_bytes(), b"ANDROID!image")

    def test_device_match_keeps_biscuit_and_radar_distinct(self):
        self.assertTrue(install.device_matches(
            "biscuit", "biscuit_puffin", "A3S5BH2HU6VAYF\x00"))
        self.assertTrue(install.device_matches(
            "radar", "radar_puffin", "A7WXQPH584YP\n"))
        self.assertFalse(install.device_matches(
            "radar", "biscuit_puffin", "A3S5BH2HU6VAYF"))
        self.assertFalse(install.device_matches(
            "biscuit", "radar_puffin", "A7WXQPH584YP"))
        self.assertFalse(install.device_matches(
            "radar", "notradar", ""))


if __name__ == "__main__":
    unittest.main()
