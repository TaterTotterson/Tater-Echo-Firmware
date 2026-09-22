package taternative

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHandshakeWakeAckFlushesPreRollInOrder(t *testing.T) {
	upgrader := websocket.Upgrader{}
	gotStart := make(chan struct{})
	allowAck := make(chan struct{})
	frames := make(chan [][]byte, 1)
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != NativeWSPath {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var hello Envelope
		if json.Unmarshal(raw, &hello) != nil || hello.Type != "hello" {
			return
		}
		ack, _ := marshalEnvelope("hello.ack", hello.ID, map[string]any{
			"ok": true, "selector": "native:echo-test", "device_token": "paired-token",
		})
		if conn.WriteMessage(websocket.TextMessage, ack) != nil {
			return
		}
		_, raw, err = conn.ReadMessage()
		if err != nil {
			return
		}
		var start Envelope
		if json.Unmarshal(raw, &start) != nil || start.Type != "voice.start" {
			return
		}
		once.Do(func() { close(gotStart) })
		<-allowAck
		startAck, _ := marshalEnvelope("voice.start.ack", start.ID, map[string]any{"ok": true, "result": 1})
		if conn.WriteMessage(websocket.TextMessage, startAck) != nil {
			return
		}
		var received [][]byte
		for len(received) < 3 {
			kind, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if kind == websocket.BinaryMessage {
				received = append(received, append([]byte(nil), payload...))
			}
		}
		frames <- received
	}))
	defer server.Close()

	tokenPath := t.TempDir() + "/token"
	c, err := New(Config{
		URL:       strings.Replace(server.URL, "http://", "ws://", 1),
		TokenPath: tokenPath, DeviceID: "echo-test", DeviceName: "Kitchen Echo",
		Reconnect: 10 * time.Millisecond, Heartbeat: time.Hour,
	}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	c.PushAudio([]byte{1, 0, 1, 0})
	c.PushAudio([]byte{2, 0, 2, 0})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for !c.Connected() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !c.Connected() {
		t.Fatal("client did not connect")
	}
	if !c.Wake("hey_tater", 0.99) {
		t.Fatal("Wake returned false")
	}
	select {
	case <-gotStart:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive voice.start")
	}
	c.PushAudio([]byte{3, 0, 3, 0})
	close(allowAck)
	select {
	case got := <-frames:
		for i, value := range []byte{1, 2, 3} {
			if got[i][0] != value {
				t.Fatalf("frame %d starts %d, want %d", i, got[i][0], value)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pre-roll was not flushed")
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil || strings.TrimSpace(string(raw)) != "paired-token" {
		t.Fatalf("paired token = %q, err=%v", raw, err)
	}
}

func TestVoiceSegmentsPlayInOrderAndFinishOnce(t *testing.T) {
	played := make(chan string, 3)
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{
		PlayVoice: func(_ context.Context, req PlayRequest) error {
			played <- req.URL
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)

	if !c.queueVoice(PlayRequest{URL: "https://audio.test/one.wav"}) ||
		!c.queueVoice(PlayRequest{URL: "https://audio.test/two.wav"}) {
		t.Fatal("voice segment was not queued")
	}
	for _, want := range []string{"https://audio.test/one.wav", "https://audio.test/two.wav"} {
		select {
		case got := <-played:
			if got != want {
				t.Fatalf("played %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}

	completions := 0
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	quiet := time.NewTimer(ttsSegmentGrace + 150*time.Millisecond)
	defer quiet.Stop()
	for {
		select {
		case frame := <-c.out:
			if frame.kind != websocket.TextMessage {
				continue
			}
			var message Envelope
			if err := json.Unmarshal(frame.data, &message); err != nil {
				t.Fatal(err)
			}
			if message.Type == "playback.finished" {
				completions++
			}
		case <-quiet.C:
			if completions != 1 {
				t.Fatalf("playback.finished count = %d, want 1", completions)
			}
			return
		case <-deadline.C:
			t.Fatalf("timed out; playback.finished count = %d", completions)
		}
	}
}
