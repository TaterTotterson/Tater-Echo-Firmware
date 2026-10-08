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
| `rook` | Echo Spot 1st Generation (2017) | amonet-rook v2.0.0 | Yes | Yes |

Target metadata used by builds and Tater lives in
[`targets/targets.json`](targets/targets.json).

## What it supports

- Independently selectable microWakeWord and openWakeWord engines: MWW-only,
  MWW + OWW agreement, or OWW-only before the optional STT wake check. Tater
  manages built-in/custom model sources, sensitivity, and wake sounds live.
  The built-in Hey Tater profile includes a matched, jointly packaged MWW and
  OWW pair so dual mode cannot accidentally combine different phrases.
  Biscuit, Checkers, and Rook all execute OWW with the optimized ARMv7 ONNX
  Runtime/XNNPACK path. Android/Bionic and Linux/musl receive separately built,
  hash-verified runtime libraries for their matching ABI.
- Wake-audio training uploads, continued conversation, barge-in, intercom,
  timers, announcements, mute, volume, and device controls.
- Sendspin multi-room and stereo-pair playback, including direct discovery by
  compatible servers such as Music Assistant, plus native TTS overlays, audio
  scenes, music ducking, clock correction, and underrun recovery.
- Tater-selectable LED animations on Biscuit. Biscuit includes seven-mic
  direction of arrival and reply direction; Checkers and Rook have complete
  native Tater display experiences and selectable color themes.
- BLE presence advertisements through Tater's room-level presence system.
- Captive-portal Wi-Fi and Tater pairing on all three devices. Checkers can
  also be provisioned over USB during factory installation.
- Verified OTA with rollback. Checkers and Rook stage their daemon and native
  screen together in A/B application slots on writable `/data`.
- Checkers and Rook weather, selected room sensors, notifications, Room Vision,
  timers, live tool status, audio-reactive response visuals, and on-screen
  press-and-hold intercom.

### Sendspin playback

Every supported Echo advertises a Sendspin v1 player over mDNS at
`_sendspin._tcp` and listens on TCP port `8928` at `/sendspin`. It accepts
48 kHz/16-bit PCM stereo from Tater's Sendspin source and the encrypted FLAC
profile used by Music Assistant. The device identity, pairings, volume, delay,
and Tater stereo-pair channel assignment survive a reboot.

Tater assigns `left`, `right`, `mono`, or `stereo` output when it builds a
pair or group; all members receive the same synchronized stream and each Echo
renders its assigned channel through its physical mono speaker. The former
Tater-specific `audio.clock.sync` and `media.session.*` transport is no longer
advertised or accepted. Native single-satellite replies, announcements,
timers, overlays, and foreground/background audio scenes remain available and
duck the Sendspin music plane in hardware.

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

Checkers 2.x runs Tater Linux while retaining TWRP. Start with an unlocked
Echo Show 5 **1st generation** (`checkers`), amonet-checkers 2.0.1 or newer,
and working TWRP ([Checkers unlock guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-5-1st-gen-2019-checkers.4762900/)).
The USB installer installs a pinned LineageOS image for this unit's Wi-Fi and
Bluetooth drivers; LineageOS is not booted.

The installer automatically downloads the tested
[Checkers LineageOS 18.1 v0.7 ZIP](https://github.com/amazon-oss/releases/releases/download/lineage-18.1-checkers-v0.7/lineage-18.1-20260904-UNOFFICIAL-checkers.zip)
(`lineage-18.1-20260904-UNOFFICIAL-checkers.zip`) when needed. It verifies the
pinned SHA-256 before formatting or flashing; `--lineage-zip PATH` uses a local
copy instead.

1. Download and extract
   `tater-echo-checkers-<version>-factory.tar.gz` from a
   [Tater Echo Linux release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases).
2. Boot the intended Show into TWRP, connect only that device using a USB
   **data** cable, and keep external power connected. The installer does not
   migrate credentials from earlier firmware; pair it as a new satellite.
3. From the extracted factory bundle, verify it and run the USB conversion:

   ```bash
   ./install.sh --verify-bundle
   ./install.sh
   ```

This installation reformats userdata and turns the former system partition into
an A/B rootfs store, so it must be done by USB, not OTA. The installer preserves
TWRP and device-specific recovery backups. After conversion, routine updates
use the normal Tater OTA path. A fresh Checkers shows its open
`Tater-Setup-XXXX` hotspot and setup page at `http://192.168.4.1`; `--wifi` and
`--usb-pair` remain available for USB provisioning. Windows commands, checksum verification, and
recovery details are in
[`factory/checkers-linux/README.md`](factory/checkers-linux/README.md).

## Install Rook (Echo Spot, 1st gen)

Rook 2.1 runs Tater Linux while retaining TWRP. Start with an unlocked 2017
Echo Spot **1st generation** (`rook`), amonet-rook v2.0.0, and working TWRP.
The USB installer uses the hash-pinned Rook LineageOS 18.1 v0.3 package only
as the source of that device's vendor drivers; LineageOS is not booted.

1. Download and extract `tater-echo-rook-<version>-factory.tar.gz` from the
   [latest release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/latest).
2. Boot the Spot into TWRP, connect USB while keeping normal power connected,
   and run the read-only verification first:

   ```bash
   ./install.sh --verify-bundle
   ./install.sh --preflight-only
   ```

3. Run `./install.sh` and type `ERASE` only after verifying the displayed
   device and partitions. Keep both cables connected until the first-boot
   checks finish, then join the `Tater-Setup-XXXX` network shown on the Spot.

The installer verifies the exact Rook model and partition map, downloads and
hash-checks the pinned vendor source, saves checksum-verified private recovery
images, preserves TWRP, and refuses destructive writes until confirmation.
Keep `backups/<serial>/` private and safe. Full prerequisites, Windows commands,
reinstall rules, and recovery guidance are in
[`factory/rook-linux/README.md`](factory/rook-linux/README.md).

## Releases and OTA

An annotated `vX.Y.Z` tag creates factory and OTA assets for every supported
target plus `firmware-manifest.json` and `SHA256SUMS`.

| Artifact | Purpose |
|---|---|
| `tater-echo-biscuit-vX.Y.Z-factory.tar.gz` | Complete post-amonet Biscuit install/recovery bundle |
| `tater-echo-biscuit-vX.Y.Z-ota.bin` | Biscuit userspace OTA |
| `tater-echo-checkers-vX.Y.Z-factory.tar.gz` | Checkers USB-to-Tater Linux factory conversion bundle |
| `tater-echo-checkers-vX.Y.Z-ota.tar.gz` | Checkers daemon + native screen A/B application OTA |
| `tater-echo-rook-vX.Y.Z-factory.tar.gz` | Rook USB-to-Tater Linux factory conversion bundle |
| `tater-echo-rook-vX.Y.Z-ota.tar.gz` | Rook daemon + native screen A/B application OTA |
| `tater-echo-<target>-vX.Y.Z-{wake-runtime,onnxruntime}.so` | ABI-matched native wake-runtime sidecars used during OTA bootstrap |
| `firmware-manifest.json` | Target compatibility, filenames, sizes, and SHA-256 hashes |

Tater reads the manifest, selects the asset matching `firmware_target`, and
sends its URL, size, and digest to the satellite. Biscuit switches its inactive
userspace slot and rolls back after repeated fast failures. Checkers and Rook
mirror that transaction for coordinated daemon/renderer application slots;
their full root filesystems remain USB/recovery platform artifacts.

See [`docs/firmware-releases.md`](docs/firmware-releases.md) for the release and
OTA contract.

## Development

Build the pinned compiler and device firmware:

```bash
docker build -t tater-echo-compiler device/compiler/
cd device
./compile.sh
./build_microwakeword_runtime.sh
./test_microwakeword_runtime.sh

# Checkers Linux (requires the pinned TECHO5 checkout and base rootfs)
cd ..
linux/checkers/build_linux.sh --version v2.1.0-dev \
  --base-rootfs /path/to/rootfs-v0.9.26.tar.gz \
  --techo5 /path/to/techo5
```

Run the main regression suites:

```bash
cd device && go test -race ./internal/... ./pkg/...
cd ..
python3 -m unittest factory.biscuit.test_install tools.test_merge_release_manifests tools.test_package_echo_release
python3 -m unittest discover -s linux/checkers -p 'test_*.py'
python3 -m unittest discover -s factory/checkers-linux -p 'test_*.py'
python3 -m unittest discover -s linux/rook -p 'test_*.py'
python3 -m unittest discover -s factory/rook-linux -p 'test_*.py'
python3 -m pytest -q factory/biscuit/test_emos_build.py
```

Release builds also compile the static emOS init, Wi-Fi tools, BusyBox, all
target binaries, the native Checkers and Rook display/camera/wake runtimes, and
both Tater Linux root filesystems in GitHub Actions.

Useful technical references:

- [`docs/tater-native-port.md`](docs/tater-native-port.md) — Tater protocol and runtime layout
- [`docs/biscuit-hardware.md`](docs/biscuit-hardware.md) — measured Biscuit audio hardware
- [`docs/checkers.md`](docs/checkers.md) — Checkers hardware and native integration
- [`linux/rook/README.md`](linux/rook/README.md) — Rook hardware and native integration
- [`docs/led-ring-states.md`](docs/led-ring-states.md) — LED state and animation behavior
- [`docs/listening.md`](docs/listening.md) — bounded on-device private-listening sessions

## Adding another Echo model

Start with the read-only profiler and guided stock-firmware probe in the
[`porting/` hardware-discovery guide](porting/README.md). After the hardware is
understood, add its metadata to `targets/targets.json`, create a target-specific
factory installer, and add hardware-backed boot, partition, audio, control, and
recovery tests. Never assume another Echo shares Biscuit, Checkers, or Rook
partition maps, kernel architecture, microphones, LEDs, controls, or recovery
procedure.

## Credits and license

This project builds on three open-source firmware projects:

- [EchoMuse](https://github.com/wilbowes/EchoMuse) by Wil Bowes supplied the
  Sendspin player, Biscuit hardware work, and emOS boot/init foundation used by
  Tater's Echo firmware. The original copyright and MIT license remain in this
  repository.
- [TECHO5](https://github.com/HuskerMinion/techo5) by HuskerMinion supplied
  the Checkers Linux hardware enablement, boot/rescue and A/B platform work.
  Adapted installer code and its MIT license are included in the factory
  bundle; the platform is pinned and verified before release.
- [TECHO5 Spot](https://github.com/HuskerMinion/techo5-spot) by HuskerMinion
  supplied the Rook Linux hardware enablement and signed platform base. Tater's
  Rook release pins and verifies that base and retains its MIT attribution.

Additional credit goes to R0rt1z2 for amonet-biscuit, Binozo for EchoGo and
GoTinyAlsa, Dragon863 for EchoCLI, microWakeWord, openWakeWord, Microsoft ONNX
Runtime, TensorFlow Lite Micro, Xiph.Org, and all hardware testers and
contributors.

The repository is [MIT licensed](LICENSE); third-party terms are recorded in
[`NOTICE.md`](NOTICE.md). Factory releases include the matching BusyBox source,
license, and build script. This project is independent and is not affiliated
with or endorsed by Amazon.
