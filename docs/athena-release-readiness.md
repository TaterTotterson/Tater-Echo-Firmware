# Athena GPU/UI development checkpoint

Unpublished owner-device work, 2026-11-14; not full release qualification.

Athena now has the shared grey GPU workspace and approved lower-right home
layout at 1280×800. It uses the real Mali-G72 through mainline Panfrost and an
isolated native Mesa runtime, not the older Shows' Android libraries. Exact
rotation/colors, the production menu and retained video/control composition
passed generated-only on-device tests. Checkers/Cronos/Crown GPU executables
rebuild byte-identically after the separate Athena compilation path was added.

The owner application/settings and previous ONNX Runtime were backed up and
hash-verified privately. A daemon/renderer-only bridge passed the normal A/B
health gate. The GPU application `v4.2.3-athena-gpu.1` was installed through the
same updater, with its own GPU helper, metadata, camera and pinned software
decoder. Installed logs confirm GPU retained composition and parallel dual
wake. Generated silence validates OWW and both NWW variants; no live audio or
camera scenes were recorded. The normal five-minute application health trial
completed and committed normally. All seven installed component hashes match
the candidate archive; helper/metadata permissions and pairing/settings hashes
were verified. Hands-on behavior is not inferred from process health.

The full rootfs candidate includes the matching wake runtime, open-source Mesa
runtime and original GPU adapter. It was built offline but **not installed**.
The stock/pinned kernel, per-device speaker tuning, rescue/boot partitions,
pairing/settings and running Tater host are not changed by this application
upgrade. The source work is not committed, tagged or pushed.

The owner subsequently reported that the requested hands-on checks all work
well. This confirms the installed GPU/UI candidate's functional checkpoint;
it does not qualify a new hardware codec, system OTA or recovery change.

## Remaining gates

- Whole-fleet final system/application OTA, reboot and rollback testing.
- Source/runtime distribution, complete license notices/source offers and
  public reproducible build/provisioning artifact review before release.
- Hardware video: Athena's production-pinned kernel lacks MediaTek VCODEC support.
  It remains software/JPEG for media decoding/encoding and does not advertise
  H.264 hardware support. GPU scaling/composition is active independently.
  A separate Athena-specific kernel/DT/firmware qualification phase is needed
  for codec parity; do not transplant MT8163 helpers or guess VPU firmware.

## Hardware codec investigation, 2026-11-14

Read-only checks confirmed that both MT8183 codec DT nodes refer to the SCP,
which the board DT disables and gives no reserved-memory pool. The retained
vendor partition contains no SCP firmware file. The upstream MT8183 candidate
is a Chromebook image with a fixed DRAM code segment at `0x50000000`; sharing
the SoC does not establish board/audio-bus compatibility.

Complete boot/recovery partition backups were hash-verified privately. The
current boot image passed the pinned structural, header digest, firmware-only
ramdisk and filled speaker-tuning-slot checks. No kernel, DT, boot/recovery
partition, firmware or running host was changed during this investigation.
The new source-only prerequisite/ELF audit has regression coverage and never
opens a video device, loads firmware or declares a codec qualified. Kernel
component compile tests are only preparation, not on-device video evidence.
The subsequent full kernel and module build passed normal symbol linking.
A structurally validated private foundation boot image preserves the original
DTB, firmware ramdisk and speaker tuning, with SCP still disabled. Neither
that image nor the new modules were installed. This is not a hardware-codec
release candidate.

The subsequent operator-present trial verified physical Volume-Up/TWRP
recovery, full-partition readback, and the new kernel with original ramdisk
and exact speaker tuning. The owner retested immediate first words, audio,
menus and touch successfully. A narrowly audited DT adds an 8 MiB SCP pool:
the coherent allocator rounds the driver's 5 MiB request up to 8 MiB.
Default-off module refusal and explicit reset-held/manual-start probing passed.

A temporary upstream MT8183 SCP firmware start and both driver capability
queries passed. The encoder reports H.264 and multiplanar YUV input; the
decoder requires frame-based stateless H.264 requests and produces MM21.
The owner confirmed microphone/speaker operation after firmware start.
This is prerequisite evidence, **not generated-video qualification** or a
working production camera/call hardware path. No SCP runtime is automatically
installed, no hardware capability is advertised, and firmware stays private.

The installed system's normal kernel updater subsequently reapplied its
bundled October kernel, undoing the manual kernel trial after its health
delay. Its kept previous-image hash matches the test kernel. Weather/sensor
settings do not explain that kernel write. Continued manual testing needs a
boot-local matching template; the actual system build must ship its matching
kernel. Rootfs source also now preserves the existing kernelctl state path
in all trial checks, rather than renaming only the shell's path. Tests cover
this correction; the updated rootfs has not been installed.

Generated-pixel qualification subsequently **failed**, so the codec remains
disabled in production. Encoder setup/IPC succeeds, but SPS generation produces
no hardware IRQ or output. VENC clocks, runtime power, reset/SRAM status, bus
clamp and stock DT interrupt assignments were checked without speculative
register writes; none established a cause. A separate stateless H.264 decoder
test exited zero and returned a correctly sized frame, but every pixel was
zero and the driver logged an error. Output size/process success is therefore
explicitly rejected as qualification. No camera scene was recorded.

The temporary native decoder runtime was isolated under `/tmp`, denied camera,
audio and GPU device opens, and did not replace the installed decoder. SCP
mapping readbacks matched the documented firmware translations. The remaining
firmware/board-access issue is unresolved; no protection or bootloader changes
were attempted. Preserve the working GPU UI/software media path while this
separate codec investigation continues.

The controlled trial was cleaned up by restoring and fully verifying the
original boot partition through TWRP. The original kernel, unchanged GPU app
and native Panfrost compositor are running again. No experimental SCP/module/
decoder runtime or template bind remains active; user settings were retained.
This is a safe GPU/UI checkpoint, not completed hardware-video parity.

Remaining hardware gates are generated encode/decode color/order tests,
Request API/MM21 userland integration, sustained cancellation, and real
camera/call/privacy tests. Keep the GPU UI and software/JPEG media path until
those pass; neither compilation nor driver inventory qualifies hardware H.264.

Factory rootfs tooling accepts `--hardware-bundle` for the GPU-only Athena set
and `--mesa-runtime` for its exact verified native runtime. An app update uses
its helper beside its own renderer; missing or mismatched runtimes safely use
the CPU UI. Explicit CPU-disable settings remain respected.

## Continued AP-codec research checkpoint

Further operator-present work isolated an SCP-domain encoder access violation
and tested a separate original AP-controlled encoder adapter, using the owner's
local codec libraries without changing secure firmware or permissions. Standard
OMX lifecycle/owned-memory tests passed. Ten generated 640×480 frames then
produced real hardware interrupts and passed independent software decoding,
Y/U/V color checks, frame IDs and moving-marker ordering.

This is an encoder research milestone, not completed video parity. The decoder,
live capture/calls, sustained cancellation/privacy checks, reproducible runtime
distribution and matched system packaging remain outstanding. No production
hardware capability was enabled and no Tater host restart was performed.
The working GPU/voice application and user settings were preserved.

Continued trials currently use the manual test kernel and temporary matching
template binds. The prior original-kernel cleanup above is historical, not the
current research state. Restore original boot or install a properly matched
system candidate before final handoff/reboot qualification.

### Generated AP decoder milestone

The separate AP-controlled decoder now also passes real-interrupt, owned-buffer
single-frame and ten-frame tests. Its complete ten-frame 640×480 I420 output is
byte-identical to independent software decoding, including all color planes,
sequential frame IDs and moving markers. An intermediate repeated-frame result
was rejected by the order test and corrected in the experimental input adapter.
Normal cleanup freed every buffer and removed the temporary codec modules.

This does not yet enable hardware video in the installed application. Live
camera/call integration, sustained cancellation/privacy testing and matched
system/runtime packaging still remain. The working GPU UI, voice and settings
are unchanged, and Tater has not been restarted. See the hardware-research
Athena codec findings for reproducible generated-result hashes and boundaries.

### Cancellation, fixed-pool and stream-protocol checks

Both hardware engines now pass repeated forced-exit/restart checks and
300-frame generated buffer-reuse tests, with independent color/frame-order
validation. A temporary decoder prototype additionally passes complete
timestamped RGBA stream comparisons at 640×480, 768×432, 1280×720 and 1280×960.
Generated encoding and decoding also pass together, using separately owned
buffers and native Linux power/clock/IRQ management. The rebuilt original
kernel adapters and offline safety-contract tests are now in the research repo;
no proprietary library or firmware was added to either repository.

This is still **not an installed hardware-media update**. The prototype uses
bounded generated Baseline/no-B inputs; production helper lifecycle, reordered
media, runtime provisioning, live camera/call/privacy and matched system
packaging remain open. A caller packet-length correction eliminated a measured
prototype-only 150 ms CPU scan, but that is not a live-camera FPS claim.
The installed GPU/voice app and settings remain unchanged, Tater was not
restarted, and the manual test kernel still needs a properly matched system
or original-boot restoration before final handoff/reboot qualification.

### Continuous helper and actual receiver qualification

Original continuous AP helpers now have reproducible source/header/output locks
and separately verified owner-local runtime provisioning. The NV12 encoder
passes paced 360-picture generated tests, including real requested IDRs and
timestamps above 32 bits. The decoder passes complete independent RGBA/PTS/EOS
comparisons for that stream. The actual Go Sat Chat receiver also delivers all
360 generated pictures with an exact software-reference RGBA hash, without
opening camera, display, audio or network endpoints.

Athena's source receiver remains behind a separate explicit experimental opt-in,
the real AP device node and all pinned dependency/helper hashes. It cannot use
the Baseline/single-slice/live-only helper for general media playback. Existing
targets retain their current paths. Capability metadata is explicitly flushed
for pipe-based app launch; the integration probe caught that buffering bug.

This is not yet an installed camera/call update: the running GPU/voice app and
owner settings are unchanged, and no host restart/push/tag was performed.
Camera conversion/encoder integration, real call/privacy tests, matched system
packaging and normal reboot/OTA/rollback are still open release gates. The
manual test-kernel/template-bind warning above remains in effect.

### Live camera integration checkpoint

The temporary Athena camera adapter now samples the existing immutable V4L2
BGGR frames into a centered, aspect-preserving 640×480 NV12 stream. It retains
the platform's tone/white balance and native-resolution still path. Four
independent row bands reduce generated conversion from 47.4 to 12.5 ms on the
device; independent RGB-reference/BT.601 checks and Linux race tests pass.
This is CPU preprocessing, hardware sensor/DMA capture and hardware H.264
encoding/decoding—not a claim of GPU Bayer conversion or full-resolution video.

Bounded in-memory camera tests deliver about 30 FPS after startup, including
simultaneous hardware encoding/decoding through the actual Go receiver and
loopback relay. Capture timestamps remain monotonic, queues do not accumulate,
and deliberately omitted predictive packets recover at subsequent IDRs. Codec
EOS, camera teardown and module unload pass; scene pixels are not recorded.
The installed GPU/voice application and owner settings remain unchanged.

An experimental, separate AP bundle verifier now pins both original ARMv7
helpers, both ARM64 kernel modules and their exact empty-tuning kernel template.
It rejects private libraries, unexpected files, links and mixed architectures.
An explicitly selected rootfs candidate can include that set; ordinary GPU-only
builds remain available. A matched boot creates an ephemeral readiness marker
only after both exact modules load. Runtime manifests alone cannot enable AP
hardware, and all Go/C target/runtime checks still apply.

Athena advertises balanced-only H.264 **only after both local encoder and
decoder preflights pass**. The staged Tater host change requires this explicit
capability and balanced support on both peers, retaining MJPEG for older peers.
GPU-only app inventories remain accepted; paired AP sidecars must be atomic.

Still required: install/boot the matched candidate, visual live-call color and
motion checks, privacy/lens-cover and voice regression checks, and normal
system/app OTA/recovery/rollback qualification. Full-resolution encoding and
general-media Main/High/B-picture decoding remain unqualified. No release,
push or host restart is implied by this source/preparation checkpoint.

### Final matched test build prepared

The complete Linux build pipeline now produces the matched
`v4.2.3-athena-ap.2` system/application candidates. A separate
`v4.2.3-athena-bridge.2` compatibility app has a distinct version, avoiding
ambiguous completion checks when upgrading the older GPU-only installer.
The camera binary matches the bounded live-camera test binary byte-for-byte;
both codec helpers rebuild to their independently tested locked hashes.
Packaged components, kernel template and archive checksums pass verification.

These are uninstalled test candidates, not a published release. The owner
clarified that the visual video test was **not performed**. Real two-device
calls, privacy/cover and wake checks, matched boot and OTA/rollback still need
completion. Nothing was flashed, pushed or tagged by this build.

### Matched installation and normal-boot startup correction

The development device now boots the matched `v4.2.3-athena-ap.3` system and
`v4.2.3-athena-ap.2` application. The compatibility app was installed first,
then the full paired-codec app through the normal A/B application updater.
The original good system slot and a healthy compatibility app remain available.

The first normal boot correctly refused AP startup because the manually loaded
SCP driver from research trials was no longer present. The corrected source
bundle also pins the three already-tested GPL SCP dependencies. On the exact
kernel/board, the patched SCP driver holds the coprocessor in reset with automatic
boot/recovery disabled; its state must be offline before AP modules load. It
does not start SCP firmware. A running/unknown SCP or any identity mismatch
fails closed, and failed startup unwinds only modules loaded by that attempt.
Synthetic execution covers normal boot, existing offline/running states,
identity rejection and every module-load failure. The corrected path passes
on hardware and after a clean installed boot without temporary template binds.

The packaging correction changes only startup dependencies, metadata and
version labels; the app, camera, codec helpers and kernel remain byte-identical
to the qualified generated/in-memory test inputs. GPU rendering and both codec
preflights pass; owner settings and recovery remain intact. The Tater test host
has been restarted with balanced-only Athena negotiation enabled.

This is still a private test installation, not release qualification. The owner
confirmed voice/speaker/touch on the compatibility stage. Full-app hands-on
wake/privacy/cover and visual two-device calls are still pending, as are the
remaining end-to-end recovery/rollback checks. No push or tag was performed.

### Installed hands-on regression results

The full application now passes owner checks for immediate wake/first-word
capture, reply interruption, menus/fades, sliders/equalizer, volume and physical
mute. Camera-cover preview and reopening pass. A sustained Athena–Cronos call
has correct colors, ordered motion and reasonable delay on both screens, with
working two-way audio and active-call mute/unmute privacy.

During the active call, the host negotiated `echo-h264-balanced` and Athena's
actual hardware encoder/decoder processes were observed. Decoder timestamps
advanced in order; sampled queue depth stayed zero and backlog did not grow.
This is not a guarantee of 30 delivered FPS under every source/light condition.
After the owner ended the call, both codec processes exited, the camera was
released, and voice/UI stayed healthy. A final post-call wake/first-word request
also passed. Both normal five-minute system/application health gates committed.

The installed test is successful. Batch release OTA/factory migration and
end-to-end rollback/recovery qualification remain separate release gates.

### Matrix conversation-text preview

The owner-device app `v4.2.3-athena-matrix.1` was installed using the normal
verified A/B transaction, retaining `athena-ap.2` in the previous application
slot. Only the daemon/settings handler and screen renderer changed; all seven
camera, decoder, license and GPU/codec sidecars match the installed originals.
The kernel/system, pairing and native configuration are unchanged. A private
hash-verified copy of all previous application slots is retained locally.

GPU retained rendering is active, and Matrix is selected on this test device.
The owner confirmed cycling symbols, clear settled STT/reply text and normal
wake/first-word capture/replies. The normal five-minute health gate committed.
Smooth remains the source default. Host capability advertising and the matching
Tater/HA per-device controls were added afterward in source; this `.1` preview
does not yet advertise that new capability. No push, tag or host restart was
performed for this preview.

### Sendspin silence: source fix, installation deferred

USB inspection of the installed Matrix preview found Sendspin receiving and
buffering music while its DAC-observation count stayed zero. Athena's active
output is `/dev/snd/pcmC0D0p`, stereo S16LE at 96 kHz. The shared Sendspin pull
path instead read `/proc/asound/card0/pcm23p/sub0/status`, which does not exist
on this device, and assumed a 48 kHz hardware delay. The failed status read
returned before asking Sendspin to fill the music buffer.

The source now selects Athena's device-0 status path and 96 kHz hardware clock
for the pull, and uses device 0 for the startup availability check. Sendspin's
48 kHz mixer timeline, the existing 2x upsampler, hardware write geometry and
other targets' device-23/48 kHz paths are unchanged.

Host race tests and ARMv7 `server,onnxruntime` tests pass for speaker, Sendspin
and direct ALSA packages. Regression coverage checks target paths, hardware
delay conversion, invalid-clock rejection and Athena's 2048 mixer frames becoming
4096 hardware frames in the existing write chunks. Per the owner's request,
this patch has **not** been installed on Athena. Audible Sendspin playback and
synchronization remain to be verified after a later test update. No push, tag,
version bump or host restart was performed.
