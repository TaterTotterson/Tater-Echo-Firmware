//go:build !dot && !spot && !panel

package camera

import "testing"

func TestTaterHeldCaptureBoardGate(t *testing.T) {
	for _, board := range []string{"cronos", "checkers", "crown", "athena", "rook", ""} {
		t.Run(board, func(t *testing.T) {
			if got := taterUseHeldCaptureSlots(board); got != (board == "cronos") {
				t.Fatalf("held capture for %q = %t", board, got)
			}
		})
	}
}

func TestTaterHeldSlotsStartupWrapAndMissedTicks(t *testing.T) {
	h := newTaterHeldSlots(254)
	for _, tc := range []struct {
		counter    uint32
		next, done int
		handoff    bool
	}{
		{255, 0, -1, false}, {0, 1, -1, true}, {1, 1, -1, false}, {2, 2, 0, true},
		{6, 0, 1, true}, {6, 0, -1, false}, {7, 0, -1, false}, {8, 1, 2, true},
	} {
		n, d, ok := h.tick(tc.counter)
		if n != tc.next || d != tc.done || ok != tc.handoff {
			t.Fatalf("counter %d: got %d/%d/%t, want %+v", tc.counter, n, d, ok, tc)
		}
	}
}

func TestTaterHeldSlotsDelayedLatchAndSlowConsumer(t *testing.T) {
	for _, delay := range []int{0, 1} {
		for _, gaps := range [][]int{{1}, {1, 1, 1, 3, 1, 4, 2, 1}, {4, 3, 2, 1}} {
			h := newTaterHeldSlots(0)
			var buffers [3]int
			active, pending, activateAt := 0, -1, 0
			pollAt, step, lastDelivered, pictures := 1, 0, 0, 0
			for tick := 1; tick < 1024; tick++ {
				if pending >= 0 && tick >= activateAt {
					active, pending = pending, -1
				}
				// Sensor writes carry true chronological IDs, not request IDs.
				buffers[active] = tick
				if tick < pollAt {
					continue
				}
				next, done, handoff := h.tick(uint32(tick) & 255)
				if handoff {
					pending, activateAt = next, tick+1+delay
					if done >= 0 {
						if done == active {
							t.Fatalf("active slot: delay %d tick %d", delay, tick)
						}
						if buffers[done] <= lastDelivered {
							t.Fatalf("stale/reversed: delay %d tick %d frame %d prior %d", delay, tick, buffers[done], lastDelivered)
						}
						lastDelivered = buffers[done]
						pictures++
					}
				}
				pollAt = tick + gaps[step%len(gaps)]
				step++
			}
			if pictures < 100 {
				t.Fatalf("no sustained progress: %d", pictures)
			}
		}
	}
}
