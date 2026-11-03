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

## Hardware validation

Cronos completed its real-device release gate on 2026-11-02 using model C76N8S
and the `v4.1.0-cronos-test1` candidate. The validated paths include the
verified factory installer and first boot; framebuffer, touch, camera and boot
branding; two-microphone capture, wake detection, AEC, speaker playback,
buttons and privacy controls; Wi-Fi setup and normal Tater pairing; SSH and
Volume Up TWRP; and the A/B root-system health commit and confirmation restart.

USB Ethernet was validated with the powered UCTRONICS B0B9ZLVGJ4 adapter and
its Realtek RTL8152 controller (`0bda:8152`, Linux `r8152`). Cronos selected its
physical MUSB host role, obtained `10.4.20.159` by DHCP, made Ethernet its only
default route, stopped Wi-Fi, reconnected to Tater, and carried SSH traffic
without packet errors. Returning to the normal USB path restores device mode
and Wi-Fi recovery behavior.

See [`../factory/cronos-linux/README.md`](../factory/cronos-linux/README.md)
for the factory procedure.
