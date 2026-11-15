# Echo Show 5 first generation (`checkers`)

Checkers is a native Tater Linux appliance. A small Alpine userspace runs
the Tater satellite daemon alongside a
native framebuffer/touch UI and camera service. TWRP remains in recovery, and
the former system partition contains two independently bootable rootfs
slots.

## Platform contract

- Device: Amazon Echo Show 5 first generation (2019), codename `checkers`.
- Unlock: amonet-checkers 2.0.1 or newer with working TWRP.
- Kernel/rescue platform: pinned TECHO5 Checkers boot image `v0.7.16`.
- Userspace base: pinned TECHO5 rootfs `v0.9.26`, patched deterministically by
  [`linux/checkers/build_rootfs.py`](../linux/checkers/build_rootfs.py).
- Tater UI: `/usr/local/bin/tater-show`, drawing `/dev/fb0` directly and
  reading the Goodix evdev touchscreen.
- Camera: loopback-only snapshot service using TECHO5's hardware-reviewed
  MediaTek ISP driver.
- Persistent state: `/data/local/etc/tater` and `/data/tater-linux`.
- Recovery: TWRP is not overwritten.

The pinned platform artifacts are content-addressed before use:

```text
TECHO5 rootfs v0.9.26
sha256 a72bbdfd0d26f536552b44aa9258f136e05d5243220c761a22c0f691a943f14e

TECHO5 Checkers boot v0.7.16
sha256 46c19fd23c210714e29eb2bf88c77b540f3290c0cf50b680b22032e7a4771da7
```

## Retained Tater features

The Linux generation keeps the existing satellite protocol and feature set:

- native microphone capture with a physically measured 18 mm two-mic array,
  independently scored left/right wake beams, winning-beam handoff,
  winning-beam STT verification/trainer/pre-roll, speech-locked steering and
  off-axis attenuation, VAD, per-path AEC, barge-in, and selectable MWW, OWW,
  NWW, MWW + OWW, or MWW + NWW detection;
- TTS, Sendspin media and synchronized group audio, timers, alarms, announcements,
  intercom, mute, and volume;
- BLE presence advertisement forwarding;
- weather, room sensors, notifications, Room Vision, tool status, media and
  timer surfaces on the native UI;
- framebuffer brightness, touchscreen controls, hardware buttons, camera
  snapshots, and privacy shutter state;
- preserved Tater pairing after a factory reinstall, or captive-portal/USB
  pairing into a fresh identity on a new unit or with `--factory-reset`.

The Checkers ALSA capture device exposes four packed 24-bit channels at 16 kHz.
Channels 0 and 1 are the two microphones; channels 2 and 3 are the left and
right playback loopbacks. Tater uses the first loopback as a frame-aligned AEC
reference only after observing it both bit-exact silent while idle and carrying
audio during playback. Until that proof, the existing software speaker tap
remains active.
The Alpine path uses `arecord` for correctly clocked reads because tinyalsa
2.x's compatibility `pcm_read` wrapper returned repeated data faster than the
DMA clock on this vendor driver. Speaker playback continues through tinyalsa.

The MediaTek Bluetooth character device returns a zero-length read when its
queue is empty. Tater treats that as an idle poll, not EOF; repeatedly reopening
the device power-cycles the MT7668 radio and can also wedge its shared Wi-Fi
transport.

## GPU UI

With the reviewed owner-local Mali runtime installed, Checkers can retain its
home, menu, climate, and conversation layers on the GPU. Text is rasterized
when it changes; fades, the voice rim, ambient edge light, and thinking dots
are composited by GLES2 shaders. Menus fade over 220 ms at the existing 30 Hz
cadence. Scene changes snapshot the
last completed GPU image so an interrupted fade does not jump backward.
Settled static screens stop submitting GPU jobs.

With scene protocol v3, STT and streamed replies animate individual words on a
retained glyph atlas. New words rise 8 pixels and fade in over 180 ms; replaced
partial words rise gently and fade out over 120 ms. Earlier words stay opaque
and keep their position. Repeated words, rapid revisions, and line overflow
retain their animation progress rather than restarting the paragraph. The
full conversation fades away when its turn closes.

The helper remains offscreen; `tater-show` owns the framebuffer, stages each
completed GPU result in RAM, and flips a complete panel page. Video, media,
camera, notifications, night mode, and volume overlays retain their existing
rendering paths. Returning to those paths invalidates the CPU page caches.
Missing runtimes and failed or stalled helpers fall back to CPU rendering.

The original rotation-only helper remains supported. Retained UI effects
require a helper advertising `scene_version: 2` or newer; word motion requires
`scene_version: 3`. Its source and owner-local
runtime preparation tool are in the hardware-research project's
`probes/mt8163-gpu/` and `scripts/prepare_tater_gpu_runtime.py`. The vendor
libraries and linker are unchanged and are not included in the public image.
`tater-gpu-probe --benchmark-scenes-offscreen` checks fade endpoints,
interruption, shader output, and round-trip timings without opening the display.

## Native camera and H.264 Sat Chat

The development capture policy again enables hardware H.264 on Checkers and
Cronos after exact local encoder/decoder preflight. The MJPEG comparison and
a watched CPU-display comparison did not resolve the Checkers Sat Chat jitter;
the host relay remains under investigation. GPU UI/video composition, the
corrected encoder colors and bounded frame pacing are retained. This restores
the hardware integration, not a live-call performance or release qualification.
Peers without compatible hardware capabilities continue using MJPEG.

Checkers and Cronos develop live MJPEG directly at 480×270 with a maximum
20 FPS cadence, including the existing `echo-balanced` call profile. Actual
FPS depends on capture/exposure and load; this is not a 20 FPS guarantee.
Native still photos, incoming playback resolution and other models are unchanged.
Live MJPEG now uses motion-priority exposure on these two qualified targets:
shutter stays within a nominal sensor frame, with gain compensating where
possible. Dim rooms can look noisier. When the final live stream closes,
normal long-exposure policy resumes; still-photo resolution is unchanged.

The Checkers hardware capture path keeps the existing open camera driver and
privacy controls, develops native 1280×720 Bayer frames directly to YUV420,
and sends them to an isolated MediaTek hardware H.264 encoder. Bayer
development/color processing remains software; this is not a replacement
Android camera HAL or a claim of fully hardware ISP processing. Color/tone
analysis is limited to 10 Hz while capture can continue at the sensor rate.
Still photos use the original exposure policy and return native 1280×720 JPEGs.

The loopback camera helper exposes `/capabilities` and `/stream.h264`.
`TATER_VENC_ENABLE=1` is honored only on Checkers/Cronos and only after the installed
`/data/tater-venc/runtime.json`, encoder helper, component, and required shared
libraries pass exact identity checks. The encoder extension is prepared by the
hardware-research project's `scripts/prepare_tater_venc_runtime.py`; it depends
on the already-qualified `/data/tater-vdec` runtime. Device-owned libraries and
the Android linker remain outside public source and firmware artifacts.

Sat Chat advertises `sat_chat_h264=true` and version 1 only when both local
encoding and decoding preflight succeed. Updated Tater or the Home Assistant
bridge must negotiate compatible H.264 support at both peers. Tater selects
`echo-h264-balanced` when both explicitly support the balanced profile
(Checkers 768×432, Cronos 640×480). Other peers and older hosts keep JPEG.
Timestamped `TSCV2` packets carry source PTS,
stream generation, sequence, and keyframe state without touching call audio.
Missing packets discard predictive state and wait for a fresh SPS/PPS-prefixed
keyframe; queues are bounded rather than accumulating stale video. The renderer
keeps Checkers' existing aspect-preserving call window.

The kernel also needs the Checkers camera-clock patch in the Tater Linux
source build. The prior probe obtained camera clock handles only when the DT
requested always-on camera power; this privacy-gated device therefore silently
ran its sensor at 13 MHz instead of 24 MHz. The fix obtains handles without
powering the sensor and propagates clock errors. Do not add an always-on DT
property to work around it. A system/kernel update and camera helper update
are required; an application OTA alone cannot fix that clock bug.

Connected development tests measured approximately 30 fps at 1280×720 for
capture/encoding alone, and 28.4 fps over five minutes when the same unit
simultaneously encoded and hardware-decoded through the production loopback
relay. A dedicated bounded receive queue prevents TCP backlog; the qualified
NEON pixel converter produces the same bytes as the scalar reference. Live
call negotiation requires that helper, while ordinary media retains support
for older verified helpers. These are bounded in-memory tests without the
real display or host, not a guarantee of 30 fps full-duplex calls. The patched
kernel compiles with the exact release identity and unchanged vendor-module
symbol versions and is installed/boot-tested on the development unit, where
native capture reached about 29.7 fps without the lab clock module. The first
longer concurrent test exposed an unexpected stream exit; the corrected path
completed five minutes, packet-loss recovery, and a native still. Installed
candidate, extended thermal, two-device call, and distribution qualification
remain release gates. See [the release checklist](checkers-release-readiness.md).

`tater-camera-probe --acknowledge-camera-in-memory` performs a bounded
capture/decode check without saving scene pixels or opening the microphone or
display. `--capture-only`, `--relay`, `--drop-every N`, and `--snapshot` isolate
capture performance, production relay behavior, packet-loss recovery, and
native still dimensions respectively. Its default duration is 12 seconds and
the normal maximum is 30 seconds. Explicit `--soak` permits a bounded test up
to 10 minutes. These modes require an operator deliberately allowing camera
capture.

## USB factory conversion

Installing Tater Linux changes the partition layout and cannot be done by OTA.
The installer downloads the tested [Checkers LineageOS 18.1 v0.7 ZIP](https://github.com/amazon-oss/releases/releases/tag/lineage-18.1-checkers-v0.7),
`lineage-18.1-20260904-UNOFFICIAL-checkers.zip` (SHA-256
`785fa643fd68b2e6f6f02d96a2da58373c6a577b92a27cf6cec69603bb94068e`),
if it is not already cached. An offline user can pass a local verified copy
with `--lineage-zip PATH`:

```bash
./install.sh
```

The installer:

1. verifies every bundled file and the connected `checkers` device, then
   downloads or verifies the pinned LineageOS ZIP before destructive writes;
2. saves the device's small partitions from TWRP and, when present, a checked
   settings-only archive containing Tater identity, Wi-Fi, SSH, and controls;
3. installs the supported LineageOS zip without booting Android, solely to
   obtain the matching vendor driver tree;
4. flashes the pinned Linux boot image, creates the two-slot store, and installs
   the Tater rootfs into slot A;
5. restores existing Tater setup, or on a new/reset unit shows an open
   `Tater-Setup-XXXX` network and serves its Wi-Fi and pairing form at
   `http://192.168.4.1`;
6. reboots and leaves slot A on trial until the runtime health window commits
   it, followed by one automatic restart into the now-good slot with read-only
   mounts.

This reformats userdata and the former system partition, but restores only the
verified settings—not old OTA application slots. Add `--factory-reset` to
discard those settings. The installer keeps TWRP and writes recovery material
under `backups/<serial>/`. Keep those backups with their matching unit. Full operator instructions are in
[`factory/checkers-linux/README.md`](../factory/checkers-linux/README.md).

## OTA and rollback

After conversion, Tater sends the `tater-echo-checkers-vX.Y.Z-ota.tar.gz`
artifact with its expected size and SHA-256 digest. The daemon downloads the
small native application bundle to writable `/data`, stages `tater-echo` and
`tater-show` together in the inactive application slot, and switches one link
atomically. The generation commits after both processes remain up for five
minutes and the daemon is connected to Tater. Three fast exits or a health
timeout switches the shared link back and reboots.

Routine OTA therefore behaves like Biscuit and never remounts the live Linux
rootfs. The application bundle updates the daemon and renderer together; the
daemon embeds the built-in wake bundle and verifies any required wake-runtime
sidecars. A separate target-verified System OTA can update camera helpers, init
scripts, and the rootfs through the inactive root slot. Kernel, boot, rescue,
partition layout, and vendor storage still change only through an explicit
USB/recovery platform migration.

During such a platform migration, the root slot is marked good only after the
same five-minute connected health window. Checkers then performs one direct
restart. This is intentional: `/` and `/store` are two views of the same ext4
filesystem, and the vendor kernel can leave a live remount-ro syscall stuck.
The slot state is synced first, and the initramfs restores both mounts as
read-only on the next boot.

## Build and verification

Fetch the pinned rootfs and TECHO5 checkout, then run:

```bash
linux/checkers/build_linux.sh \
  --version v3.0.0-dev \
  --base-rootfs /path/to/rootfs-v0.9.26.tar.gz \
  --platform /path/to/tater-echo-linux-show
```

The build uses an ARMv7 Alpine container for the CGO daemon, native UI,
microWakeWord runtime, and tinyalsa library; it builds the camera helper from
the exact pinned TECHO5 commit and then creates a deterministic rootfs archive.
The release workflow repeats those steps from clean inputs, verifies ELF
architecture and embedded target/version strings, verifies the factory bundle,
and verifies the daemon/renderer OTA bundle before publication.

Hardware acceptance requires all of the following on a trial slot before it is
committed:

- Wi-Fi and Tater reconnect after a cold power cycle;
- microphone clock tracks wall time without subscriber drops;
- speaker, AEC reference, and local wake word initialize;
- native UI connects over loopback and touch input is present;
- camera `/snapshot` returns a valid JPEG;
- BLE passive scan remains up and reports advertisements without disrupting
  Wi-Fi;
- an OTA installs the opposite application slot and a kernel-level reboot reaches it;
- TWRP remains bootable and the preceding good slot remains selectable.

## Attribution

Low-level Checkers Linux hardware enablement, rescue boot contract, slot
manager, vendor-driver handling, framebuffer research, and camera driver are
derived from [TECHO5](https://github.com/HuskerMinion/techo5) by HuskerMinion
under the MIT License. The generated image and factory bundle carry that
license and a Tater attribution notice.
