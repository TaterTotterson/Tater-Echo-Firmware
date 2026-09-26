# Echo Show 5 first generation (`checkers`)

## Current test boundary

The Checkers target is now a complete native hardware-test build on rooted
stock Fire OS 6. The release installs the Tater Show APK, native audio daemon,
microWakeWord runtime, setup hotspot, and a reversible Magisk supervisor. It
does not write a partition or replace Fire OS. OTA treats the native daemon
and signed screen APK as one generation and rolls both back together if the
new pair cannot prove healthy.

## Baseline

- Device: Amazon Echo Show 5 first generation (2019), codename `checkers`.
- Unlock: amonet 2.0.1 or newer, with TWRP retained.
- Profiling userspace: rooted stock Fire OS 6 / Android 7.1.2; `su` root is
  supported and root adbd is not required.
- Replacement userspace under active development: unofficial LineageOS 18.1 /
  Android 11. See [`checkers-lineage.md`](checkers-lineage.md) for the exact
  verified image, backup boundary, install path, and current limitations.
- RAM: the hardware-verified Fire OS 6574.1 profile exposes 997,780 kB
  (approximately 1 GB usable). Do not size the native service from the 2 GB
  assumption that appeared in the preliminary notes.
- Installer hosts: macOS and Linux, using Python 3 and `adb`.

The unlock remains the device owner's separate prerequisite:
[XDA Checkers unlock/root/TWRP guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-5-1st-gen-2019-checkers.4762900/).
After amonet reaches TWRP, install the certified Fire OS 6574.1
`NS65741/8146` (`0013222531716`) `com.amazon.checkers.android.os` package
before rooting and retain a raw backup of its boot partition. The installer
rejects any other Fire OS incremental rather than guessing compatibility.
The forum's static root image may not match the Fire OS version originally left
on the Echo; that mismatch was confirmed to cause a recoverable Echo-logo boot
loop. The exact hardware-verified package, checksum, TWRP commands, and
recovery procedure are documented in
[`factory/checkers/README.md`](../factory/checkers/README.md).
The Lineage hardware work used for comparison is documented in
[lineageos-echo-show-camera](https://github.com/jxlarrea/lineageos-echo-show-camera).

## Hardware verified on Fire OS 6574.1

- Board identity: IDME device type `A4ZP7ZC4PI6TO` maps positively to
  `checkers`; it does not inherit Biscuit's thermal table.
- Capture: ALSA card 0, device 22 (`TLV320AIC3101 Capture`), fixed at 16 kHz,
  four channels, packed 24-bit little-endian. The two fitted ADC_A inputs are
  routed from `DIF1_L` and `DIF1_R`. Hardware captures found signal on channels
  0 and 1 and bit-exact zeroes on channels 2 and 3. The native front end now
  auto-calibrates the two live ADC levels from diffuse room sound or a
  near-equidistant coherent source, finds their live fractional inter-mic
  delay, and feeds a smoothed delay-and-sum beam into a conservative
  spatial-difference noise postfilter. A dead input falls back to the remaining
  mic without halving it. It still honestly reports no screen bearing: a
  two-mic delay supplies one axis, not an unambiguous 0-360 degree direction,
  and the screen-relative orientation has not yet been physically calibrated.
- Playback: ALSA card 0, device 23 (`RT5616_Playback`), 48 kHz stereo S16. A
  stock chime trace confirmed the RT5616 `OUT` route and the active-low
  external-speaker amplifier.
- Controls: the mic/camera-off push button is `KEY_POWER` on the input device
  named `gating`; volume up/down are standard key events on `gpio-keys`; the
  camera shutter reports `SW_CAMERA_LENS_COVER` on that same node. Checkers has
  no physical action button, so intercom uses a bottom-center press-and-hold
  screen control. Five short Volume Down presses followed by holding the sixth
  for five seconds enters setup recovery even when the APK is unavailable.
- Visuals: the only Linux LED class is `lcd-backlight`; there is no LED ring.
  Native animations therefore feed the loopback screen protocol and a discard
  ring sink, never Biscuit's I2C paths.
- Memory: 997,780 kB usable on the profiled boot.

The first target-specific ARM service builds successfully and has passed its
manual hardware checks on the real device:

- a silent health run reached `Ready`, opened capture as 4-channel
  S24_3LE/16 kHz and playback as stereo S16/48 kHz, used one AEC path, held
  about 11.7 MB resident memory, and shut down cleanly;
- a bounded native `PcmSpeaker` test audibly rendered all 12 periods with zero
  underruns, then muted the codec and disabled the active-low amplifier; and
- the production Checkers mic front end and ARM microWakeWord runtime detected
  three of three spoken “Hey Tater” phrases at 0.991, 0.998, and 0.994, with
  zero queue drops, inference errors, or clipped samples.

The two-mic processing is deliberately shallower than the controller's
optional DTLN cleanup. Coherent speech recovers to unity quickly; diffuse noise
gets about 3 dB from the spatial postfilter in addition to the natural gain
from combining two independent microphone signals. Wake-word audio therefore
benefits from the array without being hard-gated, while Tater's per-device
noise-suppression option can still apply stronger ASR-only cleanup.

The release factory archive now packages this service and starts it at boot.
First-boot Wi-Fi/Tater pairing, permanent-token redemption, screen-state
integration, reboot supervision, and Android BLE scanning have passed on the
real device. BLE advertisements are coalesced in bounded batches and forwarded
through the native Tater connection, so Checkers participates in the same room
presence system as the other satellites.

The installer also validates the Amazon privacy-firewall UID cache before it
starts setup. A Magisk `post-fs-data` hook restores all IPv4 and IPv6 rules
before Android applications start; the later maintenance pass retains a
complete live firewall instead of tearing it down. On-device validation showed
no established public Amazon connection while Tater's separate app UID kept
public access for GitHub OTA downloads.

Build that artifact explicitly; a plain `./compile.sh` intentionally remains
the Biscuit default:

```bash
cd device
TATER_FIRMWARE_TARGET=checkers ./compile.sh
```

The bounded hardware diagnostics used during bring-up live under
`device/tools/speaker_smoke` and `device/tools/mww_smoke`. Both require an
explicit `-target checkers`; the wake test retains no captured audio.

## Screen architecture

The native firmware remains responsible for microphone capture, microWakeWord,
AEC, source direction, audio playback, intercom, timers, media synchronization,
buttons, Tater communication, and OTA. `com.tatertotterson.show` is a renderer
and touch surface. Android cannot suspend or restart the wake-word engine by
recreating the Activity.

The two processes communicate through newline-delimited JSON bound to
`127.0.0.1:43821`. The protocol version is `1`. The daemon sends complete
snapshots so reconnecting never requires event replay:

```json
{
  "protocol": 1,
  "type": "snapshot",
  "phase": "listening",
  "connected": true,
  "device_name": "Family Room Show",
  "room": "Family Room",
  "muted": false,
  "volume_percent": 54,
  "audio_level": 0.18,
  "direction_degrees": 92.4,
  "timer_active": false
}
```

The APK can send only the bounded commands implemented by the daemon:
`screen.ready`, `mute.toggle`, `volume.delta`, `intercom.start`,
`intercom.stop`, and `ble.advertisements`. The listener binds loopback only;
no screen-control port is available on the LAN.

The APK separately binds a single-shot front-camera endpoint to
`127.0.0.1:43823`. It accepts only `GET /snapshot`, allows one capture at a
time, bounds the JPEG to 4 MiB, and never persists it. The daemon calls this
endpoint only for a correlated Tater `camera.snapshot` command. Tater's Room
Vision verba prefers the asking Show, otherwise uses a camera-capable Show in
the asking satellite's exact room, and never silently captures from a
different room.

## Bring-up sequence

1. Unlock with amonet 2.0.1+ and confirm TWRP remains bootable.
2. Install the certified Checkers Fire OS 6574.1 (`NS65741/8146`) package
   through TWRP, boot it once, and retain a raw stock boot backup. The factory
   installer refuses other Fire OS builds until they have passed hardware
   validation.
3. Root with an image derived from that exact Fire OS release, then confirm
   both Fire OS and TWRP remain bootable.
4. Run the Checkers factory installer with `./install.sh` (add `--profile` if
   retaining a redacted hardware profile). This also installs the reversible
   Magisk replacements for the persistent Amazon speech and Bishop account
   managers, reversible Amazon Sidewalk isolation, and the Tater
   display/framework watchdog.
5. Join `Tater-Setup-XXXX`, submit Wi-Fi and Tater pairing details, and confirm
   the Show returns to the Tater screen after its automatic reboot.
6. Exercise wake, listen/reply playback, reopen mic, intercom, media, and
   screen controls while inspecting the native service log.
7. From Tater, install a same-key Checkers release and confirm that native and
   APK versions advance together. Before publishing, exercise one deliberately
   unhealthy generation and verify that both components roll back.

## APK signing and OTA

Local debug builds use Android's debug key. Tagged releases require a protected,
stable key configured as the GitHub secrets `TATER_SHOW_KEYSTORE_BASE64`,
`TATER_SHOW_KEY_ALIAS`, `TATER_SHOW_KEY_PASSWORD`, and
`TATER_SHOW_STORE_PASSWORD`. The release workflow refuses to publish Checkers
artifacts when any secret is absent.

Android accepts an APK update only when its signing identity matches the
installed app. A Show currently running a debug-signed preview therefore needs
one USB factory reinstall with the first stable-signed release. Every later
Tater OTA can update normally as long as that release key is preserved.

The Checkers OTA bundle contains the same-version native daemon and APK plus an
inner hash manifest. Installation stages the inactive daemon slot, backs up the
currently installed APK, installs the new APK, and atomically flips the daemon
link. The supervisor waits up to 90 seconds for both a Tater connection and a
matching-version `screen.ready`; otherwise it restores the old link and APK.
