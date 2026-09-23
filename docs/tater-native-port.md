# Tater-native Echo port

## Goal

Turn a rooted Amazon Echo Dot 2nd Generation into a first-class Tater-native
satellite while retaining the hardware work already proven by EchoMuse. The
device should behave like the Linux satellites from the server's point of
view: local wake detection, direct Tater voice transport, local playback,
timers, state reporting, and managed updates.

This is a derivative, not a blind rewrite. EchoMuse remains the hardware
foundation and is tracked as the `upstream` Git remote.

## Target architecture

```text
Echo 7-mic capture
    -> selected mic + fixed pre-truncation gain
    -> hardware-reference AEC
    -> mono 16 kHz S16 PCM
         |-> TFLM microfrontend -> microWakeWord model -> wake event
         |-> 2-second PCM pre-roll ring
         |-> 3-second trainer/close-miss capture ring
         `-> Tater-native voice stream after wake acknowledgement

Tater server -> response audio / timer events / LED state -> Echo hardware
```

The wake engine consumes exactly the same post-AEC PCM that is sent for ASR.
That avoids training/runtime skew and lets us compare device scores with
captured audio. Inference must run off the microphone goroutine: the capture
path may enqueue or drop work, but it must never wait for the model.

## microWakeWord contract

The initial runtime target is the model format already produced by
`Tater-Wake-Words`:

- 16 kHz mono PCM
- 30 ms feature window, advanced every 10 ms
- 40 microfrontend features
- two feature frames per model invocation
- int8 `tflite_stream_state_internal_quant` model
- resource-variable state retained across invocations
- manifest threshold and sliding-window settings, with `tater_native`
  overrides when present

The Go package in `device/internal/wakeword/microwakeword` owns this contract.
It deliberately does not depend on Android APIs. The native implementation
uses TFLite Micro and the TFLM microfrontend behind a versioned C ABI, built
for the existing API-22/ARMv7 target and loaded by Go through `dlopen`. The
runtime carries its C++ library statically; absence or an ABI mismatch disables
local wake instead of preventing the firmware from booting. This keeps the
firmware as one native service rather than adding an APK, Binder bridge,
foreground service, or a second microphone owner.

The current `hey_tater` model reports a two-frame input stride and uses 42,896
arena bytes in the pinned host runtime. The Go boundary provides a 64 KiB
minimum arena, preserving useful headroom over the model manifest's embedded
target hint.

Host CI locks the frontend and model behavior to an independent golden vector.
It compares every raw 40-bin feature frame with `pymicro-features` 2.0.2,
compares the end-to-end PCM score stream, and drives a deterministic 400-frame
feature probe through both TFLM and LiteRT 2.2.0 builtin reference kernels. The
same PCM is also replayed with irregular chunk sizes to prove capture buffer
boundaries cannot alter features or scores.

The Home Assistant Android microWakeWord engine is useful implementation prior
art for the C++ runtime and build pins, but any imported code must retain its
Apache-2.0 attribution and be reviewed as third-party code before landing.

## Wake-engine bring-up

The detector can still run as an observational EchoMuse shadow, but direct
Tater mode uses the same scorer actively. A dedicated inference goroutine
consumes the same 80 ms post-AEC, post-processor PCM frames sent to ASR. The
microphone goroutine only copies and attempts to enqueue a frame; a full
eight-frame queue drops and counts work rather than waiting for the model. A
dropped discontinuity resets the streaming frontend/model state before scoring
resumes.

Install these three files together on the Echo:

```text
/data/local/share/tater/microwakeword/
  libtater_microwakeword.so
  hey_tater.json
  hey_tater.tflite
```

The ARMv7 library is produced by `device/build_microwakeword_runtime.sh`. The
checked-in manifest is
`device/internal/wakeword/microwakeword/models/hey_tater.json`; its paired model
is the SHA-pinned download used by `device/test_microwakeword_runtime.sh`.
Enable the observer before starting the firmware with:

```text
MWW_SHADOW_ENABLED=true
MWW_MODEL=hey_tater
```

`TATER_MWW_DIR` overrides the package directory. `MWW_THRESHOLD` may override
the calibrated manifest threshold; leave it unset or set it to `0` to use the
manifest value.

The existing 30-second stats message gains an `mwwShadow` object with score
count, maximum raw probability, maximum five-score window average, crossings,
close misses, queue drops, resets, errors, and inference/queue timing. An
immediate `mww_shadow_cross` report is sent only when the controller advertises
that capability. In direct native mode a crossing starts a turn; in legacy
mode it remains observational unless the inherited on-device-wake option is
enabled.

## amonet 2.0.0 and emOS

The controlled hardware test targets **amonet-biscuit v2.0.0**, the FireOS 6
kernel, and emOS. The inherited v2 support is part of this fork:

- emOS builds a 32-bit ARM init after inspecting the Dot's own boot image.
- The v2 bootloader starts `boot_a`, so the provisioning plan always installs
  emOS there.
- If the only stock image is in slot A, it is copied to slot B and verified
  before slot A is touched.
- The stock boot image exported by the provisioning flow is both the build
  input and the recovery image. Keep it somewhere other than the Dot.
- The Echo firmware and microWakeWord runtime remain ARMv7/API-22 binaries and
  run unchanged under the 32-bit emOS environment.

A complete emOS boot image cannot be prepared in advance: it contains the
kernel and device trees escrowed from that individual Dot. After the amonet
unlock reaches TWRP, the provisioning flow reads the stock image, builds the
matching emOS image locally, verifies the preserved recovery copy, and only
then writes slot A. Do not flash the standalone Tater test bundle as a boot
image; it contains userspace files installed after emOS is running.

## Configure direct Tater mode

### First-boot setup hotspot

The Tater Echo build installs `/data/local/etc/tater/setup_enabled`. When emOS
finds that marker without both a complete private Wi-Fi configuration and a
native Tater configuration, it starts an open setup network named
`Tater-Setup-XXXX`. Connect a phone or computer and use the captive portal (or
open `http://192.168.4.1`) to enter:

- the 2.4 GHz Wi-Fi SSID and password;
- the Tater LAN URL;
- the six-digit code from **Satellites → Add Satellite**;
- the Echo's display name and room.

The portal writes `/data/emos/wpa.conf` and the native bootstrap atomically,
removes any credential belonging to an earlier pairing, and asks emOS PID 1 to
perform its normal synced, read-only-remount reboot. The next boot joins the
home network, redeems the code, and stores the permanent device token. Generic
EchoMuse/emOS installs without the marker retain the USB `em-wifi` flow.

### Return to setup mode

The native Echo uses the same physical recovery gesture as the ESP
satellites:

1. Press the action button five times quickly.
2. On the sixth press, keep holding for five seconds.
3. The ring shows click progress and then drains through the hold countdown.
4. A green ring and the same bundled confirmation sound as the ESP firmware
   confirm the reset; the Echo reboots into the white setup state and
   advertises `Tater-Setup-XXXX` again.

This removes the private emOS Wi-Fi file, native bootstrap, and permanent
device token. It does not remove or roll back the firmware. The same reset
path handles Tater's **Enter setup mode** command, so remote and physical
recovery cannot drift apart.

Create `/data/local/etc/tater/native.json` on the Echo:

```json
{
  "url": "http://tater.local:8501",
  "token": "123456",
  "device_name": "Kitchen Echo",
  "room": "Kitchen"
}
```

`token` may be a six-digit pairing code from Tater. After the first accepted
hello, the permanent device credential is written atomically with mode `0600`
to `/data/local/etc/tater/device_token`; it takes precedence on later boots.
`TATER_NATIVE_CONFIG` can select another JSON file. Environment variables
`TATER_NATIVE_URL`, `TATER_TOKEN`, `TATER_TOKEN_PATH`, `TATER_DEVICE_NAME`, and
`TATER_ROOM` override individual fields. With no configured URL, the firmware
runs the inherited EchoMuse controller mode unchanged.

The model manifest, model, and ARMv7 runtime listed above must be installed
before local wake can start. The action button still provides a useful first
voice-turn test if those assets are not present yet.

## Implemented native features

- Pairing code redemption and permanent token persistence.
- Active local microWakeWord, two-second pre-roll, `voice.start.ack`, and raw
  16 kHz PCM streaming.
- Built-in `hey_tater` plus bounded, validated, atomically cached custom
  microWakeWord manifest/model URLs from Tater's trainer/catalog workflow.
  Tater model revisions invalidate the URL cache live, and the manifest's real
  wake phrase is reported with the turn instead of being hard-coded.
- The same sensitivity and room profiles as the ESP32 satellites, including
  strict/far-field acceptance policy and mandatory STT verification for the
  TV-nearby profile.
- Hold-to-intercom action-button turns, physical setup recovery,
  continued-chat reopen, barge-in, mute sovereignty, and volume reporting.
- Optional observe/enforce second-STT wake verification using Tater's `TWV1`
  PCM packet, with bounded capture, timeout fail-open, and verifier telemetry.
- Optional good-wake and debounced close-miss uploads to the configured Tater
  trainer, using the existing raw-PCM upload contract and `trainer.local`
  resolution through the paired Tater host.
- Live built-in/custom wake-sound selection. Sounds are downloaded away from
  the audio loop, decoded and cached on-device, played locally without delaying
  `voice.start`, and suppressed during reply barge-in.
- Tater listening/thinking/tool/speaking/idle states on the local LED engine.
- TTS/announcement WAV and MP3 URL playback at the Echo's 48 kHz wire rate.
- Persistent media start/stop/pause/resume/volume/loop and response ducking.
- Tater audio-session v4 synchronized playback: prepare/commit starts on the
  Echo's monotonic clock, left/right/mono source routing for stereo pairs,
  one-second DAC-consumption playhead telemetry with hardware-buffer latency,
  gradual rate-slew correction, and underrun/rejoin telemetry. Media is
  decoded into a bounded disk-backed PCM spool while it downloads, so long
  tracks do not expand into song-sized RAM allocations. This enables
  Echo-to-Echo and mixed native-satellite music groups through Tater.
- Synchronized TTS overlays over persistent music with local ducking and
  `audio.overlay.started`/`finished` lifecycle events. Tater's buffered audio
  scenes use the same background session plus overlay path; the direct
  `audio.scene.start` compatibility command is also supported. Duck attack,
  release, and final background fade are sample-counted in the DAC mixer, so
  the requested millisecond duration is preserved across ALSA buffer
  boundaries instead of being rounded to a fixed-period fade.
- Multiple local timers with arm/list/cancel/clear/snooze/ring events.
- Live wake threshold/window/close-miss tuning, AEC, barge-in, volume, LED
  color/brightness, and animation settings (the Echo maps Tater's richer
  animation names onto its local ring renderer).
- OTA download size/SHA-256 verification, inactive-slot install, atomic slot
  flip, restart, and supervisor rollback on a bad binary.
- Five-second status heartbeats with volume, mute, timers, wake engine,
  connection state, uptime, and audio-drop counters.

Motion is not advertised by this Echo client. Audio scenes, synchronized TTS
overlays, gradual rate slewing, and underrun rejoin are advertised only because
the corresponding local implementations and protocol regression tests are
present. The decoder caps compressed input at 64 MiB and decoded PCM spools at
256 MiB; temporary spools are removed when a session finishes or is stopped.

## Test sequence

Host and cross-build checks:

```bash
cd device
go test -race ./internal/actionbutton ./internal/taternative ./internal/wakeword/microwakeword ./internal/config
./test_microwakeword_runtime.sh
./compile.sh
./build_microwakeword_runtime.sh
./package_tater_native_test.sh
```

The packaging command produces a checksummed test directory and `.tar.gz`
containing the ARM firmware, ARMv7 runtime, pinned model/manifest, and a
bootstrap JSON template. It does not contain a permanent device credential.

For an on-device transport/rejoin check that cannot make audible output, start
the all-zero diagnostic stream on the Tater host:

```bash
python3 device/scripts/silent_stream_server.py --port 8765
```

`/stall.wav` sends a bounded prebuffer, deliberately stalls for eight seconds,
then resumes. Start it through Tater at volume zero and confirm the Echo reports
one underrun followed by one rejoin and returns to `rebuffering: false`.
`/short.wav` and `/background.wav` support silent overlay/scene stress checks.
The PCM samples themselves are all zero as a second safety layer.

On one test Echo, install the firmware, native runtime/model/manifest, and the
bootstrap JSON after the amonet v2/emOS provisioning flow has completed. Then
verify in this order:

1. Pair/connect and confirm the device appears as `native:<serial>` in Tater.
2. Hold the action button, speak an intercom message, and release; confirm it
   is broadcast through Tater.
3. Say “Hey Tater”; verify the listening ring moves immediately and the first
   command word is present in STT.
4. Wake during TTS to verify barge-in, then wake while music is playing to
   verify duck/restore.
5. Exercise wake-verifier observe and enforce modes, including one rejection
   and one server-unavailable timeout to confirm fail-open behavior.
6. Change the wake word, revision, sound, sensitivity/environment, and all four
   LED animations live; verify no service/device reboot is needed.
7. Enable good-wake and close-miss trainer capture and confirm both raw clips
   arrive with the Echo name, wake phrase, score, and event type.
8. Change volume and wake/AEC settings in Tater and reboot to verify persistence.
9. Create, cancel, snooze, and allow a timer to ring; stop it locally.
10. Play WAV and MP3 media; exercise pause/resume/volume/loop.
11. Press the action button five times, hold the sixth press for five seconds,
    and confirm the Echo returns to `Tater-Setup-XXXX` without USB recovery.
11. Mute during idle and during a turn; confirm no PCM leaves the device and the
   red hardware mute indicators survive reconnect/reboot.
12. Run a native OTA and deliberately test a non-starting build once to confirm
   the supervisor returns to the previous slot.
13. Leave shadow telemetry running for an extended room trial and record CPU,
    RSS, temperature, false accepts/hour, recall, and first-word retention.

## Wake-to-stream sequencing

The pre-roll buffer is part of correctness, not an optimization:

1. Continuously retain the newest two seconds of post-AEC PCM.
2. When the local detector crosses its threshold, emit `voice.start` with the
   wake model, score, and device monotonic timestamp.
3. Continue collecting PCM while the server arbitrates/acknowledges the wake.
4. On acceptance, flush the retained PCM in chronological order, then stream
   live PCM without a gap.
5. On rejection, clear the pending turn but keep detection running.

This prevents the wake phrase and first command word from being clipped by
network and arbitration latency. The server remains authoritative when two
satellites hear the same wake.

## Rollout phases

- [x] Preserve EchoMuse history in a separate `Tater-Echo-Firmware` checkout.
- [x] Track EchoMuse as `upstream` instead of treating it as our publish
  remote.
- [x] Add host-tested Tater model-manifest validation and a PCM pre-roll ring.
- [x] Build a SHA-pinned TFLite Micro runtime and microfrontend for API 22
  ARMv7, behind a versioned, runtime-loaded C ABI.
- [x] Add golden-vector tests that compare native features and scores with the
  trainer/runtime reference.
- [x] Feed the detector from the existing post-AEC microphone path in shadow
  mode and report scores without triggering turns.
- [x] Enable active local wake with acknowledgement and pre-roll replay.
- [x] Implement the Tater-native control/audio protocol and capability
  negotiation.
- [x] Add local URL/audio playback, timer events, pairing/provisioning, and
  hash-verified A/B OTA with automatic rollback.
- [x] Merge amonet v2.0.0/FireOS 6 emOS support, including the verified
  slot-A install and stock-image preservation path.
- [ ] Measure CPU, memory, thermal behavior, false accepts per hour, recall,
  and first-word retention on real hardware before an installable release.

## Guardrails

- Keep controller-side/openWakeWord behavior available until local detection
  passes an extended shadow run on real devices.
- Unknown protocol fields and capabilities must degrade safely for mixed
  firmware fleets.
- Never block capture, AEC, or speaker timing on inference or networking.
- Treat model/config files as untrusted input: no path traversal, unsupported
  frontend shapes, or unbounded tensor arenas.
- Do not alter the pinned FireOS compiler image without an on-device test.
- Before the first published fork release, choose the canonical GitHub URL and
  update the inherited Go module/import path and release metadata together.
