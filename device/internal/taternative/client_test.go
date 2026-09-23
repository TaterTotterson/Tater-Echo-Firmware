package taternative

import (
	"context"
	"encoding/json"
	"errors"
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
	helloPayload := make(chan map[string]any, 1)
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
		helloPayload <- hello.Payload
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
	select {
	case hello := <-helloPayload:
		if hello["firmware_target"] != "biscuit" {
			t.Fatalf("firmware_target = %v, want biscuit", hello["firmware_target"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive hello")
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

func TestIntentEndContinuationReopensAfterPlayback(t *testing.T) {
	played := make(chan PlayRequest, 1)
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{
		PlayVoice: func(_ context.Context, req PlayRequest) error {
			played <- req
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)

	c.handle(Envelope{Type: "voice.event", Payload: map[string]any{
		"event": "INTENT_END",
		"data": map[string]any{
			"continue_conversation": true,
			"conversation_id":       "conversation-1",
		},
	}})
	c.handle(Envelope{Type: "play.url", Payload: map[string]any{
		"url": "https://audio.test/follow-up.wav",
	}})

	select {
	case req := <-played:
		if !req.ContinueConversation || req.ConversationID != "conversation-1" {
			t.Fatalf("play request continuation = %t, conversation = %q", req.ContinueConversation, req.ConversationID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up response did not play")
	}

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
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
			if message.Type != "voice.start" {
				continue
			}
			if source := stringValue(message.Payload["source"]); source != "continued_chat" {
				t.Fatalf("voice.start source = %q", source)
			}
			if conversationID := stringValue(message.Payload["conversation_id"]); conversationID != "conversation-1" {
				t.Fatalf("voice.start conversation_id = %q", conversationID)
			}
			return
		case <-deadline.C:
			t.Fatal("continued-chat voice.start was not sent")
		}
	}
}

func TestContinuedChatSettingPreventsMicrophoneReopen(t *testing.T) {
	played := make(chan PlayRequest, 1)
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{
		PlayVoice: func(_ context.Context, req PlayRequest) error {
			played <- req
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.stateMu.Lock()
	c.settings["continued_chat"] = false
	c.stateMu.Unlock()

	c.handle(Envelope{Type: "voice.event", Payload: map[string]any{
		"event": "INTENT_END",
		"data": map[string]any{
			"continue_conversation": true,
			"conversation_id":       "conversation-1",
		},
	}})
	c.handle(Envelope{Type: "play.url", Payload: map[string]any{
		"url": "https://audio.test/no-follow-up.wav",
	}})
	select {
	case <-played:
	case <-time.After(2 * time.Second):
		t.Fatal("response did not play")
	}

	deadline := time.NewTimer(ttsSegmentGrace + 500*time.Millisecond)
	defer deadline.Stop()
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
			if message.Type == "voice.start" {
				t.Fatal("continued-chat voice.start was sent while the setting was disabled")
			}
		case <-deadline.C:
			if got := c.State(); got != "idle" {
				t.Fatalf("state = %q, want idle", got)
			}
			return
		}
	}
}

func TestLateRunEndDoesNotClobberContinuedListening(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)

	if !c.startContinued("conversation-1") {
		t.Fatal("continued-chat voice.start was not accepted")
	}
	c.handle(Envelope{Type: "voice.event", Payload: map[string]any{"event": "RUN_END"}})

	if got := c.State(); got != "listening" {
		t.Fatalf("state after late RUN_END = %q, want listening", got)
	}
	if !c.voiceCaptureInProgress() {
		t.Fatal("late RUN_END closed the continued-chat microphone")
	}

	// The RUN_END for the continued run itself arrives after STT_END has
	// closed capture and should still return the device to idle normally.
	c.handle(Envelope{Type: "voice.event", Payload: map[string]any{"event": "STT_END"}})
	c.handle(Envelope{Type: "voice.event", Payload: map[string]any{"event": "RUN_END"}})
	if got := c.State(); got != "idle" {
		t.Fatalf("completed continued run state = %q, want idle", got)
	}
}

func TestHeartbeatQueuesWebSocketPingAndStatus(t *testing.T) {
	c, err := New(Config{
		URL: "ws://tater.test", DeviceID: "echo-test", Heartbeat: time.Millisecond,
	}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- c.heartbeat(ctx) }()

	seenPing := false
	seenStatus := false
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for !seenPing || !seenStatus {
		select {
		case frame := <-c.out:
			switch frame.kind {
			case websocket.PingMessage:
				seenPing = true
			case websocket.TextMessage:
				var message Envelope
				if err := json.Unmarshal(frame.data, &message); err != nil {
					t.Fatal(err)
				}
				if message.Type == "status" {
					seenStatus = true
				}
			}
		case <-deadline.C:
			t.Fatalf("heartbeat frames missing: ping=%t status=%t", seenPing, seenStatus)
		}
	}

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("heartbeat returned %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after cancellation")
	}
}

func TestAudioDroppedCounterUsesAlignedAtomic(t *testing.T) {
	var c Client
	const workers = 8
	const increments = 1000
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range increments {
				c.audioDropped.Add(1)
			}
		}()
	}
	wg.Wait()
	if got, want := c.audioDropped.Load(), uint64(workers*increments); got != want {
		t.Fatalf("audio dropped count = %d, want %d", got, want)
	}
}
