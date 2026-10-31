# Tater Linux for Echo Show 5 (Checkers)

This USB factory bundle installs Tater Linux on an unlocked Echo Show 5
**1st generation** (`checkers`). It preserves TWRP, flashes the pinned Checkers
Linux boot image, and creates the A/B rootfs store. Existing Tater identity and
setup are preserved by default; a new device or `--factory-reset` starts fresh.

Every Checkers running Tater firmware earlier than v4.0.0 must use this USB/TWRP
factory bundle once. The v4 transition itself is not an OTA update. Compatible
releases after v4.0.0 can update both the application and inactive root-system
slot through Tater's verified A/B updater.

The conversion reformats userdata and the former system partition, but first
saves and verifies existing Tater pairing, Wi-Fi, SSH, and satellite settings
and restores them afterward. Old OTA application slots are not restored. It
must be done over USB; it is not an OTA update. After installation, compatible
updates use Tater's A/B application updater on `/data` and its verified System
OTA for the inactive Linux rootfs slot. Kernel, boot, rescue, and layout changes
remain USB recovery operations.

## Before you start

- Confirm the device is a **2019 first-generation Echo Show 5** (`checkers`),
  unlocked with amonet-checkers 2.0.1 or newer and able to boot TWRP. Do not
  use this bundle on the second-generation Show 5 or another Echo model. See
  the [Checkers unlock guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-5-1st-gen-2019-checkers.4762900/)
  if unlocking is not yet complete.
- Use Python **3.10 or newer**, `adb`, and `fastboot` on the computer. Keep the
  Show on external power throughout and connect it with a USB **data** cable.
- Give the computer internet access so the installer can download the tested
  [Checkers LineageOS 18.1 v0.7 ZIP](https://github.com/amazon-oss/releases/releases/download/lineage-18.1-checkers-v0.7/lineage-18.1-20260904-UNOFFICIAL-checkers.zip)
  from its [publisher's release](https://github.com/amazon-oss/releases/releases/tag/lineage-18.1-checkers-v0.7).
  Alternatively, download it yourself and pass `--lineage-zip PATH`. The
  installer uses it only for the matching vendor drivers, without booting
  LineageOS. No separate LineageOS root step is needed; TWRP and the
  unlocked bootloader are the prerequisites.

The exact filename is `lineage-18.1-20260904-UNOFFICIAL-checkers.zip`. The
installer checks its SHA-256 before formatting or flashing anything:

```text
785fa643fd68b2e6f6f02d96a2da58373c6a577b92a27cf6cec69603bb94068e
```

If downloading it manually, on Linux run
`sha256sum lineage-18.1-20260904-UNOFFICIAL-checkers.zip`; on
macOS run `shasum -a 256 lineage-18.1-20260904-UNOFFICIAL-checkers.zip`; on
Windows PowerShell run
`(Get-FileHash .\lineage-18.1-20260904-UNOFFICIAL-checkers.zip -Algorithm SHA256).Hash`.
The printed hash must match exactly. The installer independently checks it.

## Install

1. Download and extract `tater-echo-checkers-vX.Y.Z-factory.tar.gz` from a
   [Tater Echo Linux release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases).
   Run commands below **inside its extracted directory**.
2. Boot the device into TWRP. Existing Tater setup is preserved by default;
   add `--factory-reset` to the install command when you want to erase it and
   enter a new pairing code. The installer will not run from a normal OS boot.
3. Check `adb devices` shows only the intended device in `recovery`, and verify the factory
   bundle. Use `--serial DEVICE_SERIAL` if more than one device is connected.

   ```sh
   ./install.sh --verify-bundle
   ```

4. Run the conversion. Without `--lineage-zip`, the installer downloads and
   verifies the pinned ZIP into `build/` (or the `--work` directory) and reuses
   a verified copy on later runs:

   ```sh
   ./install.sh
   ```

   Use `./install.sh --factory-reset` for a deliberately clean setup.

   On Windows, run `py -3 install.py --verify-bundle`, then
   `py -3 install.py` instead. `--wifi "Your network"` is optional; without it,
   Checkers asks for home Wi-Fi in its setup hotspot. For an offline install or
   a ZIP you already downloaded, add
   `--lineage-zip /path/to/lineage-18.1-20260904-UNOFFICIAL-checkers.zip`
   (use a Windows path on Windows). The Wi-Fi passphrase is prompted for
   without putting it on the command line. If the download fails, nothing is
   flashed; retry online or supply the verified local ZIP.

The installer verifies the connected device, backs up its small partitions,
asks before destructive writes, installs slot A, and waits for Tater Linux over
USB. On the verified Checkers bootloader it replaces only the 6,105-byte
Amazon wordmark slot in `expdb` with the monochrome Tater logo, then reads the
partition back. Any other bootloader hash keeps Amazon's logo, and a failed
readback restores the complete saved partition before the device restarts.
The temporary
"INSTALLING TATER" screen during USB installation is normal; it changes to
the Tater setup screen when slot A boots. A previously paired device reconnects
automatically. For a new or reset device, join the open
`Tater-Setup-XXXX` Wi-Fi network shown on Checkers, then open
`http://192.168.4.1`. Enter your home Wi-Fi, Tater server URL, and an
**Add Satellite** pairing code from Tater. If you prefer to provision over USB,
run the installer with `--usb-pair`; you can also pass `--tater-url` and
`--pairing-code` together for non-interactive USB pairing. Do not unplug power
or USB during flashing. Keep the
`backups/<serial>/` directory private: it can contain a device token and Wi-Fi
credentials, and is needed for device-specific recovery.

The setup network is intentionally open. Complete setup near the device:
the home Wi-Fi password entered in the local HTTP form is not encrypted over
that temporary network. The hotspot disappears after pairing.

To repeat setup later, press Mute five times quickly and hold the sixth press
for five seconds. This clears the saved Wi-Fi and Tater pairing and brings
back the setup hotspot; it does not erase the firmware.

While the device is unpaired, the health timeout is deferred and deliberate
setup retries refresh the root-slot boot attempts. Unplanned power cycles
still count as trial boots. After pairing and a five-minute connected health
trial, the new root slot commits and restarts once more automatically. That
final restart is expected and returns the shared root/store filesystem to
read-only.

Normal repair uses Tater Recovery. From a normally booted Checkers, connect USB
and run `python3 to_recovery.py` from this extracted factory archive. On
Windows use `py -3 to_recovery.py`; on Linux, use
`sudo python3 to_recovery.py` if the serial port reports permission denied.
Holding **Volume Up** while Checkers powers on enters its retained TWRP through
the native boot chain; it no longer requests Tater Recovery after Linux starts.

Once recovery appears, run `./install.sh --repair`
(`py -3 install.py --repair` on Windows) to repair the inactive root slot while
preserving Wi-Fi, pairing, room, and satellite settings. Add `--factory-reset`
only to clear those settings. TWRP remains the lower-level fallback through
`to_twrp.py` and the amonet recovery procedure. If the factory installer stops
before initial installation completes, keep the device connected and preserve
the matching `backups/<serial>/` directory.
