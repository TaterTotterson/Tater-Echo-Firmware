package taternative

import (
	"sync"
	"testing"
	"time"
)

func TestTimerLifecycleRingsAndCancelsLocally(t *testing.T) {
	var messagesMu sync.Mutex
	kinds := map[string]bool{}
	alarms := make(chan bool, 4)
	m := NewTimerManager(func(kind, _ string, _ map[string]any) bool {
		messagesMu.Lock()
		kinds[kind] = true
		messagesMu.Unlock()
		return true
	}, func(active bool, _ Timer) { alarms <- active }, nil)
	defer m.Close()
	m.Handle("timer.start", "request-1", map[string]any{"id": "tea", "name": "Tea", "duration_ms": 25})
	select {
	case active := <-alarms:
		if !active {
			t.Fatal("timer stopped instead of ringing")
		}
	case <-time.After(time.Second):
		t.Fatal("timer did not ring")
	}
	status := m.Status()
	if status["timer_ringing"] != true {
		t.Fatalf("timer status = %#v", status)
	}
	m.Handle("timer.cancel", "request-2", map[string]any{"id": "tea"})
	select {
	case active := <-alarms:
		if active {
			t.Fatal("cancel restarted alarm")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop alarm")
	}
	if count := m.Status()["timer_count"]; count != 0 {
		t.Fatalf("timer count = %v, want 0", count)
	}

	// Assert both result and event message families were emitted.
	messagesMu.Lock()
	defer messagesMu.Unlock()
	if !kinds["timer.result"] || !kinds["timer.event"] {
		t.Fatalf("message kinds = %#v", kinds)
	}
}

func TestTimerDisplayAndLocalStop(t *testing.T) {
	alarms := make(chan bool, 4)
	updates := make(chan TimerDisplay, 8)
	m := NewTimerManager(
		func(string, string, map[string]any) bool { return true },
		func(active bool, _ Timer) { alarms <- active },
		func(timer TimerDisplay) { updates <- timer },
	)
	defer m.Close()
	m.Handle("timer.start", "request-1", map[string]any{
		"id": "tea", "name": "Tea", "duration_ms": 20,
	})
	select {
	case update := <-updates:
		if !update.Active || update.ID != "tea" || update.DeadlineUnixMS == 0 {
			t.Fatalf("armed display = %#v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("armed timer did not update display")
	}
	select {
	case active := <-alarms:
		if !active {
			t.Fatal("timer stopped before it rang")
		}
	case <-time.After(time.Second):
		t.Fatal("timer did not ring")
	}
	if !m.Ringing() {
		t.Fatal("ringing timer was not reported")
	}
	if stopped := m.StopRinging("wake_word"); stopped != 1 {
		t.Fatalf("stopped %d ringing timers, want 1", stopped)
	}
	select {
	case active := <-alarms:
		if active {
			t.Fatal("local stop restarted alarm")
		}
	case <-time.After(time.Second):
		t.Fatal("local stop did not silence alarm")
	}
	if display := m.Display(); display.Active {
		t.Fatalf("display after stop = %#v", display)
	}
}

func TestStopDisplayedCancelsSoonestCountdown(t *testing.T) {
	m := NewTimerManager(func(string, string, map[string]any) bool { return true }, nil, nil)
	defer m.Close()
	m.Handle("timer.start", "request-1", map[string]any{
		"id": "later", "name": "Later", "duration_ms": 60_000,
	})
	m.Handle("timer.start", "request-2", map[string]any{
		"id": "soon", "name": "Soon", "duration_ms": 30_000,
	})
	if display := m.Display(); display.ID != "soon" || display.Count != 2 {
		t.Fatalf("selected display timer = %#v", display)
	}
	if !m.StopDisplayed("screen") {
		t.Fatal("screen stop did not cancel selected timer")
	}
	if display := m.Display(); display.ID != "later" || display.Count != 1 {
		t.Fatalf("remaining display timer = %#v", display)
	}
}
