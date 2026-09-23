package taternative

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	pkgspeaker "github.com/TaterTotterson/Tater-Echo-Firmware/pkg/speaker"
	"github.com/gorilla/websocket"
)

type syncTestSpeaker struct {
	mu     sync.Mutex
	music  []byte
	ended  int
	duckDB float64
}

type telemetryTestSpeaker struct {
	syncTestSpeaker
	firstRendered int64
	rendered      uint64
}

type timedDuckCall struct {
	db       float64
	duration time.Duration
}

type timedDuckTestSpeaker struct {
	syncTestSpeaker
	duckCalls []timedDuckCall
}

func (s *timedDuckTestSpeaker) SetDuckRamp(db float64, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.duckDB = db
	s.duckCalls = append(s.duckCalls, timedDuckCall{db: db, duration: duration})
}

func (s *telemetryTestSpeaker) PumpMusic(data []byte) error {
	if err := s.syncTestSpeaker.PumpMusic(data); err != nil {
		return err
	}
	s.mu.Lock()
	if s.firstRendered == 0 {
		s.firstRendered = time.Now().UnixNano()
	}
	s.rendered += uint64(len(data) / 2)
	s.mu.Unlock()
	return nil
}

func (s *telemetryTestSpeaker) MusicPlaybackStatus() pkgspeaker.MusicPlaybackStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pkgspeaker.MusicPlaybackStatus{
		RenderedFrames: s.rendered, OutputLatencyFrames: 4096,
		FirstRenderedUnixNano: s.firstRendered,
	}
}

func (s *telemetryTestSpeaker) WaitMusicIdle(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(1100 * time.Millisecond):
		return nil
	}
}

func (s *syncTestSpeaker) Init() error             { return nil }
func (s *syncTestSpeaker) PumpPeriod([]byte) error { return nil }
func (s *syncTestSpeaker) EndStream()              {}
func (s *syncTestSpeaker) Flush()                  {}
func (s *syncTestSpeaker) FlushMusic()             {}
func (s *syncTestSpeaker) SetDuck(db float64)      { s.duckDB = db }
func (s *syncTestSpeaker) Close()                  {}
func (s *syncTestSpeaker) PumpMusic(data []byte) error {
	s.mu.Lock()
	s.music = append(s.music, data...)
	s.mu.Unlock()
	return nil
}
func (s *syncTestSpeaker) EndMusicStream() {
	s.mu.Lock()
	s.ended++
	s.mu.Unlock()
}

func stereoWAV(left, right []int16) []byte {
	frames := len(left)
	data := make([]byte, frames*4)
	for index := 0; index < frames; index++ {
		binary.LittleEndian.PutUint16(data[index*4:], uint16(left[index]))
		binary.LittleEndian.PutUint16(data[index*4+2:], uint16(right[index]))
	}
	wav := make([]byte, 44+len(data))
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 2)
	binary.LittleEndian.PutUint32(wav[24:], playbackRate)
	binary.LittleEndian.PutUint32(wav[28:], playbackRate*4)
	binary.LittleEndian.PutUint16(wav[32:], 4)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(data)))
	copy(wav[44:], data)
	return wav
}

func TestStereoChannelSelectionDoesNotDownmixPairMembers(t *testing.T) {
	wav := stereoWAV([]int16{100, 200}, []int16{1000, 2000})
	left, err := decodeAudioChannel(wav, "audio/wav", "test.wav", "left")
	if err != nil {
		t.Fatal(err)
	}
	right, err := decodeAudioChannel(wav, "audio/wav", "test.wav", "right")
	if err != nil {
		t.Fatal(err)
	}
	mono, err := decodeAudioChannel(wav, "audio/wav", "test.wav", "mono")
	if err != nil {
		t.Fatal(err)
	}
	if got := int16(binary.LittleEndian.Uint16(left)); got != 100 {
		t.Fatalf("left sample = %d, want 100", got)
	}
	if got := int16(binary.LittleEndian.Uint16(right)); got != 1000 {
		t.Fatalf("right sample = %d, want 1000", got)
	}
	if got := int16(binary.LittleEndian.Uint16(mono)); got != 550 {
		t.Fatalf("mono sample = %d, want 550", got)
	}
}

func TestPreparedMediaWaitsForCommitClockAndAppliesCorrection(t *testing.T) {
	wav := stereoWAV(
		[]int16{10, 20, 30, 40},
		[]int16{100, 200, 300, 400},
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer server.Close()

	speaker := &syncTestSpeaker{}
	player := NewLocalPlayer(speaker)
	req := MediaRequest{
		SessionID: "stereo-song", GroupID: "kitchen", URL: server.URL,
		Channel: "right", VolumePercent: 100,
	}
	prepared, err := player.PrepareMedia(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.BufferedFrames != 4 || prepared.SampleRateHz != playbackRate {
		t.Fatalf("preparation = %#v", prepared)
	}
	if err := player.AdjustMedia(req.SessionID, 1, "legacy", 0); err != nil {
		t.Fatal(err)
	}
	startAt := monotonicMicros() + 40_000
	var events []MediaPlaybackEvent
	done, err := player.CommitMedia(context.Background(), req.SessionID, startAt, func(event MediaPlaybackEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("committed media did not finish")
	}
	if len(events) == 0 || events[0].Kind != "started" || events[0].ActualStartUS < startAt {
		t.Fatalf("start event = %#v, scheduled=%d", events, startAt)
	}
	speaker.mu.Lock()
	defer speaker.mu.Unlock()
	if got := int16(binary.LittleEndian.Uint16(speaker.music)); got != 200 {
		t.Fatalf("first rendered right-channel sample after correction = %d, want 200", got)
	}
	if speaker.ended != 1 {
		t.Fatalf("music EOS count = %d, want 1", speaker.ended)
	}
}

func TestAudioClockSyncUsesRequestCorrelation(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.handle(Envelope{
		Type: "audio.clock.sync", ID: "clock-request",
		Payload: map[string]any{"server_send_us": float64(12345)},
	})
	select {
	case frame := <-c.out:
		if frame.kind != websocket.TextMessage {
			t.Fatalf("frame kind = %d", frame.kind)
		}
		var response Envelope
		if err := json.Unmarshal(frame.data, &response); err != nil {
			t.Fatal(err)
		}
		if response.Type != "audio.clock.sync.result" || stringValue(response.Payload["reply_to"]) != "clock-request" {
			t.Fatalf("clock response = %#v", response)
		}
		if int64Value(response.Payload["satellite_send_us"], 0) < int64Value(response.Payload["satellite_receive_us"], 0) {
			t.Fatalf("clock response moved backwards: %#v", response.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("audio clock response was not emitted")
	}
}

func TestRendererClockProducesAudiblePlayheadInsteadOfQueuedPosition(t *testing.T) {
	wav := stereoWAV(
		[]int16{10, 20, 30, 40},
		[]int16{100, 200, 300, 400},
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer server.Close()
	speaker := &telemetryTestSpeaker{}
	player := NewLocalPlayer(speaker)
	req := MediaRequest{SessionID: "render-clock", GroupID: "group", URL: server.URL}
	prepared, err := player.PrepareMedia(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.OutputLatencyFrames != 4096 {
		t.Fatalf("output latency = %d, want 4096", prepared.OutputLatencyFrames)
	}
	events := make(chan MediaPlaybackEvent, 4)
	done, err := player.CommitMedia(context.Background(), req.SessionID, monotonicMicros(), func(event MediaPlaybackEvent) {
		events <- event
	})
	if err != nil {
		t.Fatal(err)
	}
	var started, playhead MediaPlaybackEvent
	deadline := time.After(2 * time.Second)
	for started.Kind == "" || playhead.Kind == "" {
		select {
		case event := <-events:
			if event.Kind == "started" {
				started = event
			} else if event.Kind == "playhead" {
				playhead = event
			}
		case <-deadline:
			t.Fatalf("renderer events missing: started=%#v playhead=%#v", started, playhead)
		}
	}
	if playhead.OutputFrames != 4 || playhead.RenderedFrames != 4 {
		t.Fatalf("renderer playhead = %#v", playhead)
	}
	if started.OutputLatencyFrames != 4096 || playhead.OutputLatencyFrames != 4096 {
		t.Fatalf("renderer latency missing: started=%#v playhead=%#v", started, playhead)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEchoAdvertisesAdvancedSynchronizedAudioTier(t *testing.T) {
	capabilities := DefaultCapabilities()
	for _, name := range []string{
		"audio_ducking", "persistent_media_sessions", "synchronized_media_sessions",
		"stereo_channel_selection", "media_playhead_telemetry", "media_drift_correction",
		"media_rate_slew", "media_underrun_recovery", "tts_overlays",
		"synchronized_tts_overlays", "audio_scenes",
	} {
		if capabilities[name] != true {
			t.Fatalf("capability %s = %#v", name, capabilities[name])
		}
	}
	if capabilities["audio_session_version"] != 4 {
		t.Fatalf("audio_session_version = %#v, want 4", capabilities["audio_session_version"])
	}
	if capabilities["media_render_clock"] != true || capabilities["media_output_latency_frames"] != echoOutputLatencyFrames {
		t.Fatalf("renderer clock capability = %#v", capabilities)
	}
	if capabilities["audio_scene_version"] != 1 {
		t.Fatalf("audio_scene_version = %#v, want 1", capabilities["audio_scene_version"])
	}
}

func TestPreparedMediaReturnsAfterBoundedPrebufferWhileDecoderContinues(t *testing.T) {
	frames := playbackRate * 3
	left := make([]int16, frames)
	right := make([]int16, frames)
	for index := range left {
		left[index] = int16(index % 2048)
		right[index] = int16(1000 + index%2048)
	}
	wav := stereoWAV(left, right)
	firstBytes := 44 + (mediaPrebufferFrames+8192)*4
	firstSent := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav[:firstBytes])
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(firstSent)
		select {
		case <-release:
			_, _ = w.Write(wav[firstBytes:])
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	player := NewLocalPlayer(&syncTestSpeaker{})
	player.mediaCacheDir = t.TempDir()
	preparedCh := make(chan MediaPreparation, 1)
	errCh := make(chan error, 1)
	go func() {
		prepared, err := player.PrepareMedia(context.Background(), MediaRequest{
			SessionID: "streaming", URL: server.URL, Channel: "left", VolumePercent: 100,
		})
		preparedCh <- prepared
		errCh <- err
	}()
	<-firstSent
	var prepared MediaPreparation
	select {
	case prepared = <-preparedCh:
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("prepare waited for the complete three-second asset instead of the bounded prebuffer")
	}
	if prepared.BufferedFrames < mediaPrebufferFrames || prepared.BufferedFrames >= frames {
		t.Fatalf("buffered frames = %d, want a bounded prebuffer below total %d", prepared.BufferedFrames, frames)
	}
	close(release)
	player.StopMedia()
}

func TestMediaSlewSpreadsCorrectionAcrossSettleWindow(t *testing.T) {
	var slew mediaSlew
	slew.replace(4, 400)
	var applied int64
	for range 4 {
		step := slew.next(100)
		if step != 1 {
			t.Fatalf("positive step = %d, want 1 per block", step)
		}
		applied += step
	}
	if applied != 4 || slew.pending != 0 {
		t.Fatalf("positive slew applied=%d pending=%d", applied, slew.pending)
	}
	slew.replace(-2, 400)
	if first := slew.next(199); first != 0 {
		t.Fatalf("negative slew applied too early: %d", first)
	}
	if second := slew.next(1); second != -1 {
		t.Fatalf("negative slew first step = %d, want -1", second)
	}
	if third := slew.next(200); third != -1 || slew.pending != 0 {
		t.Fatalf("negative slew final step=%d pending=%d", third, slew.pending)
	}
}

func TestSynchronizedOverlayEmitsStartedAndFinishedEvents(t *testing.T) {
	received := make(chan OverlayRequest, 1)
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{
		PlayOverlay: func(_ context.Context, req OverlayRequest, started func()) error {
			received <- req
			started()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.handle(Envelope{Type: "audio.overlay.start", ID: "request", Payload: map[string]any{
		"overlay_id": "reply-1", "group_id": "kitchen", "start_at_us": float64(1234),
		"foreground": map[string]any{"url": "http://tater/reply.wav", "kind": "tts", "volume_percent": float64(80)},
		"ducking":    map[string]any{"target_percent": float64(35), "attack_ms": float64(150), "release_ms": float64(350)},
		"finish":     map[string]any{"stop_media": true, "fade_ms": float64(500)},
	}})
	select {
	case req := <-received:
		if req.OverlayID != "reply-1" || req.GroupID != "kitchen" || req.VolumePercent != 80 || req.DuckingTargetPercent != 35 || !req.StopMediaWhenFinished || req.BackgroundFadeOut != 500*time.Millisecond {
			t.Fatalf("overlay request = %#v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("overlay hook was not called")
	}
	wanted := map[string]bool{"audio.overlay.started": false, "audio.overlay.finished": false}
	deadline := time.After(time.Second)
	for !wanted["audio.overlay.started"] || !wanted["audio.overlay.finished"] {
		select {
		case frame := <-c.out:
			var envelope Envelope
			if err := json.Unmarshal(frame.data, &envelope); err != nil {
				t.Fatal(err)
			}
			if _, ok := wanted[envelope.Type]; ok {
				wanted[envelope.Type] = true
			}
		case <-deadline:
			t.Fatalf("overlay events missing: %#v", wanted)
		}
	}
}

func TestOverlayUsesExactAttackReleaseAndFinishFadeDurations(t *testing.T) {
	wav := stereoWAV([]int16{0, 0, 0, 0}, []int16{0, 0, 0, 0})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer server.Close()

	for _, test := range []struct {
		name    string
		request OverlayRequest
		want    []timedDuckCall
	}{
		{
			name: "release",
			request: OverlayRequest{
				URL: server.URL, VolumePercent: 0, DuckingTargetPercent: 25,
				DuckingAttack: 5 * time.Millisecond, DuckingRelease: 7 * time.Millisecond,
			},
			want: []timedDuckCall{{db: 20 * math.Log10(.25), duration: 5 * time.Millisecond}, {db: 0, duration: 7 * time.Millisecond}},
		},
		{
			name: "finish fade",
			request: OverlayRequest{
				URL: server.URL, VolumePercent: 0, DuckingTargetPercent: 25,
				DuckingAttack: 5 * time.Millisecond, StopMediaWhenFinished: true,
				BackgroundFadeOut: 9 * time.Millisecond,
			},
			want: []timedDuckCall{{db: 20 * math.Log10(.25), duration: 5 * time.Millisecond}, {db: -96, duration: 9 * time.Millisecond}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			speaker := &timedDuckTestSpeaker{}
			player := NewLocalPlayer(speaker)
			player.mediaCacheDir = t.TempDir()
			if err := player.PlayOverlay(context.Background(), test.request, nil); err != nil {
				t.Fatal(err)
			}
			speaker.mu.Lock()
			defer speaker.mu.Unlock()
			if len(speaker.duckCalls) != len(test.want) {
				t.Fatalf("duck calls = %#v, want %#v", speaker.duckCalls, test.want)
			}
			for index := range test.want {
				got := speaker.duckCalls[index]
				if math.Abs(got.db-test.want[index].db) > .001 || got.duration != test.want[index].duration {
					t.Fatalf("duck call %d = %#v, want %#v", index, got, test.want[index])
				}
			}
		})
	}
}

func TestMediaStopReturnsClientToIdle(t *testing.T) {
	c, err := New(Config{URL: "ws://tater.test", DeviceID: "echo-test"}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.connected.Store(true)
	c.playMu.Lock()
	c.mediaID = "background"
	c.mediaGroup = "room"
	c.playMu.Unlock()
	c.setState("playing", nil)
	c.handle(Envelope{Type: "media.session.stop", Payload: map[string]any{"session_id": "background"}})
	if state := c.State(); state != "idle" {
		t.Fatalf("state after media stop = %q, want idle", state)
	}
}

func TestMediaSpoolIsRemovedWhenUncommittedSessionStops(t *testing.T) {
	wav := stereoWAV([]int16{1, 2, 3}, []int16{4, 5, 6})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = io.Copy(w, bytes.NewReader(wav))
	}))
	defer server.Close()
	player := NewLocalPlayer(&syncTestSpeaker{})
	player.mediaCacheDir = t.TempDir()
	if _, err := player.PrepareMedia(context.Background(), MediaRequest{SessionID: "cleanup", URL: server.URL}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(player.mediaCacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("spool entries before stop=%d err=%v", len(entries), err)
	}
	player.StopMedia()
	entries, err = os.ReadDir(player.mediaCacheDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("spool entries after stop=%d err=%v", len(entries), err)
	}
}
