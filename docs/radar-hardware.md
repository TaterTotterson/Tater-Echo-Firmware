# Echo 2 (`radar`) hardware notes

These are the hardware facts used by the supported 2017 Echo 2 Tater Linux
target. Installation instructions live in
[`factory/dot-linux/README.md`](../factory/dot-linux/README.md).

## Live read-only validation

Measured in TWRP 3.7.0 on 2026-10-09. No partition or mixer control was
written.

- Product: `radar` / `radar_puffin`; model XC56PY; IDME device type
  `A7WXQPH584YP`; Fire OS 6 userspace ABI `armeabi-v7a`.
- The amonet-radar updater's restored GPT exposes canonical by-name slots and
  no `_x` aliases: `boot_a` p10, `boot_b` p11, recovery p12, `system_a` p13,
  `system_b` p14, cache p15, and userdata p16.
- Both boot partitions are 16 MiB. The measured unit retained an ARM Fire OS 6
  kernel/system pair in slot A and an AArch64 Fire OS 5 pair in slot B. The
  Tater Linux installer leaves both stock system slots intact and uses a pinned,
  Radar-specific ARM Linux kernel in recovery.
- The MediaTek firmware and audio calibration used by the Linux driver are in
  Fire OS 6 `system_a` on the measured unit (`system/vendor/...`). The older
  `system_b` exposes a different `etc/...`/`vendor/...` layout. First boot
  probes both read-only and adopts from the board-preferred usable slot.
- The installer builds the recovery image locally from the unit's own TWRP
  header and the pinned Radar kernel, verifies the board identity, checks the
  partition size, writes recovery, and reads it back before rebooting.

The Linux rootfs uses A/B slots under `/cache/techo5`. Persistent Tater state
and routine application OTA slots live under `/data/tater-linux`; no Android
userspace or legacy slot command-line contract is involved in normal boot.

## Audio

The live kernel exposes card 0 as `mt-snd-card` with:

- playback: `pcmC0D23p`, `TLV320AIC3204 Playback`, device node 116:37;
- capture: `pcmC0D24c`, `TLV320AIC3101 Capture`, device node 116:38;
- four ADC pairs A-D, matching the shared Puffin seven-microphone frontend and
  two loopback channels used by Biscuit.

The retained vendor kernel exposes every target-specific mixer control by the
name used in the firmware: `Speaker_Amp_Switch`, `Ext_Speaker_Amp_Switch`,
`MFP Gpio Mute`,
`Headphone_Speaker_Mux`, `Audio_DacMux_Setting`, `Right Channel Only`, and the
HPL/HPR plus ADC DIF1 route switches. `biquad coefficients` is a 117-byte ALSA
byte control and read back as all zeroes before userspace initialized it. Tater
programs flat codec biquads atomically before opening the analog path, starts
the DAC on silence, and releases the external amplifier gate last. EQ, bass
protection, and limiting then run in the same native software output chain used
by the screen targets. The route, gain, mute, and full-volume playback path have
been validated on physical Radar hardware.

## Privacy LED and inputs

Fire OS 6 owns the privacy line through
`/sys/devices/soc/10010000.keypad/amz_privacy`. The red mic-off LED is
`privacy_brightness` with inverted polarity: `0` is bright and `1` is off.
Using Biscuit's raw GPIO export on this kernel would fight the keypad driver.

The measured input nodes are ACCDET on event0,
`mtk-kpd` is event1, and `keys` is event2. The final key mapping remains part
of the physical Tater bring-up rather than a claim made from node numbering.

## Unlock and recovery boundary

The required physical USB pads, external-power requirement, Fire OS procedure,
and TWRP entry sequence belong to the
[amonet-radar v1.0.0 guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-2nd-gen-2017-radar.4801290/).
The Tater installer begins only after that procedure, resolves partitions by
name, saves device-private boot-critical backups, verifies every write by
reading it back, and stores a restorable TWRP image at
`/cache/techo5/twrp.img` before Linux takes over recovery.

Radar has no dependable bootloader-level hold-button shortcut. Once the current
factory archive is installed, holding Volume Up for five seconds during the
first 90 seconds of startup enters a device-specific TWRP handoff. It restores
Linux to the shared recovery partition while TWRP remains active in RAM, so the
next reboot returns to Linux. An install without the handoff image continues
normal startup instead of entering Tater Recovery. The routine recovery request
remains available explicitly over USB with `python3 to_recovery.py`; run
`install.sh --repair` there to repair the inactive root slot. `to_twrp.py`
remains the explicit lower-level TWRP route.

The station interface `wlan0` does not accept an nl80211 change to AP type.
The vendor `/dev/wmtWifi` command `A` starts its dedicated AP-capable wiphy and
creates `ap0`; the Tater setup hotspot runs on that interface.
