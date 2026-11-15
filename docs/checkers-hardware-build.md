# Checkers hardware helper build and private runtime provisioning

This is an unpublished Checkers-only pipeline, not release qualification or a
kernel migration. Other Echo targets remain disabled. Do not flash or publish
these artifacts until the remaining [release gates](checkers-release-readiness.md)
pass.

## Source and artifact ownership

- `Tater-Echo-Hardware` contains the original helper sources and clean build
  recipe. `platforms/checkers-hardware-lock.json` pins every source file actually
  used, the public Android header revisions, NDK r21e identity, owner-local link
  inputs, and the three hardware-qualified output hashes. This is a content pin,
  not a fabricated Git commit or a reference to an unpublished GitHub repository.
- `Tater-Echo-Firmware` verifies that lock **before running** the research build
  recipe. Builds happen in a new temporary tree without old object files.
- Public output contains exactly `tater-hw-gpu`, `tater-hw-vdec`,
  `tater-hw-venc`, and `tater-hardware.json`. Vendor libraries, linker inputs,
  device identities, configuration, recordings, and private runtime directories
  are not public helper artifacts. Keep public source/license notices available
  alongside any eventual publication; repository licensing still needs review.

## Build once; consume the same verified set everywhere

```sh
python3 tools/hardware_bundle.py build \
  --source /path/to/Tater-Echo-Hardware \
  --ndk /path/to/android-ndk-r21e \
  --headers /path/to/public-header-checkouts \
  --link-libraries /path/to/owner-private-vdec/lib \
  --output /path/to/new-public-helper-directory
python3 tools/hardware_bundle.py verify /path/to/new-public-helper-directory
python3 tools/hardware_bundle.py pack /path/to/new-public-helper-directory \
  --output /path/to/new-public-helpers.tar.gz
```

The headers parent must contain the three checkout names in the lock at their
exact revisions, with no modified/untracked headers. The builder does not
download vendor files. Link inputs are read locally and are never copied to
public output. A successful build must reproduce the hardware-tested hashes;
different output requires a deliberate lock update and fresh qualification.

Pass `--hardware-bundle DIRECTORY` to `linux/checkers/build_linux.sh`, or set
`TATER_CHECKERS_HARDWARE_BUNDLE`. Without it, the script builds the set using
explicit `TATER_HARDWARE_SOURCE`, `TATER_HARDWARE_NDK`,
`TATER_HARDWARE_HEADERS`, and `TATER_HARDWARE_LINK_LIBRARIES` inputs. Checkers
cannot silently build a release rootfs without its locked helpers. Passing a
helper set to another target is refused.

The release workflow consumes a verified public helper archive using the
`TATER_CHECKERS_HARDWARE_BUNDLE_URL` repository variable. **No URL is currently
configured by this work.** Set it only after authorized public source/artifact
publication and audit. Missing input fails the Checkers release job; it does
not download private libraries or publish a software-only build as accelerated.
Downloaded archives have a size limit and an exact file allowlist; paths,
duplicates, links, non-ARM32 helpers, and lock/hash mismatches are refused.

The Checkers packer also requires an explicitly reviewed
`platforms.show.checkers_hardware` record in the platform lock: `source_commit`
must match the selected Show platform revision, `kernel_image_sha256` must
match the qualified camera kernel, and `boot_sha256` must match the selected
Checkers boot artifact. The packer independently hashes the **actual kernel
payload** inside that boot image. That record is deliberately not fabricated
by this work: the current older boot/source pins cannot be released as the new
hardware-enabled firmware.

## Application/system slots and rollback

System images place the open helpers beside their owning binaries in
`/usr/local/bin`. The release packer obtains the exact same helper set from
the verified rootfs for the full application OTA, not from a mutable `/data`
helper directory.

Each application slot contains its own daemon, renderer, camera, helpers, and
public metadata. Hardware helpers are selected beside the **actual running
executable**, never from another application/system slot or a shared helper
symlink. The camera wrapper also follows `tater-app binary tater-camera` on
Checkers. Switching/rolling back an app slot therefore switches its camera
and matching helpers together. System rollback similarly restores the helpers
inside that rootfs. Missing sidecars fail hardware preflight and retain the
software/JPEG path rather than borrowing a newer helper.

The OTA parser accepts hardware additions only for Checkers and requires all
three helpers, metadata, and the matching camera together. Bridge OTAs remain
daemon/renderer-only so an old updater can learn the extended archive format
before installing a full bundle. Their temporary absence of acceleration is
not an acceleration qualification.

The current private library ABI/tree identities are unchanged. Version 2
private manifests describe **libraries only**, not a mutable shared helper.
Valid legacy version 1 manifests remain accepted for their library identity;
the running firmware independently checks its own exact helper hash. Future
library ABI changes must use a separately qualified runtime/versioned linker
path rather than overwriting an existing `/data` runtime. This work does not
retrofit old released firmware with new manifests or promise accelerated
rollback into an older build that never supported these sidecars.

## Prepare private libraries on the owner's computer

From `Tater-Echo-Hardware`:

```sh
python3 scripts/prepare_tater_hardware_runtime.py \
  --gpu-source /path/to/owner-private-mali-runtime \
  --vdec-source /path/to/owner-private-vdec-runtime \
  --venc-component /path/to/owner-private/libMtkOmxVenc.so \
  --helpers /path/to/verified-public-helper-directory \
  --output /path/to/new-owner-private-runtime \
  --acknowledge owner-local-runtime
```

Supply already obtained, compatible owner-local runtime trees (`linker` plus
`lib/`), not arbitrary FireOS/Lineage archives. Automatic extraction from every
possible installed OS is **not implemented**. Every library-tree hash/count/size
and the encoder component are checked before copying. Existing output,
unignored Git output, and release/factory output paths are refused. Output is
private and contains no helpers, credentials, calibration, or recording files.

## Verify or explicitly provision by USB

The factory archive includes `provision_hardware.py` for Checkers:

```sh
# Local verification only; no device is opened.
python3 provision_hardware.py --runtime /path/to/new-owner-private-runtime

# Explicit installation into an already migrated, normally booted Checkers.
python3 provision_hardware.py --runtime /path/to/new-owner-private-runtime \
  --install --port /path/to/checkers-usb-console
```

The new factory installer also accepts `--hardware-runtime DIRECTORY`. It
verifies the supplied files **before** its ordinary flashing workflow and then
provisions after normal Linux boot. This does not supply the still-pending
kernel migration by itself: the USB provisioning guard requires the qualified
Checkers kernel build (`4.9.337-g8d928c5176cc`, build `#4`) and new helper-enabled
rootfs. Existing published factory boot pins do not yet satisfy that release
gate and must be migrated/qualified before publication.

The provisioner rechecks target, kernel, normal-slot boot, disk space, the
complete private inventory, uploaded archive digest and every staged file.
It verifies **all** existing library destinations before adding missing ones.
Matching legacy runtimes remain untouched; mismatched files or symlinks abort
without replacing them. Missing runtimes are renamed from private staging,
then synced. Successful transfers remove only their explicitly named temporary
files. Failed staging is retained for repair, never installed as a partial
runtime. Rerunning after an interrupted multi-runtime install is safe: complete
matching directories are verified and retained.

It never flashes a partition, changes a system/app slot, touches pairing data,
restarts services, or reboots. A normal reboot enables the new libraries.
There is no automatic replacement of a working owner's library set.

## Local evidence (2026-11-13 UTC)

- Two isolated clean builds reproduced all three hardware-qualified helpers.
- Both deterministic public archives have SHA-256
  `478cb846048ec11b78dc9b4bdf261379932a9a2e3d983ea5bfc6a92a59705d18`.
- Three complete offline `v4.2.3-checkers-rc.7` rootfs builds reproduced
  SHA-256 `09ee23c9d3627aff0da02c9531e096139f57edc75eec2e4730b9fb89fea3b5d9`
  (53,547,427 bytes). The exact embedded helper set passed the OTA packer's
  verification. No private GPU/OMX libraries, pairing config, or token were
  present in the rootfs audit. This candidate has **not** been installed.
  The third run also exercised automatic helper compilation from the pinned
  research source through the normal firmware build script.
- The actual owner-local inputs prepared and verified a 114-file library-only
  set locally. No device was accessed or changed for this work.
- Linux temporary-filesystem transactions exercise first install, idempotent
  reinstall, and refusal of mismatched existing libraries before any move.
- App tests retain distinct old/new helper sets in their slots, and the real
  shell rollback selector returns the matching camera/helper slot.
- Runtime-manifest, target-isolation, archive corruption/path/link rejection,
  camera and Go race tests cover the new paths. These are not substitutes for
  an installed-candidate hardware qualification.
