package sendspin

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTaterPlaintextSessionNegotiatesPCMAndQueuesSelectedChannel(t *testing.T) {
	client, err := New(Config{
		StorePath: filepath.Join(t.TempDir(), "sendspin.json"),
		Name:      "Tater Echo Test",
		Instance:  "tater-echo-test",
		Product:   "Tater Echo",
		Version:   "test",
		Unpaired:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !client.SetOutputChannel("left") {
		t.Fatal("left output channel was rejected")
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		if upgradeErr != nil {
			return
		}
		session := newSession(client, conn)
		client.mu.Lock()
		client.sessions[session] = true
		client.mu.Unlock()
		session.run()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var init envelope
	if err := json.Unmarshal(raw, &init); err != nil || init.Type != "client/init" {
		t.Fatalf("client init = %s err=%v", raw, err)
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "server/hello",
		"payload": map[string]any{
			"server_id": "tater-sendspin",
			"name":      "Tater",
			"version":   1,
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, raw, err = conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var hello envelope
	if err := json.Unmarshal(raw, &hello); err != nil || hello.Type != "client/hello" {
		t.Fatalf("client hello = %s err=%v", raw, err)
	}
	var helloBody clientHello
	if err := json.Unmarshal(hello.Payload, &helloBody); err != nil {
		t.Fatal(err)
	}
	if helloBody.Version != 1 {
		t.Fatalf("hello version = %d, want 1", helloBody.Version)
	}
	pcmStereo := false
	for _, format := range helloBody.PlayerSupport.SupportedFormats {
		if format.Codec == "pcm" && format.SampleRate == 48000 && format.Channels == 2 && format.BitDepth == 16 {
			pcmStereo = true
		}
	}
	if !pcmStereo {
		t.Fatalf("client hello lacks Tater PCM stereo: %#v", helloBody.PlayerSupport.SupportedFormats)
	}

	synchronized := false
	timeResponses := 0
	for attempt := 0; attempt < 24 && (!synchronized || timeResponses < 8); attempt++ {
		_, raw, err = conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var message envelope
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		switch message.Type {
		case "client/time":
			var request clientTime
			if err := json.Unmarshal(message.Payload, &request); err != nil {
				t.Fatal(err)
			}
			if err := conn.WriteJSON(map[string]any{
				"type": "server/time",
				"payload": map[string]any{
					"client_transmitted": request.ClientTransmitted,
					"server_received":    request.ClientTransmitted,
					"server_transmitted": request.ClientTransmitted,
				},
			}); err != nil {
				t.Fatal(err)
			}
			timeResponses++
		case "client/state":
			var state clientState
			if err := json.Unmarshal(message.Payload, &state); err != nil {
				t.Fatal(err)
			}
			synchronized = state.State == "synchronized" && state.Available
		}
	}
	if !synchronized {
		t.Fatal("plaintext session did not reach synchronized state")
	}
	if timeResponses < 8 {
		t.Fatalf("plaintext session supplied %d initial clock exchanges, want 8", timeResponses)
	}

	if err := conn.WriteJSON(map[string]any{
		"type": "stream/start",
		"payload": map[string]any{
			"player": map[string]any{
				"codec": "pcm", "sample_rate": 48000, "channels": 2, "bit_depth": 16,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 13)
	packet[0] = msgAudioChunk
	binary.BigEndian.PutUint64(packet[1:9], uint64(nowUs()+1_000_000))
	binary.LittleEndian.PutUint16(packet[9:11], uint16(int16(100)))
	binary.LittleEndian.PutUint16(packet[11:13], uint16(int16(1000)))
	if err := conn.WriteMessage(websocket.BinaryMessage, packet); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for !client.Active() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !client.Active() {
		t.Fatal("plaintext PCM was not queued")
	}
	client.player.mu.Lock()
	defer client.player.mu.Unlock()
	if len(client.player.q) != 1 || len(client.player.q[0].pcm) != 1 || client.player.q[0].pcm[0] != 100 {
		t.Fatalf("selected PCM = %#v, want left sample 100", client.player.q)
	}
}

func TestOutputChannelRejectsUnknownMode(t *testing.T) {
	client, err := New(Config{StorePath: filepath.Join(t.TempDir(), "sendspin.json")})
	if err != nil {
		t.Fatal(err)
	}
	if client.SetOutputChannel("sideways") {
		t.Fatal("invalid output channel was accepted")
	}
	if client.OutputChannel() != "stereo" {
		t.Fatalf("invalid mode changed output channel to %q", client.OutputChannel())
	}
}

func TestOutputChannelPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sendspin.json")
	client, err := New(Config{StorePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !client.SetOutputChannel(" RIGHT ") {
		t.Fatal("right output channel was rejected")
	}
	restarted, err := New(Config{StorePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.OutputChannel(); got != "right" {
		t.Fatalf("output channel after restart = %q, want right", got)
	}
}
