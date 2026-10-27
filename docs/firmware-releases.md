# Firmware release and OTA contract

## Versioning

Tater Echo firmware uses annotated `vMAJOR.MINOR.PATCH` tags. The exact tag is
compiled into the userspace binary and copied into `firmware-manifest.json`.
Only tags in that format trigger a firmware release.

## Build provenance

[`platforms/tater-echo-linux-lock.json`](../platforms/tater-echo-linux-lock.json) is the single
source of truth for TECHO5 family commits, platform downloads and hashes, the
Go/Alpine base, exact build packages, and the immutable multi-architecture
builder index and per-platform digests. Firmware build scripts disable Go's automatic toolchain
download and do not install packages from live repositories. Athena's rescue
store uses the APK closure frozen inside that builder. Each factory and release
manifest records the lock hash, source commit, release, and builder digest so a
published artifact can be traced back to the complete platform input set.

## Target identity

Every native `hello` reports `firmware_target`, alongside the existing Echo
hardware identity. Tater uses that target key with `targets/targets.json`;
current keys are `biscuit`, `radar`, `checkers`, `crown`, `athena`, and `rook`. A future model
receives its own target and factory installer even when its userspace binary
can be shared.

## Factory artifact

The factory archive is for a device already prepared with the target's listed
unlock. Every archive contains a complete Tater Linux rootfs, a
target-matched pinned kernel/platform payload, installer, and all required
upstream licenses and notices.

Biscuit and Radar installers run in TWRP. They build the Linux recovery image
locally from the unit's TWRP header and the pinned board kernel, preserve
verified boot-critical backups and a restorable TWRP image, then install the
A/B rootfs under cache. Checkers, Crown, and Rook use hash-pinned TECHO5
platform images and retain TWRP. Their USB installations use target-matched
LineageOS packages only as the source of each unit's vendor driver tree, then
replace Android's system partition with the A/B store. Athena instead uses
TECHO5's pinned 64-bit mainline kernel and system-partition store. Its
installer extracts the unit-specific speaker tuning from the private Fire OS
boot backup, inserts it into the release boot image, and writes only `system`
and `boot`.

v3.0.0 is the Biscuit and Radar Tater Linux factory baseline. A unit running
firmware older than v3.0.0 must install the matching factory archive through
USB/TWRP once; the pre-v3 base system cannot consume the v3 OTA format. This is
a clean reset that clears its Tater satellite settings. Routine A/B OTA begins
after the v3.0.0 factory installation. Every factory archive includes
`to_recovery.py` and a verified rootfs repair payload. The normal recovery flow
enters Tater Recovery over USB, then runs `install.sh --repair` to install the
bundle into the inactive A/B root slot without clearing the satellite's
settings. An explicit `--factory-reset` clears Wi-Fi and pairing too.

The same one-boot Tater Recovery request powers the local startup gesture on
all six targets: hold Volume Up continuously for five seconds during the first
90 seconds of Tater Linux startup. It is deliberately an early-userspace
fallback, not a bootloader replacement. Every archive therefore retains
`to_twrp.py` as a lower-level escape hatch, and a unit that cannot start Tater
Linux still needs its target-specific unlock/TWRP recovery procedure.

## OTA artifacts

Routine releases contain two deterministic OTA layers. Application bundles
have a per-file manifest; Biscuit and Radar bundles contain the native daemon;
screen-target bundles also contain the native renderer and optional camera
helper. The daemon embeds and verifies the matched built-in Hey Tater
MWW/OWW/NWW wake bundle, so
an ordinary OTA installs or repairs every built-in engine model. USB factory
bundles retain a full Linux rootfs for recovery.

Every target also publishes a `system-ota.tar.gz` wrapper containing a complete
rootfs plus an internal target, version, size, and SHA-256 manifest. Tater first
updates the application when the connected daemon lacks System OTA support,
then sends `ota.system.url`. The daemon verifies the outer download and embedded
rootfs independently before `slotctl install` writes the inactive root slot.
The established trial-boot health check commits the new slot or automatically
returns to the previous one. System OTA never replaces the kernel, boot image,
rescue environment, partition table, or device-specific vendor storage. It
also never writes bootloader branding. The monochrome Tater boot logo is a
guarded USB-factory step on Crown and Rook: the installer changes only a known
logo slot in the verified `expdb` backup, reads the partition back, and
restores the complete backup if verification fails. Unknown bootloader layouts
keep Amazon's logo. Checkers and Athena also keep Amazon's logo until their
exact bootloader layouts have been independently verified; Biscuit and Radar
have no screen.

Each target also publishes target-specific `wake-runtime.so` and
`onnxruntime.so` sidecars. Every target receives the reduced Linux/musl ARMv7
build. A firmware OTA that first introduces either runtime verifies and
installs that exact library plus the matching shared feature models before
wake startup.
Factory images already contain them. A failed sidecar bootstrap does not stop
the daemon: MWW-only remains available, explicitly selected OWW or NWW modes fail
closed, and the verified download is retried on the next boot.

Tater sends the same target-independent command envelope to every target:

```json
{
  "type": "ota.url",
  "url": "https://…/tater-echo-biscuit-v3.0.0-ota.tar.gz",
  "sha256": "<64 lowercase hex characters>",
  "size_bytes": 16410287
}
```

The actual command envelope includes the command/request identifiers used by
the Tater native protocol; this is the command payload relevant to release
selection. The device rejects a missing/invalid digest, a size mismatch, an
artifact of the wrong target format, an HTTP error, or an artifact over the
configured limit.

On every target, success stages the bundle in the inactive slot under
`/data/tater-linux/app` and switches the shared active link atomically. Either
fast exits or a failed health window restores the prior application slot.
Routine OTA never remounts the live Linux rootfs.

System OTA uses the same authenticated native connection with a distinct
command so old application updaters fail closed:

```json
{
  "type": "ota.system.url",
  "url": "https://…/tater-echo-biscuit-v3.1.0-system-ota.tar.gz",
  "sha256": "<64 lowercase hex characters>",
  "size_bytes": 73400320
}
```

Success means the inactive root slot has been staged and a trial reboot has
been requested. Tater verifies the rootfs `system_version` after reconnect;
the device's existing slot health policy remains responsible for commit or
rollback.

## Manifest

`firmware-manifest.json` is the API consumed by Tater. For every target it
records:

- unlock compatibility and support status;
- whether factory and OTA are available;
- the application, System OTA, and factory artifacts;
- the exact asset names;
- byte sizes; and
- SHA-256 hashes.
- the platform lock, upstream source revision, and builder provenance.

Tater should use the manifest rather than infer filenames. This lets future
Echo models use different factory formats without changing the OTA UI.
