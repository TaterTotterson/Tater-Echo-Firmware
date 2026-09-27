<p align="center">
  <img src="images/tater-echo-firmware-logo.png" alt="Tater Echo Firmware" width="560"/>
</p>

<p align="center">
  <a href="https://taterassistant.com"><img alt="Visit Tater Assistant" src="https://img.shields.io/badge/Tater%20Assistant-Visit%20Website-F28C28?style=for-the-badge&logo=googlechrome&logoColor=white" /></a>
  <a href="https://discord.gg/w52namKyXT"><img alt="Join the Tater Assistant Discord" src="https://img.shields.io/badge/Discord-Join%20the%20Community-5865F2?style=for-the-badge&logo=discord&logoColor=white" /></a>
</p>

<p align="center">
  <a href="https://github.com/TaterTotterson/Tater-Echo-Firmware/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/TaterTotterson/Tater-Echo-Firmware/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="https://github.com/TaterTotterson/Tater-Echo-Firmware/releases"><img alt="Firmware" src="https://img.shields.io/github/v/release/TaterTotterson/Tater-Echo-Firmware?label=firmware" /></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/github/license/TaterTotterson/Tater-Echo-Firmware" /></a>
</p>

Tater Echo Firmware turns supported, unlocked Amazon Echo hardware into native
Tater voice satellites. Wake detection, audio processing, playback, LEDs,
controls, presence scanning, and OTA run on the Echo without Home Assistant or
a separate Echo controller.

## Supported hardware

| Target | Device | Required unlock | Factory | OTA |
|---|---|---|---:|---:|
| `biscuit` | Echo Dot 2nd Generation (2016) | amonet-biscuit v2.0.0 | Yes | Yes |
| `checkers` | Echo Show 5 1st Generation (2019) | amonet-checkers v2.0.1+ | Yes | Yes |

Target metadata used by builds and Tater lives in
[`targets/targets.json`](targets/targets.json).

## What it supports

- On-device microWakeWord with live wake-word, model, sensitivity, wake-sound,
  and second-STT verification settings from Tater.
- Wake-audio training uploads, continued conversation, barge-in, intercom,
  timers, announcements, mute, volume, and device controls.
- Synchronized stereo/group media, TTS overlays, audio scenes, music ducking,
  gradual clock correction, and underrun rejoin.
- Tater-selectable LED animations. Biscuit includes seven-mic direction of
  arrival and reply direction; Checkers has a complete visual Tater interface.
- BLE presence advertisements through Tater's room-level presence system.
- Captive-portal Wi-Fi and Tater pairing from a `Tater-Setup-XXXX` hotspot.
- Verified OTA with rollback. Checkers updates the native daemon, screen APK,
  and Magisk support module as one coordinated generation.
- Checkers weather, selected room sensors, notifications, Room Vision, live
  tool status, audio-reactive orb, and on-screen press-and-hold intercom.

## Install Biscuit (Echo Dot 2)

First complete the
[amonet-biscuit v2.0.0 unlock, Fire OS 6 flash, and root procedure](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-dot-2nd-gen-2016-biscuit.4761416/).
Then:

1. Boot the Echo into TWRP (white ring) and connect USB.
2. Download `tater-echo-biscuit-<version>-factory.tar.gz` from the
   [latest release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/latest).
3. Extract it, enter the extracted directory, and run the installer:

   ```bash
   tar -xzf tater-echo-biscuit-v*-factory.tar.gz
   cd tater-echo-biscuit-v*-factory
   ./install.sh
   ```

4. After reboot, join `Tater-Setup-XXXX` and complete the captive portal.

The computer needs Python 3 and `adb`. The installer validates TWRP and the
Biscuit A/B layout, reads both boot slots, builds emOS locally from that Echo's
own stock boot image, verifies every write, preserves stock in `boot_b`, and
saves a private recovery image under `factory-backups/`. Never publish that
backup because it contains code from your device.

Full safety checks and recovery options are in
[`factory/biscuit/README.md`](factory/biscuit/README.md).

## Install Checkers (Echo Show 5, 1st gen)

Checkers keeps a rooted, privacy-hardened Fire OS base. Start with amonet 2.0.1
or newer and working TWRP, then install the tested Echo Show 5 first-generation
Fire OS build before rooting:

```text
File: update-kindle-checkers-NS65741_user_8146_0013222531716.bin
Build: Fire OS 6574.1 (NS65741/8146)
MD5: d17ab1fb8fa374cc3f9b1d813d4094dc
```

Download it from the
[Checkers firmware record on FTVDB](https://ftvdb.com/echo/firmware/com.amazon.checkers.android.os/d17ab1fb8fa374cc3f9b1d813d4094dc-13222531716-fire-os-6574-1-ns65741-8146-2026-09-15/).
Do not use a Biscuit, Crown, or second-generation Show image.

From TWRP, verify and install the package without wiping data:

```bash
# Linux
md5sum update-kindle-checkers-NS65741_user_8146_0013222531716.bin
# macOS
md5 -q update-kindle-checkers-NS65741_user_8146_0013222531716.bin

cp update-kindle-checkers-NS65741_user_8146_0013222531716.bin update.zip
adb push update.zip /sdcard/update.zip
adb shell twrp install /sdcard/update.zip
adb reboot
```

Root only with a boot image made for that exact Fire OS build. An incompatible
static `boot-root.img` can cause an Echo-logo boot loop; if TWRP still works,
reinstalling the matching Fire OS package recovers it.

After rooted Fire OS boots normally:

1. Download and extract
   `tater-echo-checkers-<version>-factory.tar.gz` from the latest release.
2. Connect USB with Android running—not TWRP—and confirm `adb devices` lists
   the Show.
3. Run `./install.sh`, then join the displayed `Tater-Setup-XXXX` hotspot.

The installer verifies the device, exact Fire OS build, root, recovery, and
amonet layout. It installs the screen and native service, disables unused
Amazon applications and daemons, applies the reversible privacy firewall, and
adds the boot/watchdog support needed to keep Tater running. Details, recovery,
uninstall, and profiling commands are in
[`factory/checkers/README.md`](factory/checkers/README.md).

## Releases and OTA

An annotated `vX.Y.Z` tag creates factory and OTA assets for every supported
target plus `firmware-manifest.json` and `SHA256SUMS`.

| Artifact | Purpose |
|---|---|
| `tater-echo-biscuit-vX.Y.Z-factory.tar.gz` | Complete post-amonet Biscuit install/recovery bundle |
| `tater-echo-biscuit-vX.Y.Z-ota.bin` | Biscuit userspace OTA |
| `tater-echo-checkers-vX.Y.Z-factory.tar.gz` | Checkers native service, screen, and support-module installer |
| `tater-echo-checkers-vX.Y.Z-screen-preview.apk` | Signed standalone Tater Show APK |
| `tater-echo-checkers-vX.Y.Z-ota.zip` | Coordinated Checkers daemon, APK, and module OTA |
| `firmware-manifest.json` | Target compatibility, filenames, sizes, and SHA-256 hashes |

Tater reads the manifest, selects the asset matching `firmware_target`, and
sends its URL, size, and digest to the satellite. Biscuit switches its inactive
userspace slot and rolls back after repeated fast failures. Checkers stages the
daemon, APK, and boot module together and restores the previous generation if
health checks fail.

See [`docs/firmware-releases.md`](docs/firmware-releases.md) for the release and
OTA contract.

## Development

Build the pinned compiler and device firmware:

```bash
docker build -t tater-echo-compiler device/compiler/
cd device
./compile.sh
TATER_FIRMWARE_TARGET=checkers ./compile.sh
./build_microwakeword_runtime.sh
./test_microwakeword_runtime.sh
```

Run the main regression suites:

```bash
cd device && go test -race ./internal/... ./pkg/...
cd ..
python3 -m unittest factory.biscuit.test_install factory.checkers.test_install factory.checkers.test_privacy tools.test_checkers_module tools.test_merge_release_manifests
python3 -m pytest -q factory/biscuit/test_emos_build.py
./screen/gradlew -p screen :app:testDebugUnitTest :app:lintDebug :app:assembleDebug
```

Release builds also compile the static emOS init, Wi-Fi tools, BusyBox, both
target binaries, and the signed Checkers APK in GitHub Actions.

Useful technical references:

- [`docs/tater-native-port.md`](docs/tater-native-port.md) — Tater protocol and runtime layout
- [`docs/biscuit-hardware.md`](docs/biscuit-hardware.md) — measured Biscuit audio hardware
- [`docs/checkers.md`](docs/checkers.md) — Checkers hardware and native integration
- [`docs/led-ring-states.md`](docs/led-ring-states.md) — LED state and animation behavior
- [`docs/listening.md`](docs/listening.md) — bounded on-device private-listening sessions

## Adding another Echo model

Add its metadata to `targets/targets.json`, create a target-specific factory
installer, and add hardware-backed boot, partition, audio, control, and recovery
tests. Never assume another Echo shares Biscuit or Checkers partition maps,
kernel architecture, microphones, LEDs, controls, or recovery procedure.

## Credits and license

This project is derived from [EchoMuse](https://github.com/wilbowes/EchoMuse)
and preserves its Git history. EchoMuse provided the original Biscuit hardware
support, emOS boot work, and recovery-safe A/B deployment that the Tater-native
firmware builds on.

Credit also goes to R0rt1z2 for amonet-biscuit, Binozo for EchoGo and
GoTinyAlsa, Dragon863 for EchoCLI, microWakeWord, TensorFlow Lite Micro,
Xiph.Org, and all hardware testers and contributors.

The repository is [MIT licensed](LICENSE); third-party terms are recorded in
[`NOTICE.md`](NOTICE.md). Factory releases include the matching BusyBox source,
license, and build script. This project is independent and is not affiliated
with or endorsed by Amazon.
