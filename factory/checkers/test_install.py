import importlib.util
from pathlib import Path
import unittest
from unittest import mock


MODULE = Path(__file__).with_name("install.py")
SPEC = importlib.util.spec_from_file_location("checkers_install", MODULE)
install = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(install)


class FakeAdb:
    def __init__(self, values):
        self.values = values
        self.runs = []

    def run(self, *args, **kwargs):
        self.runs.append(args)
        return ""

    def shell(self, command, check=True):
        self.runs.append(("shell", command))
        for prefix, value in self.values.items():
            if command.startswith(prefix):
                return value
        return ""


class CheckersInstallerTests(unittest.TestCase):
    def healthy(self, release="7.1.2", direct_root=True):
        return FakeAdb({
            "getprop ro.product.device": "checkers",
            "getprop ro.build.version.release": release,
            "id": "uid=0(root) gid=0(root)" if direct_root else "uid=2000(shell) gid=2000(shell)",
            "for p in /dev/block/by-name/recovery": "/dev/block/by-name/recovery",
            "layout=missing; for p in /dev/block/by-name/recovery": "amonet-2",
        })

    def test_accepts_stock_fire_os_and_amonet_2(self):
        self.assertEqual("stock Fire OS 6", install.require_checkers(self.healthy()))

    def test_accepts_lineage_android_11(self):
        self.assertEqual("LineageOS 18.1", install.require_checkers(self.healthy(release="11")))

    def test_accepts_stock_su_root(self):
        adb = self.healthy(direct_root=False)
        adb.values["su -c id"] = "uid=0(root) gid=0(root)"
        adb.values["su -c 'for p in /dev/block/by-name/recovery"] = "/dev/block/by-name/recovery"
        adb.values["su -c 'layout=missing; for p in /dev/block/by-name/recovery"] = "amonet-2"
        self.assertEqual("stock Fire OS 6", install.require_checkers(adb))
        self.assertTrue(any("su -c '" in args[1] for args in adb.runs if args[0] == "shell"))

    def test_rejects_a_different_echo(self):
        adb = self.healthy()
        adb.values["getprop ro.product.device"] = "biscuit"
        with self.assertRaisesRegex(install.InstallError, "not checkers"):
            install.require_checkers(adb)

    def test_rejects_amonet_1_layout(self):
        adb = self.healthy()
        adb.values["layout=missing; for p in /dev/block/by-name/recovery"] = "amonet-1"
        with self.assertRaisesRegex(install.InstallError, "2.0.1"):
            install.require_checkers(adb)

    def test_rejects_missing_root(self):
        adb = self.healthy(direct_root=False)
        with self.assertRaisesRegex(install.InstallError, "root is required"):
            install.require_checkers(adb)

    def test_rejects_unknown_android_generation(self):
        with self.assertRaisesRegex(install.InstallError, "supported bring-up"):
            install.require_checkers(self.healthy(release="9"))

    def test_launcher_change_is_optional(self):
        adb = self.healthy()
        adb.values[f"pm path {install.PACKAGE}"] = "package:/data/app/base.apk"
        with mock.patch.object(install, "PAYLOAD", Path("/tmp")):
            install.install_apk(adb, False)
        commands = adb.runs
        self.assertFalse(any("set-home-activity" in " ".join(args) for args in commands))

    def test_launcher_rejection_does_not_block_preview(self):
        adb = self.healthy()
        adb.values[f"pm path {install.PACKAGE}"] = "package:/data/app/base.apk"
        adb.values["cmd package set-home-activity"] = "Error: unsupported command"
        with mock.patch.object(install, "PAYLOAD", Path("/tmp")), mock.patch("builtins.print"):
            install.install_apk(adb, True)
        self.assertTrue(any("am start" in args[1] for args in adb.runs if args[0] == "shell"))

    def test_unlock_confirmation_is_exact(self):
        with mock.patch("builtins.print"), mock.patch("builtins.input", return_value="2.0.1"):
            with self.assertRaisesRegex(install.InstallError, "cancelled"):
                install.confirm_unlock(False)
        with mock.patch("builtins.print"), mock.patch("builtins.input", return_value="CHECKERS"):
            install.confirm_unlock(False)


if __name__ == "__main__":
    unittest.main()
