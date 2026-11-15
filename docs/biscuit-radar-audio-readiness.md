# Biscuit and Radar audio update readiness

The shared production source is integrated for both targets: +40 dB microphone
PGA gain with unity software/digital gain, preserved VAD/DOA calibration, and a
verified 80 Hz hardware-filter profile. New board-specific kernels enable the
filter; applications retain software filtering on older kernels. Existing
speaker routing, AEC, beamforming, wake models and boundary timing are retained.

`linux/dot/build_kernel.py` is the normal release build entry point. It combines
the pinned Tater Echo Linux builder, original GCC 4.8 and existing security/USB
patches with the independent overlay in `linux/dot/audio`. The source lock and
per-board kernel digests are in `platforms/dot-audio-lock.json`. Kernel config,
embedded profile and exact reviewed digest must match during packaging. This
does not publish private boot images or vendor tuning.

## Local build verification

Both target-specific ARM applications, kernels and full rootfs candidates built
as `v4.2.3-audio-dev`. Kernel configs identify their correct boards, expose the
expected retained filter/boot-parameter interfaces, and match the pinned kernel
digests. Both ONNX/MWW wake-runtime smoke checks passed. Each rootfs's hostname,
application and runtime hashes match its build inputs; the pending computer-USB
connection protection is included. Gain/filter/mixer/client race tests, 49
build/packaging/platform tests, 31 Dot tests and 21 installer/recovery tests
passed. No candidate was flashed, pushed, tagged or released during this
integration. Build success does not close the physical qualification gates.

## Biscuit

The isolated gain-only trial and isolated filter-only trial passed owner voice
checks. The latter also passed immediate first words, quiet/far requests,
reply interruption and hardware mute/unmute, with verified eight-channel filter
readback and zero capture stalls/drops through 120 seconds. Original services
and the original kernel were restored after the comparison.

Remaining: installed **combined** gain/filter qualification, longer loud/close
speech and playback/headroom checks, cold boot/recovery, and final update tests.

## Radar

The public ADC driver/header are identical to Biscuit's, and previously measured
hardware exposes the same four stereo ADCs and nine-channel capture endpoint.
Radar still has distinct board config, device trees, speaker and privacy paths.

Remaining: physical gain/filter readback, normal/quiet/close requests, first
words, reply interruption, mute/privacy, loud playback/headroom, cold boot and
recovery/update qualification. A successful build is not a hardware pass.

## Update boundary

The factory image also adds a read-only, static held-key probe and an early
Volume Up hook before `switch_root`. Both boards require a continuous
three-second hold, correct device identity, the unit's checksum-verified
Linux/TWRP/handoff images, and a matching current recovery partition. Only then
may the existing automatic-return TWRP handoff run. Missing input/cache or
failed preflight causes no recovery write. If a handoff returns without a
verified original Linux image, boot stays in the small rescue environment.
The bootloader, stock system slots and settings are not changed by this hook.
This still requires a bootable kernel and readable cache, and briefly swaps
the shared recovery partition; it is not a separate immutable TWRP partition
or a power-loss-proof transaction. Both boards' physical Volume Up, ordinary
boot, TWRP return and interrupted/refused handoff checks remain release gates.

Offline verification passed held/released/unavailable keys, both board
identities, corrupt/missing/stale cache images, failed mount and unsuccessful
handoff behavior. The digest-pinned ARMv7 builder passes the Linux evdev ABI,
physical-input-name and timing tests, `go vet`, and the static helper build.
The exact rescue BusyBox has every required applet and parses the script;
the helper's no-input fallback also runs correctly under ARM emulation.
Using Biscuit's private saved image/header and the new audio kernel, the real
pinned CPIO/boot-image helpers produce a 13,996,032-byte Linux candidate and
12,896,256-byte TWRP handoff, each below the 16,777,216-byte partition limit.
The overlay preserves its kernel bytes. These are local offline checks, not
evidence that either board has booted this new recovery path. No device was
flashed, and neither release-qualification gate was opened.

The factory bundle consumes the newly built target-specific kernel, not the
older downloaded kernel asset. Current application OTA changes the app; System
OTA v1 stages an inactive rootfs. **Neither replaces the recovery kernel.**
The release manifest states this explicitly. Existing devices need a qualified
USB/recovery kernel update until a separately tested kernel OTA transaction is
available. The planned fleet OTA test must not be reported as covering kernel
replacement without that work. Keep both `release_qualified` flags false until
their recorded gates are completed. No push, tag, or release is implied by
local build integration.
