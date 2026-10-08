import importlib.util
from pathlib import Path
import subprocess
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


MODULE = Path(__file__).with_name("build_rootfs.py")
SPEC = importlib.util.spec_from_file_location("rook_build_rootfs", MODULE)
build_rootfs = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build_rootfs)


class RookRootfsTests(unittest.TestCase):
    def test_both_linux_targets_build_and_package_native_onnx_runtime(self):
        repo = Path(__file__).resolve().parents[2]
        for target in ("checkers", "rook"):
            script = (repo / "linux" / target / "build_linux.sh").read_text()
            with self.subTest(target=target):
                self.assertIn("prepare_onnxruntime_linux_armv7.sh", script)
                self.assertIn("-tags server,onnxruntime", script)
                self.assertIn("ReleaseORTRuntimeSHA256=$ort_sha", script)
                self.assertIn("--onnx-runtime", script)
                self.assertIn("--hey-tater-oww-onnx", script)
                self.assertIn("TestLinuxARMv7ORTModels", script)

    def test_release_workflow_fetches_pinned_wake_models_before_build(self):
        workflow = (
            Path(__file__).resolve().parents[2] / ".github/workflows/release.yml"
        ).read_text()
        rook_job = workflow.split("\n  rook:\n", 1)[1].split("\n  publish:\n", 1)[0]

        fetch_at = rook_job.index("- name: Fetch pinned wake models")
        build_at = rook_job.index("- name: Build Rook boot and root filesystem")
        self.assertLess(fetch_at, build_at)
        self.assertIn(
            "device/build/microwakeword-testdata/hey_tater.tflite",
            rook_job,
        )
        self.assertIn(
            "device/build/microwakeword-testdata/stop.tflite",
            rook_job,
        )
        self.assertIn(
            "d3bf0d87c5c00ccfeda3cebba528c5d4012a5aaae51e61b7b01ae5af9008b4b9",
            rook_job,
        )
        self.assertIn(
            "020ef80d522cb09169a866f3aeeb58f2ad4045461937e78e8c806df29ff61eea",
            rook_job,
        )

    def test_workflows_build_and_cache_onnx_runtime_once(self):
        repo = Path(__file__).resolve().parents[2]
        release = (repo / ".github/workflows/release.yml").read_text()
        ci = (repo / ".github/workflows/ci.yml").read_text()
        cache_key = (
            "tater-onnxruntime-linux-armv7-v1-${{ hashFiles("
            "'device/prepare_onnxruntime_linux_armv7.sh', "
            "'device/onnxruntime/**') }}"
        )

        shared_job = release.split("\n  onnxruntime-armv7:\n", 1)[1].split(
            "\n  biscuit:\n", 1
        )[0]
        self.assertIn("actions/cache@v6", shared_job)
        self.assertIn(cache_key, shared_job)
        self.assertEqual(
            shared_job.count("device/prepare_onnxruntime_linux_armv7.sh"),
            2,
        )
        self.assertIn("name: tater-onnxruntime-linux-armv7", shared_job)

        for target, following in (("checkers", "rook"), ("rook", "publish")):
            job = release.split(f"\n  {target}:\n", 1)[1].split(
                f"\n  {following}:\n", 1
            )[0]
            with self.subTest(target=target):
                self.assertIn("needs: onnxruntime-armv7", job)
                self.assertIn("actions/download-artifact@v8", job)
                self.assertIn("name: tater-onnxruntime-linux-armv7", job)

        warm_job = ci.split("\n  onnxruntime-armv7-cache:\n", 1)[1].split(
            "\n  device-tests:\n", 1
        )[0]
        self.assertIn("github.event_name != 'pull_request'", warm_job)
        self.assertIn("actions/cache@v6", warm_job)
        self.assertIn(cache_key, warm_job)
        self.assertIn("device/prepare_onnxruntime_linux_armv7.sh", warm_job)

    def test_setup_ap_uses_primary_interface_without_changing_checkers_default(self):
        rook_script = Path(__file__).with_name("setup-ap.sh")
        shared_script = Path(__file__).resolve().parents[1] / "checkers/setup-ap.sh"
        subprocess.run(["sh", "-n", str(rook_script), str(shared_script)], check=True)
        rook = rook_script.read_text()
        shared = shared_script.read_text()
        self.assertIn("iw dev wlan0 set type __ap", rook)
        self.assertIn("TATER_SETUP_AP_IFACE=wlan0", rook)
        self.assertIn("TATER_SETUP_DHCP_BROADCAST=1", rook)
        self.assertNotIn("interface add ap0", rook)
        self.assertIn("AP_IFACE=${TATER_SETUP_AP_IFACE:-ap0}", shared)
        self.assertIn('ip addr add 192.168.4.1/24 dev "$AP_IFACE"', shared)
        self.assertIn('set -- --dhcp-broadcast', shared)
        self.assertIn('--interface="$AP_IFACE"', shared)

    def test_boot_keeps_uart_bluetooth_and_requires_camera(self):
        source = """#!/bin/sh
LOGDIR=/data/techo5-linux
mdev -s
t5_wifi_conf $LOGDIR/wpa_supplicant.conf
t5_bt_up "$BT_MODULE" /var/log
			reboot
			true
			reboot
		pid=$(pidof techo5 | cut -d' ' -f1)
		if [ -n "$pid" ]; then
			started=healthy
			if [ -n "$started" ]; then
				log "slot $s: daemon up for $TRIAL_SETTLE s, committing"
				slotctl commit >> /run/boot.log 2>&1
				exit 0
			fi
		fi
		if [ $now -ge $TRIAL_TIMEOUT ]; then
			log "trial timeout"
		fi
cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
log "boot script done"
"""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            boot = root / "etc/techo5/boot.sh"
            boot.parent.mkdir(parents=True)
            boot.write_text(source)
            build_rootfs.common.patch_boot(root, target="rook")
            patched = boot.read_text()

        self.assertIn('t5_bt_up "$BT_MODULE" /var/log', patched)
        self.assertIn("[ -s /run/tater-connected ]", patched)
        self.assertIn("pidof tater-show", patched)
        self.assertIn("pidof tater-camera", patched)
        self.assertNotIn("/dev/stpbt", patched)
        self.assertIn("tater-app commit", patched)

    def test_rejects_an_unpinned_base_before_extraction(self):
        with tempfile.TemporaryDirectory() as directory:
            candidate = Path(directory) / "wrong.tar.gz"
            candidate.write_bytes(b"not the signed Spot release")
            class Args:
                base_rootfs = candidate
            with self.assertRaises(SystemExit):
                build_rootfs.build(Args())

    def test_rootfs_bundles_camera_helper_and_service(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            base_tree = root / "base"
            board = base_tree / "etc/techo5/device.conf"
            board.parent.mkdir(parents=True)
            board.write_text("\n".join((
                "DATA_DEV=/dev/mmcblk0p13",
                "STORE_DEV=/dev/mmcblk0p11",
                "WIFI_MODULE=/vendor/lib/modules/amzn-bcmdhd.ko",
                "BT_UART=/dev/ttyMT1",
            )))
            base = root / "base.tar.gz"
            with tarfile.open(base, "w:gz") as archive:
                archive.add(base_tree, arcname=".")
            payload = root / "payload"
            payload.write_bytes(b"trial-binary")
            license_file = root / "LICENSE"
            license_file.write_text("MIT license\n")
            output = root / "rook.tar.gz"
            args = SimpleNamespace(
                base_rootfs=base, server=payload, show=payload, reboot=payload,
                camera=payload, mww_runtime=payload, onnx_runtime=payload, hey_tater_model=payload,
                hey_tater_manifest=payload, hey_tater_oww_onnx=payload,
                hey_tater_oww_metadata=payload, hey_tater_bundle=payload, stop_model=payload,
                stop_manifest=payload, oww_melspectrogram_onnx=payload,
                oww_embedding_onnx=payload, tinyalsa=payload,
                techo5_spot_license=license_file, techo5_license=license_file,
                apk_cache=root, version="v0.0.1", output=output,
            )
            with (
                patch.object(build_rootfs.common, "sha256", return_value=build_rootfs.BASE_SHA256),
                patch.object(build_rootfs.common, "SETUP_PACKAGES", []),
                patch.object(build_rootfs.common, "patch_boot"),
                patch.object(build_rootfs.common, "patch_slotctl"),
            ):
                build_rootfs.build(args)
            with tarfile.open(output, "r:gz") as archive:
                camera = archive.extractfile("./usr/local/bin/tater-camera").read()
                inittab = archive.extractfile("./etc/inittab").read().decode()
                runner = archive.extractfile("./usr/local/sbin/tater-camera-run").read().decode()
                license_text = archive.extractfile("./usr/share/licenses/tater-linux/TECHO5-LICENSE").read().decode()
                wake_bundle = archive.extractfile("./usr/share/tater/microwakeword/hey_tater.wake-bundle.json").read()
                onnx_runtime = archive.extractfile("./usr/share/tater/microwakeword/libonnxruntime.so").read()
                onnx_classifier = archive.extractfile("./usr/share/tater/microwakeword/hey_tater.oww.onnx").read()
            self.assertEqual(camera, b"trial-binary")
            self.assertIn("::respawn:/usr/local/sbin/tater-camera-run", inittab)
            self.assertIn("/usr/local/bin/tater-camera", runner)
            self.assertEqual(license_text, "MIT license\n")
            self.assertEqual(wake_bundle, b"trial-binary")
            self.assertEqual(onnx_runtime, b"trial-binary")
            self.assertEqual(onnx_classifier, b"trial-binary")


if __name__ == "__main__":
    unittest.main()
