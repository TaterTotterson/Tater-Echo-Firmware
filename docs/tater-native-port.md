# Tater-native Echo architecture

Tater Echo Firmware makes supported Echo hardware behave like the other
Tater-native satellites: wake detection and audio handling run locally, while
Tater remains the authority for turns, settings, groups, media, and updates.

## Runtime layout

```text
Echo microphones
  -> target-specific capture and beam selection
  -> echo cancellation and signal conditioning
  -> 16 kHz mono PCM
       |-> microWakeWord -> wake event
       |-> pre-roll and trainer rings
       `-> Tater voice stream after acknowledgement

Tater
  -> reply/media audio, timers, settings, LED/display state, and OTA
  -> Echo speaker, controls, LED ring, or Checkers screen
```

Microphone capture never waits on inference or networking. Wake inference runs
on its own bounded queue; a discontinuity resets the streaming frontend before
scoring continues. The wake engine receives the same post-AEC PCM sent to Tater
for speech recognition, preventing training/runtime audio skew.

## Wake engine

The runtime accepts the model format produced by `Tater-Wake-Words`:

- 16 kHz mono PCM;
- 30 ms feature windows advanced every 10 ms;
- 40 microfrontend features;
- two feature frames per model invocation;
- retained int8 streaming state; and
- manifest thresholds and sliding-window policy, including `tater_native`
  overrides.

`device/internal/wakeword/microwakeword` validates that contract. The native
TFLite Micro runtime is an ARM library loaded through a versioned C ABI. A
missing or incompatible model/runtime disables local wake without preventing
the rest of the satellite from booting.

Each installation carries:

```text
/data/local/share/tater/microwakeword/
  libtater_microwakeword.so
  hey_tater.json
  hey_tater.tflite
```

Wake models, sounds, sensitivity, verification policy, and trainer behavior can
change live from Tater. A two-second pre-roll protects the first command word
while the server acknowledges and arbitrates a wake heard by multiple rooms.

## Target integrations

### Biscuit

Biscuit boots emOS from slot A and preserves stock recovery material in slot B.
The factory installer reads the attached device's own stock boot partition,
detects its kernel architecture, builds the matching image locally, verifies a
byte-for-byte packer round trip, and reads back partition writes before reboot.
No Amazon kernel or device tree is included in a release.

The seven-mic capture, hardware playback reference, direction selection, and
audio boundaries are documented in
[`biscuit-hardware.md`](biscuit-hardware.md).

### Checkers

Checkers 2.x runs a native Alpine rootfs on the pinned TECHO5 kernel/rescue
platform. Tater owns audio and BLE directly; `tater-show` owns framebuffer and
touch, and a loopback-only camera helper owns the MediaTek ISP. The former
system partition is an A/B rootfs store, persistent Tater state remains on
userdata, and TWRP stays in recovery.

See [`checkers.md`](checkers.md) and
[`../factory/checkers-linux/README.md`](../factory/checkers-linux/README.md).

## Setup and credentials

An unconfigured Biscuit starts `Tater-Setup-XXXX`. Its captive portal stores
Wi-Fi and a one-time Tater pairing code. A fresh Checkers does the same after
USB installation; USB pairing is also available. After the first accepted hello,
the permanent device credential is written atomically with mode `0600` and
used on later boots.

The native bootstrap lives under `/data/local/etc/tater`. Biscuit can return to
setup using five quick action-button presses followed by holding the sixth for
five seconds. Checkers keeps its pairing across rootfs slots.

## Audio and device behavior

The native protocol supports:

- local wake, pre-roll, continued conversation, barge-in, intercom, optional
  STT wake verification, and trainer samples;
- TTS, announcements, timers, volume, mute, and live wake sounds;
- disk-backed WAV/MP3 media, stereo/group prepare and commit, monotonic
  playhead reports, gradual rate-slew correction, and underrun rejoin;
- synchronized TTS overlays, audio scenes, and sample-counted music ducking;
- Tater-selected LED/display animations and tool progress; and
- bounded BLE advertisement batches for Tater presence.

Capabilities are negotiated explicitly. Unknown fields are ignored safely so
mixed firmware versions can remain online during rollout.

## OTA

Biscuit downloads into the inactive userspace slot, verifies size, SHA-256, and
ELF format, then switches the active link atomically. Its supervisor restores
the previous slot after repeated fast startup failures.

Checkers mirrors that fast application transaction on writable `/data`: it
verifies a bundle containing the daemon and renderer, stages them together,
then switches their shared active link atomically. Repeated fast exits or a
failed health window restores the preceding application slot. The immutable
Linux rootfs changes only through USB/recovery when the platform itself changes.

The artifact contract is documented in
[`firmware-releases.md`](firmware-releases.md).

## Validation

Host tests cover the Tater protocol, wake frontend/model golden vectors, audio
state machines, OTA rollback, installer guards, emOS image packing, native
screen/input packages, Wi-Fi setup, and BLE HCI behavior. GitHub Actions
also cross-builds both targets and assembles the complete Checkers Linux rootfs.

Hardware release checks still include wake and first-word retention, barge-in,
stereo synchronization, BLE presence, physical recovery gestures, clean/cold
boots, a successful OTA, and one deliberate rollback.

## Guardrails

- Never block capture or playback timing on inference or networking.
- Treat models, configuration, downloads, and manifests as untrusted input.
- Keep partition writes target-specific, verified, and recoverable.
- Do not copy boot layouts, mixer settings, mic maps, or root images between
  Echo codenames.
- Compiler or boot-tool changes require a real-device recovery test before a
  release.
