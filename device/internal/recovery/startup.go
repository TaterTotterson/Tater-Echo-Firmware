package recovery

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	StartupGestureWindow = 90 * time.Second
	StartupGestureHold   = 5 * time.Second
)

type Command struct {
	Path string
	Args []string
}

func ParseUptime(contents string) (time.Duration, error) {
	fields := strings.Fields(contents)
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty uptime")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("invalid uptime %q", fields[0])
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func InStartupGestureWindow(path string) (bool, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	uptime, err := ParseUptime(string(contents))
	if err != nil {
		return false, err
	}
	return uptime <= StartupGestureWindow, nil
}

// StartupRecoveryCommands selects the Linux-side held-key fallback. Screen
// devices retain their native TWRP power-on route; a key passed through to
// Linux must also request TWRP rather than redirecting to Tater Recovery.
// Biscuit/Radar's factory-built initramfs handles held Volume Up before a root
// slot loads. Retain this late slot-rescue fallback for older boot images or
// refused early handoffs; application OTA must not assume a new boot image.
// Explicit USB Tater Recovery is independent of this physical-key policy.
func StartupRecoveryCommands(target string) ([]Command, error) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "biscuit", "radar":
		return []Command{
			{Path: "/usr/local/sbin/slotctl", Args: []string{"rescue"}},
			{Path: "/usr/local/bin/tater-reboot-now"},
		}, nil
	case "checkers", "cronos", "crown", "athena", "rook":
		return []Command{{Path: "/usr/local/bin/rebootto", Args: []string{"recovery"}}}, nil
	default:
		return nil, fmt.Errorf("no recovery gesture for target %q", target)
	}
}
