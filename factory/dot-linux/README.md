# Biscuit and Radar Tater Linux factory installer

This factory bundle installs Alpine-based Tater Linux on Biscuit or Radar.
Existing Tater pairing, Wi-Fi, SSH, and satellite settings are preserved by
default; add `--factory-reset` when you deliberately want a clean setup.

> [!IMPORTANT]
> Every Biscuit and Radar running Tater firmware earlier than v5.0.0, including
> v4.2.3, must use this USB/TWRP factory installer once. An older updater cannot
> safely install the complete v5 kernel/platform transition. The installer keeps any Tater setup it can
> verify, but replaces the old firmware and application slots. Compatible
> updates after the v5 USB installation use the normal verified application and
> System OTA flow.

> [!NOTE]
> On a Biscuit or Radar already running Tater, leave normal power connected,
> boot normally into Tater and connect USB data to the computer. Run
> `python3 to_twrp.py` from this extracted archive (`py -3 to_twrp.py` on Windows),
> wait for the computer to detect TWRP over USB, and then continue
> with the normal installer below. You do not need to hold Volume Up or enter
> Tater Recovery first. Do not use `--repair` for the v5 upgrade; repair mode
> only fixes an existing Tater system slot.

1. On an existing Tater installation, use the helper above to enter TWRP.
   For a new installation, complete the matching amonet unlock and enter TWRP
   using that device's unlock instructions instead.
2. Connect USB and extract the matching factory archive.
3. Run `./install.sh --verify-bundle`, then `./install.sh` (macOS/Linux), or
   run `python3 install.py --verify-bundle`, then `python3 install.py`
   (Windows).
4. Type `ERASE` when prompted. Add `--factory-reset` to the install command to
   erase Wi-Fi, pairing, SSH, and satellite settings.
5. A previously paired satellite reconnects automatically. On a new or reset
   satellite, join `Tater-Setup-XXXX`, open `http://192.168.4.1`, and pair it.

The factory installer and a compatible post-v5 System OTA reboot Biscuit or
Radar into a trial root-system slot. A healthy root slot commits in place on
these two targets, without the additional five-minute confirmation restart used
by Checkers, Crown, and Rook. Routine application OTA does not use the root slot.

The installer checksum-verifies every bundled file and every partition backup,
builds the Linux boot image and an automatic-return TWRP handoff from that
unit's own TWRP backup, checks the
board-specific kernel, writes only recovery/cache plus Tater-owned userdata,
and preserves the stock system slots. Before replacing Tater-owned userdata it
saves and verifies the small settings-only archive in `backups/<serial>/`; old
OTA application slots are intentionally not restored. TWRP is copied into the
Linux slot store as a lower-level fallback; normal repairs use Tater Recovery.

An empty or previously erased cache partition is supported. Preflight leaves it
untouched; after backups are verified and the user types `ERASE`, the installer
creates a fresh ext4 cache before installing the Tater Linux A/B rootfs.

Use `./install.sh --verify-bundle` to validate an extracted archive without a
device, or `./install.sh --dry-run` in TWRP to perform all checks and backups
without writing the unit.

## Tater Recovery after installing Tater Linux

Boot the Biscuit or Radar normally, connect it directly to the computer by USB,
close any program using its serial port, and run this from the extracted factory
archive:

```sh
python3 to_recovery.py
```

The helper finds one connected satellite, verifies that it is running Tater
Linux, and asks its A/B boot environment to enter Tater Recovery once. When
several compatible devices are connected, select one explicitly, for example
`python3 to_recovery.py --port /dev/ttyACM0` on Linux or
`py -3 to_recovery.py --port COM3` on Windows. If Linux reports permission
denied, close any serial terminal and run `sudo python3 to_recovery.py`.

The new factory boot image checks **Volume Up** in its small initramfs, before
loading either main system slot, Wi-Fi, audio, or Tater. Hold Volume Up before
powering on and keep holding through the early three-second confirmation. It
verifies the board identity, the saved Linux/TWRP/handoff image checksums and
the current recovery image before using the automatic-return TWRP handoff.
This starts the initial Linux kernel and reboots once into TWRP; it does not
wait for the main Linux system or Tater application to start. While TWRP runs
from RAM, the existing handoff restores Linux so the next restart returns to
Tater. Keep power connected throughout this recovery-image swap.

This early route needs the **v5 USB factory upgrade**; application/System OTA
does not install it. Old boot images retain their existing late Volume Up
gesture: a five-second hold during the first 90 seconds of Tater startup arms
Tater Recovery. That late fallback also remains available when an early
handoff is refused. Missing/corrupt cache images or an unbootable kernel still
require the matching lower-level amonet/TWRP recovery procedure. The new route
must pass physical cold-boot/recovery checks on each model before release.

Once the USB recovery console appears, repair the inactive root slot with:

```sh
./install.sh --repair
```

On Windows use `py -3 install.py --repair`. This keeps Wi-Fi, pairing, room,
and satellite settings. Add `--factory-reset` only to clear those settings and
return to setup mode.

The USB `to_recovery.py` route provides the same one-boot Tater Recovery path.
`to_twrp.py` remains the explicit lower-level TWRP route and restores the
verified handoff image while TWRP runs from RAM. If Linux cannot start far
enough to accept either helper or the button gesture, use the matching
amonet/TWRP entry procedure.
