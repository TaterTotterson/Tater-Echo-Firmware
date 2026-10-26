# Biscuit USB Ethernet

USB Ethernet is an opt-in Biscuit feature for the micro-USB port. It was
verified on an Echo Dot 2nd Gen with a powered UCTRONICS U6113 adapter
(`0bda:8152`) using its CDC-ECM configuration. The device obtained
`192.168.1.9` by DHCP and installed a default route through `eth0` on
2026-10-25.

## Runtime behavior

The default remains USB serial plus Wi-Fi. Enabling `usb_ethernet_enabled`
starts an ordered probe:

1. Keep Wi-Fi connected while switching the shared micro-USB controller from
   gadget/device mode to host mode.
2. Wait for `ethN` or `usbN`, then run an Ethernet-specific DHCP client.
3. Require both an IPv4 address and a default route on that interface.
4. Only after Ethernet is usable, stop the Wi-Fi DHCP client and supplicant.
5. Monitor carrier and the address. On disable or sustained link loss, stop
   only Ethernet DHCP, restore USB serial, and reconnect Wi-Fi.

If no adapter appears or DHCP fails, the controller restores USB serial and
leaves the already-running Wi-Fi connection alone. Wi-Fi and Ethernet use
different pid files and DHCP hooks, so changing Wi-Fi cannot kill the wired
lease.

The daemon reports desired state separately from live state:

- `usb_ethernet_supported`: this target has the controller script and kernel
  role control installed.
- `usb_ethernet_enabled`: the user's persistent preference is `wired`.
- `usb_ethernet_active`: Ethernet currently owns network traffic.
- `usb_ethernet_state`: `disabled`, `pending`, `probing`, `active`,
  `fallback`, or `unsupported`.

The installed Tater commands are `/usr/local/sbin/tater-net` and
`/usr/local/sbin/tater-usb-ethernet`. Compatibility links preserve the former
`techo5-net` and `techo5-eth` names for existing platform scripts.

## Availability and safety gate

The setting is currently advertised only on Biscuit. A daemon-only OTA does
not enable it: support also requires the immutable root filesystem controller
and `/proc/mchange` from a compatible kernel. This prevents an application OTA
from accepting a toggle that its installed Linux image cannot implement.

Radar shares parts of the Dot root filesystem, but it is intentionally gated
off until its USB controller, power arrangement, adapter enumeration, and
fallback path are verified on real hardware. Checkers, Crown, Athena, and Rook
have separate kernels and board plumbing and require their own platform work;
the common daemon contract can be reused after each target supplies and tests
an equivalent controller.

## Platform dependency

The currently pinned TECHO5 Dot v1.0.24 release does not contain the required
kernel changes, so the feature remains unavailable in a normal release until
the changes now maintained in
[`Tater-Linux/platforms/dot`](https://github.com/TaterTotterson/Tater-Linux/tree/main/platforms/dot)
are built and published as a pinned Biscuit platform artifact. The functional
kernel change is `0052-cdcether-rtl8152-no-blacklist.patch`, which lets the
generic `cdc_ether` driver bind the adapter. The fragment enables MediaTek
forced USB role control and the required usbnet/CDC-ECM support.

Do not distribute `r8152.ko` as a loose binary. CDC-ECM is the verified path,
and all kernel outputs should be reproducibly built from the pinned Tater
Linux source and published with hashes.
