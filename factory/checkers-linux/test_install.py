import importlib.util
import hashlib
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock


MODULE = Path(__file__).with_name("install.py")
SPEC = importlib.util.spec_from_file_location("checkers_linux_install", MODULE)
install = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(install)


class Completed:
    def __init__(self, output=b"", returncode=0):
        self.stdout = output
        self.stderr = b""
        self.returncode = returncode


class DownloadResponse(io.BytesIO):
    def __init__(self, data: bytes):
        super().__init__(data)
        self.headers = {"Content-Length": str(len(data))}


class InstallerTests(unittest.TestCase):
    def test_fresh_install_uses_hotspot_without_usb_pairing(self):
        args = SimpleNamespace(usb_pair=False, tater_url=None, pairing_code=None)
        with mock.patch.object(install, "load_module") as module, \
             mock.patch("builtins.input") as prompt, \
             mock.patch.object(install.getpass, "getpass") as code:
            install.finish_tater_provisioning(args, "SERIAL")
        module.assert_not_called()
        prompt.assert_not_called()
        code.assert_not_called()

    def test_partial_usb_pairing_parameters_are_rejected(self):
        args = SimpleNamespace(usb_pair=False, tater_url="http://tater.local:8501", pairing_code=None)
        with mock.patch.object(install, "load_module"):
            with self.assertRaisesRegex(install.InstallError, "pass both"):
                install.finish_tater_provisioning(args, "SERIAL")

    def test_fresh_usb_pairing_uses_provisioning_helper(self):
        args = SimpleNamespace(usb_pair=True, tater_url=None, pairing_code=None,
                               name="Kitchen", room="Kitchen")
        helper = SimpleNamespace(provision_console=mock.Mock())
        with mock.patch.object(install, "load_module", return_value=helper) as load, \
             mock.patch("builtins.input", return_value="http://tater.local:8501"), \
             mock.patch.object(install.getpass, "getpass", return_value="123 456"):
            install.finish_tater_provisioning(args, "SERIAL")
        load.assert_called_once_with("tater_checkers_provision", install.PROVISION)
        serial, tools, values = helper.provision_console.call_args.args
        self.assertEqual("SERIAL", serial)
        self.assertEqual(str(install.TECHO_TOOLS), tools)
        self.assertEqual(b"123456\n", values["token"])
        self.assertEqual("Kitchen", json.loads(values["native"])["room"])

    def test_selects_only_connected_recovery(self):
        listing = b"List of devices attached\nSERIAL\trecovery\nOFFLINE\toffline\n"
        with mock.patch.object(install, "run", return_value=Completed(listing)):
            self.assertEqual(("SERIAL", "recovery"), install.select_device("adb", None))

    def test_running_android_is_not_a_factory_install_target(self):
        listing = b"List of devices attached\nANDROID\tdevice\n"
        with mock.patch.object(install, "run", return_value=Completed(listing)):
            with self.assertRaisesRegex(install.InstallError, "device in TWRP"):
                install.select_device("adb", None)

    def test_multiple_devices_require_serial(self):
        listing = b"List of devices attached\nONE\trecovery\nTWO\trecovery\n"
        with mock.patch.object(install, "run", return_value=Completed(listing)):
            with self.assertRaisesRegex(install.InstallError, "use --serial"):
                install.select_device("adb", None)

    def test_manifest_requires_linux_generation(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "bundle-manifest.json"
            path.write_text(json.dumps({
                "target": "checkers", "base_os": "fireos-6", "files": {"x": {}},
            }))
            with mock.patch.object(install, "MANIFEST", path):
                with self.assertRaisesRegex(install.InstallError, "not a Checkers Tater Linux"):
                    install.load_manifest()

    def test_bundle_verification_rejects_size_change(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            (root / "payload").write_bytes(b"abc")
            manifest = {"files": {"payload": {"size": 4, "sha256": "bad"}}}
            with mock.patch.object(install, "ROOT", root):
                with self.assertRaisesRegex(install.InstallError, "wrong size"):
                    install.verify_bundle(manifest)

    def test_lineage_zip_requires_tested_digest(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "lineage.zip"
            path.write_bytes(b"wrong image")
            with self.assertRaisesRegex(install.InstallError, "SHA-256 does not match"):
                install.verify_lineage_zip(path)
            with mock.patch.object(install, "sha256", return_value=install.CHECKERS_LINEAGE_SHA256):
                install.verify_lineage_zip(path)

    def test_lineage_zip_must_exist(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(install.InstallError, "zip not found"):
                install.verify_lineage_zip(Path(directory) / "missing.zip")

    def test_downloads_and_reuses_verified_lineage_zip(self):
        image = b"test Checkers LineageOS image"
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            with mock.patch.object(install, "CHECKERS_LINEAGE_SHA256", hashlib.sha256(image).hexdigest()), \
                 mock.patch.object(install.urlrequest, "urlopen", return_value=DownloadResponse(image)) as fetch:
                downloaded = install.download_lineage_zip(work)
                self.assertEqual(work / install.CHECKERS_LINEAGE_NAME, downloaded)
                self.assertEqual(image, downloaded.read_bytes())
                self.assertEqual([], list(work.glob("*.partial")))
                self.assertEqual(downloaded, install.download_lineage_zip(work))
                fetch.assert_called_once()

    def test_bad_download_digest_leaves_no_cached_or_partial_image(self):
        image = b"wrong image"
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            with mock.patch.object(install.urlrequest, "urlopen", return_value=DownloadResponse(image)):
                with self.assertRaisesRegex(install.InstallError, "failed SHA-256"):
                    install.download_lineage_zip(work)
            self.assertEqual([], list(work.iterdir()))

    def test_invalid_cache_is_replaced_only_after_verified_download(self):
        image = b"correct replacement"
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            cached = work / install.CHECKERS_LINEAGE_NAME
            cached.write_bytes(b"stale image")
            with mock.patch.object(install, "CHECKERS_LINEAGE_SHA256", hashlib.sha256(image).hexdigest()), \
                 mock.patch.object(install.urlrequest, "urlopen", return_value=DownloadResponse(image)):
                self.assertEqual(cached, install.download_lineage_zip(work))
            self.assertEqual(image, cached.read_bytes())

    def test_download_size_limit_prevents_caching(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            with mock.patch.object(install, "CHECKERS_LINEAGE_MAX_BYTES", 3), \
                 mock.patch.object(install.urlrequest, "urlopen", return_value=DownloadResponse(b"too large")):
                with self.assertRaisesRegex(install.InstallError, "safety limit"):
                    install.download_lineage_zip(work)
            self.assertEqual([], list(work.iterdir()))

    def test_download_network_failure_keeps_device_install_unstarted(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            with mock.patch.object(install.urlrequest, "urlopen", side_effect=install.urlerror.URLError("offline")):
                with self.assertRaisesRegex(install.InstallError, "pass --lineage-zip PATH"):
                    install.download_lineage_zip(work)
            self.assertEqual([], list(work.iterdir()))

    def test_twrp_fetches_lineage_before_backup_and_flash(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            args = SimpleNamespace(
                verify_bundle=False, lineage_zip=None, adb="adb", serial=None,
                name=None, backups=root / "backups", work=root / "work",
            )
            image = root / install.CHECKERS_LINEAGE_NAME
            order = []
            with mock.patch.object(install, "parse_args", return_value=args), \
                 mock.patch.object(install, "load_manifest", return_value={"version": "v2.0.0"}), \
                 mock.patch.object(install, "verify_bundle"), \
                 mock.patch.object(install, "select_device", return_value=("SERIAL", "recovery")), \
                 mock.patch.object(install, "adb_text", return_value="checkers"), \
                 mock.patch.object(install, "download_lineage_zip", side_effect=lambda _: (order.append("download"), image)[1]), \
                 mock.patch.object(install, "run_platform_installer", side_effect=lambda *_: order.append("flash")), \
                 mock.patch.object(install, "finish_tater_provisioning"):
                self.assertEqual(0, install.main())
            self.assertEqual(["download", "flash"], order)
            self.assertEqual(image, args.lineage_zip)


if __name__ == "__main__":
    unittest.main()
