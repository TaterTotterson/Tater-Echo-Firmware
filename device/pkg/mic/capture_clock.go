package mic

import "time"

// CaptureClock follows sample cadence rather than the scheduling of consumers.
// A bounded phase correction follows hardware clock drift (about 345 ppm on
// Biscuit) without assigning the same time to batches delivered in a burst.
// The owner calls Restart before opening a new ALSA producer.
type CaptureClock struct {
	rate       int
	sequence   uint64
	generation uint64
	end        time.Time
	arrival    time.Time
}

func NewCaptureClock(rate int) *CaptureClock { return &CaptureClock{rate: rate} }

func (c *CaptureClock) Restart() {
	c.generation++
	c.end, c.arrival = time.Time{}, time.Time{}
}

func (c *CaptureClock) Stamp(pcm []byte, frames int, arrival time.Time) Capture {
	duration := time.Duration(frames) * time.Second / time.Duration(c.rate)
	if c.generation == 0 {
		c.Restart()
	}
	if !c.arrival.IsZero() && arrival.Sub(c.arrival) > 2*duration {
		c.Restart()
	}
	if c.end.IsZero() {
		c.end = arrival
	} else {
		c.end = c.end.Add(duration)
		// At most 1000 ppm. This tracks crystal drift while keeping scheduler
		// jitter out of the sample timeline and retaining monotonic timestamps.
		limit := duration / 1000
		correction := arrival.Sub(c.end)
		if correction > limit {
			correction = limit
		} else if correction < -limit {
			correction = -limit
		}
		c.end = c.end.Add(correction)
	}
	c.arrival = arrival
	c.sequence++
	return Capture{PCM: pcm, CapturedAt: c.end, Sequence: c.sequence, Generation: c.generation}
}
