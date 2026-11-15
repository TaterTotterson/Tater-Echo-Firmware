# Echo 5 build-input readiness

Final review: 2026-11-15. Release preparation is accepted under the owner's
[documented code-review decision and physical-test waivers](echo-5-release-review.md).
The release locks are approved; final build/CI and artifact verification remain.
No firmware push/tag/publication was performed by this review. The checkpoints
below retain the earlier build-input and physical USB evidence; earlier closed
gate/pending-test statements are historical, not the current acceptance status.

## Published and pinned inputs

- [Open GPU/video helpers, Athena AP modules and compatibility DSOs](https://github.com/TaterTotterson/Tater-Echo-Hardware/releases/tag/hardware-inputs-v1.0.0-rc.2):
  seven exact, deterministic binary-input archives plus corresponding source.
  All five helper sets and both AP
  executables reproduce from their locked sources. The separate Athena kernel
  rebuild now also reproduces all five module binaries.
- [Kernel/boot candidates](https://github.com/TaterTotterson/Tater-Echo-Linux/releases/tag/hardware-kernels-v1.0.0-rc.1):
  independently rebuilt Checkers, Cronos, Rook and Athena inputs, verified Dot
  kernels, matching source/configurations and build receipts. Checkers retains
  separate microphones and requires a new voice retest. Crown deliberately
  retains its published baseline, not the untested clock experiment.
- [FFmpeg 8.1.2-2](https://github.com/TaterTotterson/Tater-Echo-Linux/releases/tag/video-decoder-v8.1.2-2):
  restores the H.264 Annex-B output/bitstream filters needed by hardware
  playback, retaining the existing protocols and decoders. A local clean build
  and independent GitHub build reproduce the exact binary; generated-only
  tests verify Annex-B conversion and ordered decoding.
- Athena Mesa is recreated from 29 exact signed Alpine packages. Download
  hashes, the preparation script and the resulting runtime manifest are pinned.
  Both the cached-input and actual network-download paths were checked. The
  preparer has its own source pin at Hardware commit
  `23d62e186915dbe55dc9b426ac0f18aa8020700e`, including native-Linux Docker staging
  ownership cleanup. RC.2 helper provenance is pinned to Hardware commit
  `a918c6d69eef3b9c12c3bd51268ac5919903e8b1`; only Checkers' codec executables
  change. RC.1 archives remain unchanged.

`platforms/hardware-release-inputs.json` pins helper archive sizes/hashes as well
as their source commit. `platforms/tater-echo-linux-lock.json` pins platform
source commits, kernel/boot assets and the decoder. No unset GitHub helper-URL
variables are needed. The isolated **Verify public hardware release inputs**
workflow verifies public downloads and recreates Mesa without approving a
firmware build for installation.

## Automatic device-local library setup

All five screen-target factory installers prepare hash-pinned public Android
inputs **before flashing**. The bundled Python reader extracts only bounded
ordinary files from two pinned images, without mounts, administrator privileges,
filesystem tools, or an NDK. Source-built compatibility libraries travel with
their source packet and upstream notice. Each installer then waits for its
selected device's new kernel/platform to boot, assembles libraries using that
unit's retained vendor files, verifies every output, and restarts it. Completion
requires a different boot identity and a successful target/kernel check.

No proprietary library, prepared owner runtime, device identity, recording or
backup is included in public firmware. No FireOS backup is required. MT8163
installers use pinned Lineage ZIPs only to restore vendor files; Android is not
booted. Athena retains its existing vendor partition. The AP-controlled codec
startup keeps SCP offline and requires no experimental SCP firmware.

Checkers now has a separately versioned retained-Lineage codec profile at
`/data/tater-vdec-lineage-1` and `/data/tater-venc-lineage-1`. Its old FireOS
GraphicBuffer ABI is never silently substituted. The original GPU helpers and
other targets' codec output hashes are unchanged. Checkers' new codec helpers
must still pass physical qualification alongside its separate-microphone kernel.

Offline checks reproduced all 101 runtime files per MT8163 target and all 79
Athena runtime files using the production shell assembly logic, including a
second verified installation. Linux tests also cover interrupted-root commit
and retry, changed vendor inputs, wrong existing destinations, unsafe links,
download hash/size errors, and a responding old shell during delayed reboot.
These are software tests, not physical USB or codec qualification.

See [the detailed supply-chain audit](https://github.com/TaterTotterson/Tater-Echo-Hardware/blob/main/docs/findings/owner-runtime-supply-chain.md).

## USB-only test candidates

`tools/package_echo_release.py --usb-test-candidate` requires a separate empty
output directory and produces only a factory archive, checksum list and
`usb-test-candidate.json` receipt. Both manifests explicitly mark it unqualified.
It creates no OTA archives or `firmware-manifest.json` feed. The mode defers
only physical approval: actual rootfs/helper/kernel/source identity checks
still apply. Normal release packaging remains blocked by all seven false
qualification flags.

## Part 2: installed-candidate qualification

All seven complete USB test bundles were built locally on 2026-11-15. Each
extracted installer passes `--verify-bundle`; payloads/checksums and target-
specific helpers were checked. Athena's complete store passes read-only fsck,
board/version/platform checks, and contains no rescue SSH authorized keys.
The exact Rook boot from the published 4.2.3 factory archive was independently
matched to its bundle manifest and added to the strict reinstall allowlist.
All five screen binaries omit Meadow while retaining the other artwork.
No satellite has been accessed or modified during this preparation.

Begin with the older Biscuit and Rook for real pre-v5 settings-preserving USB
upgrades, then full USB reinstalls on Checkers, Cronos, Crown, Athena and Radar.
Use normal power plus USB data and identify the exact unit before entering
recovery. Preserve verified private backups/settings; do not use factory reset
unless explicitly requested. Rook's existing-install path needs
`--reinstall-existing-tater`. Keep USB connected through device-local library
setup and its final reboot. On a stopped install, keep the private work/backups
and record the failing step; do not bypass an identity/hash failure.

Install the exact candidate through
the end-user workflow. Retest immediate first words, wake/reply interruption,
touch/volume/mute and camera privacy; verify the actual negotiated codecs in
both call directions, sustained motion/colors, normal reboot, recovery,
rollback, and the planned fleet-wide OTA checks. Recheck Checkers' corrected
separate-microphone candidate specifically. Prior application-only trials do
not qualify a newly assembled factory/kernel image.

All target `release_qualified` flags remain false. Packaging verifies the
actual boot/kernel payloads and Athena's matched module inventory, not only a
filename. Neither input publication nor a passing unit test may set those
physical gates. Do not create the final `v5.0.0` firmware tag yet.

### First Biscuit USB trial

The connected Biscuit was identified as **v4.2.2**, not a development v5 build.
Its candidate was repackaged only to include the clarified end-user USB/TWRP
instructions; installer, kernel and rootfs inputs are unchanged. The tested
factory archive is SHA-256
`4a18cfe5a3646055bee4d74766df2f484c7561bed17dcd2ccd3079db8e0f51c9`.

- The extracted bundle passed `install.sh --verify-bundle`. Its unmodified
  `to_twrp.py` entered TWRP, and `install.sh --dry-run` verified all 13
  boot-critical partition backups before installation.
- The ordinary `install.sh` path, without `--factory-reset`, completed after
  independently validating the saved settings and confirming its `ERASE`
  prompt. All 18 saved regular settings files matched byte-for-byte after boot;
  Wi-Fi, pairing and SSH state were preserved and Tater reconnected.
- The device booted v5.0.0 with `echo-hardware-v1/biscuit` and a good root slot.
  Startup physically verified all microphone PGAs at +40 dB, unity digital
  gain, software boost disabled, and the hardware 80 Hz high-pass active.
  One minute of capture reported zero stalls and subscriber drops.
- The owner passed two immediate wake/first-word requests, a quiet/far request,
  spoken replies, volume buttons and physical microphone mute/unmute.
- A physical cold boot with Volume Up entered the automatic-return TWRP image.
  Its handoff reported ready, and the restored recovery partition matched the
  unit-specific new Linux image byte-for-byte. Normal reboot returned to the
  good v5 slot, reconnected to Tater and reverified hardware gain/filter.
- The owner also passed post-recovery immediate first words, reply interruption,
  close pickup without harsh distortion and normal louder playback. These are
  short listening checks, not measured SNR/headroom or an extended soak. Final
  fleet OTA/rollback and extended qualification remain pending.

Private installer logs, settings and unit-specific backups stay in the ignored
test workspace, not the repository or release. This first unit's results do
not qualify Radar or any other target, or open Biscuit's final release gate.

### Second independent Biscuit USB trial

A second owner Biscuit, not used in the earlier hardware-development trials,
was independently identified as v4.2.2 and upgraded with a fresh extraction of
the **same unchanged factory archive** listed above. Its own 13 boot-critical
backups and settings archive were verified before confirming `ERASE`; all 15
saved regular settings files matched byte-for-byte after the successful
settings-preserving installation. No development files were staged and no
service was disabled. The old firmware briefly lost its USB console before
the recovery command opened it; the unchanged helper succeeded on retry after
the console returned.

The new v5 platform booted a good root slot, reconnected with its existing
pairing, verified +40 dB hardware gain/unity digital/no software boost and
verified the hardware 80 Hz filter. The owner passed immediate first words,
quiet/far pickup, replies, volume and physical mute/unmute. A physical Volume Up
cold boot reached TWRP and its automatic-return service restored the exact new
Linux image with a matching partition read-back. Normal return produced a new
boot identity, a good v5 slot, Tater reconnection and reverified hardware gain
and filter. The owner then passed immediate first words, reply interruption,
close pickup and normal louder playback. Fleet OTA/rollback and extended
qualification remain pending; these short listening checks are not measured
headroom/SNR results.

Additional microphone-offload changes on the Shows/Rook are deferred. Their
audio profiles and the other fleet candidates remain unchanged while these USB
tests proceed. Neither Biscuit trial opens any final public release gate.

### Checkers USB trial

The initial full settings-preserving USB install completed from fresh public
inputs, without any manually staged runtime or FireOS backup. Independent
read-back matched all 101 library files, three manifests and the installed boot
image. Pairing, Wi-Fi and SSH were retained. The corrected separate-microphone
kernel captured the expected four-channel S24_3LE/16 kHz stream; the owner passed
immediate first words, replies/interruption, controls/mute and the GPU UI.
Thirty generated frames passed hardware encoding and decoding in order, with
decoded YUV identical to an independent software decoder. These generated tests
opened neither camera nor microphone.

The trial found two release-candidate issues: Sat Chat's separate live-decoder
gate still required Checkers' old helper hash, and the runtime installer's final
restart named a rescue-only BusyBox binary absent from all five installed
screen rootfs images. The initial install completed only after the normal
five-minute health reboot. Exact ABI-specific hash selection and a direct call
to the installed Tater reboot helper correct these issues without weakening any
file/target/boot-identity gate. Regressions reproduced both defects before the
fixes. Four Linux video/UI race-tested packages and all 38 Linux runtime/rootfs
tests pass.

The final corrected USB candidate is SHA-256
`a7c23cb2d4c65cb935aa7268d03767f6911a1c1f89d95f8e9182da13c761b7fc`.
Only the daemon and renderer differ in its rootfs; kernel, camera, microphone
profiles and runtime-library hashes are unchanged. Its full end-user installer
retest completed with exit status zero, including the corrected automatic
restart and a different verified boot. All 39 settings files and all 104 runtime
files/manifests match independently; the firmware and boot read-backs match the
candidate. The normal five-minute trial committed and rebooted into good A with
a read-only store.

The owner passed the final build's voice/menu and physical camera-cover checks.
A real Checkers/Cronos call negotiated `echo-h264-balanced` in both directions,
with actual versioned encoder/decoder and GPU processes verified concurrently.
The owner passed colors, sustained ordered slow motion, audio and mute/resume;
the approximately two-minute relay sample had no dropped GOPs or missing,
duplicate or reversed source sequences. Checkers encoded at 15 FPS/768×432.
Ending the call stopped the camera and codec helpers cleanly, with microphone
capture still running; the owner's immediate post-call request passed.
A physical power-cycle with Volume Up reached TWRP directly. USB confirmed the
correct target and recovery, both recovery/SWDL partitions remained unchanged,
and no cached recovery-redirect script was present. Normal reboot returned to
the good v5 slot, paired connection, GPU and hardware-call support. The owner
passed final immediate first words, replies, menu and volume after recovery.

Checkers' USB qualification passes on this one previously upgraded development
unit. Final fleet OTA/rollback and extended qualification remain pending; this
is not release approval, and all public release gates remain closed.
Refresh the other screen factory bundles with the corrected runtime restart
before their physical USB trials.

### Cronos USB trial

The refreshed Cronos candidate is SHA-256
`86337fe838e1005def8625776079dbf78bde5890e3407ae102ffff7ce0512e2c`.
It includes the shared corrected runtime restart and public RC.2 helper
provenance. Only daemon/renderer differ from the prepared Cronos rootfs;
kernel, camera helper, audio profiles and hardware runtimes are unchanged.
The unaltered settings-preserving installer downloaded and verified its inputs
from an empty cache, without development libraries or a FireOS backup. All 15
partition backups independently matched the device before erasure, and the
41-file settings archive passed validation with pairing complete.

The full installer exited zero, including automatic device-local runtime
assembly and the verified restart. Independent read-back matched firmware,
the installed boot image and all 104 runtime files/manifests. Forty settings
files are byte-identical; the remaining screen file retains all original keys
and adds the newly supported text style from Tater's per-satellite settings.
Pairing, Wi-Fi and SSH are unchanged. The normal five-minute health trial
committed A good and restarted normally, retaining a read-only store and the
expected four-channel S24_3LE/16 kHz microphone capture.

The owner passed immediate first words/replies, reply interruption, menus/fades,
touch/sliders/equalizer, Graphics GPU, volume, microphone mute/unmute and camera
cover blocking/reopening. A real Cronos/Checkers call negotiated balanced H.264
both ways, with Cronos' actual encoder/decoder and both GPU scenes active.
The 123-second sample sent 1,812/1,845 frames without dropped GOPs, missing,
duplicate or reversed source sequences, audio drops or queue accumulation.
Cronos encoded approximately 15 FPS at 640×480; occasional approximately
280 ms picture gaps remain, so this is not a zero-stall or 30 FPS claim.
The owner passed sustained ordered motion/colors, delay, two-way audio and
physical privacy mute/resume. Camera/codecs stopped cleanly, microphone/GPU
stayed running and immediate post-call voice passed.

A physical cold power-cycle with Volume Up reached native TWRP directly.
Recovery/SWDL hashes match the original backups and no cached redirect script
was present. Normal return booted the good 5.0.0 slot, read-only store, paired
connection, GPU and balanced hardware-video support. All runtime hashes and
preserved settings were rechecked; the owner passed final immediate voice,
menu and volume. USB qualification passes on this one previously upgraded
development unit. Final fleet OTA/rollback and extended qualification remain
pending, with all public release gates closed. No push/tag/release was made.

### Athena USB trial

The first settings-preserving USB attempt exposed a real path mismatch:
Athena binds userdata's `techo5` directory to `/data` in normal Linux, but the
installer did not remove that layout's old `tater-linux/app` override. The new
system installed while the old application still ran. That attempt was
rejected. The exact pinned cleanup function is now corrected during packaging,
before checksumming, with behavior tests for nested app removal, retained
pairing/Wi-Fi/SSH/settings and the separate reset path. Unknown upstream source
is refused. Private old app and partition backups remain available.

The complete retest selected the actual 5.0.0 factory binaries and passed
voice/GPU/controls, but caught a second omission: application-slot camera
launches supplied the AP codec opt-in while the immutable supervisor still
disabled encoding. The factory camera now enables it only with its exact
boot-readiness marker, runtime manifests and encoder device. Missing or
mismatched prerequisites retain JPEG; helpers still independently verify every
pinned dependency. Behavioral tests reproduced the failure before the fix.

The corrected candidate is SHA-256
`a4d1d72fe38bf24c8386ef1c431798a3f840f79cfed49e8b6269836a907e6a69`.
A full pinned-source rebuild changes only `tater-camera-run` versus the prior
rootfs: every executable, model, kernel, module, Mesa file and other member is
identical. The release-style store passes fsck, embedded script/version checks
and an empty rescue authorized-keys check. The ordinary end-user installer
exited zero, including complete fresh verified partition backups, speaker-tuning
preservation, system/boot read-back, settings retention, automatic library
staging/verification and a different final boot. Previously downloaded public
inputs were reverified from cache; vendor inputs came from the retained device
partition, not a development runtime or FireOS backup.

Independent installed checks match all 81 codec runtime files/manifests, all
125 Mesa files/manifest and all 13 checked firmware/helper/module/template/
camera-startup files. Selected and running applications use the factory
binaries, not old slots. Pairing/Wi-Fi/SSH and other saved files are retained;
the only observed differences are screen text-style and cached volume fields
refreshed during the normal trials. The mixer's separate persisted volume is
authoritative. Native GPU composition and guarded
balanced H.264 capability are active, with SCP kept offline. The factory store
starts with A already good; over five minutes of stable operation is **not** a
System OTA trial-commit/rollback test. Four-channel S32_LE/96 kHz microphone
capture reported zero stalls or subscriber drops through the call.

The owner passed immediate first words/replies/interruption, menus/fades,
touch/sliders/equalizer, Graphics GPU, volume, microphone mute/unmute, camera
cover blocking/reopening, and audible Sendspin with volume/pause/resume.
A real Athena/Checkers call negotiated `echo-h264-balanced` both ways, with
Athena's actual 640×480 encoder, 768×432 decoder and GPU compositor concurrent.
A continuous 120.1-second sample sent 1,801 frames in each direction
(approximately 15 FPS), with no dropped GOPs, missing/duplicate/reversed
sequences, audio drops or accumulating queues. Occasional relay picture gaps
up to approximately 0.5 seconds remain; this is not a zero-latency or 30 FPS
claim. The owner passed sustained ordered motion/colors/delay, two-way audio
and physical privacy mute/resume. Call end released the camera and both codec
helpers while GPU/microphone stayed active; immediate post-call voice passed.

Physical cold Volume Up reached native TWRP directly. Independent USB identity
and SHA-256 checks confirm recovery, vendor, both TEE partitions, both LK
partitions and readable boot1 are unchanged from the original backup. Cache
was mounted with no recovery-redirect script. Normal reboot returned to a new
boot, good A 5.0.0, read-only store, factory application, native GPU and balanced
hardware-video support. Every runtime/firmware check was repeated; the owner
passed final immediate first words/replies/menu/volume. This is a USB pass on
one previously upgraded development Athena, not an untouched pre-v5 migration
sample or full release approval. Final fleet OTA/rollback and extended
qualification remain pending, all public release gates remain closed, and no
firmware push/tag/release was made.

### Crown USB trial

The refreshed Crown candidate is SHA-256
`7514297cab5b91bd106753590b47fed48e6f4133cd5ccbd7db41466878f0e587`.
It retains the pinned published baseline kernel, not the unqualified offline
camera-clock candidate. Only daemon/renderer and the Athena-only camera-startup
guard differ from the prepared rootfs; Crown's camera, audio and runtime policy
are unchanged. The ordinary settings-preserving installer started from an empty
public-input cache, without development libraries or a FireOS backup. All 15
partition/boot-area backups and all 40 settings files independently matched the
device before erasure; pairing was complete.

The installer exited zero, including automatic device-local library assembly
and a verified final restart. All 104 runtime files/manifests, 10 checked
firmware/helper/startup/wake-runtime files and the branded boot image match
independently. Factory applications are selected/running, not old app overrides.
Pairing/Wi-Fi/SSH and other saved files are unchanged; the only difference is a
normal server-synced screen text-style field. The ordinary five-minute root
trial committed and rebooted into good A 5.0.0 with a read-only store, native
Crown GPU and six-channel S24_3LE/16 kHz microphone capture.

The owner passed immediate first words/replies/interruption, menus/fades,
touch/sliders/equalizer, Graphics GPU, volume, physical microphone mute/unmute
and camera-cover blocking/reopening. A real Crown/Checkers call negotiated
balanced H.264 both ways, with Crown's actual 768×432 hardware encoder/decoder
and GPU concurrent. The continuous 120.2-second sample sent 1,803 frames each
direction (approximately 15 FPS), with no dropped GOPs, missing/duplicate/
reversed sequences, audio drops or sampled queue accumulation. Occasional
picture gaps up to approximately 0.293 seconds remain; this is not a zero-stall
or 30 FPS claim. The owner passed sustained ordered motion/colors/delay,
two-way audio and physical privacy mute/resume. Camera/codecs stopped cleanly,
GPU/microphone stayed active with zero microphone stalls/subscriber drops
through 300 seconds, and the immediate post-call voice check passed.

Physical cold Volume Up reached native TWRP directly. Bootloader/TEE/logo/
recovery/SWDL/boot0 hashes match the original backups. Full byte comparisons of
the two initially differing areas isolated only numeric bootcount and ext4
mount/write bookkeeping; identity/file/inode and all other bytes are unchanged.
Cache was mounted with no recovery redirect script. Normal return booted good
A 5.0.0, read-only store, factory applications, GPU and hardware-video support;
all runtime/firmware/boot/settings checks passed again. The owner passed final
immediate first words/replies/menu/volume after recovery. Crown's USB trial
passes on this one previously upgraded development unit, not an untouched
old-firmware migration sample. Remaining fleet USB,
final fleet OTA/rollback and extended qualification are pending; all public
release gates remain closed and no firmware push/tag/release was made.
