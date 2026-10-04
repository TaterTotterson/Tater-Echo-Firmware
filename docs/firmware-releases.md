# Firmware release and OTA contract

## Versioning

Tater Echo firmware uses annotated `vMAJOR.MINOR.PATCH` tags. The exact tag is
compiled into the userspace binary and copied into `firmware-manifest.json`.
Only tags in that format trigger a firmware release.

## Target identity

Every native `hello` reports `firmware_target`, alongside the existing Echo
hardware identity. Tater uses that target key with `targets/targets.json`;
current keys are `biscuit`, `checkers`, and `rook`. A future model receives its own
target and factory installer even when its userspace binary can be shared.

## Factory artifact

The factory archive is for a device already unlocked with the target's listed
amonet version. Biscuit's archive includes Tater userspace, microWakeWord,
emOS tools, the image builder, installer, licenses, and matching BusyBox source.
Checkers' and Rook's archives include their complete Tater Linux rootfs, pinned
target boot image, USB installer, and the attributed TECHO5 components used for
platform setup. Checkers also includes its optional USB provisioning helper.

Biscuit does not include a boot image: its final emOS image is built from the
attached device's own stock boot partition. Checkers and Rook use hash-pinned
TECHO5 platform images, with upstream licenses and notices, and retain TWRP.
Their USB installations use target-matched LineageOS packages only as the
source of each unit's vendor driver tree, then replace Android's system
partition with the A/B store.

## OTA artifacts

Biscuit's OTA asset is the ARM userspace ELF. Checkers' and Rook's routine OTA
assets are deterministic gzip-compressed application bundles containing the
native daemon, native screen renderer, and a per-file manifest. Their full
Linux root filesystems remain in USB factory bundles for recovery and rare
base-system changes.
Tater sends the same target-independent command envelope for either format:

```json
{
  "type": "ota.url",
  "url": "https://…/tater-echo-biscuit-v2.0.0-ota.bin",
  "sha256": "<64 lowercase hex characters>",
  "size_bytes": 16410287
}
```

The actual command envelope includes the command/request identifiers used by
the Tater native protocol; this is the command payload relevant to release
selection. The device rejects a missing/invalid digest, a size mismatch, an
artifact of the wrong target format, an HTTP error, or an artifact over the
configured limit.

On Biscuit, success writes the inactive userspace slot and atomically switches
`/data/local/bin/server`; the supervisor switches back after three fast startup
failures. Checkers and Rook follow the same model under `/data/tater-linux/app`:
the daemon and renderer are staged together, their shared slot is switched
atomically, and either fast exits or a failed health window restores the prior
application slot. Neither routine OTA path remounts the live Linux rootfs.

## Manifest

`firmware-manifest.json` is the API consumed by Tater. For every target it
records:

- unlock compatibility and support status;
- whether factory and OTA are available;
- the exact asset names;
- byte sizes; and
- SHA-256 hashes.

Tater should use the manifest rather than infer filenames. This lets future
Echo models use different factory formats without changing the OTA UI.
