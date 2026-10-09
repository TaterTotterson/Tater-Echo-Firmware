# Tater Linux for Rook (Echo Spot 2017)

This is a separate target, not a Checkers image resized to 480×480. Rook is a
supported Tater Echo target beginning with v2.1.0. Its original boot, recovery,
system, and other boot-critical partitions were read back and hash-verified
during hardware qualification. TWRP remains in recovery.

Implemented:

- Native 480×480 framebuffer presentation, without Checkers' 90° rotation.
- `mtk-tpd` protocol-A touch with `BTN_TOUCH` release handling; Checkers'
  protocol-B `goodix-ts` path remains separate.
- A round, safe-area Tater renderer with live weather artwork, setup and
  connecting states, awareness images, timers, a hold-anywhere intercom, and
  the compact response bubble. The weather animation uses the same drawing
  routine as Checkers rather than a static icon.
- A Rook-specific daemon target: six-channel PCM22 capture (four microphones
  and two playback-reference channels), PCM23 playback with a DRAM hold,
  two-ADC mixer routes, active-high speaker amplifier, Spot jack gains,
  screen-only LEDs, Rook buttons, and HCI0 BLE scanning.
- A measured four-microphone front end. USB captures map channels clockwise as
  front-right, rear-right, rear-left, and front-left around a compact
  approximately 22×20 mm array. Two independently scored fractional-delay
  wake beams cover the strongest bearings; the winning beam supplies wake
  verification, trainer, and STT pre-roll audio before carrying into the voice
  turn. Initial and continued-chat speech use the same acquisition path. A
  dead channel is excluded automatically instead of making the Spot
  deaf; the unlocked omni mix and four steering directions retain separate
  AEC states.
- An on-demand Room Vision still helper built from TECHO5's Rook GC0312 driver.
  It listens on loopback only, does not save frames, and refuses capture when
  Tater's persisted mute state is muted or unavailable. The daemon checks its
  live mute state before and after each request as well.
- A pinned Rook rootfs builder based on the signed TECHO5 Spot v0.5.25
  platform. It retains the Rook boot/driver contract while replacing the
  application with Tater's daemon and renderer. No device-specific vendor
  files are included in the distributable image. Routine Tater OTA updates
  the daemon and renderer in application A/B slots on `/data`; a platform
  rootfs update still requires USB/rescue.
- Rook's Broadcom driver exposes the same `00:90:4c:1a:09:00` placeholder on
  every unit. The rootfs now replaces only that placeholder with the factory
  address from `/proc/idme/mac_addr` after firmware load but before association
  and DHCP. A stable serial-derived local address is used only if IDME is
  missing or invalid, allowing multiple Spots to remain online together.
- A read-only TWRP preflight that verifies the device codename, the Rook
  boot/recovery/system partition numbers, and complete local backups before
  the installer is permitted to write anything.
- An offline development boot-image builder using the Rook LineageOS boot
  header, the signed Spot Bluetooth kernel and rescue bundle, and pinned
  Alpine minirootfs. Its rescue stage now uses Tater's dark/orange install and
  recovery screen, drawn upright inside the Spot's round display. It checks
  the image fits Rook's 16 MiB boot partition and never flashes the device.
- A Rook-only USB factory installer in `factory/rook-linux`. It verifies the
  bundle, pins the Rook LineageOS ZIP, saves checksum-verified recovery copies,
  preserves the device's own vendor tree, leaves recovery and bootloader
  partitions untouched, and checks the native Tater services on the first
  slot-A boot.

Observed during hardware qualification:

- The pinned Rook LineageOS v0.3 ZIP installed from TWRP; its Broadcom Wi-Fi
  module reports the same 4.9.337 kernel family as the boot image.
- The rescue USB console came up, saw the vendor tree, and created the A/B
  store on system partition 11. Slot A booted Tater Linux `v0.0.1`.
- The native daemon and round renderer started. The renderer identified the
  480×480 panel and touch event device, and logged about 30 FPS at idle.
- HCI0 came up. The first Broadcom virtual `ap0` hotspot passed some DHCP
  traffic but stopped transmitting, so it was not usable for setup. Switching
  the primary `wlan0` into AP mode gave the iPhone an address and loaded the
  setup page at `http://192.168.4.1`; the release uses that verified mode.
- On 2026-10-03, a temporary Rook camera helper built with the pinned TECHO5
  Spot driver captured 640×480 JPEG snapshots from this unit (HTTP 200,
  43–71 KiB) through `127.0.0.1:43823`. No image was saved; the permanent
  rootfs integration then booted in slot B, captured again, and passed its
  connected health window. The production helper refuses camera access while
  the satellite is muted.
- On 2026-10-04, direct six-channel USB captures from the front, right, rear,
  and left measured the microphone ordering and aperture above. Those four
  recordings were replayed through the production Go front end; every bearing
  localized correctly. Synthetic cardinal, winner-lock, degraded-channel, and
  five-path AEC regression tests preserve that behavior without storing a
  user's captured audio in the repository.
- The OTA-capable `v0.0.3-test` rootfs booted and committed in slot A, leaving
  `v0.0.2` good in slot B. Tater delivered a Rook-only `v0.0.4-test`
  application bundle, which installed in `/data`, restarted, reconnected, and
  committed after its health window. A deliberately non-running application
  bundle then rolled back automatically to `v0.0.4-test` and reconnected.
  These were local bench artifacts, not published releases.

The rootfs base is pinned to SHA-256
`c2960f9a2792868a2707fdc514594a034b404c6a472b049c4fb3397165fe0eb2`.
The TECHO5 Spot v0.5.25 release manifest and signature were checked against
its upstream key before using the platform inputs.
The same signed release provides the Bluetooth kernel
(`368738ee20b86dc3dc0268d3b4b145e67cca9666379ac26b2379abd6c9136298`)
and rescue bundle
(`a6f3860d0e3bd5b298ff310d8fe0d6a9a7885a2f6aa792e4134783df4cbf15bf`).
The compatible [Rook LineageOS v0.3](https://github.com/amazon-oss/releases/releases/tag/lineage-18.1-rook-v0.3)
ZIP is `lineage-18.1-20251108-UNOFFICIAL-rook.zip` with SHA-256
`2755428c124df88ffd3bc6a9c36db16bbe3b7e639883fd1092dc839ac5ae9d13`.
The release-note text lists a different hash, but the ZIP's `.sha256sum`
asset and GitHub's asset digest both agree on this one; the downloaded ZIP was
verified against those two independent metadata fields before use.
The Alpine 3.24.1 armv7 minirootfs used by the rescue image is pinned to
`50942d567e6ee422c16cb46d5c282ed9d8adc9007c2a483faf4148a18c64ce32`.

The tagged release workflow runs the Rook installer, rootfs, boot, audio,
camera integration, display, touch, and OTA regressions; it publishes Rook's
factory and OTA artifacts only after all target jobs succeed.

Hardware and low-level boot references: [TECHO5 Spot](https://github.com/HuskerMinion/techo5-spot)
(MIT), especially its [hardware notes](https://github.com/HuskerMinion/techo5-spot/blob/main/docs/hardware.md)
and [installer](https://github.com/HuskerMinion/techo5-spot/blob/main/tools/install-spot.py).
Their source is a reference for a Rook-specific port, not a drop-in Checkers
image. The device's private partition backups and any extracted vendor files
must not be added to this repository or release assets.
