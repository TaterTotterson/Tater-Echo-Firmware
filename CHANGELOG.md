# Changelog

## Unreleased

- Tater can enable microWakeWord and openWakeWord independently. MWW-only
  preserves the original pipeline, dual mode requires both models to accept
  the same winning-beam audio, and OWW-only runs openWakeWord continuously
  before the optional STT wake check.
- Each engine has its own built-in Hey Tater/custom source selection. Custom
  openWakeWord models install from the verified `.wake-bundle.json` produced
  by either Tater trainer. The newly trained Hey Tater MWW and OWW classifiers
  now ship together as one verified built-in profile.
- Biscuit, Checkers, and Rook factory images carry the built-in pair and shared
  OWW feature models. Ordinary daemon OTA installs repair the same signed
  profile and securely bootstrap the matching native wake runtime. Saved
  detector choices remain user-controlled, and the timer Stop wake remains
  immediate.
- Dual mode keeps MWW and OWW warm in parallel on the same directional beam
  lanes and opens the microphone only when their timestamped crossings agree.
  This preserves mandatory two-model agreement without replaying the wake clip
  after MWW or adding a second-stage delay.
- Biscuit now runs OWW through the ARMv7 ONNX Runtime/XNNPACK path proven on
  this hardware by EchoMuse. Live validation measured same-frame MWW/OWW
  agreement while keeping the daemon near 25% CPU and 39 MB RSS.
- Checkers and Rook now use that same continuous ONNX/XNNPACK OWW pipeline
  through a reduced Linux/musl ARMv7 runtime built from pinned source. Their
  factory images and release sidecars carry the verified native runtime and
  ONNX feature models, with an emulated inference smoke test before packaging.

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
