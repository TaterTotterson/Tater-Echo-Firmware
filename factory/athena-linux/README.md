# Tater Linux factory installer for Echo Show 8 Gen 2 (Athena)

This installer is only for the 2021 Echo Show 8 second generation, model
**A8H3N2**, codename `athena`. The unit must already be unlocked with
`saiyaku-athena` from Fire OS 7080 / PS7574.1.7080N and booted into TWRP with
working ADB. It is not compatible with the first-generation `crown` model.

Keep reliable power and USB attached. From the extracted bundle, first run:

```sh
./install.sh --verify-bundle
./install.sh --dry-run
```

The dry run identifies the device, validates its GPT partition names and sizes,
checks the panel, verifies both images, reads the stock boot image, and validates
the unit-specific TAS5805M speaker tuning without writing anything. Review its
output, then install with `./install.sh`; add `--force` only to skip the final
prompt. On Windows use `py -3 install.py` for the same commands.

The installer writes only boot partition 20 and system partition 21. It preserves
TWRP, bootloader, TEE, vendor, and the other protected partitions; makes private,
checksum-verified backups under `backups/<serial>/`; and injects the speaker
tuning extracted from this unit's stock boot image into the pinned mainline boot
image. Keep that backup directory private and never substitute another unit's
filled boot image.

The system image also includes Tater's monochrome early-Linux splash. Athena's
first Amazon wordmark is inside the protected `lk_real` unlock chain, so a brief
Amazon frame can remain immediately after power-on. Tater safely replaces it as
soon as Linux owns the framebuffer. The installer never writes `tee1`, `tee2`,
`lk`, `lk2`, the preloader, or TWRP to change branding.

The tested upstream panel variants are checked automatically. An unknown panel
is refused unless the operator deliberately passes `--force-panel`; a forced
panel may remain dark. Routine Tater OTA updates the application A/B slots and
can stage a verified rootfs in the inactive System slot. Use `./install.sh
--repair` from Tater Recovery when the device cannot boot far enough for OTA.

The Athena kernel, installer, store layout, and hardware enablement are maintained
in [Tater Echo Linux](https://github.com/TaterTotterson/Tater-Echo-Linux), based
on MIT-licensed [TECHO5](https://github.com/HuskerMinion/techo5) platform work.
Tater's Athena integration includes audio, privacy-switch, camera, touch,
display, setup, recovery, and application OTA support.
