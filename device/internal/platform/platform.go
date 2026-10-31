// Package platform answers which base operating system the firmware booted on.
//
// Current native firmware runs on Tater Linux. Fire OS detection remains only
// so a Linux-only OTA can fail closed when presented to an old installation.
// Almost nothing in the firmware needs to care — the hardware is reached
// through ALSA, i2c, evdev and sysfs. The controller does care because it must
// never offer a Linux-only OTA to an old installation.
//
// This is a runtime value rather than a capability. The useful question is
// which base the device actually booted.
package platform

import (
	"os"
	"strings"
	"sync"
)

// The answers. Unknown is not a failure: firmware older than this field
// reports nothing at all, and the controller must read that as "not asked"
// rather than as FireOS. Guessing a base is how a device gets sent payloads it
// cannot use, which is the fault this exists to prevent.
const (
	TaterLinux = "tater-linux"
	FireOS     = "fireos"
	Unknown    = "unknown"
)

// Detect resolves the base OS beneath root. Split from Base for testing —
// the whole point is to exercise both platforms from a host that is neither.
//
// Fire OS does not stamp our os-release, so the two markers are mutually
// exclusive in practice.
func Detect(root string) string {
	if b, err := os.ReadFile(root + "/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			id := strings.Trim(strings.TrimPrefix(strings.TrimSpace(line), "ID="), `"`)
			switch id {
			case TaterLinux:
				return TaterLinux
			}
		}
	}
	// Android's property service is retained only as an unmistakable marker for
	// an old installation that must be factory-reset before receiving Linux OTA.
	if _, err := os.Stat(root + "/dev/__properties__"); err == nil {
		return FireOS
	}
	return Unknown
}

// Release returns the Tater Linux root filesystem version from os-release.
// It deliberately returns nothing for any other operating system so callers
// never mistake an Android/vendor version for an installed Tater system.
func Release(root string) string {
	b, err := os.ReadFile(root + "/etc/os-release")
	if err != nil {
		return ""
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[key] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	if values["ID"] != TaterLinux {
		return ""
	}
	return strings.TrimSpace(values["VERSION_ID"])
}

// Linux reports whether the device is running Tater's Alpine image.
func Linux() bool {
	return Base() == TaterLinux
}

var (
	once   sync.Once
	cached string
)

// Base returns the detected base OS, resolved once. It cannot change without a
// reboot, and caching also means the value reported on the first stats tick is
// the value reported on every later one — a field that flapped would be read as
// a device changing platform underneath the controller.
func Base() string {
	once.Do(func() { cached = Detect("") })
	return cached
}
