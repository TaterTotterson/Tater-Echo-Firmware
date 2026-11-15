//go:build !dot && !spot && !panel

package camera

import (
	"log/slog"
	"time"
)

func taterUseHeldCaptureSlots(board string) bool { return board == "cronos" }

// Cronos can miss a one-tick IMGO base handoff and leave that slot containing
// an older frame. Hold each target for two sensor ticks, then publish the
// preceding held target after another handoff. A missed tick extends a hold;
// it never invents additional target changes. At the qualified 30 Hz sensor
// clock this supplies approximately 15 independent, chronological frames/s.
type taterHeldSlots struct {
	current, previous int
	last, elapsed     uint32
}

func newTaterHeldSlots(counter uint32) taterHeldSlots {
	return taterHeldSlots{previous: -1, last: counter}
}

func (h *taterHeldSlots) tick(counter uint32) (next, done int, handoff bool) {
	delta := (counter - h.last) & 255
	h.last = counter
	h.elapsed += delta
	if h.elapsed < 2 {
		return h.current, -1, false
	}
	h.elapsed = 0
	done = h.previous
	h.previous = h.current
	h.current = (h.current + 1) % slots
	return h.current, done, true
}

// Preserve the existing three-slot allocation, ISP setup, clocks and privacy
// teardown. Only the ordinary IMGO base handoff is paced. Copy completed bytes
// before calling auto-exposure/consumers, which may block beyond a sensor tick.
// The callback is synchronous and makes the immutable Frame copy before return.
func (d *device) taterStreamHeldBuffers(stop chan struct{}, frame func([]byte)) {
	cam := d.cam
	owned := make([]byte, frameBytes)
	cam.wr(regImgoBase, d.mva)
	cam.mask(regTgVfCon, 0, 1)
	defer cam.mask(regTgVfCon, 1, 0)
	last := cam.rd(regTgInterSt) >> 16 & 255
	hold := newTaterHeldSlots(last)
	quiet := time.Now()
	for {
		select {
		case <-stop:
			return
		default:
		}
		counter := cam.rd(regTgInterSt) >> 16 & 255
		if counter == last {
			if time.Since(quiet) > 2*time.Second {
				slog.Warn("camera: no frames for 2s")
				quiet = time.Now()
			}
			time.Sleep(500 * time.Microsecond)
			continue
		}
		last = counter
		quiet = time.Now()
		next, done, handoff := hold.tick(counter)
		if !handoff {
			continue
		}
		cam.wr(regImgoBase, d.mva+uint32(next*frameBytes))
		if done < 0 {
			continue
		}
		copy(owned, d.buf[done*frameBytes:(done+1)*frameBytes])
		frame(owned)
	}
}
