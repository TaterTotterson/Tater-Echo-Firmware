# Crown hardware/UI development qualification

Date: 2026-11-14. Unpublished owner-device development, not release approval.

## Scope and rollback

Crown is the 2019 Echo Show 8, model C7H6N3, on the MT8163 platform. Its
1280×800 workspace rotates into an 800×1280 framebuffer. It has a separate
product/panel/kernel guard, helper lock and runtime identity. Neither the
Checkers nor Cronos GPU helper is accepted as a Crown helper.

Before changes, the installed application and configuration were backed up
privately and hash-verified. The original system and retained TWRP are not
flashed by this application bring-up. Private libraries were added only to
previously absent runtime directories. Their collected vendor inputs match
the reviewed newer runtime; proprietary libraries and credentials are never
included in source, helper bundles or rootfs/application artifacts.

## Completed generated-only checks

- Exact GPU rotation/color output: 1,024,000 matching pixels.
- Production Crown menu: exact native-size pixel comparison.
- Direct GPU video and retained floating controls: 60 offscreen pictures at
  approximately 30 FPS. This is not a measured installed display frame rate.
- Hardware H.264 encoder and decoder: bounded generated-only lifecycle tests.
- Production hardware receiver: all 120 generated frame IDs arrive in order.
- All three target helper sets rebuild to their expected hashes. Existing
  Checkers and Cronos helper binaries remain byte-identical.
- Linux race tests cover GPU, framebuffer, video, Show, native OTA and camera
  paths; source-level tests cover target isolation and Crown native geometry.

The approved grey workspace, themes, fade/slide effects, animated STT, video
overlays, weather visibility and GPU/Software information label are shared.
Live H.264 uses a balanced 768×432 profile at a 15 FPS application cadence;
native 1280×720 still geometry remains separate. Bayer development and some
copies remain CPU work; this is not a zero-copy/all-hardware capture claim.
Cronos' special held-slot capture policy is not enabled on Crown without
target-specific evidence.

## Installed functional checkpoint

The older application updater first receives a daemon/renderer-only bridge.
Its ordinary five-minute health trial is not bypassed. The complete candidate
then installs the camera, decoder, all three open helpers and metadata together
through the existing application A/B updater. Each application owns its own
helpers; explicit CPU-disable environment overrides remain respected.

The complete development application `v4.2.3-crown-hardware.3` committed through
the normal health gate. All nine installed component hashes match its archive;
pairing/configuration digests remain unchanged. Its rootfs candidate is built
but not installed. The system/store remain read-only, with the original kernel
and recovery partitions unchanged.

Operator checks passed:

- Native menu, fades, touch, sliders, vertical EQ and Graphics: GPU.
- Immediate post-wake first words, volume and physical microphone mute/unmute.
- Interrupting a spoken response with another wake and request.
- Camera preview and physical cover blocking/restoring the picture.
- Crown ↔ Cronos call motion and colors on the software/JPEG fallback.

That call is **not hardware-call qualification**: runtime inspection found
Tater's host allowlist still excluded Crown even though its satellite advertised
the hardware capability. The host change now includes Crown only when both
peers explicitly support the compatible protocol/profile. Older/unsupported
peers retain JPEG. Negotiation/relay regression tests pass, and the exact
change is staged in the owner's Tater test app. The owner cannot restart Tater
until morning; a fresh call must then demonstrate active encoder/decoder
processes and pass the operator's color, motion-order and delay checks.

## Offline kernel preparation, not installed

A bounded installed H.264 camera test produced a 768×432 stream at approximately
15.1 encoded FPS from 16.5 source FPS. The source cadence resembles the older
camera-clock problem on the other MT8163 boards, but Crown's clock has **not**
yet been read back. It is not evidence that this diagnosis is confirmed.

An offline candidate applies the existing clock-handle initialization patch
using Crown's exact captured configuration and the pinned GCC 8.3 toolchain.
Its release identity remains `4.9.337-g8d928c5176cc`; all 202 Wi-Fi and 96
Bluetooth imported symbol CRCs match. The original rescue ramdisk and all five
appended device trees are preserved byte-for-byte. The 13,477,888-byte private
candidate boot SHA-256 is
`ff21813871ea862ecbaf3aa9103f7ba055471685c98e6c4addc65fb55ace5871`.
It has not been flashed or hardware-qualified. Confirm target-specific clock
evidence, recovery/backups, readback and post-boot behavior before using it.

## Remaining gates

The installed older rootfs also contains a different ONNX runtime from the new
application's pinned runtime; NanoWakeWord confirmation reports unavailable.
The tested MWW/boundary wake path works. The staged full rootfs includes the
matching runtime, but its installed trial and optional NWW checks remain open;
do not label the application-only trial full system qualification.

Final whole-fleet OTA, reboot/rollback, extended A/V/thermal and
factory/provisioning qualification are deferred to the final testing phase at
the owner's request. Public source, artifact and kernel delivery pins also
remain release gates. The release workflow now expects a separate Crown helper
archive and checks the complete application sidecar inventory; its packer
refuses an unqualified Crown hardware release. No public URL, release approval,
commit, tag or publication is supplied by this development work.
