import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from tools.merge_release_manifests import merge


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


class MergeReleaseManifestTests(unittest.TestCase):
    def target(self, root, target, filename, content=b"artifact"):
        root.mkdir()
        artifact = root / filename
        artifact.write_bytes(content)
        manifest = root / "firmware-manifest.json"
        manifest.write_text(json.dumps({
            "schema": 1,
            "product": "Tater Echo Firmware",
            "version": "v1.2.3",
            "targets": {target: {"artifacts": {"factory": {
                "name": filename, "size": len(content), "sha256": sha(artifact),
            }}}},
        }))
        return manifest

    def test_merges_and_copies_verified_target_artifacts(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first = self.target(root / "a", "biscuit", "biscuit.bin", b"a")
            second = self.target(root / "b", "checkers", "checkers.apk", b"b")
            output = root / "out"
            merge("v1.2.3", [first, second], output)
            result = json.loads((output / "firmware-manifest.json").read_text())
            self.assertEqual(set(result["targets"]), {"biscuit", "checkers"})
            self.assertEqual((output / "biscuit.bin").read_bytes(), b"a")
            self.assertEqual((output / "checkers.apk").read_bytes(), b"b")

    def test_rejects_tampered_artifact(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest = self.target(root / "a", "checkers", "checkers.apk")
            (manifest.parent / "checkers.apk").write_bytes(b"changed")
            with self.assertRaises(SystemExit):
                merge("v1.2.3", [manifest], root / "out")


if __name__ == "__main__":
    unittest.main()
