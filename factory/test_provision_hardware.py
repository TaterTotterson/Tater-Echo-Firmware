import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

from factory import provision_hardware as provision


class ProvisionHardwareTests(unittest.TestCase):
    def fixture(self, root):
        directory = root / "private"
        identities = {}
        for runtime in provision.RUNTIMES:
            (directory / runtime / "lib").mkdir(parents=True)
            (directory / runtime / "linker").write_bytes(b"linker")
            (directory / runtime / "lib/libtest.so").write_bytes(b"library")
            digest = hashlib.sha256()
            for name in ("linker", "lib/libtest.so"):
                value = provision.usb.sha256(directory / runtime / name)
                digest.update(name.encode() + b"\0" + value.encode() + b"\n")
            identities[runtime] = (digest.hexdigest(), 2, 13)
        (directory / "tater-venc").mkdir()
        (directory / "tater-venc/libMtkOmxVenc.so").write_bytes(b"encoder")
        component = provision.usb.sha256(directory / "tater-venc/libMtkOmxVenc.so")
        for runtime, identity in identities.items():
            (directory / runtime / "runtime.json").write_text(json.dumps({
                "schema": 2, "runtime_id": "checkers-mt8163-mali-pbuffer-1" if runtime == "tater-gpu" else "checkers-mt8163-vdec-stock-1",
                "tree_sha256": identity[0], "file_count": identity[1], "runtime_bytes": identity[2],
            }))
        (directory / "tater-venc/runtime.json").write_text(json.dumps({
            "schema": 2, "runtime_id": "checkers-mt8163-venc-stock-1", "component_sha256": component,
            "base_tree_sha256": identities["tater-vdec"][0],
        }))
        inventory = {path.relative_to(directory).as_posix(): provision.usb.sha256(path)
                     for path in directory.rglob("*") if path.is_file()}
        (directory / "private-runtime.json").write_text(json.dumps({"schema": 1, "target": "checkers", "private": True, "files": inventory}))
        return directory, identities, component

    def test_verified_library_only_set_and_private_transfer(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory, identities, component = self.fixture(root)
            with mock.patch.object(provision, "RUNTIMES", identities), mock.patch.object(provision, "COMPONENT", component):
                inventory = provision.verify(directory)
                output = root / "private.tar"
                provision.archive_runtime(directory, inventory, output)
                self.assertEqual(output.stat().st_mode & 0o777, 0o600)
                with tarfile.open(output) as archive:
                    self.assertEqual(set(archive.getnames()), {*inventory, "SHA256SUMS", "provision.sh"})
                    self.assertFalse(any("tater-hw-" in name for name in archive.getnames()))

    def test_corrupt_extra_and_symlink_inputs_refused(self):
        for mutation in ("corrupt", "extra", "symlink"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                directory, identities, component = self.fixture(Path(temporary))
                path = directory / "tater-gpu/lib/libtest.so"
                if mutation == "corrupt":
                    path.write_bytes(b"broken")
                elif mutation == "extra":
                    (directory / "tater-gpu/device_token").write_bytes(b"never transfer")
                else:
                    path.unlink()
                    path.symlink_to(directory / "tater-gpu/linker")
                with mock.patch.object(provision, "RUNTIMES", identities), mock.patch.object(provision, "COMPONENT", component), self.assertRaises(ValueError):
                    provision.verify(directory)

    def test_wrong_device_is_refused_before_transfer(self):
        with mock.patch.object(provision, "verify", return_value={}), mock.patch.object(provision.usb, "exchange", return_value="Tater Linux Crown") as exchange, mock.patch.object(provision.usb, "upload_rootfs") as upload:
            with self.assertRaisesRegex(ValueError, "qualified Checkers"):
                provision.install(Path("private"), object(), "test-port")
            upload.assert_not_called()
            self.assertIn("4.9.337-g8d928c5176cc", exchange.call_args.args[2])

    def test_missing_or_ambiguous_normal_console_never_suggests_recovery(self):
        for ports in ([], ["one", "two"]):
            with mock.patch.object(provision.usb, "available_ports", return_value=ports):
                with self.assertRaises(ValueError) as error:
                    provision.select_normal_port(object(), None)
                self.assertNotIn("enter recovery", str(error.exception).lower())

    def test_usb_command_stays_bounded_for_large_inventory(self):
        with tempfile.TemporaryDirectory() as temporary:
            inventory = {f"tater-gpu/lib/library{index}.so": "0" * 64 for index in range(200)}
            answers = ["TATER_CHECKERS_HARDWARE_READY", "999999999", "TATER_HARDWARE_PROVISIONED"]
            with mock.patch.object(provision, "verify", return_value=inventory), mock.patch.object(provision, "archive_runtime", side_effect=lambda directory, files, output, **options: output.write_bytes(b"private")), mock.patch.object(provision.usb, "exchange", side_effect=answers) as exchange, mock.patch.object(provision.usb, "upload_rootfs"):
                provision.install(Path(temporary), object(), "test-port")
                self.assertLess(len(exchange.call_args.args[2]), 2048)
                self.assertIn("tar -xOf", exchange.call_args.args[2])

    def test_cronos_probe_is_separate_and_validates_product_before_ready(self):
        body=provision.target_probe_body("cronos")
        self.assertIn("'#2 '",body)
        self.assertIn("androidboot.product=cronos",body)
        self.assertLess(body.index("androidboot.product=cronos"),body.index("printf"))
        self.assertNotIn("checkers",body)
        command=provision.install_command({},target="cronos")
        subprocess.run(["sh","-n"],input=command,text=True,check=True)
        with self.assertRaises(ValueError): provision.target_probe_body("crown")

    def test_installer_shell_validates_before_any_destination_move(self):
        inventory = {"tater-gpu/lib/a.so": "a" * 64, "tater-vdec/lib/b.so": "b" * 64,
                     "tater-venc/libMtkOmxVenc.so": provision.COMPONENT}
        command = provision.install_command(inventory)
        subprocess.run(["sh", "-n"], input=command, text=True, check=True)
        self.assertLess(command.index("b" * 64), command.index('mv -Tn "$stage/tater-gpu"'))
        self.assertLess(command.index(provision.COMPONENT), command.index('mv -Tn "$stage/tater-gpu"'))
        self.assertIn('if [ ! -e /data/tater-gpu ]', command)
        for forbidden in ("dd ", "flash", "slotctl", "reboot", "rm -rf", "native.json", "device_token"):
            self.assertNotIn(forbidden, command)

    def test_rook_probe_uses_the_camera_clock_kernel_identity(self):
        body=provision.target_probe_body('rook')
        self.assertIn("'#2 '",body)
        self.assertIn('4.9.337-g4174e0b4d0e2',body)
        self.assertIn('480,1440',body)
        self.assertNotIn("'#1 '",body)

    @unittest.skipUnless(sys.platform == "linux", "exercise the actual BusyBox/GNU mv -Tn contract on Linux")
    def test_real_installer_transaction_is_idempotent_and_refuses_mismatched_existing_libraries(self):
        for mismatch in (False, True):
            with self.subTest(mismatch=mismatch), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                directory, identities, component = self.fixture(root)
                remote = root / "data/tater-hardware-provision"
                remote.mkdir(parents=True)
                with mock.patch.object(provision, "RUNTIMES", identities), mock.patch.object(provision, "COMPONENT", component), mock.patch.object(provision, "PROBE_BODY", "set -eu\numask 077\n"):
                    inventory = provision.verify(directory)
                    script = provision.install_command(inventory).replace("/data/", str(root / "data") + "/")
                    if mismatch:
                        target = root / "data/tater-gpu/lib"
                        target.mkdir(parents=True)
                        (target / "libtest.so").write_bytes(b"do not overwrite")
                    for attempt in range(1 if mismatch else 2):
                        provision.archive_runtime(directory, inventory, remote / "private-runtime.tar")
                        result = subprocess.run(["sh", "-c", script], text=True, capture_output=True)
                        if mismatch:
                            self.assertNotEqual(result.returncode, 0)
                            self.assertEqual(b"do not overwrite", (root / "data/tater-gpu/lib/libtest.so").read_bytes())
                            self.assertFalse((root / "data/tater-vdec").exists())
                            self.assertFalse((root / "data/tater-venc").exists())
                        else:
                            self.assertEqual(result.returncode, 0, result.stderr)
                            self.assertIn("TATER_HARDWARE_PROVISIONED", result.stdout)
                            self.assertEqual(b"library", (root / "data/tater-gpu/lib/libtest.so").read_bytes())
                            inode = (root / "data/tater-gpu/lib/libtest.so").stat().st_ino
                            if attempt == 0:
                                original_inode = inode
                            else:
                                self.assertEqual(original_inode, inode)
                    # Make only this test's generated directories removable by
                    # the non-root CI user; production runtimes remain read-only.
                    for path in (root / "data").rglob("*"):
                        if path.is_dir():
                            path.chmod(0o755)


if __name__ == "__main__":
    unittest.main()
