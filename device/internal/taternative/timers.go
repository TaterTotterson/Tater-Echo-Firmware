package taternative

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxTimers       = 8
	maxTimerSeconds = 7 * 24 * 60 * 60
)

// Timer is one locally-owned countdown timer.
type Timer struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	Label              string `json:"label,omitempty"`
	OriginalDurationMS int    `json:"original_duration_ms"`
	DurationMS         int    `json:"duration_ms"`
	RemainingMS        int    `json:"remaining_ms"`
	TotalSeconds       int    `json:"total_seconds"`
	SecondsLeft        int    `json:"seconds_left"`
	State              string `json:"state"`
	Active             bool   `json:"active"`
	Ringing            bool   `json:"ringing"`
	deadline           time.Time
	handle             *time.Timer
}

func (t *Timer) snapshot(now time.Time) Timer {
	out := *t
	out.handle = nil
	out.Label = out.Name
	out.DurationMS = out.OriginalDurationMS
	out.TotalSeconds = (out.OriginalDurationMS + 999) / 1000
	out.Active = true
	if t.Ringing {
		out.RemainingMS = 0
		out.State = "ringing"
	} else {
		out.RemainingMS = max(0, int(t.deadline.Sub(now).Milliseconds()))
		out.State = "armed"
	}
	out.SecondsLeft = (out.RemainingMS + 999) / 1000
	return out
}

type timerSend func(messageType, id string, payload map[string]any) bool

// TimerManager owns countdowns across WebSocket reconnects.
type TimerManager struct {
	mu      sync.Mutex
	timers  map[string]*Timer
	send    timerSend
	onAlarm func(active bool, timer Timer)
	closed  bool
}

func NewTimerManager(send timerSend, onAlarm func(active bool, timer Timer)) *TimerManager {
	return &TimerManager{timers: map[string]*Timer{}, send: send, onAlarm: onAlarm}
}

func (m *TimerManager) Handle(messageType, messageID string, payload map[string]any) bool {
	switch messageType {
	case "timer.start", "timer.arm":
		m.start(payload, messageID, messageType == "timer.arm")
	case "timer.list", "timer.status":
		rows := m.rows()
		m.result(messageID, "list", true, map[string]any{"timers": rows, "count": len(rows)})
	case "timer.cancel", "timer.clear":
		m.cancelTimers(payload, messageID, messageType == "timer.clear")
	case "timer.snooze":
		m.snooze(payload, messageID)
	case "timer.alarm":
		m.alarm(payload)
	default:
		return false
	}
	return true
}

func (m *TimerManager) start(payload map[string]any, replyTo string, replace bool) {
	duration := intValue(payload["remaining_ms"], 0)
	if duration == 0 {
		duration = intValue(payload["duration_ms"], 0)
	}
	if duration == 0 {
		duration = intValue(payload["remaining_s"], intValue(payload["duration_s"], 0)) * 1000
	}
	duration = clamp(duration, 0, maxTimerSeconds*1000)
	original := intValue(payload["original_duration_ms"], 0)
	if original == 0 {
		original = intValue(payload["original_duration_s"], 0) * 1000
	}
	if original == 0 {
		original = duration
	}
	id := stringValue(payload["id"])
	if id == "" {
		id = stringValue(payload["timer_id"])
	}
	if id == "" {
		id = newMessageID()[:12]
	}
	if len(id) > 47 {
		id = id[:47]
	}
	name := stringValue(payload["name"])
	if name == "" {
		name = stringValue(payload["label"])
	}
	if len(name) > 63 {
		name = name[:63]
	}
	if duration <= 0 {
		m.result(replyTo, "start", false, map[string]any{"code": "invalid_duration", "message": "Timer duration must be greater than zero."})
		return
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.result(replyTo, "start", false, map[string]any{"code": "closed"})
		return
	}
	existing := m.timers[id]
	if existing != nil && !replace {
		row := existing.snapshot(time.Now())
		m.mu.Unlock()
		m.result(replyTo, "start", true, map[string]any{"timer": row, "code": "already_exists"})
		return
	}
	if existing == nil && len(m.timers) >= maxTimers {
		m.mu.Unlock()
		m.result(replyTo, "start", false, map[string]any{"code": "timer_limit", "message": "This satellite already has the maximum number of timers."})
		return
	}
	if existing != nil && existing.handle != nil {
		existing.handle.Stop()
	}
	t := &Timer{ID: id, Name: name, OriginalDurationMS: original, deadline: time.Now().Add(time.Duration(duration) * time.Millisecond), State: "armed"}
	t.handle = time.AfterFunc(time.Duration(duration)*time.Millisecond, func() { m.expire(id, t) })
	m.timers[id] = t
	row := t.snapshot(time.Now())
	m.mu.Unlock()
	m.result(replyTo, "start", true, map[string]any{"timer": row})
	event := "armed"
	if replace && existing != nil {
		event = "updated"
	}
	m.emit(event, row, true)
}

func (m *TimerManager) expire(id string, expected *Timer) {
	m.mu.Lock()
	t := m.timers[id]
	if t != expected || t.Ringing || m.closed {
		m.mu.Unlock()
		return
	}
	t.Ringing = true
	t.State = "ringing"
	t.deadline = time.Now()
	row := t.snapshot(time.Now())
	m.mu.Unlock()
	if m.onAlarm != nil {
		m.onAlarm(true, row)
	}
	m.emit("expired", row, true)
}

func (m *TimerManager) alarm(payload map[string]any) {
	selected, _ := m.selectTimers(payload, false)
	for _, row := range selected {
		m.mu.Lock()
		t := m.timers[row.ID]
		m.mu.Unlock()
		if t != nil {
			m.expire(t.ID, t)
		}
	}
}

func (m *TimerManager) cancelTimers(payload map[string]any, replyTo string, clearAll bool) {
	selected, ambiguous := m.selectTimers(payload, clearAll)
	if ambiguous {
		selected = nil
	}
	wasRinging := false
	m.mu.Lock()
	for _, row := range selected {
		if t := m.timers[row.ID]; t != nil {
			if t.handle != nil {
				t.handle.Stop()
			}
			wasRinging = wasRinging || t.Ringing
			delete(m.timers, row.ID)
		}
	}
	remainingRinging := m.ringingLocked()
	m.mu.Unlock()
	for _, row := range selected {
		event := "cancelled"
		if clearAll {
			event = "cleared"
		}
		m.emit(event, row, false)
	}
	if wasRinging && !remainingRinging && m.onAlarm != nil {
		m.onAlarm(false, Timer{})
	}
	code, detail := "", ""
	if ambiguous {
		code, detail = "ambiguous", "More than one timer is running; specify a timer name or duration."
	} else if len(selected) == 0 {
		code, detail = "not_found", "No matching timer is running."
	}
	m.result(replyTo, "cancel", true, map[string]any{"timers": selected, "affected": len(selected), "code": code, "message": detail})
}

func (m *TimerManager) snooze(payload map[string]any, replyTo string) {
	duration := intValue(payload["duration_ms"], intValue(payload["duration_s"], 300)*1000)
	duration = clamp(duration, 1, maxTimerSeconds*1000)
	selected, ambiguous := m.selectTimers(payload, false)
	if ambiguous {
		selected = nil
	}
	rows := make([]Timer, 0, len(selected))
	wasRinging := false
	m.mu.Lock()
	for _, old := range selected {
		t := m.timers[old.ID]
		if t == nil {
			continue
		}
		if t.handle != nil {
			t.handle.Stop()
		}
		wasRinging = wasRinging || t.Ringing
		t.Ringing = false
		t.State = "armed"
		t.OriginalDurationMS = duration
		t.deadline = time.Now().Add(time.Duration(duration) * time.Millisecond)
		t.handle = time.AfterFunc(time.Duration(duration)*time.Millisecond, func() { m.expire(t.ID, t) })
		rows = append(rows, t.snapshot(time.Now()))
	}
	remainingRinging := m.ringingLocked()
	m.mu.Unlock()
	if wasRinging && !remainingRinging && m.onAlarm != nil {
		m.onAlarm(false, Timer{})
	}
	for _, row := range rows {
		m.emit("snoozed", row, true)
	}
	code, detail := "", ""
	if ambiguous {
		code, detail = "ambiguous", "More than one timer is running; specify a timer name or duration."
	} else if len(rows) == 0 {
		code, detail = "not_found", "No matching timer is running."
	}
	m.result(replyTo, "snooze", true, map[string]any{"timers": rows, "affected": len(rows), "code": code, "message": detail})
}

func (m *TimerManager) selectTimers(payload map[string]any, all bool) ([]Timer, bool) {
	rows := m.rows()
	if all || boolValue(payload["all"]) {
		return rows, false
	}
	id := stringValue(payload["id"])
	if id == "" {
		id = stringValue(payload["timer_id"])
	}
	name := strings.ToLower(stringValue(payload["name"]))
	if name == "" {
		name = strings.ToLower(stringValue(payload["label"]))
	}
	duration := intValue(payload["original_duration_ms"], intValue(payload["original_duration_s"], intValue(payload["duration_s"], 0))*1000)
	if id != "" || name != "" || duration != 0 {
		selected := make([]Timer, 0)
		for _, row := range rows {
			if id != "" && row.ID != id {
				continue
			}
			if name != "" && strings.ToLower(row.Name) != name {
				continue
			}
			if duration != 0 && row.OriginalDurationMS != duration {
				continue
			}
			selected = append(selected, row)
		}
		return selected, len(selected) > 1 && id == ""
	}
	for _, row := range rows {
		if row.Ringing {
			return []Timer{row}, false
		}
	}
	if len(rows) == 1 {
		return rows, false
	}
	return nil, len(rows) > 1
}

func (m *TimerManager) rows() []Timer {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	rows := make([]Timer, 0, len(m.timers))
	for _, t := range m.timers {
		rows = append(rows, t.snapshot(now))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func (m *TimerManager) emit(event string, timer Timer, active bool) {
	payload := timerMap(timer)
	payload["event"] = event
	payload["active"] = active
	if !active {
		payload["state"] = "stopped"
	}
	m.send("timer.event", "", payload)
}

func (m *TimerManager) result(replyTo, action string, ok bool, extra map[string]any) {
	payload := map[string]any{"reply_to": replyTo, "action": action, "ok": ok}
	for key, value := range extra {
		payload[key] = value
	}
	m.send("timer.result", "", payload)
}

func timerMap(t Timer) map[string]any {
	return map[string]any{
		"id": t.ID, "name": t.Name, "label": t.Label,
		"original_duration_ms": t.OriginalDurationMS, "duration_ms": t.DurationMS,
		"remaining_ms": t.RemainingMS, "total_seconds": t.TotalSeconds,
		"seconds_left": t.SecondsLeft, "state": t.State,
		"active": t.Active, "ringing": t.Ringing,
	}
}

func (m *TimerManager) ringingLocked() bool {
	for _, timer := range m.timers {
		if timer.Ringing {
			return true
		}
	}
	return false
}

func (m *TimerManager) Status() map[string]any {
	rows := m.rows()
	ringing := 0
	for _, row := range rows {
		if row.Ringing {
			ringing++
		}
	}
	return map[string]any{
		"timer_count": len(rows), "timer_ringing": ringing > 0,
		"timer": map[string]any{"active": len(rows) > 0, "ringing": ringing > 0, "count": len(rows), "ringing_count": ringing, "timers": rows},
	}
}

func (m *TimerManager) Close() {
	m.mu.Lock()
	m.closed = true
	for _, t := range m.timers {
		if t.handle != nil {
			t.handle.Stop()
		}
	}
	m.timers = map[string]*Timer{}
	m.mu.Unlock()
	if m.onAlarm != nil {
		m.onAlarm(false, Timer{})
	}
}

func (t Timer) String() string {
	if t.Name != "" {
		return t.Name
	}
	return fmt.Sprintf("timer %s", t.ID)
}
