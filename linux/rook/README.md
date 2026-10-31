# Tater Linux for Rook (Echo Spot 2017)

Rook is the supported first-generation 2017 Echo Spot target. It runs native
Tater Linux with its own 480×480 renderer, touch mapping, four-microphone audio
front end, camera helper, and recovery image. It is not a resized Checkers
image, and images for either target are rejected by the other.

## Runtime

- The round-safe Home view uses a large centered clock and weather hero,
  larger room readings, and the configured assistant's live
  online/listening/thinking/speaking state beside the room name.
- Touch opens Sat Chat, climate, music, Room Vision, voice, timer, and device
  controls. Holding the main surface starts intercom.
- Six-channel PCM22 capture provides four microphones followed by two hardware
  playback-reference channels. The measured microphone order is front-right,
  rear-right, rear-left, and front-left around an approximately 22×20 mm array.
- Four fractional-delay steering directions feed the two strongest continuous
  wake lanes. MWW, OWW, NWW-CNN, NWW-GRU, MWW + OWW, and MWW + NWW use the
  same winning-beam handoff into verification, trainer capture, and STT.
- PCM23 playback uses the target's two-ADC routes, DRAM hold, active-high
  amplifier, native software EQ/limiter chain, and synchronized Sendspin path.
- The loopback-only Room Vision helper uses TECHO5's Rook GC0312 driver. It
  stores no frames and refuses capture whenever mute state is active or unknown.
- BLE presence and active GATT use the native HCI0 path.

## Platform and updates

The rootfs is built from the signed, hash-pinned TECHO5 Spot `spot-v0.5.25`
platform while replacing its application layer with `tater-echo` and
`tater-show`. Device-specific vendor files are adopted during factory install
and are never committed to this repository or included in public release
assets.

Routine OTA stages the daemon and renderer together in application A/B slots
under `/data`, with connected-health commit and automatic rollback. Kernel,
rootfs, rescue, and vendor-driver changes require the USB factory/reinstall
path. TWRP remains available in recovery.

The Broadcom driver exposes the same `00:90:4c:1a:09:00` placeholder on every
unit. First boot replaces only that placeholder with the factory address from
IDME before association and DHCP; a serial-derived local address is used only
when IDME is missing or invalid.

## Pinned inputs

- TECHO5 Spot rootfs `spot-v0.5.25`:
  `c2960f9a2792868a2707fdc514594a034b404c6a472b049c4fb3397165fe0eb2`
- Tater Echo Linux Rook Ethernet kernel `rook-kernel-v1.0.0`:
  `57e6d7401e091f26d0efeadc86170144195f54b50105f98ea311cf0523c31a00`
  (built-in RTL8152 plus bounded MediaTek `/proc/mchange` role control, while
  preserving the `4.9.337-g4174e0b4d0e2` vendor-module ABI)
- TECHO5 Spot rescue bundle:
  `a6f3860d0e3bd5b298ff310d8fe0d6a9a7885a2f6aa792e4134783df4cbf15bf`
- [Rook LineageOS v0.3](https://github.com/amazon-oss/releases/releases/tag/lineage-18.1-rook-v0.3)
  vendor source:
  `2755428c124df88ffd3bc6a9c36db16bbe3b7e639883fd1092dc839ac5ae9d13`
- Alpine 3.24.1 armv7 rescue minirootfs:
  `50942d567e6ee422c16cb46d5c282ed9d8adc9007c2a483faf4148a18c64ce32`

The build verifies these inputs before assembly and tests the Rook installer,
rootfs, boot image, audio, camera, display, touch, wake runtime, and OTA package
before release publication.

Installation and recovery instructions are in
[`factory/rook-linux/README.md`](../../factory/rook-linux/README.md). Low-level
platform attribution is retained in [`NOTICE.md`](../../NOTICE.md) and in each
generated factory bundle.
