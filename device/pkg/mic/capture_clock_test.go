package mic

import (
	"testing"
	"time"
)

func TestCaptureClockCadenceDriftAndRestart(t *testing.T) {
	clock := NewCaptureClock(16000)
	base := time.Now()
	first := clock.Stamp(nil, 2560, base)
	second := clock.Stamp(nil, 2560, base.Add(160*time.Millisecond))
	if second.CapturedAt.Sub(first.CapturedAt) != 160*time.Millisecond || second.Sequence != 2 || second.Generation != first.Generation {
		t.Fatalf("continuous capture: first=%+v second=%+v", first, second)
	}
	// Bursty delivery cannot collapse or reverse the sample timeline.
	burst := clock.Stamp(nil, 2560, base.Add(161*time.Millisecond))
	if gap := burst.CapturedAt.Sub(second.CapturedAt); gap < 159*time.Millisecond {
		t.Fatalf("scheduler burst collapsed sample cadence to %s", gap)
	}
	clock.Restart()
	restarted := clock.Stamp(nil, 2560, base.Add(time.Second))
	if restarted.Generation == first.Generation || restarted.Sequence != 4 || !restarted.CapturedAt.Equal(base.Add(time.Second)) {
		t.Fatalf("restart=%+v", restarted)
	}
	gap := clock.Stamp(nil, 2560, base.Add(2*time.Second))
	if gap.Generation == restarted.Generation {
		t.Fatal("capture stall retained generation")
	}
}

func TestCaptureClockTracksFastHardwareWithoutAccumulatingFutureTime(t *testing.T) {
	clock := NewCaptureClock(16000)
	base := time.Now()
	arrival := base
	for i := 0; i < 10000; i++ {
		capture := clock.Stamp(nil, 2560, arrival)
		if delta := capture.CapturedAt.Sub(arrival); delta > time.Millisecond || delta < -time.Millisecond {
			t.Fatalf("clock drift accumulated at batch %d: %s", i, delta)
		}
		arrival = arrival.Add(160*time.Millisecond - 55200*time.Nanosecond) // 345 ppm
	}
}
