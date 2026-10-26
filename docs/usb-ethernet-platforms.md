# USB Ethernet platform qualification

USB Ethernet has one shared network policy and a separate hardware role backend
for every board family. The shared policy may keep Wi-Fi up during a probe,
accept Ethernet only after DHCP and a default route succeed, and restore Wi-Fi
after a wired failure. It must never guess how another board changes USB role or
drives VBUS.

The runtime source of truth is
[`device/internal/usbethernet/capabilities.json`](../device/internal/usbethernet/capabilities.json).
A target is advertised only when that record is `qualified`, its controller is
installed and executable, and its kernel exposes the role control required by
the selected backend. Sharing a SoC, kernel family, or root filesystem is not
enough.

## Current qualification state

| Target | Status | Candidate role backend | Hardware boundary |
|---|---|---|---|
| Biscuit | `qualified` | MediaTek `/proc/mchange` | Shared power/data micro-USB; powered pass-through adapter required; CDC-ECM verified |
| Radar | `hardware-validation` | MediaTek `/proc/mchange` candidate | Barrel-powered device; no external USB port; internal D+/D-/GND pads and externally powered adapter |
| Checkers | `baseline-required` | MediaTek MUSB candidate | Its own boot image, device tree, VBUS behavior, and USB-serial recovery |
| Crown | `baseline-required` | MediaTek MUSB candidate | Must be qualified separately from Checkers despite shared source |
| Athena | `kernel-work` | Mainline MediaTek MTU3 role-switch candidate | Current device tree is peripheral-only and disables the host controller |
| Rook | `baseline-required` | MediaTek MUSB candidate | External `usb0`; internal USB Wi-Fi remains on separate `usb1` |

Only Biscuit is enabled. The other records document what to investigate; their
candidate backend names do not make them available to the daemon.

On a qualified target, enabling Ethernet means wired-preferred automatic mode.
Wi-Fi stays connected while USB host enumeration and Ethernet DHCP run. A usable
Ethernet route disables Wi-Fi; link loss or unplug restores the device/serial
role and Wi-Fi, then the supervisor waits and retries. Biscuit has no
non-disruptive way to see a host adapter while its shared controller is in
gadget mode, so an enabled waiting state briefly interrupts USB serial once per
60-second retry. This never occurs while USB Ethernet is disabled.

## Evidence collection

Every Tater Linux root filesystem installs the read-only command:

```sh
tater-usb-inventory
```

It reports the target and kernel, relevant configuration symbols, existing role
controls, sanitized USB descriptors, loaded USB-network drivers, and network
interfaces. It does not change the controller, GPIOs, routes, interfaces, or
persistent state, and it omits USB serial numbers. For a local diagnostic with
filtered recent kernel messages:

```sh
tater-usb-inventory --include-logs
```

Review the output before building a role-changing test image. Preserve one
inventory from the known-good baseline and one from the candidate image.

Biscuit also supports an explicit bounded enumeration-only probe:

```sh
tater-usb-ethernet probe
```

That command may temporarily remove USB serial while the port is in host mode.
It does not run DHCP or stop Wi-Fi, restores device mode after enumeration, and
has an independent 60-second restoration watchdog. It is not available on an
unqualified target. A new backend must provide an equivalent bounded probe
before its target can be marked qualified.

## Per-target release gate

Complete these in order for each target:

1. Reproduce the currently pinned kernel, boot, rootfs, and rescue inputs from
   Tater Echo Linux. Publish separate, hash-pinned artifacts and boot the exact
   published files before making an Ethernet change.
2. Capture `tater-usb-inventory` while USB serial/device mode is working.
3. Identify the physical connector, controller, ID/role signal, VBUS source,
   power budget, and whether the Ethernet adapter must be externally powered.
4. Add a target-specific role backend. Default to device mode, fail closed when
   any required control is absent, and keep the target unqualified.
5. Run a bounded enumeration test with Wi-Fi left up. Prove that timeout,
   interruption, adapter absence, and explicit cancellation all restore USB
   serial without requiring the daemon.
6. Test the intended adapter and any supported alternatives: enumeration,
   driver binding, DHCP, default route, DNS, link loss, unplug/replug, cold boot,
   repeated reboot, and sustained traffic.
7. Prove recovery before qualification: Tater Recovery, TWRP or the applicable
   lower-level fallback must remain reachable after a failed wired boot.
8. Record the exact hardware, kernel/boot hash, inventory, and acceptance date.
   Only then change the capability status to `qualified` and add the backend to
   the runtime selector.

Wi-Fi must remain connected until Ethernet has both an IPv4 address and a
default route. On disable or sustained failure, Ethernet DHCP is stopped first,
the USB device/serial role is restored, and Wi-Fi is then restarted if it had
been stopped. A daemon-only OTA can never qualify a platform whose immutable
kernel or root filesystem lacks the required controller.

## Recommended order

1. Re-test Biscuit with the exact published Tater kernel artifact.
2. Boot the unchanged, hash-identical Tater custody baselines for Checkers,
   Crown, Athena, and Rook, then replace each one with a Tater-built artifact
   only after its separate boot and recovery gate passes.
3. Qualify Rook's external `usb0`, then Checkers and Crown independently.
4. Implement Athena's explicit OTG/role-switch and VBUS device-tree work.
5. Treat Radar as an experimental internal hardware modification, not a normal
   external-port feature.
