# Biscuit and Radar Tater Linux

Biscuit and Radar use a clean Alpine Linux installation built on the pinned
TECHO5 Dot platform. Fire OS and Android do not run. The unit's original TWRP
image is backed up locally and copied to the Linux slot store for recovery.

Installation is deliberately a reset rather than a migration: boot TWRP, run
the factory installer, then join `Tater-Setup-XXXX` and pair the satellite with
Tater again. Routine firmware updates use application A/B slots on userdata.

The published rootfs contains no Amazon kernel, device tree, Wi-Fi firmware,
or audio coefficients. The installer builds the boot image from that unit's
own TWRP backup, and first boot adopts the required firmware and coefficients
from its retained stock system partition. Radar prefers Fire OS 6 `system_a`
and Biscuit prefers `system_b`; first boot probes both slots and understands
both retained Fire OS directory layouts before declaring firmware unavailable.

`build_linux.sh` cross-builds the daemon and native wake runtimes in the
pinned Alpine ARMv7 environment. `build_rootfs.py` replaces the upstream
application layer while retaining its hardware-proven Linux boot contract.
`setup-ap.sh` enters the MediaTek vendor AP mode through `/dev/wmtWifi` and
serves first-boot pairing on the resulting `ap0`. Early Linux boot shows an
orange ring with a moving white segment. Once the setup hotspot is ready, the
ring changes to Tater's familiar warm-white twinkle until pairing completes.
`tater-net`, `tater-usb-ethernet`, and `wifi-set` own station association and
DHCP using only Tater's private configuration;
they never import an old userspace network. Compatibility links keep the old
`techo5-net` and `techo5-eth` command names working while installed images and
new code use the Tater names. `tater-usb-inventory` is the shared read-only
hardware evidence collector installed on every Tater Linux target. Biscuit's
`tater-usb-ethernet probe` is a bounded enumeration-only test that leaves Wi-Fi
up and restores USB serial without attempting DHCP.
