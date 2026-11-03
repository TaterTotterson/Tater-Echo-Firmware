# Tater Linux for Echo Show 5 2nd Generation (Cronos)

This factory bundle is only for the Amazon Echo Show 5 2nd Generation (2021),
model C76N8S, codename `cronos`. Do not use it on the first-generation Show 5
(`checkers`) or another Echo model.

## Requirements

- The device must already be unlocked with `amonet-cronos` v2.0.1 and booted
  into TWRP.
- Keep the normal power cable connected and connect the device directly to the
  computer by USB.
- Install `adb`, `fastboot`, and Python 3 on the computer.

The installer downloads and verifies the pinned Cronos LineageOS 18.1 v0.4
image. It is used as the tested vendor-driver baseline; Tater Linux replaces
the Android userspace.

## Install

Extract the factory archive, open a terminal in the extracted folder, and run:

```sh
./install.sh
```

The installer verifies every bundled file, confirms that the connected device
reports `cronos`, backs up the partitions it changes, installs Tater Linux, and
leaves TWRP in the dedicated recovery partition.

By default, reinstalling preserves existing Tater pairing, Wi-Fi, SSH, and
satellite settings. For a completely clean setup, run:

```sh
./install.sh --factory-reset
```

Do not disconnect power or USB while the installer is writing the device.

## Recovery

Hold Volume Up while powering on to enter TWRP. The factory archive also
includes `to_twrp.py` for entering TWRP from a normally booted satellite.

Cronos support is hardware-tested on model C76N8S, including factory install,
display and touch, audio and wake detection, recovery, and automatic USB
Ethernet handoff.
