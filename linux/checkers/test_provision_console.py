import importlib.util
from pathlib import Path
import unittest
from unittest import mock


MODULE = Path(__file__).with_name("provision_console.py")
SPEC = importlib.util.spec_from_file_location("checkers_provision", MODULE)
provision = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(provision)


class FakeConsole:
    def __init__(self):
        self.commands = []

    def run(self, command, _wait):
        self.commands.append(command)
        if "TATER-LINUX-READY" in command:
            return "TATER-LINUX-READY"
        if "echo PREPARE-OK" in command:
            return "PREPARE-OK"
        if "echo WRITE-OK" in command:
            return "WRITE-OK"
        if "sha256sum" in command:
            path = command.split("sha256sum ", 1)[1].split(";", 1)[0]
            key = "native" if "native.json" in path else "token"
            import hashlib
            return hashlib.sha256(self.values[key]).hexdigest() + "  " + path + "\nHASH-OK"
        return ""


class ProvisionTests(unittest.TestCase):
    def test_fresh_pairing_can_preserve_factory_wifi(self):
        console = FakeConsole()
        values = {"token": b"123456\n", "native": b'{"url":"http://tater:8501"}\n'}
        console.values = values
        fake_lib = type("FakeLib", (), {
            "CONSOLE_TECHO5": ("1d6b", "0104"),
            "Console": staticmethod(lambda _serial, _ids: console),
        })
        with mock.patch.object(provision, "load_techo5lib", return_value=fake_lib):
            provision.provision_console("SERIAL", "/tools", values, no_reboot=True)
        script = "\n".join(console.commands)
        self.assertIn("device_token", script)
        self.assertIn("native.json", script)
        self.assertNotIn("wpa_supplicant.conf", script)
        self.assertNotIn("rebootto", script)


if __name__ == "__main__":
    unittest.main()
