# Echo 5 development history

This preserves the pre-release implementation checkpoints. Statements about
pending tests, unpublished builds or closed gates describe their original
checkpoint, not the final release status. See [release acceptance](echo-5-release-review.md)
and [the final changelog](../CHANGELOG.md) for current coverage and features.


## v5.0.0 — release candidate

This source candidate is accepted for release preparation, but is not yet a
published firmware release. Completed USB/hardware checks and owner-accepted
code-review coverage are recorded in the
[release review](https://github.com/TaterTotterson/Tater-Echo-Firmware/blob/main/docs/echo-5-release-review.md).
Remaining physical Radar, clean older-Rook and final device OTA/rollback tests
were waived by the owner, not recorded as hardware passes. Final build/CI and
artifact verification still apply.

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

### Biscuit and Radar: entering TWRP for the USB upgrade

Every pre-v5 installation, including v4.2.3, needs the full matching USB factory
bundle; OTA or `--repair` cannot perform this migration. Settings are preserved
by default; do not add `--factory-reset` unless you want to erase them.

On a Biscuit or Radar already running Tater, leave normal power connected,
boot normally into Tater, connect USB data, and open a terminal in the extracted
factory bundle. Run:

```sh
python3 to_twrp.py
```

On Windows, use `py -3 to_twrp.py`. Wait for the computer to detect TWRP over USB,
then run `./install.sh --verify-bundle` followed by `./install.sh` (Windows:
`py -3 install.py --verify-bundle`, then `py -3 install.py`). No manual serial
commands, Volume Up hold or Tater Recovery entry are needed for this upgrade.
Radar uses the internal USB pads described in its unlock guide. A new device
still needs its matching amonet unlock/TWRP procedure first.

After the v5 USB installation, holding Volume Up during power-on provides the
new early TWRP route, before the main Linux system starts. Keep normal power
connected through the automatic-return recovery handoff. See the
[Biscuit/Radar factory guide](https://github.com/TaterTotterson/Tater-Echo-Firmware/blob/main/factory/dot-linux/README.md)
for recovery requirements and fallback procedures. Biscuit's physical USB and
recovery checks passed on two older units; Radar is accepted by code review.

### Implementation notes and earlier validation history

Pending-test statements below describe earlier development checkpoints. The
release review linked above records the final accepted scope and test waivers.

- Correct Rook's factory-supervised target/camera startup, match its actual
  boot identity during automatic runtime setup, and label Device Info as Rook
  rather than Checkers. Retain exact kernel/panel/platform/runtime checks.
  The corrected full USB reinstall, preserved settings, normal health restart,
  active GPU and owner functional checks passed on the development Rook.

- Prepare hash-pinned public Android inputs automatically before screen-device
  USB installation, then assemble GPU/video runtimes with this unit's retained
  vendor files after first boot. No FireOS backup, NDK, mounted images or extra
  filesystem tools are needed. Verify complete staged/existing directories,
  refuse changed vendor files and unsafe links, support interrupted-root retry,
  and confirm a new target-verified boot after installation. Vendor binaries
  remain device-local and are never included in public Tater archives.
- Restart after hardware-library setup with the installed Tater reboot helper,
  not a rescue-only BusyBox path, while still requiring a new verified boot.
- Clear Athena's nested persistent application slots during a full USB install,
  so an older app cannot override the newly installed firmware. Preserve pairing,
  Wi-Fi, SSH and satellite settings by default; regression-test the real nested
  layout and the separate factory-reset path. Unknown upstream cleanup code is
  refused rather than patched speculatively.
- Apply Athena's verified AP camera opt-in to the factory-supervised camera as
  well as application updates. Missing/mismatched boot readiness, runtime files
  or the encoder device keep the JPEG fallback; GPU playback is independent.
- Isolate Checkers' retained-Lineage decoder/encoder ABI in new versioned runtime
  paths, preserving its camera conversion and keeping the old FireOS ABI separate.
  Match Sat Chat's independent live-decoder hash gate to that exact Checkers ABI;
  retain the original hashes for FireOS and every other target.
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
