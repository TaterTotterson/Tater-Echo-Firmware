package main

import (
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/client"
	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/usbethernet"
)

var usbEthernet = usbethernet.New(client.FirmwareTarget)

func applyUSBEthernetSetting(values, applied map[string]any) error {
	value, ok := values["usb_ethernet_enabled"]
	if !ok {
		return nil
	}
	enabled := nativeBool(value, false)
	if err := usbEthernet.SetEnabled(enabled); err != nil {
		return err
	}
	applied["usb_ethernet_enabled"] = usbEthernet.Enabled()
	return nil
}

func usbEthernetSettings() map[string]any {
	diagnostics := usbEthernet.Diagnostics()
	settings := map[string]any{
		"usb_ethernet_supported": diagnostics["supported"],
	}
	if supported, _ := diagnostics["supported"].(bool); supported {
		settings["usb_ethernet_enabled"] = diagnostics["enabled"]
		settings["usb_ethernet_active"] = diagnostics["active"]
		settings["usb_ethernet_state"] = diagnostics["state"]
	}
	return settings
}

func usbEthernetDiagnostics() map[string]any { return usbEthernet.Diagnostics() }
