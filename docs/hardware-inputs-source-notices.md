# Open hardware inputs — v1.0.0-rc.1

These are build inputs for Echo 5 development, **not installable firmware**.
Their publication does not approve a factory install, kernel migration or OTA.
No proprietary owner-local library, SCP firmware, calibration, speaker tuning,
device backup, key or recording is included in these archives.

## Corresponding source and licenses

The helper and independent AP adapter source is Tater Echo Hardware commit
`cfa4c854f36e3537fcefc79492be0a2363a1a650`:

- <https://github.com/TaterTotterson/Tater-Echo-Hardware/tree/cfa4c854f36e3537fcefc79492be0a2363a1a650>
- The accompanying `tater-hardware-source.tar.gz` is the tracked source tree at
  that commit, not a copy of the ignored research workspace.
- Original code without a separate notice is AGPL-3.0-only. File-specific MIT
  and GPL-2.0-only notices take precedence. The source archive contains
  `LICENSE`, `NOTICE.md`, `LICENSES/MIT.txt` and `LICENSES/GPL-2.0-only.txt`.
- Android/OpenMAX headers retain their upstream notices. Their individual
  content/revision pins and the exact NDK r21e identity are in the firmware's
  `platforms/*-hardware-lock.json` and `platforms/athena-codec-lock.json`.
  They are build interfaces, not private runtime libraries in these archives.

The Athena kernel modules are GPLv2 components. Their Linux base is 6.18.56:
<https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-6.18.56.tar.xz>, SHA-256
`4633bbda2ab95eaf2a7af4041236451d6759c72e6946265531af4b3aacefbf89`.
Athena board sources and baseline patch scripts are in Tater Echo Linux commit
`505ac69ec421c066ad06447459eb53ea40f4599e` under
`platforms/show/tools/linux/athena/kernel/`. The independently written AP
module sources and SCP trial-driver patch are in the Hardware source archive,
under `kernel-patches/athena-codec/`. The firmware codec lock records their
individual source and binary hashes. The module bundle is deliberately
experimental, requires its exact matched kernel and is not a standalone
kernel update. Kernel/boot build reproducibility and final installer delivery
remain separate gates; this publication does not claim those are complete.

## Verification and separation

All five screen-device GPU/helper sets and both Athena AP executables were
freshly rebuilt from their locked sources and reproduced the locked outputs.
The Athena module binaries match their existing reviewed source/binary lock;
they were verified, not freshly rebuilt in this publication pass.

Every archive is packed twice with zero timestamps and an exact ordinary-file
allowlist, then unpacked and reverified. The committed firmware input catalog
pins the outer archive size/SHA-256 in addition to the inner source/binary
hashes. An isolated input-validation workflow can check these without opening
the physical firmware release gates.

Owner-local vendor libraries are dynamically used on qualified devices but
are not conveyed by these helper archives. Obtaining/preparing those inputs
and verifying a safe end-user USB installation is still required before the
firmware release. Do not distribute an owner's private runtime folder.
