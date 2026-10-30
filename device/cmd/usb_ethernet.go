package main

import (
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/client"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/usbethernet"
)

var usbEthernet = usbethernet.New(client.FirmwareTarget)

func applyUSBEthernetSetting(values, applied map[string]any) error {
	// USB Ethernet is hardware-detected, not a remotely persisted preference.
	// Silently discard the retired key from older servers or cached settings so
	// it cannot suppress detection or make a full settings update fail.
	if _, ok := values["usb_ethernet_enabled"]; ok {
		delete(applied, "usb_ethernet_enabled")
	}
	return nil
}

func usbEthernetSettings() map[string]any {
	diagnostics := usbEthernet.Diagnostics()
	settings := map[string]any{
		"usb_ethernet_supported": diagnostics["supported"],
		"usb_ethernet_prepared":  diagnostics["prepared"],
		"usb_ethernet_automatic": diagnostics["automatic"],
	}
	if prepared, _ := diagnostics["prepared"].(bool); prepared {
		settings["usb_ethernet_active"] = diagnostics["active"]
		settings["usb_ethernet_wifi_rescue"] = diagnostics["wifi_rescue"]
		settings["usb_ethernet_state"] = diagnostics["state"]
		settings["usb_ethernet_transport"] = diagnostics["transport"]
	}
	return settings
}

func usbEthernetDiagnostics() map[string]any { return usbEthernet.Diagnostics() }
