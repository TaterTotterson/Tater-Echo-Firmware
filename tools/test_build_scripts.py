from __future__ import annotations

import subprocess
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[1]
MWW_BUILD_SCRIPT = ROOT / "device" / "build_microwakeword_runtime.sh"
ORT_BUILD_SCRIPT = ROOT / "device" / "prepare_onnxruntime_linux_armv7.sh"
RELEASE_WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"


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

    def test_release_builds_both_puffin_emos_targets(self) -> None:
        workflow = RELEASE_WORKFLOW.read_text(encoding="utf-8")
        em_os_job = workflow.split("\n  biscuit:\n", 1)[1].split(
            "\n  checkers:\n", 1
        )[0]
        self.assertIn("- target: biscuit", em_os_job)
        self.assertIn("- target: radar", em_os_job)
        self.assertIn("FirmwareTarget=${{ matrix.target }}", em_os_job)
        self.assertIn("--target '${{ matrix.target }}'", em_os_job)
        self.assertIn("Compile both emOS init architectures", em_os_job)
        self.assertIn("aarch64-linux-android21-clang", em_os_job)
        self.assertIn("armv7a-linux-androideabi21-clang", em_os_job)
        self.assertIn("emos/build/init emos/build/init32", em_os_job)
        self.assertIn("staging/radar/firmware-manifest.json", workflow)


if __name__ == "__main__":
    unittest.main()
