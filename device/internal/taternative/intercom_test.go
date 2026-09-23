package taternative

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"
)

func readTestEnvelope(t *testing.T, c *Client) Envelope {
	t.Helper()
	frame := <-c.out
	if frame.kind != websocket.TextMessage {
		t.Fatalf("frame kind = %d, want text", frame.kind)
	}
	var message Envelope
	if err := json.Unmarshal(frame.data, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func TestIntercomHoldUsesNativeBroadcastWakeWord(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)

	if !c.StartIntercom() {
		t.Fatal("StartIntercom returned false")
	}
	start := readTestEnvelope(t, c)
	if start.Type != "voice.start" || start.Payload["wake_word"] != "push to intercom" || start.Payload["source"] != "center_button_hold" {
		t.Fatalf("unexpected intercom start: %+v", start)
	}
	pcm := []byte{1, 0, 2, 0}
	c.PushAudio(pcm)
	if !c.StopCapture(false) {
		t.Fatal("StopCapture returned false")
	}
	select {
	case frame := <-c.out:
		t.Fatalf("release sent frame before voice.start.ack: %+v", frame)
	default:
	}
	c.handleVoiceStartAck(map[string]any{"ok": true})
	audio := <-c.out
	if audio.kind != websocket.BinaryMessage || !bytes.Equal(audio.data, pcm) {
		t.Fatalf("queued intercom audio = %v, want %v", audio.data, pcm)
	}
	stop := readTestEnvelope(t, c)
	if stop.Type != "voice.stop" || boolValue(stop.Payload["abort"]) {
		t.Fatalf("unexpected intercom stop: %+v", stop)
	}
}

func TestSetupResetCommandAcknowledgesAndRunsHook(t *testing.T) {
	called := 0
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{
		SetupReset: func() error { called++; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.handle(Envelope{Type: "setup.reset", ID: "reset-1", Payload: map[string]any{}})
	ack := readTestEnvelope(t, c)
	if ack.Type != "setup.reset.ack" || ack.ID != "reset-1" || !boolValue(ack.Payload["ok"]) {
		t.Fatalf("unexpected setup reset ack: %+v", ack)
	}
	if called != 1 {
		t.Fatalf("setup reset hook calls = %d, want 1", called)
	}
}
