package taternative

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSettingsGenerationAdvancesOnlyAfterAcceptedSettings(t *testing.T) {
	client, err := New(Config{
		URL: "ws://tater.test", DeviceID: "echo-test", Heartbeat: time.Hour,
	}, Hooks{Settings: func(values map[string]any) (map[string]any, error) {
		if values["reject"] == true {
			return values, errors.New("rejected for test")
		}
		return values, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.connected.Store(true)

	client.handle(Envelope{Type: "settings", ID: "accepted", Payload: map[string]any{"wake_word": "hey_tater"}})
	accepted := nextSettingsChanged(t, client)
	if accepted.Payload["ok"] != true || accepted.Payload["settings_generation"] != float64(1) {
		t.Fatalf("accepted settings response = %#v", accepted.Payload)
	}

	client.handle(Envelope{Type: "settings", ID: "rejected", Payload: map[string]any{"reject": true}})
	rejected := nextSettingsChanged(t, client)
	if rejected.Payload["ok"] != false || rejected.Payload["settings_generation"] != float64(1) {
		t.Fatalf("rejected settings response = %#v", rejected.Payload)
	}
}

func TestHostSettingsStillAcceptSharedVoiceControls(t *testing.T) {
	called := false
	client, err := New(Config{
		URL: "ws://tater.test", DeviceID: "shared-voice-settings-test", Heartbeat: time.Hour,
	}, Hooks{Settings: func(values map[string]any) (map[string]any, error) {
		called = true
		return values, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.connected.Store(true)
	shared := map[string]any{
		"wake_sound_enabled": true,
		"wake_sound":         "waterdrop",
		"continued_chat":     false,
		"barge_in_enabled":   true,
	}
	client.handle(Envelope{Type: "settings", ID: "host-voice-settings", Payload: shared})
	message := nextSettingsChanged(t, client)
	if !called || message.Payload["ok"] != true {
		t.Fatal("host-managed voice settings did not reach the runtime hook")
	}
	client.stateMu.RLock()
	defer client.stateMu.RUnlock()
	for key, want := range shared {
		if got := client.settings[key]; got != want {
			t.Fatalf("host setting %s = %#v, want %#v", key, got, want)
		}
	}
}

func TestLocalSettingsShareValidationAndMergeWithoutErasingHostState(t *testing.T) {
	client, err := New(Config{URL: "ws://tater.test", DeviceID: "local-settings-test", Heartbeat: time.Hour}, Hooks{Settings: func(values map[string]any) (map[string]any, error) {
		if values["reject"] == true {
			return values, errors.New("rejected")
		}
		return values, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.connected.Store(true)
	if err := client.ApplyLocalSettings(map[string]any{"screen_clock_format": "24h", "screen_popup_effect": "slide", "screen_text_style": "matrix"}); err != nil {
		t.Fatal(err)
	}
	message := nextSettingsChanged(t, client)
	if message.Payload["ok"] != true {
		t.Fatal("local settings were not reported")
	}
	client.handle(Envelope{Type: "settings", Payload: map[string]any{"display_theme": "forest"}})
	nextSettingsChanged(t, client)
	client.stateMu.RLock()
	clock, motion, theme := client.settings["screen_clock_format"], client.settings["screen_popup_effect"], client.settings["display_theme"]
	textStyle := client.settings["screen_text_style"]
	client.stateMu.RUnlock()
	if clock != "24h" || motion != "slide" || theme != "forest" || textStyle != "matrix" {
		t.Fatal("partial host update erased local preferences")
	}
	if err := client.ApplyLocalSettings(map[string]any{"reject": true}); err == nil {
		t.Fatal("failed local validation was ignored")
	}
	client.stateMu.RLock()
	rejected := client.settings["reject"]
	client.stateMu.RUnlock()
	if rejected != nil {
		t.Fatal("rejected settings entered live state")
	}
}

func nextSettingsChanged(t *testing.T, client *Client) Envelope {
	t.Helper()
	select {
	case frame := <-client.out:
		if frame.kind != websocket.TextMessage {
			t.Fatalf("frame kind = %d", frame.kind)
		}
		var message Envelope
		if err := json.Unmarshal(frame.data, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type != "settings.changed" {
			t.Fatalf("message type = %q", message.Type)
		}
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for settings.changed")
		return Envelope{}
	}
}
