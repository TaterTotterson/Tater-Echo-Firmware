package actionbutton

import (
	"sort"
	"testing"
	"time"
)

type fakeTimer struct {
	when    time.Time
	fn      func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock() *fakeClock      { return &fakeClock{now: time.Unix(100, 0)} }
func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) AfterFunc(d time.Duration, fn func()) Timer {
	t := &fakeTimer{when: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, t)
	return t
}
func (c *fakeClock) advance(d time.Duration) {
	target := c.now.Add(d)
	for {
		sort.SliceStable(c.timers, func(i, j int) bool { return c.timers[i].when.Before(c.timers[j].when) })
		var next *fakeTimer
		for len(c.timers) > 0 {
			next, c.timers = c.timers[0], c.timers[1:]
			if !next.stopped {
				break
			}
			next = nil
		}
		if next == nil || next.when.After(target) {
			if next != nil {
				c.timers = append(c.timers, next)
			}
			c.now = target
			return
		}
		c.now = next.when
		next.stopped = true
		next.fn()
	}
}

func click(g *Gesture, clock *fakeClock) {
	g.Press()
	clock.advance(100 * time.Millisecond)
	g.Release()
	clock.advance(100 * time.Millisecond)
}

func TestHoldStartsIntercomAndReleaseProcessesTurn(t *testing.T) {
	clock := newFakeClock()
	starts, stops := 0, 0
	g := New(Config{Clock: clock}, Callbacks{
		StartIntercom: func() bool { starts++; return true },
		StopIntercom:  func() { stops++ },
	})
	g.Press()
	clock.advance(DefaultIntercomHold - time.Millisecond)
	if starts != 0 {
		t.Fatal("intercom started before hold threshold")
	}
	clock.advance(time.Millisecond)
	if starts != 1 {
		t.Fatalf("intercom starts = %d, want 1", starts)
	}
	g.Release()
	if stops != 1 {
		t.Fatalf("intercom stops = %d, want 1", stops)
	}
}

func TestReleaseDoesNotStopRejectedIntercom(t *testing.T) {
	clock := newFakeClock()
	stops := 0
	g := New(Config{Clock: clock}, Callbacks{
		StartIntercom: func() bool { return false },
		StopIntercom:  func() { stops++ },
	})
	g.Press()
	clock.advance(DefaultIntercomHold)
	g.Release()
	if stops != 0 {
		t.Fatalf("rejected intercom sent %d stops", stops)
	}
}

func TestFiveClicksThenSixthHoldEntersSetupWithoutIntercom(t *testing.T) {
	clock := newFakeClock()
	starts, setups := 0, 0
	var clicks []int
	var countdown []int
	g := New(Config{Clock: clock}, Callbacks{
		StartIntercom: func() bool { starts++; return true },
		ShowClicks:    func(count, _ int) { clicks = append(clicks, count) },
		ShowCountdown: func(remaining, _ int) { countdown = append(countdown, remaining) },
		SetupComplete: func() { setups++ },
	})
	for i := 0; i < DefaultClickTarget; i++ {
		click(g, clock)
	}
	g.Press()
	clock.advance(DefaultSetupHold)
	if setups != 1 {
		t.Fatalf("setup completions = %d, want 1", setups)
	}
	if starts != 0 {
		t.Fatalf("intercom starts during setup gesture = %d", starts)
	}
	if len(clicks) != DefaultClickTarget || clicks[len(clicks)-1] != DefaultClickTarget {
		t.Fatalf("click progress = %v", clicks)
	}
	if len(countdown) == 0 || countdown[0] != DefaultSteps || countdown[len(countdown)-1] != 0 {
		t.Fatalf("countdown = %v", countdown)
	}
}

func TestCancelledSixthHoldDoesNotEnterSetup(t *testing.T) {
	clock := newFakeClock()
	setups, clears := 0, 0
	g := New(Config{Clock: clock}, Callbacks{
		SetupComplete: func() { setups++ },
		ClearFeedback: func() { clears++ },
	})
	for i := 0; i < DefaultClickTarget; i++ {
		click(g, clock)
	}
	g.Press()
	clock.advance(DefaultSetupHold - time.Millisecond)
	g.Release()
	clock.advance(time.Millisecond)
	if setups != 0 {
		t.Fatal("cancelled setup hold completed")
	}
	if clears == 0 {
		t.Fatal("cancelled setup hold did not clear feedback")
	}
}

func TestClickSequenceExpires(t *testing.T) {
	clock := newFakeClock()
	starts, setups, clears := 0, 0, 0
	g := New(Config{Clock: clock}, Callbacks{
		StartIntercom: func() bool { starts++; return true },
		SetupComplete: func() { setups++ },
		ClearFeedback: func() { clears++ },
	})
	for i := 0; i < DefaultClickTarget; i++ {
		click(g, clock)
	}
	clock.advance(DefaultArmWindow + time.Millisecond)
	g.Press()
	clock.advance(DefaultIntercomHold)
	if starts != 1 || setups != 0 {
		t.Fatalf("expired sequence: starts=%d setups=%d", starts, setups)
	}
	if clears == 0 {
		t.Fatal("expired sequence did not clear feedback")
	}
}
