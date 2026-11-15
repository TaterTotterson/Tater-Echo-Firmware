package taternative

import (
	"fmt"
	"os"
	"strings"
)

// Schema 2 intentionally cannot be installed by the v4.2.3 schema-1 updater.
// All app, bridge and rootfs artifacts use it. Never publish a schema-1 bridge
// into this platform: users must install the matching full USB bundle once.
const linuxOTASchema = 2
const linuxOTAPlatformFile = "/etc/tater-ota-platform"

func linuxOTAPlatform(target string) string {
	if !isLinuxFirmwareTarget(target) {
		return ""
	}
	return "echo-hardware-v1/" + strings.ToLower(strings.TrimSpace(target))
}

func validateLinuxOTAPolicy(schema int, profile, target string) error {
	if schema != linuxOTASchema || profile == "" || profile != linuxOTAPlatform(target) {
		return fmt.Errorf("incompatible OTA platform; a one-time matching USB upgrade is required (settings are preserved)")
	}
	return nil
}

func installedLinuxOTAPlatform(path, target string) string {
	contents, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(contents)) != linuxOTAPlatform(target) {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

func (i *OTAInstaller) requireOTAPlatform() error {
	path := i.PlatformFile
	if path == "" {
		path = linuxOTAPlatformFile
	}
	return validateLinuxOTAPolicy(linuxOTASchema, installedLinuxOTAPlatform(path, i.Target), i.Target)
}
