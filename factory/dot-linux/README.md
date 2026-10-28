# Biscuit and Radar Tater Linux factory installer

This factory bundle installs Alpine-based Tater Linux on Biscuit or Radar. It
is intentionally a reset, not a migration from pre-v3 firmware.

> [!IMPORTANT]
> Biscuit and Radar running firmware older than v3.0.0 must use this USB/TWRP
> factory installer once. The pre-v3 base system cannot install the v3 OTA
> format. The reset clears the satellite's Tater settings; later releases use
> the normal Tater OTA flow.

1. Complete the matching amonet unlock and boot the unit into TWRP.
2. Connect USB and extract the matching factory archive.
3. Run `./install.sh --verify-bundle`, then `./install.sh` (macOS/Linux), or
   run `python3 install.py --verify-bundle`, then `python3 install.py`
   (Windows).
4. Type `ERASE` when prompted.
5. Join `Tater-Setup-XXXX`, open `http://192.168.4.1`, and pair the satellite.

The factory installer and a future System OTA reboot Biscuit or Radar into a
trial root-system slot. A healthy root slot commits in place on these two
targets, without the additional five-minute confirmation restart used by
Checkers, Crown, and Rook. Routine application OTA does not use the root slot.

The installer checksum-verifies every bundled file and every partition backup,
builds the Linux boot image from that unit's own TWRP header, checks the
board-specific kernel, writes only recovery/cache plus Tater-owned userdata,
and preserves the stock system slots. TWRP is copied into the Linux slot store
as a lower-level fallback; normal repairs use Tater Recovery.

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

Without a computer command, begin holding **Volume Up** before powering on or
restarting and keep it held until the satellite restarts into Tater Recovery.
Tater Linux requires a continuous five-second hold during its first 90 seconds.

Once the USB recovery console appears, repair the inactive root slot with:

```sh
./install.sh --repair
```

On Windows use `py -3 install.py --repair`. This keeps Wi-Fi, pairing, room,
and satellite settings. Add `--factory-reset` only to clear those settings and
return to setup mode.

TWRP is still retained for a lower-level escape hatch. From a normally booted
satellite, run `python3 to_twrp.py` only when Tater Recovery cannot perform the
repair. If Linux cannot start far enough to accept either helper or the button
gesture, use the matching amonet/TWRP entry procedure.
