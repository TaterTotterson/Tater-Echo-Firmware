package main

import (
	"testing"
	"time"
)

func TestFrameRequestsPresentVideoWithoutAnimationTick(t *testing.T) {
	requests := newFrameRequests()
	// The same request is used when a prepared live frame arrives. No timer
	// tick is needed to make the presentation branch runnable.
	requests.request()
	select {
	case <-requests:
	default:
		t.Fatal("ready video waited for an animation tick")
	}
	// The next animation tick still schedules a normal draw.
	requests.request()
	if len(requests) != 1 {
		t.Fatal("animation draw lost")
	}
}

func TestFrameRequestsCoalesceWithoutQueueingPictures(t *testing.T) {
	requests := newFrameRequests()
	latest := 0
	for i := 1; i <= 1000; i++ {
		latest = i
		requests.request()
	}
	<-requests
	if latest != 1000 || len(requests) != 0 {
		t.Fatal("presentation did not use latest state in one draw")
	}
}

func TestVideoFrameGapsKeepWindowBoundaryButIgnoreBackwardsTime(t *testing.T) {
	start := time.Unix(1, 0)
	var gaps videoFrameGaps
	gaps.note(start)
	gaps.note(start.Add(60 * time.Millisecond))
	gaps.note(start.Add(140 * time.Millisecond))
	gaps.note(start)
	if gaps.count != 2 || gaps.total != 140*time.Millisecond || gaps.max != 80*time.Millisecond {
		t.Fatal(gaps)
	}
	gaps.resetWindow()
	gaps.note(start.Add(210 * time.Millisecond))
	if gaps.count != 1 || gaps.total != 70*time.Millisecond || gaps.max != 70*time.Millisecond {
		t.Fatal("lost window-boundary gap", gaps)
	}
}
