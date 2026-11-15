# Cronos hardware/UI development qualification

Date: 2026-11-13. Unpublished development work; not a release approval.

## Completed-camera-buffer correction

Latest candidate: a generated marker inserted after camera acquisition stayed
in order while the operator's arm jumped backward. RAM-only slot-history
measurement then counted 406 older-slot selections in 1,800 callbacks under
the previous-slot-only policy. The new held-target implementation waits two
sensor ticks per handoff, publishes the preceding held target and copies it
before slower callbacks. It passed 1,800 callbacks with zero older selections
and zero selected-buffer changes, at approximately 15 FPS hardware encoding.
The operator confirmed the backward jumps stayed gone after the extended call.
Linux driver/adapter race tests, deterministic delayed-latch/slow-consumer/
counter-wrap/board-gate tests, normal ARM/ORT builds and 25 rootfs/hardware
tests pass. The clean application `v4.2.2-cronos-capture.2` is installed in
app A, with capture.1 retained in B. All nine component hashes match its
manifest. GPU/VDEC/VENC/FFmpeg helpers, private runtimes, pairing/token and
screen preferences are unchanged. Root and store remain read-only.

The operator confirmed normal motion and colors in the fresh installed-build
H.264 call (about 105 seconds observed), then passed two immediate post-wake
requests with first words intact, menus/touch and microphone mute/unmute.
The normal five-minute A/B health window committed without override; daemon,
renderer, GPU and owning camera helper stayed running. After the call, codecs
and camera hardware released normally. No temporary helper remains, and the
diagnostic marker/fingerprint code is not shipped. No push/tag/release was made.

This closes the reproduced backward-motion gate, not every network-quality
gate: the installed-call metadata still contains isolated transport pauses
and an opposite-direction GOP drop. It is not a zero-stall or 30 FPS claim.

### Earlier, insufficient previous-slot attempt

Generated frame-ID tests passed through the encoder, decoder, production
receive queues, GPU overlays and physical display. Fingerprint-only live
capture checks then found Cronos' selected DMA slot still changing in 35/118
short observations; the previous slot remained stable. Selecting that previous
slot with one extra startup tick passed 0/119 changing-slot checks. A reversible
camera-only trial removed the operator-observed backward jumps in an actual
hardware H.264 call, without changing GPU/decoder/overlay code.

The superseded capture.1 build applied that policy only to Cronos. Its
installed trial initially looked right, but the operator subsequently
reported the same arm-position reversals on Checkers. This correction is not
a passed live-call qualification or proof of the remaining symptom's cause.
That result led to the chronology/lifetime measurements and replacement above.
Checkers' capture policy remains unchanged.

## Hardware restoration after controlled comparisons

The operator requests restoring the hardware integration after MJPEG capture
and Checkers' watched CPU-display comparison failed to resolve its Sat Chat
jitter. The normal GPU renderer is restored. Development source re-enables
target/runtime-gated H.264 capture, keeping balanced geometry, corrected colors,
bounded pacing, hardware decoding and GPU scenes. MJPEG fallback remains;
the freezing experimental ISP path is not enabled.

`v4.2.2-cronos-hardware.1` and Checkers `v4.2.3-checkers-hardware.1` are now
installed through normal verified application OTA, retaining `mjpeg.3` for
rollback. Both report GPU scenes, hardware H.264 and balanced support. Camera,
FFmpeg, GPU and codec helper files are byte-identical to the preceding apps.
ARMv7/ORT builds, five Linux race-tested video/GPU packages and 25 rootfs/hardware
tests pass. Normal app health checks are still running. Tater was not changed
during restoration; its call transport remains under investigation and live-call
smoothness is not qualified.

## Earlier installed MJPEG motion trial

Both `v4.2.2-cronos-mjpeg.3` and Checkers `v4.2.3-checkers-mjpeg.3` are installed
through normal application OTA, retaining the previous `mjpeg.2` applications.
Both report GPU scenes active; the separate Checkers legacy-manifest
compatibility fix did not replace any proprietary library or hardware helper.
Live MJPEG holds the qualified motion-priority exposure lease without enabling
H.264 encoding. Native still geometry, incoming decoding and aspect-preserving
GPU presentation are unchanged. Normal exposure resumes when live capture ends;
dim-light gain/noise can increase during a live session.

Linux camera/GPU race regressions and normal ARMv7/ORT builds pass. Short
camera-only host-delivery timing measured about 11 FPS on Cronos and 12.5 FPS
on Checkers, versus about 6 FPS previously from Checkers. This is not a 20 FPS
or full-duplex qualification. The operator's actual-call review and normal
A/B health interval remain to be recorded before closing those gates.

## Earlier color/jitter investigation

The operator requested a capture-only return to MJPEG. The isolated live GPU
Bayer-conversion trial verified GPU use but left Checkers' visible stutter
unchanged, and was fully stopped/restored. Source now disables local H.264
capture/advertisement and handles stale H.264 requests with balanced MJPEG.
Incoming hardware decoding, GPU composition and still photos are unchanged.
The local `v4.2.2-cronos-mjpeg.1` / Checkers MJPEG trials are installed and
connected. Cronos' active slotted camera reports H.264 disabled; its renderer
has GPU and hardware decoding enabled, and GPU scenes are active. All nine
installed application hashes match the trial bundle. Camera, FFmpeg and all
hardware-helper binaries are unchanged. The operator reports better motion but
no picture on Cronos; Checkers is hardware-muted, blocking its camera. The
peer also reports software UI fallback after reboot, which needs separate
inspection. Visual qualification remains open.

The subsequent `v4.2.2-cronos-mjpeg.2` / Checkers `mjpeg.2` apps are installed
with direct 480×270 MJPEG development and a 20 FPS ceiling. Linux unit/race
tests, pixel comparisons for both sensors, and normal ARMv7/ORT builds pass.
In short camera-only host-delivery comparisons, Cronos increased from about
7.4 to 10.6 FPS; the operator confirms smoother outgoing video. During the
actual mixed call it sends around 10–12 FPS. Cronos receives and displays the
same approximately 6 FPS from Checkers, so its renderer is not discarding a
significant fraction of incoming pictures. Checkers' source timing/exposure
and separate GPU fallback still require direct inspection. No camera pixels
were saved by the timing tests. Full stills and all GPU/VDEC/FFmpeg helpers
remain unchanged; this is not a 20 FPS or release-readiness claim.

After Checkers ui.13 restored its hardware capability, the real mixed call
negotiated `echo-h264-balanced`. The operator reported smooth motion with
intermittent jitter, and incorrect colors on both screens. Generated-color
testing isolated a Checkers encoder U/V input-order bug and verified its
correction independently. Cronos' encoder and decoder must be tested separately
before qualifying the call. Those separate generated-pattern tests now confirm
Cronos' decoder reproduces blue/red exactly and its encoder has the same U/V
input reversal. The corrected encoder lowers U/V mean absolute error from about
42 to 0.54/0.68 levels. Its clean locked helper build matches SHA-256
`29bf2acbd3d2d2e0c69af6111d9a15d5c32404a0a9a6f1be5d4581d819d7cecc`.
No camera pixels were saved. hw11 is installed via normal native application
OTA, with all nine files matching the bundle. Pairing, token, display preferences
and private-runtime digests are unchanged; root/store are read-only and microphone
capture and GPU composition are running. hw10 is retained in the previous app
slot. The operator confirms correct colors on both screens and initially
improved jitter, but still reports unacceptable jitter/lag. Performance is NOT
qualified. The active mixed call negotiates `echo-h264-balanced`; hardware
encoder/decoder and GPU processes remain stable. Cronos encodes about 15 FPS,
but presents roughly 11–13 FPS with intermittent 0.3–0.7-second picture gaps.
Sampled CPU use is about 90–96%, with no growing decoder input queue and about
197 ms of sampled source-PTS decoder backlog. These observations do not locate
every stall or measure end-to-end latency. CPU Bayer development, RGBA
conversion/scaling/copies and pbuffer GPU readback remain in this partially
accelerated video pipeline; GPU enablement alone does not prove smooth calls.

Tater's full native status/settings reads also reproducibly blocked unrelated
requests for hundreds of milliseconds. The tested host correction moves settings
I/O off-loop and omits it for Sat Chat directories. After the operator's restart,
concurrent tiny-request latency is now typically 2.5 ms, maximum
21.9 ms in a 20-pair comparison while full status takes 288 ms typically.
The same concurrent requests previously stalled for hundreds of milliseconds.
Real-call jitter feedback is still required.

## Installed and passed

- Exact running kernel release `4.9.337-g8d928c5176cc`, build #2, uses the
  original live configuration and pinned GCC 8.3. All 203 Wi-Fi and 96 Bluetooth
  imported symbol CRCs match. Original ramdisk/device trees are preserved.
- TWRP root/product/partition hashes were checked before writing only the
  physical boot partition. Candidate extent readback matched. Both recovery
  partition hashes remain unchanged, and normal Wi-Fi/microphone service works.
- Good original system A remains; the new GPU UI system B passed its actual
  five-minute health trial and automatic restart, returning root/store read-only.
- App `v4.2.2-cronos-hw4` fixes missing encoder enablement in the owning app-slot
  camera. It was staged with the production SHA-verified A/B OTA transaction,
  retaining hw3 in B and a verified private archive of the replaced generations.
  Its independent five-minute app health trial also committed normally; the
  pending marker is absent, and installed daemon/renderer/camera hashes match
  the verified bundle. Root/store remain read-only.
- Pairing/configuration and device-token digests match their pre-install values.
  No microphone audio or scene pixels were retained by qualification probes.
- The operator passed the new menus/fades/touch/vertical EQ, two immediate
  post-wake requests with first words intact, volume and mute/unmute.
- The operator also passed interruption during a spoken reply with first-word
  capture intact, and physical camera-cover blocking/reopening in Tater preview.
- The installed app camera advertises 1280×960 H.264 version 1 only after
  target/helper/runtime preflight. A 20-second production-relay loopback passed:
  304 captured / 303 hardware-decoded pictures, 15.09 decoded FPS, first picture
  992 ms, maximum subsequent gap 122 ms, successful exit and codec cleanup.
- A five-minute installed production-relay soak passed: 4,639 captured and
  4,637 hardware-decoded pictures, 15.45 decoded FPS, first picture 866 ms,
  maximum gap 136 ms and pictures continuing to the end. Available memory
  sampled around 602–608 MB under load and recovered to 634 MB afterward;
  sampled CPU thermal readings reached 75°C. No thermal-policy override or
  forced CPU clock was used. This bounded test is not long-duration thermal
  certification or a two-device/full-duplex performance claim.
- Three subsequent cold camera/encoder/decoder restart cycles passed with
  pictures through each deadline and released hardware ownership between runs.
  The camera's intended five-second inter-consumer linger is allowed before
  checking sensor closure; the first one-second-idle test was overly strict.
- A 20-second loss test deliberately omitted 16 predictive packets. Decoding
  recovered and continued through the end (100 decoded pictures), but maximum
  picture gap was 4.459 seconds. This passes bounded recovery, **not** smooth
  playback under repeated loss. Native 1600×1200 still capture then passed.
  Codec/camera ownership was released, GPU/voice service stayed running,
  pairing digests remained unchanged and root/store stayed read-only.
- Full-field camera development is software Bayer/color processing; H.264
  encode/decode and retained UI composition use hardware. Sensor clock is now
  24 MHz (~30 source FPS), but live software development is the current
  throughput limit. Do not market these results as 30 FPS full-duplex calls.

## Open gates

- Post-stream operator voice health and longer A/V/thermal operation remain
  separate gates. Repeated-loss freeze duration is a known quality limitation.
- Real two-device performance: a Checkers/Cronos call negotiated H.264 and
  opened the hardware encoder, decoder and GPU compositor, but the operator
  reported substantial lag. This is a functional connection, **not** a passed
  performance gate. One-deep relay queues incorrectly dropped predictive
  frames in both host and satellite relays; regression tests reproduced the
  fault, including the satellite's outbound sender. Bounded dependency-aware
  queues now keep short GOP bursts and wait
  for a fresh keyframe after overflow. Cached startup keyframes no longer
  replay older queued packets. CPU RGBA scaling was also optimized with
  pixel-exact padded/subimage tests; scaling is still CPU work.
- Unpublished app `v4.2.2-cronos-hw6` passed its A/B trial with all nine component
  digests verified. Pairing/token/preferences match the
  durable pre-install digest baseline. Its actual five-minute health trial
  committed normally; live-call performance is still not qualified. The Checkers
  peer ran `v4.2.3-checkers-ui.8` with the same sender/relay/scaler fixes. The running
  Tater test backend was updated and restarted by the operator. No driver,
  private runtime, kernel or recovery change was needed for these app fixes.
- The operator's subsequent hw6/ui.8 retest found the picture a little
  smoother but still substantially delayed. Cronos logged about 8.5–9.6
  displayed FPS and 12.5–13.6 encoded FPS, with a 3.487-second initial host
  response and 5.586 seconds to first picture. End-to-end backlog attribution
  and further optimization remain open; the queue fixes did not resolve lag.
- A subsequent live call reproduced near-saturated CPU use and repeatedly
  changing decoder PIDs, despite the encoder and call remaining active.
  Unpublished `v4.2.2-cronos-hw7` now separates display fitting from draining
  decoded output, using bounded, owned, latest-picture buffers. Only complete
  decoded pictures are replaced; predictive packets remain ordered. It also
  logs decoder backlog from source PTS and fixes the misleading uncounted
  H.264 preparation statistic. All nine installed hashes and the persistent
  configuration digests match; the app health trial committed normally.
  The operator found the hw7/ui.9 picture much smoother, but still delayed.
  Startup host response fell to 547 ms and first picture to 1.465 seconds.
  Prepared pictures reached 15.6–16.8 FPS and displayed pictures 12.6–13.2 FPS,
  but decoder backlog grew from 394 ms to 1.119 seconds and a prediction reset
  still occurred. These are improvements, not a low-latency performance pass.
- Installed unpublished hw8/ui.10 limits live-call sensor work to 15 FPS before
  Bayer preparation/encoding, retaining full source geometry and original PTS.
  It drops only independent raw sensor frames, not encoded predictions.
  Ordinary camera previews and unpaced 30-FPS qualification probes are unchanged.
  Cadence, query validation, and related race tests pass. In the real mixed call,
  the operator reported much smaller delay. Cronos' decoder stayed alive for
  several minutes with an empty input queue and mostly 196–263 ms of source-PTS
  backlog, rather than increasing backlog and prediction resets. This is not
  an end-to-end latency measurement. Prepared pictures reached 13.7–14.5 FPS,
  displayed pictures about 12–13 FPS, and Cronos encoded about 12.5 FPS at full
  geometry. Its hw8 health trial committed normally. Do not claim 30-FPS duplex
  capture. The operator still sees intermittent repeated-frame-like jitter on
  both screens, more clearly on Checkers; smoothness qualification remains open.
- A subsequent app candidate requests a coalesced draw immediately when a live
  picture is ready instead of waiting for the next 30-Hz menu-animation tick.
  It adds bounded ready/present/source-gap timing diagnostics without retaining
  video or adding a jitter buffer. Unit and related race tests pass. In the
  hw9/ui.11 comparison, prepared/displayed rates matched more closely, but the
  operator reported unchanged jitter. Ready-picture gaps of roughly 0.5–0.94 s
  were already present before presentation, so this did not resolve the cause.
- The operator approved a screen-sized H.264 comparison: Checkers sends
  768×432 (16:9), Cronos 640×480 (4:3), preserving the full field of view and
  leaving still-photo geometry unchanged. Original converters sample the
  required source positions before YUV conversion; native full-resolution
  conversion remains available. Exact helper/source locks were rebuilt with
  unchanged GPU/decoder helpers and private libraries. Both hosts require an
  explicit balanced capability from both peers before selecting the new profile.
  Cronos' installed hw10 passed a 12-second in-memory hardware capture/relay/
  decode probe: 175 captured, 174 decoded, about 14.5 FPS, 134 ms maximum picture
  gap, and 640×480 output. No scene pixels were saved.
- The operator reported the next real call much smoother, but telemetry showed
  JPEG fallback, not balanced H.264. USB inspection of Checkers found a schema-1
  runtime manifest tied to its previous helper hash. Its correctly updated
  application helper was rejected by that metadata check. A regression reproduced
  the rejection; the fix admits only known historical target-owned metadata
  while still independently verifying the current executable, component and
  library identities. On-device capability comparison changed false to true
  without modifying the runtime. The fixed-app real H.264 retest remains open;
  the smoother JPEG call is not a balanced hardware-video qualification.
- Main Tater's directory refresh repeated a full native/settings snapshot for
  each recipient. A regression test reproduced four snapshots for three
  recipients; directory fanout now reuses one snapshot. All 19 Sat Chat tests
  pass against both repo and bundled backend, and the operator restarted Tater
  to load it. The subsequent call measured 505 ms to host response and 1.458 s
  to first picture (previously 2.883 s and 4.497 s).
- Sender regression tests also cover overflow at a new keyframe, epoch
  changes, invalid packets, byte ownership, call end/reconnect cleanup,
  concurrent producers and audio/control priority. Related Go race tests pass.
- Runtime/app/system rollback and longer A/V synchronization qualification.
- Publish/audit original helper sources and their licensing, configure the
  target-specific public helper archive, and publish the reviewed platform
  source/boot/kernel identities. Current older platform pins intentionally fail
  the hardware release packer rather than producing misleading artifacts.

Nothing was pushed, tagged or released. Proprietary runtime libraries and
serial-numbered backups remain owner-local and excluded from public output.
