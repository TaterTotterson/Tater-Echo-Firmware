import hashlib
import io
import json
import struct
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

from tools import hardware_bundle as bundle
from tools import package_echo_release as release


class HardwareBundleTests(unittest.TestCase):
    def fixture(self, directory, target="checkers"):
        directory.mkdir()
        lock = {"schema": 1, "target": target, "bundle_id": "test", "helpers": {}}
        header = bytearray(64)
        header[:6], header[18:20] = b"\x7fELF\x01\x01", b"\x28\x00"
        for name in bundle.helper_names(target):
            data = bytes(header) + name.encode()
            (directory / name).write_bytes(data)
            lock["helpers"][name] = hashlib.sha256(data).hexdigest()
        (directory / bundle.MANIFEST).write_text(json.dumps({
            "schema": 1, "target": target, "bundle_id": "test", "files": lock["helpers"],
            "source_lock_sha256": bundle.lock_digest(lock),
        }))
        return lock

    def test_athena_is_gpu_only_and_rejects_android_codec_sidecars(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root / "public"
            lock = self.fixture(directory, "athena")
            with mock.patch.object(bundle, "load_lock", return_value=lock):
                bundle.verify(directory, target="athena")
                bundle.pack(directory, root / "athena.tgz", target="athena")
                bundle.unpack(io.BytesIO((root / "athena.tgz").read_bytes()), root / "unpacked", target="athena")
                self.assertEqual(set(path.name for path in (root / "unpacked").iterdir()), {"tater-hw-gpu",bundle.MANIFEST})
                (directory / "tater-hw-vdec").write_bytes(b"unsupported on Athena")
                with self.assertRaises(ValueError):bundle.verify(directory,target="athena")
        self.assertEqual(set(bundle.load_lock("athena")["helpers"]), {"tater-hw-gpu"})

    def test_exact_public_bundle_reproducible_and_unpacks(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root / "public"
            lock = self.fixture(directory)
            with mock.patch.object(bundle, "load_lock", return_value=lock):
                bundle.verify(directory)
                bundle.pack(directory, root / "a.tgz")
                bundle.pack(directory, root / "b.tgz")
                self.assertEqual((root / "a.tgz").read_bytes(), (root / "b.tgz").read_bytes())
                bundle.unpack(io.BytesIO((root / "a.tgz").read_bytes()), root / "unpacked")
                bundle.verify(root / "unpacked")

    def test_rejects_private_file_symlink_missing_or_modified_helper(self):
        for mutation in ("private", "symlink", "missing", "changed", "manifest"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary) / "public"
                lock = self.fixture(directory)
                if mutation == "private":
                    (directory / "libMtkOmxVenc.so").write_bytes(b"never publish")
                elif mutation == "symlink":
                    (directory / bundle.HELPERS[0]).unlink()
                    (directory / bundle.HELPERS[0]).symlink_to(directory / bundle.HELPERS[1])
                elif mutation == "missing":
                    (directory / bundle.HELPERS[0]).unlink()
                elif mutation == "changed":
                    (directory / bundle.HELPERS[0]).write_bytes(b"changed")
                else:
                    (directory / bundle.MANIFEST).write_text('{}')
                with self.assertRaises(ValueError):
                    bundle.verify(directory, lock=lock)

    def test_archive_refuses_traversal_duplicate_and_links_without_writing_output(self):
        for name, kind in (("../secret", tarfile.REGTYPE), ("tater-hw-gpu", tarfile.SYMTYPE), ("lib/vendor.so", tarfile.REGTYPE)):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                stream = io.BytesIO()
                with tarfile.open(fileobj=stream, mode="w:gz") as archive:
                    entry = tarfile.TarInfo(name)
                    entry.type, entry.size = kind, 1 if kind == tarfile.REGTYPE else 0
                    archive.addfile(entry, io.BytesIO(b"x") if entry.size else None)
                stream.seek(0)
                output = Path(temporary) / "public"
                with self.assertRaises(ValueError):
                    bundle.unpack(stream, output)
                self.assertFalse(output.exists())

    def test_source_drift_fails_before_running_research_builder(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary)
            lock = {"sources": {"scripts/build_tater_hardware.py": "0" * 64}}
            with mock.patch.object(bundle, "load_lock", return_value=lock), mock.patch.object(bundle.subprocess, "run") as run:
                with self.assertRaisesRegex(ValueError, "source does not match"):
                    bundle.build(source, source, source, source, source / "output")
                run.assert_not_called()

    def test_firmware_pins_match_compiled_helper_identities_and_other_targets_stay_disabled(self):
        lock = bundle.load_lock()
        for relative, name in (("device/internal/gpucompositor/compositor_linux.go", "tater-hw-gpu"),
                               ("device/internal/videostream/hardware_linux.go", "tater-hw-vdec"),
                               ("linux/checkers/camera-helper/h264.go", "tater-hw-venc")):
            self.assertIn(lock["helpers"][name], (bundle.ROOT / relative).read_text())
        workflow = (bundle.ROOT / ".github/workflows/release.yml").read_text()
        self.assertIn("if: matrix.target == 'checkers'", workflow)
        self.assertIn("tools/release_hardware_inputs.py fetch", workflow)
        self.assertNotIn("vars.TATER_CHECKERS_HARDWARE_BUNDLE_URL", workflow)

    def test_old_kernel_release_pin_is_refused(self):
        # A release approval must not make an older/different image acceptable.
        # Do not rely on the real lock still being awaiting owner acceptance.
        for target in ("checkers", "cronos", "crown", "rook", "athena"):
            platform = json.loads(json.dumps(release.PLATFORM_LOCK))
            hardware = (platform["platforms"]["show"][target + "_hardware"]
                        if target in ("checkers", "cronos", "crown")
                        else platform["platforms"][target]["hardware"])
            hardware["release_qualified"] = True
            key = "boot_sha256" if target in ("crown", "athena") else "kernel_image_sha256"
            hardware[key] = "0" * 64
            with self.subTest(target=target), \
                    mock.patch.object(release, "PLATFORM_LOCK", platform), \
                    self.assertRaisesRegex(SystemExit, "hardware release is blocked"):
                getattr(release, "require_" + target + "_hardware_platform")()

    def test_rook_requires_explicit_release_qualification(self):
        for qualified in (None, False, "true", True):
            with self.subTest(qualified=qualified):
                platform = json.loads(json.dumps(release.PLATFORM_LOCK))
                rook=platform["platforms"]["rook"]
                rook["kernel"]["sha256"]=release.QUALIFIED_ROOK_KERNEL_SHA256
                rook["hardware"] = {"release_qualified": qualified,
                                    "source_commit":rook["commit"],
                                    "kernel_image_sha256":release.QUALIFIED_ROOK_KERNEL_SHA256}
                with mock.patch.object(release, "PLATFORM_LOCK", platform):
                    if qualified is True:
                        release.require_rook_hardware_platform()
                    else:
                        with self.assertRaisesRegex(SystemExit, "Rook hardware release is blocked"):
                            release.require_rook_hardware_platform()

    def test_crown_requires_explicit_review_and_complete_matching_pins(self):
        for mutation in (None, "unreviewed", "string_review", "source", "kernel", "boot"):
            with self.subTest(mutation=mutation):
                lock = json.loads(json.dumps(release.PLATFORM_LOCK))
                show = lock["platforms"]["show"]
                hardware = show["crown_hardware"] = {
                    "release_qualified": True,
                    "source_commit": show["checkers_commit"],
                    "kernel_image_sha256": "1" * 64,
                    "boot_sha256": show["targets"]["crown"]["boot"]["sha256"],
                }
                if mutation == "unreviewed":
                    hardware["release_qualified"] = False
                elif mutation == "string_review":
                    hardware["release_qualified"] = "true"
                elif mutation == "source":
                    hardware["source_commit"] = "0" * 40
                elif mutation == "kernel":
                    hardware["kernel_image_sha256"] = "unknown"
                elif mutation == "boot":
                    hardware["boot_sha256"] = "0" * 64
                with mock.patch.object(release, "PLATFORM_LOCK", lock):
                    if mutation is None:
                        release.require_crown_hardware_platform()
                    else:
                        with self.assertRaises(SystemExit):
                            release.require_crown_hardware_platform()

    def test_each_show_target_pins_its_own_compiled_helpers(self):
        for target in ("checkers", "cronos", "crown"):
            lock = bundle.load_lock(target)
            self.assertEqual(lock["target"], target)
            for relative, name in (("device/internal/gpucompositor/compositor_linux.go", "tater-hw-gpu"),
                                   ("device/internal/videostream/hardware_linux.go", "tater-hw-vdec"),
                                   ("linux/checkers/camera-helper/h264.go", "tater-hw-venc")):
                self.assertIn(lock["helpers"][name], (bundle.ROOT / relative).read_text())
        self.assertNotEqual(bundle.load_lock("crown")["helpers"], bundle.load_lock("cronos")["helpers"])

    def test_boot_kernel_payload_is_verified_not_just_metadata(self):
        with tempfile.TemporaryDirectory() as temporary:
            boot = Path(temporary) / "boot.img"
            kernel = b"qualified kernel fixture"
            header = bytearray(2048)
            header[:8] = b"ANDROID!"
            struct.pack_into("<I", header, 8, len(kernel))
            struct.pack_into("<I", header, 36, 2048)
            boot.write_bytes(bytes(header) + kernel)
            with mock.patch.object(release, "QUALIFIED_CHECKERS_KERNEL_SHA256", hashlib.sha256(kernel).hexdigest()):
                release.verify_checkers_hardware_boot(boot)
                boot.write_bytes(bytes(header) + b"wrong kernel")
                with self.assertRaisesRegex(SystemExit, "qualified hardware kernel"):
                    release.verify_checkers_hardware_boot(boot)

    def test_complete_reviewed_kernel_source_and_boot_pins_are_required(self):
        lock = json.loads(json.dumps(release.PLATFORM_LOCK))
        show = lock["platforms"]["show"]
        show["checkers_hardware"] = {
            "release_qualified": True,
            "source_commit": show["checkers_commit"],
            "kernel_image_sha256": release.QUALIFIED_CHECKERS_KERNEL_SHA256,
            "boot_sha256": show["targets"]["checkers"]["boot"]["sha256"],
        }
        with mock.patch.object(release, "PLATFORM_LOCK", lock):
            release.require_checkers_hardware_platform()
            show["checkers_hardware"]["source_commit"] = "0" * 40
            with self.assertRaises(SystemExit):
                release.require_checkers_hardware_platform()

    def test_input_pins_do_not_waive_physical_qualification(self):
        for target in ('checkers', 'cronos'):
            lock = json.loads(json.dumps(release.PLATFORM_LOCK))
            show = lock['platforms']['show']
            record = show[target+'_hardware'] = {
                'release_qualified': False,
                'source_commit': show['checkers_commit'],
                'kernel_image_sha256': getattr(release, 'QUALIFIED_'+target.upper()+'_KERNEL_SHA256'),
                'boot_sha256': show['targets'][target]['boot']['sha256'],
            }
            with mock.patch.object(release, 'PLATFORM_LOCK', lock):
                with self.assertRaises(SystemExit): getattr(release, 'require_'+target+'_hardware_platform')()
                record['release_qualified'] = True
                getattr(release, 'require_'+target+'_hardware_platform')()

    def test_rootfs_helpers_are_the_exact_ota_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root / "public"
            lock = self.fixture(directory)
            archive_path = root / "rootfs.tgz"
            with tarfile.open(archive_path, "w:gz") as archive:
                for path in directory.iterdir():
                    archive.add(path, arcname="./usr/local/bin/" + path.name)
            with mock.patch.object(bundle, "load_lock", return_value=lock):
                payloads = release.hardware_files_from_rootfs(archive_path)
            self.assertEqual(set(payloads), {*bundle.HELPERS, bundle.MANIFEST})
            for name, data in payloads.items():
                self.assertEqual((directory / name).read_bytes(), data)


if __name__ == "__main__":
    unittest.main()
