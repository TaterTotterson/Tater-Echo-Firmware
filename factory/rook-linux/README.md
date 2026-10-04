# Tater Linux factory installer for Echo Spot (Rook)

This is the supported Tater Linux installer for the 2017 first-generation Echo
Spot (`rook`). It is not for Show 5 (`checkers`), newer Spot models, or other
Echo devices. The device must already be unlocked with amonet-rook v2.0.0 and
booted into TWRP over USB.

The computer needs Python 3, `adb`, and `fastboot`. The installer supports
macOS, Linux, and Windows and otherwise uses only Python's standard library.

The installer verifies the Rook device identity and partition numbers, checks
the bundle and the pinned Rook LineageOS 18.1 v0.3 ZIP, then takes checksum-
verified backups of the device's boot, recovery, system, lk, and expdb
partitions. It asks for `ERASE` before formatting userdata. It installs
LineageOS without starting Android, keeps the device's own vendor drivers,
flashes Tater's Rook boot image, and converts **system partition 11** into the
Tater Linux A/B store. It never writes TWRP, lk, expdb, or a boot logo.

The pinned LineageOS ZIP is downloaded on demand or can be supplied with
`--lineage-zip PATH`. Its SHA-256 is
`2755428c124df88ffd3bc6a9c36db16bbe3b7e639883fd1092dc839ac5ae9d13`.
No device-specific vendor binaries are included in this bundle.

From inside the extracted bundle, first run `./install.sh --verify-bundle`,
then `./install.sh --preflight-only --serial YOUR_SERIAL`. The preflight also
checks or creates the private backups, but does not write to the Spot. For the
actual first factory conversion, run `./install.sh --serial YOUR_SERIAL` and
type `ERASE` when the device name and affected partitions are displayed. Keep
both power and USB connected until the first-boot check finishes. The serial
argument can be omitted when exactly one device is in TWRP.

On Windows, use `py -3 install.py --verify-bundle`,
`py -3 install.py --preflight-only`, and finally `py -3 install.py` instead of
the `./install.sh` commands.

To repeat the factory installation on a Rook that **already runs Tater Linux**, add
`--reinstall-existing-tater` to both the preflight and install commands. The
installer requires the existing Tater slot-store filesystem label and either
the bundle's boot image or the exact earlier Rook bench boot image before
allowing this path. It saves the installed system
under `backups/<serial>/pre-reinstall/` and never overwrites the original
factory backups. Reinstallation erases the existing Tater setup, settings,
and A/B slots; pair the Spot again afterward. Do not use this option on a
newly unlocked device.

During factory installation, the rescue stage shows an upright, round-safe
`INSTALLING TATER` screen. This screen is part of the boot image, so an
application OTA cannot change it on an already-installed Spot. The first boot
is expected to show Tater setup. The root slot stays in trial
state until it connects to Tater and passes its health check. On OTA-capable
builds, Tater can update the daemon and screen together in application A/B
slots with automatic rollback; the Linux root filesystem and boot image still
require USB/rescue for updates. The direct-`wlan0` setup hotspot and application
OTA rollback have been hardware-tested on Rook. If installation stops, keep the
device connected and preserve `backups/<serial>/` for recovery.

Recovery/TWRP remains installed. A bad Tater boot can be repaired by restoring
the saved boot image through fastboot and the saved system image from TWRP.
Do not erase an existing A/B store or re-run the factory conversion without
diagnosing the device first. Keep the backup directory private; it contains
device-specific Amazon data and a local platform key.

The underlying Rook boot and hardware enablement derive from
[TECHO5 Spot](https://github.com/HuskerMinion/techo5-spot), MIT-licensed.
