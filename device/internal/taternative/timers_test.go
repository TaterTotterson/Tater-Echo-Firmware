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
	}, func(active bool, _ Timer) { alarms <- active })
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
