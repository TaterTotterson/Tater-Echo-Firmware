# Echo Show 8 1st Gen (`crown`)

`crown` is the supported 2019 first-generation Echo Show 8, model **C7H6N3**.
It uses the shared TECHO5-based Tater Linux platform and publishes its own
factory and application-OTA artifacts. Audio, display, touch, camera, BLE, and
OTA are hardware-tested on physical Crown units.

The second-generation Echo Show 8, model **A8H3N2**, is a different target and
must not use Crown images. The factory installer independently requires TWRP's
`ro.product.device` property to equal `crown` before it starts the platform
installer.

## Upstream foundation

- Unlock and TWRP: [amonet-crown v2.0.1+ guide](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-8-1st-gen-2019-crown.4766687/)
- Linux platform: [TECHO5 getting started](https://github.com/HuskerMinion/techo5/blob/main/docs/getting-started.md)
- Pinned rescue boot: Tater custody baseline `tater-echo-crown-baseline-boot.img`, SHA-256
  `e00bed964d9b2ce31df152d183d2aeb45a7839cc5f7460a93dd97d70715ad1b7`
- Pinned vendor image: [LineageOS 18.1 Crown v0.5](https://github.com/amazon-oss/releases/releases/tag/lineage-18.1-crown-v0.5), SHA-256
  `a01359a5e13dad8ac24c5507e8a747163b992012410b14d2ca9a8711ec4a1491`
- Shared rootfs base: Tater custody baseline `tater-echo-show-baseline-rootfs.tar.gz`

Tater preserves the upstream kernel, boot parameters, vendor modules, camera
helper, partition handling, and A/B rootfs store. It replaces the application
layer and adds Tater's native display, setup, pairing, wake-word, playback, and
application A/B updater.

## Hardware contract used by Tater

| Function | Crown path |
|---|---|
| Display | Linux framebuffer, 1280 × 800 landscape |
| Touch | FocalTech `fts_ts`; raw 800 × 1280 coordinates rotated to landscape |
| Microphones | `hw:0,22`, 16 kHz packed S24, 6 channels; microphones 0–3 and loopback 4–5 |
| Beamforming | Four microphone omni plus four steering directions |
| Playback codec | RT5616 DAC1 route |
| Speaker amplifier | External active-low gate; enabled only after codec/PCM setup |
| Privacy | Amazon gating input plus `gpio-keys`; power-key event maps to mute |
| Camera | TECHO5 camera helper and Tater's on-demand Room Vision service |
| Bluetooth | Direct `/dev/stpbt` ownership, matching the Checkers platform path |

The factory and OTA artifacts remain target-scoped. Crown cannot accept a
Checkers application bundle, even though both use the same build machinery.

## Hardware validation

Complete these in order and retain the installer's private recovery backup:

1. Confirm the label says C7H6N3, unlock, boot TWRP, and verify
   `adb shell getprop ro.product.device` prints `crown`.
2. Run `./install.sh --verify-bundle`, then the factory installer while USB and
   external power remain connected.
3. Confirm the rescue screen is correctly oriented and explicitly says Crown;
   confirm the USB console remains available during the install.
4. Complete the local setup portal and verify Wi-Fi reconnect plus Tater
   pairing survive a reboot.
5. Exercise all four wake directions, capture levels, AEC during playback,
   speaker gain/mute, and the physical privacy controls. Stop if the amplifier
   produces a startup pop or mute is inverted.
6. Test every touch edge/corner, weather, timers, notifications, live tool
   state, and display wake/sleep behavior.
7. Test Room Vision, passive BLE presence, and one active BLE GATT device.
8. Install one Crown application OTA, confirm slot commit after the health
   trial, then exercise rollback with a deliberately rejected test bundle.

Repeat these checks whenever changing Crown's kernel, boot image, audio route,
display/input stack, or OTA path.
