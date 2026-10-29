# Tater Linux factory installer for Echo Spot (Rook)

This is the supported Tater Linux installer for the 2017 first-generation Echo
Spot (`rook`). It is not for Show 5 (`checkers`), Show 8 (`crown`), newer Spot models, or other
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
Tater Linux A/B store. It never writes TWRP or `lk`. After verifying the full
`expdb` backup and Rook's exact embedded-logo layout, it replaces only the
12,096-byte Amazon logo slot with the monochrome Tater mark. An unknown layout
is left alone, and a failed readback restores the complete `expdb` backup.
Before userdata is formatted, any existing Tater pairing, Wi-Fi, SSH, and
satellite settings are saved and verified on the computer, then restored. Add
`--factory-reset` when a clean setup is wanted. Old OTA application slots are
never restored.

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
the bundle's boot image or an exact hash-verified earlier Rook Tater boot image
before allowing this path. It saves the installed system
under `backups/<serial>/pre-reinstall/` and never overwrites the original
factory backups. Reinstallation formats userdata and erases the A/B slots;
the saved Tater setup and settings are then restored unless
`--factory-reset` is also supplied. Do not use this option on a
newly unlocked device.

The current factory bundle corrects the Broadcom driver's shared
`00:90:4c:1a:09:00` Wi-Fi placeholder before association and DHCP. An older
installation missing that platform fix needs this `--reinstall-existing-tater`
path because routine application OTA does not replace the Linux root
filesystem.

During factory installation, the bootloader and rescue stage show the same
round-safe monochrome Tater mark, followed by `INSTALLING TATER`. The rescue
screen is part of the boot image, so an
application OTA cannot change it on an already-installed Spot. A previously
paired Spot reconnects automatically; a new or reset Spot shows Tater setup.
The root slot stays in trial
state until it connects to Tater and passes its five-minute health check. It
then commits and deliberately restarts one additional time so the shared
root/store filesystem returns read-only. The same confirmation restart follows
a successful System OTA; ordinary boots and application-only OTA do not trigger
it. A System OTA first reboots into its trial slot, so two reboots during that
complete update are expected: activation, then confirmation. On OTA-capable
builds, Tater can update the daemon and screen together in application A/B
slots, then stage a verified rootfs in the inactive Linux slot with automatic
rollback. The boot and rescue images still require USB recovery. The direct-`wlan0` setup hotspot and application
OTA rollback have been hardware-tested on Rook. If installation stops, keep the
device connected and preserve `backups/<serial>/` for recovery.

Tater Recovery is the normal repair path, while TWRP remains installed as the
lower-level fallback. Keep the backup directory private; it contains
device-specific Amazon data and a local platform key.

To enter Tater Recovery from a normally booted Rook, connect USB and run
`python3 to_recovery.py` from this extracted factory archive. On Windows use
`py -3 to_recovery.py`; on Linux, use `sudo python3 to_recovery.py` if the
serial port reports permission denied. You can instead begin holding **Volume
Up** before powering on or restarting and keep it held until Rook restarts
into Tater Recovery. The gesture is accepted only during the first 90 seconds
of Linux startup and requires an uninterrupted five-second hold.

Run `./install.sh --repair` (`py -3 install.py --repair` on Windows) to repair
the inactive root slot while preserving settings. Add `--factory-reset` only
to clear Wi-Fi and pairing. Use `to_twrp.py`, fastboot, or the amonet recovery
procedure only when Tater Recovery cannot perform the repair.

The underlying Rook boot and hardware enablement derive from
[TECHO5 Spot](https://github.com/HuskerMinion/techo5-spot), MIT-licensed.
