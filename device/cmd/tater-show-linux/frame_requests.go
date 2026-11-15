package main

import "time"

// Coalesce animation ticks and newly prepared live pictures into one pending
// draw. Video must not wait for the next menu-animation tick, and requests must
// never accumulate a second display queue behind a slow presentation.
type frameRequests chan struct{}

func newFrameRequests() frameRequests { return make(frameRequests, 1) }

func (r frameRequests) request() {
	select {
	case r <- struct{}{}:
	default:
	}
}

// Bounded timing-only diagnostics; no pixels or per-frame history are retained.
type videoFrameGaps struct {
	last       time.Time
	count      int
	total, max time.Duration
}

func (g *videoFrameGaps) note(at time.Time) {
	if !g.last.IsZero() {
		gap := at.Sub(g.last)
		if gap <= 0 {
			return
		}
		g.count++
		g.total += gap
		if gap > g.max {
			g.max = gap
		}
	}
	g.last = at
}

func (g *videoFrameGaps) resetWindow() {
	g.count, g.total, g.max = 0, 0, 0
}
