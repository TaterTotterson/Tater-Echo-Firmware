# Rook hardware/UI development qualification

Current USB-candidate status (2026-11-15): the automatic library installer and
exact 4.2.3 boot allowlist are prepared. Full USB/OTA qualification is pending;
see [the fleet test plan](echo-5-build-inputs.md). Earlier sections below are
development evidence, not approval of the assembled 5.0.0 factory image.

Date: 2026-11-14. Unpublished owner-device development, not release approval.

## Scope and rollback

Rook uses a native 480×480 round display, an MT8163/Mali graphics platform and
a GC0312 640×480 camera. Its GPU helper does not rotate the square framebuffer.
The product/panel/kernel guards, helper lock and private runtime identities are
Rook-specific; another target's helper or manifest is rejected.

The original boot/recovery images and active application/configuration were
backed up privately and hash-verified. Application trials use normal A/B slots
and verified owner-local runtime directories. A subsequent, physically
recoverable camera-clock trial replaces only boot, preserving its rescue
ramdisk and both device trees. Recovery, bootloader, vendor, rootfs/store and
partition layout are unchanged. Proprietary runtime files are excluded from
repositories, public helper bundles and rootfs/application artifacts.

## Completed generated-only checks

- GPU orientation, color and circular masking: all 230,400 native pixels match.
- Production round menu: exact native-size GPU output comparison.
- Direct GPU video with retained controls: 120 generated frames, about 82 FPS
  offscreen. This is not a measurement of the installed display's frame rate.
- Real raw8/LUT input through the hardware encoder: 12 generated VGA frames,
  primary colors and frame timestamps in order.
- Hardware encoder/decoder lifecycle and generated-color checks pass.
- Production hardware receiver: 120 numbered frames in order, plus a separate
  360-frame continuous decode matching the known generated reference digest.
- Checkers, Cronos and Crown helpers rebuild byte-for-byte unchanged after the
  shared source additions. Source tests cover Rook target isolation and bounds.
- Round UI, hardware bundle, decoder, native OTA and camera tests pass; race
  checks cover the shared renderer, native OTA and hardware clients.

Camera sensor acquisition retains the existing driver's power and privacy
policy. Live-video exposure is separately leased and bounded to one sensor
frame; still photos keep the original longer-exposure limits. Bayer development,
tone conversion and copies still use the CPU; H.264 encoding/decoding and
supported display composition use hardware.
This is not an all-hardware or zero-copy camera pipeline claim. Rook sends
640×480 H.264 at the balanced application's cadence, using the existing Show
wire format. Older peers retain their negotiated JPEG fallback.

The first installed hardware call confirmed active Rook GPU, VENC and VDEC
processes and negotiated `echo-h264-balanced` with Cronos. Relay sequence
numbers had no gaps, duplicates or reversals, but the operator reported
Cronos-like stale motion from Rook's camera. A Rook-specific capture trial now
uses the held-slot state machine: two sensor ticks per target, then publish the
preceding held slot, copying bytes before exposure/callback work. Generated
delayed-latch, wraparound, missed-tick and slow-consumer tests pass. The operator
confirmed the backward jumps were gone, but source FPS was only about 2.8 while
incoming hardware playback ran at about 15. Active clock readback found CAMTG at
26 MHz, giving the sensor 13 rather than 24 MHz. The exact Rook-source/GCC8
clock correction is now installed with an unchanged configuration and all 266
Wi-Fi module import CRCs matching. Its boot extent read back correctly and
normal Wi-Fi/GPU startup passed. With live exposure capped to one frame, capture
and hardware encoding now measure about 15 FPS at the same 640×480 size, and
active CAMTG reads 48 MHz. The owner confirms smoother, correctly colored motion
without backward jumps. A contiguous two-minute metadata observation forwarded
1,799 Rook pictures (14.95 FPS) and 1,773 Cronos pictures (14.74 FPS), with no
sequence losses/duplicates/reversals, dropped GOPs or dropped audio frames.
Two-way audio, physical mute/unmute, post-call immediate first words and reply
interruption passed the owner's checks. Camera/codec owners close cleanly;
the exposure policy returns to still mode and GPU/wake readiness remain active.
Evidence is recorded separately in the research repository's
`docs/findings/rook-gpu-codec-bringup.md`.

## Application migration

The original updater safely rejected unfamiliar hardware sidecars without
switching slots. A daemon/renderer-only compatibility bundle precedes the full
application bundle; same-version trial continuation retains normal rollback.
Component capability version 3 identifies atomic GPU/codec sidecar support.
Version 2 means FFmpeg support, not hardware-sidecar support. The release
manifest now requests the version-3 bridge when necessary.

The round renderer must be linked with `main.target=rook`, independently of the
daemon target. A build regression test now checks this explicitly; framebuffer
dimensions alone do not authorize a different hardware target.

Installed diagnostics also isolated a blocking Rook IIO illuminance read: one
read took 1.79 seconds and starved the synchronous UI loop. Ambient samples now
come from a single background owner with a one-result bounded queue. The UI
never waits for a conversion, and sensor shutdown does not race a pending read.
Blocking-reader, fresh-result/error and race tests cover this behavior.

The development app's matching open-source ONNX runtime is staged in the normal
wake package directory, with the previous file backed up privately. This avoids
depending on an unpublished development release to supply the runtime. Saved
wake-mode settings are not changed.

## Installed checkpoint and completed functional acceptance

The corrected development candidate is `v4.2.3-rook-hardware.3`. Installed
diagnostics now report Rook and active GPU composition, with MWW/NWW confirmation
and the boundary model ready. After the non-blocking sensor fix, the installed
loop measures approximately 30 FPS with 0.5 ms average UI work, rather than
roughly 1 FPS and one-second stalls. All nine final motion-build component
hashes, unchanged pairing configuration and matching ONNX runtime were verified.
Its normal application health trial committed slot B without bypassing the
settle gate. The operator passed responsive menu/Sat Chat navigation, correctly
colored ordered hardware video, two-way audio/privacy and post-call voice.
The owner subsequently confirmed all remaining non-OTA functional checks,
including EQ/volume, standalone camera previews, Sendspin sound and media/popup
layouts. Rook is accepted for moving on to the next device. System OTA testing
is explicitly deferred to the combined fleet test; this functional acceptance
does not enable the public release gate.

A subsequent source-only follow-up adopts the owner's approved Quiet clock
home layout: the smaller centered clock has its date below, followed by a
compact outline icon/temperature/condition row, a separate feels-like reading
and up to two round-safe sensor rows. The assistant/room heading, status dot,
optional room temperature, themes, wallpaper rotation, clock format and
day/night selection remain supported. Weather uses the Shows' shared outline
renderer rather than the legacy shaded PNGs. Disabled weather does not leave
condition text, and independently enabled sensors remain available. Menus,
voice, camera and hardware playback are unchanged.

Native-size production previews, bounded-layout/long-label/sensor tests,
retained GPU upload checks, the renderer race suite and a Rook-linked ARM build
cover the follow-up. It has not been installed on the accepted device build;
on-device visual acceptance is deferred to the next update/fleet OTA test.

Another source-only shared-rootfs fix prevents automatic Ethernet probes from
resetting an established computer USB connection. Rook's read-only inspection
confirmed both Android gadget `CONFIGURED` and UDC `configured` state. The
controller now protects connected/enumerating/suspended hosts, allows new
enumeration to settle and checks again before a speculative role change. Idle
powered-adapter discovery and connected Ethernet recovery remain enabled;
Rook has no qualified independent cable-presence signal for detecting every
powered-adapter plug-in without probing. Synthetic controller tests cover
computer disconnect/reconnect, enumeration races, explicit diagnostic override
and existing Ethernet/Wi-Fi behavior. Installation and a real USB/Ethernet
hot-plug acceptance check remain part of the fleet system OTA test.

Final whole-fleet system OTA, reboot/rollback, factory/provisioning and extended
A/V/thermal qualification remain deferred to the owner's final testing phase.
The built rootfs is not claimed installed by an application-slot update.
Public helper/source delivery and release approval remain separate gates; the
release packer refuses distribution while Rook is not explicitly qualified.
