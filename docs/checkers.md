# Echo Show 5 first generation (`checkers`)

## Current test boundary

The Checkers target is a screen and hardware-bring-up preview. The release
installs the Tater Show APK on rooted stock Fire OS 6 and collects the original
hardware data needed to bind the native satellite to this board. It does not
write a partition, replace Fire OS, or enable the native audio daemon.

That boundary is deliberate. Checkers and Biscuit share an MT8163 family but
do not share stable ALSA device numbers, mixer routes, input enumeration,
microphone topology, display hardware, or boot layout. The first profile must
resolve each device by name and the microphone probe must establish the real
channel order before Tater can take exclusive ownership.

## Baseline

- Device: Amazon Echo Show 5 first generation (2019), codename `checkers`.
- Unlock: amonet 2.0.1 or newer, with TWRP retained.
- Profiling userspace: rooted stock Fire OS 6 / Android 7.1.2; `su` root is
  supported and root adbd is not required.
- Later native test userspace: unofficial LineageOS 18.1 / Android 11.
- RAM: the amonet 2.x layout exposes the full 2 GB; amonet 1.x exposes 1 GB.
- Installer hosts: macOS and Linux, using Python 3 and `adb`.

The unlock remains the device owner's separate prerequisite:
[XDA Checkers unlock/root/TWRP guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-5-1st-gen-2019-checkers.4762900/).
The Lineage hardware work used for comparison is documented in
[lineageos-echo-show-camera](https://github.com/jxlarrea/lineageos-echo-show-camera).

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
`screen.ready`, `mute.toggle`, `volume.delta`, `intercom.start`, and
`intercom.stop`. The listener binds loopback only; no screen-control port is
available on the LAN.

## Bring-up sequence

1. Unlock and root stock Fire OS with amonet 2.0.1+, then confirm TWRP remains
   bootable. Do not replace Fire OS yet.
2. Run the Checkers factory preview with `./install.sh --demo --profile`.
3. Review and retain the generated profile archive.
4. Run the interactive mic/audio probe while still on rooted stock Fire OS.
5. Install LineageOS 18.1 only after the original hardware evidence is saved.
6. Add named Checkers speaker, capture, buttons, mute/privacy, display, and
   thermal bindings with fixture-backed regression tests.
7. Install the native service without enabling it at boot; exercise capture
   and playback manually and inspect logs.
8. Enable the supervised service, Tater pairing, wake word, AEC, barge-in,
   media synchronization, screen state, and OTA in that order.
9. Mark Checkers hardware-tested and enable coordinated native/APK OTA only
   after rollback has been exercised on the device.

## APK signing and OTA

Preview workflows build the Android debug-signed APK. It is suitable for the
first local hardware tests but is not the permanent update identity. Before
Checkers OTA is enabled, releases need a protected, stable signing key and a
coordinated transaction that stages both the inactive native service slot and
the APK, verifies both, launches the new pair, and restores both components on
a failed health check.
