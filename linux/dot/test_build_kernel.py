import gzip
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
import zlib

SPEC = importlib.util.spec_from_file_location("dot_audio_kernel", Path(__file__).with_name("build_kernel.py"))
kernel = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(kernel)


def synthetic_kernel(target, *, profile=True):
    config = f'CONFIG_ARCH_MTK_PROJECT="{target}"\n'.encode()
    marker = kernel.MARKER + b" Tater ADC HPF Status" if profile else b"old profile"
    return b"zImage fixture" + gzip.compress(marker + b"IKCFG_ST" + gzip.compress(config))


class DotAudioKernelTests(unittest.TestCase):
    def test_correct_board_and_profile_are_required(self):
        with tempfile.TemporaryDirectory() as directory:
            filename = Path(directory) / "kernel"
            for target in ("biscuit", "radar"):
                contents = synthetic_kernel(target)
                filename.write_bytes(contents)
                value = kernel.verify_kernel(filename, target)
                self.assertEqual(hashlib.sha256(contents).hexdigest(), value["sha256"])
                with self.assertRaisesRegex(ValueError, "board does not match"):
                    kernel.verify_kernel(filename, "radar" if target == "biscuit" else "biscuit")
                filename.write_bytes(synthetic_kernel(target, profile=False))
                with self.assertRaisesRegex(ValueError, "lacks the verified"):
                    kernel.verify_kernel(filename, target)

    def test_invalid_truncated_and_oversized_payloads_fail(self):
        for value in (b"", b"old kernel", synthetic_kernel("biscuit")[:-5],
                      gzip.compress(b"x" * (64 * 1024 * 1024 + 1))):
            with self.assertRaises((ValueError, zlib.error)):
                kernel.kernel_config(value)

    def test_production_source_hashes_are_locked(self):
        lock = json.loads(kernel.LOCK.read_text())
        self.assertEqual({"biscuit", "radar"}, set(lock["targets"]))
        for name, expected in lock["source_files"].items():
            self.assertEqual(expected, hashlib.sha256((kernel.AUDIO / name).read_bytes()).hexdigest())
        self.assertIn("#define TATER_ADC_HPF80_DEFAULT 1", (kernel.AUDIO / "tater_adc3101_hpf.h").read_text())

    @unittest.skipUnless(shutil.which("cc"), "host C compiler unavailable")
    def test_exact_production_helper_has_fake_bus_rollback_coverage(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "selftest"
            subprocess.run(["cc", "-std=c11", "-O2", "-Wall", "-Wextra", "-Werror",
                            "-I", str(kernel.AUDIO), str(kernel.AUDIO / "adc3101_hpf_selftest.c"),
                            "-o", str(binary)], check=True, capture_output=True)
            subprocess.run([str(binary)], check=True, capture_output=True)

    def test_release_build_uses_target_source_instead_of_old_kernel_download(self):
        workflow = (kernel.ROOT / ".github/workflows/release.yml").read_text()
        self.assertIn("linux/dot/build_kernel.py --target '${{ matrix.target }}'", workflow)
        self.assertNotIn("'platforms.dot.targets.${{ matrix.target }}.kernel'", workflow)


if __name__ == "__main__":
    unittest.main()
