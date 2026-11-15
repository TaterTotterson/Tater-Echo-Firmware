# Checkers hardware release qualification

This is the release gate for the unpublished Checkers hardware/camera work.
Passing individual hardware probes is not sufficient to publish a release.
Do not enable the new target-specific paths on other Echo models from these
results alone.

### Hardware restoration after controlled comparisons

The operator reports smooth normal camera preview but persistent Checkers
Sat Chat jitter, including a watched CPU-only display comparison. The normal
GPU renderer was restored immediately after that test. The operator requests
restoring hardware capture and investigating Tater's call transport instead.
The development source therefore re-enables the existing target/runtime-gated
H.264 integration, retaining balanced geometry, encoder color fixes, pacing,
GPU scenes and hardware decoding. MJPEG fallback and its motion improvements
remain. No experimental ISP path is enabled.

`v4.2.3-checkers-hardware.1` and `v4.2.2-cronos-hardware.1` are now installed
through normal verified app OTA, retaining `mjpeg.3` for rollback. Both report
GPU scenes, hardware H.264 and balanced support. All nine installed Checkers
hashes match its bundle; camera, FFmpeg, GPU and codec helper files are unchanged
from `mjpeg.3`. Both normal ARMv7/ORT builds, five Linux race-tested video/GPU
packages and 25 rootfs/hardware tests pass. Normal five-minute app health checks
are still running at this checkpoint. Tater was not modified during restoration;
live-call jitter remains unresolved and the release gate stays open.

### Earlier installed MJPEG motion trial

`v4.2.3-checkers-mjpeg.3` and Cronos `v4.2.2-cronos-mjpeg.3` are installed
through normal native application OTA, retaining `mjpeg.2` for rollback. Both
report GPU scenes active. The original pinned Checkers schema-1 runtime
metadata is accepted without changing its libraries or weakening the current
firmware-owned helper hash check; all nine installed Checkers files match the
trial manifest. Kernel, recovery and private runtime files were not replaced.

Live 480×270 MJPEG now holds the existing qualified motion-exposure lease,
independently of H.264 encoding. Tests cover normal exposure restoration,
failed/double release, qualified targets, stable-scene transitions and native
still geometry. Linux camera/GPU race tests, 25 rootfs/hardware Python tests,
both ARMv7/ORT builds and whitespace checks pass. A bounded camera-only
host-delivery comparison measured about 12.5 FPS on Checkers (previously about
6), and 11 FPS on Cronos. These are not 20 FPS or full-duplex guarantees.
An installed Checkers still probe verified 1280×720; no scene pixels were saved.
Checkers' normal A/B health trial committed after the actual 300-second daemon
and renderer interval; its pending marker is absent and GPU scenes remain active.
The operator's mixed-call motion/brightness/grain review remains to be recorded.
This is an unpublished trial, not release approval.

### Earlier mixed-call checkpoints (November 13)

The operator requested a capture-only return to the existing MJPEG path after
the verified GPU Bayer-conversion trial made no visible improvement. Source now
disables local H.264 capture/advertisement and maps stale H.264 requests to
balanced MJPEG, while preserving incoming hardware decoding and GPU UI/video
presentation code. The local `v4.2.3-checkers-mjpeg.1` / Cronos MJPEG trials are
installed and connected with H.264 capture no longer advertised. Camera,
FFmpeg, GPU and decoder helper binaries are byte-identical to the prior apps.
The operator initially reports better motion but no picture on Cronos;
Checkers is hardware-muted, blocking its outgoing camera. Checkers also reports
GPU scene fallback after reboot and needs its local log checked. Visual and
normal A/B health qualification remain open; do not claim GPU active on both.

Both devices now run the subsequent `mjpeg.2` application trial: live MJPEG
is sampled directly at 480×270 with a 20 FPS ceiling, retaining JPEG quality,
framing, native stills and playback helpers. Linux unit/race tests and complete
pixel comparisons for both sensors pass. Checkers' short host-delivery timing
remains approximately 6 FPS (baseline about 5.7, trial about 6.1); Cronos sends
about 10–12 FPS and the operator reports it visibly smoother. Cronos receives
and displays Checkers' same approximately 6 FPS, locating the remaining low
cadence before Cronos playback. Do not assume the Checkers cause is exposure
or GPU without its local timings/logs; USB access has been requested.

Direct USB inspection now confirms the installed reduced-resolution camera and
the correct target/encoder-off environment; the `mjpeg.2` app is committed.
GPU startup fails on the original schema-1 manifest's known prepared-helper
hash, matching the research runtime-preparation script. A narrowly scoped
compatibility correction is prepared in source, retaining the independent
current-helper/private-library checks; it is not installed yet. A bounded
normal-camera debug run (no pixels saved) also shows shutter increasing to
2,503 lines against a 798-line nominal frame, and about 9.8 FPS with 32 ms JPEG
encoding in that test scene. Long-exposure policy therefore remains a separate
source-rate limit; it was not changed by the resolution update. The temporary
helper exited cleanly and released camera ownership. Motion-priority exposure
for MJPEG needs an explicit decision because it trades low-light noise for FPS.

The installed ui.13 legacy-manifest correction restored both hardware
capabilities, and the next real call negotiated `echo-h264-balanced`. The
operator still observed intermittent jitter and incorrect colors on both
screens. This is not a visual-quality qualification.

Generated blue/red software H.264 decoded through Checkers' production hardware
path produced exact mean RGB [0,0,255] / [255,0,0]. A separate generated moving
YUV pattern through its encoder, decoded with independent software, proved its
U/V input planes were reversed despite the advertised planar format. The
corrected OMX-boundary adapter reduced U/V mean absolute error from about 42
levels to 0.54/0.70, with luma unchanged at 0.70. No camera pixels were retained.
Clean locked builds reproduce the corrected helper. ui.14 is installed through normal native application
OTA, reconnected with both hardware flags, and retains ui.13 for rollback.
Cronos' separate generated-color test confirmed the same encoder-only U/V bug;
its correction also reproduces the intended planes within one quantization
level. ui.14 is not yet visually qualified; live mixed-call verification remains required.

Concurrent status/tiny-request tests also reproduced 250–700 ms host-loop
stalls. Tater now reads full live settings off-loop after releasing its client
lock, and Sat Chat requests lightweight snapshots without settings. Regression
tests pass in both source and bundled test backend. The operator restarted Tater;
the paired-request comparison now measures 2.5 ms typical/21.9 ms maximum tiny
request latency while full status/settings reads run. Live retest is still
required. Neither change justifies claiming all jitter is resolved.

The updated Checkers and Cronos connected using H.264, but the operator
reported substantial lag. Hardware encoding/decoding and GPU composition
were active; this is **not** a passed two-device performance qualification.
Three JPEG-style one-deep video queues (satellite sender, host relay and
satellite receive relay) were found to discard predictive dependencies.
Regression tests reproduced the bugs. The fixes preserve bounded short GOP
bursts, wait for a fresh keyframe after overflow, and avoid replaying packets
older than a priming keyframe. Audio/control priority and latest-only JPEG
preview behavior are retained. CPU scaling was optimized with pixel-exact
tests, but is not yet GPU scaling. Go race tests pass.

The subsequent unpublished `v4.2.3-checkers-ui.10` app is installed and reconnected
through the production SHA-verified OTA transaction. The shared updates decouple
decoder draining from CPU fitting and pace independent raw sensor frames before
encoding at a maximum 15 FPS for live calls only. No rootfs, kernel, recovery or
proprietary driver/runtime replacement is needed. The operator reported much
smaller delay, but still sees intermittent repeated-frame-like jitter on both
screens, most clearly on Checkers. Cronos' decoder queue stayed empty and its
source-PTS backlog did not grow over several minutes; these are not Checkers
receive-side or end-to-end latency measurements. Smoothness qualification remains
open. A subsequent app candidate presents ready live pictures without waiting
for the menu-animation tick and adds bounded gap timing diagnostics; unit and
race tests pass; the operator's hw9/ui.11 comparison found jitter unchanged.
Its frame-gap diagnostics place the larger pauses before presentation.
The Tater test backend already
contains its matching relay and directory-fanout fixes and was restarted by the
operator. Earlier UI smoke results below do not waive these open video gates.

The operator approved a screen-sized H.264 comparison: Checkers 768×432 and
Cronos 640×480, retaining the sensor aspect ratios and full-screen layout.
Both peers must explicitly advertise support; older peers retain the previous
profile. The installed ui.12/hw10 call was reported much smoother, but was
actually JPEG fallback, **not** balanced H.264. Checkers' on-device schema-1
runtime manifest named the previous encoder helper, causing capability preflight
to reject the updated app's correctly pinned executable. A regression reproduced
this. The corrected helper accepts only known historical target-owned manifest
metadata and still verifies the current executable and library hashes separately.
An on-device no-capture comparison returned `camera_h264=false` for ui.12 and
`true` for the corrected helper, without changing private libraries/manifests.
The corrected ui.13 installation and actual balanced hardware call remain pending.

**Status: release blocked.** The operator's power cycle restored USB, Wi-Fi,
display and microphone service on known-good slot A. No saved panic dump was
available to establish why the earlier repeated-start test made the unit
unresponsive. Normal codec cancellation was found to force-kill the helper
before OMX cleanup; corrected wrappers are being qualified, not yet declared
a proven fix for the whole-device failure. Corrected RC.5 has now booted in
B, passed its authenticated five-minute health trial, and rebooted with B
marked good. The operator then chose native TWRP as the Volume Up default;
RC.6 implements that policy and is entering qualification in A, with good
RC.5 retained in B and the old A rootfs archived privately. RC.3/RC.4 are
superseded. Extended A/V, runtime rollback, real-call and distribution gates
still apply.

### Workspace UI installed smoke test

On November 13, 2026, the operator authorized installation of the new shared
workspace UI on this Checkers. The unpublished `v4.2.3-checkers-ui.1` rootfs
(54,507,761 bytes) has SHA-256
`16641ecb409af2c09802b7ecbcab5abfa7d950aa50687d2b947fc60a20f7dd6c`.
It was built with the normal ARMv7/ONNX release-build path and the locked
public hardware-helper set, uploaded with on-device SHA-256 verification,
staged in inactive B with A still selected, and its installed binary hashes
checked before activation.

- B is now running and **good** after the actual connected five-minute health
  window and its automatic restart. Root and store returned read-only.
- Tested RC.6 remains **good** in A. The replaced RC.5 B rootfs was backed up
  privately on the device before replacement; its 98,227,484-byte gzip passed
  integrity checking and has SHA-256
  `75a3c0833b7433b075e02c008d49fb1032576f710028222aa5c5efa814f86ae2`.
  A second raw USB download to the Mac stalled and is retained only as an
  unverified `.partial`, not a usable restore copy. The verified on-device
  backup and local RC.5 build artifact remain available.
- The operator passed the menu layout, fade, touch controls, sliders, and a
  subsequent wake/request with immediate first-word capture.
- After the automatic restart, Tater connected again; daemon, renderer,
  camera and the owning rootfs' GPU helper were running. ALSA capture was
  running with advancing pointers; no microphone audio was saved.
- The Tater theme and initial Fade default were observed. Safe preferences
  are mode 0600; the operator's later Slide selection was left unchanged.
- The kernel, boot/recovery partitions, private runtime libraries, pairing,
  and credentials were not replaced. No Git push, tag, or release was made.

This is an installed UI smoke test, not the extended video/call/thermal or
distribution qualification below. Those release gates remain open.

### Workspace UI.2 application test

On November 13, 2026, the operator authorized the subsequent home/connected
screen refinements, full-image notification fix, and installation on Checkers.
The unpublished `v4.2.3-checkers-ui.2` application bundle is 22,483,434 bytes,
SHA-256 `8352fc9f56e41da4b81ff061d6ae1c56bb908dc885b934d161f78ef69e709928`.
It uses the production hash-verified A/B application transaction on `/data`,
not a rootfs, kernel or recovery update.

- The new daemon/renderer are running in application slot A and connected to
  Tater. The actual five-minute application health trial passed: the normal
  boot health process committed at 311.81 seconds uptime after both had stayed
  healthy for 300 seconds. This app-only commit does not require another reboot.
- System slot B remains the unchanged, good `ui.1` fallback; the application
  rollback state points to `system`. Good RC.6 also remains in root slot A.
- All older application slots were backed up privately before staging. The
  on-device 77,663,954-byte archive passed gzip integrity, is mode 0600, and
  has SHA-256 `b9ddc05ef4afa1d8f368959729203d70ba56c6d5b21e98506204d14b02a7a60d`.
- Installed component digests match the bundle. Camera, FFmpeg and public
  hardware helpers are the exact `ui.1` components, not new codec/GPU binaries.
- GPU compositor/retained UI and touch input opened after reboot; root and
  store are read-only. Pairing and saved screen preferences match their
  pre-restart digests; preferences remain mode 0600.
- ALSA capture remained RUNNING with advancing hardware/application pointers
  throughout the trial. No microphone audio or camera frames were saved.
- Notification artwork now contains the complete supplied image even when
  older senders request cover/fill. Camera/call controls remain outside video;
  their static UI is not rerasterized for each decoded frame.
- The operator confirmed the new menus look good, the complete supplied
  notification image is visible, and a wake/request with immediate speech
  wakes normally and captures the first words. These `ui.2` smoke checks pass.
  No push, tag, release, or new extended video qualification is implied.

### Workspace UI.3 paging regression test

The operator reported that Sat Chat Next/Previous changed the selected page
but could leave the old pixels displayed. The retained GPU source key omitted
both Sat Chat paging and participant contents. The regression was reproduced
with Fade, Slide, and Off before fixing source/surface invalidation and adding
tests for rapid reversals, participant updates, stable snapshots and clamping.

The unpublished `v4.2.3-checkers-ui.3` application bundle (22,484,007 bytes,
SHA-256 `85b37574e7583a80f867310ab0d7339b2f09d39b1202b3b60a6dc59132a62556`)
was staged using the production transaction and booted in application B.
Its actual connected health trial committed at 312.93 seconds uptime. The
operator then confirmed Next/Previous responds promptly, including repeated
taps. No release is implied by this passed paging smoke check.

Before staging, all application generations were archived privately on the
device as `development-backups/checkers-ui.3-before-app.tar.gz` (81,442,281
bytes, mode 0600, gzip integrity checked), SHA-256
`8943a885e7aca7c83b19306d8bab2cbb8f98e89c6aa2f224f918d6e02a77a7c9`.
The UI.2 application remains in A, and good UI.1 system B is unchanged.
Pairing retained its previous digest. Screen preferences changed during the
interactive test interval; their digest is not claimed to be unchanged.

### Workspace UI.4 full-screen video application test

The operator then requested maximum landscape video area with titles/buttons
over the picture. The local `v4.2.3-checkers-ui.4` application bundle is
22,490,210 bytes, SHA-256
`4184d79e3c7c52fad4cb7beddf8210ec017a7d6b5c25be583312dfec51b4c38b`.
It was staged through the same production verified transaction in application
A with the committed UI.3 B retained as previous. Inactive A and `.a.old`
contents are preserved by the verified private UI.3-before-app archive above.
It has booted the expected renderer/daemon in A and connected to Tater. GPU
retained scenes are active, microphone capture is RUNNING with advancing
pointers, pairing retains its previous digest, and root/store are read-only.
Allowlisted preferences remain mode 0600, but their runtime-synchronized
digest changed; byte-identical preferences are not claimed. Its actual normal
connected health trial committed at 312.61 seconds uptime after the full
300-second healthy interval, not inferred from a successful staging message.

- Landscape camera/call pictures use the complete even viewport, retaining
  source/display aspect and rounded actual-picture corners. View-only and
  talkback share stable geometry. Rook's existing round-safe layout is kept.
- Existing GPU v3 support retains a bounded 1024×512 floating-control atlas.
  New decoded pictures do not rebuild/upload controls; control-only updates
  retain the picture. Unchanged state submits no job. CPU fallback blends
  cached rectangles, including an immediate unobstructed mute badge.
- The explicitly gated owner-device **offscreen** test used synthetic
  pictures, not camera/microphone capture or framebuffer presentation. Camera
  composition reached 38.8 FPS and call composition 38.1 FPS over 60 frames
  each. No pixels exceeded an 8-channel-level CPU/GPU comparison tolerance.
  An earlier non-power-of-two atlas showed edge-filtering differences; the
  revised atlas passed without replacing the qualified helper or libraries.
- Software regression/race tests and normal pinned ARMv7 builds passed.
  Kernel, rootfs, camera helper, FFmpeg, GPU/codec helpers and private libraries
  were not updated. The operator passed the larger-picture/readable-controls
  and smooth live-playback smoke check. A Sat Chat peer/codec was not separately
  identified, so no two-Checkers H.264 qualification is inferred. Synthetic
  composition speed does not qualify capture/decode/network FPS, extended
  calls, thermal load, or a release. Nothing was pushed/tagged.

### Workspace UI.5 directory, EQ and home refinements

The next operator-requested local application is `v4.2.3-checkers-ui.5`,
22,503,248 bytes, SHA-256
`c9ef3fc882c6547e45cf5662051df6ebb82a64c0b23a428f4d14fa69c2596a1a`.
It was staged by the normal verified transaction in B, retaining committed
UI.4 A for rollback; its installed binary digests were checked before restart.
Inactive B, its `.b.old` generation and the prior application link were backed
up privately in `development-backups/checkers-ui.5-before-inactive-b.tar.gz`
(41,448,211 bytes, mode 0600, gzip verified), SHA-256
`f8f5c8b9c9f68f5767195b8d021b6c749347647940616c0937e0e05c2d52e8b7`.

- Camera/Sat Chat rows now show immediate touch highlighting and a retained
  Opening/Starting state while awaiting the actual host response. Requests
  are not fabricated as active calls or streams. Duplicate pending taps are
  suppressed, and timeout/disconnect permits retry. Dragging cannot select a
  row; busy/unavailable rows remain disabled. CPU and GPU cache keys include
  this feedback. Sat Chat skips rasterizing the fully hidden home view.
- Privacy-safe timings record request, host-state acknowledgement and first
  picture presentation without IDs, names, URLs or media content. This does
  **not** establish a measured cause or reduction in real host/stream startup
  delay. The subsequent operator retest confirmed immediate selection feedback.
  Its one observed Sat Chat attempt received call state at 3.705 seconds and
  presented its first picture at 4.970 seconds. This is startup wait rather
  than a reproduced stale-page bug, but it includes host/native setup and does
  not isolate a server cause. No separate camera timing was recorded.
- All eight existing EQ bands and Presence boost fit one landscape page.
  Vertical faders retain ±12 dB limits, half-decibel steps, local drag preview
  and validated release-only commits; other bands/settings remain unchanged.
  Rook retains its round-safe paged EQ until its own device refinement.
- Conditions move before Feels like in the home footer. Optional room
  temperature appears beside the room name, unlabelled and not duplicated.
  A green connected/idle dot returns between assistant and room, retaining
  mute/offline/voice-state colors. The configured assistant first name still
  supplies the displayed name, falling back to Tater.
- Full regression and renderer/framebuffer/video race checks, pinned ARMv7
  build and offline preview inspection passed. Installed/operator checks and
  the actual automatic health trial are recorded separately, not assumed.
  Kernel, rootfs, capture, wake/STT, codec/GPU helpers and private libraries
  were not changed. No push, tag or release.

The expected UI.5 renderer/daemon are now running in B and connected, with GPU
retained scenes active, advancing RUNNING microphone capture, and root/store
read-only. The operator confirmed immediate directory feedback and approved
the vertical EQ and revised home layout. Its normal health trial is still
in progress at the last check; UI.4 A remains the committed rollback target.

## Current candidate

- Current recovery-policy candidate: `v4.2.3-checkers-rc.6`, SHA-256
  `17a42585bc9d2a3bc970af77ea6610744330859f59b8f46078bf2dd413cb7934`
  (53,439,098 bytes), reproduced twice byte-for-byte. It restores native TWRP
  for Volume Up, passed its real authenticated health trial in A and rebooted
  with A good and read-only mounts. Kernel and private runtime libraries are
  unchanged. Native TWRP stay and normal return to good A both passed.
- Qualified development RC.5 remains good in B (SHA-256
  `627abd3acaf4fdfad072859607fb38821ba788a78a070f61bb9e43f7072fdcce`).
  Two builds reproduced that 53,440,984-byte archive. It passed its real
  health commit and normal reboot; no production release is implied.
- Before replacing inactive A, its exact `hardware-dev.3` rootfs (including
  private vendor data) was backed up on the device and ignored Mac workspace;
  archive SHA-256
  `0c5ad2c0661480e4a841b35389ad7c2bc3dcabdcafe2db1e6f914b6763c7879c`.
  The backup must never be published. The replaced inactive RC.3
  artifact remains preserved in the private workspace (SHA-256
  `094f15ffb2b4636b371baefbd580d9eeeb80f46d237161baba762558b5d3ccf6`).
- Kernel: `4.9.337-g8d928c5176cc`, image SHA-256
  `758cb695409303453232e4313574af005a670619007df47c479afb473344e957`.
- Candidate boot image: SHA-256
  `318f509bebc5cae3ed9ad7ab66ac9381a348535ba661b4bc0460800d3a519aa4`;
  original rescue ramdisk preserved byte-for-byte.
- Vendor-module symbol versions are unchanged from the previously validated
  kernel. No rejected experimental JPEG decoder is present.
- Device-owned GPU/codec runtime files are separate private installation
  inputs, not public firmware payloads.
- The qualified open NEON decoder helper has SHA-256
  `56091ad1146815d30be313be9e08cb671640033756de14f294f04ab04ef6ce91`.
  A clean rebuild from every source/object reproduced that exact hash.

## Gates

- [x] Exact boot and recovery backups retained locally and on the unit.
- [x] Retained TWRP boots and provides a root USB ADB shell.
- [x] Complete kernel, daemon, renderer, and camera helper build.
- [x] Protocol, codec cleanup, camera, host negotiation, and rootfs tests.
- [x] Boot the new kernel and verify wireless, audio, display, and camera clock.
- [x] Preserve known-good active slot A while staging earlier RC.3 in inactive
  B with `--keep-active`. The replaced dev.1 artifact is preserved locally.
- [x] Recover console/network access, fix reproduced startup/teardown defects,
  and stage/retest corrected RC.5. The earlier whole-unit hang's precise cause
  remains unconfirmed; this check does not claim otherwise.
- [x] Activate the candidate and pass the real authenticated slot-health window.
- [x] Verify GPU word animations and native camera encoding after a normal
  reboot, without temporary bind mounts or the lab camera-clock module.
- [x] Operator-check physical privacy, wake and first-word capture,
  touch/volume, and spoken-response interruption on the installed candidate.
- [x] Verify the earlier physical TWRP-to-Tater Recovery handoff (historical
  RC.5 qualification; the operator subsequently chose native TWRP instead).
- [x] Verify RC.6 retires the cached redirect, passes its real health trial,
  and stays in native TWRP without an automatic reboot (USB native-recovery
  entry; physical Volume Up's native TWRP entry was established earlier).
- [x] Complete supervised manual return from recovery through retained good A
  to corrected B, with connection, ALSA capture and B's GPU UI restored.
- [ ] Qualify version-compatible runtime rollback; the old A currently uses
  safe CPU fallback with the newer shared GPU helper.
- [x] Complete a five-minute native camera/relay/decode test with bounded
  memory/temperature monitoring, then packet-loss recovery and a native still.
- [ ] Complete extended hardware-media/A/V, thermal, and cancellation tests
  on the installed final candidate (the in-memory probe is not this gate).
- [ ] Complete a real two-Checkers H.264 Sat Chat call, including both video
  directions, audio, mute/privacy, call end/restart, and loss/reconnect behavior.
- [ ] Qualify the normal installer/update distribution path: correct kernel,
  FFmpeg features, runtime provisioning, target checks, and dependency pins.
- [ ] Reproduce final release artifacts and ensure they contain no private
  device libraries, credentials, calibration, backups, or lab-only modules.

## Performance contract

Current bounded development measurements are approximately 30 fps for native
1280×720 capture/encoding alone, and 28.4 fps over five minutes when the same
unit concurrently captures and hardware-decodes through the loopback relay.
This does not include the real display, network host, or peer device and is
not a promise of 30 fps full-duplex calls. Repeated packet loss recovers at
keyframes but reduces delivered FPS. Longer thermal and real-call results
must be recorded before advertising stronger guarantees.

## Recovery caution

This unit's TWRP deliberately redirects `by-name/recovery` to `swdl` in its
`fix-symlinks.sh`; the physical `PARTNAME=recovery` partition still contains
the stock recovery. Both corresponding images were backed up. Preserve that
intentional bootloader arrangement; identify the actual image and validate
its recorded hash before any restore. No recovery partition was changed for
the camera-kernel trial.

## Installed-kernel result

The candidate boot image was written only after TWRP root ADB and the saved
boot identity were verified. Readback matched the candidate's complete used
extent. Normal boot reached the retained good system slot A with kernel build
`#4`, the original exact configuration hash, working Wi-Fi, ALSA card,
microphone capture, framebuffer, touch, and ambient-light input.

Without loading the lab clock module or forcing the CPU floor, camera
readback showed 24 MHz and a 20-second native capture test returned 594
pictures (29.70 fps). The first five-minute concurrent relay/decode soak
instead ended unexpectedly after 187.86 seconds. It returned 3,589 decoded
pictures at 19.10 fps, with broadly stable available memory and a highest
sampled thermal value of 64.4°C. That build was rejected, not counted as a
passed soak. Normal camera and microphone services were restored afterward.

## Receive-backlog and conversion fixes

A trace of the original receiver found approximately four seconds of stale
video already queued after sixteen seconds. A dedicated network reader now
continuously drains the loopback TCP connection into a bounded
queue. Overflow discards the dependent GOP and waits for an independent IDR;
it cannot arbitrarily discard P pictures and continue decoding corrupt state.
Bounding the queue alone caused repeated decoder restarts because the scalar
conversion still could not keep pace.

The open converter now uses NEON to detile and convert sixteen pixels per
iteration, preserving scalar clipped-tail behavior. Randomized, minimum,
maximum, odd-crop, padded-stride, and 720p tests match the scalar reference
byte-for-byte. On Checkers, old and new helpers each decoded and hashed all
180 B-frame fixture pictures to the same pixel and PCM SHA-256 values.
Older verified helpers remain usable for ordinary media; live H.264 call
preflight requires the newly qualified helper rather than advertising a path
that cannot sustain simultaneous capture.

With both fixes, the five-minute in-memory production-relay test completed:

- 8,521 captured pictures, 8,517 hardware-decoded pictures, 28.39 decoded fps;
- first picture 761.6 ms, last picture 299.974 s, maximum picture gap 77.9 ms;
- warmed-up available memory approximately 683,836–686,036 KiB;
- highest sampled thermal reading 67.3°C; this is a bounded observation, not
  a thermal safety or equilibrium claim;
- no saved scene pixels, camera-clock test module, or forced CPU floor.

The subsequent thirty-second loss test deliberately omitted nine predictive
pictures, delivered 645 pictures at 21.50 fps, recovered through the end, and
had a maximum picture gap of 1.24 s. A native 1280×720 JPEG snapshot succeeded
immediately afterward. The original microphone process remained running and
all test camera/codec processes were reaped.

The new helper also completed the 1,200-picture B-frame media fixture,
hashing 2,211,840,000 RGBA bytes and 3,840,000 PCM bytes in 112.160 s. This
includes diagnostic hashing overhead and is not the playback rate. A forced
five-second cancellation reaped the decoder and FFmpeg; normal microphone
capture continued. Real display/audio timing remains an installed-candidate
gate, not something this headless probe establishes.

RC.3 was independently rebuilt twice to the exact rootfs SHA-256 recorded
above. The archive contains no owner-local GPU/OMX libraries, pairing file,
or lab probe paths. It is staged in inactive slot B (`trial 3`); slot A remains
active, running, and good. The qualified NEON and GPU-word helpers are saved
privately but have not replaced the old slot's default runtimes. RC.3 must
still pass the installed-candidate gates above with those helpers installed.

## Repeated-start blocker (later qualification)

The exact packaged camera helper was verified against the RC.3 archive and
tested with a Go 1.24/ARMv7 probe matching the release toolchain. One five-minute
run captured 9,077 pictures at 30.25 fps but produced **zero decoded pictures**.
Short reruns could pass, and another twenty-second run also returned zero
pictures with the relay URL established. This is intermittent, not evidence
of a compiler defect. A prior successful five-minute run cannot override it.

Two independently reproduced software defects are addressed in RC.4:

- Sat Chat snapshots retained mutable live pointers after releasing the lock.
  Local readers and JSON writers now receive owned call/participant snapshots.
- An eight-picture queue can discard the first GOP while OMX is still
  initializing. It now retains one 30-picture GOP plus lookahead (32 pictures,
  bounded to 32 MiB at the absolute wire limit, normally about 250 KiB).

There are also bounded local HTTP startup and eight-second picture-progress
checks, so a responsive userspace can retry a stalled live path independently
of audio. New regression tests pass on Linux/ARMv7 Go 1.24 and under the Go
race detector. These changes do **not** establish the cause of the later
whole-unit unresponsiveness or prove recovery from a blocked kernel driver.

During repeated starts with these changes, one attempt failed before feeding
decoder input; a later attempt stopped responding on USB and Wi-Fi. Ctrl-C,
console flow-resume, and the diagnostic break request produced no console
response; SSH/ping also timed out. No additional partition writes, reboot
markers, or runtime replacements were attempted. Original-service restoration
cannot be confirmed until the unit responds or is power-cycled. RC.4 has built,
but must be retested after recovery; do not mark this qualification passed.

After the operator power cycle, USB and Wi-Fi returned on the retained good
slot A. ALSA capture pointers advance, and the expected daemon, renderer,
camera and GPU helper are running. No pstore or last-kernel crash report was
available. Retained pre-reboot test logs were copied to the private workspace;
their archive SHA-256 is
`60ae809faf19243aad16911917f32e69a354697b463f399455bfef0235704fdb`.
The Mac's configured Tater port is now listening, but that alone does not
establish an authenticated candidate-slot health commit.

## Graceful codec cancellation qualification

The old camera request context and live-decoder context directly cancelled
`CommandContext`, which sends SIGKILL to the helper process group. This is an
unsafe normal teardown path for stateful OMX hardware: it bypasses the
component's EOS/Idle/Loaded release. It is a plausible contributor to restart
failures, not a confirmed explanation of the whole-device unresponsiveness.

Normal call cancellation now stops the producer and drains codec output
before waiting for the helper, with forced termination retained as a bounded
timeout fallback. The camera producer finishes its current framed raw record
before closing stdin. Output is drained concurrently with that final write,
so pipe backpressure cannot deadlock the cleanup itself. Cancelled live
decoder output is discarded while draining, and closing input unblocks a
pending writer without immediately killing the process.

Portable encoder tests exercise a complete large input record, output pipe
backpressure, and a stalled producer/process. Linux decoder tests cover both
reset and already-ended call contexts, including the forced-timeout fallback.
The new tests pass with the race detector, and the camera adapter plus driver
tests pass in Linux. The pinned ARMv7 Go 1.24 test/build also passes. Hardware
helper diagnostic retention is bounded to 8 KiB so a long final lifecycle
summary does not hide the preceding failure stage; FFmpeg URL-bearing text
remains opaque.

On the recovered unit, an isolated eight-second camera encode test captured
231 frames (28.87 fps). Two subsequent six-second starts captured 175 and
176 frames (29.16/29.33 fps). All three returned success and explicitly logged
clean encoder shutdown. An isolated synthetic decoder test consumed and
decoded all 30 frames, with `loaded_after_buffers=true` and successful exit.
The original microphone process remained running. These short isolated tests
are not substitutes for the repeated concurrent and installed-candidate gates.

Three subsequent short concurrent capture/production-relay/decode runs also
passed (329, 271 and 277 pictures; approximately 26.8–27.4 decoded fps), with
clean encoder exits, decoder reaping, and no retained test codec owners.
No image or microphone samples were saved. This establishes bounded repeat
success, not a long-term reliability guarantee.

The subsequent five-minute run completed 8,646 captured / 8,643 decoded
pictures in 300.112 seconds (28.80 decoded fps), first picture at 716.8 ms,
last at 299.999 s, and maximum picture gap 88.2 ms. Warmed available memory
was approximately 646,444–648,564 KiB. Highest sampled thermal value was
70.7°C; this is not a thermal equilibrium or safety qualification. Encoder
shutdown completed cleanly and the decoder was reaped. Original ALSA capture
continued and the original camera service resumed. Collected dmesg had no
matching panic/oops/hung-task or codec failure messages; absence of those
messages does not prove the earlier failure is resolved.

The private metadata/log archive SHA-256 is
`ea3505dda8ddecc568f1b889faf9e9f500217f11187ff92f5f15b2269cb191a0`.
RC.5 was then staged into inactive B while active/booted A remained good and
`/store` returned read-only. No kernel, recovery or default runtime was
replaced during this qualification.

For the subsequent supervised installed trial, the original GPU/VDEC helper
and manifest pairs were saved both on the device and in the private Mac
workspace (archive SHA-256
`f4f5e349e1a0e13a7f492c1fca072f5bbea4862ee170b5ddb76a699fce1294a0`).
Only the open helper/manifest pairs were replaced with the exact qualified
GPU-word and NEON versions; proprietary libraries remained untouched.
RC.5 then booted B, connected to Tater, and logged active GPU retained UI.
The running renderer, camera and GPU helper hashes match the built candidate.
The normal camera service advertises native H.264 capability without overrides.
The operator confirmed two immediate wake/request attempts capture the first
words and that on-screen text animation looks good. This is an operator
observation, not a latency benchmark or broad AEC qualification.

The operator also confirmed working touch/menu and volume controls, that
microphone mute blocks wake until unmuted, and successful wake interruption
of a longer spoken response with the first words of the new request intact.
An installed-service 20-second native capture/production-relay/decode check
without alternate helper/manifest paths delivered 549 pictures (27.29 fps),
continued through the end, and logged clean encoder shutdown.

Before installing, a thirty-second deliberate-loss run dropped 13 predictive
pictures, decoded 550, recovered through 29.974 s, and had a maximum gap of
1.310 s. The decoder was reaped and encoder exited cleanly. Loss handling
continues to trade delivered frame rate for valid keyframe recovery.

The operator confirmed that closing the physical camera cover blocks the
preview and reopening it restores the picture. They have only one Checkers
available, so the two-peer hardware-call gate cannot be completed now.
RC.5 passed the actual five-minute connected trial, automatically committed
B good, and rebooted into B with read-only mounts, native connection, GPU
helper, camera service, and ALSA capture restored. No manual commit or health
marker was fabricated.

The operator then physically power-cycled with Volume Up and saw TWRP hand
off to Tater Recovery. The USB console independently confirmed the actual
initramfs recovery environment (`/init` as PID 1 and the `TATER RECOVERY`
framebuffer process), no running system slot, both slots good, and B still
selected. The one-boot rescue marker had been consumed. The cache was mounted
read-only without journal replay to read the router log; it recorded
`Tater Recovery armed; returning to the normal boot image`. No recovery or
boot partition was flashed.

From that recovery shell, retained good A was selected and booted. It
connected to Tater with normal daemon, renderer, camera, and running ALSA
capture; `/store` was read-only and both slots remained good. Its older GPU
allowlist correctly rejected the new shared helper and used the CPU display
fallback. This proves manual rescue-slot bootability, not full accelerated
runtime rollback. Releasing this update still requires version-compatible
helper/manifest selection or restoration alongside rootfs rollback, rather
than assuming global `/data` helper replacement is transactional with slots.
Corrected good B was then reselected and booted. Tater reconnected, the
normal camera and microphone services returned, and the exact GPU-word helper
and retained UI were active again with read-only root/store mounts. Both slots
remain good. This completes manual recovery/slot-return qualification, not
runtime-version rollback or automatic fault/exhausted-trial injection.
Extended final-candidate A/V remains a separate qualification.

## Native TWRP default (RC.6 policy revision)

The operator chose to keep Volume Up in TWRP on screen devices, rather than
the earlier automatic Tater Recovery handoff. RC.6's normal startup removes
only the exact retired Tater-generated OpenRecoveryScript command. It leaves
unrelated scripts unchanged, refuses mixed scripts and symlinks, and never
writes boot/recovery partitions. The old helper name and `install` spelling
remain compatibility aliases for cleanup, not redirect installation. The
unused TWRP handoff payload is no longer packaged.

The Linux held-key fallback for Checkers, Cronos, Crown, Athena and Rook also
requests native TWRP. Biscuit/Radar retain the existing two-step slot-rescue
commands until their target-specific early-boot hardware work. Explicit USB
`to_recovery.py` still requests Tater Recovery independently of that policy.
Shell migration tests cover real cleanup, idempotence, legacy aliases,
unrelated/mixed scripts, missing state, and symlinks. Target-command tests,
screen/rootfs/factory helper tests, race tests, and the complete ARMv7 build
passed. RC.6's first boot logged successful removal of the retired command;
a second cleanup returned success without re-arming it. It passed the actual
five-minute connected trial, automatically committed A good, and rebooted
with read-only mounts; good RC.5 remains in B.

The normal native recovery helper then booted the retained TWRP. USB ADB
reported device `checkers`, TWRP `3.7.0_9-0`, and its separate
`4.9.77-g1f91447-dirty` kernel at more than 69 seconds uptime. Repeated
inspection retained the same boot ID, and the cached OpenRecoveryScript was
absent. It did not automatically hand off to Tater Recovery. This stay test
used the native USB recovery request, not a fresh physical-button press; the
bootloader's physical Volume Up entry into TWRP had already been observed.
An explicit normal reboot then returned to corrected good A, reconnected to
Tater, and restored the GPU retained UI, camera service, and running microphone
capture with read-only root/store mounts. Cleanup remained idempotent after
that boot. Neither boot nor recovery partition was flashed for the policy
change. Broader hardware-release gates above remain open.

## Distribution gates still requiring work

The local build/provisioning foundation is now implemented; see
[the hardware build guide](checkers-hardware-build.md). Clean source builds
reproduce all three qualified helpers. Checkers rootfs/full app bundles carry
their own helpers; the camera selector and helpers follow their owning slots.
Linux sandbox tests cover actual first/repeated library installation and
refusal of mismatched existing libraries, and app tests preserve distinct
helper generations. The owner-local 114-file library-only set was prepared
and verified locally. None of this work accessed/flashed the device: installed
good RC.6 A and good RC.5 B remain the latest hardware qualification. A new
offline RC.7 is a build/test candidate, not an installed/released one.

The public helper URL is not configured by this work. Release packaging fails
closed until reviewed source/kernel/boot pins exist and independently checks
the actual boot kernel payload. No proprietary binaries or imaginary source
commits/URLs were added to satisfy that gate.

- The current release lock still selects the older FFmpeg artifact; the local
  hardware candidate uses the rebuilt H.264/framecrc-capable artifact.
- The lock's platform source commit must select the reviewed kernel changes,
  not just a new locally built image. Checkers' `minimum_system_version` must
  advance to the final release's rootfs version so hosts do not mistake an
  application-only update on the old system for full hardware enablement.
- Checkers factory packaging still selects the prior pinned boot image and
  System OTA deliberately updates rootfs only. Publishing the new camera-clock
  claim requires a coordinated kernel/platform migration or a separately
  reviewed kernel-update mechanism; an application OTA is insufficient.
- The explicit owner-local provisioning path is implemented but still needs
  installed-device/factory qualification with the migrated kernel. Automatic
  extraction from arbitrary old OS images is not implemented. Graceful
  CPU/JPEG fallback is not proof users received the acceleration.
- The new per-slot helpers need installed-system/app rollback qualification.
  Earlier manual A recovery proved only safe CPU fallback with the then-shared
  helper, not a fully accelerated rollback. Private ABI identities are unchanged
  and existing library destinations are never overwritten; a future library
  change requires separately qualified versioned runtime/linker paths.
- Tater and bridge H.264 negotiation changes are local and tested but not yet
  released or verified together in a two-device call.
