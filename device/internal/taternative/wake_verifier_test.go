package taternative

import (
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBuildWakeVerifierPacket(t *testing.T) {
	pcm := []byte{1, 0, 2, 0}
	packet, err := buildWakeVerifierPacket(pcm, 0x12345678, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(packet[:4]) != "TWV1" || packet[4] != 1 || packet[5] != 1 {
		t.Fatalf("invalid verifier prefix: %v", packet[:6])
	}
	if flags := binary.LittleEndian.Uint16(packet[6:8]); flags != wakeVerifierFlagEnforce {
		t.Fatalf("flags = %d", flags)
	}
	if id := binary.LittleEndian.Uint32(packet[8:12]); id != 0x12345678 {
		t.Fatalf("request id = %#x", id)
	}
	if rate := binary.LittleEndian.Uint32(packet[12:16]); rate != SampleRate {
		t.Fatalf("sample rate = %d", rate)
	}
	if samples := binary.LittleEndian.Uint32(packet[16:20]); samples != 2 {
		t.Fatalf("samples = %d", samples)
	}
	if got := packet[wakeVerifierHeaderBytes:]; string(got) != string(pcm) {
		t.Fatalf("PCM = %v, want %v", got, pcm)
	}
}

func TestEnforcedWakeVerifierRejectsThenAccepts(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.stateMu.Lock()
	c.settings["wake_verifier_mode"] = "enforce"
	c.settings["wake_verifier_window_ms"] = 500
	c.settings["wake_verifier_timeout_ms"] = 2000
	c.stateMu.Unlock()
	c.PushAudio(make([]byte, SampleRate)) // 500 ms of S16 mono PCM

	if !c.Wake("hey_tater", 0.98) {
		t.Fatal("enforced wake was not queued")
	}
	first := <-c.out
	if first.kind != websocket.BinaryMessage || string(first.data[:4]) != "TWV1" {
		t.Fatalf("first frame is not wake verification: kind=%d", first.kind)
	}
	firstID := binary.LittleEndian.Uint32(first.data[8:12])
	c.handleWakeVerificationResult(map[string]any{
		"request_id": int(firstID), "accepted": false, "available": true, "reason": "no_phrase",
	})
	select {
	case frame := <-c.out:
		t.Fatalf("rejected verification opened a turn: kind=%d data=%q", frame.kind, frame.data)
	case <-time.After(25 * time.Millisecond):
	}

	if !c.Wake("hey_tater", 0.99) {
		t.Fatal("second enforced wake was not queued")
	}
	second := <-c.out
	secondID := binary.LittleEndian.Uint32(second.data[8:12])
	c.handleWakeVerificationResult(map[string]any{
		"request_id": int(secondID), "accepted": true, "available": true, "reason": "phrase_match",
	})
	started := <-c.out
	if started.kind != websocket.TextMessage {
		t.Fatalf("accepted wake frame kind = %d", started.kind)
	}
	var message Envelope
	if err := json.Unmarshal(started.data, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != "voice.start" || message.Payload["source"] != "local_wake" {
		t.Fatalf("accepted wake message = %#v", message)
	}
	status := c.wakeVerifierStatus()
	if status["completed"] != uint64(2) || status["rejections"] != uint64(1) {
		t.Fatalf("verifier status = %#v", status)
	}
}

func TestWakeHonorsBargeInSetting(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.stateMu.Lock()
	c.state = "speaking"
	c.settings["barge_in_enabled"] = false
	c.stateMu.Unlock()
	if c.Wake("hey_tater", 0.99) {
		t.Fatal("wake interrupted speech while barge-in was disabled")
	}
	c.stateMu.Lock()
	c.settings["barge_in_enabled"] = true
	c.stateMu.Unlock()
	if !c.Wake("hey_tater", 0.99) {
		t.Fatal("wake did not interrupt speech while barge-in was enabled")
	}
}
