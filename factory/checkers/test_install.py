import importlib.util
from pathlib import Path
import tempfile
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


class FakeRoot:
    def __init__(self):
        self.commands = []

    def run(self, command):
        self.commands.append(command)
        return ""


class CheckersInstallerTests(unittest.TestCase):
    def healthy(self, release="7.1.2", direct_root=True):
        return FakeAdb({
            "getprop ro.product.device": "checkers",
            "getprop ro.build.version.release": release,
            "getprop ro.build.display.id": install.CERTIFIED_FIREOS_DISPLAY,
            "getprop ro.build.version.incremental": install.CERTIFIED_FIREOS_INCREMENTAL,
            "id": "uid=0(root) gid=0(root)" if direct_root else "uid=2000(shell) gid=2000(shell)",
            "for p in /dev/block/by-name/recovery": "/dev/block/by-name/recovery",
            "layout=missing; for p in /dev/block/by-name/recovery": "amonet-2",
        })

    def test_accepts_stock_fire_os_and_amonet_2(self):
        self.assertEqual("stock Fire OS 6", install.require_checkers(self.healthy()))

    def test_accepts_lineage_android_11(self):
        self.assertEqual("LineageOS 18.1", install.require_checkers(self.healthy(release="11")))

    def test_rejects_untested_fire_os_build(self):
        adb = self.healthy()
        adb.values["getprop ro.build.version.incremental"] = "0013222530692"
        with self.assertRaisesRegex(install.InstallError, "requires Fire OS 6574.1"):
            install.require_checkers(adb)

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

    def test_native_install_stages_both_slots_and_boot_supervisor(self):
        adb = self.healthy()
        root = FakeRoot()
        names = {
            "server", "libtater_microwakeword.so", "hey_tater.tflite",
            "hey_tater.json", "wpa_supplicant-ap", "module.prop",
            "sepolicy.rule", "post-fs-data.sh", "service.sh", "privacy.sh",
            "privacy-packages.txt", "privacy-components.txt",
            "speech-interaction-manager.replace", "bishop.replace", "uninstall.sh",
        }
        with tempfile.TemporaryDirectory() as directory, \
                mock.patch.object(install, "PAYLOAD", Path(directory)):
            for name in names:
                (install.PAYLOAD / name).write_bytes(b"payload")
            install.install_native(adb, root)

        pushes = {args[-1].rsplit("/", 1)[-1] for args in adb.runs if args and args[0] == "push"}
        self.assertEqual(pushes, names)
        script = "\n".join(root.commands)
        self.assertIn(f"chown 2000:2000 {install.REMOTE_STAGE}", script)
        self.assertIn(f"chmod 700 {install.REMOTE_STAGE}", script)
        self.assertIn("checkers-service.pid", script)
        self.assertIn("old_parent=", script)
        self.assertIn("kill $old_server", script)
        self.assertIn("cp /data/local/bin/server_a /data/local/bin/server_b.new", script)
        self.assertIn("mv -f /data/local/bin/server_b.new /data/local/bin/server_b", script)
        self.assertIn("wpa_supplicant-ap.new", script)
        self.assertIn("mv -f /data/local/lib/tater/wpa_supplicant-ap.new", script)
        self.assertIn("ln -sf server_a /data/local/bin/server", script)
        self.assertIn("/data/adb/modules/tater_checkers/service.sh", script)
        self.assertIn("/data/adb/modules/tater_checkers/post-fs-data.sh", script)
        self.assertIn("privacy.sh firewall", script)
        self.assertLess(script.index("privacy.sh firewall"), script.index("nohup /system/bin/sh"))
        self.assertIn("SpeechInteractionManager/.replace", script)
        self.assertIn("com.amazon.bishop/.replace", script)
        self.assertNotIn("pkill -x server", script)
        self.assertNotIn("pkill -f /data/local/bin/server", script)
        self.assertIn("nohup /system/bin/sh", script)

    def test_unlock_confirmation_is_exact(self):
        with mock.patch("builtins.print"), mock.patch("builtins.input", return_value="2.0.1"):
            with self.assertRaisesRegex(install.InstallError, "cancelled"):
                install.confirm_unlock(False)
        with mock.patch("builtins.print"), mock.patch("builtins.input", return_value="CHECKERS"):
            install.confirm_unlock(False)

    def test_lineage_requires_non_persistent_preview(self):
        with self.assertRaisesRegex(install.InstallError, "Fire-OS/Magisk-specific"):
            install.require_supported_install_mode("LineageOS 18.1", False, False)
        install.require_supported_install_mode("LineageOS 18.1", True, False)
        install.require_supported_install_mode("LineageOS 18.1", False, True)
        install.require_supported_install_mode("stock Fire OS 6", False, False)

    def test_uninstall_runs_reversible_module_cleanup_before_removal(self):
        adb = self.healthy()
        root = FakeRoot()
        with mock.patch("builtins.print"):
            install.uninstall(adb, root)
        script = "\n".join(root.commands)
        self.assertLess(script.index("uninstall.sh"), script.index("rm -rf /data/adb/modules/tater_checkers"))
        self.assertIn("pm enable --user 0 com.amazon.ds2.oobe.efd", script)
        self.assertIn(("uninstall", install.PACKAGE), adb.runs)


if __name__ == "__main__":
    unittest.main()
