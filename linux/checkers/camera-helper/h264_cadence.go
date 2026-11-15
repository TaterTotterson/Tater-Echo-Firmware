package main

import (
	"errors"
	"time"
)

func h264CaptureFPS(value string) (int, error) {
	switch value {
	case "", "30":
		return 30, nil
	case "15":
		return 15, nil
	}
	return 0, errors.New("unsupported capture cadence")
}

// Pace independent sensor frames before Bayer development/encoding. Never
// remove encoded prediction pictures or retime capture PTS to hide overload.
type h264CaptureCadence struct {
	interval time.Duration
	next     time.Time
}

func (c *h264CaptureCadence) accept(at time.Time) bool {
	if c.interval <= 0 {
		return true
	}
	if c.next.IsZero() {
		c.next = at.Add(c.interval)
		return true
	}
	if at.Before(c.next) {
		return false
	}
	// Advance past missed deadlines without a burst of catch-up frames.
	c.next = c.next.Add((at.Sub(c.next)/c.interval + 1) * c.interval)
	return true
}
