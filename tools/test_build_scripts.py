from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path
import re
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
ORT_BUILD_SCRIPT = ROOT / "device" / "prepare_onnxruntime_linux_armv7.sh"
DOT_BUILD_SCRIPT = ROOT / "linux" / "dot" / "build_linux.sh"
DOT_ROOTFS_BUILDER = ROOT / "linux" / "dot" / "build_rootfs.py"
RELEASE_WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"
USB_INVENTORY = ROOT / "linux" / "common" / "tater-usb-inventory"


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
            ROOT / "linux/common/tater-usb-ethernet",
            ROOT / "linux/common/tater-sshd",
            ROOT / "linux/common/tater-ssh-authorize",
            USB_INVENTORY,
            ROOT / "linux/dot/wifi-set",
        ):
            subprocess.run(["sh", "-n", str(script)], check=True, cwd=ROOT)

    def test_usb_inventory_is_read_only_cross_target_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            proc = root / "proc"
            sys = root / "sys"
            etc = root / "etc"
            boot = root / "boot"
            bin_dir = root / "bin"
            for path in (proc, sys, etc, boot, bin_dir):
                path.mkdir(parents=True)

            (etc / "hostname").write_text("tater-rook\n")
            (etc / "tater-release").write_text("Tater Linux Rook test\n")
            (boot / "config-test").write_text(
                "CONFIG_USB_MTK_HDRC=y\nCONFIG_USB_USBNET=y\nCONFIG_USB_NET_CDCETHER=y\n"
            )
            (proc / "mchange").write_text("Active:1 host:0 force:2\n")
            (proc / "modules").write_text("cdc_ether 123 0 - Live 0x0\n")
            role = sys / "class" / "usb_role" / "usb0" / "role"
            role.parent.mkdir(parents=True)
            role.write_text("device\n")
            uevent = sys / "bus" / "usb" / "devices" / "1-1" / "uevent"
            uevent.parent.mkdir(parents=True)
            uevent.write_text(
                "DRIVER=cdc_ether\nPRODUCT=bda/8152/2000\nTYPE=0/0/0\nSERIAL=private-unit-id\n"
            )
            interface = sys / "class" / "net" / "eth0"
            interface.mkdir(parents=True)
            (interface / "operstate").write_text("up\n")
            (interface / "carrier").write_text("1\n")

            uname = bin_dir / "uname"
            uname.write_text(
                "#!/bin/sh\n[ \"${1:-}\" = -r ] && { echo test; exit; }\necho 'Linux tater-rook test'\n"
            )
            uname.chmod(0o755)
            dmesg = bin_dir / "dmesg"
            dmesg.write_text(
                "#!/bin/sh\necho 'usb SerialNumber=private-unit-id'\necho 'musb host ready'\n"
            )
            dmesg.chmod(0o755)

            environment = os.environ.copy()
            environment.update({
                "TATER_PROC_ROOT": str(proc),
                "TATER_SYS_ROOT": str(sys),
                "TATER_ETC_ROOT": str(etc),
                "TATER_BOOT_ROOT": str(boot),
                "TATER_PATH": f"{bin_dir}:/usr/bin:/bin:/usr/sbin:/sbin",
            })
            result = subprocess.run(
                ["sh", str(USB_INVENTORY), "--include-logs"],
                cwd=ROOT,
                env=environment,
                capture_output=True,
                text=True,
                check=False,
            )

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("read_only=true", result.stdout)
        self.assertIn("target=tater-rook", result.stdout)
        self.assertIn("CONFIG_USB_NET_CDCETHER=y", result.stdout)
        self.assertIn("/class/usb_role/usb0/role=device", result.stdout)
        self.assertIn("DRIVER=cdc_ether", result.stdout)
        self.assertIn("interface=eth0 operstate=up carrier=1", result.stdout)
        self.assertIn("musb host ready", result.stdout)
        self.assertNotIn("private-unit-id", result.stdout)

    def test_every_linux_rootfs_installs_usb_inventory(self) -> None:
        for builder in (
            DOT_ROOTFS_BUILDER,
            ROOT / "linux/checkers/build_rootfs.py",
            ROOT / "linux/rook/build_rootfs.py",
        ):
            self.assertIn("tater-usb-inventory", builder.read_text(encoding="utf-8"), builder)

    def test_every_linux_rootfs_installs_and_supervises_automatic_usb_ethernet(self) -> None:
        for builder in (
            DOT_ROOTFS_BUILDER,
            ROOT / "linux/checkers/build_rootfs.py",
            ROOT / "linux/rook/build_rootfs.py",
        ):
            contents = builder.read_text(encoding="utf-8")
            self.assertIn("tater-usb-ethernet", contents, builder)
        self.assertIn("::respawn:/usr/local/sbin/tater-usb-ethernet", DOT_ROOTFS_BUILDER.read_text())
        self.assertIn(
            "::respawn:/usr/local/sbin/tater-usb-ethernet",
            (ROOT / "linux/checkers/build_rootfs.py").read_text(),
        )
        self.assertIn("common.INITTAB", (ROOT / "linux/rook/build_rootfs.py").read_text())
        for script in (
            DOT_BUILD_SCRIPT,
            ROOT / "linux/checkers/build_linux.sh",
            ROOT / "linux/rook/build_linux.sh",
        ):
            contents = script.read_text(encoding="utf-8")
            self.assertIn("./usr/local/sbin/tater-usb-ethernet", contents, script)
            self.assertIn("./usr/local/sbin/tater-usb-inventory", contents, script)

    def test_every_linux_rootfs_installs_managed_ssh(self) -> None:
        dot = DOT_ROOTFS_BUILDER.read_text(encoding="utf-8")
        checkers = (ROOT / "linux/checkers/build_rootfs.py").read_text(encoding="utf-8")
        rook = (ROOT / "linux/rook/build_rootfs.py").read_text(encoding="utf-8")
        for contents in (dot, checkers, rook):
            self.assertIn("tater-sshd", contents)
            self.assertIn("tater-ssh-authorize", contents)
            self.assertIn("ensure_tater_ssh_group", contents)
        self.assertIn("::respawn:/usr/local/sbin/tater-sshd", dot)
        self.assertIn("::respawn:/usr/local/sbin/tater-sshd", checkers)
        self.assertIn("common.INITTAB", rook)
        for script in (
            DOT_BUILD_SCRIPT,
            ROOT / "linux/checkers/build_linux.sh",
            ROOT / "linux/rook/build_linux.sh",
        ):
            contents = script.read_text(encoding="utf-8")
            for path in (
                "./usr/local/sbin/tater-sshd",
                "./usr/local/sbin/tater-ssh-authorize",
                "./usr/sbin/dropbear",
                "./usr/bin/dropbearkey",
            ):
                self.assertIn(path, contents, script)
        supervisor = (ROOT / "linux/common/tater-sshd").read_text(encoding="utf-8")
        self.assertIn("-F -E -j -k", supervisor)
        self.assertNotIn("-F -E -s -g -j -k", supervisor)
        self.assertIn("-G tater-ssh", supervisor)
        self.assertIn('-D "$SSH_DIR"', supervisor)

    def test_usb_ethernet_catalog_covers_every_firmware_target(self) -> None:
        targets = json.loads((ROOT / "targets/targets.json").read_text(encoding="utf-8"))["targets"]
        capabilities = json.loads(
            (ROOT / "device/internal/usbethernet/capabilities.json").read_text(encoding="utf-8")
        )
        self.assertEqual(1, capabilities["schema"])
        self.assertEqual(set(targets), set(capabilities["targets"]))
        self.assertEqual("qualified", capabilities["targets"]["biscuit"]["status"])
        for capability in capabilities["targets"].values():
            self.assertTrue(capability["power"])
            self.assertTrue(capability["detection"])
        for target in set(targets) - {"biscuit"}:
            self.assertNotEqual("qualified", capabilities["targets"][target]["status"])

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
        ethernet = (ROOT / "linux/common/tater-usb-ethernet").read_text(encoding="utf-8")
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
                        ethernet.index('wifi_stop ||'))
        self.assertIn('root / "usr/local/sbin/tater-net"', builder)
        self.assertIn('root / "usr/local/sbin/tater-usb-ethernet"', builder)
        self.assertIn('./usr/local/sbin/tater-usb-ethernet', build_script)
        self.assertNotIn("net-mode", ethernet)
        self.assertIn("automatic USB Ethernet supervisor", ethernet)

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
        self.assertIn("tater-echo-crown-baseline-boot.img", show_job)
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

    def test_non_dot_release_inputs_are_tater_hosted(self) -> None:
        lock = json.loads((ROOT / "platforms/tater-echo-linux-lock.json").read_text(encoding="utf-8"))
        release = "satellite-baselines-v1.0.0"
        for platform in ("show", "athena", "rook"):
            self.assertEqual(release, lock["platforms"][platform]["custody_release"])

        urls: list[str] = []
        def collect(node: object) -> None:
            if isinstance(node, dict):
                if isinstance(node.get("url"), str):
                    urls.append(node["url"])
                for value in node.values():
                    collect(value)
            elif isinstance(node, list):
                for value in node:
                    collect(value)

        collect(lock["platforms"]["show"]["rootfs"])
        collect(lock["platforms"]["show"]["targets"])
        collect(lock["platforms"]["athena"]["rootfs"])
        collect(lock["platforms"]["athena"]["boot"])
        for role in ("rootfs", "kernel", "rescue"):
            collect(lock["platforms"]["rook"][role])
        custody = "https://github.com/TaterTotterson/Tater-Echo-Linux/releases/download/"
        self.assertTrue(urls)
        self.assertTrue(all(url.startswith(custody) for url in urls))

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
        self.assertIn("-f device/build/tater-rescue-fbprobe", store_step)
        self.assertIn("-l /tater-platform/logo/tater-boot-315.png", store_step)
        self.assertIn("/tmp/tater-echo-linux-show/platforms/show", athena_job)
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
