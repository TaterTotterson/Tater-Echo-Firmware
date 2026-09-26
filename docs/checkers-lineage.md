# Checkers LineageOS 18.1 development path

This is the development bridge from rooted Fire OS to a Tater-owned Echo Show
5 first generation (`checkers`). LineageOS removes Amazon's Android services
and Alexa, while retaining Android 11 long enough to run the Tater Show APK as
the native daemon, audio, radio, display, and recovery integration are moved
off the Fire-OS/Magisk assumptions.

It is not yet the default factory path. The released Tater Checkers installer
is fully persistent on rooted Fire OS. On LineageOS it currently installs only
the screen preview with `./install.sh --no-home`; the Lineage init service,
coordinated daemon/APK rollback, and OTA recovery path must be hardware-tested
before the guard is removed.

## Exact supported base

- Device: Echo Show 5 first generation (2019), codename `checkers`, model
  AEOCH.
- Unlock: amonet-checkers 2.0.1 or newer, with working TWRP retained.
- ROM: unofficial LineageOS 18.1 v0.7 for Checkers, Android 11.
- Upstream release:
  [lineage-18.1-checkers-v0.7](https://github.com/amazon-oss/releases/releases/tag/lineage-18.1-checkers-v0.7).
- XDA device thread:
  [LineageOS 18.1 for Echo Show 5 (2019)](https://xdaforums.com/t/rom-unofficial-11-checkers-lineageos-18-1-for-the-amazon-echo-show-5-2019.4763475/).

```text
Filename: lineage-18.1-20260904-UNOFFICIAL-checkers.zip
SHA-256: 785fa643fd68b2e6f6f02d96a2da58373c6a577b92a27cf6cec69603bb94068e
Android security patch reported by the package: 2024-02-05
```

The late build date does not make Android 11 current: this image reports a
February 2024 security patch. Keep it on a trusted LAN and treat it as a
transition base, not a general-purpose tablet or an internet-facing device.
Do not add Google Apps; Tater does not need them.

### Camera and Bluetooth in upstream v0.7

- Bluetooth is expected to work in v0.7, including the A2DP sink and BLE. The
  Checkers source enabled the MediaTek Bluetooth stack, compiled the A2DP sink
  overlay into the Bluetooth APK, and declared the Bluetooth LE feature before
  this image was built. Tater still treats this as unverified until classic
  pairing and a real BLE scan pass on our Show.
- Do not count the camera as working in the unmodified v0.7 image. It contains
  the initial camera-provider configuration, but the complete Checkers path
  additionally needs the Amazon-ABI compatibility shims, the compatible
  `libdpframework` build, OV9734 orientation fix, and privacy-latch handling
  documented by
  [lineageos-echo-show-camera](https://github.com/jxlarrea/lineageos-echo-show-camera).
  That patched path has been community-verified on Checkers, so the hardware
  is supportable; Tater needs a patched Lineage build before advertising it.

## Before the wipe

The first installation replaces `system` and `boot` and wipes `data`. It does
not replace `recovery`, but that is not a substitute for a verified backup.
Before proceeding:

1. Boot Fire OS and confirm the device reports `checkers`.
2. Confirm root and amonet 2.0.1+.
3. Reboot to TWRP once and confirm it is usable.
4. Save and hash `mmcblk0boot0`, `mmcblk0boot1`, `kb`, `dkb`, `lk`, `tee1`,
   `tee2`, `expdb`, `MISC`, `boot`, `recovery`, `swdl`, `persist`, and
   `metadata`.
5. Save `/data/local/etc/tater` if this Show is already paired.
6. Keep the matching Checkers Fire OS package described in
   [`factory/checkers/README.md`](../factory/checkers/README.md).

These files contain device and vendor data. Store them privately and never add
them to Git or a release. The repository ignores `device-backups/` for this
reason. Verify every host copy before deleting a temporary on-device copy.

On the hardware-tested TWRP 3.7.0_9-0 session, recovery's temporary
`/dev/block/platform/soc/11230000.mmc/by-name/recovery` alias incorrectly
resolved to 32 MiB `mmcblk0p11` (`swdl`). Checkers' actual 16 MiB recovery
partition is `mmcblk0p10`. The Lineage installer did not change `p10`; its
post-install SHA-256 matched the pre-install backup exactly. Resolve and verify
the real block number and size before reading or restoring any raw partition;
do not trust a TWRP `by-name` alias blindly.

## Download and verify

```bash
curl -fL -o lineage-18.1-20260904-UNOFFICIAL-checkers.zip \
  https://github.com/amazon-oss/releases/releases/download/lineage-18.1-checkers-v0.7/lineage-18.1-20260904-UNOFFICIAL-checkers.zip

# Linux
sha256sum lineage-18.1-20260904-UNOFFICIAL-checkers.zip

# macOS
shasum -a 256 lineage-18.1-20260904-UNOFFICIAL-checkers.zip
```

Continue only when the result is exactly:

```text
785fa643fd68b2e6f6f02d96a2da58373c6a577b92a27cf6cec69603bb94068e
```

## Install from TWRP

This step erases the current Android data partition. It must not be run until
the backups above and the exact ROM checksum have been checked.

```bash
adb reboot recovery
adb wait-for-device
adb devices -l

adb shell twrp wipe cache
adb shell twrp wipe data
adb push lineage-18.1-20260904-UNOFFICIAL-checkers.zip /sdcard/lineage.zip
adb shell twrp install /sdcard/lineage.zip
adb reboot
```

The package asserts `checkers`, writes only its block `system` image and
included `boot.img`, and leaves the amonet/TWRP recovery partition in place.
The first boot can take several minutes. Stop and recover through TWRP if the
device repeatedly returns to the boot logo; do not flash another model's ROM
or overwrite recovery.

After Lineage boots, enable Developer options, USB debugging, and rooted ADB,
then verify the base before installing any Tater component:

```bash
adb root
adb wait-for-device
adb shell getprop ro.product.device
adb shell getprop ro.build.version.release
adb shell id
```

The expected values are `checkers`, `11`, and `uid=0(root)`. Also run
`adb reboot recovery` once and confirm TWRP still starts before continuing.

## Tater bring-up order

1. Install only the APK preview with `./install.sh --no-home --demo` and check
   orientation, touch, backlight, HOME selection, and loopback screen state.
2. Revalidate Checkers ALSA card/device names and mixer controls on Lineage;
   do not assume Fire OS numbering remains stable.
3. Run the native daemon manually without boot persistence and validate wake,
   capture, reply playback, reopen mic, AEC, barge-in, media synchronization,
   BLE, Wi-Fi setup, and intercom.
4. Add a Checkers-specific Lineage init service and explicit audio ownership;
   no Magisk module or Amazon package commands may be required for this path.
5. Make daemon and APK installation one recoverable generation, then exercise
   a deliberately unhealthy update and verify rollback.
6. Only after that hardware test, allow the normal factory installer and Tater
   OTA flow on LineageOS.

The longer-term target is a small Linux image using the proven Checkers kernel
and hardware work while replacing the Home Assistant/ESPHome application layer
with Tater's daemon, protocol, screen, and update system. LineageOS is the
bootstrap and rollback-friendly test environment, not the final dependency.

## Recovery to Fire OS

If TWRP remains available, reinstall the matching Checkers Fire OS package
from [`factory/checkers/README.md`](../factory/checkers/README.md), then boot it
once before applying any matching root image. Restore device-specific raw
partitions only when diagnosing an unlock-level failure and only from the same
physical unit; blindly restoring stock boot-chain partitions can remove the
amonet unlock or brick the device.
