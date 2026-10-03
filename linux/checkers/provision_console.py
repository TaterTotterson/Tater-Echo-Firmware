#!/usr/bin/env python3
"""Provision a fresh Checkers over its Linux USB serial console.

Secret file contents are sent directly to the unit and never printed.
"""

import base64
import hashlib
import importlib.util
import json
import os
import shlex
import time


SERIAL_WAIT_SECONDS = 90
def load_techo5lib(tools_dir):
    path = os.path.join(os.path.abspath(tools_dir), "techo5lib.py")
    spec = importlib.util.spec_from_file_location("techo5lib", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load %s" % path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def encoded_write(path, data, mode):
    encoded = base64.b64encode(data).decode("ascii")
    temporary = path + ".new"
    return (
        "printf %s %s | base64 -d > %s && chmod %s %s && mv -f %s %s"
        % (
            "%s",
            shlex.quote(encoded),
            shlex.quote(temporary),
            mode,
            shlex.quote(temporary),
            shlex.quote(temporary),
            shlex.quote(path),
        )
    )


def wait_for_console(console):
    deadline = time.time() + SERIAL_WAIT_SECONDS
    while time.time() < deadline:
        # Console.run recognizes its end marker as a complete line.  Keep a
        # newline after our probe so the marker cannot be joined to it.
        response = console.run("echo TATER-LINUX-READY", 5)
        if response and "TATER-LINUX-READY" in response:
            return
        time.sleep(2)
    raise RuntimeError("the Tater Linux USB console did not answer")


def provision_console(serial, techo_tools, values, no_reboot=False):
    """Write a fresh pairing bootstrap through the Linux USB console."""
    if "native" not in values or "token" not in values:
        raise RuntimeError("provisioning requires native config and a token")
    json.loads(values["native"].decode("utf-8"))
    if not values["token"].strip():
        raise RuntimeError("device token or pairing code is empty")

    techo5lib = load_techo5lib(techo_tools)
    console = techo5lib.Console(serial, techo5lib.CONSOLE_TECHO5)
    wait_for_console(console)

    destinations = {
        "wifi": "/data/tater-linux/wpa_supplicant.conf",
        "token": "/data/local/etc/tater/device_token",
        "native": "/data/local/etc/tater/native.json",
    }
    response = console.run(
        "umask 077; mkdir -p /data/tater-linux /data/local/etc/tater /data/emos && "
        "chmod 700 /data/tater-linux /data/local/etc/tater && echo PREPARE-OK",
        10,
    ) or ""
    if "PREPARE-OK" not in response:
        raise RuntimeError("the unit could not prepare the provisioning directories")

    # Keep each serial command short enough for the console's line discipline,
    # and write native.json last: its appearance lets the supervised daemon start.
    order = [key for key in ("wifi", "token", "native") if key in values]
    for key in order:
        response = console.run(encoded_write(destinations[key], values[key], "600") + " && echo WRITE-OK", 12) or ""
        if "WRITE-OK" not in response:
            raise RuntimeError("the unit could not write %s" % key)
        expected = hashlib.sha256(values[key]).hexdigest()
        response = console.run("sha256sum %s; echo HASH-OK" % shlex.quote(destinations[key]), 8) or ""
        if "HASH-OK" not in response or expected not in response:
            raise RuntimeError("the unit did not verify %s" % key)

    if "wifi" in values:
        response = console.run(
            "rm -f /data/emos/wpa.conf && "
            "ln -s /data/tater-linux/wpa_supplicant.conf /data/emos/wpa.conf && "
            "sync && echo PROVISION-OK",
            30,
        ) or ""
        if "PROVISION-OK" not in response:
            response = console.run(
                "test \"$(readlink /data/emos/wpa.conf)\" = /data/tater-linux/wpa_supplicant.conf && "
                "echo PROVISION-OK",
                8,
            ) or ""
            if "PROVISION-OK" not in response:
                raise RuntimeError("the unit could not finish provisioning")

    if not no_reboot:
        durable = " ".join(destinations[key] for key in order)
        console.run("/usr/local/bin/tater-reboot-now %s" % durable, 3)
