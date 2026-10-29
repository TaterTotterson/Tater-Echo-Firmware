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

// StartupRecoveryCommand selects the safe physical recovery route for each
// target. Checkers, Crown, Athena, and Rook retain TWRP in a separate recovery
// partition, so their normal Tater Recovery marker remains independent of it.
//
// Biscuit and Radar run Linux from the recovery partition itself. A current
// factory install keeps a verified handoff TWRP image that restores Linux to
// that partition while TWRP continues running from RAM. Use it when present so
// Volume Up behaves like the other satellites; an older OTA-only installation
// safely falls back to Tater Recovery instead of being stranded in TWRP.
func StartupRecoveryCommand(target string) (Command, error) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "biscuit", "radar":
		return Command{
			Path: "/bin/sh",
			Args: []string{
				"-c",
				"if [ -s /store/techo5/twrp-handoff.img ]; then " +
					"/usr/local/sbin/to-twrp --handoff && exit 0; fi; " +
					"/usr/local/sbin/slotctl rescue && exec /usr/local/bin/tater-reboot-now",
			},
		}, nil
	case "checkers", "crown", "athena", "rook":
		return Command{
			Path: "/bin/sh",
			Args: []string{
				"-c",
				"/usr/local/sbin/slotctl rescue && exec /usr/local/bin/tater-reboot-now",
			},
		}, nil
	default:
		return Command{}, fmt.Errorf("unsupported recovery target %q", target)
	}
}
