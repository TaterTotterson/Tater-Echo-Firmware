//go:build linux

package clock

import (
	"time"

	"golang.org/x/sys/unix"
)

// Step sets CLOCK_REALTIME to serverUnixMs.
//
// clock_settime rather than settimeofday: arm64 is a 64-bit-time-only
// architecture and does not implement the settimeofday syscall at all.
func Step(serverUnixMs int64) error {
	ts := unix.NsecToTimespec(serverUnixMs * int64(time.Millisecond))
	return unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
}
