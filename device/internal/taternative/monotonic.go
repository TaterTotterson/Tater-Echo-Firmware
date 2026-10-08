package taternative

import "time"

var monotonicOrigin = time.Now()

// monotonicMicros schedules native overlay starts. Synchronized music now
// owns its clock through the Sendspin player. time.Since retains Go's
// monotonic component, so wall-clock correction cannot move an active clip.
func monotonicMicros() int64 {
	return time.Since(monotonicOrigin).Microseconds()
}
