<p align="center">
  <img src="images/tater-echo-firmware-logo.png" alt="Tater Echo Firmware" width="560"/>
</p>

<p align="center">
  <a href="https://taterassistant.com">
    <img alt="Visit Tater Assistant" src="https://img.shields.io/badge/Tater%20Assistant-Visit%20Website-F28C28?style=for-the-badge&logo=googlechrome&logoColor=white" />
  </a>
  <a href="https://discord.gg/w52namKyXT">
    <img alt="Join the Tater Assistant Discord" src="https://img.shields.io/badge/Discord-Join%20the%20Community-5865F2?style=for-the-badge&logo=discord&logoColor=white" />
  </a>
</p>

<p align="center">
  <a href="https://github.com/TaterTotterson/Tater-Echo-Firmware/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/TaterTotterson/Tater-Echo-Firmware/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="https://github.com/TaterTotterson/Tater-Echo-Firmware/releases"><img alt="Firmware" src="https://img.shields.io/github/v/release/TaterTotterson/Tater-Echo-Firmware?label=firmware" /></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/github/license/TaterTotterson/Tater-Echo-Firmware" /></a>
</p>

Tater Echo Firmware turns supported rooted Amazon Echo hardware into a native
Tater voice satellite. Wake detection, audio capture, playback, LEDs, device
controls, and the Tater satellite protocol all run on the Echo—no Home
Assistant or EchoMuse controller is required.

The repository is arranged for multiple Echo models. Each target has its own
factory path while sharing the Tater-native protocol and release format.

| Target | Device | Unlock base | Status | Factory | OTA |
|---|---|---|---|---:|---:|
| `biscuit` | Echo Dot 2nd Generation (2016) | amonet-biscuit v2.0.0 | Hardware tested | Yes | Yes |
| `checkers` | Echo Show 5 1st Generation (2019) | amonet-checkers v2.0.1+ | Native hardware test | Yes | Yes |

Machine-readable target data lives in [`targets/targets.json`](targets/targets.json).

## Features

- On-device microWakeWord with live wake-word/model changes from Tater.
- Wake audio upload for the trainer and optional second-STT wake verification.
- User-selected wake sounds, including bundled defaults that work offline.
- Tater-controlled LED animations, including a full-ring `Audio Glow` that
  follows real reply audio, confidence-calibrated seven-mic direction-of-arrival,
  reply direction held toward the speaker, and per-microphone echo-canceller
  state for clean beam switches.
- Checkers uses its own auto-calibrated two-mic path: fractional delay-and-sum
  combining, bounded ADC level matching, spatial noise reduction, and a
  one-live-mic fallback without inventing a 360-degree bearing.
- Target-appropriate controls: Biscuit uses its action button for
  push-to-intercom and setup recovery; Checkers has a bottom-center
  press-and-hold intercom control plus a deliberate six-press Volume Down
  recovery gesture.
- Continued conversation/reopen-mic, barge-in, synchronized stereo/group media
  playback, disk-backed streaming, rate-slew/rejoin correction, synchronized
  TTS overlays and audio scenes, local music ducking, volume,
  mute, timers, and announcements.
- Live tool progress keeps the selected tool-call animation active while Tater
  speaks the progress line. Checkers also shows the tool name and message in a
  dedicated working view; Tater continues to suppress that speech when an
  inactive stereo reply target would disturb pair synchronization.
- BLE presence advertisements for Tater's room-level presence system. Biscuit
  scans through its native radio path; Checkers uses the Android BLE stack and
  forwards bounded batches to the same native Tater protocol.
- First-boot Wi-Fi and Tater pairing portal—no browser USB wizard required.
- SHA-256 verified A/B OTA with automatic rollback; Checkers coordinates its
  native daemon and signed screen APK as one recoverable update generation.
- A lightweight Checkers screen APK with live listening, thinking, tool-call,
  reply/DOA, media, timer, mute, volume, and push-to-intercom surfaces.
- Explicit-request Room Vision on Checkers. Tater can capture one fresh still
  from the asking Show—or let another satellite assigned to the same room use
  that Show—to answer object, appearance, outfit, and weather-aware clothing
  questions. Camera access stays on device and loopback; images are held only
  in memory for the requested vision answer.

See [`docs/tater-native-port.md`](docs/tater-native-port.md) for the protocol,
runtime layout, and detailed validation notes.

## Install on an Echo Dot 2

First complete the
[amonet-biscuit v2.0.0 unlock, FireOS 6 flash, and root steps](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-dot-2nd-gen-2016-biscuit.4761416/).
Then:

1. Boot the Echo into TWRP (white LED ring) and connect USB.
2. Download the latest `tater-echo-biscuit-*-factory.tar.gz` archive from the
   [latest release](https://github.com/TaterTotterson/Tater-Echo-Firmware/releases/latest).
   In a terminal, extract the archive, enter the extracted folder, and run the
   installer:

   ```bash
   tar -xzf tater-echo-biscuit-v*-factory.tar.gz
   cd tater-echo-biscuit-v*-factory
   ./install.sh
   ```

3. After reboot, join the `Tater-Setup-XXXX` Wi-Fi network and use the captive
   page to select Wi-Fi and pair the satellite with Tater.

The host only needs Python 3 and `adb`. The script refuses to proceed unless it
sees TWRP and the Biscuit A/B partition layout, asks for an explicit `INSTALL`
confirmation, verifies the release manifest, reads both boot slots, and reads
back every partition write before rebooting.

The archive contains **no Amazon kernel or device trees**. It builds the final
emOS image on your computer from the attached Echo's own stock boot partition.
A private recovery image is stored under `factory-backups/`, and stock remains
in `boot_b`. Never publish that backup; it contains code from your device.

Installer details and recovery options are in
[`factory/biscuit/README.md`](factory/biscuit/README.md).

## Install on an Echo Show 5 (first generation)

The Checkers native test build requires amonet 2.0.1 or newer, working TWRP,
and rooted stock Fire OS 6. It keeps Fire OS and TWRP intact; Tater firmware,
the screen APK, and the reversible Magisk boot supervisor live under `/data`.

After amonet first reaches TWRP, install the certified **Echo Show 5 first
generation (`checkers`, model AEOCH)** Fire OS package before attempting root.
Do not use a `biscuit`, `crown`, or second-generation Show 5 package. The
installer intentionally accepts only the hardware-tested Fire OS 6574.1
(`NS65741/8146`) base; its filename and MD5 are:

```text
update-kindle-checkers-NS65741_user_8146_0013222531716.bin
d17ab1fb8fa374cc3f9b1d813d4094dc
```

Download it from the
[Checkers firmware record on FTVDB](https://ftvdb.com/echo/firmware/com.amazon.checkers.android.os/d17ab1fb8fa374cc3f9b1d813d4094dc-13222531716-fire-os-6574-1-ns65741-8146-2026-09-15/),
verify the checksum, and install it from TWRP without wiping data:

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

The static `boot-root.img` attached to an older root post may not match the
Fire OS kernel left on a particular device. A mismatch produces an Echo-logo
boot loop. If TWRP still starts, the device is recoverable: reinstall the
matching Checkers Fire OS package above. Do not wipe data or flash the same
root image again. Make a stock boot backup before the next root attempt and
use only a rooted boot image derived from that exact Fire OS release.

Once compatible rooted Fire OS is running, extract
`tater-echo-checkers-<version>-factory.tar.gz`, connect USB, and run:

```bash
./install.sh
```

The installer verifies the release, exact Fire OS build, device, root,
recovery, and amonet layout; installs the Tater screen and native service;
disables Amazon OTA/Alexa/telemetry/communications applications; applies a
reversible per-application privacy firewall; systemlessly hides the unused
persistent Amazon speech and Bishop account managers before Android starts;
stops Alexa indoor-location and Amazon Sidewalk daemons; and starts
`Tater-Setup-XXXX`. Before setup begins, the installer resolves and validates
the protected UID set. Magisk restores that firewall during `post-fs-data`,
before Android applications can establish public connections. The initial
package quarantine can take about two minutes, but it runs behind the visible
Tater setup screen and does not delay the captive portal.
Join that network and complete the captive portal. The Show then joins normal
Wi-Fi, pairs with Tater, and returns directly to the Tater screen on reboot. A
display watchdog relaunches the screen after an app or Android-framework
failure, clears a stale Echo boot animation, and permits one guarded reboot if
Fire OS is too damaged to launch any activity without restarting indefinitely.
Add `--profile` if you also want a redacted hardware profile archive.
Details and recovery commands are in
[`factory/checkers/README.md`](factory/checkers/README.md).

### LineageOS research notes (not the release path)

Tater's supported Checkers architecture remains the certified, privacy-hardened
Fire OS base above. The earlier LineageOS 18.1 experiment is retained as
recovery and hardware research, not as a recommended Tater installation.

The currently verified upstream package is
[`lineage-18.1-checkers-v0.7`](https://github.com/amazon-oss/releases/releases/tag/lineage-18.1-checkers-v0.7):

```text
lineage-18.1-20260904-UNOFFICIAL-checkers.zip
SHA-256: 785fa643fd68b2e6f6f02d96a2da58373c6a577b92a27cf6cec69603bb94068e
```

This path is **development-only** for now. It replaces `system`, replaces
`boot`, and requires a data wipe. Keep amonet 2.0.1+, working TWRP, verified
raw backups, and the matching Fire OS recovery package before proceeding. Do
not install Google Apps. The factory installer currently allows only
`./install.sh --no-home` on LineageOS because its persistent native supervisor
is still Fire-OS/Magisk-specific; it now refuses to claim otherwise.

The download, backup, TWRP install, verification, rollback, and current Tater
porting status are kept in
[`docs/checkers-lineage.md`](docs/checkers-lineage.md).

## Releases

An annotated `vX.Y.Z` tag builds two artifacts for each supported model:

| Artifact | Purpose |
|---|---|
| `tater-echo-biscuit-vX.Y.Z-factory.tar.gz` | Complete post-amonet installation and recovery bundle |
| `tater-echo-biscuit-vX.Y.Z-ota.bin` | Device-independent Tater userspace update over Wi-Fi |
| `tater-echo-checkers-vX.Y.Z-factory.tar.gz` | Native Checkers installer, screen, setup AP, and boot supervisor |
| `tater-echo-checkers-vX.Y.Z-screen-preview.apk` | Standalone signed Tater Show APK |
| `tater-echo-checkers-vX.Y.Z-ota.zip` | Coordinated native daemon and signed screen APK update |
| `firmware-manifest.json` | Target, compatibility, file sizes, and SHA-256 hashes |
| `SHA256SUMS` | Human/tool verification of every release artifact |

The same build also publishes the exact BusyBox source and license inside the
factory archive. GitHub's Actions artifact is produced for manual workflow
runs; pushing an annotated version tag additionally creates a GitHub Release.

Example:

```bash
git tag -a v0.1.0 --cleanup=verbatim
git push origin v0.1.0
```

The tag annotation becomes the release notes. See
[`docs/firmware-releases.md`](docs/firmware-releases.md) for the artifact and
OTA contract.

## OTA status

Both targets support Tater-managed OTA. Tater sends an `ota.url` command
containing the target's release URL, SHA-256, and size. Biscuit:

1. downloads to the inactive `server_a`/`server_b` slot;
2. verifies the exact size, SHA-256, and ELF header;
3. atomically switches the active symlink and restarts; and
4. rolls back after three fast startup failures.

Checkers downloads a signed ZIP, verifies the outer artifact and both inner
components, stages the inactive native slot, preserves the installed APK,
installs the matching screen APK, and flips the native slot. Its Magisk
supervisor commits only after the new native daemon reaches Tater and the
same-version APK connects locally. A timeout or early exit restores both old
components.

Tater resolves this repository's `firmware-manifest.json`, selects the OTA
artifact matching the satellite's `firmware_target`, and sends the same OTA
command. Checkers releases require the stable APK signing secrets documented
in [`docs/checkers.md`](docs/checkers.md); changing that key requires a one-time
USB factory reinstall.

## Development

Build the device firmware with the pinned Android compiler image:

```bash
docker build -t echomuse-compiler device/compiler/
cd device
./compile.sh                                      # Biscuit (default)
TATER_FIRMWARE_TARGET=checkers ./compile.sh       # Checkers development binary
./build_microwakeword_runtime.sh
./test_microwakeword_runtime.sh
```

Run the Go and installer regression tests:

```bash
cd device && go test -race ./internal/actionbutton ./internal/taternative ./internal/wakeword/microwakeword ./internal/beamformer ./internal/server
cd .. && python3 -m unittest factory.biscuit.test_install factory.checkers.test_install tools.test_merge_release_manifests
./screen/gradlew -p screen :app:assembleDebug
```

Release builds run in GitHub Actions because emOS also needs pinned static ARM
builds of `init32`, `wpa_supplicant`, and BusyBox.

## Adding another Echo model

Add target metadata to [`targets/targets.json`](targets/targets.json), a
target-specific installer under `factory/<codename>/`, its release build job,
and hardware-backed partition/boot tests. Do not assume another Echo shares
Biscuit's partition map, kernel architecture, LED controller, microphone
topology, or recovery procedure.

## Upstream and license

This project is derived from [EchoMuse](https://github.com/wilbowes/EchoMuse)
and preserves its Git history. EchoMuse supplied the mature Biscuit hardware
support, emOS boot tooling, and recovery-safe A/B userspace deployment that the
Tater-native implementation builds on. The `upstream` Git remote remains the
original project so relevant hardware fixes can be reviewed and merged.

Credit also goes to R0rt1z2 for amonet-biscuit, Binozo for EchoGo and
GoTinyAlsa, Dragon863 for EchoCLI, the microWakeWord and TensorFlow Lite
Micro projects, Xiph.Org for SpeexDSP, and every EchoMuse contributor and
hardware tester.

The repository is [MIT licensed](LICENSE), with third-party terms documented
in [NOTICE.md](NOTICE.md). BusyBox is GPL-2.0; each factory release includes
the exact corresponding source, license, and build script. Amazon, Echo, Echo
Dot, Alexa, and FireOS are trademarks of Amazon.com, Inc. or its affiliates.
This project is independent and is not affiliated with or endorsed by Amazon.
