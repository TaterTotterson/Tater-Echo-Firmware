# Echo Show 5 first generation (`checkers`) native test firmware

This bundle installs Tater's screen, native satellite service, first-boot setup
hotspot, and persistent Magisk supervisor on an Echo Show 5 (2019) running
rooted stock Fire OS 6. It does **not** write boot, recovery, system, or vendor.
Removing the module re-enables Amazon's first-run screen.

## Prerequisites

- The device is `checkers`, unlocked with **amonet 2.0.1 or newer**.
- TWRP still boots.
- Rooted stock Fire OS 6 is still installed and the device appears in `adb
  devices`. Root through `su` is supported; root adbd is not required.
- `adb` and Python 3 are available on the macOS or Linux host.

Do not use amonet 2.0.0 for Checkers. It has a TWRP-upgrade fault on the 1.x to
2.x path. The hardware profile collected from Fire OS 6574.1 reports 997,780
kB of usable RAM, so native-service memory budgeting must assume a 1 GB
device.

This installer can recognize LineageOS 18.1, but the full persistent install
below is still Fire-OS/Magisk-specific. On LineageOS it deliberately permits
only `./install.sh --no-home` as a non-persistent screen preview. Follow the
separate [Checkers Lineage development guide](../../docs/checkers-lineage.md)
for the verified ROM and port status.

## Install or recover stock Fire OS before rooting

The amonet process can leave the original, potentially old Fire OS installation
in place. Do not assume that the static `boot-root.img` from an older forum
post matches that installation. A mismatched boot image can reach the Echo
loading screen and then restart indefinitely even though TWRP still works.

Use firmware only for **Echo Show 5 first generation**, codename `checkers`,
model AEOCH, package `com.amazon.checkers.android.os`. Tater intentionally
requires the package verified on hardware on September 24, 2026: Fire OS
6574.1 (`NS65741/8146`):

```text
Filename: update-kindle-checkers-NS65741_user_8146_0013222531716.bin
MD5:      d17ab1fb8fa374cc3f9b1d813d4094dc
```

Download it from the
[FTVDB Checkers firmware record](https://ftvdb.com/echo/firmware/com.amazon.checkers.android.os/d17ab1fb8fa374cc3f9b1d813d4094dc-13222531716-fire-os-6574-1-ns65741-8146-2026-09-15/).
FTVDB identifies the original Amazon source URL and preserves the model,
package, build number, and checksum. On the Linux host, verify and install the
package while the Echo is in TWRP:

```bash
# Linux
md5sum update-kindle-checkers-NS65741_user_8146_0013222531716.bin
# macOS
md5 -q update-kindle-checkers-NS65741_user_8146_0013222531716.bin
# Expected: d17ab1fb8fa374cc3f9b1d813d4094dc

cp update-kindle-checkers-NS65741_user_8146_0013222531716.bin update.zip
adb devices -l
adb push update.zip /sdcard/update.zip
adb shell twrp install /sdcard/update.zip
adb reboot
```

Do not wipe data. Let Fire OS boot successfully once, then return to TWRP and
save its matching stock boot partition before attempting root:

```bash
adb shell 'dd if=/dev/block/by-name/boot of=/sdcard/checkers-stock-boot.img'
adb pull /sdcard/checkers-stock-boot.img ./checkers-stock-boot.img
sha256sum checkers-stock-boot.img
```

Keep that image private; it contains Amazon software from the device. Use only
a rooted boot image derived from the same Fire OS release. If a later root
attempt boot-loops but TWRP still starts, reinstalling the matching Fire OS
package with the commands above restores the stock boot image. Do not flash
firmware for `biscuit`, `crown`, or the second-generation Echo Show 5, and do
not modify preloader, LK, TEE, or recovery while recovering this failure.

## Install

```bash
./install.sh
```

The installer verifies every bundled file, requires the exact
`NS65741/8146` build above, checks the device codename, root access, recovery
partition, and amonet 2.x boot layout. The on-device layout cannot distinguish
2.0.0 from 2.0.1, so it also asks you to confirm 2.0.1+ and working TWRP. It
installs two native A/B userspace slots, the wake runtime/model, setup-AP
support, the screen APK, and the reversible boot supervisor.

The installer synchronously resolves and validates Tater's protected UID set,
then starts the setup screen and native service while the slower package
quarantine continues in the background. Magisk restores the cached firewall
during its early `post-fs-data` phase on every restart, before Android
applications can establish public connections. The first conversion can take
about two minutes while Fire OS disables more than 100 packages, but it does
not block the visible setup flow. It disables the tested Alexa, Amazon
OTA, telemetry, logging, communications, commerce, registration, smart-home,
Amazon media, dictation keyboard, Amazon time-zone/content-provider, and Alexa
For Everyone applications. It also stops the separate Alexa indoor-location,
location-tracker, and Amazon Sidewalk daemons while
leaving Android Bluetooth and Tater BLE presence intact. Retained audio, Wi-Fi,
Bluetooth, settings, and WebView support remain available, but every remaining
dedicated Amazon application UID is limited to loopback, link-local, and
private/LAN destinations. Tater's native daemon and screen app retain normal
Internet access, so GitHub firmware OTA and public media/wake-sound downloads
continue to work.

Amazon job components embedded in either the core `amazon.fireos` resource
package or Android Settings must remain registered. This Fire OS build schedules
them without handling a missing/disabled target: framework jobs crash
`system_server`, while Settings jobs crash-loop the Settings process. Tater
therefore leaves those component declarations intact and quarantines only
independently safe packages and native daemons. The shared Android framework
UID is made LAN-only after local, DHCP, multicast, and private-network traffic
is allowed. Tater's root-owned updater and separate screen-app UID remain able
to reach public resources.

The certified build's `amazon.speech.sim` is a persistent system application:
Fire OS can restart it even after Package Manager disables it. When its Alexa
identity dependency is quarantined, that restart loop can eventually restart
Android's framework and strand the display on the Echo logo. The Magisk module
therefore replaces `/system/priv-app/SpeechInteractionManager` with an empty
systemless directory before Android scans applications. The stock directory is
never modified and returns when the Tater module is removed.

The same certified build marks Amazon's Bishop registration/account app as
disabled, but its `PERSISTENT` flag still creates a process before Package
Manager policy is applied. Tater systemlessly replaces that directory too,
eliminating the otherwise idle account process and its startup connection. As
with the speech manager, uninstall restores the untouched stock application.

An independent display watchdog also relaunches Tater Show if Android kills the
APK or restarts its framework, and removes a stale boot-animation surface only
after the Tater process is alive. If Fire OS cannot launch any activity after
twelve attempts, it allows one clean recovery reboot. The guard rearms only
after the screen has remained healthy, preventing a damaged APK from creating
an endless reboot loop.

The policy is systemless and reversible: it records only packages Tater
disabled, restores its validated firewall before Android starts, and restores the recorded
package states when `./install.sh --uninstall` removes Tater. Fire OS code that
runs inside Android's shared UID 1000 cannot be isolated process-by-process, so
the framework as a whole keeps LAN access but not public Internet access.

When `Tater-Setup-XXXX` appears, connect a phone or computer and complete the
captive portal. The Show joins normal Wi-Fi, redeems the Tater pairing code,
stores its permanent device token, and reboots into the Tater screen.

Use `./install.sh --profile` to also collect the redacted hardware profile.
Use `./install.sh --no-home` to install only the screen preview without native
boot services, or `./install.sh --demo` to cycle its visual states.
Use `./install.sh --uninstall` to restore the privacy changes and remove Tater
Show and its boot supervisor.

The hardware-verified native build identifies capture as card
0/device 22 (four-channel S24_3LE at 16 kHz) and playback as card 0/device 23
(stereo S16 at 48 kHz). Native speaker, microWakeWord, Tater pairing, screen
integration, and reboot supervision have passed on real hardware. Checkers OTA
updates the native daemon and screen APK as a coordinated generation. The boot
supervisor commits only after both new components report healthy and restores
both on a timeout or early failure. Tagged builds must keep the same protected
APK signing key across releases; changing that key requires a USB factory
reinstall.
