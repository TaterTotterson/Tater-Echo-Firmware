# Biscuit USB Ethernet

USB Ethernet is automatic on Biscuit's micro-USB port. It was verified on an
Echo Dot 2nd Gen with a powered UCTRONICS U6113 adapter (`0bda:8152`) using its
Realtek RTL8152 driver. Release-candidate testing on 2026-10-29 found that the
adapter's advertised CDC-ECM interface enumerated and received frames but could
not transmit on Biscuit's 3.18 MUSB path. The pinned kernel therefore retains
generic CDC-ECM while binding this adapter to the built-in vendor driver.

## Physical qualification — 2026-10-29

- Hardware: Amazon Echo Dot 2nd Generation, model RS03QR (`biscuit`).
- Adapter: powered UCTRONICS U6113 (`0bda:8152`), with Ethernet providing both
  network and power to Biscuit.
- Tested firmware: commit `841f3bd45d524c9abeb69d7093cddd805d5ed5e7`,
  `v3.1.2-test8`; Tater Echo Linux commit
  `f99397141e82a3f03b0b3b1000090c9e72ef3f28`.
- Pinned Biscuit kernel SHA-256:
  `1d3d7e6ade694c60ef29fe49683497b885afcea8c2ea3b23b6e47b077d953729`.
- Passed: preserve-settings factory install, physical Volume Up TWRP and
  automatic Linux restoration, cold boot with the adapter attached, native
  `r8152` ownership, DHCP, Ethernet-only default route, Wi-Fi shutdown, SSH,
  gateway/public-internet/DNS access, a 24.1 MiB HTTPS transfer with no
  RX/TX/frame/carrier errors, and power-cycle fallback to direct USB serial,
  Wi-Fi, SSH, and public internet.

The adapter is effectively PoE for Biscuit: removing its Ethernet feed also
removes device power. A powered, in-place cable-loss test is therefore not
physically possible with this wiring. The applicable failure gate is a cold
boot on the adapter followed by a power-cycle onto direct USB, which passed.

## Automatic runtime behavior

There is no enable setting. The init-supervised controller continuously applies
this policy:

1. Keep Wi-Fi connected while looking for an enumerated USB Ethernet interface.
2. On Biscuit, briefly switch the shared micro-USB controller from gadget mode
   to host mode for a bounded adapter probe.
3. Run an Ethernet-specific DHCP client and require both an IPv4 address and a
   default route on that interface.
4. Only after Ethernet is usable, stop Wi-Fi DHCP and association.
5. Monitor carrier, address, and route. On unplug or sustained failure, stop
   only Ethernet DHCP, restore USB serial, and reconnect Wi-Fi.

During first-boot setup, Ethernet can obtain a usable route but cannot stop the
`Tater-Setup-XXXX` hotspot. The hotspot remains reachable through form
submission and closes only with the setup reboot. After that reboot, pairing
uses Ethernet when it is valid or the saved home Wi-Fi network otherwise.

If no adapter appears or DHCP fails, the controller restores USB serial and
leaves the already-running Wi-Fi connection alone. It waits 60 seconds before
the next bounded Biscuit probe. A later hot-plug therefore takes over without a
setting change or reboot. Wi-Fi and Ethernet use separate PID files and DHCP
hooks, so changing one transport cannot kill the other's lease client.

Biscuit is powered through this same connector. Use a powered pass-through
Ethernet adapter such as the verified UCTRONICS U6113 with its own 5 V input.
A passive OTG adapter cannot reliably power Biscuit and the NIC together.

Biscuit cannot identify a host adapter while the port is still a USB gadget.
Consequently each automatic detection attempt temporarily removes USB serial
for at most the enumeration timeout, even when no adapter is connected. Every
exit, timeout, DHCP failure, or link loss restores device mode. This tradeoff is
specific to the shared power/data micro-USB design.

Before connecting the powered Ethernet adapter, enable SSH in Biscuit's Tater
satellite settings. Its unique login then replaces the diagnostic shell over
Wi-Fi or Ethernet. See [`ssh-access.md`](ssh-access.md) for setup, connection,
diagnostics, and optional public-key access.

The daemon exposes detection separately from qualification and live state:

- `usb_ethernet_prepared`: the automatic controller is installed.
- `usb_ethernet_automatic`: hardware detection is running under init.
- `usb_ethernet_supported`: the target's software role backend or hardware-OTG
  path is qualified.
- `usb_ethernet_active`: Ethernet currently owns network traffic.
- `usb_ethernet_state`: `pending`, `waiting`, `probing`, `active`,
  `recovering`, `rescue`, `probe-complete`, `fallback`, `stopped`,
  `unsupported`, or `unavailable`.
- `usb_ethernet_wifi_rescue`: Wi-Fi is carrying traffic while an attached
  Ethernet adapter remains unhealthy and is being retried.

The old `usb_ethernet_enabled` setting is ignored. The installed Tater commands
are `/usr/local/sbin/tater-net` and
`/usr/local/sbin/tater-usb-ethernet`. Compatibility links preserve the former
`techo5-net` and `techo5-eth` names on Dot-family images.

## Every satellite image

The same controller is installed and supervised on Biscuit, Radar, Checkers,
Crown, Athena, and Rook. All six automatically use a USB NIC that the kernel has
already enumerated, after DHCP, route, and end-to-end path validation. A carrier,
address, and default route are not enough: the controller also binds a TCP probe
to the wired address and checks the paired Tater endpoint, with the DHCP gateway
as an independent reachability check. If that path stalls, it renews DHCP and
resets the interface before starting Wi-Fi rescue. The rescue state remains
visible in device status while the attached adapter is retried, and a recovered
wired path automatically takes ownership again. Biscuit performs an active
userspace role switch; Checkers and Crown use qualified physical OTG paths
without a firmware role write; Athena uses its qualified mainline MTU3/GPIO OTG
path; and Rook uses the qualified bounded MediaTek role backend. Radar remains
passive so untested role or VBUS writes cannot break power, serial, recovery,
or internal USB Wi-Fi.

Radar shares the Dot userspace but has no external USB port. Its barrel jack
powers the device, while its internal USB pads expose D+/D-/GND; the adapter
therefore needs external power. Checkers, Crown, Athena, and Rook completed
their separate real-hardware RTL8152 gates. The independent records are in
[`usb-ethernet-platforms.md`](usb-ethernet-platforms.md).

## Platform dependency

The branch pins the Tater Echo Linux `dot-kernel-v1.0.1` prerelease. Its
Biscuit kernel is
`1d3d7e6ade694c60ef29fe49683497b885afcea8c2ea3b23b6e47b077d953729`;
Radar has a separate target image and hash. Source is maintained under
[`Tater-Echo-Linux/platforms/dot`](https://github.com/TaterTotterson/Tater-Echo-Linux/tree/main/platforms/dot).
The kernel fragment enables MediaTek forced role control, MII, usbnet,
CDC-ECM, and built-in RTL8152. The compatibility patch supplies the EEE
helpers missing from Amazon's 3.18 tree while retaining upstream's driver
ownership rule for `0bda:8152`.

The exact published Biscuit kernel completed its physical release gate above.
For a future kernel candidate, repeat enumeration, DHCP/default-route, cold
boot, sustained traffic, power-cycle fallback, Wi-Fi recovery, and USB-serial
restoration. `tater-usb-inventory` collects read-only evidence, and
`tater-usb-ethernet probe` performs a bounded enumeration-only check without
DHCP or a Wi-Fi change.

Do not distribute `r8152.ko` as a loose binary. RTL8152 is built into the pinned
kernel so it is available before rootfs selection and cannot drift from the
kernel ABI. Kernel outputs must be built from pinned Tater Linux source and
published with hashes.
