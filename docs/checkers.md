# Echo Show 5 first generation (`checkers`)

Checkers 2.x is a native Tater Linux appliance. A small Alpine userspace runs
the Tater satellite daemon alongside a
native framebuffer/touch UI and camera service. TWRP remains in recovery, and
the former system partition contains two independently bootable rootfs
slots.

## Platform contract

- Device: Amazon Echo Show 5 first generation (2019), codename `checkers`.
- Unlock: amonet-checkers 2.0.1 or newer with working TWRP.
- Kernel/rescue platform: pinned TECHO5 Checkers boot image `v0.7.16`.
- Userspace base: pinned TECHO5 rootfs `v0.9.26`, patched deterministically by
  [`linux/checkers/build_rootfs.py`](../linux/checkers/build_rootfs.py).
- Tater UI: `/usr/local/bin/tater-show`, drawing `/dev/fb0` directly and
  reading the Goodix evdev touchscreen.
- Camera: loopback-only snapshot service using TECHO5's hardware-reviewed
  MediaTek ISP driver.
- Persistent state: `/data/local/etc/tater` and `/data/tater-linux`.
- Recovery: TWRP is not overwritten.

The pinned platform artifacts are content-addressed before use:

```text
TECHO5 rootfs v0.9.26
sha256 a72bbdfd0d26f536552b44aa9258f136e05d5243220c761a22c0f691a943f14e

TECHO5 Checkers boot v0.7.16
sha256 46c19fd23c210714e29eb2bf88c77b540f3290c0cf50b680b22032e7a4771da7
```

## Retained Tater features

The Linux generation keeps the existing satellite protocol and feature set:

- native microphone capture, beam selection, VAD, AEC, barge-in, and
  microWakeWord;
- TTS, media, synchronized group audio, timers, alarms, announcements,
  intercom, mute, and volume;
- BLE presence advertisement forwarding;
- weather, room sensors, notifications, Room Vision, tool status, media and
  timer surfaces on the native UI;
- framebuffer brightness, touchscreen controls, hardware buttons, camera
  snapshots, and privacy shutter state;
- the same permanent Tater device identity and token when converting an
  existing Tater installation with a readable backup.

The Checkers ALSA capture device exposes four packed 24-bit channels at 16 kHz.
The Alpine path uses `arecord` for correctly clocked reads because tinyalsa
2.x's compatibility `pcm_read` wrapper returned repeated data faster than the
DMA clock on this vendor driver. Speaker playback continues through tinyalsa.

The MediaTek Bluetooth character device returns a zero-length read when its
queue is empty. Tater treats that as an idle poll, not EOF; repeatedly reopening
the device power-cycles the MT7668 radio and can also wedge its shared Wi-Fi
transport.

## USB factory conversion

Installing Tater Linux changes the partition layout and cannot be done by OTA.
The installer downloads the tested [Checkers LineageOS 18.1 v0.7 ZIP](https://github.com/amazon-oss/releases/releases/tag/lineage-18.1-checkers-v0.7),
`lineage-18.1-20260904-UNOFFICIAL-checkers.zip` (SHA-256
`785fa643fd68b2e6f6f02d96a2da58373c6a577b92a27cf6cec69603bb94068e`),
if it is not already cached. An offline user can pass a local verified copy
with `--lineage-zip PATH`:

```bash
./install.sh
```

The installer:

1. verifies every bundled file and the connected `checkers` device, then
   downloads or verifies the pinned LineageOS ZIP before destructive writes;
2. saves the device's small partitions from TWRP;
3. installs the supported LineageOS zip without booting Android, solely to
   obtain the matching vendor driver tree;
4. flashes the pinned Linux boot image, creates the two-slot store, and installs
   the Tater rootfs into slot A;
5. shows an open `Tater-Setup-XXXX` network
   and serves its Wi-Fi and Tater pairing form at `http://192.168.4.1`;
6. reboots and leaves slot A on trial until the runtime health window commits
   it, followed by one automatic restart into the now-good slot with read-only
   mounts.

This erases userdata and the former system partition. The installer keeps
TWRP and writes recovery material under `backups/<serial>/`. Keep those backups
with their matching unit. Full operator instructions are in
[`factory/checkers-linux/README.md`](../factory/checkers-linux/README.md).

## OTA and rollback

After conversion, Tater sends the `tater-echo-checkers-vX.Y.Z-ota.tar.gz`
artifact with its expected size and SHA-256 digest. The daemon downloads the
small native application bundle to writable `/data`, stages `tater-echo` and
`tater-show` together in the inactive application slot, and switches one link
atomically. The generation commits after both processes remain up for five
minutes and the daemon is connected to Tater. Three fast exits or a health
timeout switches the shared link back and reboots.

Routine OTA therefore behaves like Biscuit and never remounts the live Linux
rootfs. Camera, wake runtime, libraries, init scripts, kernel, and vendor
drivers remain part of the USB/recovery platform image and change only when a
release explicitly requires a Linux base migration.

During such a platform migration, the root slot is marked good only after the
same five-minute connected health window. Checkers then performs one direct
restart. This is intentional: `/` and `/store` are two views of the same ext4
filesystem, and the vendor kernel can leave a live remount-ro syscall stuck.
The slot state is synced first, and the initramfs restores both mounts as
read-only on the next boot.

## Build and verification

Fetch the pinned rootfs and TECHO5 checkout, then run:

```bash
linux/checkers/build_linux.sh \
  --version v2.0.0-dev \
  --base-rootfs /path/to/rootfs-v0.9.26.tar.gz \
  --techo5 /path/to/techo5
```

The build uses an ARMv7 Alpine container for the CGO daemon, native UI,
microWakeWord runtime, and tinyalsa library; it builds the camera helper from
the exact pinned TECHO5 commit and then creates a deterministic rootfs archive.
The release workflow repeats those steps from clean inputs, verifies ELF
architecture and embedded target/version strings, verifies the factory bundle,
and verifies the daemon/renderer OTA bundle before publication.

Hardware acceptance requires all of the following on a trial slot before it is
committed:

- Wi-Fi and Tater reconnect after a cold power cycle;
- microphone clock tracks wall time without subscriber drops;
- speaker, AEC reference, and local wake word initialize;
- native UI connects over loopback and touch input is present;
- camera `/snapshot` returns a valid JPEG;
- BLE passive scan remains up and reports advertisements without disrupting
  Wi-Fi;
- an OTA installs the opposite application slot and a kernel-level reboot reaches it;
- TWRP remains bootable and the preceding good slot remains selectable.

## Attribution

Low-level Checkers Linux hardware enablement, rescue boot contract, slot
manager, vendor-driver handling, framebuffer research, and camera driver are
derived from [TECHO5](https://github.com/HuskerMinion/techo5) by HuskerMinion
under the MIT License. The generated image and factory bundle carry that
license and a Tater attribution notice.
