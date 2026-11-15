# Echo 5 build-input readiness

Checkpoint: 2026-11-15. This is offline release preparation, not firmware 5.0.0
release approval. No satellite was flashed or rebooted in this work.

## Published and pinned inputs

- [Open GPU/video helpers and Athena AP modules](https://github.com/TaterTotterson/Tater-Echo-Hardware/releases/tag/hardware-inputs-v1.0.0-rc.1):
  six exact, deterministic public archives. All five helper sets and both AP
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
  Both the cached-input and actual network-download paths were checked.

`platforms/hardware-release-inputs.json` pins helper archive sizes/hashes as well
as their source commit. `platforms/tater-echo-linux-lock.json` pins platform
source commits, kernel/boot assets and the decoder. No unset GitHub helper-URL
variables are needed. The isolated **Verify public hardware release inputs**
workflow verifies public downloads and recreates Mesa without approving a
firmware build for installation.

## Installer audit: still required before release

Public helpers are not the proprietary libraries they load. Existing hardware
trials used separately prepared owner-local runtimes; the public factory
artifacts must not contain an owner's private runtime or device backup.

The current private-runtime preparation/provisioning tools support Checkers,
Cronos and Rook only, and require exact, already assembled compatible local
library trees. They are not an automatic extractor for arbitrary FireOS,
Lineage or old Tater installations. Their checks refuse mismatches instead of
substituting another target's libraries.

The shared Checkers/Cronos factory path accepts and verifies a target-matched
`--hardware-runtime` before flashing. Rook's standalone provisioner is present,
but is not wired into its factory installer. Crown and Athena still need their
target-specific private-runtime preparation/install workflow completed.
Athena's public Mesa and AP module packaging do not supply its private codec
libraries or SCP runtime.

Consequently, **a full USB flash alone is not yet a complete general-user
hardware migration**. Before 5.0.0, finish a reproducible owner-local acquisition
and installation path for each target, including users already on Tater who
do not have the original FireOS files. Exercise a clean install, settings-
preserving reinstall and failure/retry paths; do not distribute the research
device's prepared libraries as a shortcut.

## Part 2: installed-candidate qualification

Once runtime delivery is complete, install the exact public candidate through
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
