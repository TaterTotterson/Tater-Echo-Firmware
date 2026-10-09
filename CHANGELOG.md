# Changelog

## v2.5.0

### What's Changed

- Added the experimental 2017 Echo 2 (`radar`) satellite target on the shared
  MT8163/Puffin emOS path. Radar receives its own hardware identity, dual-gate
  TLV320AIC3204 speaker route, ring/BLE capabilities, factory bundle, binary
  OTA, and guarded Fire OS 6 A/B installer while retaining Biscuit's proven
  seven-microphone and on-device wake pipeline.
- Radar factory installation builds from the attached unit's own boot image,
  preserves stock in `boot_b`, and resolves `boot_a`/`boot_b` by name so the
  stale `boot_a_x`/`boot_b_x` aliases from an interrupted older unlock cannot
  be selected by the installer. The target remains marked experimental pending
  full Tater hardware validation.
- Factory packaging now validates the ELF class and machine of every emOS
  executable before producing an archive. This prevents stale host binaries,
  such as a macOS BusyBox in a local build directory, from passing checksum
  verification and reaching a Biscuit or Radar image.
- Added a capability-gated eight-band speaker equalizer, presence boost, bass
  protection, and peak limiter on Biscuit, Radar, Checkers, and Rook. The Echo
  owns this final output stage in native mode so settings are applied exactly
  once and persist without rebuilding firmware.
- Replaced the generated two-tone timer alert with the same embedded Zen
  timer-finished chime used by Tater's ESP32 satellites, including the quiet
  listening gap between repeats and an offline-safe fallback.
- Hardened Radar's codec, amplifier, headphone, mute-button, LED-ring, and
  Sendspin paths using the hardware-tested route and vendor speaker filter.
  The emOS boot path now selects the init architecture from the attached
  device's stock kernel and supports `/init recovery` from the USB console.

## v2.4.0

### What's Changed

- Added secure active Bluetooth GATT connections on Biscuit, Checkers, and
  Rook, allowing Tater services such as Meshtastic Core to use a nearby Echo's
  Bluetooth radio without a separate bridge. The native satellite link now
  supports service discovery, characteristic reads and writes, notifications,
  indications, disconnects, and explicit unpairing while preserving passive
  BLE advertisement proxying.
- Added authenticated six-digit PIN pairing and persistent BLE bonds. Pairing
  credentials stay on the satellite, the PIN is never stored, and Bluetooth
  control continues to travel over the existing authenticated Tater native
  connection rather than opening another network listener.
- Added shared-controller coordination and ordered per-device GATT workers so
  scanning pauses cleanly during connection setup, active traffic cannot be
  consumed by the passive scanner, and multiple connected devices remain
  isolated. Biscuit and Checkers use the direct HCI stack required by their
  FireOS hardware, while Rook integrates with BlueZ.
- Kept openWakeWord responsive under load by pinning its native scorers to
  stable low-numbered CPUs, retaining the freshest pending audio when the
  inference queue fills, and preserving feature history across ordinary queue
  gaps. Runtime health now reports scorer affinity, queue, recovery, and drop
  diagnostics.
- Made the selected wake package the sole source of detector calibration and
  reduced the built-in Hey Tater openWakeWord patience from three frames to
  two for quicker activation. A real microphone capture-generation change
  still performs a full state reset.
- Disabled Echo requests for Sendspin's high-rate remote visualizer stream to
  prevent music telemetry from competing with wake inference. Artwork,
  metadata, playback progress, and local playback-reactive visuals remain
  available on supported Echo displays and LEDs.
- Restored Biscuit headphone-jack output to normal volume by switching the DAC
  mux together with the jack route, and protected tinyalsa writes from signal
  interruption to prevent partial PCM buffers and intermittent clicks.
- Hardened emOS networking on FireOS 6 by requesting broadcast DHCP replies
  from `udhcpc`. If an IPv6 input-drop policy cannot be installed because the
  device lacks `ip6tables`, emOS now disables IPv6 instead of leaving inbound
  IPv6 traffic unfiltered.

## v2.3.2

### What's Changed

- Made the Echo Show 5 Sendspin audio visualizer substantially smoother by
  keeping the static now-playing artwork and details cached while redrawing
  only the live audio motion.
- Increased the visualizer's frame rate, movement range, and response speed so
  its taller spectrum bars follow music cleanly without the previous lag.

## v2.3.1

### What's Changed

- Fixed slow or unreliable Dual Wake Word detection by giving microWakeWord
  and openWakeWord the same uninterrupted primary beam. Each Echo now runs one
  MWW and one OWW detector instead of two directional copies of each, without
  resetting model state or replaying audio when the estimated direction moves.
- Wake beamforming remains active on every Echo target: Biscuit uses its
  seven-microphone array, Checkers its two-microphone pair, and Rook its
  four-microphone array. The accepted direction continues into the voice and
  STT turn, including the existing calibrated echo-cancellation path.
- Detector calibration now comes from the selected model files in every mode.
  MWW uses its manifest threshold, window, and close-miss values; OWW uses the
  standalone or confirmation threshold and patience from its wake bundle.
  Firmware no longer substitutes hard-coded Dual-mode sensitivity values.
- All v2.3.0 Sendspin playback, Checkers/Rook album artwork, now-playing
  presentation, track colors, and Biscuit reactive music LED behavior remain
  included.

## v2.3.0

### What's Changed

- Biscuit OTA updates now carry the verified wake runtime, ONNX Runtime, and
  shared openWakeWord feature models inside the A/B firmware executable. Tater
  therefore repairs older installations without requiring the Echo to reach
  GitHub directly, preventing Dual and OWW modes from remaining unavailable
  after an otherwise successful update.
- Checkers now presents Sendspin music on its right-side canvas with album
  artwork, track and artist details, playback progress, and a live spectrum in
  the existing Tater visual style. Awareness notifications and timers retain
  priority, and the screen returns to now playing or weather afterward.
- Rook gains a round-screen now-playing view designed for its available center
  space, including artwork, track information, progress, and live audio motion
  without crowding the voice and tool-call states.
- Biscuit uses Sendspin's synchronized loudness, beat, peak, spectrum, and
  track colors to drive the music LED animation the user already selected.
  The speaker's local audio level remains an automatic fallback when a
  controller does not publish visualizer data.
- Presentation changes follow Sendspin's playback clock, keeping artwork,
  metadata, progress, and reactive visuals aligned with the audio. Artwork is
  requested only by display devices, so Biscuit avoids unnecessary image
  traffic and memory use.

## v2.2.0

### What's Changed

- Tater can enable microWakeWord and openWakeWord independently. MWW-only
  preserves the original pipeline, Dual Wake Word requires both models to
  accept the same winning-beam audio, and OWW-only runs openWakeWord
  continuously before the optional STT wake check.
- Dual Wake Word keeps MWW and OWW warm in parallel on the same directional
  beam lanes and opens the microphone only when their timestamped crossings
  agree within 1.2 seconds. This preserves mandatory two-model agreement
  without replaying the wake clip after MWW or adding a second-stage delay.
- Each engine has its own built-in Hey Tater/custom source selection. Custom
  openWakeWord models install from the verified `.wake-bundle.json` produced
  by either Tater trainer. The newly trained Hey Tater MWW and OWW classifiers
  now ship together as one verified built-in profile.
- Biscuit, Checkers, and Rook factory images carry the built-in pair and shared
  OWW feature models. Ordinary daemon OTA installs repair the same signed
  profile and securely bootstrap the matching native wake runtime. Saved
  detector choices remain user-controlled, and the timer Stop wake remains
  immediate.
- Biscuit runs OWW through the ARMv7 ONNX Runtime/XNNPACK path proven on this
  hardware by EchoMuse. Live validation measured same-frame MWW/OWW agreement
  while keeping the daemon near 25% CPU and 39 MB RSS.
- Checkers and Rook use that same continuous ONNX/XNNPACK OWW pipeline through
  a reduced Linux/musl ARMv7 runtime built from pinned source. Their factory
  images and release sidecars carry the verified native runtime and ONNX
  feature models, with an emulated inference smoke test before packaging.
- Replaced the firmware-specific Tater media clock/session transport with a
  Sendspin v1 player on Biscuit, Checkers, and Rook. Every Echo now advertises
  itself to Tater, Music Assistant, and other compatible Sendspin controllers,
  accepts synchronized 48 kHz PCM or FLAC, and preserves its player identity,
  volume, delay, pairing keys, and stereo/left/right/mono output assignment.
- Removed the retired `audio.clock.sync` and `media.session.*` playback path.
  Native replies, interactive TTS, announcements, wake cues, timers, overlays,
  audio scenes, ducking, and barge-in still take priority locally and return the
  speaker cleanly to Sendspin afterward.
- Added the Echo capability contract that lets Tater and the Home Assistant
  bridge select on-device microWakeWord, openWakeWord, or Dual Wake Word without
  relying on firmware-version checks.
- Both detectors receive the same timestamped 80 ms beamformed audio chunks and
  maintain continuous inference state. Agreement is restricted to the same
  directional lane, while the winning lane's pre-roll remains available to the
  wake verifier, trainer, and STT handoff.
- Fixed looping background audio continuing after a local TTS announcement
  finishes on Biscuit, Checkers, and Rook, and made the configured background
  fade-out complete in the hardware renderer before its queued music is
  stopped.
- Added a Biscuit-only music LED setting with Off, Audio Glow, Beat Pulse,
  Level Bars, Reactive Orbit, and Reactive Wave choices. Music animations use
  live speaker levels for Tater and direct Sendspin playback while reply TTS,
  wake turns, timers, mute, volume, and connection indicators retain priority.
- Added No Animation support for Biscuit's listening, thinking, tool-call,
  replying, and music states. Disabling those optional effects does not hide
  setup, error, timer, mute, volume, OTA, or connection-status indicators.

## v2.1.0

### What's Changed

- **Echo Spot 1st generation (Rook):** first supported release, with a round native Tater display, configurable weather and room sensors, display themes, touch intercom, timers, Room Vision camera access, BLE presence, four-microphone beamforming, synchronized media, captive setup, a verified USB factory installer, and A/B OTA rollback.
- **Echo Dot 2nd generation (Biscuit):** improved seven-microphone wake and speech beamforming, cleaner wake-verifier and trainer audio, automatic calibrated echo cancellation, safer mute recovery across Tater reconnects, and timer stopping from either the Stop wake word or action button.
- **Echo Show 5 1st generation (Checkers):** improved two-microphone voice processing, automatic calibrated echo cancellation, display themes, timer controls, and more reliable synchronized playback, camera, touch, and application OTA behavior.
- **All Echo satellites:** stronger wake arbitration and pre-roll handling, improved playback/AEC reference timing, and shared factory/OTA release validation. Tater now owns the user-facing experience while each Echo target automatically applies its appropriate echo-cancellation path.
- **Release reliability:** the Rook build downloads and verifies its pinned wake and Stop models before firmware assembly, with a regression check protecting both required inputs.

The v2.1.0 release publishes factory and OTA artifacts for Biscuit, Checkers, and Rook in one target-aware manifest.

## v2.0.4

### What's Changed

- Checkers keeps the current weather, timer, or notification card visible during tool calls instead of replacing it with a spinning orb. The weather heading shows the active tool, and a static status appears when no card is available.
- Checkers can show Tater's configured assistant first name in the lower-left connected label, such as `Jarvis Connected`; it falls back to `Tater Connected` when the name is not yet provided.
- Biscuit is versioned v2.0.4 for the normal factory and OTA release; its behavior is unchanged from v2.0.3.

The personalized connected label requires a Tater app update that sends the assistant name to the screen. Until that app update is installed, Checkers displays `Tater Connected`.
