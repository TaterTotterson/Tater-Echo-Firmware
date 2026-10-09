# Echo 2 (`radar`) hardware notes

These are the facts used by the experimental 2017 Echo 2 port. They separate
what was read from a live unit from what is inherited from another project, so
the first Tater hardware bring-up has concrete values to compare against.

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
  factory installer therefore identifies the kernel architecture from each
  boot image, selects Radar's tested Fire OS 6 ARM donor, and preserves the
  valid Fire OS 5 pair as recovery rather than mixing their slots.
- The final factory packer was exercised locally against copies of both real
  boot slots. It produced valid Android boot images of 7,981,056 bytes (ARM)
  and 7,882,752 bytes (AArch64), each well below the 16 MiB slot. Nothing was
  flashed back to the unit.

The factory image records all resolved block targets on its own kernel command
line: `emos.system=`, `emos.data=`, `emos.boot=`, and `emos.cache=`. Legacy
Biscuit numbers remain fallbacks for old images, not assumptions made by a new
Radar installation.

## Audio

The live kernel exposes card 0 as `mt-snd-card` with:

- playback: `pcmC0D23p`, `TLV320AIC3204 Playback`, device node 116:37;
- capture: `pcmC0D24c`, `TLV320AIC3101 Capture`, device node 116:38;
- four ADC pairs A-D, matching the shared Puffin seven-microphone frontend and
  two loopback channels used by Biscuit.

Fire OS 6 exposes every target-specific mixer control by the name used in the
firmware: `Speaker_Amp_Switch`, `Ext_Speaker_Amp_Switch`, `MFP Gpio Mute`,
`Headphone_Speaker_Mux`, `Audio_DacMux_Setting`, `Right Channel Only`, and the
HPL/HPR plus ADC DIF1 route switches. `biquad coefficients` is a 117-byte ALSA
byte control and read back as all zeroes before userspace initialized it. The
port programs the measured Radar speaker filter atomically before opening the
analog path, then enables the two amplifier switches only after the DAC is
clocking silence.

The route values and coefficient blob follow the independently hardware-tested
[`radar` branch of echolocal](https://github.com/vithurshanselvarajah/echolocal/tree/radar).
Tater's complete audio/output-chain behavior still needs its own listening and
thermal validation, so initial testing should start at low volume.

## Privacy LED and inputs

Fire OS 6 owns the privacy line through
`/sys/devices/soc/10010000.keypad/amz_privacy`. The red mic-off LED is
`privacy_brightness` with inverted polarity: `0` is bright and `1` is off.
Using Biscuit's raw GPIO export on this kernel would fight the keypad driver.

The input nodes match the existing emOS device table: ACCDET is event0,
`mtk-kpd` is event1, and `keys` is event2. The final key mapping remains part
of the physical Tater bring-up rather than a claim made from node numbering.

## Unlock and recovery boundary

The required physical USB pads, external-power requirement, Fire OS procedure,
and TWRP entry sequence belong to the
[amonet-radar v1.0.0 guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-2nd-gen-2017-radar.4801290/).
The Tater installer begins only after that procedure, resolves partitions by
name, saves a device-private stock boot image, verifies every write by reading
it back, and leaves TWRP in recovery.

Radar does not provide a dependable hold-button shortcut for recovery. From a
normal emOS boot, the USB-console command `/init recovery` is the dependable
route back to TWRP.
