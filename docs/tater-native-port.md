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

## Configure direct Tater mode

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
- Action-button turns, continued-chat reopen, barge-in, mute sovereignty, and
  volume reporting.
- Optional observe/enforce second-STT wake verification using Tater's `TWV1`
  PCM packet, with bounded capture, timeout fail-open, and verifier telemetry.
- Tater listening/thinking/tool/speaking/idle states on the local LED engine.
- TTS/announcement WAV and MP3 URL playback at the Echo's 48 kHz wire rate.
- Persistent media start/stop/pause/resume/volume/loop and response ducking.
- Multiple local timers with arm/list/cancel/clear/snooze/ring events.
- Live wake threshold/window/close-miss tuning, AEC, barge-in, volume, LED
  color/brightness, and animation settings (the Echo maps Tater's richer
  animation names onto its local ring renderer).
- OTA download size/SHA-256 verification, inactive-slot install, atomic slot
  flip, restart, and supervisor rollback on a bad binary.
- Five-second status heartbeats with volume, mute, timers, wake engine,
  connection state, uptime, and audio-drop counters.

Synchronized stereo/group playback, audio-scene mixing, TTS overlays, and
motion are not advertised by this Echo client. Tater therefore falls back to
the supported single-device playback path instead of sending commands the
hardware client cannot honor.

## Test sequence

Host and cross-build checks:

```bash
cd device
go test -race ./internal/taternative ./internal/wakeword/microwakeword ./internal/config
./test_microwakeword_runtime.sh
./compile.sh
./build_microwakeword_runtime.sh
./package_tater_native_test.sh
```

The packaging command produces a checksummed test directory and `.tar.gz`
containing the ARM firmware, ARMv7 runtime, pinned model/manifest, and a
bootstrap JSON template. It does not contain a permanent device credential.

On one test Echo, install the firmware, native runtime/model/manifest, and the
bootstrap JSON. Then verify in this order:

1. Pair/connect and confirm the device appears as `native:<serial>` in Tater.
2. Press the action button and complete one voice turn.
3. Say “Hey Tater”; verify the listening ring moves immediately and the first
   command word is present in STT.
4. Wake during TTS to verify barge-in, then wake while music is playing to
   verify duck/restore.
5. Exercise wake-verifier observe and enforce modes, including one rejection
   and one server-unavailable timeout to confirm fail-open behavior.
6. Change volume and wake/AEC settings in Tater and reboot to verify persistence.
7. Create, cancel, snooze, and allow a timer to ring; stop it locally.
8. Play WAV and MP3 media; exercise pause/resume/volume/loop.
9. Mute during idle and during a turn; confirm no PCM leaves the device and the
   red hardware mute indicators survive reconnect/reboot.
10. Run a native OTA and deliberately test a non-starting build once to confirm
   the supervisor returns to the previous slot.
11. Leave shadow telemetry running for an extended room trial and record CPU,
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
