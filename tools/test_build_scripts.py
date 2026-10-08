from __future__ import annotations

import subprocess
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[1]
MWW_BUILD_SCRIPT = ROOT / "device" / "build_microwakeword_runtime.sh"
ORT_BUILD_SCRIPT = ROOT / "device" / "prepare_onnxruntime_linux_armv7.sh"


class BuildScriptTests(unittest.TestCase):
    def test_microwakeword_container_preserves_host_ownership(self) -> None:
        script = MWW_BUILD_SCRIPT.read_text(encoding="utf-8")

        self.assertIn('--user "$(id -u):$(id -g)"', script)
        self.assertIn("--env HOME=/tmp", script)
        self.assertEqual(script.count('"${DOCKER_USER_ARGS[@]}"'), 2)
        self.assertIn('! -w "$DEVICE_DIR/build"', script)
        self.assertIn('! -w "$(dirname "$LIBRARY")"', script)

    def test_microwakeword_build_script_parses(self) -> None:
        subprocess.run(
            ["bash", "-n", str(MWW_BUILD_SCRIPT)],
            check=True,
            cwd=ROOT,
        )

    def test_linux_armv7_onnxruntime_uses_verified_release_seed(self) -> None:
        script = ORT_BUILD_SCRIPT.read_text(encoding="utf-8")

        self.assertIn("tater-echo-checkers-v2.2.0-onnxruntime.so", script)
        self.assertIn("a0954d59f1f99ae7d3d8823dadf8acf63f2044d8b7bca838edf2e454f9325941", script)
        self.assertIn("f4047359e0dbf2078fff0e88bfb806de3c2b8891a895ac0dffdc3dfcb8bb489b", script)
        self.assertIn("TATER_ORT_FORCE_SOURCE_BUILD", script)

    def test_linux_armv7_onnxruntime_script_parses(self) -> None:
        subprocess.run(
            ["sh", "-n", str(ORT_BUILD_SCRIPT)],
            check=True,
            cwd=ROOT,
        )


if __name__ == "__main__":
    unittest.main()
