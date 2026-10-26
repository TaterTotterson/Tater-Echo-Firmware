from __future__ import annotations

import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[2]
ETHERNET = ROOT / "linux" / "common" / "tater-usb-ethernet"


class USBEthernetControllerTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.data = self.root / "data"
        self.run = self.root / "run"
        self.sys_net = self.root / "sys" / "class" / "net"
        self.proc = self.root / "proc"
        self.bin = self.root / "bin"
        for path in (self.data, self.run, self.sys_net, self.proc, self.bin):
            path.mkdir(parents=True)
        self.mchange = self.root / "mchange"
        self.mchange.write_text("Active:1 host:0 force:2\n", encoding="utf-8")
        self.net_log = self.root / "net.log"
        self.net = self.root / "tater-net"
        self.net.write_text(
            '#!/bin/sh\nprintf "%s\\n" "${1:-start}" >> "$TATER_TEST_NET_LOG"\n',
            encoding="utf-8",
        )
        self.net.chmod(0o755)
        self._command("dmesg", "#!/bin/sh\nexit 0\n")
        self._command("route", "#!/bin/sh\nexit 1\n")
        self._command("ifconfig", "#!/bin/sh\nexit 0\n")
        self._command("udhcpc", "#!/bin/sh\nexit 0\n")
        self._command("pidof", "#!/bin/sh\nexit 1\n")
        self.techo5_lib = self.root / "techo5-lib.sh"
        self.techo5_lib.write_text(
            "t5_wifi_conf() { :; }\n"
            't5_wifi_up() { printf "fallback\\n" >> "$TATER_TEST_NET_LOG"; }\n',
            encoding="utf-8",
        )
        self.device_conf = self.root / "device.conf"
        self.device_conf.write_text("WIFI_MODULE=builtin\n", encoding="utf-8")

    def _command(self, name: str, contents: str) -> None:
        path = self.bin / name
        path.write_text(contents, encoding="utf-8")
        path.chmod(0o755)

    def _usb_interface(self, name: str = "eth0", driver: str = "cdc_ether") -> Path:
        interface = self.sys_net / name
        device = interface / "device"
        device.mkdir(parents=True)
        (device / "uevent").write_text(f"DRIVER={driver}\n", encoding="utf-8")
        (interface / "carrier").write_text("1\n", encoding="utf-8")
        return interface

    def _environment(self, **extra: str) -> dict[str, str]:
        environment = os.environ.copy()
        environment.update({
            "TATER_TARGET": "biscuit",
            "TATER_DATA_DIR": str(self.data),
            "TATER_RUN_DIR": str(self.run),
            "TATER_SYS_NET": str(self.sys_net),
            "TATER_PROC_DIR": str(self.proc),
            "TATER_MCHANGE": str(self.mchange),
            "TATER_NET_SCRIPT": str(self.net),
            "TATER_TECHO5_LIB": str(self.techo5_lib),
            "TATER_DEVICE_CONF": str(self.device_conf),
            "TATER_WIFI_CONF": str(self.root / "wpa_supplicant.conf"),
            "TATER_PATH": f"{self.bin}:/usr/bin:/bin:/usr/sbin:/sbin",
            "TATER_TEST_NET_LOG": str(self.net_log),
            "TATER_RESOLV_CONF": str(self.root / "resolv.conf"),
            "TATER_ETH_ENUM_TIMEOUT": "0",
            "TATER_ETH_DHCP_TIMEOUT": "1",
            "TATER_ETH_MONITOR_INTERVAL": "0.05",
            "TATER_ETH_FAILURE_LIMIT": "1",
        })
        environment.update(extra)
        return environment

    def test_missing_adapter_restores_serial_without_flapping_wifi(self) -> None:
        result = subprocess.run(
            ["sh", str(ETHERNET), "once"],
            env=self._environment(),
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )

        self.assertEqual(1, result.returncode)
        self.assertEqual("fallback no-adapter", (self.run / "tater-usb-ethernet.state").read_text().strip())
        self.assertEqual("device", self.mchange.read_text().strip())
        self.assertFalse(self.net_log.exists(), "Wi-Fi was restarted during a non-disruptive probe")

    def test_adapter_without_lease_restores_serial_and_keeps_wifi(self) -> None:
        self._usb_interface()
        self.mchange.write_text("Active:1 host:1 force:1\n", encoding="utf-8")
        result = subprocess.run(
            ["sh", str(ETHERNET), "once"],
            env=self._environment(),
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )

        self.assertEqual(1, result.returncode)
        self.assertEqual("fallback no-lease", (self.run / "tater-usb-ethernet.state").read_text().strip())
        self.assertEqual("device", self.mchange.read_text().strip())
        self.assertFalse(self.net_log.exists(), "failed Ethernet DHCP changed Wi-Fi")

    def test_supervisor_retries_hotplug_then_prefers_usable_ethernet(self) -> None:
        process = subprocess.Popen(
            ["sh", str(ETHERNET)],
            env=self._environment(
                TATER_ETH_ENUM_TIMEOUT="1",
                TATER_ETH_RETRY_INTERVAL="1",
            ),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        state = self.run / "tater-usb-ethernet.state"
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if state.exists() and state.read_text().startswith("waiting no-adapter"):
                break
            time.sleep(0.02)
        else:
            process.kill()
            stdout, stderr = process.communicate()
            self.fail(f"Ethernet supervisor never entered waiting state: {stdout=} {stderr=}")

        # An enumerated adapter means the controller is now in host mode.
        self.mchange.write_text("Active:1 host:1 force:1\n", encoding="utf-8")
        interface = self._usb_interface()
        self._install_usable_ethernet_commands()

        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if state.read_text().startswith("active eth0 192.168.1.9"):
                if self.net_log.exists() and self.net_log.read_text().splitlines() == ["stop"]:
                    break
            time.sleep(0.02)
        else:
            process.kill()
            stdout, stderr = process.communicate()
            self.fail(f"Hot-plugged Ethernet never became active: {stdout=} {stderr=}")

        (interface / "carrier").write_text("0\n", encoding="utf-8")
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if state.read_text().startswith("waiting link-lost"):
                if self.net_log.read_text().splitlines() == ["stop", "fallback"]:
                    break
            time.sleep(0.02)
        else:
            process.kill()
            stdout, stderr = process.communicate()
            self.fail(f"Wired loss did not restore Wi-Fi and enter waiting: {stdout=} {stderr=}")

        subprocess.run(
            ["sh", str(ETHERNET), "stop"],
            env=self._environment(),
            check=True,
            timeout=5,
        )
        stdout, stderr = process.communicate(timeout=5)
        self.assertEqual(0, process.returncode, f"{stdout=} {stderr=}")
        self.assertEqual(["stop", "fallback"], self.net_log.read_text().splitlines())

    def test_bounded_probe_restores_serial_without_dhcp_or_wifi_changes(self) -> None:
        self._usb_interface()
        result = subprocess.run(
            ["sh", str(ETHERNET), "probe"],
            env=self._environment(
                TATER_ETH_ENUM_TIMEOUT="1",
                TATER_ETH_PROBE_MAX_SECONDS="2",
            ),
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(
            "probe-complete eth0",
            (self.run / "tater-usb-ethernet.state").read_text().strip(),
        )
        self.assertEqual("device", self.mchange.read_text().strip())
        self.assertFalse(self.net_log.exists(), "bounded probe changed Wi-Fi")
        self.assertFalse((self.run / "tater-usb-ethernet.lease").exists(), "bounded probe ran DHCP")

    def test_bounded_probe_watchdog_restores_serial(self) -> None:
        result = subprocess.run(
            ["sh", str(ETHERNET), "probe"],
            env=self._environment(
                TATER_ETH_ENUM_TIMEOUT="30",
                TATER_ETH_PROBE_MAX_SECONDS="1",
            ),
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )

        self.assertEqual(1, result.returncode, result.stderr)
        self.assertEqual(
            "fallback probe-timeout",
            (self.run / "tater-usb-ethernet.state").read_text().strip(),
        )
        self.assertEqual("device", self.mchange.read_text().strip())
        self.assertFalse(self.net_log.exists(), "probe watchdog changed Wi-Fi")

    def test_unknown_mode_fails_without_changing_usb_or_wifi(self) -> None:
        result = subprocess.run(
            ["sh", str(ETHERNET), "typo"],
            env=self._environment(),
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )

        self.assertEqual(2, result.returncode, result.stderr)
        self.assertEqual(
            "unsupported bad-command",
            (self.run / "tater-usb-ethernet.state").read_text().strip(),
        )
        self.assertEqual("Active:1 host:0 force:2", self.mchange.read_text().strip())
        self.assertFalse(self.net_log.exists(), "unknown mode changed Wi-Fi")

    def _install_usable_ethernet_commands(self) -> None:
        self._command(
            "ifconfig",
            """#!/bin/sh
if [ "$#" -eq 1 ] && [ "$1" = eth0 ]; then
    echo 'eth0 Link encap:Ethernet  HWaddr 00:11:22:33:44:55'
    echo '          inet addr:192.168.1.9  Bcast:192.168.1.255  Mask:255.255.255.0'
fi
exit 0
""",
        )
        self._command(
            "route",
            """#!/bin/sh
if [ "${1:-}" = -n ]; then
    echo 'Kernel IP routing table'
    echo 'Destination Gateway Genmask Flags Metric Ref Use Iface'
    echo '0.0.0.0 192.168.1.1 0.0.0.0 UG 0 0 0 eth0'
    exit 0
fi
if [ "${1:-}" = add ]; then
    exit 0
fi
exit 1
""",
        )
        self._command(
            "udhcpc",
            """#!/bin/sh
printf '192.168.1.1\n1.1.1.1 8.8.8.8\n' > "$TATER_ETH_LEASE_FILE"
exit 0
""",
        )

    def _start_usable_ethernet(self) -> tuple[subprocess.Popen[str], Path, Path]:
        interface = self._usb_interface()
        self.mchange.write_text("Active:1 host:1 force:1\n", encoding="utf-8")
        self._install_usable_ethernet_commands()

        process = subprocess.Popen(
            ["sh", str(ETHERNET), "once"],
            env=self._environment(TATER_ETH_ENUM_TIMEOUT="1"),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        state = self.run / "tater-usb-ethernet.state"
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if state.exists() and state.read_text().startswith("active eth0 192.168.1.9"):
                if self.net_log.exists() and self.net_log.read_text().splitlines() == ["stop"]:
                    break
            time.sleep(0.02)
        else:
            process.kill()
            stdout, stderr = process.communicate()
            self.fail(f"Ethernet never became active: {stdout=} {stderr=}")

        return process, state, interface

    def test_usable_ethernet_stops_wifi_then_disable_restores_it(self) -> None:
        process, state, _ = self._start_usable_ethernet()

        stop = subprocess.run(
            ["sh", str(ETHERNET), "stop"],
            env=self._environment(),
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )
        self.assertEqual(0, stop.returncode, stop.stderr)
        stdout, stderr = process.communicate(timeout=5)
        self.assertEqual(0, process.returncode, f"{stdout=} {stderr=}")
        self.assertEqual("stopped", state.read_text().strip())
        self.assertEqual("device", self.mchange.read_text().strip())
        self.assertEqual(["stop", "fallback"], self.net_log.read_text().splitlines())

    def test_link_loss_restores_serial_and_wifi(self) -> None:
        process, state, interface = self._start_usable_ethernet()

        (interface / "carrier").write_text("0\n", encoding="utf-8")
        stdout, stderr = process.communicate(timeout=5)
        self.assertEqual(0, process.returncode, f"{stdout=} {stderr=}")
        self.assertEqual("fallback link-lost", state.read_text().strip())
        self.assertEqual("device", self.mchange.read_text().strip())
        self.assertEqual(["stop", "fallback"], self.net_log.read_text().splitlines())

    def test_passive_target_detects_usb_adapter_without_changing_role(self) -> None:
        self._usb_interface()
        original = self.mchange.read_text()
        result = subprocess.run(
            ["sh", str(ETHERNET), "probe"],
            env=self._environment(TATER_TARGET="checkers"),
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual("probe-complete eth0", (self.run / "tater-usb-ethernet.state").read_text().strip())
        self.assertEqual(original, self.mchange.read_text())
        self.assertFalse(self.net_log.exists(), "passive detection changed Wi-Fi")

    def test_passive_target_uses_detected_adapter_and_restores_wifi_on_loss(self) -> None:
        interface = self._usb_interface()
        self._install_usable_ethernet_commands()
        original = self.mchange.read_text()
        process = subprocess.Popen(
            ["sh", str(ETHERNET), "once"],
            env=self._environment(TATER_TARGET="checkers"),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        state = self.run / "tater-usb-ethernet.state"
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if state.exists() and state.read_text().startswith("active eth0 192.168.1.9"):
                break
            time.sleep(0.02)
        else:
            process.kill()
            stdout, stderr = process.communicate()
            self.fail(f"Passive target never used detected Ethernet: {stdout=} {stderr=}")

        (interface / "carrier").write_text("0\n", encoding="utf-8")
        stdout, stderr = process.communicate(timeout=5)
        self.assertEqual(0, process.returncode, f"{stdout=} {stderr=}")
        self.assertEqual("fallback link-lost", state.read_text().strip())
        self.assertEqual(["fallback"], self.net_log.read_text().splitlines())
        self.assertEqual(original, self.mchange.read_text())

    def test_gadget_rndis_interface_is_not_mistaken_for_usb_ethernet(self) -> None:
        self._usb_interface("usb0", "g_ether")
        result = subprocess.run(
            ["sh", str(ETHERNET), "probe"],
            env=self._environment(
                TATER_TARGET="checkers",
                TATER_ETH_ENUM_TIMEOUT="0",
                TATER_ETH_PROBE_MAX_SECONDS="2",
            ),
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )

        self.assertEqual(1, result.returncode, result.stderr)
        self.assertEqual("fallback no-adapter", (self.run / "tater-usb-ethernet.state").read_text().strip())
        self.assertEqual("Active:1 host:0 force:2", self.mchange.read_text().strip())


if __name__ == "__main__":
    unittest.main()
