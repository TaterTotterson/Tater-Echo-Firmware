# Handoff: Biscuit wired USB Ethernet — what mainline Tater needs to build in

**Status: VERIFIED END-TO-END including a DHCP lease** (2026-10-25:
`udhcpc: lease of 192.168.1.9 obtained from 192.168.1.1, lease time 86400`,
default route installed, 66 packets / 11.5 KiB received on the wire — adapter
on a PoE switch port). This document is written for the Tater dev and
covers everything: kernel, rootfs, userspace integration work still outstanding,
and open design decisions. Companion analysis with the full investigation
history: [`biscuit-usb-ethernet.md`](biscuit-usb-ethernet.md).

Everything below was proven on a real unit (G090LF1072841H4G) on 2026-10-25
unless marked UNVERIFIED. Base: Tater-Echo-Firmware @ v3.1.0 +
techo5-dot @ pinned `33b402480ed946dda98bd615a2dfcdeeb4e7a167`.

---

## TL;DR — the five changes

1. **Kernel config** (`techo5-dot/tools/linux/kernel-usbnet.fragment`, new file):
   `CONFIG_CMD_MODE_CHANGE=y` (Amazon's own forced host/device switch),
   `CONFIG_USB_ANNOUNCE_NEW_DEVICES=y`, `CONFIG_MII=y`, `CONFIG_USB_USBNET=y`,
   `CONFIG_USB_NET_CDCETHER=y`, the whole rest of the usbnet family pinned
   off (several `default y` and all would break the build — see Gotchas),
   plus optionally `CONFIG_USB_RTL8152=m` (plan B).
2. **Kernel build graft** (`build-kernel.sh`, ~15 lines): Amazon's GPL tarball
   omits `drivers/net/mii.c`, `drivers/net/usb/{usbnet.c,cdc_ether.c,r8152.c}`,
   `include/linux/usb/usbnet.h` — graft them from upstream `linux-3.18.19`.
3. **Kernel patches** (`tools/linux/kernel-patches/`):
   - **`0052-cdcether-rtl8152-no-blacklist.patch` — REQUIRED.** The one real
     fix (see "The fix" below).
   - `0049-usbnet-report-probe-failures.patch` — optional-but-recommended
     small diagnostics (3.18's usbnet probe path is otherwise silent without
     dynamic debug).
   - `0050-r8152-3.18-eee-helpers.patch` — needed only if RTL8152 is built.
4. **Rootfs script** (`linux/dot/techo5-eth`, already in the repo): boots into
   USB host mode, waits for the CDC netdev, DHCPs, self-reverts on failure.
   Wired in via `build_rootfs.py` `--eth` (already in the repo: `--eth` arg,
   `/usr/local/sbin/techo5-eth` copy, `::once:` inittab line, pass-through in
   `build_linux.sh`).
5. **Userspace integration — PARTIALLY LANDED:** the daemon accepts Tater's
   `usb_ethernet_enabled` toggle (default off) and mirrors it into
   `/data/tater-linux/net-mode`, applying the switch live (see §7d). Still to
   do: `techo5-net`'s `eth0` branch and `device/internal/wifi/wifi.go`
   wired-awareness (see §Userspace).

Distribution decision is still open (§Design decisions).

---

## 1. The hardware, as verified on-device

| Fact | Value | How verified |
|---|---|---|
| Adapter | UCTRONICS U6113 ("USB 10/100 LAN") micro-USB Ethernet dongle | on device |
| USB ID | `0bda:8152`, bcdDevice `0x0200`, high-speed | descriptor dump |
| Chip | **Realtek RTL8152 — NOT the ASIX AX88772D** CLAUDE.md assumed | idVendor/idProduct |
| Active config | **config 2 = CDC-ECM**: I/F 2.0 = `02/06/00` (CDC control), I/F 2.1 = `0a/00/00` (ECM data) | sysfs `bInterface*` |
| Device serial | `00E04C360E9B` = the netdev MAC `00:e0:4c:36:0e:9b` (`0x00e04c` = Realtek OUI) | dmesg + announce |
| Power | U6113 has its own 5 V "Power In"; the Echo's micro-USB VBUS is host output, no conflict | on device |

Two important consequences of config 2 being CDC-ECM by default:
- The **in-kernel `cdc_ether`** (not the ASIX vendor driver, not r8152) is the
  right driver. `ax88179_178a`/`cdc_ether`'s old "do not enable" advice in
  CLAUDE.md applied to a hypothetical AX88772D; this adapter is a Realtek.
- The RTL8152's *config 1* is the Realtek vendor configuration; if you ever
  want `r8152` to bind it, first `echo 1 > /sys/bus/usb/devices/1-1/bConfigurationValue`,
  then load r8152. As shipped here r8152 is only a plan-B module.

## 2. Host mode on MT8163 3.18 (the gate, verified)

- `CONFIG_CMD_MODE_CHANGE=y` enables Amazon's own forced-mode facility, exposed
  as `/proc/mchange` (Kconfig text explicitly says it's for development).
  Kernel functions: `musb_change_mode_show/write` in
  `drivers/usb/musb/musb_core.c`; the force mode overrides the IDDIG read in
  `musb_is_host()` inside `musb_id_pin_work`
  (`drivers/misc/mediatek/usb20/mt8163/usb20_host.c`).
- `echo host > /proc/mchange` runs the full host-entry sequence (host EP FIFO
  table, force PHY idle → session → force PHY host 0x6c/0x6d → `musb_start` →
  `MUSB_HST_MODE`). `echo device > /proc/mchange` returns to gadget mode. The
  controller does **not** drive VBUS under this interface; the U6113's own
  power input provides VBUS, which is why this works cleanly.
- The older `/sys/devices/platform/mt_usb/cmode=2` route is a **no-op** for the
  controller (CABLE_MODE_HOST_ONLY just arms the ID-pin EINT) — do not build on
  it. ID-pin grounding is unnecessary with `/proc/mchange`.
- **The console is the USB gadget on the same micro-USB port.** Entering host
  mode drops ttyGS0; it returns after `echo device`. Any automation must be
  able to switch and revert blind (the rootfs script does this and logs to
  `/data/tater-linux/eth.log` — verified).

## 3. The fix that made it all work (required patch)

**Symptom through four reflashes:** everything up to the driver worked; dmesg
showed `cdc_ether 1-1:2.0: probe by cdc_ether` and then *nothing* — no bind,
no netdev, no error anywhere.

**Cause (upstream table entry, verified against pristine `linux-3.18.19`):**
3.18's `cdc_ether.c` ID table carries

```c
USB_DEVICE_AND_INTERFACE_INFO(REALTEK_VENDOR_ID, 0x8152, USB_CLASS_COMM,
        USB_CDC_SUBCLASS_ETHERNET, USB_CDC_PROTO_NONE),
.driver_info = 0,
```

listed *before* the generic CDC-ECM wildcard — an **intentional upstream
blacklist**: `usbnet_probe` maps `driver_info == 0` → `info == NULL` →
`dev_dbg`-only "blacklisted" branch → `-ENODEV`, silently. Upstream expects the
`r8152` vendor driver to claim RTL8152 devices instead; our builds never load
r8152 at boot. (The same pattern blacklists Huawei/Samsung/RTL8153 entries.)

**Fix `0052-cdcether-rtl8152-no-blacklist.patch`:** delete just the 0bda:8152
entry. The generic wildcard then matches and `cdc_info` drives the interface.
That is the entire functional kernel change; everything else in the patch set
is config, grafts, or diagnostics.

## 4. Kernel recipe (techo5-dot, exact proven steps)

### 4.1 Fragment — `tools/linux/kernel-usbnet.fragment` (new, already written)

Applied automatically after `kernel-bt.fragment` by the fragment loop below.
Full contents (with the reasoning comments) are in the file in this repo; the
non-comment lines are:

```
CONFIG_CMD_MODE_CHANGE=y
CONFIG_USB_ANNOUNCE_NEW_DEVICES=y
CONFIG_MII=y                    # ← grafted source, see below
CONFIG_USB_USBNET=y             # ← grafted source
CONFIG_USB_NET_CDCETHER=y       # ← grafted source
CONFIG_USB_RTL8152=m            # plan-B module (optional but shipped)
# plus "# CONFIG_USB_NET_* is not set" pins for the rest of the usbnet family
# (AX8817X, AX88179_178A, CDC_NCM, NET1080, CDC_SUBSET, ZAURUS, PLUSB,
#  MCS7830, INT51X1, DM9601, RNDIS_HOST, CDC_EEM, SMSC75XX, SMSC95XX,
#  SR9700, SR9800, CX82310_ETH, CDC_MBIM, QMI_WWAN, HUAWEI_CDC_NCM)
```

Notes:
- `CONFIG_USB_NET_CDCETHER` (no underscore) is this tree's symbol name;
  `USB_NET_CDC_ETHER` is the 4.x spelling.
- The pins are essential: the unit's stock config has `USB_USBNET` unset so the
  whole usbnet family is invisible to olddefconfig, and several entries default
  to `y`; most have no sources in Amazon's tarball or drag in `drivers/net/phy`
  (absent) — the build dies. Only `CDCETHER`+`RTL8152` have real sources here.
- Module mechanics: `MODVERSIONS` off, `MODULE_SIG` off. An out-of-tree module
  against this kernel needs vermagic `3.18.19 SMP preempt mod_unload ARMv7
  p2v8` (note: no `-bt` suffix on the module side — `vermagic.h` uses
  `UTS_RELEASE_MOD`).

### 4.2 build-kernel.sh changes (small, patch-style)

Three blocks, all in this repo's copy (see the file for exact lines):
1. Fragment list: append `kernel-usbnet.fragment` if present (2 lines).
2. `fragments_landed()` double-check: since `silentoldconfig` can silently drop
   fragment lines mid-build (e.g. invisible tristates), the check now runs both
   after olddefconfig and after the final build, and validates both `=y`
   lines and `not set` pins (function bodies ~5 lines changed).
3. Graft block (next to the existing Bluetooth graft): extract
   `drivers/net/mii.c`, `drivers/net/usb/usbnet.c`, `drivers/net/usb/cdc_ether.c`,
   `drivers/net/usb/r8152.c`, `include/linux/usb/usbnet.h` from the
   `linux-3.18.19` tarball into `$SRC` (with the existing `cmp`-differs note).
   `announce.c` is NOT grafted — it does not exist in 3.18.19; the announce
   code lives in Amazon's own `hub.c` behind the config flag.

If a fragment line doesn't land in the final config the script aborts — the
two `fragment_landed` calls are the safety net; don't remove them.

### 4.3 Patches (go through the existing `NNNN-*.patch` mechanism)

| Patch | Keep for mainline? | What it does |
|---|---|---|
| `0052-cdcether-rtl8152-no-blacklist.patch` | **YES (required)** | removes the 0bda:8152 silent-reject entry; verified this is what creates `eth0` |
| `0049-usbnet-report-probe-failures.patch` | recommended, optional | 4 small `dev_err`/`dev_info` prints on the silent 3.18 usbnet failure paths (blacklist reject, bind fail, init_status fail, register_netdev fail). Pure diagnostics; drop if unwanted |
| `0050-r8152-3.18-eee-helpers.patch` | only if RTL8152 is built | 3.18 copies of `mmd_eee_cap_to_ethtool_sup_t` / `mmd_eee_adv_to_ethtool_adv_t` / `ethtool_adv_to_mmd_eee_adv_t` (guarded by `KERNEL_VERSION(4,2,0)`) for the grafted upstream `r8152.c` |

(An earlier `0051-usbcore-probe-path-verbose.patch` was used during forensics
in the verified flash and removed from the clean tree — do not re-add it.)

### 4.4 Build + expected result

```bash
WORK=~/biscuit-build bash tools/linux/build-kernel.sh --board biscuit -o build/kernel
```
(needs `bc`, the case-sensitive Linux FS caveat, and the Amazon tarball +
AOSP arm-eabi-4.8 toolchain; downloads are cached/sha-checked — see
`docs/building.md`). The grafted-driver build emits only cosmetic modpost
notes for r8152; **`0050` makes r8152 compile clean on 3.18**.

Verified device evidence (kernel `be99d61e…`, flash #5):

```
usb 1-1: new high-speed USB device number 2 using musb-hdrc
usb 1-1: New USB device found, idVendor=0bda, idProduct=8152
cdc_ether 1-1:2.0: probe by cdc_ether
cdc_ether 1-1:2.0: usbnet: CDC Ethernet Device bound
cdc_ether 1-1:2.0 eth0: register 'cdc_ether' at usb-musb-hdrc.0.auto-1, CDC Ethernet Device, 00:e0:4c:36:0e:9b
```
`eth0` UP; the first udhcpc DISCOVER (90 bytes) was transmitted through the
dongle. `uname -r` on the flashed unit: `3.18.19-bt` (unchanged).

### 4.5 Netdev naming

3.18's usbnet names CDC netdevs via `alloc_etherdev` → **`eth0`**, not `usb0`.
Userspace should accept either (`techo5-eth` matches `eth[0-9]* | usb[0-9]*`).

## 5. Rootfs recipe (already in the Tater repo, verified pieces)

| Piece | Where | Notes |
|---|---|---|
| `linux/dot/techo5-eth` | new script, POSIX `sh`, `set -u` | guarded boot-time job (see below) |
| `build_rootfs.py` | `--eth` arg | copies it to `/usr/local/sbin/techo5-eth`, adds `::once:/usr/local/sbin/techo5-eth` to `/etc/inittab`, plus an existing `::once:` for hosttest-style boot hooks |
| `build_linux.sh` | passes `--eth "$script_dir/techo5-eth"` | no other change |
| `tools/test_build_scripts.py` | `sh -n` list includes techo5-eth | green |

`techo5-eth` behavior (verified end-to-end in flash #5):
1. Exits unless `/proc/mchange` exists and reports `force:2` (i.e. a stock-ish
   boot in device mode) and `/data/tater-linux/net-mode` contains `wired` —
   so **opt-in per unit** via the persistent flag, and a no-op for Wi-Fi or a
   PC-attached unit in gadget mode after the first boot's revert.
2. `echo host > /proc/mchange`, logs; takes a one-instance lock dir in /run.
3. Waits ≤15 s for `eth[0-9]*|usb[0-9]*` in `/sys/class/net`.
4. Writes the standard `udhcpc` hook `/run/udhcpc.sh` (identical in content to
   `techo5-net`'s `lease()` writer), `ifconfig up`, `udhcpc -b -t 8`.
5. Waits ≤45 s for a lease → success logs `eth0 up, <addr>` and *stays* in
   host mode. No lease → dumps diagnostics (`ls /sys/bus/usb/devices`,
   device nodes, `dmesg | tail`), `echo device > /proc/mchange`, exits 1 —
   **the console keeps working** (verified twice: cable swap mid-test and a
   boot without the adapter attached).

Wi-Fi is untouched: `techo5-net` runs as before. **Wired currently does not
win over Wi-Fi automatically** — that's part of the userspace integration
below; today the `net-mode=wired` flag is the operator control.

## 6. Userspace integration still to do (UNVERIFIED designs — build first)

1. **`techo5-net` eth branch** (CLAUDE.md plan item 3): when a `eth0`-style
   netdev with carrier exists, run `udhcpc` on it and skip/stop
   `wpa_supplicant`; Wi-Fi keeps the fallback role. Keep the `restart` path
   used by `wifi-set` working (it must now consider both interfaces).
2. **`device/internal/wifi/wifi.go`** (plan item 4): `iface = "wlan0"` is
   hard-coded (~line 86). Wired-only units must not attempt Wi-Fi reload/scan;
   the daemon should report the *active* interface (eth0 vs wlan0). Keep all
   interface logic in one place.
3. **Setup portal / pairing** (plan item 5): **DECIDED 2026-10-26 — pairing
   stays Wi-Fi-only, always.** A unit pairs and connects over Wi-Fi first;
   wired USB Ethernet is then enabled post-pairing from the Tater UI
   (`usb_ethernet_enabled`, default off). No setup-portal change; the serial
   console remains the recovery path for a Wi-Fi-broken unit
   (`biscuit-usb-ethernet.md` §2 options C/D).
4. **`targets/targets.json` / docs**: add the wired-mode flag
   (`/data/tater-linux/net-mode = "wired"`) and adapter requirement to the
   biscuit target metadata.

## 7. Design decisions still open

- **(a) Built-in vs modules:** current verified build has `MII`/`USBNET`/
  `CDCETHER` **built in** (no `.ko` ordering to get wrong, no rootfs additions
  needed beyond the script). A module build (`USBNET=m`+`CDCETHER=m`+`MII=m`)
  avoids kernel-flash coupling only if the same rootfs+kernel pairing ships
  anyway. Recommendation: **built-in**, matching what was verified.
- **(b) r8152 as plan B:** keep `RTL8152=m`+`0050` in the tree (harmless,
  ships in rootfs as `/usr/local/sbin/r8152.ko` in the handover bundle) or cut
  it to slim the rootfs. The CDC-ECM path is the verified one.
- **(c) How the kernel reaches production:** the factory installer builds the
  boot image from the unit's own TWRP recovery header + rescue initramfs +
  **the bundled kernel** (`payload/kernel.zImage-dtb`), writes it to the
  recovery partition (mmcblk0p12) and boots it. **OTA does not update the
  kernel** (A/B userspace slots only) — so once shipped, the kernel rides with
  factory installs only unless the OTA pipeline learns to update it. This is a
  release-pipeline decision for the Tater dev.
- **(d) Default `net-mode`:** **DECIDED 2026-10-26 — ship unset (default off)
  with a Tater UI toggle.** The daemon accepts `usb_ethernet_enabled`
  (`device/cmd/usb_ethernet.go`): it mirrors the toggle into
  `/data/tater-linux/net-mode`, runs the self-reverting switch script live on
  enable (host mode → netdev wait → DHCP → revert on failure), and reverts to
  device mode on disable — no reboot needed. An unchanged value never re-runs
  the switch; a settings push that omits the key never touches the flag.
  Wi-Fi stays up as the control path throughout. The toggle widget itself
  lives in the Tater server/app; the daemon reports the flag state back on
  connect.

## 8. Gotchas (all hit for real)

- Amazon's GPL tarball **omits files its build never compiled** — graft from
  upstream `linux-3.18.19` and keep the sha256 checks; the graft list is in
  `build-kernel.sh`.
- `MII` is an invisible tristate in this tree; hand-`=y` fragment lines get
  dropped by `silentoldconfig` — the grafted `mii.c` + selected-by-usbnet path
  is why `CONFIG_MII=y` lands.
- Rootfs tar for the installer must be written **gzip-compressed** (a plain
  `tarfile` `w:` produced "tar: invalid magic" on device).
- Don't enable the ASIX Kconfig symbols — they `default y` and `select PHYLIB`
  into a directory Amazon's tarball doesn't have.
- `announce.c` does not exist in 3.18.19 — grafting it breaks the build; the
  announce code is in Amazon's `hub.c` behind `CONFIG_USB_ANNOUNCE_NEW_DEVICES`.
- `/proc/mchange` host entry: the unit reboots its console (ttyGS0) — scripts
  must log to files and self-revert; never make the switch the only path.
- The fake 2010 RTC: timestamps in logs are per-boot; match boot boundaries by
  kernel timestamps, not wall clock.
- Never copy boot layouts/mixer/mic maps between Echo codenames (Biscuit
  shares `linux/dot` with Radar — keep changes target-scoped).

## 9. How to verify the integration (recipe that was used)

1. Build kernel per §4.4; `zcat /proc/config.gz | grep -E
   "USB_.*(USBNET|CDCETHER|RTL8152)|MII|CMD_MODE_CHANGE|ANNOUNCE_NEW"` must
   show the fragment lines landed.
2. Rootfs: `techo5-eth` at `/usr/local/sbin/`, the `::once:` line in
   `/etc/inittab`, `net-mode` flag writable under `/data/tater-linux/`.
3. U6113 powered (5 V into Power In), RJ45 into a router/switch:
   `echo 'wired' > /data/tater-linux/net-mode && sync`, reboot **with the
   adapter attached**.
4. Expected `eth.log`: `entered host mode` → `eth0 appeared` →
   `eth0 up, 192.168.x.x` (lease) — **verified with a router/PoE switch**:
   `udhcpc: lease of 192.168.1.9 obtained from 192.168.1.1`, default route via
   192.168.1.1 installed, RX packets on eth0. Without a router: DISCOVER
   frames counted in `ifconfig eth0` TX, then the clean revert (`no DHCP lease
   in 45s, back to device mode`) — also verified.
5. Then verify the daemon sees the interface (§6.2) and Wi-Fi still works
   when the flag is unset.

## 10. Porting to other Echo targets

Nothing in this work is Biscuit-locked: the daemon toggle, the
`/data/tater-linux/net-mode` flag protocol, and the script's guards are
device-agnostic, and everything Biscuit-specific is called out above. Per
target:

- **Radar** (shares the `linux/dot` rootfs): `techo5-eth` already ships in
  its rootfs as a guarded no-op. Two on-device checks unlock it: does
  `/proc/mchange` exist (same MTK family), and can Radar's kernel tree take
  the §4 fragment (`USB_USBNET` + `USB_NET_CDCETHER` +
  `CONFIG_CMD_MODE_CHANGE`). If both hold, the same U6113 and
  `net-mode=wired` work unchanged. TODO(verify-on-device).
- **Other TECHO5 Linux targets (Athena, Rook, Crown):** the recipe
  transfers: (1) find the SoC's forced host-mode entry point, (2) enable
  `USB_USBNET` + `USB_NET_CDCETHER` in that platform's kernel (grafting
  matching upstream sources as in §4.2 where the vendor tarball omits them),
  (3) port `techo5-eth`'s enter/wait/revert sequence to that entry point,
  keeping the self-revert so the device's console/recovery path always
  survives, (4) reuse the daemon toggle and flag file unchanged. Each is
  kernel work in that platform's pinned repo, like §4.
- **Checkers (Fire OS base):** out of scope for this repo; the concept (USB
  host + CDC-ECM class driver + UI flag) holds, but the integration point
  differs (no inittab / techo5 scripts).

Two notes that hold everywhere: prefer the CDC-ECM **class** driver over a
vendor driver so adapters bind without per-chip work (USB-C targets use USB-C
Ethernet adapters — usually RTL8152/8153 inside, same class); and the
exclusive USB-port role (host **or** gadget console) makes the self-revert
story mandatory on any target, not just this one.