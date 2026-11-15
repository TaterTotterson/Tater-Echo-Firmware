from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest


HELPER_PATH = Path(__file__).with_name("to_twrp.py")
SPEC = importlib.util.spec_from_file_location("tater_echo_to_twrp", HELPER_PATH)
assert SPEC is not None and SPEC.loader is not None
helper = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(helper)


class FakeConsoleLibrary:
    CONSOLE_DOT = ("18d1", "4ee7")
    CONSOLE_TECHO5 = ("1d6b", "0104")

    def __init__(self, ports=None, answers=None) -> None:
        self.ports = {
            self.CONSOLE_DOT: [("/dev/ttyACM0", "serial")],
            self.CONSOLE_TECHO5: [],
        } if ports is None else ports
        self.answers = list(answers or [])
        self.requested_ids: list[tuple[str, str]] = []
        self.commands: list[tuple[str, str, int]] = []

    def list_consoles(self, ids):
        self.requested_ids.append(ids)
        return self.ports.get(ids, [])

    def console_exchange(self, port: str, command: str, wait: int):
        self.commands.append((port, command, wait))
        return self.answers.pop(0)


class TaterEchoTwrpHelperTests(unittest.TestCase):
    def test_selects_one_console_from_either_usb_family(self) -> None:
        console = FakeConsoleLibrary()

        self.assertEqual("/dev/ttyACM0", helper.select_port(console, None))
        self.assertEqual([console.CONSOLE_DOT, console.CONSOLE_TECHO5], console.requested_ids)

    def test_deduplicates_overlapping_macos_discovery(self) -> None:
        same = [("/dev/cu.usbmodem123", "123")]
        console = FakeConsoleLibrary(ports={
            FakeConsoleLibrary.CONSOLE_DOT: same,
            FakeConsoleLibrary.CONSOLE_TECHO5: same,
        })

        self.assertEqual("/dev/cu.usbmodem123", helper.select_port(console, None))

    def test_requires_port_selection_when_multiple_are_connected(self) -> None:
        console = FakeConsoleLibrary(ports={
            FakeConsoleLibrary.CONSOLE_DOT: [("COM3", "one")],
            FakeConsoleLibrary.CONSOLE_TECHO5: [("COM4", "two")],
        })

        with self.assertRaisesRegex(helper.Fail, "more than one"):
            helper.select_port(console, None)

    def test_explicit_port_skips_discovery(self) -> None:
        console = FakeConsoleLibrary(ports={})

        self.assertEqual("COM9", helper.select_port(console, "COM9"))
        self.assertEqual([], console.requested_ids)

    def test_dot_restores_saved_twrp_before_rebooting(self) -> None:
        console = FakeConsoleLibrary(
            answers=["TATER_RECOVERY_DOT", "TATER_TWRP_REBOOTING"]
        )

        helper.reboot_to_twrp(console, "/dev/ttyACM0")

        self.assertEqual(2, len(console.commands))
        self.assertIn("/etc/tater-release", console.commands[0][1])
        self.assertIn("/usr/local/sbin/to-twrp --yes", console.commands[1][1])

    def test_screen_target_supports_old_and_retired_router_helpers(self) -> None:
        console = FakeConsoleLibrary(
            answers=["TATER_RECOVERY_SHOW_ROUTER", "TATER_TWRP_REBOOTING"]
        )

        helper.reboot_to_twrp(console, "/dev/ttyACM0")

        self.assertIn("tater-recovery-router install", console.commands[1][1])
        self.assertIn("/usr/local/sbin/tater-recovery-router twrp", console.commands[1][1])

    def test_pre_router_screen_release_keeps_legacy_twrp_route(self) -> None:
        console = FakeConsoleLibrary(
            answers=["TATER_RECOVERY_SHOW_LEGACY", "TATER_TWRP_REBOOTING"]
        )

        helper.reboot_to_twrp(console, "/dev/ttyACM0")

        self.assertIn("/usr/local/bin/rebootto recovery", console.commands[1][1])
        self.assertNotIn("tater-recovery-router twrp", console.commands[1][1])

    def test_refuses_an_unrecognized_console(self) -> None:
        console = FakeConsoleLibrary(answers=[""])

        with self.assertRaisesRegex(helper.Fail, "not a supported Tater Linux Echo"):
            helper.reboot_to_twrp(console, "/dev/ttyACM0")
        self.assertEqual(1, len(console.commands))


if __name__ == "__main__":
    unittest.main()
