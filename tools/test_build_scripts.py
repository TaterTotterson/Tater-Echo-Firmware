from __future__ import annotations

import subprocess
from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]
ORT_BUILD_SCRIPT = ROOT / "device" / "prepare_onnxruntime_linux_armv7.sh"
DOT_BUILD_SCRIPT = ROOT / "linux" / "dot" / "build_linux.sh"
DOT_ROOTFS_BUILDER = ROOT / "linux" / "dot" / "build_rootfs.py"
RELEASE_WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"


class BuildScriptTests(unittest.TestCase):
    def test_linux_armv7_onnxruntime_uses_verified_release_seed(self) -> None:
        script = ORT_BUILD_SCRIPT.read_text(encoding="utf-8")
        self.assertIn("tater-echo-checkers-v2.2.0-onnxruntime.so", script)
        self.assertIn("a0954d59f1f99ae7d3d8823dadf8acf63f2044d8b7bca838edf2e454f9325941", script)
        self.assertIn("f4047359e0dbf2078fff0e88bfb806de3c2b8891a895ac0dffdc3dfcb8bb489b", script)
        self.assertIn("TATER_ORT_FORCE_SOURCE_BUILD", script)

    def test_release_reuses_only_validated_compatible_onnxruntime_caches(self) -> None:
        script = ORT_BUILD_SCRIPT.read_text(encoding="utf-8")
        workflow = RELEASE_WORKFLOW.read_text(encoding="utf-8")

        self.assertIn("version=1.19.2", script)
        self.assertIn("restore-keys: |\n            tater-onnxruntime-linux-armv7-v1-", workflow)
        self.assertIn("Prepare pinned Linux ARMv7 ONNX Runtime", workflow)
        self.assertIn("device/prepare_onnxruntime_linux_armv7.sh", workflow)
        self.assertIn("sha256sum device/onnxruntime/required_operators.config", workflow)

    def test_linux_shell_build_scripts_parse(self) -> None:
        for script in (
            ORT_BUILD_SCRIPT,
            DOT_BUILD_SCRIPT,
            ROOT / "linux/checkers/build_linux.sh",
            ROOT / "linux/rook/build_linux.sh",
            ROOT / "device/tools/build_tools.sh",
            ROOT / "linux/dot/setup-ap.sh",
            ROOT / "linux/dot/tater-net",
            ROOT / "linux/dot/tater-usb-ethernet",
            ROOT / "linux/dot/wifi-set",
        ):
            subprocess.run(["sh", "-n", str(script)], check=True, cwd=ROOT)

    def test_dot_setup_uses_mediatek_vendor_ap_interface(self) -> None:
        setup = (ROOT / "linux/dot/setup-ap.sh").read_text(encoding="utf-8")
        self.assertIn("printf A > /dev/wmtWifi", setup)
        self.assertIn("TATER_SETUP_AP_IFACE=ap0", setup)
        self.assertNotIn("iw dev wlan0 set type __ap", setup)
        shared = (ROOT / "linux/checkers/setup-ap.sh").read_text(encoding="utf-8")
        self.assertIn('"$DNSMASQ" --conf-file=', shared)
        self.assertNotIn("--conf-file=/dev/null", shared)

    def test_release_builds_both_dot_linux_targets(self) -> None:
        workflow = RELEASE_WORKFLOW.read_text(encoding="utf-8")
        dot_job = workflow.split("\n  biscuit:\n", 1)[1].split("\n  checkers:\n", 1)[0]
        self.assertIn("- target: biscuit", dot_job)
        self.assertIn("- target: radar", dot_job)
        self.assertIn("linux/dot/build_linux.sh", dot_job)
        self.assertIn("platforms.dot.rootfs", dot_job)
        self.assertIn("tater-echo-biscuit-kernel.zImage-dtb", dot_job)
        self.assertIn("tater-echo-radar-kernel.zImage-dtb", dot_job)
        self.assertIn("TATER_DOT_PLATFORM_SOURCE", dot_job)
        self.assertNotIn("Android compiler", dot_job)
        self.assertIn("staging/radar/firmware-manifest.json", workflow)

    def test_dot_rootfs_verification_uses_host_writable_tempfile(self) -> None:
        script = DOT_BUILD_SCRIPT.read_text(encoding="utf-8")
        self.assertIn('mktemp "${TMPDIR:-/tmp}/tater-$target-rootfs.XXXXXX"', script)
        self.assertIn("trap 'rm -f \"$listing\"' EXIT HUP INT TERM", script)
        self.assertNotIn("build/dot/$target-rootfs.list", script)

    def test_dot_network_controllers_keep_dhcp_clients_separate(self) -> None:
        wifi = (ROOT / "linux/dot/tater-net").read_text(encoding="utf-8")
        ethernet = (ROOT / "linux/dot/tater-usb-ethernet").read_text(encoding="utf-8")
        setter = (ROOT / "linux/dot/wifi-set").read_text(encoding="utf-8")
        builder = DOT_ROOTFS_BUILDER.read_text(encoding="utf-8")
        build_script = DOT_BUILD_SCRIPT.read_text(encoding="utf-8")

        self.assertNotIn("killall", wifi + ethernet + setter)
        self.assertIn("wifi-dhcp.pid", wifi)
        self.assertIn("eth-dhcp.pid", ethernet)
        self.assertIn("tater-udhcpc-wlan0.sh", wifi)
        self.assertIn("tater-udhcpc-ethernet.sh", ethernet)
        self.assertIn('set_state active "$iface" "$address"', ethernet)
        self.assertLess(ethernet.index('set_state active "$iface" "$address"'),
                        ethernet.index('"$NET" stop'))
        self.assertIn('root / "usr/local/sbin/tater-net"', builder)
        self.assertIn('root / "usr/local/sbin/tater-usb-ethernet"', builder)
        self.assertIn('./usr/local/sbin/tater-usb-ethernet', build_script)

    def test_dot_rootfs_probes_both_fireos_system_layouts(self) -> None:
        builder = DOT_ROOTFS_BUILDER.read_text(encoding="utf-8")
        self.assertIn("SYSTEM_A=/dev/mmcblk0p13", builder)
        self.assertIn("SYSTEM_B=/dev/mmcblk0p14", builder)
        self.assertIn("mount_vendor_system()", builder)
        self.assertIn("/android/system/vendor/firmware/WIFI_RAM_CODE_8163", builder)
        self.assertIn("/android/etc/firmware/WIFI_RAM_CODE_8163", builder)
        self.assertIn("/usr/local/sbin/tater-boot-ring &", builder)
        self.assertIn("orange=601800", builder)

    def test_dot_rootfs_includes_pinned_cpp_runtime_for_wake_engines(self) -> None:
        builder = DOT_ROOTFS_BUILDER.read_text(encoding="utf-8")
        build_script = DOT_BUILD_SCRIPT.read_text(encoding="utf-8")
        self.assertIn('"libstdc++-15.2.0-r5.apk"', builder)
        self.assertIn('"c57f103f92812e2c6c8fb81ffa61df3dcb08a49917a2284aff64bddeade6f310"', builder)
        self.assertIn('"usr/lib/libstdc++.so.6.0.34"', builder)
        self.assertIn('cxx_link.symlink_to(Path(member).name)', builder)
        self.assertIn('./usr/lib/libstdc++.so.6', build_script)
        self.assertIn('./usr/lib/libstdc++.so.6.0.34', build_script)

    def test_dot_rootfs_includes_show_style_clocked_alsa_capture(self) -> None:
        builder = DOT_ROOTFS_BUILDER.read_text(encoding="utf-8")
        build_script = DOT_BUILD_SCRIPT.read_text(encoding="utf-8")
        mic = (ROOT / "device/internal/bindings/mic/pcm_microphone.go").read_text(encoding="utf-8")
        self.assertIn('"alsa-utils-1.2.15.2-r1.apk"', builder)
        self.assertIn('"d297c4da007497f8a10f20f3eb82811ee705039a860198202d3ae5d77a94a596"', builder)
        self.assertIn('arecord.symlink_to("aplay")', builder)
        self.assertIn('./usr/bin/arecord', build_script)
        self.assertIn('linuxCapture: platform.Base() == platform.TaterLinux', mic)

    def test_release_builds_both_tater_show_targets(self) -> None:
        workflow = RELEASE_WORKFLOW.read_text(encoding="utf-8")
        show_job = workflow.split("\n  checkers:\n", 1)[1].split("\n  rook:\n", 1)[0]
        self.assertIn("- target: checkers", show_job)
        self.assertIn("- target: crown", show_job)
        self.assertIn("techo5-boot-crown-v1.0.1.img", show_job)
        self.assertIn("source.repository", show_job)
        self.assertIn("--platform /tmp/tater-echo-linux-show", show_job)
        self.assertIn("--target '${{ matrix.target }}'", show_job)
        self.assertIn("staging/crown/firmware-manifest.json", workflow)
        show_build = (ROOT / "linux/checkers/build_linux.sh").read_text(encoding="utf-8")
        self.assertIn("-X main.target=$TATER_TARGET", show_build)

    def test_release_uses_tater_echo_linux_for_every_platform_source(self) -> None:
        workflow = RELEASE_WORKFLOW.read_text(encoding="utf-8")
        self.assertEqual(workflow.count("get source.repository"), 5)
        self.assertNotIn("get platforms.techo5", workflow)
        self.assertNotIn("get platforms.dot.repository", workflow)
        self.assertNotIn("get platforms.rook.repository", workflow)
        self.assertIn("--show-source /tmp/tater-echo-linux-show", workflow)

    def test_athena_store_build_avoids_incomplete_runner_id_map(self) -> None:
        workflow = RELEASE_WORKFLOW.read_text(encoding="utf-8")
        athena_job = workflow.split("\n  athena:\n", 1)[1].split("\n  rook:\n", 1)[0]
        store_step = athena_job.split(
            "- name: Build and verify Athena system store", 1
        )[1].split("- name: Package and verify Athena firmware", 1)[0]
        self.assertIn(
            "tools/platform_lock.py builder-reference --platform linux/amd64",
            store_step,
        )
        self.assertIn("/opt/tater/athena-inputs", store_step)
        self.assertIn("--inner", store_step)
        self.assertNotIn("apt-get", store_step)
        self.assertNotIn("fetch-inputs.py", athena_job)
        self.assertNotIn("uidmap", store_step)

    def test_release_toolchain_is_digest_locked_and_offline(self) -> None:
        scripts = "\n".join(path.read_text(encoding="utf-8") for path in (
            ORT_BUILD_SCRIPT,
            DOT_BUILD_SCRIPT,
            ROOT / "linux/checkers/build_linux.sh",
            ROOT / "linux/rook/build_linux.sh",
            ROOT / "device/tools/build_tools.sh",
        ))
        self.assertNotIn("apk add", scripts)
        self.assertNotIn("GOTOOLCHAIN=auto", scripts)
        self.assertNotIn("TATER_ALPINE_ARMV7_IMAGE", scripts)
        self.assertGreaterEqual(scripts.count("builder-reference"), 5)
        workflow = RELEASE_WORKFLOW.read_text(encoding="utf-8")
        self.assertNotIn("ubuntu-latest", workflow)
        self.assertNotIn("apt-get", workflow)
        for action in re.findall(r"uses:\s+[^\s@]+@([^\s#]+)", workflow):
            self.assertRegex(action, r"^[0-9a-f]{40}$")

    def test_firmware_cross_builds_disable_unavailable_vcs_stamping(self) -> None:
        for path in (
            DOT_BUILD_SCRIPT,
            ROOT / "linux/checkers/build_linux.sh",
            ROOT / "linux/rook/build_linux.sh",
            ROOT / "device/tools/build_tools.sh",
            ROOT / "linux/checkers/build_camera_helper.sh",
            ROOT / "linux/rook/build_camera_helper.sh",
        ):
            script = path.read_text(encoding="utf-8")
            self.assertIn("GOFLAGS=-buildvcs=false", script, path)


if __name__ == "__main__":
    unittest.main()
