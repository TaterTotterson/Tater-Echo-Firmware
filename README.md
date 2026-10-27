<p align="center">
  <img src="images/tater-echo-firmware-logo.png" alt="Tater Echo Firmware" width="560"/>
</p>

<p align="center">
  <a href="https://taterassistant.com"><img alt="Visit Tater Assistant" src="https://img.shields.io/badge/Tater%20Assistant-Visit%20Website-F28C28?style=for-the-badge&logo=googlechrome&logoColor=white" /></a>
</p>

<p align="center">
  <a href="https://github.com/TaterTotterson/Tater-Echo-Firmware/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/TaterTotterson/Tater-Echo-Firmware/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="https://github.com/TaterTotterson/Tater-Echo-Firmware/releases"><img alt="Firmware" src="https://img.shields.io/github/v/release/TaterTotterson/Tater-Echo-Firmware?label=firmware" /></a>
  <a href="LICENSE"><img alt="License: GNU AGPL v3" src="https://img.shields.io/badge/license-AGPL--3.0-blue.svg" /></a>
</p>

Tater Echo Firmware turns supported, unlocked Amazon Echo hardware into native
Tater voice satellites. Wake detection, audio processing, playback, displays,
controls, Bluetooth, and OTA updates run directly on the Echo. Home Assistant
and a separate Echo controller are not required.

## Supported devices

Only the exact generations and codenames below are supported. Do not use an
installer on a similar-looking Echo from another generation.

| Device | Target | Required unlock | Installed system | Factory | OTA |
|---|---|---|---|---:|---:|
| Echo Dot 2nd Generation (2016), RS03QR | `biscuit` | amonet-biscuit v2.0.0 | Tater Linux | Yes | Yes |
| Echo 2nd Generation (2017), XC56PY | `radar` | amonet-radar v1.0.0 | Tater Linux | Yes | Yes |
| Echo Show 5 1st Generation (2019), H23K37 | `checkers` | amonet-checkers v2.0.1+ | Tater Linux | Yes | Yes |
| Echo Show 8 1st Generation (2019), C7H6N3 | `crown` | amonet-crown v2.0.1+ | Tater Linux | Yes | Yes |
| Echo Show 8 2nd Generation (2021), A8H3N2 | `athena` | saiyaku-athena, Fire OS 7080 | Tater Linux | Yes | Yes |
| Echo Spot 1st Generation (2017), VN94DQ | `rook` | amonet-rook v2.0.0 | Tater Linux | Yes | Yes |

Target metadata used by Tater and the release builds lives in
[`targets/targets.json`](targets/targets.json).

> [!IMPORTANT]
> Biscuit and Radar running firmware older than **v3.0.0** require one USB/TWRP
> factory install to move to Tater Linux. They cannot install v3.0.0 through
> OTA. The factory install is a clean reset and clears the satellite's Tater
> settings; normal OTA updates resume after v3.0.0 is installed.

> [!IMPORTANT]
> Athena's installer validates the display panel and unit-specific speaker
> tuning before writing either partition. Keep the private Fire OS backup it
> creates; it is required for rollback and contains that unit's original
> speaker tuning. See [`docs/athena.md`](docs/athena.md) for the supported
> hardware contract and regression checklist.

## Features

- Sat Chat audio and video calls from satellite to satellite and from Tater on
  a phone to a satellite. Checkers, Crown, Athena, and Rook support audio and video;
  Biscuit and Radar participate in audio calls.
- On-device microWakeWord, openWakeWord, or NanoWakeWord CNN/GRU detection,
  including MWW + OWW and MWW + NWW confirmation modes, built-in or custom
  wake bundles, live sensitivity, and room-environment profiles managed from
  Tater.
- Hardware-appropriate microphone processing, echo cancellation, and
  beamforming: seven microphones on Biscuit and Radar, two on Checkers, and
  four on Crown, Athena, and Rook.
- Continued conversation, playback barge-in, room intercom, announcements,
  wake sounds, mute, volume, and wake-audio training samples.
- Local timers and alarms with spoken `Stop`, physical action-button controls,
  and on-screen Stop controls on Checkers, Crown, Athena, and Rook.
- Sendspin multi-room and stereo-pair playback with synchronized PCM/FLAC,
  native TTS overlays, audio scenes, music ducking, and persistent output
  channel selection.
- Native Checkers, Crown, Athena, and Rook photo dashboards with bundled landscapes or
  user wallpapers, weather and room sensors, music, timers, Awareness
  notifications, configurable themes, and live assistant status.
- Touch access on Checkers, Crown, Athena, and Rook to Sat Chat, integration-backed
  thermostat controls, and Room Vision camera and doorbell feeds. Integration
  HLS and UniFi Protect RTSPS use the pinned FFmpeg decoder, Tater Echo cameras
  use the native MJPEG relay, and all retain automatic JPEG fallback and optional
  push-to-talk audio.
- Tater-controlled Biscuit and Radar LED animations for wake, voice, music,
  volume, mute, timer, setup, connection, and update states.
- Passive BLE advertisement forwarding for room-level presence, plus secure
  active BLE GATT connections for services such as Meshtastic Core with PIN
  pairing, persistent bonds, notifications, and unpairing.
- Captive-portal Wi-Fi and Tater pairing, followed by verified A/B OTA updates
  with automatic rollback.

## Install Biscuit — Echo Dot 2

Start with an Echo Dot 2nd Generation (`biscuit`, model **RS03QR**) that has
completed the [amonet-biscuit v2.0.0 bootloader unlock](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-dot-2nd-gen-2016-biscuit.4761416/)
and has working TWRP. Do not flash or boot Fire OS as part of the Tater
installation. These steps are also the required upgrade path for every Biscuit
running Tater firmware older than v3.0.0.

1. Boot the Echo into TWRP—the ring should be white—and connect USB.
2. Download `tater-echo-biscuit-<version>-factory.tar.gz` from the
   [latest release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/latest).
3. Extract the archive and run its installer:

   ```bash
   tar -xzf tater-echo-biscuit-v*-factory.tar.gz
   cd tater-echo-biscuit-v*-factory
   ./install.sh --verify-bundle
   ./install.sh
   ```

4. After reboot, join `Tater-Setup-XXXX` and complete the setup page.

The computer needs Python 3 and `adb`. This is a clean reset rather than a
migration: the installer verifies the device and bundle, backs up every
boot-critical partition, builds the Linux recovery image from that unit's own
TWRP header, installs an A/B Alpine rootfs on cache, and clears prior satellite
settings. Stock system partitions and a verified TWRP copy are retained. See
the shared [`factory/dot-linux` guide](factory/dot-linux/README.md). Once v3.0.0
or newer is running, reconnect USB and run `python3 to_recovery.py` from an
extracted Biscuit or Radar factory archive to enter Tater Recovery.

## Install Radar — Echo 2

Start with the 2017 full-size Echo 2 (`radar`, model **XC56PY**) after completing
the [amonet-radar v1.0.0 bootloader unlock](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-2nd-gen-2017-radar.4801290/)
and confirming that TWRP works. Do not flash or boot Fire OS as part of the
Tater installation. The device has no external USB socket; keep normal power
connected and use the D+, D-, and GND pads documented in that guide.
These steps are also the required upgrade path for every Radar running Tater
firmware older than v3.0.0.

1. Boot TWRP using the method documented by the amonet-radar unlock guide.
   Radar does not provide a dependable hold-button shortcut for recovery.
2. Download and extract `tater-echo-radar-<version>-factory.tar.gz`.
3. Verify, then install the bundle:

   ```bash
   ./install.sh --verify-bundle
   ./install.sh
   ```

4. After reboot, join `Tater-Setup-XXXX` and complete setup.

The installer uses the pinned Radar-specific Linux kernel, builds the recovery
image from the unit's own TWRP header, verifies every partition backup and
write, and retains the stock system slots as first-boot firmware donors. Keep
the private backup and USB connection available. From Tater Linux, reconnect
USB and run `python3 to_recovery.py` from the extracted factory archive to
enter Tater Recovery. See the shared
[`factory/dot-linux` guide](factory/dot-linux/README.md).

## Install Checkers — Echo Show 5

Start with an unlocked Echo Show 5 **1st Generation** (`checkers`, model
**H23K37**), amonet-checkers 2.0.1 or newer, and working TWRP. The
[Checkers unlock guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-5-1st-gen-2019-checkers.4762900/)
covers those prerequisites.

1. Download and extract `tater-echo-checkers-<version>-factory.tar.gz` from the
   [latest release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/latest).
2. Boot Checkers into TWRP. Connect a USB **data** cable and keep external
   power connected.
3. Verify the bundle, then run the conversion:

   ```bash
   ./install.sh --verify-bundle
   ./install.sh
   ```

4. On first boot, join `Tater-Setup-XXXX` and complete the setup page.

This factory conversion reformats userdata and turns the former system
partition into an A/B Tater Linux store. It preserves TWRP and recovery
backups, downloads only the pinned and checksum-verified LineageOS driver
source, and never boots LineageOS. Routine updates are OTA afterward. See the
[`factory/checkers-linux` guide](factory/checkers-linux/README.md) for Windows,
USB provisioning, checksum, and recovery instructions.

## Install Crown — Echo Show 8 (1st Gen)

Crown support is hardware-tested on model **C7H6N3**, codename `crown`.
Start with that exact model, unlocked with amonet-crown v2.0.1 or newer and
able to boot TWRP. Follow the
[Crown unlock guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-8-1st-gen-2019-crown.4766687/).
Do not use this on the second-generation Echo Show 8 (model A8H3N2).

1. Download and extract `tater-echo-crown-<version>-factory.tar.gz`.
2. Boot Crown into TWRP, connect a USB data cable, and keep external power on.
3. Verify the bundle and run the conversion:

   ```bash
   ./install.sh --verify-bundle
   ./install.sh
   ```

4. Join `Tater-Setup-XXXX` on first boot and complete the setup page.

The installer requires TWRP to report `ro.product.device=crown`, preserves
recovery and backups, and uses checksum-pinned Crown boot and vendor-driver
inputs. It reformats userdata and the former system partition. See the
[`factory/crown-linux` guide](factory/crown-linux/README.md) for exact model,
checksum, provisioning, and recovery requirements.

## Install Athena — Echo Show 8 (2nd Gen)

Athena is the 2021 second-generation Echo Show 8, model **A8H3N2**. It is not
Crown. Begin with Fire OS 7080 / PS7574.1.7080N and complete the
[saiyaku-athena unlock and TWRP procedure](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-8-2nd-gen-2021-athena.4804958/).

1. Download and extract `tater-echo-athena-<version>-factory.tar.gz`.
2. Boot Athena into TWRP, connect USB data, and keep normal power attached.
3. Verify the bundle and perform the write-free hardware preflight:

   ```bash
   ./install.sh --verify-bundle
   ./install.sh --dry-run
   ```

4. Read the reported panel, partition, and TAS5805M tuning results. If all are
   recognized, run `./install.sh` and keep the resulting private backup.
5. After restart, join `Tater-Setup-XXXX` and complete Tater pairing.

The installer uses TECHO5's pinned 64-bit mainline kernel and Athena store
layout while Tater runs as an ARMv7 userspace. It writes only `system` p21 and
`boot` p20, preserves TWRP and the unlock partitions, and injects the speaker
tuning from that unit's own Fire OS boot image. Do not use another unit's
filled boot image, and do not bypass an unknown panel with `--force-panel`
unless you are prepared to recover a dark display. See
[`factory/athena-linux/README.md`](factory/athena-linux/README.md) and
[`docs/athena.md`](docs/athena.md).

## Install Rook — Echo Spot

Start with an unlocked 2017 Echo Spot **1st Generation** (`rook`, model
**VN94DQ**), amonet-rook v2.0.0, and working TWRP.

1. Download and extract `tater-echo-rook-<version>-factory.tar.gz` from the
   [latest release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/latest).
2. Boot Rook into TWRP. Connect USB while keeping normal power connected.
3. Verify the bundle and device without writing anything:

   ```bash
   ./install.sh --verify-bundle
   ./install.sh --preflight-only
   ```

4. Run `./install.sh` and type `ERASE` only after checking the displayed model
   and partition information. Keep both cables connected until first-boot
   checks finish.
5. Join the `Tater-Setup-XXXX` network shown on the Spot and complete setup.

The conversion creates an A/B Tater Linux store, preserves TWRP, and saves
private recovery backups. It uses only the pinned and checksum-verified Rook
LineageOS package as a source for hardware drivers; LineageOS is not booted.
See the [`factory/rook-linux` guide](factory/rook-linux/README.md) for complete
prerequisites, Windows commands, reinstall rules, and recovery steps.

Every factory archive contains `to_recovery.py` and the repair payload. From a
normally booted Tater Linux satellite, connect USB and run
`python3 to_recovery.py` inside the extracted archive. After the satellite
enters Tater Recovery, repair its inactive root slot with
`./install.sh --repair` (`py -3 install.py --repair` on Windows). The repair
preserves Wi-Fi, pairing, room, and satellite settings. Add `--factory-reset`
only when those settings should also be cleared.

All six Echo targets also support a local Tater Recovery gesture. Begin
holding **Volume Up** before powering on or restarting the satellite and keep
it held until the unit restarts into Tater Recovery. The firmware accepts the
gesture only during the first 90 seconds of Linux startup and only after an
uninterrupted five-second hold. This requires Tater Linux to start far enough
to read the button; use the USB helper when it cannot.

TWRP remains available as the lower-level, device-specific fallback. Run
`python3 to_twrp.py` from a normally booted satellite only when Tater Recovery
cannot perform the repair. A unit that cannot start Tater Linux still needs its
amonet/TWRP recovery procedure.

## Updates and release files

After factory installation, use Tater's normal satellite updater. The first
v3 update for Biscuit and Radar is the exception: firmware older than v3.0.0
must use the matching USB/TWRP factory archive and cannot use the OTA archive.
Biscuit and Radar then stage their daemon in A/B application slots; Checkers,
Crown, Athena, and Rook stage the daemon and native display together. Tater can
then stage a verified rootfs in the inactive Linux slot. Failed application or
rootfs health checks roll back to their preceding working slots. Kernel, boot,
rescue, partition-layout, and bootloader-logo updates still use the
factory/recovery archive. The monochrome Tater boot logo is currently applied
only by the guarded Checkers, Crown, and Rook USB factory installers; unsupported or
unknown bootloader layouts retain Amazon's logo.

| Release file | Use |
|---|---|
| `tater-echo-biscuit-vX.Y.Z-factory.tar.gz` | Complete Biscuit installation or recovery |
| `tater-echo-biscuit-vX.Y.Z-ota.tar.gz` | Biscuit application OTA |
| `tater-echo-radar-vX.Y.Z-factory.tar.gz` | Complete Radar installation or recovery |
| `tater-echo-radar-vX.Y.Z-ota.tar.gz` | Radar application OTA |
| `tater-echo-checkers-vX.Y.Z-factory.tar.gz` | Complete Checkers USB conversion |
| `tater-echo-checkers-vX.Y.Z-ota.tar.gz` | Checkers application OTA |
| `tater-echo-crown-vX.Y.Z-factory.tar.gz` | Complete Crown USB conversion |
| `tater-echo-crown-vX.Y.Z-ota.tar.gz` | Crown application OTA |
| `tater-echo-athena-vX.Y.Z-factory.tar.gz` | Complete Athena USB conversion |
| `tater-echo-athena-vX.Y.Z-ota.tar.gz` | Athena application OTA |
| `tater-echo-rook-vX.Y.Z-factory.tar.gz` | Complete Rook USB conversion |
| `tater-echo-rook-vX.Y.Z-ota.tar.gz` | Rook application OTA |
| `tater-echo-TARGET-vX.Y.Z-system-ota.tar.gz` | Target-matched Tater Linux rootfs OTA for any of the six satellites |
| `firmware-manifest.json` | Version, compatibility, filename, size, and SHA-256 metadata used by Tater |

An annotated `vX.Y.Z` tag builds all six targets and publishes the shared
manifest and checksums. See [`docs/firmware-releases.md`](docs/firmware-releases.md)
for the complete release and OTA contract.

## Development

Build and test the native Linux daemon and wake runtime:

```bash
linux/dot/build_linux.sh --version v0.0.0-dev --target biscuit \
  --base-rootfs /path/to/techo5-dot-rootfs.tar.gz \
  --platform /path/to/pinned/techo5-dot
(cd device && ./test_microwakeword_runtime.sh)
(cd device && go test -race ./internal/... ./pkg/...)
```

Tagged release builds compile the appropriate daemon, native screen/camera
components, wake runtime, and complete Tater Linux rootfs in GitHub Actions.
All TECHO5 revisions, downloaded platform images, package versions, and the
multi-architecture build container are controlled by
[`platforms/tater-echo-linux-lock.json`](platforms/tater-echo-linux-lock.json). Release manifests
include that lock's SHA-256, the upstream source revision, and the immutable
builder image digest. The lock also records each builder platform's immutable
manifest digest so mixed ARMv7/AMD64 jobs never have to retarget one local
multi-architecture image reference. Update the lock deliberately when adopting
a new upstream platform; ordinary firmware builds do not follow upstream
`latest` state.

Technical references:

- [`docs/tater-native-port.md`](docs/tater-native-port.md) — native protocol,
  Bluetooth, runtime layout, and OTA behavior
- [`docs/biscuit-hardware.md`](docs/biscuit-hardware.md) — measured Biscuit
  audio hardware
- [`docs/radar-hardware.md`](docs/radar-hardware.md) — live Radar partition,
  audio, privacy, and factory-build measurements
- [`factory/dot-linux/README.md`](factory/dot-linux/README.md) — Biscuit and
  Radar Linux installation and recovery contract
- [`docs/checkers.md`](docs/checkers.md) — Checkers hardware and native Linux
  integration
- [`docs/crown.md`](docs/crown.md) — Crown hardware assumptions and first-unit
  validation checklist
- [`docs/athena.md`](docs/athena.md) — Athena mainline hardware contract and
  bring-up checklist
- [`linux/rook/README.md`](linux/rook/README.md) — Rook hardware and native
  Linux integration
- [`docs/led-ring-states.md`](docs/led-ring-states.md) — Biscuit LED behavior
- [`docs/usb-ethernet-platforms.md`](docs/usb-ethernet-platforms.md) — automatic
  Ethernet policy and per-satellite hardware qualification
- [`docs/ssh-access.md`](docs/ssh-access.md) — per-satellite maintenance access
  over Wi-Fi or Ethernet
- [`porting/README.md`](porting/README.md) — adding another Echo model

## Credits and license

This project uses HuskerMinion's
[TECHO5](https://github.com/HuskerMinion/techo5) platform work for the Echo
devices' Linux kernels and boot/rescue environment, device hardware enablement,
A/B rootfs tooling, and camera support. Retained licenses and exact
source-specific attribution are recorded in [`NOTICE.md`](NOTICE.md).

Supported Echo satellites also rely on R0rt1z2's amonet and saiyaku bootloader
unlock and recovery work for device preparation and installation, documented in the
[Amazon Echo development forum](https://xdaforums.com/f/amazon-echo.6148/).

Tater-owned portions of Tater Echo Firmware are licensed under the
[GNU Affero General Public License v3](LICENSE), matching Tater. Third-party
components retain their original terms, recorded in [`NOTICE.md`](NOTICE.md)
and [`LICENSES`](LICENSES). This project is independent and is not affiliated
with or endorsed by Amazon.
