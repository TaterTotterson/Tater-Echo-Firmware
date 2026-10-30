package taternative

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDNDActiveAtManualAndDailyWindows(t *testing.T) {
	at := func(hour, minute int) time.Time {
		return time.Date(2026, time.October, 29, hour, minute, 0, 0, time.Local)
	}
	tests := []struct {
		name     string
		settings map[string]any
		now      time.Time
		want     bool
	}{
		{"off", map[string]any{}, at(23, 0), false},
		{"manual", map[string]any{"dnd_enabled": true}, at(12, 0), true},
		{"overnight before midnight", map[string]any{"dnd_schedule_enabled": true, "dnd_start": "22:00", "dnd_end": "07:00"}, at(23, 0), true},
		{"overnight after midnight", map[string]any{"dnd_schedule_enabled": true, "dnd_start": "22:00", "dnd_end": "07:00"}, at(6, 59), true},
		{"overnight ended", map[string]any{"dnd_schedule_enabled": true, "dnd_start": "22:00", "dnd_end": "07:00"}, at(7, 0), false},
		{"daytime window", map[string]any{"dnd_schedule_enabled": true, "dnd_start": "09:00", "dnd_end": "17:00"}, at(12, 0), true},
		{"equal means all day", map[string]any{"dnd_schedule_enabled": true, "dnd_start": "00:00", "dnd_end": "00:00"}, at(12, 0), true},
		{"invalid disabled", map[string]any{"dnd_schedule_enabled": true, "dnd_start": "later", "dnd_end": "07:00"}, at(23, 0), false},
		{"controller local offset", map[string]any{"dnd_schedule_enabled": true, "dnd_start": "22:00", "dnd_end": "07:00", "utc_offset_seconds": -5 * 60 * 60}, time.Date(2026, time.October, 30, 4, 0, 0, 0, time.UTC), true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dndActiveAt(test.settings, test.now); got != test.want {
				t.Fatalf("dndActiveAt() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDNDRejectsRemoteVoicePlayback(t *testing.T) {
	played := false
	client, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{
		PlayVoice: func(context.Context, PlayRequest) error {
			played = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.connected.Store(true)
	client.stateMu.Lock()
	client.settings["dnd_enabled"] = true
	client.stateMu.Unlock()

	client.handle(Envelope{
		Type: "play.url",
		ID:   "dnd-play",
		Payload: map[string]any{
			"url": "https://audio.test/announcement.wav",
		},
	})
	if played {
		t.Fatal("DND allowed remote voice playback")
	}
	select {
	case frame := <-client.out:
		if frame.kind != websocket.TextMessage {
			t.Fatalf("response kind = %d, want text", frame.kind)
		}
		var message Envelope
		if err := json.Unmarshal(frame.data, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type != "playback.finished" || boolValue(message.Payload["ok"]) || !boolValue(message.Payload["dnd_active"]) {
			t.Fatalf("DND response = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("DND rejection was not reported")
	}
}
