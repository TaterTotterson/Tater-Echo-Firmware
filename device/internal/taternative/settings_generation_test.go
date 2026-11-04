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
