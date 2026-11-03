# Echo Show 5 second generation (`cronos`)

Cronos is the 2021 Amazon Echo Show 5 second generation, model C76N8S. Tater
uses the same responsive 960×480 interface as Checkers, but Cronos is a
separate target because its boot image, vendor drivers, audio amplifier, input
devices, privacy latch, and partition sizes are not interchangeable.

## Pinned platform

- Unlock: `amonet-cronos` v2.0.1 with permanent TWRP in the dedicated recovery
  partition.
- Vendor-driver baseline: LineageOS 18.1 Cronos v0.4,
  `lineage-18.1-20260904-UNOFFICIAL-cronos.zip`, SHA-256
  `4c355998061a454792128d4b730932b47ed05a3d2a6d2628599218f44cc84678`.
- Tater rescue/boot baseline: `tater-echo-cronos-baseline-boot.img`, SHA-256
  `7e9ce93a54fc613cbfab5d15929aa06bfd6f63c2c432d6f8261b3599fc3cd791`.
- CPU and userspace: ARMv7 userspace on the pinned ARM64 MediaTek MT8163 Tater
  kernel. The unlock's TWRP environment itself reports ARMv7.

The USB installer verifies the attached `cronos` identity and every pinned
input before changing the device. It preserves TWRP in recovery, converts the
system partition into Tater's A/B rootfs store, and keeps persistent settings
on userdata.

## Hardware contract

The first connected unit reports board ID `A1XWJRHALS1REP` and these measured
interfaces:

| Function | Cronos interface |
|---|---|
| Display | ST7701S panel, 960×480 logical Tater canvas |
| Touch | Goodix `goodix-ts` input |
| Microphones | ALSA card 0, PCM 22, four packed 24-bit channels at 16 kHz; two microphones followed by two playback-loopback channels |
| Speaker | ALSA card 0, PCM 23, stereo S16 at 48 kHz; TAS5805M on this unit, with MAX98396 supported as the alternate factory variant |
| Volume buttons | `gpio-keys` input |
| Privacy button | `gpio-privacy-button`, `KEY_POWER` |
| Privacy state | `/sys/devices/platform/gpio-privacy/state`; software may engage the write-only `/sys/devices/platform/gpio-privacy/enable`, while physical release remains authoritative |
| Ambient light | vendor `m_alsps_input` interface |
| Recovery | 32 MiB dedicated recovery partition containing TWRP |

The two microphone channels use Cronos' measured Checkers-shaped coherent
frontend. The two loopback channels provide the aligned playback reference for
AEC. Cronos keeps its own speaker-route and hardware-privacy implementations;
it does not reuse Checkers' RT5616 amplifier controls or `gating` input node.

## Validation gate

Cronos remains `hardware-validation` until this real device passes:

1. verified factory install and first boot;
2. framebuffer, touch, auto-brightness, camera, and boot logo;
3. microphones, wake word, AEC, speaker, volume, and privacy latch;
4. Wi-Fi setup, pairing, Bluetooth, SSH, and reconnect behavior;
5. application OTA, System OTA trial/commit/rollback, and Volume Up TWRP;
6. USB Ethernet enumeration, wired preference, Wi-Fi fallback, reboot, and
   USB-serial restoration.

See [`../factory/cronos-linux/README.md`](../factory/cronos-linux/README.md)
for the factory procedure.
