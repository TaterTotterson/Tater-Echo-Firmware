# Tater Linux for Checkers

Checkers 2.x runs a small Alpine Linux image while retaining
the existing Tater audio, wake-word, AEC, media, timer, intercom, BLE presence,
OTA, camera, and screen protocol. The native `tater-show` process renders
directly to the framebuffer.

The low-level boot and A/B slot contract is based on the MIT-licensed TECHO5
work. TWRP remains in the recovery partition. The system partition holds two
root filesystem slots for platform recovery; routine Tater updates use small
coordinated daemon/renderer application slots under writable `/data`.

Factory installation is deliberately a USB operation because it reformats
userdata and replaces the former system partition. Later updates use the
normal Tater OTA channel and write only the inactive application slot. A device
that has not been converted rejects the Linux application artifact before writing it.
Fresh devices then advertise an open `Tater-Setup-XXXX` network shown on the
display. The local page at `http://192.168.4.1`
accepts home Wi-Fi, the Tater server, and a one-time satellite pairing code.
The hotspot is isolated from the home network and stops after pairing; USB
provisioning remains available if the radio or portal cannot start.

To return a paired Checkers to setup, press the physical Mute button five
times quickly, then hold the sixth press for five seconds. This clears its
saved Wi-Fi and Tater pairing, but leaves the firmware installed. A single
Mute press continues to toggle the microphone normally.

An application slot commits only after `tater-echo` and `tater-show` have run
continuously for five minutes and the daemon is connected to Tater. Until then
their preceding shared slot remains the automatic fallback. Rootfs trials use
the same health evidence during a USB/recovery platform migration. A healthy
rootfs trial performs one automatic restart after its durable commit; this
returns Checkers' shared `/` and `/store` filesystem to read-only without using
the vendor kernel's unreliable live remount-ro path. An unpaired first install
can stay in setup without exhausting the trial timeout. Saving the setup form
or using the physical setup reset refreshes the boot attempts before the
intentional restart; after pairing, the slot still must connect to Tater and
pass its health window before it commits.

Pinned platform inputs:

- TECHO5 rootfs `v0.9.26`, SHA-256
  `a72bbdfd0d26f536552b44aa9258f136e05d5243220c761a22c0f691a943f14e`
- TECHO5 Checkers boot `v0.7.16`, SHA-256
  `46c19fd23c210714e29eb2bf88c77b540f3290c0cf50b680b22032e7a4771da7`
- Alpine Linux v3.24 armv7 `hostapd-2.11-r4` and `dnsmasq-2.92_p2-r0`
  packages, SHA-256 pinned in `build_rootfs.py` and extracted without their
  package scripts for the first-boot hotspot.

Both are verified before packaging or flashing. See the bundled TECHO5 license
and attribution in the generated root filesystem.
The factory bundle keeps the pinned Checkers kernel and boot flow, but gives
the temporary first-install rescue display a dark Tater palette and an
"INSTALLING TATER" message. Actual recovery boots show "TATER RECOVERY"
instead. The normal Tater display takes over after slot A starts.
