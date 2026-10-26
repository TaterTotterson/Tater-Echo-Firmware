# Handoff folder — Biscuit wired USB Ethernet (Realtek RTL8152 CDC-ECM)

Everything the Tater dev needs to bring wired USB Ethernet into mainline.
Start with **`biscuit-usb-ethernet-handoff.md`** — it is the integration doc
and describes how each file below is used.

Status: full chain verified on a real Echo Dot 2nd Gen on 2026-10-25
(host mode → enumeration → cdc_ether bind → `eth0` registered → udhcpc
DISCOVER on the wire). Only an actual DHCP lease is unverified (needs a
router on the adapter). Full decision history:
`biscuit-usb-ethernet.md`.

## Files

| File | Goes to (techo5-dot repo, pinned `33b4024`) | Notes |
|---|---|---|
| `build-kernel.sh` | `tools/linux/build-kernel.sh` | **modified copy** — fragment loop + `fragments_landed()` double-check + graft block. Diff it against the pristine pinned version before applying; ~15 changed lines. |
| `kernel-usbnet.fragment` | `tools/linux/kernel-usbnet.fragment` | **new** — auto-detected by the modified script (safe no-op if absent). |
| `0049-usbnet-report-probe-failures.patch` | `tools/linux/kernel-patches/` | optional diagnostics; not part of the fix |
| `0050-r8152-3.18-eee-helpers.patch` | `tools/linux/kernel-patches/` | needed only if `CONFIG_USB_RTL8152` is built |
| `0052-cdcether-rtl8152-no-blacklist.patch` | `tools/linux/kernel-patches/` | **REQUIRED — the fix** (removes cdc_ether's silent 0bda:8152 reject entry) |
| `techo5-eth` | Tater repo `linux/dot/techo5-eth` | **already in this repo** (`linux/dot/`); copied here so the folder is self-contained |
| `r8152.ko` | prebuilt convenience only | plan-B module, built against the verified clean kernel (`d7d2ce44…`); don't ship blind — rebuild from your own tree |
| `biscuit-usb-ethernet-handoff.md` | read first | the integration doc |
| `biscuit-usb-ethernet.md` | reference | full investigation/decision history |

## One-line summary

Kernel config fragment + 3 patches + 5 grafted source files →
`CONFIG_USB_NET_CDCETHER=y` claims the 0bda:8152 CDC-ECM adapter in
`/proc/mchange` host mode → netdev `eth0`; rootfs script `techo5-eth`
(often wired via `build_rootfs.py --eth`, already in this repo) drives it at
boot, opt-in via `/data/tater-linux/net-mode = "wired"`, self-reverting so
the serial console always survives.