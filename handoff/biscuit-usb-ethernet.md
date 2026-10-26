# Biscuit USB Ethernet (AX88772D) — investigation spec

> **2026-10-25 update: the investigation concluded successfully.** The adapter
> turned out to be a Realtek RTL8152 (not ASIX; see §2c), host mode works via
> `/proc/mchange` (§2a), and the full chain — host mode → enumeration →
> cdc_ether bind → `eth0` (after patch 0052 removes the upstream cdc_ether
> blacklist entry) → **DHCP lease (`192.168.1.9` via `192.168.1.1`, route +
> RX traffic on the wire)** — is **verified on hardware**. The
> developer-facing integration document is
> **[`biscuit-usb-ethernet-handoff.md`](biscuit-usb-ethernet-handoff.md)**;
> this file remains as the analysis/decision history.

Findings from the read-only investigation session of 2025-10-23. Repos examined:
`Tater-Echo-Firmware` @ `7eb003c` (v3.0.4) and `techo5-dot` @ `6e57ed8`
(pinned commit `33b4024` present, 6 commits back). Everything here is sourced
from code reads unless marked **unverified**; nothing has run on hardware.

> Repo drift note: CLAUDE.md lists `factory/biscuit/` but the installer is
> actually **`factory/dot-linux/`**, shared by biscuit and radar. Also note the
> stale pre-v3 clones on this host (`/home/tater/Tater-Echo-Firmware`,
> `/home/tater/TEF-2.5.5` — `emos/`, old `linux/`) must not be used.

## 1. Kernel / install path

### The full chain (verified, with code pointers)

| Step | What happens |
|---|---|
| Kernel build | `techo5-dot/tools/linux/build-kernel.sh`: Amazon Echo Dot src tarball + `kernel-bt.fragment` + CVE patches → `zImage-dtb` per `--board`. |
| Kernel release | TECHO5 publishes `techo5-dot-kernel-bt.zImage-dtb` in its `dot-v1.0.24` GitHub release. |
| Tater release CI | `.github/workflows/release.yml:162-189` downloads that pinned kernel artifact, sha-pins it (`matrix.kernel_sha256`, lines 82-87), sets `TATER_BISCUIT_KERNEL=/tmp/...zImage-dtb`, runs `tools/package_echo_release.py`. |
| Packaging | `package_echo_release.py:267` copies it verbatim into the factory bundle as `payload/kernel.zImage-dtb` (no kernel build of its own; also lifts `dotimage.py`, `mkimage.py`, `init`, maintenance scripts from the pinned `techo5-dot` checkout, lines 237-288). |
| Factory install | `factory/dot-linux/install.py:284-296` verifies the kernel via `dotimage.kernel_board()` (reads `CONFIG_ARCH_MTK_PROJECT="biscuit"` out of the kernel's embedded IKCONFIG — `techo5-dot/tools/dotimage.py:27`), then `dotimage.build_boot_image()` assembles the boot image from **that unit's own backed-up TWRP recovery image header** + Alpine rescue initramfs + the bundled kernel. |
| Write | Image is dd'd to the **`recovery` partition** (`install.py:321-326`, md5 read-back). `boot_a`/`boot_b` keep stock Fire OS. The kernel the unit runs daily is the one in the recovery partition. |
| Rootfs | `rootfs.tar.gz` unpacked into slot `a` on `/cache` (`install.py:57-74`); boot switches to it. `techo5-net`/`wifi-set` are baked into **both** the rootfs (`build_rootfs.py:484`) and the rescue initramfs (`package_echo_release.py:278-281` → `dotimage.build_boot_image` script list, `dotimage.py:100`). |

### Swap-in points for a custom kernel

1. **Build side (techo5-dot):** `build-kernel.sh` sets `FRAGMENT=$HERE/kernel-bt.fragment`
   (~line 100) and aborts if a fragment option doesn't land. A new
   `kernel-usbnet.fragment`, applied the same way, is the intended mechanism.
   *(Mechanism verified as "one fragment applied"; the config-merge loop below
   line 140 was not traced — abort behavior is per CLAUDE.md, **not fully
   verified**.)*
2. **Release side:** `release.yml:82-83` + `release.yml:187` — sha-pin a new
   kernel artifact there (or build in CI). `TATER_BISCUIT_KERNEL` is the single
   injection point; a local release build can already point it anywhere.
3. **Constraint:** custom kernel must still pass `install.py:285`'s
   `kernel_board() == board` check → must keep **`CONFIG_IKCONFIG`** and
   `CONFIG_ARCH_MTK_PROJECT="biscuit"`. Expected to hold (build starts from the
   unit's own config dump), **unverified** until a test zImage passes
   `python tools/dotimage.py board <file>`.

### OTA never touches the kernel (verified)

- Dot-target OTA artifact contains exactly one file, the daemon
  (`package_echo_release.py:204-206`); `release.yml` enforces
  `tar -tzf ... | grep -qx 'tater-echo'`.
- Device side `device/internal/taternative/ota.go:17-56` installs into the
  inactive application slot on `/data` (symlink flip + rollback). The rootfs
  slot is only replaced by `install.py --repair`. No kernel handling anywhere
  in `slotctl install`.
- **Consequence:** the kernel (and the rescue initramfs) is written once, at
  factory install. Shipping a new kernel to an already-installed unit has **no
  existing code path** — needs a factory re-install, `to_twrp` + factory run,
  or a new "update the recovery partition" path (design decision below).
- `linux.img` in the rescue initramfs embeds `techo5-net`/`wifi-set` too, so
  rescue image and running rootfs can drift unless both are re-packaged.

## 2. Pairing without Wi-Fi

### How pairing works today (verified)

- Storage: `/data/local/etc/tater/native.json` = `Bootstrap{URL, Token,
  DeviceName, Room}` (`taternative/bootstrap.go:11-23`). The **Token starts as
  the 6-digit pairing code** typed into Tater → Satellites → Add Satellite;
  the permanent device token from `hello.ack` lands in
  `/data/local/etc/tater/device_token` (`setup_portal.go:23`).
- Setup-mode trigger: `tater-run` (inittab respawn) sees `native.json` missing
  → `tater-setup-ap` → `exec tater-echo setup-mode` (`build_rootfs.py:315-319`,
  `server.go:97`).
- Hotspot: `setup-ap.sh` creates `ap0` via `printf A > /dev/wmtWifi`, runs
  `tater-setup-ap-base` with `TATER_SETUP_AP_IFACE=ap0 DHCP broadcast`, writes
  SSID to `/run/tater-setup-ssid`, kills `wpa_supplicant` first.
- Portal: `setup-mode` (`server.go:1400-1447`) serves captive portal at
  `192.168.4.1:80`. On Save it *requires* an SSID (validated via
  `wifi.LinuxProvisioningConfig`, `wifi/ssid.go:147`), writes the wpa conf
  **and** native.json, then SIGTERMs PID 1 (`setup_portal.go:196-287`).
- `wifi.Provision()`'s rollback/backup path is used by other flows; the dot
  portal writes the conf file directly.

### Critical observation

The hotspot is local (phone → Dot, no uplink needed), so a wired unit whose
Wi-Fi radio works can pair over the hotspot today — **except** the portal form
requires an SSID and always writes a Wi-Fi config
(`setup_portal.go:124` required; `:253-259`), and `wifi.Provision()` tries to
associate. So a Wi-Fi bypass is still needed.

### Options

| Option | What it is | Effort | Risk |
|---|---|---|---|
| **A. Hotspot pairing + "use wired" toggle** | Keep `Tater-Setup-XXXX` for wired units; add "Skip Wi-Fi, use Ethernet" to the portal that saves native.json and skips the wpa conf (seam exists: `SetupPortalOptions.WiFiConfig`, `:40,82`). `techo5-net` prefers eth0. | Small-medium | Low. Unverified: `wmtup`/`wlan0` coexisting with live eth0 (expected fine — independent). |
| **B. Serve portal over wired** | Setup-mode brings up eth0 (DHCP) and binds the portal there too (`Listen` already an option); announce via mDNS (5353 allowed in) or "find the IP in your router". | Medium | Medium: unauthenticated portal open on LAN during setup; worse IP-discovery UX. |
| **C. Serial-console pairing script** | `wifi-set`-style POSIX script, e.g. `tater-pair "http://…" 123456 "Name" "Room"`, writing native.json, run from the existing root shell console (`techo5-console` on ttyGS0). | Low | Low. Requires physical USB access (good posture); long URL over 115200 baud is painful. Best for a truly Wi-Fi-broken unit. |
| **D. Serial console today, zero code** | `cat > /data/local/etc/tater/native.json <<EOF …` from serial; `wifi-set --forget` to stop Wi-Fi interference. Strict JSON (`bootstrap.go:27-46`). | Zero code (docs) | Typos surface as "daemon rejects empty URL" at boot (`server.go:234-239`). |
| **E. `TATER_NATIVE_URL` env-only launch** | URL-only bootstrap is a *daemon-internal* path (no token, `server.go:234`). **Unverified** whether a token-less unit completes hello without a pairing code. | n/a | Not recommended as designed. |

**Recommendation to discuss:** **A (or A+C).** A is the natural UX; C covers
Wi-Fi-hardware-broken cheaply and reuses the established `wifi-set` pattern.

**Decision (2026-10-26):** pairing stays Wi-Fi-only, always. A unit pairs and
connects over Wi-Fi first; wired USB Ethernet is enabled afterwards from the
Tater UI (`usb_ethernet_enabled` toggle, default off — see
`biscuit-usb-ethernet-handoff.md` §7d). No portal change is needed; options
C/D remain the serial-console recovery path for a Wi-Fi-broken unit.

**Unverified items.** 1. Host-mode gate (plan step 1). 2. `cmode=2 = host` in
the MUSB driver. 3. Custom kernel keeps IKCONFIG / `CONFIG_ARCH_MTK_PROJECT`
(check `python tools/dotimage.py board <file>` before install). 4.
`ax_usb_nic` 3.18 compile-compat (`ethtool_link_ksettings` suspect). 5.
`cmode` writes vs USB-gadget console interchanged mid-test.

## 2a. Host entry: the Amazon source verdict (2026-10-25, code-read)

Read from `Echo_Dot_src-6.5.7.1-20251024` `platform.tar` →
`kernel/mediatek/mt8163/3.18_hl/drivers/misc/mediatek/usb20/` (the authority
the CLAUDE.md plan asked for).

- **`cmode=2` is a no-op for the controller** (`usb20.c`
  `mt_usb_store_cmode`): `CABLE_MODE{CHRG_ONLY=0,NORMAL=1,HOST_ONLY=2}`.
  `HOST_ONLY` is stored and read by nothing outside the IPO/charger logging;
  `usb_check_connect()` → `mt_usb_connect()` runs the **gadget** start; and
  `switch_int_to_host()` only arms the IDDIG EINT (IRQF_TRIGGER_LOW +
  `enable_irq` — the source of the unbalanced-WARN). Fully explains the
  2026-10-24/25 hardware test.
- **The host-entry sequence already exists in Amazon's kernel** behind
  `CONFIG_CMD_MODE_CHANGE` (bool, `usb20/Kconfig:31`, `default n`, no deps;
  running unit has it unset, per the config dump and the WARN observation):
  - Kernel creates `/proc/mchange` (`musb_core.c` `proc_mchange_init`).
  - `echo host > /proc/mchange` sets `force_mode = USB_FORCE_HOST` and
    schedules `musb_id_pin_work`; in `usb20_host.c` that work uses
    `musb_is_host()`, which with `ID_PIN_USE_EX_EINT` (defined in
    `usb20.h`) **overrides the IDDIG GPIO read with force_mode**, then the
    work does: host EP FIFO table (`ep_config_from_table_for_host`),
    session clear → force PHY idle (0x6c/0x6d) → session set → force PHY
    host (`VBUSVALID,AVALID,BVALID,IDDIG=0`) → `musb_start()` →
    `MUSB_HST_MODE()`. **No ID pin grounding needed.**
  - Under `CMD_MODE_CHANGE` the work deliberately does **not** drive VBUS
    (`musb_platform_set_vbus` skipped) — VBUS must come from a powered
    OTG cable (the Y-cable provides it).
  - `echo device > /proc/mchange` reverses (stop host, device mode,
    `mt_usb_connect()` re-opens the gadget → console returns).
  - Booting stays in device mode: `mt_usb_init` sets
    `force_mode = USB_FORCE_DEVICE`; normal gadget/console operation and
    the charger thread are unaffected (`usb_cable_connected()` keeps its
    normal meaning in device force).
- **Therefore plan step 1(c)'s custom host-entry patch is unnecessary**;
  the fix is enabling `CONFIG_CMD_MODE_CHANGE=y` (plus `CONFIG_MII=y` for
  the later `ax_usb_nic.ko`) via the new
  `techo5-dot/tools/linux/kernel-usbnet.fragment`, applied by
  `build-kernel.sh` (now loops over all `kernel-*.fragment` files). Both
  **unverified on device** until the `mchangetest` below passes.
- Device test script: `/data/tater-linux/mchangetest.sh` (source on the
  build host at `~/biscuit-build/mchangetest.sh`): `echo host >
  /proc/mchange`, sleeps 60, dumps `/proc/mchange`, `/sys/bus/usb/devices`,
  `dmesg tail` to `/data/tater-linux/mchangetest.log`, then `echo device` to
  restore the console. Success = `host:1`, DevCtl HOSTMODE set / BDEVICE
  clear, and the AX88772D enumerating as `0b95:1790` under the MUSB root
  hub.
- **VERIFIED ON DEVICE (2026-10-25, boot-time `::once:` run with UCTRONICS
  U6113 attached, no Y-cable — powered through the U6113's own power leg):
  HOST MODE WORKS.** `/etc/inittab` line `::once:/data/tater-linux/
  mchangetest.sh`. Log: `/proc/mchange` = `Active:1 host:1 suspend:0
  force:1`; dmesg `[USB]musb_change_mode_write ... step: 1/2/3` then
  `usb 1-1: new high-speed USB device number 2 using musb-hdrc`;
  `/sys/bus/usb/devices` grew `1-1`, `1-1:2.0`, `1-1:2.1` (no driver bound
  yet — no hub driver line, so `1-1` does not look like a hub; it has two
  interfaces, not yet identified). Revert `echo device` returned
  `host:0 force:2` and the serial console. Plan step 1 is **done**.
  Gotchas that cost two wasted cycles: busybox vi auto-indent broke the
  shebang (`  #!/bin/sh` → exec fails silently) and the inittab line was
  pasted with a stray `8:::` prefix — both fixed via `sed`.
- Distribution note: the kernel goes into the **recovery-partition boot
  image at factory install** — an official release bundle cannot be
  installed to pick up this kernel; it must be repacked with
  `payload/kernel.zImage-dtb` swapped (bundle-manifest.json size/sha256
  updated; `install.py` re-verifies both plus `dotimage.kernel_board()`).

## 3. Everything that assumes `wlan0`

### `linux/dot/techo5-net` (shipped in rootfs **and** rescue initramfs)
- `associcate()` (`wpa_supplicant -i wlan0`, `wpa_cli -i wlan0`), `lease()`
  (`udhcpc -i wlan0`), `restart` (`killall udhcpc wpa_supplicant`,
  `ifconfig wlan0 0.0.0.0`).
- Gate `[ -e /sys/class/net/wlan0 ] || exit 1` alone makes wired-only units
  fail bring-up (runs once from `boot.sh` with no args).
- **Needs:** wired branch (insmod `ax_usb_nic` + `udhcpc -i eth0` + same
  `/run/udhcpc.sh` hook), wired-preferred/Wi-Fi-fallback, wire-aware `restart`.
  DHCP plumbing and hook are interface-agnostic — reuse.
- **Distribution gotcha:** must flow through both `build_rootfs.py --network`
  and `package_echo_release.py`'s `tools/linux/rootfs/.../techo5-net` input
  (the boot-image script list is a literal tuple in the platform repo; new
  script names do not flow in automatically).

### `linux/dot/wifi-set`
- `--forget` kills `udhcpc wpa_supplicant` and zeroes `wlan0` — would kill
  wired DHCP too unless `techo5-net restart` is wire-aware (both call
  `restart`).

### `linux/dot/setup-ap.sh`
- Hotspot-only, kills all wpa_supplicant. **No change needed** for option A;
  maybe a guard not to kill a wired stack if code is shared.

### Platform rootfs (shipped unchanged by Tater)
- `boot.sh`: waits ≤45 s for `wlan0` after `wmtup`, then calls `techo5-net`
  once. Fine if `techo5-net` handles eth0 internally (wired arrives slightly
  late in boot — acceptable; flag if latency matters).
- `techo5-firewall`: AirPlay (5000, 6001-6010) and Spotify (4070) rules bound
  to `-i wlan0` (`techo5-dot/.../techo5-firewall:34-36`) — those services are
  **unreachable over eth0** until broadened. Lives in the platform repo: patch
  via Tater's build pipeline or accept the gap. General inbound
  (6053/5353/22/8928/8181/DHCP/ICMP) is interface-agnostic, so the daemon is
  reachable over eth0 as-is.
- `techo5-bt` derives the Bluetooth address from
  `/sys/class/net/wlan0/address` — orthogonal; just be aware.

### Go daemon
- `device/internal/wifi/wifi.go`: `iface = "wlan0"` is a **const** (line 86)
  used at `wpaCli` (215), `currentIPv4()` (308, also gates the change flow's
  DHCP step), `startTaterLinuxSupplicant` (581). Needs a runtime-resolved
  "active interface". `restartTaterLinuxNetwork()` (588) runs
  `techo5-net restart` on every Wi-Fi change — kills wired unless restart
  becomes interface-selective. `Scan()`/`CurrentSSID()`/`CurrentLinkInfo()`
  need a wired status path (eth0 link/IP, no PHY info).
- `device/internal/taternative/setup_portal.go`: SSID required; save always
  writes the wpa conf (`:253-259`); `WiFiConfig`/`ApplyWiFi` are the seams.
  `wifi.Provision` (`wifi.go:810`) requires SSID + association + IP on
  `wlan0` — needs a wired bypass.
- Tests: `tools/test_build_scripts.py:23-38` runs `sh -n` on techo5-net/wifi-set
  (add new scripts to the list). `wifi_test.go` has indirections for paths/
  `chownFile`/`baseOS` but **none for `iface`** (const) — add a seam for
  wired-path tests. `factory/dot-linux/test_install.py` is only ~70 lines of
  bundle-manifest checks.

## 2c. The adapter is a Realtek, not ASIX (2026-10-25, on-device)

The eth0test boot cycle captured the actual USB descriptors of `1-1`
(UCTRONICS U6113, in host mode, no driver loaded):

```
idVendor=0bda idProduct=8152 bcdDevice=2000 (i.e. 0x0200)
manufacturer=Realtek product="USB 10/100 LAN" speed=480
bNumConfigurations=2, selected config 2, bMaxPower=100mA
1-1:2.0: class=02 sub=06 proto=00  → CDC-ECM control
1-1:2.1: class=0a sub=00 proto=00  → CDC-ECM data
```

So the chip is a **Realtek RTL8152, and the unit already selected its CDC-ECM
configuration** (usbcore's config heuristic prefers class-driver configs over
vendor-specific ones — config 1 is vendor mode). Earlier docs (CLAUDE.md and
the pre-§2a notes) identified the adapter as **AX88772D `0b95:1790`** — that
came from PC-side probing and is wrong for this adapter as the unit sees it;
treat the on-device descriptors as authority. Driver accordingly:

- `CONFIG_USB_USBNET=m` + `CONFIG_USB_NET_CDC_ETHER=m` (+ `CONFIG_MII=m`,
  selected by usbnet) — built from the **already-running `-bt` kernel tree**
  (no reflash at all). Grafted from upstream linux-3.18.19 because Amazon's
  tarball omits `drivers/net/usb/usbnet.c`, `cdc_ether.c` and
  `include/linux/usb/usbnet.h` (the uapi `cdc.h` was already there).
- kconfig gotchas on this tree: the two ASIX usbnet drivers have `default y`
  and `select PHYLIB`, dragging in `drivers/net/phy/` — which Amazon's
  tarball doesn't carry (build dies on its missing Makefile). Everything
  except `USB_USBNET`, `USB_NET_CDCETHER`, `MII` must be `n` in the module
  build config; then `PHYLIB`/`OF_MDIO` deselect cleanly.
- Built: `mii.ko` (6.7 KB), `usbnet.ko` (33 KB), `cdc_ether.ko` (11 KB)
  stripped, vermagic `3.18.19 SMP preempt mod_unload ARMv7 p2v8` — all in
  `~/biscuit-build/handoff/`. Load order: `insmod mii.ko usbnet.ko
  cdc_ether.ko` (dependencies: cdc_ether→usbnet→mii).
- The ASIX `ax_usb_nic.ko` (§2b) would never bind `0bda:8152`; its port
  value stands as the CLAUDE.md `MII`/`ethtool_*` 3.18-compat reference, but
  it is **not needed for the U6113**. If a genuine AX88772D adapter appears,
  it applies `0b95:1790` bcdDevice ≤0x0300 → `ax88772d_info`.
- 10/100 only (100M chip) — fine for a satellite; document the expectation.

Device test: `~/biscuit-build/handoff/ethcdc.sh` (same `::once:` boot pattern,
insmods the three modules, host mode, `udhcpc -i eth0`, capture, revert).

### 2c-1. The bind never happens — silent probe failure (2026-10-25)

With `USB_USBNET`/`CDCETHER`/`MII` **built in** (reflashed kernel with
IKCONFIG-verified `=y`s and `usbcore: registered new interface driver
cdc_ether` in dmesg at 0.59 s), the U6113 cycle still ended `no
cdc-ethernet netdev in 15s`: `usb 1-1` enumerates (~1 s after host entry)
into interfaces `1-1:2.0` (02/06/00) and `1-1:2.1` (0a/00/00), the ID table
must match (generic `USB_INTERFACE_INFO(02,06,00)` wildcard plus explicit
`0bda:8152` entries exist in 3.18's `cdc_ether.c`), **but no probe attempt
or failure ever appears in dmesg**. Reading the sources: all failures in
`usbnet_generic_cdc_bind` are `dev_dbg`, and 3.18 has no dynamic debug — a
failed bind is invisible. Also established the hard way:
3.18's usbnet uses `alloc_etherdev` → the CDC netdev **is named `eth0`**
(not `usb0` — the Kconfig help text was right and the earlier `usb[0-9]`
techo5-eth heuristic is harmless belt-and-braces). Also: `announce.c`
doesn't exist in 3.18.19 (the split happened in later kernels); the announce
code is in Amazon's own `hub.c:2208` behind
`CONFIG_USB_ANNOUNCE_NEW_DEVICES` — enabling the config in the fragment is
enough, grafting announce.c breaks the build (tar failure under `set -e`).

**ROOT CAUSE FOUND AND FIXED, VERIFIED ON DEVICE (2026-10-25 evening):** the
`0bda:8152` entry earlier in cdc_ether's ID table carries **`.driver_info = 0`**
— an *intentional upstream blacklist*: cdc_ether lists the Realtek entries so it
silently *rejects* them in favor of the `r8152` vendor driver, and 3.18's
`usbnet_probe` turns `driver_info == 0` into `info == NULL` → `dev_dbg`-only
"blacklisted" branch → `-ENODEV`. That is why `usb_probe_interface` printed
nothing after the probe entry and none of the 0049 prints ever reached. Fix:
`0052-cdcether-rtl8152-no-blacklist.patch` removes the 0bda:8152 entry so the
generic CDC-ECM whitelist entry claims the device via `cdc_info` (this build
never auto-binds r8152 at boot); `0049` additionally makes the blacklist branch
itself print (`usbnet: blacklisted by <drv>, no driver_info`).

**Verified result (flash #5, kernel `be99d61e…bc81f`):**
`cdc_ether 1-1:2.0: usbnet: CDC Ethernet Device bound` →
`cdc_ether 1-1:2.0 eth0: register 'cdc_ether' at usb-musb-hdrc.0.auto-1,
CDC Ethernet Device, 00:e0:4c:36:0e:9b` → `eth0` UP, first udhcpc DISCOVER
actually transmitted on the wire (TX packets:1, 90 bytes). Full chain proven:
mchange host entry → RTL8152 enumeration → cdc_ether bind → eth0 → udhcpc →
**lease obtained** (`udhcpc: lease of 192.168.1.9 obtained from 192.168.1.1,
lease time 86400`; route + RX traffic on a PoE switch port). Nothing remains
unverified on the bring-up path.

Forensic patches (0049/0050/0051; 0051 trim or revert at handoff):
- `kernel-patches/0049-usbnet-report-probe-failures.patch` — blacklist-reject
  print, `dev_err` on the bind failure path of `usbnet_probe`
  ("usbnet: <desc> bind failed, error N"), "bound" breadcrumb, init_status and
  register_netdev failure prints.
- fragment `CONFIG_USB_ANNOUNCE_NEW_DEVICES=y` — full device/config/interface
  announce lines after every enumeration.
- fragment `CONFIG_USB_RTL8152=m` + patch
  `0050-r8152-3.18-eee-helpers.patch` (local 3.18 copies of the three EEE
  converters) — plan B module; if it comes to that, switch the adapter to
  its vendor configuration first: `echo 1 >
  /sys/bus/usb/devices/1-1/bConfigurationValue`, then
  `insmod /usr/local/sbin/r8152.ko`.

## 2b. The ASIX module on 3.18 (2026-10-25 build status)

`ax_usb_nic.ko` builds out-of-tree against the `-bt` tree (`~/biscuit-build/
asix`, from `ASIXElectronics/asix-usb-nic-linux-driver` @ `main`). The
driver is standalone (no usbnet) and binds `0b95:1790` with bcdDevice range
0x0000-0x0300 to `ax88772d_info` (`ax88179a_772d.c`) — the U6113 chip.
Zero modpost warnings; vermagic `3.18.19 SMP preempt mod_unload ARMv7 p2v8`
(`vermagic.h` uses `UTS_RELEASE_MOD` on both module and kernel sides, so no
`-bt` suffix and no match problem). Stripped size 98 KB.

3.18 ports applied to the vendor driver (each marked `3.18 port:` in the
source):
- `ax_main.c` `strscpy` guard moved to 4.3 (vendor guard at 3.18 was wrong;
  `ax_strscpy` fallback used below it); `txqueue` ⇒ `#define txqueue 0` for
  <5.6 (ndo_tx_timeout has no queue arg, device has one queue);
  `ax_select_queue` gets the pre-4.19 4-argument prototype; `linux/
  highmem.h` include added; `skb_frag_off()` ⇒ `frag->page_offset` <5.4;
  `min_mtu`/`max_mtu` assignment guarded to ≥4.10.
- `ax88179a_772d.c`: local copies of `mmd_eee_cap_to_ethtool_sup_t` /
  `mmd_eee_adv_to_ethtool_adv_t` (don't exist in 3.18) guarded <4.2 —
  TODO(verify): exact first-upstream version if built elsewhere; `udelay
  (10000)` in the flash-write path ⇒ `mdelay(10)` (ARM `__bad_udelay`
  limit).
- PTP: `ENABLE_PTP_FUNC` is 0 in `ax_config.h`; `ax_ptp.h` now guards the
  PTP kernel-header include and `struct ax_ptp_cfg` (Amazon's tree ships no
  `ptp_clock_kernel.h`).
- `drivers/net/mii.c` **grafted from upstream linux-3.18.19** into the
  module objects (Amazon's tree omits the file; `mii_ethtool_gset/sset`,
  `mii_nway_restart`, `generic_mii_ioctl` resolve in-module; Amazon's
  `include/linux/mii.h` is identical to upstream).

Module build: `LOCALVERSION` is irrelevant to vermagic (see above), but the
M= build regenerates `include/generated/utsrelease.h` — after touching it,
run `make prepare` before `make M=... modules` and delete stale `.ko`/
`.mod.o` or the link is skipped.

TODO(verify-on-device): insmod + `/proc/mchange` host → AX88772D probes and
`eth0` appears; udhcpc on eth0 with the U6113's RJ45 on a live LAN; module
reverts cleanly after `echo device` (rmmod). Distribution decision (`.ko` in
rootfs tar vs built-in) still open (see below).

## Open design decisions

a. Pairing: **DECIDED (2026-10-26)** — always Wi-Fi; wired Ethernet is a
   post-pairing UI opt-in (`usb_ethernet_enabled`, default off). The serial
   console (C/D) stays as the recovery path.
b. Wired module: out-of-tree `.ko` in the rootfs tar vs built-in (`=y`) in the zImage.
c. How to distribute a changed kernel (and changed `techo5-net` in the boot
   image) to already-installed units — today no path short of factory re-install.

## TODO(verify-on-device)

- [x] USB host mode on the Biscuit board — done 2026-10-25 via `/proc/mchange`
      (CONFIG_CMD_MODE_CHANGE), no Y-cable needed; U6113 enumerated.
- [x] `cmode=2` semantics — documented no-op for the controller (§2a).
- [x] Test zImage passes kernel_board() — `biscuit` (verified on the VPS).
- [x] `ax_usb_nic.ko` compiles against 3.18 — 0 modpost warnings (§2b lists
      the exact ports). Runtime (insmod, probe, eth0) still unverified.
- [ ] `wlan0` (wmtup) comes up alongside a live eth0.
- [ ] Fragment-apply loop in `build-kernel.sh` aborts on non-landing options (code
      trace, not device, but unverified).