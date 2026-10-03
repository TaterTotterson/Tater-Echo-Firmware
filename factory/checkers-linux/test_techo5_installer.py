import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock


SOURCE = Path(__file__).parent / "tools/techo5/install-show.py"
SPEC = importlib.util.spec_from_file_location("tater_techo5_install_show_test", SOURCE)
installer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(installer)


class FakeAdb:
    def __init__(self, digest):
        self.digest = digest
        self.actions = []

    def sh(self, command):
        self.actions.append(command)
        if command == "twrp format data":
            return "Done"
        if command == "mount":
            return " /data "
        if command.startswith("sha256sum "):
            return self.digest + "  /data/lineage.zip"
        if command.startswith("twrp install "):
            return "succeeded"
        return ""

    def state(self):
        return "recovery"

    def reboot(self, target):
        self.actions.append("reboot " + target)

    def push(self, source, destination, ready=None):
        self.actions.append("push " + destination)


class LineageInstallTests(unittest.TestCase):
    def test_settles_adb_before_pushing_zip(self):
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / "lineage.zip"
            image.write_bytes(b"lineage test image")
            adb = FakeAdb(hashlib.sha256(image.read_bytes()).hexdigest())

            def immediate_wait(_description, _timeout, test, _interval):
                for _ in range(3):
                    if test():
                        return
                self.fail("ADB never became ready")

            with mock.patch.object(installer, "wait_for", side_effect=immediate_wait), \
                 mock.patch.object(installer.time, "monotonic", side_effect=[0, 21]):
                installer.install_lineage(adb, str(image))

            self.assertIn("push /data/lineage.zip", adb.actions)
            self.assertLess(adb.actions.index("reboot recovery"),
                            adb.actions.index("push /data/lineage.zip"))
            self.assertIn("twrp install /data/lineage.zip", adb.actions)


if __name__ == "__main__":
    unittest.main()
