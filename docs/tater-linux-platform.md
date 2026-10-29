# Tater Echo Linux platform repository

Tater owns one public platform repository,
[`TaterTotterson/Tater-Echo-Linux`](https://github.com/TaterTotterson/Tater-Echo-Linux),
rather than maintaining three unrelated forks. It imports the histories needed
by Tater Echo Firmware:

| Tater Linux directory | Upstream history | Boards |
|---|---|---|
| `platforms/dot/` | `HuskerMinion/techo5-dot` | Biscuit, Radar |
| `platforms/show/` | `HuskerMinion/techo5` | Checkers, Crown, Athena |
| `platforms/spot/` | `HuskerMinion/techo5-spot` | Rook |

All three upstream projects are MIT-licensed. Their license files, copyright
notices, source URLs, and commit histories are retained. Import tags identify
the exact upstream baselines currently pinned by this firmware repository.

The directories stay distinct because these boards do not share one
interchangeable kernel, device tree, partition map, or boot image. Common
build, release, hash, and provenance tooling can move under `tools/` after the
three existing builds reproduce their currently pinned artifacts.

## Migration status

Completed:

- Created the public Tater Linux repository.
- Imported all three histories without squashing them.
- Tagged the upstream baselines currently pinned by Tater Echo Firmware.
- Moved the Biscuit USB Ethernet fragment, kernel build integration, and
  source patches into `platforms/dot/`.
- Made `Tater` names canonical for the new platform repository and firmware
  network controllers while retaining required compatibility names.
- Switched every firmware platform source checkout to the Tater repository.
  The Dot family uses a Tater-owned commit based directly on the pinned
  `dot-v1.0.24` source; Show retains its exact imported baseline commits until
  its replacement artifacts are reproduced.
- Added a six-target manifest and validation workflow covering Biscuit, Radar,
  Checkers, Crown, Athena, and Rook.
- Built and published separately hashed Biscuit and Radar kernels from the
  pinned Tater Dot source as the `dot-kernel-v1.0.1` prerelease. The kernels
  retain generic CDC-ECM and add the built-in RTL8152 driver required by the
  qualified powered adapter. Firmware consumes those Tater-named artifacts.
- Built and published Rook's `rook-kernel-v1.0.0` prerelease from its pinned
  Amazon source and toolchain with RTL8152 and bounded MediaTek role switching.
  Firmware now consumes that Tater kernel while rootfs and rescue remain on
  their independently pinned custody inputs.

Remaining release work:

1. Reproduce the exact currently pinned Show, Athena, and remaining Spot binary
   artifacts before changing each target's download. Source now comes from
   Tater Echo Linux; existing rootfs, rescue, and other unchanged binary URLs
   remain verified custody baselines during this gate.
2. Build and publish separate, hash-pinned Tater artifacts for every board. A
   Biscuit kernel must never silently replace Radar or Show artifacts.
3. Change each firmware artifact URL to its Tater Echo Linux release only
   after its factory and OTA bundles pass verification.
4. Keep compatibility links for legacy paths such as `/etc/techo5` and
   `techo5-*` until factory, rescue, rollback, and upgrade paths no longer
   reference them.
5. Remove compatibility names only in a later, explicitly tested migration.

This staged move lets Tater modify kernels and root filesystems directly while
keeping recovery artifacts reproducible and preserving upstream attribution.
