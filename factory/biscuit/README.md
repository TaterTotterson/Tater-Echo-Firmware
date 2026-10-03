# Echo Dot 2 (biscuit) factory installer

This installer is the supported bridge from **amonet-biscuit v2.0.0 + TWRP**
to Tater Echo Firmware. It runs on macOS or Linux and needs only `adb` and
Python 3.

> **Use the published factory archive, not this source directory.** The source
> checkout's `factory/biscuit/` folder contains the installer template only.
> The release workflow adds `bundle-manifest.json`, compiled firmware, emOS,
> wake-word assets, and the other verified payload files.

1. Finish amonet v2.0.0 and the FireOS 6 flash/root steps from the XDA guide.
2. Boot the Echo into TWRP (white ring) and connect its USB cable.
3. Download and extract `tater-echo-biscuit-*-factory.tar.gz` from the
   [latest release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/latest).
4. From the extracted directory, run `./install.sh`.
5. After reboot, join `Tater-Setup-XXXX` and complete the Tater setup page.

The archive intentionally contains no Amazon kernel or device tree. The
installer reads those from the attached Echo, saves a private recovery image
under `factory-backups/`, builds the final emOS image locally, verifies both
partition writes by reading them back, keeps stock in `boot_b`, and activates
Tater in `boot_a`.

To stop before reboot, use `./install.sh --no-reboot`. To restore a saved stock
boot while in TWRP, use `./install.sh --restore factory-backups/<image>.img`.

Never publish or commit anything in `factory-backups/`; it contains code read
from your own device.
