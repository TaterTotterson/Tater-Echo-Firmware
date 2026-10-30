package usbethernet

import (
	"os"
	"strings"
)

// Paths are the installed automatic controller, MediaTek USB role control,
// and runtime state written by the controller.
type Paths struct {
	Script    string
	MChange   string
	RoleMode  string
	RoleClass string
	State     string
}

// State describes the detected link rather than a user preference. Phase is
// one of pending, waiting, probing, active, recovering, rescue, probe-complete,
// fallback, stopped, unsupported, or unavailable.
type State struct {
	Phase     string
	Interface string
	Address   string
	Reason    string
}

// Manager exposes the automatic USB Ethernet controller to status/settings
// reporting. Process supervision belongs to BusyBox init in every rootfs.
type Manager struct {
	Target string
	Paths  Paths
}

func New(target string) *Manager {
	return &Manager{
		Target: target,
		Paths: Paths{
			Script:    "/usr/local/sbin/tater-usb-ethernet",
			MChange:   "/proc/mchange",
			RoleMode:  "/sys/class/dual_role_usb/dual-role-usb20/mode",
			RoleClass: "/sys/class/usb_role",
			State:     "/run/tater-usb-ethernet.state",
		},
	}
}

// Prepared means the common controller is installed for a known satellite.
// It does not imply that unverified role/VBUS hardware is qualified.
func (m *Manager) Prepared() bool {
	capability := CapabilityForTarget(m.Target)
	if capability.Backend == "none" {
		return false
	}
	info, err := os.Stat(m.Paths.Script)
	return err == nil && info.Mode()&0o111 != 0
}

// Supported remains the strict hardware qualification gate. All targets can
// passively detect an already-enumerated adapter, but only qualified software
// role backends or verified hardware-OTG paths may advertise support.
func (m *Manager) Supported() bool {
	if !m.Prepared() {
		return false
	}
	return backendFor(CapabilityForTarget(m.Target)).available(m.Paths)
}

func (m *Manager) State() State {
	data, err := os.ReadFile(m.Paths.State)
	if err != nil {
		if m.Prepared() {
			return State{Phase: "pending"}
		}
		return State{Phase: "unavailable"}
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return State{Phase: "unknown"}
	}
	state := State{Phase: fields[0]}
	switch state.Phase {
	case "active", "probing", "probe-complete":
		if len(fields) > 1 {
			state.Interface = fields[1]
		}
		if len(fields) > 2 {
			state.Address = fields[2]
		}
	case "recovering", "rescue":
		if len(fields) > 1 {
			state.Interface = fields[1]
		}
		if len(fields) > 2 && fields[2] != "-" {
			state.Address = fields[2]
		}
		if len(fields) > 3 {
			state.Reason = fields[3]
		}
	case "fallback", "unsupported", "waiting":
		if len(fields) > 1 {
			state.Reason = fields[1]
		}
	}
	return state
}

func (m *Manager) Diagnostics() map[string]any {
	state := m.State()
	prepared := m.Prepared()
	supported := m.Supported()
	capability := CapabilityForTarget(m.Target)
	diagnostics := map[string]any{
		"target":        capability.Target,
		"backend":       capability.Backend,
		"detection":     capability.Detection,
		"qualification": capability.Status,
		"port":          capability.Port,
		"power":         capability.Power,
		"recovery":      capability.Recovery,
		"prepared":      prepared,
		"automatic":     prepared,
		"supported":     supported,
		"active":        prepared && state.Phase == "active",
		"wifi_rescue":   prepared && state.Phase == "rescue",
		"state":         state.Phase,
	}
	if prepared {
		if state.Phase == "active" || state.Phase == "recovering" {
			diagnostics["transport"] = "ethernet"
		} else {
			diagnostics["transport"] = "wifi"
		}
	}
	if capability.Reason != "" {
		diagnostics["qualification_reason"] = capability.Reason
	}
	if state.Interface != "" {
		diagnostics["interface"] = state.Interface
	}
	if state.Address != "" {
		diagnostics["address"] = state.Address
	}
	if state.Reason != "" {
		diagnostics["reason"] = state.Reason
	}
	return diagnostics
}
