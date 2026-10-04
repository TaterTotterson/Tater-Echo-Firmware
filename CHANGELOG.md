# Changelog

## v2.1.0 — 2026-10-04

### What's Changed

- **Echo Spot 1st generation (Rook):** first supported release, with a round native Tater display, configurable weather and room sensors, display themes, touch intercom, timers, Room Vision camera access, BLE presence, four-microphone beamforming, synchronized media, captive setup, a verified USB factory installer, and A/B OTA rollback.
- **Echo Dot 2nd generation (Biscuit):** improved seven-microphone wake and speech beamforming, cleaner wake-verifier and trainer audio, automatic calibrated echo cancellation, safer mute recovery across Tater reconnects, and timer stopping from either the Stop wake word or action button.
- **Echo Show 5 1st generation (Checkers):** improved two-microphone voice processing, automatic calibrated echo cancellation, display themes, timer controls, and more reliable synchronized playback, camera, touch, and application OTA behavior.
- **All Echo satellites:** stronger wake arbitration and pre-roll handling, improved playback/AEC reference timing, and shared factory/OTA release validation. Tater now owns the user-facing experience while each Echo target automatically applies its appropriate echo-cancellation path.

The v2.1.0 release publishes factory and OTA artifacts for Biscuit, Checkers, and Rook in one target-aware manifest.

## v2.0.4 — 2026-10-03

### What's Changed

- Checkers keeps the current weather, timer, or notification card visible during tool calls instead of replacing it with a spinning orb. The weather heading shows the active tool, and a static status appears when no card is available.
- Checkers can show Tater's configured assistant first name in the lower-left connected label, such as `Jarvis Connected`; it falls back to `Tater Connected` when the name is not yet provided.
- Biscuit is versioned v2.0.4 for the normal factory and OTA release; its behavior is unchanged from v2.0.3.

The personalized connected label requires a Tater app update that sends the assistant name to the screen. Until that app update is installed, Checkers displays `Tater Connected`.
