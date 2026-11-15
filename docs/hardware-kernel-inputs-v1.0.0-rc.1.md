# Echo hardware kernel inputs v1.0.0-rc.1

Developer build inputs for Echo 5, **not an installable firmware release**.
Do not flash these files directly. Publication does not qualify USB migration,
OTA, recovery, rollback, voice, privacy or two-device video behavior. The
firmware's physical release gates remain closed.

## What was reproduced

- Checkers, Cronos and Rook: fresh, offline Arm GCC 8.3 builds reproduce their
  reviewed raw kernels. The accompanying build receipts identify configurations,
  source archives, patches, toolchains and output hashes.
- Checkers: the candidate retains the public baseline's five device trees and
  separate microphone channels. This corrects the older camera trial's
  `amzn,mic-downmix` property. A second clean build reproduced the corrected
  input. It has **not been flashed** and needs a new physical voice test.
  The clean raw kernel also corrects stale timestamps in an empty built-in
  initramfs; the byte audit isolates all other raw-image changes to its build ID.
- Cronos: its eleven public baseline device trees and public ramdisk are
  preserved, rather than substituting newly compiled board trees.
- Rook: the camera-clock kernel reproduces exactly. The canonical public source
  archive and the earlier source archive have identical normalized source trees
  (60,322 entries). Only the kernel is supplied; factory tooling assembles the
  target-specific boot image from its separately pinned public inputs.
- Athena: a fresh Linux 6.18.56 build reproduces the kernel, empty-tuning DTB
  and all five SCP/AP kernel modules. The prepared boot reproduces the reviewed
  empty-tuning template with its narrowly audited 8 MiB codec reservation.
  It retains the public baseline's licensed MediaTek Wi-Fi/BT ramdisk, not an
  owner's private backup. The existing factory installer fills speaker tuning
  locally from the individual device.
- Biscuit and Radar: the previously built, target-specific ADC gain/80 Hz HPF
  kernels are verified against their source and output locks. This publication
  does not represent a new physical Radar test.
- Crown: retains its existing pinned public baseline. The uninstalled
  experimental camera-clock kernel is deliberately excluded.

## Source, recipes and licenses

Tater Echo Linux source/recipes:
[af8de59aa2e589133d733cb06624cc82cb564c54](https://github.com/TaterTotterson/Tater-Echo-Linux/tree/af8de59aa2e589133d733cb06624cc82cb564c54).
`tater-hardware-kernel-source.tar.gz` contains that tracked source tree,
including configurations, patch scripts and the build/boot preparation tools.
See `build-inputs/hardware-kernels/README.md` for rebuild instructions.

Original AP/SCP adapter source:
[Tater Echo Hardware cfa4c854f36e3537fcefc79492be0a2363a1a650](https://github.com/TaterTotterson/Tater-Echo-Hardware/tree/cfa4c854f36e3537fcefc79492be0a2363a1a650),
also included as `tater-hardware-source.tar.gz`. Its matching open helper and
module artifacts are published in the
[hardware-input prerelease](https://github.com/TaterTotterson/Tater-Echo-Hardware/releases/tag/hardware-inputs-v1.0.0-rc.1).

The independent Dot HPF patch, source lock and build wrapper are included in
`tater-audio-source.tar.gz`, from
[firmware b68247219457017a59a276eddf7afbf2430ee471](https://github.com/TaterTotterson/Tater-Echo-Firmware/tree/b68247219457017a59a276eddf7afbf2430ee471/linux/dot/audio).

The exact upstream source archives are supplied alongside these recipes:

- Amazon MT8163 commits `8d928c5176cc1ced93a564dd8a949a6cda3b8231`
  (Checkers/Cronos) and `4174e0b4d0e2eb452d14fbe3c0e5094611f748b6` (Rook).
- Linux 6.18.56 (Athena).
- Amazon's Echo Dot/Echo v2 6.5.7.1 source releases and Linux 3.18.19
  (Biscuit/Radar).

Kernel code, configurations and kernel patches retain GPLv2 and their
file-specific upstream notices; `GPL-2.0-only.txt` is included. Independent
tooling retains its own repository/file-specific notices. The public baseline
ramdisk's MediaTek Wi-Fi/BT firmware license is retained inside that ramdisk
and supplied separately as `LICENCE.mediatek`; publication does not relicense
that firmware. `SOURCE.json` and
`SHA256SUMS` identify every supplied input.

No owner-local GPU/codec library, private SCP firmware, filled speaker tuning,
calibration, key, pairing/Wi-Fi setting, device backup or recording is supplied.
Completing and qualifying owner-local runtime delivery through the end-user
USB workflow remains a firmware release requirement.
