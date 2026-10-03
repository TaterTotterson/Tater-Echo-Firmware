package taternative

import "time"

var monotonicOrigin = time.Now()

// monotonicMicros is the clock shared by audio.clock.sync, scheduled media
// commits, and playhead reports. time.Since retains Go's monotonic component,
// so wall-clock correction after boot cannot move an active stereo session.
func monotonicMicros() int64 {
	return time.Since(monotonicOrigin).Microseconds()
}
