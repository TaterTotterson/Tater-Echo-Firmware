# USB Ethernet platform qualification

USB Ethernet has one automatic network policy and a separate hardware role
backend for every board family. Every image passively detects a USB-network
interface, keeps Wi-Fi up through DHCP and route validation, and restores Wi-Fi
after a wired failure. The shared policy must never guess how an unqualified
board changes USB role or drives VBUS.

The runtime source of truth is
[`device/internal/usbethernet/capabilities.json`](../device/internal/usbethernet/capabilities.json).
`prepared` means the controller is installed and can use an interface the
kernel has already enumerated. A target advertises `supported` only when its
record is `qualified` and its required software role control or hardware-OTG
path exists. Sharing a SoC, kernel family, or root filesystem is not enough to
qualify a target.

## Current qualification state

| Target | Status | Candidate role backend | Hardware boundary |
|---|---|---|---|
| Biscuit | `qualified` | MediaTek `/proc/mchange` | Shared power/data micro-USB; powered pass-through adapter required; built-in RTL8152 driver |
| Radar | `hardware-validation` | MediaTek `/proc/mchange` candidate | Barrel-powered device; no external USB port; internal D+/D-/GND pads and externally powered adapter |
| Checkers | `qualified` | MediaTek MUSB hardware OTG | Separately powered Show 5; physical OTG ID path, RTL8152 Ethernet, Wi-Fi fallback, and USB serial verified |
| Crown | `qualified` | MediaTek MUSB hardware OTG | Separately powered Show 8; physical OTG ID path, RTL8152 Ethernet, Wi-Fi fallback, and USB serial verified |
| Athena | `hardware-validation` | Mainline MediaTek MTU3 + GPIO OTG | Tater dual-role kernel booted; normal-cable serial/RNDIS, bounded host/device switching, reboot, and Wi-Fi fallback passed; real adapter/VBUS still pending |
| Rook | `qualified` | MediaTek `/proc/mchange` | Shared power/data micro-USB; powered UCTRONICS B0B9ZLVGJ4, RTL8152, bounded forced-role switching, fallback, and serial verified |

All six images start the controller automatically. Radar remains an
unqualified passive candidate: if its kernel enumerates a supported USB NIC,
the common policy can use it, but firmware performs no speculative role or VBUS
write. Checkers and Crown are passive in userspace, but their physical MUSB OTG
signaling and automatic device-mode restoration are qualified independently on
real hardware. Rook's stock dual-role sysfs files are intentionally read-only
and the powered pass-through adapter does not assert its physical ID pin. Its
Tater kernel therefore exposes MediaTek `/proc/mchange` control and includes
the RTL8152 driver, while the common controller uses the same bounded host
probe as Biscuit. Internal Wi-Fi remains on the separate USB1 controller during
the probe. Rook passed the complete adapter and recovery gate. Athena's kernel
now follows the connector's physical ID and VBUS-detect GPIOs: a normal cable
remains in device mode, while an OTG plug selects host mode and enables
GPIO150's modelled 5 V supply. It remains unqualified until those host-side
paths are exercised with a real adapter.

### Biscuit qualification record — 2026-10-29

- Hardware: Amazon Echo Dot 2nd Generation, model RS03QR (`biscuit`).
- Adapter: powered UCTRONICS U6113, Realtek RTL8152 (`0bda:8152`, `r8152`).
  Ethernet supplies both network and device power, so removing the live link
  also power-cycles Biscuit; an in-place powered link-loss test is not
  physically available with this wiring.
- Tested firmware: commit `841f3bd45d524c9abeb69d7093cddd805d5ed5e7`,
  `v3.1.2-test8`; Tater Echo Linux commit
  `f99397141e82a3f03b0b3b1000090c9e72ef3f28`.
- Pinned Biscuit kernel SHA-256:
  `1d3d7e6ade694c60ef29fe49683497b885afcea8c2ea3b23b6e47b077d953729`.
- Passed: preserve-settings factory install, Volume Up TWRP and automatic
  Linux restoration, cold boot on powered Ethernet, native `r8152` binding,
  DHCP, Ethernet-only default route, Wi-Fi shutdown, SSH, DNS and public
  internet, 24.1 MiB of sustained HTTPS traffic without interface errors, and
  power-cycle fallback to direct USB serial plus Wi-Fi without repair.

### Checkers qualification record — 2026-10-27

- Hardware: Amazon Echo Show 5 first generation, model H23K37 (`checkers`),
  with separate device power.
- Adapter: [UCTRONICS B0B9ZLVGJ4](https://www.amazon.com/dp/B0B9ZLVGJ4),
  Realtek RTL8152 (`r8152`).
- Tested firmware: commit `e43f9688c13dc6820a1d683d7d808a0311a53048`,
  `v3.2.0-ethernet-test`; Tater Linux platform v0.9.26.
- Pinned Checkers boot SHA-256:
  `46c19fd23c210714e29eb2bf88c77b540f3290c0cf50b680b22032e7a4771da7`.
- Passed: RTL8152 enumeration, DHCP, Ethernet-only default route, Wi-Fi
  shutdown, link-loss/unplug fallback to Wi-Fi without reboot, USB serial
  restoration, SSH, application OTA, A/B System OTA activation and
  confirmation reboots, boot with the adapter attached, and volume-up TWRP.

### Crown qualification record — 2026-10-27

- Hardware: Amazon Echo Show 8 first generation, model C7H6N3 (`crown`), with
  separate device power.
- Adapter: [UCTRONICS B0B9ZLVGJ4](https://www.amazon.com/dp/B0B9ZLVGJ4),
  Realtek RTL8152 (`r8152`).
- Tested firmware: commit `e43f9688c13dc6820a1d683d7d808a0311a53048`,
  `v3.2.0-ethernet-test`; Tater Linux platform v0.9.26, kernel
  `4.9.337-g8d928c5176cc`.
- Pinned rescue boot SHA-256:
  `e00bed964d9b2ce31df152d183d2aeb45a7839cc5f7460a93dd97d70715ad1b7`.
- Passed: RTL8152 enumeration, DHCP, Ethernet-only default route, Wi-Fi
  shutdown, link-loss/unplug fallback to Wi-Fi without reboot, USB serial
  restoration, SSH on Ethernet, application OTA, A/B System OTA activation and
  confirmation reboots, boot with the adapter attached, and volume-up TWRP.
- Sanitized post-test inventory confirmed `CONFIG_USB_MTK_OTG=y`,
  `CONFIG_USB_RTL8152=y`, the MUSB dual-role node, device-mode USB serial after
  unplug, and the exact Tater release above.

### Rook qualification record — 2026-10-27

- Hardware: Amazon Echo Spot first generation (2017), model VN94DQ (`rook`),
  using the powered pass-through adapter for its shared power/data micro-USB
  port.
- Adapter: [UCTRONICS B0B9ZLVGJ4](https://www.amazon.com/dp/B0B9ZLVGJ4),
  Realtek RTL8152 (`r8152`).
- Tested firmware: commit `e43f9688c13dc6820a1d683d7d808a0311a53048`,
  `v3.2.0-ethernet-test`; Tater Linux platform v0.5.25, kernel ABI
  `4.9.337-g4174e0b4d0e2`.
- Tater Rook Ethernet kernel SHA-256:
  `57e6d7401e091f26d0efeadc86170144195f54b50105f98ea311cf0523c31a00`.
- Passed: bounded forced-host switching, RTL8152 enumeration, DHCP,
  Ethernet-only default route, Wi-Fi shutdown, link-loss/unplug fallback,
  device-mode USB serial restoration, SSH, pairing over Ethernet, application
  OTA, A/B System OTA activation and confirmation reboots, factory install,
  boot with the adapter attached, and volume-up TWRP.

Wi-Fi stays connected while Ethernet DHCP runs. A usable Ethernet route
disables Wi-Fi; link loss or unplug restores Wi-Fi and, where the controller
claimed the port, the device/serial role. Biscuit and Rook have
no non-disruptive way to see a host adapter while their shared controller is in
gadget mode, so automatic detection briefly interrupts USB serial once per
60-second retry. There is no user enable flag. Before testing that transition,
enable the per-satellite maintenance service described in
[`ssh-access.md`](ssh-access.md); it provides the diagnostic shell over either
Wi-Fi or Ethernet when USB serial is absent.

First-boot setup is the exception to the Wi-Fi shutdown rule. Ethernet may
enumerate, obtain DHCP, and become the preferred uplink while
`Tater-Setup-XXXX` is open, but it cannot stop that setup hotspot. The guard
covers both the boot race before the hotspot publishes its runtime marker and
the interval after **Save and connect** writes `native.json` but before the
requested reboot. On the next normal boot, a valid wired route takes priority;
otherwise the saved home Wi-Fi credentials provide the fallback and pairing
continues over whichever uplink is usable.

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

Biscuit and Rook, whose kernels expose `/proc/mchange`, support an
explicit bounded enumeration-only probe:

```sh
tater-usb-ethernet probe
```

That command may temporarily remove USB serial while the port is in host mode.
It does not run DHCP or stop Wi-Fi, restores device mode after enumeration, and
has an independent 60-second restoration watchdog. A new backend must provide
an equivalent bounded probe before its target can be marked qualified.

## Per-target release gate

Complete these in order for each target:

1. Reproduce the currently pinned kernel, boot, rootfs, and rescue inputs from
   Tater Echo Linux. Publish separate, hash-pinned artifacts and boot the exact
   published files before making an Ethernet change.
2. Capture `tater-usb-inventory` while USB serial/device mode is working.
   Provision and verify SSH while serial is still available so the later
   network transition remains observable.
3. Identify the physical connector, controller, ID/role signal, VBUS source,
   power budget, and whether the Ethernet adapter must be externally powered.
4. Add a target-specific role backend. Passive USB-interface detection remains
   available, but role writes must fail closed when any required control is
   absent and the target must remain unqualified.
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
default route. Outside setup, a usable wired route stops Wi-Fi and puts its
interface down. On disable or sustained failure, Ethernet DHCP is stopped first,
the USB device/serial role is restored, and Wi-Fi is then restarted if it had
been stopped. A daemon-only OTA can never qualify a platform whose immutable
kernel or root filesystem lacks the required controller.

## Recommended order

1. Complete Athena's VBUS, enumeration, DHCP, traffic, unplug/replug, and cold
   boot qualification with the intended OTG Ethernet adapter.
2. Treat Radar as an experimental internal hardware modification, not a normal
   external-port feature.

Biscuit completed its exact-kernel real-hardware release gate on 2026-10-29.
Checkers, Crown, and Rook completed their independent gates on 2026-10-27.
