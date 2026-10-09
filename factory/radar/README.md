# Echo 2 (radar) factory installer

This installer is the bridge from **amonet-radar v1.0.0 + TWRP + Fire OS 6**
to Tater Echo Firmware for the 2017 full-size Echo 2.
It runs on macOS or Linux and needs Python 3 and `adb`.

> **Use the published factory archive, not this source directory.** The release
> workflow adds `bundle-manifest.json`, the Radar-targeted firmware, emOS,
> wake-word assets, and the other verified payload files.

1. Follow the complete
   [amonet-radar unlock guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-2nd-gen-2017-radar.4801290/).
   Radar has no external USB socket; use the documented D+, D-, and GND pads
   with normal external power. Install Fire OS 6 to both A/B slots as the
   guide requires.
2. Boot TWRP using the method documented by the amonet-radar unlock guide,
   then connect the USB data pads. Radar does not provide a dependable
   hold-button shortcut for recovery. From a normal emOS boot, use its USB
   serial console: the command `/init recovery` is the dependable route back
   to TWRP.
3. Download and extract `tater-echo-radar-*-factory.tar.gz` from the
   [latest release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/latest).
4. Verify the bundle without touching the Echo: `./install.sh --verify-bundle`.
5. Run `./install.sh`. After reboot, join `Tater-Setup-XXXX` and finish setup.

The archive contains no Amazon kernel or device tree. The shared Puffin emOS
installer reads Radar's own stock `boot_a`/`boot_b`, saves a private recovery
image under `factory-backups/`, builds the final image locally, verifies every
write by reading it back, keeps stock in `boot_b`, and activates Tater in
`boot_a`. It resolves Radar's canonical boot, system, userdata, and cache
partitions by name after the amonet installer restores the GPT; it never
selects stale `boot_a_x`/`boot_b_x` aliases from an interrupted older unlock.
Both ARM and AArch64 emOS init binaries are included, and the installer selects
the one matching the stock kernel it actually read rather than inferring it
from Android's userspace ABI. Radar itself requires the hardware-tested Fire OS
6 ARM kernel. If the other slot still holds Fire OS 5/AArch64, the installer
leaves that valid kernel/system pair untouched as stock recovery and builds
Tater from the Fire OS 6 slot.

Radar's complete Tater path has been validated on physical hardware, including
setup, audio output, mute, speaker tuning, timers, and OTA. Keep the
soldered/pogo USB connection and the saved boot image available for recovery.
Use `./install.sh --no-reboot` to stop in TWRP. To restore stock boot, run
`./install.sh --restore factory-backups/<image>.img`.

Never publish or commit `factory-backups/`; it contains code read from your
own device.
