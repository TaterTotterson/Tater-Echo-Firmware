import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("athena_factory_under_test", Path(__file__).with_name("install.py"))
install = importlib.util.module_from_spec(spec)
spec.loader.exec_module(install)


class AthenaInstallTests(unittest.TestCase):
    def setup_mocks(self, hardware):
        loader = SimpleNamespace(exec_module=lambda module: None)
        return (mock.patch.object(install, "verify_bundle", return_value={"version": "v5.0.0"}),
                mock.patch.object(install.importlib.util, "spec_from_file_location", return_value=SimpleNamespace(loader=loader)),
                mock.patch.object(install.importlib.util, "module_from_spec", return_value=hardware))

    def test_public_preparation_precedes_install_and_selected_device_is_reused(self):
        hardware, events = mock.Mock(), []
        hardware.prepare.side_effect = lambda *args: events.append("prepare") or "plan"
        hardware.finish_factory.side_effect = lambda *args: events.append("finish")
        a, b, c = self.setup_mocks(hardware)
        with a, b, c, mock.patch.object(install.subprocess, "call", side_effect=lambda *args, **kw: events.append("flash") or 0):
            self.assertEqual(install.main(["--serial", "selected-unit", "--work", "test-work"]), 0)
        self.assertEqual(events, ["prepare", "flash", "finish"])
        hardware.finish_factory.assert_called_once_with("plan", install.ROOT / "tools", "selected-unit")

    def test_preparation_failure_never_calls_platform_installer(self):
        hardware = mock.Mock()
        hardware.prepare.side_effect = ValueError("invalid pinned input")
        a, b, c = self.setup_mocks(hardware)
        with a, b, c, mock.patch.object(install.subprocess, "call") as flash:
            self.assertEqual(install.main(["--serial", "selected-unit"]), 1)
            flash.assert_not_called()

    def test_dry_run_and_failed_flash_never_install_runtime(self):
        for arguments, status in ((["--serial=selected-unit", "--dry-run"], 0), (["--serial=selected-unit"], 1)):
            hardware = mock.Mock()
            a, b, c = self.setup_mocks(hardware)
            with self.subTest(arguments=arguments), a, b, c, mock.patch.object(install.subprocess, "call", return_value=status):
                self.assertEqual(install.main(arguments), status)
                hardware.finish_factory.assert_not_called()


if __name__ == "__main__":
    unittest.main()
