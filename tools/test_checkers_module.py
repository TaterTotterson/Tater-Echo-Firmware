import io
from pathlib import Path
import tempfile
import unittest
import zipfile

from tools.checkers_module import MODULE_FILES, build_module_archive, module_archive_bytes


class CheckersModuleTests(unittest.TestCase):
    def test_archive_is_deterministic_complete_and_versioned(self):
        first = module_archive_bytes("v1.2.3")
        second = module_archive_bytes("v1.2.3")
        self.assertEqual(first, second)
        with zipfile.ZipFile(io.BytesIO(first)) as archive:
            self.assertEqual(
                set(archive.namelist()),
                {archive_name for _, archive_name, _ in MODULE_FILES},
            )
            module_prop = archive.read("module.prop").decode()
            self.assertIn("version=1.2.3", module_prop)
            self.assertIn("versionCode=100020300", module_prop)
            modes = {
                info.filename: (info.external_attr >> 16) & 0o777
                for info in archive.infolist()
            }
            self.assertEqual(modes["service.sh"], 0o755)
            self.assertEqual(modes["module.prop"], 0o644)

    def test_writer_creates_parent_and_archive(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "nested" / "module.zip"
            self.assertEqual(build_module_archive("v0.2.3", output), output)
            self.assertTrue(output.is_file())


if __name__ == "__main__":
    unittest.main()
