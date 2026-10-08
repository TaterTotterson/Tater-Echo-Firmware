# Changelog

## v2.2.0

### What's Changed

- Replaced the firmware-specific Tater media clock/session transport with a
  Sendspin v1 player on Biscuit, Checkers, and Rook. Every Echo now advertises
  itself to Tater, Music Assistant, and other compatible Sendspin controllers,
  accepts synchronized 48 kHz PCM or FLAC, and preserves its player identity,
  volume, delay, pairing keys, and stereo/left/right/mono output assignment.
- Removed the retired `audio.clock.sync` and `media.session.*` playback path.
  Native replies, interactive TTS, announcements, wake cues, timers, overlays,
  audio scenes, ducking, and barge-in still take priority locally and return the
  speaker cleanly to Sendspin afterward.
- Added the Echo capability contract for Tater's new OpenWakeWord path,
  including wake-detector selection and dual-wake confirmation. The permanent
  Echo wake stream and independent beamformed wake lanes remain available so
  on-device microWakeWord and Tater's OWW detector can participate in the same
  wake decision instead of competing as separate turns.
- Keeps the winning wake lane's pre-roll available to the second detector,
  wake verifier, trainer, and STT handoff, with fail-open behavior when the
  remote confirmation path is unavailable.
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
