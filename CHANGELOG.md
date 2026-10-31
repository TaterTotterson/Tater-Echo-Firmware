# Changelog

## v4.0.0

Tater Echo Firmware v4.0.0 adds automatic USB Ethernet, future system updates
through Tater, remote maintenance, new satellite controls, and improvements to
screen, audio, recovery, and installation behavior.

### Before you update

- **A one-time USB/TWRP installation is required** for every satellite running
  a version earlier than v4.0.0. Download the factory archive made for your
  exact Echo model and follow its included instructions.
- The installer keeps existing Tater pairing, Wi-Fi, SSH, and satellite
  settings by default. Use `--factory-reset` only when you want a clean setup.
  Existing Rook installations should follow the additional reinstall command
  shown in the Rook instructions.
- Keep the satellite powered until installation finishes and it reconnects to
  Tater. A factory installation or system update may reboot more than once.
- After v4.0.0 is installed, Tater can deliver compatible application and Linux
  system updates over the network. A failed system update automatically returns
  to the previous working version. Some low-level changes, including boot-logo
  updates, still require USB/TWRP.
- Use Tater v2.0.0 or newer to manage the new settings and system updates.

### USB Ethernet

- Connect a supported USB Ethernet adapter and the satellite automatically uses
  Ethernet when the wired connection is ready. Wi-Fi returns automatically if
  Ethernet is unplugged or stops working.
- USB Ethernet was tested with the powered
  [UCTRONICS B0B9ZLVGJ4](https://www.amazon.com/dp/B0B9ZLVGJ4) adapter on
  Biscuit, Checkers, Crown, Athena, and Rook.
- Biscuit can receive both power and Ethernet through the adapter, giving it a
  simple PoE-style setup. Checkers, Crown, Athena, and Rook continue using their
  normal power supplies.
- Similar powered USB Ethernet adapters may also work, but only the adapter
  linked above has been tested.
- Radar's USB Ethernet support is included, but it is currently disabled
  because using it requires a permanent internal USB hardware modification.

### New satellite controls

- SSH can be turned on separately for each satellite from Tater. It uses a
  generated password and remains off by default.
- Do Not Disturb can be turned on manually or scheduled for each satellite. It
  silences music, announcements, wake sounds, and other normal playback while
  keeping wake detection, timers, and alarms available.

### Screens and video

- Checkers, Crown, Athena, and Rook can play fullscreen camera and video streams
  with an automatic still-image fallback when streaming is unavailable.
- Screen menus and sensor layouts have been refreshed, including a layout made
  for Rook's round display.
- Screen satellites can use a 12-hour or 24-hour clock. The 12-hour option shows
  an AM/PM marker.

### Audio and reliability

- Athena now uses all four microphones more effectively for wake detection and
  voice capture, with a safe fallback if a microphone is unavailable.
- Fixed the slight popping that could be heard during Sendspin playback on
  Biscuit.
- Improved reconnection when a wired satellite loses contact with Tater.

### Recovery and branding

- Holding Volume Up during startup enters TWRP on all supported satellites.
  Biscuit and Radar automatically return to Tater Linux on the next normal
  reboot.
- Added Tater boot branding to Checkers, Crown, and Rook, plus a safe Tater
  startup splash on Athena.

### New Contributor

Contributions from @heapsoftware included in this release:

- [#4 — Automatic USB Ethernet and Tater satellite baselines](https://github.com/TaterTotterson/Tater-Echo-Firmware/pull/4)

## v3.1.2

### What's Changed

#### Voice playback

- Preserved the Echo speaker's fixed 4,096-byte playback periods across
  arbitrary streaming HTTP chunk boundaries, eliminating static between reply
  chunks; only the final incomplete period is padded with silence.

## v3.1.1

### What's Changed

#### Voice capture

- Removed the pause previously needed between the local wake word and command.
  Echo satellites now mark the exact end of their saved wake pre-roll so Tater
  excludes the wake phrase from both command VAD and speech recognition while
  opening on the first post-wake command frame. Firmware also preserves speech
  captured during wake-model agreement or enforced wake verification and hands
  it to the voice turn without an audio gap.

#### Voice playback

- Accepted the unknown-length `0xFFFFFFFF` data marker used by streaming PCM
  WAV replies while keeping malformed non-data and truncated chunks strict.
- Added bounded incremental WAV decoding and stateful resampling so Home
  Assistant and Tater can begin filling the speaker queue before the TTS HTTP
  response finishes. MP3 and older controllers retain the buffered fallback.
- Advertised streaming voice playback version 1 and now log playback failures
  locally while returning the existing structured failure to the controller.

#### Licensing

- Changed Tater-owned firmware code from MIT to the GNU Affero General Public
  License v3 to match Tater. Bundled platform, runtime, and model components
  retain their existing upstream licenses and notices.

#### Documentation

- Promoted Echo Show 8 2nd Gen (`athena`, A8H3N2) from experimental to
  supported after its hardware test matrix passed, and documented the retail
  model number for every supported Echo satellite.
- Corrected the Biscuit and Radar installation prerequisites: Tater requires
  the matching amonet bootloader unlock and working TWRP, not a separate Fire
  OS flash, boot, or rooted Android installation.

#### Build reliability

- Locked the TECHO5/TECHO5 Dot/TECHO5 Spot source revisions, platform assets,
  Go/Alpine build environment, Alpine packages, GitHub Actions, and runner OS.
  Tagged firmware jobs now build through one digest-pinned multi-architecture
  builder, keep Go toolchain downloads disabled, assemble Athena's rescue store
  from frozen offline inputs, and record platform provenance in manifests.
- Compatible shared ARMv7 ONNX Runtime caches now survive build-script-only
  changes. Release jobs revalidate the ARM ELF and exact required-operator
  fingerprint before reuse, while incompatible caches still rebuild normally.
- Disabled Go's unavailable repository stamping inside the isolated firmware
  builder; release manifests continue recording the pinned source and platform
  provenance without depending on the host checkout's Git metadata.

## v3.1.0

### What's Changed

- Added experimental Echo Show 8 2nd Gen (`athena`, A8H3N2) support on the
  pinned TECHO5 v1.6.4 mainline platform, including a guarded TWRP installer,
  Tater Linux store/rootfs release job, 96 kHz four-microphone capture, 96 kHz
  speaker output, landscape display/touch, camera, Bluetooth, buttons, setup,
  recovery, and application A/B OTA paths.
- Synchronized Athena's authoritative `SW_MUTE_DEVICE` hardware privacy latch
  with Tater's capture gate, persisted mute state, display, and controller
  reporting without double-toggling on its accompanying `KEY_MICMUTE` event.
- Corrected Athena's DRM XRGB8888 pixel packing so the absent alpha channel no
  longer forces every rendered pixel blue, and recognized the mainline
  `generic ft5x06 (3e)` touchscreen exposed by tested hardware.
- Added shared display power management for Checkers, Crown, Athena, and Rook:
  manual brightness, smoothed sensor-driven auto brightness, per-satellite
  overnight schedules, dim/red-clock/screen-off night behavior, and display
  diagnostics with safe manual fallback when a light sensor is unavailable.
- Kept the target marked experimental until its documented display, audio,
  privacy, camera, setup, recovery, rollback, and power-cycle matrix passes on
  physical A8H3N2 hardware.

## v3.0.6

### What's Changed

- Added a large centered volume overlay to Checkers, Crown, and Rook. Physical
  and remote volume changes now show the current level for two seconds using
  Tater orange or the selected display theme's primary color, and repeated
  adjustments update the meter without disturbing the screen underneath.
- Fixed Rook's physical microphone button by synchronizing Tater's mute state
  with the kernel privacy driver. The hardware switch now updates capture,
  persistence, screen state, and controller reporting without a second evdev
  toggle undoing the press; devices without that driver retain the fallback.
- Restored Rook camera snapshots for Verba and Sat Chat by routing every
  capture through the OTA-updated camera helper instead of the stale rootfs
  endpoint. Camera access now honors both Tater's logical mute and Rook's
  hardware privacy state before and during capture.

## v3.0.5

### What's Changed

- Fixed intermittent static and badly distorted replies on Biscuit and Radar
  by combining their 2048-frame playback ring with the hardware-tested
  2048-to-2064 linear drift correction already used by the related MediaTek
  playback path.
- Added queue headroom before corrected ALSA writes so the vendor DL1 callback
  cannot silently accept only part of a nearly-full write and accumulate the
  missing audio into a repeating playback underrun.
- Kept the existing uncorrected 1024-frame compatibility fallback for devices
  that cannot open the corrected playback configuration, and added regression
  coverage for corrected, uncorrected, and legacy write paths.

## v3.0.4

### What's Changed

- Replaced the built-in Hey Tater microWakeWord, openWakeWord, NanoWakeWord
  CNN, and NanoWakeWord GRU models with the latest quality-passed trainer
  outputs. The embedded wake bundle now carries each model's matched digest,
  validation result, and calibrated standalone or confirmation policy.
- Eliminated the remaining intermittent Biscuit and Radar reply distortion by
  restoring 171 ms of direct-ALSA headroom and writing each native 2048-frame
  mixer period to the hardware intact. The smaller playback configuration
  remains an automatic compatibility fallback if a device cannot open the
  larger ring.
- Added Tater Recovery to Biscuit, Radar, Checkers, Crown, and Rook. Holding
  Volume Up for five seconds during the first 90 seconds of Linux startup now
  requests the one-boot recovery environment; factory archives also include a
  USB recovery helper and a verified `install.sh --repair` path that preserves
  satellite settings unless `--factory-reset` is explicitly selected.
- Retained TWRP as the lower-level recovery escape hatch and included both
  recovery helpers in every factory archive, with target verification,
  inactive-slot installation, checksum validation, and automated packaging
  coverage.
- Kept the existing six-step physical setup-reset gesture active while the
  captive setup interface is running, allowing a satellite with incorrect
  Wi-Fi or pairing details to return safely to setup mode.

## v3.0.3

### What's Changed

- Eliminated the remaining Biscuit playback static observed on v3.0.2. While
  audio is playing, the continuous wake detector now reuses the already
  echo-cancelled main microphone stream instead of running a second AEC filter
  and switching retained acoustic paths twice per capture batch.
- Applied the single-pass playback path to Biscuit, Radar, Crown, and Rook,
  preventing their hardware echo-reference work from starving ALSA. Checkers
  retains its existing software-reference behavior and the same sample-ordered
  single-pass protection.
- Preserved directional wake beamforming whenever playback is inactive, with
  regression coverage for both playback reuse and idle directional pickup.

## v3.0.2

### What's Changed

- Eliminated intermittent playback static caused by ALSA underruns when the
  hardware echo reference was active. Biscuit, Radar, Crown, and Rook now skip
  the unused software-reference lock instead of letting a 160 ms AEC capture
  batch stall their real-time playback buffers.
- Preserved the software-reference path for Checkers and for any device whose
  hardware loopback has not been confirmed, so echo cancellation behavior and
  fallback compatibility are unchanged.
- Prevented the same playback starvation on Checkers by separating its required
  software-reference ring from the longer-running AEC filter lock. Speaker
  writes remain deadline-safe while the reference stays sample-ordered and the
  existing delay, resynchronization, and convergence behavior is retained.

## v3.0.1

### What's Changed

- Added live per-model tuning overrides for microWakeWord, openWakeWord, and
  NanoWakeWord. Tater can now override each active model's threshold and
  window or patience without replacing the calibration stored in the selected
  built-in model, catalog bundle, or custom wake bundle.
- Applied openWakeWord and NanoWakeWord overrides when constructing their
  continuous on-device scorers, including both standalone and MWW confirmation
  modes, and kept the effective values visible in native diagnostics.
- Advertised wake-model tuning support on Biscuit, Radar, Checkers, Crown, and
  Rook while preserving the existing room sensitivity and environment policy.

## v3.0.0

### What's Changed

- **USB update required for Biscuit and Radar:** devices running firmware older
  than v3.0.0 cannot cross this base-system change with OTA. Boot TWRP and run
  the matching v3.0.0 factory installer over USB once. This clean reset clears
  the satellite's Tater settings; routine A/B OTA updates resume afterward.
- Replaced the retired Biscuit and Radar userspace with the same Alpine-based
  Tater Linux architecture used by the screen satellites. Both targets now
  receive a complete factory image, A/B rootfs and application slots, automatic
  rollback, a retained TWRP recovery route, and target-pinned Linux kernels.
- Added one guarded USB/TWRP installer for Biscuit and Radar. It verifies the
  target and every bundled input, preserves private boot-critical backups and
  stock donor partitions, builds recovery from the unit's own TWRP header, and
  supports bundle verification and no-write preflight checks.
- Moved Biscuit and Radar playback to the native Linux direct-ALSA path used by
  the screen satellites. Radar gains its tested codec route, amplifier timing,
  flat hardware EQ, Tater-controlled output EQ, and clean Sendspin/TTS playback
  without the severe distortion produced by the former path.
- Restored complete Linux bring-up for the two ring devices, including Wi-Fi,
  Bluetooth, seven-microphone capture, setup and boot ring animations, buttons,
  ambient light, Sendspin, Sat Chat audio, setup reset, and the Tater pairing
  portal. Boot now uses an orange ring with a rotating white segment and setup
  mode uses the flashing white indication.
- Added NanoWakeWord CNN and GRU detection across Biscuit, Radar, Checkers,
  Crown, and Rook. Satellites can run NWW by itself or require MWW + NWW,
  while Tater controls the active architecture and sends MWW-only fallbacks
  to devices that do not advertise NanoWakeWord support.
- Bundled matched Hey Tater MWW, OWW, NWW-CNN, and NWW-GRU models in factory
  and OTA packages. Firmware now validates trainer-produced schema-v3 wake
  bundles, their calibrated thresholds, sizes, and SHA-256 digests before
  activating downloaded models.
- Kept the selected OWW or NWW inference lane isolated on the companion CPU
  core and expanded the minimal ARMv7 ONNX Runtime operator set for CNN and
  GRU models. Build caches are now tied to the required-operator fingerprint
  so an older runtime cannot be reused with a newer wake model.
- Unified all five tagged target builds around target-specific Tater Linux
  factory archives, deterministic application OTAs, the shared reduced ARMv7
  ONNX Runtime, checksums, and one compatibility manifest.
- Removed the superseded Android compiler, legacy userspace, factory scripts,
  controller hooks, and obsolete firewall integration. The supported tree and
  documentation now describe only the native Tater firmware paths.

## v2.6.3

### What's Changed

- Redesigned Rook's photo home screen around a large centered clock and
  weather hero so both are readable at a glance. Selected weather and room
  sensor readings are larger and arranged in a round-screen-safe two-row grid.
- Kept the configured assistant name beside the room name and made the same
  live status treatment used by Checkers and Crown clearly visible on Rook,
  including Online, Listening, Thinking, Working, and Replying states.

## v2.6.2

### What's Changed

- Simplified every supported Echo target to the Tater-native firmware path.
  The daemon now requires its native Tater bootstrap, and the authenticated
  native connection exclusively owns pairing, settings, voice audio, playback,
  timers, Bluetooth proxying, displays, and OTA. The retired controller,
  discovery, private-listening, console, and shell paths have been
  removed without removing anything used by native mode.
- Made spoken `Stop` reliable while a timer or alarm is ringing. Firmware now
  installs the bundled Stop detector before the first alarm sound, uses the
  model's calibrated sliding-window policy instead of normal room wake-word
  hardening, and restores the user's selected wake detector after the final
  ringing timer stops.
- Added support for the Stop model's three-frame streaming input and clearer
  native-runtime shape diagnostics. Regression coverage verifies the embedded
  alarm sound cannot trigger its own Stop detector, while the action button and
  screen Stop control remain immediate local fallbacks.

## v2.6.1

### What's Changed

- Added the versioned generic screen-notification contract used by generated
  images and future Verbas on Checkers, Crown, and Rook. Display senders can
  provide a readable card label, choose contain or cover image fitting, and
  deliver inline authenticated image data without requiring another
  firmware-specific layout. Existing Awareness camera and doorbell cards keep
  their established presentation.
- Updated all native Echo wake targets to honor the live
  `wake_sensitivity` and `wake_environment` settings when constructing the
  local microWakeWord policy. The selected wake package remains authoritative
  for its calibrated threshold, window, and close-miss values, while profiles
  such as `tv_nearby` can now improve initial wake recall as intended before
  the existing verification stage rejects television false wakes.

## v2.6.0

### What's Changed

- Rebuilt the Checkers, Crown, and Rook home screens around full-screen photo
  backgrounds, seven bundled landscapes, optional user photos, configurable
  rotation intervals, static day/night weather art, selected sensor readings,
  assistant status, and theme-aware state glows. Rook receives a dedicated
  circular crop and curved layout while Checkers and Crown retain their
  rectangular profiles.
- Added a touch-first action menu for Sat Chat, Climate, and Cameras. A tap
  opens the menu, a hold still starts an intercom broadcast, large directories
  page cleanly on every screen, and all layouts follow the configured Tater
  name and theme color.
- Added auto-connected Sat Chat audio and video among Checkers, Crown, and
  Rook, with audio-only participation for Biscuit and Radar. Low-latency camera
  helpers, newest-frame queues, target-specific video profiles, native
  framebuffer conversion, and a compact Crown viewer keep audio responsive
  while delivering the smoothest video each device can sustain.
- Added integration-backed thermostat controls and room-temperature views,
  including working target and HVAC-mode controls, graceful unavailable
  states, six configurable rooms on Checkers and Crown, and three on Rook.
- Added camera and doorbell browsing with live video and optional push-to-talk
  audio. Camera lists use the same paged layout as Sat Chat, and each target
  receives screen-safe close and talk controls.
- Promoted Awareness events and music to clear full-screen experiences.
  Notifications now retry missing images, preserve aspect ratio, shrink long
  camera names to fit, show a reliable lifetime indicator, and dismiss on tap;
  music visualization and screen-state transitions remain smooth through
  regional rendering and cached compositions.
- Strengthened echo cancellation and feedback control for Sat Chat while
  preserving double-talk, and applied the improved residual guard to normal
  music and alarm barge-in. Wake capture now recovers more reliably under
  playback and other sustained audio load.
- Extended the A/B application updater with staged bridge migrations, optional
  camera-helper components, immediate post-reboot continuation, and reusable
  slot migration metadata. Existing supported releases can update normally,
  while USB/TWRP factory installation remains available.

## v2.5.5

### What's Changed

- Reduced native display CPU work on Checkers, Crown, and Rook by caching each
  unchanged composition and redrawing only live weather, music, progress, and
  reply-animation layers. Checkers and Crown now submit only their changed
  framebuffer regions, while unchanged static voice frames skip presentation
  entirely, keeping touch, weather motion, and music visualization responsive.
- Kept Crown's lower context card useful throughout a voice turn: listening,
  thinking, speaking, and intercom status remain in the permanent left column
  instead of replacing active music or the most recent Awareness event. Crown
  Awareness camera names, timestamps, and descriptions are also larger and
  easier to read at the Show 8 viewing distance.
- Added Checkers-style audio-reactive reply edges to Crown. The glow follows
  reply loudness without rebuilding the 1280×800 frame, and the microphone and
  mute controls remain correctly layered above it.
- Added pixel-equivalence and animation-region coverage for the cached
  Checkers, Crown, and Rook render paths so optimized frames remain visually
  identical to full compositions.

## v2.5.4

### What's Changed

- Added Echo Show 8 1st Generation (`crown`, model C7H6N3)
  support across the complete Tater Linux lifecycle: guarded TWRP factory
  installation, per-device backups and bootstrap keys, target-specific
  four-microphone capture and beamforming, speaker and button routing, camera,
  BLE/GATT, release packaging, verified application OTA, and rollback-safe A/B
  application slots.
- Added Crown's native 1280×800 display experience while retaining the proven
  Checkers interaction model. Weather and current-room conditions remain
  visible above the context pane; music, timers, transient Awareness alerts,
  and an aspect-correct persistent Last Event card share the lower pane.
  Daylight-aware weather art shows a moon at night, and an empty history is
  identified explicitly instead of displaying an idle placeholder.
- Matched Crown touch handling to Checkers' responsive direct-event cadence,
  while preserving Crown's coordinate mapping and repeated-contact handling.
  Regional framebuffer updates and a static composition cache keep weather and
  music motion at 30 Hz without rotating the unchanged 1280×800 frame or
  rebuilding it each second as playback progress advances.
- Raised microphone capture to the FireOS reference level on Biscuit, Radar,
  Checkers, Crown, and Rook by applying each target's ADC differential-input
  gain controls during codec setup. This corrects the roughly 6 dB lower Linux
  default and improves pickup at a distance without changing FireOS devices.
- Hardened the shared Echo Show installer and rescue path with target-specific
  LineageOS and boot-image verification, bounded rescue-console operations,
  safe stock-logo restoration, and strict cross-target rejection before any
  partition is modified.

## v2.5.3

### What's Changed

- Improved wake verification on Biscuit, Radar, Checkers, and Rook by giving
  the verifier its own detector-timestamped two-second capture. Dual MWW/OWW
  agreement now anchors that capture to the MWW phrase boundary, so inference
  latency cannot trim the opening “Hey” while the longer command pre-roll
  remains available to STT.
- Stabilized array pickup across a wake utterance without restoring the former
  multi-lane scorer. MWW and OWW continue consuming one uninterrupted acoustic
  stream, while a speech-confirmed bearing is held through the phrase and the
  exact winning direction carries into the command turn. Silence, timeout, and
  unhealthy-array paths return safely to adaptive or omni pickup.
- Allowed `--reinstall-existing-tater` to recognize the exact hash-verified
  earlier Rook v2.5.0 Tater boot image used by the first multi-Spot installs.
  Unknown or modified boot images remain rejected, so an existing supported
  Rook can be reinstalled from TWRP without first restoring Fire OS.

## v2.5.2

### What's Changed

- Fixed multi-Spot installations by replacing Rook's shared Broadcom
  `00:90:4c:1a:09:00` placeholder with each unit's factory Wi-Fi address before
  association and DHCP. Invalid or missing IDME MAC data receives a stable,
  locally administered address derived from the unit serial instead.
- Kept the correction Rook-specific and guarded: Checkers, Radar, Biscuit, and
  any interface that already has a device-specific address are unchanged.
  Existing Rook installations can receive the corrected root filesystem by
  reinstalling the factory bundle from TWRP with
  `--reinstall-existing-tater`; this resets pairing and local settings.

## v2.5.1

### What's Changed

- Biscuit and Radar now request Sendspin track colors and a reduced 10 Hz
  visualizer whenever they act as music players. A selected music animation
  uses the brighter track palette, synchronized loudness and beat/peak events,
  while the new default, Level Bars, maps Sendspin's twelve spectrum bins
  directly to the twelve ring LEDs. No Animation still keeps the ring dark,
  and local speaker levels remain the fallback when a controller does not
  publish visualizer data.
- Release builders now fetch the pinned Alpine ARMv7 image through Amazon's
  public container mirror, avoiding Docker Hub's anonymous pull limit across
  the Biscuit, Radar, Checkers, and Rook jobs.

## v2.5.0

### What's Changed

- Added the experimental 2017 Echo 2 (`radar`) satellite target on the shared
  legacy MT8163/Puffin runtime. Radar receives its own hardware identity, dual-gate
  TLV320AIC3204 speaker route, ring/BLE capabilities, factory bundle, binary
  OTA, and guarded Fire OS 6 A/B installer while retaining Biscuit's proven
  seven-microphone and on-device wake pipeline.
- Radar factory installation builds from the attached unit's own boot image,
  preserves stock in `boot_b`, and resolves `boot_a`/`boot_b` by name so the
  stale `boot_a_x`/`boot_b_x` aliases from an interrupted older unlock cannot
  be selected by the installer. The target remains marked experimental pending
  full Tater hardware validation.
- Factory packaging now validates the ELF class and machine of every native
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
  The legacy boot path now selects the init architecture from the attached
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
- Hardened legacy Fire OS networking by requesting broadcast DHCP replies
  from `udhcpc`. If an IPv6 input-drop policy cannot be installed because the
  device lacks `ip6tables`, the runtime now disables IPv6 instead of leaving inbound
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
- Biscuit runs OWW through the hardware-tested ARMv7 ONNX Runtime/XNNPACK
  path. Live validation measured same-frame MWW/OWW agreement
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
