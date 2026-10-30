# Echo Show 8 2nd Gen (`athena`)

`athena` is the 2021 second-generation Echo Show 8, model **A8H3N2**. It is a
separate target from the 2019 Crown and must never receive a Crown image. Tater
supports Athena factory installation, setup, native display and audio, camera,
privacy controls, recovery, and application OTA updates. The validated hardware
contract and ongoing regression checklist are recorded below.

## Platform foundation

- Unlock and TWRP: [saiyaku-athena on XDA](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-8-2nd-gen-2021-athena.4804958/), from Fire OS 7080 / PS7574.1.7080N.
- Linux platform: [Tater Echo Linux](https://github.com/TaterTotterson/Tater-Echo-Linux),
  based on the MIT-licensed TECHO5 Athena work.
- Pinned Tater Echo Linux source commit:
  `3a0c2d480e281226d28c831730abc3301d605ba4` (compatible platform release
  `v1.6.4`).
- Base rootfs SHA-256:
  `cce1d383df8df280ae47605ed13111215d868e4a3d2eb6693ab1ddd62f0d48e5`.
- Mainline boot SHA-256:
  `5a0d8a151bd3bf14c80d3e779371c844481a77ac43d440db0a0e2b8b5f92a191`.

The release build layers Tater's ARMv7 daemon, renderer, camera adapter, wake
runtimes, and setup flow onto that rootfs, then uses Tater Echo Linux's Athena
store builder to produce the `system` image. The 64-bit mainline kernel is kept
as a pinned platform input rather than copied into this repository.

### USB Ethernet qualification

Athena's physical GPIO ID/VBUS path, MTU3 host/device switching, GPIO150 host
power, and pinned RTL8152 kernel path were qualified on model A8H3N2 with the
powered UCTRONICS B0B9ZLVGJ4 adapter on 2026-10-28. DHCP, Ethernet-only routing,
Wi-Fi shutdown and fallback, SSH, cold boot/reboot, USB serial/RNDIS return,
OTA, preserve-settings factory reinstall, Volume Up TWRP, and recovery return
all passed. The tested boot image SHA-256 is
`5a0d8a151bd3bf14c80d3e779371c844481a77ac43d440db0a0e2b8b5f92a191`.

## Hardware contract used by Tater

| Function | Athena path |
|---|---|
| CPU/userspace | MT8183; AArch64 kernel with ARMv7 Alpine/Tater userspace |
| Display | Mainline DRM framebuffer, 800 × 1280 portrait memory rotated to 1280 × 800 landscape |
| Backlight | `/sys/class/backlight/backlight-lcd0` with runtime max scaling |
| Ambient light | AMS TCS3400 at I²C `0x29`; mainline IIO clear/red/green/blue channels with adaptive gain and software lux conversion |
| Touch | Mainline FocalTech `generic ft5x06` (`generic ft5x06 (3e)` on the tested unit); accepts either panel-transformed landscape or portrait coordinates |
| Microphones | `hw:0,2`, 96 kHz S32_LE, four TLV320ADC5140 channels |
| Mic conversion | Independent four-channel +5.89 dB normalization, 192-tap Blackman low-pass and 6:1 decimation; healthy-channel omni plus adaptive coherent wake/turn beam at 16 kHz S16 |
| Playback | `hw:0,0`, stereo S16 at 96 kHz; Tater's 48 kHz output is upsampled 2:1 |
| Speaker | TAS5805M with Amazon tuning extracted from this unit's stock boot image |
| Buttons/privacy | Mainline `gpio-keys`; volume keys, `KEY_MICMUTE` (248), and authoritative `SW_MUTE_DEVICE` (14) latch state |
| Camera | TECHO5 V4L2 support through Tater's on-demand Room Vision helper |
| Bluetooth | Mainline Bluetooth/BlueZ path |

Hardware captures on 2026-10-29 confirmed all four microphones healthy and
within 0.4 dB. Left/right speech produced the repeatable opposing channel
groups expected from the array: channels 0/3 lead from one side and channels
1/2 lead from the other, with a measured 0.9-1.2 sample span at 16 kHz. The
remaining front/back physical placement has not been mapped, so firmware does
not report an invented compass angle.

Athena instead estimates relative arrival time directly from each active
speech batch and aligns every healthy channel into one coherent wake beam. The
search is bounded to four samples, wider than the measured array but too narrow
for the false long-lag peaks observed in diffuse room noise. Activity,
correlation, and peak-margin gates acquire in one 32 ms production batch; an
accepted wake freezes the same delay vector for the voice turn. Idle audio,
uncertain correlation, or fewer than two healthy microphones expires to the
established four-channel omni path. Coherent and omni output retain separate
AEC state. AEC uses the software speaker tap; firmware does not assume an
unverified Athena capture-loopback channel.

## Storage and installer safety

Athena's mainline kernel mounts `system` p21 as a store containing its rescue
root plus A/B rootfs slots. `userdata` p23 remains the persistent state volume.
The installer also writes `boot` p20 after filling its reserved amp-tuning
slot. It validates the GPT layout, panel, boot header, tuning hash, and image
checksums before the first write, then reads each written partition back. A
factory reinstall preserves Tater pairing, Wi-Fi, SSH, and satellite settings
by default while removing stale application OTA slots; `--factory-reset`
clears Tater setup instead.

The installer never writes TWRP (`recovery`), `vendor`, the bootloader, TEE,
preloader, or saiyaku's real partitions. Keep `backups/<serial>` private and
permanent: its original Fire OS boot image is both the rollback source and the
source of that unit's speaker tuning.

### Boot branding

Athena's first Amazon wordmark is compiled into `lk_real`, a protected part of
the saiyaku unlock chain. Tater never patches it, because a bad `lk` write can
prevent both normal boot and TWRP. A brief Amazon frame can therefore remain
immediately after power-on.

The factory-built `system` store instead contains a static framebuffer helper
and Tater's 315 x 170 monochrome mark. Early Linux paints that mark over the
preserved bootloader framebuffer and leaves it in place until Tater's normal
animated startup takes over. This path was verified on physical Athena hardware
on 2026-10-26. It changes only the writable `system` image; `tee1`, `tee2`,
`lk`, `lk2`, the preloader, and TWRP remain untouched.

## Hardware validation and regression checklist

Athena was accepted on model A8H3N2 with the ILI9881C/BOE panel after the
following checks. Repeat them after changes to the kernel, display, audio,
installer, recovery, or OTA paths:

1. Run `./install.sh --verify-bundle` and `./install.sh --dry-run`; confirm the
   exact partitions, tested panel, and known speaker-tuning hash.
2. Complete the factory install with both power and USB stable. Confirm TWRP
   still boots, the private backup can be read, and the early Tater splash
   appears after the protected power-on frame without modifying the protected
   boot chain.
3. Complete `Tater-Setup-XXXX`; verify pairing, Wi-Fi reconnect, reboot, and a
   deliberate loss/recovery of the access point.
4. Check every screen edge and corner, rotation, backlight levels, idle wake,
   dashboard surfaces, camera views, Sat Chat, and the on-screen controls.
5. Record all four microphone channels at 96 kHz. Verify polarity, gain,
   clipping, the measured 0/3-versus-1/2 left/right delay signature, adaptive
   wake acquisition, locked-turn STT quality, idle omni fallback, and
   single-microphone degradation. Repeat a complete physical tap/angle map
   before adding compass-bearing reporting.
6. Exercise speaker startup, silence, full volume, long music playback,
   announcements, AEC/barge-in, and underrun recovery. Stop immediately for a
   pop, DC offset, overheating, or unexpected limiter behavior.
7. Verify volume and physical privacy controls, including booting both muted
   and unmuted, and confirm `SW_MUTE_DEVICE`, the hardware camera/microphone
   cutout, the screen, capture gate, persistence, and controller report agree.
8. Test passive BLE presence, active GATT, Room Vision, a complete application
   OTA health commit, a rejected bundle, automatic rollback, Tater Recovery,
   and the TWRP fallback.
9. Power-cycle repeatedly and confirm no dark-panel boot, rootfs trial loop,
   setup-mode reboot loop, or failure to return from recovery.
10. Recheck USB Ethernet enumeration, DHCP/default route, Wi-Fi shutdown and
    fallback, USB serial/RNDIS restoration, and SSH after any MTU3, GPIO,
    kernel, installer, or network-controller change.

The two panel variants tested by upstream are ILI9881C with BOE glass and
JD9365 with KD/HSD glass. The factory installer refuses other variants unless
`--force-panel` is explicitly supplied; that override is a bring-up decision,
not a compatibility guarantee.
