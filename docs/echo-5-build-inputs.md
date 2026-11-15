# Echo 5 build-input readiness

Checkpoint: 2026-11-15. This is offline release preparation, not firmware 5.0.0
release approval. No satellite was flashed or rebooted in this work.

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
