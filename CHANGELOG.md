# Changelog

## v5.0.0 — release candidate

This source candidate is not a published firmware release. Final physical USB,
OTA, recovery and rollback qualification remain required;
the release packer intentionally refuses unqualified platform inputs.

### Highlights

- New theme-aware GPU UI for Checkers, Cronos, Crown, Athena and Rook, including
  refreshed home screens, touch menus, EQ, cameras, climate and media popups.
- Smooth or Matrix conversation text, modern thinking animation, full-image
  notifications and larger aspect-preserving camera/Sat Chat video.
- Target-specific hardware H.264 encode/decode, balanced call profiles, corrected
  colors and completed-buffer ownership to prevent backward camera motion.
- Biscuit/Radar hardware mic gain and verified 80 Hz filtering; early Volume Up
  recovery support and safer USB Ethernet detection.
- Athena Sendspin playback correction and shared audio/UI responsiveness fixes.
- A mandatory one-time full USB installation for every pre-v5 Echo, preserving
  verified settings by default. Old OTA updaters reject the new package schema;
  compatible A/B application/system updates resume after migration.
- Public source roles documented in Tater Echo Linux and Tater Echo Hardware.

### Development and qualification notes

- Prepare hash-pinned public Android inputs automatically before screen-device
  USB installation, then assemble GPU/video runtimes with this unit's retained
  vendor files after first boot. No FireOS backup, NDK, mounted images or extra
  filesystem tools are needed. Verify complete staged/existing directories,
  refuse changed vendor files and unsafe links, support interrupted-root retry,
  and confirm a new target-verified boot after installation. Vendor binaries
  remain device-local and are never included in public Tater archives.
- Isolate Checkers' retained-Lineage decoder/encoder ABI in new versioned runtime
  paths, preserving its camera conversion and keeping the old FireOS ABI separate.
  Add Rook's exact published 4.2.3 boot to the guarded USB reinstall allowlist.
  Produce unqualified USB-only candidate bundles without any OTA/feed artifacts.
  Physical fleet qualification, including Checkers' corrected microphone kernel,
  is still required before the final release.
- Keep Athena's pinned store builder compatible with Alpine's disk-size tools
  without changing the upstream platform source or installing floating tools.

- Require a one-time full USB upgrade for the new hardware platform on every
  Echo target. Publish schema-2 app, bridge and system packages that v4.2.3
  rejects before installation, independently of host update order. Require a
  matching installed target/platform marker and block rootfs-only repair from
  crossing the kernel/platform boundary. Tater/HA Bridge explain the USB
  requirement while preserving normal OTA after migration. Regression checks
  against the actual v4.2.3 updater pass for all seven targets and all three
  package kinds; final physical USB/OTA qualification is still required.

- Add early Volume Up detection to Biscuit/Radar USB factory boot images,
  before the main system slot, network, audio or Tater starts. A continuous
  three-second hold uses this unit's verified automatic-return TWRP handoff;
  board/image/current-kernel mismatches refuse the recovery write. Retain the
  late Tater Recovery fallback for older boot images. This does not modify the
  bootloader or remove the shared-recovery/power-loss limitation. Physical
  cold-boot, TWRP return and both-model qualification remain pending; routine
  application/System OTA does not install this early boot change.

- Integrate Biscuit/Radar's verified 80 Hz microphone high-pass into their
  target-specific kernel builds, preserving the existing vendor biquad tuning.
  The new kernels enable it by default with a boot-time opt-out. Skip the
  equivalent software filter only after all eight ADC slots verify; older
  kernels and other satellites retain software filtering. Build from pinned
  source/toolchain plus a hash-locked independent overlay instead of fetching
  the older kernel asset. Reject wrong-board, older and unqualified kernels
  during release packaging. No general miniDSP, AEC or wake-model offload is
  implied. Biscuit's isolated filter trial passed; combined gain/filter, Radar
  hardware and final kernel/OTA qualification remain release gates. System
  OTA v1 still carries rootfs only; it does not silently replace the kernel.

- Move Biscuit and Radar's fixed microphone boost from +40 dB software gain
  to the four microphone ADCs' +40 dB analog PGAs, with unity ADC digital
  volume and no software boost. Verify both channels of every ADC before
  capture starts; reject mismatched controls and restore partial PGA changes
  on failure. Legacy gain settings cannot stack another software boost on top.
  Preserve the existing DOA/VAD level calibration, echo-reference extraction,
  wake models and first-word timing. Other satellites' gain staging is unchanged.
  Biscuit's short owner-operated trial passed; Radar's hardware listening test,
  extended headroom/playback checks and final OTA qualification remain pending.
  This source change has not been installed or released.

- Protect computer USB connections on Rook and Biscuit from automatic Ethernet
  host-mode probes, including enumeration and suspend. Read gadget/UDC state,
  allow new connections to settle and recheck before changing role. Passive
  Show detection, idle adapter discovery, wired health/recovery and Wi-Fi rescue
  remain available. An explicit diagnostic `probe` can still force a bounded
  role test. The shared rootfs change is tested but not installed or released.

- Adopt the approved Quiet clock home layout on Rook: a smaller centered clock,
  date below, compact outline weather/temperature/condition row, separate
  feels-like reading and round-safe optional sensor rows. Retain the assistant
  name, room temperature, colored status dot, themes, rotating backgrounds,
  12/24-hour time and day/night weather selection. The weather artwork now uses
  the Shows' shared theme-aware renderer. This source-only follow-up is built
  and regression-tested; installation is deferred to the fleet OTA test.

- Add Rook's round GPU workspace, theme-aware menus/media/notifications and
  qualified target-scoped H.264 adapters. Non-blocking ambient-light reads keep
  touch/navigation responsive. Held camera slots prevent stale-frame backward
  jumps; motion-priority live exposure and the matching camera-clock kernel
  correction restore roughly 15 FPS at the native 640×480 capture size. Still
  photos retain their longer exposure policy. Public release remains gated on
  final kernel/runtime/installer/OTA qualification; no vendor code is bundled.

- Advertise conversation-text style support to Tater and the Home Assistant
  bridge, enabling capability-gated per-satellite Smooth/Matrix controls.

### Show popup and conversation refresh

- Match the approved charcoal GPU workspace across Checkers, Cronos, Crown and
  Athena: full-screen contained notification artwork with shaded text overlays,
  the default Tater image, quieter source/category labels and an inset countdown.
- Refresh video pause/loading/error controls and the linked-player now-playing
  screen with theme-aware accents, larger type, rounded artwork and slim progress
  tracks. Video pictures and control textures update independently; paused
  controls fade in on the existing retained GPU protocol. Playing video has no
  permanent HUD. Existing pause/resume/stop commands, aspect ratios, privacy
  indicators, hardware playback and software fallbacks remain supported.
- Replace the Shows' thinking/working halos with three softly breathing GPU
  sprites, sharing a bounded atlas with Smooth/Matrix text. Steady animation
  sends only small quad changes, without repainting/uploading the transcript.
  Final user text is centered while waiting; streaming text keeps its existing
  smooth behavior. The round Rook layout is unchanged.
- Automated renderer, race and ARM checks plus native-size synthetic previews
  cover the refresh. It is not installed or released; on-device animation and
  playback checks remain pending. Wake/audio/camera capture are unchanged.

### Athena Sendspin audio

- Fix Sendspin's renderer-clock lookup on Athena: read its actual ALSA device 0
  and interpret queued frames at the 96 kHz hardware rate. The legacy device 23
  lookup could leave music buffering silently while the playing screen appeared
  normal. Startup's speaker-availability check now uses the same target device.
  The 48 kHz music mixer, existing upsampler and other satellites' playback paths
  are unchanged. Automated regression tests pass; an on-device listening test
  remains pending.

### Conversation text styles

- Added a per-satellite **Conversation text** choice in Display → Appearance &
  motion. **Smooth** keeps the current default animation; **Matrix** cycles
  theme-colored symbols into STT/reply text, resolving characters independently.
  Settled words stay readable across streamed updates. Retained GPU atlases
  keep animation ticks bounded; software rendering retains its readable fallback.
  Preferences persist locally without changing wake, capture, or audio settings.

### Hardware video qualification fixes

- Added target-scoped Crown hardware/UI integration for its 1280×800 screen:
  native-size GPU scenes, animated text, retained video overlays and independently
  verified H.264 encode/decode. The balanced live profile is 768×432; native
  still geometry, audio/privacy controls and software fallbacks are retained.
  Application updates can discover their own qualified sidecars when launched
  by an older supervisor, while respecting explicit CPU-disable overrides.
  This is unpublished development pending installed/final OTA qualification.
  The release workflow consumes a separately verified Crown helper set and
  checks all matching application sidecars; unqualified public release remains
  blocked. Hardware metadata is packaged as non-executable JSON.

- Correct Cronos' camera-buffer handoffs: hold each target for two sensor
  ticks and copy the preceding completed target before slower callbacks.
  This avoids publishing stale slots that made motion jump backward in Sat
  Chat. The controlled full-duplex trial passed 1,800 chronological/stability
  checks at approximately 15 FPS; the operator confirmed ordered motion.
  GPU composition, overlays, hardware H.264 encode/decode and native still
  geometry are retained. Other boards' capture policies are unchanged.

- Added compatibility for Checkers' original pinned schema-1 GPU runtime
  metadata, which names the previously prepared helper. The current renderer
  still requires its own exact helper hash and unchanged private-library hashes;
  no older/shared executable is used. This addresses the diagnosed UI software
  fallback without replacing private runtimes.
- Live MJPEG capture now holds the same qualified Checkers/Cronos motion-priority
  exposure lease as the existing H.264 path, without enabling H.264 encoding.
  The shutter stays within a nominal sensor frame while gain compensates;
  dim scenes may be noisier. Normal exposure resumes after the final live
  stream closes. Native still resolution and other models are unchanged.
  Stable-scene exposure holds no longer suppress changes into/out of live mode.
- Reduced Checkers/Cronos live MJPEG camera and Sat Chat output to 480×270
  with a 20 FPS ceiling. Direct Bayer sampling avoids developing a larger
  intermediate image; existing JPEG quality, framing and privacy gates remain.
  Native-resolution still photos and other satellite models are unchanged.
- Earlier qualification trials temporarily used MJPEG sending on Checkers and
  Cronos. Qualified hardware H.264 sending is now restored, with balanced MJPEG
  fallback. GPU composition, incoming H.264 hardware decoding, full-screen video
  layouts, and full-resolution still photos remain enabled/unchanged.
- Corrected Checkers' and Cronos' hardware encoder input chroma order: their
  qualified OMX runtimes advertise planar YUV but consume Y,V,U. Bayer conversion
  remains standard I420; the adapter swaps only chroma at the encoder boundary.
  Generated-pattern hardware encode/software decode verifies the correction.
- Accept valid H.264 SPS picture-order mode 2, including baseline streams
  generated by x264. Geometry validation and bounded decoding remain enforced.

### Shared screen workspace UI

- Restored the approved classic lower-right home layout on Show 5 and Show 8
  while retaining current GPU fonts, outline weather artwork, themes,
  backgrounds and sizes. Clock/weather, date/conditions, assistant/status/room/
  indoor temperature and optional bottom readings share right alignment.
  Native-size previews are covered by exact pixel comparisons.
- Added Athena's native Mesa/Panfrost GPU UI adapter for its Mali-G72:
  retained scenes, fades, animated text and video scaling with CPU fallback.
  The loader, dependencies and app-owned helper are separately hash-checked;
  MT8163 Android libraries/codecs are not used. OTA accepts an atomic GPU-only
  Athena helper set without claiming hardware H.264. Owner functional checks
  and final system OTA remain pending.

- Hide the home-footer condition when its main weather slot is hidden, so
  residual condition metadata no longer leaves a stray "Sunny" label. Other
  independently enabled sensor and feels-like readings remain visible.
- Simplified Device Info's Graphics value to "GPU" or "Software", following
  renderer availability rather than implying a new hardware driver or NPU.

- Removed local wake-sound, Continued Chat, and Interrupt Replies controls;
  these shared voice settings remain controlled by Tater/the connected host.
  Older local copies are ignored on restore without changing host settings.
  Sound & Wake now links directly to Equalizer and Do Not Disturb; Presence
  boost remains an existing speaker-EQ option under Equalizer.
- Added immediate pressed-row highlighting and explicit camera/call startup
  feedback. Suppressed repeated startup taps while pending, bounded the wait
  with retry feedback, and included interactions in CPU/GPU cache keys.
  Sat Chat no longer rasterizes its hidden home background. Privacy-safe
  request/host-state/first-picture timings distinguish UI and stream startup.
- Replaced paged landscape EQ rows with eight vertical half-decibel faders
  and Presence boost on one screen. Drag previews remain local; release sends
  the existing validated EQ setting without changing other bands.
- Moved weather conditions before Feels like in the home footer, and the
  optional unlabelled room temperature beside the room name in the heading.
  Restored a green online status dot, with mute/offline/voice-state colors.
  The displayed assistant name still follows the configured first name.
- Fixed retained-GPU Sat Chat paging: Next/Previous now upload the new page
  immediately, including rapid taps during a transition. Satellite names,
  rooms, capabilities, busy status, and disappearing participants invalidate
  the directory cache; identical snapshots retain it. Surface switches always
  replace the GPU source. Added regression coverage for Fade, Slide, and Off.
- Refined the home weather into theme-aware outline art; optional readings now
  follow Feels like along the bottom, without the duplicated right-hand room
  temperature. Expanded Room humidity's label and removed the home swipe cue.
  Home menus now open on taps, not drags/swipes; sliders and broadcast holds
  retain their existing gestures. These refinements are included in the
  unpublished Checkers `ui.2` application test over the unchanged `ui.1` system.
- Implemented the approved Sat Chat, Cameras, and Climate workspace designs:
  quiet grouped directories, capability/availability labels, larger climate
  setpoint controls, segmented HVAC modes, and aligned read-only room readings.
  Existing paging, call routing, thermostat limits, and talkback gates remain.
- Maximized landscape camera/Sat Chat video across the full screen, underneath
  floating titles, Back/Close and Hold to talk/End Chat. Aspect remains intact,
  without cropping/stretching; rounded corners follow the actual picture.
  Rook retains its existing round-safe video layout pending device qualification.
  View-only/talkback share a stable viewport, and source-aspect changes clear
  old bars without restarting the decoder.
  JPEG/hardware fitting and the software fallback retain explicit transparent
  padding, with display-aspect fitting on the latter. These connected-screen
  updates are included in the installed `ui.2` application trial, not a
  hardware-qualified or published release.
- Checkers composites cached floating video controls with its existing GPU
  atlas/quad support. New pictures do not rebuild or upload the controls; a
  control-only change retains the picture. Unchanged state submits no GPU job.
  CPU fallback blends only cached control rectangles, and mute updates remain
  immediate and visible without covering Close. No GPU driver/helper changed.
  JPEG and hardware H.264 retain max-fit preparation; removed an intermediate
  allocation/copy from JPEG contain preparation.
- Notifications now always contain the full supplied image, including Awareness
  Core/older senders requesting cover or fill. Wide and portrait artwork is
  letterboxed rather than cropped; the default Tater image remains unchanged.
- Added the charcoal main-app-inspired workspace as the shared screen UI,
  with the softer default Tater orange and existing theme accents preserved.
  The photo home has unboxed text, a larger clock, weather, and no top bar.
- Replaced the action menu with six tiles: Sat Chat, Cameras, Climate,
  Sound & Wake, Display, and Device Info. Added round-safe Rook tiles and
  paged local settings, including volume, edge glow, display/night preferences,
  themes, rotation, DND, and the EQ. Shared voice behavior stays host-managed.
- Local changes use a bounded allowlist and the existing daemon settings hook,
  report to the host, and persist safe preferences atomically. Microphone,
  model, credential, and speaker-protection controls remain out of the local
  UI. Ordinary screen changes do not reconfigure wake detection or AEC.
- Matched notification, Sendspin, and media-companion presentation to the new
  workspace, retaining default Tater notification artwork and poster backdrops.
- Added Checkers retained-GPU fades, optional Slide, and Off, inspired by the
  main app's Compotato choices. Fade is the satellite default; the 3D desktop
  effects are not exposed. Progress/spectrum use a bounded v3 atlas and quad
  updates without repeatedly uploading artwork or restarting entrances.
- Kept CPU fallback, existing safety priorities, and direct critical/mute
  updates. Swipe movement no longer qualifies as a stationary broadcast hold.
  Offline render, daemon, regression, and race checks do not replace installed
  device/GPU qualification; no new hardware qualification is claimed here.

### Screen backgrounds

- Added alpine sunset, misty lake, and desert dusk to the bundled photo
  rotation, alongside the six retained landscapes. Removed Meadow from both
  embedded display-size sets and the rotation; nine backgrounds remain.
  Both native display
  families are included, with the existing centered cover layout for Rook.
- Preserved configured rotation timing, uploaded-photo precedence, and the
  single active decoded-image cache. The charcoal settings-menu redesign is
  now implemented in the shared workspace described above.

### Checkers hardware build and provisioning

- Added locked, clean builds of the original GPU/H.264 helpers from
  `Tater-Echo-Hardware`, with exact source, toolchain/header, input and
  hardware-qualified output identities. Public archives allow only the three
  open helpers and their metadata, never private libraries.
- Included matching helpers in Checkers rootfs and full application slots;
  camera/helper selection now follows the running system/app slot during
  rollback instead of relying on globally replaced `/data` helpers.
- Added separately prepared, fully verified owner-local library provisioning
  to the Checkers USB factory workflow. Existing libraries are never replaced;
  other devices stay disabled. No kernel migration is implied.
- Added library-only runtime manifests, backwards-compatible library checks,
  complete-set OTA validation, and safe software/JPEG fallback. Public helper
  publication/configuration and installed hardware qualification remain gates.

### Checkers native camera and Sat Chat

- Added a negotiated screen-sized H.264 call profile for compatible Checkers
  (768×432) and Cronos (640×480), preserving source aspect and the full-screen
  view. Live calls pace raw sensor work before encoding; full-resolution stills
  and ordinary previews are unchanged. Two-device smoothness qualification is
  still in progress; do not infer a 30-FPS duplex guarantee.
- Present ready live pictures without waiting for the menu-animation tick and
  add bounded frame-gap diagnostics without recording video or adding a buffer.
- Keep known legacy encoder-runtime metadata compatible with app-owned helper
  updates; the current executable and private-library hashes remain mandatory.
  This prevents an older manifest silently disabling hardware capture after OTA.
- Added native 1280×720 hardware H.264 capture through an isolated encoder
  using a separately verified, device-owned runtime; no vendor binaries are
  included in firmware source or release artifacts.
- Added timestamped H.264 Sat Chat negotiation and relay support with matching
  Tater and Home Assistant bridge changes. Only two qualified Checkers peers
  select this path; mixed devices and older hosts retain JPEG compatibility.
- Added sequence-gap/keyframe recovery, bounded latest-frame queues, and
  cancellation cleanup without changing call audio or microphone capture.
- Kept the decoder input separate from TCP reception so a slow decoder cannot
  accumulate seconds of stale video. Queue overflow waits for a fresh keyframe.
- Accelerated the open pixel converter with byte-exact NEON processing. Live
  H.264 calls require the qualified helper; older runtimes retain ordinary
  media compatibility without advertising the slower live-call path.
- Isolated live-call snapshots from concurrent updates, retained a full startup
  GOP in the bounded receive queue, and added HTTP/picture-progress deadlines.
  Repeated-start hardware qualification is still blocked by a unit that became
  unresponsive during testing; this work is not release-qualified yet.
- Restored native-resolution still snapshots and removed unused full-image
  development from the lower-cost JPEG profiles.
- Added a Checkers-only live-video exposure policy and faster Bayer/tone
  preparation while preserving normal still-photo exposure and privacy gates.
- Qualified a camera clock fix for the system kernel: acquire clock handles
  without requiring always-on camera power, so normal capture selects the
  intended 24 MHz sensor clock. This requires a system/kernel update, not only
  an application OTA. The kernel build and temporary live clock proof passed;
  the kernel is now installed and boot-tested on the development Checkers,
  including 24 MHz clock readback without the lab module. Release qualification
  is still in progress.
- Measured approximately 30 fps for capture/encoding alone and 28.4 fps across
  a five-minute simultaneous capture/local hardware-decode test at 720p.
  Packet-loss recovery and a native still passed afterward. Full-duplex
  two-device calls, extended thermal behavior, and the installed-candidate
  release gates remain pending.

### Checkers GPU UI

- Added retained Mali layers for the home, action menu, climate controls, and
  conversation display, uploading text/layout only when visible content changes.
- Added GPU crossfades for opening/closing the menu and streaming STT/reply
  updates, including smooth interruption when another word or tap arrives.
- Added individual word animation for STT and streamed replies: new words rise
  8 pixels and fade in over 180 ms, corrected partial words fade out over 120 ms,
  and retained words stay steady or scroll smoothly when lines overflow.
- Retained a GPU glyph atlas so text animation sends word positions/opacity
  instead of repainting the full paragraph each frame; the word path requires
  scene protocol v3, while v1/v2 helpers keep their existing rendering behavior.
- Moved the conversation edge glow, reactive reply rim, and thinking dots to
  GPU shaders at the existing 30 Hz display cadence.
- Preserved the CPU renderer and video/media paths, with immediate CPU fallback
  if the optional owner-local GPU runtime is unavailable or fails.
- Kept support for the original rotation helper; retained effects require the
  reviewed helper with scene protocol v2 and the existing device-owned libraries.

### Recovery

- Kept Volume Up at power-on on the native TWRP route for Checkers, Cronos,
  Crown, Athena and Rook, including the Linux-side held-key fallback.
- Retired the automatic TWRP-to-Tater Recovery redirect. System updates remove
  only Tater's known cached command; unrelated recovery commands are preserved.
- Kept explicit USB `to_recovery.py` access to Tater Recovery and `to_twrp.py`
  access to TWRP. Biscuit/Radar's existing recovery behavior is unchanged;
  early-boot improvements are deferred to their hardware work.

## v4.2.3

Tater Echo Firmware v4.2.3 makes wake and command capture resilient to Linux
microphone stalls, preserves directional wake pickup during playback, and
protects the first word spoken immediately after a wake phrase.

This release is delivered by the normal application OTA and does not require a
Tater Linux system update.

### Wake and command capture

- Corrected sample timestamps when splitting microphone capture into 80 ms
  wake chunks, preserving the wake/command boundary through capture queuing.
- Added a tested 80 ms command guard to verified boundary-model endpoints so
  the first consonant is retained when speech begins immediately after a wake.
- Carried capture generations through asynchronous wake inference and
  dual-model agreement, preventing detections from audio before a capture loss
  or microphone restart from opening a new command turn.

### Microphone reliability

- Added capture sequence and producer-generation tracking so subscriber drops,
  ALSA restarts, and sample discontinuities cannot splice unrelated audio into
  wake detection or speech recognition.
- Added a complete-batch progress watchdog and fatal ALSA error handling so a
  stalled Linux microphone is restarted instead of remaining silently deaf.
- Reset partial audio, beamforming history, gain state, and streaming detector
  state after a real capture discontinuity while preserving learned AEC filter
  coefficients.

### Playback and barge-in

- Preserved Biscuit and Radar directional wake pickup during playback by
  selecting the primary array beam before the single shared AEC pass.
- Accelerated near-end speech recovery after assistant replies, music, and
  alarms while retaining correlated-echo attenuation and conservative
  closed-call feedback protection.

### Documentation

- Documented the wake boundary model in the feature overview and updated the
  Biscuit and Tater Native audio-path notes for the continuous primary wake
  beam and capture-recovery design.

**Full Changelog**: [v4.2.2...v4.2.3](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v4.2.2...v4.2.3)

## v4.2.2

Tater Echo Firmware v4.2.2 redesigns Tater Tube now-playing screens and keeps
poster artwork stable while linked players report playback progress.

This release is delivered by the normal application OTA and does not require a
Tater Linux system update.

### Tater Tube now playing

- Rebuilt the rectangular and round now-playing presentations around a
  darkened full-screen poster backdrop, prominent foreground artwork, larger
  series titles, season and episode details, linked-player labeling, and a
  clearer progress bar and elapsed/total times.
- Fixed artwork flashing on every Tater Tube Server activity poll by separating
  static presentation and artwork identities from frequently changing
  progress, lease, and revision fields.
- Kept decoded poster artwork resident across same-event progress refreshes and
  limited animation work to the progress region on Checkers, Crown, Athena,
  Cronos, and Rook displays.
- Fixed smooth local progress interpolation for Companion Display's natural
  playback-speed multiplier while retaining compatibility with the older
  thousandths-based scale.

### Release compatibility

- Added per-target minimum Tater Linux system versions to release manifests so
  Tater can distinguish ordinary application updates from releases that truly
  require a newer root system.
- Preserved the existing v4 system baselines for this application-only release,
  avoiding unnecessary System OTA prompts while keeping cumulative System OTA
  artifacts available for satellites that are genuinely behind.

**Full Changelog**: [v4.2.1...v4.2.2](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v4.2.1...v4.2.2)

## v4.2.1

Tater Echo Firmware v4.2.1 restores the trusted-LAN connection used by
Companion Display media presentations and notifications on screen-capable
Tater Linux Echo satellites.

This fix is delivered by the normal application OTA and does not require a
Tater Linux system update.

### Companion display connectivity

- Fixed the fail-closed Tater Linux firewall silently dropping Companion
  Display traffic even though the Echo successfully advertised the service
  over mDNS.
- Added an idempotent startup repair that permits TCP and UDP port 8930 only
  after the Companion Display listeners bind successfully, immediately fixing
  already-installed screen devices through the normal firmware update.
- Added the same allowlist entries to future Tater Linux system images while
  leaving platforms without the managed firewall unchanged.
- Restored the capabilities endpoint, active UDP discovery, now-playing media
  presentations, and notification cards through the shared Companion Display
  service.

**Full Changelog**: [v4.2.0...v4.2.1](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v4.2.0...v4.2.1)

## v4.2.0

Tater Echo Firmware v4.2.0 adds model-driven wake-word boundaries for fast
speakers and a trusted-LAN companion display service for screen-capable Echo
satellites.

These features are delivered by the normal application OTA and do not require
a Tater Linux system update.

### Wake-word command capture

- Added schema-v4 wake bundles with a small, hash-verified `wake_boundary`
  TFLite model. Echo keeps it resident and replays buffered wake audio after
  any MWW, OWW, NanoWakeWord, or dual-model acceptance to locate the acoustic
  end of the wake phrase before command audio is sent to speech recognition.
- Added the boundary model to the built-in Hey Tater package so existing Echo
  satellites receive it through the normal firmware update without retraining
  or changing their selected detector mode.
- Retained audio from the first threshold-active score in each accepted wake
  window plus an 80 ms bounded safety guard, independently of the final
  detection timestamp, so detector patience and sliding-window confirmation do
  not clip the first word spoken immediately after the wake phrase.
- Let MWW + OWW and MWW + NWW use the earlier bounded command boundary from
  either agreeing detector while keeping MWW's original timestamp for wake
  verification, beam selection, and room arbitration.
- Kept older bundles compatible: a valid acoustic endpoint is authoritative,
  while missing models or inference failures safely retain the guarded detector
  estimate. Added synthetic coverage for every supported single and dual-model
  mode plus installation, hash, timing, and concurrent audio-ring regressions.

### Companion displays

- Added Companion Display v1 on screen-capable Echo satellites, with mDNS and
  UDP discovery, a capabilities endpoint, and a documented trusted-LAN HTTP API
  on port 8930 that works independently of Tater and Home Assistant.
- Added leased now-playing presentations with uploaded JPEG, PNG, or WebP
  artwork, locally interpolated progress, pause state, automatic expiry, and
  source ownership so stale clients cannot clear another active presentation.
- Added bounded temporary notifications with priority, TTL, uploaded artwork,
  touch dismissal, and optional Crown Last Event retention. Notifications
  without artwork use the embedded Tater artwork instead of an anonymous
  placeholder.
- Added responsive now-playing layouts and efficient progress-only redraws for
  the rectangular and round Echo Show renderers, including artwork retention
  across same-event lease renewals and automatic return to the home screen.

**Full Changelog**: [v4.1.2...v4.2.0](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v4.1.2...v4.2.0)

## v4.1.2

Tater Echo Firmware v4.1.2 improves Bluetooth proxy compatibility on older
Echo Linux kernels and preserves command audio spoken immediately after the
wake word.

These fixes are delivered by the normal application OTA and do not require a
Tater Linux system update.

### Bluetooth reliability

- Fixed Radar and Biscuit active GATT discovery on older kernels whose BlueZ
  management layer rejects discovery filters by safely continuing with
  unfiltered low-energy discovery.
- Added bounded retries for transient connection failures, accurate negotiated
  MTU and ATT handle reporting, reliable notification acquisition and fallback
  tracking, and cleanup when adapters, devices, or `bluetoothd` disappear.
- Kept direct-controller GATT paths and authenticated PIN pairing unchanged.

### Wake audio

- Timestamped batched wake-detection scores at the audio samples that produced
  them instead of treating the end of the inference batch as the wake point.
- Split the wake and command boundary at the detector-crossing sample so speech
  immediately following the wake word is retained for command VAD and speech
  recognition.
- Added cadence and lag tracking for native microWakeWord, openWakeWord, and
  NanoWakeWord detectors, plus race and regression coverage for the boundary.

**Full Changelog**: [v4.1.1...v4.1.2](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v4.1.1...v4.1.2)

## v4.1.1

Tater Echo Firmware v4.1.1 improves custom wake-word compatibility and keeps
Biscuit and Radar LED mute state stable across boot and reconnects.

### Wake words

- Added compatibility with standard ESPHome microWakeWord manifests that omit
  `model_format`, `quantization`, and `sample_rate`, covering V1-V3 and V8
  bundle manifests while continuing to reject partial or incompatible
  execution metadata.
- Fixed MWW + OWW and MWW + NWW bundle installation on Echo satellites when
  the bridge supplies the portable ESPHome manifest.
- Reported the active custom model URL and a successful settings generation so
  the controller no longer shows a working custom model as inactive or leaves
  settings confirmation at generation 0.
- Verified the compatibility parser against all 1,072 published wake-word
  manifests in the current catalog.

### LED reliability

- Fixed Biscuit and Radar mute LEDs becoming stuck or flipping after a muted
  reboot by waiting for the early Tater Linux boot-ring animation to release
  the LED hardware before the firmware restores mute state.
- Prevented a final stale orange disconnected frame from overwriting the
  connected, idle, or mute ring after Tater reconnects.

Thanks to [@MartyTremblay](https://github.com/MartyTremblay) and
[@Kollisionskurs](https://github.com/Kollisionskurs) for the manifest
compatibility reports and catalog analysis.

**Full Changelog**: [v4.1.0...v4.1.1](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v4.1.0...v4.1.1)

## v4.1.0

Tater Echo Firmware v4.1.0 adds hardware-tested support for the Amazon Echo
Show 5 second generation (2021), model C76N8S, codename `cronos`.

### Cronos support

- Added a dedicated Cronos factory archive with guarded model detection,
  verified backups, pinned LineageOS driver inputs, a Tater Linux A/B system,
  preserved TWRP recovery, and optional settings-preserving reinstalls.
- Added the native 960×480 Tater display, touch controls, camera, dashboards,
  Sat Chat, climate and camera panels, live conversation screens, clock
  choices, Do Not Disturb, and screen power controls.
- Added Cronos-specific two-microphone capture and AEC, speaker routing, volume
  buttons, hardware privacy controls, wake detection, Sendspin playback, SSH,
  Bluetooth, and application and System OTA support.
- Added Volume Up access to TWRP, Tater boot branding, and verified A/B health
  checks with automatic rollback.

### USB Ethernet

- Hardware-qualified automatic USB Ethernet on Cronos with the powered
  [UCTRONICS B0B9ZLVGJ4](https://www.amazon.com/dp/B0B9ZLVGJ4) adapter and its
  Realtek RTL8152 controller.
- Verified passive MUSB host selection, DHCP, Ethernet-only routing, automatic
  Wi-Fi shutdown, Tater reconnection, SSH, and return to the normal Wi-Fi and
  USB device path.

### Companion app

- Use Tater v2.0.4 or newer for distinct Cronos identification, settings,
  device artwork, and target-matched factory and OTA downloads.

**Full Changelog**: [v4.0.2...v4.1.0](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v4.0.2...v4.1.0)

## v4.0.2

### What's Changed

#### Live conversations on Echo screens

- Added one shared live-conversation display protocol for Checkers, Crown,
  Athena, and Rook, with streamed user transcription and assistant reply text.
- Added a polished full-screen listening, thinking, working, and replying flow
  with theme-aware edge lighting, smooth transitions, and layouts tailored to
  both rectangular and round displays.
- Centered the initial listening prompt and faded it naturally into the first
  transcript instead of abruptly replacing the screen.
- Kept the completed user request visible while Tater thinks and works, then
  transitioned into the assistant reply without competing screen states or
  flicker.
- Supported both streaming and non-streaming speech engines: partial text is
  shown live when available, while a complete transcript or reply appears when
  an engine only supplies final text.
- Ignored late, out-of-order conversation revisions so an older listening
  update cannot replace a newer thinking, working, or reply state.

#### Music display

- Kept Sendspin's timestamped spectrum visualization synchronized while
  calming its attack and release so the bars feel musical instead of overly
  twitchy.
- Fixed the music progress bar rapidly jumping between two positions on
  page-flipped displays by updating the progress region on every rendered
  frame.
- Applied the music display fixes across Checkers, Crown, Athena, and Rook.

#### Reliability and testing

- Added renderer coverage for cached screen composition, conversation state
  ordering, smooth spectrum response, and consistent progress updates across
  framebuffer pages.
- Added the approved live-conversation screen mockup to the firmware
  documentation assets.

**Full Changelog**: [v4.0.1...v4.0.2](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v4.0.1...v4.0.2)

## v4.0.1

### What's Changed

#### Microphone capture

- Raised the calibrated default microphone input gain to +40 dB on Biscuit and
  Radar, whose shared seven-microphone capture path is quieter than the other
  Echo targets.
- Raised the default input gain to +28 dB on Checkers, Crown, Athena, and Rook,
  improving wake detection and command capture while retaining headroom for
  close speech.
- Kept `MIC_GAIN_DB` as an explicit override for installations that already
  provide their own device-level calibration.

**Full Changelog**: [v4.0.0...v4.0.1](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v4.0.0...v4.0.1)

## v4.0.0

Tater Echo Firmware v4.0.0 adds automatic USB Ethernet, future system updates
through Tater, remote maintenance, new satellite controls, and improvements to
screen, audio, recovery, and installation behavior.

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

### Before you update

- **A one-time USB/TWRP installation is required** for every satellite running
  a version earlier than v4.0.0. Download the factory archive made for your
  exact Echo model and follow its included instructions.
- On Checkers, Crown, Athena, and Rook, hold Volume Up while the satellite
  starts to enter TWRP.
- **Pre-v4 Biscuit and Radar:**

  1. Boot the satellite normally.
  2. Extract the v4 factory archive for the device.
  3. Connect the satellite directly by USB.
  4. Open Terminal in the extracted archive folder and run:

     ```bash
     python3 to_twrp.py
     ```

  5. Wait for TWRP to start.
  6. Run the factory installer:

     ```bash
     ./install.sh
     ```

  You do not need to hold Volume Up or enter Tater Recovery. Do not use
  `--repair`; it only repairs an existing Tater system slot.
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

### New Contributor

Contributions from @heapsoftware included in this release:

- [#4 — Automatic USB Ethernet and Tater satellite baselines](https://github.com/TaterTotterson/Tater-Echo-Firmware/pull/4)

**Full Changelog**: [v3.1.2...v4.0.0](https://github.com/TaterTotterson/Tater-Echo-Firmware/compare/v3.1.2...v4.0.0)

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
