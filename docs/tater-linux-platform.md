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
  `dot-v1.0.24` source; Show and Spot retain their exact imported baseline
  commits until their replacement artifacts are reproduced.
- Added a six-target manifest and validation workflow covering Biscuit, Radar,
  Checkers, Crown, Athena, and Rook.

Remaining release work:

1. Reproduce the exact currently pinned binary artifacts before changing each
   target's download. Source now comes from Tater Echo Linux; the existing
   binary URLs remain verified upstream baselines during this gate.
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
