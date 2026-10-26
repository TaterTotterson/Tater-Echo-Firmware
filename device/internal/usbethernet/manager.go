package usbethernet

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const wiredFlag = "wired"

// Paths are the persistent desired-mode flag, the platform control script,
// the MediaTek USB role control, and the runtime state written by that script.
type Paths struct {
	Flag    string
	Script  string
	MChange string
	State   string
}

// State separates the user's persistent preference from the link that is
// actually carrying traffic. Phase is one of disabled, probing, active, or
// fallback; Interface and Address are populated while probing/active, and
// Reason records a fallback or unsupported cause.
type State struct {
	Phase     string
	Interface string
	Address   string
	Reason    string
}

// Starter launches the platform script without making the settings handler
// wait for USB enumeration or DHCP.
type Starter func(path string, args ...string) error

// Manager owns the small persistent/runtime contract shared by the daemon and
// /usr/local/sbin/tater-usb-ethernet.
type Manager struct {
	Target string
	Paths  Paths
	Start  Starter
}

func New(target string) *Manager {
	return &Manager{
		Target: target,
		Paths: Paths{
			Flag:    "/data/tater-linux/net-mode",
			Script:  "/usr/local/sbin/tater-usb-ethernet",
			MChange: "/proc/mchange",
			State:   "/run/tater-usb-ethernet.state",
		},
		Start: startCommand,
	}
}

func startCommand(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("[eth] %s exited: %v (see /data/tater-linux/eth.log)", filepath.Base(path), err)
		}
	}()
	return nil
}

// Supported is deliberately hardware- and installation-gated. A daemon-only
// OTA must not advertise wired networking when the immutable rootfs or kernel
// is still the old version, and the Biscuit implementation must not leak onto
// Radar or the screen targets.
func (m *Manager) Supported() bool {
	if !strings.EqualFold(strings.TrimSpace(m.Target), "biscuit") {
		return false
	}
	info, err := os.Stat(m.Paths.Script)
	if err != nil || info.Mode()&0o111 == 0 {
		return false
	}
	_, err = os.Stat(m.Paths.MChange)
	return err == nil
}

func (m *Manager) Enabled() bool {
	data, err := os.ReadFile(m.Paths.Flag)
	return err == nil && strings.TrimSpace(string(data)) == wiredFlag
}

func (m *Manager) State() State {
	data, err := os.ReadFile(m.Paths.State)
	if err != nil {
		if m.Enabled() {
			return State{Phase: "pending"}
		}
		return State{Phase: "disabled"}
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return State{Phase: "unknown"}
	}
	state := State{Phase: fields[0]}
	switch state.Phase {
	case "active", "probing":
		if len(fields) > 1 {
			state.Interface = fields[1]
		}
		if len(fields) > 2 {
			state.Address = fields[2]
		}
	case "fallback", "unsupported":
		if len(fields) > 1 {
			state.Reason = fields[1]
		}
	}
	return state
}

func (m *Manager) Diagnostics() map[string]any {
	state := m.State()
	supported := m.Supported()
	diagnostics := map[string]any{
		"supported": supported,
		"enabled":   supported && m.Enabled(),
		"active":    supported && state.Phase == "active",
		"state":     state.Phase,
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

// SetEnabled records the desired behavior and starts the platform controller.
// Repeated settings reconciliation is intentionally idempotent: after a failed
// probe, the user can explicitly toggle off/on to retry without every Tater
// reconnect repeatedly taking the serial port through a new host-mode probe.
func (m *Manager) SetEnabled(enabled bool) error {
	wasEnabled := m.Enabled()
	if enabled {
		if !m.Supported() {
			return fmt.Errorf("USB Ethernet is unavailable on this target or installed platform")
		}
		if wasEnabled {
			return nil
		}
		if err := m.writeFlag(true); err != nil {
			return err
		}
		if err := m.start(); err != nil {
			_ = m.writeFlag(false)
			return fmt.Errorf("start USB Ethernet controller: %w", err)
		}
		return nil
	}

	if err := m.writeFlag(false); err != nil {
		return err
	}
	state := m.State()
	if m.Supported() && (wasEnabled || state.Phase == "active" || state.Phase == "probing") {
		if err := m.start("stop"); err != nil {
			m.restoreDeviceMode()
			return fmt.Errorf("stop USB Ethernet controller: %w", err)
		}
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(m.Target), "biscuit") {
		m.restoreDeviceMode()
	}
	return nil
}

func (m *Manager) start(args ...string) error {
	if m.Start == nil {
		return fmt.Errorf("no command starter configured")
	}
	return m.Start(m.Paths.Script, args...)
}

func (m *Manager) writeFlag(enabled bool) error {
	if !enabled {
		if err := os.Remove(m.Paths.Flag); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", m.Paths.Flag, err)
		}
		return nil
	}
	dir := filepath.Dir(m.Paths.Flag)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("USB Ethernet is unavailable: %s is missing", dir)
	}
	temporary, err := os.CreateTemp(dir, ".net-mode-*")
	if err != nil {
		return fmt.Errorf("create temporary network mode: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.WriteString(wiredFlag + "\n"); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary network mode: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary network mode: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("chmod temporary network mode: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary network mode: %w", err)
	}
	if err := os.Rename(temporaryName, m.Paths.Flag); err != nil {
		return fmt.Errorf("install %s: %w", m.Paths.Flag, err)
	}
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func (m *Manager) restoreDeviceMode() {
	data, err := os.ReadFile(m.Paths.MChange)
	if err != nil {
		return
	}
	for _, field := range strings.Fields(string(data)) {
		if !strings.HasPrefix(field, "force:") {
			continue
		}
		if strings.TrimPrefix(field, "force:") == "2" {
			return
		}
		if err := os.WriteFile(m.Paths.MChange, []byte("device\n"), 0o644); err != nil {
			log.Printf("[eth] could not restore USB device mode: %v", err)
		}
		return
	}
}
