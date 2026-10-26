package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Wired USB Ethernet on the Biscuit: the boot script /usr/local/sbin/techo5-eth
// owns the USB role at boot and opts in when /data/tater-linux/net-mode
// contains "wired". The daemon only mirrors Tater's toggle into that flag and
// applies the role change live. Enabling runs the self-reverting boot script
// (enter host mode, wait for the CDC netdev, DHCP, revert to device mode on
// failure — the serial console always survives); disabling restores device
// mode. Wi-Fi is untouched and remains the control path in both states, so a
// wired unit paired over Wi-Fi keeps its Tater connection while the toggle is
// used.
const usbEthernetWired = "wired"

// Overridden in tests.
var usbEthernetPaths = struct {
	flag, script, mchange string
}{
	flag:    "/data/tater-linux/net-mode",
	script:  "/usr/local/sbin/techo5-eth",
	mchange: "/proc/mchange",
}

// applyUSBEthernetSetting maps Tater's usb_ethernet_enabled toggle onto the
// boot flag and applies the USB role change live. A settings push that omits
// the key never touches the flag, and an unchanged value never re-runs the
// switch, so a re-push on every connect cannot flap the USB role.
func applyUSBEthernetSetting(values, applied map[string]any) error {
	value, ok := values["usb_ethernet_enabled"]
	if !ok {
		return nil
	}
	enabled := nativeBool(value, false)
	applied["usb_ethernet_enabled"] = enabled
	if usbEthernetFlagEnabled() == enabled {
		return nil
	}
	if err := writeUSBEthernetFlag(enabled); err != nil {
		return err
	}
	if enabled {
		startUSBEthernetScript()
	} else {
		restoreUSBDeviceMode()
	}
	return nil
}

// usbEthernetFlagEnabled reports the current on-device flag state. The flag is
// absent by default, which is the off state techo5-eth treats as a no-op.
func usbEthernetFlagEnabled() bool {
	data, err := os.ReadFile(usbEthernetPaths.flag)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == usbEthernetWired
}

// writeUSBEthernetFlag mirrors the toggle into the boot flag. Writing fails
// with an error where /data/tater-linux does not exist, so a toggle offered to
// a device without wired support surfaces that instead of silently doing
// nothing.
func writeUSBEthernetFlag(enabled bool) error {
	if !enabled {
		if err := os.Remove(usbEthernetPaths.flag); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", usbEthernetPaths.flag, err)
		}
		return nil
	}
	dir := filepath.Dir(usbEthernetPaths.flag)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("usb ethernet is not available: %s is missing", dir)
	}
	tmp := usbEthernetPaths.flag + ".tmp"
	if err := os.WriteFile(tmp, []byte(usbEthernetWired+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, usbEthernetPaths.flag); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", usbEthernetPaths.flag, err)
	}
	syscall.Sync()
	return nil
}

// startUSBEthernetScript runs the boot script in the background; it logs to
// /data/tater-linux/eth.log and can take up to a minute to fail and revert.
// A missing script (rootfs older than the daemon) is not fatal: the flag is
// already written and the next boot applies it.
func startUSBEthernetScript() {
	cmd := exec.Command(usbEthernetPaths.script)
	if err := cmd.Start(); err != nil {
		log.Printf("[eth] could not run %s: %v", usbEthernetPaths.script, err)
		return
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("[eth] techo5-eth exited: %v (see /data/tater-linux/eth.log)", err)
		}
	}()
}

// restoreUSBDeviceMode reverts a live host mode so the serial console comes
// back. /proc/mchange reporting force:2 means device mode is already active;
// anything else means host mode was entered and needs the revert. A missing
// file means this is not a MUSB Biscuit — nothing to revert.
func restoreUSBDeviceMode() {
	data, err := os.ReadFile(usbEthernetPaths.mchange)
	if err != nil {
		return
	}
	for _, field := range strings.Fields(string(data)) {
		if !strings.HasPrefix(field, "force:") {
			continue
		}
		force := strings.TrimPrefix(field, "force:")
		if force == "2" {
			return
		}
		if err := os.WriteFile(usbEthernetPaths.mchange, []byte("device\n"), 0o644); err != nil {
			log.Printf("[eth] could not revert USB role: %v", err)
		}
		return
	}
}
