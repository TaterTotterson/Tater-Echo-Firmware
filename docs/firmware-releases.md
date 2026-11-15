# Firmware release and OTA contract

## Versioning

Tater Echo firmware uses annotated `vMAJOR.MINOR.PATCH` tags. The exact tag is
compiled into the userspace binary and copied into `firmware-manifest.json`.
Only tags in that format trigger a firmware release.

## Build provenance

[`platforms/tater-echo-linux-lock.json`](../platforms/tater-echo-linux-lock.json) is the single
source of truth for Tater Echo Linux commits, platform downloads and hashes, the
Go/Alpine base, exact build packages, and the immutable multi-architecture
builder index and per-platform digests. Firmware build scripts disable Go's automatic toolchain
download and do not install packages from live repositories. Athena's rescue
store uses the APK closure frozen inside that builder. Each factory and release
manifest records the lock hash, source commit, release, and builder digest so a
published artifact can be traced back to the complete platform input set.

## Target identity

Every native `hello` reports `firmware_target`, alongside the existing Echo
hardware identity. Tater uses that target key with `targets/targets.json`;
current keys are `biscuit`, `radar`, `checkers`, `cronos`, `crown`, `athena`,
and `rook`. A future model
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
A/B rootfs under cache. Checkers, Cronos, Crown, and Rook use hash-pinned Tater Echo Linux
platform images and retain TWRP. Their USB installations use target-matched
LineageOS packages only as the source of each unit's vendor driver tree, then
replace Android's system partition with the A/B store. Athena instead uses
Tater Echo Linux's pinned 64-bit mainline kernel and system-partition store. Its
installer extracts the unit-specific speaker tuning from the private Fire OS
boot backup, inserts it into the release boot image, and writes only `system`
and `boot`.

v4.0.0 is a new factory baseline for Biscuit, Radar, Checkers, Crown, Athena,
and Rook. Every unit running Tater firmware earlier than v4.0.0 must install its
matching v4.0.0 factory archive through USB/TWRP once; a pre-v4 updater cannot
safely apply the complete platform, recovery, kernel/boot, branding, and System
OTA transition. The v4 release must therefore never be offered as an
application-only OTA substitute to a pre-v4 satellite. Factory installers
preserve verified Tater identity, Wi-Fi,
SSH, and satellite settings by default, while discarding prior OTA application
slots so the bundled version boots. Pass `--factory-reset` for a clean setup.
Routine verified application and root-system OTA begins after the v4.0.0
factory installation. Every factory archive includes
`to_recovery.py` and a verified rootfs repair payload. The normal recovery flow
enters Tater Recovery over USB, then runs `install.sh --repair` to install the
bundle into the inactive A/B root slot without clearing the satellite's
settings. An explicit `--factory-reset` clears Wi-Fi and pairing too.

Volume Up at power-on keeps native TWRP on Checkers, Cronos, Crown, Athena,
and Rook. The Linux-side held-key fallback requests TWRP too. Screen system
updates remove the retired Tater-generated cache redirect without replacing
unrelated OpenRecoveryScript commands. There is no automatic handoff to Tater
Recovery. Every archive retains `to_recovery.py` as the explicit USB route to
Tater Recovery and `to_twrp.py` as the explicit TWRP route.

Biscuit and Radar's new USB boot image checks a three-second Volume Up hold
in its initramfs, before starting a main system slot. Correct board identity,
the unit's checked Linux/TWRP/handoff copies and matching current recovery
image are required before the existing automatic-return TWRP handoff runs.
Older boot images retain the late five-second Tater Recovery fallback. The
early route still starts a kernel, reboots once and swaps the shared recovery
partition; it is not a bootloader change or a power-loss-proof transaction.
Both-model physical entry/return qualification remains required.

## Hardware-platform USB transition (after v4.2.3)

This hardware release introduces inner OTA **schema 2** for applications,
compatibility bridges and complete systems on all seven targets. Each package
requires the exact platform `echo-hardware-v1/<target>`. Factory root filesystems
contain the matching read-only `/etc/tater-ota-platform`; the packer refuses
old, wrong-target or unstamped rootfs inputs. A copied new daemon on an old
system cannot authorize the platform OTA. Rootfs-only USB `--repair` also
refuses crossing this platform boundary: use the **full target-specific USB
factory upgrade**, which preserves verified settings by default.

The outer release catalog stays schema 1 so older hosts can still discover it.
An unchanged v4.2.3 updater refuses the schema-2 inner packages before selecting
an app slot or invoking system installation. Older hosts may download a bundle
and show a generic rejection, but must not be supplied a schema-1 bypass bridge.
New Tater and HA Bridge versions inspect `ota_policy`, `ota_package_schema`
and `ota_platform` to show a clear USB-required message before downloading.
After the full USB upgrade, compatible schema-2 application and system OTA
remain available. A later incompatible kernel/platform transition must change
the platform identity, not merely the displayed application version.

`system_ota_version=1` still describes the command/transport support; it is not
the inner package schema. Firmware additionally reports `ota_package_schema=2`
and reports `ota_platform` only when its installed rootfs marker matches its
target. The HA Bridge verifies a system-stage reboot's `system_version` before
sending the final app, preventing rollback from being mistaken for success.

Before distribution, run `python3 -m tools.check_legacy_ota`. It compiles the
actual OTA implementation from v4.2.3 commit
`d5b395a6e680a5b765fcd6c3339598f7d3807ff5` against schema-2 app/bridge/system
fixtures for all targets, using only a temporary directory and loopback HTTP.
The 21 legacy rejection checks, current updater/host tests and packaging tests
do not replace end-to-end USB preservation, cold-boot, recovery and migrated
fleet OTA qualification. No push, tag or release is implied by these checks.

## OTA artifacts

Routine releases contain two deterministic OTA layers. Application bundles
have a per-file manifest; Biscuit and Radar bundles contain the native daemon;
screen-target bundles also contain the native renderer and optional camera
helper. The daemon embeds and verifies the matched built-in Hey Tater
MWW/OWW/NWW wake bundle, so
an ordinary OTA installs or repairs every built-in engine model. USB factory
bundles retain a full Linux rootfs for recovery.

Every target also publishes a `system-ota.tar.gz` wrapper containing a complete
rootfs plus an internal target, version, size, and SHA-256 manifest. This path
is available after the v4.0.0 factory baseline is installed; it does not make
the pre-v4-to-v4 transition OTA-compatible. For later compatible releases, Tater
first updates the application when the connected daemon lacks the required
System OTA support, then sends `ota.system.url`. The daemon verifies the outer
download and embedded rootfs independently before `slotctl install` writes the
inactive root slot. The established trial-boot health check commits the new
slot or automatically returns to the previous one. The System OTA transaction
itself stages only rootfs; it does not flash boot/recovery, partition tables or
vendor storage. Athena's separate platform kernel updater can apply its matched
bundled kernel after the root slot settles, preserving unit speaker tuning and
its kernel trial/rollback policy. The other targets' new kernel migration is
delivered through the full USB upgrade. OTA never writes bootloader branding. The monochrome Tater
boot logo is a guarded USB-factory step on Checkers, Cronos, Crown, and Rook: the
installer changes only a known logo slot in the verified `expdb` backup, reads
the partition back, and
restores the complete backup if verification fails. Unknown bootloader layouts
keep Amazon's logo. Athena deliberately never patches its protected `lk_real`
unlock chain. Its factory-built system store paints the Tater mark at the first
safe Linux framebuffer stage, so a brief Amazon frame can remain immediately
after power-on. Biscuit and Radar have no screen.

Each target's manifest policy includes `minimum_system_version`, the oldest
Tater Linux rootfs that satisfies the current application release. Tater
compares the satellite's separately reported `system_version` against this
minimum. App-only releases retain the previous minimum, while a release that
requires rootfs changes advances it for only the affected targets. The latest
system artifact is cumulative, so a satellite that skipped a required system
release can install the current rootfs directly. Manifests created before this
field existed retain the conservative behavior of matching the system to the
overall firmware release version.

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
  "url": "https://…/tater-echo-biscuit-v4.0.1-ota.tar.gz",
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
  "url": "https://…/tater-echo-biscuit-v4.0.1-system-ota.tar.gz",
  "sha256": "<64 lowercase hex characters>",
  "size_bytes": 73400320
}
```

Success means the inactive root slot has been staged and a trial reboot has
been requested. Tater verifies the rootfs `system_version` after reconnect;
the device's existing slot health policy remains responsible for commit or
rollback.

### Expected root-system reboots

Factory installation and System OTA both activate a newly written root-system
slot as a trial. This behavior is separate from routine application OTA and
does not occur on an ordinary boot.

| Target | Root-slot activation | After the health trial passes |
|---|---|---|
| Biscuit | Reboots into the new trial slot | Commits in place; no additional confirmation reboot |
| Radar | Reboots into the new trial slot | Commits in place; no additional confirmation reboot |
| Checkers | Reboots into the new trial slot | After five connected, healthy minutes, commits and restarts once more with read-only root/store mounts |
| Cronos | Reboots into the new trial slot | After five connected, healthy minutes, commits and restarts once more with read-only root/store mounts |
| Crown | Reboots into the new trial slot | After five connected, healthy minutes, commits and restarts once more with read-only root/store mounts |
| Athena | Reboots into the new trial slot | Commits in place; no additional confirmation reboot |
| Rook | Reboots into the new trial slot | After five connected, healthy minutes, commits and restarts once more with read-only root/store mounts |

Consequently, a successful Checkers, Cronos, Crown, or Rook System OTA normally
shows two reboots across the complete operation: one to activate the trial slot
and one after the health commit. The second reboot is intentional and should
not repeat after the slot is marked good.

## Manifest

`firmware-manifest.json` is the API consumed by Tater. For every target it
records:

- unlock compatibility and support status;
- whether factory and OTA are available;
- the minimum compatible Tater Linux system version;
- the application, System OTA, and factory artifacts;
- the exact asset names;
- byte sizes; and
- SHA-256 hashes.
- the platform lock, upstream source revision, and builder provenance.

Tater should use the manifest rather than infer filenames. This lets future
Echo models use different factory formats without changing the OTA UI.
