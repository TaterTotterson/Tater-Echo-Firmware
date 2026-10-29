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

// StartupTWRPCommand selects the Linux-side Volume Up route used only by
// Biscuit and Radar. The screen satellites enter their retained TWRP directly
// in the boot chain, before Linux starts, so they must not retain the older
// Linux-side gesture that requested Tater Recovery.
//
// Biscuit and Radar run Linux from the recovery partition itself. A current
// factory install keeps a verified handoff TWRP image that restores Linux to
// that partition while TWRP continues running from RAM. Do not fall back to
// Tater Recovery when that image is absent: the physical gesture now has one
// unambiguous meaning on every satellite, while to_recovery.py remains the
// explicit route to Tater Recovery.
func StartupTWRPCommand(target string) (Command, error) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "biscuit", "radar":
		return Command{
			Path: "/usr/local/sbin/to-twrp",
			Args: []string{
				"--handoff",
			},
		}, nil
	default:
		return Command{}, fmt.Errorf("no Linux-side TWRP gesture for target %q", target)
	}
}
