from __future__ import annotations

import subprocess
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[1]
MWW_BUILD_SCRIPT = ROOT / "device" / "build_microwakeword_runtime.sh"


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


if __name__ == "__main__":
    unittest.main()
