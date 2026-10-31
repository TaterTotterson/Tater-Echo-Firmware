package usbethernet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// roleBackend isolates board-specific USB role control from the common
// Ethernet/Wi-Fi policy. An implementation is selectable only after its target
// capability is qualified on real hardware.
type roleBackend interface {
	available(Paths) bool
	restoreDeviceMode(Paths) error
}

type unavailableBackend struct{}

func (unavailableBackend) available(Paths) bool          { return false }
func (unavailableBackend) restoreDeviceMode(Paths) error { return nil }

// hardwareOTGBackend represents a connector whose physical ID signaling owns
// the role transition. Userspace never writes the role node; availability only
// confirms that the qualified kernel path is present.
type hardwareOTGBackend struct{}

func (hardwareOTGBackend) available(paths Paths) bool {
	_, err := os.Stat(paths.RoleMode)
	return err == nil
}

func (hardwareOTGBackend) restoreDeviceMode(Paths) error { return nil }

// usbRoleOTGBackend is the mainline equivalent used by Athena's GPIO-backed
// MTU3 connector. Physical ID/VBUS signaling owns the transition; the status
// path only proves that the qualified kernel role switch is present.
type usbRoleOTGBackend struct{}

func (usbRoleOTGBackend) available(paths Paths) bool {
	roles, err := filepath.Glob(filepath.Join(paths.RoleClass, "*", "role"))
	return err == nil && len(roles) > 0
}

func (usbRoleOTGBackend) restoreDeviceMode(Paths) error { return nil }

type mchangeBackend struct{}

func (mchangeBackend) available(paths Paths) bool {
	_, err := os.Stat(paths.MChange)
	return err == nil
}

func (mchangeBackend) restoreDeviceMode(paths Paths) error {
	data, err := os.ReadFile(paths.MChange)
	if err != nil {
		return err
	}
	for _, field := range strings.Fields(string(data)) {
		if !strings.HasPrefix(field, "force:") {
			continue
		}
		if strings.TrimPrefix(field, "force:") == "2" {
			return nil
		}
		if err := os.WriteFile(paths.MChange, []byte("device\n"), 0o644); err != nil {
			return fmt.Errorf("restore MediaTek USB device mode: %w", err)
		}
		return nil
	}
	return fmt.Errorf("MediaTek USB role state has no force field")
}

func backendFor(capability Capability) roleBackend {
	if !capability.qualified() {
		return unavailableBackend{}
	}
	switch capability.Backend {
	case "mediatek-mchange":
		return mchangeBackend{}
	case "mediatek-musb-hardware-otg":
		return hardwareOTGBackend{}
	case "mediatek-mtu3-gpio-otg":
		return usbRoleOTGBackend{}
	default:
		return unavailableBackend{}
	}
}
