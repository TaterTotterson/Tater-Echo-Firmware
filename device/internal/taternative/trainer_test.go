package taternative

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWakeCaptureUploadsTrainerCompatibleRawAudio(t *testing.T) {
	type received struct {
		path      string
		body      []byte
		eventType string
		wakeWord  string
		device    string
		format    string
		rate      string
	}
	uploaded := make(chan received, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		uploaded <- received{
			path: request.URL.Path, body: body,
			eventType: request.Header.Get("X-Event-Type"),
			wakeWord:  request.Header.Get("X-Wake-Word"),
			device:    request.Header.Get("X-Source-Device"),
			format:    request.Header.Get("X-Audio-Format"),
			rate:      request.Header.Get("X-Sample-Rate"),
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	c, err := New(Config{
		URL: "ws://tater.test", DeviceID: "echo-test", DeviceName: "Family Room Echo",
	}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.stateMu.Lock()
	c.settings["capture_wake_audio"] = true
	c.settings["trainer_app_url"] = server.URL
	c.settings["wake_threshold"] = 0.91
	c.stateMu.Unlock()
	pcm := make([]byte, trainerMinimumCaptureBytes)
	for index := range pcm {
		pcm[index] = byte(index)
	}
	c.PushAudio(pcm)

	if !c.Wake("custom_tater", 0.96) {
		t.Fatal("wake was not accepted")
	}
	select {
	case got := <-uploaded:
		if got.path != trainerUploadPath || got.eventType != "wake_detected" ||
			got.wakeWord != "custom_tater" || got.device != "Family Room Echo" ||
			got.format != "pcm_s16le" || got.rate != "16000" {
			t.Fatalf("unexpected trainer request: %#v", got)
		}
		if string(got.body) != string(pcm) {
			t.Fatalf("trainer body bytes = %d, want %d", len(got.body), len(pcm))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wake capture was not uploaded")
	}
}

func TestTrainerLocalResolvesThroughTaterHost(t *testing.T) {
	got, err := resolveTrainerUploadURL(
		"http://trainer.local:8789",
		"wss://10.4.20.210:8501/api/tater/satellite/v1/ws",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://10.4.20.210:8789"+trainerUploadPath {
		t.Fatalf("resolved trainer URL = %q", got)
	}
}

func TestCloseMissesCollapseAndRespectCaptureSetting(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.stateMu.Lock()
	c.settings["capture_close_misses"] = true
	c.stateMu.Unlock()
	c.CloseMiss("hey_tater", 0.80)
	c.CloseMiss("hey_tater", 0.85)
	c.trainerMu.Lock()
	if c.trainerCloseTimer == nil || c.trainerCloseScore != 0.85 {
		c.trainerMu.Unlock()
		t.Fatal("adjacent close misses did not collapse to the strongest score")
	}
	c.trainerMu.Unlock()
	c.cancelCloseMiss()

	c.stateMu.Lock()
	c.settings["capture_close_misses"] = false
	c.stateMu.Unlock()
	c.CloseMiss("hey_tater", 0.90)
	c.trainerMu.Lock()
	pending := c.trainerCloseTimer != nil
	c.trainerMu.Unlock()
	if pending {
		t.Fatal("close miss was retained while capture was disabled")
	}
}

func TestWakeSoundDoesNotPlayDuringBargeIn(t *testing.T) {
	played := make(chan struct{}, 1)
	newClient := func(state string, barge bool) *Client {
		c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-" + strings.ReplaceAll(state, "_", "-")}, Hooks{
			PlayWakeSound: func() bool {
				played <- struct{}{}
				return true
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		c.connected.Store(true)
		c.stateMu.Lock()
		c.state = state
		c.settings["barge_in_enabled"] = barge
		c.stateMu.Unlock()
		return c
	}

	idle := newClient("idle", false)
	defer idle.Close()
	if !idle.Wake("hey_tater", 0.99) {
		t.Fatal("idle wake failed")
	}
	select {
	case <-played:
	case <-time.After(time.Second):
		t.Fatal("normal wake did not play the configured sound")
	}

	barge := newClient("speaking", true)
	defer barge.Close()
	if !barge.Wake("hey_tater", 0.99) {
		t.Fatal("barge-in wake failed")
	}
	select {
	case <-played:
		t.Fatal("barge-in played a wake sound over the interrupted reply")
	case <-time.After(25 * time.Millisecond):
	}
}
