package taternative

import (
	"math"
	"testing"
	"time"
)

func TestSyncClockFromServerCorrectsBogusEchoClock(t *testing.T) {
	deviceNow := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	serverNow := time.Date(2026, 9, 23, 8, 30, 0, 125000000, time.UTC)
	var steppedTo int64
	changed, err := syncClockFromServer(
		float64(serverNow.UnixMilli())/1000,
		deviceNow,
		func(value int64) error { steppedTo = value; return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("2010 device clock was not corrected from Tater's timestamp")
	}
	if steppedTo != serverNow.UnixMilli() {
		t.Fatalf("clock stepped to %d, want %d", steppedTo, serverNow.UnixMilli())
	}
}

func TestSyncClockFromServerIgnoresCurrentAndInvalidTimes(t *testing.T) {
	now := time.Date(2026, 9, 23, 8, 30, 0, 0, time.UTC)
	calls := 0
	step := func(int64) error { calls++; return nil }
	for _, serverTime := range []float64{
		float64(now.Add(time.Second).UnixMilli()) / 1000,
		0,
		math.NaN(),
		math.Inf(1),
	} {
		changed, err := syncClockFromServer(serverTime, now, step)
		if err != nil || changed {
			t.Fatalf("server time %v changed=%t err=%v, want ignored", serverTime, changed, err)
		}
	}
	if calls != 0 {
		t.Fatalf("step called %d times for ignored timestamps", calls)
	}
}
