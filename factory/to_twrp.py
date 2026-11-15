#!/usr/bin/env python3
"""Reboot a USB-connected Tater Linux Echo satellite into TWRP."""

from __future__ import annotations

import argparse
import importlib.util
from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parent
TOOL_PATHS = (ROOT / "tools", ROOT / "tools/techo5")

PROBE_COMMAND = (
    "if grep -q 'Tater Linux' /etc/tater-release 2>/dev/null; then "
    "if [ -x /usr/local/sbin/to-twrp ]; then echo TATER_RECOVERY_DOT; "
    "elif [ -x /usr/local/sbin/tater-recovery-router ]; then echo TATER_RECOVERY_SHOW_ROUTER; "
    "elif [ -x /usr/local/bin/rebootto ]; then echo TATER_RECOVERY_SHOW_LEGACY; fi; fi"
)
DOT_REBOOT_COMMAND = (
    "(/bin/sleep 1; /usr/local/sbin/to-twrp --yes) "
    ">/tmp/to-twrp.log 2>&1 & echo TATER_TWRP_REBOOTING"
)
SHOW_ROUTER_REBOOT_COMMAND = (
    # New helpers treat 'install' as redirect cleanup. Older development
    # helpers retain their one-shot bypass through 'twrp'; support both.
    "if /usr/local/sbin/tater-recovery-router install >/tmp/to-twrp.log 2>&1; then "
    "(/bin/sleep 1; /usr/local/sbin/tater-recovery-router twrp) "
    ">>/tmp/to-twrp.log 2>&1 & echo TATER_TWRP_REBOOTING; fi"
)
SHOW_LEGACY_REBOOT_COMMAND = (
    "(/bin/sleep 1; /usr/local/bin/rebootto recovery) "
    ">/tmp/to-twrp.log 2>&1 & echo TATER_TWRP_REBOOTING"
)


class Fail(Exception):
    """A user-facing recovery-helper failure."""


def load_console_library():
    """Load the standard-library-only USB console code bundled beside this tool."""
    for path in TOOL_PATHS:
        library = path / "techo5lib.py"
        if not library.is_file():
            continue
        spec = importlib.util.spec_from_file_location("tater_recovery_techo5lib", library)
        if spec is None or spec.loader is None:
            break
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    raise Fail(
        "the bundled techo5lib.py is missing; extract the complete factory "
        "archive and run this helper from that directory"
    )


def available_ports(console_library) -> list[tuple[str, str]]:
    """Return each supported USB console once; macOS discovery overlaps the ID sets."""
    discovered: list[tuple[str, str]] = []
    seen: set[str] = set()
    for ids in (console_library.CONSOLE_DOT, console_library.CONSOLE_TECHO5):
        for port, serial in console_library.list_consoles(ids):
            if port in seen:
                continue
            seen.add(port)
            discovered.append((port, serial))
    return discovered


def select_port(console_library, requested: str | None) -> str:
    if requested:
        return requested
    ports = available_ports(console_library)
    if not ports:
        raise Fail(
            "no Tater Echo USB console was found; boot the satellite normally, "
            "connect it directly by USB, and close any serial terminal using it"
        )
    if len(ports) > 1:
        names = ", ".join(port for port, _ in ports)
        raise Fail(
            f"more than one compatible USB console was found ({names}); "
            "rerun with --port followed by the intended port"
        )
    return ports[0][0]


def exchange(console_library, port: str, command: str, wait: int) -> str | None:
    try:
        return console_library.console_exchange(port, command, wait)
    except OSError as error:
        detail = error.strerror or str(error)
        raise Fail(
            f"could not open {port}: {detail}. Close any serial terminal using it; "
            "on Linux, rerun this command with sudo or grant your user serial-port access"
        ) from error


def recovery_kind(answer: str | None) -> str:
    lines = [] if answer is None else answer.splitlines()
    if "TATER_RECOVERY_DOT" in lines:
        return "dot"
    if "TATER_RECOVERY_SHOW_ROUTER" in lines:
        return "show-router"
    if "TATER_RECOVERY_SHOW_LEGACY" in lines:
        return "show-legacy"
    return ""


def reboot_to_twrp(console_library, port: str) -> None:
    print(f"== checking the Tater Linux console on {port}", flush=True)
    answer = exchange(console_library, port, PROBE_COMMAND, 5)
    if answer is None:
        raise Fail(
            f"the USB console on {port} did not answer; wait for Tater Linux to finish "
            "booting and make sure no terminal has the port open"
        )
    kind = recovery_kind(answer)
    if not kind:
        raise Fail(
            f"the device on {port} is not a supported Tater Linux Echo satellite "
            "with a TWRP recovery route"
        )

    command = {
        "dot": DOT_REBOOT_COMMAND,
        "show-router": SHOW_ROUTER_REBOOT_COMMAND,
        "show-legacy": SHOW_LEGACY_REBOOT_COMMAND,
    }[kind]
    print("== rebooting the satellite into TWRP", flush=True)
    answer = exchange(console_library, port, command, 5)
    if answer is None or "TATER_TWRP_REBOOTING" not in answer.splitlines():
        raise Fail(
            "the reboot command was not acknowledged; the satellite was not intentionally changed"
        )
    print("TWRP reboot requested. Keep USB connected and wait for TWRP to appear.")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--port",
        help="USB serial port to use when more than one is connected (for example /dev/ttyACM0)",
    )
    args = parser.parse_args()
    try:
        console_library = load_console_library()
        port = select_port(console_library, args.port)
        reboot_to_twrp(console_library, port)
    except Fail as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
