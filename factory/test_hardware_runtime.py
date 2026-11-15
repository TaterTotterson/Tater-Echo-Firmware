import hashlib
import io
import json
from pathlib import Path
import subprocess
import struct
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

from factory import hardware_runtime as runtime


class Response(io.BytesIO):
    def geturl(self):
        return "https://example.test/pinned-input"


class HardwareRuntimeTests(unittest.TestCase):
    def test_portable_image_reader_is_bounded_and_rejects_unsafe_nodes(self):
        def image_bytes():
            data = bytearray(16 * 1024)
            def u32(at, value):
                struct.pack_into("<I", data, at, value)
            def u16(at, value):
                struct.pack_into("<H", data, at, value)
            for at, value in ((0, 8), (4, 16), (20, 1), (24, 0), (40, 8), (96, 0x42)):
                u32(1024 + at, value)
            u16(1024 + 56, 0xef53)
            u16(1024 + 88, 256)
            u16(1024 + 254, 32)
            u32(2048 + 8, 3)
            for number, mode, size, block in ((2, 0x41ed, 1024, 5), (3, 0x81a4, 8, 6)):
                at = 3072 + (number - 1) * 256
                u16(at, mode)
                u32(at + 4, size)
                u32(at + 32, 0x80000)
                struct.pack_into("<HHHHI", data, at + 40, 0xf30a, 1, 4, 0, 0)
                struct.pack_into("<IHHI", data, at + 52, 0, 1, 0, block)
            struct.pack_into("<IHBB", data, 5120, 3, 1024, 4, 1)
            data[5128:5132] = b"file"
            data[6144:6152] = b"verified"
            return data
        with tempfile.TemporaryDirectory() as temporary:
            image = Path(temporary) / "image"
            image.write_bytes(image_bytes())
            self.assertEqual(runtime.read_ext4_file(image, "/file"), b"verified")
            for path in ("file", "/../file", "/missing", "/file/child"):
                with self.subTest(path=path), self.assertRaises(ValueError):
                    runtime.read_ext4_file(image, path)
            for offset, replacement in ((1024 + 56, b"xx"), (5124, b"\x00\x00"),
                                        (3584, b"\xff\xa1"), (3584 + 60, b"\xff\xff\xff\xff")):
                data = image_bytes()
                data[offset:offset + len(replacement)] = replacement
                image.write_bytes(data)
                with self.subTest(offset=offset), self.assertRaises(ValueError):
                    runtime.read_ext4_file(image, "/file")

    def fixture(self, root):
        objects = root / "objects"
        objects.mkdir()
        data = b"public bootstrap"
        sha = hashlib.sha256(data).hexdigest()
        (objects / sha).write_bytes(data)
        vendor = root / "vendor/lib"
        vendor.mkdir(parents=True)
        (vendor / "codec.so").write_bytes(b"device-local codec")
        profile = {"kernel": "test-kernel", "panel": "480,2880", "roots": ["tater-one", "tater-two"],
                   "files": {"tater-one/linker": {"sha256": sha, "public": sha},
                             "tater-two/lib/codec.so": {"sha256": runtime.digest(vendor / "codec.so"), "vendor": "codec.so"}},
                   "manifests": {"tater-one/runtime.json": {"schema": 2, "runtime_id": "test-one"},
                                 "tater-two/runtime.json": {"schema": 2, "runtime_id": "test-two"}}}
        return {"target": "checkers", "profile": profile, "objects": objects}

    def test_all_targets_have_safe_paths_and_exact_identities(self):
        for target in ("checkers", "cronos", "crown", "rook", "athena"):
            value, profile = runtime.recipe(target)
            self.assertTrue(profile["files"])
            self.assertTrue(runtime.public_inputs(value, profile))
            for manifest in profile["manifests"].values():
                self.assertIn("runtime_id", manifest)
        with self.assertRaises(ValueError):
            runtime.recipe("biscuit")
        checkers = runtime.recipe("checkers")[1]
        self.assertIn("tater-vdec-lineage-1", checkers["roots"])
        self.assertNotIn("tater-vdec", checkers["roots"])

    def test_fetch_checks_hash_limit_and_https_without_device_access(self):
        for mode in ("good", "wrong-hash", "oversize", "redirect"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                data = b"pinned public bytes"
                expected = hashlib.sha256(data).hexdigest()
                response = Response(data)
                if mode == "redirect":
                    response.geturl = lambda: "http://bad.test"
                with mock.patch.object(runtime.request, "urlopen", return_value=response):
                    path = Path(temporary) / "cache"
                    if mode == "good":
                        runtime.fetch("https://example.test/input", path, expected, 1024)
                        self.assertEqual(path.read_bytes(), data)
                    else:
                        with self.assertRaises(ValueError):
                            runtime.fetch("https://example.test/input", path, "0" * 64 if mode == "wrong-hash" else expected,
                                          1 if mode == "oversize" else 1024)
                        self.assertFalse(path.exists())

    def test_transfer_contains_no_vendor_bytes_and_refuses_changed_public_inputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            plan = self.fixture(root)
            payload = root / "payload.tar"
            runtime.archive(plan, payload)
            self.assertEqual(payload.stat().st_mode & 0o777, 0o600)
            with tarfile.open(payload) as archive:
                for name in archive.getnames():
                    data = archive.extractfile(name).read()
                    self.assertNotIn(b"device-local codec", data)
                script = archive.extractfile("install.sh").read().decode()
                self.assertIn("/vendor/lib/codec.so", script)
                self.assertLess(script.index("if [ -e /data/tater-two"), script.index("mv -Tn"))
                self.assertNotIn("mkfs", script)
                self.assertNotIn("fastboot", script)
            next(plan["objects"].iterdir()).write_bytes(b"changed")
            with self.assertRaises(ValueError):
                runtime.archive(plan, root / "bad.tar")
            self.assertFalse((root / "bad.tar").exists())

    def test_target_guard_precedes_transfer(self):
        with tempfile.TemporaryDirectory() as temporary:
            plan = self.fixture(Path(temporary))
            console = mock.Mock()
            console.run.return_value = "wrong device"
            with mock.patch("factory.recovery_install.upload_rootfs") as upload:
                with self.assertRaisesRegex(ValueError, "preflight"):
                    runtime.install(plan, mock.Mock(), console)
                upload.assert_not_called()

    def test_runtime_restart_uses_the_installed_firmware_reboot_helper(self):
        with tempfile.TemporaryDirectory() as temporary:
            plan = self.fixture(Path(temporary))
            console = mock.Mock()
            console.run.side_effect = ["TATER_RUNTIME_READY", "1000000", None]
            with mock.patch("factory.recovery_install.upload_rootfs"), \
                    mock.patch("factory.recovery_install.exchange", return_value="TATER_RUNTIME_INSTALLED"):
                runtime.install(plan, mock.Mock(), console)
            console.run.assert_called_with("/usr/local/bin/tater-reboot-now", 3)

    def test_probes_check_exact_product_kernel_panel_and_immutable_platform(self):
        for target in ("checkers", "cronos", "crown", "rook", "athena"):
            profile = runtime.recipe(target)[1]
            body = runtime.probe(target, profile)
            self.assertIn("echo-hardware-v1/" + target, body)
            self.assertIn(profile["kernel"], body)
            self.assertIn("test -s /run/techo5/slot", body)
            if target == "athena":
                self.assertIn("amzn,athena", body)
            else:
                self.assertIn("androidboot.product=" + target, body)
                self.assertIn(profile["panel"], body)

    def test_unsafe_paths_are_rejected(self):
        for name in ("/absolute", "../outside", "x/../y", "x//y", "$(command)", "x\ny"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                runtime.safe_path(name)

    def test_rook_probe_accepts_qualified_metadata_without_optional_product(self):
        profile = runtime.recipe("rook")[1]
        good = "androidboot.hardware=mt8163 lcm=1-hx8379c_wvga_dsi_vdo techo5=linux"
        cases = [(good, True), (good + " androidboot.product=rook", True),
                 (good + " androidboot.product=crown", False),
                 (good + " androidboot.product=", False),
                 (good + " androidboot.product=rook androidboot.product=rook", False),
                 (good.replace("hx8379c", "other"), False),
                 (good + " lcm=1-hx8379c_wvga_dsi_vdo", False),
                 (good.replace("mt8163", "mt8183"), False),
                 (good + " androidboot.hardware=mt8163", False),
                 (good.replace("techo5=linux", "techo5=android"), False)]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            files = {"/run/techo5/slot": "a\n", "/proc/mounts": "disk /data ext4 rw 0 0\n",
                     "/etc/tater-ota-platform": "echo-hardware-v1/rook\n",
                     "/sys/class/graphics/fb0/virtual_size": profile["panel"] + "\n",
                     "/sys/class/graphics/fb0/bits_per_pixel": "32\n", "/proc/cmdline": good}
            for name, value in files.items():
                path = root / name[1:]
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(value)
            script = runtime.probe("rook", profile)
            for name in files:
                script = script.replace(name, str(root / name[1:]))
            script = 'id() { echo 0; }; uname() { echo "' + profile["kernel"] + '"; };\n' + script
            for command, accepted in cases:
                with self.subTest(command=command):
                    (root / "proc/cmdline").write_text(command + "\n")
                    result = subprocess.run(["sh", "-c", script], capture_output=True, text=True)
                    self.assertEqual(result.returncode == 0, accepted, result.stderr)
            (root / "proc/cmdline").write_text(good + "\n")
            for name, invalid in (("/sys/class/graphics/fb0/virtual_size", "480,2880"),
                                  ("/sys/class/graphics/fb0/bits_per_pixel", "16"),
                                  ("/etc/tater-ota-platform", "echo-hardware-v1/crown")):
                path = root / name[1:]
                original = path.read_text()
                path.write_text(invalid + "\n")
                try:
                    self.assertNotEqual(subprocess.run(["sh", "-c", script], capture_output=True).returncode, 0)
                finally:
                    path.write_text(original)

    def test_post_install_wait_requires_a_new_verified_boot(self):
        previous = "00000000-0000-0000-0000-000000000001"
        current = "00000000-0000-0000-0000-000000000002"
        console = mock.Mock()
        for replies, accepted in (([previous], False), ([""], False),
                                  ([current, "wrong platform"], False),
                                  ([current, "TATER_RUNTIME_READY"], True)):
            console.run.side_effect = replies
            self.assertEqual(runtime.runtime_reboot_ready(console, previous, "guard"), accepted)

    @unittest.skipUnless(sys.platform.startswith("linux"), "exercise the production BusyBox/GNU shell in Linux")
    def test_native_install_retry_and_invalid_existing_destination(self):
        for mode in ("success-retry", "partial-retry", "bad-vendor", "bad-existing", "symlink-vendor"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                plan = self.fixture(root)
                data = root / "data"
                remote = data / "tater-runtime-install"
                remote.mkdir(parents=True)
                if mode == "bad-vendor":
                    (root / "vendor/lib/codec.so").write_bytes(b"bad source")
                if mode == "symlink-vendor":
                    source = root / "vendor/lib/codec.so"
                    source.rename(source.with_name("other.so"))
                    source.symlink_to(source.with_name("other.so"))
                if mode == "bad-existing":
                    existing = data / "tater-two"
                    existing.mkdir()
                    (existing / "keep-me").write_bytes(b"must not change")
                with mock.patch.object(runtime, "probe", return_value="set -eu"):
                    script = runtime.installation_script("checkers", plan["profile"])
                script = script.replace("/data/", str(data) + "/").replace("/vendor/", str(root / "vendor") + "/")
                runner = root / "run.sh"
                runner.write_text(script)
                if mode == "partial-retry":
                    # Simulate a power loss after the first atomic root move.
                    first = script.replace('if [ ! -e ' + str(data) + '/tater-two ]; then', 'exit 93\nif [ ! -e ' + str(data) + '/tater-two ]; then')
                    runner.write_text(first)
                    with mock.patch.object(runtime, "installation_script", return_value=first):
                        runtime.archive(plan, remote / "payload.tar")
                    result = subprocess.run(["sh", str(runner)], capture_output=True, text=True)
                    self.assertEqual(result.returncode, 93, result.stderr)
                    self.assertTrue((data / "tater-one/linker").is_file())
                    self.assertFalse((data / "tater-two").exists())
                    (remote / "payload.tar").unlink()
                    runner.write_text(script)
                for attempt in range(2 if mode == "success-retry" else 1):
                    with mock.patch.object(runtime, "installation_script", return_value=script):
                        runtime.archive(plan, remote / "payload.tar")
                    result = subprocess.run(["sh", str(runner)], capture_output=True, text=True)
                    if mode not in ("success-retry", "partial-retry"):
                        self.assertNotEqual(result.returncode, 0)
                        self.assertFalse((data / "tater-one").exists())
                        if mode == "bad-existing":
                            self.assertEqual((data / "tater-two/keep-me").read_bytes(), b"must not change")
                    else:
                        self.assertEqual(result.returncode, 0, result.stderr)
                        self.assertIn("TATER_RUNTIME_INSTALLED", result.stdout)
                        self.assertEqual((data / "tater-two/lib/codec.so").read_bytes(), b"device-local codec")
                        self.assertEqual((data / "tater-one/linker").stat().st_mode & 0o777, 0o555)
                        self.assertEqual(json.loads((data / "tater-two/runtime.json").read_text()), {"schema": 2, "runtime_id": "test-two"})


if __name__ == "__main__":
    unittest.main()
