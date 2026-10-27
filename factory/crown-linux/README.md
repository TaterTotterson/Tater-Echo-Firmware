# Tater Linux for Echo Show 8 (1st Gen, Crown)

This USB factory bundle installs Tater Linux on an unlocked **2019
first-generation Echo Show 8**, model **C7H6N3**, codename `crown`. It
preserves TWRP, flashes the pinned Crown Linux boot image, and creates the A/B
rootfs store. It pairs as a new Tater satellite.

> **Do not use this bundle on an Echo Show 8 2nd Gen (`A8H3N2`) or any other
> model.** The installer also reads `ro.product.device` in TWRP and refuses to
> proceed unless it reports `crown`.

The conversion **erases userdata and the former system partition**. It must be
done over USB and is not an OTA update. Routine updates after conversion use
Tater's A/B application updater on `/data`.

## Before you start

- Unlock the device with amonet-crown v2.0.1 or newer and confirm it can boot
  TWRP. Follow the
  [Crown unlock/root/TWRP guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-8-1st-gen-2019-crown.4766687/).
- Use Python **3.10 or newer**, `adb`, and `fastboot`. Keep the Show on external
  power and use a USB **data** cable.
- Give the computer internet access so the installer can download the pinned
  [Crown LineageOS 18.1 v0.5 ZIP](https://github.com/amazon-oss/releases/releases/tag/lineage-18.1-crown-v0.5).
  It supplies the matching vendor drivers and is not booted as the final OS.
  No separate LineageOS installation is required.

The exact ZIP is `lineage-18.1-20260904-UNOFFICIAL-crown.zip`. The installer
checks this SHA-256 before formatting or flashing:

```text
a01359a5e13dad8ac24c5507e8a747163b992012410b14d2ca9a8711ec4a1491
```

## Install

1. Download and extract `tater-echo-crown-vX.Y.Z-factory.tar.gz` from a Tater
   Echo Firmware release, then run commands inside the extracted directory.
2. Boot the Show into TWRP. Existing userdata and pairing are erased.
3. Check `adb devices` shows the intended device as `recovery`, then verify the
   bundle:

   ```sh
   ./install.sh --verify-bundle
   ```

4. Start the conversion:

   ```sh
   ./install.sh
   ```

   On Windows use `py -3 install.py`. For an offline install, pass
   `--lineage-zip /path/to/lineage-18.1-20260904-UNOFFICIAL-crown.zip`.

The installer verifies the target, downloads and verifies the pinned vendor
image before any device write, backs up small partitions, asks before
destructive changes, installs slot A, and waits for Tater Linux over USB. Keep
`backups/<serial>/` private and retain it for device-specific recovery.
On a recognized amonet-crown bootloader it replaces only the verified Amazon
wordmark slot in `expdb` with the monochrome Tater logo, then reads the
partition back. Unknown bootloader revisions keep Amazon's logo, and a failed
readback restores the complete saved partition.

When the Tater setup screen appears, join its open `Tater-Setup-XXXX` Wi-Fi
network, browse to `http://192.168.4.1`, and enter home Wi-Fi plus an **Add
Satellite** pairing code. Alternatively run the installer with `--usb-pair`.
Do not disconnect USB or power while flashing.

Crown's audio, display, touch, camera, reboot, OTA, and Tater Recovery paths
have been validated on physical hardware.

To enter Tater Recovery from a normally booted Crown, connect USB and run
`python3 to_recovery.py` from this extracted factory archive. On Windows use
`py -3 to_recovery.py`; on Linux, use `sudo python3 to_recovery.py` if the
serial port reports permission denied. You can instead begin holding **Volume
Up** before powering on or restarting and keep it held until Crown restarts
into Tater Recovery. The gesture is accepted only during the first 90 seconds
of Linux startup and requires an uninterrupted five-second hold.

Run `./install.sh --repair` (`py -3 install.py --repair` on Windows) in Tater
Recovery to repair the inactive root slot while preserving settings. Add
`--factory-reset` only to clear Wi-Fi and pairing. TWRP remains the lower-level
fallback through `to_twrp.py` and the amonet recovery procedure.
